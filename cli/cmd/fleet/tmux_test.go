// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/config"
)

func TestAgentTmuxAndConnect(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	paths := config.GetPaths()
	_ = paths.EnsureDirectories()

	// 1. Missing args
	var stdout, stderr bytes.Buffer
	code := Run([]string{"agent", "tmux"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 for agent tmux without args, got %d", code)
	}
	if !strings.Contains(stderr.String(), "Usage: fleet agent tmux <name>") {
		t.Errorf("expected usage output in stderr, got: %s", stderr.String())
	}

	stderr.Reset()
	code = Run([]string{"agent", "connect"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 for agent connect without args, got %d", code)
	}
	if !strings.Contains(stderr.String(), "deprecated") {
		t.Errorf("expected deprecation notice in stderr, got: %s", stderr.String())
	}

	// 2. Nonexistent agent
	stderr.Reset()
	code = Run([]string{"agent", "tmux", "nonexistent-agent"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 for nonexistent agent, got %d", code)
	}

	stderr.Reset()
	code = Run([]string{"agent", "connect", "nonexistent-agent"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 for nonexistent agent via connect, got %d", code)
	}
	if !strings.Contains(stderr.String(), "deprecated") {
		t.Errorf("expected deprecation notice, got: %s", stderr.String())
	}

	// 3. Valid agent with mock exec
	cfg, err := config.NewAgentConfig("tmux-test-agent", "base", "developer")
	if err != nil {
		t.Fatalf("failed creating agent config: %v", err)
	}
	if err := config.SaveAgentConfig(cfg, paths); err != nil {
		t.Fatalf("failed saving agent config: %v", err)
	}

	var executedCmds []string
	restoreExec := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		executedCmds = append(executedCmds, fmt.Sprintf("%s %s", name, strings.Join(args, " ")))
		return exec.Command("true")
	})
	defer restoreExec()

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"agent", "tmux", "tmux-test-agent"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("agent tmux failed: %d, stderr: %s", code, stderr.String())
	}
	expectedAttach := fmt.Sprintf("podman exec -it %s tmux attach", cfg.ContainerName)
	if len(executedCmds) == 0 || !strings.Contains(executedCmds[len(executedCmds)-1], expectedAttach) {
		t.Errorf("expected %s command, got: %v", expectedAttach, executedCmds)
	}

	// Connect alias
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"agent", "connect", "tmux-test-agent"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("agent connect failed: %d, stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Notice: 'fleet agent connect' is deprecated. Please use 'fleet agent tmux tmux-test-agent' instead.") {
		t.Errorf("expected deprecation notice in stderr, got: %s", stderr.String())
	}

	// Error when podman exec fails
	restoreFail := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("false")
	})
	defer restoreFail()
	stderr.Reset()
	code = Run([]string{"agent", "tmux", "tmux-test-agent"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected error when exec fails, got %d", code)
	}

	// 4. Host container connect and tmux guards
	cfgNoContainer, _ := config.NewAgentConfig("nocont-test-agent", "base", "developer")
	cfgNoContainer.Runtime = "no-container"
	cfgNoContainer.HostOnly = true
	_ = config.SaveAgentConfig(cfgNoContainer, paths)

	stderr.Reset()
	code = Run([]string{"agent", "tmux", "nocont-test-agent"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "no-container agent") {
		t.Errorf("expected no-container tmux guard error, got code %d: %s", code, stderr.String())
	}

	cfgHostContainer, _ := config.NewAgentConfig("host-cont-agent", "tool", "developer")
	cfgHostContainer.Runtime = "host-container"
	cfgHostContainer.WorkspacePath = tmpDir
	_ = config.SaveAgentConfig(cfgHostContainer, paths)

	stderr.Reset()
	code = Run([]string{"agent", "tmux", "host-cont-agent"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "host tool container without tmux") {
		t.Errorf("expected host container tmux guard error, got code %d: %s", code, stderr.String())
	}

	mockExec := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		executedCmds = append(executedCmds, fmt.Sprintf("%s %s", name, strings.Join(args, " ")))
		if len(args) >= 3 && args[0] == "inspect" {
			return exec.Command("echo", `[{"Id":"test-id","Names":["host-cont-agent"],"State":{"Status":"running","Running":true}}]`)
		}
		return exec.Command("true")
	}
	restoreRuntime2 := SetExecCommandContextForTesting(mockExec)
	defer restoreRuntime2()

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"agent", "connect", "host-cont-agent"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("host container connect failed: code %d, stderr: %s", code, stderr.String())
	}
	expectedHostConnect := fmt.Sprintf("podman exec -it --user 1000 %s /bin/bash", cfgHostContainer.ContainerName)
	if len(executedCmds) == 0 || !strings.Contains(executedCmds[len(executedCmds)-1], expectedHostConnect) {
		t.Errorf("expected %s command, got: %v", expectedHostConnect, executedCmds)
	}

	// 5. Connect empty args
	stderr.Reset()
	code = Run([]string{"agent", "connect"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected 1 on empty agent connect args, got %d", code)
	}

	// 6. Connect stopped host container
	mockStopped := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if len(args) >= 3 && args[0] == "inspect" {
			return exec.Command("echo", `[{"Id":"test-id","Names":["host-cont-agent"],"State":{"Status":"stopped","Running":false}}]`)
		}
		return exec.Command("true")
	}
	restoreStopped := SetExecCommandContextForTesting(mockStopped)
	stderr.Reset()
	code = Run([]string{"agent", "connect", "host-cont-agent"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected 1 when host container is stopped, got %d", code)
	}
	restoreStopped()
}
