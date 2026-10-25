// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boggycreek/sandbox/pkg/config"
)

func TestAgentCreateRollbackOnFailure(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	// Create paths first
	paths := config.GetPaths()
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("failed to ensure directories: %v", err)
	}

	// Make EnsureIDEKeypair fail during ProvisionAgent:
	// Create ~/.ssh/fleet as a directory containing an unwritable file so it cannot be read as a key nor removed
	sshDir := filepath.Join(tmpDir, ".ssh")
	keyDir := filepath.Join(sshDir, "fleet")
	if err := os.MkdirAll(keyDir, 0755); err != nil {
		t.Fatalf("failed to create keyDir: %v", err)
	}
	blockerFile := filepath.Join(keyDir, "blocker")
	if err := os.WriteFile(blockerFile, []byte("x"), 0400); err != nil {
		t.Fatalf("failed to create blockerFile: %v", err)
	}
	// Make sshDir read-only so os.Remove(keyDir) fails with permission denied
	_ = os.Chmod(sshDir, 0555)
	t.Cleanup(func() {
		_ = os.Chmod(sshDir, 0755)
		_ = os.Chmod(keyDir, 0755)
		_ = os.RemoveAll(sshDir)
	})

	// 1. Create agent, expecting failure due to Provisioning errors (EnsureIDEKeypair)
	code, out, errOut := runFleet([]string{"agent", "create", "fail-bot", "as", "base"})

	// Create should return 1 due to provisioning errors and rollback
	if code != 1 {
		t.Errorf("expected agent create to fail due to provisioning errors, got code %d. out: %s, errOut: %s", code, out, errOut)
	}

	if !strings.Contains(errOut, "rolling back partial state") {
		t.Errorf("expected rollback message in stderr, got: %s", errOut)
	}

	// 2. Verify config is deleted (rollback success)
	if _, err := config.LoadAgentConfig("fail-bot", paths); err == nil {
		t.Errorf("expected fail-bot config to be deleted after rollback")
	}
}

func TestAgentRetireForce(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	// Retire missing agent without --force
	code, _, errOut := runFleet([]string{"agent", "retire", "missing-bot"})
	if code != 1 {
		t.Errorf("expected failure when retiring missing agent without force, got code %d", code)
	}
	if !strings.Contains(errOut, "error") {
		t.Errorf("expected error output, got: %s", errOut)
	}

	// Retire missing agent with --force
	code, out, errOut := runFleet([]string{"agent", "retire", "missing-bot", "--force"})
	if code != 0 {
		t.Errorf("expected success (exit 0) when retiring missing agent with force, got code %d. out: %s, errOut: %s", code, out, errOut)
	}
	if !strings.Contains(errOut, "proceeding with forced cleanup") {
		t.Errorf("expected forced cleanup message, got: %s", errOut)
	}
	
	_ = out
}
