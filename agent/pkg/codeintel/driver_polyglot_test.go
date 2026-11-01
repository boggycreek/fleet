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

func TestPolyglotDriver_Python(t *testing.T) {
	driver := NewPolyglotDriver()
	if driver.Name() != "polyglot-ast" {
		t.Fatalf("expected driver name polyglot-ast, got %s", driver.Name())
	}

	pySrc := `"""
Module docstring for task execution.
"""

import sys

@dataclass
@observable
class TaskRunner(BaseRunner):
    """
    Executes worker tasks asynchronously.
    """
    timeout: int = 30

    @retry(attempts=3)
    def run_job(self, task_id: str) -> bool:
        """Runs an individual job."""
        return True

    async def cancel(self):
        pass

@logged
def standalone_func(a: int, b: int) -> int:
    """Helper addition."""
    return a + b

async def fetch_status():
    return "ok"
`
	ctx := context.Background()
	outline, err := driver.GetOutline(ctx, "tasks.py", []byte(pySrc))
	if err != nil {
		t.Fatalf("failed to get python outline: %v", err)
	}

	if outline.Language != "python" {
		t.Errorf("expected python language, got %s", outline.Language)
	}
	if len(outline.Symbols) < 3 {
		t.Fatalf("expected at least 3 top-level symbols, got %d", len(outline.Symbols))
	}

	var foundRunner *SymbolNode
	var foundStandalone *SymbolNode
	var foundFetch *SymbolNode

	for i := range outline.Symbols {
		s := &outline.Symbols[i]
		switch s.Name {
		case "TaskRunner":
			foundRunner = s
		case "standalone_func":
			foundStandalone = s
		case "fetch_status":
			foundFetch = s
		}
	}

	if foundRunner == nil {
		t.Fatalf("TaskRunner class not found in symbols")
	}
	if foundRunner.Kind != SymbolKindStruct {
		t.Errorf("expected TaskRunner kind struct, got %s", foundRunner.Kind)
	}
	if !strings.Contains(foundRunner.Doc, "Executes worker tasks asynchronously.") {
		t.Errorf("expected class docstring, got: %q", foundRunner.Doc)
	}
	if !strings.Contains(foundRunner.Signature, "@dataclass @observable class TaskRunner(BaseRunner)") {
		t.Errorf("unexpected class signature: %s", foundRunner.Signature)
	}
	if len(foundRunner.Children) != 2 {
		t.Fatalf("expected 2 methods in TaskRunner, got %d", len(foundRunner.Children))
	}
	if foundRunner.Children[0].Name != "run_job" || foundRunner.Children[0].Kind != SymbolKindMethod {
		t.Errorf("unexpected first method: %+v", foundRunner.Children[0])
	}
	if !strings.Contains(foundRunner.Children[0].Doc, "Runs an individual job.") {
		t.Errorf("expected method docstring, got: %q", foundRunner.Children[0].Doc)
	}
	if !strings.Contains(foundRunner.Children[0].Signature, "@retry(attempts=3) def run_job(self, task_id: str) -> bool") {
		t.Errorf("unexpected method signature: %s", foundRunner.Children[0].Signature)
	}

	if foundStandalone == nil {
		t.Fatalf("standalone_func not found")
	}
	if foundStandalone.Kind != SymbolKindFunction {
		t.Errorf("expected standalone_func kind function, got %s", foundStandalone.Kind)
	}
	if !strings.Contains(foundStandalone.Signature, "@logged def standalone_func(a: int, b: int) -> int") {
		t.Errorf("unexpected standalone signature: %s", foundStandalone.Signature)
	}
	if !strings.Contains(foundStandalone.Doc, "Helper addition.") {
		t.Errorf("expected standalone doc, got: %q", foundStandalone.Doc)
	}

	if foundFetch == nil {
		t.Fatalf("fetch_status not found")
	}
	if !strings.HasPrefix(foundFetch.Signature, "async def fetch_status()") {
		t.Errorf("unexpected async signature: %s", foundFetch.Signature)
	}
}

func TestPolyglotDriver_TypeScriptJavaScript(t *testing.T) {
	driver := NewPolyglotDriver()
	ctx := context.Background()

	tsSrc := `/**
 * Workspace manager service.
 */
export class WorkspaceManager extends BaseManager implements IManager {
    /**
     * Initializes manager instance.
     */
    constructor(private root: string) {}

    public async syncState(force: boolean): Promise<void> {
        // sync
    }

    private cleanup(): void {
        // cleanup
    }
}

/**
 * Service options configuration.
 */
export interface ServiceOptions {
    retries: number;
    timeoutMs: number;
}

export type HandlerMap = Record<string, Function>;

/**
 * Global helper function.
 */
export async function initializeAgent(id: string): Promise<boolean> {
    return true;
}

export const executeTask = async (task: Task): Promise<Result> => {
    return { ok: true };
};

const singleExprArrow = x => x * 2;
`
	outline, err := driver.GetOutline(ctx, "manager.ts", []byte(tsSrc))
	if err != nil {
		t.Fatalf("failed to get ts outline: %v", err)
	}

	if outline.Language != "typescript" {
		t.Errorf("expected typescript language, got %s", outline.Language)
	}

	var foundManager *SymbolNode
	var foundIface *SymbolNode
	var foundType *SymbolNode
	var foundInit *SymbolNode
	var foundExecute *SymbolNode
	var foundSingle *SymbolNode

	for i := range outline.Symbols {
		s := &outline.Symbols[i]
		switch s.Name {
		case "WorkspaceManager":
			foundManager = s
		case "ServiceOptions":
			foundIface = s
		case "HandlerMap":
			foundType = s
		case "initializeAgent":
			foundInit = s
		case "executeTask":
			foundExecute = s
		case "singleExprArrow":
			foundSingle = s
		}
	}

	if foundManager == nil {
		t.Fatalf("WorkspaceManager class not found")
	}
	if foundManager.Kind != SymbolKindStruct {
		t.Errorf("expected class kind struct, got %s", foundManager.Kind)
	}
	if !strings.Contains(foundManager.Doc, "Workspace manager service.") {
		t.Errorf("expected class JSDoc, got: %q", foundManager.Doc)
	}
	if len(foundManager.Children) != 3 {
		t.Fatalf("expected 3 methods/constructor in WorkspaceManager, got %d", len(foundManager.Children))
	}
	if foundManager.Children[0].Name != "constructor" || foundManager.Children[0].Kind != SymbolKindFunction {
		t.Errorf("unexpected constructor node: %+v", foundManager.Children[0])
	}
	if foundManager.Children[1].Name != "syncState" || foundManager.Children[1].Kind != SymbolKindMethod {
		t.Errorf("unexpected syncState node: %+v", foundManager.Children[1])
	}

	if foundIface == nil || foundIface.Kind != SymbolKindInterface {
		t.Errorf("expected interface ServiceOptions, got %+v", foundIface)
	}
	if !strings.Contains(foundIface.Doc, "Service options configuration.") {
		t.Errorf("expected interface doc: %q", foundIface.Doc)
	}

	if foundType == nil || foundType.Kind != SymbolKindType {
		t.Errorf("expected type HandlerMap, got %+v", foundType)
	}

	if foundInit == nil || foundInit.Kind != SymbolKindFunction {
		t.Errorf("expected function initializeAgent, got %+v", foundInit)
	}
	if !strings.Contains(foundInit.Doc, "Global helper function.") {
		t.Errorf("expected function doc: %q", foundInit.Doc)
	}

	if foundExecute == nil || foundExecute.Kind != SymbolKindFunction {
		t.Errorf("expected arrow function executeTask, got %+v", foundExecute)
	}

	if foundSingle == nil || foundSingle.Kind != SymbolKindFunction {
		t.Errorf("expected single expression arrow function, got %+v", foundSingle)
	}
}

func TestPolyglotDriver_Rust(t *testing.T) {
	driver := NewPolyglotDriver()
	ctx := context.Background()

	rustSrc := `/// Core engine structure.
pub struct EngineConfig {
    pub max_workers: usize,
}

/// Status enumeration.
pub enum EngineStatus {
    Starting,
    Running,
    Stopped,
}

/// Lifecycle trait.
pub trait Lifecycle {
    fn start(&mut self) -> Result<(), String>;
}

/// Implementation for EngineConfig.
impl EngineConfig {
    /// Creates a new config.
    pub fn new(workers: usize) -> Self {
        Self { max_workers: workers }
    }

    pub fn workers(&self) -> usize {
        self.max_workers
    }
}

impl Lifecycle for EngineConfig {
    fn start(&mut self) -> Result<(), String> {
        Ok(())
    }
}

/// Standalone entrypoint.
pub async fn bootstrap() -> Result<(), ()> {
    Ok(())
}
`
	outline, err := driver.GetOutline(ctx, "engine.rs", []byte(rustSrc))
	if err != nil {
		t.Fatalf("failed to get rust outline: %v", err)
	}

	if outline.Language != "rust" {
		t.Errorf("expected rust language, got %s", outline.Language)
	}

	var foundStruct *SymbolNode
	var foundEnum *SymbolNode
	var foundTrait *SymbolNode
	var foundImpl *SymbolNode
	var foundTraitImpl *SymbolNode
	var foundFn *SymbolNode

	for i := range outline.Symbols {
		s := &outline.Symbols[i]
		switch s.Name {
		case "EngineConfig":
			if s.Signature != "" && strings.HasPrefix(s.Signature, "impl") {
				foundImpl = s
			} else {
				foundStruct = s
			}
		case "EngineStatus":
			foundEnum = s
		case "Lifecycle":
			foundTrait = s
		case "Lifecycle for EngineConfig":
			foundTraitImpl = s
		case "bootstrap":
			foundFn = s
		}
	}

	if foundStruct == nil || foundStruct.Kind != SymbolKindStruct {
		t.Errorf("expected struct EngineConfig, got %+v", foundStruct)
	}
	if !strings.Contains(foundStruct.Doc, "Core engine structure.") {
		t.Errorf("expected struct doc: %q", foundStruct.Doc)
	}

	if foundEnum == nil || foundEnum.Kind != SymbolKindType {
		t.Errorf("expected enum EngineStatus, got %+v", foundEnum)
	}

	if foundTrait == nil || foundTrait.Kind != SymbolKindInterface {
		t.Errorf("expected trait Lifecycle, got %+v", foundTrait)
	}

	if foundImpl == nil || len(foundImpl.Children) != 2 {
		t.Fatalf("expected impl EngineConfig with 2 methods, got %+v", foundImpl)
	}
	if foundImpl.Children[0].Name != "new" || foundImpl.Children[0].Kind != SymbolKindMethod {
		t.Errorf("unexpected first method in impl: %+v", foundImpl.Children[0])
	}
	if !strings.Contains(foundImpl.Children[0].Doc, "Creates a new config.") {
		t.Errorf("expected method doc: %q", foundImpl.Children[0].Doc)
	}

	if foundTraitImpl == nil || len(foundTraitImpl.Children) != 1 {
		t.Fatalf("expected impl Lifecycle for EngineConfig, got %+v", foundTraitImpl)
	}

	if foundFn == nil || foundFn.Kind != SymbolKindFunction {
		t.Errorf("expected standalone fn bootstrap, got %+v", foundFn)
	}
}

func TestPolyglotDriver_Shell(t *testing.T) {
	driver := NewPolyglotDriver()
	ctx := context.Background()

	shSrc := `#!/usr/bin/env bash
# Strict execution mode
set -euo pipefail

# Deploys the fleet infrastructure
# Arguments: target host
deploy_infra() {
    echo "deploying"
}

# Verifies container health status
function check_health {
    curl -f http://localhost:8080/health
}
`
	outline, err := driver.GetOutline(ctx, "deploy.sh", []byte(shSrc))
	if err != nil {
		t.Fatalf("failed to get shell outline: %v", err)
	}

	if outline.Language != "shell" {
		t.Errorf("expected shell language, got %s", outline.Language)
	}
	if len(outline.Symbols) != 2 {
		t.Fatalf("expected 2 shell functions, got %d", len(outline.Symbols))
	}

	if outline.Symbols[0].Name != "deploy_infra" || outline.Symbols[0].Kind != SymbolKindFunction {
		t.Errorf("unexpected first shell symbol: %+v", outline.Symbols[0])
	}
	if !strings.Contains(outline.Symbols[0].Doc, "Deploys the fleet infrastructure") {
		t.Errorf("expected shell doc comment: %q", outline.Symbols[0].Doc)
	}

	if outline.Symbols[1].Name != "check_health" || outline.Symbols[1].Kind != SymbolKindFunction {
		t.Errorf("unexpected second shell symbol: %+v", outline.Symbols[1])
	}
}

func TestPolyglotDriver_DiskAndEdgeCases(t *testing.T) {
	driver := NewPolyglotDriver()
	ctx := context.Background()

	// Empty content
	emptyOutline, err := driver.GetOutline(ctx, "empty.py", []byte(""))
	if err != nil || len(emptyOutline.Symbols) != 0 {
		t.Errorf("expected empty symbols for empty file: %v", err)
	}

	// Read from disk
	tmpDir := t.TempDir()
	pyFile := filepath.Join(tmpDir, "script.py")
	_ = os.WriteFile(pyFile, []byte("def hello():\n    pass\n"), 0644)

	diskOutline, err := driver.GetOutline(ctx, pyFile, nil)
	if err != nil || len(diskOutline.Symbols) != 1 {
		t.Fatalf("expected 1 symbol from disk outline: %v", err)
	}

	// Missing file from disk
	if _, err := driver.GetOutline(ctx, filepath.Join(tmpDir, "missing.py"), nil); err == nil {
		t.Error("expected error for missing file")
	}

	// Shell basename detection (bashrc)
	bashrcOutline, err := driver.GetOutline(ctx, ".bashrc", []byte("my_prompt() {\n  echo PS1\n}\n"))
	if err != nil || bashrcOutline.Language != "shell" {
		t.Errorf("expected shell language for .bashrc, got %s", bashrcOutline.Language)
	}

	// Unknown extension routed to JS fallback
	unknownOutline, err := driver.GetOutline(ctx, "module.unknown", []byte("function test() {}"))
	if err != nil || len(unknownOutline.Symbols) != 1 {
		t.Errorf("expected 1 symbol from fallback, got %v", err)
	}
}

func TestPolyglotDriver_ValidateSyntax(t *testing.T) {
	driver := NewPolyglotDriver()
	ctx := context.Background()

	// Valid python
	validPy := []byte("def add(a, b):\n    return a + b\n")
	diags, err := driver.ValidateSyntax(ctx, "test.py", validPy)
	if err != nil || len(diags) != 0 {
		t.Errorf("expected no diags for valid python, got %v", diags)
	}

	// Broken python (unclosed triple quote)
	brokenPyQuote := []byte(`"""unclosed docstring
def broken():
    pass
`)
	diags, err = driver.ValidateSyntax(ctx, "test.py", brokenPyQuote)
	if err != nil || len(diags) == 0 {
		t.Errorf("expected diagnostic for unclosed triple quote: %v", diags)
	}

	// Broken JS bracket mismatch
	brokenJS := []byte("function test() {\n    if (true) {\n        console.log('hi');\n}\n")
	diags, err = driver.ValidateSyntax(ctx, "test.js", brokenJS)
	if err != nil || len(diags) == 0 {
		t.Errorf("expected diagnostic for unmatched brace: %v", diags)
	}

	// Unmatched closing brace
	unmatchedClose := []byte("const x = 1;\n}\n")
	diags, err = driver.ValidateSyntax(ctx, "test.ts", unmatchedClose)
	if err != nil || len(diags) == 0 {
		t.Errorf("expected diagnostic for unmatched closing brace: %v", diags)
	}

	// From disk
	tmpDir := t.TempDir()
	validDisk := filepath.Join(tmpDir, "valid.rs")
	_ = os.WriteFile(validDisk, []byte("fn main() {}\n"), 0644)
	diags, err = driver.ValidateSyntax(ctx, validDisk, nil)
	if err != nil || len(diags) != 0 {
		t.Errorf("unexpected error for disk syntax check: %v", err)
	}

	// Missing file
	if _, err := driver.ValidateSyntax(ctx, filepath.Join(tmpDir, "none.rs"), nil); err == nil {
		t.Error("expected error for missing file in ValidateSyntax")
	}
}

func TestPolyglotDriver_FindDefinitionAndReferences(t *testing.T) {
	tmpDir := t.TempDir()
	pyPath := filepath.Join(tmpDir, "calc.py")
	src := `class Calculator:
    def add(self, a, b):
        return a + b

c = Calculator()
c.add(1, 2)
`
	_ = os.WriteFile(pyPath, []byte(src), 0644)

	driver := NewPolyglotDriver()
	ctx := context.Background()

	// Find def of add
	defs, err := driver.FindDefinition(ctx, pyPath, 6, 4) // "add"
	if err != nil || len(defs) == 0 {
		t.Fatalf("expected to find def of add: %v", err)
	}
	if defs[0].Line != 2 {
		t.Errorf("expected def of add at line 2, got %d", defs[0].Line)
	}

	// Non-word position
	emptyDefs, err := driver.FindDefinition(ctx, pyPath, 4, 1)
	if err != nil || len(emptyDefs) != 0 {
		t.Errorf("expected empty defs on empty line: %v", emptyDefs)
	}

	// Missing file def
	if _, err := driver.FindDefinition(ctx, filepath.Join(tmpDir, "none.py"), 1, 1); err == nil {
		t.Error("expected error for missing file in FindDefinition")
	}

	// Find references of Calculator
	refs, err := driver.FindReferences(ctx, pyPath, 5, 6)
	if err != nil || len(refs) < 2 {
		t.Errorf("expected at least 2 references to Calculator, got %d", len(refs))
	}

	// Empty references
	emptyRefs, err := driver.FindReferences(ctx, pyPath, 4, 1)
	if err != nil || len(emptyRefs) != 0 {
		t.Errorf("expected empty refs: %v", emptyRefs)
	}

	// Missing file references
	if _, err := driver.FindReferences(ctx, filepath.Join(tmpDir, "none.py"), 1, 1); err == nil {
		t.Error("expected error for missing file in FindReferences")
	}
}
