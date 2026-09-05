package main

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"snowball.local/voice-gateway/devproto"
)

func TestPCMASignalDetection(t *testing.T) {
	if pcmaHasSignal([]byte{0xd5, 0xd5, 0x55, 0xd5, 0xd5, 0x55, 0xd5, 0xd5}) {
		t.Fatal("A-law silence was classified as audio")
	}
	if !pcmaHasSignal([]byte{0xd5, 0xd5, 0x2a, 0x2a, 0x2a, 0x2a, 0x2a, 0x2a}) {
		t.Fatal("A-law audio was classified as silence")
	}
}

func TestBrowserCandidateCatalogIsBoundedAndAuthenticated(t *testing.T) {
	browser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/candidates" {
			t.Fatalf("unexpected candidate request: %s %s", r.Method, r.URL.Path)
		}
		writeJSON(w, http.StatusOK, browserCandidateCatalog{
			Version: 1, Source: "chatgpt-web", Authenticated: true,
			VoiceState: "available", Voices: []string{"Cove", "cove"}, Projects: []string{" Snowball ", "Lab"},
		})
	}))
	defer browser.Close()
	gateway := &gateway{cfg: config{BrowserController: browser.URL}, httpClient: browser.Client()}
	catalog, err := gateway.browserCandidateCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Voices) != 1 || catalog.Voices[0] != "Cove" {
		t.Fatalf("voice candidates were not normalized: %#v", catalog.Voices)
	}
	if len(catalog.Projects) != 2 || catalog.Projects[0] != "Snowball" {
		t.Fatalf("project candidates were not normalized: %#v", catalog.Projects)
	}
}

func TestDeviceCandidateCatalogFitsBoundedTransport(t *testing.T) {
	values := make([]string, maximumDeviceCandidates+5)
	for index := range values {
		values[index] = strings.Repeat("x", maximumDeviceCandidateNameLength+10)
	}
	catalog := deviceCandidateCatalog(browserCandidateCatalog{
		Version: 1, Source: "chatgpt-web", Authenticated: true,
		VoiceState: "available", Voices: values, Projects: values,
	})
	if len(catalog.Voices) != maximumDeviceCandidates || len(catalog.Projects) != maximumDeviceCandidates {
		t.Fatalf("device candidate catalog was not bounded: voices=%d projects=%d", len(catalog.Voices), len(catalog.Projects))
	}
	for _, name := range append(catalog.Voices, catalog.Projects...) {
		if len(name) != maximumDeviceCandidateNameLength {
			t.Fatalf("candidate name length=%d, want %d", len(name), maximumDeviceCandidateNameLength)
		}
	}
}

func TestProjectDeviceCapabilityIsExplicitPerTarget(t *testing.T) {
	if action, _ := projectDeviceCapability("codex"); action != "codex_project_host_unavailable" {
		t.Fatalf("unexpected Codex capability action: %s", action)
	}
	if action, _ := projectDeviceCapability("chatgpt"); action != "chatgpt_project_turn_ready" {
		t.Fatalf("unexpected ChatGPT capability action: %s", action)
	}
}

func TestRecoverStalledDeviceVoiceClosesPeerAndStopsBrowserVoice(t *testing.T) {
	stopCalls := 0
	browser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/voice/stop" {
			t.Fatalf("unexpected browser request: %s %s", r.Method, r.URL.Path)
		}
		stopCalls++
		writeJSON(w, http.StatusOK, browserStatus{State: "ready", Authenticated: true})
	}))
	defer browser.Close()

	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	gateway := &gateway{
		cfg:                    config{BrowserController: browser.URL},
		httpClient:             browser.Client(),
		peer:                   peer,
		peerConnected:          true,
		peerDeviceFingerprint:  "device-fingerprint",
		peerDeviceVoiceStarted: true,
		peerAudioMode:          "pcma",
		lastDeviceUplinkAudio:  time.Now().Add(-deviceAudioStallAfter - time.Second),
	}

	gateway.recoverStalledDeviceVoice(browserStatus{VoiceActive: true})

	if stopCalls != 1 {
		t.Fatalf("expected one browser Voice stop, got %d", stopCalls)
	}
	if gateway.peer != nil || gateway.peerConnected {
		t.Fatal("stalled device peer remained active")
	}
	if gateway.lastVoiceRecovery.IsZero() {
		t.Fatal("recovery cooldown was not recorded")
	}
}

func TestRecoverStalledDeviceVoiceAcceptsContinuousSilentDownlink(t *testing.T) {
	stopCalls := 0
	browser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stopCalls++
		writeJSON(w, http.StatusOK, browserStatus{State: "ready", Authenticated: true})
	}))
	defer browser.Close()

	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	gateway := &gateway{
		cfg:                      config{BrowserController: browser.URL},
		httpClient:               browser.Client(),
		peer:                     peer,
		peerConnected:            true,
		peerDeviceFingerprint:    "device-fingerprint",
		peerDeviceVoiceStarted:   true,
		peerAudioMode:            "pcma",
		lastDeviceUplinkAudio:    time.Now().Add(-deviceAudioStallAfter - time.Second),
		lastDeviceDownlinkAudio:  time.Time{},
		lastDeviceDownlinkPacket: time.Now(),
	}

	gateway.recoverStalledDeviceVoice(browserStatus{VoiceActive: true})

	if stopCalls != 0 {
		t.Fatalf("continuous silent RTP incorrectly stopped browser Voice %d times", stopCalls)
	}
	if gateway.peer != peer || !gateway.peerConnected {
		t.Fatal("healthy silent downlink transport was closed")
	}
}

func TestDeviceVoiceStatusRequiresTwoInactiveObservations(t *testing.T) {
	gateway := &gateway{
		peerDeviceFingerprint:  "device-fingerprint",
		peerDeviceVoiceStarted: true,
	}
	if gateway.observeDeviceVoiceStatus(false) {
		t.Fatal("a single transient inactive observation ended Voice")
	}
	if gateway.observeDeviceVoiceStatus(true) {
		t.Fatal("an active observation ended Voice")
	}
	if gateway.observeDeviceVoiceStatus(false) {
		t.Fatal("the first inactive observation after recovery ended Voice")
	}
	if !gateway.observeDeviceVoiceStatus(false) {
		t.Fatal("two consecutive inactive observations did not end Voice")
	}
	if gateway.observeDeviceVoiceStatus(false) {
		t.Fatal("the same inactive observation was retriggered repeatedly")
	}
}

func TestAuthoritativeDeviceVoiceStatusDoesNotInvertActiveState(t *testing.T) {
	gateway := &gateway{
		peerDeviceFingerprint:  "device-fingerprint",
		peerDeviceVoiceStarted: true,
	}
	active := browserStatus{State: "ready", VoiceButtonPresent: true, VoiceActive: true}
	idle := browserStatus{State: "ready", VoiceButtonPresent: true, VoiceActive: false}
	transitional := browserStatus{State: "starting", VoiceButtonPresent: false, VoiceActive: false}

	if gateway.observeAuthoritativeDeviceVoiceStatus(active) {
		t.Fatal("an active Voice observation ended the device peer")
	}
	if gateway.observeAuthoritativeDeviceVoiceStatus(idle) {
		t.Fatal("the first authoritative idle observation ended Voice")
	}
	if !gateway.observeAuthoritativeDeviceVoiceStatus(idle) {
		t.Fatal("two authoritative idle observations did not end Voice")
	}
	if gateway.observeAuthoritativeDeviceVoiceStatus(transitional) {
		t.Fatal("a transitional browser observation ended Voice")
	}
	if gateway.deviceVoiceInactiveObservations != 0 {
		t.Fatal("transitional browser state did not reset the inactive debounce")
	}
}

func TestAuthoritativeVoiceLiveRequiresBrowserAndBoundPeer(t *testing.T) {
	tests := []struct {
		name          string
		browserActive bool
		peer          bool
		connected     bool
		device        bool
		deviceStarted bool
		audioMode     string
		want          bool
	}{
		{name: "idle browser", browserActive: false, peer: true, connected: true, audioMode: "opus", want: false},
		{name: "browser active before peer", browserActive: true, peer: false, connected: false, audioMode: "", want: false},
		{name: "browser voice live", browserActive: true, peer: true, connected: true, audioMode: "opus", want: true},
		{name: "device transport before browser action", browserActive: true, peer: true, connected: true, device: true, audioMode: "pcma", want: false},
		{name: "device voice live", browserActive: true, peer: true, connected: true, device: true, deviceStarted: true, audioMode: "pcma", want: true},
		{name: "device peer with browser idle", browserActive: false, peer: true, connected: true, device: true, deviceStarted: true, audioMode: "pcma", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gateway := &gateway{
				peer: func() *webrtc.PeerConnection {
					if test.peer {
						return &webrtc.PeerConnection{}
					}
					return nil
				}(),
				peerConnected: test.connected,
				peerDeviceFingerprint: func() string {
					if test.device {
						return "device"
					}
					return ""
				}(),
				peerDeviceVoiceStarted: test.deviceStarted,
				peerAudioMode:          test.audioMode,
			}
			status := browserStatus{VoiceActive: test.browserActive}
			gateway.peerMu.RLock()
			peerPresent := gateway.peer != nil
			connected := gateway.peerConnected
			deviceFingerprint := gateway.peerDeviceFingerprint
			deviceVoiceStarted := gateway.peerDeviceVoiceStarted
			audioMode := gateway.peerAudioMode
			gateway.peerMu.RUnlock()
			got := gateway.authoritativeVoiceLive(status, peerPresent, connected, deviceFingerprint, deviceVoiceStarted, audioMode)
			if got != test.want {
				t.Fatalf("voiceLive=%t, want %t", got, test.want)
			}
		})
	}
}

func TestPushSubscriptionAcceptsStandardShapeAndRejectsUnknownFields(t *testing.T) {
	gateway := &gateway{cfg: config{StateDir: t.TempDir()}}
	valid := `{"endpoint":"https://push.example.test/subscription","expirationTime":null,"keys":{"auth":"auth-key","p256dh":"p256dh-key"}}`
	request := httptest.NewRequest(http.MethodPost, "/api/push/subscribe", strings.NewReader(valid))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	gateway.handlePushSubscribe(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("standard PushSubscription was rejected: %d %s", response.Code, response.Body.String())
	}

	unknown := `{"endpoint":"https://push.example.test/subscription","expirationTime":null,"keys":{"auth":"auth-key","p256dh":"p256dh-key"},"unexpected":true}`
	request = httptest.NewRequest(http.MethodPost, "/api/push/subscribe", strings.NewReader(unknown))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	gateway.handlePushSubscribe(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown PushSubscription field was accepted: %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/push/subscribe", strings.NewReader(valid))
	request.Header.Set("Content-Type", "application/json-patch+json")
	response = httptest.NewRecorder()
	gateway.handlePushSubscribe(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unexpected JSON media type was accepted: %d", response.Code)
	}
}

func activeTestDevice(t *testing.T) (*enrollmentManager, devproto.EnrollmentRequest, *ecdsa.PrivateKey) {
	t.Helper()
	directory := t.TempDir()
	manager, err := newEnrollmentManager(directory, writeTestCA(t, directory))
	if err != nil {
		t.Fatal(err)
	}
	request, key := testDeviceRequestAndKey(t)
	material, err := manager.issue(request, "192.168.1.1", 8088, 8443, 300)
	if err != nil {
		t.Fatal(err)
	}
	nonce := "0123456789abcdefghijklmnopqrstuv"
	digest := sha256.Sum256(devproto.EnrollmentProofMessage(material.EnrollmentToken, request.HardwareID, request.PublicKeyFingerprint, nonce))
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.complete(devproto.EnrollmentProof{
		EnrollmentToken: material.EnrollmentToken, HardwareID: request.HardwareID,
		PublicKeyFingerprint: request.PublicKeyFingerprint, Nonce: nonce,
		Signature: base64.RawURLEncoding.EncodeToString(signature),
	}); err != nil {
		t.Fatal(err)
	}
	return manager, request, key
}

func TestStartVoiceDeviceEventRequiresBoundMediaAndAuthoritativeBrowserState(t *testing.T) {
	manager, device, key := activeTestDevice(t)
	browserCalls := 0
	browser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		browserCalls++
		if r.Method == http.MethodGet && r.URL.Path == "/candidates" {
			writeJSON(w, http.StatusOK, browserCandidateCatalog{
				Version: 1, Source: "chatgpt-web", Authenticated: true,
				VoiceState: "available", Voices: []string{"Cove"}, Projects: []string{"Snowball"},
			})
			return
		}
		if (r.Method != http.MethodGet || r.URL.Path != "/status") &&
			(r.Method != http.MethodPost || (r.URL.Path != "/voice/stop" && r.URL.Path != "/navigate" && r.URL.Path != "/voice/start")) {
			t.Errorf("unexpected browser request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		active := r.URL.Path == "/voice/start"
		writeJSON(w, http.StatusOK, browserStatus{State: "ready", Authenticated: true, VoiceActive: active})
	}))
	defer browser.Close()

	gateway := &gateway{
		cfg:                   config{BrowserController: browser.URL},
		httpClient:            browser.Client(),
		enrollment:            manager,
		peer:                  &webrtc.PeerConnection{},
		peerConnected:         true,
		peerDeviceFingerprint: device.PublicKeyFingerprint,
		peerAudioMode:         "pcma",
	}
	event := devproto.EventRequest{
		Version: 1, HardwareID: device.HardwareID, PublicKeyFingerprint: device.PublicKeyFingerprint,
		BootNonce: 3, Counter: 1, Event: "command", Wake: "hi_esp", Command: "start_voice",
		Target: "chatgpt", Confidence: 1,
	}
	digest := sha256.Sum256(devproto.EventProofMessage(event))
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	event.Signature = base64.RawURLEncoding.EncodeToString(signature)
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/device/events", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	gateway.handleDeviceEvent(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"outcome":"processing"`) {
		t.Fatalf("voice start was not accepted asynchronously: %d %s", response.Code, response.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		manager.mu.Lock()
		status := manager.devices[0].LastEventStatus
		manager.mu.Unlock()
		if status != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if browserCalls != 3 || !gateway.peerDeviceVoiceStarted {
		t.Fatalf("voice start was not bound to the active device peer: calls=%d started=%v", browserCalls, gateway.peerDeviceVoiceStarted)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.devices[0].LastEventStatus != http.StatusOK || !strings.Contains(string(manager.devices[0].LastEventResponse), `"outcome":"executed"`) {
		t.Fatalf("voice start terminal receipt was not persisted: status=%d body=%s", manager.devices[0].LastEventStatus, manager.devices[0].LastEventResponse)
	}
}

func TestInFlightDeviceEventReturnsProcessingWithoutRepeatingBrowserAction(t *testing.T) {
	manager, device, key := activeTestDevice(t)
	event := devproto.EventRequest{
		Version: 1, HardwareID: device.HardwareID, PublicKeyFingerprint: device.PublicKeyFingerprint,
		BootNonce: 21, Counter: 1, Event: "command", Wake: "hi_esp", Command: "new_chat",
		Target: "chatgpt", Confidence: 1,
	}
	signTestDeviceEvent(t, &event, key)
	if acceptance, err := manager.beginEvent(event); err != nil || acceptance.Replay {
		t.Fatalf("could not create in-flight event: %#v, %v", acceptance, err)
	}

	gateway := &gateway{enrollment: manager}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/device/events", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	gateway.handleDeviceEvent(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("in-flight event did not return 202: %d %s", response.Code, response.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["outcome"] != "processing" || result["action"] != "event_in_flight" || result["accepted"] != true {
		t.Fatalf("unexpected in-flight response: %#v", result)
	}
	// The replay path may reschedule a dispatch after a Gateway restart. Wait
	// for that bounded worker to persist its terminal receipt before t.TempDir
	// removes the enrollment state; otherwise the worker can race cleanup while
	// writing the receipt's atomic temp file.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		manager.mu.Lock()
		status := manager.devices[0].LastEventStatus
		manager.mu.Unlock()
		if status != 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("in-flight dispatch did not persist a terminal receipt")
}

func TestClosingPeerDisarmsOnlyItsOnDemandMediaBridge(t *testing.T) {
	for _, test := range []struct {
		mode string
		path string
	}{
		{mode: "opus", path: browserMediaActivePath},
		{mode: "pcma", path: deviceMediaActivePath},
	} {
		t.Run(test.mode, func(t *testing.T) {
			_ = os.Remove(browserMediaActivePath)
			_ = os.Remove(deviceMediaActivePath)
			if err := os.WriteFile(test.path, []byte("active\n"), 0600); err != nil {
				t.Fatal(err)
			}
			gateway := &gateway{peerAudioMode: test.mode}
			gateway.closeActivePeer()
			if _, err := os.Stat(test.path); !os.IsNotExist(err) {
				t.Fatalf("media marker still exists after peer close: %v", err)
			}
		})
	}
}
