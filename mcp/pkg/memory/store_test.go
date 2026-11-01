// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package memory

import (
	"os"
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

func TestNewStoreDirectoryErrorsAndCorruptedFiles(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("NewStore fails when rootDir is a file", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "a-regular-file")
		if err := os.WriteFile(filePath, []byte("plain text"), 0644); err != nil {
			t.Fatalf("failed to create dummy file: %v", err)
		}

		// Trying to create a directory where a file exists will cause MkdirAll to fail
		subDirPath := filepath.Join(filePath, "subfolder")
		_, err := NewStore(StoreConfig{RootDir: subDirPath})
		if err == nil {
			t.Errorf("expected error initializing store inside a regular file path, got nil")
		}
	})

	t.Run("loadExisting handles corrupted and non-memory markdown files gracefully", func(t *testing.T) {
		storeDir := filepath.Join(tempDir, "corrupted_store")
		if err := os.MkdirAll(filepath.Join(storeDir, "episodic"), 0755); err != nil {
			t.Fatalf("failed to create storeDir: %v", err)
		}

		// Write a valid memory file
		validItem := &MemoryItem{
			ID:            "valid-1",
			Pillar:        "episodic",
			Title:         "Valid Item",
			Tier1Abstract: "Valid abstract",
		}
		formatted := FormatDocument(validItem)
		if err := os.WriteFile(filepath.Join(storeDir, "episodic", "2026-10-30-valid-item.md"), []byte(formatted), 0644); err != nil {
			t.Fatalf("failed to write valid item: %v", err)
		}

		// Write an invalid markdown file without valid frontmatter
		if err := os.WriteFile(filepath.Join(storeDir, "episodic", "non-memory.md"), []byte("# Just a random note"), 0644); err != nil {
			t.Fatalf("failed to write non-memory note: %v", err)
		}

		// Write a non-markdown file (.txt)
		if err := os.WriteFile(filepath.Join(storeDir, "notes.txt"), []byte("random text"), 0644); err != nil {
			t.Fatalf("failed to write notes.txt: %v", err)
		}

		store, err := NewStore(StoreConfig{RootDir: storeDir})
		if err != nil {
			t.Fatalf("expected NewStore to ignore invalid markdown files gracefully, got: %v", err)
		}

		item, err := store.Get("valid-1", 1)
		if err != nil || item == nil {
			t.Fatalf("expected to retrieve valid-1, got err: %v", err)
		}
	})

	t.Run("Save fails when pillar directory cannot be created", func(t *testing.T) {
		storeDir := filepath.Join(tempDir, "pillar_fail_store")
		if err := os.MkdirAll(storeDir, 0755); err != nil {
			t.Fatalf("failed to create storeDir: %v", err)
		}

		// Create a file named 'episodic' so MkdirAll fails
		pillarFilePath := filepath.Join(storeDir, "episodic")
		if err := os.WriteFile(pillarFilePath, []byte("file blocker"), 0644); err != nil {
			t.Fatalf("failed to create pillar blocking file: %v", err)
		}

		store, err := NewStore(StoreConfig{RootDir: storeDir})
		if err != nil {
			t.Fatalf("failed to initialize store: %v", err)
		}

		_, err = store.Save(&MemoryItem{
			Pillar:        "episodic",
			Title:         "Should Fail",
			Tier1Abstract: "Test",
		})
		if err == nil {
			t.Errorf("expected error when pillar directory is blocked by a file, got nil")
		}
	})

	t.Run("Save fails when file write fails", func(t *testing.T) {
		storeDir := filepath.Join(tempDir, "write_fail_store")
		store, err := NewStore(StoreConfig{RootDir: storeDir})
		if err != nil {
			t.Fatalf("failed to initialize store: %v", err)
		}

		// Pre-create a directory with the exact memory file name
		item := &MemoryItem{
			Pillar:        "semantic",
			Timestamp:     "2026-10-30T10:00:00Z",
			Title:         "Directory Blocker",
			Tier1Abstract: "Abstract",
		}
		targetFileName := "2026-10-30-directory-blocker.md"
		targetFilePath := filepath.Join(storeDir, "semantic", targetFileName)
		if err := os.MkdirAll(targetFilePath, 0755); err != nil {
			t.Fatalf("failed to create directory blocker: %v", err)
		}

		_, err = store.Save(item)
		if err == nil {
			t.Errorf("expected error when file cannot be written (directory exists with same name), got nil")
		}
	})
}

func TestStoreEdgeCases(t *testing.T) {
	t.Run("cloneItem nil returns nil", func(t *testing.T) {
		if got := cloneItem(nil, 3); got != nil {
			t.Errorf("expected nil for cloneItem(nil), got %+v", got)
		}
	})

	t.Run("Get with clean path fallback", func(t *testing.T) {
		tempDir := t.TempDir()
		store, err := NewStore(StoreConfig{RootDir: tempDir})
		if err != nil {
			t.Fatalf("failed to create store: %v", err)
		}

		saved, err := store.Save(&MemoryItem{
			Pillar:        "episodic",
			Title:         "Path Cleaning Test",
			Tier1Abstract: "Test",
		})
		if err != nil {
			t.Fatalf("failed to save: %v", err)
		}

		// Retrieve with uncleaned relative path (e.g. episodic/./filename)
		uncleanPath := filepath.Join("episodic", ".", filepath.Base(saved.Path))
		got, err := store.Get(uncleanPath, 1)
		if err != nil {
			t.Fatalf("expected Get with unclean path to succeed via Clean: %v", err)
		}
		if got.ID != saved.ID {
			t.Errorf("expected ID %q, got %q", saved.ID, got.ID)
		}
	})

	t.Run("Save with fallback timestamp on parse error and title fallback", func(t *testing.T) {
		tempDir := t.TempDir()
		store, _ := NewStore(StoreConfig{RootDir: tempDir})

		saved, err := store.Save(&MemoryItem{
			Pillar:        "episodic",
			Timestamp:     "not-a-valid-timestamp",
			Title:         "",
			Tier1Abstract: "Fallback Abstract",
		})
		if err != nil {
			t.Fatalf("unexpected save error: %v", err)
		}
		if saved.Title != "Fallback Abstract" {
			t.Errorf("expected title to fall back to Tier1Abstract, got %q", saved.Title)
		}
		if saved.Timestamp == "not-a-valid-timestamp" {
			t.Errorf("expected timestamp to fall back to current RFC3339 time, got %q", saved.Timestamp)
		}
	})
}
