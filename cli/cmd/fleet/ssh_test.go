// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"os/exec"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/config"
)

func TestAgentSSHDirect(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	paths := config.GetPaths()
	_ = paths.EnsureDirectories()

	cfg, _ := config.NewAgentConfig("ssh-agent", "base", "developer")
	_ = config.SaveAgentConfig(cfg, paths)

	mockSSH := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "port" {
			return exec.Command("echo", "127.0.0.1:44444")
		}
		if name == "ssh" {
			return exec.Command("true")
		}
		return exec.Command("true")
	}
	restore := SetExecCommandContextForTesting(mockSSH)
	defer restore()

	var stdout, stderr bytes.Buffer
	code := Run([]string{"agent", "ssh", "ssh-agent"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("agent ssh should succeed with mock port and mock ssh, got %d", code)
	}

	// Failure branch
	failSSH := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "port" {
			return exec.Command("echo", "127.0.0.1:44444")
		}
		if name == "ssh" {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restoreFail := SetExecCommandContextForTesting(failSSH)
	defer restoreFail()

	code = Run([]string{"agent", "ssh", "ssh-agent"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("agent ssh should return 1 when ssh command fails, got %d", code)
	}

	// Empty args
	code = Run([]string{"agent", "ssh"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected 1 on empty ssh args, got %d", code)
	}

	// Nonexistent agent
	code = Run([]string{"agent", "ssh", "nonexistent"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected 1 on nonexistent ssh agent, got %d", code)
	}

	// No-container agent
	cfgNoCont, _ := config.NewAgentConfig("nocont-agent", "base", "developer")
	cfgNoCont.Runtime = "no-container"
	_ = config.SaveAgentConfig(cfgNoCont, paths)
	code = Run([]string{"agent", "ssh", "nocont-agent"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected 1 on nocont-agent ssh, got %d", code)
	}
}
