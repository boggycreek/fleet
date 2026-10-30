// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
	"github.com/boggycreek/sandbox/backplane/pkg/libbp/resp"
	"github.com/boggycreek/sandbox/pkg/config"
)

var envLock sync.Mutex

func startTestValkeyServer(t *testing.T, handler func(cmd string, args []resp.Value) (resp.Value, bool)) (int, func()) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed starting mock valkey listener: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	stopCh := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-stopCh:
					return
				default:
					return
				}
			}
			go func(c net.Conn) {
				defer c.Close()
				r := resp.NewReader(c)
				w := resp.NewWriter(c)

				for {
					val, err := r.ReadValue()
					if err != nil {
						return
					}
					args, err := val.AsArray()
					if err != nil || len(args) == 0 {
						continue
					}

					cmd := strings.ToUpper(args[0].String())
					if handler != nil {
						if respVal, handled := handler(cmd, args); handled {
							_ = w.WriteValue(respVal)
							continue
						}
					}

					switch cmd {
					case "AUTH":
						_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "OK"})
					case "PING":
						_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "PONG"})
					case "ACL":
						_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "OK"})
					case "HGETALL":
						_ = w.WriteValue(resp.Value{Type: resp.TypeArray, Array: []resp.Value{}})
					default:
						_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "OK"})
					}
				}
			}(conn)
		}
	}()

	return port, func() {
		close(stopCh)
		_ = ln.Close()
	}
}

func TestCheckAndHealValkey(t *testing.T) {
	envLock.Lock()
	defer envLock.Unlock()

	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	paths := config.Paths{
		DataHome: filepath.Join(tmpDir, "data"),
	}
	_ = paths.EnsureDirectories()

	t.Run("ValkeyOffline_StatusWarning", func(t *testing.T) {
		os.Setenv("BP_HOST", "127.0.0.1")
		os.Setenv("BP_PORT", "65500") // Unreachable port
		os.Setenv("ADMIN_BACKPLANE_PASSWORD", "testpass")

		cfg := &config.AgentConfig{Name: "valkey-offline-agent", Role: "coder"}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealValkey(context.Background(), cfg, paths, report)

		if report.WarningCount != 1 {
			t.Fatalf("expected WarningCount=1, got %d", report.WarningCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusWarning {
			t.Fatalf("expected StatusWarning, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "offline") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("ValkeyHealthy_StatusOK", func(t *testing.T) {
		cfg := &config.AgentConfig{
			Name:         "valkey-ok-agent",
			Role:         "coder",
			Password:     "testpass",
			PublicKeyB64: "dGVzdC1wdWJrZXk=",
		}

		idRec := libbp.IdentityRecord{
			Name:   cfg.Name,
			Role:   cfg.Role,
			Kind:   "agent",
			PubKey: cfg.PublicKeyB64,
		}
		idJSON, _ := json.Marshal(idRec)

		port, cleanup := startTestValkeyServer(t, func(cmd string, args []resp.Value) (resp.Value, bool) {
			if cmd == "GET" && len(args) > 1 && strings.Contains(args[1].String(), cfg.Name) {
				return resp.Value{
					Type: resp.TypeBulkString,
					Str:  string(idJSON),
				}, true
			}
			if cmd == "ACL" && len(args) > 1 && strings.ToUpper(args[1].String()) == "GETUSER" {
				return resp.Value{
					Type: resp.TypeArray,
					Array: []resp.Value{
						{Type: resp.TypeBulkString, Str: "flags"},
						{Type: resp.TypeArray, Array: []resp.Value{{Type: resp.TypeBulkString, Str: "on"}}},
					},
				}, true
			}
			return resp.Value{}, false
		})
		defer cleanup()

		os.Setenv("BP_HOST", "127.0.0.1")
		os.Setenv("BP_PORT", fmt.Sprintf("%d", port))
		os.Setenv("ADMIN_BACKPLANE_PASSWORD", "testpass")

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealValkey(context.Background(), cfg, paths, report)

		if report.HealedCount != 0 || report.UnrepairableCount != 0 {
			t.Errorf("expected clean run, got healed=%d, unrep=%d", report.HealedCount, report.UnrepairableCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK check, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "verified") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("ValkeyNeedsHeal_StatusHealed", func(t *testing.T) {
		cfg := &config.AgentConfig{
			Name:         "valkey-heal-agent",
			Role:         "coder",
			Password:     "testpass",
			PublicKeyB64: "dGVzdC1wdWJrZXk=",
		}

		port, cleanup := startTestValkeyServer(t, nil) // Default returns empty GETUSER / HGETALL -> triggers heal
		defer cleanup()

		os.Setenv("BP_HOST", "127.0.0.1")
		os.Setenv("BP_PORT", fmt.Sprintf("%d", port))
		os.Setenv("ADMIN_BACKPLANE_PASSWORD", "testpass")

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealValkey(context.Background(), cfg, paths, report)

		if report.HealedCount != 1 {
			t.Fatalf("expected HealedCount=1, got %d", report.HealedCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusHealed {
			t.Fatalf("expected StatusHealed, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "Repaired Valkey ACL permissions and registry identity") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("ValkeyACLExecError_Unrepairable", func(t *testing.T) {
		cfg := &config.AgentConfig{
			Name:         "valkey-err-agent",
			Role:         "coder",
			Password:     "testpass",
			PublicKeyB64: "dGVzdC1wdWJrZXk=",
		}

		port, cleanup := startTestValkeyServer(t, func(cmd string, args []resp.Value) (resp.Value, bool) {
			if cmd == "ACL" && len(args) > 1 && strings.ToUpper(args[1].String()) == "SETUSER" {
				return resp.Value{Type: resp.TypeError, Str: "ERR syntax error"}, true
			}
			return resp.Value{}, false
		})
		defer cleanup()

		os.Setenv("BP_HOST", "127.0.0.1")
		os.Setenv("BP_PORT", fmt.Sprintf("%d", port))
		os.Setenv("ADMIN_BACKPLANE_PASSWORD", "testpass")

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealValkey(context.Background(), cfg, paths, report)

		if report.UnrepairableCount != 1 {
			t.Fatalf("expected UnrepairableCount=1, got %d", report.UnrepairableCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusError || !report.Checks[0].Unrepairable {
			t.Fatalf("expected StatusError with Unrepairable=true, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "Failed setting Valkey ACL") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("ValkeyRegisterIdentityError_Unrepairable", func(t *testing.T) {
		cfg := &config.AgentConfig{
			Name:         "valkey-reg-err-agent",
			Role:         "coder",
			Password:     "testpass",
			PublicKeyB64: "dGVzdC1wdWJrZXk=",
		}

		port, cleanup := startTestValkeyServer(t, func(cmd string, args []resp.Value) (resp.Value, bool) {
			if cmd == "SET" && len(args) > 1 && strings.Contains(args[1].String(), "identity:") {
				return resp.Value{Type: resp.TypeError, Str: "ERR readonly replica"}, true
			}
			return resp.Value{}, false
		})
		defer cleanup()

		os.Setenv("BP_HOST", "127.0.0.1")
		os.Setenv("BP_PORT", fmt.Sprintf("%d", port))
		os.Setenv("ADMIN_BACKPLANE_PASSWORD", "testpass")

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealValkey(context.Background(), cfg, paths, report)

		if report.UnrepairableCount != 1 {
			t.Fatalf("expected UnrepairableCount=1, got %d", report.UnrepairableCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusError || !report.Checks[0].Unrepairable {
			t.Fatalf("expected StatusError with Unrepairable=true, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "Failed registering Valkey identity") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})
}

func TestCheckAndHealForgejo(t *testing.T) {
	envLock.Lock()
	defer envLock.Unlock()

	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		SSHDir:        filepath.Join(tmpDir, "ssh"),
		IDEKeyFile:    filepath.Join(tmpDir, "ssh", "fleet"),
		SSHPrivateKey: filepath.Join(tmpDir, "ssh", "fleet"),
	}
	_ = paths.EnsureDirectories()
	_ = os.WriteFile(paths.SSHPrivateKey+".pub", []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 mock-pub"), 0644)

	t.Run("NoContainer_StatusOK", func(t *testing.T) {
		cfg := &config.AgentConfig{Name: "host-agent", Runtime: "host", HostOnly: true}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealForgejo(context.Background(), cfg, paths, report)

		if len(report.Checks) != 1 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK for no-container agent, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "no Forgejo account required") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("ForgejoOffline_StatusWarning", func(t *testing.T) {
		os.Setenv("FORGEJO_URL", "http://127.0.0.1:65506")
		os.Setenv("GITEA_URL", "http://127.0.0.1:65506")

		cfg := &config.AgentConfig{Name: "offline-agent"}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealForgejo(context.Background(), cfg, paths, report)

		if report.WarningCount != 1 {
			t.Fatalf("expected WarningCount=1, got %d", report.WarningCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusWarning {
			t.Fatalf("expected StatusWarning, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "offline") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("ForgejoVersion500_StatusWarning", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		os.Setenv("FORGEJO_URL", server.URL)
		os.Setenv("GITEA_URL", server.URL)

		cfg := &config.AgentConfig{Name: "err-agent"}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealForgejo(context.Background(), cfg, paths, report)

		if report.WarningCount != 1 {
			t.Fatalf("expected WarningCount=1, got %d", report.WarningCount)
		}
		if !strings.Contains(report.Checks[0].Message, "offline") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("ForgejoHealthy_StatusOK", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/version":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"version":"1.22.0"}`))
			case "/api/v1/users/healthy-forgejo-agent":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"username":"healthy-forgejo-agent"}`))
			case "/api/v1/users/healthy-forgejo-agent/keys":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[{"title":"healthy-forgejo-agent-ide-key","key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 mock-pub"}]`))
			case "/api/v1/orgs/fleet/members/healthy-forgejo-agent":
				w.WriteHeader(http.StatusNoContent)
			default:
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer server.Close()

		os.Setenv("FORGEJO_URL", server.URL)
		os.Setenv("GITEA_URL", server.URL)

		cfg := &config.AgentConfig{Name: "healthy-forgejo-agent"}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealForgejo(context.Background(), cfg, paths, report)

		if report.UnrepairableCount != 0 || report.WarningCount != 0 {
			t.Errorf("expected clean run, got unrep=%d, warn=%d", report.UnrepairableCount, report.WarningCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK check, got %+v", report.Checks)
		}
	})

	t.Run("ForgejoEnsureUserFailure_Unrepairable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/version" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"version":"1.22.0"}`))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		os.Setenv("FORGEJO_URL", server.URL)
		os.Setenv("GITEA_URL", server.URL)

		cfg := &config.AgentConfig{Name: "fail-user-agent"}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealForgejo(context.Background(), cfg, paths, report)

		if report.UnrepairableCount != 1 {
			t.Fatalf("expected UnrepairableCount=1, got %d", report.UnrepairableCount)
		}
		if !strings.Contains(report.Checks[0].Message, "Failed ensuring Forgejo user") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})
}

func TestCheckAndHealSonar(t *testing.T) {
	envLock.Lock()
	defer envLock.Unlock()

	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	paths := config.Paths{
		DataHome:   filepath.Join(tmpDir, "data"),
		AgentsDir:  filepath.Join(tmpDir, "data", "agents"),
		SecretsDir: filepath.Join(tmpDir, "data", "secrets"),
	}
	_ = paths.EnsureDirectories()

	t.Run("NoContainer_StatusOK", func(t *testing.T) {
		cfg := &config.AgentConfig{Name: "sonar-host-agent", Runtime: "host", HostOnly: true}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealSonar(context.Background(), cfg, paths, report)

		if len(report.Checks) != 1 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK for no-container agent, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "no SonarQube account required") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("SonarDisabled_StatusOK", func(t *testing.T) {
		os.Setenv("SONAR_DISABLED", "1")
		defer os.Unsetenv("SONAR_DISABLED")

		cfg := &config.AgentConfig{Name: "sonar-disabled-agent"}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealSonar(context.Background(), cfg, paths, report)

		if len(report.Checks) != 1 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK when sonar disabled, got %+v", report.Checks)
		}
	})

	t.Run("SonarOffline_StatusWarning", func(t *testing.T) {
		os.Setenv("SONAR_HOST_URL", "http://127.0.0.1:65505")
		cfg := &config.AgentConfig{Name: "offline-sonar-agent"}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealSonar(context.Background(), cfg, paths, report)

		if report.WarningCount != 1 {
			t.Fatalf("expected WarningCount=1, got %d", report.WarningCount)
		}
		if !strings.Contains(report.Checks[0].Message, "offline") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("SonarHealthy_StatusOK", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/system/status":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"UP"}`))
			case "/api/users/current":
				w.WriteHeader(http.StatusOK)
			case "/api/users/create":
				w.WriteHeader(http.StatusOK)
			default:
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer server.Close()

		os.Setenv("SONAR_HOST_URL", server.URL)
		cfg := &config.AgentConfig{
			Name:       "healthy-sonar-agent",
			SonarToken: "sqa_existing_token_123",
		}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealSonar(context.Background(), cfg, paths, report)

		if report.HealedCount != 0 || report.UnrepairableCount != 0 {
			t.Errorf("expected clean run, got healed=%d, unrep=%d", report.HealedCount, report.UnrepairableCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK check, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "verified") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("SonarMissingToken_HealsToken", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/system/status":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"UP"}`))
			case "/api/users/current":
				w.WriteHeader(http.StatusOK)
			case "/api/users/create":
				w.WriteHeader(http.StatusOK)
			case "/api/user_tokens/generate":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"token":"sqa_generated_token_456"}`))
			default:
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer server.Close()

		os.Setenv("SONAR_HOST_URL", server.URL)
		cfg := &config.AgentConfig{
			Name:       "heal-sonar-agent",
			SonarToken: "",
		}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealSonar(context.Background(), cfg, paths, report)

		if report.HealedCount != 1 {
			t.Fatalf("expected HealedCount=1, got %d", report.HealedCount)
		}
		if cfg.SonarToken != "sqa_generated_token_456" {
			t.Errorf("expected token to be updated in cfg, got %q", cfg.SonarToken)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusHealed {
			t.Fatalf("expected StatusHealed check, got %+v", report.Checks)
		}
	})

	t.Run("SonarGenerateTokenFails_Unrepairable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/system/status":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"UP"}`))
			case "/api/users/current":
				w.WriteHeader(http.StatusOK)
			case "/api/users/create":
				w.WriteHeader(http.StatusOK)
			case "/api/user_tokens/generate":
				w.WriteHeader(http.StatusInternalServerError)
			default:
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer server.Close()

		os.Setenv("SONAR_HOST_URL", server.URL)
		cfg := &config.AgentConfig{
			Name:       "fail-token-sonar-agent",
			SonarToken: "",
		}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealSonar(context.Background(), cfg, paths, report)

		if report.UnrepairableCount != 1 {
			t.Fatalf("expected UnrepairableCount=1, got %d", report.UnrepairableCount)
		}
		if !strings.Contains(report.Checks[0].Message, "Failed generating SonarQube token") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})
}
