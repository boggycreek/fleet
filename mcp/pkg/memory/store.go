// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package memory

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var nonAlphanumericRegex = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// Store manages long-term cognitive memory files on disk and provides in-memory indexing.
type Store struct {
	rootDir   string
	mu        sync.RWMutex
	itemsByID map[string]*MemoryItem
	itemsByPath map[string]*MemoryItem
	index     *BM25Index
}

// NewStore initializes a new Store instance using the given StoreConfig.
// If RootDir is empty, it checks $MEMORY_DIR before defaulting to /home/agent/.memory.
func NewStore(cfg StoreConfig) (*Store, error) {
	rootDir := cfg.RootDir
	if rootDir == "" {
		rootDir = os.Getenv("MEMORY_DIR")
	}
	if rootDir == "" {
		rootDir = "/home/agent/.memory"
	}

	if err := os.MkdirAll(rootDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create memory root directory %s: %w", rootDir, err)
	}

	s := &Store{
		rootDir:     rootDir,
		itemsByID:   make(map[string]*MemoryItem),
		itemsByPath: make(map[string]*MemoryItem),
		index:       NewBM25Index(),
	}

	if err := s.loadExisting(); err != nil {
		return nil, err
	}

	return s, nil
}

// RootDir returns the root storage directory path.
func (s *Store) RootDir() string {
	return s.rootDir
}

func (s *Store) loadExisting() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return filepath.Walk(s.rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil // best-effort skip
		}

		item, err := ParseDocument(string(data))
		if err != nil {
			return nil // skip non-memory markdown files
		}

		relPath, err := filepath.Rel(s.rootDir, path)
		if err != nil {
			relPath = path
		}
		item.Path = relPath

		if item.ID != "" {
			s.itemsByID[item.ID] = item
		}
		s.itemsByPath[relPath] = item
		s.itemsByPath[path] = item
		return nil
	})
}

// Save persists a MemoryItem to disk and updates the in-memory index.
func (s *Store) Save(item *MemoryItem) (*MemoryItem, error) {
	if item == nil {
		return nil, fmt.Errorf("cannot save nil memory item")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	pillar := strings.ToLower(strings.TrimSpace(item.Pillar))
	if pillar == "" {
		pillar = PillarEpisodic
	}
	if !IsValidPillar(pillar) {
		return nil, fmt.Errorf("invalid pillar %q: must be one of episodic, semantic, procedural, prospective, evaluative", item.Pillar)
	}

	timestamp := strings.TrimSpace(item.Timestamp)
	var t time.Time
	var err error
	if timestamp == "" {
		t = time.Now().UTC()
		timestamp = t.Format(time.RFC3339)
	} else {
		t, err = time.Parse(time.RFC3339, timestamp)
		if err != nil {
			// Fall back to current time if parsing fails
			t = time.Now().UTC()
			timestamp = t.Format(time.RFC3339)
		}
	}

	id := strings.TrimSpace(item.ID)
	if id == "" {
		id = generateID()
	}

	title := strings.TrimSpace(item.Title)
	if title == "" && item.Tier1Abstract != "" {
		title = item.Tier1Abstract
	}

	kebabTitle := ToKebabCase(title)
	if kebabTitle == "" {
		kebabTitle = fmt.Sprintf("memory-%s", id)
	}

	dateStr := t.Format("2006-01-02")
	fileName := fmt.Sprintf("%s-%s.md", dateStr, kebabTitle)
	pillarDir := filepath.Join(s.rootDir, pillar)

	if err := os.MkdirAll(pillarDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create pillar directory %s: %w", pillarDir, err)
	}

	relPath := filepath.Join(pillar, fileName)
	fullPath := filepath.Join(s.rootDir, relPath)

	savedItem := &MemoryItem{
		ID:            id,
		Timestamp:     timestamp,
		Pillar:        pillar,
		Tags:          item.Tags,
		Tier1Abstract: item.Tier1Abstract,
		Tier2Summary:  item.Tier2Summary,
		Title:         title,
		Path:          relPath,
		Body:          item.Body,
	}

	docContent := FormatDocument(savedItem)
	if err := os.WriteFile(fullPath, []byte(docContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write memory file %s: %w", fullPath, err)
	}

	s.itemsByID[id] = savedItem
	s.itemsByPath[relPath] = savedItem
	s.itemsByPath[fullPath] = savedItem

	return cloneItem(savedItem, 3), nil
}

// Get retrieves a specific memory document by id or file path at specified tier (1, 2, or 3).
func (s *Store) Get(idOrPath string, maxTier int) (*MemoryItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	idOrPath = strings.TrimSpace(idOrPath)
	if idOrPath == "" {
		return nil, fmt.Errorf("memory id or path is required")
	}

	if item, exists := s.itemsByID[idOrPath]; exists {
		return cloneItem(item, maxTier), nil
	}

	if item, exists := s.itemsByPath[idOrPath]; exists {
		return cloneItem(item, maxTier), nil
	}

	// Try relative or cleaned path
	cleanPath := filepath.Clean(idOrPath)
	if item, exists := s.itemsByPath[cleanPath]; exists {
		return cloneItem(item, maxTier), nil
	}

	return nil, fmt.Errorf("memory item not found for key %q", idOrPath)
}

// List returns memory items filtered by optional pillar and tag with Tier 1 abstracts.
func (s *Store) List(pillar, tag string, limit int) []*MemoryItem {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pillar = strings.ToLower(strings.TrimSpace(pillar))
	tag = strings.ToLower(strings.TrimSpace(tag))

	var matches []*MemoryItem
	for _, item := range s.itemsByID {
		if pillar != "" && strings.ToLower(item.Pillar) != pillar {
			continue
		}
		if tag != "" {
			hasTag := false
			for _, t := range item.Tags {
				if strings.ToLower(t) == tag {
					hasTag = true
					break
				}
			}
			if !hasTag {
				continue
			}
		}
		matches = append(matches, item)
	}

	// Sort by timestamp descending
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].Timestamp > matches[j].Timestamp
	})

	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}

	results := make([]*MemoryItem, len(matches))
	for i, m := range matches {
		results[i] = cloneItem(m, 1)
	}

	return results
}

// Recall queries memories by text query, optional pillar filter, and maximum tier disclosure.
func (s *Store) Recall(query, pillar string, maxTier int, limit int) []*MemoryItem {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pillar = strings.ToLower(strings.TrimSpace(pillar))

	var candidates []*MemoryItem
	for _, item := range s.itemsByID {
		if pillar != "" && strings.ToLower(item.Pillar) != pillar {
			continue
		}
		candidates = append(candidates, item)
	}

	searchRes := s.index.Search(candidates, query)

	if limit <= 0 {
		limit = 10
	}

	var matched []*MemoryItem
	for _, sr := range searchRes {
		if strings.TrimSpace(query) != "" && sr.Score <= 0 {
			continue
		}
		matched = append(matched, sr.Item)
		if len(matched) >= limit {
			break
		}
	}

	results := make([]*MemoryItem, len(matched))
	for i, item := range matched {
		results[i] = cloneItem(item, maxTier)
	}

	return results
}

func cloneItem(item *MemoryItem, maxTier int) *MemoryItem {
	if item == nil {
		return nil
	}
	if maxTier <= 0 {
		maxTier = 3
	}

	tagsCopy := make([]string, len(item.Tags))
	copy(tagsCopy, item.Tags)

	newItem := &MemoryItem{
		ID:            item.ID,
		Timestamp:     item.Timestamp,
		Pillar:        item.Pillar,
		Tags:          tagsCopy,
		Tier1Abstract: item.Tier1Abstract,
		Title:         item.Title,
		Path:          item.Path,
	}

	if maxTier >= 2 {
		newItem.Tier2Summary = item.Tier2Summary
	}
	if maxTier >= 3 {
		newItem.Body = item.Body
	}

	return newItem
}

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("mem-%s", hex.EncodeToString(b))
}

// ToKebabCase converts a title string into a kebab-case slug.
func ToKebabCase(s string) string {
	s = strings.TrimSpace(s)
	s = nonAlphanumericRegex.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return strings.ToLower(s)
}
