// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/boggycreek/fleet/cli/pkg/config"
)

const secretMask = "********"

// ValkeyInfraInfo holds connection and credential details for Valkey.
type ValkeyInfraInfo struct {
	Status           string `json:"status"`
	Container        string `json:"container"`
	InternalEndpoint string `json:"internal_endpoint"`
	HostPort         string `json:"host_port"`
	ConnectionURL    string `json:"connection_url"`
	CLICommand       string `json:"cli_command"`
	OperatorUser     string `json:"operator_user"`
	OperatorPassword string `json:"operator_password"`
}

// ForgejoInfraInfo holds connection and credential details for Forgejo.
type ForgejoInfraInfo struct {
	Status        string `json:"status"`
	Container     string `json:"container"`
	WebURL        string `json:"web_url"`
	InternalURL   string `json:"internal_url"`
	SSHPort       string `json:"ssh_port"`
	GitHTTPClone  string `json:"git_http_clone"`
	GitSSHClone   string `json:"git_ssh_clone"`
	AdminUser     string `json:"admin_user"`
	AdminPassword string `json:"admin_password"`
}

// GiteaInfraInfo is a backward-compatible alias for ForgejoInfraInfo.
type GiteaInfraInfo = ForgejoInfraInfo

// PostgresInfraInfo holds connection and credential details for PostgreSQL.
type PostgresInfraInfo struct {
	Status           string `json:"status"`
	Container        string `json:"container"`
	InternalEndpoint string `json:"internal_endpoint"`
	HostPort         string `json:"host_port"`
	Database         string `json:"database"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	ConnectionURL    string `json:"connection_url"`
	CLICommand       string `json:"cli_command"`
}

// SonarQubeInfraInfo holds connection and credential details for SonarQube.
type SonarQubeInfraInfo struct {
	Status        string `json:"status"`
	Container     string `json:"container"`
	WebURL        string `json:"web_url"`
	InternalURL   string `json:"internal_url"`
	AdminUser     string `json:"admin_user"`
	AdminPassword string `json:"admin_password"`
}

// OpenRouterInfraInfo holds status and details for OpenRouter proxy gateway.
type OpenRouterInfraInfo struct {
	Status        string `json:"status"`
	Container     string `json:"container"`
	InternalURL   string `json:"internal_url"`
	HostEndpoint  string `json:"host_endpoint"`
	KeyConfigured bool   `json:"key_configured"`
}

// InfraStackInfo holds the complete information for all shared infrastructure services.
type InfraStackInfo struct {
	Valkey     *ValkeyInfraInfo     `json:"valkey"`
	Forgejo    *ForgejoInfraInfo    `json:"forgejo"`
	Gitea      *ForgejoInfraInfo    `json:"gitea,omitempty"`
	PostgreSQL *PostgresInfraInfo   `json:"postgresql"`
	SonarQube  *SonarQubeInfraInfo  `json:"sonarqube"`
	OpenRouter *OpenRouterInfraInfo `json:"openrouter"`
}

type infoPair struct {
	key   string
	value string
}

type infoSection struct {
	header string
	pairs  []infoPair
}

// GetInfraInfo inspects the runtime state of all shared infrastructure services
// and compiles endpoint URLs, ports, and connection strings.
func GetInfraInfo(ctx context.Context, paths config.Paths, showSecrets bool) (*InfraStackInfo, error) {
	states := make(map[string]string)
	stack, err := InspectInfraStack(ctx)
	if err == nil {
		for _, item := range stack {
			displayName := item.ID
			if len(item.Names) > 0 && item.Names[0] != "" {
				displayName = item.Names[0]
			}
			states[displayName] = item.State
			states[item.ID] = item.State
		}
	}

	getState := func(ctr string) string {
		if s, ok := states[ctr]; ok && s != "" {
			return s
		}
		return "stopped"
	}

	// 1. Valkey
	valkeyPort := os.Getenv("BP_PORT")
	if valkeyPort == "" {
		valkeyPort = "6379"
	}
	adminPass := os.Getenv("ADMIN_BACKPLANE_PASSWORD")
	if adminPass == "" {
		adminPass = "admin_backplane_pass"
	}
	humanPass := os.Getenv("HUMAN_BACKPLANE_PASSWORD")
	if humanPass == "" {
		humanPass = adminPass
	}
	humanUser := os.Getenv("HUMAN_NAME")
	if humanUser == "" {
		humanUser = "human"
	}

	valkeyDisplayPass := secretMask
	if showSecrets {
		valkeyDisplayPass = humanPass
	}

	valkeyInfo := &ValkeyInfraInfo{
		Status:           getState(infraValkeyContainer),
		Container:        infraValkeyContainer,
		InternalEndpoint: fmt.Sprintf("valkey:%s", valkeyPort),
		HostPort:         fmt.Sprintf("127.0.0.1:%s", valkeyPort),
		ConnectionURL:    fmt.Sprintf("valkey://%s:%s@127.0.0.1:%s", humanUser, valkeyDisplayPass, valkeyPort),
		CLICommand:       fmt.Sprintf("valkey-cli -h 127.0.0.1 -p %s -a %s", valkeyPort, valkeyDisplayPass),
		OperatorUser:     humanUser,
		OperatorPassword: valkeyDisplayPass,
	}

	// 2. Forgejo
	forgejoPass := os.Getenv("FORGEJO_ADMIN_PASSWORD")
	if forgejoPass == "" {
		forgejoPass = os.Getenv("GITEA_ADMIN_PASSWORD")
	}
	if forgejoPass == "" {
		forgejoPass = adminPass
	}

	forgejoURL := os.Getenv("FORGEJO_URL")
	if forgejoURL == "" {
		forgejoURL = os.Getenv("GITEA_URL")
	}
	if forgejoURL == "" {
		forgejoURL = "http://127.0.0.1:3000"
	}
	cleanForgejoURL := strings.TrimRight(forgejoURL, "/")

	forgejoUser := os.Getenv("FORGEJO_ADMIN_USER")
	if forgejoUser == "" {
		forgejoUser = os.Getenv("GITEA_ADMIN_USER")
	}
	if forgejoUser == "" {
		forgejoUser = "forgejoadmin"
	}

	forgejoDisplayPass := secretMask
	if showSecrets {
		forgejoDisplayPass = forgejoPass
	}

	forgejoInfo := &ForgejoInfraInfo{
		Status:        getState(infraForgejoContainer),
		Container:     infraForgejoContainer,
		WebURL:        cleanForgejoURL,
		InternalURL:   "http://forgejo:3000",
		SSHPort:       "127.0.0.1:2223",
		GitHTTPClone:  fmt.Sprintf("%s/fleet/tasks.git", cleanForgejoURL),
		GitSSHClone:   "ssh://git@127.0.0.1:2223/fleet/tasks.git",
		AdminUser:     forgejoUser,
		AdminPassword: forgejoDisplayPass,
	}

	// 3. PostgreSQL
	pgPass := "sonar"
	pgDisplayPass := secretMask
	pgCLI := "psql -h 127.0.0.1 -p 5432 -U sonar -d sonar"
	if showSecrets {
		pgDisplayPass = pgPass
		pgCLI = fmt.Sprintf("PGPASSWORD=%s psql -h 127.0.0.1 -p 5432 -U sonar -d sonar", pgPass)
	}

	postgresInfo := &PostgresInfraInfo{
		Status:           getState(infraPostgresContainer),
		Container:        infraPostgresContainer,
		InternalEndpoint: "postgres:5432",
		HostPort:         "127.0.0.1:5432",
		Database:         "sonar",
		Username:         "sonar",
		Password:         pgDisplayPass,
		ConnectionURL:    fmt.Sprintf("postgresql://sonar:%s@127.0.0.1:5432/sonar", pgDisplayPass),
		CLICommand:       pgCLI,
	}

	// 4. SonarQube
	sonarStatus := getState(infraSonarContainer)
	if sonarStatus == "stopped" {
		if os.Getenv("SONAR_DISABLED") == "1" {
			sonarStatus = "disabled"
		} else if err := CommandContext(ctx, "podman", "image", "exists", "fleet-sonarqube:latest").Run(); err != nil {
			sonarStatus = "not_deployed"
		}
	}

	sonarURL := os.Getenv("SONAR_HOST_URL")
	if sonarURL == "" {
		sonarURL = os.Getenv("SONARQUBE_URL")
	}
	if sonarURL == "" {
		sonarURL = "http://127.0.0.1:9000"
	}
	cleanSonarURL := strings.TrimRight(sonarURL, "/")

	sonarUser := os.Getenv("SONAR_ADMIN_USER")
	if sonarUser == "" {
		sonarUser = "admin"
	}
	sonarPass := config.GetSonarAdminPassword()
	sonarDisplayPass := secretMask
	if showSecrets {
		sonarDisplayPass = sonarPass
	}

	sonarInfo := &SonarQubeInfraInfo{
		Status:        sonarStatus,
		Container:     infraSonarContainer,
		WebURL:        cleanSonarURL,
		InternalURL:   "http://sonarqube:9000",
		AdminUser:     sonarUser,
		AdminPassword: sonarDisplayPass,
	}

	// 5. OpenRouter Proxy
	orKey := config.GetOpenRouterAPIKey(paths)
	orStatus := getState(infraOpenRouterContainer)
	if orStatus == "stopped" && orKey == "" {
		orStatus = "not_configured"
	}

	openRouterInfo := &OpenRouterInfraInfo{
		Status:        orStatus,
		Container:     infraOpenRouterContainer,
		InternalURL:   "http://fleet-openrouter:8080/v1",
		HostEndpoint:  "http://127.0.0.1:8080/v1",
		KeyConfigured: orKey != "",
	}

	return &InfraStackInfo{
		Valkey:     valkeyInfo,
		Forgejo:    forgejoInfo,
		Gitea:      forgejoInfo,
		PostgreSQL: postgresInfo,
		SonarQube:  sonarInfo,
		OpenRouter: openRouterInfo,
	}, nil
}

// FormatInfraInfo renders InfraStackInfo into an indented, fixed-justified TOML-style representation.
func FormatInfraInfo(info *InfraStackInfo) string {
	if info == nil {
		return ""
	}

	var sections []infoSection

	if info.Valkey != nil {
		sections = append(sections, infoSection{
			header: "[valkey]",
			pairs: []infoPair{
				{"status", info.Valkey.Status},
				{"container", info.Valkey.Container},
				{"internal_endpoint", info.Valkey.InternalEndpoint},
				{"host_port", info.Valkey.HostPort},
				{"connection_url", info.Valkey.ConnectionURL},
				{"cli_command", info.Valkey.CLICommand},
				{"operator_user", info.Valkey.OperatorUser},
				{"operator_password", info.Valkey.OperatorPassword},
			},
		})
	}

	if info.OpenRouter != nil {
		sections = append(sections, infoSection{
			header: "[openrouter]",
			pairs: []infoPair{
				{"status", info.OpenRouter.Status},
				{"container", info.OpenRouter.Container},
				{"internal_url", info.OpenRouter.InternalURL},
				{"host_endpoint", info.OpenRouter.HostEndpoint},
				{"key_configured", fmt.Sprintf("%t", info.OpenRouter.KeyConfigured)},
			},
		})
	}

	forgejoSec := info.Forgejo
	if forgejoSec == nil {
		forgejoSec = info.Gitea
	}
	if forgejoSec != nil {
		sections = append(sections, infoSection{
			header: "[forgejo]",
			pairs: []infoPair{
				{"status", forgejoSec.Status},
				{"container", forgejoSec.Container},
				{"web_url", forgejoSec.WebURL},
				{"internal_url", forgejoSec.InternalURL},
				{"ssh_port", forgejoSec.SSHPort},
				{"git_http_clone", forgejoSec.GitHTTPClone},
				{"git_ssh_clone", forgejoSec.GitSSHClone},
				{"admin_user", forgejoSec.AdminUser},
				{"admin_password", forgejoSec.AdminPassword},
			},
		})
	}

	if info.PostgreSQL != nil {
		sections = append(sections, infoSection{
			header: "[postgresql]",
			pairs: []infoPair{
				{"status", info.PostgreSQL.Status},
				{"container", info.PostgreSQL.Container},
				{"internal_endpoint", info.PostgreSQL.InternalEndpoint},
				{"host_port", info.PostgreSQL.HostPort},
				{"database", info.PostgreSQL.Database},
				{"username", info.PostgreSQL.Username},
				{"password", info.PostgreSQL.Password},
				{"connection_url", info.PostgreSQL.ConnectionURL},
				{"cli_command", info.PostgreSQL.CLICommand},
			},
		})
	}

	if info.SonarQube != nil {
		sections = append(sections, infoSection{
			header: "[sonarqube]",
			pairs: []infoPair{
				{"status", info.SonarQube.Status},
				{"container", info.SonarQube.Container},
				{"web_url", info.SonarQube.WebURL},
				{"internal_url", info.SonarQube.InternalURL},
				{"admin_user", info.SonarQube.AdminUser},
				{"admin_password", info.SonarQube.AdminPassword},
			},
		})
	}

	// Calculate maximum key length (with colon) across all sections
	maxKeyLen := 0
	for _, sec := range sections {
		for _, p := range sec.pairs {
			keyLen := len(p.key) + 1 // key + ":"
			if keyLen > maxKeyLen {
				maxKeyLen = keyLen
			}
		}
	}

	var sb strings.Builder
	for i, sec := range sections {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(sec.header)
		sb.WriteString("\n")
		for _, p := range sec.pairs {
			keyWithColon := p.key + ":"
			padding := strings.Repeat(" ", maxKeyLen-len(keyWithColon)+2)
			sb.WriteString("  ")
			sb.WriteString(keyWithColon)
			sb.WriteString(padding)
			sb.WriteString(p.value)
			sb.WriteString("\n")
		}
	}

	return sb.String()
}
