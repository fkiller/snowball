package devproto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
)

// ParseP256PublicKey decodes a PEM-encoded SubjectPublicKeyInfo block and
// returns the P-256 public key. The PEM must contain exactly one block of
// type "PUBLIC KEY" with a P-256 key that is on-curve.
func ParseP256PublicKey(publicPEM string) (*ecdsa.PublicKey, error) {
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

// SignProof hashes the canonical message with SHA-256, signs it with the
// provided P-256 private key using ASN.1 DER encoding, and returns the
// base64url-encoded signature string (no padding) along with the digest.
func SignProof(key *ecdsa.PrivateKey, message []byte) (string, [sha256.Size]byte, error) {
	digest := sha256.Sum256(message)
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		return "", digest, err
	}
	return base64.RawURLEncoding.EncodeToString(signature), digest, nil
}

// VerifyProof decodes a base64url signature and verifies it against the
// SHA-256 hash of the canonical message using the provided P-256 public key.
// Returns whether the signature is valid and the computed digest.
func VerifyProof(key *ecdsa.PublicKey, message []byte, signatureB64 string) (bool, [sha256.Size]byte) {
	digest := sha256.Sum256(message)
	signature, err := base64.RawURLEncoding.DecodeString(signatureB64)
	if err != nil || len(signature) < 64 || len(signature) > 80 {
		return false, digest
	}
	return ecdsa.VerifyASN1(key, digest[:], signature), digest
}
