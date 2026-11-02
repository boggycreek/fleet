// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package config

import (
	"testing"
)

func TestGetModelPresets(t *testing.T) {
	presets := GetModelPresets()
	if len(presets) < 3 {
		t.Fatalf("expected at least 3 model presets, got %d", len(presets))
	}

	expectedIDs := map[string]bool{
		"lfm-3b":      false,
		"lfm-7b":      false,
		"lfm-40b-moe": false,
	}

	for _, p := range presets {
		if _, ok := expectedIDs[p.ID]; ok {
			expectedIDs[p.ID] = true
		}
		if p.ID == "" || p.Name == "" || p.Model == "" || p.Description == "" || p.ContextWindow <= 0 {
			t.Errorf("preset %q has incomplete fields: %+v", p.ID, p)
		}
		if len(p.Aliases) == 0 {
			t.Errorf("preset %q must have at least one alias", p.ID)
		}
	}

	for id, found := range expectedIDs {
		if !found {
			t.Errorf("expected preset %q not found in GetModelPresets()", id)
		}
	}
}

func TestGetModelPreset(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		expectFound bool
		expectedID  string
		expectedMod string
	}{
		{
			name:        "lookup by id lowercase",
			query:       "lfm-3b",
			expectFound: true,
			expectedID:  "lfm-3b",
			expectedMod: "liquid/lfm-3b-instruct",
		},
		{
			name:        "lookup by id uppercase",
			query:       "LFM-7B",
			expectFound: true,
			expectedID:  "lfm-7b",
			expectedMod: "liquid/lfm-7b-instruct",
		},
		{
			name:        "lookup by target model",
			query:       "liquid/lfm-3b-instruct",
			expectFound: true,
			expectedID:  "lfm-3b",
			expectedMod: "liquid/lfm-3b-instruct",
		},
		{
			name:        "lookup by alias with whitespace",
			query:       "  liquid/lfm-7b  ",
			expectFound: true,
			expectedID:  "lfm-7b",
			expectedMod: "liquid/lfm-7b-instruct",
		},
		{
			name:        "lookup 40b alias",
			query:       "lfm40b-moe",
			expectFound: true,
			expectedID:  "lfm-40b-moe",
			expectedMod: "liquid/lfm-40b-moe",
		},
		{
			name:        "lookup 40b shorthand alias",
			query:       "lfm-40b",
			expectFound: true,
			expectedID:  "lfm-40b-moe",
			expectedMod: "liquid/lfm-40b-moe",
		},
		{
			name:        "unknown model",
			query:       "gpt-4o",
			expectFound: false,
		},
		{
			name:        "empty query",
			query:       "   ",
			expectFound: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			preset, found := GetModelPreset(tc.query)
			if found != tc.expectFound {
				t.Fatalf("query %q: expected found=%v, got %v", tc.query, tc.expectFound, found)
			}
			if found {
				if preset.ID != tc.expectedID {
					t.Errorf("query %q: expected ID %q, got %q", tc.query, tc.expectedID, preset.ID)
				}
				if preset.Model != tc.expectedMod {
					t.Errorf("query %q: expected Model %q, got %q", tc.query, tc.expectedMod, preset.Model)
				}
			}
		})
	}
}

func TestResolveModelPreset(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "resolve lfm-3b",
			input:    "lfm-3b",
			expected: "liquid/lfm-3b-instruct",
		},
		{
			name:     "resolve lfm3b alias",
			input:    "lfm3b",
			expected: "liquid/lfm-3b-instruct",
		},
		{
			name:     "resolve liquid/lfm-3b alias",
			input:    "liquid/lfm-3b",
			expected: "liquid/lfm-3b-instruct",
		},
		{
			name:     "resolve lfm-7b case insensitive",
			input:    "LFM-7B",
			expected: "liquid/lfm-7b-instruct",
		},
		{
			name:     "resolve lfm-40b-moe",
			input:    "lfm-40b-moe",
			expected: "liquid/lfm-40b-moe",
		},
		{
			name:     "resolve already canonical model name",
			input:    "liquid/lfm-3b-instruct",
			expected: "liquid/lfm-3b-instruct",
		},
		{
			name:     "pass through unknown model name",
			input:    "qwen2.5-coder:32b",
			expected: "qwen2.5-coder:32b",
		},
		{
			name:     "pass through trimmed custom model name",
			input:    "  custom-org/custom-model  ",
			expected: "custom-org/custom-model",
		},
		{
			name:     "empty string returns empty",
			input:    "   ",
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveModelPreset(tc.input)
			if got != tc.expected {
				t.Errorf("ResolveModelPreset(%q): expected %q, got %q", tc.input, tc.expected, got)
			}
		})
	}
}

func TestNewAgentConfig_ModelPresetResolution(t *testing.T) {
	cfg, err := NewAgentConfig("test-agent", "opencode", "worker", "http://localhost:11434/v1", "lfm-3b", "my-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.ModelName != "liquid/lfm-3b-instruct" {
		t.Errorf("expected ModelName 'liquid/lfm-3b-instruct', got %q", cfg.ModelName)
	}

	// Verify unknown model preserved
	cfg2, err := NewAgentConfig("test-agent2", "opencode", "worker", "http://localhost:11434/v1", "claude-3-5-sonnet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg2.ModelName != "claude-3-5-sonnet" {
		t.Errorf("expected ModelName 'claude-3-5-sonnet', got %q", cfg2.ModelName)
	}

	// Verify empty model preserved
	cfg3, err := NewAgentConfig("test-agent3", "opencode", "worker", "http://localhost:11434/v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg3.ModelName != "" {
		t.Errorf("expected empty ModelName, got %q", cfg3.ModelName)
	}
}
