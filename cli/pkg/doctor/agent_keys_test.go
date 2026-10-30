// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"context"
	"crypto/ed25519"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
	"github.com/boggycreek/fleet/cli/pkg/config"
)

func TestParseEd25519PrivateKey(t *testing.T) {
	t.Parallel()

	t.Run("ValidPKCS8PEM", func(t *testing.T) {
		t.Parallel()
		priv, _, err := libbp.GenerateKeypair()
		if err != nil {
			t.Fatalf("failed generating keypair: %v", err)
		}
		pemStr, err := libbp.EncodePrivateKeyPEM(priv)
		if err != nil {
			t.Fatalf("failed encoding PKCS#8 PEM: %v", err)
		}
		parsed, err := parseEd25519PrivateKey([]byte(pemStr))
		if err != nil {
			t.Fatalf("parseEd25519PrivateKey failed on valid PKCS#8 PEM: %v", err)
		}
		if len(parsed) != ed25519.PrivateKeySize {
			t.Errorf("expected %d bytes, got %d", ed25519.PrivateKeySize, len(parsed))
		}
	})

	t.Run("Raw64BytePEM", func(t *testing.T) {
		t.Parallel()
		raw64 := make([]byte, ed25519.PrivateKeySize)
		for i := range raw64 {
			raw64[i] = byte(i)
		}
		pem64 := pem.EncodeToMemory(&pem.Block{Type: "ED25519 PRIVATE KEY", Bytes: raw64})
		parsed, err := parseEd25519PrivateKey(pem64)
		if err != nil {
			t.Fatalf("parseEd25519PrivateKey failed on raw 64-byte key: %v", err)
		}
		if len(parsed) != ed25519.PrivateKeySize {
			t.Errorf("expected %d bytes, got %d", ed25519.PrivateKeySize, len(parsed))
		}
	})

	t.Run("Raw32ByteSeedPEM", func(t *testing.T) {
		t.Parallel()
		seed32 := make([]byte, ed25519.SeedSize)
		for i := range seed32 {
			seed32[i] = byte(i + 1)
		}
		pem32 := pem.EncodeToMemory(&pem.Block{Type: "ED25519 SEED", Bytes: seed32})
		parsed, err := parseEd25519PrivateKey(pem32)
		if err != nil {
			t.Fatalf("parseEd25519PrivateKey failed on 32-byte seed: %v", err)
		}
		if len(parsed) != ed25519.PrivateKeySize {
			t.Errorf("expected %d bytes, got %d", ed25519.PrivateKeySize, len(parsed))
		}
	})

	t.Run("InvalidNonPEMData", func(t *testing.T) {
		t.Parallel()
		if _, err := parseEd25519PrivateKey([]byte("not-valid-pem-block")); err == nil {
			t.Errorf("expected error decoding non-PEM data")
		}
	})

	t.Run("InvalidPEMBlockLength", func(t *testing.T) {
		t.Parallel()
		shortBytes := pem.EncodeToMemory(&pem.Block{Type: "UNKNOWN", Bytes: []byte("short")})
		if _, err := parseEd25519PrivateKey(shortBytes); err == nil {
			t.Errorf("expected error decoding short PEM block")
		}

		oddBytes := pem.EncodeToMemory(&pem.Block{Type: "ED25519 PRIVATE KEY", Bytes: make([]byte, 48)})
		if _, err := parseEd25519PrivateKey(oddBytes); err == nil {
			t.Errorf("expected error decoding 48-byte PEM block")
		}
	})
}

func TestCheckAndHealSigningKey(t *testing.T) {
	t.Parallel()

	t.Run("MissingKey_Unrepairable", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		paths := config.Paths{
			DataHome:   filepath.Join(tmpDir, "data"),
			SecretsDir: filepath.Join(tmpDir, "data", "secrets"),
		}
		_ = paths.EnsureDirectories()

		cfg := &config.AgentConfig{Name: "missing-key-agent"}
		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealSigningKey(context.Background(), cfg, paths, report)

		if report.UnrepairableCount != 1 {
			t.Fatalf("expected UnrepairableCount=1, got %d", report.UnrepairableCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusError || !report.Checks[0].Unrepairable {
			t.Fatalf("expected 1 unrepairable error check, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "missing") || !strings.Contains(report.Checks[0].Message, "never be regenerated") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("EmptyKey_Unrepairable", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		paths := config.Paths{
			DataHome:   filepath.Join(tmpDir, "data"),
			SecretsDir: filepath.Join(tmpDir, "data", "secrets"),
		}
		_ = paths.EnsureDirectories()

		cfg := &config.AgentConfig{Name: "empty-key-agent"}
		agentSecretDir := filepath.Join(paths.SecretsDir, cfg.Name)
		_ = os.MkdirAll(agentSecretDir, 0700)
		keyPath := filepath.Join(agentSecretDir, "signing-key.pem")
		_ = os.WriteFile(keyPath, []byte(""), 0600)

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealSigningKey(context.Background(), cfg, paths, report)

		if report.UnrepairableCount != 1 {
			t.Fatalf("expected UnrepairableCount=1, got %d", report.UnrepairableCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusError || !report.Checks[0].Unrepairable {
			t.Fatalf("expected unrepairable error check, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "empty") || !strings.Contains(report.Checks[0].Message, "never be regenerated") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("CorruptKey_PreservedAndUnrepairable", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		paths := config.Paths{
			DataHome:   filepath.Join(tmpDir, "data"),
			SecretsDir: filepath.Join(tmpDir, "data", "secrets"),
		}
		_ = paths.EnsureDirectories()

		cfg := &config.AgentConfig{Name: "corrupt-key-agent"}
		agentSecretDir := filepath.Join(paths.SecretsDir, cfg.Name)
		_ = os.MkdirAll(agentSecretDir, 0700)
		keyPath := filepath.Join(agentSecretDir, "signing-key.pem")
		corruptContent := "corrupt-unparseable-data"
		_ = os.WriteFile(keyPath, []byte(corruptContent), 0600)

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealSigningKey(context.Background(), cfg, paths, report)

		if report.UnrepairableCount != 1 {
			t.Fatalf("expected UnrepairableCount=1, got %d", report.UnrepairableCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusError || !report.Checks[0].Unrepairable {
			t.Fatalf("expected unrepairable error check, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "corrupt") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}

		// Ensure file was NOT overwritten
		diskData, err := os.ReadFile(keyPath)
		if err != nil || string(diskData) != corruptContent {
			t.Errorf("corrupt key file was unexpectedly modified or lost: %v, content=%q", err, string(diskData))
		}
	})

	t.Run("ValidKey_IntactStatusOK", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		paths := config.Paths{
			DataHome:   filepath.Join(tmpDir, "data"),
			SecretsDir: filepath.Join(tmpDir, "data", "secrets"),
		}
		_ = paths.EnsureDirectories()
		_ = os.Chmod(paths.SecretsDir, 0700)

		priv, pub, err := libbp.GenerateKeypair()
		if err != nil {
			t.Fatalf("failed generating keypair: %v", err)
		}
		pemStr, err := libbp.EncodePrivateKeyPEM(priv)
		if err != nil {
			t.Fatalf("failed encoding PEM: %v", err)
		}
		pubB64 := libbp.EncodePublicKeyBase64(pub)

		cfg := &config.AgentConfig{
			Name:          "intact-key-agent",
			SigningKeyPEM: pemStr,
			PublicKeyB64:  pubB64,
		}
		agentSecretDir := filepath.Join(paths.SecretsDir, cfg.Name)
		_ = os.MkdirAll(agentSecretDir, 0700)
		_ = os.Chmod(agentSecretDir, 0700)
		keyPath := filepath.Join(agentSecretDir, "signing-key.pem")
		_ = os.WriteFile(keyPath, []byte(pemStr), 0600)

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealSigningKey(context.Background(), cfg, paths, report)

		if report.HealedCount != 0 || report.UnrepairableCount != 0 {
			t.Errorf("expected clean run, got healed=%d unrepairable=%d", report.HealedCount, report.UnrepairableCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK check, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "intact") {
			t.Errorf("expected intact message, got: %s", report.Checks[0].Message)
		}
	})

	t.Run("PermissionHeal_KeyFile0644_to_0600", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		paths := config.Paths{
			DataHome:   filepath.Join(tmpDir, "data"),
			SecretsDir: filepath.Join(tmpDir, "data", "secrets"),
		}
		_ = paths.EnsureDirectories()
		_ = os.Chmod(paths.SecretsDir, 0700)

		priv, pub, err := libbp.GenerateKeypair()
		if err != nil {
			t.Fatalf("failed generating keypair: %v", err)
		}
		pemStr, _ := libbp.EncodePrivateKeyPEM(priv)
		pubB64 := libbp.EncodePublicKeyBase64(pub)

		cfg := &config.AgentConfig{
			Name:          "perm-key-agent",
			SigningKeyPEM: pemStr,
			PublicKeyB64:  pubB64,
		}
		agentSecretDir := filepath.Join(paths.SecretsDir, cfg.Name)
		_ = os.MkdirAll(agentSecretDir, 0700)
		keyPath := filepath.Join(agentSecretDir, "signing-key.pem")
		_ = os.WriteFile(keyPath, []byte(pemStr), 0644)

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealSigningKey(context.Background(), cfg, paths, report)

		if report.HealedCount != 1 {
			t.Fatalf("expected HealedCount=1, got %d", report.HealedCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusHealed {
			t.Fatalf("expected StatusHealed, got %+v", report.Checks)
		}
		if fi, err := os.Stat(keyPath); err != nil || fi.Mode().Perm() != 0600 {
			t.Errorf("expected key permissions repaired to 0600, got: %o", fi.Mode().Perm())
		}
	})

	t.Run("PermissionHeal_SecretsDirAndKeyDir0700", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		paths := config.Paths{
			DataHome:   filepath.Join(tmpDir, "data"),
			SecretsDir: filepath.Join(tmpDir, "data", "secrets"),
		}
		_ = paths.EnsureDirectories()
		_ = os.Chmod(paths.SecretsDir, 0755)

		priv, pub, err := libbp.GenerateKeypair()
		if err != nil {
			t.Fatalf("failed generating keypair: %v", err)
		}
		pemStr, _ := libbp.EncodePrivateKeyPEM(priv)
		pubB64 := libbp.EncodePublicKeyBase64(pub)

		cfg := &config.AgentConfig{
			Name:          "dirperm-key-agent",
			SigningKeyPEM: pemStr,
			PublicKeyB64:  pubB64,
		}
		agentSecretDir := filepath.Join(paths.SecretsDir, cfg.Name)
		_ = os.MkdirAll(agentSecretDir, 0755)
		keyPath := filepath.Join(agentSecretDir, "signing-key.pem")
		_ = os.WriteFile(keyPath, []byte(pemStr), 0600)

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealSigningKey(context.Background(), cfg, paths, report)

		if report.HealedCount != 1 {
			t.Fatalf("expected HealedCount=1, got %d", report.HealedCount)
		}
		if fi, err := os.Stat(paths.SecretsDir); err != nil || fi.Mode().Perm() != 0700 {
			t.Errorf("expected SecretsDir 0700, got: %o", fi.Mode().Perm())
		}
		if fi, err := os.Stat(agentSecretDir); err != nil || fi.Mode().Perm() != 0700 {
			t.Errorf("expected agentSecretDir 0700, got: %o", fi.Mode().Perm())
		}
	})

	t.Run("ConfigSynchronization_FromValidKeyFile", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		paths := config.Paths{
			DataHome:   filepath.Join(tmpDir, "data"),
			AgentsDir:  filepath.Join(tmpDir, "data", "agents"),
			SecretsDir: filepath.Join(tmpDir, "data", "secrets"),
		}
		_ = paths.EnsureDirectories()
		_ = os.Chmod(paths.SecretsDir, 0700)

		priv, pub, err := libbp.GenerateKeypair()
		if err != nil {
			t.Fatalf("failed generating keypair: %v", err)
		}
		pemStr, _ := libbp.EncodePrivateKeyPEM(priv)
		pubB64 := libbp.EncodePublicKeyBase64(pub)

		cfg := &config.AgentConfig{
			Name: "sync-key-agent",
		}
		agentSecretDir := filepath.Join(paths.SecretsDir, cfg.Name)
		_ = os.MkdirAll(agentSecretDir, 0700)
		keyPath := filepath.Join(agentSecretDir, "signing-key.pem")
		_ = os.WriteFile(keyPath, []byte(pemStr), 0600)

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealSigningKey(context.Background(), cfg, paths, report)

		if report.HealedCount != 1 {
			t.Fatalf("expected HealedCount=1, got %d", report.HealedCount)
		}
		if cfg.SigningKeyPEM != pemStr || cfg.PublicKeyB64 != pubB64 {
			t.Fatalf("expected cfg to synchronize key fields from file")
		}
		if !strings.Contains(report.Checks[0].Message, "Synchronized agent configuration") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("Combined_PermissionHealAndConfigSync", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		paths := config.Paths{
			DataHome:   filepath.Join(tmpDir, "data"),
			AgentsDir:  filepath.Join(tmpDir, "data", "agents"),
			SecretsDir: filepath.Join(tmpDir, "data", "secrets"),
		}
		_ = paths.EnsureDirectories()
		_ = os.Chmod(paths.SecretsDir, 0700)

		priv, _, err := libbp.GenerateKeypair()
		if err != nil {
			t.Fatalf("failed generating keypair: %v", err)
		}
		pemStr, _ := libbp.EncodePrivateKeyPEM(priv)

		cfg := &config.AgentConfig{
			Name: "both-key-agent",
		}
		agentSecretDir := filepath.Join(paths.SecretsDir, cfg.Name)
		_ = os.MkdirAll(agentSecretDir, 0700)
		keyPath := filepath.Join(agentSecretDir, "signing-key.pem")
		_ = os.WriteFile(keyPath, []byte(pemStr), 0644)

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealSigningKey(context.Background(), cfg, paths, report)

		if report.HealedCount != 1 {
			t.Fatalf("expected HealedCount=1, got %d", report.HealedCount)
		}
		if fi, err := os.Stat(keyPath); err != nil || fi.Mode().Perm() != 0600 {
			t.Errorf("expected key perms healed to 0600, got: %o", fi.Mode().Perm())
		}
		if cfg.SigningKeyPEM == "" || cfg.PublicKeyB64 == "" {
			t.Errorf("expected config to be synchronized")
		}
		if !strings.Contains(report.Checks[0].Message, "Repaired signing key file permissions to 0600 and synchronized config") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})
}
