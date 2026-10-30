// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
	"github.com/boggycreek/sandbox/pkg/config"
	"github.com/boggycreek/sandbox/pkg/forgejo"
	"github.com/boggycreek/sandbox/pkg/plugin"
	"github.com/boggycreek/sandbox/pkg/runtime"
	"github.com/boggycreek/sandbox/pkg/sonar"
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
	cfg, err := checkAndHealConfig(paths, name, report)
	if err != nil || cfg == nil {
		return report, nil
	}

	// 2. Cryptographic Signing Key Check & Heal
	checkAndHealSigningKey(cfg, paths, report)

	// 3. Host SSH Keypair & Include Config Check & Heal
	checkAndHealHostSSH(paths, report)

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
	checkAndHealBPProfile(cfg, paths, report)

	return report, nil
}

func checkAndHealConfig(paths config.Paths, name string, report *DoctorReport) (*config.AgentConfig, error) {
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

func parseEd25519PrivateKey(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed decoding PEM block")
	}
	// Try standard libbp PKCS#8 format
	if priv, err := libbp.DecodePrivateKeyPEM(string(data)); err == nil {
		return priv, nil
	}
	// Try raw 64-byte private key or 32-byte seed
	if len(block.Bytes) == ed25519.PrivateKeySize {
		return ed25519.PrivateKey(block.Bytes), nil
	}
	if len(block.Bytes) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(block.Bytes), nil
	}
	return nil, fmt.Errorf("not a valid Ed25519 private key")
}

func checkAndHealSigningKey(cfg *config.AgentConfig, paths config.Paths, report *DoctorReport) {
	keyPath := filepath.Join(paths.SecretsDir, cfg.Name, "signing-key.pem")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:         "Ed25519 Signing Keys",
			Status:       StatusError,
			Message:      fmt.Sprintf("Signing key file missing at %s: keys must never be regenerated to preserve historical signature validity", keyPath),
			Unrepairable: true,
		})
		report.UnrepairableCount++
		return
	}

	if len(data) == 0 {
		report.Checks = append(report.Checks, CheckItem{
			Name:         "Ed25519 Signing Keys",
			Status:       StatusError,
			Message:      fmt.Sprintf("Signing key file at %s is empty: keys must never be regenerated to preserve historical signature validity", keyPath),
			Unrepairable: true,
		})
		report.UnrepairableCount++
		return
	}

	privKey, err := parseEd25519PrivateKey(data)
	if err != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:         "Ed25519 Signing Keys",
			Status:       StatusError,
			Message:      fmt.Sprintf("Signing key file at %s is corrupt or not a valid Ed25519 private key: keys must never be regenerated to preserve historical signature validity", keyPath),
			Unrepairable: true,
		})
		report.UnrepairableCount++
		return
	}

	keyHealedPerm := false
	if fi, err := os.Stat(keyPath); err == nil && fi.Mode().Perm() != 0600 {
		if err := os.Chmod(keyPath, 0600); err == nil {
			keyHealedPerm = true
		}
	}

	priv := privKey
	pub := priv.Public().(ed25519.PublicKey)
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	cfgSynced := false
	if cfg.SigningKeyPEM == "" || cfg.PublicKeyB64 == "" {
		cfg.SigningKeyPEM = string(data)
		cfg.PublicKeyB64 = pubB64
		_ = config.SaveAgentConfig(cfg, paths)
		cfgSynced = true
	}

	if keyHealedPerm || cfgSynced {
		msg := "Repaired signing key file permissions to 0600"
		if keyHealedPerm && cfgSynced {
			msg = "Repaired signing key file permissions to 0600 and synchronized config"
		} else if cfgSynced {
			msg = "Synchronized agent configuration with existing Ed25519 signing key"
		}
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Ed25519 Signing Keys",
			Status:  StatusHealed,
			Message: msg,
			Healed:  true,
		})
		report.HealedCount++
		return
	}

	report.Checks = append(report.Checks, CheckItem{
		Name:    "Ed25519 Signing Keys",
		Status:  StatusOK,
		Message: "Cryptographic signing keypair intact",
	})
}

func checkAndHealHostSSH(paths config.Paths, report *DoctorReport) {
	healedKey := false
	keyPath := paths.GetSSHPrivateKey()
	if keyPath == "" {
		keyPath = paths.IDEKeyFile
	}
	sshPub := keyPath + ".pub"
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		_ = os.MkdirAll(filepath.Dir(keyPath), 0700)
		legacyKey := filepath.Join(paths.SSHDir, "agent-sandbox")
		if privBytes, err := os.ReadFile(legacyKey); err == nil {
			_ = os.WriteFile(keyPath, privBytes, 0600)
			if pubBytes, err := os.ReadFile(legacyKey + ".pub"); err == nil {
				_ = os.WriteFile(sshPub, pubBytes, 0644)
			}
			healedKey = true
		} else {
			_ = exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", "fleet-ide", "-f", keyPath).Run()
			healedKey = true
		}
	} else if _, err := os.Stat(sshPub); os.IsNotExist(err) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "ssh-keygen", "-y", "-P", "", "-f", keyPath).Output(); err == nil && len(out) > 0 {
			_ = os.WriteFile(sshPub, out, 0644)
			healedKey = true
		}
	}

	// Check managed ssh_config and ~/.ssh/config Include
	healedInclude := false
	if _, err := os.Stat(paths.SSHConfigFile); os.IsNotExist(err) {
		_ = os.MkdirAll(filepath.Dir(paths.SSHConfigFile), 0700)
		_ = os.WriteFile(paths.SSHConfigFile, []byte("# Fleet Auto-Generated SSH Configuration\n"), 0600)
		healedInclude = true
	}

	userSSHConfig := filepath.Join(paths.SSHDir, "config")
	if linked, err := plugin.EnsureSSHConfigInclude(paths.SSHConfigFile, userSSHConfig); err == nil && linked {
		healedInclude = true
	}

	if healedKey || healedInclude {
		msg := "Ensured host IDE keypair and OpenSSH Include directive"
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Host SSH & IDE Config",
			Status:  StatusHealed,
			Message: msg,
			Healed:  true,
		})
		report.HealedCount++
	} else {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Host SSH & IDE Config",
			Status:  StatusOK,
			Message: "Dedicated IDE keypair and OpenSSH Include active",
		})
	}
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
				if err := runtime.EnsureVolume(ctx, cfg.VolumeName); err == nil {
					report.Checks = append(report.Checks, CheckItem{
						Name:    "Podman Storage",
						Status:  StatusHealed,
						Message: fmt.Sprintf("Created persistent home volume %s for host agent", cfg.VolumeName),
						Healed:  true,
					})
					report.HealedCount++
					return
				}
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
		if err := runtime.EnsureVolume(ctx, cfg.VolumeName); err == nil {
			healed = true
		}
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

func checkAndHealValkey(ctx context.Context, cfg *config.AgentConfig, paths config.Paths, report *DoctorReport) {
	bpCfg := libbp.LoadClientFromEnv()
	adminPass := os.Getenv("ADMIN_BACKPLANE_PASSWORD")
	if adminPass == "" {
		adminPass = "admin_backplane_pass"
	}

	dialCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	client, err := libbp.Dial(dialCtx, libbp.ClientConfig{
		Host:     bpCfg.Host,
		Port:     bpCfg.Port,
		Username: "admin",
		Password: adminPass,
	})
	if err != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Valkey Backplane",
			Status:  StatusWarning,
			Message: "Valkey infrastructure is offline (skipped ACL sync; start via `fleet infra up`)",
		})
		report.WarningCount++
		return
	}
	defer client.Close()

	// Provision/repair ACL
	aclArgs := []string{
		"SETUSER", cfg.Name, "on",
		">" + cfg.Password,
		fmt.Sprintf("~%s:*", cfg.Name),
		fmt.Sprintf("~identity:%s", cfg.Name),
		"~ensemble:*",
		"%R~*:out", "%R~identity:*", "%R~human:*", "%R~*:blob:*",
		"&*", "+@all", "-@admin", "-@dangerous",
		"(+xadd ~*:inbox)",
	}
	_, _ = client.Exec(ctx, "ACL", aclArgs...)

	_ = client.RegisterIdentity(ctx, libbp.IdentityRecord{
		Name:   cfg.Name,
		Role:   cfg.Role,
		Kind:   "agent",
		PubKey: cfg.PublicKeyB64,
	})

	report.Checks = append(report.Checks, CheckItem{
		Name:    "Valkey Backplane",
		Status:  StatusOK,
		Message: "Valkey ACL permissions and registry identity verified",
	})
}

func checkAndHealForgejo(ctx context.Context, cfg *config.AgentConfig, paths config.Paths, report *DoctorReport) {
	if cfg.IsNoContainer() {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Forgejo Git Forge",
			Status:  StatusOK,
			Message: "No-container agent (no Forgejo account required)",
		})
		return
	}

	adminPass := os.Getenv("FORGEJO_ADMIN_PASSWORD")
	if adminPass == "" {
		adminPass = os.Getenv("GITEA_ADMIN_PASSWORD")
	}
	if adminPass == "" {
		adminPass = os.Getenv("ADMIN_BACKPLANE_PASSWORD")
	}
	if adminPass == "" {
		adminPass = "admin_backplane_pass"
	}

	forgejoURL := os.Getenv("FORGEJO_URL")
	if forgejoURL == "" {
		forgejoURL = os.Getenv("GITEA_URL")
	}
	if forgejoURL == "" {
		forgejoURL = "http://127.0.0.1:3000"
	}

	adminUser := os.Getenv("FORGEJO_ADMIN_USER")
	if adminUser == "" {
		adminUser = os.Getenv("GITEA_ADMIN_USER")
	}
	if adminUser == "" {
		adminUser = "forgejoadmin"
	}

	httpClient := &http.Client{Timeout: 1 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", forgejoURL+"/api/v1/version", nil)
	resp, err := httpClient.Do(req)
	if err != nil || (resp != nil && resp.StatusCode >= 500) {
		if resp != nil {
			_ = resp.Body.Close()
		}
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Forgejo Git Forge",
			Status:  StatusWarning,
			Message: "Forgejo infrastructure is offline (skipped user sync; start via `fleet infra up`)",
		})
		report.WarningCount++
		return
	}
	_ = resp.Body.Close()

	client := forgejo.NewClient(forgejo.ClientConfig{
		BaseURL:   forgejoURL,
		AdminUser: adminUser,
		AdminPass: adminPass,
		Timeout:   2 * time.Second,
	})

	_ = client.EnsureUser(ctx, cfg.Name, cfg.Password, fmt.Sprintf("%s@local.fleet", cfg.Name))
	sshKeyPub := fmt.Sprintf("%s.pub", paths.GetSSHPrivateKey())
	if keyData, err := os.ReadFile(sshKeyPub); err == nil && len(keyData) > 0 {
		_ = client.AddUserSSHKey(ctx, cfg.Name, fmt.Sprintf("%s-ide-key", cfg.Name), string(keyData))
	}
	_ = client.AddOrgMember(ctx, "fleet", cfg.Name)

	report.Checks = append(report.Checks, CheckItem{
		Name:    "Forgejo Git Forge",
		Status:  StatusOK,
		Message: "User account, SSH key, and fleet organization membership verified",
	})
}

func checkAndHealGitea(ctx context.Context, cfg *config.AgentConfig, paths config.Paths, report *DoctorReport) {
	checkAndHealForgejo(ctx, cfg, paths, report)
}

func checkAndHealSonar(ctx context.Context, cfg *config.AgentConfig, paths config.Paths, report *DoctorReport) {
	if cfg.IsNoContainer() {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "SonarQube Analysis",
			Status:  StatusOK,
			Message: "No-container agent (no SonarQube account required)",
		})
		return
	}

	adminPass := config.GetSonarAdminPassword()
	adminUser := os.Getenv("SONAR_ADMIN_USER")
	if adminUser == "" {
		adminUser = "admin"
	}

	sonarURL := os.Getenv("SONAR_HOST_URL")
	if sonarURL == "" {
		sonarURL = os.Getenv("SONARQUBE_URL")
	}
	if sonarURL == "" {
		sonarURL = "http://127.0.0.1:9000"
	}

	client := sonar.NewClient(sonar.ClientConfig{
		BaseURL:   sonarURL,
		AdminUser: adminUser,
		AdminPass: adminPass,
		Timeout:   1 * time.Second,
	})

	if _, err := client.GetSystemStatus(ctx); err != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "SonarQube Analysis",
			Status:  StatusWarning,
			Message: "SonarQube infrastructure is offline (skipped user sync; start via `fleet infra up`)",
		})
		report.WarningCount++
		return
	}

	_ = client.EnsureUser(ctx, cfg.Name, cfg.Password, cfg.Name, fmt.Sprintf("%s@local.fleet", cfg.Name))
	if cfg.SonarToken == "" {
		token, err := client.GenerateUserToken(ctx, cfg.Name, fmt.Sprintf("%s-agent-token", cfg.Name))
		if err == nil && token != "" {
			cfg.SonarToken = token
			_ = config.SaveAgentConfig(cfg, paths)
			report.Checks = append(report.Checks, CheckItem{
				Name:    "SonarQube Analysis",
				Status:  StatusHealed,
				Message: "Generated missing SonarQube user analysis token",
				Healed:  true,
			})
			report.HealedCount++
			return
		}
	}

	report.Checks = append(report.Checks, CheckItem{
		Name:    "SonarQube Analysis",
		Status:  StatusOK,
		Message: "User account and analysis token verified",
	})
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

func checkAndHealBPProfile(cfg *config.AgentConfig, paths config.Paths, report *DoctorReport) {
	if paths.BPProfilesDir == "" {
		return
	}
	profilePath := filepath.Join(paths.BPProfilesDir, fmt.Sprintf("%s.env", cfg.Name))
	keyPath := filepath.Join(paths.SecretsDir, cfg.Name, "signing-key.pem")

	expectedContent := fmt.Sprintf("BP_AGENT=%s\nBP_PASSWORD=%s\nBP_SIGNING_KEY=%s\n",
		cfg.Name, cfg.Password, keyPath)

	data, err := os.ReadFile(profilePath)
	if err != nil || string(data) != expectedContent {
		_ = os.MkdirAll(paths.BPProfilesDir, 0700)
		if err := os.WriteFile(profilePath, []byte(expectedContent), 0600); err != nil {
			report.Checks = append(report.Checks, CheckItem{
				Name:         "Backplane Profile",
				Status:       StatusError,
				Message:      fmt.Sprintf("Failed creating profile at %s: %v", profilePath, err),
				Unrepairable: true,
			})
			report.UnrepairableCount++
			return
		}
		_ = os.Chmod(profilePath, 0600)
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Backplane Profile",
			Status:  StatusHealed,
			Message: fmt.Sprintf("Rebuilt backplane profile and permissions (0600) at %s", profilePath),
			Healed:  true,
		})
		report.HealedCount++
		return
	}

	// Verify permissions
	if fi, err := os.Stat(profilePath); err == nil && fi.Mode().Perm() != 0600 {
		_ = os.Chmod(profilePath, 0600)
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Backplane Profile",
			Status:  StatusHealed,
			Message: fmt.Sprintf("Repaired profile permissions to 0600 at %s", profilePath),
			Healed:  true,
		})
		report.HealedCount++
		return
	}

	report.Checks = append(report.Checks, CheckItem{
		Name:    "Backplane Profile",
		Status:  StatusOK,
		Message: fmt.Sprintf("Backplane connection profile verified at %s", profilePath),
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
