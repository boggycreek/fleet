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
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boggycreek/sandbox/backplane/test/harness"
	"github.com/boggycreek/sandbox/pkg/config"
)

func TestAgentDoctorScenarios(t *testing.T) {
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	paths := config.GetPaths()
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("failed to create paths: %v", err)
	}

	var out, errOut string

	var code int
	// 1. Doctor without agent name argument
	code, _, errOut = runFleet([]string{"agent", "doctor"})
	if code != 1 || !strings.Contains(errOut, "Usage: fleet agent doctor") {
		t.Errorf("expected usage error when agent name missing, got code %d: %s", code, errOut)
	}

	// 2. Doctor on nonexistent agent
	code, out, errOut = runFleet([]string{"agent", "doctor", "missing-agent"})
	if code != 1 || (!strings.Contains(out, "Missing config") && !strings.Contains(out, "unrepairable")) {
		t.Errorf("expected missing agent error, got code %d: out=%s err=%s", code, out, errOut)
	}

	// 3. Doctor on corrupted agent json
	corruptFile := filepath.Join(paths.AgentsDir, "broken.json")
	if err := os.WriteFile(corruptFile, []byte("{invalid-json"), 0600); err != nil {
		t.Fatalf("failed to write corrupted agent json: %v", err)
	}
	code, out, errOut = runFleet([]string{"agent", "doctor", "broken"})
	if code != 1 || (!strings.Contains(out, "Corrupt JSON") && !strings.Contains(out, "corrupted agent config")) {
		t.Errorf("expected corrupted agent error, got code %d: out=%s err=%s", code, out, errOut)
	}

	// 4. Doctor on healthy agent configuration
	restorePodmanScenario4 := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("true")
	})
	defer restorePodmanScenario4()

	cfg, err := config.NewAgentConfig("doc-bot", "base", "qa-tester")
	if err != nil {
		t.Fatalf("failed creating agent config: %v", err)
	}
	if err := config.SaveAgentConfig(cfg, paths); err != nil {
		t.Fatalf("failed saving agent config: %v", err)
	}

	code, out, _ = runFleet([]string{"agent", "doctor", "doc-bot"})
	if code != 0 {
		t.Errorf("expected doctor check to pass for healthy config, got code %d: %s", code, out)
	}
	if !strings.Contains(out, "doc-bot") {
		t.Errorf("expected doctor output to summarize doc-bot, got: %s", out)
	}
}

func TestAgentSSHConfigGeneration(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	paths := config.GetPaths()
	_ = paths.EnsureDirectories()

	// 1. ssh-config for nonexistent agent
	code, out, errOut := runFleet([]string{"agent", "ssh-config", "nonexistent"})
	if code != 1 || (!strings.Contains(errOut, "not found") && !strings.Contains(out, "not found")) {
		t.Errorf("expected error for nonexistent agent ssh-config, got %d: %s", code, errOut)
	}

	// 2. ssh-config with no args when stopped agents exist
	cfg, _ := config.NewAgentConfig("stopped-agent", "base", "tester")
	_ = config.SaveAgentConfig(cfg, paths)

	code, out, _ = runFleet([]string{"agent", "ssh-config"})
	if code != 0 {
		t.Errorf("expected ssh-config to succeed, got %d: %s", code, out)
	}

	// 3. ssh-config --all flag
	code, out, _ = runFleet([]string{"agent", "ssh-config", "--all"})
	if code != 0 {
		t.Errorf("expected ssh-config --all to succeed, got %d: %s", code, out)
	}

	// 4. Test printSSHConfigBlock helper output
	var buf bytes.Buffer
	printSSHConfigBlock(&buf, "my-test-agent", 2222, "/home/test/.ssh/fleet")
	rendered := buf.String()
	if !strings.Contains(rendered, "Host fleet-my-test-agent") || !strings.Contains(rendered, "Port 2222") {
		t.Errorf("unexpected ssh config block rendering: %s", rendered)
	}
	if !strings.Contains(rendered, "IdentitiesOnly yes") || !strings.Contains(rendered, "StrictHostKeyChecking accept-new") {
		t.Errorf("missing standard security directives in ssh config block: %s", rendered)
	}

	// 5. Running agent with port lookup mock
	restoreSSHExec := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "port" {
			return exec.Command("echo", "127.0.0.1:42222")
		}
		return exec.Command("true")
	})

	code, out, errOut = runFleet([]string{"agent", "ssh-config", "stopped-agent"})
	if code != 0 {
		t.Errorf("expected ssh-config to succeed for running agent, got %d: %s", code, errOut)
	}
	if !strings.Contains(out, "Host fleet-stopped-agent") || !strings.Contains(out, "Port 42222") {
		t.Errorf("expected rendered ssh config block, got: %s", out)
	}

	// 6. Multiple agents with port lookup mock
	cfg2, _ := config.NewAgentConfig("second-agent", "base", "tester")
	_ = config.SaveAgentConfig(cfg2, paths)
	code, out, _ = runFleet([]string{"agent", "ssh-config"})
	if code != 0 || !strings.Contains(out, "Host fleet-second-agent") {
		t.Errorf("expected multiple agent ssh config blocks, got: %s", out)
	}

	// 7. Running agent when port lookup fails
	restoreFailExec := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "port" {
			return exec.Command("false")
		}
		return exec.Command("true")
	})
	defer restoreFailExec()

	code, _, errOut = runFleet([]string{"agent", "ssh-config", "stopped-agent"})
	if code != 1 || !strings.Contains(errOut, "not running or SSH port is unavailable") {
		t.Errorf("expected unavailable port error, got code %d: %s", code, errOut)
	}
	restoreSSHExec()
}

func TestAgentCleanAndDeprovisioningWithMocks(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("ADMIN_BACKPLANE_PASSWORD", valkey.AdminPass)
	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", fmt.Sprintf("%d", valkey.Port))

	paths := config.GetPaths()
	_ = paths.EnsureDirectories()
	_ = os.WriteFile(paths.IDEKeyFile+".pub", []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA test"), 0644)

	// Mock Gitea server
	giteaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer giteaServer.Close()
	t.Setenv("FORGEJO_URL", giteaServer.URL)
	t.Setenv("GITEA_URL", giteaServer.URL)

	// Mock Sonar server
	sonarServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/system/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"UP"}`))
		case "/api/user_tokens/generate":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"token":"sqa_mock_token"}`))
		case "/api/user_tokens/revoke":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer sonarServer.Close()
	t.Setenv("SONAR_HOST_URL", sonarServer.URL)

	// 1. Create agent with Gitea and Sonar integration active
	code, out, errOut := runFleet([]string{"agent", "create", "fleet-agent", "as", "base", "--role", "integrator"})
	if code != 0 {
		t.Fatalf("agent create with forge mocks failed: code=%d, err=%s", code, errOut)
	}
	if !strings.Contains(out, "fleet-agent") || !strings.Contains(out, "created successfully") {
		t.Errorf("expected successful creation output, got: %s", out)
	}

	// 2. Clean agent container
	code, out, errOut = runFleet([]string{"agent", "clean", "fleet-agent"})
	if code != 0 {
		t.Errorf("agent clean failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "fleet-agent") || !strings.Contains(out, "removed") {
		t.Errorf("expected clean success output, got: %s", out)
	}

	// 3. Retire agent with --force
	code, out, errOut = runFleet([]string{"agent", "retire", "fleet-agent", "--force"})
	if code != 0 {
		t.Fatalf("agent retire failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "fleet-agent") || !strings.Contains(out, "retired") {
		t.Errorf("expected retire success output, got: %s", out)
	}

	// Verify agent configuration was removed
	if _, err := config.LoadAgentConfig("fleet-agent", paths); err == nil {
		t.Errorf("expected agent config to be deleted after retire")
	}

	// 4. Retire and clean error handling
	code, _, errOut = runFleet([]string{"agent", "retire"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage on empty retire args")
	}
	code, _, errOut = runFleet([]string{"agent", "retire", "--force"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage on retire --force without name")
	}
	code, _, errOut = runFleet([]string{"agent", "retire", "nonexistent"})
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("expected not found error on retire nonexistent")
	}

	code, _, errOut = runFleet([]string{"agent", "clean"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage on empty clean args")
	}
	code, _, errOut = runFleet([]string{"agent", "clean", "nonexistent"})
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("expected not found error on clean nonexistent")
	}
}

func TestInfraDoctorScenario(t *testing.T) {
	code, out, errOut := runFleet([]string{"infra", "doctor"})
	if code != 0 {
		t.Errorf("infra doctor failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "infrastructure") {
		t.Errorf("expected infra doctor report, got: %s", out)
	}
}

func TestInfraUpScenario(t *testing.T) {
	restoreExec := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("true")
	})
	defer restoreExec()

	code, out, errOut := runFleet([]string{"infra", "up"})
	if code != 0 {
		t.Errorf("infra up failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "Starting shared infrastructure") || !strings.Contains(out, "online") {
		t.Errorf("expected online message from infra up, got: %s", out)
	}

	// 2. infra down
	code, out, errOut = runFleet([]string{"infra", "down"})
	if code != 0 {
		t.Errorf("infra down failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "Stopping shared infrastructure") {
		t.Errorf("expected stopping message, got: %s", out)
	}

	// 3. infra list
	code, out, errOut = runFleet([]string{"infra", "list"})
	if code != 0 {
		t.Errorf("infra list failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "Shared infrastructure status:") {
		t.Errorf("expected infra status output, got: %s", out)
	}

	// 4. infra help & empty
	code, _, errOut = runFleet([]string{"infra"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage on empty infra args, got code %d: %s", code, errOut)
	}
	code, out, _ = runFleet([]string{"infra", "help"})
	if code != 0 || !strings.Contains(out, "Usage:") {
		t.Errorf("expected usage on infra help, got code %d: %s", code, out)
	}

	// 5. infra unknown command
	code, _, errOut = runFleet([]string{"infra", "unknown-cmd"})
	if code != 1 || !strings.Contains(errOut, "unknown command") {
		t.Errorf("expected unknown command error, got code %d: %s", code, errOut)
	}

	// 6. infra failure branches
	restoreFail := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("false")
	})
	code, _, errOut = runFleet([]string{"infra", "up"})
	if code != 1 || !strings.Contains(errOut, "fleet infra error") {
		t.Errorf("expected error on infra up failure, got code %d: %s", code, errOut)
	}
	restoreFail()
}

func TestAgentStartAndStopLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	paths := config.GetPaths()
	_ = paths.EnsureDirectories()

	// 1. Start nonexistent agent fails
	code, _, errOut := runFleet([]string{"agent", "start", "nonexistent-start"})
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("expected start nonexistent agent to fail, got %d: %s", code, errOut)
	}

	// 2. Start existing agent with mocked podman
	restore := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("true")
	})
	defer restore()

	cfg, _ := config.NewAgentConfig("run-bot", "base", "developer")
	_ = config.SaveAgentConfig(cfg, paths)

	code, out, errOut := runFleet([]string{"agent", "start", "run-bot"})
	if code != 0 {
		t.Errorf("agent start run-bot failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "started") {
		t.Errorf("expected start success output, got: %s", out)
	}

	// 3. Stop existing agent
	code, out, errOut = runFleet([]string{"agent", "stop", "run-bot"})
	if code != 0 {
		t.Errorf("agent stop run-bot failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "stopped") {
		t.Errorf("expected stop success output, got: %s", out)
	}

	// 4. Start host agent
	cfgHost, _ := config.NewAgentConfig("host-lifecycle-bot", "base", "developer")
	cfgHost.Runtime = "host"
	cfgHost.HostOnly = true
	_ = config.SaveAgentConfig(cfgHost, paths)

	code, out, errOut = runFleet([]string{"agent", "start", "host-lifecycle-bot"})
	if code != 0 {
		t.Errorf("agent start host-lifecycle-bot failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "no-container agent") {
		t.Errorf("expected host execution message, got: %s", out)
	}

	// 5. Host agent stop
	code, out, errOut = runFleet([]string{"agent", "stop", "host-lifecycle-bot"})
	if code != 0 {
		t.Errorf("expected stop host agent to exit 0, got %d: %s", code, errOut)
	}
	if !strings.Contains(out, "no container to stop") {
		t.Errorf("expected no container to stop message, got: %s", out)
	}

	// 6. Host agent stop --all skips host cleanly
	code, out, errOut = runFleet([]string{"agent", "stop", "--all"})
	if code != 0 {
		t.Errorf("expected stop --all to succeed, got %d: %s", code, errOut)
	}

	// 7. Host agent clean guard
	code, _, errOut = runFleet([]string{"agent", "clean", "host-lifecycle-bot"})
	if code != 1 || !strings.Contains(errOut, "no container or home volume to clean") {
		t.Errorf("expected clean guard error for host agent, got code %d: %s", code, errOut)
	}

	// 8. Host agent ssh guard
	code, _, errOut = runFleet([]string{"agent", "ssh", "host-lifecycle-bot"})
	if code != 1 || !strings.Contains(errOut, "runs directly on host, no SSH connection required") {
		t.Errorf("expected ssh guard error for host agent, got code %d: %s", code, errOut)
	}

	// 9. Host agent tmux guard
	code, _, errOut = runFleet([]string{"agent", "tmux", "host-lifecycle-bot"})
	if code != 1 || !strings.Contains(errOut, "no container or tmux session") {
		t.Errorf("expected tmux guard error for host agent, got code %d: %s", code, errOut)
	}

	// 10. Host agent clone guard
	code, _, errOut = runFleet([]string{"agent", "clone", "host-lifecycle-bot", "http://example.com/repo.git"})
	if code != 1 || !strings.Contains(errOut, "use host 'git clone' directly") {
		t.Errorf("expected clone guard error for host agent, got code %d: %s", code, errOut)
	}

	// 11. Host agent remote guard
	code, _, errOut = runFleet([]string{"agent", "remote", "host-lifecycle-bot", "list"})
	if code != 1 || !strings.Contains(errOut, "use host 'git remote' directly") {
		t.Errorf("expected remote guard error for host agent, got code %d: %s", code, errOut)
	}
}

func TestAgentListTableMultipleAgents(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	paths := config.GetPaths()
	_ = paths.EnsureDirectories()

	cfg1, _ := config.NewAgentConfig("alpha-bot", "base", "researcher")
	cfg2, _ := config.NewAgentConfig("beta-bot", "opencode", "coder")
	cfgHost, _ := config.NewAgentConfig("gamma-host-bot", "base", "assistant")
	cfgHost.Runtime = "host"
	cfgHost.HostOnly = true
	cfgHostCtr, _ := config.NewAgentConfig("delta-host-ctr", "tool", "assistant")
	cfgHostCtr.Runtime = "host-container"
	cfgHostCtr.WorkspacePath = "/tmp/ws"
	_ = config.SaveAgentConfig(cfg1, paths)
	_ = config.SaveAgentConfig(cfg2, paths)
	_ = config.SaveAgentConfig(cfgHost, paths)
	_ = config.SaveAgentConfig(cfgHostCtr, paths)

	code, out, errOut := runFleet([]string{"agent", "list"})
	if code != 0 {
		t.Fatalf("agent list failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "alpha-bot") || !strings.Contains(out, "beta-bot") || !strings.Contains(out, "gamma-host-bot") {
		t.Errorf("expected all agents in table output, got: %s", out)
	}
	if !strings.Contains(out, "HOST") {
		t.Errorf("expected HOST status in table output, got: %s", out)
	}
	if !strings.Contains(out, "AGENT_NAME") || !strings.Contains(out, "STATUS") {
		t.Errorf("expected table header in list output, got: %s", out)
	}

	// JSON list output
	code, out, errOut = runFleet([]string{"agent", "list", "--json"})
	if code != 0 {
		t.Fatalf("agent list --json failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, `"delta-host-ctr"`) {
		t.Errorf("expected delta-host-ctr in json output, got: %s", out)
	}
}

func TestUpdateFallbackAndSanitizeEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	origHTTP := httpClientForDownload
	defer func() {
		httpClientForDownload = origHTTP
	}()

	// 1. Success via prebuilt binary download
	httpClientForDownload = &mockHTTPClient{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("#!/bin/sh\nexit 0\n")),
			}, nil
		},
	}
	restoreExec := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("true")
	})
	defer restoreExec()

	code, out, _ := runFleet([]string{"update"})
	if code != 0 {
		t.Errorf("expected update to succeed via prebuilt download, got code %d", code)
	}
	if !strings.Contains(out, "Update complete") {
		t.Errorf("expected update complete message, got: %s", out)
	}

	// 2. Prebuilt 404 fallback to local build
	httpClientForDownload = &mockHTTPClient{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("404")),
			}, nil
		},
	}
	code, _, _ = runFleet([]string{"update"})
	if code != 0 {
		t.Errorf("expected update to succeed via local build fallback, got code %d", code)
	}

	// 3. Test sanitizeGoEnv
	cleaned := sanitizeGoEnv([]string{"PATH=/bin", "GOROOT=/opt/bad/goroot", "HOME=/home/user"})
	for _, env := range cleaned {
		if strings.HasPrefix(env, "GOROOT=") {
			t.Errorf("expected GOROOT to be removed by sanitizeGoEnv, got: %s", env)
		}
	}
}

type mockHTTPClient struct {
	doFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if m.doFunc != nil {
		return m.doFunc(req)
	}
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(strings.NewReader("404 Not Found")),
	}, nil
}

func TestAgentCloneAndRemote(t *testing.T) {
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	paths := config.GetPaths()
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("failed to create paths: %v", err)
	}

	// 1. Error when arguments are missing
	code, _, errOut := runFleet([]string{"agent", "clone"})
	if code != 1 || !strings.Contains(errOut, "Usage: fleet agent clone") {
		t.Errorf("expected clone usage error, got code %d: %s", code, errOut)
	}

	code, _, errOut = runFleet([]string{"agent", "remote"})
	if code != 1 || !strings.Contains(errOut, "Usage: fleet agent remote") {
		t.Errorf("expected remote usage error, got code %d: %s", code, errOut)
	}

	// 2. Error when agent does not exist
	code, _, errOut = runFleet([]string{"agent", "clone", "nonexistent", "https://example.com/repo.git"})
	if code != 1 || !strings.Contains(errOut, "fleet error") {
		t.Errorf("expected error on nonexistent agent clone, got code %d: %s", code, errOut)
	}

	code, _, errOut = runFleet([]string{"agent", "remote", "nonexistent"})
	if code != 1 || !strings.Contains(errOut, "fleet error") {
		t.Errorf("expected error on nonexistent agent remote, got code %d: %s", code, errOut)
	}

	// Create valid agent config
	cfg, err := config.NewAgentConfig("test-git-agent", "base", "developer")
	if err != nil {
		t.Fatalf("failed creating agent config: %v", err)
	}
	if err := config.SaveAgentConfig(cfg, paths); err != nil {
		t.Fatalf("failed saving agent config: %v", err)
	}

	// Mock commands
	var executedCommands [][]string
	restoreExec := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		full := append([]string{name}, args...)
		executedCommands = append(executedCommands, full)
		// Container inspection: report running
		if len(args) >= 4 && args[0] == "container" && args[1] == "inspect" {
			return exec.Command("echo", "true")
		}
		return exec.Command("true")
	})
	defer restoreExec()

	// 3. Successful clone
	code, out, errOut := runFleet([]string{"agent", "clone", "test-git-agent", "http://gitea:3000/fleet/tools.git", "tools"})
	if code != 0 {
		t.Errorf("expected clone to succeed, got code %d, errOut: %s", code, errOut)
	}
	if !strings.Contains(out, "successfully cloned") {
		t.Errorf("expected success message in clone output, got: %s", out)
	}

	// Clone on host agent fails
	cfgHost, _ := config.NewAgentConfig("test-host-git-agent", "base", "developer")
	cfgHost.Runtime = "no-container"
	_ = config.SaveAgentConfig(cfgHost, paths)

	code, _, errOut = runFleet([]string{"agent", "clone", "test-host-git-agent", "http://gitea:3000/fleet/tools.git"})
	if code != 1 || !strings.Contains(errOut, "host agent") {
		t.Errorf("expected host agent clone error, got code %d: %s", code, errOut)
	}

	// Clone with escaping directory fails
	code, _, errOut = runFleet([]string{"agent", "clone", "test-git-agent", "http://gitea:3000/fleet/tools.git", "../../escape"})
	if code != 1 || !strings.Contains(errOut, "target directory must remain within") {
		t.Errorf("expected escaping directory clone error, got code %d: %s", code, errOut)
	}

	// 4. Remote list / get
	code, _, errOut = runFleet([]string{"agent", "remote", "test-git-agent", "list"})
	if code != 0 {
		t.Errorf("expected remote list to succeed, got code %d: %s", code, errOut)
	}

	// 5. Remote set missing args
	code, _, errOut = runFleet([]string{"agent", "remote", "test-git-agent", "set", "origin"})
	if code != 1 || !strings.Contains(errOut, "Usage: fleet agent remote") {
		t.Errorf("expected remote set usage error, got code %d: %s", code, errOut)
	}

	// 6. Remote set success
	code, out, errOut = runFleet([]string{"agent", "remote", "test-git-agent", "set", "origin", "http://gitea:3000/fleet/tools.git"})
	if code != 0 {
		t.Errorf("expected remote set to succeed, got code %d: %s", code, errOut)
	}
	if !strings.Contains(out, "Updated remote") {
		t.Errorf("expected Updated remote in output, got: %s", out)
	}

	// 7. Remote unknown subcommand
	code, _, errOut = runFleet([]string{"agent", "remote", "test-git-agent", "invalid-sub"})
	if code != 1 || !strings.Contains(errOut, "unknown subcommand") {
		t.Errorf("expected unknown subcommand error, got code %d: %s", code, errOut)
	}

	// 8. Remote on host agent
	code, _, errOut = runFleet([]string{"agent", "remote", "test-host-git-agent"})
	if code != 1 || !strings.Contains(errOut, "host agent") {
		t.Errorf("expected host agent error for remote, got code %d: %s", code, errOut)
	}

	// 9. Remote with invalid dir
	code, _, errOut = runFleet([]string{"agent", "remote", "test-git-agent", "list", "--dir=../../escape"})
	if code != 1 || !strings.Contains(errOut, "target directory must remain within") {
		t.Errorf("expected escaping dir error, got code %d: %s", code, errOut)
	}

	// 10. Remote get
	code, _, errOut = runFleet([]string{"agent", "remote", "test-git-agent", "get", "upstream"})
	if code != 0 {
		t.Errorf("expected remote get to succeed, got code %d: %s", code, errOut)
	}
	code, _, errOut = runFleet([]string{"agent", "remote", "test-git-agent", "get"})
	if code != 0 {
		t.Errorf("expected remote get default to succeed, got code %d: %s", code, errOut)
	}
}

func TestHostContainerLifecycleAndWorkspaceProjection(t *testing.T) {
	t.Setenv("SONAR_DISABLED", "1")
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	wsDir := filepath.Join(tmpDir, "my-workspace")
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		t.Fatalf("failed creating workspace dir: %v", err)
	}

	paths := config.GetPaths()
	_ = paths.EnsureDirectories()

	// 1. Conflicting flags --host and --no-container
	code, _, errOut := runFleet([]string{"agent", "create", "bad-bot", "--host", "--no-container"})
	if code != 1 || !strings.Contains(errOut, "cannot specify both --host and --no-container") {
		t.Errorf("expected conflicting flags error, got code %d: %s", code, errOut)
	}

	// 2. Nonexistent --pwd path
	code, _, errOut = runFleet([]string{"agent", "create", "bad-pwd-bot", "--host", "--pwd", filepath.Join(tmpDir, "nonexistent")})
	if code != 1 || !strings.Contains(errOut, "does not exist") {
		t.Errorf("expected nonexistent workspace error, got code %d: %s", code, errOut)
	}

	// 2b. Root and system path projection denial
	code, _, errOut = runFleet([]string{"agent", "create", "bad-root-bot", "--host", "--pwd", "/"})
	if code != 1 || !strings.Contains(errOut, "cannot project root filesystem") {
		t.Errorf("expected root projection denial error, got code %d: %s", code, errOut)
	}

	code, _, errOut = runFleet([]string{"agent", "create", "bad-sys-bot", "--host", "--pwd", "/etc"})
	if code != 1 || !strings.Contains(errOut, "cannot project system directory") {
		t.Errorf("expected system directory projection denial error, got code %d: %s", code, errOut)
	}

	homeDir, _ := os.UserHomeDir()
	if homeDir != "" {
		code, _, errOut = runFleet([]string{"agent", "create", "bad-home-bot", "--host", "--pwd", homeDir})
		if code != 1 || !strings.Contains(errOut, "projecting user home directory") {
			t.Errorf("expected home directory projection denial error, got code %d: %s", code, errOut)
		}
	}

	// 3. Create host-connected container agent with --host and --pwd
	code, out, errOut := runFleet([]string{"agent", "create", "host-coder", "as", "tool", "--host", "--pwd", wsDir, "--role", "interactive-pair", "--model-url", "http://localhost:11434/v1"})
	if code != 0 {
		t.Fatalf("agent create --host failed: code=%d err=%s out=%s", code, errOut, out)
	}
	if !strings.Contains(out, "host-container (host workspace projection)") {
		t.Errorf("expected host-container runtime in create output, got: %s", out)
	}
	if !strings.Contains(out, wsDir) {
		t.Errorf("expected workspace directory in output, got: %s", out)
	}

	cfg, err := config.LoadAgentConfig("host-coder", paths)
	if err != nil {
		t.Fatalf("failed loading created host-coder config: %v", err)
	}
	if !cfg.IsHostContainer() || !cfg.IsHost() || cfg.IsNoContainer() {
		t.Errorf("expected IsHostContainer()=true, IsHost()=true, got IsHostContainer=%v, IsHost=%v, IsNoContainer=%v",
			cfg.IsHostContainer(), cfg.IsHost(), cfg.IsNoContainer())
	}
	if cfg.WorkspacePath != wsDir {
		t.Errorf("expected WorkspacePath=%q, got %q", wsDir, cfg.WorkspacePath)
	}
	if cfg.ContainerName != "fleet-agent-host-coder" {
		t.Errorf("expected ContainerName 'fleet-agent-host-coder', got %q", cfg.ContainerName)
	}

	// 4. Create no-container agent with --no-container
	code, out, errOut = runFleet([]string{"agent", "create", "bare-bot", "--no-container", "--role", "host-script"})
	if code != 0 {
		t.Fatalf("agent create --no-container failed: code=%d err=%s out=%s", code, errOut, out)
	}
	if !strings.Contains(out, "no-container (direct host execution, no container)") {
		t.Errorf("expected no-container runtime in output, got: %s", out)
	}

	cfgBare, err := config.LoadAgentConfig("bare-bot", paths)
	if err != nil {
		t.Fatalf("failed loading bare-bot config: %v", err)
	}
	if !cfgBare.IsNoContainer() || !cfgBare.IsHost() || cfgBare.IsHostContainer() {
		t.Errorf("expected IsNoContainer()=true for bare-bot, got: IsNoContainer=%v, IsHostContainer=%v",
			cfgBare.IsNoContainer(), cfgBare.IsHostContainer())
	}
	if cfgBare.Runtime != "no-container" {
		t.Errorf("expected Runtime 'no-container', got %q", cfgBare.Runtime)
	}
	if cfgBare.Image != "no-container" {
		t.Errorf("expected Image 'no-container', got %q", cfgBare.Image)
	}

	// 5. Start host-connected container with Podman mock
	var executedCmds []string
	mockFn := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmdStr := fmt.Sprintf("%s %s", name, strings.Join(args, " "))
		executedCmds = append(executedCmds, cmdStr)
		if len(args) >= 3 && args[0] == "container" && args[1] == "exists" {
			// Container does not exist yet; trigger 'podman run'
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restoreRuntime := SetExecCommandContextForTesting(mockFn)
	defer restoreRuntime()

	code, out, errOut = runFleet([]string{"agent", "start", "host-coder"})
	if code != 0 {
		t.Fatalf("agent start host-coder failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "Host agent \"host-coder\" started") {
		t.Errorf("expected Host agent started message, got: %s", out)
	}
	if !strings.Contains(out, "Connect: fleet agent connect host-coder") {
		t.Errorf("expected connect instruction in output, got: %s", out)
	}

	// Verify podman run args included --net=host, keep-id, and workspace mount
	var runCmd string
	for _, c := range executedCmds {
		if strings.Contains(c, "podman run") {
			runCmd = c
			break
		}
	}
	if runCmd == "" {
		t.Fatalf("expected podman run command to be executed, executed: %v", executedCmds)
	}
	if !strings.Contains(runCmd, "--network fleet-infra") {
		t.Errorf("expected --network fleet-infra in run command, got: %s", runCmd)
	}
	if !strings.Contains(runCmd, "--add-host llm-gateway:host-gateway") {
		t.Errorf("expected --add-host in run command, got: %s", runCmd)
	}
	if !strings.Contains(runCmd, "--userns=keep-id:uid=1000,gid=1000") {
		t.Errorf("expected keep-id userns mapping in run command, got: %s", runCmd)
	}
	expectedMount := fmt.Sprintf("-v %s:/home/agent/workspace:z", wsDir)
	if !strings.Contains(runCmd, expectedMount) {
		t.Errorf("expected workspace mount %q in run command, got: %s", expectedMount, runCmd)
	}
}
