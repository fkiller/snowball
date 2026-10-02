// Package devproto defines the Snowball device protocol wire types, canonical
// proof message construction, P-256 signing/verification helpers, and input
// validation used by the Gateway and the device emulator.
//
// Every type, constant, and function in this package was extracted from the
// Gateway's main package to allow a future device emulator to share the same
// protocol implementation without handwriting a second canonical format.
package devproto

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// ProtocolVersion is the current Snowball device protocol version.
const ProtocolVersion = 1

// Model is the only supported device model string.
const Model = "Waveshare ESP32-S3-AUDIO-Board"

// MaxEventResponseBytes bounds the persisted terminal HTTP result body.
const MaxEventResponseBytes = 32 << 10

// EnrollmentRequest is submitted by the Admin UI to begin device pairing.
type EnrollmentRequest struct {
	Name                 string `json:"name"`
	Model                string `json:"model"`
	FirmwareVersion      string `json:"firmwareVersion"`
	ProtocolVersion      int    `json:"protocolVersion"`
	HardwareID           string `json:"hardwareId"`
	PublicKey            string `json:"publicKey,omitempty"`
	PublicKeyFingerprint string `json:"publicKeyFingerprint"`
}

// Record is the persistent representation of an enrolled device.
type Record struct {
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

// EnrollmentMaterial is returned to the Admin UI after issuing a pairing
// request, containing the information the ESP32 needs to complete enrollment.
type EnrollmentMaterial struct {
	EnrollmentToken string `json:"enrollmentToken"`
	Gateway         string `json:"gateway"`
	GatewayHTTPPort int    `json:"gatewayHttpPort"`
	GatewayPort     int    `json:"gatewayPort"`
	CASHA256        string `json:"caSha256"`
	ExpiresIn       int    `json:"expiresInSeconds"`
}

// EnrollmentProof is sent by the ESP32 to complete enrollment with a
// cryptographic proof of private key possession.
type EnrollmentProof struct {
	EnrollmentToken      string `json:"enrollmentToken"`
	HardwareID           string `json:"hardwareId"`
	PublicKeyFingerprint string `json:"publicKeyFingerprint"`
	Nonce                string `json:"nonce"`
	Signature            string `json:"signature"`
}

// EventRequest is the authenticated control-plane message sent by a paired
// speaker. The signature covers every field that can affect command dispatch,
// so a LAN peer cannot rewrite a valid wake or command event.
type EventRequest struct {
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

// EventResult holds the HTTP status and body for a terminal device event
// response.
type EventResult struct {
	Status int
	Body   []byte
}

// EventAcceptance is the result of authenticating and accepting a device
// event through the enrollment manager.
type EventAcceptance struct {
	Record    Record
	Replay    bool
	Completed bool
	Result    EventResult
}

// MediaOfferRequest authenticates the one-shot WebRTC offer used by a paired
// speaker. The SDP itself is hashed into the signature so a LAN peer cannot
// substitute its own media endpoint after observing a valid request.
type MediaOfferRequest struct {
	Version              int    `json:"version"`
	HardwareID           string `json:"hardwareId"`
	PublicKeyFingerprint string `json:"publicKeyFingerprint"`
	BootNonce            uint32 `json:"bootNonce"`
	Counter              uint32 `json:"counter"`
	Type                 string `json:"type"`
	SDP                  string `json:"sdp"`
	Signature            string `json:"signature"`
}

// RegistryState is the top-level JSON structure for the persisted device
// registry file.
type RegistryState struct {
	Version int      `json:"version"`
	Devices []Record `json:"devices"`
}

// EnrollmentProofMessage constructs the canonical byte sequence that is hashed
// and signed for enrollment proof verification.
func EnrollmentProofMessage(token, hardwareID, fingerprint, nonce string) []byte {
	return []byte("snowball-enroll-v1\n" + token + "\n" + hardwareID + "\n" + fingerprint + "\n" + nonce)
}

// EventProofMessage constructs the canonical byte sequence that is hashed and
// signed for device event authentication.
func EventProofMessage(input EventRequest) []byte {
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

// MediaOfferProofMessage constructs the canonical byte sequence that is hashed
// and signed for device media offer authentication. The SDP content is included
// via its SHA-256 digest so that the signature covers the actual media
// description.
func MediaOfferProofMessage(input MediaOfferRequest) []byte {
	sdpDigest := sha256.Sum256([]byte(input.Type + "\n" + input.SDP))
	return []byte("snowball-device-media-v1\n" +
		strings.ToLower(strings.TrimSpace(input.HardwareID)) + "\n" +
		strings.ToLower(strings.TrimSpace(input.PublicKeyFingerprint)) + "\n" +
		strconv.FormatUint(uint64(input.BootNonce), 10) + "\n" +
		strconv.FormatUint(uint64(input.Counter), 10) + "\n" +
		hex.EncodeToString(sdpDigest[:]))
}

// PublicRecord returns a copy of the record with sensitive replay-cursor and
// key fields redacted for API consumers.
func PublicRecord(record Record) Record {
	record.PublicKey = ""
	record.LastBootNonce = 0
	record.LastCounter = 0
	record.LastEventProof = ""
	record.LastEventStatus = 0
	record.LastEventResponse = nil
	return record
}
