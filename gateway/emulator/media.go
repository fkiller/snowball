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

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"snowball.local/voice-gateway/devproto"
)

// MediaSession manages the WebRTC peer connection, PCMA uplink track, and
// downlink audio capture for the emulated speaker.
type MediaSession struct {
	mu            sync.Mutex
	peer          *webrtc.PeerConnection
	uplinkTrack   *webrtc.TrackLocalStaticRTP
	downlinkAudio [][]byte
	signalFrames  int
	connected     bool
	closed        bool
	sequenceNum   uint16
	timestamp     uint32
	ssrc          uint32
	onDownlink    func(payload []byte)
}

// NewMediaSession initializes a Pion WebRTC peer connection configured for
// PCMA/8000 mono audio.
func NewMediaSession() (*MediaSession, error) {
	api := webrtc.NewAPI()
	peer, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return nil, fmt.Errorf("create peer connection: %w", err)
	}

	codec := webrtc.RTPCodecCapability{
		MimeType:  webrtc.MimeTypePCMA,
		ClockRate: SampleRate8k,
		Channels:  1,
	}

	uplinkTrack, err := webrtc.NewTrackLocalStaticRTP(codec, "snowball-emulator-mic", "snowball-mic")
	if err != nil {
		_ = peer.Close()
		return nil, fmt.Errorf("create local PCMA track: %w", err)
	}

	sender, err := peer.AddTrack(uplinkTrack)
	if err != nil {
		_ = peer.Close()
		return nil, fmt.Errorf("add track to peer: %w", err)
	}

	// Drain incoming RTCP from sender
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
		}
	}()

	session := &MediaSession{
		peer:        peer,
		uplinkTrack: uplinkTrack,
		ssrc:        0x12345678,
	}

	// Handle incoming downlink PCMA track from Gateway
	peer.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			packet, _, err := track.ReadRTP()
			if err != nil {
				return
			}
			session.mu.Lock()
			payloadCopy := make([]byte, len(packet.Payload))
			copy(payloadCopy, packet.Payload)
			session.downlinkAudio = append(session.downlinkAudio, payloadCopy)
			if HasSignal(packet.Payload) {
				session.signalFrames++
			}
			cb := session.onDownlink
			session.mu.Unlock()

			if cb != nil {
				cb(payloadCopy)
			}
		}
	})

	peer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		session.mu.Lock()
		defer session.mu.Unlock()
		session.connected = (state == webrtc.PeerConnectionStateConnected)
	})

	return session, nil
}

// CreateSignedOffer creates the WebRTC SDP offer, waits for ICE gathering to
// complete, and signs the offer envelope using the emulator's P-256 private key.
func (s *MediaSession) CreateSignedOffer(identity Identity, counter uint32) (*devproto.MediaOfferRequest, error) {
	gatherComplete := webrtc.GatheringCompletePromise(s.peer)

	offer, err := s.peer.CreateOffer(nil)
	if err != nil {
		return nil, fmt.Errorf("create offer: %w", err)
	}

	if err := s.peer.SetLocalDescription(offer); err != nil {
		return nil, fmt.Errorf("set local description: %w", err)
	}

	select {
	case <-gatherComplete:
	case <-time.After(5 * time.Second):
		return nil, errors.New("ICE gathering timed out")
	}

	sdp := s.peer.LocalDescription().SDP
	offerReq := devproto.MediaOfferRequest{
		Version:              devproto.ProtocolVersion,
		HardwareID:           identity.HardwareID,
		PublicKeyFingerprint: identity.PublicKeyFingerprint,
		BootNonce:            identity.BootNonce,
		Counter:              counter,
		Type:                 "offer",
		SDP:                  sdp,
	}

	proofMsg := devproto.MediaOfferProofMessage(offerReq)
	sig, _, err := devproto.SignProof(identity.PrivateKey, proofMsg)
	if err != nil {
		return nil, fmt.Errorf("sign media offer proof: %w", err)
	}
	offerReq.Signature = sig

	return &offerReq, nil
}

// ExchangeOffer submits the signed offer to the Gateway's POST /api/device/webrtc/offer
// and applies the returned SDP answer.
func (s *MediaSession) ExchangeOffer(
	client *http.Client,
	gatewayURL string,
	offerReq *devproto.MediaOfferRequest,
) error {
	bodyBytes, err := json.Marshal(offerReq)
	if err != nil {
		return err
	}

	url := strings.TrimRight(gatewayURL, "/") + "/api/device/webrtc/offer"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post media offer HTTP error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("media offer rejected (status %d): %s", resp.StatusCode, string(body))
	}

	var answerResp struct {
		Version int    `json:"version"`
		Type    string `json:"type"`
		SDP     string `json:"sdp"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&answerResp); err != nil {
		return fmt.Errorf("decode media answer: %w", err)
	}

	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  answerResp.SDP,
	}

	if err := s.peer.SetRemoteDescription(answer); err != nil {
		return fmt.Errorf("set remote description: %w", err)
	}

	// Wait up to 5 seconds for connection to establish
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		conn := s.connected
		s.mu.Unlock()
		if conn {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}

	return nil
}

// SendFrame sends a single 40 ms (320-byte) G.711 A-law frame as an RTP packet.
func (s *MediaSession) SendFrame(frame []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed || s.uplinkTrack == nil {
		return errors.New("media session is closed")
	}

	packet := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    8, // PCMA
			SequenceNumber: s.sequenceNum,
			Timestamp:      s.timestamp,
			SSRC:           s.ssrc,
		},
		Payload: frame,
	}

	s.sequenceNum++
	s.timestamp += FrameSamples8k // 320 samples per 40ms frame @ 8kHz

	return s.uplinkTrack.WriteRTP(packet)
}

// SendFrames streams multiple frames sequentially with accurate 40 ms pacing.
func (s *MediaSession) SendFrames(frames [][]byte, onFrameSent func(index int)) int {
	sent := 0
	for i, frame := range frames {
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			break
		}
		if err := s.SendFrame(frame); err != nil {
			break
		}
		sent++
		if onFrameSent != nil {
			onFrameSent(i)
		}
		time.Sleep(FrameDurationMs * time.Millisecond)
	}
	return sent
}

// GetDownlinkStats returns metrics on captured downlink audio.
func (s *MediaSession) GetDownlinkStats() (totalFrames, signalFrames int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.downlinkAudio), s.signalFrames
}

// IsConnected returns whether the WebRTC connection is active.
func (s *MediaSession) IsConnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

// Close gracefully terminates the WebRTC session.
func (s *MediaSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.connected = false
	if s.peer != nil {
		return s.peer.Close()
	}
	return nil
}
