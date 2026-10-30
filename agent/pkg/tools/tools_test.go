// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package tools

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/fleet/mcp/pkg/mcp"
)

// --- Registry Tests ---

type mockTool struct {
	name        string
	description string
	params      map[string]any
	execFunc    func(ctx context.Context, args map[string]any) (string, error)
}

func (m *mockTool) Name() string               { return m.name }
func (m *mockTool) Description() string        { return m.description }
func (m *mockTool) Parameters() map[string]any { return m.params }
func (m *mockTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	if m.execFunc != nil {
		return m.execFunc(ctx, args)
	}
	return "ok", nil
}

func TestRegistry(t *testing.T) {
	reg := NewRegistry()

	// 1. Empty name
	if err := reg.Register(&mockTool{name: ""}); err == nil {
		t.Error("expected error registering empty tool name")
	}

	// 2. Success
	tool1 := &mockTool{
		name:        "test_tool",
		description: "A test tool",
		params: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"arg1": map[string]any{"type": "string"},
			},
		},
		execFunc: func(ctx context.Context, args map[string]any) (string, error) {
			val, _ := args["arg1"].(string)
			return "result: " + val, nil
		},
	}
	if err := reg.Register(tool1); err != nil {
		t.Fatalf("failed to register tool1: %v", err)
	}

	// 3. Duplicate name
	if err := reg.Register(tool1); !errors.Is(err, ErrToolAlreadyRegistered) {
		t.Errorf("expected ErrToolAlreadyRegistered, got: %v", err)
	}

	// 4. Get & List
	if got, ok := reg.Get("test_tool"); !ok || got.Name() != "test_tool" {
		t.Errorf("failed to get registered tool")
	}
	if len(reg.List()) != 1 {
		t.Errorf("expected 1 tool in list, got %d", len(reg.List()))
	}

	// 5. ToOpenAITools
	openaiTools := reg.ToOpenAITools()
	if len(openaiTools) != 1 || openaiTools[0]["type"] != "function" {
		t.Fatalf("unexpected OpenAI tools export: %+v", openaiTools)
	}

	// 6. Execute
	ctx := context.Background()
	res, err := reg.Execute(ctx, "test_tool", `{"arg1":"hello"}`)
	if err != nil || res != "result: hello" {
		t.Errorf("unexpected execute result: %q, err: %v", res, err)
	}

	// 7. Execute with empty string args
	resEmpty, err := reg.Execute(ctx, "test_tool", "")
	if err != nil || resEmpty != "result: " {
		t.Errorf("unexpected execute result on empty args: %q, err: %v", resEmpty, err)
	}

	// 8. Execute invalid JSON
	if _, err := reg.Execute(ctx, "test_tool", "{invalid-json"); err == nil {
		t.Error("expected error on invalid JSON arguments")
	}

	// 9. Execute non-existent tool
	if _, err := reg.Execute(ctx, "nonexistent", "{}"); !errors.Is(err, ErrToolNotFound) {
		t.Errorf("expected ErrToolNotFound, got: %v", err)
	}
}

// --- Builtin Tools Tests ---

func TestResolveWorkspacePath(t *testing.T) {
	tmpDir := t.TempDir()

	// Inside workspace
	p1, err := ResolveWorkspacePath(tmpDir, "file.txt")
	if err != nil || !strings.HasPrefix(p1, tmpDir) {
		t.Errorf("expected path inside workspace, got %s, err: %v", p1, err)
	}

	// Subdirectory inside workspace
	p2, err := ResolveWorkspacePath(tmpDir, "sub/dir/nested.txt")
	if err != nil || !strings.HasPrefix(p2, tmpDir) {
		t.Errorf("expected nested path inside workspace, got %s, err: %v", p2, err)
	}

	// Path traversal attempt: ../
	if _, err := ResolveWorkspacePath(tmpDir, "../secret.txt"); !errors.Is(err, ErrPathEscapesWorkspace) {
		t.Errorf("expected ErrPathEscapesWorkspace on ../, got: %v", err)
	}

	// Path traversal attempt: /etc/passwd
	if _, err := ResolveWorkspacePath(tmpDir, "/etc/passwd"); !errors.Is(err, ErrPathEscapesWorkspace) {
		t.Errorf("expected ErrPathEscapesWorkspace on absolute path outside, got: %v", err)
	}
}

func TestBuiltinWorkspaceTools(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	reg := NewRegistry()
	if err := RegisterBuiltinTools(reg, tmpDir); err != nil {
		t.Fatalf("failed to register builtin tools: %v", err)
	}

	// 1. write_file
	writeTool, _ := reg.Get("write_file")
	out, err := writeTool.Execute(ctx, map[string]any{
		"path":    "hello.txt",
		"content": "Line 1\nLine 2\nLine 3\nLine 4\n",
	})
	if err != nil || !strings.Contains(out, "Wrote") {
		t.Fatalf("write_file failed: %q, err: %v", out, err)
	}

	// write_file outside workspace
	if _, err := writeTool.Execute(ctx, map[string]any{"path": "../escape.txt", "content": "bad"}); !errors.Is(err, ErrPathEscapesWorkspace) {
		t.Errorf("expected ErrPathEscapesWorkspace on write outside, got: %v", err)
	}

	// 2. read_file
	readTool, _ := reg.Get("read_file")
	content, err := readTool.Execute(ctx, map[string]any{"path": "hello.txt"})
	if err != nil || !strings.Contains(content, "Line 1") {
		t.Fatalf("read_file failed: %q, err: %v", content, err)
	}

	// read_file with offset and limit
	sliceContent, err := readTool.Execute(ctx, map[string]any{
		"path":   "hello.txt",
		"offset": float64(2),
		"limit":  float64(2),
	})
	if err != nil || sliceContent != "Line 2\nLine 3" {
		t.Errorf("read_file slice failed: %q, err: %v", sliceContent, err)
	}

	// 3. edit_file
	editTool, _ := reg.Get("edit_file")
	editRes, err := editTool.Execute(ctx, map[string]any{
		"path":     "hello.txt",
		"old_text": "Line 2",
		"new_text": "Replaced Line 2",
	})
	if err != nil || !strings.Contains(editRes, "Successfully edited") {
		t.Fatalf("edit_file failed: %q, err: %v", editRes, err)
	}

	// Verify edit
	data, _ := os.ReadFile(filepath.Join(tmpDir, "hello.txt"))
	if !strings.Contains(string(data), "Replaced Line 2") {
		t.Errorf("file did not contain replacement: %s", string(data))
	}

	// edit_file not found
	if _, err := editTool.Execute(ctx, map[string]any{"path": "hello.txt", "old_text": "nonexistent", "new_text": "foo"}); err == nil {
		t.Error("expected error when old_text not found")
	}

	// 4. bash
	bashTool, _ := reg.Get("bash")
	bashOut, err := bashTool.Execute(ctx, map[string]any{"command": "echo 'sandbox running'"})
	if err != nil || !strings.Contains(bashOut, "sandbox running") {
		t.Fatalf("bash tool failed: %q, err: %v", bashOut, err)
	}

	// bash timeout
	_, timeoutErr := bashTool.Execute(ctx, map[string]any{
		"command":         "sleep 2",
		"timeout_seconds": float64(0.05),
	})
	if timeoutErr == nil {
		t.Error("expected timeout error from bash tool")
	}
}

// --- MCP Adapter Tests ---

func TestMCPAdapter(t *testing.T) {
	// Setup bidirectional pipe: clientStdin -> serverReader, serverWriter -> clientStdout
	serverR, clientW := io.Pipe()
	clientR, serverW := io.Pipe()

	toolsList := []mcp.Tool{
		{
			Name:        "fleet_ping",
			Description: "Send ping over fleet backplane",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"target": {Type: "string", Description: "Target agent ID"},
				},
				Required: []string{"target"},
			},
		},
	}

	server := mcp.NewServer("test-mcp", "1.0.0", serverR, serverW, toolsList, func(ctx context.Context, params mcp.CallToolParams) mcp.CallToolResult {
		args := params.ParseArguments()
		target, _ := args["target"].(string)
		if target == "fail" {
			return mcp.ErrorResult("intentional failure")
		}
		return mcp.TextResult("pong: " + target)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = server.Serve(ctx)
	}()

	client := NewMCPClient(clientR, clientW, clientW)
	defer client.Close()

	// 1. Handshake
	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("client.Initialize failed: %v", err)
	}

	// 2. Discover Tools
	discovered, err := client.DiscoverTools(ctx)
	if err != nil || len(discovered) != 1 {
		t.Fatalf("DiscoverTools failed: %v, count: %d", err, len(discovered))
	}

	tool := discovered[0]
	if tool.Name() != "fleet_ping" || tool.Description() != "Send ping over fleet backplane" {
		t.Errorf("unexpected tool attributes: %s, %s", tool.Name(), tool.Description())
	}

	// 3. Execute tool successfully
	out, err := tool.Execute(ctx, map[string]any{"target": "agent-42"})
	if err != nil || out != "pong: agent-42" {
		t.Errorf("unexpected MCP tool execution result: %q, err: %v", out, err)
	}

	// 4. Execute tool error
	_, errFail := tool.Execute(ctx, map[string]any{"target": "fail"})
	if errFail == nil || !strings.Contains(errFail.Error(), "intentional failure") {
		t.Errorf("expected intentional failure error, got: %v", errFail)
	}
}

func TestToolsParametersAndDescriptions(t *testing.T) {
	tmpDir := t.TempDir()
	reg := NewRegistry()
	RegisterBuiltinTools(reg, tmpDir)

	expectedOrder := []string{"bash", "edit_file", "list_dir", "read_file", "write_file"}
	for _, name := range expectedOrder {
		tool, ok := reg.Get(name)
		if !ok {
			t.Fatalf("tool %s not registered", name)
		}
		if tool.Description() == "" {
			t.Errorf("tool %s has empty description", name)
		}
		params := tool.Parameters()
		if params == nil || params["type"] != "object" {
			t.Errorf("tool %s returned invalid params schema: %v", name, params)
		}
	}

	// Schema conversion should succeed and contain 5 definitions in alphabetical order
	openAITools := reg.ToOpenAITools()
	if len(openAITools) != 5 {
		t.Fatalf("expected 5 openAI tools, got %d", len(openAITools))
	}
	for i, ot := range openAITools {
		fn := ot["function"].(map[string]any)
		if fn["name"] != expectedOrder[i] {
			t.Errorf("expected tool %d to be %s, got %s", i, expectedOrder[i], fn["name"])
		}
	}

	// List() should also return sorted
	listed := reg.List()
	for i, lt := range listed {
		if lt.Name() != expectedOrder[i] {
			t.Errorf("expected listed tool %d to be %s, got %s", i, expectedOrder[i], lt.Name())
		}
	}
}

func TestBuiltinToolEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	bash := &BashTool{workspaceDir: tmpDir}
	// Missing / empty command
	if _, err := bash.Execute(ctx, map[string]any{}); err == nil {
		t.Error("expected error for missing command")
	}
	if _, err := bash.Execute(ctx, map[string]any{"command": "   "}); err == nil {
		t.Error("expected error for blank command")
	}
	// Non-zero exit code
	if _, err := bash.Execute(ctx, map[string]any{"command": "exit 42"}); err == nil {
		t.Error("expected error for non-zero exit")
	}
	// Long output truncation
	out, err := bash.Execute(ctx, map[string]any{"command": "python3 -c 'print(\"A\" * 12000)' || perl -e 'print \"A\" x 12000' || printf '%12000s'"})
	if err == nil && !strings.Contains(out, "[output truncated]") {
		t.Errorf("expected output truncation marker, got len=%d", len(out))
	}

	rf := &ReadFileTool{workspaceDir: tmpDir}
	// Missing path
	if _, err := rf.Execute(ctx, map[string]any{}); err == nil {
		t.Error("expected error for missing path")
	}
	// File not found
	if _, err := rf.Execute(ctx, map[string]any{"path": "non-existent.txt"}); err == nil {
		t.Error("expected error for missing file")
	}
	// Reading with offset exceeding line count
	testFile := filepath.Join(tmpDir, "lines.txt")
	_ = os.WriteFile(testFile, []byte("1\n2\n3"), 0644)
	out, err = rf.Execute(ctx, map[string]any{"path": "lines.txt", "offset": float64(10)})
	if err != nil || out != "" {
		t.Errorf("expected empty string when offset exceeds file, got %q, err=%v", out, err)
	}
	// Reading with limit exceeding total lines
	out, err = rf.Execute(ctx, map[string]any{"path": "lines.txt", "offset": float64(2), "limit": float64(10)})
	if err != nil || out != "2\n3" {
		t.Errorf("expected lines 2 and 3, got %q, err=%v", out, err)
	}

	wf := &WriteFileTool{workspaceDir: tmpDir}
	// Missing path
	if _, err := wf.Execute(ctx, map[string]any{"content": "hello"}); err == nil {
		t.Error("expected error for missing path")
	}
	// Missing content
	if _, err := wf.Execute(ctx, map[string]any{"path": "foo.txt"}); err == nil {
		t.Error("expected error for missing content")
	}
	// Write with directory creation
	_, err = wf.Execute(ctx, map[string]any{"path": "sub/dir/test.txt", "content": "nested"})
	if err != nil {
		t.Fatalf("failed to write nested file: %v", err)
	}

	ef := &EditFileTool{workspaceDir: tmpDir}
	// Missing arguments
	if _, err := ef.Execute(ctx, map[string]any{}); err == nil {
		t.Error("expected error for missing path")
	}
	if _, err := ef.Execute(ctx, map[string]any{"path": "lines.txt"}); err == nil {
		t.Error("expected error for missing old_string")
	}
	if _, err := ef.Execute(ctx, map[string]any{"path": "lines.txt", "old_text": "1"}); err == nil {
		t.Error("expected error for missing new_text")
	}
	// File not found
	if _, err := ef.Execute(ctx, map[string]any{"path": "nope.txt", "old_text": "a", "new_text": "b"}); err == nil {
		t.Error("expected error for missing file")
	}
	// Old text not found
	if _, err := ef.Execute(ctx, map[string]any{"path": "lines.txt", "old_text": "999", "new_text": "b"}); err == nil {
		t.Error("expected error when old text not found")
	}
	// Old text ambiguous (>1 occurrences)
	_ = os.WriteFile(filepath.Join(tmpDir, "dup.txt"), []byte("apple\napple"), 0644)
	if _, err := ef.Execute(ctx, map[string]any{"path": "dup.txt", "old_text": "apple", "new_text": "banana"}); err == nil {
		t.Error("expected error for ambiguous match")
	}
}

func TestResolveWorkspacePathSymlinkEscape(t *testing.T) {
	tmpDir := t.TempDir()
	outsideDir := t.TempDir()

	symlinkPath := filepath.Join(tmpDir, "escape_link")
	_ = os.Symlink(outsideDir, symlinkPath)

	_, err := ResolveWorkspacePath(tmpDir, "escape_link/secret.txt")
	if err == nil {
		t.Error("expected ResolveWorkspacePath to reject symlink pointing outside workspace")
	}

	// Empty path error
	if _, err := ResolveWorkspacePath(tmpDir, "   "); err == nil {
		t.Error("expected error for blank path")
	}
}

func TestMCPProcessAndEdgeCases(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Process client with non-existent executable
	_, err := NewProcessMCPClient(ctx, "non-existent-binary-xyz-1234")
	if err == nil {
		t.Error("expected error for non-existent executable")
	}

	// 2. Process client with valid command (cat)
	procClient, err := NewProcessMCPClient(ctx, "cat")
	if err != nil {
		t.Fatalf("failed to spawn cat process MCP client: %v", err)
	}
	if procClient.Stderr() != "" {
		t.Errorf("expected empty stderr for clean process, got: %s", procClient.Stderr())
	}
	_ = procClient.Close()

	// 2b. Process client with stderr output on termination
	shClient, err := NewProcessMCPClient(ctx, "sh", "-c", "echo 'mcp startup fatal' >&2; exit 1")
	if err == nil {
		defer shClient.Close()
		time.Sleep(20 * time.Millisecond)
		_, reqErr := shClient.Request(ctx, "test", nil)
		if reqErr == nil || !strings.Contains(reqErr.Error(), "mcp startup fatal") {
			t.Errorf("expected stderr in request error, got: %v", reqErr)
		}
		if !strings.Contains(shClient.Stderr(), "mcp startup fatal") {
			t.Errorf("expected Stderr() to contain output, got: %s", shClient.Stderr())
		}
	}

	// 3. MCPClient Notify error on closed writer
	r, w := io.Pipe()
	_ = w.Close()
	dummyClient := NewMCPClient(r, w, w)
	defer dummyClient.Close()
	if err := dummyClient.Notify("test_method", nil); err == nil {
		t.Error("expected error notifying closed client")
	}

	// 4. MCPClient Request timeout / cancelled ctx
	cancelledCtx, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, err := dummyClient.Request(cancelledCtx, "test_method", nil); err == nil {
		t.Error("expected error requesting with cancelled context")
	}

	// 5. MCPToolAdapter schema with parameters
	adapter := &MCPToolAdapter{
		client: dummyClient,
		mcpTool: mcp.Tool{
			Name:        "sample_tool",
			Description: "A sample tool",
			InputSchema: mcp.InputSchema{
				Type: "object",
				Properties: map[string]mcp.Property{
					"key": {Type: "string"},
				},
			},
		},
	}
	if adapter.Name() != "sample_tool" || adapter.Description() != "A sample tool" {
		t.Errorf("unexpected adapter name/desc: %s, %s", adapter.Name(), adapter.Description())
	}
	p := adapter.Parameters()
	if p["type"] != "object" {
		t.Errorf("unexpected params schema: %v", p)
	}

	// 6. Test adapter with empty content result
	emptyServerR, emptyClientW := io.Pipe()
	emptyClientR, emptyServerW := io.Pipe()
	s := mcp.NewServer("empty-srv", "1.0", emptyServerR, emptyServerW, []mcp.Tool{adapter.mcpTool}, func(ctx context.Context, params mcp.CallToolParams) mcp.CallToolResult {
		return mcp.CallToolResult{Content: []mcp.ContentBlock{}}
	})
	go func() { _ = s.Serve(ctx) }()

	adapterClient := NewMCPClient(emptyClientR, emptyClientW, emptyClientW)
	defer adapterClient.Close()
	adapter.client = adapterClient

	out, err := adapter.Execute(ctx, map[string]any{})
	if err != nil || out != "" {
		t.Errorf("expected empty string and nil error, got %q, %v", out, err)
	}

	// 7. Test closePending when server pipe closes during pending request
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	abortClient := NewMCPClient(clientR, clientW, clientW)
	go func() {
		// Discard anything written to clientW so Write doesn't block
		go func() {
			buf := make([]byte, 1024)
			for {
				if _, err := serverR.Read(buf); err != nil {
					return
				}
			}
		}()
		time.Sleep(20 * time.Millisecond)
		_ = serverW.Close()
	}()
	if _, err := abortClient.Request(ctx, "aborted_call", nil); err == nil {
		t.Error("expected error when connection is aborted during pending request")
	}
	_ = abortClient.Close()

	// 8. Test Initialize and DiscoverTools error with cancelled ctx
	if err := dummyClient.Initialize(cancelledCtx); err == nil {
		t.Error("expected Initialize error on cancelled context")
	}
	if _, err := dummyClient.DiscoverTools(cancelledCtx); err == nil {
		t.Error("expected DiscoverTools error on cancelled context")
	}
}

func TestRegisterBuiltinToolsNil(t *testing.T) {
	if err := RegisterBuiltinTools(nil, "/tmp"); err == nil {
		t.Error("expected error for nil registry")
	}
}

func TestToolPathTraversalErrors(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()

	rf := &ReadFileTool{workspaceDir: tmpDir}
	if _, err := rf.Execute(ctx, map[string]any{"path": "../escape.txt"}); err == nil {
		t.Error("expected path traversal error on read_file")
	}

	wf := &WriteFileTool{workspaceDir: tmpDir}
	if _, err := wf.Execute(ctx, map[string]any{"path": "../escape.txt", "content": "bad"}); err == nil {
		t.Error("expected path traversal error on write_file")
	}

	ef := &EditFileTool{workspaceDir: tmpDir}
	if _, err := ef.Execute(ctx, map[string]any{"path": "../escape.txt", "old_text": "a", "new_text": "b"}); err == nil {
		t.Error("expected path traversal error on edit_file")
	}
}

func TestMCPClientNotifyEdgeCases(t *testing.T) {
	r, w := io.Pipe()
	client := NewMCPClient(r, w, w)
	// Notify with params while open
	go func() {
		buf := make([]byte, 1024)
		_, _ = r.Read(buf)
	}()
	if err := client.Notify("test_notify", map[string]string{"foo": "bar"}); err != nil {
		t.Errorf("expected notify success, got: %v", err)
	}

	_ = client.Close()
	// Notify when closed
	if err := client.Notify("test_notify", nil); err == nil {
		t.Error("expected error notifying closed client")
	}
}

func TestListDirTool(t *testing.T) {
	tmpDir := t.TempDir()
	ctx := context.Background()
	ld := &ListDirTool{workspaceDir: tmpDir}

	// 1. Empty directory
	out, err := ld.Execute(ctx, map[string]any{})
	if err != nil || out != "(empty directory)" {
		t.Errorf("expected empty directory message, got %q, err=%v", out, err)
	}

	// 2. Directory with file and subfolder
	_ = os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("hello world"), 0644)
	_ = os.Mkdir(filepath.Join(tmpDir, "subfolder"), 0755)

	out, err = ld.Execute(ctx, map[string]any{"path": "."})
	if err != nil {
		t.Fatalf("list_dir failed: %v", err)
	}
	if !strings.Contains(out, "[FILE] 11         file.txt") || !strings.Contains(out, "[DIR ] 0          subfolder") {
		t.Errorf("unexpected directory listing format: %s", out)
	}

	// 3. Stat non-existent path
	_, err = ld.Execute(ctx, map[string]any{"path": "does-not-exist"})
	if err == nil {
		t.Error("expected error listing non-existent directory")
	}

	// 4. Listing a regular file instead of directory
	_, err = ld.Execute(ctx, map[string]any{"path": "file.txt"})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("expected not a directory error, got: %v", err)
	}

	// 5. Escaping path traversal
	_, err = ld.Execute(ctx, map[string]any{"path": "../escape"})
	if err == nil {
		t.Error("expected traversal error on list_dir")
	}
}
