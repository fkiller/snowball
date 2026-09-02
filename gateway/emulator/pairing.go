package emulator

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"snowball.local/voice-gateway/devproto"
)

// RequestEnrollment calls POST /api/devices/enrollment (protected by Admin session)
// to issue enrollment material for the device.
func RequestEnrollment(
	client *http.Client,
	gatewayURL string,
	adminCookie string,
	csrfToken string,
	deviceName string,
	identity Identity,
) (*devproto.EnrollmentMaterial, error) {
	if deviceName == "" {
		deviceName = "Emulated Speaker"
	}
	reqBody := devproto.EnrollmentRequest{
		Name:                 deviceName,
		Model:                devproto.Model,
		FirmwareVersion:      "0.1.0-emulator",
		ProtocolVersion:      devproto.ProtocolVersion,
		HardwareID:           identity.HardwareID,
		PublicKey:            identity.PublicKeyPEM,
		PublicKeyFingerprint: identity.PublicKeyFingerprint,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	url := strings.TrimRight(gatewayURL, "/") + "/api/devices/enrollment"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if adminCookie != "" {
		req.Header.Set("Cookie", adminCookie)
	}
	if csrfToken != "" {
		req.Header.Set("X-CSRF-Token", csrfToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request enrollment HTTP error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("enrollment issue failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var material devproto.EnrollmentMaterial
	if err := json.NewDecoder(resp.Body).Decode(&material); err != nil {
		return nil, fmt.Errorf("decode enrollment material: %w", err)
	}
	return &material, nil
}

// CompleteEnrollment signs and submits the cryptographic enrollment proof to
// POST /api/auth/device-enroll.
func CompleteEnrollment(
	client *http.Client,
	gatewayURL string,
	identity Identity,
	enrollmentToken string,
) error {
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return err
	}
	nonce := hex.EncodeToString(nonceBytes)

	proofMsg := devproto.EnrollmentProofMessage(
		enrollmentToken,
		identity.HardwareID,
		identity.PublicKeyFingerprint,
		nonce,
	)

	sig, _, err := devproto.SignProof(identity.PrivateKey, proofMsg)
	if err != nil {
		return fmt.Errorf("sign enrollment proof: %w", err)
	}

	reqBody := devproto.EnrollmentProof{
		EnrollmentToken:      enrollmentToken,
		HardwareID:           identity.HardwareID,
		PublicKeyFingerprint: identity.PublicKeyFingerprint,
		Nonce:                nonce,
		Signature:            sig,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	url := strings.TrimRight(gatewayURL, "/") + "/api/auth/device-enroll"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("submit enrollment proof HTTP error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("enrollment completion failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Enrolled bool `json:"enrolled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode enrollment completion response: %w", err)
	}
	if !result.Enrolled {
		return fmt.Errorf("device enrollment response reported enrolled=false")
	}
	return nil
}

// PairDevice executes the complete 2-step enrollment flow.
func PairDevice(
	client *http.Client,
	gatewayURL string,
	adminCookie string,
	csrfToken string,
	deviceName string,
	identity Identity,
) (*devproto.EnrollmentMaterial, error) {
	material, err := RequestEnrollment(client, gatewayURL, adminCookie, csrfToken, deviceName, identity)
	if err != nil {
		return nil, fmt.Errorf("pair step 1 (request): %w", err)
	}

	if err := CompleteEnrollment(client, gatewayURL, identity, material.EnrollmentToken); err != nil {
		return nil, fmt.Errorf("pair step 2 (complete): %w", err)
	}

	return material, nil
}
