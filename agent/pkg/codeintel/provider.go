// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package codeintel

import "context"

// CodeIntelligenceProvider is the pluggable driver contract for AST, LSP, and regex intelligence.
type CodeIntelligenceProvider interface {
	// Name returns the identifier of the provider (e.g. "native-go", "universal-fallback").
	Name() string

	// Languages returns file extensions (e.g. ".go", "go") or wildcard "*" supported by this provider.
	Languages() []string

	// GetOutline extracts a hierarchical symbol outline from the file content or file path.
	GetOutline(ctx context.Context, filePath string, content []byte) (*Outline, error)

	// FindDefinition locates symbol definitions matching coordinates (1-based line and column).
	FindDefinition(ctx context.Context, filePath string, line, col int) ([]Location, error)

	// FindReferences finds symbol usage occurrences matching coordinates (1-based line and column).
	FindReferences(ctx context.Context, filePath string, line, col int) ([]Location, error)

	// ValidateSyntax parses the content and checks for language syntax errors.
	ValidateSyntax(ctx context.Context, filePath string, content []byte) ([]Diagnostic, error)
}
