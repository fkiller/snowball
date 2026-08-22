package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testDeviceRequest(t *testing.T) deviceEnrollmentRequest {
	t.Helper()
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
	return deviceEnrollmentRequest{
		Name:                 "Kitchen Speaker",
		Model:                deviceModel,
		FirmwareVersion:      "0.1.0-dev",
		ProtocolVersion:      deviceProtocolVersion,
		HardwareID:           "02:00:00:00:00:01",
		PublicKey:            publicPEM,
		PublicKeyFingerprint: hex.EncodeToString(digest[:]),
	}
}

func testDeviceRequestAndKey(t *testing.T) (deviceEnrollmentRequest, *ecdsa.PrivateKey) {
	t.Helper()
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
	return deviceEnrollmentRequest{
		Name:                 "Kitchen Speaker",
		Model:                deviceModel,
		FirmwareVersion:      "0.1.0-dev",
		ProtocolVersion:      deviceProtocolVersion,
		HardwareID:           "02:00:00:00:00:01",
		PublicKey:            publicPEM,
		PublicKeyFingerprint: hex.EncodeToString(digest[:]),
	}, key
}

func writeTestCA(t *testing.T, directory string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Snowball Test CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "ca.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEnrollmentBindsTokenAndPersistsRedactedDevice(t *testing.T) {
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
	if len(material.EnrollmentToken) < 32 || material.Gateway != "192.168.1.1" || len(material.CASHA256) != 64 {
		t.Fatalf("invalid enrollment material: %#v", material)
	}
	raw, err := os.ReadFile(filepath.Join(directory, "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" || containsSensitiveDeviceValue(string(raw), material.EnrollmentToken, "PRIVATE KEY") {
		t.Fatal("device registry contains enrollment secrets or private key material")
	}
	info, err := os.Stat(filepath.Join(directory, "devices.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("device registry permissions are not private: %v", err)
	}
	nonce := "0123456789abcdefghijklmnopqrstuv"
	digest := sha256.Sum256(enrollmentProofMessage(material.EnrollmentToken, request.HardwareID, request.PublicKeyFingerprint, nonce))
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	record, err := manager.complete(deviceEnrollmentProof{
		EnrollmentToken:      material.EnrollmentToken,
		HardwareID:           request.HardwareID,
		PublicKeyFingerprint: request.PublicKeyFingerprint,
		Nonce:                nonce,
		Signature:            base64.RawURLEncoding.EncodeToString(signature),
	})
	if err != nil || record.State != "active" {
		t.Fatalf("valid device proof failed: %#v, %v", record, err)
	}
	if _, err := manager.complete(deviceEnrollmentProof{
		EnrollmentToken:      material.EnrollmentToken,
		HardwareID:           request.HardwareID,
		PublicKeyFingerprint: request.PublicKeyFingerprint,
		Nonce:                nonce,
		Signature:            base64.RawURLEncoding.EncodeToString(signature),
	}); err == nil {
		t.Fatal("one-time enrollment token was reused")
	}
}

func containsSensitiveDeviceValue(raw string, values ...string) bool {
	for _, value := range values {
		if value != "" && len(raw) >= len(value) {
			for index := 0; index+len(value) <= len(raw); index++ {
				if raw[index:index+len(value)] == value {
					return true
				}
			}
		}
	}
	return false
}

func signTestDeviceEvent(t *testing.T, event *deviceEventRequest, key *ecdsa.PrivateKey) {
	t.Helper()
	digest := sha256.Sum256(deviceEventProofMessage(*event))
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	event.Signature = base64.RawURLEncoding.EncodeToString(signature)
}

func activeDeviceManagerForReceipt(t *testing.T) (*enrollmentManager, deviceEnrollmentRequest, *ecdsa.PrivateKey, string) {
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
	digest := sha256.Sum256(enrollmentProofMessage(material.EnrollmentToken, request.HardwareID, request.PublicKeyFingerprint, nonce))
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.complete(deviceEnrollmentProof{
		EnrollmentToken: material.EnrollmentToken, HardwareID: request.HardwareID,
		PublicKeyFingerprint: request.PublicKeyFingerprint, Nonce: nonce,
		Signature: base64.RawURLEncoding.EncodeToString(signature),
	}); err != nil {
		t.Fatal(err)
	}
	return manager, request, key, directory
}

func TestEnrollmentRejectsMismatchedFingerprintAndUnsupportedBoard(t *testing.T) {
	request := testDeviceRequest(t)
	request.PublicKeyFingerprint = string(make([]byte, 64))
	if err := validateDeviceEnrollment(&request); err == nil {
		t.Fatal("mismatched public key fingerprint was accepted")
	}
	request = testDeviceRequest(t)
	request.Model = "Unknown Board"
	if err := validateDeviceEnrollment(&request); err == nil {
		t.Fatal("unsupported hardware was accepted")
	}
}

func TestDeviceEventValidationRequiresWakeAndCommandNames(t *testing.T) {
	valid := deviceEventRequest{
		Version: 1, HardwareID: "02:00:00:00:00:01", PublicKeyFingerprint: strings.Repeat("a", 64),
		BootNonce: 1, Counter: 1, Event: "command", Wake: "hi_esp", Command: "project",
		Target: "chatgpt", Name: "Snowball", Confidence: 0.9, Signature: strings.Repeat("A", 64),
	}
	if err := validateDeviceEvent(&valid); err != nil {
		t.Fatalf("valid project event rejected: %v", err)
	}
	tests := map[string]func(*deviceEventRequest){
		"missing command wake": func(event *deviceEventRequest) { event.Wake = "" },
		"wrong command wake":   func(event *deviceEventRequest) { event.Wake = "chatgpt" },
		"missing project name": func(event *deviceEventRequest) { event.Name = "" },
		"missing voice name": func(event *deviceEventRequest) {
			event.Command, event.Name = "voice", ""
		},
		"wake with command fields": func(event *deviceEventRequest) {
			event.Event = "wake"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			event := valid
			mutate(&event)
			if err := validateDeviceEvent(&event); err == nil {
				t.Fatal("invalid device event was accepted")
			}
		})
	}
}

func TestDeviceCandidateSyncValidation(t *testing.T) {
	valid := deviceEventRequest{
		Version: 1, HardwareID: "02:00:00:00:00:01", PublicKeyFingerprint: strings.Repeat("a", 64),
		BootNonce: 1, Counter: 1, Event: "sync", Confidence: 0, Signature: strings.Repeat("A", 64),
	}
	if err := validateDeviceEvent(&valid); err != nil {
		t.Fatalf("valid candidate sync event rejected: %v", err)
	}
	for name, mutate := range map[string]func(*deviceEventRequest){
		"wake":    func(event *deviceEventRequest) { event.Wake = "hi_esp" },
		"command": func(event *deviceEventRequest) { event.Command = "resume" },
		"target":  func(event *deviceEventRequest) { event.Target = "chatgpt" },
		"name":    func(event *deviceEventRequest) { event.Name = "Snowball" },
	} {
		t.Run(name, func(t *testing.T) {
			event := valid
			mutate(&event)
			if err := validateDeviceEvent(&event); err == nil {
				t.Fatal("candidate sync event with command fields was accepted")
			}
		})
	}
}

func TestDeviceEventResultIsIdempotentAndSurvivesRestart(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   []byte
	}{
		{name: "success", status: 200, body: []byte(`{"version":1,"accepted":true,"outcome":"executed"}`)},
		{name: "browser conflict", status: 409, body: []byte(`{"error":"browser not ready"}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, device, key, directory := activeDeviceManagerForReceipt(t)
			event := deviceEventRequest{
				Version: 1, HardwareID: device.HardwareID, PublicKeyFingerprint: device.PublicKeyFingerprint,
				BootNonce: 17, Counter: 1, Event: "command", Wake: "hi_esp", Command: "new_chat",
				Target: "chatgpt", Confidence: 1,
			}
			signTestDeviceEvent(t, &event, key)

			first, err := manager.beginEvent(event)
			if err != nil || first.Replay {
				t.Fatalf("new event was not accepted for dispatch: %#v, %v", first, err)
			}
			pending, err := manager.beginEvent(event)
			if err != nil || !pending.Replay || pending.Completed {
				t.Fatalf("in-flight retry was not recognized: %#v, %v", pending, err)
			}
			if err := manager.completeEvent(event, test.status, test.body); err != nil {
				t.Fatal(err)
			}
			if err := manager.completeEvent(event, test.status, test.body); err != nil {
				t.Fatalf("same terminal result was not idempotent: %v", err)
			}
			if err := manager.completeEvent(event, test.status, []byte(`{"error":"different"}`)); err == nil {
				t.Fatal("terminal event result was overwritten")
			}

			retry, err := manager.beginEvent(event)
			if err != nil || !retry.Replay || !retry.Completed || retry.Result.Status != test.status ||
				!bytes.Equal(retry.Result.Body, test.body) {
				t.Fatalf("terminal retry did not return the cached result: %#v, %v", retry, err)
			}

			reloaded, err := newEnrollmentManager(directory, filepath.Join(directory, "ca.crt"))
			if err != nil {
				t.Fatal(err)
			}
			retry, err = reloaded.beginEvent(event)
			if err != nil || !retry.Replay || !retry.Completed || retry.Result.Status != test.status ||
				!bytes.Equal(retry.Result.Body, test.body) {
				t.Fatalf("persisted event result was not replayed after restart: %#v, %v", retry, err)
			}
			public := reloaded.list()[0]
			if public.LastEventProof != "" || public.LastEventStatus != 0 || len(public.LastEventResponse) != 0 {
				t.Fatal("private event receipt was exposed by the device registry API")
			}

			conflicting := event
			conflicting.Command = "resume"
			signTestDeviceEvent(t, &conflicting, key)
			if _, err := reloaded.beginEvent(conflicting); err == nil {
				t.Fatal("different signed content reused an accepted event counter")
			}
		})
	}
}

func TestDeviceEventAuthenticationAndReplayCursor(t *testing.T) {
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
	digest := sha256.Sum256(enrollmentProofMessage(material.EnrollmentToken, request.HardwareID, request.PublicKeyFingerprint, nonce))
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.complete(deviceEnrollmentProof{
		EnrollmentToken: material.EnrollmentToken, HardwareID: request.HardwareID,
		PublicKeyFingerprint: request.PublicKeyFingerprint, Nonce: nonce,
		Signature: base64.RawURLEncoding.EncodeToString(signature),
	}); err != nil {
		t.Fatal(err)
	}
	event := deviceEventRequest{
		Version: 1, HardwareID: request.HardwareID, PublicKeyFingerprint: request.PublicKeyFingerprint,
		BootNonce: 7, Counter: 1, Event: "command", Wake: "hi_esp", Command: "new_chat",
		Target: "chatgpt", Confidence: 1,
	}
	eventDigest := sha256.Sum256(deviceEventProofMessage(event))
	eventSignature, err := ecdsa.SignASN1(rand.Reader, key, eventDigest[:])
	if err != nil {
		t.Fatal(err)
	}
	event.Signature = base64.RawURLEncoding.EncodeToString(eventSignature)
	if _, err := manager.acceptEvent(event); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	if _, err := manager.acceptEvent(event); err == nil {
		t.Fatal("replayed device event was accepted")
	}
	event.Counter = 2
	eventDigest = sha256.Sum256(deviceEventProofMessage(event))
	eventSignature, err = ecdsa.SignASN1(rand.Reader, key, eventDigest[:])
	if err != nil {
		t.Fatal(err)
	}
	event.Signature = base64.RawURLEncoding.EncodeToString(eventSignature)
	if _, err := manager.acceptEvent(event); err != nil {
		t.Fatalf("next device event rejected: %v", err)
	}
	// A new boot sequence may restart the per-boot counter, but an event from
	// the older boot must never become valid again afterwards.
	event.BootNonce = 8
	event.Counter = 1
	eventDigest = sha256.Sum256(deviceEventProofMessage(event))
	eventSignature, err = ecdsa.SignASN1(rand.Reader, key, eventDigest[:])
	if err != nil {
		t.Fatal(err)
	}
	event.Signature = base64.RawURLEncoding.EncodeToString(eventSignature)
	if _, err := manager.acceptEvent(event); err != nil {
		t.Fatalf("new boot event rejected: %v", err)
	}
	event.BootNonce = 7
	event.Counter = 3
	eventDigest = sha256.Sum256(deviceEventProofMessage(event))
	eventSignature, err = ecdsa.SignASN1(rand.Reader, key, eventDigest[:])
	if err != nil {
		t.Fatal(err)
	}
	event.Signature = base64.RawURLEncoding.EncodeToString(eventSignature)
	if _, err := manager.acceptEvent(event); err == nil {
		t.Fatal("event from an older boot sequence was accepted")
	}
}

func TestDeviceMediaOfferAuthenticationAndSharedReplayCursor(t *testing.T) {
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
	enrollmentDigest := sha256.Sum256(enrollmentProofMessage(material.EnrollmentToken, request.HardwareID, request.PublicKeyFingerprint, nonce))
	enrollmentSignature, err := ecdsa.SignASN1(rand.Reader, key, enrollmentDigest[:])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.complete(deviceEnrollmentProof{
		EnrollmentToken: material.EnrollmentToken, HardwareID: request.HardwareID,
		PublicKeyFingerprint: request.PublicKeyFingerprint, Nonce: nonce,
		Signature: base64.RawURLEncoding.EncodeToString(enrollmentSignature),
	}); err != nil {
		t.Fatal(err)
	}

	offer := deviceMediaOfferRequest{
		Version: 1, HardwareID: request.HardwareID, PublicKeyFingerprint: request.PublicKeyFingerprint,
		BootNonce: 11, Counter: 1, Type: "offer",
		SDP: "v=0\r\no=- 1 1 IN IP4 192.168.1.20\r\ns=Snowball speaker\r\nt=0 0\r\na=group:BUNDLE 0\r\nm=audio 9 UDP/TLS/RTP/SAVPF 8\r\nc=IN IP4 0.0.0.0\r\na=mid:0\r\na=rtpmap:8 PCMA/8000\r\na=sendrecv\r\na=ice-ufrag:snowball\r\na=ice-pwd:0123456789abcdefghijklmn\r\n",
	}
	offerDigest := sha256.Sum256(deviceMediaOfferProofMessage(offer))
	offerSignature, err := ecdsa.SignASN1(rand.Reader, key, offerDigest[:])
	if err != nil {
		t.Fatal(err)
	}
	offer.Signature = base64.RawURLEncoding.EncodeToString(offerSignature)
	if _, err := manager.acceptMediaOffer(offer); err != nil {
		t.Fatalf("valid media offer rejected: %v", err)
	}
	if !manager.isActive(request.PublicKeyFingerprint) {
		t.Fatal("active device was not reported active")
	}
	if _, err := manager.acceptMediaOffer(offer); err == nil {
		t.Fatal("replayed media offer was accepted")
	}

	// Event and media signaling share one monotonically increasing cursor.
	event := deviceEventRequest{
		Version: 1, HardwareID: request.HardwareID, PublicKeyFingerprint: request.PublicKeyFingerprint,
		BootNonce: 11, Counter: 2, Event: "command", Wake: "hi_esp", Command: "start_voice",
		Target: "chatgpt", Confidence: 1,
	}
	eventDigest := sha256.Sum256(deviceEventProofMessage(event))
	eventSignature, err := ecdsa.SignASN1(rand.Reader, key, eventDigest[:])
	if err != nil {
		t.Fatal(err)
	}
	event.Signature = base64.RawURLEncoding.EncodeToString(eventSignature)
	if _, err := manager.acceptEvent(event); err != nil {
		t.Fatalf("event after media offer rejected: %v", err)
	}

	offer.Counter = 1
	if _, err := manager.acceptMediaOffer(offer); err == nil {
		t.Fatal("media offer older than the event cursor was accepted")
	}
}
