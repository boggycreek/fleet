// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package codeintel

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// LSPConfig specifies configuration for an external LSP server driver.
type LSPConfig struct {
	Name           string         `json:"name,omitempty"`
	Command        string         `json:"command"`
	Args           []string       `json:"args,omitempty"`
	Languages      []string       `json:"languages"`
	Initialization map[string]any `json:"initialization_options,omitempty"`
}

// Config defines the declarative code intelligence configuration loaded from
// .fleet/intel.json or .agent_config.json.
type Config struct {
	DefaultProvider string            `json:"default_provider,omitempty"`
	Providers       map[string]string `json:"providers,omitempty"` // file extension -> driver name
	Disabled        []string          `json:"disabled,omitempty"`  // driver names to disable
	LSPServers      []LSPConfig       `json:"lsp_servers,omitempty"`
}

// NewDefaultRegistry instantiates a Registry pre-populated with NativeGoDriver
// and UniversalFallbackDriver.
func NewDefaultRegistry() *Registry {
	reg := NewRegistry()
	reg.Register(NewNativeGoDriver())
	reg.Register(NewUniversalFallbackDriver())
	return reg
}

// LoadRegistry searches for .fleet/intel.json or .agent_config.json in workspaceDir,
// parses declarative configuration if present, and returns a fully initialized Registry.
func LoadRegistry(workspaceDir string) (*Registry, error) {
	reg := NewDefaultRegistry()

	configPath := filepath.Join(workspaceDir, ".fleet", "intel.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		configPath = filepath.Join(workspaceDir, ".agent_config.json")
		data, err = os.ReadFile(configPath)
	}

	if err != nil {
		// No custom config file present, use default registry
		return reg, nil
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return reg, err
	}

	disabledSet := make(map[string]bool)
	for _, d := range cfg.Disabled {
		disabledSet[d] = true
	}

	// If disabled drivers are specified, construct a filtered registry base
	if len(cfg.Disabled) > 0 {
		filteredReg := NewRegistry()
		if !disabledSet["native-go"] {
			filteredReg.Register(NewNativeGoDriver())
		}
		if !disabledSet["universal-fallback"] {
			filteredReg.Register(NewUniversalFallbackDriver())
		}
		reg = filteredReg
	}

	// Register any configured LSP drivers
	for _, lspCfg := range cfg.LSPServers {
		name := lspCfg.Name
		if name == "" {
			name = "lsp"
		}
		if disabledSet[name] || disabledSet["lsp"] {
			continue
		}
		driver := NewLSPDriver(LSPDriverConfig{
			Name:           name,
			Command:        lspCfg.Command,
			Args:           lspCfg.Args,
			Languages:      lspCfg.Languages,
			WorkspaceDir:   workspaceDir,
			Initialization: lspCfg.Initialization,
		})
		reg.Register(driver)
	}

	return reg, nil
}
