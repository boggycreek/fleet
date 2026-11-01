// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package codeintel

import (
	"fmt"
	"strings"
)

// SymbolKind represents the classification of a code symbol.
type SymbolKind string

const (
	SymbolKindPackage   SymbolKind = "package"
	SymbolKindStruct    SymbolKind = "struct"
	SymbolKindInterface SymbolKind = "interface"
	SymbolKindFunction  SymbolKind = "function"
	SymbolKindMethod    SymbolKind = "method"
	SymbolKindConstant  SymbolKind = "constant"
	SymbolKindVariable  SymbolKind = "variable"
	SymbolKindType      SymbolKind = "type"
)

// SymbolNode represents a node in the hierarchical symbol outline tree.
type SymbolNode struct {
	Name      string       `json:"name"`
	Kind      SymbolKind   `json:"kind"`
	Signature string       `json:"signature,omitempty"`
	Doc       string       `json:"doc,omitempty"`
	Line      int          `json:"line"`
	Col       int          `json:"col"`
	EndLine   int          `json:"end_line"`
	EndCol    int          `json:"end_col"`
	Children  []SymbolNode `json:"children,omitempty"`
}

// Outline represents the structural summary of a source file.
type Outline struct {
	FilePath    string       `json:"file_path"`
	Language    string       `json:"language"`
	PackageName string       `json:"package_name,omitempty"`
	Symbols     []SymbolNode `json:"symbols"`
}

// Location represents a specific coordinate range within a source file.
type Location struct {
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	EndLine  int    `json:"end_line"`
	EndCol   int    `json:"end_col"`
}

// DiagnosticSeverity indicates the severity level of a diagnostic issue.
type DiagnosticSeverity string

const (
	SeverityError   DiagnosticSeverity = "error"
	SeverityWarning DiagnosticSeverity = "warning"
	SeverityInfo    DiagnosticSeverity = "info"
)

// Diagnostic represents a syntax error, warning, or linting issue.
type Diagnostic struct {
	Message  string             `json:"message"`
	Severity DiagnosticSeverity `json:"severity"`
	Line     int                `json:"line"`
	Col      int                `json:"col"`
	EndLine  int                `json:"end_line,omitempty"`
	EndCol   int                `json:"end_col,omitempty"`
	Rule     string             `json:"rule,omitempty"`
}

// FormatOutlineMarkdown generates a concise markdown representation of an outline,
// optimizing for token efficiency when ingested by LLM reasoning engines.
func FormatOutlineMarkdown(outline *Outline) string {
	if outline == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## Outline: %s\n", outline.FilePath))
	sb.WriteString(fmt.Sprintf("- **Language**: %s\n", outline.Language))
	if outline.PackageName != "" {
		sb.WriteString(fmt.Sprintf("- **Package**: `%s`\n", outline.PackageName))
	}
	sb.WriteString(fmt.Sprintf("- **Symbols**: %d top-level declarations\n\n", len(outline.Symbols)))

	var writeSymbol func(s SymbolNode, indent int)
	writeSymbol = func(s SymbolNode, indent int) {
		prefix := strings.Repeat("  ", indent)
		locStr := ""
		if s.Line > 0 {
			if s.EndLine > s.Line {
				locStr = fmt.Sprintf(" (lines %d-%d)", s.Line, s.EndLine)
			} else {
				locStr = fmt.Sprintf(" (line %d)", s.Line)
			}
		}

		sig := s.Signature
		if sig == "" {
			sig = s.Name
		}

		switch s.Kind {
		case SymbolKindStruct, SymbolKindInterface, SymbolKindType:
			sb.WriteString(fmt.Sprintf("%s- `%s` [%s]%s\n", prefix, sig, s.Kind, locStr))
		case SymbolKindFunction, SymbolKindMethod:
			sb.WriteString(fmt.Sprintf("%s- `%s`%s\n", prefix, sig, locStr))
		case SymbolKindPackage:
			sb.WriteString(fmt.Sprintf("%s- import `%s`%s\n", prefix, s.Name, locStr))
		case SymbolKindConstant, SymbolKindVariable:
			sb.WriteString(fmt.Sprintf("%s- %s `%s`%s\n", prefix, s.Kind, sig, locStr))
		default:
			sb.WriteString(fmt.Sprintf("%s- `%s` [%s]%s\n", prefix, sig, s.Kind, locStr))
		}

		if s.Doc != "" {
			docLines := strings.Split(strings.TrimSpace(s.Doc), "\n")
			for _, dl := range docLines {
				cleanDoc := strings.TrimSpace(dl)
				if cleanDoc != "" {
					sb.WriteString(fmt.Sprintf("%s    // %s\n", prefix, cleanDoc))
				}
			}
		}

		for _, child := range s.Children {
			writeSymbol(child, indent+1)
		}
	}

	for _, s := range outline.Symbols {
		writeSymbol(s, 0)
	}

	return strings.TrimRight(sb.String(), "\n") + "\n"
}
