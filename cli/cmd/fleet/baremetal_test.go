// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/ide"
	"github.com/boggycreek/fleet/cli/pkg/runtime"
)

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestBaremetalAgentCreateValidation(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{
		DataHome:      t.TempDir(),
		StateHome:     t.TempDir(),
		AgentsDir:     t.TempDir(),
		SecretsDir:    t.TempDir(),
		BPProfilesDir: t.TempDir(),
	}

	var stdout, stderr bytes.Buffer

	// 1. Missing --ssh-host
	code := handleAgentCreate(ctx, paths, []string{"bare-test", "--baremetal"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected exit code 1 for missing --ssh-host, got %d", code)
	}
	if !strings.Contains(stderr.String(), "--ssh-host <host> is required") {
		t.Errorf("expected error message about --ssh-host, got: %s", stderr.String())
	}

	// 2. Combining --baremetal with --host
	stderr.Reset()
	code = handleAgentCreate(ctx, paths, []string{"bare-test", "--baremetal", "--host", "--ssh-host", "192.168.1.50"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected exit code 1 for --baremetal with --host, got %d", code)
	}
	if !strings.Contains(stderr.String(), "cannot combine --baremetal with --host") {
		t.Errorf("expected error about combining flags, got: %s", stderr.String())
	}

	// 3. Combining --baremetal with --no-container
	stderr.Reset()
	code = handleAgentCreate(ctx, paths, []string{"bare-test", "--baremetal", "--no-container", "--ssh-host", "192.168.1.50"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected exit code 1 for --baremetal with --no-container, got %d", code)
	}
	if !strings.Contains(stderr.String(), "cannot combine --baremetal with --host") {
		t.Errorf("expected error about combining flags, got: %s", stderr.String())
	}
}

func TestBaremetalAgentCreateAndLifecycle(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:      tmpDir,
		StateHome:     filepath.Join(tmpDir, "config"),
		AgentsDir:     filepath.Join(tmpDir, "agents"),
		SecretsDir:    filepath.Join(tmpDir, "secrets"),
		BPProfilesDir: filepath.Join(tmpDir, "profiles"),
		SSHConfigFile: filepath.Join(tmpDir, "ssh_config"),
		IDEKeyFile:    filepath.Join(tmpDir, "id_test"),
	}
	_ = paths.EnsureDirectories()
	_ = os.WriteFile(paths.IDEKeyFile, []byte("mock-key"), 0600)
	_ = os.WriteFile(paths.IDEKeyFile+".pub", []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI test-key controller@fleet\n"), 0644)

	t.Setenv("FORGEJO_DISABLED", "1")
	t.Setenv("SONAR_DISABLED", "1")

	restoreLookPath := ide.SetLookPathForTesting(func(name string) (string, error) {
		if name == "code" {
			return "/usr/bin/code", nil
		}
		return "", fmt.Errorf("not found: %s", name)
	})
	defer restoreLookPath()

	var stdout, stderr bytes.Buffer

	// 1. Create baremetal agent with --no-enroll
	code := handleAgentCreate(ctx, paths, []string{
		"dell-7559",
		"--baremetal",
		"--ssh-host", "192.168.1.155",
		"--ssh-port", "2222",
		"--ssh-user", "debian",
		"--role", "embedded-engineer",
		"--no-enroll",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("handleAgentCreate failed (code %d): %s\n%s", code, stdout.String(), stderr.String())
	}

	if !strings.Contains(stdout.String(), "baremetal (physical host execution)") {
		t.Errorf("expected baremetal runtime output: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "debian@192.168.1.155:2222") {
		t.Errorf("expected SSH Target output: %s", stdout.String())
	}

	// Verify saved config
	cfg, err := config.LoadAgentConfig("dell-7559", paths)
	if err != nil {
		t.Fatalf("failed loading agent config: %v", err)
	}
	if !cfg.IsBaremetal() || !cfg.Baremetal || cfg.Runtime != "baremetal" {
		t.Errorf("expected IsBaremetal()=true, got Baremetal=%v, Runtime=%s", cfg.Baremetal, cfg.Runtime)
	}
	if cfg.SSHHost != "192.168.1.155" || cfg.SSHPort != 2222 || cfg.SSHUser != "debian" {
		t.Errorf("unexpected SSH params: %+v", cfg)
	}

	// 2. handleAgentStart
	stdout.Reset()
	stderr.Reset()
	code = handleAgentStart(ctx, paths, []string{"dell-7559"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("handleAgentStart returned %d", code)
	}
	if !strings.Contains(stdout.String(), "baremetal agent running via systemd") {
		t.Errorf("unexpected start output: %s", stdout.String())
	}

	// 3. handleAgentStop
	stdout.Reset()
	stderr.Reset()
	code = handleAgentStop(ctx, paths, []string{"dell-7559"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("handleAgentStop returned %d", code)
	}
	if !strings.Contains(stdout.String(), "systemctl --user stop fleet-agent") {
		t.Errorf("unexpected stop output: %s", stdout.String())
	}

	// 4. handleAgentStop --all (should skip baremetal)
	stdout.Reset()
	stderr.Reset()
	code = handleAgentStop(ctx, paths, []string{"--all"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("handleAgentStop --all returned %d", code)
	}

	// 5. handleAgentTmux
	stdout.Reset()
	stderr.Reset()
	code = handleAgentTmux(ctx, paths, []string{"dell-7559"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected handleAgentTmux to fail with code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "baremetal agent (runs headless daemon") {
		t.Errorf("unexpected tmux stderr: %s", stderr.String())
	}

	// 6. handleAgentConnect
	stdout.Reset()
	stderr.Reset()
	code = handleAgentConnect(ctx, paths, []string{"dell-7559"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected handleAgentConnect to fail with code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "baremetal agent (use 'ssh' to connect)") {
		t.Errorf("unexpected connect stderr: %s", stderr.String())
	}

	// 7. handleAgentClean
	stdout.Reset()
	stderr.Reset()
	code = handleAgentClean(ctx, paths, []string{"dell-7559"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("handleAgentClean returned %d", code)
	}
	if !strings.Contains(stdout.String(), "no container to clean") {
		t.Errorf("unexpected clean output: %s", stdout.String())
	}

	// 8. handleAgentSSHConfig
	stdout.Reset()
	stderr.Reset()
	code = handleAgentSSHConfig(ctx, paths, []string{"dell-7559"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("handleAgentSSHConfig returned %d", code)
	}
	sshConfigOut := stdout.String()
	if !strings.Contains(sshConfigOut, "Host fleet-dell-7559") ||
		!strings.Contains(sshConfigOut, "HostName 192.168.1.155") ||
		!strings.Contains(sshConfigOut, "Port 2222") ||
		!strings.Contains(sshConfigOut, "User debian") {
		t.Errorf("unexpected ssh-config output: %s", sshConfigOut)
	}

	// handleAgentSSHConfig for all agents
	stdout.Reset()
	stderr.Reset()
	code = handleAgentSSHConfig(ctx, paths, []string{"--all"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("handleAgentSSHConfig --all returned %d", code)
	}
	if !strings.Contains(stdout.String(), "Host fleet-dell-7559") {
		t.Errorf("expected baremetal host block in --all output: %s", stdout.String())
	}

	// 9. handleAgentList (text)
	stdout.Reset()
	stderr.Reset()
	code = handleAgentList(ctx, paths, []string{}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("handleAgentList returned %d", code)
	}
	if !strings.Contains(stdout.String(), "BAREMETAL") || !strings.Contains(stdout.String(), "dell-7559") {
		t.Errorf("unexpected agent list output: %s", stdout.String())
	}

	// 10. handleAgentList (--json)
	stdout.Reset()
	stderr.Reset()
	code = handleAgentList(ctx, paths, []string{"--json"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("handleAgentList --json returned %d", code)
	}
	var statuses []runtime.AgentStatus
	if err := json.Unmarshal(stdout.Bytes(), &statuses); err != nil {
		t.Fatalf("failed decoding json agent list: %v", err)
	}
	found := false
	for _, s := range statuses {
		if s.Name == "dell-7559" {
			found = true
			if s.ContainerState != "BAREMETAL" || s.SSHPort != 2222 {
				t.Errorf("unexpected status struct: %+v", s)
			}
		}
	}
	if !found {
		t.Errorf("dell-7559 not found in JSON agent list: %s", stdout.String())
	}

	// 11. handleAgentOpen with --no-launch in vscode
	stdout.Reset()
	stderr.Reset()
	code = handleAgentOpen(ctx, paths, []string{"dell-7559", "in", "code", "--no-launch"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Errorf("handleAgentOpen returned %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "vscode-remote://ssh-remote+fleet-dell-7559/home/debian/workspace") {
		t.Errorf("unexpected vs code URI in open: %s", stdout.String())
	}

	// 12. handleAgentSSH
	var capturedSSHArgs []string
	mockSSH := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "ssh" {
			capturedSSHArgs = args
			return exec.Command("true")
		}
		return exec.Command("true")
	}
	restoreSSH := SetExecCommandContextForTesting(mockSSH)
	defer restoreSSH()

	stdout.Reset()
	stderr.Reset()
	code = handleAgentSSH(ctx, paths, []string{"dell-7559"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("handleAgentSSH returned %d: %s", code, stderr.String())
	}
	joinedArgs := strings.Join(capturedSSHArgs, " ")
	if !strings.Contains(joinedArgs, "debian@192.168.1.155") || !strings.Contains(joinedArgs, "-p 2222") {
		t.Errorf("unexpected ssh args: %s", joinedArgs)
	}

	// 13. handleAgentRetire
	stdout.Reset()
	stderr.Reset()
	code = handleAgentRetire(ctx, paths, []string{"dell-7559", "--force"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("handleAgentRetire returned %d", code)
	}
}

func TestBaremetalAgentCreateWithActiveEnrollment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:      tmpDir,
		StateHome:     filepath.Join(tmpDir, "config"),
		AgentsDir:     filepath.Join(tmpDir, "agents"),
		SecretsDir:    filepath.Join(tmpDir, "secrets"),
		BPProfilesDir: filepath.Join(tmpDir, "profiles"),
		SSHConfigFile: filepath.Join(tmpDir, "ssh_config"),
		IDEKeyFile:    filepath.Join(tmpDir, "id_test"),
	}
	_ = paths.EnsureDirectories()
	_ = os.WriteFile(paths.IDEKeyFile, []byte("mock-key"), 0600)
	_ = os.WriteFile(paths.IDEKeyFile+".pub", []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI test-key controller@fleet\n"), 0644)

	t.Setenv("FORGEJO_DISABLED", "1")
	t.Setenv("SONAR_DISABLED", "1")

	// Pick a free port for enrollment
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed finding free port: %v", err)
	}
	freePort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	var stdout, stderr safeBuffer

	// Launch agent create in background goroutine
	createDone := make(chan int, 1)
	go func() {
		code := handleAgentCreate(ctx, paths, []string{
			"live-enroll-node",
			"--baremetal",
			"--ssh-host", "127.0.0.1",
			"--ssh-port", "22",
			"--ssh-user", "tester",
			"--enroll-port", fmt.Sprintf("%d", freePort),
			"--controller-host", "127.0.0.1",
		}, &stdout, &stderr)
		createDone <- code
	}()

	// Poll until enrollment endpoint responds to curl
	client := &http.Client{Timeout: 500 * time.Millisecond}
	enrollBase := fmt.Sprintf("http://127.0.0.1:%d", freePort)
	installURL := fmt.Sprintf("%s/install/live-enroll-node", enrollBase)

	var token string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(installURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			// Extract token from curl command in install script or stdout
			outputStr := stdout.String()
			if idx := strings.Index(outputStr, "token="); idx != -1 {
				sub := outputStr[idx+6:]
				end := strings.IndexAny(sub, " |\n\"")
				if end != -1 {
					token = sub[:end]
				}
			}
			if token == "" && strings.Contains(string(body), "ENROLL_TOKEN") {
				token = "test"
			}
			if token != "" {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	if token == "" {
		t.Fatalf("timed out waiting for enrollment server: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}

	// Make secret collection request with token
	collectURL := fmt.Sprintf("%s/api/v1/enroll/live-enroll-node?token=%s", enrollBase, token)
	resp, err := client.Get(collectURL)
	if err != nil {
		t.Fatalf("failed collecting secrets: %v", err)
	}
	secretsBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK collecting secrets, got %d: %s", resp.StatusCode, string(secretsBody))
	}

	if !strings.Contains(string(secretsBody), `AGENT_NAME="live-enroll-node"`) {
		t.Errorf("expected AGENT_NAME in secrets payload: %s", string(secretsBody))
	}
	if !strings.Contains(string(secretsBody), `CONTROLLER_SSH_PUB_KEY="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI test-key controller@fleet"`) {
		t.Errorf("expected CONTROLLER_SSH_PUB_KEY in secrets payload: %s", string(secretsBody))
	}

	// Wait for create to finish
	select {
	case code := <-createDone:
		if code != 0 {
			t.Errorf("handleAgentCreate exited with code %d, stderr: %s", code, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for handleAgentCreate to exit after secrets collected")
	}

	if !strings.Contains(stdout.String(), "Credentials collected and token burned successfully!") {
		t.Errorf("expected success message in stdout: %s", stdout.String())
	}
}

func TestBaremetalAgentCreateCancelledAndDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:      tmpDir,
		StateHome:     filepath.Join(tmpDir, "config"),
		AgentsDir:     filepath.Join(tmpDir, "agents"),
		SecretsDir:    filepath.Join(tmpDir, "secrets"),
		BPProfilesDir: filepath.Join(tmpDir, "profiles"),
		SSHConfigFile: filepath.Join(tmpDir, "ssh_config"),
		IDEKeyFile:    filepath.Join(tmpDir, "id_test"),
	}
	_ = paths.EnsureDirectories()
	_ = os.WriteFile(paths.IDEKeyFile, []byte("mock-key"), 0600)
	_ = os.WriteFile(paths.IDEKeyFile+".pub", []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI test-key controller@fleet\n"), 0644)

	t.Setenv("FORGEJO_DISABLED", "1")
	t.Setenv("SONAR_DISABLED", "1")

	// 1. Create with defaults (sshUser empty, modelURL provided)
	var stdout, stderr bytes.Buffer
	code := handleAgentCreate(context.Background(), paths, []string{
		"defaults-node",
		"--baremetal",
		"--ssh-host", "127.0.0.1",
		"--model-url", "http://localhost:8080/v1",
		"--model-name", "qwen",
		"--no-enroll",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("handleAgentCreate with defaults failed: %d, stderr: %s", code, stderr.String())
	}

	cfg, err := config.LoadAgentConfig("defaults-node", paths)
	if err != nil {
		t.Fatalf("failed loading agent config: %v", err)
	}
	if cfg.SSHPort != 22 {
		t.Errorf("expected default SSH port 22, got %d", cfg.SSHPort)
	}
	if cfg.SSHUser == "" {
		t.Errorf("expected non-empty default SSH user")
	}

	// Test handleAgentSSH with baremetal defaults
	var capturedArgs []string
	restoreSSH := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "ssh" {
			capturedArgs = args
			return exec.Command("true")
		}
		return exec.Command("true")
	})
	defer restoreSSH()

	// Clear SSHHost, SSHPort, SSHUser on cfg to test default fallbacks in handleAgentSSH
	cfg.SSHHost = ""
	cfg.SSHPort = 0
	cfg.SSHUser = ""
	_ = config.SaveAgentConfig(cfg, paths)

	stdout.Reset()
	stderr.Reset()
	code = handleAgentSSH(context.Background(), paths, []string{"defaults-node"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("handleAgentSSH with defaults returned %d: %s", code, stderr.String())
	}
	joined := strings.Join(capturedArgs, " ")
	if !strings.Contains(joined, "127.0.0.1") || !strings.Contains(joined, "-p 22") {
		t.Errorf("unexpected fallback ssh args: %s", joined)
	}

	// 2. Cancelled enrollment
	ctxCancel, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	stdout.Reset()
	stderr.Reset()
	code = handleAgentCreate(ctxCancel, paths, []string{
		"cancel-enroll-node",
		"--baremetal",
		"--ssh-host", "127.0.0.1",
		"--enroll-port", "0",
	}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected exit code 1 for cancelled enrollment, got %d", code)
	}
	if !strings.Contains(stderr.String(), "enrollment timed out or cancelled") {
		t.Errorf("expected cancelled message in stderr, got: %s", stderr.String())
	}
}
