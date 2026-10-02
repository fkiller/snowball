package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"snowball.local/voice-gateway/devproto"
)

type pendingDeviceEnrollment struct {
	Fingerprint string
	HardwareID  string
	PublicKey    *ecdsa.PublicKey
	ExpiresAt   time.Time
}

type enrollmentManager struct {
	mu            sync.Mutex
	path          string
	caFingerprint string
	pending       map[[sha256.Size]byte]pendingDeviceEnrollment
	devices       []devproto.Record
}

func certificateFingerprint(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return "", errors.New("Gateway CA certificate is not a single PEM certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse Gateway CA certificate: %w", err)
	}
	digest := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(digest[:]), nil
}

func newEnrollmentManager(stateDir, caPath string) (*enrollmentManager, error) {
	fingerprint, err := certificateFingerprint(caPath)
	if err != nil {
		return nil, err
	}
	manager := &enrollmentManager{
		path:          filepath.Join(stateDir, "devices.json"),
		caFingerprint: fingerprint,
		pending:       make(map[[sha256.Size]byte]pendingDeviceEnrollment),
	}
	raw, err := os.ReadFile(manager.path)
	if errors.Is(err, os.ErrNotExist) {
		return manager, nil
	}
	if err != nil {
		return nil, err
	}
	var state devproto.RegistryState
	if err := devproto.StrictUnmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode device registry: %w", err)
	}
	if state.Version != 1 || len(state.Devices) > 128 {
		return nil, errors.New("device registry uses an unsupported format")
	}
	for _, record := range state.Devices {
		candidate := devproto.EnrollmentRequest{
			Name:                 record.Name,
			Model:                record.Model,
			FirmwareVersion:      record.FirmwareVersion,
			ProtocolVersion:      record.ProtocolVersion,
			HardwareID:           record.HardwareID,
			PublicKey:            record.PublicKey,
			PublicKeyFingerprint: record.PublicKeyFingerprint,
		}
		if err := devproto.ValidateEnrollment(&candidate); err != nil ||
			(record.State != "pending" && record.State != "active" && record.State != "revoked") ||
			devproto.ValidateEventReceipt(record) != nil {
			return nil, errors.New("device registry contains an invalid record")
		}
	}
	manager.devices = state.Devices
	return manager, nil
}

func (m *enrollmentManager) issue(input devproto.EnrollmentRequest, gateway string, httpPort, port, lifetimeSeconds int) (devproto.EnrollmentMaterial, error) {
	parsedGateway := net.ParseIP(gateway)
	if parsedGateway == nil || parsedGateway.To4() == nil || !parsedGateway.IsPrivate() ||
		httpPort < 1 || httpPort > 65535 || port < 1 || port > 65535 || lifetimeSeconds < 30 || lifetimeSeconds > 900 {
		return devproto.EnrollmentMaterial{}, errors.New("Gateway enrollment parameters are invalid")
	}
	if err := devproto.ValidateEnrollment(&input); err != nil {
		return devproto.EnrollmentMaterial{}, err
	}
	token, err := randomToken(32)
	if err != nil {
		return devproto.EnrollmentMaterial{}, err
	}
	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(lifetimeSeconds) * time.Second)
	tokenDigest := sha256.Sum256([]byte(token))
	publicKey, err := devproto.ParseP256PublicKey(input.PublicKey)
	if err != nil {
		return devproto.EnrollmentMaterial{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	previousDevices := append([]devproto.Record(nil), m.devices...)
	for key, pending := range m.pending {
		if !pending.ExpiresAt.After(now) {
			delete(m.pending, key)
		}
	}
	if len(m.pending) >= 32 {
		return devproto.EnrollmentMaterial{}, errors.New("too many pairing requests are pending")
	}
	for _, existing := range m.devices {
		if existing.PublicKeyFingerprint == input.PublicKeyFingerprint && existing.HardwareID != input.HardwareID {
			return devproto.EnrollmentMaterial{}, errors.New("device key is already bound to another hardware identifier")
		}
		if existing.HardwareID == input.HardwareID && existing.PublicKeyFingerprint != input.PublicKeyFingerprint {
			return devproto.EnrollmentMaterial{}, errors.New("hardware identifier is already bound to another device key")
		}
	}
	m.pending[tokenDigest] = pendingDeviceEnrollment{
		Fingerprint: input.PublicKeyFingerprint,
		HardwareID:  input.HardwareID,
		PublicKey:    publicKey,
		ExpiresAt:   expiresAt,
	}

	record := devproto.Record{
		Name:                 input.Name,
		Model:                input.Model,
		FirmwareVersion:      input.FirmwareVersion,
		ProtocolVersion:      input.ProtocolVersion,
		HardwareID:           input.HardwareID,
		PublicKeyFingerprint: input.PublicKeyFingerprint,
		PublicKey:            input.PublicKey,
		State:                "pending",
		CreatedAt:            now.Format(time.RFC3339),
		UpdatedAt:            now.Format(time.RFC3339),
	}
	found := false
	for index := range m.devices {
		if m.devices[index].PublicKeyFingerprint == input.PublicKeyFingerprint {
			record.CreatedAt = m.devices[index].CreatedAt
			m.devices[index] = record
			found = true
			break
		}
	}
	if !found {
		if len(m.devices) >= 128 {
			delete(m.pending, tokenDigest)
			return devproto.EnrollmentMaterial{}, errors.New("device registry limit reached")
		}
		m.devices = append(m.devices, record)
	}
	if err := writePrivateJSON(m.path, devproto.RegistryState{Version: 1, Devices: m.devices}); err != nil {
		delete(m.pending, tokenDigest)
		m.devices = previousDevices
		return devproto.EnrollmentMaterial{}, err
	}
	return devproto.EnrollmentMaterial{
		EnrollmentToken: token,
		Gateway:         gateway,
		GatewayHTTPPort: httpPort,
		GatewayPort:     port,
		CASHA256:        m.caFingerprint,
		ExpiresIn:       lifetimeSeconds,
	}, nil
}

func (m *enrollmentManager) complete(input devproto.EnrollmentProof) (devproto.Record, error) {
	input.EnrollmentToken = strings.TrimSpace(input.EnrollmentToken)
	input.HardwareID = strings.ToLower(strings.TrimSpace(input.HardwareID))
	input.PublicKeyFingerprint = strings.ToLower(strings.TrimSpace(input.PublicKeyFingerprint))
	input.Nonce = strings.TrimSpace(input.Nonce)
	input.Signature = strings.TrimSpace(input.Signature)
	if !devproto.ValidBase64URL(input.EnrollmentToken, 20, 512) || !devproto.ValidBase64URL(input.Nonce, 20, 128) ||
		!devproto.ValidBase64URL(input.Signature, 64, 192) || len(input.PublicKeyFingerprint) != 64 {
		return devproto.Record{}, errors.New("device enrollment proof is invalid")
	}
	signature, err := base64.RawURLEncoding.DecodeString(input.Signature)
	if err != nil || len(signature) < 64 || len(signature) > 80 {
		return devproto.Record{}, errors.New("device enrollment signature is invalid")
	}
	tokenDigest := sha256.Sum256([]byte(input.EnrollmentToken))
	digest := sha256.Sum256(devproto.EnrollmentProofMessage(input.EnrollmentToken, input.HardwareID, input.PublicKeyFingerprint, input.Nonce))
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()
	pending, ok := m.pending[tokenDigest]
	if !ok || !pending.ExpiresAt.After(now) || pending.HardwareID != input.HardwareID ||
		pending.Fingerprint != input.PublicKeyFingerprint || pending.PublicKey == nil ||
		!ecdsa.VerifyASN1(pending.PublicKey, digest[:], signature) {
		return devproto.Record{}, errors.New("device enrollment proof is not authorized")
	}
	previousDevices := append([]devproto.Record(nil), m.devices...)
	for index := range m.devices {
		if m.devices[index].PublicKeyFingerprint != pending.Fingerprint || m.devices[index].HardwareID != pending.HardwareID {
			continue
		}
		m.devices[index].State = "active"
		m.devices[index].UpdatedAt = now.Format(time.RFC3339)
		if err := writePrivateJSON(m.path, devproto.RegistryState{Version: 1, Devices: m.devices}); err != nil {
			m.devices = previousDevices
			return devproto.Record{}, err
		}
		delete(m.pending, tokenDigest)
		return m.devices[index], nil
	}
	return devproto.Record{}, errors.New("device enrollment record is unavailable")
}

func prepareDeviceEvent(input devproto.EventRequest) (devproto.EventRequest, [sha256.Size]byte, []byte, error) {
	if err := devproto.ValidateEvent(&input); err != nil {
		return input, [sha256.Size]byte{}, nil, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(input.Signature)
	if err != nil || len(signature) < 64 || len(signature) > 80 {
		return input, [sha256.Size]byte{}, nil, errors.New("device event signature is invalid")
	}
	digest := sha256.Sum256(devproto.EventProofMessage(input))
	return input, digest, signature, nil
}

// beginEvent authenticates a device event and advances its replay cursor
// atomically with a pending receipt. An exact retry of the most recent event is
// identified by its signed proof and never mistaken for an unauthenticated
// replay. The HTTP handler must skip browser dispatch when Replay is true and
// return the cached terminal result when Completed is true.
func (m *enrollmentManager) beginEvent(input devproto.EventRequest) (devproto.EventAcceptance, error) {
	input, digest, signature, err := prepareDeviceEvent(input)
	if err != nil {
		return devproto.EventAcceptance{}, err
	}
	proof := hex.EncodeToString(digest[:])
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.devices {
		record := &m.devices[index]
		if record.HardwareID != input.HardwareID || record.PublicKeyFingerprint != input.PublicKeyFingerprint {
			continue
		}
		if record.State != "active" {
			return devproto.EventAcceptance{}, errors.New("device is not active")
		}
		publicKey, parseErr := devproto.ParseP256PublicKey(record.PublicKey)
		if parseErr != nil || !ecdsa.VerifyASN1(publicKey, digest[:], signature) {
			return devproto.EventAcceptance{}, errors.New("device event signature is not authorized")
		}
		if input.BootNonce < record.LastBootNonce ||
			(input.BootNonce == record.LastBootNonce && input.Counter < record.LastCounter) {
			return devproto.EventAcceptance{}, errors.New("device event replay rejected")
		}
		if input.BootNonce == record.LastBootNonce && input.Counter == record.LastCounter {
			if record.LastEventProof != proof {
				return devproto.EventAcceptance{}, errors.New("device event replay rejected")
			}
			acceptance := devproto.EventAcceptance{Record: *record, Replay: true}
			if record.LastEventStatus != 0 {
				acceptance.Completed = true
				acceptance.Result = devproto.EventResult{
					Status: record.LastEventStatus,
					Body:   append([]byte(nil), record.LastEventResponse...),
				}
			}
			return acceptance, nil
		}
		previous := *record
		record.LastBootNonce, record.LastCounter = input.BootNonce, input.Counter
		record.LastEventProof = proof
		record.LastEventStatus = 0
		record.LastEventResponse = nil
		if err := writePrivateJSON(m.path, devproto.RegistryState{Version: 1, Devices: m.devices}); err != nil {
			*record = previous
			return devproto.EventAcceptance{}, err
		}
		return devproto.EventAcceptance{Record: *record}, nil
	}
	return devproto.EventAcceptance{}, errors.New("device is not enrolled")
}

// completeEvent durably stores the exact JSON response before it is returned
// to the device. A later retry can therefore receive the same status and body
// without repeating browser automation, including after a Gateway restart.
func (m *enrollmentManager) completeEvent(input devproto.EventRequest, status int, body []byte) error {
	if status < 200 || status > 599 || len(body) == 0 || len(body) > devproto.MaxEventResponseBytes || !json.Valid(body) {
		return errors.New("device event result is invalid")
	}
	input, digest, signature, err := prepareDeviceEvent(input)
	if err != nil {
		return err
	}
	proof := hex.EncodeToString(digest[:])
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.devices {
		record := &m.devices[index]
		if record.HardwareID != input.HardwareID || record.PublicKeyFingerprint != input.PublicKeyFingerprint {
			continue
		}
		if record.State != "active" {
			return errors.New("device is not active")
		}
		publicKey, parseErr := devproto.ParseP256PublicKey(record.PublicKey)
		if parseErr != nil || !ecdsa.VerifyASN1(publicKey, digest[:], signature) {
			return errors.New("device event signature is not authorized")
		}
		if input.BootNonce != record.LastBootNonce || input.Counter != record.LastCounter || record.LastEventProof != proof {
			return errors.New("device event is no longer current")
		}
		if record.LastEventStatus != 0 {
			if record.LastEventStatus == status && bytes.Equal(record.LastEventResponse, body) {
				return nil
			}
			return errors.New("device event result is already complete")
		}
		previous := *record
		record.LastEventStatus = status
		record.LastEventResponse = append([]byte(nil), body...)
		if err := writePrivateJSON(m.path, devproto.RegistryState{Version: 1, Devices: m.devices}); err != nil {
			*record = previous
			return err
		}
		return nil
	}
	return errors.New("device is not enrolled")
}

func (m *enrollmentManager) getEventResult(input devproto.EventRequest) (devproto.EventResult, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.devices {
		record := &m.devices[index]
		if record.HardwareID != input.HardwareID || record.PublicKeyFingerprint != input.PublicKeyFingerprint {
			continue
		}
		if input.BootNonce == record.LastBootNonce && input.Counter == record.LastCounter && record.LastEventStatus != 0 {
			return devproto.EventResult{
				Status: record.LastEventStatus,
				Body:   append([]byte(nil), record.LastEventResponse...),
			}, true
		}
	}
	return devproto.EventResult{}, false
}

// acceptEvent preserves the original strict replay API for callers that have
// not opted into cached HTTP results. New device HTTP handlers should use
// beginEvent and completeEvent as a pair.
func (m *enrollmentManager) acceptEvent(input devproto.EventRequest) (devproto.Record, error) {
	acceptance, err := m.beginEvent(input)
	if err != nil {
		return devproto.Record{}, err
	}
	if acceptance.Replay {
		return devproto.Record{}, errors.New("device event replay rejected")
	}
	return acceptance.Record, nil
}

func (m *enrollmentManager) acceptMediaOffer(input devproto.MediaOfferRequest) (devproto.Record, error) {
	if err := devproto.ValidateMediaOffer(&input); err != nil {
		return devproto.Record{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(input.Signature)
	if err != nil || len(signature) < 64 || len(signature) > 80 {
		return devproto.Record{}, errors.New("device media offer signature is invalid")
	}
	digest := sha256.Sum256(devproto.MediaOfferProofMessage(input))
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.devices {
		record := &m.devices[index]
		if record.HardwareID != input.HardwareID || record.PublicKeyFingerprint != input.PublicKeyFingerprint {
			continue
		}
		if record.State != "active" {
			return devproto.Record{}, errors.New("device is not active")
		}
		publicKey, parseErr := devproto.ParseP256PublicKey(record.PublicKey)
		if parseErr != nil || !ecdsa.VerifyASN1(publicKey, digest[:], signature) {
			return devproto.Record{}, errors.New("device media offer signature is not authorized")
		}
		if input.BootNonce < record.LastBootNonce ||
			(input.BootNonce == record.LastBootNonce && input.Counter <= record.LastCounter) {
			return devproto.Record{}, errors.New("device media offer replay rejected")
		}
		previous := *record
		record.LastBootNonce, record.LastCounter = input.BootNonce, input.Counter
		record.LastEventProof = ""
		record.LastEventStatus = 0
		record.LastEventResponse = nil
		if err := writePrivateJSON(m.path, devproto.RegistryState{Version: 1, Devices: m.devices}); err != nil {
			*record = previous
			return devproto.Record{}, err
		}
		return *record, nil
	}
	return devproto.Record{}, errors.New("device is not enrolled")
}

func (m *enrollmentManager) isActive(fingerprint string) bool {
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, record := range m.devices {
		if record.PublicKeyFingerprint == fingerprint {
			return record.State == "active"
		}
	}
	return false
}

func (m *enrollmentManager) list() []devproto.Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]devproto.Record, len(m.devices))
	for index, record := range m.devices {
		result[index] = devproto.PublicRecord(record)
	}
	return result
}
