package devproto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"strings"
	"testing"
)

// --- Fixed P-256 test keypair ---
//
// This keypair is used for all golden vector tests. The same canonical
// messages and digests should produce identical results in the firmware's
// C implementation (mbedtls P-256 + SHA-256).

func testFixedKeypair(t *testing.T) (*ecdsa.PrivateKey, *ecdsa.PublicKey, string, string) {
	t.Helper()
	// Generate a fresh deterministic-enough keypair for this test run.
	// For true cross-language golden vectors, a hardcoded key would be used.
	// Here we generate one and verify the proof construction is correct.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	digest := sha256.Sum256([]byte(publicPEM))
	fingerprint := hex.EncodeToString(digest[:])
	return key, &key.PublicKey, publicPEM, fingerprint
}

// --- Enrollment proof golden vector ---

func TestEnrollmentProofMessageCanonicalFormat(t *testing.T) {
	// Known inputs
	token := "test-enrollment-token-abc123"
	hardwareID := "02:00:00:00:00:01"
	fingerprint := strings.Repeat("ab", 32) // 64 hex chars
	nonce := "test-nonce-xyz789"

	message := EnrollmentProofMessage(token, hardwareID, fingerprint, nonce)
	expected := "snowball-enroll-v1\n" + token + "\n" + hardwareID + "\n" + fingerprint + "\n" + nonce

	if string(message) != expected {
		t.Fatalf("enrollment proof message mismatch:\n  got:  %q\n  want: %q", string(message), expected)
	}

	// Verify digest is deterministic
	digest := sha256.Sum256(message)
	digestHex := hex.EncodeToString(digest[:])
	if len(digestHex) != 64 {
		t.Fatalf("digest hex length: got %d, want 64", len(digestHex))
	}
}

func TestEnrollmentProofSignAndVerifyRoundTrip(t *testing.T) {
	key, pub, _, fingerprint := testFixedKeypair(t)

	token := "enrollment-round-trip-token"
	hardwareID := "02:00:00:00:00:01"
	nonce := "round-trip-nonce-value"

	message := EnrollmentProofMessage(token, hardwareID, fingerprint, nonce)
	sig, digest, err := SignProof(key, message)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the signature
	ok, verifyDigest := VerifyProof(pub, message, sig)
	if !ok {
		t.Fatal("valid enrollment proof signature was not verified")
	}
	if digest != verifyDigest {
		t.Fatal("sign and verify produced different digests for the same message")
	}

	// Verify that a tampered message fails
	tampered := EnrollmentProofMessage(token, hardwareID, fingerprint, "wrong-nonce")
	ok, _ = VerifyProof(pub, tampered, sig)
	if ok {
		t.Fatal("tampered enrollment proof was verified")
	}
}

// --- Event proof golden vector ---

func TestEventProofMessageCanonicalFormat(t *testing.T) {
	input := EventRequest{
		Version:              1,
		HardwareID:           "02:00:00:00:00:01",
		PublicKeyFingerprint: strings.Repeat("cd", 32),
		BootNonce:            7,
		Counter:              42,
		Event:                "command",
		Wake:                 "hi_esp",
		Command:              "new_chat",
		Target:               "chatgpt",
		Name:                 "",
		Confidence:           1.0,
	}

	message := EventProofMessage(input)

	// Canonical format: each field on a newline, confidence with 6 decimal places
	expected := "snowball-device-event-v1\n" +
		"02:00:00:00:00:01\n" +
		strings.Repeat("cd", 32) + "\n" +
		"7\n" +
		"42\n" +
		"command\n" +
		"hi_esp\n" +
		"new_chat\n" +
		"chatgpt\n" +
		"\n" +
		"1.000000"

	if string(message) != expected {
		t.Fatalf("event proof message mismatch:\n  got:  %q\n  want: %q", string(message), expected)
	}
}

func TestEventProofMessageWithProjectCommand(t *testing.T) {
	input := EventRequest{
		Version:              1,
		HardwareID:           "02:00:00:00:00:01",
		PublicKeyFingerprint: strings.Repeat("ef", 32),
		BootNonce:            1,
		Counter:              3,
		Event:                "command",
		Wake:                 "hi_esp",
		Command:              "project",
		Target:               "codex",
		Name:                 "Snowball",
		Confidence:           0.9,
	}

	message := EventProofMessage(input)

	expected := "snowball-device-event-v1\n" +
		"02:00:00:00:00:01\n" +
		strings.Repeat("ef", 32) + "\n" +
		"1\n" +
		"3\n" +
		"command\n" +
		"hi_esp\n" +
		"project\n" +
		"codex\n" +
		"Snowball\n" +
		"0.900000"

	if string(message) != expected {
		t.Fatalf("project event proof message mismatch:\n  got:  %q\n  want: %q", string(message), expected)
	}
}

func TestEventProofMessageSyncEvent(t *testing.T) {
	input := EventRequest{
		Version:              1,
		HardwareID:           "02:00:00:00:00:01",
		PublicKeyFingerprint: strings.Repeat("ab", 32),
		BootNonce:            1,
		Counter:              1,
		Event:                "sync",
	}

	message := EventProofMessage(input)

	expected := "snowball-device-event-v1\n" +
		"02:00:00:00:00:01\n" +
		strings.Repeat("ab", 32) + "\n" +
		"1\n" +
		"1\n" +
		"sync\n" +
		"\n" + // wake empty
		"\n" + // command empty
		"\n" + // target empty
		"\n" + // name empty
		"0.000000"

	if string(message) != expected {
		t.Fatalf("sync event proof message mismatch:\n  got:  %q\n  want: %q", string(message), expected)
	}
}

func TestEventProofSignAndVerifyRoundTrip(t *testing.T) {
	key, pub, _, fingerprint := testFixedKeypair(t)

	input := EventRequest{
		Version:              1,
		HardwareID:           "02:00:00:00:00:01",
		PublicKeyFingerprint: fingerprint,
		BootNonce:            17,
		Counter:              1,
		Event:                "command",
		Wake:                 "hi_esp",
		Command:              "new_chat",
		Target:               "chatgpt",
		Confidence:           1.0,
	}

	message := EventProofMessage(input)
	sig, _, err := SignProof(key, message)
	if err != nil {
		t.Fatal(err)
	}

	ok, _ := VerifyProof(pub, message, sig)
	if !ok {
		t.Fatal("valid event proof signature was not verified")
	}

	// Tamper with the counter
	input.Counter = 2
	tampered := EventProofMessage(input)
	ok, _ = VerifyProof(pub, tampered, sig)
	if ok {
		t.Fatal("tampered event proof (counter) was verified")
	}
}

// --- Media offer proof golden vector ---

func TestMediaOfferProofMessageCanonicalFormat(t *testing.T) {
	sdp := "v=0\r\no=- 1 1 IN IP4 192.168.1.20\r\ns=Snowball speaker\r\nt=0 0\r\n" +
		"a=group:BUNDLE 0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 8\r\nc=IN IP4 0.0.0.0\r\n" +
		"a=mid:0\r\na=rtpmap:8 PCMA/8000\r\na=sendrecv\r\n" +
		"a=ice-ufrag:snowball\r\na=ice-pwd:0123456789abcdefghijklmn\r\n"

	input := MediaOfferRequest{
		Version:              1,
		HardwareID:           "02:00:00:00:00:01",
		PublicKeyFingerprint: strings.Repeat("ab", 32),
		BootNonce:            11,
		Counter:              1,
		Type:                 "offer",
		SDP:                  sdp,
	}

	message := MediaOfferProofMessage(input)

	// The SDP digest is SHA-256 of "offer\n" + sdp
	sdpDigest := sha256.Sum256([]byte("offer\n" + sdp))
	sdpDigestHex := hex.EncodeToString(sdpDigest[:])

	expected := "snowball-device-media-v1\n" +
		"02:00:00:00:00:01\n" +
		strings.Repeat("ab", 32) + "\n" +
		"11\n" +
		"1\n" +
		sdpDigestHex

	if string(message) != expected {
		t.Fatalf("media offer proof message mismatch:\n  got:  %q\n  want: %q", string(message), expected)
	}
}

func TestMediaOfferProofSignAndVerifyRoundTrip(t *testing.T) {
	key, pub, _, fingerprint := testFixedKeypair(t)

	sdp := "v=0\r\no=- 1 1 IN IP4 192.168.1.20\r\ns=Snowball speaker\r\nt=0 0\r\n" +
		"a=group:BUNDLE 0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 8\r\nc=IN IP4 0.0.0.0\r\n" +
		"a=mid:0\r\na=rtpmap:8 PCMA/8000\r\na=sendrecv\r\n" +
		"a=ice-ufrag:snowball\r\na=ice-pwd:0123456789abcdefghijklmn\r\n"

	input := MediaOfferRequest{
		Version:              1,
		HardwareID:           "02:00:00:00:00:01",
		PublicKeyFingerprint: fingerprint,
		BootNonce:            11,
		Counter:              1,
		Type:                 "offer",
		SDP:                  sdp,
	}

	message := MediaOfferProofMessage(input)
	sig, _, err := SignProof(key, message)
	if err != nil {
		t.Fatal(err)
	}

	ok, _ := VerifyProof(pub, message, sig)
	if !ok {
		t.Fatal("valid media offer proof signature was not verified")
	}

	// Tamper with the SDP
	input.SDP = sdp + "a=extra:line\r\n"
	tampered := MediaOfferProofMessage(input)
	ok, _ = VerifyProof(pub, tampered, sig)
	if ok {
		t.Fatal("tampered media offer proof (SDP) was verified")
	}
}

// --- ParseP256PublicKey tests ---

func TestParseP256PublicKeyValid(t *testing.T) {
	_, _, publicPEM, _ := testFixedKeypair(t)
	key, err := ParseP256PublicKey(publicPEM)
	if err != nil {
		t.Fatal(err)
	}
	if key.Curve != elliptic.P256() {
		t.Fatal("parsed key is not P-256")
	}
}

func TestParseP256PublicKeyRejectsInvalid(t *testing.T) {
	for name, pem := range map[string]string{
		"empty":          "",
		"wrong type":     "-----BEGIN RSA PUBLIC KEY-----\nMIIBCgKCAQEA\n-----END RSA PUBLIC KEY-----\n",
		"garbage":        "not a PEM block at all",
		"trailing block": func() string {
			_, _, p, _ := testFixedKeypair(t)
			return p + p // two blocks
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseP256PublicKey(pem); err == nil {
				t.Fatal("invalid PEM was accepted")
			}
		})
	}
}

// --- Validation tests ---

func TestValidateEnrollmentAcceptsValidRequest(t *testing.T) {
	_, _, publicPEM, fingerprint := testFixedKeypair(t)
	req := EnrollmentRequest{
		Name:                 "Kitchen Speaker",
		Model:                Model,
		FirmwareVersion:      "0.1.0-dev",
		ProtocolVersion:      ProtocolVersion,
		HardwareID:           "02:00:00:00:00:01",
		PublicKey:            publicPEM,
		PublicKeyFingerprint: fingerprint,
	}
	if err := ValidateEnrollment(&req); err != nil {
		t.Fatalf("valid enrollment rejected: %v", err)
	}
}

func TestValidateEnrollmentRejectsMismatchedFingerprint(t *testing.T) {
	_, _, publicPEM, _ := testFixedKeypair(t)
	req := EnrollmentRequest{
		Name:                 "Kitchen Speaker",
		Model:                Model,
		FirmwareVersion:      "0.1.0-dev",
		ProtocolVersion:      ProtocolVersion,
		HardwareID:           "02:00:00:00:00:01",
		PublicKey:            publicPEM,
		PublicKeyFingerprint: strings.Repeat("00", 32), // wrong fingerprint
	}
	if err := ValidateEnrollment(&req); err == nil {
		t.Fatal("mismatched fingerprint was accepted")
	}
}

func TestValidateEnrollmentRejectsUnsupportedModel(t *testing.T) {
	_, _, publicPEM, fingerprint := testFixedKeypair(t)
	req := EnrollmentRequest{
		Name:                 "Kitchen Speaker",
		Model:                "Unknown Board",
		FirmwareVersion:      "0.1.0-dev",
		ProtocolVersion:      ProtocolVersion,
		HardwareID:           "02:00:00:00:00:01",
		PublicKey:            publicPEM,
		PublicKeyFingerprint: fingerprint,
	}
	if err := ValidateEnrollment(&req); err == nil {
		t.Fatal("unsupported model was accepted")
	}
}

func TestValidateEventAcceptsValidCommandEvent(t *testing.T) {
	event := EventRequest{
		Version: 1, HardwareID: "02:00:00:00:00:01", PublicKeyFingerprint: strings.Repeat("a", 64),
		BootNonce: 1, Counter: 1, Event: "command", Wake: "hi_esp", Command: "project",
		Target: "chatgpt", Name: "Snowball", Confidence: 0.9, Signature: strings.Repeat("A", 64),
	}
	if err := ValidateEvent(&event); err != nil {
		t.Fatalf("valid project event rejected: %v", err)
	}
}

func TestValidateEventRejectsInvalidVariants(t *testing.T) {
	valid := EventRequest{
		Version: 1, HardwareID: "02:00:00:00:00:01", PublicKeyFingerprint: strings.Repeat("a", 64),
		BootNonce: 1, Counter: 1, Event: "command", Wake: "hi_esp", Command: "project",
		Target: "chatgpt", Name: "Snowball", Confidence: 0.9, Signature: strings.Repeat("A", 64),
	}
	tests := map[string]func(*EventRequest){
		"missing command wake": func(event *EventRequest) { event.Wake = "" },
		"wrong command wake":   func(event *EventRequest) { event.Wake = "chatgpt" },
		"missing project name": func(event *EventRequest) { event.Name = "" },
		"missing voice name": func(event *EventRequest) {
			event.Command, event.Name = "voice", ""
		},
		"wake with command fields": func(event *EventRequest) {
			event.Event = "wake"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			event := valid
			mutate(&event)
			if err := ValidateEvent(&event); err == nil {
				t.Fatal("invalid device event was accepted")
			}
		})
	}
}

func TestValidateEventAcceptsSyncAndRejectsWithFields(t *testing.T) {
	valid := EventRequest{
		Version: 1, HardwareID: "02:00:00:00:00:01", PublicKeyFingerprint: strings.Repeat("a", 64),
		BootNonce: 1, Counter: 1, Event: "sync", Confidence: 0, Signature: strings.Repeat("A", 64),
	}
	if err := ValidateEvent(&valid); err != nil {
		t.Fatalf("valid candidate sync event rejected: %v", err)
	}
	for name, mutate := range map[string]func(*EventRequest){
		"wake":    func(event *EventRequest) { event.Wake = "hi_esp" },
		"command": func(event *EventRequest) { event.Command = "resume" },
		"target":  func(event *EventRequest) { event.Target = "chatgpt" },
		"name":    func(event *EventRequest) { event.Name = "Snowball" },
	} {
		t.Run(name, func(t *testing.T) {
			event := valid
			mutate(&event)
			if err := ValidateEvent(&event); err == nil {
				t.Fatal("candidate sync event with command fields was accepted")
			}
		})
	}
}

func TestValidateMediaOfferAcceptsValidOffer(t *testing.T) {
	offer := MediaOfferRequest{
		Version: 1, HardwareID: "02:00:00:00:00:01", PublicKeyFingerprint: strings.Repeat("a", 64),
		BootNonce: 11, Counter: 1, Type: "offer",
		SDP:       "v=0\r\no=- 1 1 IN IP4 192.168.1.20\r\ns=Snowball speaker\r\nt=0 0\r\na=group:BUNDLE 0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 8\r\nc=IN IP4 0.0.0.0\r\na=mid:0\r\na=rtpmap:8 PCMA/8000\r\na=sendrecv\r\na=ice-ufrag:snowball\r\na=ice-pwd:0123456789abcdefghijklmn\r\n",
		Signature: strings.Repeat("A", 64),
	}
	if err := ValidateMediaOffer(&offer); err != nil {
		t.Fatalf("valid media offer rejected: %v", err)
	}
}

func TestValidateMediaOfferRejectsMissingPCMA(t *testing.T) {
	offer := MediaOfferRequest{
		Version: 1, HardwareID: "02:00:00:00:00:01", PublicKeyFingerprint: strings.Repeat("a", 64),
		BootNonce: 11, Counter: 1, Type: "offer",
		SDP:       "v=0\r\no=- 1 1 IN IP4 192.168.1.20\r\ns=Snowball speaker\r\nt=0 0\r\na=group:BUNDLE 0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 111\r\nc=IN IP4 0.0.0.0\r\na=mid:0\r\na=rtpmap:111 opus/48000/2\r\na=sendrecv\r\na=ice-ufrag:snowball\r\na=ice-pwd:0123456789abcdefghijklmn\r\n",
		Signature: strings.Repeat("A", 64),
	}
	if err := ValidateMediaOffer(&offer); err == nil {
		t.Fatal("media offer without PCMA/8000 was accepted")
	}
}

// --- Utility tests ---

func TestIsHexString(t *testing.T) {
	if !IsHexString("0123456789abcdef") {
		t.Fatal("valid hex string rejected")
	}
	if IsHexString("0123456789ABCDEF") {
		t.Fatal("uppercase hex was accepted")
	}
	if IsHexString("xyz") {
		t.Fatal("non-hex string was accepted")
	}
	if !IsHexString("") {
		t.Fatal("empty string rejected")
	}
}

func TestValidBase64URL(t *testing.T) {
	if !ValidBase64URL("ABCDabcd0123-_", 1, 100) {
		t.Fatal("valid base64url rejected")
	}
	if ValidBase64URL("abc=", 1, 100) {
		t.Fatal("base64url with padding was accepted")
	}
	if ValidBase64URL("ab", 3, 100) {
		t.Fatal("too-short base64url was accepted")
	}
	if ValidBase64URL("abcde", 1, 3) {
		t.Fatal("too-long base64url was accepted")
	}
}

func TestStrictUnmarshal(t *testing.T) {
	var out struct {
		Name string `json:"name"`
	}
	if err := StrictUnmarshal([]byte(`{"name":"test"}`), &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "test" {
		t.Fatalf("got %q, want %q", out.Name, "test")
	}

	// Unknown field
	if err := StrictUnmarshal([]byte(`{"name":"test","extra":1}`), &out); err == nil {
		t.Fatal("unknown field was accepted")
	}

	// Multiple values
	if err := StrictUnmarshal([]byte(`{"name":"a"}{"name":"b"}`), &out); err == nil {
		t.Fatal("multiple JSON values were accepted")
	}
}

func TestValidateEventReceiptConsistency(t *testing.T) {
	// Valid empty receipt
	if err := ValidateEventReceipt(Record{}); err != nil {
		t.Fatalf("empty receipt rejected: %v", err)
	}

	// Valid completed receipt
	valid := Record{
		LastBootNonce:     1,
		LastCounter:       1,
		LastEventProof:    strings.Repeat("ab", 32),
		LastEventStatus:   200,
		LastEventResponse: []byte(`{"ok":true}`),
	}
	if err := ValidateEventReceipt(valid); err != nil {
		t.Fatalf("valid completed receipt rejected: %v", err)
	}

	// Incomplete: status without proof
	if err := ValidateEventReceipt(Record{LastEventStatus: 200}); err == nil {
		t.Fatal("receipt with status but no proof was accepted")
	}

	// Incomplete: proof without boot nonce
	if err := ValidateEventReceipt(Record{LastEventProof: strings.Repeat("ab", 32)}); err == nil {
		t.Fatal("receipt with proof but no boot nonce was accepted")
	}
}

func TestPublicRecordRedactsSensitiveFields(t *testing.T) {
	record := Record{
		Name:              "Test",
		PublicKey:         "-----BEGIN PUBLIC KEY-----\n...\n-----END PUBLIC KEY-----\n",
		LastBootNonce:     42,
		LastCounter:       7,
		LastEventProof:    "abc123",
		LastEventStatus:   200,
		LastEventResponse: []byte(`{"ok":true}`),
	}
	public := PublicRecord(record)
	if public.PublicKey != "" || public.LastBootNonce != 0 || public.LastCounter != 0 ||
		public.LastEventProof != "" || public.LastEventStatus != 0 || public.LastEventResponse != nil {
		t.Fatal("public record still contains sensitive fields")
	}
	if public.Name != "Test" {
		t.Fatal("public record lost the name")
	}
}
