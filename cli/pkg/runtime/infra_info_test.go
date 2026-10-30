// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/config"
)

func TestGetInfraInfo_Masked(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{}

	t.Setenv("ADMIN_BACKPLANE_PASSWORD", "superadminpass")
	t.Setenv("HUMAN_BACKPLANE_PASSWORD", "superhumanpass")
	t.Setenv("HUMAN_NAME", "operator")
	t.Setenv("BP_PORT", "6380")
	t.Setenv("FORGEJO_URL", "http://127.0.0.1:3000/")
	t.Setenv("SONAR_HOST_URL", "http://127.0.0.1:9000/")
	t.Setenv("SONAR_ADMIN_USER", "sonaradmin")
	t.Setenv("SONAR_ADMIN_PASSWORD", "SuperSonar123")

	info, err := GetInfraInfo(ctx, paths, false)
	if err != nil {
		t.Fatalf("GetInfraInfo failed: %v", err)
	}

	if info.Valkey == nil || info.Forgejo == nil || info.PostgreSQL == nil || info.SonarQube == nil {
		t.Fatalf("expected all service structs to be non-nil")
	}

	// Verify backward-compatibility alias
	if info.Gitea != info.Forgejo {
		t.Errorf("expected Gitea alias to match Forgejo struct pointer")
	}

	// Verify masking
	if info.Valkey.OperatorPassword != secretMask {
		t.Errorf("expected valkey password masked, got: %s", info.Valkey.OperatorPassword)
	}
	if strings.Contains(info.Valkey.ConnectionURL, "superhumanpass") {
		t.Errorf("connection url leaked valkey password: %s", info.Valkey.ConnectionURL)
	}
	if !strings.Contains(info.Valkey.ConnectionURL, secretMask) {
		t.Errorf("expected masked connection url, got: %s", info.Valkey.ConnectionURL)
	}
	if strings.Contains(info.Valkey.CLICommand, "superhumanpass") {
		t.Errorf("cli command leaked valkey password: %s", info.Valkey.CLICommand)
	}

	if info.Forgejo.AdminPassword != secretMask {
		t.Errorf("expected forgejo admin password masked, got: %s", info.Forgejo.AdminPassword)
	}

	if info.PostgreSQL.Password != secretMask {
		t.Errorf("expected postgres password masked, got: %s", info.PostgreSQL.Password)
	}
	if strings.Contains(info.PostgreSQL.ConnectionURL, "sonar@") || !strings.Contains(info.PostgreSQL.ConnectionURL, secretMask) {
		t.Errorf("expected postgres connection url masked, got: %s", info.PostgreSQL.ConnectionURL)
	}
	if strings.Contains(info.PostgreSQL.CLICommand, "PGPASSWORD") {
		t.Errorf("expected psql cli command not to include password when masked, got: %s", info.PostgreSQL.CLICommand)
	}

	if info.SonarQube.AdminPassword != secretMask {
		t.Errorf("expected sonarqube admin password masked, got: %s", info.SonarQube.AdminPassword)
	}

	// Verify endpoints
	if info.Valkey.HostPort != "127.0.0.1:6380" {
		t.Errorf("expected port 6380, got: %s", info.Valkey.HostPort)
	}
	if info.Forgejo.WebURL != "http://127.0.0.1:3000" {
		t.Errorf("expected trimmed forgejo URL, got: %s", info.Forgejo.WebURL)
	}
	if info.SonarQube.WebURL != "http://127.0.0.1:9000" {
		t.Errorf("expected trimmed sonar URL, got: %s", info.SonarQube.WebURL)
	}
}

func TestGetInfraInfo_ShowSecrets(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{}

	t.Setenv("ADMIN_BACKPLANE_PASSWORD", "superadminpass")
	t.Setenv("HUMAN_BACKPLANE_PASSWORD", "superhumanpass")
	t.Setenv("HUMAN_NAME", "operator")
	t.Setenv("BP_PORT", "6379")
	t.Setenv("FORGEJO_URL", "http://127.0.0.1:3000")
	t.Setenv("SONAR_HOST_URL", "http://127.0.0.1:9000")
	t.Setenv("SONAR_ADMIN_USER", "sonaradmin")
	t.Setenv("SONAR_ADMIN_PASSWORD", "SuperSonar123")

	info, err := GetInfraInfo(ctx, paths, true)
	if err != nil {
		t.Fatalf("GetInfraInfo failed: %v", err)
	}

	// Verify secrets revealed
	if info.Valkey.OperatorPassword != "superhumanpass" {
		t.Errorf("expected revealed valkey password, got: %s", info.Valkey.OperatorPassword)
	}
	if !strings.Contains(info.Valkey.ConnectionURL, "superhumanpass") {
		t.Errorf("expected connection url to include password, got: %s", info.Valkey.ConnectionURL)
	}
	if !strings.Contains(info.Valkey.CLICommand, "superhumanpass") {
		t.Errorf("expected cli command to include password, got: %s", info.Valkey.CLICommand)
	}

	if info.Forgejo.AdminPassword != "superadminpass" {
		t.Errorf("expected revealed forgejo admin password, got: %s", info.Forgejo.AdminPassword)
	}

	if info.PostgreSQL.Password != "sonar" {
		t.Errorf("expected revealed postgres password 'sonar', got: %s", info.PostgreSQL.Password)
	}
	if !strings.Contains(info.PostgreSQL.ConnectionURL, ":sonar@") {
		t.Errorf("expected revealed postgres connection url, got: %s", info.PostgreSQL.ConnectionURL)
	}
	if !strings.Contains(info.PostgreSQL.CLICommand, "PGPASSWORD=sonar") {
		t.Errorf("expected revealed psql cli command, got: %s", info.PostgreSQL.CLICommand)
	}

	if info.SonarQube.AdminPassword != "SuperSonar123" {
		t.Errorf("expected revealed sonarqube admin password, got: %s", info.SonarQube.AdminPassword)
	}
}

func TestGetInfraInfo_DefaultsAndFallbacks(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{}

	restoreSonar := SetInfraSonarContainerForTest("non-existent-sonar-test")
	defer restoreSonar()

	t.Setenv("ADMIN_BACKPLANE_PASSWORD", "")
	t.Setenv("HUMAN_BACKPLANE_PASSWORD", "")
	t.Setenv("HUMAN_NAME", "")
	t.Setenv("BP_PORT", "")
	t.Setenv("FORGEJO_URL", "")
	t.Setenv("GITEA_URL", "http://127.0.0.1:3000")
	t.Setenv("FORGEJO_ADMIN_USER", "")
	t.Setenv("GITEA_ADMIN_USER", "")
	t.Setenv("SONAR_HOST_URL", "")
	t.Setenv("SONARQUBE_URL", "")
	t.Setenv("SONAR_ADMIN_USER", "")
	t.Setenv("SONAR_ADMIN_PASSWORD", "")
	t.Setenv("SONAR_DISABLED", "1")

	info, err := GetInfraInfo(ctx, paths, true)
	if err != nil {
		t.Fatalf("GetInfraInfo failed: %v", err)
	}

	if info.Valkey.OperatorUser != "human" {
		t.Errorf("expected default human user, got: %s", info.Valkey.OperatorUser)
	}
	if info.Valkey.OperatorPassword != "admin_backplane_pass" {
		t.Errorf("expected default admin backplane pass, got: %s", info.Valkey.OperatorPassword)
	}
	if info.Valkey.HostPort != "127.0.0.1:6379" {
		t.Errorf("expected default port 6379, got: %s", info.Valkey.HostPort)
	}
	if info.Forgejo.WebURL != "http://127.0.0.1:3000" {
		t.Errorf("expected default forgejo URL, got: %s", info.Forgejo.WebURL)
	}
	if info.Forgejo.AdminUser != "forgejoadmin" {
		t.Errorf("expected default forgejoadmin, got: %s", info.Forgejo.AdminUser)
	}
	if info.SonarQube.Status != "disabled" {
		t.Errorf("expected sonarqube status 'disabled' when SONAR_DISABLED=1, got: %s", info.SonarQube.Status)
	}
}

func TestFormatInfraInfo_Alignment(t *testing.T) {
	info := &InfraStackInfo{
		Valkey: &ValkeyInfraInfo{
			Status:           "running",
			Container:        "fleet-valkey",
			InternalEndpoint: "valkey:6379",
			HostPort:         "127.0.0.1:6379",
			ConnectionURL:    "valkey://human:********@127.0.0.1:6379",
			CLICommand:       "valkey-cli -h 127.0.0.1 -p 6379",
			OperatorUser:     "human",
			OperatorPassword: "********",
		},
		Forgejo: &ForgejoInfraInfo{
			Status:        "running",
			Container:     "fleet-forgejo",
			WebURL:        "http://127.0.0.1:3000",
			InternalURL:   "http://forgejo:3000",
			SSHPort:       "127.0.0.1:2223",
			GitHTTPClone:  "http://127.0.0.1:3000/fleet/tasks.git",
			GitSSHClone:   "ssh://git@127.0.0.1:2223/fleet/tasks.git",
			AdminUser:     "forgejoadmin",
			AdminPassword: "********",
		},
		PostgreSQL: &PostgresInfraInfo{
			Status:           "running",
			Container:        "fleet-postgres",
			InternalEndpoint: "postgres:5432",
			HostPort:         "127.0.0.1:5432",
			Database:         "sonar",
			Username:         "sonar",
			Password:         "********",
			ConnectionURL:    "postgresql://sonar:********@127.0.0.1:5432/sonar",
			CLICommand:       "psql -h 127.0.0.1 -p 5432 -U sonar -d sonar",
		},
		SonarQube: &SonarQubeInfraInfo{
			Status:        "running",
			Container:     "fleet-sonarqube",
			WebURL:        "http://127.0.0.1:9000",
			InternalURL:   "http://sonarqube:9000",
			AdminUser:     "admin",
			AdminPassword: "********",
		},
	}

	formatted := FormatInfraInfo(info)

	if !strings.Contains(formatted, "[valkey]") || !strings.Contains(formatted, "[forgejo]") ||
		!strings.Contains(formatted, "[postgresql]") || !strings.Contains(formatted, "[sonarqube]") {
		t.Fatalf("missing expected section header in formatted output:\n%s", formatted)
	}

	// Verify fixed justified value column across all key-value lines
	lines := strings.Split(formatted, "\n")
	valueColIndex := -1

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "[") {
			continue
		}
		// Line must start with 2 spaces
		if !strings.HasPrefix(line, "  ") {
			t.Errorf("expected 2-space indentation on kv line: %q", line)
		}
		colonIdx := strings.Index(line, ":")
		if colonIdx == -1 {
			t.Errorf("expected colon on kv line: %q", line)
			continue
		}

		// Find start of value after colon and spaces
		valIdx := colonIdx + 1
		for valIdx < len(line) && line[valIdx] == ' ' {
			valIdx++
		}

		if valueColIndex == -1 {
			valueColIndex = valIdx
		} else if valIdx != valueColIndex {
			t.Errorf("inconsistent value column alignment on line %q: got column %d, expected %d", line, valIdx, valueColIndex)
		}
	}

	if valueColIndex == -1 {
		t.Errorf("no kv lines processed for alignment validation")
	}
}

func TestFormatInfraInfo_NilAndPartial(t *testing.T) {
	if got := FormatInfraInfo(nil); got != "" {
		t.Errorf("expected empty string for nil info, got: %q", got)
	}

	partial := &InfraStackInfo{
		Valkey: &ValkeyInfraInfo{
			Status:    "running",
			Container: "fleet-valkey",
		},
	}
	out := FormatInfraInfo(partial)
	if !strings.Contains(out, "[valkey]") {
		t.Errorf("expected [valkey] in output: %s", out)
	}
	if strings.Contains(out, "[forgejo]") {
		t.Errorf("did not expect [forgejo] in partial output: %s", out)
	}

	// Test backward compatibility fallback to Gitea when Forgejo is nil
	giteaOnly := &InfraStackInfo{
		Gitea: &ForgejoInfraInfo{
			Status:    "running",
			Container: "fleet-forgejo",
		},
	}
	outGitea := FormatInfraInfo(giteaOnly)
	if !strings.Contains(outGitea, "[forgejo]") {
		t.Errorf("expected [forgejo] header when only Gitea field is provided: %s", outGitea)
	}
}
