// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package engine

import (
	"context"
	"strings"
	"time"
)

const (
	// EpisodicMemoriesDirectivePrefix is the marker prefix identifying relevant episodic memories.
	EpisodicMemoriesDirectivePrefix = "=== RELEVANT EPISODIC MEMORIES & ARCHITECTURAL NOTES ==="

	// EpisodicMemoriesDirectiveSuffix is the marker suffix closing the episodic memory directive block.
	EpisodicMemoriesDirectiveSuffix = "=== END EPISODIC MEMORIES ==="

	// DefaultMemoryRetrievalLimit is the default maximum number of memories recalled for prompt assembly.
	DefaultMemoryRetrievalLimit = 5

	// DefaultMemoryRetrievalTimeout is the default deadline for querying memories before degrading gracefully.
	DefaultMemoryRetrievalTimeout = 2 * time.Second
)

// MemoryRetriever defines a clean seam for recalling episodic memories and architectural notes.
type MemoryRetriever interface {
	SearchMemories(ctx context.Context, query string, limit int) ([]string, error)
}

// EpisodicMemoryRetriever is an alias for MemoryRetriever for semantic domain clarity.
type EpisodicMemoryRetriever = MemoryRetriever

// FormatEpisodicMemories formats a slice of retrieved memory strings into a structured directive block.
// If memories is empty or contains only blank entries, an empty string is returned.
func FormatEpisodicMemories(memories []string) string {
	if len(memories) == 0 {
		return ""
	}

	var valid []string
	for _, m := range memories {
		trimmed := strings.TrimSpace(m)
		if trimmed != "" {
			valid = append(valid, trimmed)
		}
	}
	if len(valid) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(EpisodicMemoriesDirectivePrefix)
	sb.WriteString("\n")
	for _, m := range valid {
		if strings.HasPrefix(m, "- ") {
			sb.WriteString(m)
		} else if strings.HasPrefix(m, "* ") {
			sb.WriteString("- ")
			sb.WriteString(strings.TrimSpace(m[2:]))
		} else {
			sb.WriteString("- ")
			sb.WriteString(m)
		}
		sb.WriteString("\n")
	}
	sb.WriteString(EpisodicMemoriesDirectiveSuffix)
	return sb.String()
}
