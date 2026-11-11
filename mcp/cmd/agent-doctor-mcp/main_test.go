// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
	"github.com/boggycreek/fleet/mcp/pkg/mcp"
)

type mockBPClient struct {
	tellFunc func(ctx context.Context, recipient, content string) (*libbp.Message, error)
	closed   bool
}

func (m *mockBPClient) Tell(ctx context.Context, recipient, content string) (*libbp.Message, error) {
	if m.tellFunc != nil {
		return m.tellFunc(ctx, recipient, content)
	}
	return &libbp.Message{ID: "1000-0", Sender: "test-agent", Destination: recipient, Content: content}, nil
}

func (m *mockBPClient) Close() error {
	m.closed = true
	return nil
}

type mockChecker struct {
	tcpFunc  func(ctx context.Context, address string, timeout time.Duration) (time.Duration, error)
	httpFunc func(ctx context.Context, targetURL string, timeout time.Duration) (int, time.Duration, error)
}

func (m *mockChecker) CheckTCP(ctx context.Context, address string, timeout time.Duration) (time.Duration, error) {
	if m.tcpFunc != nil {
		return m.tcpFunc(ctx, address, timeout)
	}
	return 5 * time.Millisecond, nil
}

func (m *mockChecker) CheckHTTP(ctx context.Context, targetURL string, timeout time.Duration) (int, time.Duration, error) {
	if m.httpFunc != nil {
		return m.httpFunc(ctx, targetURL, timeout)
	}
	return 200, 10 * time.Millisecond, nil
}

func TestDefaultConnectivityChecker(t *testing.T) {
	t.Parallel()
	checker := &DefaultConnectivityChecker{}

	// Test CheckTCP on live listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed starting TCP listener: %v", err)
	}
	defer ln.Close()

	lat, err := checker.CheckTCP(context.Background(), ln.Addr().String(), 1*time.Second)
	if err != nil {
		t.Fatalf("expected successful TCP check, got: %v", err)
	}
	if lat < 0 {
		t.Errorf("invalid latency: %v", lat)
	}

	// Test CheckTCP on unreachable address
	_, err = checker.CheckTCP(context.Background(), "127.0.0.1:65500", 100*time.Millisecond)
	if err == nil {
		t.Errorf("expected error on unreachable TCP port")
	}

	// Test CheckHTTP on live server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	code, lat, err := checker.CheckHTTP(context.Background(), ts.URL, 1*time.Second)
	if err != nil || code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got code=%d, err=%v", code, err)
	}
	if lat < 0 {
		t.Errorf("invalid latency: %v", lat)
	}

	// Test CheckHTTP on unreachable server
	_, _, err = checker.CheckHTTP(context.Background(), "http://127.0.0.1:65500/unreachable", 100*time.Millisecond)
	if err == nil {
		t.Errorf("expected error on unreachable HTTP url")
	}

	// Test CheckHTTP with invalid URL
	_, _, err = checker.CheckHTTP(context.Background(), "://invalid-url", 100*time.Millisecond)
	if err == nil {
		t.Errorf("expected error on malformed URL")
	}
}

func TestAgentDoctor_EnvCheckConnectivity(t *testing.T) {
	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", "6379")
	t.Setenv("FORGEJO_URL", "http://127.0.0.1:3000")
	t.Setenv("LLM_GATEWAY_URL", "http://llm-gateway:11434")

	checker := &mockChecker{
		tcpFunc: func(ctx context.Context, address string, timeout time.Duration) (time.Duration, error) {
			if strings.Contains(address, "unreachable") {
				return 50 * time.Millisecond, errors.New("connection refused")
			}
			return 5 * time.Millisecond, nil
		},
		httpFunc: func(ctx context.Context, targetURL string, timeout time.Duration) (int, time.Duration, error) {
			if strings.Contains(targetURL, "llm-gateway") {
				return 0, 50 * time.Millisecond, errors.New("gateway offline")
			}
			return 200, 10 * time.Millisecond, nil
		},
	}

	server := NewMCPServer(nil, checker, &bytes.Buffer{}, &bytes.Buffer{})

	args := map[string]any{
		"timeout_ms": float64(500),
		"endpoints": []any{
			"http://custom-service:8080",
			"unreachable:9999",
		},
	}

	rawArgs, _ := json.Marshal(args)
	res := server.executeTool(context.Background(), mcp.CallToolParams{
		Name:      "env_check_connectivity",
		Arguments: rawArgs,
	})

	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content[0].Text)
	}

	var results []EndpointResult
	if err := json.Unmarshal([]byte(res.Content[0].Text), &results); err != nil {
		t.Fatalf("failed unmarshaling results: %v", err)
	}

	if len(results) < 5 {
		t.Fatalf("expected at least 5 results, got %d", len(results))
	}

	foundHTTP := false
	foundTCP := false
	foundUnreachable := false
	for _, r := range results {
		if r.Protocol == "HTTP" {
			foundHTTP = true
		}
		if r.Protocol == "TCP" {
			foundTCP = true
		}
		if r.Status == "UNREACHABLE" {
			foundUnreachable = true
		}
	}

	if !foundHTTP || !foundTCP || !foundUnreachable {
		t.Errorf("expected mixed protocols and statuses, got results: %+v", results)
	}
}

func TestAgentDoctor_EnvCheckConnectivity_FallbackGitea(t *testing.T) {
	t.Setenv("BP_HOST", "")
	t.Setenv("BP_PORT", "")
	t.Setenv("FORGEJO_URL", "")
	t.Setenv("GITEA_URL", "http://127.0.0.1:3001")
	t.Setenv("LLM_GATEWAY_URL", "")

	server := NewMCPServer(nil, nil, &bytes.Buffer{}, &bytes.Buffer{})
	res := server.executeTool(context.Background(), mcp.CallToolParams{
		Name:      "env_check_connectivity",
		Arguments: json.RawMessage("{}"),
	})

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].Text)
	}
}

func TestAgentDoctor_EnvCheckConnectivity_SecurityHardening(t *testing.T) {
	var observedTimeout time.Duration
	checker := &mockChecker{
		tcpFunc: func(ctx context.Context, address string, timeout time.Duration) (time.Duration, error) {
			observedTimeout = timeout
			if strings.Contains(address, "secretpass") {
				return 0, errors.New("connection failed to secretpass")
			}
			return 5 * time.Millisecond, nil
		},
		httpFunc: func(ctx context.Context, targetURL string, timeout time.Duration) (int, time.Duration, error) {
			observedTimeout = timeout
			if strings.Contains(targetURL, "secrettoken") {
				return 0, 0, fmt.Errorf("dial error to %s", targetURL)
			}
			return 200, 5 * time.Millisecond, nil
		},
	}

	server := NewMCPServer(nil, checker, &bytes.Buffer{}, &bytes.Buffer{})

	// Construct endpoints with credentials, oversized endpoints, control characters, and excess (>20)
	var customEndpoints []any
	customEndpoints = append(customEndpoints, "http://admin:secrettoken@internal-auth:8080/health")
	customEndpoints = append(customEndpoints, "tcp-user:secretpass@127.0.0.1:9000")
	customEndpoints = append(customEndpoints, "http://bad-control\x00char:8080")
	customEndpoints = append(customEndpoints, "http://bad-newline\nchar:8080")
	customEndpoints = append(customEndpoints, strings.Repeat("a", 2049))

	for i := 0; i < 30; i++ {
		customEndpoints = append(customEndpoints, fmt.Sprintf("http://endpoint-%d.local:80", i))
	}

	args := map[string]any{
		"timeout_ms": float64(60000), // Exceeds 30s max
		"endpoints":  customEndpoints,
	}

	rawArgs, _ := json.Marshal(args)
	res := server.executeTool(context.Background(), mcp.CallToolParams{
		Name:      "env_check_connectivity",
		Arguments: rawArgs,
	})

	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content[0].Text)
	}

	// Verify timeout clamped to 30s
	if observedTimeout != 30*time.Second {
		t.Errorf("expected timeout clamped to 30s, got: %v", observedTimeout)
	}

	var results []EndpointResult
	if err := json.Unmarshal([]byte(res.Content[0].Text), &results); err != nil {
		t.Fatalf("failed unmarshaling results: %v", err)
	}

	foundRedactedHTTP := false
	for _, r := range results {
		if strings.Contains(r.Target, "secrettoken") || strings.Contains(r.Error, "secrettoken") {
			t.Errorf("sensitive credential leaked in result: %+v", r)
		}
		if strings.Contains(r.Target, "admin:xxxxx@internal-auth:8080/health") {
			foundRedactedHTTP = true
		}
		if strings.Contains(r.Target, "bad-control") || strings.Contains(r.Target, "bad-newline") {
			t.Errorf("endpoint with control characters was not filtered out: %s", r.Target)
		}
		if len(r.Target) > 2048 {
			t.Errorf("oversized endpoint was not filtered out")
		}
	}

	if !foundRedactedHTTP {
		t.Errorf("expected redacted userinfo in target, results: %+v", results)
	}

	// 3 default endpoints (bp, forgejo/gitea if any or bp + llmGateway = 2-3) + max 20 custom = max 23
	if len(results) > 24 {
		t.Errorf("expected custom endpoints capped to 20, got total results %d", len(results))
	}
}

func TestAgentDoctor_EnvInspectResources(t *testing.T) {
	t.Setenv("BP_AGENT", "test-agent-alpha")
	t.Setenv("TEST_APP_PASSWORD", "super-secret-pw")
	t.Setenv("DB_PASS", "db-pass-123")
	t.Setenv("SSL_CERT", "cert-data")
	t.Setenv("SESSION_COOKIE", "cookie-xyz")
	t.Setenv("WEB_SESSION_ID", "sess-abc")
	t.Setenv("API_TOKEN", "bearer-token-123")
	t.Setenv("UNSAFE_KEY", "priv-key")
	t.Setenv("PLAIN_VAR", "public-val")

	server := NewMCPServer(nil, nil, &bytes.Buffer{}, &bytes.Buffer{})

	res := server.executeTool(context.Background(), mcp.CallToolParams{
		Name:      "env_inspect_resources",
		Arguments: json.RawMessage("{}"),
	})

	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].Text)
	}

	var report map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &report); err != nil {
		t.Fatalf("failed parsing json: %v", err)
	}

	envMap := report["environment"].(map[string]any)
	if envMap["TEST_APP_PASSWORD"] != "[REDACTED]" {
		t.Errorf("expected TEST_APP_PASSWORD redacted, got: %v", envMap["TEST_APP_PASSWORD"])
	}
	if envMap["DB_PASS"] != "[REDACTED]" {
		t.Errorf("expected DB_PASS redacted, got: %v", envMap["DB_PASS"])
	}
	if envMap["SSL_CERT"] != "[REDACTED]" {
		t.Errorf("expected SSL_CERT redacted, got: %v", envMap["SSL_CERT"])
	}
	if envMap["SESSION_COOKIE"] != "[REDACTED]" {
		t.Errorf("expected SESSION_COOKIE redacted, got: %v", envMap["SESSION_COOKIE"])
	}
	if envMap["WEB_SESSION_ID"] != "[REDACTED]" {
		t.Errorf("expected WEB_SESSION_ID redacted, got: %v", envMap["WEB_SESSION_ID"])
	}
	if envMap["API_TOKEN"] != "[REDACTED]" {
		t.Errorf("expected API_TOKEN redacted, got: %v", envMap["API_TOKEN"])
	}
	if envMap["PLAIN_VAR"] != "public-val" {
		t.Errorf("expected PLAIN_VAR preserved, got: %v", envMap["PLAIN_VAR"])
	}

	agentMap := report["agent"].(map[string]any)
	if agentMap["name"] != "test-agent-alpha" {
		t.Errorf("expected agent name test-agent-alpha, got: %v", agentMap["name"])
	}
}

func TestAgentDoctor_EnvRequestHostAssistance(t *testing.T) {
	t.Run("MissingReason", func(t *testing.T) {
		server := NewMCPServer(nil, nil, &bytes.Buffer{}, &bytes.Buffer{})
		res := server.executeTool(context.Background(), mcp.CallToolParams{
			Name:      "env_request_host_assistance",
			Arguments: json.RawMessage("{}"),
		})
		if !res.IsError || !strings.Contains(res.Content[0].Text, "Missing required argument 'reason'") {
			t.Fatalf("expected missing reason error, got: %+v", res)
		}
	})

	t.Run("NoBPClientConfigured", func(t *testing.T) {
		server := NewMCPServer(nil, nil, &bytes.Buffer{}, &bytes.Buffer{})
		res := server.executeTool(context.Background(), mcp.CallToolParams{
			Name:      "env_request_host_assistance",
			Arguments: json.RawMessage(`{"reason":"Need permission for disk resize"}`),
		})
		if !res.IsError || !strings.Contains(res.Content[0].Text, "Backplane client not configured") {
			t.Fatalf("expected unconfigured BP client error, got: %+v", res)
		}
	})

	t.Run("SuccessfulDispatchWithContext", func(t *testing.T) {
		t.Setenv("BP_AGENT", "coder-1")
		t.Setenv("HUMAN_NAME", "super-operator")

		dispatchedTo := ""
		dispatchedContent := ""
		mock := &mockBPClient{
			tellFunc: func(ctx context.Context, recipient, content string) (*libbp.Message, error) {
				dispatchedTo = recipient
				dispatchedContent = content
				return &libbp.Message{ID: "42-0", Sender: "coder-1", Destination: recipient, Content: content}, nil
			},
		}

		server := NewMCPServer(mock, nil, &bytes.Buffer{}, &bytes.Buffer{})
		res := server.executeTool(context.Background(), mcp.CallToolParams{
			Name:      "env_request_host_assistance",
			Arguments: json.RawMessage(`{"reason":"Build failure on locked file","context":"error: permission denied at /etc/locked"}`),
		})

		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content[0].Text)
		}

		if dispatchedTo != "super-operator" {
			t.Errorf("expected dispatchedTo super-operator, got %q", dispatchedTo)
		}
		if !strings.Contains(dispatchedContent, "Build failure on locked file") || !strings.Contains(dispatchedContent, "locked file") {
			t.Errorf("unexpected content: %s", dispatchedContent)
		}
	})

	t.Run("TellErrorHandling", func(t *testing.T) {
		t.Setenv("BP_AGENT", "")
		t.Setenv("HUMAN_NAME", "")

		mock := &mockBPClient{
			tellFunc: func(ctx context.Context, recipient, content string) (*libbp.Message, error) {
				return nil, errors.New("valkey bus disconnected")
			},
		}

		server := NewMCPServer(mock, nil, &bytes.Buffer{}, &bytes.Buffer{})
		res := server.executeTool(context.Background(), mcp.CallToolParams{
			Name:      "env_request_host_assistance",
			Arguments: json.RawMessage(`{"reason":"General assistance"}`),
		})

		if !res.IsError || !strings.Contains(res.Content[0].Text, "valkey bus disconnected") {
			t.Fatalf("expected valkey disconnected error, got: %+v", res)
		}
	})
}

func TestAgentDoctor_UnknownTool(t *testing.T) {
	server := NewMCPServer(nil, nil, &bytes.Buffer{}, &bytes.Buffer{})
	res := server.executeTool(context.Background(), mcp.CallToolParams{
		Name:      "nonexistent_tool",
		Arguments: json.RawMessage("{}"),
	})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "Unknown tool") {
		t.Fatalf("expected unknown tool error, got: %+v", res)
	}
}

func TestAgentDoctor_JSONRPCServe(t *testing.T) {
	t.Parallel()

	initMsg := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}` + "\n"
	inspectMsg := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"env_inspect_resources","arguments":{}}}` + "\n"

	in := bytes.NewBufferString(initMsg + inspectMsg)
	out := &bytes.Buffer{}

	server := NewMCPServer(nil, nil, in, out)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := server.Serve(ctx); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("server.Serve failed: %v", err)
	}

	outStr := out.String()
	if !strings.Contains(outStr, `"result"`) || !strings.Contains(outStr, `environment`) {
		t.Errorf("expected tools/call result in output, got: %s", outStr)
	}
}

func TestAgentDoctor_Run(t *testing.T) {
	// Test Run entrypoint with empty input (EOF)
	in := bytes.NewBufferString("")
	out := &bytes.Buffer{}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := Run(ctx, in, out)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Errorf("unexpected Run error: %v", err)
	}
}
