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

// Config defines the declarative code intelligence configuration loaded from
// .fleet/intel.json or .agent_config.json.
type Config struct {
	DefaultProvider string            `json:"default_provider,omitempty"`
	Providers       map[string]string `json:"providers,omitempty"` // file extension -> driver name
	Disabled        []string          `json:"disabled,omitempty"`  // driver names to disable
}

// NewDefaultRegistry instantiates a Registry pre-populated with NativeGoDriver,
// PolyglotDriver, and UniversalFallbackDriver.
func NewDefaultRegistry() *Registry {
	reg := NewRegistry()
	reg.Register(NewNativeGoDriver())
	reg.Register(NewPolyglotDriver())
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

	// If disabled drivers are specified, construct a filtered registry
	if len(cfg.Disabled) > 0 {
		disabledSet := make(map[string]bool)
		for _, d := range cfg.Disabled {
			disabledSet[d] = true
		}

		filteredReg := NewRegistry()
		if !disabledSet["native-go"] {
			filteredReg.Register(NewNativeGoDriver())
		}
		if !disabledSet["polyglot-ast"] {
			filteredReg.Register(NewPolyglotDriver())
		}
		if !disabledSet["universal-fallback"] {
			filteredReg.Register(NewUniversalFallbackDriver())
		}
		return filteredReg, nil
	}

	return reg, nil
}
