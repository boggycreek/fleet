// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/plugin"
)

func TestHandlePluginCommands(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SSHDir:        filepath.Join(tmpDir, ".ssh"),
		IDEKeyFile:    filepath.Join(tmpDir, ".ssh", "fleet"),
		SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
	}
	_ = paths.EnsureDirectories()

	ctx := context.Background()

	// 1. No arguments -> error + usage
	{
		var stdout, stderr bytes.Buffer
		code := handlePlugin(ctx, paths, []string{}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("expected exit code 1 for no args, got %d", code)
		}
		if !strings.Contains(stderr.String(), "Usage:") {
			t.Errorf("expected usage in stderr, got: %s", stderr.String())
		}
	}

	// 2. Help
	for _, h := range []string{"help", "-h", "--help"} {
		var stdout, stderr bytes.Buffer
		code := handlePlugin(ctx, paths, []string{h}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("expected exit code 0 for help flag %s, got %d", h, code)
		}
		if !strings.Contains(stdout.String(), "Fleet Plugin Manager") {
			t.Errorf("expected plugin manager help text, got: %s", stdout.String())
		}
	}

	// 3. List command
	{
		var stdout, stderr bytes.Buffer
		code := handlePlugin(ctx, paths, []string{"list"}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("expected exit code 0 for list, got %d", code)
		}
		outStr := stdout.String()
		if !strings.Contains(outStr, "PLUGIN") || !strings.Contains(outStr, "gateway") || !strings.Contains(outStr, "toolbox") || !strings.Contains(outStr, "vscode") {
			t.Errorf("expected all plugins in stdout, got: %s", outStr)
		}
	}

	// 4. Add & Remove without target -> error
	for _, cmd := range []string{"add", "remove"} {
		var stdout, stderr bytes.Buffer
		code := handlePlugin(ctx, paths, []string{cmd}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("expected exit code 1 for %s without args, got %d", cmd, code)
		}
	}

	// 5. Add Gateway & Remove Gateway
	{
		var stdout, stderr bytes.Buffer
		code := handlePlugin(ctx, paths, []string{"add", "gateway"}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("expected exit code 0 for add gateway, got %d (stderr: %s)", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Fleet JetBrains Gateway Plugin") {
			t.Errorf("expected gateway plugin success text, got: %s", stdout.String())
		}

		var remOut, remErr bytes.Buffer
		remCode := handlePlugin(ctx, paths, []string{"remove", "gateway"}, &remOut, &remErr)
		if remCode != 0 {
			t.Errorf("expected exit code 0 for remove gateway, got %d (stderr: %s)", remCode, remErr.String())
		}
		if !strings.Contains(remOut.String(), "successfully removed") {
			t.Errorf("expected remove text in stdout, got: %s", remOut.String())
		}
	}

	// 6. Add Toolbox & Remove Toolbox
	{
		var stdout, stderr bytes.Buffer
		code := handlePlugin(ctx, paths, []string{"add", "toolbox"}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("expected exit code 0 for add toolbox, got %d (stderr: %s)", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Fleet JetBrains Toolbox Plugin") {
			t.Errorf("expected toolbox plugin success text, got: %s", stdout.String())
		}

		// Remove Toolbox
		var remOut, remErr bytes.Buffer
		remCode := handlePlugin(ctx, paths, []string{"remove", "toolbox"}, &remOut, &remErr)
		if remCode != 0 {
			t.Errorf("expected exit code 0 for remove toolbox, got %d (stderr: %s)", remCode, remErr.String())
		}
		if !strings.Contains(remOut.String(), "successfully removed") {
			t.Errorf("expected remove text in stdout, got: %s", remOut.String())
		}
	}

	// 7. Rejected aliases and shorthands (should fail with code 1)
	unsupportedCommands := []string{"install", "rm", "uninstall", "delete", "ls", "toolbox", "vscode", "gateway"}
	for _, cmd := range unsupportedCommands {
		var stdout, stderr bytes.Buffer
		code := handlePlugin(ctx, paths, []string{cmd}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("expected exit code 1 for unsupported command or shorthand %q, got %d", cmd, code)
		}
	}

	// 8. Add VS Code & Remove VS Code
	{
		origExec := plugin.ExecCommandContext
		defer func() { plugin.ExecCommandContext = origExec }()
		plugin.ExecCommandContext = func(ctx context.Context, command string, args ...string) *exec.Cmd {
			return exec.Command("true")
		}

		var stdout, stderr bytes.Buffer
		code := handlePlugin(ctx, paths, []string{"add", "vscode"}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("expected exit code 0 for add vscode, got %d", code)
		}
		if !strings.Contains(stdout.String(), "Fleet VS Code Integration") {
			t.Errorf("expected vscode output, got: %s", stdout.String())
		}

		// Remove VS Code
		var remOut, remErr bytes.Buffer
		remCode := handlePlugin(ctx, paths, []string{"remove", "vscode"}, &remOut, &remErr)
		if remCode != 0 {
			t.Errorf("expected exit code 0 for remove vscode, got %d", remCode)
		}
		if !strings.Contains(remOut.String(), "VS Code Remote-SSH configuration unlinked") {
			t.Errorf("expected unlinked text in stdout, got: %s", remOut.String())
		}
	}

	// 9. Unknown command or plugin targets
	{
		var stdout, stderr bytes.Buffer
		code := handlePlugin(ctx, paths, []string{"unknown-cmd"}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("expected exit code 1 for unknown-cmd, got %d", code)
		}

		codeAddUnknown := handlePlugin(ctx, paths, []string{"add", "unknown-target"}, &stdout, &stderr)
		if codeAddUnknown != 1 {
			t.Errorf("expected exit code 1 for add unknown-target, got %d", codeAddUnknown)
		}

		codeRemUnknown := handlePlugin(ctx, paths, []string{"remove", "unknown-target"}, &stdout, &stderr)
		if codeRemUnknown != 1 {
			t.Errorf("expected exit code 1 for remove unknown-target, got %d", codeRemUnknown)
		}
	}
}
