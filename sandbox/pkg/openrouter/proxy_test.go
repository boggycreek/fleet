// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package openrouter

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewProxy_Defaults(t *testing.T) {
	p, err := NewProxy(Config{})
	if err != nil {
		t.Fatalf("unexpected error creating proxy: %v", err)
	}
	if p.Addr() != DefaultListenAddr {
		t.Errorf("expected default listen addr %s, got %s", DefaultListenAddr, p.Addr())
	}
	if p.cfg.UpstreamURL != DefaultUpstreamURL {
		t.Errorf("expected default upstream URL %s, got %s", DefaultUpstreamURL, p.cfg.UpstreamURL)
	}
	if p.cfg.Referer != DefaultReferer {
		t.Errorf("expected default referer %s, got %s", DefaultReferer, p.cfg.Referer)
	}
	if p.cfg.Title != DefaultTitle {
		t.Errorf("expected default title %s, got %s", DefaultTitle, p.cfg.Title)
	}
}

func TestProxy_Healthz(t *testing.T) {
	p, err := NewProxy(Config{
		APIKey: "test-key-123",
	})
	if err != nil {
		t.Fatalf("failed creating proxy: %v", err)
	}

	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()
	p.server.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", rr.Code)
	}

	var res map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed decoding json: %v", err)
	}
	if res["status"] != "ok" {
		t.Errorf("expected status ok, got %v", res["status"])
	}
	if res["configured"] != true {
		t.Errorf("expected configured true, got %v", res["configured"])
	}
}

func TestProxy_MissingKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	p, err := NewProxy(Config{
		KeyFile: filepath.Join(t.TempDir(), "nonexistent.key"),
	})
	if err != nil {
		t.Fatalf("failed creating proxy: %v", err)
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	p.server.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestProxy_ForwardingAndStreaming(t *testing.T) {
	var receivedAuth, receivedReferer, receivedTitle string
	var receivedBody string

	// Upstream mock server
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedReferer = r.Header.Get("HTTP-Referer")
		receivedTitle = r.Header.Get("X-Title")

		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)

		if r.URL.Path == "/chat/completions" || r.URL.Path == "/v1/chat/completions" {
			if strings.Contains(receivedBody, `"stream":true`) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				flusher := w.(http.Flusher)
				_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n"))
				flusher.Flush()
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
				flusher.Flush()
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chat-123","choices":[{"message":{"content":"Hi"}}]}`))
			return
		}
		if r.URL.Path == "/models" || r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[{"id":"openai/gpt-4o"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer mockUpstream.Close()

	p, err := NewProxy(Config{
		UpstreamURL: mockUpstream.URL + "/v1",
		APIKey:      "sk-openrouter-secret-token",
		Referer:     "https://fleet.test",
		Title:       "Fleet Test Runner",
		Client:      mockUpstream.Client(),
	})
	if err != nil {
		t.Fatalf("failed creating proxy: %v", err)
	}

	// 1. Non-streaming completions test
	req := httptest.NewRequest("POST", "/v1/chat/completions?extra=1", strings.NewReader(`{"model":"gpt-4o","stream":false}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	p.server.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", rr.Code, rr.Body.String())
	}
	if receivedAuth != "Bearer sk-openrouter-secret-token" {
		t.Errorf("expected injected auth header, got: %s", receivedAuth)
	}
	if receivedReferer != "https://fleet.test" {
		t.Errorf("expected referer https://fleet.test, got: %s", receivedReferer)
	}
	if receivedTitle != "Fleet Test Runner" {
		t.Errorf("expected title Fleet Test Runner, got: %s", receivedTitle)
	}
	if !strings.Contains(rr.Body.String(), "chat-123") {
		t.Errorf("unexpected body: %s", rr.Body.String())
	}

	// 2. Streaming completions test
	reqStream := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","stream":true}`))
	rrStream := httptest.NewRecorder()
	p.server.Handler.ServeHTTP(rrStream, reqStream)

	if rrStream.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for stream, got %d", rrStream.Code)
	}
	if !strings.Contains(rrStream.Body.String(), "[DONE]") {
		t.Errorf("expected [DONE] in stream output, got: %s", rrStream.Body.String())
	}

	// 3. Models list test
	reqModels := httptest.NewRequest("GET", "/v1/models", nil)
	rrModels := httptest.NewRecorder()
	p.server.Handler.ServeHTTP(rrModels, reqModels)

	if rrModels.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for models, got %d", rrModels.Code)
	}
	if !strings.Contains(rrModels.Body.String(), "openai/gpt-4o") {
		t.Errorf("expected models in response, got: %s", rrModels.Body.String())
	}
}

func TestProxy_KeyFileAndSetAPIKey(t *testing.T) {
	tmpDir := t.TempDir()
	keyFile := filepath.Join(tmpDir, "openrouter.key")
	if err := os.WriteFile(keyFile, []byte("key-from-file\n"), 0600); err != nil {
		t.Fatalf("failed writing keyfile: %v", err)
	}

	p, err := NewProxy(Config{
		KeyFile: keyFile,
	})
	if err != nil {
		t.Fatalf("failed creating proxy: %v", err)
	}

	if p.GetAPIKey() != "key-from-file" {
		t.Errorf("expected key-from-file, got %s", p.GetAPIKey())
	}

	p.SetAPIKey("new-dynamic-key")
	if p.GetAPIKey() != "new-dynamic-key" {
		t.Errorf("expected new-dynamic-key, got %s", p.GetAPIKey())
	}
}

func TestProxy_UpstreamErrorAndBadURL(t *testing.T) {
	// Point to an unavailable local port
	p, err := NewProxy(Config{
		UpstreamURL: "http://127.0.0.1:59999/v1",
		APIKey:      "test-key",
	})
	if err != nil {
		t.Fatalf("failed creating proxy: %v", err)
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	p.server.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected 502 Bad Gateway, got %d", rr.Code)
	}
}

func TestProxy_StartAndShutdown(t *testing.T) {
	// Find free port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed listening: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()

	p, err := NewProxy(Config{
		ListenAddr: addr,
		APIKey:     "test-key",
	})
	if err != nil {
		t.Fatalf("failed creating proxy: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.Start()
	}()

	// Wait for server to start
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := p.Shutdown(ctx); err != nil {
		t.Fatalf("failed shutdown: %v", err)
	}

	runErr := <-errCh
	if runErr != nil && runErr != http.ErrServerClosed {
		t.Errorf("unexpected Start error: %v", runErr)
	}
}
