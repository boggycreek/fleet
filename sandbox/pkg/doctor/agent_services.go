// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
	"github.com/boggycreek/sandbox/pkg/config"
	"github.com/boggycreek/sandbox/pkg/forgejo"
	"github.com/boggycreek/sandbox/pkg/sonar"
)

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

	needsHeal := false
	if existingID, err := client.GetIdentity(ctx, cfg.Name); err != nil || existingID == nil || existingID.PubKey != cfg.PublicKeyB64 || existingID.Role != cfg.Role {
		needsHeal = true
	}
	if userVal, err := client.Exec(ctx, "ACL", "GETUSER", cfg.Name); err != nil || userVal.IsNull || len(userVal.Array) == 0 {
		needsHeal = true
	}

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
	if _, err := client.Exec(ctx, "ACL", aclArgs...); err != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:         "Valkey Backplane",
			Status:       StatusError,
			Message:      fmt.Sprintf("Failed setting Valkey ACL for %s: %v", cfg.Name, err),
			Unrepairable: true,
		})
		report.UnrepairableCount++
		return
	}

	if err := client.RegisterIdentity(ctx, libbp.IdentityRecord{
		Name:   cfg.Name,
		Role:   cfg.Role,
		Kind:   "agent",
		PubKey: cfg.PublicKeyB64,
	}); err != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:         "Valkey Backplane",
			Status:       StatusError,
			Message:      fmt.Sprintf("Failed registering Valkey identity for %s: %v", cfg.Name, err),
			Unrepairable: true,
		})
		report.UnrepairableCount++
		return
	}

	if needsHeal {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Valkey Backplane",
			Status:  StatusHealed,
			Message: "Repaired Valkey ACL permissions and registry identity",
			Healed:  true,
		})
		report.HealedCount++
		return
	}

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

	if err := client.EnsureUser(ctx, cfg.Name, cfg.Password, fmt.Sprintf("%s@local.fleet", cfg.Name)); err != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:         "Forgejo Git Forge",
			Status:       StatusError,
			Message:      fmt.Sprintf("Failed ensuring Forgejo user for %s: %v", cfg.Name, err),
			Unrepairable: true,
		})
		report.UnrepairableCount++
		return
	}
	sshKey := paths.GetSSHPrivateKey()
	if sshKey == "" {
		sshKey = paths.IDEKeyFile
	}
	sshKeyPub := fmt.Sprintf("%s.pub", sshKey)
	if keyData, err := os.ReadFile(sshKeyPub); err == nil && len(keyData) > 0 {
		if err := client.AddUserSSHKey(ctx, cfg.Name, fmt.Sprintf("%s-ide-key", cfg.Name), string(keyData)); err != nil {
			report.Checks = append(report.Checks, CheckItem{
				Name:         "Forgejo Git Forge",
				Status:       StatusError,
				Message:      fmt.Sprintf("Failed registering SSH key for %s in Forgejo: %v", cfg.Name, err),
				Unrepairable: true,
			})
			report.UnrepairableCount++
			return
		}
	}
	if err := client.AddOrgMember(ctx, "fleet", cfg.Name); err != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:         "Forgejo Git Forge",
			Status:       StatusError,
			Message:      fmt.Sprintf("Failed adding %s to fleet organization in Forgejo: %v", cfg.Name, err),
			Unrepairable: true,
		})
		report.UnrepairableCount++
		return
	}

	report.Checks = append(report.Checks, CheckItem{
		Name:    "Forgejo Git Forge",
		Status:  StatusOK,
		Message: "User account, SSH key, and fleet organization membership verified",
	})
}

func checkAndHealSonar(ctx context.Context, cfg *config.AgentConfig, paths config.Paths, report *DoctorReport) {
	if cfg.IsNoContainer() || os.Getenv("SONAR_DISABLED") == "1" {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "SonarQube Analysis",
			Status:  StatusOK,
			Message: "No-container agent or SonarQube disabled (no SonarQube account required)",
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

	reqAuth, _ := http.NewRequestWithContext(ctx, "GET", sonarURL+"/api/users/current", nil)
	reqAuth.SetBasicAuth(adminUser, adminPass)
	respAuth, errAuth := (&http.Client{Timeout: 1 * time.Second}).Do(reqAuth)
	if errAuth != nil || respAuth == nil || respAuth.StatusCode != http.StatusOK {
		if respAuth != nil {
			_ = respAuth.Body.Close()
		}
		report.Checks = append(report.Checks, CheckItem{
			Name:    "SonarQube Analysis",
			Status:  StatusWarning,
			Message: "SonarQube infrastructure is offline (skipped user sync; start via `fleet infra up`)",
		})
		report.WarningCount++
		return
	}
	_ = respAuth.Body.Close()

	_ = client.EnsureUser(ctx, cfg.Name, cfg.Password, cfg.Name, fmt.Sprintf("%s@local.fleet", cfg.Name))
	if cfg.SonarToken == "" {
		token, err := client.GenerateUserToken(ctx, cfg.Name, fmt.Sprintf("%s-agent-token", cfg.Name))
		if err != nil || token == "" {
			report.Checks = append(report.Checks, CheckItem{
				Name:         "SonarQube Analysis",
				Status:       StatusError,
				Message:      fmt.Sprintf("Failed generating SonarQube token for %s: %v", cfg.Name, err),
				Unrepairable: true,
			})
			report.UnrepairableCount++
			return
		}
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

	report.Checks = append(report.Checks, CheckItem{
		Name:    "SonarQube Analysis",
		Status:  StatusOK,
		Message: "User account and analysis token verified",
	})
}
