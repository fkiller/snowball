package devproto

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
)

// StrictUnmarshal decodes JSON with unknown-field rejection and single-value
// enforcement. This is the standard decoder for all device protocol messages.
func StrictUnmarshal(raw []byte, value any) error {
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

// ValidateEnrollment normalizes and validates a device enrollment request.
// The input fields are trimmed and lowercased in place.
func ValidateEnrollment(input *EnrollmentRequest) error {
	input.Name = strings.Join(strings.Fields(input.Name), " ")
	input.Model = strings.TrimSpace(input.Model)
	input.FirmwareVersion = strings.TrimSpace(input.FirmwareVersion)
	input.HardwareID = strings.ToLower(strings.TrimSpace(input.HardwareID))
	input.PublicKeyFingerprint = strings.ToLower(strings.TrimSpace(input.PublicKeyFingerprint))
	if input.Name == "" || len(input.Name) > 64 {
		return errors.New("device name must be between 1 and 64 characters")
	}
	if input.Model != Model || input.ProtocolVersion != ProtocolVersion || input.FirmwareVersion == "" || len(input.FirmwareVersion) > 64 {
		return errors.New("device model, firmware, or protocol is not supported")
	}
	mac, err := net.ParseMAC(input.HardwareID)
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 {
		return errors.New("device hardware identifier is invalid")
	}
	if len(input.PublicKey) < 100 || len(input.PublicKey) > 512 {
		return errors.New("device public key is invalid")
	}
	if _, err := ParseP256PublicKey(input.PublicKey); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(input.PublicKey))
	if len(input.PublicKeyFingerprint) != 64 || input.PublicKeyFingerprint != hex.EncodeToString(digest[:]) {
		return errors.New("device public key fingerprint does not match")
	}
	return nil
}

// ValidateEvent normalizes and validates a device event request. The input
// fields are trimmed and lowercased in place.
func ValidateEvent(input *EventRequest) error {
	input.HardwareID = strings.ToLower(strings.TrimSpace(input.HardwareID))
	input.PublicKeyFingerprint = strings.ToLower(strings.TrimSpace(input.PublicKeyFingerprint))
	input.Event = strings.TrimSpace(input.Event)
	input.Wake = strings.TrimSpace(input.Wake)
	input.Command = strings.TrimSpace(input.Command)
	input.Target = strings.ToLower(strings.TrimSpace(input.Target))
	input.Name = strings.Join(strings.Fields(input.Name), " ")
	input.Signature = strings.TrimSpace(input.Signature)
	if input.Version != ProtocolVersion || input.BootNonce == 0 || input.Counter == 0 {
		return errors.New("device event envelope is invalid")
	}
	if mac, err := net.ParseMAC(input.HardwareID); err != nil || len(mac) != 6 || len(input.PublicKeyFingerprint) != 64 ||
		!IsHexString(input.PublicKeyFingerprint) {
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
	if !ValidBase64URL(input.Signature, 64, 192) {
		return errors.New("device event signature is invalid")
	}
	return nil
}

// ValidateMediaOffer normalizes and validates a device media offer request.
// The input fields are trimmed and lowercased in place.
func ValidateMediaOffer(input *MediaOfferRequest) error {
	input.HardwareID = strings.ToLower(strings.TrimSpace(input.HardwareID))
	input.PublicKeyFingerprint = strings.ToLower(strings.TrimSpace(input.PublicKeyFingerprint))
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	input.Signature = strings.TrimSpace(input.Signature)
	if input.Version != ProtocolVersion || input.BootNonce == 0 || input.Counter == 0 || input.Type != "offer" {
		return errors.New("device media offer envelope is invalid")
	}
	if mac, err := net.ParseMAC(input.HardwareID); err != nil || len(mac) != 6 || len(input.PublicKeyFingerprint) != 64 ||
		!IsHexString(input.PublicKeyFingerprint) {
		return errors.New("device media offer identity is invalid")
	}
	if len(input.SDP) < 128 || len(input.SDP) > 64<<10 || !strings.HasPrefix(input.SDP, "v=0") ||
		strings.ContainsRune(input.SDP, '\x00') || !strings.Contains(strings.ToUpper(input.SDP), "PCMA/8000") {
		return errors.New("device media offer SDP is invalid")
	}
	if !ValidBase64URL(input.Signature, 64, 192) {
		return errors.New("device media offer signature is invalid")
	}
	return nil
}

// ValidateEventReceipt checks that a device record's persisted event receipt
// fields are internally consistent.
func ValidateEventReceipt(record Record) error {
	if record.LastEventProof == "" {
		if record.LastEventStatus != 0 || len(record.LastEventResponse) != 0 {
			return errors.New("device event receipt is incomplete")
		}
		return nil
	}
	if record.LastBootNonce == 0 || record.LastCounter == 0 || len(record.LastEventProof) != sha256.Size*2 ||
		!IsHexString(record.LastEventProof) {
		return errors.New("device event receipt proof is invalid")
	}
	if record.LastEventStatus == 0 {
		if len(record.LastEventResponse) != 0 {
			return errors.New("pending device event receipt contains a response")
		}
		return nil
	}
	if record.LastEventStatus < 200 || record.LastEventStatus > 599 ||
		len(record.LastEventResponse) == 0 || len(record.LastEventResponse) > MaxEventResponseBytes ||
		!json.Valid(record.LastEventResponse) {
		return errors.New("device event receipt result is invalid")
	}
	return nil
}

// IsHexString returns true if value contains only lowercase hexadecimal
// characters [0-9a-f].
func IsHexString(value string) bool {
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

// ValidBase64URL returns true if value has a length within [minimum, maximum]
// and contains only base64url characters [A-Za-z0-9_-].
func ValidBase64URL(value string, minimum, maximum int) bool {
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
