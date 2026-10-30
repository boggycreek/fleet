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

	"github.com/boggycreek/sandbox/pkg/config"
	"github.com/boggycreek/sandbox/pkg/runtime"
)

var infraEnvLock sync.Mutex

func mockContainerState(state string, mounts ...runtime.ContainerMount) func() {
	inspectJSON, _ := json.Marshal([]map[string]interface{}{
		{
			"Id": "test-infra-id",
			"State": map[string]interface{}{
				"Status":  state,
				"Running": state == "running",
			},
			"Mounts": mounts,
		},
	})
	return runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) >= 3 && args[0] == "inspect" {
			return exec.Command("echo", string(inspectJSON))
		}
		return exec.Command("true")
	}))
}

func startMockPostgresServer(t *testing.T) (int, func()) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed starting mock postgres listener: %v", err)
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
			_ = conn.Close()
		}
	}()

	return port, func() {
		close(stopCh)
		_ = ln.Close()
	}
}

func TestCheckAndHealPostgresContainer(t *testing.T) {
	infraEnvLock.Lock()
	defer infraEnvLock.Unlock()

	t.Run("PostgresOffline_StatusWarning", func(t *testing.T) {
		restoreMock := mockContainerState("stopped")
		defer restoreMock()

		os.Setenv("POSTGRES_HOST", "127.0.0.1")
		os.Setenv("POSTGRES_PORT", "65504") // Unreachable port

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealPostgresContainer(context.Background(), report)

		if report.WarningCount != 1 {
			t.Fatalf("expected WarningCount=1, got %d", report.WarningCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusWarning {
			t.Fatalf("expected StatusWarning, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "stopped") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("PostgresOnline_StatusOK", func(t *testing.T) {
		restoreMock := mockContainerState("running")
		defer restoreMock()

		port, cleanup := startMockPostgresServer(t)
		defer cleanup()

		os.Setenv("POSTGRES_HOST", "127.0.0.1")
		os.Setenv("POSTGRES_PORT", fmt.Sprintf("%d", port))

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealPostgresContainer(context.Background(), report)

		if report.WarningCount != 0 {
			t.Errorf("expected clean run, got warnings=%d", report.WarningCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "database: sonar") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})
}

func TestCheckAndHealValkeyContainer(t *testing.T) {
	infraEnvLock.Lock()
	defer infraEnvLock.Unlock()

	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	paths := config.Paths{
		DataHome: filepath.Join(tmpDir, "data"),
	}
	_ = paths.EnsureDirectories()

	t.Run("ValkeyOffline_StatusWarning", func(t *testing.T) {
		restoreMock := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.Command("false")
		}))
		defer restoreMock()

		os.Setenv("BP_HOST", "127.0.0.1")
		os.Setenv("BP_PORT", "65501")
		os.Setenv("ADMIN_BACKPLANE_PASSWORD", "testpass")

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealValkeyContainer(canceledCtx, paths, report)

		if report.WarningCount != 1 {
			t.Fatalf("expected WarningCount=1, got %d", report.WarningCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusWarning {
			t.Fatalf("expected StatusWarning, got %+v", report.Checks)
		}
	})

	t.Run("ValkeyOnline_StatusOK", func(t *testing.T) {
		valkeyDir := filepath.Join(paths.DataHome, "valkey")
		restoreMock := mockContainerState("running", runtime.ContainerMount{
			Destination: "/etc/valkey",
			Source:      valkeyDir,
		})
		defer restoreMock()

		port, cleanup := startTestValkeyServer(t, nil)
		defer cleanup()

		os.Setenv("BP_HOST", "127.0.0.1")
		os.Setenv("BP_PORT", fmt.Sprintf("%d", port))
		os.Setenv("ADMIN_BACKPLANE_PASSWORD", "testpass")

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealValkeyContainer(context.Background(), paths, report)

		if report.WarningCount != 0 || report.UnrepairableCount != 0 {
			t.Errorf("expected clean run, got warnings=%d unrepairable=%d", report.WarningCount, report.UnrepairableCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK check, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "admin & operator ACLs verified") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("ValkeyMountMismatch_AutoHeal", func(t *testing.T) {
		// Mock container with wrong mount source to trigger heal path
		restoreMock := mockContainerState("running", runtime.ContainerMount{
			Destination: "/etc/valkey",
			Source:      "/tmp/wrong-source",
		})
		defer restoreMock()

		port, cleanup := startTestValkeyServer(t, nil)
		defer cleanup()

		os.Setenv("BP_HOST", "127.0.0.1")
		os.Setenv("BP_PORT", fmt.Sprintf("%d", port))
		os.Setenv("ADMIN_BACKPLANE_PASSWORD", "testpass")

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealValkeyContainer(context.Background(), paths, report)

		// RecreateValkeyContainer succeeds with mock podman, then reconnects to mock Valkey server -> StatusHealed
		if report.HealedCount != 1 {
			t.Fatalf("expected HealedCount=1, got %d (report: %+v)", report.HealedCount, report.Checks)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusHealed {
			t.Fatalf("expected StatusHealed check, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "Repaired Valkey container mount") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})
}

func TestCheckAndHealForgejoContainer(t *testing.T) {
	infraEnvLock.Lock()
	defer infraEnvLock.Unlock()

	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	paths := config.Paths{
		DataHome: filepath.Join(tmpDir, "data"),
	}

	t.Run("ForgejoOffline_StatusWarning", func(t *testing.T) {
		restoreMock := mockContainerState("stopped")
		defer restoreMock()

		os.Setenv("FORGEJO_URL", "http://127.0.0.1:65502")
		os.Setenv("GITEA_URL", "http://127.0.0.1:65502")

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealForgejoContainer(context.Background(), paths, report)

		if report.WarningCount != 1 {
			t.Fatalf("expected WarningCount=1, got %d", report.WarningCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusWarning {
			t.Fatalf("expected StatusWarning, got %+v", report.Checks)
		}
	})

	t.Run("ForgejoHTTP500_StatusWarning", func(t *testing.T) {
		restoreMock := mockContainerState("running")
		defer restoreMock()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		os.Setenv("FORGEJO_URL", server.URL)
		os.Setenv("GITEA_URL", server.URL)

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealForgejoContainer(context.Background(), paths, report)

		if report.WarningCount != 1 {
			t.Fatalf("expected WarningCount=1, got %d", report.WarningCount)
		}
	})

	t.Run("ForgejoOnline_StatusOK", func(t *testing.T) {
		restoreMock := mockContainerState("running")
		defer restoreMock()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/version":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"version":"1.22.0"}`))
			default:
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer server.Close()

		os.Setenv("FORGEJO_URL", server.URL)
		os.Setenv("GITEA_URL", server.URL)

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealForgejoContainer(context.Background(), paths, report)

		if report.WarningCount != 0 {
			t.Errorf("expected clean run, got warnings=%d", report.WarningCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK check, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "fleet repositories verified") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}

		// Also verify checkAndHealGiteaContainer alias works
		reportAlias := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealGiteaContainer(context.Background(), paths, reportAlias)
		if len(reportAlias.Checks) != 1 || reportAlias.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK on checkAndHealGiteaContainer, got %+v", reportAlias.Checks)
		}
	})
}

func TestCheckAndHealSonarContainer(t *testing.T) {
	infraEnvLock.Lock()
	defer infraEnvLock.Unlock()

	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	paths := config.Paths{
		DataHome: filepath.Join(tmpDir, "data"),
		EnvFile:  filepath.Join(tmpDir, "data", ".env"),
	}
	_ = paths.EnsureDirectories()
	_ = os.WriteFile(paths.EnvFile, []byte(""), 0600)

	t.Run("SonarOffline_StatusWarning", func(t *testing.T) {
		restoreMock := mockContainerState("stopped")
		defer restoreMock()

		os.Setenv("SONAR_HOST_URL", "http://127.0.0.1:65503")

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealSonarContainer(context.Background(), paths, report)

		if report.WarningCount != 1 {
			t.Fatalf("expected WarningCount=1, got %d", report.WarningCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusWarning {
			t.Fatalf("expected StatusWarning, got %+v", report.Checks)
		}
	})

	t.Run("SonarOnline_StatusOK", func(t *testing.T) {
		restoreMock := mockContainerState("running")
		defer restoreMock()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/system/status":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"UP"}`))
			case "/api/users/current":
				w.WriteHeader(http.StatusOK)
			default:
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer server.Close()

		os.Setenv("SONAR_HOST_URL", server.URL)

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealSonarContainer(context.Background(), paths, report)

		if report.WarningCount != 0 || report.UnrepairableCount != 0 {
			t.Errorf("expected clean run, got warnings=%d unrepairable=%d", report.WarningCount, report.UnrepairableCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK check, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "admin: verified") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("SonarDefaultPassword_AutoHeal", func(t *testing.T) {
		restoreMock := mockContainerState("running")
		defer restoreMock()

		var changedPassword bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/system/status":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"UP"}`))
			case "/api/users/current":
				w.WriteHeader(http.StatusUnauthorized)
			case "/api/users/change_password":
				user, pass, ok := r.BasicAuth()
				if ok && user == "admin" && pass == "admin" {
					changedPassword = true
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.WriteHeader(http.StatusBadRequest)
			default:
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer server.Close()

		os.Setenv("SONAR_HOST_URL", server.URL)

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealSonarContainer(context.Background(), paths, report)

		if !changedPassword || report.HealedCount != 1 {
			t.Fatalf("expected password change and HealedCount=1, got changed=%v healed=%d", changedPassword, report.HealedCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusHealed {
			t.Fatalf("expected StatusHealed check, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "auto-healed default admin password") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})
}
