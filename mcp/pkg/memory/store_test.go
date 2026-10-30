// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package memory

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestStoreSaveAndGet(t *testing.T) {
	tempDir := t.TempDir()

	store, err := NewStore(StoreConfig{RootDir: tempDir})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	item := &MemoryItem{
		Pillar:        PillarEpisodic,
		Title:         "Setup Fleet Agent Environment",
		Tags:          []string{"setup", "agent"},
		Tier1Abstract: "Configured local agent harness container.",
		Tier2Summary:  "Verified Podman network isolation and SSH port forwarding.",
		Body:          "Detailed setup notes for agent environment creation.",
	}

	saved, err := store.Save(item)
	if err != nil {
		t.Fatalf("unexpected error saving memory: %v", err)
	}

	if saved.ID == "" {
		t.Errorf("expected generated ID, got empty string")
	}
	if saved.Timestamp == "" {
		t.Errorf("expected generated timestamp, got empty string")
	}
	if !strings.HasPrefix(saved.Path, "episodic/") {
		t.Errorf("expected relative path to start with 'episodic/', got %q", saved.Path)
	}

	// Retrieve at Tier 3
	gotTier3, err := store.Get(saved.ID, 3)
	if err != nil {
		t.Fatalf("unexpected error getting memory: %v", err)
	}
	if gotTier3.Body != item.Body {
		t.Errorf("Tier 3 body mismatch: got %q, want %q", gotTier3.Body, item.Body)
	}

	// Retrieve at Tier 2 (body should be stripped)
	gotTier2, err := store.Get(saved.ID, 2)
	if err != nil {
		t.Fatalf("unexpected error getting memory at tier 2: %v", err)
	}
	if gotTier2.Body != "" {
		t.Errorf("expected empty body for Tier 2 disclosure, got %q", gotTier2.Body)
	}
	if gotTier2.Tier2Summary != item.Tier2Summary {
		t.Errorf("Tier 2 summary mismatch: got %q, want %q", gotTier2.Tier2Summary, item.Tier2Summary)
	}

	// Retrieve at Tier 1 (summary and body should be stripped)
	gotTier1, err := store.Get(saved.Path, 1)
	if err != nil {
		t.Fatalf("unexpected error getting memory by path at tier 1: %v", err)
	}
	if gotTier1.Tier2Summary != "" || gotTier1.Body != "" {
		t.Errorf("expected empty summary and body for Tier 1, got summary %q, body %q", gotTier1.Tier2Summary, gotTier1.Body)
	}
}

func TestStorePersistenceAndReopen(t *testing.T) {
	tempDir := t.TempDir()

	store1, err := NewStore(StoreConfig{RootDir: tempDir})
	if err != nil {
		t.Fatalf("failed to create store1: %v", err)
	}

	item := &MemoryItem{
		ID:            "persistent-mem-1",
		Timestamp:     "2026-10-30T15:00:00Z",
		Pillar:        PillarSemantic,
		Title:         "Persistent Cognitive Memory",
		Tier1Abstract: "Memory persists across container restarts.",
		Tier2Summary:  "Persisted under /home/agent/.memory named volume.",
		Body:          "Full markdown document content.",
	}

	_, err = store1.Save(item)
	if err != nil {
		t.Fatalf("failed to save item: %v", err)
	}

	// Reopen store from same directory
	store2, err := NewStore(StoreConfig{RootDir: tempDir})
	if err != nil {
		t.Fatalf("failed to reopen store: %v", err)
	}

	reloaded, err := store2.Get("persistent-mem-1", 3)
	if err != nil {
		t.Fatalf("failed to reload item from disk: %v", err)
	}

	if reloaded.Title != item.Title {
		t.Errorf("reloaded title mismatch: got %q, want %q", reloaded.Title, item.Title)
	}
}

func TestStoreListAndRecall(t *testing.T) {
	tempDir := t.TempDir()
	store, _ := NewStore(StoreConfig{RootDir: tempDir})

	mem1 := &MemoryItem{
		ID:            "m1",
		Timestamp:     "2026-10-30T10:00:00Z",
		Pillar:        PillarEpisodic,
		Title:         "Fix Backplane Connection Error",
		Tags:          []string{"bugfix", "backplane"},
		Tier1Abstract: "Resolved socket connection drop in backplane client.",
		Tier2Summary:  "Added automatic exponential backoff retry in libbp.",
		Body:          "Details of backplane retry logic.",
	}

	mem2 := &MemoryItem{
		ID:            "m2",
		Timestamp:     "2026-10-30T11:00:00Z",
		Pillar:        PillarProcedural,
		Title:         "Build OCI Image Presets",
		Tags:          []string{"oci", "build"},
		Tier1Abstract: "Standardized multi-stage Containerfile for fleet images.",
		Tier2Summary:  "Optimized image layer caching and size reduction.",
		Body:          "Details of Podman image builds.",
	}

	_, _ = store.Save(mem1)
	_, _ = store.Save(mem2)

	t.Run("List with pillar filter", func(t *testing.T) {
		listed := store.List("procedural", "", 0)
		if len(listed) != 1 {
			t.Fatalf("expected 1 procedural item, got %d", len(listed))
		}
		if listed[0].ID != "m2" {
			t.Errorf("expected m2, got %s", listed[0].ID)
		}
	})

	t.Run("List with tag filter", func(t *testing.T) {
		listed := store.List("", "bugfix", 0)
		if len(listed) != 1 {
			t.Fatalf("expected 1 item with tag bugfix, got %d", len(listed))
		}
		if listed[0].ID != "m1" {
			t.Errorf("expected m1, got %s", listed[0].ID)
		}
	})

	t.Run("Recall matching query", func(t *testing.T) {
		recalled := store.Recall("backplane retry", "", 3, 5)
		if len(recalled) == 0 {
			t.Fatalf("expected matches for 'backplane retry', got 0")
		}
		if recalled[0].ID != "m1" {
			t.Errorf("expected top result m1, got %s", recalled[0].ID)
		}
	})
}

func TestStoreValidationAndErrors(t *testing.T) {
	tempDir := t.TempDir()
	store, _ := NewStore(StoreConfig{RootDir: tempDir})

	t.Run("Save nil item returns error", func(t *testing.T) {
		_, err := store.Save(nil)
		if err == nil {
			t.Errorf("expected error when saving nil item, got nil")
		}
	})

	t.Run("Save invalid pillar returns error", func(t *testing.T) {
		_, err := store.Save(&MemoryItem{Pillar: "invalid_pillar"})
		if err == nil {
			t.Errorf("expected error for invalid pillar, got nil")
		}
	})

	t.Run("Get empty key returns error", func(t *testing.T) {
		_, err := store.Get("", 3)
		if err == nil {
			t.Errorf("expected error for empty key, got nil")
		}
	})

	t.Run("Get non-existent memory returns error", func(t *testing.T) {
		_, err := store.Get("non-existent-id", 3)
		if err == nil {
			t.Errorf("expected error for non-existent memory, got nil")
		}
	})
}

func TestStoreConcurrentAccess(t *testing.T) {
	tempDir := t.TempDir()
	store, _ := NewStore(StoreConfig{RootDir: tempDir})

	var wg sync.WaitGroup
	workers := 10

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			item := &MemoryItem{
				Pillar:        PillarEpisodic,
				Title:         "Concurrent Task",
				Tier1Abstract: "Worker memory abstract",
				Tier2Summary:  "Worker summary details",
				Body:          "Worker body",
			}
			saved, err := store.Save(item)
			if err != nil {
				t.Errorf("concurrent save error: %v", err)
				return
			}
			_, _ = store.Get(saved.ID, 3)
			_ = store.List("episodic", "", 10)
			_ = store.Recall("Worker", "episodic", 2, 5)
		}(i)
	}

	wg.Wait()
}

func TestToKebabCase(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello World!", "hello-world"},
		{"  Implement MEMORY-mcp Server  ", "implement-memory-mcp-server"},
		{"Special!@#$%^&*()Chars", "special-chars"},
		{"", ""},
	}

	for _, tc := range tests {
		got := ToKebabCase(tc.input)
		if got != tc.expected {
			t.Errorf("ToKebabCase(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestNewStoreEnvDirFallback(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("MEMORY_DIR", filepath.Join(tempDir, "env_memory"))

	store, err := NewStore(StoreConfig{})
	if err != nil {
		t.Fatalf("failed to create store with MEMORY_DIR: %v", err)
	}

	if store.RootDir() != filepath.Join(tempDir, "env_memory") {
		t.Errorf("expected store rootDir %q, got %q", filepath.Join(tempDir, "env_memory"), store.RootDir())
	}
}
