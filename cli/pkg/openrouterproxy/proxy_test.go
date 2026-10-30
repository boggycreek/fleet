// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package openrouterproxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProxy_ChatCompletions_SSE(t *testing.T) {
	apiKey := "sk-or-v1-test-key-12345"
	receivedAuth := ""
	receivedReferer := ""
	receivedTitle := ""
	receivedPath := ""

	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedReferer = r.Header.Get("HTTP-Referer")
		receivedTitle = r.Header.Get("X-Title")
		receivedPath = r.URL.Path

		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		flusher, ok := w.(http.Flusher)
		if ok {
			flusher.Flush()
		}

		chunks := []string{
			`data: {"id":"gen-1","choices":[{"delta":{"content":"Hello"}}]}`,
			`data: {"id":"gen-1","choices":[{"delta":{"content":" world!"}}]}`,
			`data: [DONE]`,
		}

		for _, chunk := range chunks {
			_, _ = fmt.Fprintf(w, "%s\n\n", chunk)
			if ok {
				flusher.Flush()
			}
		}
	}))
	defer upstreamServer.Close()

	cfg := Config{
		APIKey:      apiKey,
		UpstreamURL: upstreamServer.URL + "/api/v1",
		Port:        8080,
	}

	handler, err := NewHandler(cfg)
	if err != nil {
		t.Fatalf("NewHandler failed: %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	reqBody := `{"model":"openai/gpt-4o","messages":[{"role":"user","content":"Hi"}],"stream":true}`
	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/v1/chat/completions", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := proxyServer.Client()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Proxy request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	if receivedAuth != "Bearer "+apiKey {
		t.Errorf("Expected Authorization 'Bearer %s', got %q", apiKey, receivedAuth)
	}
	if receivedReferer != DefaultReferer {
		t.Errorf("Expected Referer %q, got %q", DefaultReferer, receivedReferer)
	}
	if receivedTitle != DefaultTitle {
		t.Errorf("Expected Title %q, got %q", DefaultTitle, receivedTitle)
	}
	if receivedPath != "/api/v1/chat/completions" {
		t.Errorf("Expected upstream path '/api/v1/chat/completions', got %q", receivedPath)
	}

	scanner := bufio.NewScanner(resp.Body)
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			lines = append(lines, line)
		}
	}

	if len(lines) < 3 {
		t.Fatalf("Expected at least 3 SSE lines, got %d (%v)", len(lines), lines)
	}
	if !strings.Contains(lines[0], "Hello") {
		t.Errorf("Expected first SSE line to contain 'Hello', got %q", lines[0])
	}
	if !strings.Contains(lines[1], "world!") {
		t.Errorf("Expected second SSE line to contain 'world!', got %q", lines[1])
	}
	if lines[2] != "data: [DONE]" {
		t.Errorf("Expected third SSE line to be 'data: [DONE]', got %q", lines[2])
	}
}

func TestProxy_ModelsEndpoint(t *testing.T) {
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"id":"openai/gpt-4o"},{"id":"anthropic/claude-3.5-sonnet"}]}`))
	}))
	defer upstreamServer.Close()

	cfg := Config{
		APIKey:      "test-key",
		UpstreamURL: upstreamServer.URL + "/api/v1",
	}

	handler, err := NewHandler(cfg)
	if err != nil {
		t.Fatalf("NewHandler failed: %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	resp, err := proxyServer.Client().Get(proxyServer.URL + "/v1/models")
	if err != nil {
		t.Fatalf("Failed GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200, got %d (%s)", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "openai/gpt-4o") {
		t.Errorf("Expected body to contain model 'openai/gpt-4o', got %s", string(body))
	}

	resp2, err := proxyServer.Client().Get(proxyServer.URL + "/models")
	if err != nil {
		t.Fatalf("Failed GET /models: %v", err)
	}
	defer resp2.Body.Close()

	body2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200 for /models, got %d (%s)", resp2.StatusCode, string(body2))
	}
}

func TestProxy_HealthChecks(t *testing.T) {
	cfg := Config{
		APIKey:      "key-present",
		UpstreamURL: "http://localhost:9999",
	}

	handler, err := NewHandler(cfg)
	if err != nil {
		t.Fatalf("NewHandler failed: %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	resp, err := proxyServer.Client().Get(proxyServer.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"openrouter_key":"configured"`) {
		t.Errorf("Expected body to indicate key configured, got %s", string(body))
	}

	resp2, err := proxyServer.Client().Get(proxyServer.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if string(body2) != "OK" {
		t.Errorf("Expected 'OK', got %q", string(body2))
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "env-key-999")
	t.Setenv("OPENROUTER_UPSTREAM_URL", "https://custom.openrouter.ai/api/v1")
	t.Setenv("PORT", "9090")
	t.Setenv("OPENROUTER_REFERER", "https://custom.ref")
	t.Setenv("OPENROUTER_TITLE", "Custom App")

	cfg := LoadConfigFromEnv()
	if cfg.APIKey != "env-key-999" {
		t.Errorf("Expected APIKey env-key-999, got %q", cfg.APIKey)
	}
	if cfg.UpstreamURL != "https://custom.openrouter.ai/api/v1" {
		t.Errorf("Expected UpstreamURL custom, got %q", cfg.UpstreamURL)
	}
	if cfg.Port != 9090 {
		t.Errorf("Expected Port 9090, got %d", cfg.Port)
	}
	if cfg.Referer != "https://custom.ref" {
		t.Errorf("Expected Referer custom, got %q", cfg.Referer)
	}
	if cfg.Title != "Custom App" {
		t.Errorf("Expected Title Custom App, got %q", cfg.Title)
	}
}

func TestProxy_InvalidUpstreamURL(t *testing.T) {
	cfg := Config{
		UpstreamURL: "http://[invalid-host-port:bad",
	}
	if _, err := NewHandler(cfg); err == nil {
		t.Errorf("Expected error for invalid upstream URL")
	}
}

func TestStartServerAndShutdown(t *testing.T) {
	cfg := Config{
		Port:        0,
		UpstreamURL: "http://localhost:8080",
	}

	ctx, cancel := context.WithCancel(context.Background())
	srv, err := StartServer(ctx, cfg)
	if err != nil {
		t.Fatalf("StartServer failed: %v", err)
	}

	cancel()
	time.Sleep(100 * time.Millisecond)
	_ = srv
}
