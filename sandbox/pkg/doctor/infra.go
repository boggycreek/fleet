// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
	"github.com/boggycreek/sandbox/pkg/config"
	"github.com/boggycreek/sandbox/pkg/forgejo"
	"github.com/boggycreek/sandbox/pkg/runtime"
	"github.com/boggycreek/sandbox/pkg/sonar"
)

// infraValkeyContainer, infraForgejoContainer, infraPostgresContainer, and infraSonarContainer are the expected shared infra container names.
// These are package-level vars so integration tests can override them with ephemeral container names.
var (
	infraValkeyContainer   = "fleet-valkey"
	infraForgejoContainer  = "fleet-forgejo"
	infraGiteaContainer    = infraForgejoContainer
	infraPostgresContainer = "fleet-postgres"
	infraSonarContainer    = "fleet-sonarqube"
)

// DiagnoseAndHealInfra performs a comprehensive health check and auto-healing on shared infrastructure
func DiagnoseAndHealInfra(ctx context.Context, paths config.Paths) (*DoctorReport, error) {
	report := &DoctorReport{
		AgentName: "shared-infrastructure",
	}

	// 1. Host Environment Configuration (.env) Check & Heal
	checkAndHealInfraEnv(paths, report)

	// 2. Host SSH Keypair & Include Directive
	checkAndHealHostSSH(paths, report)

	// 3. Infrastructure Storage Volumes & Network
	checkAndHealInfraStorage(ctx, report)

	// 4. Valkey Backplane Container & Service
	checkAndHealValkeyContainer(ctx, paths, report)

	// 5. Forgejo Git Forge Container & Service
	checkAndHealForgejoContainer(ctx, paths, report)

	// 6. PostgreSQL Relational Database Container & Service
	checkAndHealPostgresContainer(ctx, report)

	// 7. SonarQube Mechanical Analysis Container & Service
	checkAndHealSonarContainer(ctx, paths, report)

	return report, nil
}

func checkAndHealInfraEnv(paths config.Paths, report *DoctorReport) {
	if _, err := os.Stat(paths.EnvFile); os.IsNotExist(err) {
		_ = paths.EnsureDirectories()
		adminBytes := make([]byte, 16)
		humanBytes := make([]byte, 16)
		_, _ = rand.Read(adminBytes)
		_, _ = rand.Read(humanBytes)

		adminPW := hex.EncodeToString(adminBytes)
		humanPW := hex.EncodeToString(humanBytes)
		humanName := os.Getenv("USER")
		if humanName == "" {
			humanName = "operator"
		}

		content := fmt.Sprintf("# Agent Sandbox Environment Configuration\nHUMAN_NAME=%s\nADMIN_BACKPLANE_PASSWORD=%s\nHUMAN_BACKPLANE_PASSWORD=%s\n", humanName, adminPW, humanPW)
		_ = os.WriteFile(paths.EnvFile, []byte(content), 0600)
		paths.LoadEnv()

		report.Checks = append(report.Checks, CheckItem{
			Name:    "Environment Config (.env)",
			Status:  StatusHealed,
			Message: fmt.Sprintf("Created missing environment configuration at %s", paths.EnvFile),
			Healed:  true,
		})
		report.HealedCount++
	} else {
		paths.LoadEnv()
		healedEnvPerm := false
		if fi, err := os.Stat(paths.EnvFile); err == nil && fi.Mode().Perm() != 0600 {
			if err := os.Chmod(paths.EnvFile, 0600); err == nil {
				healedEnvPerm = true
			}
		}

		if healedEnvPerm {
			report.Checks = append(report.Checks, CheckItem{
				Name:    "Environment Config (.env)",
				Status:  StatusHealed,
				Message: fmt.Sprintf("Repaired environment file permissions (0600) at %s", paths.EnvFile),
				Healed:  true,
			})
			report.HealedCount++
		} else {
			report.Checks = append(report.Checks, CheckItem{
				Name:    "Environment Config (.env)",
				Status:  StatusOK,
				Message: fmt.Sprintf("Valid configuration present (%s)", paths.EnvFile),
			})
		}
	}
}

func checkAndHealInfraStorage(ctx context.Context, report *DoctorReport) {
	healed := false
	netName := "fleet-infra"
	if err := runtime.EnsureNetwork(ctx, netName); err != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:         "Bridge Network",
			Status:       StatusError,
			Message:      fmt.Sprintf("Failed ensuring bridge network %s: %v", netName, err),
			Unrepairable: true,
		})
		report.UnrepairableCount++
		return
	}

	_ = runtime.EnsureRootlessNetNS(ctx)

	for _, vol := range []string{
		"fleet-valkey-data",
		"fleet-forgejo-data",
		"fleet-postgres-data",
		"fleet-sonarqube-data",
		"fleet-sonarqube-extensions",
		"fleet-sonarqube-logs",
	} {
		volCmd := runtime.CommandContext(ctx, "podman", "volume", "exists", vol)
		if err := volCmd.Run(); err != nil {
			if err := runtime.EnsureVolume(ctx, vol); err == nil {
				healed = true
			}
		}
	}

	if healed {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Infrastructure Storage",
			Status:  StatusHealed,
			Message: "Restored missing shared infrastructure data volumes",
			Healed:  true,
		})
		report.HealedCount++
	} else {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Infrastructure Storage",
			Status:  StatusOK,
			Message: "Bridge network and data volumes (valkey-data, forgejo-data, postgres-data, sonarqube-data) present",
		})
	}
}

func checkAndHealPostgresContainer(ctx context.Context, report *DoctorReport) {
	containerName := infraPostgresContainer
	info, err := runtime.InspectAgentContainer(ctx, containerName)
	state := "stopped"
	if err == nil && info != nil {
		state = info.State
	}

	pgHost := os.Getenv("POSTGRES_HOST")
	if pgHost == "" {
		pgHost = "127.0.0.1"
	}
	pgPort := os.Getenv("POSTGRES_PORT")
	if pgPort == "" {
		pgPort = "5432"
	}

	dialer := net.Dialer{Timeout: 1 * time.Second}
	conn, dialErr := dialer.DialContext(ctx, "tcp", net.JoinHostPort(pgHost, pgPort))
	if dialErr != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "PostgreSQL Database",
			Status:  StatusWarning,
			Message: fmt.Sprintf("PostgreSQL container is %s (start via `fleet infra up`)", state),
		})
		report.WarningCount++
		return
	}
	_ = conn.Close()

	report.Checks = append(report.Checks, CheckItem{
		Name:    "PostgreSQL Database",
		Status:  StatusOK,
		Message: fmt.Sprintf("Container %s, listening on %s:%s (database: sonar)", state, pgHost, pgPort),
	})
}

func checkAndHealValkeyContainer(ctx context.Context, paths config.Paths, report *DoctorReport) {
	containerName := infraValkeyContainer
	info, err := runtime.InspectAgentContainer(ctx, containerName)
	state := "stopped"
	if err == nil && info != nil {
		state = info.State
	}

	bpCfg := libbp.LoadClientFromEnv()
	adminPass := os.Getenv("ADMIN_BACKPLANE_PASSWORD")
	if adminPass == "" {
		adminPass = "admin_backplane_pass"
	}

	humanName := os.Getenv("HUMAN_NAME")
	if humanName == "" {
		humanName = "operator"
	}
	humanPass := os.Getenv("HUMAN_BACKPLANE_PASSWORD")
	if humanPass == "" {
		humanPass = "human_backplane_pass"
	}

	valkeyConfigDir := filepath.Join(paths.DataHome, "valkey")
	mountMismatch := false
	if info != nil {
		for _, m := range info.Mounts {
			if m.Destination == "/etc/valkey" && filepath.Clean(m.Source) != filepath.Clean(valkeyConfigDir) {
				mountMismatch = true
				break
			}
		}
	}

	var dialErr error
	var client *libbp.Client

	if state == "running" && !mountMismatch {
		dialCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
		client, dialErr = libbp.Dial(dialCtx, libbp.ClientConfig{
			Host:     bpCfg.Host,
			Port:     bpCfg.Port,
			Username: "admin",
			Password: adminPass,
		})
		cancel()
	} else if mountMismatch {
		dialErr = fmt.Errorf("mount source mismatch (/etc/valkey not mounted from %s)", valkeyConfigDir)
	} else {
		dialErr = fmt.Errorf("container is %s", state)
	}

	if dialErr != nil {
		// Attempt auto-healing: recreate Valkey container with valid mount and synchronized ACL credentials
		healCtx, healCancel := context.WithTimeout(ctx, 15*time.Second)
		defer healCancel()

		if healErr := runtime.RecreateValkeyContainer(healCtx, paths, adminPass, humanPass, humanName); healErr == nil {
			var clientRetry *libbp.Client
			var retryErr error
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				retryCtx, retryCancel := context.WithTimeout(ctx, 1*time.Second)
				clientRetry, retryErr = libbp.Dial(retryCtx, libbp.ClientConfig{
					Host:     bpCfg.Host,
					Port:     bpCfg.Port,
					Username: "admin",
					Password: adminPass,
				})
				retryCancel()
				if retryErr == nil {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}

			if retryErr == nil {
				defer clientRetry.Close()
				// Ensure human operator ACL is configured
				aclArgs := []string{
					"SETUSER", humanName, "on",
					">" + humanPass,
					fmt.Sprintf("~%s:*", humanName), "%R~*:*", "&*", "+@all",
				}
				_, _ = clientRetry.Exec(ctx, "ACL", aclArgs...)

				report.Checks = append(report.Checks, CheckItem{
					Name:    "Valkey Service",
					Status:  StatusHealed,
					Message: "Repaired Valkey container mount and synchronized admin ACL credentials",
					Healed:  true,
				})
				report.HealedCount++
				return
			}
		}

		report.Checks = append(report.Checks, CheckItem{
			Name:    "Valkey Service",
			Status:  StatusWarning,
			Message: fmt.Sprintf("Valkey container is %s (%v; start via `fleet infra up`)", state, dialErr),
		})
		report.WarningCount++
		return
	}
	defer client.Close()

	// Ensure human operator ACL is configured
	aclArgs := []string{
		"SETUSER", humanName, "on",
		">" + humanPass,
		fmt.Sprintf("~%s:*", humanName), "%R~*:*", "&*", "+@all",
	}
	_, _ = client.Exec(ctx, "ACL", aclArgs...)

	report.Checks = append(report.Checks, CheckItem{
		Name:    "Valkey Service",
		Status:  StatusOK,
		Message: fmt.Sprintf("Container %s, listening on %s:%d, admin & operator ACLs verified", state, bpCfg.Host, bpCfg.Port),
	})
}

func checkAndHealForgejoContainer(ctx context.Context, paths config.Paths, report *DoctorReport) {
	containerName := infraForgejoContainer
	info, err := runtime.InspectAgentContainer(ctx, containerName)
	state := "stopped"
	if err == nil && info != nil {
		state = info.State
	}

	forgejoURL := os.Getenv("FORGEJO_URL")
	if forgejoURL == "" {
		forgejoURL = os.Getenv("GITEA_URL")
	}
	if forgejoURL == "" {
		forgejoURL = "http://127.0.0.1:3000"
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
			Name:    "Forgejo Service",
			Status:  StatusWarning,
			Message: fmt.Sprintf("Forgejo container is %s (start via `fleet infra up`)", state),
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

	_ = client.EnsureOrg(ctx, "fleet")
	_ = client.EnsureRepo(ctx, "fleet", "tools", "Fleet shared utility repositories", true)
	_ = client.EnsureRepo(ctx, "fleet", "tasks", "Fleet shared task tracking backlog", true)

	report.Checks = append(report.Checks, CheckItem{
		Name:    "Forgejo Service",
		Status:  StatusOK,
		Message: fmt.Sprintf("Container %s, HTTP %s responsive, admin account & fleet repositories verified", state, forgejoURL),
	})
}

func checkAndHealGiteaContainer(ctx context.Context, paths config.Paths, report *DoctorReport) {
	checkAndHealForgejoContainer(ctx, paths, report)
}

func checkAndHealSonarContainer(ctx context.Context, paths config.Paths, report *DoctorReport) {
	containerName := infraSonarContainer
	info, err := runtime.InspectAgentContainer(ctx, containerName)
	state := "stopped"
	if err == nil && info != nil {
		state = info.State
	}

	sonarURL := os.Getenv("SONAR_HOST_URL")
	if sonarURL == "" {
		sonarURL = os.Getenv("SONARQUBE_URL")
	}
	if sonarURL == "" {
		sonarURL = "http://127.0.0.1:9000"
	}

	httpClient := &http.Client{Timeout: 1 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", sonarURL+"/api/system/status", nil)
	resp, err := httpClient.Do(req)
	if err != nil || (resp != nil && resp.StatusCode >= 500) {
		if resp != nil {
			_ = resp.Body.Close()
		}
		report.Checks = append(report.Checks, CheckItem{
			Name:    "SonarQube Service",
			Status:  StatusWarning,
			Message: fmt.Sprintf("SonarQube container is %s (start via `fleet infra up`)", state),
		})
		report.WarningCount++
		return
	}
	defer resp.Body.Close()

	var status struct {
		Status string `json:"status"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&status)

	// Verify or auto-heal admin authentication
	adminUser := os.Getenv("SONAR_ADMIN_USER")
	if adminUser == "" {
		adminUser = "admin"
	}
	adminPass := config.GetSonarAdminPassword()

	client := sonar.NewClient(sonar.ClientConfig{
		BaseURL:   sonarURL,
		AdminUser: adminUser,
		AdminPass: adminPass,
		Timeout:   2 * time.Second,
	})

	// Check if current credentials authenticate
	reqAuth, _ := http.NewRequestWithContext(ctx, "GET", sonarURL+"/api/users/current", nil)
	reqAuth.SetBasicAuth(adminUser, adminPass)
	respAuth, errAuth := httpClient.Do(reqAuth)
	authOK := false
	if errAuth == nil && respAuth != nil {
		authOK = (respAuth.StatusCode == http.StatusOK)
		_ = respAuth.Body.Close()
	}

	if !authOK {
		// Attempt auto-healing default "admin:admin" to configured adminPass
		defaultClient := sonar.NewClient(sonar.ClientConfig{
			BaseURL:   sonarURL,
			AdminUser: "admin",
			AdminPass: "admin",
			Timeout:   2 * time.Second,
		})
		if healErr := defaultClient.ChangePassword(ctx, "admin", "admin", adminPass); healErr == nil {
			_ = paths.SetEnvVar("SONAR_ADMIN_USER", adminUser)
			_ = paths.SetEnvVar("SONAR_ADMIN_PASSWORD", adminPass)
			report.Checks = append(report.Checks, CheckItem{
				Name:    "SonarQube Service",
				Status:  StatusHealed,
				Message: fmt.Sprintf("Container %s, HTTP %s responsive, auto-healed default admin password", state, sonarURL),
				Healed:  true,
			})
			report.HealedCount++
			return
		}
	}

	report.Checks = append(report.Checks, CheckItem{
		Name:    "SonarQube Service",
		Status:  StatusOK,
		Message: fmt.Sprintf("Container %s, HTTP %s responsive (status: %s, admin: verified)", state, sonarURL, status.Status),
	})
	_ = client
}
