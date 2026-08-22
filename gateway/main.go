package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
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
	LANIP              string
	HTTPSPort          int
	HTTPPort           int
	ICEPort            int
	UplinkRTPPort      int
	DownlinkRTPPort    int
	DeviceUplinkPort   int
	DeviceDownlinkPort int
	ListenAddress      string
	BrowserController  string
	StateDir           string
	CACertificate      string
	SessionTTL         time.Duration
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
	SelectedVoice      string `json:"selectedVoice,omitempty"`
}

// browserCandidateCatalog is the only source of authoritative voice/project
// names. It is deliberately a bounded, read-only snapshot of what the
// authenticated Chromium session can currently see; neither the Gateway nor
// the firmware embeds production names.
type browserCandidateCatalog struct {
	Version       int      `json:"version"`
	Source        string   `json:"source"`
	Authenticated bool     `json:"authenticated"`
	VoiceState    string   `json:"voiceState"`
	Voices        []string `json:"voices"`
	Projects      []string `json:"projects"`
}

const maximumBrowserCandidates = 250

// The browser snapshot may be fairly large, but the device event response is
// deliberately small: it is carried over the ESP32 HTTPS client and persisted
// in a bounded response buffer. Keep the device grammar useful without
// allowing a workspace with hundreds of names to overflow that buffer.
const (
	maximumDeviceCandidates          = 24
	maximumDeviceCandidateNameLength = 64
)

type pushKeys struct {
	Private string `json:"private"`
	Public  string `json:"public"`
}

const (
	browserMediaActivePath = "/tmp/snowball-browser-media.active"
	deviceMediaActivePath  = "/tmp/snowball-device-media.active"
	deviceAudioStallAfter  = 45 * time.Second
	voiceRecoveryCooldown  = 2 * time.Minute
	// Two consecutive authoritative inactive observations remain the debounce
	// gate. Poll at one second so a normal Bye cannot leave a live device peer
	// (and its audio bridge) around for an entire 20–30 second browser tail.
	deviceVoiceWatchInterval = 1 * time.Second
)

var errDeviceMediaNotReady = errors.New("device media session is not ready")

type gateway struct {
	cfg config

	api              *webrtc.API
	iceMux           io.Closer
	uplinkConn       *net.UDPConn
	downConn         *net.UDPConn
	deviceUplinkConn *net.UDPConn
	deviceDownConn   *net.UDPConn

	peerMu                   sync.RWMutex
	peer                     *webrtc.PeerConnection
	outputTrack              *webrtc.TrackLocalStaticRTP
	peerAddress              string
	peerConnected            bool
	peerSession              [sha256.Size]byte
	peerHasSession           bool
	peerDeviceFingerprint    string
	peerDeviceVoiceStarted   bool
	peerDeviceProjectStarted bool
	peerConnectedAt          time.Time
	// ChatGPT can briefly hide the End Voice control while a Voice turn or
	// navigation transition is settling. Require two consecutive authoritative
	// inactive observations before tearing down a live device peer; a single
	// transient status gap must not end the user's session.
	deviceVoiceInactiveObservations uint8
	peerAudioMode                   string

	audioMu                 sync.RWMutex
	lastDeviceUplinkAudio   time.Time
	lastDeviceDownlinkAudio time.Time
	lastVoiceRecovery       time.Time
	deviceUplinkLogged      bool
	deviceDownlinkLogged    bool
	voiceMu                 sync.Mutex

	pushMu        sync.RWMutex
	pushKeys      pushKeys
	subscriptions []webpush.Subscription

	browserMu         sync.RWMutex
	lastBrowser       browserStatus
	lastAlertKind     string
	httpClient        *http.Client
	auth              *authManager
	settings          *settingsStore
	enrollment        *enrollmentManager
	deviceDispatchMu  sync.Mutex
	deviceDispatchSem chan struct{}
	deviceDispatching map[string]struct{}
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
		LANIP:              env("SNOWBALL_LAN_IP", "192.168.1.1"),
		HTTPSPort:          envInt("SNOWBALL_HTTPS_PORT", 8443),
		HTTPPort:           envInt("SNOWBALL_HTTP_PORT", 8088),
		ICEPort:            envInt("SNOWBALL_ICE_PORT", 49000),
		UplinkRTPPort:      envInt("SNOWBALL_UPLINK_RTP_PORT", 49001),
		DownlinkRTPPort:    envInt("SNOWBALL_DOWNLINK_RTP_PORT", 49002),
		DeviceUplinkPort:   envInt("SNOWBALL_DEVICE_UPLINK_RTP_PORT", 49003),
		DeviceDownlinkPort: envInt("SNOWBALL_DEVICE_DOWNLINK_RTP_PORT", 49004),
		ListenAddress:      env("GATEWAY_LISTEN", "127.0.0.1:8080"),
		BrowserController:  env("BROWSER_CONTROLLER_URL", "http://127.0.0.1:3100"),
		StateDir:           env("STATE_DIR", "/data/state"),
		CACertificate:      env("SNOWBALL_CA_CERT", "/data/certs/ca.crt"),
		SessionTTL:         time.Duration(envInt("SNOWBALL_SESSION_TTL_HOURS", 24)) * time.Hour,
	}
}

func newGateway(cfg config) (*gateway, error) {
	// A container restart must not leave the on-demand GStreamer bridge armed.
	_ = os.Remove(browserMediaActivePath)
	_ = os.Remove(deviceMediaActivePath)
	lanIP := net.ParseIP(cfg.LANIP)
	if lanIP == nil || lanIP.To4() == nil || !lanIP.IsPrivate() {
		return nil, fmt.Errorf("SNOWBALL_LAN_IP must be a private IPv4 address, got %q", cfg.LANIP)
	}
	auth, err := newAuthManager(cfg.StateDir, cfg.SessionTTL)
	if err != nil {
		return nil, fmt.Errorf("initialize authentication: %w", err)
	}
	settings, err := newSettingsStore(cfg.StateDir)
	if err != nil {
		return nil, fmt.Errorf("initialize settings: %w", err)
	}
	enrollment, err := newEnrollmentManager(cfg.StateDir, cfg.CACertificate)
	if err != nil {
		return nil, fmt.Errorf("initialize device enrollment: %w", err)
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

	deviceUplinkConn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: cfg.DeviceUplinkPort})
	if err != nil {
		iceConn.Close()
		uplinkConn.Close()
		downConn.Close()
		return nil, fmt.Errorf("open device uplink RTP socket: %w", err)
	}

	deviceDownConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: cfg.DeviceDownlinkPort})
	if err != nil {
		iceConn.Close()
		uplinkConn.Close()
		downConn.Close()
		deviceUplinkConn.Close()
		return nil, fmt.Errorf("open device downlink RTP socket: %w", err)
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
		cfg:              cfg,
		api:              webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine), webrtc.WithInterceptorRegistry(registry), webrtc.WithSettingEngine(settingEngine)),
		iceMux:           iceMux,
		uplinkConn:       uplinkConn,
		downConn:         downConn,
		deviceUplinkConn: deviceUplinkConn,
		deviceDownConn:   deviceDownConn,
		// A fresh ChatGPT page can take longer than the ordinary status poll to
		// settle before Voice becomes authoritative. Keep the operation bounded,
		// but do not tear down a healthy device media session during that normal
		// browser startup window.
		httpClient:        &http.Client{Timeout: 30 * time.Second},
		lastBrowser:       browserStatus{State: "starting", Reason: "Browser controller is starting."},
		auth:              auth,
		settings:          settings,
		enrollment:        enrollment,
		deviceDispatchSem: make(chan struct{}, 1),
		deviceDispatching: make(map[string]struct{}),
	}

	if err := g.loadPushState(); err != nil {
		return nil, err
	}
	go g.forwardBrowserAudio()
	go g.forwardDeviceAudio()
	go g.watchBrowser()
	return g, nil
}

func (g *gateway) close() {
	g.closeActivePeer()
	_ = g.iceMux.Close()
	_ = g.uplinkConn.Close()
	_ = g.downConn.Close()
	_ = g.deviceUplinkConn.Close()
	_ = g.deviceDownConn.Close()
}

func (g *gateway) closeActivePeer() bool {
	g.peerMu.Lock()
	peer := g.peer
	previousAudioMode := g.peerAudioMode
	ownedDeviceVoice := g.peerDeviceFingerprint != "" && g.peerDeviceVoiceStarted
	ownedDeviceProject := g.peerDeviceFingerprint != "" && g.peerDeviceProjectStarted
	deviceFingerprint := g.peerDeviceFingerprint
	peerConnectedAt := g.peerConnectedAt
	g.peer = nil
	g.outputTrack = nil
	g.peerAddress = ""
	g.peerConnected = false
	g.peerHasSession = false
	g.peerSession = [sha256.Size]byte{}
	g.peerDeviceFingerprint = ""
	g.peerDeviceVoiceStarted = false
	g.peerDeviceProjectStarted = false
	g.deviceVoiceInactiveObservations = 0
	g.peerAudioMode = ""
	g.peerConnectedAt = time.Time{}
	g.peerMu.Unlock()
	if peer != nil && previousAudioMode == "pcma" {
		log.Printf(
			"device media peer closed fingerprint=%s ownedVoice=%t ownedProject=%t connectedFor=%s",
			shortFingerprint(deviceFingerprint),
			ownedDeviceVoice,
			ownedDeviceProject,
			peerDuration(peerConnectedAt),
		)
	}
	g.audioMu.Lock()
	g.lastDeviceUplinkAudio = time.Time{}
	g.lastDeviceDownlinkAudio = time.Time{}
	g.deviceUplinkLogged = false
	g.deviceDownlinkLogged = false
	g.audioMu.Unlock()
	if previousAudioMode == "pcma" {
		_ = os.Remove(deviceMediaActivePath)
	} else if previousAudioMode == "opus" {
		_ = os.Remove(browserMediaActivePath)
	}
	if peer != nil {
		_ = peer.Close()
	}
	if ownedDeviceProject {
		g.stopBrowserProject("device media peer closed")
	}
	return ownedDeviceVoice
}

func shortFingerprint(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func peerDuration(started time.Time) string {
	if started.IsZero() {
		return "unknown"
	}
	return time.Since(started).Round(time.Millisecond).String()
}

func (g *gateway) stopBrowserVoice(reason string) {
	g.voiceMu.Lock()
	defer g.voiceMu.Unlock()
	if _, code, err := g.browserRequest(http.MethodPost, "/voice/stop"); err != nil || code >= 400 {
		if err == nil {
			err = fmt.Errorf("browser controller returned HTTP %d", code)
		}
		log.Printf("could not stop orphaned browser Voice (%s): %v", reason, err)
	}
}

func (g *gateway) stopBrowserProject(reason string) {
	if _, code, err := g.browserRequest(http.MethodPost, "/project/stop"); err != nil || code >= 400 {
		if err == nil {
			err = fmt.Errorf("browser controller returned HTTP %d", code)
		}
		log.Printf("could not stop orphaned project annotation (%s): %v", reason, err)
	}
}

func (g *gateway) enforcePeerSession() {
	g.peerMu.RLock()
	hasPeer := g.peer != nil
	hasSession := g.peerHasSession
	session := g.peerSession
	deviceFingerprint := g.peerDeviceFingerprint
	g.peerMu.RUnlock()
	adminExpired := hasPeer && deviceFingerprint == "" && (!hasSession || !g.auth.sessionKeyActive(session))
	deviceRevoked := hasPeer && deviceFingerprint != "" && !g.enrollment.isActive(deviceFingerprint)
	if adminExpired || deviceRevoked {
		g.closeActivePeer()
		g.stopBrowserVoice("peer authorization ended")
	}
}

func pcmaHasSignal(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	signal := 0
	for _, encoded := range payload {
		encoded ^= 0x55
		value := int(encoded&0x0f) << 4
		segment := int((encoded & 0x70) >> 4)
		switch segment {
		case 0:
			value += 8
		case 1:
			value += 0x108
		default:
			value += 0x108
			value <<= segment - 1
		}
		if encoded&0x80 == 0 {
			value = -value
		}
		if value > 512 || value < -512 {
			signal++
		}
	}
	return signal >= (len(payload)+7)/8
}

func (g *gateway) recoverStalledDeviceVoice(status browserStatus) {
	if !status.VoiceActive {
		return
	}
	g.peerMu.RLock()
	devicePeerActive := g.peer != nil && g.peerConnected && g.peerDeviceFingerprint != "" &&
		g.peerDeviceVoiceStarted && g.peerAudioMode == "pcma"
	g.peerMu.RUnlock()
	if !devicePeerActive {
		return
	}
	now := time.Now()
	g.audioMu.RLock()
	lastUplink := g.lastDeviceUplinkAudio
	lastDownlink := g.lastDeviceDownlinkAudio
	lastRecovery := g.lastVoiceRecovery
	g.audioMu.RUnlock()
	if lastUplink.IsZero() || now.Sub(lastUplink) < deviceAudioStallAfter ||
		(!lastDownlink.IsZero() && now.Sub(lastDownlink) < deviceAudioStallAfter) ||
		(!lastRecovery.IsZero() && now.Sub(lastRecovery) < voiceRecoveryCooldown) {
		return
	}
	g.audioMu.Lock()
	if !g.lastVoiceRecovery.IsZero() && now.Sub(g.lastVoiceRecovery) < voiceRecoveryCooldown {
		g.audioMu.Unlock()
		return
	}
	g.lastVoiceRecovery = now
	g.audioMu.Unlock()
	log.Printf("recovering stalled device Voice: no PCMA response for %s after microphone activity", deviceAudioStallAfter)
	g.closeActivePeer()
	g.stopBrowserVoice("device audio stalled")
}

func (g *gateway) handleOffer(w http.ResponseWriter, r *http.Request) {
	var offer webrtc.SessionDescription
	if err := decodeJSONBody(w, r, 1<<20, &offer); err != nil {
		writeError(w, http.StatusBadRequest, "invalid WebRTC offer")
		return
	}
	if offer.Type != webrtc.SDPTypeOffer {
		writeError(w, http.StatusBadRequest, "expected an SDP offer")
		return
	}
	var adminSession [sha256.Size]byte
	hasAdminSession := false
	if cookie, err := r.Cookie(authCookieName); err == nil && cookie.Value != "" {
		adminSession = sessionKey(cookie.Value)
		hasAdminSession = true
	}
	answer, status, err := g.installPeer(offer, clientAddress(r), "opus", "", adminSession, hasAdminSession)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, answer)
}

func (g *gateway) handleDeviceMediaOffer(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	var input deviceMediaOfferRequest
	if err := decodeJSONBody(w, r, 128<<10, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid device media offer")
		return
	}
	record, err := g.enrollment.acceptMediaOffer(input)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if !strings.Contains(strings.ToUpper(input.SDP), "PCMA/8000") {
		writeError(w, http.StatusBadRequest, "device media offer must contain PCMA/8000 audio")
		return
	}
	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: input.SDP}
	answer, status, err := g.installPeer(
		offer,
		clientAddress(r),
		"pcma",
		record.PublicKeyFingerprint,
		[sha256.Size]byte{},
		false,
	)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	log.Printf(
		"device media offer accepted fingerprint=%s peer=%s elapsed=%s",
		shortFingerprint(record.PublicKeyFingerprint),
		clientAddress(r),
		time.Since(started).Round(time.Millisecond),
	)
	writeJSON(w, http.StatusOK, map[string]any{
		"version": 1,
		"type":    "answer",
		"sdp":     answer.SDP,
	})
}

func (g *gateway) installPeer(
	offer webrtc.SessionDescription,
	address string,
	audioMode string,
	deviceFingerprint string,
	adminSession [sha256.Size]byte,
	hasAdminSession bool,
) (*webrtc.SessionDescription, int, error) {
	codec := webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}
	uplink := g.uplinkConn
	if audioMode == "pcma" {
		codec = webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMA, ClockRate: 8000, Channels: 1}
		uplink = g.deviceUplinkConn
	}

	peer, err := g.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}

	outputTrack, err := webrtc.NewTrackLocalStaticRTP(
		codec,
		"snowball-output",
		"snowball",
	)
	if err != nil {
		_ = peer.Close()
		return nil, http.StatusInternalServerError, err
	}
	sender, err := peer.AddTrack(outputTrack)
	if err != nil {
		_ = peer.Close()
		return nil, http.StatusInternalServerError, err
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
		if track.Kind() != webrtc.RTPCodecTypeAudio || !strings.EqualFold(track.Codec().MimeType, codec.MimeType) {
			return
		}
		for {
			packet, _, err := track.ReadRTP()
			if err != nil {
				return
			}
			if audioMode == "pcma" && pcmaHasSignal(packet.Payload) {
				g.audioMu.Lock()
				g.lastDeviceUplinkAudio = time.Now()
				if !g.deviceUplinkLogged {
					g.deviceUplinkLogged = true
					log.Printf(
						"device media uplink audio first fingerprint=%s bytes=%d",
						shortFingerprint(deviceFingerprint),
						len(packet.Payload),
					)
				}
				g.audioMu.Unlock()
			}
			raw, err := packet.Marshal()
			if err == nil {
				_, _ = uplink.Write(raw)
			}
		}
	})

	peer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		closeFailedPeer := false
		g.peerMu.Lock()
		if g.peer == peer {
			g.peerConnected = state == webrtc.PeerConnectionStateConnected
			if state == webrtc.PeerConnectionStateConnected {
				g.peerConnectedAt = time.Now()
			}
			if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
				g.peerConnected = false
				g.peerConnectedAt = time.Time{}
				closeFailedPeer = true
			}
		}
		g.peerMu.Unlock()
		if deviceFingerprint != "" {
			log.Printf(
				"device media peer state fingerprint=%s state=%s connected=%t",
				shortFingerprint(deviceFingerprint),
				state.String(),
				state == webrtc.PeerConnectionStateConnected,
			)
		}
		if closeFailedPeer {
			go g.closePeerIfCurrent(peer)
		}
	})

	if err := peer.SetRemoteDescription(offer); err != nil {
		_ = peer.Close()
		return nil, http.StatusBadRequest, err
	}
	answer, err := peer.CreateAnswer(nil)
	if err != nil {
		_ = peer.Close()
		return nil, http.StatusInternalServerError, err
	}
	gatherComplete := webrtc.GatheringCompletePromise(peer)
	if err := peer.SetLocalDescription(answer); err != nil {
		_ = peer.Close()
		return nil, http.StatusInternalServerError, err
	}
	select {
	case <-gatherComplete:
	case <-time.After(8 * time.Second):
		_ = peer.Close()
		return nil, http.StatusGatewayTimeout, errors.New("ICE gathering timed out")
	}

	g.peerMu.Lock()
	oldPeer := g.peer
	oldAudioMode := g.peerAudioMode
	oldPeerOwnedDeviceVoice := g.peerDeviceFingerprint != "" && g.peerDeviceVoiceStarted
	oldPeerOwnedDeviceProject := g.peerDeviceFingerprint != "" && g.peerDeviceProjectStarted
	g.peer = peer
	g.outputTrack = outputTrack
	g.peerAddress = address
	g.peerConnected = peer.ConnectionState() == webrtc.PeerConnectionStateConnected
	g.peerConnectedAt = time.Time{}
	if g.peerConnected {
		g.peerConnectedAt = time.Now()
	}
	g.peerSession = adminSession
	g.peerHasSession = hasAdminSession
	g.peerDeviceFingerprint = deviceFingerprint
	g.peerDeviceVoiceStarted = false
	g.peerDeviceProjectStarted = false
	g.deviceVoiceInactiveObservations = 0
	g.peerAudioMode = audioMode
	g.peerMu.Unlock()
	g.audioMu.Lock()
	g.lastDeviceUplinkAudio = time.Time{}
	g.lastDeviceDownlinkAudio = time.Time{}
	g.lastVoiceRecovery = time.Time{}
	g.deviceUplinkLogged = false
	g.deviceDownlinkLogged = false
	g.audioMu.Unlock()
	activePath := browserMediaActivePath
	if audioMode == "pcma" {
		activePath = deviceMediaActivePath
	}
	if err := os.WriteFile(activePath, []byte("active\n"), 0600); err != nil {
		g.closeActivePeer()
		if oldPeer != nil {
			_ = oldPeer.Close()
		}
		return nil, http.StatusInternalServerError, errors.New("could not start the audio bridge")
	}
	if oldAudioMode != "" && oldAudioMode != audioMode {
		if oldAudioMode == "pcma" {
			_ = os.Remove(deviceMediaActivePath)
		} else if oldAudioMode == "opus" {
			_ = os.Remove(browserMediaActivePath)
		}
	}
	if oldPeer != nil {
		_ = oldPeer.Close()
	}
	if oldPeerOwnedDeviceVoice {
		g.stopBrowserVoice("media peer was replaced")
	}
	if oldPeerOwnedDeviceProject {
		g.stopBrowserProject("media peer was replaced")
	}

	return peer.LocalDescription(), http.StatusOK, nil
}

func (g *gateway) closePeerIfCurrent(candidate *webrtc.PeerConnection) {
	g.peerMu.RLock()
	current := g.peer == candidate
	g.peerMu.RUnlock()
	if current {
		if g.closeActivePeer() {
			g.stopBrowserVoice("device media peer closed")
		}
	}
}

func (g *gateway) forwardBrowserAudio() {
	g.forwardAudio(g.downConn, "opus")
}

func (g *gateway) forwardDeviceAudio() {
	g.forwardAudio(g.deviceDownConn, "pcma")
}

func (g *gateway) forwardAudio(connection *net.UDPConn, audioMode string) {
	buffer := make([]byte, 2048)
	for {
		n, _, err := connection.ReadFromUDP(buffer)
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
		currentMode := g.peerAudioMode
		g.peerMu.RUnlock()
		if track != nil && connected && currentMode == audioMode {
			if audioMode == "pcma" && pcmaHasSignal(packet.Payload) {
				g.audioMu.Lock()
				g.lastDeviceDownlinkAudio = time.Now()
				if !g.deviceDownlinkLogged {
					g.deviceDownlinkLogged = true
					log.Printf(
						"device media downlink audio first bytes=%d",
						len(packet.Payload),
					)
				}
				g.audioMu.Unlock()
			}
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
	if response.StatusCode >= 400 {
		var detail struct {
			Error   string        `json:"error"`
			Browser browserStatus `json:"browser"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&detail); err != nil {
			return detail.Browser, response.StatusCode, err
		}
		if detail.Error == "" {
			detail.Error = fmt.Sprintf("browser controller returned HTTP %d", response.StatusCode)
		}
		return detail.Browser, response.StatusCode, errors.New(detail.Error)
	}
	var status browserStatus
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status); err != nil {
		return status, response.StatusCode, err
	}
	return status, response.StatusCode, nil
}

func (g *gateway) browserJSONRequest(method, path string, input, output any, timeout time.Duration) (int, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequest(method, g.cfg.BrowserController+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: timeout}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		var detail struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&detail)
		if detail.Error == "" {
			detail.Error = "browser controller rejected the request"
		}
		return response.StatusCode, errors.New(detail.Error)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(output); err != nil {
		return response.StatusCode, err
	}
	return response.StatusCode, nil
}

func (g *gateway) browserNames(path string) ([]string, error) {
	request, err := http.NewRequest(http.MethodGet, g.cfg.BrowserController+path, nil)
	if err != nil {
		return nil, err
	}
	response, err := g.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var output struct {
		Names []string `json:"names"`
		Error string   `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&output); err != nil {
		return nil, err
	}
	if response.StatusCode >= 400 {
		if output.Error == "" {
			output.Error = "browser controller could not list names"
		}
		return nil, errors.New(output.Error)
	}
	if len(output.Names) == 0 || len(output.Names) > 250 {
		return nil, errors.New("browser controller returned no usable names")
	}
	return output.Names, nil
}

func normalizeBrowserCandidates(values []string) ([]string, error) {
	if len(values) > maximumBrowserCandidates {
		return nil, errors.New("browser controller returned too many candidates")
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
		if value == "" || len(value) > 128 || strings.ContainsAny(value, "\x00\r\n") {
			return nil, errors.New("browser controller returned an invalid candidate")
		}
		key := strings.ToLower(value)
		if !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	return result, nil
}

func (g *gateway) browserCandidateCatalog() (browserCandidateCatalog, error) {
	request, err := http.NewRequest(http.MethodGet, g.cfg.BrowserController+"/candidates", nil)
	if err != nil {
		return browserCandidateCatalog{}, err
	}
	response, err := g.httpClient.Do(request)
	if err != nil {
		return browserCandidateCatalog{}, err
	}
	defer response.Body.Close()
	var output browserCandidateCatalog
	if err := json.NewDecoder(io.LimitReader(response.Body, 256<<10)).Decode(&output); err != nil {
		return browserCandidateCatalog{}, err
	}
	if response.StatusCode >= 400 {
		return browserCandidateCatalog{}, fmt.Errorf("browser controller returned HTTP %d", response.StatusCode)
	}
	if output.Version != 1 || output.Source != "chatgpt-web" ||
		(output.VoiceState != "available" && output.VoiceState != "requires_voice") {
		return browserCandidateCatalog{}, errors.New("browser controller returned an unsupported candidate catalog")
	}
	output.Voices, err = normalizeBrowserCandidates(output.Voices)
	if err != nil {
		return browserCandidateCatalog{}, err
	}
	output.Projects, err = normalizeBrowserCandidates(output.Projects)
	if err != nil {
		return browserCandidateCatalog{}, err
	}
	if !output.Authenticated {
		output.Voices = nil
		output.Projects = nil
	}
	return output, nil
}

func deviceCandidateCatalog(catalog browserCandidateCatalog) browserCandidateCatalog {
	bounded := catalog
	bounded.Voices = boundedCandidateNames(catalog.Voices)
	bounded.Projects = boundedCandidateNames(catalog.Projects)
	return bounded
}

func boundedCandidateNames(values []string) []string {
	if len(values) > maximumDeviceCandidates {
		values = values[:maximumDeviceCandidates]
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if len(value) > maximumDeviceCandidateNameLength {
			value = value[:maximumDeviceCandidateNameLength]
		}
		result = append(result, value)
	}
	return result
}

func (g *gateway) activeDevicePeer(fingerprint string) *webrtc.PeerConnection {
	g.peerMu.RLock()
	defer g.peerMu.RUnlock()
	if g.peer == nil || !g.peerConnected || g.peerDeviceFingerprint != fingerprint || g.peerAudioMode != "pcma" {
		return nil
	}
	return g.peer
}

func (g *gateway) devicePeerStillCurrent(peer *webrtc.PeerConnection, fingerprint string) bool {
	g.peerMu.RLock()
	defer g.peerMu.RUnlock()
	return peer != nil && g.peer == peer && g.peerConnected &&
		g.peerDeviceFingerprint == fingerprint && g.peerAudioMode == "pcma"
}

func (g *gateway) browserVoiceAction(path string) (browserStatus, error) {
	started := time.Now()
	status, code, err := g.browserRequest(http.MethodPost, path)
	log.Printf(
		"device Voice browser action path=%s code=%d active=%t elapsed=%s err=%v",
		path,
		code,
		status.VoiceActive,
		time.Since(started).Round(time.Millisecond),
		err,
	)
	if err != nil {
		return status, err
	}
	if code >= 400 {
		return status, fmt.Errorf("browser controller returned HTTP %d", code)
	}
	return status, nil
}

// startDeviceVoiceSession serializes every browser mutation that belongs to one
// device command. A bare/start_voice command is deliberately treated as a new
// conversation; only the explicit resume command may reuse the last chat.
func (g *gateway) startDeviceVoiceSession(
	fingerprint string,
	mode string,
	spokenVoice string,
) (browserStatus, string, error) {
	peer := g.activeDevicePeer(fingerprint)
	if peer == nil {
		return browserStatus{}, "", errDeviceMediaNotReady
	}

	g.voiceMu.Lock()
	defer g.voiceMu.Unlock()
	if !g.devicePeerStillCurrent(peer, fingerprint) {
		return browserStatus{}, "", errDeviceMediaNotReady
	}
	// Do not let the browser-status watcher interpret the intentional stop in a
	// new-chat transaction as an unexpected end of this media session.
	g.peerMu.Lock()
	if g.peer == peer {
		g.peerDeviceVoiceStarted = false
		g.deviceVoiceInactiveObservations = 0
	}
	g.peerMu.Unlock()

	cleanupBrowser := func(reason error) {
		if _, code, err := g.browserRequest(http.MethodPost, "/voice/stop"); err != nil || code >= 400 {
			if err == nil {
				err = fmt.Errorf("browser controller returned HTTP %d", code)
			}
			log.Printf("device Voice rollback after %v could not stop browser Voice: %v", reason, err)
		}
	}

	var status browserStatus
	var err error
	switch mode {
	case "new":
		// Avoid sending a redundant End Voice click on the normal idle path. It
		// used to serialize behind Chromium's previous operation and added an
		// unnecessary round trip before the fresh-chat navigation.
		current, _, statusErr := g.browserRequest(http.MethodGet, "/status")
		if statusErr != nil || current.VoiceActive {
			_, err = g.browserVoiceAction("/voice/stop")
		}
		if err == nil {
			_, err = g.browserVoiceAction("/navigate")
		}
		if err == nil {
			status, err = g.browserVoiceAction("/voice/start")
		}
	case "resume":
		status, err = g.browserVoiceAction("/voice/resume")
	default:
		err = errors.New("unsupported device Voice mode")
	}
	if err != nil {
		cleanupBrowser(err)
		return status, "", err
	}
	if !status.VoiceActive {
		err = errors.New("browser did not report authoritative Voice state")
		cleanupBrowser(err)
		return status, "", err
	}

	selectedVoice := ""
	if spokenVoice != "" {
		voices, listErr := g.browserNames("/voices")
		if listErr != nil {
			cleanupBrowser(listErr)
			return status, "", listErr
		}
		selectedVoice, err = resolveSpokenName(spokenVoice, voices)
		if err != nil {
			cleanupBrowser(err)
			return status, "", err
		}
		selection := browserStatus{}
		if _, err = g.browserJSONRequest(
			http.MethodPost,
			"/voice/select",
			map[string]string{"name": selectedVoice},
			&selection,
			30*time.Second,
		); err != nil {
			cleanupBrowser(err)
			return status, "", err
		}
		if !selection.VoiceActive || selection.SelectedVoice != selectedVoice {
			err = errors.New("browser did not confirm the requested Voice selection")
			cleanupBrowser(err)
			return selection, "", err
		}
		status = selection
	}

	if !g.devicePeerStillCurrent(peer, fingerprint) {
		err = errors.New("device media session ended while browser Voice was starting")
		cleanupBrowser(err)
		return status, "", err
	}
	g.peerMu.Lock()
	if g.peer == peer {
		g.peerDeviceVoiceStarted = true
		g.deviceVoiceInactiveObservations = 0
	}
	g.peerMu.Unlock()
	return status, selectedVoice, nil
}

// startDeviceProjectSession keeps the authenticated PCMA peer alive while the
// browser performs turn-based dictation, project submission, and Read aloud.
// The browser adapter owns the DOM/VAD details; the Gateway only supplies the
// validated project name and localized resources from protected settings.
func (g *gateway) startDeviceProjectSession(
	fingerprint string,
	target string,
	spokenProject string,
) (map[string]any, error) {
	peer := g.activeDevicePeer(fingerprint)
	if peer == nil {
		return nil, errDeviceMediaNotReady
	}
	if target != "chatgpt" {
		action, reason := projectDeviceCapability(target)
		return map[string]any{"outcome": "not_ready", "action": action, "reason": reason}, nil
	}

	g.voiceMu.Lock()
	defer g.voiceMu.Unlock()
	if !g.devicePeerStillCurrent(peer, fingerprint) {
		return nil, errDeviceMediaNotReady
	}

	projects, err := g.browserNames("/projects?target=chatgpt")
	if err != nil {
		return nil, err
	}
	projectName, err := resolveSpokenName(spokenProject, projects)
	if err != nil {
		return nil, err
	}
	if g.settings == nil {
		return nil, errors.New("settings store is unavailable")
	}
	settings := g.settings.get()
	prompts, ok := settings.Prompts[settings.Language]
	if !ok {
		return nil, errors.New("selected language has no project prompt resources")
	}

	g.peerMu.Lock()
	if g.peer == peer {
		g.peerDeviceProjectStarted = true
	}
	g.peerMu.Unlock()
	defer func() {
		g.peerMu.Lock()
		if g.peer == peer {
			g.peerDeviceProjectStarted = false
		}
		g.peerMu.Unlock()
	}()

	input := map[string]any{
		"target":             target,
		"projectName":        projectName,
		"language":           settings.Language,
		"silenceThresholdMs": settings.TurnBased.SilenceThresholdMS,
		"maxUtteranceMs":     settings.TurnBased.MaxUtteranceMS,
		"projectReady":       prompts.ProjectReady,
		"projectNext":        prompts.ProjectNext,
		"projectUnavailable": prompts.ProjectUnavailable,
		"exitCommands":       prompts.ExitCommands,
	}
	var output map[string]any
	if _, err := g.browserJSONRequest(http.MethodPost, "/project/annotation", input, &output, 8*time.Minute); err != nil {
		return nil, err
	}
	if !g.devicePeerStillCurrent(peer, fingerprint) {
		return nil, errDeviceMediaNotReady
	}
	return output, nil
}

func (g *gateway) handleVoice(w http.ResponseWriter, r *http.Request, action string) {
	g.voiceMu.Lock()
	defer g.voiceMu.Unlock()
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
	peerKind := "browser"
	if g.peerDeviceFingerprint != "" {
		peerKind = "speaker"
	}
	audioMode := g.peerAudioMode
	deviceVoiceStarted := g.peerDeviceVoiceStarted
	deviceFingerprint := g.peerDeviceFingerprint
	peerPresent := g.peer != nil
	g.peerMu.RUnlock()
	g.pushMu.RLock()
	subscriberCount := len(g.subscriptions)
	g.pushMu.RUnlock()

	gatewayState := "ready"
	if err != nil {
		gatewayState = "degraded"
	}
	// `browser.voiceActive` is authoritative for the ChatGPT UI, but it is not
	// sufficient to tell a client that Snowball has a usable live session. A
	// browser can remain in Voice while the WebRTC peer is still negotiating or
	// after the peer has already been torn down. Expose one derived state so the
	// PWA and diagnostics use the same lifecycle contract.
	voiceLive := err == nil && g.authoritativeVoiceLive(status, peerPresent, connected, deviceFingerprint, deviceVoiceStarted, audioMode)
	writeJSON(w, http.StatusOK, map[string]any{
		"gateway":   gatewayState,
		"browser":   status,
		"voiceLive": voiceLive,
		"webrtc":    map[string]any{"connected": connected, "peer": peerAddress, "client": peerKind, "audioMode": audioMode},
		"push":      map[string]any{"subscribers": subscriberCount},
		"network":   map[string]any{"lanIp": g.cfg.LANIP, "httpsPort": g.cfg.HTTPSPort, "icePort": g.cfg.ICEPort},
	})
}

func (g *gateway) authoritativeVoiceLive(
	status browserStatus,
	peerPresent bool,
	connected bool,
	deviceFingerprint string,
	deviceVoiceStarted bool,
	audioMode string,
) bool {
	return status.VoiceActive && peerPresent && connected &&
		((deviceFingerprint == "" && audioMode == "opus") ||
			(deviceFingerprint != "" && audioMode == "pcma" && deviceVoiceStarted))
}

// observeDeviceVoiceStatus returns true only after two consecutive inactive
// observations for an already-started device Voice peer. ChatGPT may briefly
// hide its End Voice control during a turn transition; one missing control is
// not sufficient evidence that the transport session ended.
func (g *gateway) observeDeviceVoiceStatus(voiceActive bool) bool {
	g.peerMu.Lock()
	defer g.peerMu.Unlock()
	if g.peerDeviceFingerprint == "" || !g.peerDeviceVoiceStarted {
		g.deviceVoiceInactiveObservations = 0
		return false
	}
	if voiceActive {
		g.deviceVoiceInactiveObservations = 0
		return false
	}
	if g.deviceVoiceInactiveObservations < 2 {
		g.deviceVoiceInactiveObservations++
	}
	if g.deviceVoiceInactiveObservations < 2 {
		return false
	}
	g.deviceVoiceInactiveObservations = 0
	return true
}

// observeAuthoritativeDeviceVoiceStatus only feeds stable, fully inspected
// browser state into the inactive debounce. Navigation and modal transitions
// temporarily hide both Voice controls; those observations must reset the
// debounce instead of being counted as an end. The previous implementation
// passed the derived "browser idle" value into observeDeviceVoiceStatus,
// inverting active and inactive states and closing a healthy peer two polls
// after Voice started.
func (g *gateway) observeAuthoritativeDeviceVoiceStatus(status browserStatus) bool {
	if status.State != "ready" || !status.VoiceButtonPresent {
		g.peerMu.Lock()
		g.deviceVoiceInactiveObservations = 0
		g.peerMu.Unlock()
		return false
	}
	return g.observeDeviceVoiceStatus(status.VoiceActive)
}

func (g *gateway) watchBrowser() {
	ticker := time.NewTicker(deviceVoiceWatchInterval)
	defer ticker.Stop()
	for range ticker.C {
		g.enforcePeerSession()
		status, _, err := g.browserRequest(http.MethodGet, "/status")
		if err != nil {
			continue
		}
		// Only a fully inspected, ready page is evidence that Voice is idle.
		// During navigation ChatGPT briefly hides both controls; treating that
		// transient as an end would cut the first turn off while Voice starts.
		if g.observeAuthoritativeDeviceVoiceStatus(status) {
			log.Printf("device Voice browser state inactive twice; closing media peer")
			g.closeActivePeer()
		}
		g.recoverStalledDeviceVoice(status)
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
	return writePrivateFile(path, raw)
}

func (g *gateway) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Endpoint       string       `json:"endpoint"`
		ExpirationTime *int64       `json:"expirationTime"`
		Keys           webpush.Keys `json:"keys"`
	}
	if err := decodeJSONBody(w, r, 64<<10, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid push subscription")
		return
	}
	endpoint, err := url.Parse(input.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || len(input.Endpoint) > 4096 || input.Keys.Auth == "" || len(input.Keys.Auth) > 1024 || input.Keys.P256dh == "" || len(input.Keys.P256dh) > 1024 {
		writeError(w, http.StatusBadRequest, "invalid push subscription")
		return
	}
	subscription := webpush.Subscription{Endpoint: input.Endpoint, Keys: input.Keys}
	g.pushMu.Lock()
	found := false
	for index := range g.subscriptions {
		if g.subscriptions[index].Endpoint == subscription.Endpoint {
			g.subscriptions[index] = subscription
			found = true
		}
	}
	if !found {
		if len(g.subscriptions) >= 64 {
			g.pushMu.Unlock()
			writeError(w, http.StatusConflict, "push subscription limit reached")
			return
		}
		g.subscriptions = append(g.subscriptions, subscription)
	}
	err = writePrivateJSON(filepath.Join(g.cfg.StateDir, "subscriptions.json"), g.subscriptions)
	count := len(g.subscriptions)
	g.pushMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscribers": count})
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, maximum int64, value any) error {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(contentType, "application/json") {
		return errors.New("content type must be application/json")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maximum))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func (g *gateway) protect(handler http.HandlerFunc, mutation bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := g.auth.authorize(w, r, mutation); !ok {
			return
		}
		handler(w, r)
	}
}

func (g *gateway) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"settings": g.settings.get(), "schema": settingsSchema()})
		return
	}
	var next snowballSettings
	if err := decodeJSONBody(w, r, 128<<10, &next); err != nil {
		writeError(w, http.StatusBadRequest, "invalid settings document")
		return
	}
	if err := g.settings.replace(next); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": g.settings.get(), "schema": settingsSchema()})
}

func (g *gateway) handleDevices(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"devices": g.enrollment.list()})
}

func (g *gateway) handleCandidates(w http.ResponseWriter, _ *http.Request) {
	// Candidate discovery is read-only but still protected because project and
	// voice names disclose the user's authenticated ChatGPT workspace.
	g.voiceMu.Lock()
	defer g.voiceMu.Unlock()
	catalog, err := g.browserCandidateCatalog()
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"catalog": catalog})
}

func (g *gateway) handleDeviceEnrollment(w http.ResponseWriter, r *http.Request) {
	var input deviceEnrollmentRequest
	if err := decodeJSONBody(w, r, 8<<10, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid device enrollment request")
		return
	}
	settings := g.settings.get()
	material, err := g.enrollment.issue(input, g.cfg.LANIP, g.cfg.HTTPPort, g.cfg.HTTPSPort, settings.Discovery.PairingWindowSeconds)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, material)
}

func (g *gateway) handleDeviceEnrollmentProof(w http.ResponseWriter, r *http.Request) {
	var input deviceEnrollmentProof
	if err := decodeJSONBody(w, r, 4<<10, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid device enrollment proof")
		return
	}
	record, err := g.enrollment.complete(input)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":              1,
		"enrolled":             true,
		"hardwareId":           record.HardwareID,
		"publicKeyFingerprint": record.PublicKeyFingerprint,
	})
}

func deviceBrowserCommand(command string) bool {
	switch command {
	case "new_chat", "start_voice", "resume", "voice", "project":
		return true
	default:
		return false
	}
}

func projectDeviceCapability(target string) (string, string) {
	switch target {
	case "codex":
		return "codex_project_host_unavailable", "Codex local projects require a paired desktop host; the router Chromium cannot access local Codex folders."
	case "chatgpt":
		return "chatgpt_project_turn_ready", "ChatGPT Projects use turn-based annotation; the authenticated browser captures the utterance and reads the answer aloud."
	default:
		return "project_target_invalid", "The project target is not supported."
	}
}

func deviceDispatchKey(input deviceEventRequest) string {
	digest := sha256.Sum256(deviceEventProofMessage(input))
	return hex.EncodeToString(digest[:])
}

// scheduleDeviceDispatch moves browser automation out of the device HTTP
// request. The board keeps its media session alive while the browser performs
// navigation and Voice startup, and retries the same signed envelope until
// completeEvent stores the terminal result. Only one browser transaction is
// admitted at a time; the replay proof prevents duplicate work on retries.
func (g *gateway) scheduleDeviceDispatch(input deviceEventRequest, deviceName string) (bool, bool) {
	key := deviceDispatchKey(input)
	g.deviceDispatchMu.Lock()
	if g.deviceDispatchSem == nil {
		g.deviceDispatchSem = make(chan struct{}, 1)
	}
	if g.deviceDispatching == nil {
		g.deviceDispatching = make(map[string]struct{})
	}
	if _, exists := g.deviceDispatching[key]; exists {
		g.deviceDispatchMu.Unlock()
		return true, true
	}
	select {
	case g.deviceDispatchSem <- struct{}{}:
		g.deviceDispatching[key] = struct{}{}
	default:
		g.deviceDispatchMu.Unlock()
		return false, false
	}
	g.deviceDispatchMu.Unlock()
	go func() {
		defer func() {
			g.deviceDispatchMu.Lock()
			delete(g.deviceDispatching, key)
			<-g.deviceDispatchSem
			g.deviceDispatchMu.Unlock()
		}()
		g.dispatchDeviceCommand(input, deviceName)
	}()
	return true, false
}

func (g *gateway) dispatchDeviceCommand(input deviceEventRequest, deviceName string) {
	started := time.Now()
	log.Printf(
		"device command dispatch started device=%s command=%s target=%s",
		deviceName,
		input.Command,
		input.Target,
	)
	result := map[string]any{
		"version":  1,
		"accepted": true,
		"device":   deviceName,
		"event":    input.Event,
		"command":  input.Command,
		"outcome":  "accepted",
	}
	statusCode := http.StatusOK
	wakeWordEnabled := true
	if g.settings != nil {
		wakeWordEnabled = g.settings.get().WakeWord.Enabled
	}
	if !wakeWordEnabled {
		result["action"] = "wake_word_commands_disabled"
		result["outcome"] = "not_ready"
	} else {
		switch input.Command {
		case "start_voice", "new_chat":
			status, _, dispatchErr := g.startDeviceVoiceSession(input.PublicKeyFingerprint, "new", "")
			if errors.Is(dispatchErr, errDeviceMediaNotReady) {
				result["action"] = "awaiting_device_media_session"
				result["outcome"] = "not_ready"
			} else if dispatchErr != nil {
				result["action"] = "new_chat_voice_start_failed"
				result["outcome"] = "failed"
				statusCode = http.StatusConflict
			} else {
				result["action"] = "start_new_chat_voice"
				result["browser"] = status
				result["outcome"] = "executed"
			}
		case "resume":
			status, _, dispatchErr := g.startDeviceVoiceSession(input.PublicKeyFingerprint, "resume", "")
			if errors.Is(dispatchErr, errDeviceMediaNotReady) {
				result["action"] = "awaiting_device_media_session"
				result["outcome"] = "not_ready"
			} else if dispatchErr != nil {
				result["action"] = "resume_chat_failed"
				result["outcome"] = "failed"
				statusCode = http.StatusConflict
			} else {
				result["action"] = "resume_current_chat"
				result["browser"] = status
				result["outcome"] = "executed"
			}
		case "voice":
			status, selectedVoice, dispatchErr := g.startDeviceVoiceSession(input.PublicKeyFingerprint, "new", input.Name)
			if errors.Is(dispatchErr, errDeviceMediaNotReady) {
				result["action"] = "awaiting_device_media_session"
				result["outcome"] = "not_ready"
			} else if dispatchErr != nil {
				result["action"] = "voice_start_failed"
				result["outcome"] = "failed"
				statusCode = http.StatusConflict
			} else {
				result["action"] = "start_new_chat_with_voice"
				result["selectedVoice"] = selectedVoice
				result["browser"] = status
				result["outcome"] = "executed"
			}
		case "project":
			result["mode"] = "turn_based"
			if input.Target == "codex" {
				result["action"], result["reason"] = projectDeviceCapability(input.Target)
				result["outcome"] = "not_ready"
				break
			}
			projectResult, dispatchErr := g.startDeviceProjectSession(
				input.PublicKeyFingerprint,
				input.Target,
				input.Name,
			)
			if errors.Is(dispatchErr, errDeviceMediaNotReady) {
				result["action"] = "awaiting_device_media_session"
				result["outcome"] = "not_ready"
			} else if dispatchErr != nil {
				result["action"] = "project_turn_failed"
				result["outcome"] = "failed"
				statusCode = http.StatusConflict
			} else if projectResult == nil {
				result["action"] = "project_turn_failed"
				result["outcome"] = "failed"
				statusCode = http.StatusConflict
			} else {
				result["browser"] = projectResult
				if outcome, _ := projectResult["outcome"].(string); outcome == "not_ready" {
					result["action"] = "project_turn_not_ready"
					result["reason"] = projectResult["reason"]
					result["outcome"] = "not_ready"
				} else {
					result["action"] = "project_turn_completed"
					result["outcome"] = "executed"
				}
			}
		default:
			result["action"] = "unsupported_device_command"
			result["outcome"] = "not_ready"
		}
	}
	body, err := json.Marshal(result)
	if err != nil {
		log.Printf("device command result could not be encoded command=%s: %v", input.Command, err)
		return
	}
	if err := g.enrollment.completeEvent(input, statusCode, body); err != nil {
		log.Printf("device command result could not be persisted command=%s elapsed=%s: %v", input.Command, time.Since(started).Round(time.Millisecond), err)
		return
	}
	log.Printf("device command dispatched command=%s outcome=%s elapsed=%s", input.Command, result["outcome"], time.Since(started).Round(time.Millisecond))
}

func (g *gateway) deviceCandidateSyncResult(input deviceEventRequest, deviceName string) (int, []byte) {
	result := map[string]any{
		"version":  1,
		"accepted": true,
		"device":   deviceName,
		"event":    "sync",
		"outcome":  "accepted",
	}
	g.voiceMu.Lock()
	catalog, err := g.browserCandidateCatalog()
	g.voiceMu.Unlock()
	status := http.StatusOK
	if err != nil {
		result["action"] = "candidate_sync_failed"
		result["outcome"] = "failed"
		result["reason"] = err.Error()
		status = http.StatusConflict
	} else {
		result["action"] = "candidates_synchronized"
		result["outcome"] = "executed"
		result["catalog"] = deviceCandidateCatalog(catalog)
	}
	body, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return http.StatusInternalServerError, []byte(`{"accepted":false,"outcome":"failed","action":"candidate_sync_encode_failed"}`)
	}
	return status, body
}

func (g *gateway) handleDeviceEvent(w http.ResponseWriter, r *http.Request) {
	// This route is intentionally outside the administrator-cookie middleware:
	// the board authenticates with its enrolled P-256 key and a replay cursor.
	// It is still deny-by-default because acceptEvent verifies identity, state,
	// signature, and counter before anything is returned as accepted.
	var input deviceEventRequest
	if err := decodeJSONBody(w, r, 8<<10, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid device event")
		return
	}
	acceptance, err := g.enrollment.beginEvent(input)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if acceptance.Replay {
		if acceptance.Completed {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(acceptance.Result.Status)
			_, _ = w.Write(acceptance.Result.Body)
			return
		}
		if input.Event == "sync" {
			status, body := g.deviceCandidateSyncResult(input, acceptance.Record.Name)
			if err := g.enrollment.completeEvent(input, status, body); err != nil {
				writeError(w, http.StatusInternalServerError, "device candidate sync result could not be persisted")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(body)
			return
		}
		// The first request is still dispatching. Re-scheduling is harmless after
		// a Gateway restart (the in-memory dispatch map is empty), while the map
		// prevents an ordinary board retry from launching duplicate browser work.
		if input.Event == "command" && deviceBrowserCommand(input.Command) {
			if scheduled, _ := g.scheduleDeviceDispatch(input, acceptance.Record.Name); !scheduled {
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{
					"version":  1,
					"accepted": true,
					"event":    input.Event,
					"outcome":  "failed",
					"action":   "device_command_queue_full",
				})
				return
			}
		}
		// Tell the board to retry the same signed event instead of turning an
		// in-flight browser operation into a terminal failure. beginEvent's
		// proof cursor makes this retry idempotent and no browser action repeats.
		writeJSON(w, http.StatusAccepted, map[string]any{
			"version":      1,
			"accepted":     true,
			"event":        input.Event,
			"outcome":      "processing",
			"action":       "event_in_flight",
			"retryAfterMs": 500,
		})
		return
	}
	record := acceptance.Record
	if !acceptance.Replay {
		log.Printf(
			"device event accepted device=%s event=%s command=%s target=%s counter=%d",
			record.Name,
			input.Event,
			input.Command,
			input.Target,
			input.Counter,
		)
	}
	writeDeviceResult := func(status int, value any) {
		body, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			writeError(w, http.StatusInternalServerError, "device event result could not be encoded")
			return
		}
		if completeErr := g.enrollment.completeEvent(input, status, body); completeErr != nil {
			writeError(w, http.StatusInternalServerError, "device event result could not be persisted")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
	result := map[string]any{
		"version":  1,
		"accepted": true,
		"device":   record.Name,
		"event":    input.Event,
		"outcome":  "accepted",
	}
	if input.Event == "sync" {
		status, body := g.deviceCandidateSyncResult(input, record.Name)
		if err := g.enrollment.completeEvent(input, status, body); err != nil {
			writeError(w, http.StatusInternalServerError, "device candidate sync result could not be persisted")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
		return
	}
	// Browser automation is deliberately conservative at this boundary. A
	// signed command is accepted quickly, then dispatched by one bounded worker;
	// the board keeps the media peer alive while Chromium catches up.
	if input.Event == "command" {
		result["command"] = input.Command
		wakeWordEnabled := true
		if g.settings != nil {
			wakeWordEnabled = g.settings.get().WakeWord.Enabled
		}
		if !wakeWordEnabled {
			result["action"] = "wake_word_commands_disabled"
			result["outcome"] = "not_ready"
			writeDeviceResult(http.StatusOK, result)
			return
		}
		if deviceBrowserCommand(input.Command) {
			scheduled, _ := g.scheduleDeviceDispatch(input, record.Name)
			if !scheduled {
				result["action"] = "device_command_queue_full"
				result["outcome"] = "failed"
				writeDeviceResult(http.StatusServiceUnavailable, result)
				return
			}
			result["action"] = "device_command_processing"
			result["outcome"] = "processing"
			result["retryAfterMs"] = 500
			body, marshalErr := json.Marshal(result)
			if marshalErr != nil {
				writeError(w, http.StatusInternalServerError, "device event result could not be encoded")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write(body)
			return
		}
		if input.Command == "project" {
			result["action"], result["reason"] = projectDeviceCapability(input.Target)
			result["mode"] = "turn_based"
			result["outcome"] = "not_ready"
		}
	}
	writeDeviceResult(http.StatusOK, result)
}

func (g *gateway) handleInterpretCommand(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Transcript string `json:"transcript"`
	}
	if err := decodeJSONBody(w, r, 4<<10, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid command")
		return
	}
	if input.Transcript = strings.TrimSpace(input.Transcript); input.Transcript == "" || len(input.Transcript) > 1024 {
		writeError(w, http.StatusBadRequest, "command transcript is empty or too long")
		return
	}
	writeJSON(w, http.StatusOK, interpretWakeCommand(input.Transcript, g.settings.get()))
}

func (g *gateway) handleExitCommand(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Transcript string `json:"transcript"`
	}
	if err := decodeJSONBody(w, r, 4<<10, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid command")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"exit": isExitCommand(input.Transcript, g.settings.get())})
}

func (g *gateway) handleNewChat(w http.ResponseWriter, _ *http.Request) {
	g.voiceMu.Lock()
	defer g.voiceMu.Unlock()
	status, code, err := g.browserRequest(http.MethodPost, "/navigate")
	if err != nil || code >= 400 {
		writeError(w, http.StatusConflict, "ChatGPT could not start a new chat")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (g *gateway) handleSelectVoice(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSONBody(w, r, 4<<10, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid voice selection")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 64 {
		writeError(w, http.StatusBadRequest, "voice name is empty or too long")
		return
	}
	g.voiceMu.Lock()
	defer g.voiceMu.Unlock()
	voices, err := g.browserNames("/voices")
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	resolved, err := resolveSpokenName(input.Name, voices)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	input.Name = resolved
	var output map[string]any
	if _, err := g.browserJSONRequest(http.MethodPost, "/voice/select", input, &output, 15*time.Second); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, output)
}

func (g *gateway) handleProjectTurn(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Target      string `json:"target"`
		ProjectName string `json:"projectName"`
		Prompt      string `json:"prompt"`
	}
	if err := decodeJSONBody(w, r, 40<<10, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid project request")
		return
	}
	input.Target = strings.ToLower(strings.TrimSpace(input.Target))
	input.ProjectName = strings.TrimSpace(input.ProjectName)
	input.Prompt = strings.TrimSpace(input.Prompt)
	if (input.Target != "chatgpt" && input.Target != "codex") || input.ProjectName == "" || len(input.ProjectName) > 128 || input.Prompt == "" || len(input.Prompt) > 32_000 {
		writeError(w, http.StatusBadRequest, "project target, name, or request is invalid")
		return
	}
	g.voiceMu.Lock()
	defer g.voiceMu.Unlock()
	if input.Target == "chatgpt" {
		projects, err := g.browserNames("/projects?target=chatgpt")
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		resolved, err := resolveSpokenName(input.ProjectName, projects)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		input.ProjectName = resolved
	}
	var output map[string]any
	if _, err := g.browserJSONRequest(http.MethodPost, "/project/turn", input, &output, 6*time.Minute); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, output)
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
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/auth/status", g.handleAuthStatus)
	mux.HandleFunc("POST /api/auth/bootstrap", g.handleAuthBootstrap)
	mux.HandleFunc("POST /api/auth/login", g.handleAuthLogin)
	// Device authentication bootstrap is authorized by a one-use token plus
	// proof of possession of the P-256 key bound in authenticated Admin.
	mux.HandleFunc("POST /api/auth/device-enroll", g.handleDeviceEnrollmentProof)
	mux.HandleFunc("POST /api/device/events", g.handleDeviceEvent)
	mux.HandleFunc("POST /api/device/webrtc/offer", g.handleDeviceMediaOffer)
	mux.HandleFunc("POST /api/auth/logout", g.protect(g.handleAuthLogout, true))
	mux.HandleFunc("GET /api/auth/authorize", g.handleAuthAuthorize)
	mux.HandleFunc("GET /api/status", g.protect(g.handleStatus, false))
	mux.HandleFunc("POST /api/webrtc/offer", g.protect(g.handleOffer, true))
	mux.HandleFunc("POST /api/voice/start", g.protect(func(w http.ResponseWriter, r *http.Request) { g.handleVoice(w, r, "start") }, true))
	mux.HandleFunc("POST /api/voice/stop", g.protect(func(w http.ResponseWriter, r *http.Request) { g.handleVoice(w, r, "stop") }, true))
	mux.HandleFunc("GET /api/settings", g.protect(g.handleSettings, false))
	mux.HandleFunc("PUT /api/settings", g.protect(g.handleSettings, true))
	mux.HandleFunc("GET /api/devices", g.protect(g.handleDevices, false))
	mux.HandleFunc("GET /api/candidates", g.protect(g.handleCandidates, false))
	mux.HandleFunc("POST /api/devices/enrollment", g.protect(g.handleDeviceEnrollment, true))
	mux.HandleFunc("POST /api/commands/interpret", g.protect(g.handleInterpretCommand, true))
	mux.HandleFunc("POST /api/commands/exit", g.protect(g.handleExitCommand, true))
	mux.HandleFunc("POST /api/browser/new-chat", g.protect(g.handleNewChat, true))
	mux.HandleFunc("POST /api/voice/select", g.protect(g.handleSelectVoice, true))
	mux.HandleFunc("POST /api/project/turn", g.protect(g.handleProjectTurn, true))
	mux.HandleFunc("GET /api/push/key", g.protect(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"publicKey": g.pushKeys.Public})
	}, false))
	mux.HandleFunc("POST /api/push/subscribe", g.protect(g.handlePushSubscribe, true))
	mux.HandleFunc("POST /api/push/test", g.protect(func(w http.ResponseWriter, _ *http.Request) {
		incident := randomIncidentID()
		if err := g.sendPush("Snowball test alert", "Push recovery is working on this device.", "/?incident="+incident); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"incident": incident})
	}, true))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("cache-control", "no-store")
		w.Header().Set("x-content-type-options", "nosniff")
		w.Header().Set("referrer-policy", "no-referrer")
		w.Header().Set("vary", "Origin, Sec-Fetch-Site")
		mux.ServeHTTP(w, r)
	})
}

func clientAddress(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(forwarded) != nil {
		return forwarded
	}
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
		WriteTimeout:      30 * time.Second,
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
