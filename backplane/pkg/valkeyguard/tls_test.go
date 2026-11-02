// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package valkeyguard

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// generateTestCertificate generates a test certificate signed by parent (or self-signed if parent is nil).
func generateTestCertificate(
	t *testing.T,
	isCA bool,
	commonName string,
	ipAddresses []net.IP,
	parent *x509.Certificate,
	parentKey *ecdsa.PrivateKey,
) (*x509.Certificate, *ecdsa.PrivateKey, []byte, []byte) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("failed to generate serial number: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"Boggy Creek Software LLC"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	if isCA {
		template.IsCA = true
		template.KeyUsage |= x509.KeyUsageCertSign
	}

	if len(ipAddresses) > 0 {
		template.IPAddresses = ipAddresses
	}

	signParent := template
	signKey := priv
	if parent != nil && parentKey != nil {
		signParent = parent
		signKey = parentKey
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, signParent, &priv.PublicKey, signKey)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	cert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	return cert, priv, certPEM, keyPEM
}

func TestLoadServerTLSConfig(t *testing.T) {
	tempDir := t.TempDir()

	// Generate root CA
	caCert, caKey, caCertPEM, _ := generateTestCertificate(t, true, "Test CA", nil, nil, nil)
	caFile := filepath.Join(tempDir, "ca.crt")
	if err := os.WriteFile(caFile, caCertPEM, 0600); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}

	// Generate server certificate signed by CA
	_, _, serverCertPEM, serverKeyPEM := generateTestCertificate(
		t,
		false,
		"localhost",
		[]net.IP{net.ParseIP("127.0.0.1")},
		caCert,
		caKey,
	)
	serverCertFile := filepath.Join(tempDir, "server.crt")
	serverKeyFile := filepath.Join(tempDir, "server.key")
	if err := os.WriteFile(serverCertFile, serverCertPEM, 0600); err != nil {
		t.Fatalf("failed to write server cert file: %v", err)
	}
	if err := os.WriteFile(serverKeyFile, serverKeyPEM, 0600); err != nil {
		t.Fatalf("failed to write server key file: %v", err)
	}

	t.Run("Empty certFile", func(t *testing.T) {
		cfg, err := LoadServerTLSConfig("", serverKeyFile, "")
		if err == nil || cfg != nil {
			t.Fatalf("expected error for empty certFile, got cfg: %v, err: %v", cfg, err)
		}
	})

	t.Run("Empty keyFile", func(t *testing.T) {
		cfg, err := LoadServerTLSConfig(serverCertFile, "", "")
		if err == nil || cfg != nil {
			t.Fatalf("expected error for empty keyFile, got cfg: %v, err: %v", cfg, err)
		}
	})

	t.Run("Non-existent certFile", func(t *testing.T) {
		cfg, err := LoadServerTLSConfig(filepath.Join(tempDir, "missing.crt"), serverKeyFile, "")
		if err == nil || cfg != nil {
			t.Fatalf("expected error for missing certFile, got cfg: %v, err: %v", cfg, err)
		}
	})

	t.Run("Server TLS without client CA (one-way TLS)", func(t *testing.T) {
		cfg, err := LoadServerTLSConfig(serverCertFile, serverKeyFile, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg == nil {
			t.Fatalf("expected non-nil tls.Config")
		}
		if len(cfg.Certificates) != 1 {
			t.Fatalf("expected 1 certificate, got %d", len(cfg.Certificates))
		}
		if cfg.MinVersion != tls.VersionTLS12 {
			t.Errorf("expected MinVersion TLS 1.2, got %x", cfg.MinVersion)
		}
		if cfg.ClientAuth != tls.NoClientCert {
			t.Errorf("expected ClientAuth NoClientCert, got %v", cfg.ClientAuth)
		}
		if cfg.ClientCAs != nil {
			t.Errorf("expected nil ClientCAs, got %v", cfg.ClientCAs)
		}
	})

	t.Run("Server TLS with client CA (mTLS)", func(t *testing.T) {
		cfg, err := LoadServerTLSConfig(serverCertFile, serverKeyFile, caFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg == nil {
			t.Fatalf("expected non-nil tls.Config")
		}
		if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
			t.Errorf("expected RequireAndVerifyClientCert, got %v", cfg.ClientAuth)
		}
		if cfg.ClientCAs == nil {
			t.Fatalf("expected non-nil ClientCAs")
		}
	})

	t.Run("Non-existent client CA file", func(t *testing.T) {
		cfg, err := LoadServerTLSConfig(serverCertFile, serverKeyFile, filepath.Join(tempDir, "missing-ca.crt"))
		if err == nil || cfg != nil {
			t.Fatalf("expected error for missing client CA file, got cfg: %v, err: %v", cfg, err)
		}
	})

	t.Run("Invalid client CA file content", func(t *testing.T) {
		badCAFile := filepath.Join(tempDir, "bad-ca.crt")
		if err := os.WriteFile(badCAFile, []byte("NOT A CERTIFICATE"), 0600); err != nil {
			t.Fatalf("failed to write bad CA file: %v", err)
		}
		cfg, err := LoadServerTLSConfig(serverCertFile, serverKeyFile, badCAFile)
		if err == nil || cfg != nil {
			t.Fatalf("expected error for invalid CA PEM, got cfg: %v, err: %v", cfg, err)
		}
	})
}
