// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/runtime"
)

func TestDoctor_HostSSH_PubkeyRegeneration(t *testing.T) {
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	sshDir := filepath.Join(tmpDir, "ssh")
	_ = os.MkdirAll(sshDir, 0700)
	keyPath := filepath.Join(sshDir, "fleet")
	pubPath := keyPath + ".pub"

	// Generate real ed25519 key without passphrase using ssh-keygen
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", "fleet-coverage", "-f", keyPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed generating ssh key: %v (%s)", err, string(out))
	}

	// Remove .pub to force regeneration branch
	_ = os.Remove(pubPath)

	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		SSHDir:        sshDir,
		IDEKeyFile:    keyPath,
		SSHPrivateKey: keyPath,
		SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
	}
	_ = paths.EnsureDirectories()

	ctx := context.Background()
	report := &DoctorReport{AgentName: "ssh-regen-agent"}
	checkAndHealHostSSH(ctx, paths, report)

	if _, err := os.Stat(pubPath); err != nil {
		t.Errorf("expected regenerated pubkey at %s, got: %v", pubPath, err)
	}
	if report.HealedCount == 0 {
		t.Errorf("expected report to indicate healed host SSH pubkey")
	}

	// Test fallback when SSHPrivateKey is empty but IDEKeyFile is set
	pathsEmptySSH := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data2"),
		SSHDir:        sshDir,
		IDEKeyFile:    keyPath,
		SSHPrivateKey: "",
		SSHConfigFile: filepath.Join(tmpDir, "data2", "ssh_config"),
	}
	_ = pathsEmptySSH.EnsureDirectories()
	report2 := &DoctorReport{AgentName: "ssh-empty-agent"}
	checkAndHealHostSSH(ctx, pathsEmptySSH, report2)
	if len(report2.Checks) == 0 || (report2.Checks[0].Status != StatusOK && report2.Checks[0].Status != StatusHealed) {
		t.Errorf("expected StatusOK or StatusHealed with IDEKeyFile fallback, got %+v", report2.Checks)
	}
}

func TestDoctor_PodmanStorage_Coverage(t *testing.T) {
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	ctx := context.Background()

	t.Run("HostContainer_WorkspaceMissing", func(t *testing.T) {
		restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.Command("true")
		}))
		defer restore()

		cfg := &config.AgentConfig{
			Name:          "host-missing-ws",
			Runtime:       "host-container",
			ContainerName: "sndbx-agent-host-missing-ws",
			VolumeName:    "sndbx-agent-host-missing-ws-home",
			WorkspacePath: filepath.Join(tmpDir, "nonexistent-ws-dir"),
		}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealPodmanStorage(ctx, cfg, report)

		if report.UnrepairableCount == 0 {
			t.Errorf("expected unrepairable error when workspace directory is missing")
		}
	})

	t.Run("HostContainer_HealMissingVolume", func(t *testing.T) {
		restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "podman" && len(args) >= 2 && args[0] == "volume" && args[1] == "exists" {
				return exec.Command("false")
			}
			return exec.Command("true")
		}))
		defer restore()

		cfg := &config.AgentConfig{
			Name:          "host-heal-vol",
			Runtime:       "host-container",
			ContainerName: "sndbx-agent-host-heal-vol",
			VolumeName:    "sndbx-agent-host-heal-vol-home",
			WorkspacePath: tmpDir,
		}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealPodmanStorage(ctx, cfg, report)

		if report.HealedCount == 0 {
			t.Errorf("expected healed volume count > 0 for host container")
		}
	})

	t.Run("StandardContainer_EnsureNetworkFail", func(t *testing.T) {
		restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "podman" && len(args) >= 1 && args[0] == "network" {
				return exec.Command("false")
			}
			return exec.Command("true")
		}))
		defer restore()

		cfg := &config.AgentConfig{
			Name:          "std-net-fail",
			Runtime:       "container",
			ContainerName: "sndbx-agent-std-net-fail",
			VolumeName:    "sndbx-agent-std-net-fail-home",
		}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealPodmanStorage(ctx, cfg, report)

		if report.UnrepairableCount == 0 {
			t.Errorf("expected unrepairable error when EnsureNetwork fails")
		}
	})

	t.Run("StandardContainer_HealMissingVolume", func(t *testing.T) {
		restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "podman" && len(args) >= 2 && args[0] == "volume" && args[1] == "exists" {
				return exec.Command("false")
			}
			return exec.Command("true")
		}))
		defer restore()

		cfg := &config.AgentConfig{
			Name:          "std-heal-vol",
			Runtime:       "container",
			ContainerName: "sndbx-agent-std-heal-vol",
			VolumeName:    "sndbx-agent-std-heal-vol-home",
		}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealPodmanStorage(ctx, cfg, report)

		if report.HealedCount == 0 {
			t.Errorf("expected healed volume count > 0 for standard container")
		}
	})
}

func TestDoctor_ImageResolution_Heal(t *testing.T) {
	restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("true")
	}))
	defer restore()

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

	cfg := &config.AgentConfig{
		Name:  "preset-image-agent",
		Image: "base",
	}
	_ = config.SaveAgentConfig(cfg, paths)

	report := &DoctorReport{AgentName: cfg.Name}
	checkAndHealImage(context.Background(), cfg, paths, report)

	if report.HealedCount == 0 {
		t.Errorf("expected image reference to be auto-healed, report: %+v", report.Checks)
	}
	if cfg.Image != config.WellKnownImages["base"] {
		t.Errorf("expected image resolved to %s, got %s", config.WellKnownImages["base"], cfg.Image)
	}
}

func TestDoctor_NoContainer_ForgejoAndSonar(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{}
	cfg := &config.AgentConfig{
		Name:     "no-container-agent",
		Runtime:  "host",
		HostOnly: true,
	}

	reportForgejo := &DoctorReport{AgentName: cfg.Name}
	checkAndHealForgejo(ctx, cfg, paths, reportForgejo)
	if len(reportForgejo.Checks) == 0 || reportForgejo.Checks[0].Status != StatusOK || !strings.Contains(reportForgejo.Checks[0].Message, "No-container agent") {
		t.Errorf("unexpected forgejo check for no-container agent: %+v", reportForgejo.Checks)
	}

	reportSonar := &DoctorReport{AgentName: cfg.Name}
	checkAndHealSonar(ctx, cfg, paths, reportSonar)
	if len(reportSonar.Checks) == 0 || reportSonar.Checks[0].Status != StatusOK || !strings.Contains(reportSonar.Checks[0].Message, "No-container agent") {
		t.Errorf("unexpected sonar check for no-container agent: %+v", reportSonar.Checks)
	}
}

func TestDoctor_Baremetal_DoctorChecks(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{}
	cfg := &config.AgentConfig{
		Name:      "bare-doc",
		Baremetal: true,
		SSHHost:   "192.168.1.100",
		SSHPort:   22,
		SSHUser:   "debian",
	}

	repStorage := &DoctorReport{AgentName: cfg.Name}
	checkAndHealPodmanStorage(ctx, cfg, repStorage)
	if len(repStorage.Checks) == 0 || repStorage.Checks[0].Status != StatusOK || !strings.Contains(repStorage.Checks[0].Message, "Baremetal agent") {
		t.Errorf("unexpected storage check for baremetal agent: %+v", repStorage.Checks)
	}

	repImg := &DoctorReport{AgentName: cfg.Name}
	checkAndHealImage(ctx, cfg, paths, repImg)
	if len(repImg.Checks) == 0 || repImg.Checks[0].Status != StatusOK || !strings.Contains(repImg.Checks[0].Message, "Baremetal agent") {
		t.Errorf("unexpected image check for baremetal agent: %+v", repImg.Checks)
	}

	repCont := &DoctorReport{AgentName: cfg.Name}
	checkAndHealContainer(ctx, cfg, paths, repCont)
	if len(repCont.Checks) == 0 || repCont.Checks[0].Status != StatusOK || !strings.Contains(repCont.Checks[0].Message, "192.168.1.100") {
		t.Errorf("unexpected container check for baremetal agent: %+v", repCont.Checks)
	}

	repSonar := &DoctorReport{AgentName: cfg.Name}
	checkAndHealSonar(ctx, cfg, paths, repSonar)
	if len(repSonar.Checks) == 0 || repSonar.Checks[0].Status != StatusOK || !strings.Contains(repSonar.Checks[0].Message, "Baremetal agent") {
		t.Errorf("unexpected sonar check for baremetal agent: %+v", repSonar.Checks)
	}
}

func TestDoctor_ValkeyAndForgejo_DefaultAdminPassword(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{}
	cfg := &config.AgentConfig{Name: "def-pass-agent"}

	t.Setenv("ADMIN_BACKPLANE_PASSWORD", "")
	t.Setenv("FORGEJO_ADMIN_PASSWORD", "")
	t.Setenv("GITEA_ADMIN_PASSWORD", "")
	t.Setenv("FORGEJO_URL", "http://127.0.0.1:65532")
	t.Setenv("GITEA_URL", "")
	t.Setenv("BP_PORT", "65530") // Unreachable port

	reportVk := &DoctorReport{AgentName: cfg.Name}
	checkAndHealValkey(ctx, cfg, paths, reportVk)
	if reportVk.WarningCount == 0 {
		t.Errorf("expected warning for offline valkey with default admin password")
	}

	reportFj := &DoctorReport{AgentName: cfg.Name}
	checkAndHealForgejo(ctx, cfg, paths, reportFj)
	if reportFj.WarningCount == 0 {
		t.Errorf("expected warning for offline forgejo with default admin password")
	}
}

func TestDoctor_ContainerAndRuntime_Coverage(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	t.Run("HostContainer_RunningWithSSHPort", func(t *testing.T) {
		restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "podman" && len(args) >= 2 && args[0] == "inspect" {
				return exec.Command("printf", "%s", `[{"Id":"c1","State":{"Status":"running","Running":true},"Mounts":[]}]`)
			}
			if name == "podman" && len(args) >= 2 && args[0] == "port" {
				return exec.Command("printf", "%s", "127.0.0.1:41234\n")
			}
			return exec.Command("true")
		}))
		defer restore()

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data_host_ssh"),
			SSHConfigFile: "", // Exercise fallback to filepath.Join(paths.DataHome, "ssh_config")
		}
		_ = paths.EnsureDirectories()

		cfg := &config.AgentConfig{
			Name:          "host-running-ssh",
			Runtime:       "host-container",
			ContainerName: "sndbx-agent-host-running-ssh",
			WorkspacePath: tmpDir,
		}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealContainer(ctx, cfg, paths, report)

		if len(report.Checks) == 0 || !strings.Contains(report.Checks[0].Message, "SSH port 41234") {
			t.Errorf("expected SSH port 41234 in message, got: %+v", report.Checks)
		}
	})

	t.Run("HostContainer_RunningWithoutSSHPort", func(t *testing.T) {
		restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "podman" && len(args) >= 2 && args[0] == "inspect" {
				return exec.Command("printf", "%s", `[{"Id":"c1","State":{"Status":"running","Running":true},"Mounts":[]}]`)
			}
			if name == "podman" && len(args) >= 2 && args[0] == "port" {
				return exec.Command("false")
			}
			return exec.Command("true")
		}))
		defer restore()

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data_host_nossh"),
			SSHConfigFile: filepath.Join(tmpDir, "data_host_nossh", "ssh_config"),
		}
		_ = paths.EnsureDirectories()

		cfg := &config.AgentConfig{
			Name:          "host-running-nossh",
			Runtime:       "host-container",
			ContainerName: "sndbx-agent-host-running-nossh",
			WorkspacePath: tmpDir,
		}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealContainer(ctx, cfg, paths, report)

		if len(report.Checks) == 0 || !strings.Contains(report.Checks[0].Message, "Host container running with workspace") {
			t.Errorf("expected host container running with workspace in message, got: %+v", report.Checks)
		}
	})

	t.Run("StandardContainer_RunningWithSSHPort", func(t *testing.T) {
		restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "podman" && len(args) >= 2 && args[0] == "inspect" {
				return exec.Command("printf", "%s", `[{"Id":"c2","State":{"Status":"running","Running":true},"Mounts":[]}]`)
			}
			if name == "podman" && len(args) >= 2 && args[0] == "port" {
				return exec.Command("printf", "%s", "127.0.0.1:42345\n")
			}
			return exec.Command("true")
		}))
		defer restore()

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data_std_ssh"),
			SSHConfigFile: "", // Exercise fallback to filepath.Join(paths.DataHome, "ssh_config")
		}
		_ = paths.EnsureDirectories()

		cfg := &config.AgentConfig{
			Name:          "std-running-ssh",
			Runtime:       "container",
			ContainerName: "sndbx-agent-std-running-ssh",
		}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealContainer(ctx, cfg, paths, report)

		if len(report.Checks) == 0 || !strings.Contains(report.Checks[0].Message, "Container running (SSH port 42345") {
			t.Errorf("expected container running with SSH port 42345, got: %+v", report.Checks)
		}
	})

	t.Run("StandardContainer_RunningWithoutSSHPort", func(t *testing.T) {
		restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "podman" && len(args) >= 2 && args[0] == "inspect" {
				return exec.Command("printf", "%s", `[{"Id":"c3","State":{"Status":"running","Running":true},"Mounts":[]}]`)
			}
			if name == "podman" && len(args) >= 2 && args[0] == "port" {
				return exec.Command("false")
			}
			return exec.Command("true")
		}))
		defer restore()

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data_std_nossh"),
			SSHConfigFile: filepath.Join(tmpDir, "data_std_nossh", "ssh_config"),
		}
		_ = paths.EnsureDirectories()

		cfg := &config.AgentConfig{
			Name:          "std-running-nossh",
			Runtime:       "container",
			ContainerName: "sndbx-agent-std-running-nossh",
		}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealContainer(ctx, cfg, paths, report)

		if len(report.Checks) == 0 || !strings.Contains(report.Checks[0].Message, "Container running (start via") {
			t.Errorf("expected container running message, got: %+v", report.Checks)
		}
	})
}

func TestDoctor_BackplaneProfile_Coverage(t *testing.T) {
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	cfg := &config.AgentConfig{
		Name:     "bp-prof-agent",
		Password: "bp-password",
	}
	ctx := context.Background()

	t.Run("EmptyProfileDir", func(t *testing.T) {
		paths := config.Paths{BPProfilesDir: ""}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealBPProfile(ctx, cfg, paths, report)
		if len(report.Checks) != 0 {
			t.Errorf("expected no checks when BPProfilesDir is empty")
		}
	})

	t.Run("WriteError_Unrepairable", func(t *testing.T) {
		profDir := filepath.Join(tmpDir, "readonly_prof_dir")
		_ = os.MkdirAll(profDir, 0700)
		targetFile := filepath.Join(profDir, fmt.Sprintf("%s.env", cfg.Name))
		// Create a directory where the file should be written to cause WriteFile error
		_ = os.Mkdir(targetFile, 0700)

		paths := config.Paths{
			BPProfilesDir: profDir,
			SecretsDir:    filepath.Join(tmpDir, "secrets"),
		}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealBPProfile(ctx, cfg, paths, report)

		if report.UnrepairableCount == 0 {
			t.Errorf("expected unrepairable error when writing profile fails")
		}
	})

	t.Run("MissingProfile_RebuiltAndHealed", func(t *testing.T) {
		profDir := filepath.Join(tmpDir, "rebuild_prof_dir")
		paths := config.Paths{
			BPProfilesDir: profDir,
			SecretsDir:    filepath.Join(tmpDir, "secrets"),
		}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealBPProfile(ctx, cfg, paths, report)

		if report.HealedCount == 0 {
			t.Errorf("expected profile rebuild when missing")
		}
		profilePath := filepath.Join(profDir, fmt.Sprintf("%s.env", cfg.Name))
		if _, err := os.Stat(profilePath); err != nil {
			t.Errorf("expected rebuilt profile at %s", profilePath)
		}
	})

	t.Run("PermissionRepairedTo0600", func(t *testing.T) {
		profDir := filepath.Join(tmpDir, "perm_prof_dir")
		_ = os.MkdirAll(profDir, 0700)
		paths := config.Paths{
			BPProfilesDir: profDir,
			SecretsDir:    filepath.Join(tmpDir, "secrets"),
		}
		keyPath := filepath.Join(paths.SecretsDir, cfg.Name, "signing-key.pem")
		expectedContent := fmt.Sprintf("BP_AGENT=%s\nBP_PASSWORD=%s\nBP_SIGNING_KEY=%s\n",
			cfg.Name, cfg.Password, keyPath)

		profilePath := filepath.Join(profDir, fmt.Sprintf("%s.env", cfg.Name))
		_ = os.WriteFile(profilePath, []byte(expectedContent), 0644)

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealBPProfile(ctx, cfg, paths, report)

		if report.HealedCount == 0 {
			t.Errorf("expected permission auto-healing for backplane profile")
		}
		if fi, err := os.Stat(profilePath); err != nil || fi.Mode().Perm() != 0600 {
			t.Errorf("expected profile mode 0600, got %o", fi.Mode().Perm())
		}

		// Run again: profile intact and mode 0600 -> StatusOK
		reportOK := &DoctorReport{AgentName: cfg.Name}
		checkAndHealBPProfile(ctx, cfg, paths, reportOK)
		if len(reportOK.Checks) == 0 || reportOK.Checks[0].Status != StatusOK {
			t.Errorf("expected StatusOK for intact profile, got %+v", reportOK.Checks)
		}
	})
}

func TestDoctor_InfraStorage_NetworkFail(t *testing.T) {
	restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) >= 1 && args[0] == "network" {
			return exec.Command("false")
		}
		return exec.Command("true")
	}))
	defer restore()

	report := &DoctorReport{AgentName: "shared-infrastructure"}
	checkAndHealInfraStorage(context.Background(), report)

	if report.UnrepairableCount == 0 {
		t.Errorf("expected unrepairable error when EnsureNetwork fails in checkAndHealInfraStorage")
	}
}

func TestDoctor_InfraEnv_DefaultUser(t *testing.T) {
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	origUser := os.Getenv("USER")
	origAdmin := os.Getenv("ADMIN_BACKPLANE_PASSWORD")
	origHumanPass := os.Getenv("HUMAN_BACKPLANE_PASSWORD")
	origHuman := os.Getenv("HUMAN_NAME")

	t.Setenv("USER", "")
	t.Cleanup(func() {
		os.Setenv("USER", origUser)
		os.Setenv("ADMIN_BACKPLANE_PASSWORD", origAdmin)
		os.Setenv("HUMAN_BACKPLANE_PASSWORD", origHumanPass)
		os.Setenv("HUMAN_NAME", origHuman)
	})

	paths := config.Paths{
		DataHome: tmpDir,
		EnvFile:  filepath.Join(tmpDir, "environment"),
	}

	report := &DoctorReport{AgentName: "shared-infrastructure"}
	checkAndHealInfraEnv(paths, report)

	data, err := os.ReadFile(paths.EnvFile)
	if err != nil {
		t.Fatalf("failed reading generated env file: %v", err)
	}
	if !strings.Contains(string(data), "HUMAN_NAME=operator") {
		t.Errorf("expected HUMAN_NAME=operator when USER is empty, got: %s", string(data))
	}
}

func TestDoctor_PostgresContainer_Coverage(t *testing.T) {
	ctx := context.Background()

	t.Run("RunningAndHealthy", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed starting mock postgres listener: %v", err)
		}
		defer ln.Close()
		port := ln.Addr().(*net.TCPAddr).Port

		restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "podman" && len(args) >= 2 && args[0] == "inspect" {
				return exec.Command("printf", "%s", `[{"Id":"pg1","State":{"Status":"running","Running":true},"Mounts":[]}]`)
			}
			return exec.Command("true")
		}))
		defer restore()

		t.Setenv("POSTGRES_HOST", "127.0.0.1")
		t.Setenv("POSTGRES_PORT", fmt.Sprintf("%d", port))

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealPostgresContainer(ctx, report)

		if len(report.Checks) == 0 || report.Checks[0].Status != StatusOK || !strings.Contains(report.Checks[0].Message, "database: sonar") {
			t.Errorf("expected StatusOK for healthy postgres container, got: %+v", report.Checks)
		}
	})

	t.Run("DefaultHostAndPort", func(t *testing.T) {
		restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "podman" && len(args) >= 2 && args[0] == "inspect" {
				return exec.Command("printf", "%s", `[{"Id":"pg1","State":{"Status":"running","Running":true},"Mounts":[]}]`)
			}
			return exec.Command("true")
		}))
		defer restore()

		t.Setenv("POSTGRES_HOST", "127.0.0.1")
		t.Setenv("POSTGRES_PORT", "65531")

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealPostgresContainer(ctx, report)

		if report.WarningCount == 0 {
			t.Errorf("expected warning for offline postgres on port 65531")
		}
	})
}

func TestDoctor_ForgejoAndSonarContainers_Coverage(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{}

	restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) >= 2 && args[0] == "inspect" {
			return exec.Command("printf", "%s", `[{"Id":"c1","State":{"Status":"running","Running":true},"Mounts":[]}]`)
		}
		return exec.Command("true")
	}))
	defer restore()

	t.Run("ForgejoContainer_HTTP500_BodyClosed", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("error"))
		}))
		defer ts.Close()

		origURL := os.Getenv("FORGEJO_URL")
		origGiteaURL := os.Getenv("GITEA_URL")
		origPass := os.Getenv("FORGEJO_ADMIN_PASSWORD")
		origGiteaPass := os.Getenv("GITEA_ADMIN_PASSWORD")
		origAdminBackplane := os.Getenv("ADMIN_BACKPLANE_PASSWORD")
		origAdminUser := os.Getenv("FORGEJO_ADMIN_USER")

		os.Setenv("FORGEJO_URL", ts.URL)
		os.Setenv("GITEA_URL", "")
		os.Setenv("FORGEJO_ADMIN_PASSWORD", "")
		os.Setenv("GITEA_ADMIN_PASSWORD", "")
		os.Setenv("ADMIN_BACKPLANE_PASSWORD", "")
		os.Setenv("FORGEJO_ADMIN_USER", "")

		defer func() {
			os.Setenv("FORGEJO_URL", origURL)
			os.Setenv("GITEA_URL", origGiteaURL)
			os.Setenv("FORGEJO_ADMIN_PASSWORD", origPass)
			os.Setenv("GITEA_ADMIN_PASSWORD", origGiteaPass)
			os.Setenv("ADMIN_BACKPLANE_PASSWORD", origAdminBackplane)
			os.Setenv("FORGEJO_ADMIN_USER", origAdminUser)
		}()

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealForgejoContainer(ctx, paths, report)

		if report.WarningCount == 0 {
			t.Errorf("expected warning for forgejo returning 500")
		}
	})

	t.Run("SonarContainer_HTTP502_BodyClosed", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("bad gateway"))
		}))
		defer ts.Close()

		origURL := os.Getenv("SONAR_HOST_URL")
		origSonarQubeURL := os.Getenv("SONARQUBE_URL")

		os.Setenv("SONAR_HOST_URL", ts.URL)
		os.Setenv("SONARQUBE_URL", "")

		defer func() {
			os.Setenv("SONAR_HOST_URL", origURL)
			os.Setenv("SONARQUBE_URL", origSonarQubeURL)
		}()

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealSonarContainer(ctx, paths, report)

		if report.WarningCount == 0 {
			t.Errorf("expected warning for sonar returning 502")
		}
	})
}

func TestDoctor_ValkeyContainer_Coverage(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	paths := config.Paths{
		DataHome: filepath.Join(tmpDir, "data"),
	}
	_ = paths.EnsureDirectories()

	t.Run("MountMismatch_TriggersRecreate", func(t *testing.T) {
		port, cleanup := startMockValkeyForDoctor(t)
		defer cleanup()

		restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "podman" && len(args) >= 2 && args[0] == "inspect" {
				// Return inspect with mismatched /etc/valkey mount source
				return exec.Command("printf", "%s", `[{"Id":"vk1","State":{"Status":"running","Running":true},"Mounts":[{"Destination":"/etc/valkey","Source":"/mismatched/path"}]}]`)
			}
			return exec.Command("true")
		}))
		defer restore()

		t.Setenv("BP_HOST", "127.0.0.1")
		t.Setenv("BP_PORT", fmt.Sprintf("%d", port))
		t.Setenv("ADMIN_BACKPLANE_PASSWORD", "mockpass")
		t.Setenv("HUMAN_NAME", "operator")
		t.Setenv("HUMAN_BACKPLANE_PASSWORD", "humanpass")

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealValkeyContainer(ctx, paths, report)

		if report.HealedCount == 0 || len(report.Checks) == 0 || report.Checks[0].Status != StatusHealed {
			t.Errorf("expected StatusHealed when valkey container mount is repaired, got: %+v", report.Checks)
		}
	})

	t.Run("RunningHealthy_ExecutesACLAndStatusOK", func(t *testing.T) {
		port, cleanup := startMockValkeyForDoctor(t)
		defer cleanup()

		valkeyDir := filepath.Join(paths.DataHome, "valkey")
		_ = os.MkdirAll(valkeyDir, 0700)

		restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "podman" && len(args) >= 2 && args[0] == "inspect" {
				// Return inspect with matching mount
				jsonMount := fmt.Sprintf(`[{"Id":"vk2","State":{"Status":"running","Running":true},"Mounts":[{"Destination":"/etc/valkey","Source":%q}]}]`, valkeyDir)
				return exec.Command("printf", "%s", jsonMount)
			}
			return exec.Command("true")
		}))
		defer restore()

		t.Setenv("BP_HOST", "127.0.0.1")
		t.Setenv("BP_PORT", fmt.Sprintf("%d", port))
		t.Setenv("ADMIN_BACKPLANE_PASSWORD", "mockpass")
		t.Setenv("HUMAN_NAME", "operator")
		t.Setenv("HUMAN_BACKPLANE_PASSWORD", "humanpass")

		report := &DoctorReport{AgentName: "shared-infrastructure"}
		checkAndHealValkeyContainer(ctx, paths, report)

		if len(report.Checks) == 0 || report.Checks[0].Status != StatusOK {
			t.Errorf("expected StatusOK for running healthy valkey container, got: %+v", report.Checks)
		}
	})
}
