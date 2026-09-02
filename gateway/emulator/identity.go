package emulator

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
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
)

// IdentityManager manages cryptographic keys, hardware identity, and the
// replay cursor (boot nonce and counter) for the emulator.
type IdentityManager struct {
	mu       sync.Mutex
	path     string
	identity Identity
}

// NewIdentityManager creates an IdentityManager. If a state file exists at
// path, it loads the saved identity and increments the boot nonce. If not, it
// generates a fresh P-256 keypair with bootNonce=1 and counter=0.
func NewIdentityManager(stateDir, hardwareID string) (*IdentityManager, error) {
	if hardwareID == "" {
		hardwareID = "02:00:00:00:00:02" // default emulator MAC (locally administered unicast)
	}
	mac, err := net.ParseMAC(hardwareID)
	if err != nil || len(mac) != 6 || mac[0]&1 != 0 {
		return nil, fmt.Errorf("invalid emulator hardware ID: %s", hardwareID)
	}

	manager := &IdentityManager{
		path: filepath.Join(stateDir, "emulator-identity.json"),
	}

	if stateDir != "" {
		if err := os.MkdirAll(stateDir, 0700); err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(manager.path)
		if err == nil {
			var saved struct {
				HardwareID           string `json:"hardwareId"`
				PrivateKeyPEM        string `json:"privateKeyPem"`
				PublicKeyPEM         string `json:"publicKeyPem"`
				PublicKeyFingerprint string `json:"publicKeyFingerprint"`
				BootNonce            uint32 `json:"bootNonce"`
			}
			if err := json.Unmarshal(raw, &saved); err == nil {
				key, err := parseP256PrivateKey(saved.PrivateKeyPEM)
				if err == nil {
					manager.identity = Identity{
						HardwareID:           saved.HardwareID,
						PublicKeyPEM:         saved.PublicKeyPEM,
						PublicKeyFingerprint: saved.PublicKeyFingerprint,
						PrivateKey:           key,
						BootNonce:            saved.BootNonce + 1, // New boot sequence
						Counter:              0,                   // Reset per-boot counter
					}
					_ = manager.persist()
					return manager, nil
				}
			}
		}
	}

	// Generate new P-256 identity
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate P-256 key: %w", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("marshal public key: %w", err)
	}
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	digest := sha256.Sum256([]byte(publicPEM))
	fingerprint := hex.EncodeToString(digest[:])

	manager.identity = Identity{
		HardwareID:           strings.ToLower(hardwareID),
		PublicKeyPEM:         publicPEM,
		PublicKeyFingerprint: fingerprint,
		PrivateKey:           key,
		BootNonce:            1,
		Counter:              0,
	}

	if stateDir != "" {
		_ = manager.persist()
	}

	return manager, nil
}

// NextCounter increments and returns the next monotonic counter value.
func (m *IdentityManager) NextCounter() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.identity.Counter++
	return m.identity.Counter
}

// GetIdentity returns a snapshot of the current identity and cursor.
func (m *IdentityManager) GetIdentity() Identity {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.identity
}

// ResetBoot increments the boot nonce and resets the event counter, simulating
// a device reboot.
func (m *IdentityManager) ResetBoot() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.identity.BootNonce++
	m.identity.Counter = 0
	_ = managerPersist(m.path, m.identity)
}

func (m *IdentityManager) persist() error {
	return managerPersist(m.path, m.identity)
}

func managerPersist(path string, id Identity) error {
	if path == "" {
		return nil
	}
	der, err := x509.MarshalECPrivateKey(id.PrivateKey)
	if err != nil {
		return err
	}
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}))
	data, err := json.MarshalIndent(map[string]any{
		"hardwareId":           id.HardwareID,
		"privateKeyPem":        privPEM,
		"publicKeyPem":         id.PublicKeyPEM,
		"publicKeyFingerprint": id.PublicKeyFingerprint,
		"bootNonce":            id.BootNonce,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func parseP256PrivateKey(pemStr string) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("invalid private key PEM")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	if key.Curve != elliptic.P256() {
		return nil, errors.New("private key must be P-256")
	}
	return key, nil
}
