// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/config"
)

func TestCheckAndHealBPProfile(t *testing.T) {
	t.Parallel()

	t.Run("EmptyBPProfilesDir_ReturnsImmediately", func(t *testing.T) {
		t.Parallel()
		paths := config.Paths{BPProfilesDir: ""}
		cfg := &config.AgentConfig{Name: "empty-dir-agent"}
		report := &DoctorReport{AgentName: cfg.Name}

		checkAndHealBPProfile(context.Background(), cfg, paths, report)
		if len(report.Checks) != 0 {
			t.Errorf("expected 0 checks when BPProfilesDir is empty, got %d", len(report.Checks))
		}
	})

	t.Run("MissingProfile_RebuiltAndHealed", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data"),
			SecretsDir:    filepath.Join(tmpDir, "data", "secrets"),
			BPProfilesDir: filepath.Join(tmpDir, "data", "profiles"),
		}
		cfg := &config.AgentConfig{
			Name:     "missing-prof-agent",
			Password: "agent-password-123",
		}
		report := &DoctorReport{AgentName: cfg.Name}

		checkAndHealBPProfile(context.Background(), cfg, paths, report)

		if report.HealedCount != 1 {
			t.Fatalf("expected HealedCount=1, got %d", report.HealedCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusHealed {
			t.Fatalf("expected StatusHealed, got %+v", report.Checks)
		}

		profilePath := filepath.Join(paths.BPProfilesDir, fmt.Sprintf("%s.env", cfg.Name))
		content, err := os.ReadFile(profilePath)
		if err != nil {
			t.Fatalf("failed reading rebuilt profile: %v", err)
		}
		keyPath := filepath.Join(paths.SecretsDir, cfg.Name, "signing-key.pem")
		expected := fmt.Sprintf("BP_AGENT=%s\nBP_PASSWORD=%s\nBP_SIGNING_KEY=%s\n", cfg.Name, cfg.Password, keyPath)
		if string(content) != expected {
			t.Errorf("unexpected profile content:\nGot: %q\nWant: %q", string(content), expected)
		}

		if fi, err := os.Stat(profilePath); err != nil || fi.Mode().Perm() != 0600 {
			t.Errorf("expected profile permissions 0600, got: %o", fi.Mode().Perm())
		}
		if fi, err := os.Stat(paths.BPProfilesDir); err != nil || fi.Mode().Perm() != 0700 {
			t.Errorf("expected BPProfilesDir permissions 0700, got: %o", fi.Mode().Perm())
		}
	})

	t.Run("ContentMismatch_OverwritesAndHeals", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data"),
			SecretsDir:    filepath.Join(tmpDir, "data", "secrets"),
			BPProfilesDir: filepath.Join(tmpDir, "data", "profiles"),
		}
		_ = os.MkdirAll(paths.BPProfilesDir, 0700)

		cfg := &config.AgentConfig{
			Name:     "mismatch-prof-agent",
			Password: "agent-new-password",
		}
		profilePath := filepath.Join(paths.BPProfilesDir, fmt.Sprintf("%s.env", cfg.Name))
		_ = os.WriteFile(profilePath, []byte("BP_AGENT=old\nBP_PASSWORD=old\nBP_SIGNING_KEY=old\n"), 0600)

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealBPProfile(context.Background(), cfg, paths, report)

		if report.HealedCount != 1 {
			t.Fatalf("expected HealedCount=1, got %d", report.HealedCount)
		}

		content, _ := os.ReadFile(profilePath)
		keyPath := filepath.Join(paths.SecretsDir, cfg.Name, "signing-key.pem")
		expected := fmt.Sprintf("BP_AGENT=%s\nBP_PASSWORD=%s\nBP_SIGNING_KEY=%s\n", cfg.Name, cfg.Password, keyPath)
		if string(content) != expected {
			t.Errorf("expected updated profile content, got: %s", string(content))
		}
	})

	t.Run("WriteFailure_UnrepairableError", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data"),
			SecretsDir:    filepath.Join(tmpDir, "data", "secrets"),
			BPProfilesDir: filepath.Join(tmpDir, "data", "profiles"),
		}
		_ = os.MkdirAll(paths.BPProfilesDir, 0700)

		cfg := &config.AgentConfig{
			Name:     "write-fail-agent",
			Password: "pass",
		}
		profilePath := filepath.Join(paths.BPProfilesDir, fmt.Sprintf("%s.env", cfg.Name))
		// Create a directory where the file should be to induce os.WriteFile error
		_ = os.Mkdir(profilePath, 0700)

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealBPProfile(context.Background(), cfg, paths, report)

		if report.UnrepairableCount != 1 {
			t.Fatalf("expected UnrepairableCount=1, got %d", report.UnrepairableCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusError || !report.Checks[0].Unrepairable {
			t.Fatalf("expected StatusError with Unrepairable=true, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "Failed creating profile") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("PermissionRepair_ProfileFile0644_to_0600", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data"),
			SecretsDir:    filepath.Join(tmpDir, "data", "secrets"),
			BPProfilesDir: filepath.Join(tmpDir, "data", "profiles"),
		}
		_ = os.MkdirAll(paths.BPProfilesDir, 0700)

		cfg := &config.AgentConfig{
			Name:     "perm-prof-agent",
			Password: "pass",
		}
		profilePath := filepath.Join(paths.BPProfilesDir, fmt.Sprintf("%s.env", cfg.Name))
		keyPath := filepath.Join(paths.SecretsDir, cfg.Name, "signing-key.pem")
		content := fmt.Sprintf("BP_AGENT=%s\nBP_PASSWORD=%s\nBP_SIGNING_KEY=%s\n", cfg.Name, cfg.Password, keyPath)
		_ = os.WriteFile(profilePath, []byte(content), 0644)

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealBPProfile(context.Background(), cfg, paths, report)

		if report.HealedCount != 1 {
			t.Fatalf("expected HealedCount=1, got %d", report.HealedCount)
		}
		if fi, err := os.Stat(profilePath); err != nil || fi.Mode().Perm() != 0600 {
			t.Errorf("expected profile repaired to 0600, got: %o", fi.Mode().Perm())
		}
		if !strings.Contains(report.Checks[0].Message, "Repaired profile permissions to 0600") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("PermissionRepair_BPProfilesDir0755_to_0700", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data"),
			SecretsDir:    filepath.Join(tmpDir, "data", "secrets"),
			BPProfilesDir: filepath.Join(tmpDir, "data", "profiles"),
		}
		_ = os.MkdirAll(paths.BPProfilesDir, 0755)

		cfg := &config.AgentConfig{
			Name:     "dirperm-prof-agent",
			Password: "pass",
		}
		profilePath := filepath.Join(paths.BPProfilesDir, fmt.Sprintf("%s.env", cfg.Name))
		keyPath := filepath.Join(paths.SecretsDir, cfg.Name, "signing-key.pem")
		content := fmt.Sprintf("BP_AGENT=%s\nBP_PASSWORD=%s\nBP_SIGNING_KEY=%s\n", cfg.Name, cfg.Password, keyPath)
		_ = os.WriteFile(profilePath, []byte(content), 0600)

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealBPProfile(context.Background(), cfg, paths, report)

		if report.HealedCount != 1 {
			t.Fatalf("expected HealedCount=1, got %d", report.HealedCount)
		}
		if fi, err := os.Stat(paths.BPProfilesDir); err != nil || fi.Mode().Perm() != 0700 {
			t.Errorf("expected BPProfilesDir repaired to 0700, got: %o", fi.Mode().Perm())
		}
	})

	t.Run("PermissionRepair_BothDirAndFile", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data"),
			SecretsDir:    filepath.Join(tmpDir, "data", "secrets"),
			BPProfilesDir: filepath.Join(tmpDir, "data", "profiles"),
		}
		_ = os.MkdirAll(paths.BPProfilesDir, 0755)

		cfg := &config.AgentConfig{
			Name:     "bothperm-prof-agent",
			Password: "pass",
		}
		profilePath := filepath.Join(paths.BPProfilesDir, fmt.Sprintf("%s.env", cfg.Name))
		keyPath := filepath.Join(paths.SecretsDir, cfg.Name, "signing-key.pem")
		content := fmt.Sprintf("BP_AGENT=%s\nBP_PASSWORD=%s\nBP_SIGNING_KEY=%s\n", cfg.Name, cfg.Password, keyPath)
		_ = os.WriteFile(profilePath, []byte(content), 0644)

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealBPProfile(context.Background(), cfg, paths, report)

		if report.HealedCount != 1 {
			t.Fatalf("expected HealedCount=1, got %d", report.HealedCount)
		}
		if fi, err := os.Stat(paths.BPProfilesDir); err != nil || fi.Mode().Perm() != 0700 {
			t.Errorf("expected BPProfilesDir 0700, got: %o", fi.Mode().Perm())
		}
		if fi, err := os.Stat(profilePath); err != nil || fi.Mode().Perm() != 0600 {
			t.Errorf("expected profilePath 0600, got: %o", fi.Mode().Perm())
		}
	})

	t.Run("Healthy_StatusOK", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data"),
			SecretsDir:    filepath.Join(tmpDir, "data", "secrets"),
			BPProfilesDir: filepath.Join(tmpDir, "data", "profiles"),
		}
		_ = os.MkdirAll(paths.BPProfilesDir, 0700)

		cfg := &config.AgentConfig{
			Name:     "healthy-prof-agent",
			Password: "pass",
		}
		profilePath := filepath.Join(paths.BPProfilesDir, fmt.Sprintf("%s.env", cfg.Name))
		keyPath := filepath.Join(paths.SecretsDir, cfg.Name, "signing-key.pem")
		content := fmt.Sprintf("BP_AGENT=%s\nBP_PASSWORD=%s\nBP_SIGNING_KEY=%s\n", cfg.Name, cfg.Password, keyPath)
		_ = os.WriteFile(profilePath, []byte(content), 0600)

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealBPProfile(context.Background(), cfg, paths, report)

		if report.HealedCount != 0 || report.UnrepairableCount != 0 {
			t.Errorf("expected clean run, got healed=%d unrepairable=%d", report.HealedCount, report.UnrepairableCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK check, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "verified") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})
}
