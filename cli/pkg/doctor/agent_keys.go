// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
	"github.com/boggycreek/fleet/cli/pkg/config"
)

func parseEd25519PrivateKey(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed decoding PEM block")
	}
	// Try standard libbp PKCS#8 format
	if priv, err := libbp.DecodePrivateKeyPEM(string(data)); err == nil {
		return priv, nil
	}
	// Try raw 64-byte private key or 32-byte seed
	if len(block.Bytes) == ed25519.PrivateKeySize {
		return ed25519.PrivateKey(block.Bytes), nil
	}
	if len(block.Bytes) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(block.Bytes), nil
	}
	return nil, fmt.Errorf("not a valid Ed25519 private key")
}

func checkAndHealSigningKey(ctx context.Context, cfg *config.AgentConfig, paths config.Paths, report *DoctorReport) {
	keyPath := filepath.Join(paths.SecretsDir, cfg.Name, "signing-key.pem")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:         "Ed25519 Signing Keys",
			Status:       StatusError,
			Message:      fmt.Sprintf("Signing key file missing at %s: keys must never be regenerated to preserve historical signature validity", keyPath),
			Unrepairable: true,
		})
		report.UnrepairableCount++
		return
	}

	if len(data) == 0 {
		report.Checks = append(report.Checks, CheckItem{
			Name:         "Ed25519 Signing Keys",
			Status:       StatusError,
			Message:      fmt.Sprintf("Signing key file at %s is empty: keys must never be regenerated to preserve historical signature validity", keyPath),
			Unrepairable: true,
		})
		report.UnrepairableCount++
		return
	}

	privKey, err := parseEd25519PrivateKey(data)
	if err != nil {
		report.Checks = append(report.Checks, CheckItem{
			Name:         "Ed25519 Signing Keys",
			Status:       StatusError,
			Message:      fmt.Sprintf("Signing key file at %s is corrupt or not a valid Ed25519 private key: keys must never be regenerated to preserve historical signature validity", keyPath),
			Unrepairable: true,
		})
		report.UnrepairableCount++
		return
	}

	keyHealedPerm := false
	if fi, err := os.Stat(keyPath); err == nil && fi.Mode().Perm() != 0600 {
		if err := os.Chmod(keyPath, 0600); err == nil {
			keyHealedPerm = true
		}
	}
	if fi, err := os.Stat(paths.SecretsDir); err == nil && fi.Mode().Perm() != 0700 {
		if err := os.Chmod(paths.SecretsDir, 0700); err == nil {
			keyHealedPerm = true
		}
	}
	keyDir := filepath.Dir(keyPath)
	if fi, err := os.Stat(keyDir); err == nil && fi.Mode().Perm() != 0700 {
		if err := os.Chmod(keyDir, 0700); err == nil {
			keyHealedPerm = true
		}
	}

	priv := privKey
	pub := priv.Public().(ed25519.PublicKey)
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	cfgSynced := false
	if cfg.SigningKeyPEM == "" || cfg.PublicKeyB64 == "" {
		cfg.SigningKeyPEM = string(data)
		cfg.PublicKeyB64 = pubB64
		_ = config.SaveAgentConfig(cfg, paths)
		cfgSynced = true
	}

	if keyHealedPerm || cfgSynced {
		msg := "Repaired signing key file permissions to 0600"
		if keyHealedPerm && cfgSynced {
			msg = "Repaired signing key file permissions to 0600 and synchronized config"
		} else if cfgSynced {
			msg = "Synchronized agent configuration with existing Ed25519 signing key"
		}
		report.Checks = append(report.Checks, CheckItem{
			Name:    "Ed25519 Signing Keys",
			Status:  StatusHealed,
			Message: msg,
			Healed:  true,
		})
		report.HealedCount++
		return
	}

	report.Checks = append(report.Checks, CheckItem{
		Name:    "Ed25519 Signing Keys",
		Status:  StatusOK,
		Message: "Cryptographic signing keypair intact",
	})
}
