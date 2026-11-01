// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package codeintel

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// mockPipeTransport simulates a bidirectional stdio transport between client and mock server using io.Pipe.
type mockPipeTransport struct {
	clientReader *io.PipeReader
	serverWriter *io.PipeWriter

	serverReader *io.PipeReader
	clientWriter *io.PipeWriter
}

func newMockPipeTransport() (*mockPipeTransport, *mockServerIO) {
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()

	client := &mockPipeTransport{
		clientReader: cr,
		serverWriter: sw,
		serverReader: sr,
		clientWriter: cw,
	}

	server := &mockServerIO{
		reader: sr,
		writer: sw,
	}

	return client, server
}

func (m *mockPipeTransport) Read(p []byte) (int, error) {
	return m.clientReader.Read(p)
}

func (m *mockPipeTransport) Write(p []byte) (int, error) {
	return m.clientWriter.Write(p)
}

func (m *mockPipeTransport) Close() error {
	_ = m.clientReader.Close()
	_ = m.clientWriter.Close()
	_ = m.serverReader.Close()
	_ = m.serverWriter.Close()
	return nil
}

type mockServerIO struct {
	reader io.ReadCloser
	writer io.WriteCloser
}

func (s *mockServerIO) run(handler func(req lspRequest) (any, error, bool)) {
	bufReader := bufio.NewReader(s.reader)
	for {
		msgBytes, err := readLSPMessage(bufReader)
		if err != nil {
			return
		}

		var req lspRequest
		if err := json.Unmarshal(msgBytes, &req); err != nil {
			continue
		}

		res, rerr, isNotification := handler(req)
		if isNotification || req.ID == nil {
			// No response sent back for notifications
			continue
		}

		var resp lspResponse
		resp.JSONRPC = "2.0"
		resp.ID = req.ID
		if rerr != nil {
			resp.Error = &lspError{Code: -32603, Message: rerr.Error()}
		} else {
			rawRes, _ := json.Marshal(res)
			resp.Result = rawRes
		}

		outBytes, _ := json.Marshal(resp)
		_ = writeLSPMessage(s.writer, outBytes)
	}
}

func TestLSPDriver_LifecycleAndOutline(t *testing.T) {
	clientTransport, serverIO := newMockPipeTransport()

	go serverIO.run(func(req lspRequest) (any, error, bool) {
		switch req.Method {
		case "initialize":
			return map[string]any{
				"capabilities": map[string]any{
					"documentSymbolProvider": true,
				},
			}, nil, false
		case "initialized":
			return nil, nil, true
		case "textDocument/didOpen":
			return nil, nil, true
		case "textDocument/documentSymbol":
			// Return hierarchical document symbols
			return []lspDocumentSymbol{
				{
					Name:   "Service",
					Detail: "type Service struct",
					Kind:   lspSymbolStruct,
					Range: lspRange{
						Start: lspPosition{Line: 5, Character: 0},
						End:   lspPosition{Line: 15, Character: 1},
					},
					SelectionRange: lspRange{
						Start: lspPosition{Line: 5, Character: 5},
						End:   lspPosition{Line: 5, Character: 12},
					},
					Children: []lspDocumentSymbol{
						{
							Name:   "Start",
							Detail: "func (s *Service) Start() error",
							Kind:   lspSymbolMethod,
							Range: lspRange{
								Start: lspPosition{Line: 10, Character: 0},
								End:   lspPosition{Line: 12, Character: 1},
							},
						},
					},
				},
			}, nil, false
		case "shutdown":
			return nil, nil, false
		case "exit":
			return nil, nil, true
		default:
			return nil, fmt.Errorf("unhandled method: %s", req.Method), false
		}
	})

	driver := NewLSPDriver(LSPDriverConfig{
		Name:      "test-lsp",
		Languages: []string{".go", "go"},
		TransportFactory: func() (LSPTransport, error) {
			return clientTransport, nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if driver.Name() != "test-lsp" {
		t.Errorf("expected test-lsp, got %s", driver.Name())
	}
	langs := driver.Languages()
	if len(langs) != 2 || langs[0] != ".go" {
		t.Errorf("unexpected languages: %v", langs)
	}

	outline, err := driver.GetOutline(ctx, "/path/to/main.go", []byte("package main\n\ntype Service struct{}"))
	if err != nil {
		t.Fatalf("GetOutline failed: %v", err)
	}

	if outline.Language != "go" {
		t.Errorf("expected go language, got %s", outline.Language)
	}
	if len(outline.Symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(outline.Symbols))
	}
	sym := outline.Symbols[0]
	if sym.Name != "Service" || sym.Kind != SymbolKindStruct {
		t.Errorf("unexpected symbol: %+v", sym)
	}
	if sym.Line != 6 || sym.EndLine != 16 {
		t.Errorf("unexpected symbol coordinates: line %d, endLine %d", sym.Line, sym.EndLine)
	}
	if len(sym.Children) != 1 || sym.Children[0].Name != "Start" || sym.Children[0].Kind != SymbolKindMethod {
		t.Errorf("unexpected children: %+v", sym.Children)
	}

	// Close driver gracefully
	if err := driver.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Calling Close again should be idempotent
	if err := driver.Close(); err != nil {
		t.Errorf("idempotent Close returned error: %v", err)
	}
}

func TestLSPDriver_FlatSymbolInformation(t *testing.T) {
	clientTransport, serverIO := newMockPipeTransport()

	go serverIO.run(func(req lspRequest) (any, error, bool) {
		switch req.Method {
		case "initialize":
			return map[string]any{}, nil, false
		case "initialized":
			return nil, nil, true
		case "textDocument/didOpen":
			return nil, nil, true
		case "textDocument/documentSymbol":
			// Return flat symbol information
			return []lspSymbolInformation{
				{
					Name: "ProcessJob",
					Kind: lspSymbolFunction,
					Location: lspLocation{
						URI: "file:///path/to/script.py",
						Range: lspRange{
							Start: lspPosition{Line: 20, Character: 4},
							End:   lspPosition{Line: 25, Character: 10},
						},
					},
				},
			}, nil, false
		default:
			return nil, nil, false
		}
	})

	driver := NewLSPDriver(LSPDriverConfig{
		Languages: []string{".py"},
		TransportFactory: func() (LSPTransport, error) {
			return clientTransport, nil
		},
	})
	defer driver.Close()

	ctx := context.Background()
	outline, err := driver.GetOutline(ctx, "/path/to/script.py", []byte("def ProcessJob(): pass"))
	if err != nil {
		t.Fatalf("GetOutline flat symbols failed: %v", err)
	}
	if len(outline.Symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(outline.Symbols))
	}
	s := outline.Symbols[0]
	if s.Name != "ProcessJob" || s.Kind != SymbolKindFunction {
		t.Errorf("unexpected symbol info: %+v", s)
	}
	if s.Line != 21 || s.Col != 5 {
		t.Errorf("expected 1-based line 21, col 5; got line %d, col %d", s.Line, s.Col)
	}
}

func TestLSPDriver_DefinitionAndReferences(t *testing.T) {
	clientTransport, serverIO := newMockPipeTransport()

	go serverIO.run(func(req lspRequest) (any, error, bool) {
		switch req.Method {
		case "initialize":
			return map[string]any{}, nil, false
		case "initialized":
			return nil, nil, true
		case "textDocument/definition":
			// Return array of locations
			return []lspLocation{
				{
					URI: "file:///workspace/types.go",
					Range: lspRange{
						Start: lspPosition{Line: 14, Character: 5},
						End:   lspPosition{Line: 14, Character: 15},
					},
				},
			}, nil, false
		case "textDocument/references":
			return []lspLocation{
				{
					URI: "file:///workspace/app.go",
					Range: lspRange{
						Start: lspPosition{Line: 2, Character: 10},
						End:   lspPosition{Line: 2, Character: 20},
					},
				},
				{
					URI: "file:///workspace/worker.go",
					Range: lspRange{
						Start: lspPosition{Line: 45, Character: 1},
						End:   lspPosition{Line: 45, Character: 11},
					},
				},
			}, nil, false
		default:
			return nil, nil, false
		}
	})

	driver := NewLSPDriver(LSPDriverConfig{
		TransportFactory: func() (LSPTransport, error) {
			return clientTransport, nil
		},
	})
	defer driver.Close()

	ctx := context.Background()

	// FindDefinition
	defs, err := driver.FindDefinition(ctx, "/workspace/main.go", 10, 5)
	if err != nil {
		t.Fatalf("FindDefinition failed: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1 definition location, got %d", len(defs))
	}
	if defs[0].FilePath != "/workspace/types.go" || defs[0].Line != 15 || defs[0].Col != 6 {
		t.Errorf("unexpected definition: %+v", defs[0])
	}

	// FindReferences
	refs, err := driver.FindReferences(ctx, "/workspace/main.go", 10, 5)
	if err != nil {
		t.Fatalf("FindReferences failed: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 references, got %d", len(refs))
	}
	if refs[0].FilePath != "/workspace/app.go" || refs[0].Line != 3 {
		t.Errorf("unexpected ref 0: %+v", refs[0])
	}
	if refs[1].FilePath != "/workspace/worker.go" || refs[1].Line != 46 {
		t.Errorf("unexpected ref 1: %+v", refs[1])
	}
}

func TestLSPDriver_DefinitionSingleAndLink(t *testing.T) {
	// 1. Single Location response
	t.Run("SingleLocation", func(t *testing.T) {
		clientTransport, serverIO := newMockPipeTransport()
		go serverIO.run(func(req lspRequest) (any, error, bool) {
			if req.Method == "initialize" {
				return map[string]any{}, nil, false
			}
			if req.Method == "textDocument/definition" {
				return lspLocation{
					URI: "file:///workspace/single.go",
					Range: lspRange{
						Start: lspPosition{Line: 3, Character: 2},
						End:   lspPosition{Line: 3, Character: 8},
					},
				}, nil, false
			}
			return nil, nil, false
		})

		driver := NewLSPDriver(LSPDriverConfig{
			TransportFactory: func() (LSPTransport, error) {
				return clientTransport, nil
			},
		})
		defer driver.Close()

		defs, err := driver.FindDefinition(context.Background(), "/workspace/main.go", 1, 1)
		if err != nil || len(defs) != 1 {
			t.Fatalf("expected 1 def, got %d (err: %v)", len(defs), err)
		}
		if defs[0].FilePath != "/workspace/single.go" || defs[0].Line != 4 {
			t.Errorf("unexpected single def: %+v", defs[0])
		}
	})

	// 2. LocationLink response
	t.Run("LocationLink", func(t *testing.T) {
		clientTransport, serverIO := newMockPipeTransport()
		go serverIO.run(func(req lspRequest) (any, error, bool) {
			if req.Method == "initialize" {
				return map[string]any{}, nil, false
			}
			if req.Method == "textDocument/definition" {
				return []lspLocationLink{
					{
						TargetURI: "file:///workspace/target.rs",
						TargetSelectionRange: lspRange{
							Start: lspPosition{Line: 12, Character: 4},
							End:   lspPosition{Line: 12, Character: 10},
						},
					},
				}, nil, false
			}
			return nil, nil, false
		})

		driver := NewLSPDriver(LSPDriverConfig{
			TransportFactory: func() (LSPTransport, error) {
				return clientTransport, nil
			},
		})
		defer driver.Close()

		defs, err := driver.FindDefinition(context.Background(), "/workspace/main.rs", 1, 1)
		if err != nil || len(defs) != 1 {
			t.Fatalf("expected 1 link def, got %d (err: %v)", len(defs), err)
		}
		if defs[0].FilePath != "/workspace/target.rs" || defs[0].Line != 13 || defs[0].Col != 5 {
			t.Errorf("unexpected link def: %+v", defs[0])
		}
	})
}

func TestLSPDriver_ValidateSyntaxDiagnostics(t *testing.T) {
	clientTransport, serverIO := newMockPipeTransport()

	go serverIO.run(func(req lspRequest) (any, error, bool) {
		switch req.Method {
		case "initialize":
			return map[string]any{}, nil, false
		case "initialized":
			return nil, nil, true
		case "textDocument/didOpen":
			// Send publishDiagnostics notification asynchronously
			go func() {
				time.Sleep(10 * time.Millisecond)
				diagNotify := lspRequest{
					JSONRPC: "2.0",
					Method:  "textDocument/publishDiagnostics",
					Params: lspPublishDiagnosticsParams{
						URI: "file:///workspace/broken.go",
						Diagnostics: []lspDiagnostic{
							{
								Range: lspRange{
									Start: lspPosition{Line: 8, Character: 2},
									End:   lspPosition{Line: 8, Character: 7},
								},
								Severity: 1, // Error
								Source:   "compiler",
								Code:     "syntax",
								Message:  "unexpected semicolon",
							},
							{
								Range: lspRange{
									Start: lspPosition{Line: 12, Character: 0},
									End:   lspPosition{Line: 12, Character: 5},
								},
								Severity: 2, // Warning
								Message:  "unused variable",
							},
						},
					},
				}
				bytes, _ := json.Marshal(diagNotify)
				_ = writeLSPMessage(serverIO.writer, bytes)
			}()
			return nil, nil, true
		default:
			return nil, nil, false
		}
	})

	driver := NewLSPDriver(LSPDriverConfig{
		TransportFactory: func() (LSPTransport, error) {
			return clientTransport, nil
		},
	})
	defer driver.Close()

	ctx := context.Background()
	diags, err := driver.ValidateSyntax(ctx, "/workspace/broken.go", []byte("package main\n\nfunc main() { ; }"))
	if err != nil {
		t.Fatalf("ValidateSyntax failed: %v", err)
	}

	if len(diags) != 2 {
		t.Fatalf("expected 2 diagnostics, got %d", len(diags))
	}
	if diags[0].Severity != SeverityError || diags[0].Line != 9 || diags[0].Col != 3 {
		t.Errorf("unexpected diag 0: %+v", diags[0])
	}
	if diags[1].Severity != SeverityWarning || diags[1].Line != 13 || diags[1].Col != 1 {
		t.Errorf("unexpected diag 1: %+v", diags[1])
	}
}

func TestLSPDriver_ErrorsAndTimeouts(t *testing.T) {
	// 1. Context timeout on call
	t.Run("ContextTimeout", func(t *testing.T) {
		clientTransport, serverIO := newMockPipeTransport()
		go serverIO.run(func(req lspRequest) (any, error, bool) {
			if req.Method == "initialize" {
				return map[string]any{}, nil, false
			}
			// Hang on definition
			time.Sleep(500 * time.Millisecond)
			return nil, nil, false
		})

		driver := NewLSPDriver(LSPDriverConfig{
			TransportFactory: func() (LSPTransport, error) {
				return clientTransport, nil
			},
		})
		defer driver.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		_, err := driver.FindDefinition(ctx, "/workspace/test.go", 1, 1)
		if err == nil {
			t.Error("expected context deadline error, got nil")
		}
	})

	// 2. JSON-RPC error from server
	t.Run("JSONRPCError", func(t *testing.T) {
		clientTransport, serverIO := newMockPipeTransport()
		go serverIO.run(func(req lspRequest) (any, error, bool) {
			if req.Method == "initialize" {
				return nil, fmt.Errorf("initialization rejected"), false
			}
			return nil, nil, false
		})

		driver := NewLSPDriver(LSPDriverConfig{
			TransportFactory: func() (LSPTransport, error) {
				return clientTransport, nil
			},
		})
		defer driver.Close()

		_, err := driver.GetOutline(context.Background(), "/workspace/test.go", []byte("content"))
		if err == nil {
			t.Error("expected error from rejected initialization")
		}
	})

	// 3. Command execution failure
	t.Run("CommandExecutionFailure", func(t *testing.T) {
		driver := NewLSPDriver(LSPDriverConfig{
			Command: "nonexistent-binary-path-12345",
		})
		_, err := driver.GetOutline(context.Background(), "/workspace/test.go", []byte("content"))
		if err == nil {
			t.Error("expected error for non-existent command")
		}
	})

	// 4. Closed driver call
	t.Run("ClosedDriverCall", func(t *testing.T) {
		driver := NewLSPDriver(LSPDriverConfig{
			Command: "nonexistent",
		})
		_ = driver.Close()
		_, err := driver.GetOutline(context.Background(), "/workspace/test.go", []byte("content"))
		if err == nil {
			t.Error("expected error when driver is closed")
		}
	})
}

func TestLSPDriver_ConfigIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	fleetDir := filepath.Join(tmpDir, ".fleet")
	_ = os.MkdirAll(fleetDir, 0755)

	configJSON := `{
		"lsp_servers": [
			{
				"name": "gopls",
				"command": "gopls",
				"languages": [".go"],
				"initialization_options": {
					"usePlaceholders": true
				}
			},
			{
				"name": "pyright",
				"command": "pyright-langserver",
				"args": ["--stdio"],
				"languages": [".py"]
			}
		]
	}`
	_ = os.WriteFile(filepath.Join(fleetDir, "intel.json"), []byte(configJSON), 0644)

	reg, err := LoadRegistry(tmpDir)
	if err != nil {
		t.Fatalf("LoadRegistry failed: %v", err)
	}

	pGo, ok := reg.GetProvider("service.go")
	if !ok || pGo.Name() != "gopls" {
		t.Errorf("expected gopls provider for .go, got %v", pGo)
	}

	pPy, ok := reg.GetProvider("main.py")
	if !ok || pPy.Name() != "pyright" {
		t.Errorf("expected pyright provider for .py, got %v", pPy)
	}

	// Test disabling an LSP server
	disabledJSON := `{
		"disabled": ["gopls"],
		"lsp_servers": [
			{
				"name": "gopls",
				"command": "gopls",
				"languages": [".go"]
			}
		]
	}`
	_ = os.WriteFile(filepath.Join(fleetDir, "intel.json"), []byte(disabledJSON), 0644)
	regDisabled, err := LoadRegistry(tmpDir)
	if err != nil {
		t.Fatalf("LoadRegistry failed: %v", err)
	}
	pGoDisabled, ok := regDisabled.GetProvider("service.go")
	if !ok || pGoDisabled.Name() == "gopls" {
		t.Errorf("expected gopls to be disabled, got %v", pGoDisabled)
	}
}

func TestLSPDriver_HelperFunctions(t *testing.T) {
	// Language IDs
	if detectLanguageID("main.rs") != "rust" {
		t.Errorf("expected rust")
	}
	if detectLanguageID("app.js") != "javascript" {
		t.Errorf("expected javascript")
	}
	if detectLanguageID("app.ts") != "typescript" {
		t.Errorf("expected typescript")
	}
	if detectLanguageID("app.json") != "json" {
		t.Errorf("expected json")
	}
	if detectLanguageID("app.c") != "c" {
		t.Errorf("expected c")
	}
	if detectLanguageID("app.cpp") != "cpp" {
		t.Errorf("expected cpp")
	}
	if detectLanguageID("app.unknown") != "unknown" {
		t.Errorf("expected unknown")
	}

	// URI conversions
	uri := pathToURI("/my/project/file.go")
	path := uriToPath(uri)
	if !filepath.IsAbs(path) {
		t.Errorf("expected absolute path, got %s", path)
	}
	if uriToPath("not-a-uri") != "not-a-uri" {
		t.Errorf("expected raw string for non-uri")
	}

	// Kind mappings
	if mapLSPKind(lspSymbolMethod) != SymbolKindMethod {
		t.Errorf("expected method")
	}
	if mapLSPKind(lspSymbolVariable) != SymbolKindVariable {
		t.Errorf("expected variable")
	}
	if mapLSPKind(lspSymbolConstant) != SymbolKindConstant {
		t.Errorf("expected constant")
	}
	if mapLSPKind(lspSymbolInterface) != SymbolKindInterface {
		t.Errorf("expected interface")
	}
	if mapLSPKind(999) != SymbolKindType {
		t.Errorf("expected fallback type")
	}
}

func TestProcessTransport_DirectCoverage(t *testing.T) {
	cmd := exec.Command("cat")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdinpipe failed: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdoutpipe failed: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	pt := &processTransport{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
	}

	testData := []byte("hello lsp\n")
	n, err := pt.Write(testData)
	if err != nil || n != len(testData) {
		t.Errorf("write failed: %v", err)
	}

	buf := make([]byte, len(testData))
	rn, err := pt.Read(buf)
	if err != nil || rn != len(testData) {
		t.Errorf("read failed: %v", err)
	}

	if err := pt.Close(); err != nil {
		t.Errorf("close failed: %v", err)
	}
}

func TestLSPDriver_ValidateSyntaxEdgeCases(t *testing.T) {
	// 1. ValidateSyntax with file on disk
	t.Run("FromDisk", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "sample.go")
		_ = os.WriteFile(filePath, []byte("package sample\n"), 0644)

		clientTransport, serverIO := newMockPipeTransport()
		go serverIO.run(func(req lspRequest) (any, error, bool) {
			if req.Method == "initialize" {
				return map[string]any{}, nil, false
			}
			return nil, nil, true
		})

		driver := NewLSPDriver(LSPDriverConfig{
			TransportFactory: func() (LSPTransport, error) {
				return clientTransport, nil
			},
		})
		defer driver.Close()

		diags, err := driver.ValidateSyntax(context.Background(), filePath, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(diags) != 0 {
			t.Errorf("expected 0 diags, got %d", len(diags))
		}

		// Non-existent file
		_, err = driver.ValidateSyntax(context.Background(), filepath.Join(tmpDir, "missing.go"), nil)
		if err == nil {
			t.Error("expected error for missing file")
		}
	})

	// 2. ValidateSyntax with context cancellation while waiting
	t.Run("ContextCancellation", func(t *testing.T) {
		clientTransport, serverIO := newMockPipeTransport()
		go serverIO.run(func(req lspRequest) (any, error, bool) {
			if req.Method == "initialize" {
				return map[string]any{}, nil, false
			}
			return nil, nil, true
		})

		driver := NewLSPDriver(LSPDriverConfig{
			TransportFactory: func() (LSPTransport, error) {
				return clientTransport, nil
			},
		})
		defer driver.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		_, err := driver.ValidateSyntax(ctx, "/path/to/test.go", []byte("package test"))
		if err == nil {
			t.Error("expected context deadline exceeded error")
		}
	})
}

func TestLSPDriver_GetOutlineDiskAndNull(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "disk.go")
	_ = os.WriteFile(filePath, []byte("package disk\n"), 0644)

	clientTransport, serverIO := newMockPipeTransport()
	go serverIO.run(func(req lspRequest) (any, error, bool) {
		if req.Method == "initialize" {
			return map[string]any{}, nil, false
		}
		if req.Method == "textDocument/documentSymbol" {
			return nil, nil, false // null result
		}
		return nil, nil, true
	})

	driver := NewLSPDriver(LSPDriverConfig{
		TransportFactory: func() (LSPTransport, error) {
			return clientTransport, nil
		},
	})
	defer driver.Close()

	// 1. GetOutline from disk with null symbols
	outline, err := driver.GetOutline(context.Background(), filePath, nil)
	if err != nil {
		t.Fatalf("GetOutline failed: %v", err)
	}
	if len(outline.Symbols) != 0 {
		t.Errorf("expected 0 symbols for null response, got %d", len(outline.Symbols))
	}

	// 2. GetOutline missing file from disk
	_, err = driver.GetOutline(context.Background(), filepath.Join(tmpDir, "missing.go"), nil)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestNativeGoDriver_ExtraCoverage(t *testing.T) {
	driver := NewNativeGoDriver()
	ctx := context.Background()

	// 1. ValidateSyntax with parser error fallback (non-scanner)
	diags, err := driver.ValidateSyntax(ctx, "test.go", []byte("package main\nvar x = 1 2\n"))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(diags) == 0 {
		t.Error("expected diags for parser error")
	}

	// 2. FindDefinition on GenDecl type spec
	tmpDir := t.TempDir()
	fPath := filepath.Join(tmpDir, "type.go")
	_ = os.WriteFile(fPath, []byte("package main\ntype MyStruct struct{}\nfunc Run(s MyStruct) {}\n"), 0644)
	locs, err := driver.FindDefinition(ctx, fPath, 3, 12) // MyStruct parameter
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(locs) == 0 {
		t.Error("expected to find definition of MyStruct")
	}
}
