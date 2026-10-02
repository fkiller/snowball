package emulator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"snowball.local/voice-gateway/devproto"
)

// Client represents an emulated Snowball speaker instance managing identity,
// state transitions, media streaming, and control event dispatching.
type Client struct {
	mu           sync.Mutex
	cfg          Config
	identityMgr  *IdentityManager
	preVoiceBuf  *PreVoiceBuffer
	media        *MediaSession
	state        State
	httpClient   *http.Client
	transitions  []StateTransition
	lastReceipt  devproto.EventResult
	stateChangeC chan State
}

// NewClient creates a new emulated speaker Client.
func NewClient(cfg Config, idMgr *IdentityManager) *Client {
	if cfg.CommandTimeout <= 0 {
		cfg.CommandTimeout = 30 * time.Second
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	return &Client{
		cfg:          cfg,
		identityMgr:  idMgr,
		preVoiceBuf:  NewPreVoiceBuffer(),
		state:        StateIdle,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		stateChangeC: make(chan State, 16),
	}
}

// GetState returns the current state of the emulated speaker.
func (c *Client) GetState() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *Client) transitionTo(next State, reason string) {
	c.mu.Lock()
	from := c.state
	c.state = next
	t := StateTransition{
		From:      from,
		To:        next,
		Timestamp: time.Now(),
		Reason:    reason,
	}
	c.transitions = append(c.transitions, t)
	c.mu.Unlock()

	select {
	case c.stateChangeC <- next:
	default:
	}
}

// Pair performs the two-step enrollment with the Gateway.
func (c *Client) Pair(adminCookie, csrfToken string) error {
	id := c.identityMgr.GetIdentity()
	_, err := PairDevice(
		c.httpClient,
		c.cfg.GatewayURL,
		adminCookie,
		csrfToken,
		c.cfg.DeviceName,
		id,
	)
	return err
}

// SubmitEvent signs and posts a device control event to POST /api/device/events.
func (c *Client) SubmitEvent(event, wake, command, target, name string, confidence float64, counter uint32) (int, []byte, error) {
	id := c.identityMgr.GetIdentity()
	eventReq := devproto.EventRequest{
		Version:              devproto.ProtocolVersion,
		HardwareID:           id.HardwareID,
		PublicKeyFingerprint: id.PublicKeyFingerprint,
		BootNonce:            id.BootNonce,
		Counter:              counter,
		Event:                event,
		Wake:                 wake,
		Command:              command,
		Target:               target,
		Name:                 name,
		Confidence:           confidence,
	}

	proofMsg := devproto.EventProofMessage(eventReq)
	sig, _, err := devproto.SignProof(id.PrivateKey, proofMsg)
	if err != nil {
		return 0, nil, fmt.Errorf("sign event proof: %w", err)
	}
	eventReq.Signature = sig

	bodyBytes, err := json.Marshal(eventReq)
	if err != nil {
		return 0, nil, err
	}

	url := strings.TrimRight(c.cfg.GatewayURL, "/") + "/api/device/events"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("submit event HTTP error: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}

	return resp.StatusCode, respBody, nil
}

// SubmitEventWithRetry sends an event and handles HTTP 202 processing by
// retrying the exact same envelope at 500ms intervals until HTTP 200 or timeout.
func (c *Client) SubmitEventWithRetry(
	event, wake, command, target, name string,
	confidence float64,
	counter uint32,
) (devproto.EventResult, error) {
	deadline := time.Now().Add(c.cfg.CommandTimeout)

	for time.Now().Before(deadline) {
		status, body, err := c.SubmitEvent(event, wake, command, target, name, confidence, counter)
		if err != nil {
			return devproto.EventResult{}, err
		}

		if status == http.StatusOK {
			c.mu.Lock()
			c.lastReceipt = devproto.EventResult{Status: status, Body: body}
			c.mu.Unlock()
			return devproto.EventResult{Status: status, Body: body}, nil
		}

		if status == http.StatusAccepted {
			// In-flight command, retry identical envelope after interval
			time.Sleep(c.cfg.PollInterval)
			continue
		}

		// Any other status is an error or terminal failure
		return devproto.EventResult{Status: status, Body: body}, fmt.Errorf("event rejected with status %d: %s", status, string(body))
	}

	return devproto.EventResult{}, errors.New("command execution polling timed out")
}

// StartMediaSession creates a WebRTC PCMA media session and exchanges offers with the Gateway.
func (c *Client) StartMediaSession() (*MediaSession, error) {
	media, err := NewMediaSession()
	if err != nil {
		return nil, err
	}

	mediaCounter := c.identityMgr.NextCounter()
	offerReq, err := media.CreateSignedOffer(c.identityMgr.GetIdentity(), mediaCounter)
	if err != nil {
		_ = media.Close()
		return nil, fmt.Errorf("create signed media offer: %w", err)
	}

	if err := media.ExchangeOffer(c.httpClient, c.cfg.GatewayURL, offerReq); err != nil {
		_ = media.Close()
		return nil, fmt.Errorf("exchange media offer: %w", err)
	}

	c.mu.Lock()
	c.media = media
	c.mu.Unlock()

	return media, nil
}

// CloseMedia terminates the media session cleanly.
func (c *Client) CloseMedia() {
	c.mu.Lock()
	media := c.media
	c.media = nil
	c.mu.Unlock()

	if media != nil {
		_ = media.Close()
	}
}

// GetTransitions returns a copy of all recorded state transitions.
func (c *Client) GetTransitions() []StateTransition {
	c.mu.Lock()
	defer c.mu.Unlock()
	res := make([]StateTransition, len(c.transitions))
	copy(res, c.transitions)
	return res
}

// Reset clears state and buffers for a new session.
func (c *Client) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.media != nil {
		_ = c.media.Close()
		c.media = nil
	}
	c.preVoiceBuf.Reset()
	c.state = StateIdle
	c.transitions = nil
	c.lastReceipt = devproto.EventResult{}
}
