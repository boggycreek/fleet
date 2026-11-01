// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package codeintel

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode"
)

var (
	reHeading    = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	rePyFunc     = regexp.MustCompile(`^\s*(?:async\s+)?def\s+([a-zA-Z0-9_]+)\s*\((.*?)\)`)
	rePyClass    = regexp.MustCompile(`^\s*class\s+([a-zA-Z0-9_]+)`)
	reJSFunc     = regexp.MustCompile(`^\s*(?:export\s+)?(?:async\s+)?function\s+([a-zA-Z0-9_]+)\s*\((.*?)\)`)
	reJSClass    = regexp.MustCompile(`^\s*(?:export\s+)?class\s+([a-zA-Z0-9_]+)`)
	reShellFunc  = regexp.MustCompile(`^\s*([a-zA-Z0-9_-]+)\s*\(\)\s*\{`)
	reRustFunc   = regexp.MustCompile(`^\s*(?:pub\s+)?fn\s+([a-zA-Z0-9_]+)`)
	reRustStruct = regexp.MustCompile(`^\s*(?:pub\s+)?(?:struct|enum|trait)\s+([a-zA-Z0-9_]+)`)
)

// UniversalFallbackDriver provides lightweight regex-based outline extraction for plain text,
// markdown, shell scripts, Python, JavaScript, and unhandled file formats.
type UniversalFallbackDriver struct{}

// NewUniversalFallbackDriver creates a new fallback provider.
func NewUniversalFallbackDriver() *UniversalFallbackDriver {
	return &UniversalFallbackDriver{}
}

// Name returns the provider driver name.
func (d *UniversalFallbackDriver) Name() string {
	return "universal-fallback"
}

// Languages returns wildcard matching any language.
func (d *UniversalFallbackDriver) Languages() []string {
	return []string{"*"}
}

// GetOutline scans content with regex heuristics to extract outline symbols.
func (d *UniversalFallbackDriver) GetOutline(_ context.Context, filePath string, content []byte) (*Outline, error) {
	if len(content) == 0 && filePath != "" {
		var err error
		content, err = os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read source file: %w", err)
		}
	}

	outline := &Outline{
		FilePath: filePath,
		Language: "universal",
		Symbols:  make([]SymbolNode, 0),
	}

	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if m := reHeading.FindStringSubmatch(line); len(m) > 2 {
			level := len(m[1])
			headingText := strings.TrimSpace(m[2])
			outline.Symbols = append(outline.Symbols, SymbolNode{
				Name:      headingText,
				Kind:      SymbolKindPackage,
				Signature: fmt.Sprintf("H%d: %s", level, headingText),
				Line:      lineNum,
				Col:       1,
				EndLine:   lineNum,
				EndCol:    len(line),
			})
			continue
		}

		if m := rePyFunc.FindStringSubmatch(line); len(m) > 1 {
			outline.Symbols = append(outline.Symbols, SymbolNode{
				Name:      m[1],
				Kind:      SymbolKindFunction,
				Signature: trimmed,
				Line:      lineNum,
				Col:       strings.Index(line, m[1]) + 1,
				EndLine:   lineNum,
				EndCol:    len(line),
			})
			continue
		}

		if m := rePyClass.FindStringSubmatch(line); len(m) > 1 {
			outline.Symbols = append(outline.Symbols, SymbolNode{
				Name:      m[1],
				Kind:      SymbolKindStruct,
				Signature: trimmed,
				Line:      lineNum,
				Col:       strings.Index(line, m[1]) + 1,
				EndLine:   lineNum,
				EndCol:    len(line),
			})
			continue
		}

		if m := reJSFunc.FindStringSubmatch(line); len(m) > 1 {
			outline.Symbols = append(outline.Symbols, SymbolNode{
				Name:      m[1],
				Kind:      SymbolKindFunction,
				Signature: trimmed,
				Line:      lineNum,
				Col:       strings.Index(line, m[1]) + 1,
				EndLine:   lineNum,
				EndCol:    len(line),
			})
			continue
		}

		if m := reJSClass.FindStringSubmatch(line); len(m) > 1 {
			outline.Symbols = append(outline.Symbols, SymbolNode{
				Name:      m[1],
				Kind:      SymbolKindStruct,
				Signature: trimmed,
				Line:      lineNum,
				Col:       strings.Index(line, m[1]) + 1,
				EndLine:   lineNum,
				EndCol:    len(line),
			})
			continue
		}

		if m := reShellFunc.FindStringSubmatch(line); len(m) > 1 {
			outline.Symbols = append(outline.Symbols, SymbolNode{
				Name:      m[1],
				Kind:      SymbolKindFunction,
				Signature: m[1] + "()",
				Line:      lineNum,
				Col:       strings.Index(line, m[1]) + 1,
				EndLine:   lineNum,
				EndCol:    len(line),
			})
			continue
		}

		if m := reRustFunc.FindStringSubmatch(line); len(m) > 1 {
			outline.Symbols = append(outline.Symbols, SymbolNode{
				Name:      m[1],
				Kind:      SymbolKindFunction,
				Signature: trimmed,
				Line:      lineNum,
				Col:       strings.Index(line, m[1]) + 1,
				EndLine:   lineNum,
				EndCol:    len(line),
			})
			continue
		}

		if m := reRustStruct.FindStringSubmatch(line); len(m) > 1 {
			outline.Symbols = append(outline.Symbols, SymbolNode{
				Name:      m[1],
				Kind:      SymbolKindStruct,
				Signature: trimmed,
				Line:      lineNum,
				Col:       strings.Index(line, m[1]) + 1,
				EndLine:   lineNum,
				EndCol:    len(line),
			})
			continue
		}
	}

	return outline, nil
}

// ValidateSyntax for universal fallback is a no-op that returns zero diagnostics.
func (d *UniversalFallbackDriver) ValidateSyntax(_ context.Context, _ string, _ []byte) ([]Diagnostic, error) {
	return []Diagnostic{}, nil
}

// FindDefinition performs a regex scan for definitions of the symbol name.
func (d *UniversalFallbackDriver) FindDefinition(_ context.Context, filePath string, line, col int) ([]Location, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read source file: %w", err)
	}

	targetWord := extractWordAt(content, line, col)
	if targetWord == "" {
		return []Location{}, nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0
	var locs []Location
	defPattern := regexp.MustCompile(fmt.Sprintf(`\b(?:def|class|function|fn|struct|enum)\s+%s\b`, regexp.QuoteMeta(targetWord)))

	for scanner.Scan() {
		lineNum++
		text := scanner.Text()
		if idx := defPattern.FindStringIndex(text); idx != nil {
			locs = append(locs, Location{
				FilePath: filePath,
				Line:     lineNum,
				Col:      idx[0] + 1,
				EndLine:  lineNum,
				EndCol:   idx[1] + 1,
			})
		}
	}

	return locs, nil
}

// FindReferences scans for occurrences of the word at line and col.
func (d *UniversalFallbackDriver) FindReferences(_ context.Context, filePath string, line, col int) ([]Location, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read source file: %w", err)
	}

	targetWord := extractWordAt(content, line, col)
	if targetWord == "" {
		return []Location{}, nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0
	var locs []Location
	wordPattern := regexp.MustCompile(fmt.Sprintf(`\b%s\b`, regexp.QuoteMeta(targetWord)))

	for scanner.Scan() {
		lineNum++
		text := scanner.Text()
		matches := wordPattern.FindAllStringIndex(text, -1)
		for _, m := range matches {
			locs = append(locs, Location{
				FilePath: filePath,
				Line:     lineNum,
				Col:      m[0] + 1,
				EndLine:  lineNum,
				EndCol:   m[1] + 1,
			})
		}
	}

	return locs, nil
}

func extractWordAt(content []byte, targetLine, targetCol int) string {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum == targetLine {
			line := scanner.Text()
			if targetCol < 1 || targetCol > len(line) {
				return ""
			}
			runes := []rune(line)
			pos := targetCol - 1
			if pos >= len(runes) || (!unicode.IsLetter(runes[pos]) && !unicode.IsDigit(runes[pos]) && runes[pos] != '_') {
				return ""
			}
			start := pos
			for start > 0 && (unicode.IsLetter(runes[start-1]) || unicode.IsDigit(runes[start-1]) || runes[start-1] == '_') {
				start--
			}
			end := pos
			for end < len(runes) && (unicode.IsLetter(runes[end]) || unicode.IsDigit(runes[end]) || runes[end] == '_') {
				end++
			}
			return string(runes[start:end])
		}
	}
	return ""
}
