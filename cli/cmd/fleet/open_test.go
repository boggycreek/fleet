// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/ide"
	"github.com/boggycreek/fleet/cli/pkg/runtime"
)

func TestAgentOpenValidationAndErrors(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	paths := config.GetPaths()
	_ = paths.EnsureDirectories()

	// Inject a fake lookPath: only "code" is installed.
	restoreLookPath := ide.SetLookPathForTesting(func(name string) (string, error) {
		if name == "code" {
			return "/usr/bin/code", nil
		}
		return "", fmt.Errorf("not found: %s", name)
	})
	defer restoreLookPath()

	var stdout, stderr bytes.Buffer

	// 1. Missing args
	code := Run([]string{"agent", "open"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 for open without args, got %d", code)
	}
	if !strings.Contains(stderr.String(), "Usage: fleet agent open") {
		t.Errorf("expected usage message, got: %s", stderr.String())
	}

	// 2. Dangling 'in'
	stderr.Reset()
	code = Run([]string{"agent", "open", "myagent", "in"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 for dangling in, got %d", code)
	}
	if !strings.Contains(stderr.String(), "Usage: fleet agent open") {
		t.Errorf("expected usage message, got: %s", stderr.String())
	}

	// 2b. Dangling 'in' followed by flag
	stderr.Reset()
	code = Run([]string{"agent", "open", "myagent", "in", "--no-launch"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 for dangling in with flag, got %d", code)
	}
	if !strings.Contains(stderr.String(), "Usage: fleet agent open") {
		t.Errorf("expected usage message, got: %s", stderr.String())
	}

	// 2c. Extra trailing arguments
	stderr.Reset()
	code = Run([]string{"agent", "open", "myagent", "in", "code", "extra"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 for extra arguments, got %d", code)
	}
	if !strings.Contains(stderr.String(), "Usage: fleet agent open") {
		t.Errorf("expected usage message, got: %s", stderr.String())
	}

	// 3. Invalid flag
	stderr.Reset()
	code = Run([]string{"agent", "open", "myagent", "--unrecognized-flag"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 for invalid flag, got %d", code)
	}

	// 3b. Removed flags (--ide, --local, --remote) are rejected as unknown flags with no deprecation path
	for _, flagArg := range []string{"--ide", "--local", "--remote"} {
		stderr.Reset()
		code = Run([]string{"agent", "open", "myagent", flagArg}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("expected code 1 for unknown flag %s, got %d", flagArg, code)
		}
		if !strings.Contains(stderr.String(), "flag provided but not defined") {
			t.Errorf("expected flag error for %s, got: %s", flagArg, stderr.String())
		}
	}

	// 4. Completely unknown IDE (not in catalogue)
	stderr.Reset()
	code = Run([]string{"agent", "open", "myagent", "in", "emacs"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 for unknown ide emacs, got %d", code)
	}
	if !strings.Contains(stderr.String(), `unknown IDE "emacs"`) {
		t.Errorf("expected unknown IDE error, got: %s", stderr.String())
	}

	// 5. Known IDE (cursor) that is not installed on this fake host
	stderr.Reset()
	code = Run([]string{"agent", "open", "myagent", "in", "cursor"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 for uninstalled ide cursor, got %d", code)
	}
	if !strings.Contains(stderr.String(), "is not installed on this host") {
		t.Errorf("expected not-installed error, got: %s", stderr.String())
	}

	// 5b. When 'in <ide>' is omitted and no supported IDEs detected on PATH
	restoreEmptyLookPath := ide.SetLookPathForTesting(func(name string) (string, error) {
		return "", fmt.Errorf("not found: %s", name)
	})
	stderr.Reset()
	code = Run([]string{"agent", "open", "myagent"}, &stdout, &stderr)
	restoreEmptyLookPath()
	if code != 1 {
		t.Errorf("expected code 1 when no IDEs installed, got %d", code)
	}
	if !strings.Contains(stderr.String(), "no supported IDEs detected on PATH") {
		t.Errorf("expected no supported IDEs error, got: %s", stderr.String())
	}

	// 5c. When 'in <ide>' is omitted and stdin is non-interactive
	restoreNonInteractive := SetIsTerminalForTesting(func(r io.Reader) bool { return false })
	stderr.Reset()
	code = Run([]string{"agent", "open", "myagent"}, &stdout, &stderr)
	restoreNonInteractive()
	if code != 1 {
		t.Errorf("expected code 1 for non-interactive stdin, got %d", code)
	}
	if !strings.Contains(stderr.String(), "fleet error: target IDE is required. Specify 'in <ide>' with one of: code") {
		t.Errorf("expected target IDE required error, got: %s", stderr.String())
	}

	// 5d. Interactive stdin invalid inputs (out of bounds, non-numeric, EOF)
	restoreInteractive := SetIsTerminalForTesting(func(r io.Reader) bool { return true })
	defer restoreInteractive()

	stderr.Reset()
	code = handleAgentOpen(context.Background(), paths, []string{"myagent"}, strings.NewReader("99\n"), &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 for out-of-range selection, got %d", code)
	}
	if !strings.Contains(stderr.String(), "fleet error: invalid selection") {
		t.Errorf("expected invalid selection error, got: %s", stderr.String())
	}

	stderr.Reset()
	code = handleAgentOpen(context.Background(), paths, []string{"myagent"}, strings.NewReader("abc\n"), &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 for non-numeric selection, got %d", code)
	}
	if !strings.Contains(stderr.String(), "fleet error: invalid selection") {
		t.Errorf("expected invalid selection error, got: %s", stderr.String())
	}

	stderr.Reset()
	code = handleAgentOpen(context.Background(), paths, []string{"myagent"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 for empty reader, got %d", code)
	}
	restoreInteractive()

	// 6. Nonexistent agent (IDE resolves OK, agent config missing)
	stderr.Reset()
	code = Run([]string{"agent", "open", "nonexistent-agent", "in", "code"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 for nonexistent agent, got %d", code)
	}
	if !strings.Contains(stderr.String(), "fleet error:") {
		t.Errorf("expected fleet error, got: %s", stderr.String())
	}

	// 7. No-container agent (cannot use container IDE forwarding)
	hostCfg, _ := config.NewAgentConfig("host-open-bot", "base", "developer")
	hostCfg.Runtime = "no-container"
	hostCfg.HostOnly = true
	_ = config.SaveAgentConfig(hostCfg, paths)

	stderr.Reset()
	code = Run([]string{"agent", "open", "host-open-bot", "in", "code"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 for no-container agent open, got %d", code)
	}
	if !strings.Contains(stderr.String(), "is a no-container agent (runs directly on host, no container SSH IDE forwarding)") {
		t.Errorf("expected host agent IDE error, got: %s", stderr.String())
	}

	// 8. Host container agent (opens remote container SSH workspace uniformly)
	hostCtrCfg, _ := config.NewAgentConfig("host-ctr-open", "tool", "developer")
	hostCtrCfg.Runtime = "host-container"
	hostCtrCfg.WorkspacePath = "/tmp/test-host-workspace"
	_ = config.SaveAgentConfig(hostCtrCfg, paths)

	mockFn := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "port" {
			return exec.Command("echo", "127.0.0.1:32777")
		}
		return exec.Command("true")
	}
	restoreRuntime := SetExecCommandContextForTesting(mockFn)
	defer restoreRuntime()

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"agent", "open", "host-ctr-open", "in", "code", "--no-launch"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("expected code 0 for host container open --no-launch, got %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SSH host alias prepared: fleet-host-ctr-open") {
		t.Errorf("expected host alias output, got: %s", stdout.String())
	}
}

func TestAgentOpenExecution(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	paths := config.GetPaths()
	_ = paths.EnsureDirectories()

	cfg, err := config.NewAgentConfig("ide-agent", "base", "developer")
	if err != nil {
		t.Fatalf("failed creating agent config: %v", err)
	}
	if err := config.SaveAgentConfig(cfg, paths); err != nil {
		t.Fatalf("failed saving agent config: %v\n", err)
	}

	// Inject a fake lookPath: code, webstorm, and goland are "installed".
	restoreLookPath := ide.SetLookPathForTesting(func(name string) (string, error) {
		switch name {
		case "code":
			return "/usr/bin/code", nil
		case "webstorm":
			return "/home/user/.local/share/JetBrains/Toolbox/scripts/webstorm", nil
		case "goland":
			return "/home/user/.local/share/JetBrains/Toolbox/scripts/goland", nil
		}
		return "", fmt.Errorf("not found: %s", name)
	})
	defer restoreLookPath()
	var executedCmds []string
	mockFn := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmdStr := fmt.Sprintf("%s %s", name, strings.Join(args, " "))
		executedCmds = append(executedCmds, cmdStr)

		// Mock podman port to return host port 32777
		if name == "podman" && len(args) > 1 && args[0] == "port" {
			return exec.Command("echo", "127.0.0.1:32777")
		}
		// Mock VS Code server presence check
		if name == "podman" && len(args) > 3 && strings.Contains(args[3], "test -d /home/agent/.vscode-server") {
			return exec.Command("true")
		}
		return exec.Command("true")
	}

	restoreRuntime := SetExecCommandContextForTesting(mockFn)
	defer restoreRuntime()

	restoreInteractive := SetIsTerminalForTesting(func(r io.Reader) bool { return true })
	defer restoreInteractive()

	var stdout, stderr bytes.Buffer

	// Test 1: Omitting 'in <ide>' with interactive selection (selecting 1: code) and --no-launch
	restoreStdin := SetStdinForTesting(strings.NewReader("1\n"))
	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"agent", "open", "ide-agent", "--no-launch"}, &stdout, &stderr)
	restoreStdin()
	if code != 0 {
		t.Fatalf("agent open --no-launch failed: %d, stderr: %s", code, stderr.String())
	}
	outStr := stdout.String()
	if !strings.Contains(outStr, `Select an IDE to open agent "ide-agent":`) {
		t.Errorf("expected interactive prompt header, got: %s", outStr)
	}
	if !strings.Contains(outStr, "1) VS Code (/usr/bin/code)") {
		t.Errorf("expected VS Code option in prompt, got: %s", outStr)
	}
	if !strings.Contains(outStr, "SSH host alias prepared: fleet-ide-agent (Port: 32777)") {
		t.Errorf("expected host alias prepared output, got: %s", outStr)
	}
	if !strings.Contains(outStr, "vscode-remote://ssh-remote+fleet-ide-agent/home/agent/workspace") {
		t.Errorf("expected VS Code Remote URI, got: %s", outStr)
	}

	// Test 1b: Omitting 'in <ide>' with interactive selection selecting 2 (goland) and --no-launch
	restoreStdin2 := SetStdinForTesting(strings.NewReader("2\n"))
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"agent", "open", "ide-agent", "--no-launch"}, &stdout, &stderr)
	restoreStdin2()
	if code != 0 {
		t.Fatalf("agent open interactive selection 2 failed: %d, stderr: %s", code, stderr.String())
	}
	outStr = stdout.String()
	if !strings.Contains(outStr, "Connect via GoLand") {
		t.Errorf("expected GoLand connection info, got: %s", outStr)
	}

	// Test 1c: Omitting 'in <ide>' with interactive selection selecting 3 (webstorm) and --no-launch
	restoreStdin3 := SetStdinForTesting(strings.NewReader("3\n"))
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"agent", "open", "ide-agent", "--no-launch"}, &stdout, &stderr)
	restoreStdin3()
	if code != 0 {
		t.Fatalf("agent open interactive selection 3 failed: %d, stderr: %s", code, stderr.String())
	}
	outStr = stdout.String()
	if !strings.Contains(outStr, "Connect via WebStorm") {
		t.Errorf("expected WebStorm connection info, got: %s", outStr)
	}

	// Test 2: positional 'in webstorm --no-launch' (JetBrains family)
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"agent", "open", "ide-agent", "in", "webstorm", "--no-launch"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("agent open in webstorm --no-launch failed: %d, stderr: %s", code, stderr.String())
	}
	outStr = stdout.String()
	if !strings.Contains(outStr, "Connect via WebStorm") {
		t.Errorf("expected WebStorm connection info, got: %s", outStr)
	}
	if !strings.Contains(outStr, "ssh://agent@localhost:32777/home/agent/workspace") {
		t.Errorf("expected JetBrains SSH URI in no-launch output, got: %s", outStr)
	}

	// Test 3: positional 'in goland --no-launch' (JetBrains, different binary)
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"agent", "open", "ide-agent", "in", "goland", "--no-launch"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("agent open in goland --no-launch failed: %d, stderr: %s", code, stderr.String())
	}
	outStr = stdout.String()
	if !strings.Contains(outStr, "GoLand") {
		t.Errorf("expected GoLand in output, got: %s", outStr)
	}

	// Test 4: positional 'in code' launch (VS Code) with extension installation
	executedCmds = nil
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"agent", "open", "ide-agent", "in", "code"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("agent open in code failed: %d, stderr: %s", code, stderr.String())
	}
	outStr = stdout.String()
	if !strings.Contains(outStr, "Opening ide-agent in VS Code") {
		t.Errorf("expected opening message for VS Code, got: %s", outStr)
	}
	// Verify code binary was called with --file-uri
	foundCodeCmd := false
	foundExtensionInstall := false
	for _, cmd := range executedCmds {
		if strings.Contains(cmd, "--file-uri") && strings.Contains(cmd, "vscode-remote://ssh-remote+fleet-ide-agent") {
			foundCodeCmd = true
		}
		if strings.Contains(cmd, "anthropic.claude-code") {
			foundExtensionInstall = true
		}
	}
	if !foundCodeCmd {
		t.Errorf("expected --file-uri vscode-remote:// command, got: %v", executedCmds)
	}
	if !foundExtensionInstall {
		t.Errorf("expected anthropic.claude-code installation command, got: %v", executedCmds)
	}

	// Test 5: positional 'in webstorm' launch (JetBrains family) via system URI launcher
	executedCmds = nil
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"agent", "open", "ide-agent", "in", "webstorm"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("agent open in webstorm failed: %d, stderr: %s", code, stderr.String())
	}
	outStr = stdout.String()
	if !strings.Contains(outStr, "Opening ide-agent in WebStorm via Remote Development") {
		t.Errorf("expected WebStorm opening message, got: %s", outStr)
	}
	if !strings.Contains(outStr, "jetbrains://gateway/ssh/environment?h=127.0.0.1&u=agent&p=32777&launchIde=true&projectHint=/home/agent/workspace") {
		t.Errorf("expected JetBrains URI in stdout, got: %s", outStr)
	}
	foundURILaunch := false
	for _, cmd := range executedCmds {
		if strings.Contains(cmd, "jetbrains://gateway/ssh/environment") {
			foundURILaunch = true
		}
	}
	if !foundURILaunch {
		t.Errorf("expected system URI launch command for JetBrains, got: %v", executedCmds)
	}

	// Test 5b: JetBrains launch on macOS ("open")
	restoreDarwinLauncher := SetSystemURILauncherForTesting(func() string { return "open" })
	executedCmds = nil
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"agent", "open", "ide-agent", "in", "goland"}, &stdout, &stderr)
	restoreDarwinLauncher()
	if code != 0 {
		t.Fatalf("agent open in goland (darwin) failed: %d, stderr: %s", code, stderr.String())
	}
	foundDarwinLaunch := false
	for _, cmd := range executedCmds {
		if strings.HasPrefix(cmd, "open jetbrains://") {
			foundDarwinLaunch = true
		}
	}
	if !foundDarwinLaunch {
		t.Errorf("expected darwin 'open' launch command for JetBrains, got: %v", executedCmds)
	}

	// Test 6: VS Code launch failure
	failFn := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "port" {
			return exec.Command("echo", "127.0.0.1:32777")
		}
		if name == "/usr/bin/code" {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restoreRuntime2 := SetExecCommandContextForTesting(failFn)
	defer restoreRuntime2()

	stderr.Reset()
	code = Run([]string{"agent", "open", "ide-agent", "in", "code"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 when launching VS Code fails, got %d", code)
	}
	if !strings.Contains(stderr.String(), "fleet error launching") {
		t.Errorf("expected launch error message in stderr, got: %s", stderr.String())
	}

	// Test 7a: JetBrains URI launcher fails, but fallback to target IDE binary succeeds
	restoreRuntime()
	var fallbackCmds []string
	jbURIFailFn := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmdStr := fmt.Sprintf("%s %s", name, strings.Join(args, " "))
		fallbackCmds = append(fallbackCmds, cmdStr)
		if name == "podman" && len(args) > 1 && args[0] == "port" {
			return exec.Command("echo", "127.0.0.1:32777")
		}
		if name == "xdg-open" || name == "open" {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restoreRuntime3 := SetExecCommandContextForTesting(jbURIFailFn)
	defer restoreRuntime3()

	stderr.Reset()
	stdout.Reset()
	code = Run([]string{"agent", "open", "ide-agent", "in", "webstorm"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0 when URI fails but fallback succeeds, got %d: %s", code, stderr.String())
	}
	foundFallbackBinary := false
	for _, cmd := range fallbackCmds {
		if strings.Contains(cmd, "webstorm") {
			foundFallbackBinary = true
		}
	}
	if !foundFallbackBinary {
		t.Errorf("expected fallback execution of target binary, got: %v", fallbackCmds)
	}

	// Test 7b: JetBrains URI launcher fails AND fallback binary fails
	restoreRuntime3()
	jbBothFailFn := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "port" {
			return exec.Command("echo", "127.0.0.1:32777")
		}
		if name == "xdg-open" || name == "open" || strings.Contains(name, "webstorm") {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restoreRuntimeBoth := SetExecCommandContextForTesting(jbBothFailFn)
	defer restoreRuntimeBoth()

	stderr.Reset()
	stdout.Reset()
	code = Run([]string{"agent", "open", "ide-agent", "in", "webstorm"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 when launching WebStorm fallback fails, got %d", code)
	}
	if !strings.Contains(stderr.String(), "fleet error launching WebStorm") {
		t.Errorf("expected launch error for webstorm, got: %s", stderr.String())
	}

	// Test 8: Container start failure when stopped
	restoreRuntimeBoth()
	startFailFn := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "port" {
			return exec.Command("false") // Port fails
		}
		if name == "podman" && len(args) > 0 && args[0] == "run" {
			return exec.Command("false") // Start fails
		}
		return exec.Command("true")
	}
	restoreRuntime4 := SetExecCommandContextForTesting(startFailFn)
	defer restoreRuntime4()

	stderr.Reset()
	code = Run([]string{"agent", "open", "ide-agent", "in", "code"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 when start container fails, got %d", code)
	}

	// Test 9: Container starts, but port remains unavailable
	portFailFn := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "port" {
			return exec.Command("false") // Port always fails
		}
		return exec.Command("true") // Container start succeeds
	}
	restoreRuntime5 := SetExecCommandContextForTesting(portFailFn)
	defer restoreRuntime5()

	stderr.Reset()
	code = Run([]string{"agent", "open", "ide-agent", "in", "code"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 when port remains unavailable, got %d", code)
	}
	if !strings.Contains(stderr.String(), "discovering SSH port") {
		t.Errorf("expected discovering SSH port error, got: %s", stderr.String())
	}

	// Test 10: SyncSSHConfigFile error path
	restoreRuntime10 := SetExecCommandContextForTesting(mockFn)
	defer restoreRuntime10()
	badPaths := config.Paths{
		DataHome:      "/dev/null/forbidden",
		SSHConfigFile: "/dev/null/forbidden/ssh_config",
		AgentsDir:     paths.AgentsDir,
	}
	stderr.Reset()
	c := handleAgentOpen(context.Background(), badPaths, []string{"ide-agent", "in", "code"}, nil, &stdout, &stderr)
	if c != 1 {
		t.Errorf("expected code 1 on sync ssh config failure, got %d", c)
	}

	// Direct tests for runtime.ClearAgentKnownHosts
	runtime.ClearAgentKnownHosts("ide-agent", paths)
}
