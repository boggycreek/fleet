// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package config

import (
	"fmt"
	"strings"
)

// TopologyPreset represents a pre-configured KV cache and slot allocation topology
// for local host-bridged LLM inference engines (such as llama-server).
type TopologyPreset struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Aliases        []string `json:"aliases"`
	Slots          int      `json:"slots"`
	ContextPerSlot int      `json:"context_per_slot"`
	TotalContext   int      `json:"total_context"`
	QuantKV        string   `json:"quant_kv"`
	TargetWorkload string   `json:"target_workload"`
	EstimatedVRAM  string   `json:"estimated_vram"`
	Description    string   `json:"description"`
}

// LlamaServerArgs returns the command-line arguments for llama-server
// corresponding to this topology preset.
func (t TopologyPreset) LlamaServerArgs() []string {
	return []string{
		"-np", fmt.Sprintf("%d", t.Slots),
		"-c", fmt.Sprintf("%d", t.TotalContext),
		"-ctk", t.QuantKV,
		"-ctv", t.QuantKV,
	}
}

// LlamaServerArgsString returns the launch arguments formatted as a single command-line string.
func (t TopologyPreset) LlamaServerArgsString() string {
	return strings.Join(t.LlamaServerArgs(), " ")
}

// ExampleCommand returns a complete llama-server launch command string with model path and port.
func (t TopologyPreset) ExampleCommand(modelPath string, port int) string {
	if modelPath == "" {
		modelPath = "/path/to/model.gguf"
	}
	if port <= 0 {
		port = 11434
	}
	return fmt.Sprintf("llama-server -m %s %s --port %d", modelPath, t.LlamaServerArgsString(), port)
}

// Validate checks the structural integrity and math of the topology preset.
func (t TopologyPreset) Validate() error {
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("topology preset ID cannot be empty")
	}
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("topology preset Name cannot be empty")
	}
	if t.Slots <= 0 {
		return fmt.Errorf("topology preset slots must be > 0, got %d", t.Slots)
	}
	if t.ContextPerSlot <= 0 {
		return fmt.Errorf("topology preset context per slot must be > 0, got %d", t.ContextPerSlot)
	}
	if t.TotalContext <= 0 {
		return fmt.Errorf("topology preset total context must be > 0, got %d", t.TotalContext)
	}
	if t.Slots*t.ContextPerSlot != t.TotalContext {
		return fmt.Errorf("topology preset slots (%d) * context_per_slot (%d) != total_context (%d)",
			t.Slots, t.ContextPerSlot, t.TotalContext)
	}
	if strings.TrimSpace(t.QuantKV) == "" {
		return fmt.Errorf("topology preset KV quantization cannot be empty")
	}
	return nil
}

// Well-known 128k KV cache topology presets defined in ADR 00055.
var (
	// TopologyPresetSwarm (Topology A) dedicates 4 slots @ 32k context each for concurrent multi-agent swarm tasks.
	TopologyPresetSwarm = TopologyPreset{
		ID:             "topology-a",
		Name:           "swarm",
		Aliases:        []string{"swarm", "topology-a", "4x32k"},
		Slots:          4,
		ContextPerSlot: 32768,
		TotalContext:   131072,
		QuantKV:        "q8_0",
		TargetWorkload: "Autonomous 4-agent swarm (Fuzzer, Auditor, Verifier, Orchestrator)",
		EstimatedVRAM:  "~6.4 GB (Spark-X2.5-4B Q4_K_M + Q8_0 KV)",
		Description:    "4 concurrent slots @ 32k context each. Eliminates HTTP 400 overflows during multi-agent collaboration.",
	}

	// TopologyPresetDeep (Topology B) dedicates 2 slots @ 64k context each for deep-context pair programming.
	TopologyPresetDeep = TopologyPreset{
		ID:             "topology-b",
		Name:           "deep-context",
		Aliases:        []string{"deep", "deep-context", "topology-b", "2x64k"},
		Slots:          2,
		ContextPerSlot: 65536,
		TotalContext:   131072,
		QuantKV:        "q8_0",
		TargetWorkload: "Deep pair programming & whole-package AST inspection (Fast-Reflex + Deep-Focus)",
		EstimatedVRAM:  "~6.4 GB (Spark-X2.5-4B Q4_K_M + Q8_0 KV)",
		Description:    "2 concurrent slots @ 64k context each. Maximizes context depth for large PR reviews and refactoring.",
	}
)

var defaultTopologies = []TopologyPreset{
	TopologyPresetSwarm,
	TopologyPresetDeep,
}

// GetTopologyPresets returns all available KV cache topology presets.
func GetTopologyPresets() []TopologyPreset {
	out := make([]TopologyPreset, len(defaultTopologies))
	copy(out, defaultTopologies)
	return out
}

// GetTopologyPreset retrieves a topology preset by ID, name, or alias (case-insensitive).
func GetTopologyPreset(name string) (TopologyPreset, error) {
	norm := strings.ToLower(strings.TrimSpace(name))
	if norm == "" {
		return TopologyPreset{}, fmt.Errorf("empty topology preset name")
	}

	for _, preset := range defaultTopologies {
		if strings.ToLower(preset.ID) == norm || strings.ToLower(preset.Name) == norm {
			return preset, nil
		}
		for _, alias := range preset.Aliases {
			if strings.ToLower(alias) == norm {
				return preset, nil
			}
		}
	}
	return TopologyPreset{}, fmt.Errorf("unknown topology preset %q (available: swarm/topology-a, deep-context/topology-b)", name)
}

// MatchTopologyPreset attempts to match active slots and context per slot against known presets.
// Returns matched preset or nil if custom.
func MatchTopologyPreset(slots int, contextPerSlot int) *TopologyPreset {
	for _, preset := range defaultTopologies {
		if preset.Slots == slots && preset.ContextPerSlot == contextPerSlot {
			p := preset
			return &p
		}
	}
	return nil
}

// FormatContextK returns a human-friendly context string like "32k" or "64k".
func FormatContextK(tokens int) string {
	if tokens > 0 && tokens%1024 == 0 {
		return fmt.Sprintf("%dk", tokens/1024)
	}
	return fmt.Sprintf("%d", tokens)
}

// FormatTopologyTable formats the list of presets as a readable ASCII table.
func FormatTopologyTable(presets []TopologyPreset) string {
	var sb strings.Builder
	sb.WriteString("Available 128k KV Cache Topology Presets (ADR 00055):\n\n")
	fmt.Fprintf(&sb, "%-14s %-18s %-6s %-14s %-10s %-10s %s\n",
		"PRESET", "ALIASES", "SLOTS", "CONTEXT/SLOT", "TOTAL KV", "EST. VRAM", "TARGET WORKLOAD")
	sb.WriteString(strings.Repeat("-", 100) + "\n")
	for _, p := range presets {
		aliases := strings.Join(p.Aliases, ",")
		ctxSlot := fmt.Sprintf("%s (%d)", FormatContextK(p.ContextPerSlot), p.ContextPerSlot)
		totalKV := FormatContextK(p.TotalContext)
		fmt.Fprintf(&sb, "%-14s %-18s %-6d %-14s %-10s %-10s %s\n",
			p.Name, aliases, p.Slots, ctxSlot, totalKV, "~6.4 GB", p.TargetWorkload)
	}
	return sb.String()
}

// FormatTopologyDetail formats full details for a single preset.
func FormatTopologyDetail(p TopologyPreset) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Topology Preset: %s (%s)\n", p.Name, p.ID)
	fmt.Fprintf(&sb, "  Aliases:            %s\n", strings.Join(p.Aliases, ", "))
	fmt.Fprintf(&sb, "  Concurrent Slots:   %d\n", p.Slots)
	fmt.Fprintf(&sb, "  Context Per Slot:   %s (%d tokens)\n", FormatContextK(p.ContextPerSlot), p.ContextPerSlot)
	fmt.Fprintf(&sb, "  Total KV Cache:     %s (%d tokens)\n", FormatContextK(p.TotalContext), p.TotalContext)
	fmt.Fprintf(&sb, "  KV Quantization:    %s (K) / %s (V)\n", p.QuantKV, p.QuantKV)
	fmt.Fprintf(&sb, "  Estimated VRAM:     %s\n", p.EstimatedVRAM)
	fmt.Fprintf(&sb, "  Target Workload:    %s\n", p.TargetWorkload)
	fmt.Fprintf(&sb, "  Description:        %s\n", p.Description)
	sb.WriteString("\n")
	sb.WriteString("  Launch Flags:\n")
	fmt.Fprintf(&sb, "    %s\n\n", p.LlamaServerArgsString())
	sb.WriteString("  Example Launch Command:\n")
	fmt.Fprintf(&sb, "    %s\n", p.ExampleCommand("/path/to/model.gguf", 11434))
	return sb.String()
}
