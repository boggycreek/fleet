// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package memory

import "strings"

const (
	PillarEpisodic    = "episodic"
	PillarSemantic    = "semantic"
	PillarProcedural  = "procedural"
	PillarProspective = "prospective"
	PillarEvaluative  = "evaluative"
)

// ValidPillars lists all valid cognitive memory classification pillars.
var ValidPillars = map[string]bool{
	PillarEpisodic:    true,
	PillarSemantic:    true,
	PillarProcedural:  true,
	PillarProspective: true,
	PillarEvaluative:  true,
}

// IsValidPillar returns true if the given string is a supported pillar.
func IsValidPillar(p string) bool {
	return ValidPillars[strings.ToLower(strings.TrimSpace(p))]
}

// MemoryItem represents a cognitive memory document with YAML frontmatter metadata and body.
type MemoryItem struct {
	ID            string   `json:"id"`
	Timestamp     string   `json:"timestamp"`
	Pillar        string   `json:"pillar"`
	Tags          []string `json:"tags"`
	Tier1Abstract string   `json:"tier1_abstract"`
	Tier2Summary  string   `json:"tier2_summary"`
	Title         string   `json:"title,omitempty"`
	Path          string   `json:"path,omitempty"`
	Body          string   `json:"body,omitempty"`
}

// StoreConfig defines configuration options for the memory store.
type StoreConfig struct {
	RootDir string
}
