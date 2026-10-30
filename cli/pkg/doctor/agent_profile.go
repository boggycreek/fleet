// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/boggycreek/fleet/cli/pkg/config"
)

func checkAndHealBPProfile(ctx context.Context, cfg *config.AgentConfig, paths config.Paths, report *DoctorReport) {
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
		if fi, err := os.Stat(paths.BPProfilesDir); err == nil && fi.Mode().Perm() != 0700 {
			_ = os.Chmod(paths.BPProfilesDir, 0700)
		}
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

	healedPerm := false
	if fi, err := os.Stat(paths.BPProfilesDir); err == nil && fi.Mode().Perm() != 0700 {
		if err := os.Chmod(paths.BPProfilesDir, 0700); err == nil {
			healedPerm = true
		}
	}
	// Verify permissions
	if fi, err := os.Stat(profilePath); err == nil && fi.Mode().Perm() != 0600 {
		if err := os.Chmod(profilePath, 0600); err == nil {
			healedPerm = true
		}
	}

	if healedPerm {
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
