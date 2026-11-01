// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/valkeyguard"
)

func TestCLI_HelpAndUsage(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "-help"} {
		var stdout, stderr bytes.Buffer
		code := RunCLI([]string{flag}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("expected return code 0 for help flag %s, got %d", flag, code)
		}
		if !strings.Contains(stdout.String(), "Usage: valkey-guard") {
			t.Errorf("expected usage string in stdout for %s, got %s", flag, stdout.String())
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

	// 2. Custom flags
	args := []string{
		"--listen", "127.0.0.1:6399",
		"--target", "127.0.0.1:6379",
		"--max-payload", "2097152",
		"--max-stream-len", "2000",
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

	// 3. Environment variables
	os.Setenv("VALKEY_GUARD_LISTEN", "127.0.0.1:7000")
	os.Setenv("VALKEY_GUARD_TARGET", "127.0.0.1:7001")
	os.Setenv("VALKEY_GUARD_MAX_PAYLOAD", "500000")
	os.Setenv("VALKEY_GUARD_MAX_STREAM_LEN", "500")
	defer func() {
		os.Unsetenv("VALKEY_GUARD_LISTEN")
		os.Unsetenv("VALKEY_GUARD_TARGET")
		os.Unsetenv("VALKEY_GUARD_MAX_PAYLOAD")
		os.Unsetenv("VALKEY_GUARD_MAX_STREAM_LEN")
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

	// 4. Flags override environment variables
	cfg, isHelp, err = LoadConfig([]string{"--listen", "127.0.0.1:8000"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil || isHelp {
		t.Fatalf("unexpected error parsing override: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:8000" {
		t.Errorf("flag should override env var: got %s", cfg.ListenAddr)
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

func TestCLI_RunCLIContext_GracefulShutdown(t *testing.T) {
	var stdout, stderr bytes.Buffer
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
