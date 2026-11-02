// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/runtime"
)

// DiagnoseAndHealInfra performs a comprehensive health check and auto-healing on shared infrastructure
func DiagnoseAndHealInfra(ctx context.Context, paths config.Paths) (*DoctorReport, error) {
	report := &DoctorReport{
		AgentName: "shared-infrastructure",
	}

	// 1. Host Environment Configuration (.env) Check & Heal
	checkAndHealInfraEnv(paths, report)

	// 2. Host SSH Keypair & Include Directive
	checkAndHealHostSSH(ctx, paths, report)

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

	// 8. OpenRouter Inference Proxy Gateway Container & Service
	checkAndHealOpenRouterContainer(ctx, paths, report)

	// 9. Local LLM Host Inference Gateway / Slots
	checkLocalInferenceSlots(ctx, report)

	// 10. Local Model Gateways
	checkLocalModelGateways(ctx, report)

	return report, nil
}

func checkLocalModelGateways(ctx context.Context, report *DoctorReport) {
	results := ProbeWellKnownGateways(ctx)

	var healthy []*GatewayProbeResult
	for _, r := range results {
		if r != nil && r.Reachable {
			healthy = append(healthy, r)
		}
	}

	if len(healthy) == 0 {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Local Model Gateways",
			Status:  StatusOK,
			Message: "INFO: No local LLM inference engines detected on default ports",
		})
		return
	}

	for _, h := range healthy {
		modelCount := len(h.Models)
		var modelDesc string
		if modelCount == 1 {
			modelDesc = "1 active model"
		} else {
			modelDesc = fmt.Sprintf("%d active models", modelCount)
		}
		if modelCount > 0 {
			modelDesc = fmt.Sprintf("%s: %s", modelDesc, strings.Join(h.Models, ", "))
		}
		msg := fmt.Sprintf("Local %s engine healthy at %s (%s)", h.Engine, h.URL, modelDesc)
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Local Model Gateways",
			Status:  StatusOK,
			Message: msg,
		})
	}
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
