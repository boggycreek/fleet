// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package lifecycle

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/boggycreek/fleet/cli/pkg/config"
	sndbxRuntime "github.com/boggycreek/fleet/cli/pkg/runtime"
)

func TestLifecycleNoContainerAndDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		// #nosec G204 -- test cleanup hook
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	t.Setenv("HOME", tmpDir)

	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SecretsDir:    filepath.Join(tmpDir, "data", "secrets"),
		SSHDir:        filepath.Join(tmpDir, "ssh"),
		IDEKeyFile:    filepath.Join(tmpDir, "ssh", "fleet"),
		SSHPrivateKey: filepath.Join(tmpDir, "ssh", "fleet"),
		SSHConfigFile: filepath.Join(tmpDir, "ssh", "config"),
	}
	_ = paths.EnsureDirectories()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Mock CommandExecutor to avoid running live host podman commands
	mockExec := sndbxRuntime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("true")
	})
	ctx = sndbxRuntime.WithCommandExecutor(ctx, mockExec)

	// 1. Agent configured with Runtime = "no-container" (IsNoContainer() == true)
	cfg := &config.AgentConfig{
		Name:     "nocontainer-agent",
		Runtime:  "no-container",
		Role:     "coder",
		Password: "secret123",
	}
	_ = config.SaveAgentConfig(cfg, paths)

	// RegisterForgejoUser and RegisterSonarUser directly with no-container agent
	if err := RegisterForgejoUser(ctx, cfg, paths); err != nil {
		t.Fatalf("RegisterForgejoUser should succeed for no-container agent: %v", err)
	}
	if err := RegisterSonarUser(ctx, cfg, paths); err != nil {
		t.Fatalf("RegisterSonarUser should succeed for no-container agent: %v", err)
	}

	// Offline backplane to make Valkey skip
	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", "65530")

	// ProvisionAgent for no-container
	provRep := ProvisionAgent(ctx, cfg, paths)
	if provRep.HasErrors() {
		t.Errorf("expected no errors for no-container provision, got: %s", FormatReport(provRep))
	}

	// DeprovisionAgent for no-container
	deprovRep := DeprovisionAgent(ctx, cfg, paths)
	if deprovRep.HasErrors() {
		t.Errorf("expected no errors for no-container deprovision, got: %s", FormatReport(deprovRep))
	}

	// 2. Container agent with FORGEJO_DISABLED=1 and SONAR_DISABLED=1
	t.Setenv("FORGEJO_DISABLED", "1")
	t.Setenv("SONAR_DISABLED", "1")

	cfg2 := &config.AgentConfig{
		Name:          "disabled-agent",
		Runtime:       "container",
		ContainerName: "disabled-agent-cnt",
		VolumeName:    "disabled-agent-vol",
		Role:          "coder",
		Password:      "secret456",
	}
	_ = config.SaveAgentConfig(cfg2, paths)

	if err := RegisterSonarUser(ctx, cfg2, paths); err != nil {
		t.Fatalf("RegisterSonarUser should return nil when SONAR_DISABLED=1: %v", err)
	}

	provRep2 := ProvisionAgent(ctx, cfg2, paths)
	if provRep2.HasErrors() {
		t.Errorf("expected no errors when services disabled: %s", FormatReport(provRep2))
	}

	deprovRep2 := DeprovisionAgent(ctx, cfg2, paths)
	if deprovRep2.HasErrors() {
		t.Errorf("expected no errors when deprovisioning with disabled services: %s", FormatReport(deprovRep2))
	}

	// 3. Baremetal agent with FORGEJO_DISABLED=1 and SONAR_DISABLED=1
	cfgBare := &config.AgentConfig{
		Name:      "bare-lifecycle-node",
		Baremetal: true,
		Runtime:   "baremetal",
		SSHHost:   "10.0.0.99",
		SSHPort:   22,
		SSHUser:   "nodeadmin",
		Password:  "baresecret789",
	}
	_ = config.SaveAgentConfig(cfgBare, paths)

	provRepBare := ProvisionAgent(ctx, cfgBare, paths)
	if provRepBare.HasErrors() {
		t.Errorf("expected no errors when provisioning baremetal agent: %s", FormatReport(provRepBare))
	}

	deprovRepBare := DeprovisionAgent(ctx, cfgBare, paths)
	if deprovRepBare.HasErrors() {
		t.Errorf("expected no errors when deprovisioning baremetal agent: %s", FormatReport(deprovRepBare))
	}
}

func TestDeprovisionNotFound404Handling(t *testing.T) {
	// 1. Forgejo user 404
	mockForgejo404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message": "user does not exist (404)"}`))
	}))
	defer mockForgejo404.Close()

	t.Setenv("FORGEJO_URL", mockForgejo404.URL)
	t.Setenv("ADMIN_BACKPLANE_PASSWORD", "testpass")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := DeprovisionForgejoUser(ctx, "ghost-agent"); err != nil {
		t.Fatalf("expected nil error on 404 user does not exist, got: %v", err)
	}

	// 2. SonarQube user 404
	mockSonar404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/user_tokens/revoke" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors": [{"msg": "user not found"}]}`))
	}))
	defer mockSonar404.Close()

	t.Setenv("SONAR_HOST_URL", mockSonar404.URL)
	if err := DeprovisionSonarUser(ctx, "ghost-agent"); err != nil {
		t.Fatalf("expected nil error on Sonar 404 user not found, got: %v", err)
	}
}

func TestRegisterSonarUserEmptyToken(t *testing.T) {
	mockSonarEmptyToken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/system/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status": "UP"}`))
		case "/api/users/create":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"user": {"login": "token-agent"}}`))
		case "/api/user_tokens/generate":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"token": ""}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer mockSonarEmptyToken.Close()

	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:   filepath.Join(tmpDir, "data"),
		AgentsDir:  filepath.Join(tmpDir, "data", "agents"),
		SecretsDir: filepath.Join(tmpDir, "data", "secrets"),
	}
	_ = paths.EnsureDirectories()

	t.Setenv("SONAR_HOST_URL", mockSonarEmptyToken.URL)
	t.Setenv("SONAR_DISABLED", "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := &config.AgentConfig{Name: "token-agent", Password: "testpassword"}
	_ = config.SaveAgentConfig(cfg, paths)

	if err := RegisterSonarUser(ctx, cfg, paths); err != nil {
		t.Fatalf("RegisterSonarUser failed on empty token: %v", err)
	}
	if cfg.SonarToken != "" {
		t.Fatalf("expected empty SonarToken, got: %s", cfg.SonarToken)
	}
}

func TestEnsureIDEKeypairCorruptFallback(t *testing.T) {
	tmpDir := t.TempDir()
	paths := config.Paths{
		SSHPrivateKey: filepath.Join(tmpDir, "fleet"),
		IDEKeyFile:    filepath.Join(tmpDir, "fleet"),
	}

	// Write invalid private key
	_ = os.WriteFile(paths.SSHPrivateKey, []byte("NOT_A_VALID_PRIVATE_KEY"), 0600)

	// Case 1: .pub file does NOT exist -> EnsureIDEKeypair should fail
	if err := EnsureIDEKeypair(paths); err == nil {
		t.Fatalf("expected EnsureIDEKeypair to fail when private key is corrupt and pub file is missing")
	}

	// Case 2: .pub file exists and is non-empty -> EnsureIDEKeypair should succeed (fallback)
	pubFile := paths.SSHPrivateKey + ".pub"
	_ = os.WriteFile(pubFile, []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA existing-pub-key"), 0644)
	if err := EnsureIDEKeypair(paths); err != nil {
		t.Fatalf("expected EnsureIDEKeypair to return nil when .pub exists: %v", err)
	}
}

func TestEnsureKeysFileSystemErrors(t *testing.T) {
	tmpDir := t.TempDir()
	// Block directory creation by creating a file where secrets dir should be
	blockingFile := filepath.Join(tmpDir, "blocking_file")
	_ = os.WriteFile(blockingFile, []byte("blocker"), 0600)

	paths := config.Paths{
		DataHome:   tmpDir,
		SecretsDir: blockingFile,
	}

	cfg := &config.AgentConfig{Name: "agent-err"}
	if err := EnsureAgentKeys(cfg, paths); err == nil {
		t.Errorf("expected EnsureAgentKeys to fail when SecretsDir is blocked by file")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := EnsureOperatorKeys(ctx, paths, "operator-err"); err == nil {
		t.Errorf("expected EnsureOperatorKeys to fail when SecretsDir is blocked by file")
	}

	if os.Geteuid() == 0 {
		t.Skip("skipping read-only directory test under root")
	}

	// Test read-only secret directory write failures
	secretsDir := filepath.Join(tmpDir, "readonly_secrets")
	agentSecretDir := filepath.Join(secretsDir, "agent-ro")
	_ = os.MkdirAll(agentSecretDir, 0700)
	_ = os.Chmod(agentSecretDir, 0500)
	defer func() { _ = os.Chmod(agentSecretDir, 0700) }()

	pathsRO := config.Paths{
		DataHome:   tmpDir,
		SecretsDir: secretsDir,
	}
	cfgRO := &config.AgentConfig{Name: "agent-ro"}
	if err := EnsureAgentKeys(cfgRO, pathsRO); err == nil {
		t.Errorf("expected EnsureAgentKeys to fail when secret directory is read-only")
	}

	opSecretDir := filepath.Join(secretsDir, "operator-ro")
	_ = os.MkdirAll(opSecretDir, 0700)
	_ = os.Chmod(opSecretDir, 0500)
	defer func() { _ = os.Chmod(opSecretDir, 0700) }()
	if _, err := EnsureOperatorKeys(ctx, pathsRO, "operator-ro"); err == nil {
		t.Errorf("expected EnsureOperatorKeys to fail when secret directory is read-only")
	}
}

func TestEnsureIDEKeypairInvalidPath(t *testing.T) {
	tmpDir := t.TempDir()
	fileAsDir := filepath.Join(tmpDir, "blocker_file")
	_ = os.WriteFile(fileAsDir, []byte("not_a_dir"), 0600)

	paths := config.Paths{
		SSHPrivateKey: filepath.Join(fileAsDir, "fleet"),
		IDEKeyFile:    filepath.Join(fileAsDir, "fleet"),
	}

	if err := EnsureIDEKeypair(paths); err == nil {
		t.Errorf("expected EnsureIDEKeypair to fail when key path cannot be created")
	}
}

func TestRegisterForgejoUserAddKeyFailure(t *testing.T) {
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		IDEKeyFile:    filepath.Join(tmpDir, "fleet"),
		SSHPrivateKey: filepath.Join(tmpDir, "fleet"),
	}
	_ = os.WriteFile(fmt.Sprintf("%s.pub", paths.SSHPrivateKey), []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 key-fail"), 0600)

	mockForgejoKeyErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/users/keyfail-agent" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": 1, "username": "keyfail-agent"}`))
			return
		}
		if r.URL.Path == "/api/v1/admin/users/keyfail-agent/keys" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message": "failed inserting ssh key"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer mockForgejoKeyErr.Close()

	t.Setenv("FORGEJO_URL", mockForgejoKeyErr.URL)
	t.Setenv("FORGEJO_DISABLED", "")
	t.Setenv("GITEA_DISABLED", "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := &config.AgentConfig{Name: "keyfail-agent", Password: "testpassword"}
	if err := RegisterForgejoUser(ctx, cfg, paths); err == nil {
		t.Errorf("expected RegisterForgejoUser to fail when AddUserSSHKey fails")
	}
}
