// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/fleet/cli/pkg/config"
)

func TestCheckAndHealHostSSH(t *testing.T) {
	t.Parallel()

	t.Run("EmptyPaths_FallbackToIDEKeyFile", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		sshDir := filepath.Join(tmpDir, "ssh")
		_ = os.MkdirAll(sshDir, 0700)
		keyPath := filepath.Join(sshDir, "custom-ide-key")

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data"),
			SSHDir:        sshDir,
			IDEKeyFile:    keyPath,
			SSHPrivateKey: "", // empty SSHPrivateKey
			SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
		}
		_ = paths.EnsureDirectories()

		report := &DoctorReport{AgentName: "test-agent"}
		checkAndHealHostSSH(context.Background(), paths, report)

		if report.HealedCount == 0 {
			t.Errorf("expected healed count > 0 with fallback to IDEKeyFile")
		}
		if _, err := os.Stat(keyPath); os.IsNotExist(err) {
			t.Errorf("expected fallback IDEKeyFile %s to be created", keyPath)
		}
	})

	t.Run("MissingKey_GeneratesNewKeypair", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		sshDir := filepath.Join(tmpDir, "ssh")
		_ = os.MkdirAll(sshDir, 0700)
		keyPath := filepath.Join(sshDir, "fleet")

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data"),
			SSHDir:        sshDir,
			IDEKeyFile:    keyPath,
			SSHPrivateKey: keyPath,
			SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
		}
		_ = paths.EnsureDirectories()

		report := &DoctorReport{AgentName: "test-agent"}
		checkAndHealHostSSH(context.Background(), paths, report)

		if report.HealedCount == 0 {
			t.Errorf("expected healed count > 0 for new key generation")
		}
		if fi, err := os.Stat(keyPath); err != nil || fi.Mode().Perm() != 0600 {
			t.Errorf("expected key to exist with 0600 permissions: %v", err)
		}
		if _, err := os.Stat(keyPath + ".pub"); err != nil {
			t.Errorf("expected pubkey to exist: %v", err)
		}
	})

	t.Run("LegacyKeyMigration", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		sshDir := filepath.Join(tmpDir, "ssh")
		_ = os.MkdirAll(sshDir, 0700)
		legacyKey := filepath.Join(sshDir, "agent-sandbox")
		_ = os.WriteFile(legacyKey, []byte("legacy-private-key-data"), 0600)
		_ = os.WriteFile(legacyKey+".pub", []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 legacy-pub-key"), 0644)

		fleetKey := filepath.Join(sshDir, "fleet")
		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data"),
			SSHDir:        sshDir,
			IDEKeyFile:    fleetKey,
			SSHPrivateKey: fleetKey,
			SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
		}
		_ = paths.EnsureDirectories()

		report := &DoctorReport{AgentName: "test-agent"}
		checkAndHealHostSSH(context.Background(), paths, report)

		if report.HealedCount == 0 {
			t.Errorf("expected healed count > 0 for legacy migration")
		}
		data, err := os.ReadFile(fleetKey)
		if err != nil || string(data) != "legacy-private-key-data" {
			t.Errorf("expected migrated private key to match legacy content: %v, %s", err, string(data))
		}
		pubData, err := os.ReadFile(fleetKey + ".pub")
		if err != nil || string(pubData) != "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 legacy-pub-key" {
			t.Errorf("expected migrated pubkey to match legacy content: %v, %s", err, string(pubData))
		}
	})

	t.Run("MissingPubkeyDerivation", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		sshDir := filepath.Join(tmpDir, "ssh")
		_ = os.MkdirAll(sshDir, 0700)
		keyPath := filepath.Join(sshDir, "fleet")

		cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", "fleet-test", "-f", keyPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("failed generating test ssh key: %v (%s)", err, string(out))
		}
		_ = os.Remove(keyPath + ".pub")

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data"),
			SSHDir:        sshDir,
			IDEKeyFile:    keyPath,
			SSHPrivateKey: keyPath,
			SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
		}
		_ = paths.EnsureDirectories()

		report := &DoctorReport{AgentName: "test-agent"}
		checkAndHealHostSSH(context.Background(), paths, report)

		if report.HealedCount == 0 {
			t.Errorf("expected healed count > 0 for pubkey derivation")
		}
		if fi, err := os.Stat(keyPath + ".pub"); err != nil || fi.Mode().Perm() != 0644 {
			t.Errorf("expected regenerated pubkey with 0644 permissions: %v", err)
		}
	})

	t.Run("DerivationFailure_Unrepairable", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		sshDir := filepath.Join(tmpDir, "ssh")
		_ = os.MkdirAll(sshDir, 0700)
		keyPath := filepath.Join(sshDir, "fleet")
		_ = os.WriteFile(keyPath, []byte("invalid-key-data-cannot-derive"), 0600)
		// Ensure no .pub file exists

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data"),
			SSHDir:        sshDir,
			IDEKeyFile:    keyPath,
			SSHPrivateKey: keyPath,
			SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
		}
		_ = paths.EnsureDirectories()

		report := &DoctorReport{AgentName: "test-agent"}
		checkAndHealHostSSH(context.Background(), paths, report)

		if report.UnrepairableCount != 1 {
			t.Fatalf("expected UnrepairableCount=1, got %d", report.UnrepairableCount)
		}
		if len(report.Checks) != 1 || report.Checks[0].Status != StatusError || !report.Checks[0].Unrepairable {
			t.Fatalf("expected StatusError with Unrepairable=true, got %+v", report.Checks)
		}
		if !strings.Contains(report.Checks[0].Message, "Failed deriving public key") {
			t.Errorf("unexpected check message: %s", report.Checks[0].Message)
		}
	})

	t.Run("PassphraseProtectedKey_TimeoutHandling", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		sshDir := filepath.Join(tmpDir, "ssh")
		_ = os.MkdirAll(sshDir, 0700)
		keyPath := filepath.Join(sshDir, "fleet")

		cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "secret-passphrase", "-f", keyPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("failed generating passphrase key: %v (%s)", err, string(out))
		}
		_ = os.Remove(keyPath + ".pub")

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data"),
			SSHDir:        sshDir,
			IDEKeyFile:    keyPath,
			SSHPrivateKey: keyPath,
			SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
		}
		_ = paths.EnsureDirectories()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		done := make(chan struct{})
		report := &DoctorReport{AgentName: "test-agent"}
		go func() {
			checkAndHealHostSSH(ctx, paths, report)
			close(done)
		}()

		select {
		case <-done:
			// Passed if it terminates promptly without blocking indefinitely
			if report.UnrepairableCount != 1 {
				t.Logf("completed with report checks: %+v", report.Checks)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("checkAndHealHostSSH hung indefinitely on passphrase-protected key")
		}
	})

	t.Run("PermissionsHeal_0644_to_0600", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		sshDir := filepath.Join(tmpDir, "ssh")
		_ = os.MkdirAll(sshDir, 0700)
		keyPath := filepath.Join(sshDir, "fleet")

		cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("failed generating test ssh key: %v (%s)", err, string(out))
		}
		_ = os.Chmod(keyPath, 0644)

		userSSHConfig := filepath.Join(sshDir, "config")
		_ = os.WriteFile(userSSHConfig, []byte(""), 0600)
		sshConfigFile := filepath.Join(tmpDir, "data", "ssh_config")
		_ = os.MkdirAll(filepath.Dir(sshConfigFile), 0700)
		_ = os.WriteFile(sshConfigFile, []byte("# Fleet Auto-Generated SSH Configuration\n"), 0600)

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data"),
			SSHDir:        sshDir,
			IDEKeyFile:    keyPath,
			SSHPrivateKey: keyPath,
			SSHConfigFile: sshConfigFile,
		}

		report := &DoctorReport{AgentName: "test-agent"}
		checkAndHealHostSSH(context.Background(), paths, report)

		if report.HealedCount == 0 {
			t.Errorf("expected healed count > 0 for permission repair")
		}
		if fi, err := os.Stat(keyPath); err != nil || fi.Mode().Perm() != 0600 {
			t.Errorf("expected private key permission healed to 0600, got: %o", fi.Mode().Perm())
		}
	})

	t.Run("IncludeConfigCreation", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		sshDir := filepath.Join(tmpDir, "ssh")
		_ = os.MkdirAll(sshDir, 0700)
		keyPath := filepath.Join(sshDir, "fleet")
		_ = exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyPath).Run()

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data"),
			SSHDir:        sshDir,
			IDEKeyFile:    keyPath,
			SSHPrivateKey: keyPath,
			SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
		}
		_ = paths.EnsureDirectories()

		// managed ssh config does not exist
		_ = os.Remove(paths.SSHConfigFile)

		report := &DoctorReport{AgentName: "test-agent"}
		checkAndHealHostSSH(context.Background(), paths, report)

		if report.HealedCount == 0 {
			t.Errorf("expected healed count > 0 for Include config creation")
		}
		if _, err := os.Stat(paths.SSHConfigFile); os.IsNotExist(err) {
			t.Errorf("expected managed SSHConfigFile to be created")
		}
		userConfigData, err := os.ReadFile(filepath.Join(sshDir, "config"))
		if err != nil || !strings.Contains(string(userConfigData), paths.SSHConfigFile) {
			t.Errorf("expected ~/.ssh/config to contain Include directive for %s", paths.SSHConfigFile)
		}
	})

	t.Run("Healthy_StatusOK", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		sshDir := filepath.Join(tmpDir, "ssh")
		_ = os.MkdirAll(sshDir, 0700)
		keyPath := filepath.Join(sshDir, "fleet")
		_ = exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyPath).Run()
		_ = os.Chmod(keyPath, 0600)

		paths := config.Paths{
			DataHome:      filepath.Join(tmpDir, "data"),
			SSHDir:        sshDir,
			IDEKeyFile:    keyPath,
			SSHPrivateKey: keyPath,
			SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
		}
		_ = paths.EnsureDirectories()

		// Prime the include config
		report1 := &DoctorReport{AgentName: "test-agent"}
		checkAndHealHostSSH(context.Background(), paths, report1)

		// Second pass should be clean
		report2 := &DoctorReport{AgentName: "test-agent"}
		checkAndHealHostSSH(context.Background(), paths, report2)

		if report2.HealedCount != 0 || report2.UnrepairableCount != 0 {
			t.Errorf("expected clean run, got healed=%d, unrep=%d", report2.HealedCount, report2.UnrepairableCount)
		}
		if len(report2.Checks) != 1 || report2.Checks[0].Status != StatusOK {
			t.Fatalf("expected StatusOK check, got %+v", report2.Checks)
		}
		if !strings.Contains(report2.Checks[0].Message, "Dedicated IDE keypair and OpenSSH Include active") {
			t.Errorf("unexpected check message: %s", report2.Checks[0].Message)
		}
	})
}
