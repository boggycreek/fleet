// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathsAndAgentConfig(t *testing.T) {
	tmpDir := t.TempDir()
	paths := Paths{
		DataHome:   filepath.Join(tmpDir, "data"),
		AgentsDir:  filepath.Join(tmpDir, "data", "agents"),
		SecretsDir: filepath.Join(tmpDir, "data", "secrets"),
		BinDir:     filepath.Join(tmpDir, "bin"),
		SSHDir:     filepath.Join(tmpDir, "ssh"),
		IDEKeyFile: filepath.Join(tmpDir, "ssh", "key"),
	}

	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	// 1. Resolve image
	if img := ResolveImage("native"); img != "fleet-native:latest" {
		t.Errorf("expected native preset image, got %s", img)
	}
	if img := ResolveImage("fleet"); img != "fleet-native:latest" {
		t.Errorf("expected fleet preset image, got %s", img)
	}
	if img := ResolveImage("sndbx"); img != "fleet-native:latest" {
		t.Errorf("expected sndbx preset image, got %s", img)
	}
	if img := ResolveImage("opencode"); img != "fleet-opencode:latest" {
		t.Errorf("expected opencode preset image, got %s", img)
	}
	if img := ResolveImage("claude"); img != "fleet-claude:latest" {
		t.Errorf("expected claude preset image, got %s", img)
	}
	if img := ResolveImage("agy"); img != "fleet-agy:latest" {
		t.Errorf("expected agy preset image, got %s", img)
	}
	if img := ResolveImage("pig"); img != "fleet-pig:latest" {
		t.Errorf("expected pig preset image, got %s", img)
	}
	if img := ResolveImage("egress"); img != "fleet-egress:latest" {
		t.Errorf("expected egress preset image, got %s", img)
	}
	if img := ResolveImage("sonarqube"); img != "fleet-sonarqube:latest" {
		t.Errorf("expected sonarqube preset image, got %s", img)
	}
	if img := ResolveImage("custom/image:tag"); img != "custom/image:tag" {
		t.Errorf("expected custom image passthrough, got %s", img)
	}
	if img := ResolveImage(""); img != "fleet-base:latest" {
		t.Errorf("expected default base image, got %s", img)
	}

	// 2. NewAgentConfig errors & creation
	if _, err := NewAgentConfig("", "base", "role"); err == nil {
		t.Errorf("expected error for empty agent name")
	}
	if _, err := NewAgentConfig("operator", "base", "role"); err == nil {
		t.Errorf("expected error for reserved agent name 'operator'")
	}
	if _, err := NewAgentConfig("human", "base", "role"); err == nil {
		t.Errorf("expected error for reserved agent name 'human'")
	}
	if _, err := NewAgentConfig("-invalid-start", "base", "role"); err == nil {
		t.Errorf("expected error for invalid agent name '-invalid-start'")
	}
	if _, err := NewAgentConfig("a", "base", "role"); err == nil {
		t.Errorf("expected error for too short agent name 'a'")
	}
	if _, err := NewAgentConfig("invalid/slash", "base", "role"); err == nil {
		t.Errorf("expected error for agent name with slash")
	}

	cfg, err := NewAgentConfig("test-agent", "opencode", "", "http://localhost:11434/v1", "llama3", "secret-key")
	if err != nil {
		t.Fatalf("NewAgentConfig failed: %v", err)
	}
	if cfg.Name != "test-agent" || cfg.Role != "coding-agent" || cfg.ModelURL != "http://localhost:11434/v1" || cfg.ModelName != "llama3" || cfg.ModelAPIKey != "secret-key" || !strings.Contains(cfg.ContainerName, "test-agent") {
		t.Errorf("unexpected agent config: %+v", cfg)
	}

	// 3. Save & Load & List
	if err := SaveAgentConfig(cfg, paths); err != nil {
		t.Fatalf("SaveAgentConfig failed: %v", err)
	}

	loaded, err := LoadAgentConfig("test-agent", paths)
	if err != nil {
		t.Fatalf("LoadAgentConfig failed: %v", err)
	}
	if loaded.Name != cfg.Name || loaded.Password != cfg.Password {
		t.Errorf("loaded config mismatch: %+v vs %+v", loaded, cfg)
	}

	list, err := ListAgentConfigs(paths)
	if err != nil || len(list) != 1 {
		t.Errorf("ListAgentConfigs failed: len=%d, err=%v", len(list), err)
	}

	// 4. Missing agent error
	if _, err := LoadAgentConfig("nonexistent", paths); err == nil {
		t.Errorf("expected error for nonexistent agent")
	}

	// Host-agent tests
	cfg.Runtime = "no-container"
	if !cfg.IsNoContainer() || !cfg.IsHost() || cfg.IsHostContainer() {
		t.Errorf("expected IsNoContainer()=true, IsHost()=true, IsHostContainer()=false when Runtime=no-container")
	}
	cfg.Runtime = "host-container"
	cfg.WorkspacePath = "/tmp"
	if !cfg.IsHostContainer() || !cfg.IsHost() || cfg.IsNoContainer() {
		t.Errorf("expected IsHostContainer()=true, IsHost()=true, IsNoContainer()=false when Runtime=host-container")
	}
	cfg.Runtime = "container"
	cfg.HostOnly = true
	if !cfg.IsNoContainer() || !cfg.IsHost() {
		t.Errorf("expected IsNoContainer() and IsHost() to return true when HostOnly=true")
	}
	cfg.HostOnly = false
	if cfg.IsHost() || cfg.IsHostContainer() || cfg.IsNoContainer() {
		t.Errorf("expected all host helpers to return false for standard container")
	}

	// Baremetal agent tests
	if cfg.IsBaremetal() {
		t.Errorf("expected IsBaremetal()=false for standard container")
	}
	cfg.Baremetal = true
	cfg.SSHHost = "192.168.1.100"
	cfg.SSHPort = 2222
	cfg.SSHUser = "debian"
	if !cfg.IsBaremetal() {
		t.Errorf("expected IsBaremetal()=true when Baremetal=true")
	}
	cfg.Baremetal = false
	cfg.Runtime = "baremetal"
	if !cfg.IsBaremetal() {
		t.Errorf("expected IsBaremetal()=true when Runtime=baremetal")
	}
	cfg.Runtime = "container"

	// New image presets
	if img := ResolveImage("core"); img != "fleet-core:latest" {
		t.Errorf("expected core preset to resolve to fleet-core:latest, got %q", img)
	}
	if img := ResolveImage("daemon"); img != "fleet-daemon-base:latest" {
		t.Errorf("expected daemon preset to resolve to fleet-daemon-base:latest, got %q", img)
	}
	if img := ResolveImage("tool"); img != "fleet-tool-base:latest" {
		t.Errorf("expected tool preset to resolve to fleet-tool-base:latest, got %q", img)
	}

	// 5. Delete agent
	if err := DeleteAgentConfig("test-agent", paths); err != nil {
		t.Errorf("DeleteAgentConfig error: %v", err)
	}
	listAfter, _ := ListAgentConfigs(paths)
	if len(listAfter) != 0 {
		t.Errorf("expected 0 agents after delete, got %d", len(listAfter))
	}

	// 6. GetPaths
	defaultPaths := GetPaths()
	if defaultPaths.DataHome == "" || defaultPaths.SSHConfigFile == "" {
		t.Errorf("GetPaths returned empty DataHome or SSHConfigFile: %+v", defaultPaths)
	}

	// 7. Whitespace in ResolveImage defaults to base
	if img := ResolveImage("   \t\n"); img != "fleet-base:latest" {
		t.Errorf("expected whitespace to resolve to base image, got %q", img)
	}

	// 8. Corrupt agent config handling
	corruptFile := filepath.Join(paths.AgentsDir, "broken.json")
	if err := os.WriteFile(corruptFile, []byte("{invalid-json-content"), 0600); err != nil {
		t.Fatalf("failed to write corrupt agent json: %v", err)
	}
	if _, err := LoadAgentConfig("broken", paths); err == nil {
		t.Errorf("expected LoadAgentConfig to fail on corrupt json")
	}

	// ListAgentConfigs skips corrupt json without failing
	validCfg, _ := NewAgentConfig("valid-agent", "base", "worker")
	_ = SaveAgentConfig(validCfg, paths)
	agents, err := ListAgentConfigs(paths)
	if err != nil {
		t.Errorf("ListAgentConfigs failed with corrupt file present: %v", err)
	}
	if len(agents) != 1 || agents[0].Name != "valid-agent" {
		t.Errorf("expected 1 valid agent in list, got %d", len(agents))
	}

	// 9. SaveAgentConfig fails when EnsureDirectories fails
	blockedPaths := Paths{
		DataHome: corruptFile,
	}
	if saveErr := SaveAgentConfig(validCfg, blockedPaths); saveErr == nil {
		t.Errorf("expected SaveAgentConfig to fail when EnsureDirectories fails")
	}

	// 10. ListAgentConfigs with non-existent directory returns nil, nil
	missingDirPaths := Paths{AgentsDir: filepath.Join(tmpDir, "does-not-exist")}
	missingAgents, listErr := ListAgentConfigs(missingDirPaths)
	if listErr != nil || len(missingAgents) != 0 {
		t.Errorf("expected empty list without error for nonexistent directory, got len=%d, err=%v", len(missingAgents), listErr)
	}

	// 11. ListAgentConfigs with file instead of directory returns error
	errorDirPaths := Paths{AgentsDir: corruptFile}
	if _, errDir := ListAgentConfigs(errorDirPaths); errDir == nil {
		t.Errorf("expected ListAgentConfigs to return error when AgentsDir is a file")
	}
}

func TestSaveAgentConfig_BPProfilesAndErrors(t *testing.T) {
	tmpDir := t.TempDir()
	bpDir := filepath.Join(tmpDir, "profiles")
	paths := Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SecretsDir:    filepath.Join(tmpDir, "data", "secrets"),
		BPProfilesDir: bpDir,
	}
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	cfg, err := NewAgentConfig("bp-agent", "base", "worker")
	if err != nil {
		t.Fatalf("NewAgentConfig failed: %v", err)
	}

	// 1. Successful save with BPProfilesDir configured
	if err := SaveAgentConfig(cfg, paths); err != nil {
		t.Fatalf("SaveAgentConfig failed: %v", err)
	}
	profilePath := filepath.Join(bpDir, "bp-agent.env")
	data, err := os.ReadFile(profilePath)
	if err != nil || !strings.Contains(string(data), "BP_AGENT=bp-agent") {
		t.Errorf("expected backplane profile created with BP_AGENT, got err=%v, data=%s", err, string(data))
	}

	// Verify explicit file and directory permissions
	jsonPath := filepath.Join(paths.AgentsDir, "bp-agent.json")
	if info, err := os.Stat(jsonPath); err != nil {
		t.Errorf("failed to stat agent json: %v", err)
	} else if info.Mode().Perm() != 0600 {
		t.Errorf("expected agent json mode 0600, got %v", info.Mode().Perm())
	}

	secretDir := filepath.Join(paths.SecretsDir, "bp-agent")
	if info, err := os.Stat(secretDir); err != nil {
		t.Errorf("failed to stat agent secret directory: %v", err)
	} else if info.Mode().Perm() != 0700 {
		t.Errorf("expected agent secret directory mode 0700, got %v", info.Mode().Perm())
	}

	keyPath := filepath.Join(secretDir, "signing-key.pem")
	if info, err := os.Stat(keyPath); err != nil {
		t.Errorf("failed to stat signing key: %v", err)
	} else if info.Mode().Perm() != 0600 {
		t.Errorf("expected signing key mode 0600, got %v", info.Mode().Perm())
	}

	if info, err := os.Stat(profilePath); err != nil {
		t.Errorf("failed to stat bp profile: %v", err)
	} else if info.Mode().Perm() != 0600 {
		t.Errorf("expected bp profile mode 0600, got %v", info.Mode().Perm())
	}

	// 2. DeleteAgentConfig removes profile
	if err := DeleteAgentConfig("bp-agent", paths); err != nil {
		t.Fatalf("DeleteAgentConfig failed: %v", err)
	}
	if _, err := os.Stat(profilePath); !os.IsNotExist(err) {
		t.Errorf("expected profile file to be deleted")
	}

	// 3. SaveAgentConfig fails when BPProfilesDir cannot be created (is a file)
	blockerFile := filepath.Join(tmpDir, "blocker")
	_ = os.WriteFile(blockerFile, []byte("blocker"), 0600)
	failBPPaths := paths
	failBPPaths.BPProfilesDir = filepath.Join(blockerFile, "sub")
	if err := SaveAgentConfig(cfg, failBPPaths); err == nil {
		t.Errorf("expected SaveAgentConfig to fail when BPProfilesDir cannot be created")
	}

	// 4. SaveAgentConfig fails when profilePath cannot be written (profilePath is a directory)
	failWritePaths := paths
	_ = os.MkdirAll(filepath.Join(bpDir, "dir-agent.env"), 0755)
	cfgDirAgent, _ := NewAgentConfig("dir-agent", "base", "worker")
	if err := SaveAgentConfig(cfgDirAgent, failWritePaths); err == nil {
		t.Errorf("expected SaveAgentConfig to fail when profile path is a directory")
	}

	// 5. SaveAgentConfig fails when SecretsDir has a file preventing secret directory creation
	failSecretPaths := paths
	cfgSecret, _ := NewAgentConfig("secret-agent", "base", "worker")
	_ = os.WriteFile(filepath.Join(paths.SecretsDir, "secret-agent"), []byte("blocking-file"), 0600)
	if err := SaveAgentConfig(cfgSecret, failSecretPaths); err == nil {
		t.Errorf("expected SaveAgentConfig to fail when secret directory cannot be created")
	}

	// 5b. SaveAgentConfig fails when signing-key.pem cannot be written (signing-key.pem is a directory)
	failKeyPaths := paths
	cfgKeyDir, _ := NewAgentConfig("keydir-agent", "base", "worker")
	_ = os.MkdirAll(filepath.Join(paths.SecretsDir, "keydir-agent", "signing-key.pem"), 0755)
	if err := SaveAgentConfig(cfgKeyDir, failKeyPaths); err == nil {
		t.Errorf("expected SaveAgentConfig to fail when signing-key.pem is a directory")
	}

	// 6. SaveAgentConfig fails when agent JSON file cannot be written (jsonPath is a directory)
	failJSONPaths := paths
	_ = os.MkdirAll(filepath.Join(paths.AgentsDir, "dir-json.json"), 0755)
	cfgJSONDir, _ := NewAgentConfig("dir-json", "base", "worker")
	if err := SaveAgentConfig(cfgJSONDir, failJSONPaths); err == nil {
		t.Errorf("expected SaveAgentConfig to fail when jsonPath is a directory")
	}
}

func TestSaveAgentConfig_DoNotOverwriteSigningKeyWhenEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	paths := Paths{
		DataHome:   filepath.Join(tmpDir, "data"),
		AgentsDir:  filepath.Join(tmpDir, "data", "agents"),
		SecretsDir: filepath.Join(tmpDir, "data", "secrets"),
	}
	_ = paths.EnsureDirectories()

	cfg, err := NewAgentConfig("preserve-key", "base", "coder")
	if err != nil {
		t.Fatalf("failed creating agent config: %v", err)
	}
	originalKey := cfg.SigningKeyPEM
	if err := SaveAgentConfig(cfg, paths); err != nil {
		t.Fatalf("first SaveAgentConfig failed: %v", err)
	}

	keyPath := filepath.Join(paths.SecretsDir, cfg.Name, "signing-key.pem")
	if data, err := os.ReadFile(keyPath); err != nil || string(data) != originalKey {
		t.Fatalf("expected key to match original, got %s, err=%v", string(data), err)
	}

	// Now empty SigningKeyPEM and call SaveAgentConfig again; existing key must NOT be overwritten
	cfg.SigningKeyPEM = ""
	if err := SaveAgentConfig(cfg, paths); err != nil {
		t.Fatalf("second SaveAgentConfig failed: %v", err)
	}

	if data, err := os.ReadFile(keyPath); err != nil || string(data) != originalKey {
		t.Errorf("expected signing-key.pem to be preserved with non-empty content, got: %s (err: %v)", string(data), err)
	}
}

func TestOpenRouterKeyAndURLResolution(t *testing.T) {
	tmpDir := t.TempDir()
	paths := Paths{
		DataHome:   filepath.Join(tmpDir, "data"),
		SecretsDir: filepath.Join(tmpDir, "data", "secrets"),
	}
	_ = paths.EnsureDirectories()

	// 1. Test NormalizeModelURL
	if norm := NormalizeModelURL("openrouter"); norm != "http://fleet-openrouter:8080/v1" {
		t.Errorf("expected http://fleet-openrouter:8080/v1, got %s", norm)
	}
	if norm := NormalizeModelURL("http://openrouter"); norm != "http://fleet-openrouter:8080/v1" {
		t.Errorf("expected http://fleet-openrouter:8080/v1, got %s", norm)
	}
	if norm := NormalizeModelURL("http://localhost:11434/v1"); norm != "http://localhost:11434/v1" {
		t.Errorf("expected original URL preserved, got %s", norm)
	}

	// 2. Test GetOpenRouterAPIKey when empty
	t.Setenv("OPENROUTER_API_KEY", "")
	if key := GetOpenRouterAPIKey(paths); key != "" {
		t.Errorf("expected empty key, got %q", key)
	}

	// 3. Test GetOpenRouterAPIKey from env
	t.Setenv("OPENROUTER_API_KEY", "sk-or-env-key-1234")
	if key := GetOpenRouterAPIKey(paths); key != "sk-or-env-key-1234" {
		t.Errorf("expected env key, got %q", key)
	}

	// 4. Test GetOpenRouterAPIKey from secrets file override
	keyFile := filepath.Join(paths.SecretsDir, "openrouter.key")
	if err := os.WriteFile(keyFile, []byte("  sk-or-file-key-5678 \n"), 0600); err != nil {
		t.Fatalf("failed to write openrouter.key: %v", err)
	}
	if key := paths.GetOpenRouterAPIKey(); key != "sk-or-file-key-5678" {
		t.Errorf("expected file key to take precedence, got %q", key)
	}
}
