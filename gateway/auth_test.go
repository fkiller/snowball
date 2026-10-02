package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPBKDF2KnownVector(t *testing.T) {
	actual := derivePassword([]byte("password"), []byte("salt"), 2, sha256.Size)
	const expected = "ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43"
	if hex.EncodeToString(actual) != expected {
		t.Fatalf("unexpected PBKDF2 output: %x", actual)
	}
}

func TestInitialSetupAndPasswordVerification(t *testing.T) {
	stateDir := t.TempDir()
	auth, err := newAuthManager(stateDir, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	setupCode := auth.bootstrapToken
	if setupCode == "" || !auth.setupRequired() {
		t.Fatal("initial setup was not created")
	}
	if err := auth.bootstrap(setupCode, "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if auth.setupRequired() || !auth.verifyPassword("correct horse battery staple") || auth.verifyPassword("incorrect password") {
		t.Fatal("password credential verification failed")
	}
	info, err := os.Stat(filepath.Join(stateDir, "admin-credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("credential permissions are %o", info.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(stateDir, "initial-setup-code")); !os.IsNotExist(err) {
		t.Fatal("one-time setup code was not removed")
	}
}

func TestSessionAndCSRFProtection(t *testing.T) {
	auth := &authManager{
		sessions:    make(map[[sha256.Size]byte]authSession),
		loginGuards: make(map[string]loginGuard),
		sessionTTL:  time.Hour,
	}
	token, session, err := auth.createSession()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://192.168.1.1:8443/api/voice/start", nil)
	request.Host = "192.168.1.1:8443"
	request.Header.Set("Origin", "https://192.168.1.1:8443")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.Header.Set("X-CSRF-Token", session.CSRFToken)
	request.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
	if _, ok := auth.authorize(httptest.NewRecorder(), request, true); !ok {
		t.Fatal("valid protected request was rejected")
	}

	request.Header.Set("Origin", "https://attacker.example")
	if _, ok := auth.authorize(httptest.NewRecorder(), request, true); ok {
		t.Fatal("cross-origin request was accepted")
	}
	request.Header.Set("Origin", "https://192.168.1.1:9443")
	if _, ok := auth.authorize(httptest.NewRecorder(), request, true); ok {
		t.Fatal("same-host request from a different port was accepted")
	}
	request.Header.Set("Origin", "https://192.168.1.1:8443")
	request.Header.Del("X-CSRF-Token")
	if _, ok := auth.authorize(httptest.NewRecorder(), request, true); ok {
		t.Fatal("request without CSRF token was accepted")
	}
}
