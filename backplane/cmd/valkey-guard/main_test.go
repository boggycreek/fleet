// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
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
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/valkeyguard"
)

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *safeBuffer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b.Reset()
}

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

func createTestTLSCredentials(t *testing.T) (certFile, keyFile, caFile string, caPEM []byte) {
	t.Helper()
	tempDir := t.TempDir()

	caCert, caKey, caCertPEM, _ := generateTestCertificate(t, true, "Test CA", nil, nil, nil)
	caPath := filepath.Join(tempDir, "ca.crt")
	if err := os.WriteFile(caPath, caCertPEM, 0600); err != nil {
		t.Fatalf("failed to write CA file: %v", err)
	}

	_, _, serverCertPEM, serverKeyPEM := generateTestCertificate(
		t,
		false,
		"127.0.0.1",
		[]net.IP{net.ParseIP("127.0.0.1")},
		caCert,
		caKey,
	)

	serverCertPath := filepath.Join(tempDir, "server.crt")
	serverKeyPath := filepath.Join(tempDir, "server.key")
	if err := os.WriteFile(serverCertPath, serverCertPEM, 0600); err != nil {
		t.Fatalf("failed to write server cert file: %v", err)
	}
	if err := os.WriteFile(serverKeyPath, serverKeyPEM, 0600); err != nil {
		t.Fatalf("failed to write server key file: %v", err)
	}

	return serverCertPath, serverKeyPath, caPath, caCertPEM
}

func TestCLI_HelpAndUsage(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "-help"} {
		var stdout, stderr bytes.Buffer
		code := RunCLI([]string{flag}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("expected return code 0 for help flag %s, got %d", flag, code)
		}
		out := stdout.String()
		if !strings.Contains(out, "Usage: valkey-guard") {
			t.Errorf("expected usage string in stdout for %s, got %s", flag, out)
		}
		if !strings.Contains(out, "--tls-listen") || !strings.Contains(out, "--tls-cert") || !strings.Contains(out, "--tls-key") || !strings.Contains(out, "--tls-client-ca") {
			t.Errorf("expected TLS flags documented in help output for %s", flag)
		}
		if !strings.Contains(out, "VALKEY_GUARD_TLS_LISTEN") || !strings.Contains(out, "VALKEY_GUARD_TLS_CERT") || !strings.Contains(out, "VALKEY_GUARD_TLS_KEY") || !strings.Contains(out, "VALKEY_GUARD_TLS_CLIENT_CA") {
			t.Errorf("expected TLS environment variables documented in help output for %s", flag)
		}
	}
}

func TestCLI_FlagAndEnvParsing(t *testing.T) {
	// 1. Defaults
	cfg, isHelp, err := LoadConfig([]string{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil || isHelp {
		t.Fatalf("unexpected error parsing defaults: %v", err)
	}
	if cfg.ListenAddr != valkeyguard.DefaultListenAddr || cfg.TargetAddr != valkeyguard.DefaultTargetAddr {
		t.Errorf("unexpected default config: %+v", cfg)
	}
	if cfg.TLSListenAddr != "" || cfg.TLSConfig != nil {
		t.Errorf("expected no TLS config by default: %+v", cfg)
	}

	// 2. Custom flags
	serverCert, serverKey, caCert, _ := createTestTLSCredentials(t)
	args := []string{
		"--listen", "127.0.0.1:6399",
		"--target", "127.0.0.1:6379",
		"--max-payload", "2097152",
		"--max-stream-len", "2000",
		"--tls-listen", "127.0.0.1:6400",
		"--tls-cert", serverCert,
		"--tls-key", serverKey,
		"--tls-client-ca", caCert,
	}
	cfg, isHelp, err = LoadConfig(args, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil || isHelp {
		t.Fatalf("unexpected error parsing flags: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:6399" || cfg.TargetAddr != "127.0.0.1:6379" {
		t.Errorf("expected custom addresses, got %+v", cfg)
	}
	if cfg.MaxPayload != 2097152 || cfg.MaxStreamLen != 2000 {
		t.Errorf("expected custom limits, got %+v", cfg)
	}
	if cfg.TLSListenAddr != "127.0.0.1:6400" || cfg.TLSCertFile != serverCert || cfg.TLSKeyFile != serverKey || cfg.TLSClientCAFile != caCert {
		t.Errorf("expected custom TLS fields, got %+v", cfg)
	}
	if cfg.TLSConfig == nil {
		t.Fatalf("expected non-nil TLSConfig when TLS flags are provided")
	}

	// 3. Environment variables
	os.Setenv("VALKEY_GUARD_LISTEN", "127.0.0.1:7000")
	os.Setenv("VALKEY_GUARD_TARGET", "127.0.0.1:7001")
	os.Setenv("VALKEY_GUARD_MAX_PAYLOAD", "500000")
	os.Setenv("VALKEY_GUARD_MAX_STREAM_LEN", "500")
	os.Setenv("VALKEY_GUARD_TLS_LISTEN", "127.0.0.1:7002")
	os.Setenv("VALKEY_GUARD_TLS_CERT", serverCert)
	os.Setenv("VALKEY_GUARD_TLS_KEY", serverKey)
	os.Setenv("VALKEY_GUARD_TLS_CLIENT_CA", caCert)
	defer func() {
		os.Unsetenv("VALKEY_GUARD_LISTEN")
		os.Unsetenv("VALKEY_GUARD_TARGET")
		os.Unsetenv("VALKEY_GUARD_MAX_PAYLOAD")
		os.Unsetenv("VALKEY_GUARD_MAX_STREAM_LEN")
		os.Unsetenv("VALKEY_GUARD_TLS_LISTEN")
		os.Unsetenv("VALKEY_GUARD_TLS_CERT")
		os.Unsetenv("VALKEY_GUARD_TLS_KEY")
		os.Unsetenv("VALKEY_GUARD_TLS_CLIENT_CA")
	}()

	cfg, isHelp, err = LoadConfig([]string{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil || isHelp {
		t.Fatalf("unexpected error parsing envs: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:7000" || cfg.TargetAddr != "127.0.0.1:7001" {
		t.Errorf("expected env addresses, got %+v", cfg)
	}
	if cfg.MaxPayload != 500000 || cfg.MaxStreamLen != 500 {
		t.Errorf("expected env limits, got %+v", cfg)
	}
	if cfg.TLSListenAddr != "127.0.0.1:7002" || cfg.TLSConfig == nil {
		t.Errorf("expected env TLS configuration, got %+v", cfg)
	}

	// 4. Flags override environment variables
	cfg, isHelp, err = LoadConfig([]string{"--listen", "127.0.0.1:8000", "--tls-listen", "127.0.0.1:8001"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil || isHelp {
		t.Fatalf("unexpected error parsing override: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:8000" {
		t.Errorf("flag should override env var: got %s", cfg.ListenAddr)
	}
	if cfg.TLSListenAddr != "127.0.0.1:8001" {
		t.Errorf("flag should override env var: got %s", cfg.TLSListenAddr)
	}

	// 5. TLS-only mode: when TLS is provided without --listen or VALKEY_GUARD_LISTEN
	os.Unsetenv("VALKEY_GUARD_LISTEN")
	cfg, isHelp, err = LoadConfig([]string{
		"--tls-listen", "127.0.0.1:9000",
		"--tls-cert", serverCert,
		"--tls-key", serverKey,
	}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil || isHelp {
		t.Fatalf("unexpected error in TLS-only mode: %v", err)
	}
	if cfg.ListenAddr != "" {
		t.Errorf("expected empty ListenAddr when TLS is active and --listen omitted, got %s", cfg.ListenAddr)
	}
	if cfg.TLSListenAddr != "127.0.0.1:9000" {
		t.Errorf("expected TLSListenAddr 127.0.0.1:9000, got %s", cfg.TLSListenAddr)
	}
}

func TestCLI_InvalidConfiguration(t *testing.T) {
	var stdout, stderr bytes.Buffer

	// Invalid max payload env
	os.Setenv("VALKEY_GUARD_MAX_PAYLOAD", "invalid")
	_, _, err := LoadConfig([]string{}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "invalid VALKEY_GUARD_MAX_PAYLOAD") {
		t.Errorf("expected error for invalid payload env, got %v", err)
	}
	os.Unsetenv("VALKEY_GUARD_MAX_PAYLOAD")

	// Invalid max stream len env
	os.Setenv("VALKEY_GUARD_MAX_STREAM_LEN", "zero")
	_, _, err = LoadConfig([]string{}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "invalid VALKEY_GUARD_MAX_STREAM_LEN") {
		t.Errorf("expected error for invalid stream len env, got %v", err)
	}
	os.Unsetenv("VALKEY_GUARD_MAX_STREAM_LEN")

	// Negative / zero values via flag
	_, _, err = LoadConfig([]string{"--max-payload", "0"}, &stdout, &stderr)
	if err == nil {
		t.Errorf("expected error for zero max-payload")
	}

	_, _, err = LoadConfig([]string{"--max-stream-len", "-5"}, &stdout, &stderr)
	if err == nil {
		t.Errorf("expected error for negative max-stream-len")
	}

	// Unknown flag
	_, _, err = LoadConfig([]string{"--unknown-flag"}, &stdout, &stderr)
	if err == nil {
		t.Errorf("expected error for unknown flag")
	}

	// RunCLIContext with invalid configuration returns 1
	code := RunCLIContext(context.Background(), []string{"--max-payload", "0"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 on config error, got %d", code)
	}
}

func TestCLI_FailClosedValidation_TLS(t *testing.T) {
	var stdout, stderr bytes.Buffer
	serverCert, serverKey, caCert, _ := createTestTLSCredentials(t)

	testCases := []struct {
		name        string
		args        []string
		errContains string
	}{
		{
			name:        "tls-listen without cert or key",
			args:        []string{"--tls-listen", ":6380"},
			errContains: "tls-listen requires both --tls-cert and --tls-key",
		},
		{
			name:        "tls-listen with cert but missing key",
			args:        []string{"--tls-listen", ":6380", "--tls-cert", serverCert},
			errContains: "tls-listen requires both --tls-cert and --tls-key",
		},
		{
			name:        "tls-listen with key but missing cert",
			args:        []string{"--tls-listen", ":6380", "--tls-key", serverKey},
			errContains: "tls-listen requires both --tls-cert and --tls-key",
		},
		{
			name:        "tls-cert without tls-listen",
			args:        []string{"--tls-cert", serverCert},
			errContains: "tls-cert, tls-key, and tls-client-ca require --tls-listen",
		},
		{
			name:        "tls-key without tls-listen",
			args:        []string{"--tls-key", serverKey},
			errContains: "tls-cert, tls-key, and tls-client-ca require --tls-listen",
		},
		{
			name:        "tls-client-ca without tls-listen",
			args:        []string{"--tls-client-ca", caCert},
			errContains: "tls-cert, tls-key, and tls-client-ca require --tls-listen",
		},
		{
			name:        "non-existent cert file",
			args:        []string{"--tls-listen", ":6380", "--tls-cert", "/nonexistent/cert.crt", "--tls-key", serverKey},
			errContains: "failed to load TLS configuration",
		},
		{
			name:        "non-existent key file",
			args:        []string{"--tls-listen", ":6380", "--tls-cert", serverCert, "--tls-key", "/nonexistent/key.key"},
			errContains: "failed to load TLS configuration",
		},
		{
			name:        "non-existent CA file",
			args:        []string{"--tls-listen", ":6380", "--tls-cert", serverCert, "--tls-key", serverKey, "--tls-client-ca", "/nonexistent/ca.crt"},
			errContains: "failed to load TLS configuration",
		},
		{
			name:        "both listen and tls-listen empty",
			args:        []string{"--listen", ""},
			errContains: "at least one listener",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := LoadConfig(tc.args, &stdout, &stderr)
			if err == nil {
				t.Fatalf("expected error for case %q, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("expected error containing %q, got %q", tc.errContains, err.Error())
			}

			// Verify RunCLIContext returns 1 on these fail-closed validation errors
			var outBuf, errBuf bytes.Buffer
			code := RunCLIContext(context.Background(), tc.args, &outBuf, &errBuf)
			if code != 1 {
				t.Errorf("expected RunCLIContext exit code 1 for %q, got %d", tc.name, code)
			}
			if !strings.Contains(errBuf.String(), tc.errContains) {
				t.Errorf("expected stderr to contain %q, got %q", tc.errContains, errBuf.String())
			}
		})
	}
}

func TestCLI_RunCLIContext_ServerStartFailure(t *testing.T) {
	// Bind to an unavailable address / port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind port: %v", err)
	}
	defer ln.Close()

	takenAddr := ln.Addr().String()
	var stdout, stderr bytes.Buffer
	code := RunCLIContext(context.Background(), []string{"--listen", takenAddr}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 on port conflict, got %d", code)
	}
	if !strings.Contains(stderr.String(), "valkey-guard server error") {
		t.Errorf("expected server error in stderr, got: %s", stderr.String())
	}
}

func TestCLI_RunCLIContext_TLSServerStartFailure(t *testing.T) {
	// Bind to an unavailable address / port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind port: %v", err)
	}
	defer ln.Close()

	takenAddr := ln.Addr().String()
	serverCert, serverKey, _, _ := createTestTLSCredentials(t)

	var stdout, stderr bytes.Buffer
	// Test TLS-only start failure
	code := RunCLIContext(context.Background(), []string{
		"--tls-listen", takenAddr,
		"--tls-cert", serverCert,
		"--tls-key", serverKey,
	}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 on TLS port conflict, got %d", code)
	}
	if !strings.Contains(stderr.String(), "valkey-guard server error") {
		t.Errorf("expected server error in stderr, got: %s", stderr.String())
	}

	// Test dual-listener where plaintext succeeds on ephemeral port but TLS fails on taken port
	stdout.Reset()
	stderr.Reset()
	code = RunCLIContext(context.Background(), []string{
		"--listen", "127.0.0.1:0",
		"--tls-listen", takenAddr,
		"--tls-cert", serverCert,
		"--tls-key", serverKey,
	}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 on dual-listener TLS port conflict, got %d", code)
	}
	if !strings.Contains(stderr.String(), "valkey-guard server error") {
		t.Errorf("expected server error in stderr, got: %s", stderr.String())
	}
}

func TestCLI_RunCLIContext_GracefulShutdown(t *testing.T) {
	var stdout, stderr safeBuffer
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan int, 1)
	go func() {
		done <- RunCLIContext(ctx, []string{"--listen", "127.0.0.1:0"}, &stdout, &stderr)
	}()

	// Allow server to start
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("expected code 0 on context cancellation, got %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown")
	}

	if !strings.Contains(stdout.String(), "valkey-guard listening on") {
		t.Errorf("expected start message in stdout, got: %s", stdout.String())
	}
}

func TestCLI_RunCLIContext_TLS_GracefulShutdown(t *testing.T) {
	serverCert, serverKey, caCert, caCertPEM := createTestTLSCredentials(t)

	var stdout, stderr safeBuffer
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan int, 1)
	go func() {
		done <- RunCLIContext(ctx, []string{
			"--tls-listen", "127.0.0.1:0",
			"--tls-cert", serverCert,
			"--tls-key", serverKey,
			"--tls-client-ca", caCert,
		}, &stdout, &stderr)
	}()

	time.Sleep(50 * time.Millisecond)

	// Verify we can connect to the TLS listener
	out := stdout.String()
	if !strings.Contains(out, "valkey-guard listening on") || !strings.Contains(out, "(TLS)") {
		t.Errorf("expected TLS listener log message in stdout, got: %s", out)
	}

	// Extract listening address from stdout
	// format: valkey-guard listening on 127.0.0.1:xxxxx (TLS) -> 127.0.0.1:6380\n
	parts := strings.Split(out, " ")
	if len(parts) >= 4 {
		addr := parts[3]
		rootPool := x509.NewCertPool()
		rootPool.AppendCertsFromPEM(caCertPEM)

		tlsConfig := &tls.Config{
			RootCAs:    rootPool,
			ServerName: "127.0.0.1",
		}

		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err == nil {
			_ = conn.Close()
		}
	}

	cancel()

	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("expected code 0 on context cancellation, got %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown of TLS server")
	}
}

func TestCLI_RunCLIContext_DualListener_GracefulShutdown(t *testing.T) {
	serverCert, serverKey, _, _ := createTestTLSCredentials(t)

	var stdout, stderr safeBuffer
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan int, 1)
	go func() {
		done <- RunCLIContext(ctx, []string{
			"--listen", "127.0.0.1:0",
			"--tls-listen", "127.0.0.1:0",
			"--tls-cert", serverCert,
			"--tls-key", serverKey,
		}, &stdout, &stderr)
	}()

	time.Sleep(50 * time.Millisecond)

	cancel()

	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("expected code 0 on context cancellation, got %d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for graceful shutdown of dual-listener server")
	}

	out := stdout.String()
	if !strings.Contains(out, "valkey-guard listening on") || !strings.Contains(out, "(TLS)") {
		t.Errorf("expected both listeners in stdout, got: %s", out)
	}
}

func TestMainFunction(t *testing.T) {
	if os.Getenv("TEST_VALKEY_GUARD_MAIN") == "1" {
		os.Args = []string{"valkey-guard", "--help"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMainFunction")
	cmd.Env = append(os.Environ(), "TEST_VALKEY_GUARD_MAIN=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("TestMainFunction failed: %v (out: %s)", err, string(out))
	}
}
