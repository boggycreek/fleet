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

	"github.com/boggycreek/sandbox/pkg/config"
	"github.com/boggycreek/sandbox/pkg/plugin"
)

func checkAndHealHostSSH(ctx context.Context, paths config.Paths, report *DoctorReport) {
	healedKey := false
	keyPath := paths.GetSSHPrivateKey()
	if keyPath == "" {
		keyPath = paths.IDEKeyFile
	}
	sshPub := keyPath + ".pub"
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		_ = os.MkdirAll(filepath.Dir(keyPath), 0700)
		legacyKey := filepath.Join(paths.SSHDir, "agent-sandbox")
		if privBytes, err := os.ReadFile(legacyKey); err == nil {
			_ = os.WriteFile(keyPath, privBytes, 0600)
			if pubBytes, err := os.ReadFile(legacyKey + ".pub"); err == nil {
				_ = os.WriteFile(sshPub, pubBytes, 0644)
			}
			healedKey = true
		} else {
			_ = exec.CommandContext(ctx, "ssh-keygen", "-t", "ed25519", "-N", "", "-C", "fleet-ide", "-f", keyPath).Run()
			healedKey = true
		}
	}

	if fi, err := os.Stat(keyPath); err == nil && fi.Mode().Perm() != 0600 {
		if err := os.Chmod(keyPath, 0600); err == nil {
			healedKey = true
		}
	}

	if _, err := os.Stat(sshPub); os.IsNotExist(err) {
		out, err := exec.CommandContext(ctx, "ssh-keygen", "-y", "-P", "", "-f", keyPath).Output()
		if err != nil || len(out) == 0 {
			report.Checks = append(report.Checks, CheckItem{
				Name:         "Host SSH & IDE Config",
				Status:       StatusError,
				Message:      fmt.Sprintf("Failed deriving public key from %s: %v", keyPath, err),
				Unrepairable: true,
			})
			report.UnrepairableCount++
			return
		}
		if err := os.WriteFile(sshPub, out, 0644); err != nil {
			report.Checks = append(report.Checks, CheckItem{
				Name:         "Host SSH & IDE Config",
				Status:       StatusError,
				Message:      fmt.Sprintf("Failed writing public key to %s: %v", sshPub, err),
				Unrepairable: true,
			})
			report.UnrepairableCount++
			return
		}
		healedKey = true
	}

	// Check managed ssh_config and ~/.ssh/config Include
	healedInclude := false
	if _, err := os.Stat(paths.SSHConfigFile); os.IsNotExist(err) {
		_ = os.MkdirAll(filepath.Dir(paths.SSHConfigFile), 0700)
		_ = os.WriteFile(paths.SSHConfigFile, []byte("# Fleet Auto-Generated SSH Configuration\n"), 0600)
		healedInclude = true
	}

	userSSHConfig := filepath.Join(paths.SSHDir, "config")
	if linked, err := plugin.EnsureSSHConfigInclude(paths.SSHConfigFile, userSSHConfig); err == nil && linked {
		healedInclude = true
	}

	if healedKey || healedInclude {
		msg := "Ensured host IDE keypair and OpenSSH Include directive"
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Host SSH & IDE Config",
			Status:  StatusHealed,
			Message: msg,
			Healed:  true,
		})
		report.HealedCount++
	} else {
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Host SSH & IDE Config",
			Status:  StatusOK,
			Message: "Dedicated IDE keypair and OpenSSH Include active",
		})
	}
}
