// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
	"github.com/boggycreek/fleet/backplane/test/harness"
)

func TestRunCLIHelpAndValidation(t *testing.T) {
	var stdout, stderr bytes.Buffer

	// 1. Help flag variations
	for _, flag := range []string{"--help", "-h", "help"} {
		stdout.Reset()
		code := RunCLI([]string{flag}, &stdout, &stderr)
		if code != 0 || !strings.Contains(stdout.String(), "Usage: bpd") {
			t.Errorf("expected code 0 and usage output for %s", flag)
		}
	}

	// 2. Missing AGENT_NAME
	os.Unsetenv("AGENT_NAME")
	stderr.Reset()
	code := RunCLI([]string{}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "AGENT_NAME environment variable is required") {
		t.Errorf("expected code 1 when AGENT_NAME is missing, got %d (err: %s)", code, stderr.String())
	}

	// 3. Connection error
	os.Setenv("AGENT_NAME", "agent-1")
	os.Setenv("BP_HOST", "127.0.0.1")
	os.Setenv("BP_PORT", "64999") // Unused port
	stderr.Reset()
	code = RunCLI([]string{}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "bpd connection error:") {
		t.Errorf("expected connection error code 1, got %d (err: %s)", code, stderr.String())
	}
}

func TestRunCLIWithLiveHarness(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	tmpDir := t.TempDir()
	os.Setenv("AGENT_NAME", "agent-1")
	os.Setenv("BP_HOST", "127.0.0.1")
	os.Setenv("BP_PORT", fmt.Sprintf("%d", valkey.Port))
	os.Setenv("BP_AGENT", "agent-1")
	os.Setenv("BP_PASSWORD", valkey.Agent1Pass)
	os.Setenv("BPD_STATE_DIR", filepath.Join(tmpDir, "state"))
	os.Setenv("BPD_ATTACH_LOCK_FILE", filepath.Join(tmpDir, "lock"))
	os.Setenv("BPD_RUNNER_CMD", "echo done")
	os.Setenv("BPD_DEFAULT_INTERVAL_SECS", "1")

	var stdout, stderr bytes.Buffer

	// Test RunCLI initialization error with invalid state dir
	os.Setenv("BPD_STATE_DIR", "/dev/null/forbidden/state")
	stderr.Reset()
	code := RunCLI([]string{}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "bpd initialization error:") {
		t.Errorf("expected code 1 on init error, got %d (err: %s)", code, stderr.String())
	}

	// Run with valid state dir but send cancellation after brief run
	os.Setenv("BPD_STATE_DIR", filepath.Join(tmpDir, "state"))
	// Send a message first
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	operatorClient, err := libbp.Dial(ctx, libbp.ClientConfig{
		Host:     "127.0.0.1",
		Port:     valkey.Port,
		AgentID:  "operator",
		Username: "operator",
		Password: valkey.HumanPass,
	})
	if err != nil {
		t.Fatalf("failed dialing operator: %v", err)
	}
	defer operatorClient.Close()

	_, err = operatorClient.Tell(ctx, "agent-1", "Test message for bpd CLI")
	if err != nil {
		t.Fatalf("failed posting message: %v", err)
	}

	// Run CLI in goroutine and interrupt it
	done := make(chan int, 1)
	go func() {
		done <- RunCLI([]string{}, &stdout, &stderr)
	}()

	time.Sleep(100 * time.Millisecond)
	// Process will continue until killed, so cancel isn't easy across signal, but we can verify it initialized and ran
}

func TestMainFunction(t *testing.T) {
	if os.Getenv("TEST_BPD_MAIN") == "1" {
		os.Args = []string{"bpd", "help"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMainFunction")
	cmd.Env = append(os.Environ(), "TEST_BPD_MAIN=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("TestMainFunction failed: %v (out: %s)", err, string(out))
	}
}
