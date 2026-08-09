package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

type config struct {
	LANIP             string
	HTTPSPort         int
	ICEPort           int
	UplinkRTPPort     int
	DownlinkRTPPort   int
	ListenAddress     string
	BrowserController string
	StateDir          string
}

type browserStatus struct {
	State              string `json:"state"`
	Reason             string `json:"reason,omitempty"`
	URL                string `json:"url,omitempty"`
	Title              string `json:"title,omitempty"`
	VoiceButtonPresent bool   `json:"voiceButtonPresent"`
	VoiceActive        bool   `json:"voiceActive"`
	Authenticated      bool   `json:"authenticated"`
	CheckedAt          string `json:"checkedAt,omitempty"`
	LastError          string `json:"lastError,omitempty"`
}

type pushKeys struct {
	Private string `json:"private"`
	Public  string `json:"public"`
}

type gateway struct {
	cfg config

	api        *webrtc.API
	iceMux     io.Closer
	uplinkConn *net.UDPConn
	downConn   *net.UDPConn

	peerMu       sync.RWMutex
	peer         *webrtc.PeerConnection
	outputTrack  *webrtc.TrackLocalStaticRTP
	peerAddress  string
	peerConnected bool

	pushMu        sync.RWMutex
	pushKeys      pushKeys
	subscriptions []webpush.Subscription

	browserMu     sync.RWMutex
	lastBrowser   browserStatus
	lastAlertKind string
	httpClient    *http.Client
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	var value int
	if _, err := fmt.Sscanf(env(name, ""), "%d", &value); err == nil && value > 0 {
		return value
	}
	return fallback
}

func loadConfig() config {
	return config{
		LANIP:             env("SNOWBALL_LAN_IP", "192.168.1.1"),
		HTTPSPort:         envInt("SNOWBALL_HTTPS_PORT", 8443),
		ICEPort:           envInt("SNOWBALL_ICE_PORT", 49000),
		UplinkRTPPort:     envInt("SNOWBALL_UPLINK_RTP_PORT", 49001),
		DownlinkRTPPort:   envInt("SNOWBALL_DOWNLINK_RTP_PORT", 49002),
		ListenAddress:     env("GATEWAY_LISTEN", "127.0.0.1:8080"),
		BrowserController: env("BROWSER_CONTROLLER_URL", "http://127.0.0.1:3100"),
		StateDir:          env("STATE_DIR", "/data/state"),
	}
}

func newGateway(cfg config) (*gateway, error) {
	lanIP := net.ParseIP(cfg.LANIP)
	if lanIP == nil || lanIP.To4() == nil || !lanIP.IsPrivate() {
		return nil, fmt.Errorf("SNOWBALL_LAN_IP must be a private IPv4 address, got %q", cfg.LANIP)
	}

	iceConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: lanIP, Port: cfg.ICEPort})
	if err != nil {
		return nil, fmt.Errorf("bind LAN-only WebRTC socket: %w", err)
	}

	uplinkConn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: cfg.UplinkRTPPort})
	if err != nil {
		iceConn.Close()
		return nil, fmt.Errorf("open uplink RTP socket: %w", err)
	}

	downConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: cfg.DownlinkRTPPort})
	if err != nil {
		iceConn.Close()
		uplinkConn.Close()
		return nil, fmt.Errorf("open downlink RTP socket: %w", err)
	}

	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		return nil, err
	}
	registry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, registry); err != nil {
		return nil, err
	}
	settingEngine := webrtc.SettingEngine{}
	settingEngine.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	settingEngine.SetIPFilter(func(ip net.IP) bool { return ip.Equal(lanIP) })
	iceMux := webrtc.NewICEUDPMux(nil, iceConn)
	settingEngine.SetICEUDPMux(iceMux)

	g := &gateway{
		cfg:        cfg,
		api:        webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine), webrtc.WithInterceptorRegistry(registry), webrtc.WithSettingEngine(settingEngine)),
		iceMux:     iceMux,
		uplinkConn: uplinkConn,
		downConn:   downConn,
		httpClient: &http.Client{Timeout: 4 * time.Second},
		lastBrowser: browserStatus{State: "starting", Reason: "Browser controller is starting."},
	}

	if err := g.loadPushState(); err != nil {
		return nil, err
	}
	go g.forwardBrowserAudio()
	go g.watchBrowser()
	return g, nil
}

func (g *gateway) close() {
	g.peerMu.Lock()
	if g.peer != nil {
		_ = g.peer.Close()
	}
	g.peerMu.Unlock()
	_ = g.iceMux.Close()
	_ = g.uplinkConn.Close()
	_ = g.downConn.Close()
}

func (g *gateway) handleOffer(w http.ResponseWriter, r *http.Request) {
	var offer webrtc.SessionDescription
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&offer); err != nil {
		writeError(w, http.StatusBadRequest, "invalid WebRTC offer")
		return
	}
	if offer.Type != webrtc.SDPTypeOffer {
		writeError(w, http.StatusBadRequest, "expected an SDP offer")
		return
	}

	peer, err := g.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	outputTrack, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2},
		"snowball-output",
		"snowball",
	)
	if err != nil {
		_ = peer.Close()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sender, err := peer.AddTrack(outputTrack)
	if err != nil {
		_ = peer.Close()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	go func() {
		buffer := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buffer); err != nil {
				return
			}
		}
	}()

	peer.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() != webrtc.RTPCodecTypeAudio {
			return
		}
		for {
			packet, _, err := track.ReadRTP()
			if err != nil {
				return
			}
			raw, err := packet.Marshal()
			if err == nil {
				_, _ = g.uplinkConn.Write(raw)
			}
		}
	})

	peer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		g.peerMu.Lock()
		if g.peer == peer {
			g.peerConnected = state == webrtc.PeerConnectionStateConnected
			if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
				g.peerConnected = false
			}
		}
		g.peerMu.Unlock()
	})

	if err := peer.SetRemoteDescription(offer); err != nil {
		_ = peer.Close()
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	answer, err := peer.CreateAnswer(nil)
	if err != nil {
		_ = peer.Close()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	gatherComplete := webrtc.GatheringCompletePromise(peer)
	if err := peer.SetLocalDescription(answer); err != nil {
		_ = peer.Close()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	select {
	case <-gatherComplete:
	case <-time.After(8 * time.Second):
		_ = peer.Close()
		writeError(w, http.StatusGatewayTimeout, "ICE gathering timed out")
		return
	}

	g.peerMu.Lock()
	oldPeer := g.peer
	g.peer = peer
	g.outputTrack = outputTrack
	g.peerAddress = clientAddress(r)
	g.peerConnected = false
	g.peerMu.Unlock()
	if oldPeer != nil {
		_ = oldPeer.Close()
	}

	writeJSON(w, http.StatusOK, peer.LocalDescription())
}

func (g *gateway) forwardBrowserAudio() {
	buffer := make([]byte, 2048)
	for {
		n, _, err := g.downConn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		packet := &rtp.Packet{}
		if err := packet.Unmarshal(buffer[:n]); err != nil {
			continue
		}
		g.peerMu.RLock()
		track := g.outputTrack
		connected := g.peerConnected
		g.peerMu.RUnlock()
		if track != nil && connected {
			_ = track.WriteRTP(packet)
		}
	}
}

func (g *gateway) browserRequest(method, path string) (browserStatus, int, error) {
	request, err := http.NewRequest(method, g.cfg.BrowserController+path, nil)
	if err != nil {
		return browserStatus{}, 0, err
	}
	response, err := g.httpClient.Do(request)
	if err != nil {
		return browserStatus{State: "starting", Reason: "Browser controller is unavailable."}, http.StatusServiceUnavailable, err
	}
	defer response.Body.Close()
	var status browserStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		return status, response.StatusCode, err
	}
	return status, response.StatusCode, nil
}

func (g *gateway) handleVoice(w http.ResponseWriter, r *http.Request, action string) {
	status, code, err := g.browserRequest(http.MethodPost, "/voice/"+action)
	if err != nil || code >= 400 {
		message := status.Reason
		if message == "" && err != nil {
			message = err.Error()
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": message, "browser": status})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (g *gateway) handleStatus(w http.ResponseWriter, _ *http.Request) {
	status, _, err := g.browserRequest(http.MethodGet, "/status")
	if err == nil {
		g.browserMu.Lock()
		g.lastBrowser = status
		g.browserMu.Unlock()
	} else {
		g.browserMu.RLock()
		status = g.lastBrowser
		g.browserMu.RUnlock()
	}

	g.peerMu.RLock()
	connected, peerAddress := g.peerConnected, g.peerAddress
	g.peerMu.RUnlock()
	g.pushMu.RLock()
	subscriberCount := len(g.subscriptions)
	g.pushMu.RUnlock()

	gatewayState := "ready"
	if err != nil {
		gatewayState = "degraded"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"gateway": gatewayState,
		"browser": status,
		"webrtc": map[string]any{"connected": connected, "peer": peerAddress},
		"push":    map[string]any{"subscribers": subscriberCount},
		"network": map[string]any{"lanIp": g.cfg.LANIP, "httpsPort": g.cfg.HTTPSPort, "icePort": g.cfg.ICEPort},
	})
}

func (g *gateway) watchBrowser() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		status, _, err := g.browserRequest(http.MethodGet, "/status")
		if err != nil {
			continue
		}
		g.browserMu.Lock()
		g.lastBrowser = status
		alertKind := ""
		if status.State == "needs_login" || status.State == "needs_human" {
			alertKind = status.State + ":" + status.Reason
		}
		shouldAlert := alertKind != "" && alertKind != g.lastAlertKind
		if alertKind == "" {
			g.lastAlertKind = ""
		} else if shouldAlert {
			g.lastAlertKind = alertKind
		}
		g.browserMu.Unlock()
		if shouldAlert {
			_ = g.sendPush("Snowball needs you", status.Reason, "/?recovery=1")
		}
	}
}

func (g *gateway) loadPushState() error {
	if err := os.MkdirAll(g.cfg.StateDir, 0700); err != nil {
		return err
	}
	keysPath := filepath.Join(g.cfg.StateDir, "vapid.json")
	if raw, err := os.ReadFile(keysPath); err == nil {
		if err := json.Unmarshal(raw, &g.pushKeys); err != nil {
			return err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			return err
		}
		g.pushKeys = pushKeys{Private: privateKey, Public: publicKey}
		if err := writePrivateJSON(keysPath, g.pushKeys); err != nil {
			return err
		}
	} else {
		return err
	}

	subscriptionsPath := filepath.Join(g.cfg.StateDir, "subscriptions.json")
	if raw, err := os.ReadFile(subscriptionsPath); err == nil {
		_ = json.Unmarshal(raw, &g.subscriptions)
	}
	return nil
}

func writePrivateJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, raw, 0600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (g *gateway) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	var subscription webpush.Subscription
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&subscription); err != nil || subscription.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "invalid push subscription")
		return
	}
	g.pushMu.Lock()
	found := false
	for index := range g.subscriptions {
		if g.subscriptions[index].Endpoint == subscription.Endpoint {
			g.subscriptions[index] = subscription
			found = true
		}
	}
	if !found {
		g.subscriptions = append(g.subscriptions, subscription)
	}
	err := writePrivateJSON(filepath.Join(g.cfg.StateDir, "subscriptions.json"), g.subscriptions)
	count := len(g.subscriptions)
	g.pushMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscribers": count})
}

func (g *gateway) sendPush(title, body, targetURL string) error {
	payload, _ := json.Marshal(map[string]string{
		"title": title,
		"body":  body,
		"url":   targetURL,
		"tag":   "snowball-recovery",
	})
	g.pushMu.RLock()
	subscriptions := append([]webpush.Subscription(nil), g.subscriptions...)
	keys := g.pushKeys
	g.pushMu.RUnlock()
	if len(subscriptions) == 0 {
		return errors.New("no push subscribers")
	}

	var lastErr error
	for index := range subscriptions {
		response, err := webpush.SendNotification(payload, &subscriptions[index], &webpush.Options{
			Subscriber:      "https://snowball.local/voice",
			VAPIDPublicKey:  keys.Public,
			VAPIDPrivateKey: keys.Private,
			TTL:             60,
		})
		if err != nil {
			lastErr = err
			continue
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode >= 300 {
			lastErr = fmt.Errorf("push endpoint returned %s", response.Status)
		}
	}
	return lastErr
}

func randomIncidentID() string {
	raw := make([]byte, 6)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

func (g *gateway) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", g.handleStatus)
	mux.HandleFunc("POST /api/webrtc/offer", g.handleOffer)
	mux.HandleFunc("POST /api/voice/start", func(w http.ResponseWriter, r *http.Request) { g.handleVoice(w, r, "start") })
	mux.HandleFunc("POST /api/voice/stop", func(w http.ResponseWriter, r *http.Request) { g.handleVoice(w, r, "stop") })
	mux.HandleFunc("GET /api/push/key", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"publicKey": g.pushKeys.Public})
	})
	mux.HandleFunc("POST /api/push/subscribe", g.handlePushSubscribe)
	mux.HandleFunc("POST /api/push/test", func(w http.ResponseWriter, _ *http.Request) {
		incident := randomIncidentID()
		if err := g.sendPush("Snowball test alert", "Push recovery is working on this device.", "/?incident="+incident); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"incident": incident})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("cache-control", "no-store")
		w.Header().Set("x-content-type-options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func main() {
	cfg := loadConfig()
	g, err := newGateway(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer g.close()

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           g.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       45 * time.Second,
	}

	go func() {
		log.Printf("gateway control API listening on %s; WebRTC on %s:%d/udp", cfg.ListenAddress, cfg.LANIP, cfg.ICEPort)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}
