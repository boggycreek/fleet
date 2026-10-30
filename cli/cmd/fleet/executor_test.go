// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/runtime"
)

func TestCommandExecutorInFleet(t *testing.T) {
	// 1. Test SetCommandExecutor
	var executed []string
	mock := runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		executed = append(executed, name)
		return exec.Command("true")
	})

	restore := SetCommandExecutor(mock)
	defer restore()

	cmd := runtime.CommandContext(context.Background(), "test-cli-cmd")
	if cmd == nil || len(executed) != 1 || executed[0] != "test-cli-cmd" {
		t.Errorf("expected mock command to execute through SetCommandExecutor")
	}

	// 2. Test RunWithContext with context-injected CommandExecutor
	var injectedCmds []string
	contextMock := runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		injectedCmds = append(injectedCmds, name)
		return exec.Command("true")
	})

	injectedCtx := runtime.WithCommandExecutor(context.Background(), contextMock)
	var stdout, stderr bytes.Buffer
	code := RunWithContext(injectedCtx, []string{"help"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("expected RunWithContext(help) to succeed, got %d", code)
	}
	if !strings.Contains(stdout.String(), "Fleet CLI") {
		t.Errorf("expected usage output, got: %s", stdout.String())
	}
}
