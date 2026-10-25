// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package lifecycle manages provisioning and deprovisioning of agent infrastructure.
package lifecycle

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
	"github.com/boggycreek/sandbox/pkg/config"
	"github.com/boggycreek/sandbox/pkg/forgejo"
	"github.com/boggycreek/sandbox/pkg/runtime"
	"github.com/boggycreek/sandbox/pkg/sonar"
)

// DeprovisionValkeyUser removes the agent's Valkey ACL user, streams, and identity registration.
func DeprovisionValkeyUser(ctx context.Context, agentName string) error {
	bpCfg := libbp.LoadClientFromEnv()
	client, err := libbp.Dial(ctx, libbp.ClientConfig{
		Host:     bpCfg.Host,
		Port:     bpCfg.Port,
		Username: "admin",
		Password: os.Getenv("ADMIN_BACKPLANE_PASSWORD"),
	})
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	// Delete ACL user
	if _, execErr := client.Exec(ctx, "ACL", "DELUSER", agentName); execErr != nil {
		return execErr
	}

	// Clean up backplane keys & identity
	_, err = client.Exec(ctx, "DEL",
		fmt.Sprintf("identity:%s", agentName),
		fmt.Sprintf("%s:inbox", agentName),
		fmt.Sprintf("%s:out", agentName),
		fmt.Sprintf("%s:seq", agentName),
		fmt.Sprintf("%s:cursor", agentName),
		fmt.Sprintf("%s:finger", agentName),
		fmt.Sprintf("%s:status", agentName),
	)
	return err
}

// DeprovisionForgejoUser removes the agent account and public keys from the local Forgejo instance.
func DeprovisionForgejoUser(ctx context.Context, agentName string) error {
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

	client := forgejo.NewClient(forgejo.ClientConfig{
		BaseURL:   forgejoURL,
		AdminUser: adminUser,
		AdminPass: adminPass,
		Timeout:   3 * time.Second,
	})

	err := client.DeleteUser(ctx, agentName, true)
	if err != nil && (strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "user does not exist")) {
		return nil
	}
	return err
}

// DeprovisionGiteaUser aliases DeprovisionForgejoUser for backward compatibility.
func DeprovisionGiteaUser(ctx context.Context, agentName string) error {
	return DeprovisionForgejoUser(ctx, agentName)
}

// DeprovisionSonarUser revokes the agent's analysis token and deactivates the agent account in SonarQube.
func DeprovisionSonarUser(ctx context.Context, agentName string) error {
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
		Timeout:   2 * time.Second,
	})

	if tokenErr := client.RevokeUserToken(ctx, agentName, fmt.Sprintf("%s-agent-token", agentName)); tokenErr != nil && IsOfflineError(tokenErr) {
		return tokenErr
	}
	err := client.DeactivateUser(ctx, agentName)
	if err != nil && (strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "user does not exist")) {
		return nil
	}
	return err
}

// DeprovisionAgent deprovisions container, storage, configuration, and infrastructure for an agent.
//
//nolint:gocritic // paths passed by value for consistency with config package
func DeprovisionAgent(ctx context.Context, cfg *config.AgentConfig, paths config.Paths) *ProvisioningReport {
	report := NewProvisioningReport(cfg.Name, "deprovision")

	// 1. Destroy container and persistent volume
	if cfg.IsNoContainer() {
		report.AddStep("Container & Volume", StatusSkipped, "No-container agent has no container or volume to destroy (skipped)", nil)
	} else if err := runtime.DestroyAgentContainer(ctx, cfg.ContainerName, cfg.VolumeName); err != nil {
		report.AddStep("Container & Volume", StatusError, fmt.Sprintf("Failed to remove container/volume: %v", err), err)
	} else {
		report.AddStep("Container & Volume", StatusOK, fmt.Sprintf("Removed container (%s) and volume (%s)", cfg.ContainerName, cfg.VolumeName), nil)
	}

	// 2. Clear per-agent known hosts
	if cfg.IsNoContainer() {
		report.AddStep("Known Hosts", StatusSkipped, "No-container agent has no SSH host entry (skipped)", nil)
	} else {
		runtime.ClearAgentKnownHosts(cfg.Name, paths)
		report.AddStep("Known Hosts", StatusOK, "Cleared agent SSH known hosts entry", nil)
	}

	// 3. Deprovision Valkey ACL user & streams
	if err := DeprovisionValkeyUser(ctx, cfg.Name); err == nil {
		report.AddStep("Valkey Identity", StatusOK, "Removed Valkey ACL user and backplane identity", nil)
	} else if IsOfflineError(err) {
		report.AddStep("Valkey Identity", StatusSkipped, fmt.Sprintf("Valkey infrastructure is offline (skipped): %v", err), err)
	} else {
		report.AddStep("Valkey Identity", StatusError, fmt.Sprintf("Failed to remove Valkey identity: %v", err), err)
	}

	// 4. Deprovision Forgejo user & keys
	if cfg.IsNoContainer() {
		report.AddStep("Forgejo Account", StatusSkipped, "No-container agent has no Forgejo account (skipped)", nil)
	} else if os.Getenv("FORGEJO_DISABLED") == "1" || os.Getenv("GITEA_DISABLED") == "1" {
		report.AddStep("Forgejo Account", StatusSkipped, "Forgejo infrastructure is disabled via FORGEJO_DISABLED (skipped)", nil)
	} else if err := DeprovisionForgejoUser(ctx, cfg.Name); err == nil {
		report.AddStep("Forgejo Account", StatusOK, "Purged Forgejo user account and authorized keys", nil)
	} else if IsOfflineError(err) {
		report.AddStep("Forgejo Account", StatusSkipped, fmt.Sprintf("Forgejo infrastructure is offline (skipped): %v", err), err)
	} else {
		report.AddStep("Forgejo Account", StatusError, fmt.Sprintf("Failed to purge Forgejo user: %v", err), err)
	}

	// 5. Deprovision SonarQube user & analysis tokens
	if cfg.IsNoContainer() {
		report.AddStep("SonarQube Account", StatusSkipped, "No-container agent has no SonarQube account (skipped)", nil)
	} else if os.Getenv("SONAR_DISABLED") == "1" || (cfg.SonarToken == "" && os.Getenv("SONAR_HOST_URL") == "") {
		report.AddStep("SonarQube Account", StatusSkipped, "SonarQube is disabled or not provisioned for agent (skipped)", nil)
	} else if err := DeprovisionSonarUser(ctx, cfg.Name); err == nil {
		report.AddStep("SonarQube Account", StatusOK, "Purged SonarQube user account and analysis tokens", nil)
	} else if IsOfflineError(err) {
		report.AddStep("SonarQube Account", StatusSkipped, fmt.Sprintf("SonarQube infrastructure is offline (skipped): %v", err), err)
	} else {
		report.AddStep("SonarQube Account", StatusError, fmt.Sprintf("Failed to deprovision SonarQube user: %v", err), err)
	}

	// 6. Update SSH config file
	if cfg.IsNoContainer() {
		report.AddStep("SSH Configuration", StatusSkipped, "No-container agent has no SSH config stanza (skipped)", nil)
	} else if err := runtime.SyncSSHConfigFile(ctx, paths); err != nil {
		report.AddStep("SSH Configuration", StatusWarning, fmt.Sprintf("Failed to synchronize SSH config: %v", err), err)
	} else {
		report.AddStep("SSH Configuration", StatusOK, "Synchronized host SSH config", nil)
	}

	// 7. Delete local config and secrets (Done last so config remains if other steps fail and we need to retry)
	if report.HasErrors() {
		report.AddStep("Local Configuration", StatusSkipped, "Retained local configuration for future retry due to previous errors", nil)
	} else if err := config.DeleteAgentConfig(cfg.Name, paths); err != nil {
		report.AddStep("Local Configuration", StatusError, fmt.Sprintf("Failed to delete local configuration: %v", err), err)
	} else {
		report.AddStep("Local Configuration", StatusOK, "Purged local configuration, secrets, and profile", nil)
	}

	return report
}
