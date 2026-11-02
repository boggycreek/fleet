// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
)

// Well-known image presets
var WellKnownImages = map[string]string{
	"base":      "fleet-base:latest",
	"core":      "fleet-core:latest",
	"daemon":    "fleet-daemon-base:latest",
	"tool":      "fleet-tool-base:latest",
	"native":    "fleet-native:latest",
	"fleet":     "fleet-native:latest",
	"sndbx":     "fleet-native:latest",
	"opencode":  "fleet-opencode:latest",
	"claude":    "fleet-claude:latest",
	"agy":       "fleet-agy:latest",
	"pig":       "fleet-pig:latest",
	"egress":    "fleet-egress:latest",
	"sonarqube": "fleet-sonarqube:latest",
}

// AgentConfig holds persistent configuration for a single named agent
type AgentConfig struct {
	Name          string    `json:"name"`
	Role          string    `json:"role"`
	Image         string    `json:"image"`
	Harness       string    `json:"harness,omitempty"`
	ContainerName string    `json:"container_name,omitempty"`
	VolumeName    string    `json:"volume_name,omitempty"`
	WorkspacePath string    `json:"workspace_path,omitempty"`
	Password      string    `json:"password"`
	SigningKeyPEM string    `json:"signing_key_pem"`
	PublicKeyB64  string    `json:"public_key_b64"`
	ModelURL      string    `json:"model_url,omitempty"`
	ModelName     string    `json:"model_name,omitempty"`
	ModelAPIKey   string    `json:"model_api_key,omitempty"`
	SonarToken    string    `json:"sonar_token,omitempty"`
	PodName       string    `json:"pod_name,omitempty"`
	UsePod        bool      `json:"use_pod,omitempty"`
	Runtime       string    `json:"runtime,omitempty"` // "container" (default), "host-container", "no-container", or "baremetal"
	HostOnly      bool      `json:"host_only,omitempty"`
	Baremetal     bool      `json:"baremetal,omitempty"`
	SSHHost       string    `json:"ssh_host,omitempty"`
	SSHPort       int       `json:"ssh_port,omitempty"`
	SSHUser       string    `json:"ssh_user,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// GetPodName returns the dedicated Podman pod name for the agent.
func (c *AgentConfig) GetPodName() string {
	if c.PodName != "" {
		return c.PodName
	}
	return fmt.Sprintf("fleet-%s", c.Name)
}

// UsePods returns true if the agent is configured to run inside a Podman pod.
func (c *AgentConfig) UsePods() bool {
	if c.UsePod {
		return true
	}
	return os.Getenv("FLEET_ENABLE_PODS") == "1"
}

// IsBaremetal returns true if the agent is configured to run directly on a baremetal physical machine.
func (c *AgentConfig) IsBaremetal() bool {
	return c.Baremetal || c.Runtime == "baremetal"
}

// IsNoContainer returns true if the agent runs directly on the host without any container.
func (c *AgentConfig) IsNoContainer() bool {
	return c.HostOnly || c.Runtime == "no-container" || (c.Runtime == "host" && c.WorkspacePath == "" && c.ContainerName == "")
}

// IsHostContainer returns true if the agent runs as a host-connected container with workspace projection
func (c *AgentConfig) IsHostContainer() bool {
	return c.Runtime == "host-container" || (c.Runtime == "host" && (c.WorkspacePath != "" || c.ContainerName != ""))
}

// IsHost returns true if the agent operates in host context (no-container host or host-connected container)
func (c *AgentConfig) IsHost() bool {
	return c.IsNoContainer() || c.IsHostContainer()
}

// NormalizeModelURL normalizes dynamic or shortcut model URLs such as "openrouter" to canonical endpoints.
func NormalizeModelURL(input string) string {
	clean := strings.TrimSpace(input)
	lower := strings.ToLower(clean)
	if lower == "openrouter" || lower == "http://openrouter" || lower == "https://openrouter" ||
		lower == "openrouter/v1" || lower == "http://openrouter/v1" || lower == "https://openrouter/v1" ||
		lower == "http://fleet-openrouter/v1" || lower == "fleet-openrouter" {
		return "http://fleet-openrouter:8080/v1"
	}
	return clean
}

// ResolveImage returns the canonical OCI image name for a given input or preset
func ResolveImage(input string) string {
	lower := strings.ToLower(strings.TrimSpace(input))
	if img, found := WellKnownImages[lower]; found {
		return img
	}
	if lower == "" {
		return WellKnownImages["base"]
	}
	return input
}

var (
	validAgentName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)
	reservedNames  = map[string]bool{
		"operator": true,
		"human":    true,
		"default":  true,
		"admin":    true,
		"system":   true,
	}
)

// NewAgentConfig creates a newly initialized AgentConfig with random credentials
func NewAgentConfig(name, imageInput, role string, modelOpts ...string) (*AgentConfig, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil, fmt.Errorf("agent name cannot be empty")
	}
	if !validAgentName.MatchString(name) {
		return nil, fmt.Errorf("invalid agent name %q: must match ^[a-z0-9][a-z0-9_-]{1,63}$", name)
	}
	if reservedNames[name] {
		return nil, fmt.Errorf("agent name %q is reserved", name)
	}

	if role == "" {
		role = "coding-agent"
	}

	var modelURL, modelName, modelAPIKey string
	if len(modelOpts) > 0 {
		modelURL = NormalizeModelURL(modelOpts[0])
	}
	if len(modelOpts) > 1 {
		modelName = ResolveModelPreset(modelOpts[1])
	}
	if len(modelOpts) > 2 {
		modelAPIKey = strings.TrimSpace(modelOpts[2])
	}

	// Generate random 32-character hex password
	pwBytes := make([]byte, 16)
	if _, err := rand.Read(pwBytes); err != nil {
		return nil, err
	}
	password := hex.EncodeToString(pwBytes)

	// Generate Ed25519 signing keypair
	priv, pub, err := libbp.GenerateKeypair()
	if err != nil {
		return nil, err
	}
	pemStr, err := libbp.EncodePrivateKeyPEM(priv)
	if err != nil {
		return nil, err
	}
	pubB64 := libbp.EncodePublicKeyBase64(pub)

	return &AgentConfig{
		Name:          name,
		Role:          role,
		Image:         ResolveImage(imageInput),
		ContainerName: fmt.Sprintf("fleet-agent-%s", name),
		PodName:       fmt.Sprintf("fleet-%s", name),
		VolumeName:    fmt.Sprintf("fleet-agent-%s-home", name),
		Password:      password,
		SigningKeyPEM: pemStr,
		PublicKeyB64:  pubB64,
		ModelURL:      modelURL,
		ModelName:     modelName,
		ModelAPIKey:   modelAPIKey,
		CreatedAt:     time.Now().UTC(),
	}, nil
}

// SaveAgentConfig writes the agent descriptor and secret key files
func SaveAgentConfig(cfg *AgentConfig, paths Paths) error {
	if err := paths.EnsureDirectories(); err != nil {
		return err
	}

	// Save agent JSON
	jsonPath := filepath.Join(paths.AgentsDir, fmt.Sprintf("%s.json", cfg.Name))
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, data, 0600); err != nil {
		return err
	}

	// Save dedicated secret directory
	agentSecretDir := filepath.Join(paths.SecretsDir, cfg.Name)
	if err := os.MkdirAll(agentSecretDir, 0700); err != nil {
		return err
	}

	keyPath := filepath.Join(agentSecretDir, "signing-key.pem")
	skipKeyWrite := false
	if cfg.SigningKeyPEM == "" {
		if fi, err := os.Stat(keyPath); err == nil && fi.Size() > 0 {
			skipKeyWrite = true
		}
	}
	if !skipKeyWrite {
		if err := os.WriteFile(keyPath, []byte(cfg.SigningKeyPEM), 0600); err != nil {
			return err
		}
	}

	// Write BP profile .env in StateDir/bp/profiles/<name>.env if directory is configured
	if paths.BPProfilesDir != "" {
		if err := os.MkdirAll(paths.BPProfilesDir, 0700); err != nil {
			return fmt.Errorf("failed creating backplane profiles directory: %w", err)
		}
		profilePath := filepath.Join(paths.BPProfilesDir, fmt.Sprintf("%s.env", cfg.Name))
		profileContent := fmt.Sprintf("BP_AGENT=%s\nBP_PASSWORD=%s\nBP_SIGNING_KEY=%s\n",
			cfg.Name, cfg.Password, keyPath)
		if err := os.WriteFile(profilePath, []byte(profileContent), 0600); err != nil {
			return fmt.Errorf("failed writing backplane profile %s: %w", profilePath, err)
		}
		_ = os.Chmod(profilePath, 0600)
	}

	return nil
}

// LoadAgentConfig reads an existing agent configuration by name
func LoadAgentConfig(name string, paths Paths) (*AgentConfig, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	jsonPath := filepath.Join(paths.AgentsDir, fmt.Sprintf("%s.json", name))
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("agent %q not found (no config at %s)", name, jsonPath)
	}

	var cfg AgentConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("corrupted agent config for %q: %w", name, err)
	}
	return &cfg, nil
}

// ListAgentConfigs discovers all configured agents in the repository
func ListAgentConfigs(paths Paths) ([]*AgentConfig, error) {
	if err := paths.EnsureDirectories(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(paths.AgentsDir)
	if err != nil {
		return nil, err
	}

	var configs []*AgentConfig
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			name := strings.TrimSuffix(e.Name(), ".json")
			cfg, err := LoadAgentConfig(name, paths)
			if err == nil && cfg != nil {
				configs = append(configs, cfg)
			}
		}
	}
	return configs, nil
}

// DeleteAgentConfig removes the agent config, secrets, and profile
func DeleteAgentConfig(name string, paths Paths) error {
	name = strings.ToLower(strings.TrimSpace(name))
	jsonPath := filepath.Join(paths.AgentsDir, fmt.Sprintf("%s.json", name))
	_ = os.Remove(jsonPath)

	agentSecretDir := filepath.Join(paths.SecretsDir, name)
	_ = os.RemoveAll(agentSecretDir)

	if paths.BPProfilesDir != "" {
		profilePath := filepath.Join(paths.BPProfilesDir, fmt.Sprintf("%s.env", name))
		_ = os.Remove(profilePath)
	}
	return nil
}
