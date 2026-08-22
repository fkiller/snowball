package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	authCookieName      = "__Host-snowball_session"
	passwordIterations  = 600_000
	minimumPasswordSize = 12
	maximumPasswordSize = 256
)

type passwordCredentials struct {
	Version    int    `json:"version"`
	Salt       string `json:"salt"`
	Hash       string `json:"hash"`
	Iterations int    `json:"iterations"`
	CreatedAt  string `json:"createdAt"`
}

type authSession struct {
	CSRFToken string
	ExpiresAt time.Time
}

type loginGuard struct {
	Failures     int
	FirstFailure time.Time
	BlockedUntil time.Time
}

type authManager struct {
	mu             sync.Mutex
	stateDir       string
	credentials    *passwordCredentials
	bootstrapToken string
	sessions       map[[sha256.Size]byte]authSession
	loginGuards    map[string]loginGuard
	sessionTTL     time.Duration
}

type authStatus struct {
	Authenticated bool   `json:"authenticated"`
	SetupRequired bool   `json:"setupRequired"`
	CSRFToken     string `json:"csrfToken,omitempty"`
	ExpiresAt     string `json:"expiresAt,omitempty"`
}

func newAuthManager(stateDir string, sessionTTL time.Duration) (*authManager, error) {
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, err
	}
	auth := &authManager{
		stateDir:    stateDir,
		sessions:    make(map[[sha256.Size]byte]authSession),
		loginGuards: make(map[string]loginGuard),
		sessionTTL:  sessionTTL,
	}

	credentialsPath := filepath.Join(stateDir, "admin-credentials.json")
	raw, err := os.ReadFile(credentialsPath)
	if err == nil {
		var credentials passwordCredentials
		if err := json.Unmarshal(raw, &credentials); err != nil {
			return nil, fmt.Errorf("decode admin credentials: %w", err)
		}
		if credentials.Version != 1 || credentials.Iterations < passwordIterations || credentials.Salt == "" || credentials.Hash == "" {
			return nil, errors.New("admin credentials use an unsupported or weakened format")
		}
		auth.credentials = &credentials
		_ = os.Remove(filepath.Join(stateDir, "initial-setup-code"))
		return auth, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read admin credentials: %w", err)
	}

	setupPath := filepath.Join(stateDir, "initial-setup-code")
	if raw, err := os.ReadFile(setupPath); err == nil {
		auth.bootstrapToken = strings.TrimSpace(string(raw))
	} else if errors.Is(err, os.ErrNotExist) {
		auth.bootstrapToken, err = randomToken(24)
		if err != nil {
			return nil, err
		}
		if err := writePrivateFile(setupPath, []byte(auth.bootstrapToken+"\n")); err != nil {
			return nil, err
		}
	} else {
		return nil, fmt.Errorf("read initial setup code: %w", err)
	}
	if auth.bootstrapToken == "" {
		return nil, errors.New("initial setup code is empty")
	}
	log.Printf("Snowball initial setup is required. Initial setup code: %s", auth.bootstrapToken)
	return auth, nil
}

func randomToken(byteCount int) (string, error) {
	raw := make([]byte, byteCount)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func writePrivateFile(path string, value []byte) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, value, 0600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0600); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return os.Rename(temporary, path)
}

func derivePassword(password, salt []byte, iterations, keyLength int) []byte {
	hashLength := sha256.Size
	blocks := (keyLength + hashLength - 1) / hashLength
	derived := make([]byte, 0, blocks*hashLength)
	blockIndex := make([]byte, 4)
	for block := 1; block <= blocks; block++ {
		binary.BigEndian.PutUint32(blockIndex, uint32(block))
		mac := hmac.New(sha256.New, password)
		_, _ = mac.Write(salt)
		_, _ = mac.Write(blockIndex)
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for iteration := 1; iteration < iterations; iteration++ {
			mac = hmac.New(sha256.New, password)
			_, _ = mac.Write(u)
			u = mac.Sum(nil)
			for index := range t {
				t[index] ^= u[index]
			}
		}
		derived = append(derived, t...)
	}
	return derived[:keyLength]
}

func validPasswordLength(password string) bool {
	return len(password) >= minimumPasswordSize && len(password) <= maximumPasswordSize
}

func (a *authManager) setupRequired() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.credentials == nil
}

func (a *authManager) bootstrap(setupCode, password string) error {
	if !validPasswordLength(password) {
		return fmt.Errorf("password must be between %d and %d bytes", minimumPasswordSize, maximumPasswordSize)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.credentials != nil {
		return errors.New("initial setup is already complete")
	}
	expected := sha256.Sum256([]byte(a.bootstrapToken))
	provided := sha256.Sum256([]byte(strings.TrimSpace(setupCode)))
	if subtle.ConstantTimeCompare(expected[:], provided[:]) != 1 {
		return errors.New("initial setup code is not valid")
	}
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	hash := derivePassword([]byte(password), salt, passwordIterations, sha256.Size)
	credentials := &passwordCredentials{
		Version:    1,
		Salt:       base64.RawStdEncoding.EncodeToString(salt),
		Hash:       base64.RawStdEncoding.EncodeToString(hash),
		Iterations: passwordIterations,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if err := writePrivateJSON(filepath.Join(a.stateDir, "admin-credentials.json"), credentials); err != nil {
		return err
	}
	a.credentials = credentials
	a.bootstrapToken = ""
	_ = os.Remove(filepath.Join(a.stateDir, "initial-setup-code"))
	return nil
}

func (a *authManager) verifyPassword(password string) bool {
	a.mu.Lock()
	if a.credentials == nil || len(password) > maximumPasswordSize {
		a.mu.Unlock()
		return false
	}
	credentials := *a.credentials
	a.mu.Unlock()
	salt, saltErr := base64.RawStdEncoding.DecodeString(credentials.Salt)
	expected, hashErr := base64.RawStdEncoding.DecodeString(credentials.Hash)
	if saltErr != nil || hashErr != nil || len(expected) != sha256.Size {
		return false
	}
	actual := derivePassword([]byte(password), salt, credentials.Iterations, len(expected))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func (a *authManager) canAttemptLogin(client string) (bool, time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	guard := a.loginGuards[client]
	if guard.BlockedUntil.After(time.Now()) {
		return false, time.Until(guard.BlockedUntil)
	}
	return true, 0
}

func (a *authManager) recordLogin(client string, succeeded bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if succeeded {
		delete(a.loginGuards, client)
		return
	}
	now := time.Now()
	guard := a.loginGuards[client]
	if guard.FirstFailure.IsZero() || now.Sub(guard.FirstFailure) > 15*time.Minute {
		guard = loginGuard{FirstFailure: now}
	}
	guard.Failures++
	if guard.Failures >= 5 {
		guard.BlockedUntil = now.Add(5 * time.Minute)
	}
	a.loginGuards[client] = guard
}

func sessionKey(token string) [sha256.Size]byte {
	return sha256.Sum256([]byte(token))
}

func (a *authManager) createSession() (string, authSession, error) {
	token, err := randomToken(32)
	if err != nil {
		return "", authSession{}, err
	}
	csrf, err := randomToken(24)
	if err != nil {
		return "", authSession{}, err
	}
	session := authSession{CSRFToken: csrf, ExpiresAt: time.Now().Add(a.sessionTTL)}
	a.mu.Lock()
	a.sessions[sessionKey(token)] = session
	a.pruneSessionsLocked()
	a.mu.Unlock()
	return token, session, nil
}

func (a *authManager) pruneSessionsLocked() {
	now := time.Now()
	for key, session := range a.sessions {
		if !session.ExpiresAt.After(now) {
			delete(a.sessions, key)
		}
	}
}

func (a *authManager) requestSession(r *http.Request) (authSession, bool) {
	cookie, err := r.Cookie(authCookieName)
	if err != nil || cookie.Value == "" {
		return authSession{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[sessionKey(cookie.Value)]
	if !ok || !session.ExpiresAt.After(time.Now()) {
		if ok {
			delete(a.sessions, sessionKey(cookie.Value))
		}
		return authSession{}, false
	}
	return session, true
}

func (a *authManager) revokeRequestSession(r *http.Request) {
	cookie, err := r.Cookie(authCookieName)
	if err != nil {
		return
	}
	a.mu.Lock()
	delete(a.sessions, sessionKey(cookie.Value))
	a.mu.Unlock()
}

func (a *authManager) sessionKeyActive(key [sha256.Size]byte) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[key]
	if !ok || !session.ExpiresAt.After(time.Now()) {
		if ok {
			delete(a.sessions, key)
		}
		return false
	}
	return true
}

func setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func requestClient(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(forwarded) != nil {
		return forwarded
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func requestOriginAllowed(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		fetchSite := r.Header.Get("Sec-Fetch-Site")
		return fetchSite == "none" || fetchSite == "same-origin"
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return false
	}
	target := r.Header.Get("X-Forwarded-Host")
	if target == "" {
		target = r.Host
	}
	return strings.EqualFold(parsed.Host, target)
}

func (g *gateway) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	status := authStatus{SetupRequired: g.auth.setupRequired()}
	if session, ok := g.auth.requestSession(r); ok {
		status.Authenticated = true
		status.CSRFToken = session.CSRFToken
		status.ExpiresAt = session.ExpiresAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, status)
}

func (g *gateway) issueSession(w http.ResponseWriter) error {
	token, session, err := g.auth.createSession()
	if err != nil {
		return err
	}
	setSessionCookie(w, token, session.ExpiresAt)
	writeJSON(w, http.StatusOK, authStatus{
		Authenticated: true,
		SetupRequired: false,
		CSRFToken:     session.CSRFToken,
		ExpiresAt:     session.ExpiresAt.UTC().Format(time.RFC3339),
	})
	return nil
}

func (g *gateway) handleAuthBootstrap(w http.ResponseWriter, r *http.Request) {
	if !requestOriginAllowed(r) {
		writeError(w, http.StatusForbidden, "request origin is not allowed")
		return
	}
	var input struct {
		SetupCode string `json:"setupCode"`
		Password  string `json:"password"`
	}
	if err := decodeJSONBody(w, r, 8<<10, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid setup request")
		return
	}
	client := requestClient(r)
	if allowed, retry := g.auth.canAttemptLogin(client); !allowed {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", max(1, int(retry.Seconds()))))
		writeError(w, http.StatusTooManyRequests, "too many attempts; try again later")
		return
	}
	if err := g.auth.bootstrap(input.SetupCode, input.Password); err != nil {
		g.auth.recordLogin(client, false)
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	g.auth.recordLogin(client, true)
	if err := g.issueSession(w); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
	}
}

func (g *gateway) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !requestOriginAllowed(r) {
		writeError(w, http.StatusForbidden, "request origin is not allowed")
		return
	}
	if g.auth.setupRequired() {
		writeError(w, http.StatusConflict, "initial setup is required")
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if err := decodeJSONBody(w, r, 4<<10, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid login request")
		return
	}
	client := requestClient(r)
	if allowed, retry := g.auth.canAttemptLogin(client); !allowed {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", max(1, int(retry.Seconds()))))
		writeError(w, http.StatusTooManyRequests, "too many attempts; try again later")
		return
	}
	if !g.auth.verifyPassword(input.Password) {
		g.auth.recordLogin(client, false)
		time.Sleep(250 * time.Millisecond)
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}
	g.auth.recordLogin(client, true)
	if err := g.issueSession(w); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
	}
}

func (g *gateway) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	g.auth.revokeRequestSession(r)
	g.closeActivePeer()
	_, _, _ = g.browserRequest(http.MethodPost, "/voice/stop")
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
}

func (g *gateway) handleAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.auth.requestSession(r); !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *authManager) authorize(w http.ResponseWriter, r *http.Request, mutation bool) (authSession, bool) {
	session, ok := a.requestSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return authSession{}, false
	}
	if mutation {
		csrf := r.Header.Get("X-CSRF-Token")
		if !requestOriginAllowed(r) || len(csrf) != len(session.CSRFToken) || subtle.ConstantTimeCompare([]byte(csrf), []byte(session.CSRFToken)) != 1 {
			writeError(w, http.StatusForbidden, "request verification failed")
			return authSession{}, false
		}
	}
	return session, true
}
