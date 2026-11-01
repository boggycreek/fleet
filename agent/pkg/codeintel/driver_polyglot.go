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
	"path/filepath"
	"regexp"
	"strings"
)

// Supported polyglot file extensions
var polyglotExtensions = []string{
	".py", "py",
	".js", "js", ".jsx", "jsx", ".mjs", "mjs", ".cjs", "cjs",
	".ts", "ts", ".tsx", "tsx",
	".rs", "rs",
	".sh", "sh", ".bash", "bash", ".zsh", "zsh",
}

// PolyglotDriver provides structural symbol and outline extraction for Python,
// TypeScript/JavaScript, Rust, and Shell without Cgo or external dependencies.
type PolyglotDriver struct{}

// NewPolyglotDriver constructs a new polyglot structural AST driver.
func NewPolyglotDriver() *PolyglotDriver {
	return &PolyglotDriver{}
}

// Name returns the provider driver name.
func (d *PolyglotDriver) Name() string {
	return "polyglot-ast"
}

// Languages returns supported file extensions.
func (d *PolyglotDriver) Languages() []string {
	return polyglotExtensions
}

// GetOutline extracts a hierarchical symbol tree across supported languages.
func (d *PolyglotDriver) GetOutline(_ context.Context, filePath string, content []byte) (*Outline, error) {
	if content == nil && filePath != "" {
		var err error
		content, err = os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read source file: %w", err)
		}
	}

	lang := detectLanguage(filePath)
	outline := &Outline{
		FilePath: filePath,
		Language: lang,
		Symbols:  make([]SymbolNode, 0),
	}

	lines := splitLines(content)
	if len(lines) == 0 {
		return outline, nil
	}

	switch lang {
	case "python":
		outline.Symbols = parsePythonSymbols(lines)
	case "typescript", "javascript":
		outline.Symbols = parseJSTSSymbols(lines)
	case "rust":
		outline.Symbols = parseRustSymbols(lines)
	case "shell":
		outline.Symbols = parseShellSymbols(lines)
	default:
		// Fallback for unknown extension routed here
		outline.Symbols = parseJSTSSymbols(lines)
	}

	if outline.Symbols == nil {
		outline.Symbols = make([]SymbolNode, 0)
	}

	return outline, nil
}

// ValidateSyntax performs basic structural syntax validation across languages.
func (d *PolyglotDriver) ValidateSyntax(_ context.Context, filePath string, content []byte) ([]Diagnostic, error) {
	if len(content) == 0 && filePath != "" {
		var err error
		content, err = os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read source file: %w", err)
		}
	}

	lang := detectLanguage(filePath)
	lines := splitLines(content)
	var diags []Diagnostic

	switch lang {
	case "python":
		diags = validatePythonIndentAndQuotes(lines)
	case "typescript", "javascript", "rust", "shell":
		diags = validateBracketMatching(lines)
	}

	if diags == nil {
		diags = []Diagnostic{}
	}
	return diags, nil
}

// FindDefinition scans for symbol definitions.
func (d *PolyglotDriver) FindDefinition(ctx context.Context, filePath string, line, col int) ([]Location, error) {
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
	defPattern := regexp.MustCompile(fmt.Sprintf(`\b(?:def|class|function|fn|struct|enum|trait|interface|type)\s+%s\b`, regexp.QuoteMeta(targetWord)))

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

// FindReferences scans for occurrences of symbol.
func (d *PolyglotDriver) FindReferences(ctx context.Context, filePath string, line, col int) ([]Location, error) {
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

// detectLanguage maps file path extensions to normalized language strings.
func detectLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".py":
		return "python"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".rs":
		return "rust"
	case ".sh", ".bash", ".zsh":
		return "shell"
	}
	base := strings.ToLower(filepath.Base(filePath))
	if strings.Contains(base, "bashrc") || strings.Contains(base, "zshrc") || strings.Contains(base, "profile") {
		return "shell"
	}
	return "universal"
}

func splitLines(content []byte) []string {
	if len(content) == 0 {
		return nil
	}
	raw := string(content)
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	return strings.Split(raw, "\n")
}

// -----------------------------------------------------------------------------
// Python Parser
// -----------------------------------------------------------------------------

var (
	pyClassRe = regexp.MustCompile(`^class\s+([a-zA-Z0-9_]+)(?:\s*\((.*?)\))?\s*:`)
	pyFuncRe  = regexp.MustCompile(`^(?:async\s+)?def\s+([a-zA-Z0-9_]+)\s*\((.*?)\)(?:\s*->\s*(.*?))?\s*:`)
	pyDecoRe  = regexp.MustCompile(`^@([a-zA-Z0-9_.]+(?:\(.*?\))?)`)
)

func parsePythonSymbols(lines []string) []SymbolNode {
	var symbols []SymbolNode
	n := len(lines)

	type pendingDeco struct {
		name string
		line int
	}
	var currentDecos []pendingDeco

	countIndent := func(line string) int {
		indent := 0
		for _, ch := range line {
			if ch == ' ' {
				indent++
			} else if ch == '\t' {
				indent += 4
			} else {
				break
			}
		}
		return indent
	}

	// Helper to find end line of an indented block
	findPythonBlockEnd := func(startIdx int, baseIndent int) int {
		lastContentLine := startIdx + 1
		for i := startIdx + 1; i < n; i++ {
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			lineIndent := countIndent(lines[i])
			if lineIndent <= baseIndent {
				return lastContentLine
			}
			lastContentLine = i + 1
		}
		return lastContentLine
	}

	// Helper to extract docstring right after def/class
	extractPythonDocstring := func(startIdx int) string {
		if startIdx+1 >= n {
			return ""
		}
		for i := startIdx + 1; i < n && i <= startIdx+3; i++ {
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, `"""`) || strings.HasPrefix(trimmed, `'''`) {
				quote := trimmed[:3]
				after := strings.TrimPrefix(trimmed, quote)
				if idx := strings.Index(after, quote); idx != -1 {
					return strings.TrimSpace(after[:idx])
				}
				// Multi-line docstring
				var docLines []string
				if after != "" {
					docLines = append(docLines, strings.TrimSpace(after))
				}
				for j := i + 1; j < n; j++ {
					t := strings.TrimSpace(lines[j])
					if idx := strings.Index(t, quote); idx != -1 {
						if t[:idx] != "" {
							docLines = append(docLines, strings.TrimSpace(t[:idx]))
						}
						return strings.Join(docLines, "\n")
					}
					docLines = append(docLines, t)
				}
				return strings.Join(docLines, "\n")
			}
			break
		}
		return ""
	}

	i := 0
	for i < n {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		lineNum := i + 1

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			i++
			continue
		}

		// Decorator
		if m := pyDecoRe.FindStringSubmatch(trimmed); len(m) > 1 {
			currentDecos = append(currentDecos, pendingDeco{
				name: "@" + m[1],
				line: lineNum,
			})
			i++
			continue
		}

		indent := countIndent(line)

		// Top-level or nested class / func
		if m := pyClassRe.FindStringSubmatch(trimmed); len(m) > 1 {
			className := m[1]
			baseClasses := ""
			if len(m) > 2 && m[2] != "" {
				baseClasses = fmt.Sprintf("(%s)", m[2])
			}
			endLine := findPythonBlockEnd(i, indent)
			doc := extractPythonDocstring(i)

			sig := fmt.Sprintf("class %s%s", className, baseClasses)
			if len(currentDecos) > 0 {
				var decoNames []string
				for _, d := range currentDecos {
					decoNames = append(decoNames, d.name)
				}
				sig = strings.Join(decoNames, " ") + " " + sig
			}

			classNode := SymbolNode{
				Name:      className,
				Kind:      SymbolKindStruct,
				Signature: sig,
				Doc:       doc,
				Line:      lineNum,
				Col:       strings.Index(line, className) + 1,
				EndLine:   endLine,
				EndCol:    len(lines[endLine-1]),
			}

			// Scan methods inside class
			var methods []SymbolNode
			var innerDecos []pendingDeco
			for j := i + 1; j < endLine; j++ {
				innerLine := lines[j]
				innerTrimmed := strings.TrimSpace(innerLine)
				innerLineNum := j + 1
				if innerTrimmed == "" || strings.HasPrefix(innerTrimmed, "#") {
					continue
				}

				if dm := pyDecoRe.FindStringSubmatch(innerTrimmed); len(dm) > 1 {
					innerDecos = append(innerDecos, pendingDeco{
						name: "@" + dm[1],
						line: innerLineNum,
					})
					continue
				}

				innerIndent := countIndent(innerLine)
				if innerIndent > indent {
					if fm := pyFuncRe.FindStringSubmatch(innerTrimmed); len(fm) > 1 {
						fnName := fm[1]
						params := fm[2]
						retType := ""
						if len(fm) > 3 && fm[3] != "" {
							retType = " -> " + fm[3]
						}
						mEndLine := findPythonBlockEnd(j, innerIndent)
						mDoc := extractPythonDocstring(j)

						mSig := fmt.Sprintf("def %s(%s)%s", fnName, params, retType)
						if len(innerDecos) > 0 {
							var idNames []string
							for _, id := range innerDecos {
								idNames = append(idNames, id.name)
							}
							mSig = strings.Join(idNames, " ") + " " + mSig
						}

						methods = append(methods, SymbolNode{
							Name:      fnName,
							Kind:      SymbolKindMethod,
							Signature: mSig,
							Doc:       mDoc,
							Line:      innerLineNum,
							Col:       strings.Index(innerLine, fnName) + 1,
							EndLine:   mEndLine,
							EndCol:    len(lines[mEndLine-1]),
						})
					}
				}
				innerDecos = nil
			}

			classNode.Children = methods
			symbols = append(symbols, classNode)
			currentDecos = nil
			i = endLine
			continue
		}

		if m := pyFuncRe.FindStringSubmatch(trimmed); len(m) > 1 {
			fnName := m[1]
			params := m[2]
			retType := ""
			if len(m) > 3 && m[3] != "" {
				retType = " -> " + m[3]
			}
			endLine := findPythonBlockEnd(i, indent)
			doc := extractPythonDocstring(i)

			sig := fmt.Sprintf("def %s(%s)%s", fnName, params, retType)
			if strings.HasPrefix(trimmed, "async ") {
				sig = "async " + sig
			}
			if len(currentDecos) > 0 {
				var decoNames []string
				for _, d := range currentDecos {
					decoNames = append(decoNames, d.name)
				}
				sig = strings.Join(decoNames, " ") + " " + sig
			}

			symbols = append(symbols, SymbolNode{
				Name:      fnName,
				Kind:      SymbolKindFunction,
				Signature: sig,
				Doc:       doc,
				Line:      lineNum,
				Col:       strings.Index(line, fnName) + 1,
				EndLine:   endLine,
				EndCol:    len(lines[endLine-1]),
			})
			currentDecos = nil
			i = endLine
			continue
		}

		currentDecos = nil
		i++
	}

	return symbols
}

// -----------------------------------------------------------------------------
// TypeScript / JavaScript Parser
// -----------------------------------------------------------------------------

var (
	jsClassRe     = regexp.MustCompile(`^(?:export\s+(?:default\s+)?)?class\s+([a-zA-Z0-9_]+)(?:\s+extends\s+([a-zA-Z0-9_]+))?(?:\s+implements\s+([^{]+))?`)
	jsInterfaceRe = regexp.MustCompile(`^(?:export\s+)?interface\s+([a-zA-Z0-9_]+)(?:<.*?>)?(?:\s+extends\s+([^{]+))?`)
	jsTypeRe      = regexp.MustCompile(`^(?:export\s+)?type\s+([a-zA-Z0-9_]+)(?:<.*?>)?\s*=`)
	jsFuncRe      = regexp.MustCompile(`^(?:export\s+(?:default\s+)?)?(?:async\s+)?function(?:\s+([a-zA-Z0-9_]+))?\s*\((.*?)\)(?:\s*:\s*([^{;]+))?`)
	jsArrowRe     = regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+([a-zA-Z0-9_]+)(?:\s*:\s*([a-zA-Z0-9_<>, \[\]]+))?\s*=\s*(?:async\s*)?(?:\((.*?)\)|[a-zA-Z0-9_]+)\s*(?::\s*([^{;]+))?\s*=>`)
	jsMethodRe    = regexp.MustCompile(`^\s*(?:public|private|protected|static|async|readonly|\s)*\s*([a-zA-Z0-9_]+)\s*\((.*?)\)(?:\s*:\s*([^{;]+))?`)
)

func parseJSTSSymbols(lines []string) []SymbolNode {
	var symbols []SymbolNode
	n := len(lines)

	findBraceEnd := func(startLine int) int {
		openBraces := 0
		foundFirst := false
		for i := startLine; i < n; i++ {
			line := lines[i]
			// Strip string literals and comments for brace counting
			clean := stripQuotesAndComments(line)
			for _, ch := range clean {
				if ch == '{' {
					openBraces++
					foundFirst = true
				} else if ch == '}' {
					openBraces--
					if foundFirst && openBraces <= 0 {
						return i + 1
					}
				}
			}
		}
		if startLine+1 <= n {
			return startLine + 1
		}
		return n
	}

	extractJSDoc := func(startLineIdx int) string {
		if startLineIdx <= 0 {
			return ""
		}
		var docLines []string
		inDoc := false
		for i := startLineIdx - 1; i >= 0 && i >= startLineIdx-15; i-- {
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == "" {
				if inDoc {
					break
				}
				continue
			}
			if strings.HasSuffix(trimmed, "*/") {
				inDoc = true
			}
			if inDoc {
				clean := strings.TrimPrefix(trimmed, "/**")
				clean = strings.TrimPrefix(clean, "/*")
				clean = strings.TrimSuffix(clean, "*/")
				clean = strings.TrimPrefix(clean, "*")
				clean = strings.TrimSpace(clean)
				if clean != "" {
					docLines = append([]string{clean}, docLines...)
				}
				if strings.HasPrefix(trimmed, "/*") {
					break
				}
			} else if strings.HasPrefix(trimmed, "//") {
				docLines = append([]string{strings.TrimSpace(strings.TrimPrefix(trimmed, "//"))}, docLines...)
			} else {
				break
			}
		}
		return strings.Join(docLines, "\n")
	}

	for i := 0; i < n; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		lineNum := i + 1

		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}

		// Classes
		if m := jsClassRe.FindStringSubmatch(trimmed); len(m) > 1 {
			className := m[1]
			endLine := findBraceEnd(i)
			doc := extractJSDoc(i)
			sig := strings.TrimSuffix(trimmed, "{")
			sig = strings.TrimSpace(sig)

			classNode := SymbolNode{
				Name:      className,
				Kind:      SymbolKindStruct,
				Signature: sig,
				Doc:       doc,
				Line:      lineNum,
				Col:       strings.Index(line, className) + 1,
				EndLine:   endLine,
				EndCol:    len(lines[endLine-1]),
			}

			// Scan class members
			var children []SymbolNode
			for j := i + 1; j < endLine-1; j++ {
				mLine := lines[j]
				mTrimmed := strings.TrimSpace(mLine)
				if mTrimmed == "" || strings.HasPrefix(mTrimmed, "//") || strings.HasPrefix(mTrimmed, "/*") {
					continue
				}
				if mm := jsMethodRe.FindStringSubmatch(mTrimmed); len(mm) > 1 {
					mName := mm[1]
					if mName == "if" || mName == "for" || mName == "while" || mName == "switch" || mName == "catch" {
						continue
					}
					mEnd := findBraceEnd(j)
					mDoc := extractJSDoc(j)
					mSig := strings.TrimSuffix(mTrimmed, "{")
					mSig = strings.TrimSpace(mSig)

					kind := SymbolKindMethod
					if mName == "constructor" {
						kind = SymbolKindFunction
					}

					children = append(children, SymbolNode{
						Name:      mName,
						Kind:      kind,
						Signature: mSig,
						Doc:       mDoc,
						Line:      j + 1,
						Col:       strings.Index(mLine, mName) + 1,
						EndLine:   mEnd,
						EndCol:    len(lines[mEnd-1]),
					})
					if mEnd > j+1 {
						j = mEnd - 1
					}
				}
			}

			classNode.Children = children
			symbols = append(symbols, classNode)
			if endLine > i+1 {
				i = endLine - 1
			}
			continue
		}

		// Interfaces
		if m := jsInterfaceRe.FindStringSubmatch(trimmed); len(m) > 1 {
			ifaceName := m[1]
			endLine := findBraceEnd(i)
			doc := extractJSDoc(i)
			sig := strings.TrimSuffix(trimmed, "{")
			sig = strings.TrimSpace(sig)

			symbols = append(symbols, SymbolNode{
				Name:      ifaceName,
				Kind:      SymbolKindInterface,
				Signature: sig,
				Doc:       doc,
				Line:      lineNum,
				Col:       strings.Index(line, ifaceName) + 1,
				EndLine:   endLine,
				EndCol:    len(lines[endLine-1]),
			})
			if endLine > i+1 {
				i = endLine - 1
			}
			continue
		}

		// Type Aliases
		if m := jsTypeRe.FindStringSubmatch(trimmed); len(m) > 1 {
			typeName := m[1]
			doc := extractJSDoc(i)
			endLine := lineNum
			// Find semicolon or closing line
			for k := i; k < n; k++ {
				if strings.Contains(lines[k], ";") {
					endLine = k + 1
					break
				}
			}
			symbols = append(symbols, SymbolNode{
				Name:      typeName,
				Kind:      SymbolKindType,
				Signature: strings.TrimSpace(trimmed),
				Doc:       doc,
				Line:      lineNum,
				Col:       strings.Index(line, typeName) + 1,
				EndLine:   endLine,
				EndCol:    len(lines[endLine-1]),
			})
			if endLine > i+1 {
				i = endLine - 1
			}
			continue
		}

		// Functions
		if m := jsFuncRe.FindStringSubmatch(trimmed); len(m) > 0 {
			fnName := "anonymous"
			if len(m) > 1 && m[1] != "" {
				fnName = m[1]
			}
			endLine := findBraceEnd(i)
			doc := extractJSDoc(i)
			sig := strings.TrimSuffix(trimmed, "{")
			sig = strings.TrimSpace(sig)

			col := strings.Index(line, fnName)
			if col < 0 {
				col = 0
			}

			symbols = append(symbols, SymbolNode{
				Name:      fnName,
				Kind:      SymbolKindFunction,
				Signature: sig,
				Doc:       doc,
				Line:      lineNum,
				Col:       col + 1,
				EndLine:   endLine,
				EndCol:    len(lines[endLine-1]),
			})
			if endLine > i+1 {
				i = endLine - 1
			}
			continue
		}

		// Arrow functions assigned to const/let/var
		if m := jsArrowRe.FindStringSubmatch(trimmed); len(m) > 1 {
			varName := m[1]
			endLine := findBraceEnd(i)
			if !strings.Contains(trimmed, "{") {
				// Single expression arrow fn
				endLine = lineNum
			}
			doc := extractJSDoc(i)
			sig := strings.TrimSuffix(trimmed, "{")
			sig = strings.TrimSpace(sig)

			symbols = append(symbols, SymbolNode{
				Name:      varName,
				Kind:      SymbolKindFunction,
				Signature: sig,
				Doc:       doc,
				Line:      lineNum,
				Col:       strings.Index(line, varName) + 1,
				EndLine:   endLine,
				EndCol:    len(lines[endLine-1]),
			})
			if endLine > i+1 {
				i = endLine - 1
			}
			continue
		}
	}

	return symbols
}

// -----------------------------------------------------------------------------
// Rust Parser
// -----------------------------------------------------------------------------

var (
	rustStructRe = regexp.MustCompile(`^(?:pub(?:\(.*?\))?\s+)?struct\s+([a-zA-Z0-9_]+)`)
	rustEnumRe   = regexp.MustCompile(`^(?:pub(?:\(.*?\))?\s+)?enum\s+([a-zA-Z0-9_]+)`)
	rustTraitRe  = regexp.MustCompile(`^(?:pub(?:\(.*?\))?\s+)?trait\s+([a-zA-Z0-9_]+)`)
	rustImplRe   = regexp.MustCompile(`^impl(?:<.*?>)?\s+(?:([a-zA-Z0-9_]+)\s+for\s+)?([a-zA-Z0-9_]+)`)
	rustFnRe     = regexp.MustCompile(`^(?:pub(?:\(.*?\))?\s+)?(?:async\s+)?(?:const\s+)?(?:unsafe\s+)?fn\s+([a-zA-Z0-9_]+)\s*(?:<.*?>)?\s*\((.*?)\)(?:\s*->\s*([^{;]+))?`)
)

func parseRustSymbols(lines []string) []SymbolNode {
	var symbols []SymbolNode
	n := len(lines)

	findBraceEnd := func(startLine int) int {
		openBraces := 0
		foundFirst := false
		for i := startLine; i < n; i++ {
			clean := stripQuotesAndComments(lines[i])
			for _, ch := range clean {
				if ch == '{' {
					openBraces++
					foundFirst = true
				} else if ch == '}' {
					openBraces--
					if foundFirst && openBraces <= 0 {
						return i + 1
					}
				}
			}
		}
		if startLine+1 <= n {
			return startLine + 1
		}
		return n
	}

	extractRustDoc := func(startLineIdx int) string {
		if startLineIdx <= 0 {
			return ""
		}
		var docLines []string
		for i := startLineIdx - 1; i >= 0 && i >= startLineIdx-15; i-- {
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, "///") || strings.HasPrefix(trimmed, "//!") {
				clean := strings.TrimPrefix(trimmed, "///")
				clean = strings.TrimPrefix(clean, "//!")
				docLines = append([]string{strings.TrimSpace(clean)}, docLines...)
			} else {
				break
			}
		}
		return strings.Join(docLines, "\n")
	}

	for i := 0; i < n; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		lineNum := i + 1

		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#[") {
			continue
		}

		// Struct
		if m := rustStructRe.FindStringSubmatch(trimmed); len(m) > 1 {
			structName := m[1]
			endLine := lineNum
			if strings.Contains(trimmed, "{") {
				endLine = findBraceEnd(i)
			} else if strings.Contains(trimmed, ";") {
				endLine = lineNum
			}
			doc := extractRustDoc(i)
			sig := strings.TrimSuffix(trimmed, "{")
			sig = strings.TrimSpace(sig)

			symbols = append(symbols, SymbolNode{
				Name:      structName,
				Kind:      SymbolKindStruct,
				Signature: sig,
				Doc:       doc,
				Line:      lineNum,
				Col:       strings.Index(line, structName) + 1,
				EndLine:   endLine,
				EndCol:    len(lines[endLine-1]),
			})
			if endLine > i+1 {
				i = endLine - 1
			}
			continue
		}

		// Enum
		if m := rustEnumRe.FindStringSubmatch(trimmed); len(m) > 1 {
			enumName := m[1]
			endLine := findBraceEnd(i)
			doc := extractRustDoc(i)
			sig := strings.TrimSuffix(trimmed, "{")
			sig = strings.TrimSpace(sig)

			symbols = append(symbols, SymbolNode{
				Name:      enumName,
				Kind:      SymbolKindType,
				Signature: sig,
				Doc:       doc,
				Line:      lineNum,
				Col:       strings.Index(line, enumName) + 1,
				EndLine:   endLine,
				EndCol:    len(lines[endLine-1]),
			})
			if endLine > i+1 {
				i = endLine - 1
			}
			continue
		}

		// Trait
		if m := rustTraitRe.FindStringSubmatch(trimmed); len(m) > 1 {
			traitName := m[1]
			endLine := findBraceEnd(i)
			doc := extractRustDoc(i)
			sig := strings.TrimSuffix(trimmed, "{")
			sig = strings.TrimSpace(sig)

			symbols = append(symbols, SymbolNode{
				Name:      traitName,
				Kind:      SymbolKindInterface,
				Signature: sig,
				Doc:       doc,
				Line:      lineNum,
				Col:       strings.Index(line, traitName) + 1,
				EndLine:   endLine,
				EndCol:    len(lines[endLine-1]),
			})
			if endLine > i+1 {
				i = endLine - 1
			}
			continue
		}

		// Impl blocks
		if m := rustImplRe.FindStringSubmatch(trimmed); len(m) > 2 {
			traitName := m[1]
			targetType := m[2]
			implName := targetType
			if traitName != "" {
				implName = fmt.Sprintf("%s for %s", traitName, targetType)
			}
			endLine := findBraceEnd(i)
			doc := extractRustDoc(i)
			sig := strings.TrimSuffix(trimmed, "{")
			sig = strings.TrimSpace(sig)

			implNode := SymbolNode{
				Name:      implName,
				Kind:      SymbolKindStruct,
				Signature: sig,
				Doc:       doc,
				Line:      lineNum,
				Col:       strings.Index(line, "impl") + 1,
				EndLine:   endLine,
				EndCol:    len(lines[endLine-1]),
			}

			// Scan methods within impl block
			var methods []SymbolNode
			for j := i + 1; j < endLine-1; j++ {
				mLine := lines[j]
				mTrimmed := strings.TrimSpace(mLine)
				if mTrimmed == "" || strings.HasPrefix(mTrimmed, "//") || strings.HasPrefix(mTrimmed, "#[") {
					continue
				}
				if fm := rustFnRe.FindStringSubmatch(mTrimmed); len(fm) > 1 {
					fnName := fm[1]
					mEnd := findBraceEnd(j)
					mDoc := extractRustDoc(j)
					mSig := strings.TrimSuffix(mTrimmed, "{")
					mSig = strings.TrimSpace(mSig)

					methods = append(methods, SymbolNode{
						Name:      fnName,
						Kind:      SymbolKindMethod,
						Signature: mSig,
						Doc:       mDoc,
						Line:      j + 1,
						Col:       strings.Index(mLine, fnName) + 1,
						EndLine:   mEnd,
						EndCol:    len(lines[mEnd-1]),
					})
					if mEnd > j+1 {
						j = mEnd - 1
					}
				}
			}

			implNode.Children = methods
			symbols = append(symbols, implNode)
			if endLine > i+1 {
				i = endLine - 1
			}
			continue
		}

		// Standalone functions
		if m := rustFnRe.FindStringSubmatch(trimmed); len(m) > 1 {
			fnName := m[1]
			endLine := findBraceEnd(i)
			doc := extractRustDoc(i)
			sig := strings.TrimSuffix(trimmed, "{")
			sig = strings.TrimSpace(sig)

			symbols = append(symbols, SymbolNode{
				Name:      fnName,
				Kind:      SymbolKindFunction,
				Signature: sig,
				Doc:       doc,
				Line:      lineNum,
				Col:       strings.Index(line, fnName) + 1,
				EndLine:   endLine,
				EndCol:    len(lines[endLine-1]),
			})
			if endLine > i+1 {
				i = endLine - 1
			}
			continue
		}
	}

	return symbols
}

// -----------------------------------------------------------------------------
// Shell Parser
// -----------------------------------------------------------------------------

var (
	shellFuncStandard = regexp.MustCompile(`^\s*(?:function\s+)?([a-zA-Z0-9_-]+)\s*\(\)\s*\{?`)
	shellFuncKeyword  = regexp.MustCompile(`^\s*function\s+([a-zA-Z0-9_-]+)\s*\{?`)
)

func parseShellSymbols(lines []string) []SymbolNode {
	var symbols []SymbolNode
	n := len(lines)

	findBraceEnd := func(startLine int) int {
		openBraces := 0
		foundFirst := false
		for i := startLine; i < n; i++ {
			clean := stripQuotesAndComments(lines[i])
			for _, ch := range clean {
				if ch == '{' {
					openBraces++
					foundFirst = true
				} else if ch == '}' {
					openBraces--
					if foundFirst && openBraces <= 0 {
						return i + 1
					}
				}
			}
		}
		if startLine+1 <= n {
			return startLine + 1
		}
		return n
	}

	extractShellDoc := func(startLineIdx int) string {
		if startLineIdx <= 0 {
			return ""
		}
		var docLines []string
		for i := startLineIdx - 1; i >= 0 && i >= startLineIdx-10; i-- {
			trimmed := strings.TrimSpace(lines[i])
			if trimmed == "" {
				continue
			}
			if strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "#!") {
				clean := strings.TrimPrefix(trimmed, "#")
				docLines = append([]string{strings.TrimSpace(clean)}, docLines...)
			} else {
				break
			}
		}
		return strings.Join(docLines, "\n")
	}

	for i := 0; i < n; i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		lineNum := i + 1

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		fnName := ""
		if m := shellFuncStandard.FindStringSubmatch(trimmed); len(m) > 1 {
			fnName = m[1]
		} else if m := shellFuncKeyword.FindStringSubmatch(trimmed); len(m) > 1 {
			fnName = m[1]
		}

		if fnName != "" {
			endLine := findBraceEnd(i)
			doc := extractShellDoc(i)

			symbols = append(symbols, SymbolNode{
				Name:      fnName,
				Kind:      SymbolKindFunction,
				Signature: fnName + "()",
				Doc:       doc,
				Line:      lineNum,
				Col:       strings.Index(line, fnName) + 1,
				EndLine:   endLine,
				EndCol:    len(lines[endLine-1]),
			})
			if endLine > i+1 {
				i = endLine - 1
			}
		}
	}

	return symbols
}

// -----------------------------------------------------------------------------
// Validation Utilities
// -----------------------------------------------------------------------------

func stripQuotesAndComments(line string) string {
	var sb strings.Builder
	inQuote := byte(0)
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if inQuote != 0 {
			if byte(r) == inQuote && (i == 0 || runes[i-1] != '\\') {
				inQuote = 0
			}
			continue
		}
		if r == '"' || r == '\'' || r == '`' {
			inQuote = byte(r)
			continue
		}
		if r == '/' && i+1 < len(runes) && runes[i+1] == '/' {
			break
		}
		if r == '#' {
			break
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

func validateBracketMatching(lines []string) []Diagnostic {
	var diags []Diagnostic
	type bracketPos struct {
		ch   rune
		line int
		col  int
	}
	var stack []bracketPos

	for lineIdx, line := range lines {
		clean := stripQuotesAndComments(line)
		for colIdx, ch := range clean {
			switch ch {
			case '{', '(', '[':
				stack = append(stack, bracketPos{ch: ch, line: lineIdx + 1, col: colIdx + 1})
			case '}':
				if len(stack) == 0 || stack[len(stack)-1].ch != '{' {
					diags = append(diags, Diagnostic{
						Message:  "unmatched closing brace '}'",
						Severity: SeverityError,
						Line:     lineIdx + 1,
						Col:      colIdx + 1,
						Rule:     "syntax-error",
					})
				} else {
					stack = stack[:len(stack)-1]
				}
			case ')':
				if len(stack) == 0 || stack[len(stack)-1].ch != '(' {
					diags = append(diags, Diagnostic{
						Message:  "unmatched closing parenthesis ')'",
						Severity: SeverityError,
						Line:     lineIdx + 1,
						Col:      colIdx + 1,
						Rule:     "syntax-error",
					})
				} else {
					stack = stack[:len(stack)-1]
				}
			case ']':
				if len(stack) == 0 || stack[len(stack)-1].ch != '[' {
					diags = append(diags, Diagnostic{
						Message:  "unmatched closing bracket ']'",
						Severity: SeverityError,
						Line:     lineIdx + 1,
						Col:      colIdx + 1,
						Rule:     "syntax-error",
					})
				} else {
					stack = stack[:len(stack)-1]
				}
			}
		}
	}

	for _, unclosed := range stack {
		diags = append(diags, Diagnostic{
			Message:  fmt.Sprintf("unclosed delimiter '%c'", unclosed.ch),
			Severity: SeverityError,
			Line:     unclosed.line,
			Col:      unclosed.col,
			Rule:     "syntax-error",
		})
	}

	return diags
}

func validatePythonIndentAndQuotes(lines []string) []Diagnostic {
	var diags []Diagnostic
	inTripleQuote := false
	tripleQuoteLine := 0

	for lineIdx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		tCount3 := strings.Count(line, `"""`)
		if tCount3%2 != 0 {
			if inTripleQuote {
				inTripleQuote = false
			} else {
				inTripleQuote = true
				tripleQuoteLine = lineIdx + 1
			}
		}
	}

	if inTripleQuote {
		diags = append(diags, Diagnostic{
			Message:  "unclosed triple-quoted string",
			Severity: SeverityError,
			Line:     tripleQuoteLine,
			Col:      1,
			Rule:     "syntax-error",
		})
	}

	// Also verify standard brackets inside python code
	bracketDiags := validateBracketMatching(lines)
	diags = append(diags, bracketDiags...)

	return diags
}
