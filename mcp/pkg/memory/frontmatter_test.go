// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package memory

import (
	"strings"
	"testing"
)

func TestParseAndFormatFrontmatter(t *testing.T) {
	item := &MemoryItem{
		ID:            "mem-100",
		Timestamp:     "2026-10-30T12:00:00Z",
		Pillar:        PillarEpisodic,
		Title:         "Refactored MCP Server Architecture",
		Tags:          []string{"mcp", "architecture", "refactor"},
		Tier1Abstract: "Updated MCP server handlers for long-term memory.",
		Tier2Summary:  "Added BM25 search ranking and multi-tier disclosure support across episodic memory files.",
		Body:          "# Memory Notes\n\nFull description of the memory item.",
	}

	formatted := FormatDocument(item)
	if !strings.HasPrefix(formatted, "---\n") {
		t.Fatalf("expected formatted document to start with '---\\n', got:\n%s", formatted)
	}

	parsed, err := ParseDocument(formatted)
	if err != nil {
		t.Fatalf("unexpected error parsing formatted document: %v", err)
	}

	if parsed.ID != item.ID {
		t.Errorf("ID mismatch: got %q, want %q", parsed.ID, item.ID)
	}
	if parsed.Pillar != item.Pillar {
		t.Errorf("Pillar mismatch: got %q, want %q", parsed.Pillar, item.Pillar)
	}
	if parsed.Title != item.Title {
		t.Errorf("Title mismatch: got %q, want %q", parsed.Title, item.Title)
	}
	if len(parsed.Tags) != len(item.Tags) {
		t.Fatalf("Tags count mismatch: got %d, want %d", len(parsed.Tags), len(item.Tags))
	}
	for i, tag := range parsed.Tags {
		if tag != item.Tags[i] {
			t.Errorf("Tag[%d] mismatch: got %q, want %q", i, tag, item.Tags[i])
		}
	}
	if parsed.Tier1Abstract != item.Tier1Abstract {
		t.Errorf("Tier1Abstract mismatch: got %q, want %q", parsed.Tier1Abstract, item.Tier1Abstract)
	}
	if parsed.Tier2Summary != item.Tier2Summary {
		t.Errorf("Tier2Summary mismatch: got %q, want %q", parsed.Tier2Summary, item.Tier2Summary)
	}
	if parsed.Body != item.Body {
		t.Errorf("Body mismatch: got %q, want %q", parsed.Body, item.Body)
	}
}

func TestParseDocumentInvalid(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"empty string", ""},
		{"no frontmatter boundary", "Hello world\nno YAML here"},
		{"unclosed frontmatter", "---\nid: test\ntitle: test\nno closing"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDocument(tc.content)
			if err == nil {
				t.Errorf("expected error for invalid frontmatter, got nil")
			}
		})
	}
}

func TestParseDocumentInlineTagsAndMultiline(t *testing.T) {
	content := `---
id: "mem-200"
timestamp: "2026-10-30T10:00:00Z"
pillar: "semantic"
title: "Inline and Multiline Test"
tags: [go, unit-test, "quoted-tag"]
tier1_abstract: Tier 1 abstract multiline
 continuation text line
tier2_summary: "Tier 2 summary"
---

Body content here
`

	item, err := ParseDocument(content)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	if len(item.Tags) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(item.Tags))
	}
	if item.Tags[2] != "quoted-tag" {
		t.Errorf("expected quoted-tag, got %q", item.Tags[2])
	}
	if !strings.Contains(item.Tier1Abstract, "continuation text line") {
		t.Errorf("expected multiline abstract to contain continuation, got %q", item.Tier1Abstract)
	}
}

func TestParseDocumentEmptyTagsAndQuotes(t *testing.T) {
	content := `---
id: 'mem-300'
pillar: 'procedural'
title: Simple
tags: []
tier1_abstract: Abstract
---
`
	item, err := ParseDocument(content)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if item.ID != "mem-300" {
		t.Errorf("expected ID mem-300, got %q", item.ID)
	}
	if len(item.Tags) != 0 {
		t.Errorf("expected empty tags, got %v", item.Tags)
	}
}
