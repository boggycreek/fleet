// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/config"
)

func TestAgentProvisionCommand(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", "65530")
	t.Setenv("FORGEJO_URL", "http://127.0.0.1:65531")
	t.Setenv("GITEA_URL", "http://127.0.0.1:65531")
	t.Setenv("SONAR_HOST_URL", "http://127.0.0.1:65532")

	paths := config.GetPaths()
	_ = paths.EnsureDirectories()

	// 1. Usage error on missing arguments
	code, _, errOut := runFleet([]string{"agent", "provision"})
	if code != 1 || !strings.Contains(errOut, "Usage: fleet agent provision") {
		t.Errorf("expected usage error on agent provision without args, got: %s", errOut)
	}

	// 2. Nonexistent agent error
	code, _, errOut = runFleet([]string{"agent", "provision", "nonexistent"})
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("expected not found error on provision nonexistent, got: %s", errOut)
	}

	// 3. Provision existing agent
	cfg, _ := config.NewAgentConfig("prov-bot", "base", "developer")
	_ = config.SaveAgentConfig(cfg, paths)

	code, out, _ := runFleet([]string{"agent", "provision", "prov-bot"})
	if code != 0 {
		t.Errorf("expected provision prov-bot to succeed, got code %d, out: %s", code, out)
	}
	if !strings.Contains(out, "fully provisioned") {
		t.Errorf("expected 'fully provisioned' in output, got: %s", out)
	}
}

func TestAgentKeysCommand(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	paths := config.GetPaths()
	_ = paths.EnsureDirectories()

	cfg, _ := config.NewAgentConfig("keys-bot", "base", "developer")
	_ = config.SaveAgentConfig(cfg, paths)

	// 1. Inspect specific agent keys
	code, out, _ := runFleet([]string{"agent", "keys", "keys-bot"})
	if code != 0 {
		t.Errorf("expected agent keys keys-bot to succeed, got %d", code)
	}
	if !strings.Contains(out, "Signing Key:") || !strings.Contains(out, "Public Key:") {
		t.Errorf("expected key details in output, got: %s", out)
	}

	// 2. Inspect nonexistent agent keys
	code, _, errOut := runFleet([]string{"agent", "keys", "nonexistent"})
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("expected not found for nonexistent keys query, got: %s", errOut)
	}

	// 3. Inspect all agent keys
	code, out, _ = runFleet([]string{"agent", "keys"})
	if code != 0 || !strings.Contains(out, "keys-bot") {
		t.Errorf("expected all keys list to include keys-bot, got: %s", out)
	}
}

func TestAgentDeprovisionCommand(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", "65530")
	t.Setenv("FORGEJO_URL", "http://127.0.0.1:65531")
	t.Setenv("GITEA_URL", "http://127.0.0.1:65531")
	t.Setenv("SONAR_HOST_URL", "http://127.0.0.1:65532")

	paths := config.GetPaths()
	_ = paths.EnsureDirectories()

	cfg, _ := config.NewAgentConfig("deprov-bot", "base", "developer")
	_ = config.SaveAgentConfig(cfg, paths)

	// Deprovision agent
	code, out, _ := runFleet([]string{"agent", "deprovision", "deprov-bot"})
	if code != 0 {
		t.Errorf("expected deprovision to succeed, got %d, out: %s", code, out)
	}
	if !strings.Contains(out, "deprovisioned successfully") {
		t.Errorf("expected deprovisioned message, got: %s", out)
	}

	// Verify agent config removed
	if _, err := config.LoadAgentConfig("deprov-bot", paths); err == nil {
		t.Errorf("expected deprov-bot config to be deleted after deprovision")
	}
}
