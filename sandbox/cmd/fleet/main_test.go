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

	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
	"github.com/boggycreek/sandbox/backplane/test/harness"
	"github.com/boggycreek/sandbox/pkg/config"
)

func runFleet(args []string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestFleetCLIUsageAndHelp(t *testing.T) {
	// 1. Empty args
	code, out, _ := runFleet([]string{})
	if code != 1 || !strings.Contains(out, "Fleet CLI (fleet)") {
		t.Errorf("expected usage output on empty args")
	}

	// 2. Help
	for _, h := range []string{"help", "-h", "--help"} {
		code, out, _ = runFleet([]string{h})
		if code != 0 || !strings.Contains(out, "Usage:") {
			t.Errorf("%s failed", h)
		}
	}

	// 3. Unknown command
	for _, unknown := range []string{"unknown", "repo"} {
		code, _, errOut := runFleet([]string{unknown})
		if code != 1 || !strings.Contains(errOut, "unknown command") {
			t.Errorf("expected unknown command error for %q, got: %s", unknown, errOut)
		}
	}
}

func TestFleetAgentDomain(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("BP_PORT", "65530")
	t.Setenv("FORGEJO_URL", "http://127.0.0.1:65531")
	t.Setenv("GITEA_URL", "http://127.0.0.1:65531")
	t.Setenv("SONAR_HOST_URL", "http://127.0.0.1:65532")

	// Agent help / empty
	code, _, errOut := runFleet([]string{"agent"})
	if code != 1 || !strings.Contains(errOut, "Usage: fleet agent") {
		t.Errorf("agent empty failed")
	}

	for _, h := range []string{"help", "-h", "--help"} {
		code, out, _ := runFleet([]string{"agent", h})
		if code != 0 || !strings.Contains(out, "Usage: fleet agent") {
			t.Errorf("agent %s failed", h)
		}
	}

	// Unknown agent sub
	code, _, errOut = runFleet([]string{"agent", "unknownsub"})
	if code != 1 || !strings.Contains(errOut, "unknown command") {
		t.Errorf("agent unknown sub failed")
	}

	// Create without args
	code, _, errOut = runFleet([]string{"agent", "create"})
	if code != 1 || !strings.Contains(errOut, "Usage: fleet agent create") {
		t.Errorf("agent create empty failed")
	}

	// Create with 'as opencode'
	code, out, _ := runFleet([]string{"agent", "create", "coder-1", "as", "opencode", "--role", "developer"})
	if code != 0 || !strings.Contains(out, "created successfully") || !strings.Contains(out, "fleet-opencode:latest") {
		t.Errorf("agent create as opencode failed: %s", out)
	}

	// Create with '--image claude'
	code, out, _ = runFleet([]string{"agent", "create", "coder-2", "--image", "claude"})
	if code != 0 || !strings.Contains(out, "created successfully") || !strings.Contains(out, "fleet-claude:latest") {
		t.Errorf("agent create --image claude failed: %s", out)
	}

	// Create with 'as pig'
	code, out, _ = runFleet([]string{"agent", "create", "coder-pig", "as", "pig", "--role", "fuzzer"})
	if code != 0 || !strings.Contains(out, "created successfully") || !strings.Contains(out, "fleet-pig:latest") {
		t.Errorf("agent create as pig failed: %s", out)
	}

	// Create with 'as native'
	code, out, _ = runFleet([]string{"agent", "create", "coder-native", "as", "native", "--role", "engine"})
	if code != 0 || !strings.Contains(out, "created successfully") || !strings.Contains(out, "fleet-native:latest") {
		t.Errorf("agent create as native failed: %s", out)
	}

	// Create with '--model-url' and '--model-name'
	code, out, _ = runFleet([]string{"agent", "create", "coder-ollama", "as", "opencode", "--model-url", "http://localhost:11434/v1", "--model-name", "qwen2.5-coder:32b", "--model-key", "ollama-key"})
	if code != 0 || !strings.Contains(out, "created successfully") || !strings.Contains(out, "http://localhost:11434/v1") || !strings.Contains(out, "qwen2.5-coder:32b") {
		t.Errorf("agent create with model flags failed: %s", out)
	}

	// List
	code, out, _ = runFleet([]string{"agent", "list"})
	if code != 0 || !strings.Contains(out, "coder-1") || !strings.Contains(out, "coder-2") {
		t.Errorf("agent list failed: %s", out)
	}

	// List JSON
	code, out, _ = runFleet([]string{"agent", "list", "--json"})
	if code != 0 || !strings.Contains(out, "[") || !strings.Contains(out, "coder-1") {
		t.Errorf("agent list json failed: %s", out)
	}

	// Stop missing args
	code, _, errOut = runFleet([]string{"agent", "stop"})
	if code != 1 || !strings.Contains(errOut, "Usage: fleet agent stop") {
		t.Errorf("agent stop empty failed")
	}

	// Stop nonexistent
	code, _, errOut = runFleet([]string{"agent", "stop", "nonexistent"})
	if code != 1 {
		t.Errorf("expected error stopping nonexistent agent")
	}

	// Clean nonexistent
	code, _, errOut = runFleet([]string{"agent", "clean", "nonexistent"})
	if code != 1 {
		t.Errorf("expected error cleaning nonexistent agent")
	}

	// Retire
	code, out, _ = runFleet([]string{"agent", "retire", "coder-1", "--force"})
	if code != 0 || !strings.Contains(out, "retired and deprovisioned successfully") {
		t.Errorf("agent retire failed: %s", out)
	}

	// Missing commands
	code, _, _ = runFleet([]string{"agent", "start"})
	if code != 1 {
		t.Errorf("agent start missing args should fail")
	}
	code, _, _ = runFleet([]string{"agent", "connect"})
	if code != 1 {
		t.Errorf("agent connect missing args should fail")
	}
	code, _, _ = runFleet([]string{"agent", "ssh"})
	if code != 1 {
		t.Errorf("agent ssh missing args should fail")
	}
}

func TestFleetWithLiveValkey(t *testing.T) {
	t.Setenv("SONAR_DISABLED", "1")
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", fmt.Sprintf("%d", valkey.Port))
	t.Setenv("ADMIN_BACKPLANE_PASSWORD", valkey.AdminPass)

	// Create agent with live Valkey connected
	code, out, _ := runFleet([]string{"agent", "create", "valkey-bot", "as", "base", "--role", "worker"})
	if code != 0 || !strings.Contains(out, "created successfully") {
		t.Errorf("agent create with live valkey failed: %s", out)
	}

	// Verify agent ACL was registered by connecting as valkey-bot
	loaded, err := config.LoadAgentConfig("valkey-bot", config.GetPaths())
	if err != nil {
		t.Fatalf("failed loading agent config: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	agentClient, err := libbp.Dial(ctx, libbp.ClientConfig{
		Host:     "127.0.0.1",
		Port:     valkey.Port,
		Username: "valkey-bot",
		Password: loaded.Password,
		AgentID:  "valkey-bot",
	})
	if err != nil {
		t.Fatalf("failed dialing valkey as registered agent: %v", err)
	}
	defer agentClient.Close()

	// Stop all agents flag test
	code, out, _ = runFleet([]string{"agent", "stop", "--all"})
	if code != 0 || !strings.Contains(out, "Stopped valkey-bot") {
		t.Errorf("agent stop --all failed: %s", out)
	}

	// Infra subcommands
	code, _, _ = runFleet([]string{"infra"})
	if code != 1 {
		t.Errorf("infra empty should fail")
	}

	for _, h := range []string{"help", "-h", "--help"} {
		code, out, _ := runFleet([]string{"infra", h})
		if code != 0 || !strings.Contains(out, "Usage: fleet infra") {
			t.Errorf("infra %s failed", h)
		}
	}

	code, _, errOut := runFleet([]string{"infra", "unknown"})
	if code != 1 || !strings.Contains(errOut, "unknown command") {
		t.Errorf("infra unknown failed")
	}

	// Infra up / list / down execution test
	code, out, _ = runFleet([]string{"infra", "list"})
	if code != 0 || !strings.Contains(out, "SERVICE") {
		t.Errorf("infra list failed: %s", out)
	}

	code, out, _ = runFleet([]string{"infra", "down"})
	if code != 0 || !strings.Contains(out, "infrastructure stopped") {
		t.Errorf("infra down failed: %s", out)
	}
}

func TestFleetUpdateDomain(t *testing.T) {
	// 1. Update help flags
	for _, h := range []string{"help", "-h", "--help"} {
		code, out, _ := runFleet([]string{"update", h})
		if code != 0 || !strings.Contains(out, "Usage: fleet update") {
			t.Errorf("update %s failed", h)
		}
	}

	// 2. Update execution with mocked commands in an isolated repository
	restoreExec := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("true")
	})
	defer restoreExec()

	tempRepo := t.TempDir()
	_ = os.WriteFile(filepath.Join(tempRepo, "Makefile"), []byte("all:\n"), 0644)
	_ = os.MkdirAll(filepath.Join(tempRepo, ".git"), 0755)
	t.Setenv("AGENT_SANDBOX_REPO", tempRepo)
	t.Setenv("FLEET_UPDATE_TIMEOUT", "15m")

	code, out, errOut := runFleet([]string{"update"})
	if code != 0 {
		t.Errorf("expected update to succeed with mocked commands, got code %d, err: %s", code, errOut)
	}
	if !strings.Contains(out, "Update complete") {
		t.Errorf("expected update complete output, got: %s", out)
	}
}

func TestFleetGUIDomain(t *testing.T) {
	code, out, _ := runFleet([]string{"gui"})
	if code != 0 || !strings.Contains(out, "Launching Backplane GUI") {
		t.Errorf("gui command failed: %s", out)
	}
}

func TestMainFunction(t *testing.T) {
	if os.Getenv("TEST_RUN_MAIN") == "1" {
		os.Args = []string{"fleet", "help"}
		main()
		return
	}
	if os.Getenv("TEST_RUN_MAIN_FT") == "1" {
		os.Args = []string{"ft", "help"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMainFunction")
	cmd.Env = append(os.Environ(), "TEST_RUN_MAIN=1")
	_ = cmd.Run()

	cmdFt := exec.Command(os.Args[0], "-test.run=TestMainFunction")
	cmdFt.Env = append(os.Environ(), "TEST_RUN_MAIN_FT=1")
	_ = cmdFt.Run()
}

func TestBinaryNameResolution(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	tests := []struct {
		args     []string
		expected string
	}{
		{args: []string{}, expected: "fleet"},
		{args: []string{"fleet"}, expected: "fleet"},
		{args: []string{"/usr/local/bin/fleet"}, expected: "fleet"},
		{args: []string{"ft"}, expected: "ft"},
		{args: []string{"/usr/local/bin/ft"}, expected: "ft"},
		{args: []string{"./bin/ft"}, expected: "ft"},
		{args: []string{"other"}, expected: "fleet"},
	}

	for _, tc := range tests {
		os.Args = tc.args
		got := binaryName()
		if got != tc.expected {
			t.Errorf("binaryName() for os.Args=%v: expected %q, got %q", tc.args, tc.expected, got)
		}
	}
}

func TestUsageOutputAdaptsWhenInvokedAsFt(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	os.Args = []string{"ft"}

	// 1. Empty args
	code, out, _ := runFleet([]string{})
	if code != 1 {
		t.Errorf("expected exit code 1 on empty args, got %d", code)
	}
	if !strings.Contains(out, "Fleet CLI (ft)") {
		t.Errorf("expected usage title 'Fleet CLI (ft)', got: %s", out)
	}
	if strings.Contains(out, "Fleet CLI (fleet)") {
		t.Errorf("did not expect 'fleet' in header when invoked as 'ft'")
	}
	if !strings.Contains(out, "  ft <domain|command> [args...]") {
		t.Errorf("expected 'ft <domain|command>' usage line, got: %s", out)
	}
	if !strings.Contains(out, "  ft agent create") || !strings.Contains(out, "  ft infra up") || !strings.Contains(out, "  ft plugin add") || !strings.Contains(out, "  ft update") {
		t.Errorf("expected command examples to use 'ft', got: %s", out)
	}
	if !strings.Contains(out, "Run 'ft <domain> help'") {
		t.Errorf("expected hint to use 'ft', got: %s", out)
	}

	// 2. Help flags
	for _, h := range []string{"help", "-h", "--help"} {
		code, out, _ = runFleet([]string{h})
		if code != 0 || !strings.Contains(out, "Fleet CLI (ft)") {
			t.Errorf("expected help output with 'ft' for %s", h)
		}
	}

	// 3. Agent domain help
	code, _, errOut := runFleet([]string{"agent"})
	if code != 1 || !strings.Contains(errOut, "Usage: ft agent") {
		t.Errorf("expected 'Usage: ft agent' on empty agent args, got: %s", errOut)
	}

	code, out, _ = runFleet([]string{"agent", "help"})
	if code != 0 || !strings.Contains(out, "Usage: ft agent <command>") {
		t.Errorf("expected 'Usage: ft agent <command>' on agent help, got: %s", out)
	}

	// 4. Agent subcommands missing required args
	agentSubTests := []struct {
		args     []string
		expected string
	}{
		{[]string{"agent", "create"}, "Usage: ft agent create"},
		{[]string{"agent", "start"}, "Usage: ft agent start"},
		{[]string{"agent", "tmux"}, "Usage: ft agent tmux"},
		{[]string{"agent", "connect"}, "Usage: ft agent connect"},
		{[]string{"agent", "open"}, "Usage: ft agent open"},
		{[]string{"agent", "ssh"}, "Usage: ft agent ssh"},
		{[]string{"agent", "doctor"}, "Usage: ft agent doctor"},
		{[]string{"agent", "stop"}, "Usage: ft agent stop"},
		{[]string{"agent", "clean"}, "Usage: ft agent clean"},
		{[]string{"agent", "retire"}, "Usage: ft agent retire"},
		{[]string{"agent", "provision"}, "Usage: ft agent provision"},
		{[]string{"agent", "clone"}, "Usage: ft agent clone"},
		{[]string{"agent", "remote"}, "Usage: ft agent remote"},
	}
	for _, tc := range agentSubTests {
		code, _, errOut = runFleet(tc.args)
		if code != 1 || !strings.Contains(errOut, tc.expected) {
			t.Errorf("args %v: expected error to contain %q, got code %d, err: %s", tc.args, tc.expected, code, errOut)
		}
	}

	// 5. Infra domain help
	code, _, errOut = runFleet([]string{"infra"})
	if code != 1 || !strings.Contains(errOut, "Usage: ft infra") {
		t.Errorf("expected 'Usage: ft infra' on empty infra args, got: %s", errOut)
	}

	code, out, _ = runFleet([]string{"infra", "help"})
	if code != 0 || !strings.Contains(out, "Usage: ft infra <command>") {
		t.Errorf("expected 'Usage: ft infra <command>' on infra help, got: %s", out)
	}

	// 6. Plugin domain help
	code, out, _ = runFleet([]string{"plugin", "help"})
	if code != 0 || !strings.Contains(out, "ft plugin <command>") {
		t.Errorf("expected 'ft plugin <command>' on plugin help, got: %s", out)
	}

	code, _, errOut = runFleet([]string{"plugin", "add"})
	if code != 1 || !strings.Contains(errOut, "Usage: ft plugin add") {
		t.Errorf("expected 'Usage: ft plugin add' on plugin add empty, got: %s", errOut)
	}

	code, _, errOut = runFleet([]string{"plugin", "remove"})
	if code != 1 || !strings.Contains(errOut, "Usage: ft plugin remove") {
		t.Errorf("expected 'Usage: ft plugin remove' on plugin remove empty, got: %s", errOut)
	}

	// 7. Update domain help
	code, out, _ = runFleet([]string{"update", "help"})
	if code != 0 || !strings.Contains(out, "Usage: ft update") {
		t.Errorf("expected 'Usage: ft update' on update help, got: %s", out)
	}

	// 8. Unknown command error
	code, _, errOut = runFleet([]string{"unknowncmd"})
	if code != 1 || !strings.Contains(errOut, "ft: unknown command \"unknowncmd\" (see 'ft help')") {
		t.Errorf("expected 'ft: unknown command' error, got: %s", errOut)
	}
}

func TestSymlinkCreationLogic(t *testing.T) {
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = os.RemoveAll(tmpDir)
	})

	fleetBin := filepath.Join(tmpDir, "fleet")
	ftBin := filepath.Join(tmpDir, "ft")

	// 1. Create a dummy executable fleet
	if err := os.WriteFile(fleetBin, []byte("#!/bin/sh\necho fleet\n"), 0755); err != nil {
		t.Fatalf("failed to create dummy fleet binary: %v", err)
	}

	// 2. Create symlink ft -> fleet
	if err := os.Symlink(fleetBin, ftBin); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// Verify symlink is valid
	fi, err := os.Lstat(ftBin)
	if err != nil {
		t.Fatalf("failed to lstat symlink: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected ft to be a symlink")
	}

	target, err := os.Readlink(ftBin)
	if err != nil {
		t.Fatalf("failed to readlink: %v", err)
	}
	if target != fleetBin && filepath.Base(target) != "fleet" {
		t.Errorf("expected symlink target %q, got %q", fleetBin, target)
	}

	// 3. Test overwrite logic (idempotency)
	_ = os.Remove(ftBin)
	if err := os.Symlink("fleet", ftBin); err != nil {
		t.Fatalf("failed to recreate relative symlink: %v", err)
	}
	relTarget, err := os.Readlink(ftBin)
	if err != nil {
		t.Fatalf("failed to readlink relative symlink: %v", err)
	}
	if relTarget != "fleet" {
		t.Errorf("expected relative symlink target 'fleet', got %q", relTarget)
	}

	// 4. Test ensureFtSymlink function directly when no external ft exists
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	fleetTarget := filepath.Join(binDir, "fleet")
	_ = os.WriteFile(fleetTarget, []byte("#!/bin/sh\n"), 0755)

	ensureFtSymlink(binDir)
	ftPath := filepath.Join(binDir, "ft")
	fi, err = os.Lstat(ftPath)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected ensureFtSymlink to create symlink: %v", err)
	}

	// 5. If another ft exists on PATH, ensureFtSymlink skips
	otherBinDir := t.TempDir()
	otherFt := filepath.Join(otherBinDir, "ft")
	_ = os.WriteFile(otherFt, []byte("#!/bin/sh\n"), 0755)
	t.Setenv("PATH", otherBinDir+":"+os.Getenv("PATH"))

	skipBinDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(skipBinDir, "fleet"), []byte("#!/bin/sh\n"), 0755)
	ensureFtSymlink(skipBinDir)
	if _, err := os.Lstat(filepath.Join(skipBinDir, "ft")); !os.IsNotExist(err) {
		t.Errorf("expected ft symlink to be skipped when another ft exists on PATH")
	}
}
