// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package memory

import (
	"testing"
)

func TestTokenize(t *testing.T) {
	text := "Hello World! This is a test 123 -- tokenization, special-chars & case."
	tokens := Tokenize(text)

	expected := []string{"hello", "world", "this", "is", "a", "test", "123", "tokenization", "special", "chars", "case"}

	if len(tokens) != len(expected) {
		t.Fatalf("tokens count mismatch: got %d, want %d (%v)", len(tokens), len(expected), tokens)
	}

	for i, token := range tokens {
		if token != expected[i] {
			t.Errorf("token[%d] mismatch: got %q, want %q", i, token, expected[i])
		}
	}
}

func TestBM25IndexSearch(t *testing.T) {
	item1 := &MemoryItem{
		ID:            "1",
		Timestamp:     "2026-10-30T10:00:00Z",
		Pillar:        PillarEpisodic,
		Title:         "Valkey Backplane Configuration",
		Tags:          []string{"valkey", "backplane"},
		Tier1Abstract: "Setup Valkey pubsub messaging stream.",
		Tier2Summary:  "Detailed configuration for Valkey streams and ACL isolation.",
		Body:          "Valkey operates on port 6379.",
	}

	item2 := &MemoryItem{
		ID:            "2",
		Timestamp:     "2026-10-30T11:00:00Z",
		Pillar:        PillarSemantic,
		Title:         "Podman Container Isolation",
		Tags:          []string{"podman", "container"},
		Tier1Abstract: "Podman container runtime security boundaries.",
		Tier2Summary:  "Non-root containers run with unprivileged user UID 1000.",
		Body:          "Podman utilizes user namespaces.",
	}

	items := []*MemoryItem{item1, item2}
	idx := NewBM25Index()

	t.Run("Empty search query returns all items with score 0", func(t *testing.T) {
		results := idx.Search(items, "")
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		for _, r := range results {
			if r.Score != 0.0 {
				t.Errorf("expected score 0.0, got %f", r.Score)
			}
		}
	})

	t.Run("Specific term match ranks relevant document first", func(t *testing.T) {
		results := idx.Search(items, "Valkey backplane")
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		if results[0].Item.ID != "1" {
			t.Errorf("expected item 1 first, got item %s", results[0].Item.ID)
		}
		if results[0].Score <= 0 {
			t.Errorf("expected positive score for matching item, got %f", results[0].Score)
		}
	})

	t.Run("Podman term search ranks item 2 first", func(t *testing.T) {
		results := idx.Search(items, "Podman namespaces")
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		if results[0].Item.ID != "2" {
			t.Errorf("expected item 2 first, got item %s", results[0].Item.ID)
		}
	})

	t.Run("Empty items slice", func(t *testing.T) {
		results := idx.Search(nil, "test")
		if len(results) != 0 {
			t.Errorf("expected empty results for nil items, got %d", len(results))
		}
	})
}
