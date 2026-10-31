// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package config

import (
	"strings"
	"testing"
)

func TestTopologyPresets_ListingAndRetrieval(t *testing.T) {
	presets := GetTopologyPresets()
	if len(presets) != 2 {
		t.Fatalf("expected 2 presets, got %d", len(presets))
	}

	for _, p := range presets {
		if err := p.Validate(); err != nil {
			t.Errorf("preset %s failed validation: %v", p.Name, err)
		}
	}

	// Test lookups by name, id, and aliases (case-insensitive)
	testCases := []struct {
		query       string
		expectedID  string
		expectedNP  int
		expectedCtx int
	}{
		{"swarm", "topology-a", 4, 32768},
		{"SWARM", "topology-a", 4, 32768},
		{"topology-a", "topology-a", 4, 32768},
		{"Topology-A", "topology-a", 4, 32768},
		{"4x32k", "topology-a", 4, 32768},
		{"deep", "topology-b", 2, 65536},
		{"deep-context", "topology-b", 2, 65536},
		{"DEEP-CONTEXT", "topology-b", 2, 65536},
		{"topology-b", "topology-b", 2, 65536},
		{"2x64k", "topology-b", 2, 65536},
	}

	for _, tc := range testCases {
		p, err := GetTopologyPreset(tc.query)
		if err != nil {
			t.Errorf("GetTopologyPreset(%q) returned unexpected error: %v", tc.query, err)
			continue
		}
		if p.ID != tc.expectedID {
			t.Errorf("GetTopologyPreset(%q) ID = %q, expected %q", tc.query, p.ID, tc.expectedID)
		}
		if p.Slots != tc.expectedNP {
			t.Errorf("GetTopologyPreset(%q) Slots = %d, expected %d", tc.query, p.Slots, tc.expectedNP)
		}
		if p.ContextPerSlot != tc.expectedCtx {
			t.Errorf("GetTopologyPreset(%q) ContextPerSlot = %d, expected %d", tc.query, p.ContextPerSlot, tc.expectedCtx)
		}
	}

	// Test error cases
	if _, err := GetTopologyPreset(""); err == nil {
		t.Errorf("expected error for empty preset name")
	}
	if _, err := GetTopologyPreset("   "); err == nil {
		t.Errorf("expected error for whitespace preset name")
	}
	if _, err := GetTopologyPreset("nonexistent-topology"); err == nil {
		t.Errorf("expected error for nonexistent preset")
	}
}

func TestTopologyPreset_Validate(t *testing.T) {
	valid := TopologyPreset{
		ID:             "test-id",
		Name:           "test-name",
		Slots:          4,
		ContextPerSlot: 1024,
		TotalContext:   4096,
		QuantKV:        "q8_0",
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid preset to pass: %v", err)
	}

	// Missing ID
	inv := valid
	inv.ID = ""
	if err := inv.Validate(); err == nil {
		t.Errorf("expected error on empty ID")
	}

	// Missing Name
	inv = valid
	inv.Name = ""
	if err := inv.Validate(); err == nil {
		t.Errorf("expected error on empty Name")
	}

	// Slots <= 0
	inv = valid
	inv.Slots = 0
	if err := inv.Validate(); err == nil {
		t.Errorf("expected error on slots <= 0")
	}

	// ContextPerSlot <= 0
	inv = valid
	inv.ContextPerSlot = 0
	if err := inv.Validate(); err == nil {
		t.Errorf("expected error on ContextPerSlot <= 0")
	}

	// TotalContext <= 0
	inv = valid
	inv.TotalContext = 0
	if err := inv.Validate(); err == nil {
		t.Errorf("expected error on TotalContext <= 0")
	}

	// Math mismatch
	inv = valid
	inv.TotalContext = 5000
	if err := inv.Validate(); err == nil {
		t.Errorf("expected error on TotalContext mismatch")
	}

	// Missing QuantKV
	inv = valid
	inv.QuantKV = ""
	if err := inv.Validate(); err == nil {
		t.Errorf("expected error on empty QuantKV")
	}
}

func TestTopologyPreset_LlamaServerArgs(t *testing.T) {
	swarm := TopologyPresetSwarm
	args := swarm.LlamaServerArgs()
	expectedArgs := []string{"-np", "4", "-c", "131072", "-ctk", "q8_0", "-ctv", "q8_0"}
	if len(args) != len(expectedArgs) {
		t.Fatalf("expected %d args, got %d", len(expectedArgs), len(args))
	}
	for i, arg := range args {
		if arg != expectedArgs[i] {
			t.Errorf("arg[%d] = %q, expected %q", i, arg, expectedArgs[i])
		}
	}

	argsStr := swarm.LlamaServerArgsString()
	if argsStr != "-np 4 -c 131072 -ctk q8_0 -ctv q8_0" {
		t.Errorf("unexpected args string: %s", argsStr)
	}

	cmd := swarm.ExampleCommand("", 0)
	if !strings.Contains(cmd, "llama-server -m /path/to/model.gguf") || !strings.Contains(cmd, "--port 11434") {
		t.Errorf("unexpected default example command: %s", cmd)
	}

	customCmd := swarm.ExampleCommand("/models/spark.gguf", 8080)
	if !strings.Contains(customCmd, "-m /models/spark.gguf") || !strings.Contains(customCmd, "--port 8080") {
		t.Errorf("unexpected custom example command: %s", customCmd)
	}
}

func TestTopologyPreset_Matching(t *testing.T) {
	// Match Swarm
	matchA := MatchTopologyPreset(4, 32768)
	if matchA == nil || matchA.ID != "topology-a" {
		t.Errorf("expected match for topology-a, got %+v", matchA)
	}

	// Match Deep
	matchB := MatchTopologyPreset(2, 65536)
	if matchB == nil || matchB.ID != "topology-b" {
		t.Errorf("expected match for topology-b, got %+v", matchB)
	}

	// Custom (no match)
	noMatch := MatchTopologyPreset(8, 16384)
	if noMatch != nil {
		t.Errorf("expected nil match for custom config, got %+v", noMatch)
	}
}

func TestTopologyPreset_Formatting(t *testing.T) {
	// FormatContextK
	if got := FormatContextK(32768); got != "32k" {
		t.Errorf("expected 32k, got %s", got)
	}
	if got := FormatContextK(65536); got != "64k" {
		t.Errorf("expected 64k, got %s", got)
	}
	if got := FormatContextK(1000); got != "1000" {
		t.Errorf("expected 1000, got %s", got)
	}
	if got := FormatContextK(0); got != "0" {
		t.Errorf("expected 0, got %s", got)
	}

	// FormatTopologyTable
	presets := GetTopologyPresets()
	table := FormatTopologyTable(presets)
	if !strings.Contains(table, "Available 128k KV Cache Topology Presets") {
		t.Errorf("expected header in table: %s", table)
	}
	if !strings.Contains(table, "swarm") || !strings.Contains(table, "deep-context") {
		t.Errorf("expected presets in table: %s", table)
	}

	// FormatTopologyDetail
	detail := FormatTopologyDetail(TopologyPresetSwarm)
	if !strings.Contains(detail, "Topology Preset: swarm (topology-a)") {
		t.Errorf("expected name in detail: %s", detail)
	}
	if !strings.Contains(detail, "-np 4 -c 131072 -ctk q8_0 -ctv q8_0") {
		t.Errorf("expected launch flags in detail: %s", detail)
	}
}
