package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	deviceProtocolVersion = 1
	deviceModel           = "Waveshare ESP32-S3-AUDIO-Board"
)

type deviceEnrollmentRequest struct {
	Name                 string `json:"name"`
	Model                string `json:"model"`
	FirmwareVersion      string `json:"firmwareVersion"`
	ProtocolVersion      int    `json:"protocolVersion"`
	HardwareID           string `json:"hardwareId"`
	PublicKey            string `json:"publicKey,omitempty"`
	PublicKeyFingerprint string `json:"publicKeyFingerprint"`
}

type deviceRecord struct {
	Name                 string `json:"name"`
	Model                string `json:"model"`
	FirmwareVersion      string `json:"firmwareVersion"`
	ProtocolVersion      int    `json:"protocolVersion"`
	HardwareID           string `json:"hardwareId"`
	PublicKeyFingerprint string `json:"publicKeyFingerprint"`
	PublicKey            string `json:"publicKey"`
	State                string `json:"state"`
	CreatedAt            string `json:"createdAt"`
	UpdatedAt            string `json:"updatedAt"`
	LastBootNonce        uint32 `json:"lastBootNonce,omitempty"`
	LastCounter          uint32 `json:"lastCounter,omitempty"`
	// The last accepted event proof and its terminal HTTP result make the
	// signed device protocol idempotent. The ESP32 intentionally retries an
	// identical envelope when a response is lost; without this receipt the
	// persisted replay cursor would turn that safe retry into a 401.
	LastEventProof    string `json:"lastEventProof,omitempty"`
	LastEventStatus   int    `json:"lastEventStatus,omitempty"`
	LastEventResponse []byte `json:"lastEventResponse,omitempty"`
}

type pendingDeviceEnrollment struct {
	Fingerprint string
	HardwareID  string
	PublicKey   *ecdsa.PublicKey
	ExpiresAt   time.Time
}

type deviceState struct {
	Version int            `json:"version"`
	Devices []deviceRecord `json:"devices"`
}

type enrollmentManager struct {
	mu            sync.Mutex
	path          string
	caFingerprint string
	pending       map[[sha256.Size]byte]pendingDeviceEnrollment
	devices       []deviceRecord
}

type enrollmentMaterial struct {
	EnrollmentToken string `json:"enrollmentToken"`
	Gateway         string `json:"gateway"`
	GatewayHTTPPort int    `json:"gatewayHttpPort"`
	GatewayPort     int    `json:"gatewayPort"`
	CASHA256        string `json:"caSha256"`
	ExpiresIn       int    `json:"expiresInSeconds"`
}

type deviceEnrollmentProof struct {
	EnrollmentToken      string `json:"enrollmentToken"`
	HardwareID           string `json:"hardwareId"`
	PublicKeyFingerprint string `json:"publicKeyFingerprint"`
	Nonce                string `json:"nonce"`
	Signature            string `json:"signature"`
}

// deviceEventRequest is the authenticated control-plane message sent by a
// paired speaker. The signature covers every field that can affect command
// dispatch, so a LAN peer cannot rewrite a valid wake or command event.
type deviceEventRequest struct {
	Version              int     `json:"version"`
	HardwareID           string  `json:"hardwareId"`
	PublicKeyFingerprint string  `json:"publicKeyFingerprint"`
	BootNonce            uint32  `json:"bootNonce"`
	Counter              uint32  `json:"counter"`
	Event                string  `json:"event"`
	Wake                 string  `json:"wake,omitempty"`
	Command              string  `json:"command,omitempty"`
	Target               string  `json:"target,omitempty"`
	Name                 string  `json:"name,omitempty"`
	Confidence           float64 `json:"confidence,omitempty"`
	Signature            string  `json:"signature"`
}

type deviceEventResult struct {
	Status int
	Body   []byte
}

type deviceEventAcceptance struct {
	Record    deviceRecord
	Replay    bool
	Completed bool
	Result    deviceEventResult
}

const maximumDeviceEventResponseBytes = 32 << 10

// deviceMediaOfferRequest authenticates the one-shot WebRTC offer used by a
// paired speaker. The SDP itself is hashed into the signature so a LAN peer
// cannot substitute its own media endpoint after observing a valid request.
type deviceMediaOfferRequest struct {
	Version              int    `json:"version"`
	HardwareID           string `json:"hardwareId"`
	PublicKeyFingerprint string `json:"publicKeyFingerprint"`
	BootNonce            uint32 `json:"bootNonce"`
	Counter              uint32 `json:"counter"`
	Type                 string `json:"type"`
	SDP                  string `json:"sdp"`
	Signature            string `json:"signature"`
}

func deviceEventProofMessage(input deviceEventRequest) []byte {
	return []byte("snowball-device-event-v1\n" +
		strings.ToLower(strings.TrimSpace(input.HardwareID)) + "\n" +
		strings.ToLower(strings.TrimSpace(input.PublicKeyFingerprint)) + "\n" +
		strconv.FormatUint(uint64(input.BootNonce), 10) + "\n" +
		strconv.FormatUint(uint64(input.Counter), 10) + "\n" +
		strings.TrimSpace(input.Event) + "\n" +
		strings.TrimSpace(input.Wake) + "\n" +
		strings.TrimSpace(input.Command) + "\n" +
		strings.TrimSpace(input.Target) + "\n" +
		strings.TrimSpace(input.Name) + "\n" +
		strconv.FormatFloat(input.Confidence, 'f', 6, 64))
}

func deviceMediaOfferProofMessage(input deviceMediaOfferRequest) []byte {
	sdpDigest := sha256.Sum256([]byte(input.Type + "\n" + input.SDP))
	return []byte("snowball-device-media-v1\n" +
		strings.ToLower(strings.TrimSpace(input.HardwareID)) + "\n" +
		strings.ToLower(strings.TrimSpace(input.PublicKeyFingerprint)) + "\n" +
		strconv.FormatUint(uint64(input.BootNonce), 10) + "\n" +
		strconv.FormatUint(uint64(input.Counter), 10) + "\n" +
		hex.EncodeToString(sdpDigest[:]))
}

func strictUnmarshal(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("document must contain one JSON value")
	}
	return nil
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
	var state deviceState
	if err := strictUnmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode device registry: %w", err)
	}
	if state.Version != 1 || len(state.Devices) > 128 {
		return nil, errors.New("device registry uses an unsupported format")
	}
	for _, record := range state.Devices {
		candidate := deviceEnrollmentRequest{
			Name:                 record.Name,
			Model:                record.Model,
			FirmwareVersion:      record.FirmwareVersion,
			ProtocolVersion:      record.ProtocolVersion,
			HardwareID:           record.HardwareID,
			PublicKey:            record.PublicKey,
			PublicKeyFingerprint: record.PublicKeyFingerprint,
		}
		if err := validateDeviceEnrollment(&candidate); err != nil ||
			(record.State != "pending" && record.State != "active" && record.State != "revoked") ||
			validateDeviceEventReceipt(record) != nil {
			return nil, errors.New("device registry contains an invalid record")
		}
	}
	manager.devices = state.Devices
	return manager, nil
}

func validateDeviceEventReceipt(record deviceRecord) error {
	if record.LastEventProof == "" {
		if record.LastEventStatus != 0 || len(record.LastEventResponse) != 0 {
			return errors.New("device event receipt is incomplete")
		}
		return nil
	}
	if record.LastBootNonce == 0 || record.LastCounter == 0 || len(record.LastEventProof) != sha256.Size*2 ||
		!isHexString(record.LastEventProof) {
		return errors.New("device event receipt proof is invalid")
	}
	if record.LastEventStatus == 0 {
		if len(record.LastEventResponse) != 0 {
			return errors.New("pending device event receipt contains a response")
		}
		return nil
	}
	if record.LastEventStatus < 200 || record.LastEventStatus > 599 ||
		len(record.LastEventResponse) == 0 || len(record.LastEventResponse) > maximumDeviceEventResponseBytes ||
		!json.Valid(record.LastEventResponse) {
		return errors.New("device event receipt result is invalid")
	}
	return nil
}

func validateDeviceEnrollment(input *deviceEnrollmentRequest) error {
	input.Name = strings.Join(strings.Fields(input.Name), " ")
	input.Model = strings.TrimSpace(input.Model)
	input.FirmwareVersion = strings.TrimSpace(input.FirmwareVersion)
	input.HardwareID = strings.ToLower(strings.TrimSpace(input.HardwareID))
	input.PublicKeyFingerprint = strings.ToLower(strings.TrimSpace(input.PublicKeyFingerprint))
	if input.Name == "" || len(input.Name) > 64 {
		return errors.New("device name must be between 1 and 64 characters")
	}
	if input.Model != deviceModel || input.ProtocolVersion != deviceProtocolVersion || input.FirmwareVersion == "" || len(input.FirmwareVersion) > 64 {
		return errors.New("device model, firmware, or protocol is not supported")
	}
	mac, err := net.ParseMAC(input.HardwareID)
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 {
		return errors.New("device hardware identifier is invalid")
	}
	if len(input.PublicKey) < 100 || len(input.PublicKey) > 512 {
		return errors.New("device public key is invalid")
	}
	if _, err := parseP256PublicKey(input.PublicKey); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(input.PublicKey))
	if len(input.PublicKeyFingerprint) != 64 || input.PublicKeyFingerprint != hex.EncodeToString(digest[:]) {
		return errors.New("device public key fingerprint does not match")
	}
	return nil
}

func parseP256PublicKey(publicPEM string) (*ecdsa.PublicKey, error) {
	block, rest := pem.Decode([]byte(publicPEM))
	if block == nil || block.Type != "PUBLIC KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("device public key is invalid")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	publicKey, ok := parsed.(*ecdsa.PublicKey)
	if err != nil || !ok || publicKey.Curve != elliptic.P256() || !publicKey.Curve.IsOnCurve(publicKey.X, publicKey.Y) {
		return nil, errors.New("device public key must be P-256")
	}
	return publicKey, nil
}

func (m *enrollmentManager) issue(input deviceEnrollmentRequest, gateway string, httpPort, port, lifetimeSeconds int) (enrollmentMaterial, error) {
	parsedGateway := net.ParseIP(gateway)
	if parsedGateway == nil || parsedGateway.To4() == nil || !parsedGateway.IsPrivate() ||
		httpPort < 1 || httpPort > 65535 || port < 1 || port > 65535 || lifetimeSeconds < 30 || lifetimeSeconds > 900 {
		return enrollmentMaterial{}, errors.New("Gateway enrollment parameters are invalid")
	}
	if err := validateDeviceEnrollment(&input); err != nil {
		return enrollmentMaterial{}, err
	}
	token, err := randomToken(32)
	if err != nil {
		return enrollmentMaterial{}, err
	}
	now := time.Now().UTC()
	expiresAt := now.Add(time.Duration(lifetimeSeconds) * time.Second)
	tokenDigest := sha256.Sum256([]byte(token))
	publicKey, err := parseP256PublicKey(input.PublicKey)
	if err != nil {
		return enrollmentMaterial{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	previousDevices := append([]deviceRecord(nil), m.devices...)
	for key, pending := range m.pending {
		if !pending.ExpiresAt.After(now) {
			delete(m.pending, key)
		}
	}
	if len(m.pending) >= 32 {
		return enrollmentMaterial{}, errors.New("too many pairing requests are pending")
	}
	for _, existing := range m.devices {
		if existing.PublicKeyFingerprint == input.PublicKeyFingerprint && existing.HardwareID != input.HardwareID {
			return enrollmentMaterial{}, errors.New("device key is already bound to another hardware identifier")
		}
		if existing.HardwareID == input.HardwareID && existing.PublicKeyFingerprint != input.PublicKeyFingerprint {
			return enrollmentMaterial{}, errors.New("hardware identifier is already bound to another device key")
		}
	}
	m.pending[tokenDigest] = pendingDeviceEnrollment{
		Fingerprint: input.PublicKeyFingerprint,
		HardwareID:  input.HardwareID,
		PublicKey:   publicKey,
		ExpiresAt:   expiresAt,
	}

	record := deviceRecord{
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
			return enrollmentMaterial{}, errors.New("device registry limit reached")
		}
		m.devices = append(m.devices, record)
	}
	if err := writePrivateJSON(m.path, deviceState{Version: 1, Devices: m.devices}); err != nil {
		delete(m.pending, tokenDigest)
		m.devices = previousDevices
		return enrollmentMaterial{}, err
	}
	return enrollmentMaterial{
		EnrollmentToken: token,
		Gateway:         gateway,
		GatewayHTTPPort: httpPort,
		GatewayPort:     port,
		CASHA256:        m.caFingerprint,
		ExpiresIn:       lifetimeSeconds,
	}, nil
}

func validBase64URL(value string, minimum, maximum int) bool {
	if len(value) < minimum || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if !(character >= 'A' && character <= 'Z') && !(character >= 'a' && character <= 'z') &&
			!(character >= '0' && character <= '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func enrollmentProofMessage(token, hardwareID, fingerprint, nonce string) []byte {
	return []byte("snowball-enroll-v1\n" + token + "\n" + hardwareID + "\n" + fingerprint + "\n" + nonce)
}

func (m *enrollmentManager) complete(input deviceEnrollmentProof) (deviceRecord, error) {
	input.EnrollmentToken = strings.TrimSpace(input.EnrollmentToken)
	input.HardwareID = strings.ToLower(strings.TrimSpace(input.HardwareID))
	input.PublicKeyFingerprint = strings.ToLower(strings.TrimSpace(input.PublicKeyFingerprint))
	input.Nonce = strings.TrimSpace(input.Nonce)
	input.Signature = strings.TrimSpace(input.Signature)
	if !validBase64URL(input.EnrollmentToken, 20, 512) || !validBase64URL(input.Nonce, 20, 128) ||
		!validBase64URL(input.Signature, 64, 192) || len(input.PublicKeyFingerprint) != 64 {
		return deviceRecord{}, errors.New("device enrollment proof is invalid")
	}
	signature, err := base64.RawURLEncoding.DecodeString(input.Signature)
	if err != nil || len(signature) < 64 || len(signature) > 80 {
		return deviceRecord{}, errors.New("device enrollment signature is invalid")
	}
	tokenDigest := sha256.Sum256([]byte(input.EnrollmentToken))
	digest := sha256.Sum256(enrollmentProofMessage(input.EnrollmentToken, input.HardwareID, input.PublicKeyFingerprint, input.Nonce))
	now := time.Now().UTC()

	m.mu.Lock()
	defer m.mu.Unlock()
	pending, ok := m.pending[tokenDigest]
	if !ok || !pending.ExpiresAt.After(now) || pending.HardwareID != input.HardwareID ||
		pending.Fingerprint != input.PublicKeyFingerprint || pending.PublicKey == nil ||
		!ecdsa.VerifyASN1(pending.PublicKey, digest[:], signature) {
		return deviceRecord{}, errors.New("device enrollment proof is not authorized")
	}
	previousDevices := append([]deviceRecord(nil), m.devices...)
	for index := range m.devices {
		if m.devices[index].PublicKeyFingerprint != pending.Fingerprint || m.devices[index].HardwareID != pending.HardwareID {
			continue
		}
		m.devices[index].State = "active"
		m.devices[index].UpdatedAt = now.Format(time.RFC3339)
		if err := writePrivateJSON(m.path, deviceState{Version: 1, Devices: m.devices}); err != nil {
			m.devices = previousDevices
			return deviceRecord{}, err
		}
		delete(m.pending, tokenDigest)
		return m.devices[index], nil
	}
	return deviceRecord{}, errors.New("device enrollment record is unavailable")
}

func validateDeviceEvent(input *deviceEventRequest) error {
	input.HardwareID = strings.ToLower(strings.TrimSpace(input.HardwareID))
	input.PublicKeyFingerprint = strings.ToLower(strings.TrimSpace(input.PublicKeyFingerprint))
	input.Event = strings.TrimSpace(input.Event)
	input.Wake = strings.TrimSpace(input.Wake)
	input.Command = strings.TrimSpace(input.Command)
	input.Target = strings.ToLower(strings.TrimSpace(input.Target))
	input.Name = strings.Join(strings.Fields(input.Name), " ")
	input.Signature = strings.TrimSpace(input.Signature)
	if input.Version != deviceProtocolVersion || input.BootNonce == 0 || input.Counter == 0 {
		return errors.New("device event envelope is invalid")
	}
	if mac, err := net.ParseMAC(input.HardwareID); err != nil || len(mac) != 6 || len(input.PublicKeyFingerprint) != 64 ||
		!isHexString(input.PublicKeyFingerprint) {
		return errors.New("device event identity is invalid")
	}
	if input.Event != "wake" && input.Event != "command" && input.Event != "sync" {
		return errors.New("device event type is invalid")
	}
	if input.Event == "sync" {
		if input.Wake != "" || input.Command != "" || input.Target != "" || input.Name != "" || input.Confidence != 0 {
			return errors.New("device candidate sync contains event fields")
		}
	} else if input.Wake != "hi_esp" {
		return errors.New("device wake is invalid")
	} else if input.Event == "wake" {
		if input.Command != "" || input.Target != "" || input.Name != "" {
			return errors.New("device wake event contains command fields")
		}
	} else {
		switch input.Command {
		case "new_chat", "start_voice", "resume", "project", "voice":
		default:
			return errors.New("device command is invalid")
		}
		if input.Command == "project" && input.Target != "chatgpt" && input.Target != "codex" {
			return errors.New("device project target is invalid")
		}
		if (input.Command == "project" || input.Command == "voice") && input.Name == "" {
			return errors.New("device command name is required")
		}
	}
	if input.Wake != "" && len(input.Wake) > 32 || input.Command != "" && len(input.Command) > 32 ||
		input.Target != "" && len(input.Target) > 32 || len(input.Name) > 128 ||
		input.Confidence < 0 || input.Confidence > 1 {
		return errors.New("device event fields are out of bounds")
	}
	if !validBase64URL(input.Signature, 64, 192) {
		return errors.New("device event signature is invalid")
	}
	return nil
}

func validateDeviceMediaOffer(input *deviceMediaOfferRequest) error {
	input.HardwareID = strings.ToLower(strings.TrimSpace(input.HardwareID))
	input.PublicKeyFingerprint = strings.ToLower(strings.TrimSpace(input.PublicKeyFingerprint))
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	input.Signature = strings.TrimSpace(input.Signature)
	if input.Version != deviceProtocolVersion || input.BootNonce == 0 || input.Counter == 0 || input.Type != "offer" {
		return errors.New("device media offer envelope is invalid")
	}
	if mac, err := net.ParseMAC(input.HardwareID); err != nil || len(mac) != 6 || len(input.PublicKeyFingerprint) != 64 ||
		!isHexString(input.PublicKeyFingerprint) {
		return errors.New("device media offer identity is invalid")
	}
	if len(input.SDP) < 128 || len(input.SDP) > 64<<10 || !strings.HasPrefix(input.SDP, "v=0") ||
		strings.ContainsRune(input.SDP, '\x00') || !strings.Contains(strings.ToUpper(input.SDP), "PCMA/8000") {
		return errors.New("device media offer SDP is invalid")
	}
	if !validBase64URL(input.Signature, 64, 192) {
		return errors.New("device media offer signature is invalid")
	}
	return nil
}

func isHexString(value string) bool {
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func prepareDeviceEvent(input deviceEventRequest) (deviceEventRequest, [sha256.Size]byte, []byte, error) {
	if err := validateDeviceEvent(&input); err != nil {
		return input, [sha256.Size]byte{}, nil, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(input.Signature)
	if err != nil || len(signature) < 64 || len(signature) > 80 {
		return input, [sha256.Size]byte{}, nil, errors.New("device event signature is invalid")
	}
	digest := sha256.Sum256(deviceEventProofMessage(input))
	return input, digest, signature, nil
}

// beginEvent authenticates a device event and advances its replay cursor
// atomically with a pending receipt. An exact retry of the most recent event is
// identified by its signed proof and never mistaken for an unauthenticated
// replay. The HTTP handler must skip browser dispatch when Replay is true and
// return the cached terminal result when Completed is true.
func (m *enrollmentManager) beginEvent(input deviceEventRequest) (deviceEventAcceptance, error) {
	input, digest, signature, err := prepareDeviceEvent(input)
	if err != nil {
		return deviceEventAcceptance{}, err
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
			return deviceEventAcceptance{}, errors.New("device is not active")
		}
		publicKey, parseErr := parseP256PublicKey(record.PublicKey)
		if parseErr != nil || !ecdsa.VerifyASN1(publicKey, digest[:], signature) {
			return deviceEventAcceptance{}, errors.New("device event signature is not authorized")
		}
		if input.BootNonce < record.LastBootNonce ||
			(input.BootNonce == record.LastBootNonce && input.Counter < record.LastCounter) {
			return deviceEventAcceptance{}, errors.New("device event replay rejected")
		}
		if input.BootNonce == record.LastBootNonce && input.Counter == record.LastCounter {
			if record.LastEventProof != proof {
				return deviceEventAcceptance{}, errors.New("device event replay rejected")
			}
			acceptance := deviceEventAcceptance{Record: *record, Replay: true}
			if record.LastEventStatus != 0 {
				acceptance.Completed = true
				acceptance.Result = deviceEventResult{
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
		if err := writePrivateJSON(m.path, deviceState{Version: 1, Devices: m.devices}); err != nil {
			*record = previous
			return deviceEventAcceptance{}, err
		}
		return deviceEventAcceptance{Record: *record}, nil
	}
	return deviceEventAcceptance{}, errors.New("device is not enrolled")
}

// completeEvent durably stores the exact JSON response before it is returned
// to the device. A later retry can therefore receive the same status and body
// without repeating browser automation, including after a Gateway restart.
func (m *enrollmentManager) completeEvent(input deviceEventRequest, status int, body []byte) error {
	if status < 200 || status > 599 || len(body) == 0 || len(body) > maximumDeviceEventResponseBytes || !json.Valid(body) {
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
		publicKey, parseErr := parseP256PublicKey(record.PublicKey)
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
		if err := writePrivateJSON(m.path, deviceState{Version: 1, Devices: m.devices}); err != nil {
			*record = previous
			return err
		}
		return nil
	}
	return errors.New("device is not enrolled")
}

// acceptEvent preserves the original strict replay API for callers that have
// not opted into cached HTTP results. New device HTTP handlers should use
// beginEvent and completeEvent as a pair.
func (m *enrollmentManager) acceptEvent(input deviceEventRequest) (deviceRecord, error) {
	acceptance, err := m.beginEvent(input)
	if err != nil {
		return deviceRecord{}, err
	}
	if acceptance.Replay {
		return deviceRecord{}, errors.New("device event replay rejected")
	}
	return acceptance.Record, nil
}

func (m *enrollmentManager) acceptMediaOffer(input deviceMediaOfferRequest) (deviceRecord, error) {
	if err := validateDeviceMediaOffer(&input); err != nil {
		return deviceRecord{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(input.Signature)
	if err != nil || len(signature) < 64 || len(signature) > 80 {
		return deviceRecord{}, errors.New("device media offer signature is invalid")
	}
	digest := sha256.Sum256(deviceMediaOfferProofMessage(input))
	m.mu.Lock()
	defer m.mu.Unlock()
	for index := range m.devices {
		record := &m.devices[index]
		if record.HardwareID != input.HardwareID || record.PublicKeyFingerprint != input.PublicKeyFingerprint {
			continue
		}
		if record.State != "active" {
			return deviceRecord{}, errors.New("device is not active")
		}
		publicKey, parseErr := parseP256PublicKey(record.PublicKey)
		if parseErr != nil || !ecdsa.VerifyASN1(publicKey, digest[:], signature) {
			return deviceRecord{}, errors.New("device media offer signature is not authorized")
		}
		if input.BootNonce < record.LastBootNonce ||
			(input.BootNonce == record.LastBootNonce && input.Counter <= record.LastCounter) {
			return deviceRecord{}, errors.New("device media offer replay rejected")
		}
		previous := *record
		record.LastBootNonce, record.LastCounter = input.BootNonce, input.Counter
		record.LastEventProof = ""
		record.LastEventStatus = 0
		record.LastEventResponse = nil
		if err := writePrivateJSON(m.path, deviceState{Version: 1, Devices: m.devices}); err != nil {
			*record = previous
			return deviceRecord{}, err
		}
		return *record, nil
	}
	return deviceRecord{}, errors.New("device is not enrolled")
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

func publicDeviceRecord(record deviceRecord) deviceRecord {
	record.PublicKey = ""
	record.LastBootNonce = 0
	record.LastCounter = 0
	record.LastEventProof = ""
	record.LastEventStatus = 0
	record.LastEventResponse = nil
	return record
}

func (m *enrollmentManager) list() []deviceRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]deviceRecord, len(m.devices))
	for index, record := range m.devices {
		result[index] = publicDeviceRecord(record)
	}
	return result
}
