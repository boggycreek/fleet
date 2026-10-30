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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/test/harness"
	"github.com/boggycreek/fleet/cli/pkg/config"
)

func TestProvisionAgentAndDeprovisionAgentHappyPath(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

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
	if err := EnsureIDEKeypair(paths); err != nil {
		t.Fatalf("failed generating IDE keypair: %v", err)
	}

	// Mock Gitea
	mockGitea := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/users/happy-agent":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": 1, "username": "happy-agent"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/users/happy-agent/keys":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 1, "title": "happy-agent-ide-key"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/orgs/fleet/members/happy-agent":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/admin/users/happy-agent":
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer mockGitea.Close()

	// Mock SonarQube
	mockSonar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/system/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status": "UP"}`))
		case "/api/users/create":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"user": {"login": "happy-agent"}}`))
		case "/api/user_tokens/generate":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"token": "mock-token-xyz"}`))
		case "/api/user_tokens/revoke":
			w.WriteHeader(http.StatusNoContent)
		case "/api/users/deactivate":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"user": {"login": "happy-agent", "active": false}}`))
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer mockSonar.Close()

	t.Setenv("ADMIN_BACKPLANE_PASSWORD", valkey.AdminPass)
	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", fmt.Sprintf("%d", valkey.Port))
	t.Setenv("GITEA_URL", mockGitea.URL)
	t.Setenv("SONAR_HOST_URL", mockSonar.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg, err := config.NewAgentConfig("happy-agent", "base", "coder")
	if err != nil {
		t.Fatalf("failed creating agent config: %v", err)
	}
	if err := config.SaveAgentConfig(cfg, paths); err != nil {
		t.Fatalf("failed saving agent config: %v", err)
	}

	// 1. ProvisionAgent
	provReport := ProvisionAgent(ctx, cfg, paths)
	if provReport.HasErrors() {
		t.Errorf("expected no errors in ProvisionAgent: %s", FormatReport(provReport))
	}
	if provReport.SuccessCount != 5 {
		t.Errorf("expected 5 successful steps in ProvisionAgent, got %d", provReport.SuccessCount)
	}
	outText := FormatReport(provReport)
	if !strings.Contains(outText, "[✓]") || !strings.Contains(outText, "Valkey ACL & Identity") {
		t.Errorf("unexpected provision report format: %s", outText)
	}

	// 2. DeprovisionAgent
	deprovReport := DeprovisionAgent(ctx, cfg, paths)
	if deprovReport.HasErrors() {
		t.Errorf("expected no errors in DeprovisionAgent: %s", FormatReport(deprovReport))
	}
	if deprovReport.SuccessCount < 6 {
		t.Errorf("expected >= 6 successful steps in DeprovisionAgent, got %d", deprovReport.SuccessCount)
	}
	deprovText := FormatReport(deprovReport)
	if !strings.Contains(deprovText, "[✓]") || !strings.Contains(deprovText, "Valkey Identity") {
		t.Errorf("unexpected deprovision report format: %s", deprovText)
	}
}

func TestProvisionAgentAndDeprovisionAgentOfflineServices(t *testing.T) {
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		// #nosec G204 -- test cleanup hook
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

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

	// Direct ports to offline addresses
	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", "65530")
	t.Setenv("GITEA_URL", "http://127.0.0.1:65531")
	t.Setenv("SONAR_HOST_URL", "http://127.0.0.1:65532")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg, err := config.NewAgentConfig("offline-agent", "base", "coder")
	if err != nil {
		t.Fatalf("failed creating agent config: %v", err)
	}
	_ = config.SaveAgentConfig(cfg, paths)

	// 1. ProvisionAgent with offline services
	provReport := ProvisionAgent(ctx, cfg, paths)
	if provReport.HasErrors() {
		t.Errorf("expected offline services to be skipped rather than fatal errors: %+v", provReport)
	}
	if provReport.SkippedCount != 3 {
		t.Errorf("expected 3 skipped steps in ProvisionAgent, got %d", provReport.SkippedCount)
	}
	provText := FormatReport(provReport)
	if !strings.Contains(provText, "[-]") || !strings.Contains(provText, "offline (skipped") {
		t.Errorf("expected skipped markers in provision report:\n%s", provText)
	}

	// 2. DeprovisionAgent with offline services
	deprovReport := DeprovisionAgent(ctx, cfg, paths)
	if deprovReport.HasErrors() {
		t.Errorf("expected offline services in DeprovisionAgent to not report errors: %+v", deprovReport)
	}
	if deprovReport.SkippedCount != 3 {
		t.Errorf("expected 3 skipped steps in DeprovisionAgent, got %d", deprovReport.SkippedCount)
	}
	deprovText := FormatReport(deprovReport)
	if !strings.Contains(deprovText, "[-]") || !strings.Contains(deprovText, "offline (skipped") {
		t.Errorf("expected skipped markers in deprovision report:\n%s", deprovText)
	}
}

func TestProvisionAgentAndDeprovisionAgentErrorPaths(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	tmpDir := t.TempDir()
	t.Cleanup(func() {
		// #nosec G204 -- test cleanup hook
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

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

	// Gitea returning 500 error
	mockGiteaErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message": "internal server error"}`))
	}))
	defer mockGiteaErr.Close()

	// Sonar returning UP on system status, but 500 on user create / deactivate
	mockSonarErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/system/status" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status": "UP"}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"errors": [{"msg": "internal error"}]}`))
	}))
	defer mockSonarErr.Close()

	// Invalid Valkey password to force auth failure (non-offline error)
	t.Setenv("ADMIN_BACKPLANE_PASSWORD", "wrong-password")
	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", fmt.Sprintf("%d", valkey.Port))
	t.Setenv("GITEA_URL", mockGiteaErr.URL)
	t.Setenv("SONAR_HOST_URL", mockSonarErr.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg, err := config.NewAgentConfig("err-agent", "base", "coder")
	if err != nil {
		t.Fatalf("failed creating agent config: %v", err)
	}

	// 1. ProvisionAgent errors
	provReport := ProvisionAgent(ctx, cfg, paths)
	if !provReport.HasErrors() {
		t.Errorf("expected HasErrors to be true when services return 500 / auth failure")
	}
	if provReport.ErrorCount != 3 {
		t.Errorf("expected 3 error steps in ProvisionAgent, got %d", provReport.ErrorCount)
	}
	provText := FormatReport(provReport)
	if !strings.Contains(provText, "[✗]") {
		t.Errorf("expected error marker [✗] in provision report:\n%s", provText)
	}

	// 2. DeprovisionAgent errors
	deprovReport := DeprovisionAgent(ctx, cfg, paths)
	if !deprovReport.HasErrors() {
		t.Errorf("expected HasErrors to be true when deprovision services fail with 500 / auth failure")
	}
	deprovText := FormatReport(deprovReport)
	if !strings.Contains(deprovText, "[✗]") {
		t.Errorf("expected error marker [✗] in deprovision report:\n%s", deprovText)
	}
}
