// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package lifecycle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/test/harness"
	"github.com/boggycreek/fleet/cli/pkg/config"
)

func TestEnsureAgentKeysAndEnsureOperatorKeys(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:   filepath.Join(tmpDir, "data"),
		AgentsDir:  filepath.Join(tmpDir, "data", "agents"),
		SecretsDir: filepath.Join(tmpDir, "data", "secrets"),
	}
	_ = paths.EnsureDirectories()

	t.Setenv("ADMIN_BACKPLANE_PASSWORD", valkey.AdminPass)
	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", fmt.Sprintf("%d", valkey.Port))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. EnsureAgentKeys when keys are empty
	cfg := &config.AgentConfig{Name: "agent-keys"}
	if err := EnsureAgentKeys(cfg, paths); err != nil {
		t.Fatalf("EnsureAgentKeys failed: %v", err)
	}
	if cfg.SigningKeyPEM == "" || cfg.PublicKeyB64 == "" {
		t.Errorf("expected keys to be generated")
	}

	keyFile := filepath.Join(paths.SecretsDir, "agent-keys", "signing-key.pem")
	if _, err := os.Stat(keyFile); err != nil {
		t.Errorf("expected secret key file to exist at %s", keyFile)
	}

	// EnsureAgentKeys when keys already exist
	oldPEM := cfg.SigningKeyPEM
	if err := EnsureAgentKeys(cfg, paths); err != nil {
		t.Fatalf("second EnsureAgentKeys failed: %v", err)
	}
	if cfg.SigningKeyPEM != oldPEM {
		t.Errorf("expected signing key to remain unchanged")
	}

	// 2. EnsureOperatorKeys with custom name
	t.Setenv("HUMAN_NAME", "admin-alice")
	rec, err := EnsureOperatorKeys(ctx, paths, "")
	if err != nil {
		t.Fatalf("EnsureOperatorKeys failed: %v", err)
	}
	if rec.Name != "admin-alice" || rec.Kind != "human" || rec.PubKey == "" {
		t.Errorf("unexpected operator record: %+v", rec)
	}

	// Verify key file was created
	opKeyFile := filepath.Join(paths.SecretsDir, "admin-alice", "signing-key.pem")
	if _, err := os.Stat(opKeyFile); err != nil {
		t.Errorf("expected operator key file at %s", opKeyFile)
	}

	// 3. EnsureOperatorKeys when key already exists on disk
	rec2, err := EnsureOperatorKeys(ctx, paths, "admin-alice")
	if err != nil {
		t.Fatalf("second EnsureOperatorKeys failed: %v", err)
	}
	if rec2.PubKey != rec.PubKey {
		t.Errorf("expected same public key decoded from existing PEM file")
	}

	// 4. Fallback when HUMAN_NAME is empty
	t.Setenv("HUMAN_NAME", "")
	recDefault, err := EnsureOperatorKeys(ctx, paths, "")
	if err != nil {
		t.Fatalf("EnsureOperatorKeys with default name failed: %v", err)
	}
	if recDefault.Name != "operator" {
		t.Errorf("expected default operator name, got %s", recDefault.Name)
	}
}
