// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package libbp

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

var (
	ErrInvalidKey       = errors.New("invalid Ed25519 key")
	ErrInvalidSignature = errors.New("invalid signature format")
)

// GenerateKeypair creates a new random Ed25519 private/public keypair
func GenerateKeypair() (ed25519.PrivateKey, ed25519.PublicKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed generating Ed25519 keypair: %w", err)
	}
	return priv, pub, nil
}

// EncodePrivateKeyPEM converts an Ed25519 private key to PKCS#8 PEM string
func EncodePrivateKeyPEM(priv ed25519.PrivateKey) (string, error) {
	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", err
	}
	block := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: pkcs8Bytes,
	}
	return string(pem.EncodeToMemory(block)), nil
}

// DecodePrivateKeyPEM parses a PKCS#8 or raw PEM formatted Ed25519 private key
func DecodePrivateKeyPEM(pemStr string) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(pemStr)))
	if block == nil {
		return nil, fmt.Errorf("%w: failed decoding PEM block", ErrInvalidKey)
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}

	edKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%w: not an Ed25519 private key", ErrInvalidKey)
	}
	return edKey, nil
}

// LoadPrivateKeyFromFile reads and parses a PEM private key from a filesystem path
func LoadPrivateKeyFromFile(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return DecodePrivateKeyPEM(string(data))
}

// EncodePublicKeyBase64 returns the base64-encoded representation of the public key
func EncodePublicKeyBase64(pub ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pub)
}

// DecodePublicKeyBase64 parses a base64-encoded public key
func DecodePublicKeyBase64(b64 string) (ed25519.PublicKey, error) {
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("%w: invalid base64 encoding", ErrInvalidKey)
	}
	if len(data) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: invalid public key length %d (expected %d)", ErrInvalidKey, len(data), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(data), nil
}

// SignPayload signs a byte slice using the private key and returns standard base64 signature
func SignPayload(priv ed25519.PrivateKey, payload []byte) string {
	sig := ed25519.Sign(priv, payload)
	return base64.StdEncoding.EncodeToString(sig)
}

// VerifyPayload verifies the base64 signature against the payload using the public key
func VerifyPayload(pub ed25519.PublicKey, payload []byte, sigBase64 string) bool {
	if len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigBase64))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, payload, sig)
}
