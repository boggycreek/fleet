// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package config

import (
	"strings"
)

// ModelPreset represents a pre-configured LLM preset definition for edge-native
// agent cognition (e.g. Liquid AI Liquid Foundation Models per ADR 00035).
type ModelPreset struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Model         string   `json:"model"`
	Description   string   `json:"description"`
	ContextWindow int      `json:"context_window"`
	Aliases       []string `json:"aliases"`
}

// Well-known Liquid AI Liquid Foundation Model (LFM) presets.
var (
	// ModelPresetLFM3B represents Liquid Foundation Model 3B Instruct.
	ModelPresetLFM3B = ModelPreset{
		ID:            "lfm-3b",
		Name:          "Liquid Foundation Model 3B Instruct",
		Model:         "liquid/lfm-3b-instruct",
		Description:   "Liquid Foundation Model 3B Instruct - edge-native tri-hybrid dynamical model for developer laptops",
		ContextWindow: 32768,
		Aliases:       []string{"lfm-3b", "liquid/lfm-3b", "lfm3b"},
	}

	// ModelPresetLFM7B represents Liquid Foundation Model 7B Instruct.
	ModelPresetLFM7B = ModelPreset{
		ID:            "lfm-7b",
		Name:          "Liquid Foundation Model 7B Instruct",
		Model:         "liquid/lfm-7b-instruct",
		Description:   "Liquid Foundation Model 7B Instruct - mid-tier tri-hybrid dynamical model for complex tool invocation",
		ContextWindow: 32768,
		Aliases:       []string{"lfm-7b", "liquid/lfm-7b", "lfm7b"},
	}

	// ModelPresetLFM40BMoE represents Liquid Foundation Model 40B MoE.
	ModelPresetLFM40BMoE = ModelPreset{
		ID:            "lfm-40b-moe",
		Name:          "Liquid Foundation Model 40B MoE",
		Model:         "liquid/lfm-40b-moe",
		Description:   "Liquid Foundation Model 40B MoE - high-capacity sparse mixture-of-experts dynamical model for server swarms",
		ContextWindow: 65536,
		Aliases:       []string{"lfm-40b-moe", "liquid/lfm-40b-moe", "lfm40b-moe", "lfm-40b", "liquid/lfm-40b", "lfm40b"},
	}
)

var defaultModelPresets = []ModelPreset{
	ModelPresetLFM3B,
	ModelPresetLFM7B,
	ModelPresetLFM40BMoE,
}

// GetModelPresets returns all registered model presets.
func GetModelPresets() []ModelPreset {
	out := make([]ModelPreset, len(defaultModelPresets))
	copy(out, defaultModelPresets)
	return out
}

// GetModelPreset returns the model preset corresponding to name (case-insensitive query
// matching ID, target model, or alias).
func GetModelPreset(name string) (ModelPreset, bool) {
	clean := strings.ToLower(strings.TrimSpace(name))
	if clean == "" {
		return ModelPreset{}, false
	}

	for _, preset := range defaultModelPresets {
		if strings.ToLower(preset.ID) == clean || strings.ToLower(preset.Model) == clean {
			return preset, true
		}
		for _, alias := range preset.Aliases {
			if strings.ToLower(alias) == clean {
				return preset, true
			}
		}
	}
	return ModelPreset{}, false
}

// ResolveModelPreset returns the canonical target model name (e.g. liquid/lfm-3b-instruct)
// if name matches a preset ID or alias; otherwise returns trimmed name unchanged.
func ResolveModelPreset(name string) string {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return ""
	}
	if preset, found := GetModelPreset(clean); found {
		return preset.Model
	}
	return clean
}
