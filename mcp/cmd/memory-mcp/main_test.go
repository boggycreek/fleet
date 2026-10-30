// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/sandbox/mcp/pkg/memory"
	"github.com/boggycreek/sandbox/mcp/pkg/mcp"
)

func rawArgs(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func TestMCPServerLifecycleAndTools(t *testing.T) {
	tempDir := t.TempDir()
	store, err := memory.NewStore(memory.StoreConfig{RootDir: tempDir})
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}

	reqBuf := &bytes.Buffer{}
	respBuf := &bytes.Buffer{}

	server := NewMCPServer(store, reqBuf, respBuf)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Helper to write JSON-RPC message to request buffer
	sendMsg := func(id any, method string, params any) {
		pData, _ := json.Marshal(params)
		msg := JSONRPCMessage{
			JSONRPC: "2.0",
			ID:      id,
			Method:  method,
			Params:  pData,
		}
		data, _ := json.Marshal(msg)
		reqBuf.Write(data)
		reqBuf.WriteByte('\n')
	}

	// 1. initialize
	sendMsg(1, "initialize", map[string]any{})
	// 2. tools/list
	sendMsg(2, "tools/list", map[string]any{})

	// 3. store_memory
	sendMsg(3, "tools/call", mcp.CallToolParams{
		Name: "store_memory",
		Arguments: rawArgs(map[string]any{
			"pillar":         "episodic",
			"title":          "Implement Memory MCP Server",
			"tags":           []any{"mcp", "memory", 123},
			"tier1_abstract": "Implemented episodic memory MCP server.",
			"tier2_summary":  "Added store_memory, recall_memory, get_memory, and list_memories handlers.",
			"body":           "# Technical Implementation\n\nFull description of memory-mcp server implementation.",
			"id":             "mem-test-1",
		}),
	})

	// 4. list_memories
	sendMsg(4, "tools/call", mcp.CallToolParams{
		Name: "list_memories",
		Arguments: rawArgs(map[string]any{
			"pillar": "episodic",
			"tag":    "mcp",
			"limit":  float64(5),
		}),
	})

	// 5. recall_memory
	sendMsg(5, "tools/call", mcp.CallToolParams{
		Name: "recall_memory",
		Arguments: rawArgs(map[string]any{
			"query":    "memory handlers",
			"max_tier": float64(2),
			"limit":    float64(10),
		}),
	})

	// 6. get_memory by id
	sendMsg(6, "tools/call", mcp.CallToolParams{
		Name: "get_memory",
		Arguments: rawArgs(map[string]any{
			"id":       "mem-test-1",
			"max_tier": int64(3),
		}),
	})

	// 7. get_memory by path
	sendMsg(7, "tools/call", mcp.CallToolParams{
		Name: "get_memory",
		Arguments: rawArgs(map[string]any{
			"path":     "episodic/2026-10-30-implement-memory-mcp-server.md",
			"max_tier": 3,
		}),
	})

	// Run server in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ctx)
	}()

	// Allow server time to drain buffer
	time.Sleep(100 * time.Millisecond)
	cancel()

	<-errCh

	lines := strings.Split(strings.TrimSpace(respBuf.String()), "\n")
	if len(lines) < 7 {
		t.Fatalf("expected at least 7 responses, got %d:\n%s", len(lines), respBuf.String())
	}

	var initResp JSONRPCMessage
	_ = json.Unmarshal([]byte(lines[0]), &initResp)
	if initResp.Error != nil {
		t.Fatalf("initialize failed: %v", initResp.Error)
	}

	var toolsListResp JSONRPCMessage
	_ = json.Unmarshal([]byte(lines[1]), &toolsListResp)
	if toolsListResp.Error != nil {
		t.Fatalf("tools/list failed: %v", toolsListResp.Error)
	}

	var storeResp JSONRPCMessage
	_ = json.Unmarshal([]byte(lines[2]), &storeResp)
	if storeResp.Error != nil {
		t.Fatalf("store_memory failed: %v", storeResp.Error)
	}

	var listResp JSONRPCMessage
	_ = json.Unmarshal([]byte(lines[3]), &listResp)
	if listResp.Error != nil {
		t.Fatalf("list_memories failed: %v", listResp.Error)
	}

	var recallResp JSONRPCMessage
	_ = json.Unmarshal([]byte(lines[4]), &recallResp)
	if recallResp.Error != nil {
		t.Fatalf("recall_memory failed: %v", recallResp.Error)
	}

	var getResp JSONRPCMessage
	_ = json.Unmarshal([]byte(lines[5]), &getResp)
	if getResp.Error != nil {
		t.Fatalf("get_memory failed: %v", getResp.Error)
	}
}

func TestMCPServerToolErrorsAndEdgeCases(t *testing.T) {
	tempDir := t.TempDir()
	store, _ := memory.NewStore(memory.StoreConfig{RootDir: tempDir})

	server := NewMCPServer(store, nil, nil)
	ctx := context.Background()

	t.Run("store_memory missing required pillar", func(t *testing.T) {
		res := server.executeTool(ctx, mcp.CallToolParams{
			Name: "store_memory",
			Arguments: rawArgs(map[string]any{
				"tier1_abstract": "abstract",
				"tier2_summary":  "summary",
				"body":           "body",
			}),
		})
		if !res.IsError {
			t.Errorf("expected error result when missing pillar")
		}
	})

	t.Run("store_memory missing abstract", func(t *testing.T) {
		res := server.executeTool(ctx, mcp.CallToolParams{
			Name: "store_memory",
			Arguments: rawArgs(map[string]any{
				"pillar":        "episodic",
				"tier2_summary": "summary",
				"body":          "body",
			}),
		})
		if !res.IsError {
			t.Errorf("expected error result when missing tier1_abstract")
		}
	})

	t.Run("store_memory missing summary", func(t *testing.T) {
		res := server.executeTool(ctx, mcp.CallToolParams{
			Name: "store_memory",
			Arguments: rawArgs(map[string]any{
				"pillar":         "episodic",
				"tier1_abstract": "abstract",
				"body":           "body",
			}),
		})
		if !res.IsError {
			t.Errorf("expected error result when missing tier2_summary")
		}
	})

	t.Run("store_memory missing body", func(t *testing.T) {
		res := server.executeTool(ctx, mcp.CallToolParams{
			Name: "store_memory",
			Arguments: rawArgs(map[string]any{
				"pillar":         "episodic",
				"tier1_abstract": "abstract",
				"tier2_summary":  "summary",
			}),
		})
		if !res.IsError {
			t.Errorf("expected error result when missing body")
		}
	})

	t.Run("store_memory invalid pillar store error", func(t *testing.T) {
		res := server.executeTool(ctx, mcp.CallToolParams{
			Name: "store_memory",
			Arguments: rawArgs(map[string]any{
				"pillar":         "invalid-pillar-name",
				"tier1_abstract": "abstract",
				"tier2_summary":  "summary",
				"body":           "body",
			}),
		})
		if !res.IsError {
			t.Errorf("expected store error for invalid pillar")
		}
	})

	t.Run("store_memory with string slice tags", func(t *testing.T) {
		res := server.executeTool(ctx, mcp.CallToolParams{
			Name: "store_memory",
			Arguments: rawArgs(map[string]any{
				"pillar":         "semantic",
				"tier1_abstract": "abstract text",
				"tier2_summary":  "summary text",
				"body":           "body text",
				"tags":           []string{"tagA", "tagB"},
			}),
		})
		if res.IsError {
			t.Fatalf("unexpected error result: %v", res)
		}
	})

	t.Run("store_memory with comma-separated tags string", func(t *testing.T) {
		res := server.executeTool(ctx, mcp.CallToolParams{
			Name: "store_memory",
			Arguments: rawArgs(map[string]any{
				"pillar":         "semantic",
				"tier1_abstract": "abstract text",
				"tier2_summary":  "summary text",
				"body":           "body text",
				"tags":           "tag1, tag2, tag3",
			}),
		})
		if res.IsError {
			t.Fatalf("unexpected error result: %v", res)
		}
	})

	t.Run("get_memory missing id and path", func(t *testing.T) {
		res := server.executeTool(ctx, mcp.CallToolParams{
			Name:      "get_memory",
			Arguments: rawArgs(map[string]any{}),
		})
		if !res.IsError {
			t.Errorf("expected error result when missing both id and path")
		}
	})

	t.Run("get_memory non-existent key", func(t *testing.T) {
		res := server.executeTool(ctx, mcp.CallToolParams{
			Name: "get_memory",
			Arguments: rawArgs(map[string]any{
				"id": "non-existent-id",
			}),
		})
		if !res.IsError {
			t.Errorf("expected error result for non-existent key")
		}
	})

	t.Run("unknown tool name", func(t *testing.T) {
		res := server.executeTool(ctx, mcp.CallToolParams{
			Name:      "unknown_tool",
			Arguments: rawArgs(map[string]any{}),
		})
		if !res.IsError {
			t.Errorf("expected error result for unknown tool")
		}
	})
}

func TestRunMain(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("MEMORY_DIR", filepath.Join(tempDir, "run_mem"))

	inBuf := &bytes.Buffer{}
	outBuf := &bytes.Buffer{}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := Run(ctx, inBuf, outBuf)
	if err != nil && !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("unexpected Run error: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(tempDir, "run_mem")); os.IsNotExist(statErr) {
		t.Errorf("expected MEMORY_DIR to be created during Run")
	}
}

func TestRunMainInvalidDir(t *testing.T) {
	// Point MEMORY_DIR to a file so os.MkdirAll fails
	tempFile := filepath.Join(t.TempDir(), "invalid_dir_file")
	_ = os.WriteFile(tempFile, []byte("data"), 0644)
	t.Setenv("MEMORY_DIR", filepath.Join(tempFile, "subdir"))

	err := Run(context.Background(), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Errorf("expected error initializing store with invalid path, got nil")
	}
}
