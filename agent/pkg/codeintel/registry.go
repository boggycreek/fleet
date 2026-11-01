// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package codeintel

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// Registry manages code intelligence providers and routes file requests.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]CodeIntelligenceProvider
	fallback  CodeIntelligenceProvider
}

// NewRegistry creates an empty CodeIntelligenceProvider registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]CodeIntelligenceProvider),
	}
}

// Register adds a provider to the registry. If the provider supports "*", it is registered as fallback.
func (r *Registry) Register(p CodeIntelligenceProvider) {
	if p == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, lang := range p.Languages() {
		norm := strings.ToLower(strings.TrimSpace(lang))
		if norm == "*" {
			r.fallback = p
			continue
		}
		r.providers[norm] = p
		if strings.HasPrefix(norm, ".") {
			r.providers[strings.TrimPrefix(norm, ".")] = p
		} else {
			r.providers["."+norm] = p
		}
	}
}

// GetProvider resolves the most specific provider for the given file path, falling back if needed.
func (r *Registry) GetProvider(filePath string) (CodeIntelligenceProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != "" {
		if p, ok := r.providers[ext]; ok {
			return p, true
		}
	}

	base := strings.ToLower(filepath.Base(filePath))
	if base != "" {
		if p, ok := r.providers[base]; ok {
			return p, true
		}
	}

	if r.fallback != nil {
		return r.fallback, true
	}

	return nil, false
}

// GetOutline delegates outline generation to the matched provider.
func (r *Registry) GetOutline(ctx context.Context, filePath string, content []byte) (*Outline, error) {
	provider, ok := r.GetProvider(filePath)
	if !ok {
		return nil, fmt.Errorf("no code intelligence provider registered for %s", filePath)
	}
	return provider.GetOutline(ctx, filePath, content)
}

// ValidateSyntax runs syntax validation through the matched provider.
// If no provider is available, it gracefully returns an empty diagnostic slice.
func (r *Registry) ValidateSyntax(ctx context.Context, filePath string, content []byte) ([]Diagnostic, error) {
	provider, ok := r.GetProvider(filePath)
	if !ok {
		return []Diagnostic{}, nil
	}
	return provider.ValidateSyntax(ctx, filePath, content)
}

// FindDefinition resolves definitions via the matched provider.
func (r *Registry) FindDefinition(ctx context.Context, filePath string, line, col int) ([]Location, error) {
	provider, ok := r.GetProvider(filePath)
	if !ok {
		return []Location{}, nil
	}
	return provider.FindDefinition(ctx, filePath, line, col)
}

// FindReferences resolves references via the matched provider.
func (r *Registry) FindReferences(ctx context.Context, filePath string, line, col int) ([]Location, error) {
	provider, ok := r.GetProvider(filePath)
	if !ok {
		return []Location{}, nil
	}
	return provider.FindReferences(ctx, filePath, line, col)
}
