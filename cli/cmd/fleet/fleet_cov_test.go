// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/config"
)

func TestFleetCmdCoverageExtensions(t *testing.T) {
	t.Setenv("SONAR_DISABLED", "1")
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	paths := config.GetPaths()
	_ = paths.EnsureDirectories()
	_ = os.WriteFile(paths.EnvFile, []byte("BP_HOST=127.0.0.1\nBP_PORT=6379\nFORGEJO_ADMIN_PASS=pass\n"), 0600)

	// 0. Main CLI flags: help
	for _, flag := range []string{"help", "-h", "--help"} {
		code, out, _ := runFleet([]string{flag})
		if code != 0 || len(out) == 0 {
			t.Errorf("expected zero exit code for main flag %s, got code %d", flag, code)
		}
	}

	// 1. Agent subcommand error paths & invalid subcommands & create flags
	code, _, errOut := runFleet([]string{"agent", "unknown-cmd"})
	if code != 1 || !strings.Contains(errOut, "unknown command") {
		t.Errorf("expected unknown command error for agent")
	}

	code, _, errOut = runFleet([]string{"agent", "create", "bad-both", "--host", "--no-container"})
	if code != 1 || !strings.Contains(errOut, "cannot specify both --host and --no-container") {
		t.Errorf("expected conflicting flags error")
	}

	code, _, errOut = runFleet([]string{"agent", "create", "bad-root", "--host", "--pwd", "/"})
	if code != 1 || !strings.Contains(errOut, "cannot project root filesystem") {
		t.Errorf("expected root projection denial error")
	}

	code, _, errOut = runFleet([]string{"agent", "create", "bad-sys", "--host", "--pwd", "/etc"})
	if code != 1 || !strings.Contains(errOut, "cannot project system directory") {
		t.Errorf("expected system directory denial error")
	}

	code, _, errOut = runFleet([]string{"agent", "create", "bad-nonexist", "--host", "--pwd", filepath.Join(tmpDir, "nonexistent")})
	if code != 1 || !strings.Contains(errOut, "does not exist") {
		t.Errorf("expected non-existent workspace error")
	}

	wsDir := filepath.Join(tmpDir, "workspace")
	_ = os.MkdirAll(wsDir, 0755)

	code, out, _ := runFleet([]string{"agent", "create", "harness-bot", "as", "claude"})
	if code != 0 {
		t.Errorf("expected harness-bot creation success, got %d: %s", code, out)
	}

	code, out, _ = runFleet([]string{"agent", "create", "host-cov-bot", "--host", "--pwd", wsDir})
	if code != 0 || !strings.Contains(out, "host-container") {
		t.Errorf("expected host-container creation success, got %d: %s", code, out)
	}

	code, out, _ = runFleet([]string{"agent", "create", "no-ctr-cov-bot", "--no-container"})
	if code != 0 || !strings.Contains(out, "no-container") {
		t.Errorf("expected no-container creation success, got %d: %s", code, out)
	}

	// agent open, tmux, connect, ssh guards on created host/no-ctr agents
	_, _, _ = runFleet([]string{"agent", "open", "harness-bot", "in", "unknown-ide"})
	_, _, _ = runFleet([]string{"agent", "open", "no-ctr-cov-bot", "in", "code"})
	_, _, _ = runFleet([]string{"agent", "tmux", "no-ctr-cov-bot"})
	_, _, _ = runFleet([]string{"agent", "tmux", "host-cov-bot"})
	_, _, _ = runFleet([]string{"agent", "connect", "host-cov-bot"})
	_, _, _ = runFleet([]string{"agent", "ssh", "no-ctr-cov-bot"})

	code, _, errOut = runFleet([]string{"agent", "start"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected start usage error")
	}

	code, _, errOut = runFleet([]string{"agent", "connect"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected connect usage error")
	}
	code, _, errOut = runFleet([]string{"agent", "connect", "nonexistent"})
	if code != 1 || (!strings.Contains(errOut, "not found") && !strings.Contains(errOut, "error")) {
		t.Errorf("expected connect nonexistent error")
	}

	code, _, errOut = runFleet([]string{"agent", "open"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected open usage error")
	}

	code, _, errOut = runFleet([]string{"agent", "ssh"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected ssh usage error")
	}
	code, _, errOut = runFleet([]string{"agent", "ssh", "nonexistent"})
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("expected ssh nonexistent error")
	}

	code, _, errOut = runFleet([]string{"agent", "stop"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected stop usage error")
	}
	code, _, errOut = runFleet([]string{"agent", "stop", "nonexistent"})
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("expected stop nonexistent error")
	}

	code, _, errOut = runFleet([]string{"agent", "provision"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected provision usage error")
	}
	code, _, errOut = runFleet([]string{"agent", "provision", "nonexistent"})
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("expected provision nonexistent error")
	}

	code, _, errOut = runFleet([]string{"agent", "deprovision"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected deprovision usage error")
	}
	code, _, errOut = runFleet([]string{"agent", "deprovision", "nonexistent"})
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("expected deprovision nonexistent error")
	}

	// Agent list with agents & json mode
	code, out, _ = runFleet([]string{"agent", "list"})
	if code != 0 || !strings.Contains(out, "AGENT_NAME") {
		t.Errorf("expected agent table output, got code %d: %s", code, out)
	}

	code, out, _ = runFleet([]string{"agent", "list", "--json"})
	if code != 0 {
		t.Errorf("expected agent list --json output, got code %d: %s", code, out)
	}

	// Agent keys error paths
	code, out, _ = runFleet([]string{"agent", "keys"})
	if code != 0 {
		t.Errorf("expected success listing keys for all agents, got %d: %s", code, out)
	}

	code, _, errOut = runFleet([]string{"agent", "keys", "nonexistent"})
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("expected not found error on agent keys, got %d: %s", code, errOut)
	}

	// Create valid agent config
	cfg, err := config.NewAgentConfig("keys-bot", "base", "developer")
	if err == nil {
		_ = config.SaveAgentConfig(cfg, paths)
	}

	// Agent keys success
	code, out, _ = runFleet([]string{"agent", "keys", "keys-bot"})
	if code != 0 || !strings.Contains(out, "Public Key:") {
		t.Errorf("expected keys output, got code %d: %s", code, out)
	}

	// Agent clean, doctor, start, stop, provision, retire with mocked podman
	restoreExec := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) >= 2 && args[0] == "port" {
			return exec.Command("echo", "127.0.0.1:2222")
		}
		if name == "podman" && len(args) >= 3 && args[0] == "container" && args[1] == "inspect" {
			return exec.Command("echo", "true")
		}
		return exec.Command("true")
	})
	defer restoreExec()

	// Infra info subcommand coverage with mocked podman
	_, _, _ = runFleet([]string{"infra", "info"})
	_, _, _ = runFleet([]string{"infra", "info", "--show-secrets"})
	_, _, _ = runFleet([]string{"infra", "info", "--json"})
	_, _, _ = runFleet([]string{"infra", "info", "--show-secrets", "--json"})
	_, _, _ = runFleet([]string{"infra", "info", "--invalid-flag"})

	// agent ssh-config on keys-bot with running podman port mock
	_, _, _ = runFleet([]string{"agent", "ssh-config"})
	_, _, _ = runFleet([]string{"agent", "ssh-config", "keys-bot"})

	_, _, _ = runFleet([]string{"agent", "start", "keys-bot"})
	_, _, _ = runFleet([]string{"agent", "stop", "keys-bot"})

	code, out, _ = runFleet([]string{"agent", "clean", "keys-bot"})
	if code != 0 {
		t.Errorf("expected agent clean to succeed, got %d: %s", code, out)
	}

	code, out, _ = runFleet([]string{"agent", "doctor", "keys-bot"})
	if code != 0 {
		t.Errorf("expected agent doctor to succeed, got %d: %s", code, out)
	}

	// agent clone & remote subcommands on keys-bot
	_, _, _ = runFleet([]string{"agent", "clone", "keys-bot", "http://gitea/repo.git"})
	_, _, _ = runFleet([]string{"agent", "clone", "keys-bot", "http://gitea/repo.git", "subfolder"})

	_, _, _ = runFleet([]string{"agent", "remote", "keys-bot"})
	_, _, _ = runFleet([]string{"agent", "remote", "keys-bot", "list"})
	_, _, _ = runFleet([]string{"agent", "remote", "keys-bot", "get"})
	_, _, _ = runFleet([]string{"agent", "remote", "keys-bot", "get", "upstream"})
	_, _, _ = runFleet([]string{"agent", "remote", "keys-bot", "set", "origin", "http://gitea/repo.git"})
	_, _, _ = runFleet([]string{"agent", "remote", "keys-bot", "invalid-sub"})

	// Infra subcommand coverage
	for _, sub := range []string{"up", "down", "list", "doctor", "help", "-h", "--help", "invalid-sub"} {
		_, _, _ = runFleet([]string{"infra", sub})
	}

	// 2. Project subcommands error paths & mock creation
	mockGitea := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer mockGitea.Close()

	mockSonar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"UP"}`))
	}))
	defer mockSonar.Close()

	t.Setenv("GITEA_URL", mockGitea.URL)
	t.Setenv("FORGEJO_URL", mockGitea.URL)
	t.Setenv("SONAR_HOST_URL", mockSonar.URL)

	code, _, errOut = runFleet([]string{"project", "create"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage error for project create without name")
	}

	code, out, _ = runFleet([]string{"project", "create", "factory/proj-one", "--key", "proj-key", "-d", "desc"})
	if code != 0 {
		t.Errorf("expected project create to succeed, got %d: %s", code, out)
	}

	code, out, _ = runFleet([]string{"project", "link", "factory/proj-link", "sonar-link-key"})
	if code != 0 {
		t.Errorf("expected project link to succeed, got %d: %s", code, out)
	}

	code, out, _ = runFleet([]string{"project", "list", "--json"})
	if code != 0 {
		t.Errorf("expected project list --json to succeed, got %d: %s", code, out)
	}

	code, out, _ = runFleet([]string{"project", "doctor"})
	if code != 0 {
		t.Errorf("expected project doctor to succeed, got %d: %s", code, out)
	}

	code, _, errOut = runFleet([]string{"project", "delete"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage error for project delete without name, got %d: %s", code, errOut)
	}

	code, _, errOut = runFleet([]string{"project", "link"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage error for project link without repo, got %d: %s", code, errOut)
	}

	code, _, errOut = runFleet([]string{"project", "unknown-sub"})
	if code != 1 || !strings.Contains(errOut, "unknown command") {
		t.Errorf("expected error on unknown project subcommand, got %d: %s", code, errOut)
	}

	code, out, _ = runFleet([]string{"project", "delete", "proj-one", "--org", "factory", "--force"})
	if code != 0 {
		t.Errorf("expected project delete to succeed, got %d: %s", code, out)
	}

	// 3. Plugin subcommand coverage
	code, _, errOut = runFleet([]string{"plugin"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected plugin usage error on empty args")
	}

	code, out, _ = runFleet([]string{"plugin", "help"})
	if code != 0 || !strings.Contains(out, "Usage:") {
		t.Errorf("expected plugin help output")
	}

	code, _, errOut = runFleet([]string{"plugin", "add"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected plugin add usage error")
	}

	code, _, errOut = runFleet([]string{"plugin", "remove"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected plugin remove usage error")
	}

	code, _, errOut = runFleet([]string{"plugin", "unknown-sub"})
	if code != 1 || !strings.Contains(errOut, "unknown command") {
		t.Errorf("expected plugin unknown subcommand error")
	}

	code, out, _ = runFleet([]string{"plugin", "list"})
	if code != 0 {
		t.Errorf("expected plugin list to succeed")
	}

	for _, target := range []string{"gateway", "toolbox", "vscode", "unknown-target"} {
		_, _, _ = runFleet([]string{"plugin", "add", target})
		_, _, _ = runFleet([]string{"plugin", "remove", target})
	}

	// Plugin error branches on blocked path
	blockedFile := filepath.Join(tmpDir, "blocked_file")
	_ = os.WriteFile(blockedFile, []byte("blocker"), 0600)
	pBlockedPlugin := paths
	pBlockedPlugin.DataHome = filepath.Join(blockedFile, "plugins")
	var nullBuf strings.Builder
	bgCtx := context.Background()
	handlePluginAdd(bgCtx, pBlockedPlugin, "gateway", &nullBuf, &nullBuf)
	handlePluginAdd(bgCtx, pBlockedPlugin, "toolbox", &nullBuf, &nullBuf)
	handlePluginRemove(bgCtx, pBlockedPlugin, "gateway", &nullBuf, &nullBuf)
	handlePluginRemove(bgCtx, pBlockedPlugin, "toolbox", &nullBuf, &nullBuf)

	// 4. Update --check flag & help & ensureFtSymlink edge cases & GUI & update error branches
	code, out, _ = runFleet([]string{"gui"})
	if code != 0 || !strings.Contains(out, "Launching Backplane GUI client") {
		t.Errorf("expected gui launcher message, got: %s", out)
	}

	code, out, _ = runFleet([]string{"update", "--check"})
	if code != 0 {
		t.Errorf("expected update --check to succeed, got %d: %s", code, out)
	}

	code, out, _ = runFleet([]string{"update", "help"})
	if code != 0 || !strings.Contains(out, "Usage:") {
		t.Errorf("expected update help output")
	}

	// Update error branches with failing command mock
	restoreFail := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("false")
	})
	pCustomRepo := paths
	pCustomRepo.DataHome = filepath.Join(tmpDir, "no_repo")
	code, _, _ = runFleet([]string{"update"})
	_ = code
	restoreFail()

	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	fleetBin := filepath.Join(binDir, "fleet")
	ftBin := filepath.Join(binDir, "ft")

	// ensureFtSymlink when fleet doesn't exist
	ensureFtSymlink(binDir)

	// ensureFtSymlink when fleet exists and ft doesn't exist
	_ = os.WriteFile(fleetBin, []byte("fleet binary"), 0755)
	ensureFtSymlink(binDir)

	// ensureFtSymlink when ft is a valid symlink to fleet
	ensureFtSymlink(binDir)

	// ensureFtSymlink when ft is a regular file
	_ = os.Remove(ftBin)
	_ = os.WriteFile(ftBin, []byte("regular file"), 0755)
	ensureFtSymlink(binDir)

	// Provisioning & retiring keys-bot
	_, _, _ = runFleet([]string{"agent", "provision", "keys-bot"})
	_, _, _ = runFleet([]string{"agent", "retire", "keys-bot", "--force"})
}
