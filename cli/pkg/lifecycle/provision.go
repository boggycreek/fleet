// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package lifecycle manages provisioning and deprovisioning of agent infrastructure.
package lifecycle

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/forgejo"
	"github.com/boggycreek/fleet/cli/pkg/runtime"
	"github.com/boggycreek/fleet/cli/pkg/sonar"
)

// RegisterValkeyACL configures Valkey ACL permissions and registers identity for a newly provisioned agent.
//
//nolint:gocritic // paths passed by value for consistency with config package
func RegisterValkeyACL(ctx context.Context, cfg *config.AgentConfig, paths config.Paths) error {
	bpCfg := libbp.LoadClientFromEnv()
	client, err := libbp.Dial(ctx, libbp.ClientConfig{
		Host:     bpCfg.Host,
		Port:     bpCfg.Port,
		Username: "admin",
		Password: os.Getenv("ADMIN_BACKPLANE_PASSWORD"),
	})
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	// Set Valkey ACL for agent
	// Restrict stream reads to public broadcast streams, identity records, human broadcasts, and blobs
	aclArgs := []string{
		"SETUSER", cfg.Name, "on",
		">" + cfg.Password,
		fmt.Sprintf("~%s:*", cfg.Name),
		fmt.Sprintf("~identity:%s", cfg.Name),
		"~ensemble:*",
		"%R~*:out", "%R~identity:*", "%R~human:*", "%R~*:blob:*", "%R~specialty:*",
		"&*", "+@all", "-@admin", "-@dangerous",
		"(+xadd ~*:inbox)",
	}
	if _, err := client.Exec(ctx, "ACL", aclArgs...); err != nil {
		return err
	}

	// Register Identity
	return client.RegisterIdentity(ctx, libbp.IdentityRecord{
		Name:   cfg.Name,
		Role:   cfg.Role,
		Kind:   "agent",
		PubKey: cfg.PublicKeyB64,
	})
}

// RegisterForgejoUser provisions an agent account, registers IDE public SSH keys, and adds the agent to the fleet org.
//
//nolint:gocritic // paths passed by value for consistency with config package
func RegisterForgejoUser(ctx context.Context, cfg *config.AgentConfig, paths config.Paths) error {
	if cfg.IsNoContainer() {
		return nil
	}

	adminPass := os.Getenv("FORGEJO_ADMIN_PASSWORD")
	if adminPass == "" {
		adminPass = os.Getenv("GITEA_ADMIN_PASSWORD")
	}
	if adminPass == "" {
		adminPass = os.Getenv("ADMIN_BACKPLANE_PASSWORD")
	}
	if adminPass == "" {
		adminPass = "admin_backplane_pass"
	}

	forgejoURL := os.Getenv("FORGEJO_URL")
	if forgejoURL == "" {
		forgejoURL = os.Getenv("GITEA_URL")
	}
	if forgejoURL == "" {
		forgejoURL = "http://127.0.0.1:3000"
	}

	adminUser := os.Getenv("FORGEJO_ADMIN_USER")
	if adminUser == "" {
		adminUser = os.Getenv("GITEA_ADMIN_USER")
	}
	if adminUser == "" {
		adminUser = "forgejoadmin"
	}

	client := forgejo.NewClient(forgejo.ClientConfig{
		BaseURL:   forgejoURL,
		AdminUser: adminUser,
		AdminPass: adminPass,
		Timeout:   2 * time.Second,
	})

	// 1. Provision user account in Forgejo
	if err := client.EnsureUser(ctx, cfg.Name, cfg.Password, fmt.Sprintf("%s@local.fleet", cfg.Name)); err != nil {
		return err
	}

	// 2. Add public SSH key if host IDE key exists
	sshKeyPub := filepath.Clean(fmt.Sprintf("%s.pub", paths.GetSSHPrivateKey()))
	// #nosec G304 -- reading controlled IDE public key file
	if keyData, err := os.ReadFile(sshKeyPub); err == nil && len(keyData) > 0 {
		if keyErr := client.AddUserSSHKey(ctx, cfg.Name, fmt.Sprintf("%s-ide-key", cfg.Name), string(keyData)); keyErr != nil {
			return fmt.Errorf("failed registering IDE public key with Forgejo: %w", keyErr)
		}
	}

	// 3. Add user to default fleet organization
	return client.AddOrgMember(ctx, "fleet", cfg.Name)
}

// RegisterGiteaUser aliases RegisterForgejoUser for backward compatibility.
func RegisterGiteaUser(ctx context.Context, cfg *config.AgentConfig, paths config.Paths) error {
	return RegisterForgejoUser(ctx, cfg, paths)
}

// RegisterSonarUser provisions an agent user in SonarQube and generates an analysis token.
//
//nolint:gocritic // paths passed by value for consistency with config package
func RegisterSonarUser(ctx context.Context, cfg *config.AgentConfig, paths config.Paths) error {
	if cfg.IsNoContainer() || os.Getenv("SONAR_DISABLED") == "1" {
		return nil
	}
	adminPass := config.GetSonarAdminPassword()
	adminUser := os.Getenv("SONAR_ADMIN_USER")
	if adminUser == "" {
		adminUser = "admin"
	}

	sonarURL := os.Getenv("SONAR_HOST_URL")
	if sonarURL == "" {
		sonarURL = os.Getenv("SONARQUBE_URL")
	}
	if sonarURL == "" {
		sonarURL = "http://127.0.0.1:9000"
	}

	client := sonar.NewClient(sonar.ClientConfig{
		BaseURL:   sonarURL,
		AdminUser: adminUser,
		AdminPass: adminPass,
		Timeout:   2 * time.Second,
	})

	// Check if SonarQube is responsive
	if _, err := client.GetSystemStatus(ctx); err != nil {
		return err
	}

	// 1. Provision user in SonarQube
	if err := client.EnsureUser(ctx, cfg.Name, cfg.Password, cfg.Name, fmt.Sprintf("%s@local.fleet", cfg.Name)); err != nil {
		return err
	}

	// 2. Generate agent-specific analysis token
	token, err := client.GenerateUserToken(ctx, cfg.Name, fmt.Sprintf("%s-agent-token", cfg.Name))
	if err != nil {
		return fmt.Errorf("failed generating SonarQube analysis token: %w", err)
	}
	if token != "" {
		cfg.SonarToken = token
		return config.SaveAgentConfig(cfg, paths)
	}
	return nil
}

// EnsureAgentKeys verifies that the agent has valid Ed25519 signing keys and on-disk PEM secrets.
// If missing, keys are generated and persisted.
func EnsureAgentKeys(cfg *config.AgentConfig, paths config.Paths) error {
	if cfg.SigningKeyPEM == "" || cfg.PublicKeyB64 == "" {
		priv, pub, err := libbp.GenerateKeypair()
		if err != nil {
			return fmt.Errorf("failed generating Ed25519 keypair for agent %s: %w", cfg.Name, err)
		}
		pemStr, err := libbp.EncodePrivateKeyPEM(priv)
		if err != nil {
			return fmt.Errorf("failed encoding signing key PEM: %w", err)
		}
		cfg.SigningKeyPEM = pemStr
		cfg.PublicKeyB64 = libbp.EncodePublicKeyBase64(pub)
	}

	secretDir := filepath.Join(paths.SecretsDir, cfg.Name)
	if err := os.MkdirAll(secretDir, 0700); err != nil {
		return fmt.Errorf("failed creating secret dir %s: %w", secretDir, err)
	}

	keyPath := filepath.Join(secretDir, "signing-key.pem")
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		if err := os.WriteFile(keyPath, []byte(cfg.SigningKeyPEM), 0600); err != nil {
			return fmt.Errorf("failed writing signing key file: %w", err)
		}
	}

	return config.SaveAgentConfig(cfg, paths)
}

// EnsureIDEKeypair guarantees the dedicated host IDE keypair exists at paths.SSHPrivateKey (~/.ssh/fleet).
func EnsureIDEKeypair(paths config.Paths) error {
	keyPath := paths.GetSSHPrivateKey()
	if keyPath == "" {
		return nil
	}
	sshPub := fmt.Sprintf("%s.pub", keyPath)
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		_ = os.MkdirAll(filepath.Dir(keyPath), 0700)
		cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", "fleet-ide", "-f", keyPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed generating IDE keypair: %w (%s)", err, string(out))
		}
		_ = os.Chmod(keyPath, 0600)
		_ = os.Chmod(sshPub, 0644)
	} else {
		// Private key exists; check public key
		existingPubBytes, readErr := os.ReadFile(sshPub)
		existingPub := strings.TrimSpace(string(existingPubBytes))

		// Try deriving public key non-interactively with empty passphrase flag (-P "")
		cmd := exec.Command("ssh-keygen", "-y", "-P", "", "-f", keyPath)
		out, err := cmd.Output()
		if err != nil {
			// If ssh-keygen derivation fails (e.g. key has passphrase or tool unavailable):
			// If .pub already exists and is non-empty, do not fail provisioning!
			if readErr == nil && len(existingPub) > 0 {
				return nil
			}
			return fmt.Errorf("failed deriving IDE public key: %w", err)
		}

		derivedPub := strings.TrimSpace(string(out))
		if readErr != nil || len(existingPub) == 0 || existingPub != derivedPub {
			if len(existingPub) > 0 && existingPub != derivedPub {
				fmt.Fprintf(os.Stderr, "fleet warning: %s does not match private key %s; updating public key file\n", sshPub, keyPath)
			}
			pubData := append([]byte(derivedPub), '\n')
			dir := filepath.Dir(sshPub)
			tmpFile, tmpErr := os.CreateTemp(dir, ".pub.tmp.*")
			if tmpErr != nil {
				return fmt.Errorf("failed creating temp public key file: %w", tmpErr)
			}
			tmpName := tmpFile.Name()
			defer func() { _ = os.Remove(tmpName) }()
			if _, wErr := tmpFile.Write(pubData); wErr != nil {
				_ = tmpFile.Close()
				return fmt.Errorf("failed writing IDE public key: %w", wErr)
			}
			_ = tmpFile.Close()
			_ = os.Chmod(tmpName, 0644)
			if rErr := os.Rename(tmpName, sshPub); rErr != nil {
				return fmt.Errorf("failed replacing IDE public key: %w", rErr)
			}
		}
	}
	return nil
}

// EnsureOperatorKeys provisions the host operator's Ed25519 signing keypair and backplane identity.
func EnsureOperatorKeys(ctx context.Context, paths config.Paths, humanName string) (*libbp.IdentityRecord, error) {
	if humanName == "" {
		humanName = os.Getenv("HUMAN_NAME")
	}
	if humanName == "" {
		humanName = "operator"
	}
	humanName = strings.ToLower(strings.TrimSpace(humanName))

	secretDir := filepath.Join(paths.SecretsDir, humanName)
	if err := os.MkdirAll(secretDir, 0700); err != nil {
		return nil, fmt.Errorf("failed creating operator secret dir %s: %w", secretDir, err)
	}

	keyPath := filepath.Join(secretDir, "signing-key.pem")
	var pubB64 string
	if data, err := os.ReadFile(keyPath); err == nil && len(data) > 0 {
		priv, err := libbp.DecodePrivateKeyPEM(string(data))
		if err == nil {
			pubB64 = libbp.EncodePublicKeyBase64(priv.Public().(ed25519.PublicKey))
		}
	}

	if pubB64 == "" {
		priv, pub, err := libbp.GenerateKeypair()
		if err != nil {
			return nil, fmt.Errorf("failed generating operator keypair: %w", err)
		}
		pemStr, err := libbp.EncodePrivateKeyPEM(priv)
		if err != nil {
			return nil, fmt.Errorf("failed encoding operator signing key: %w", err)
		}
		if err := os.WriteFile(keyPath, []byte(pemStr), 0600); err != nil {
			return nil, fmt.Errorf("failed writing operator signing key: %w", err)
		}
		pubB64 = libbp.EncodePublicKeyBase64(pub)
	}

	record := &libbp.IdentityRecord{
		Name:   humanName,
		Role:   "Human Operator",
		Kind:   "human",
		PubKey: pubB64,
	}

	// Try registering in Valkey if reachable
	bpCfg := libbp.LoadClientFromEnv()
	client, err := libbp.Dial(ctx, libbp.ClientConfig{
		Host:     bpCfg.Host,
		Port:     bpCfg.Port,
		Username: "admin",
		Password: os.Getenv("ADMIN_BACKPLANE_PASSWORD"),
	})
	if err == nil {
		defer func() { _ = client.Close() }()
		_ = client.RegisterIdentity(ctx, *record)
	}

	return record, nil
}

// ProvisionAgent performs all infrastructure provisioning steps for an agent and returns a structured ProvisioningReport.
//
//nolint:gocritic // paths passed by value for consistency with config package
func ProvisionAgent(ctx context.Context, cfg *config.AgentConfig, paths config.Paths) *ProvisioningReport {
	report := NewProvisioningReport(cfg.Name, "provision")

	// 1. Cryptographic Keys
	if err := EnsureIDEKeypair(paths); err != nil {
		report.AddStep("Host IDE Keypair", StatusWarning, fmt.Sprintf("Failed ensuring host IDE keypair: %v", err), err)
	}
	if err := EnsureAgentKeys(cfg, paths); err == nil {
		report.AddStep("Cryptographic Signing Keys", StatusOK, "Verified Ed25519 signing keypair and on-disk secret", nil)
	} else {
		report.AddStep("Cryptographic Signing Keys", StatusError, fmt.Sprintf("Failed ensuring signing keys: %v", err), err)
	}

	// 2. Valkey ACL & Identity
	if err := RegisterValkeyACL(ctx, cfg, paths); err == nil {
		report.AddStep("Valkey ACL & Identity", StatusOK, "Registered ACL user and backplane identity", nil)
	} else if IsOfflineError(err) {
		report.AddStep("Valkey ACL & Identity", StatusSkipped, fmt.Sprintf("Valkey infrastructure is offline (skipped; start via 'fleet infra up'): %v", err), err)
	} else {
		report.AddStep("Valkey ACL & Identity", StatusError, fmt.Sprintf("Failed registering Valkey ACL: %v", err), err)
	}

	// 3. Forgejo Account & Keys
	if cfg.IsNoContainer() {
		report.AddStep("Forgejo User & Keys", StatusSkipped, "No-container agent has no container boundary (skipped)", nil)
	} else if os.Getenv("FORGEJO_DISABLED") == "1" || os.Getenv("GITEA_DISABLED") == "1" {
		report.AddStep("Forgejo User & Keys", StatusSkipped, "Forgejo infrastructure is disabled via FORGEJO_DISABLED (skipped)", nil)
	} else if err := RegisterForgejoUser(ctx, cfg, paths); err == nil {
		report.AddStep("Forgejo User & Keys", StatusOK, "Registered user account, IDE key, and fleet organization", nil)
	} else if IsOfflineError(err) {
		report.AddStep("Forgejo User & Keys", StatusSkipped, fmt.Sprintf("Forgejo infrastructure is offline (skipped; start via 'fleet infra up'): %v", err), err)
	} else {
		report.AddStep("Forgejo User & Keys", StatusError, fmt.Sprintf("Failed registering Forgejo user: %v", err), err)
	}

	// 4. SonarQube User & Analysis Token
	if cfg.IsNoContainer() {
		report.AddStep("SonarQube Account & Token", StatusSkipped, "No-container agent has no container boundary (skipped)", nil)
	} else if os.Getenv("SONAR_DISABLED") == "1" {
		report.AddStep("SonarQube Account & Token", StatusSkipped, "SonarQube infrastructure is disabled via SONAR_DISABLED (skipped)", nil)
	} else if err := RegisterSonarUser(ctx, cfg, paths); err == nil {
		report.AddStep("SonarQube Account & Token", StatusOK, "Registered user account and generated analysis token", nil)
	} else if IsOfflineError(err) {
		report.AddStep("SonarQube Account & Token", StatusSkipped, fmt.Sprintf("SonarQube infrastructure is offline (skipped; start via 'fleet infra up'): %v", err), err)
	} else {
		report.AddStep("SonarQube Account & Token", StatusError, fmt.Sprintf("Failed registering SonarQube user: %v", err), err)
	}

	// 5. Host SSH Configuration Sync
	if cfg.IsNoContainer() {
		report.AddStep("SSH Configuration", StatusSkipped, "No-container agent does not expose container SSH daemon (skipped)", nil)
	} else if err := runtime.SyncSSHConfigFile(ctx, paths); err == nil {
		report.AddStep("SSH Configuration", StatusOK, "Synchronized host SSH config", nil)
	} else {
		report.AddStep("SSH Configuration", StatusWarning, fmt.Sprintf("Failed to synchronize SSH config: %v", err), err)
	}

	return report
}
