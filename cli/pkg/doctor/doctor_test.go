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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp/resp"
	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/runtime"
)

func startMockValkeyForDoctor(t *testing.T) (int, func()) {
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
					switch cmd {
					case "AUTH":
						_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "OK"})
					case "PING":
						_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "PONG"})
					case "ACL":
						_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "OK"})
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

func startMockDoctorPostgres(t *testing.T) (int, func()) {
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

func TestDiagnoseAndHealAgent(t *testing.T) {
	vkPort, vkCleanup := startMockValkeyForDoctor(t)
	defer vkCleanup()

	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	os.Setenv("ADMIN_BACKPLANE_PASSWORD", "mockpass")
	os.Setenv("BP_HOST", "127.0.0.1")
	os.Setenv("BP_PORT", fmt.Sprintf("%d", vkPort))
	os.Setenv("SONAR_HOST_URL", "http://127.0.0.1:65505")
	os.Setenv("FORGEJO_URL", "http://127.0.0.1:65506")

	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SecretsDir:    filepath.Join(tmpDir, "data", "secrets"),
		BinDir:        filepath.Join(tmpDir, "bin"),
		SSHDir:        filepath.Join(tmpDir, "ssh"),
		IDEKeyFile:    filepath.Join(tmpDir, "ssh", "fleet"),
		SSHPrivateKey: filepath.Join(tmpDir, "ssh", "fleet"),
		SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
		BPProfilesDir: filepath.Join(tmpDir, "data", "profiles"),
	}
	_ = paths.EnsureDirectories()

	restorePodman := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("true")
	}))
	defer restorePodman()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Empty agent name returns error
	if _, err := DiagnoseAndHealAgent(ctx, "", paths); err == nil {
		t.Errorf("expected error on empty agent name")
	}

	// 2. Nonexistent agent reports unrepairable
	report, err := DiagnoseAndHealAgent(ctx, "nonexistent", paths)
	if err != nil {
		t.Errorf("unexpected error on diagnosing nonexistent agent: %v", err)
	}
	if report.UnrepairableCount == 0 {
		t.Errorf("expected unrepairable issues for nonexistent agent")
	}

	// 3. Corrupted agent JSON
	corrupted := filepath.Join(paths.AgentsDir, "corrupted.json")
	_ = os.WriteFile(corrupted, []byte("{broken-json"), 0600)
	report, _ = DiagnoseAndHealAgent(ctx, "corrupted", paths)
	if report.UnrepairableCount == 0 {
		t.Errorf("expected unrepairable issue for corrupted JSON")
	}

	// 4. Create an agent config with missing fields & bad key permissions (auto-healing test)
	cfg, err := config.NewAgentConfig("heal-agent", "base", "coder")
	if err != nil {
		t.Fatalf("failed creating agent config: %v", err)
	}
	cfg.Password = ""
	_ = config.SaveAgentConfig(cfg, paths)
	_ = os.Chmod(filepath.Join(paths.SecretsDir, cfg.Name, "signing-key.pem"), 0644)

	report, err = DiagnoseAndHealAgent(ctx, "heal-agent", paths)
	if err != nil {
		t.Fatalf("DiagnoseAndHealAgent failed on heal-agent: %v", err)
	}
	if report.HealedCount == 0 {
		t.Errorf("expected auto-healed issues for heal-agent")
	}

	// 5. Healthy agent on second run
	healthyReport, err := DiagnoseAndHealAgent(ctx, "heal-agent", paths)
	if err != nil {
		t.Fatalf("DiagnoseAndHealAgent failed on second pass: %v", err)
	}
	if healthyReport.UnrepairableCount > 0 {
		t.Errorf("expected 0 unrepairable issues on healed agent")
	}

	// 6. Offline service warning
	os.Setenv("BP_PORT", "65500")
	offlineReport, _ := DiagnoseAndHealAgent(ctx, "heal-agent", paths)
	if offlineReport.WarningCount == 0 {
		t.Errorf("expected warning count > 0 when Valkey is offline")
	}
}

func TestDiagnoseAndHealInfra(t *testing.T) {
	vkPort, vkCleanup := startMockValkeyForDoctor(t)
	defer vkCleanup()

	pgPort, pgCleanup := startMockDoctorPostgres(t)
	defer pgCleanup()

	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	os.Setenv("ADMIN_BACKPLANE_PASSWORD", "mockpass")
	os.Setenv("BP_HOST", "127.0.0.1")
	os.Setenv("BP_PORT", fmt.Sprintf("%d", vkPort))
	os.Setenv("SONAR_HOST_URL", "http://127.0.0.1:65505")
	os.Setenv("FORGEJO_URL", "http://127.0.0.1:65506")
	os.Setenv("POSTGRES_HOST", "127.0.0.1")
	os.Setenv("POSTGRES_PORT", fmt.Sprintf("%d", pgPort))

	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SecretsDir:    filepath.Join(tmpDir, "data", "secrets"),
		BinDir:        filepath.Join(tmpDir, "bin"),
		EnvFile:       filepath.Join(tmpDir, "data", ".env"),
		SSHDir:        filepath.Join(tmpDir, "ssh"),
		IDEKeyFile:    filepath.Join(tmpDir, "ssh", "fleet"),
		SSHPrivateKey: filepath.Join(tmpDir, "ssh", "fleet"),
		SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
		BPProfilesDir: filepath.Join(tmpDir, "data", "profiles"),
	}

	restorePodman := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) >= 2 && args[0] == "port" {
			return exec.Command("echo", "127.0.0.1:2222")
		}
		if name == "podman" && len(args) >= 3 && args[0] == "container" && args[1] == "inspect" {
			return exec.Command("echo", `[{"State":{"Status":"running","Running":true}}]`)
		}
		return exec.Command("true")
	}))
	defer restorePodman()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Initial run: heals missing .env and missing storage
	report1, err := DiagnoseAndHealInfra(ctx, paths)
	if err != nil {
		t.Fatalf("DiagnoseAndHealInfra failed: %v", err)
	}
	if report1.HealedCount == 0 {
		t.Errorf("expected healed items for initial infra run")
	}

	// 2. Permission heal for .env (0644 -> 0600)
	_ = os.Chmod(paths.EnvFile, 0644)
	reportEnvPerm := &DoctorReport{AgentName: "shared-infrastructure"}
	checkAndHealInfraEnv(paths, reportEnvPerm)
	if reportEnvPerm.HealedCount == 0 {
		t.Errorf("expected permission auto-healing for .env file")
	}

	// 3. Second run with mock services running: clean run
	report2, err := DiagnoseAndHealInfra(ctx, paths)
	if err != nil {
		t.Fatalf("DiagnoseAndHealInfra second pass failed: %v", err)
	}
	if report2.UnrepairableCount > 0 {
		t.Errorf("expected 0 unrepairable items on second pass, got %d", report2.UnrepairableCount)
	}
}

func TestCheckAndHealConfig(t *testing.T) {
	t.Parallel()
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

	ctx := context.Background()

	t.Run("MissingConfig_Unrepairable", func(t *testing.T) {
		report := &DoctorReport{AgentName: "missing-cfg"}
		cfg, err := checkAndHealConfig(ctx, paths, "missing-cfg", report)
		if err == nil || cfg != nil {
			t.Errorf("expected error on missing config")
		}
		if report.UnrepairableCount != 1 {
			t.Errorf("expected 1 unrepairable item, got %d", report.UnrepairableCount)
		}
	})

	t.Run("CorruptedJSON_Unrepairable", func(t *testing.T) {
		corruptPath := filepath.Join(paths.AgentsDir, "corrupted-cfg.json")
		_ = os.WriteFile(corruptPath, []byte("invalid-json{"), 0600)

		report := &DoctorReport{AgentName: "corrupted-cfg"}
		cfg, err := checkAndHealConfig(ctx, paths, "corrupted-cfg", report)
		if err == nil || cfg != nil {
			t.Errorf("expected error on corrupted config JSON")
		}
		if report.UnrepairableCount != 1 {
			t.Errorf("expected 1 unrepairable item, got %d", report.UnrepairableCount)
		}
	})

	t.Run("PermissionsHeal_0644_to_0600", func(t *testing.T) {
		cfg, err := config.NewAgentConfig("perm-cfg-agent", "base", "coder")
		if err != nil {
			t.Fatalf("failed creating agent config: %v", err)
		}
		_ = config.SaveAgentConfig(cfg, paths)
		cfgPath := filepath.Join(paths.AgentsDir, fmt.Sprintf("%s.json", cfg.Name))
		_ = os.Chmod(cfgPath, 0644)

		report := &DoctorReport{AgentName: cfg.Name}
		repaired, err := checkAndHealConfig(ctx, paths, cfg.Name, report)
		if err != nil || repaired == nil {
			t.Fatalf("checkAndHealConfig failed: %v", err)
		}
		if report.HealedCount == 0 {
			t.Errorf("expected permission auto-healing for config JSON")
		}
		if fi, err := os.Stat(cfgPath); err != nil || fi.Mode().Perm() != 0600 {
			t.Errorf("expected config permissions 0600, got: %o", fi.Mode().Perm())
		}
	})

	t.Run("MissingFields_Healed", func(t *testing.T) {
		incomplete := &config.AgentConfig{Name: "missing-fields"}
		cfgPath := filepath.Join(paths.AgentsDir, "missing-fields.json")
		data, _ := json.Marshal(incomplete)
		_ = os.WriteFile(cfgPath, data, 0600)

		report := &DoctorReport{AgentName: "missing-fields"}
		repaired, err := checkAndHealConfig(ctx, paths, "missing-fields", report)
		if err != nil || repaired == nil {
			t.Fatalf("checkAndHealConfig failed: %v", err)
		}
		if repaired.ContainerName == "" || repaired.VolumeName == "" || repaired.Image == "" || repaired.Password == "" {
			t.Errorf("expected missing fields (ContainerName, VolumeName, Image, Password) to be healed")
		}
		if report.HealedCount == 0 {
			t.Errorf("expected HealedCount > 0 for healed missing fields")
		}
	})
}

func TestCheckAndHealPodmanStorage(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	ctx := context.Background()

	t.Run("HostAgent_StatusOK", func(t *testing.T) {
		hostCfg := &config.AgentConfig{Name: "host-agent", Runtime: "host", HostOnly: true}
		report := &DoctorReport{AgentName: hostCfg.Name}
		checkAndHealPodmanStorage(ctx, hostCfg, report)

		if len(report.Checks) != 1 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK for host agent storage, got %+v", report.Checks)
		}
	})

	t.Run("HostContainer_WorkspaceCheck", func(t *testing.T) {
		restoreMock := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.Command("true")
		}))
		defer restoreMock()

		hostContCfg := &config.AgentConfig{
			Name:          "host-cont-test",
			Runtime:       "host-container",
			ContainerName: "sndbx-agent-host-cont-test",
			VolumeName:    "sndbx-agent-host-cont-test-home",
			WorkspacePath: tmpDir,
		}
		report := &DoctorReport{AgentName: hostContCfg.Name}
		checkAndHealPodmanStorage(ctx, hostContCfg, report)

		if len(report.Checks) == 0 || (report.Checks[0].Status != StatusOK && report.Checks[0].Status != StatusHealed) {
			t.Fatalf("expected StatusOK or StatusHealed, got %+v", report.Checks)
		}
	})

	t.Run("StandardContainer_HealMissingVolume", func(t *testing.T) {
		restoreMock := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.Command("true")
		}))
		defer restoreMock()

		stdCfg := &config.AgentConfig{
			Name:          "std-agent",
			ContainerName: "sndbx-agent-std",
			VolumeName:    "sndbx-agent-std-home",
		}
		report := &DoctorReport{AgentName: stdCfg.Name}
		checkAndHealPodmanStorage(ctx, stdCfg, report)

		if len(report.Checks) == 0 {
			t.Fatalf("expected storage checks in report")
		}
	})
}

func TestCheckAndHealImage(t *testing.T) {
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	paths := config.Paths{
		DataHome:   filepath.Join(tmpDir, "data"),
		AgentsDir:  filepath.Join(tmpDir, "agents"),
		SecretsDir: filepath.Join(tmpDir, "secrets"),
	}
	_ = paths.EnsureDirectories()
	ctx := context.Background()

	t.Run("HostAgent_StatusOK", func(t *testing.T) {
		hostCfg := &config.AgentConfig{Name: "host-img-agent", Runtime: "host", HostOnly: true}
		report := &DoctorReport{AgentName: hostCfg.Name}
		checkAndHealImage(ctx, hostCfg, paths, report)

		if len(report.Checks) != 1 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK for host agent image, got %+v", report.Checks)
		}
	})

	t.Run("UntaggedImage_HealAppendLatest", func(t *testing.T) {
		restoreMock := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.Command("false")
		}))
		defer restoreMock()

		cfgUntagged := &config.AgentConfig{Name: "untagged-agent", Image: "docker.io/library/alpine"}
		report := &DoctorReport{AgentName: cfgUntagged.Name}
		checkAndHealImage(ctx, cfgUntagged, paths, report)

		if cfgUntagged.Image != "docker.io/library/alpine:latest" {
			t.Errorf("expected image to append :latest, got %s", cfgUntagged.Image)
		}
		if report.HealedCount != 1 {
			t.Errorf("expected HealedCount=1 for untagged image heal, got %d", report.HealedCount)
		}
	})

	t.Run("NonLocalImage_Warning", func(t *testing.T) {
		restoreMock := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "podman" && len(args) >= 2 && args[0] == "image" && args[1] == "exists" {
				return exec.Command("false")
			}
			return exec.Command("true")
		}))
		defer restoreMock()

		cfgRemote := &config.AgentConfig{Name: "remote-img-agent", Image: "docker.io/library/busybox:latest"}
		report := &DoctorReport{AgentName: cfgRemote.Name}
		checkAndHealImage(ctx, cfgRemote, paths, report)

		if report.WarningCount == 0 {
			t.Errorf("expected warning for non-local image")
		}
	})
}

func TestCheckAndHealContainer(t *testing.T) {
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		SSHDir:        filepath.Join(tmpDir, "ssh"),
		SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
	}
	_ = paths.EnsureDirectories()
	ctx := context.Background()

	t.Run("HostAgent_StatusOK", func(t *testing.T) {
		hostCfg := &config.AgentConfig{Name: "host-cont-agent", Runtime: "host", HostOnly: true}
		report := &DoctorReport{AgentName: hostCfg.Name}
		checkAndHealContainer(ctx, hostCfg, paths, report)

		if len(report.Checks) != 1 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK for host agent container, got %+v", report.Checks)
		}
	})

	t.Run("StoppedContainer_StatusOK", func(t *testing.T) {
		restoreMock := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "podman" && len(args) >= 3 && args[0] == "inspect" {
				return exec.Command("echo", `[{"State":{"Status":"stopped","Running":false}}]`)
			}
			return exec.Command("true")
		}))
		defer restoreMock()

		cfg := &config.AgentConfig{Name: "stopped-agent", ContainerName: "sndbx-stopped"}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealContainer(ctx, cfg, paths, report)

		if len(report.Checks) != 1 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK for stopped agent container, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "stopped") {
			t.Errorf("expected stopped in message: %s", report.Checks[0].Message)
		}
	})

	t.Run("RunningContainer_StatusOK", func(t *testing.T) {
		restoreMock := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "podman" && len(args) >= 3 && args[0] == "inspect" {
				return exec.Command("echo", `[{"State":{"Status":"running","Running":true}}]`)
			}
			if name == "podman" && len(args) >= 2 && args[0] == "port" {
				return exec.Command("echo", "127.0.0.1:2222")
			}
			return exec.Command("true")
		}))
		defer restoreMock()

		cfg := &config.AgentConfig{Name: "running-agent", ContainerName: "sndbx-running"}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealContainer(ctx, cfg, paths, report)

		if len(report.Checks) == 0 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK on running container, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "running") {
			t.Errorf("expected running in message: %s", report.Checks[0].Message)
		}
	})
}

func TestFormatDoctorReport(t *testing.T) {
	t.Parallel()

	t.Run("StatusSymbols", func(t *testing.T) {
		report := &DoctorReport{
			AgentName: "status-test",
			Checks: []CheckItem{
				{Name: "Check 1", Status: StatusOK, Message: "All good"},
				{Name: "Check 2", Status: StatusHealed, Message: "Fixed issue", Healed: true},
				{Name: "Check 3", Status: StatusWarning, Message: "Warning item"},
				{Name: "Check 4", Status: StatusError, Message: "Fatal error", Unrepairable: true},
			},
			HealedCount:       1,
			WarningCount:      1,
			UnrepairableCount: 1,
		}
		formatted := FormatDoctorReport(report)
		if !strings.Contains(formatted, "[✓]") ||
			!strings.Contains(formatted, "[⚡]") ||
			!strings.Contains(formatted, "[-]") ||
			!strings.Contains(formatted, "[✗]") {
			t.Errorf("FormatDoctorReport missed status symbols: %s", formatted)
		}
	})

	t.Run("AgentUnrepairableSummary", func(t *testing.T) {
		report := &DoctorReport{
			AgentName:         "unrep-agent",
			UnrepairableCount: 2,
			Checks: []CheckItem{
				{Name: "Check 1", Status: StatusError, Message: "error", Unrepairable: true},
			},
		}
		out := FormatDoctorReport(report)
		if !strings.Contains(out, "unrepairable issue(s)") {
			t.Errorf("expected unrepairable issues summary: %s", out)
		}
	})

	t.Run("InfraUnrepairableSummary", func(t *testing.T) {
		report := &DoctorReport{
			AgentName:         "shared-infrastructure",
			UnrepairableCount: 1,
			Checks: []CheckItem{
				{Name: "Infra Check", Status: StatusError, Message: "error", Unrepairable: true},
			},
		}
		out := FormatDoctorReport(report)
		if !strings.Contains(out, "unrepairable issue(s) detected in shared infrastructure") {
			t.Errorf("expected shared infra unrepairable summary: %s", out)
		}
	})

	t.Run("InfraWarningSummary", func(t *testing.T) {
		report := &DoctorReport{
			AgentName:    "shared-infrastructure",
			WarningCount: 2,
			Checks: []CheckItem{
				{Name: "Warning Check", Status: StatusWarning, Message: "warn"},
			},
		}
		out := FormatDoctorReport(report)
		if !strings.Contains(out, "warning(s) (offline services)") {
			t.Errorf("expected shared infra warning summary: %s", out)
		}
	})

	t.Run("InfraCleanSummary", func(t *testing.T) {
		report := &DoctorReport{
			AgentName: "shared-infrastructure",
			Checks: []CheckItem{
				{Name: "Clean Check", Status: StatusOK, Message: "ok"},
			},
		}
		out := FormatDoctorReport(report)
		if !strings.Contains(out, "fully healthy (0 issues found)") {
			t.Errorf("expected shared infra clean summary: %s", out)
		}
	})

	t.Run("AgentCleanSummary", func(t *testing.T) {
		report := &DoctorReport{
			AgentName: "clean-agent",
			Checks: []CheckItem{
				{Name: "Clean Check", Status: StatusOK, Message: "ok"},
			},
		}
		out := FormatDoctorReport(report)
		if !strings.Contains(out, "fully healthy (0 issues found)") {
			t.Errorf("expected agent clean summary: %s", out)
		}
	})
}
