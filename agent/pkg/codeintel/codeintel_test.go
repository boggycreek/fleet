// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package codeintel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeGoDriver_Outline(t *testing.T) {
	driver := NewNativeGoDriver()
	if driver.Name() != "native-go" {
		t.Fatalf("expected driver name native-go, got %s", driver.Name())
	}
	langs := driver.Languages()
	if len(langs) == 0 || langs[0] != ".go" {
		t.Fatalf("expected .go language, got %v", langs)
	}

	src := `// Package sample demonstrates code outline.
package sample

import (
	"fmt"
	m "math"
)

// MaxRetries is a constant limit.
const MaxRetries = 3

// ErrCount tracks errors.
var ErrCount int

// Counter is a custom alias.
type Counter = int

// Worker performs jobs.
type Worker struct {
	// Name of worker.
	Name string
	ID   int
	Counter
}

// Handler defines handler contract.
type Handler interface {
	// Handle processes work.
	Handle(ctx context.Context) error
}

// NewWorker constructs worker.
func NewWorker(name string) *Worker {
	return &Worker{Name: name}
}

// Run executes worker tasks.
func (w *Worker) Run() error {
	fmt.Println(m.Pi)
	return nil
}
`
	ctx := context.Background()
	outline, err := driver.GetOutline(ctx, "sample.go", []byte(src))
	if err != nil {
		t.Fatalf("failed to get outline: %v", err)
	}

	if outline.PackageName != "sample" {
		t.Errorf("expected package sample, got %s", outline.PackageName)
	}
	if outline.Language != "go" {
		t.Errorf("expected language go, got %s", outline.Language)
	}
	if len(outline.Symbols) == 0 {
		t.Fatalf("expected symbols, got none")
	}

	// Verify symbol kinds
	foundWorker := false
	foundHandler := false
	foundNewWorker := false
	foundRun := false
	foundConst := false
	foundVar := false
	foundImport := false

	for _, s := range outline.Symbols {
		switch s.Name {
		case "Worker":
			foundWorker = true
			if s.Kind != SymbolKindStruct {
				t.Errorf("expected Worker to be struct, got %s", s.Kind)
			}
			if len(s.Children) < 3 {
				t.Errorf("expected Worker to have 3 fields, got %d", len(s.Children))
			}
		case "Handler":
			foundHandler = true
			if s.Kind != SymbolKindInterface {
				t.Errorf("expected Handler to be interface, got %s", s.Kind)
			}
			if len(s.Children) < 1 {
				t.Errorf("expected Handler to have 1 method, got %d", len(s.Children))
			}
		case "NewWorker":
			foundNewWorker = true
			if s.Kind != SymbolKindFunction {
				t.Errorf("expected NewWorker to be function, got %s", s.Kind)
			}
			if !strings.Contains(s.Signature, "func NewWorker(name string) *Worker") {
				t.Errorf("unexpected signature: %s", s.Signature)
			}
		case "Run":
			foundRun = true
			if s.Kind != SymbolKindMethod {
				t.Errorf("expected Run to be method, got %s", s.Kind)
			}
			if !strings.Contains(s.Signature, "func (w *Worker) Run() error") {
				t.Errorf("unexpected method signature: %s", s.Signature)
			}
		case "MaxRetries":
			foundConst = true
			if s.Kind != SymbolKindConstant {
				t.Errorf("expected MaxRetries to be constant, got %s", s.Kind)
			}
		case "ErrCount":
			foundVar = true
			if s.Kind != SymbolKindVariable {
				t.Errorf("expected ErrCount to be variable, got %s", s.Kind)
			}
		case "fmt":
			foundImport = true
			if s.Kind != SymbolKindPackage {
				t.Errorf("expected fmt to be package import, got %s", s.Kind)
			}
		}
	}

	if !foundWorker || !foundHandler || !foundNewWorker || !foundRun || !foundConst || !foundVar || !foundImport {
		t.Errorf("missing expected symbols in outline: worker=%v, handler=%v, newWorker=%v, run=%v, const=%v, var=%v, import=%v",
			foundWorker, foundHandler, foundNewWorker, foundRun, foundConst, foundVar, foundImport)
	}

	// Verify Markdown output formatting
	md := FormatOutlineMarkdown(outline)
	if !strings.Contains(md, "## Outline: sample.go") {
		t.Errorf("expected title in markdown, got: %s", md)
	}
	if !strings.Contains(md, "type Worker struct") {
		t.Errorf("expected Worker struct in markdown: %s", md)
	}
	if !strings.Contains(md, "func (w *Worker) Run() error") {
		t.Errorf("expected Run method in markdown: %s", md)
	}
}

func TestNativeGoDriver_FromDisk(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.go")
	content := []byte("package main\n\nfunc Main() {}\n")
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	driver := NewNativeGoDriver()
	outline, err := driver.GetOutline(context.Background(), filePath, nil)
	if err != nil {
		t.Fatalf("expected successful outline from disk: %v", err)
	}
	if len(outline.Symbols) != 1 || outline.Symbols[0].Name != "Main" {
		t.Errorf("unexpected outline symbols: %+v", outline.Symbols)
	}

	// Non-existent file
	if _, err := driver.GetOutline(context.Background(), filepath.Join(tmpDir, "missing.go"), nil); err == nil {
		t.Error("expected error for non-existent file")
	}

	// Invalid Go file parse error
	invalidPath := filepath.Join(tmpDir, "invalid.go")
	_ = os.WriteFile(invalidPath, []byte("package"), 0644)
	if _, err := driver.GetOutline(context.Background(), invalidPath, nil); err == nil {
		t.Error("expected error for unparseable Go file")
	}
}

func TestNativeGoDriver_ValidateSyntax(t *testing.T) {
	driver := NewNativeGoDriver()
	ctx := context.Background()

	// Valid Go syntax
	validSrc := []byte("package main\n\nfunc main() {}\n")
	diags, err := driver.ValidateSyntax(ctx, "main.go", validSrc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("expected 0 diagnostics for valid syntax, got %d", len(diags))
	}

	// Broken Go syntax
	invalidSrc := []byte("package main\n\nfunc main() {\n")
	diags, err = driver.ValidateSyntax(ctx, "main.go", invalidSrc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(diags) == 0 {
		t.Fatalf("expected syntax error diagnostics, got none")
	}
	if diags[0].Severity != SeverityError {
		t.Errorf("expected error severity, got %s", diags[0].Severity)
	}
	if diags[0].Line < 1 {
		t.Errorf("expected positive line number, got %d", diags[0].Line)
	}

	// From disk
	tmpDir := t.TempDir()
	validDisk := filepath.Join(tmpDir, "valid.go")
	_ = os.WriteFile(validDisk, validSrc, 0644)
	diags, err = driver.ValidateSyntax(ctx, validDisk, nil)
	if err != nil || len(diags) != 0 {
		t.Errorf("failed validation from disk: %v, diags=%d", err, len(diags))
	}

	// Non-existent file
	if _, err := driver.ValidateSyntax(ctx, filepath.Join(tmpDir, "missing.go"), nil); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestNativeGoDriver_FindDefinitionAndReferences(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "def.go")
	src := `package main

type Item struct {
	Val int
}

func Process(it *Item) int {
	return it.Val
}

func Main() {
	x := &Item{Val: 10}
	Process(x)
}
`
	if err := os.WriteFile(filePath, []byte(src), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	driver := NewNativeGoDriver()
	ctx := context.Background()

	// Find definition of Process at line 13 col 2 (Process(x))
	locs, err := driver.FindDefinition(ctx, filePath, 13, 3)
	if err != nil {
		t.Fatalf("FindDefinition failed: %v", err)
	}
	if len(locs) == 0 {
		t.Fatalf("expected to find definition of Process, got none")
	}
	if locs[0].Line != 7 {
		t.Errorf("expected definition at line 7, got line %d", locs[0].Line)
	}

	// Find definition on top-level Item
	itemLocs, err := driver.FindDefinition(ctx, filePath, 12, 8)
	if err != nil || len(itemLocs) == 0 {
		t.Fatalf("failed to resolve Item definition: %v", err)
	}

	// Out of bounds coordinate
	emptyLocs, err := driver.FindDefinition(ctx, filePath, 999, 1)
	if err != nil || len(emptyLocs) != 0 {
		t.Errorf("expected empty locs for out of bounds coord, got %v", emptyLocs)
	}

	// Missing file
	if _, err := driver.FindDefinition(ctx, filepath.Join(tmpDir, "none.go"), 1, 1); err == nil {
		t.Error("expected error for missing file in FindDefinition")
	}

	// Find references of Process
	refs, err := driver.FindReferences(ctx, filePath, 7, 6)
	if err != nil {
		t.Fatalf("FindReferences failed: %v", err)
	}
	if len(refs) < 2 {
		t.Errorf("expected at least 2 references to Process, got %d", len(refs))
	}

	// References on non-identifier position
	emptyRefs, err := driver.FindReferences(ctx, filePath, 2, 1)
	if err != nil || len(emptyRefs) != 0 {
		t.Errorf("expected no references on empty line, got %v", emptyRefs)
	}

	// Missing file
	if _, err := driver.FindReferences(ctx, filepath.Join(tmpDir, "none.go"), 1, 1); err == nil {
		t.Error("expected error for missing file in FindReferences")
	}
}

func TestUniversalFallbackDriver(t *testing.T) {
	driver := NewUniversalFallbackDriver()
	if driver.Name() != "universal-fallback" {
		t.Fatalf("expected universal-fallback, got %s", driver.Name())
	}
	langs := driver.Languages()
	if len(langs) != 1 || langs[0] != "*" {
		t.Fatalf("expected [*], got %v", langs)
	}

	ctx := context.Background()

	// Text with Python, Markdown, Shell, and Rust
	text := `# Architecture Overview
## Subsystems

class Calculator:
    def add(a, b):
        return a + b

function runApp() {
    return true;
}

deploy_agent() {
    echo "deploying"
}

pub fn calculate() -> u32 {
    42
}

struct Point {
    x: f64
}
`
	outline, err := driver.GetOutline(ctx, "script.py", []byte(text))
	if err != nil {
		t.Fatalf("GetOutline failed: %v", err)
	}
	if len(outline.Symbols) < 7 {
		t.Fatalf("expected at least 7 symbols extracted, got %d", len(outline.Symbols))
	}

	// Validate syntax returns empty
	diags, err := driver.ValidateSyntax(ctx, "script.py", []byte(text))
	if err != nil || len(diags) != 0 {
		t.Fatalf("expected 0 diags for fallback driver: %v", err)
	}

	// Test from disk
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "script.py")
	_ = os.WriteFile(filePath, []byte(text), 0644)

	diskOutline, err := driver.GetOutline(ctx, filePath, nil)
	if err != nil || len(diskOutline.Symbols) == 0 {
		t.Fatalf("failed GetOutline from disk: %v", err)
	}

	// Missing file
	if _, err := driver.GetOutline(ctx, filepath.Join(tmpDir, "missing.txt"), nil); err == nil {
		t.Error("expected error on missing file")
	}

	// FindDefinition in fallback
	locs, err := driver.FindDefinition(ctx, filePath, 5, 10) // "add"
	if err != nil || len(locs) == 0 {
		t.Fatalf("expected to find definition of 'add': %v", err)
	}

	// FindDefinition with no word at coord
	locsEmpty, err := driver.FindDefinition(ctx, filePath, 1, 1)
	if err != nil || len(locsEmpty) != 0 {
		t.Errorf("expected empty locs for non-word coord: %v", locsEmpty)
	}

	// Missing file FindDefinition
	if _, err := driver.FindDefinition(ctx, filepath.Join(tmpDir, "none.txt"), 1, 1); err == nil {
		t.Error("expected error for missing file")
	}

	// FindReferences in fallback
	refs, err := driver.FindReferences(ctx, filePath, 5, 10)
	if err != nil || len(refs) == 0 {
		t.Fatalf("expected references for 'add': %v", err)
	}

	// Missing file FindReferences
	if _, err := driver.FindReferences(ctx, filepath.Join(tmpDir, "none.txt"), 1, 1); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestRegistry_RoutingAndFallbacks(t *testing.T) {
	reg := NewRegistry()
	reg.Register(nil) // should be safely ignored

	goDriver := NewNativeGoDriver()
	fallbackDriver := NewUniversalFallbackDriver()

	reg.Register(goDriver)
	reg.Register(fallbackDriver)

	ctx := context.Background()

	// 1. Exact extension match
	p, ok := reg.GetProvider("foo/bar.go")
	if !ok || p.Name() != "native-go" {
		t.Fatalf("expected native-go provider for .go, got %v", p)
	}

	// 2. Fallback match for unknown extension
	pFallback, ok := reg.GetProvider("README.md")
	if !ok || pFallback.Name() != "universal-fallback" {
		t.Fatalf("expected universal-fallback for .md, got %v", pFallback)
	}

	// 3. Fallback for extensionless file
	pDocker, ok := reg.GetProvider("Dockerfile")
	if !ok || pDocker.Name() != "universal-fallback" {
		t.Fatalf("expected fallback for Dockerfile, got %v", pDocker)
	}

	// 4. GetOutline routing
	goSrc := "package main\nfunc Run() {}"
	outline, err := reg.GetOutline(ctx, "main.go", []byte(goSrc))
	if err != nil || outline.Language != "go" {
		t.Fatalf("GetOutline routing failed: %v", err)
	}

	// 5. ValidateSyntax routing
	diags, err := reg.ValidateSyntax(ctx, "main.go", []byte(goSrc))
	if err != nil || len(diags) != 0 {
		t.Fatalf("ValidateSyntax routing failed: %v", err)
	}

	// 6. FindDefinition & FindReferences routing
	tmpDir := t.TempDir()
	fPath := filepath.Join(tmpDir, "main.go")
	_ = os.WriteFile(fPath, []byte(goSrc), 0644)

	defLocs, err := reg.FindDefinition(ctx, fPath, 2, 6)
	if err != nil || len(defLocs) == 0 {
		t.Fatalf("FindDefinition routing failed: %v", err)
	}

	refLocs, err := reg.FindReferences(ctx, fPath, 2, 6)
	if err != nil || len(refLocs) == 0 {
		t.Fatalf("FindReferences routing failed: %v", err)
	}

	// 7. Empty registry
	emptyReg := NewRegistry()
	if _, ok := emptyReg.GetProvider("main.go"); ok {
		t.Error("expected no provider in empty registry")
	}
	if _, err := emptyReg.GetOutline(ctx, "main.go", []byte(goSrc)); err == nil {
		t.Error("expected error for empty registry GetOutline")
	}
	emptyDiags, err := emptyReg.ValidateSyntax(ctx, "main.go", []byte(goSrc))
	if err != nil || len(emptyDiags) != 0 {
		t.Error("expected empty diags on empty registry")
	}
	emptyDef, err := emptyReg.FindDefinition(ctx, "main.go", 1, 1)
	if err != nil || len(emptyDef) != 0 {
		t.Error("expected empty def on empty registry")
	}
	emptyRef, err := emptyReg.FindReferences(ctx, "main.go", 1, 1)
	if err != nil || len(emptyRef) != 0 {
		t.Error("expected empty ref on empty registry")
	}
}

func TestConfig_LoadingAndDefaults(t *testing.T) {
	// 1. NewDefaultRegistry has native-go, polyglot-ast, and universal-fallback drivers
	defReg := NewDefaultRegistry()
	if p, ok := defReg.GetProvider("test.go"); !ok || p.Name() != "native-go" {
		t.Errorf("expected native-go in default registry")
	}
	if p, ok := defReg.GetProvider("test.py"); !ok || p.Name() != "polyglot-ast" {
		t.Errorf("expected polyglot-ast for .py in default registry")
	}
	if p, ok := defReg.GetProvider("test.unknown"); !ok || p.Name() != "universal-fallback" {
		t.Errorf("expected universal-fallback in default registry")
	}

	// 2. LoadRegistry without config file
	tmpDir := t.TempDir()
	reg, err := LoadRegistry(tmpDir)
	if err != nil {
		t.Fatalf("LoadRegistry failed: %v", err)
	}
	if p, ok := reg.GetProvider("test.go"); !ok || p.Name() != "native-go" {
		t.Errorf("expected default native-go provider")
	}

	// 3. LoadRegistry with .fleet/intel.json disabling native-go
	fleetDir := filepath.Join(tmpDir, ".fleet")
	_ = os.MkdirAll(fleetDir, 0755)
	intelJSON := `{"disabled": ["native-go"]}`
	_ = os.WriteFile(filepath.Join(fleetDir, "intel.json"), []byte(intelJSON), 0644)

	regCustom, err := LoadRegistry(tmpDir)
	if err != nil {
		t.Fatalf("LoadRegistry failed with .fleet/intel.json: %v", err)
	}
	// Since native-go is disabled, test.go should fall back to universal-fallback
	if p, ok := regCustom.GetProvider("test.go"); !ok || p.Name() != "universal-fallback" {
		t.Errorf("expected universal-fallback when native-go is disabled, got %v", p)
	}

	// 4. LoadRegistry with .agent_config.json
	tmpDir2 := t.TempDir()
	agentConfigJSON := `{"disabled": ["universal-fallback"]}`
	_ = os.WriteFile(filepath.Join(tmpDir2, ".agent_config.json"), []byte(agentConfigJSON), 0644)

	regAgentCfg, err := LoadRegistry(tmpDir2)
	if err != nil {
		t.Fatalf("LoadRegistry failed with .agent_config.json: %v", err)
	}
	if _, ok := regAgentCfg.GetProvider("unknown.xyz"); ok {
		t.Error("expected no fallback when universal-fallback is disabled")
	}

	// 5. Corrupt config JSON
	tmpDir3 := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmpDir3, ".agent_config.json"), []byte("{broken json"), 0644)
	regCorrupt, err := LoadRegistry(tmpDir3)
	if err == nil {
		t.Error("expected error for corrupted config JSON")
	}
	if regCorrupt == nil {
		t.Error("expected non-nil registry even on corrupt config")
	}
}

func TestFormatOutlineMarkdown_EdgeCases(t *testing.T) {
	if FormatOutlineMarkdown(nil) != "" {
		t.Errorf("expected empty string for nil outline")
	}

	outline := &Outline{
		FilePath: "test.go",
		Language: "go",
		Symbols: []SymbolNode{
			{
				Name:      "SingleLine",
				Kind:      SymbolKindFunction,
				Signature: "func SingleLine()",
				Line:      10,
				EndLine:   10,
			},
			{
				Name: "Custom",
				Kind: SymbolKindType,
				Doc:  "Custom type doc\nSecond line",
			},
		},
	}
	out := FormatOutlineMarkdown(outline)
	if !strings.Contains(out, "(line 10)") {
		t.Errorf("expected (line 10), got: %s", out)
	}
	if !strings.Contains(out, "// Custom type doc") {
		t.Errorf("expected doc comment in output: %s", out)
	}
}
