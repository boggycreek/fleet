// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/runtime"
)

// CheckStatus indicates the outcome of an individual diagnostic check
type CheckStatus string

const (
	StatusOK      CheckStatus = "OK"
	StatusHealed  CheckStatus = "HEALED"
	StatusWarning CheckStatus = "WARNING"
	StatusError   CheckStatus = "ERROR"
)

// CheckItem represents one diagnostic check and auto-heal outcome
type CheckItem struct {
	Name         string      `json:"name"`
	Status       CheckStatus `json:"status"`
	Message      string      `json:"message"`
	Healed       bool        `json:"healed"`
	Unrepairable bool        `json:"unrepairable"`
}

// DoctorReport contains all diagnostic results for an agent
type DoctorReport struct {
	AgentName         string      `json:"agent_name"`
	Checks            []CheckItem `json:"checks"`
	HealedCount       int         `json:"healed_count"`
	WarningCount      int         `json:"warning_count"`
	UnrepairableCount int         `json:"unrepairable_count"`
}

// DiagnoseAndHealAgent performs complete health check and auto-healing on an agent
func DiagnoseAndHealAgent(ctx context.Context, agentName string, paths config.Paths) (*DoctorReport, error) {
	name := strings.ToLower(strings.TrimSpace(agentName))
	report := &DoctorReport{
		AgentName: name,
	}

	if name == "" {
		return nil, fmt.Errorf("agent name cannot be empty")
	}

	// 1. Configuration Check & Heal
	cfg, err := checkAndHealConfig(ctx, paths, name, report)
	if err != nil || cfg == nil {
		return report, nil
	}

	// 2. Cryptographic Signing Key Check & Heal
	checkAndHealSigningKey(ctx, cfg, paths, report)

	// 3. Host SSH Keypair & Include Config Check & Heal
	checkAndHealHostSSH(ctx, paths, report)

	// 4. Podman Storage (Volume & Network) Check & Heal
	checkAndHealPodmanStorage(ctx, cfg, report)

	// 5. Image Resolution and Cache Verification
	checkAndHealImage(ctx, cfg, paths, report)

	// 6. Valkey Backplane Registration Check & Heal
	checkAndHealValkey(ctx, cfg, paths, report)

	// 7. Forgejo Git Forge Registration Check & Heal
	checkAndHealForgejo(ctx, cfg, paths, report)

	// 8. SonarQube Mechanical Analysis Registration Check & Heal
	checkAndHealSonar(ctx, cfg, paths, report)

	// 9. Container State & SSH Config Sync
	checkAndHealContainer(ctx, cfg, paths, report)

	// 10. Backplane Profile Check & Heal
	checkAndHealBPProfile(ctx, cfg, paths, report)

	return report, nil
}

func checkAndHealConfig(ctx context.Context, paths config.Paths, name string, report *DoctorReport) (*config.AgentConfig, error) {
	cfgPath := filepath.Join(paths.AgentsDir, fmt.Sprintf("%s.json", name))
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		report.Checks = append(report.Checks, CheckItem{
			Name:         "Configuration File",
			Status:       StatusError,
			Message:      fmt.Sprintf("Missing config at %s (unrepairable: recreate agent via `fleet agent create %s`)", cfgPath, name),
			Unrepairable: true,
		})
		report.UnrepairableCount++
		return nil, err
	}

	cfg, err := config.LoadAgentConfig(name, paths)
	if err != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:         "Configuration Parsing",
			Status:       StatusError,
			Message:      fmt.Sprintf("Corrupt JSON configuration: %v", err),
			Unrepairable: true,
		})
		report.UnrepairableCount++
		return nil, err
	}

	healed := false
	if !cfg.IsNoContainer() {
		if cfg.ContainerName == "" {
			cfg.ContainerName = fmt.Sprintf("fleet-agent-%s", name)
			healed = true
		}
		if cfg.VolumeName == "" {
			cfg.VolumeName = fmt.Sprintf("fleet-agent-%s-home", name)
			healed = true
		}
		if cfg.Image == "" {
			cfg.Image = "fleet-base:latest"
			healed = true
		}
	}
	if cfg.Password == "" {
		pBytes := make([]byte, 16)
		_, _ = rand.Read(pBytes)
		cfg.Password = hex.EncodeToString(pBytes)
		healed = true
	}

	// If cfg.SigningKeyPEM == "" but signing-key.pem exists on disk, populate cfg.SigningKeyPEM
	keyPath := filepath.Join(paths.SecretsDir, cfg.Name, "signing-key.pem")
	if cfg.SigningKeyPEM == "" {
		if data, err := os.ReadFile(keyPath); err == nil && len(data) > 0 {
			cfg.SigningKeyPEM = string(data)
			healed = true
		}
	}

	// Check and heal file permissions to 0600
	if fi, err := os.Stat(cfgPath); err == nil && fi.Mode().Perm() != 0600 {
		if err := os.Chmod(cfgPath, 0600); err == nil {
			healed = true
		}
	}

	if healed {
		_ = config.SaveAgentConfig(cfg, paths)
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Configuration File",
			Status:  StatusHealed,
			Message: fmt.Sprintf("Repaired configuration and permissions (0600) at %s", cfgPath),
			Healed:  true,
		})
		report.HealedCount++
	} else {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Configuration File",
			Status:  StatusOK,
			Message: fmt.Sprintf("Valid configuration and permissions (%s)", cfgPath),
		})
	}

	return cfg, nil
}

func checkAndHealPodmanStorage(ctx context.Context, cfg *config.AgentConfig, report *DoctorReport) {
	if cfg.IsNoContainer() {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Podman Storage",
			Status:  StatusOK,
			Message: "No-container agent (direct host execution, no container storage required)",
		})
		return
	}
	if cfg.IsHostContainer() {
		if cfg.WorkspacePath != "" {
			if st, err := os.Stat(cfg.WorkspacePath); err != nil || !st.IsDir() {
				report.Checks = append(report.Checks, CheckItem{
					Name:         "Host Workspace Directory",
					Status:       StatusError,
					Message:      fmt.Sprintf("Host workspace directory %q does not exist or is not a directory", cfg.WorkspacePath),
					Unrepairable: true,
				})
				report.UnrepairableCount++
			} else {
				report.Checks = append(report.Checks, CheckItem{
					Name:    "Host Workspace Directory",
					Status:  StatusOK,
					Message: fmt.Sprintf("Projected workspace directory intact: %s", cfg.WorkspacePath),
				})
			}
		}
		if cfg.VolumeName != "" {
			volCmd := runtime.CommandContext(ctx, "podman", "volume", "exists", cfg.VolumeName)
			if err := volCmd.Run(); err != nil {
				if err := runtime.EnsureVolume(ctx, cfg.VolumeName); err != nil {
					report.Checks = append(report.Checks, CheckItem{
						Name:         "Podman Storage",
						Status:       StatusError,
						Message:      fmt.Sprintf("Failed ensuring volume %s: %v", cfg.VolumeName, err),
						Unrepairable: true,
					})
					report.UnrepairableCount++
					return
				}
				report.Checks = append(report.Checks, CheckItem{
					Name:    "Podman Storage",
					Status:  StatusHealed,
					Message: fmt.Sprintf("Created persistent home volume %s for host agent", cfg.VolumeName),
					Healed:  true,
				})
				report.HealedCount++
				return
			}
			report.Checks = append(report.Checks, CheckItem{
				Name:    "Podman Storage",
				Status:  StatusOK,
				Message: fmt.Sprintf("Host container storage verified (volume: %s)", cfg.VolumeName),
			})
		}
		return
	}
	healed := false
	netName := "fleet-infra"
	if err := runtime.EnsureNetwork(ctx, netName); err != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:         "Podman Network & Storage",
			Status:       StatusError,
			Message:      fmt.Sprintf("Failed ensuring bridge network %s: %v", netName, err),
			Unrepairable: true,
		})
		report.UnrepairableCount++
		return
	}

	_ = runtime.EnsureRootlessNetNS(ctx)

	volCmd := runtime.CommandContext(ctx, "podman", "volume", "exists", cfg.VolumeName)
	if err := volCmd.Run(); err != nil {
		if err := runtime.EnsureVolume(ctx, cfg.VolumeName); err != nil {
			report.Checks = append(report.Checks, CheckItem{
				Name:         "Podman Storage",
				Status:       StatusError,
				Message:      fmt.Sprintf("Failed ensuring volume %s: %v", cfg.VolumeName, err),
				Unrepairable: true,
			})
			report.UnrepairableCount++
			return
		}
		healed = true
	}

	if healed {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Podman Storage",
			Status:  StatusHealed,
			Message: fmt.Sprintf("Created missing persistent home volume (%s)", cfg.VolumeName),
			Healed:  true,
		})
		report.HealedCount++
	} else {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Podman Storage",
			Status:  StatusOK,
			Message: fmt.Sprintf("Network (%s) and volume (%s) present", netName, cfg.VolumeName),
		})
	}
}

func checkAndHealImage(ctx context.Context, cfg *config.AgentConfig, paths config.Paths, report *DoctorReport) {
	if cfg.IsNoContainer() {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Agent Image",
			Status:  StatusOK,
			Message: "No-container agent (no OCI container image required)",
		})
		return
	}
	resolved, isLocal := runtime.ResolveAgentImage(ctx, cfg.Image)
	if resolved != cfg.Image {
		cfg.Image = resolved
		_ = config.SaveAgentConfig(cfg, paths)
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Agent Image",
			Status:  StatusHealed,
			Message: fmt.Sprintf("Updated image reference to %s", resolved),
			Healed:  true,
		})
		report.HealedCount++
		return
	}

	if isLocal {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Agent Image",
			Status:  StatusOK,
			Message: fmt.Sprintf("Image %s present in local Podman storage", cfg.Image),
		})
	} else {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Agent Image",
			Status:  StatusWarning,
			Message: fmt.Sprintf("Image %s not cached locally (will be pulled on start)", cfg.Image),
		})
		report.WarningCount++
	}
}


func checkAndHealContainer(ctx context.Context, cfg *config.AgentConfig, paths config.Paths, report *DoctorReport) {
	if cfg.IsNoContainer() {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Container & Runtime",
			Status:  StatusOK,
			Message: "No-container agent (direct host execution, no container runtime required)",
		})
		return
	}
	if cfg.IsHostContainer() {
		info, err := runtime.InspectAgentContainer(ctx, cfg.ContainerName)
		state := "stopped"
		if err == nil && info != nil {
			state = info.State
		}
		var msg string
		if state == "running" {
			sshPort := 0
			if p, err := runtime.GetAgentSSHPort(ctx, cfg.ContainerName); err == nil && p > 0 {
				sshPort = p
			}
			sshConfigTarget := paths.SSHConfigFile
			if sshConfigTarget == "" {
				sshConfigTarget = filepath.Join(paths.DataHome, "ssh_config")
			}
			data, err := os.ReadFile(sshConfigTarget)
			expectedHost := fmt.Sprintf("Host fleet-%s", cfg.Name)
			if err != nil || !strings.Contains(string(data), expectedHost) {
				_ = runtime.SyncSSHConfigFile(ctx, paths)
			}
			if sshPort > 0 {
				msg = fmt.Sprintf("Host container running (SSH port %d, alias: ssh fleet-%s, connect: `fleet agent connect %s`)", sshPort, cfg.Name, cfg.Name)
			} else {
				msg = fmt.Sprintf("Host container running with workspace %s (connect via `fleet agent connect %s`)", cfg.WorkspacePath, cfg.Name)
			}
		} else {
			msg = fmt.Sprintf("Host container %s (start via `fleet agent start %s`)", state, cfg.Name)
		}
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Container & Runtime",
			Status:  StatusOK,
			Message: msg,
		})
		return
	}
	info, err := runtime.InspectAgentContainer(ctx, cfg.ContainerName)
	state := "stopped"
	if err == nil && info != nil {
		state = info.State
	}

	sshPort := 0
	if state == "running" {
		if p, err := runtime.GetAgentSSHPort(ctx, cfg.ContainerName); err == nil && p > 0 {
			sshPort = p
		}
	}

	sshConfigTarget := paths.SSHConfigFile
	if sshConfigTarget == "" {
		sshConfigTarget = filepath.Join(paths.DataHome, "ssh_config")
	}
	data, err := os.ReadFile(sshConfigTarget)
	expectedHost := fmt.Sprintf("Host fleet-%s", cfg.Name)
	if err != nil || !strings.Contains(string(data), expectedHost) {
		_ = runtime.SyncSSHConfigFile(ctx, paths)
	}

	var msg string
	if state == "running" && sshPort > 0 {
		msg = fmt.Sprintf("Container running (SSH port %d, alias: ssh fleet-%s)", sshPort, cfg.Name)
	} else {
		msg = fmt.Sprintf("Container %s (start via `fleet agent start %s`)", state, cfg.Name)
	}

	report.Checks = append(report.Checks, CheckItem{
		Name:    "Container & Runtime",
		Status:  StatusOK,
		Message: msg,
	})
}

// FormatDoctorReport formats the diagnostic report as human-readable text
func FormatDoctorReport(report *DoctorReport) string {
	var b bytes.Buffer
	if report.AgentName == "shared-infrastructure" || report.AgentName == "infra" {
		fmt.Fprintf(&b, "Diagnosing shared infrastructure...\n")
	} else {
		fmt.Fprintf(&b, "Diagnosing agent %q...\n", report.AgentName)
	}

	for _, c := range report.Checks {
		symbol := "[✓]"
		switch c.Status {
		case StatusHealed:
			symbol = "[⚡]"
		case StatusWarning:
			symbol = "[-]"
		case StatusError:
			symbol = "[✗]"
		}
		fmt.Fprintf(&b, "  %-3s %-25s : %s\n", symbol, c.Name, c.Message)
	}

	fmt.Fprintln(&b)
	if report.AgentName == "shared-infrastructure" || report.AgentName == "infra" {
		if report.UnrepairableCount > 0 {
			fmt.Fprintf(&b, "Summary: %d unrepairable issue(s) detected in shared infrastructure.\n", report.UnrepairableCount)
		} else if report.HealedCount > 0 {
			fmt.Fprintf(&b, "Summary: Shared infrastructure is healthy (%d issue(s) automatically healed).\n", report.HealedCount)
		} else if report.WarningCount > 0 {
			fmt.Fprintf(&b, "Summary: Shared infrastructure has %d warning(s) (offline services).\n", report.WarningCount)
		} else {
			fmt.Fprintf(&b, "Summary: Shared infrastructure is fully healthy (0 issues found).\n")
		}
	} else {
		if report.UnrepairableCount > 0 {
			fmt.Fprintf(&b, "Summary: %d unrepairable issue(s) detected for agent %q.\n", report.UnrepairableCount, report.AgentName)
		} else if report.HealedCount > 0 {
			fmt.Fprintf(&b, "Summary: Agent %q is healthy (%d issue(s) automatically healed).\n", report.AgentName, report.HealedCount)
		} else if report.WarningCount > 0 {
			fmt.Fprintf(&b, "Summary: Agent %q has %d warning(s) (offline dependencies).\n", report.AgentName, report.WarningCount)
		} else {
			fmt.Fprintf(&b, "Summary: Agent %q is fully healthy (0 issues found).\n", report.AgentName)
		}
	}

	return b.String()
}
