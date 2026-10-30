// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/boggycreek/sandbox/agent/pkg/engine"
	"github.com/boggycreek/sandbox/agent/pkg/runtime"
	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
	"github.com/boggycreek/sandbox/backplane/pkg/libbp/resp"
)

type syncWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func waitForLog(w *syncWriter, substr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(w.String(), substr) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return strings.Contains(w.String(), substr)
}

func startDummyValkey(t *testing.T) (string, func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on test port: %v", err)
	}

	done := make(chan struct{})
	var connsMu sync.Mutex
	var conns []net.Conn

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			connsMu.Lock()
			conns = append(conns, conn)
			connsMu.Unlock()

			go func(c net.Conn) {
				defer c.Close()
				reader := resp.NewReader(c)
				for {
					val, err := reader.ReadValue()
					if err != nil {
						return
					}
					arr, err := val.AsArray()
					if err != nil || len(arr) == 0 {
						_, _ = c.Write([]byte("+OK\r\n"))
						continue
					}
					cmd := strings.ToUpper(arr[0].Str)
					switch cmd {
					case "SCAN":
						_, _ = c.Write([]byte("*2\r\n$1\r\n0\r\n*0\r\n"))
					case "XREAD":
						select {
						case <-done:
							return
						case <-time.After(20 * time.Millisecond):
						}
						_, _ = c.Write([]byte("*-1\r\n"))
					case "INCR":
						_, _ = c.Write([]byte(":1\r\n"))
					case "XADD":
						_, _ = c.Write([]byte("$15\r\n1700000000000-0\r\n"))
					default:
						_, _ = c.Write([]byte("+OK\r\n"))
					}
				}
			}(conn)
		}
	}()

	cleanup := func() {
		close(done)
		_ = l.Close()
		connsMu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		connsMu.Unlock()
	}
	return l.Addr().String(), cleanup
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig([]string{})
	if err != nil {
		t.Fatalf("unexpected error loading defaults: %v", err)
	}

	if cfg.AgentName == "" {
		t.Error("expected non-empty default agent name")
	}
	if cfg.AgentRole != "General Software Engineer" {
		t.Errorf("unexpected default role: %s", cfg.AgentRole)
	}
	if cfg.LogFormat != "ndjson" {
		t.Errorf("unexpected default log format: %s", cfg.LogFormat)
	}
	if !strings.HasSuffix(cfg.StateFile, ".agent_state.json") {
		t.Errorf("unexpected default state file: %s", cfg.StateFile)
	}
}

func TestLoadConfigFlagsAndEnv(t *testing.T) {
	t.Setenv("AGENT_NAME", "env-agent")
	t.Setenv("OPENAI_MODEL", "env-model")

	flags := []string{
		"--name", "flag-agent",
		"--role", "Security Auditor",
		"--workspace", "/tmp/test-ws",
		"--state-file", "/tmp/test-ws/custom-state.json",
		"--model-url", "http://localhost:8000/v1",
		"--model-key", "secret-key",
		"--model-name", "flag-model",
		"--valkey-addr", "localhost:6379",
		"--valkey-password", "valkey-pass",
		"--mcp-binaries", "/usr/bin/tool1, /usr/bin/tool2",
		"--log-format", "text",
	}

	cfg, err := LoadConfig(flags)
	if err != nil {
		t.Fatalf("failed to parse flags: %v", err)
	}

	if cfg.AgentName != "flag-agent" {
		t.Errorf("expected flag override, got: %s", cfg.AgentName)
	}
	if cfg.AgentRole != "Security Auditor" {
		t.Errorf("unexpected role: %s", cfg.AgentRole)
	}
	if cfg.WorkspaceDir != "/tmp/test-ws" {
		t.Errorf("unexpected workspace: %s", cfg.WorkspaceDir)
	}
	if cfg.StateFile != "/tmp/test-ws/custom-state.json" {
		t.Errorf("unexpected state file: %s", cfg.StateFile)
	}
	if cfg.OpenAIBaseURL != "http://localhost:8000/v1" {
		t.Errorf("unexpected model url: %s", cfg.OpenAIBaseURL)
	}
	if cfg.OpenAIAPIKey != "secret-key" {
		t.Errorf("unexpected model key: %s", cfg.OpenAIAPIKey)
	}
	if cfg.OpenAIModel != "flag-model" {
		t.Errorf("unexpected model name: %s", cfg.OpenAIModel)
	}
	if cfg.ValkeyAddr != "localhost:6379" || cfg.ValkeyPassword != "valkey-pass" {
		t.Errorf("unexpected valkey settings: %s, %s", cfg.ValkeyAddr, cfg.ValkeyPassword)
	}
	if len(cfg.MCPBinaries) != 2 || cfg.MCPBinaries[0] != "/usr/bin/tool1" || cfg.MCPBinaries[1] != "/usr/bin/tool2" {
		t.Errorf("unexpected mcp binaries: %+v", cfg.MCPBinaries)
	}
	if cfg.LogFormat != "text" {
		t.Errorf("unexpected log format: %s", cfg.LogFormat)
	}
}

func TestLoadConfig_BackplaneEnv(t *testing.T) {
	t.Run("bp_host default port and bp_password", func(t *testing.T) {
		t.Setenv("VALKEY_ADDR", "")
		t.Setenv("VALKEY_PASSWORD", "")
		t.Setenv("BP_HOST", "valkey.example.internal")
		t.Setenv("BP_PORT", "")
		t.Setenv("BP_PASSWORD", "bp-secret")

		cfg, err := LoadConfig(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.ValkeyAddr != "valkey.example.internal:6379" {
			t.Errorf("expected valkey.example.internal:6379, got: %s", cfg.ValkeyAddr)
		}
		if cfg.ValkeyPassword != "bp-secret" {
			t.Errorf("expected bp-secret, got: %s", cfg.ValkeyPassword)
		}
	})

	t.Run("bp_host custom port", func(t *testing.T) {
		t.Setenv("VALKEY_ADDR", "")
		t.Setenv("VALKEY_PASSWORD", "")
		t.Setenv("BP_HOST", "valkey.example.internal")
		t.Setenv("BP_PORT", "6380")
		t.Setenv("BP_PASSWORD", "")

		cfg, err := LoadConfig(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.ValkeyAddr != "valkey.example.internal:6380" {
			t.Errorf("expected valkey.example.internal:6380, got: %s", cfg.ValkeyAddr)
		}
	})
}

func TestLoggerOutput(t *testing.T) {
	// 1. NDJSON logger
	var jsonBuf bytes.Buffer
	jsonLogger := NewLogger(&jsonBuf, "ndjson")
	jsonLogger.Log("INFO", "Agent initialized", map[string]any{"step": 1})

	var entry LogEntry
	if err := json.Unmarshal(jsonBuf.Bytes(), &entry); err != nil {
		t.Fatalf("failed to parse ndjson log entry: %v", err)
	}
	if entry.Level != "INFO" || entry.Message != "Agent initialized" || entry.Fields["step"].(float64) != 1 {
		t.Errorf("unexpected log entry contents: %+v", entry)
	}

	// 2. Text logger
	var textBuf bytes.Buffer
	textLogger := NewLogger(&textBuf, "text")
	textLogger.Log("WARN", "Warning alert", map[string]any{"code": 404})
	textOut := textBuf.String()
	if !strings.Contains(textOut, "WARN: Warning alert") || !strings.Contains(textOut, `"code":404`) {
		t.Errorf("unexpected text log output: %s", textOut)
	}

	// 3. Text logger without fields
	textBuf.Reset()
	textLogger.Log("INFO", "Clean message", nil)
	if !strings.Contains(textBuf.String(), "INFO: Clean message\n") {
		t.Errorf("unexpected clean text log output: %s", textBuf.String())
	}
}

func TestStartZombieReaper(t *testing.T) {
	t.Run("not PID 1 without force", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var logBuf bytes.Buffer
		logger := NewLogger(&logBuf, "ndjson")
		StartZombieReaper(ctx, logger)
	})

	t.Run("forced mode reaps managed and unmanaged", func(t *testing.T) {
		t.Setenv("FORCE_ZOMBIE_REAPER", "1")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		writer := &syncWriter{}
		reaperLogger := NewLogger(writer, "ndjson")
		StartZombieReaper(ctx, reaperLogger)

		cmd := exec.Command("true")
		if err := cmd.Start(); err != nil {
			t.Fatalf("failed to start true command: %v", err)
		}
		runtime.RegisterChildPID(cmd.Process.Pid)
		defer runtime.UnregisterChildPID(cmd.Process.Pid)

		if !waitForLog(writer, `"managed":true`, 3*time.Second) {
			t.Fatal("timed out waiting for reaper to reap managed process")
		}

		if err := runtime.WaitManagedCmd(cmd); err != nil {
			t.Fatalf("expected clean exit from WaitManagedCmd: %v", err)
		}

		cmdUnmanaged := exec.Command("true")
		if err := cmdUnmanaged.Start(); err != nil {
			t.Fatalf("failed to start unmanaged cmd: %v", err)
		}

		if !waitForLog(writer, `"managed":false`, 3*time.Second) {
			t.Fatal("timed out waiting for reaper to reap unmanaged process")
		}
	})

	t.Run("nil logger does not panic", func(t *testing.T) {
		t.Setenv("FORCE_ZOMBIE_REAPER", "1")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		StartZombieReaper(ctx, nil)
		cmd := exec.Command("true")
		if err := cmd.Start(); err != nil {
			t.Fatalf("failed to start command: %v", err)
		}
		_ = cmd.Wait()
	})
}

func TestRunCleanShutdown(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := engine.ChatCompletionResponse{
			ID: "resp-1",
			Choices: []engine.ChatChoice{
				{
					Message: engine.ChatMessage{
						Role:    "assistant",
						Content: "Standing by.",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	cfg := &Config{
		AgentName:          "test-agent",
		AgentRole:          "Tester",
		WorkspaceDir:       tmpDir,
		StateFile:          filepath.Join(tmpDir, "state.json"),
		OpenAIBaseURL:      ts.URL,
		OpenAIModel:        "test-model",
		ValkeyAddr:         "", // No Valkey for this unit test
		MCPBinaries:        []string{"/non/existent/mcp-binary-xyz"},
		CustomInstructions: "Rule 1",
		LogFormat:          "ndjson",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	writer := &syncWriter{}
	errChan := make(chan error, 1)
	go func() {
		errChan <- Run(ctx, cfg, writer)
	}()

	if !waitForLog(writer, "Starting supervisor workers", 3*time.Second) {
		t.Fatal("timed out waiting for supervisor workers to start")
	}
	cancel()

	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("unexpected error on clean shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not exit within timeout")
	}

	out := writer.String()
	if !strings.Contains(out, "Starting fleet-agent runtime") || !strings.Contains(out, "shut down cleanly") {
		t.Errorf("expected startup and shutdown logs, got:\n%s", out)
	}
}

func TestRun_ValkeyGateway(t *testing.T) {
	addr, cleanup := startDummyValkey(t)
	defer cleanup()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := engine.ChatCompletionResponse{
			ID: "resp-vg",
			Choices: []engine.ChatChoice{
				{
					Message: engine.ChatMessage{Role: "assistant", Content: "Ready."},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	cfg := &Config{
		AgentName:     "valkey-agent",
		AgentRole:     "Tester",
		WorkspaceDir:  tmpDir,
		StateFile:     filepath.Join(tmpDir, "state.json"),
		OpenAIBaseURL: ts.URL,
		OpenAIModel:   "test-model",
		ValkeyAddr:    addr,
		LogFormat:     "ndjson",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	writer := &syncWriter{}
	errChan := make(chan error, 1)
	go func() {
		errChan <- Run(ctx, cfg, writer)
	}()

	if !waitForLog(writer, "Configured Valkey gateway subsystem with verified signatures", 3*time.Second) {
		t.Fatal("timed out waiting for Valkey gateway subsystem configuration")
	}
	cancel()

	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not exit within timeout")
	}

	if !strings.Contains(writer.String(), "Configured Valkey gateway subsystem with verified signatures") {
		t.Errorf("expected gateway configured log, got: %s", writer.String())
	}
}

func TestRun_MCPFailureModes(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Script that exits with error on initialize
	failInitScript := filepath.Join(tmpDir, "fail-init.sh")
	if err := os.WriteFile(failInitScript, []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatalf("failed to write failInitScript: %v", err)
	}

	// 2. Script that initializes but fails tool discovery
	failDiscScript := filepath.Join(tmpDir, "fail-disc.sh")
	discContent := `#!/bin/sh
while read -r line; do
  case "$line" in
    *initialize*)
      echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"mock","version":"1.0"}}}'
      ;;
    *tools/list*)
      echo '{"jsonrpc":"2.0","id":2,"error":{"code":-32603,"message":"discovery failed"}}'
      ;;
  esac
done
`
	if err := os.WriteFile(failDiscScript, []byte(discContent), 0755); err != nil {
		t.Fatalf("failed to write failDiscScript: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := engine.ChatCompletionResponse{
			ID: "resp-mcp",
			Choices: []engine.ChatChoice{
				{Message: engine.ChatMessage{Role: "assistant", Content: "Ready."}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	writer := &syncWriter{}
	cfg := &Config{
		AgentName:     "mcp-failure-agent",
		AgentRole:     "Tester",
		WorkspaceDir:  tmpDir,
		StateFile:     filepath.Join(tmpDir, "state.json"),
		OpenAIBaseURL: ts.URL,
		OpenAIModel:   "test-model",
		MCPBinaries:   []string{failInitScript, failDiscScript},
		LogFormat:     "ndjson",
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- Run(ctx, cfg, writer)
	}()

	if !waitForLog(writer, "Starting supervisor workers", 3*time.Second) {
		t.Fatal("timed out waiting for supervisor workers")
	}
	cancel()

	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not exit within timeout")
	}

	logs := writer.String()
	if !strings.Contains(logs, "Failed to initialize MCP client") {
		t.Errorf("expected Failed to initialize MCP client log, got:\n%s", logs)
	}
	if !strings.Contains(logs, "Failed to discover MCP tools") {
		t.Errorf("expected Failed to discover MCP tools log, got:\n%s", logs)
	}
}

func TestRun_ValkeyAddrWithoutPort(t *testing.T) {
	tmpDir := t.TempDir()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := engine.ChatCompletionResponse{
			ID: "resp-nop",
			Choices: []engine.ChatChoice{
				{Message: engine.ChatMessage{Role: "assistant", Content: "Ready."}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	writer := &syncWriter{}
	cfg := &Config{
		AgentName:     "valkey-noport",
		AgentRole:     "Tester",
		WorkspaceDir:  tmpDir,
		StateFile:     filepath.Join(tmpDir, "state.json"),
		OpenAIBaseURL: ts.URL,
		OpenAIModel:   "test-model",
		ValkeyAddr:    "192.0.2.1", // Unreachable IP from RFC 5737 without port!
		LogFormat:     "ndjson",
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- Run(ctx, cfg, writer)
	}()

	_ = waitForLog(writer, "Failed to connect to Valkey", 500*time.Millisecond)

	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not exit within timeout")
	}

	if !strings.Contains(writer.String(), "Failed to connect to Valkey") {
		t.Errorf("expected Valkey dial failure log, got:\n%s", writer.String())
	}
}

func TestRunMainAndEdgeCases(t *testing.T) {
	// 1. NewLogger with nil writer
	nilLogger := NewLogger(nil, "ndjson")
	if nilLogger.out == nil {
		t.Error("expected default stdout writer")
	}

	// 2. LoadConfig error with invalid flag
	_, err := LoadConfig([]string{"--unrecognized-flag-xyz"})
	if err == nil {
		t.Error("expected error for unrecognized flag")
	}

	// 3. runMain with invalid flag
	err = runMain(context.Background(), []string{"--unrecognized-flag-xyz"}, io.Discard)
	if err == nil {
		t.Error("expected error from runMain with invalid flag")
	}

	// 4. Mock MCP server script
	tmpDir := t.TempDir()
	mockScript := filepath.Join(tmpDir, "mock-mcp.sh")
	scriptContent := `#!/bin/sh
while read -r line; do
  case "$line" in
    *initialize*)
      echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"mock","version":"1.0"}}}'
      ;;
    *tools/list*)
      echo '{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"mock_tool","description":"mock","inputSchema":{"type":"object"}}]}}'
      ;;
  esac
done
`
	if err := os.WriteFile(mockScript, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("failed to write mock mcp script: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := engine.ChatCompletionResponse{
			ID: "resp-mock",
			Choices: []engine.ChatChoice{
				{Message: engine.ChatMessage{Role: "assistant", Content: "Ready."}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	writer := &syncWriter{}
	args := []string{
		"--name", "test-agent-mcp",
		"--workspace", tmpDir,
		"--model-url", ts.URL,
		"--mcp-binaries", mockScript,
		"--valkey-addr", "127.0.0.1:1", // unreachable port to exercise dial error path
	}

	errChan := make(chan error, 1)
	go func() {
		errChan <- runMain(ctx, args, writer)
	}()

	if !waitForLog(writer, "Starting supervisor workers", 3*time.Second) {
		t.Fatal("timed out waiting for supervisor workers")
	}
	cancel()

	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("unexpected error running runMain with mock MCP: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runMain did not exit within timeout")
	}

	logs := writer.String()
	if !strings.Contains(logs, "Registered MCP tools") {
		t.Errorf("expected mock MCP registration log, got:\n%s", logs)
	}
	if !strings.Contains(logs, "Failed to connect to Valkey") {
		t.Errorf("expected Valkey dial failure log, got:\n%s", logs)
	}
}

func TestRunMain_Help(t *testing.T) {
	var buf bytes.Buffer
	err := runMain(context.Background(), []string{"--help"}, &buf)
	if err != nil {
		t.Fatalf("expected nil error on --help, got: %v", err)
	}
}

func TestBackplaneResponder(t *testing.T) {
	t.Run("nil receiver and empty parameters", func(t *testing.T) {
		var nilResp *backplaneResponder
		if err := nilResp.SendReply(context.Background(), "alice", "hello"); err != nil {
			t.Errorf("expected nil error on nil responder, got: %v", err)
		}
		resp := &backplaneResponder{client: nil}
		if err := resp.SendReply(context.Background(), "", "hello"); err != nil {
			t.Errorf("expected nil error on empty recipient, got: %v", err)
		}
		if err := resp.SendReply(context.Background(), "alice", ""); err != nil {
			t.Errorf("expected nil error on empty content, got: %v", err)
		}
	})

	t.Run("successful reply with active client", func(t *testing.T) {
		addr, cleanup := startDummyValkey(t)
		defer cleanup()

		host, portStr, _ := net.SplitHostPort(addr)
		port, _ := strconv.Atoi(portStr)

		bpClient, err := libbp.Dial(context.Background(), libbp.ClientConfig{
			Host:    host,
			Port:    port,
			AgentID: "test-sender",
		})
		if err != nil {
			t.Fatalf("failed to dial dummy valkey: %v", err)
		}
		defer bpClient.Close()

		resp := &backplaneResponder{client: bpClient}
		if err := resp.SendReply(context.Background(), "alice", "hello from backplane"); err != nil {
			t.Fatalf("expected nil error on SendReply, got: %v", err)
		}
	})
}
