// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizeGatewayBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name:     "empty input",
			input:    "   ",
			wantErr:  true,
		},
		{
			name:     "already normalized",
			input:    "http://127.0.0.1:11434",
			expected: "http://127.0.0.1:11434",
		},
		{
			name:     "strips trailing slash",
			input:    "http://127.0.0.1:11434/",
			expected: "http://127.0.0.1:11434",
		},
		{
			name:     "strips trailing /v1",
			input:    "http://127.0.0.1:8080/v1",
			expected: "http://127.0.0.1:8080",
		},
		{
			name:     "strips trailing /v1/models",
			input:    "http://127.0.0.1:8080/v1/models",
			expected: "http://127.0.0.1:8080",
		},
		{
			name:     "strips trailing /api/tags",
			input:    "http://127.0.0.1:11434/api/tags",
			expected: "http://127.0.0.1:11434",
		},
		{
			name:     "prepends http scheme if missing",
			input:    "localhost:52415",
			expected: "http://localhost:52415",
		},
		{
			name:     "strips query and fragment",
			input:    "https://custom-gateway.local:8000/v1?foo=bar#frag",
			expected: "https://custom-gateway.local:8000",
		},
		{
			name:    "invalid control character url",
			input:   "http://\x7f",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeGatewayBaseURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %q, got nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for input %q: %v", tc.input, err)
			}
			if got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestDetectEngine(t *testing.T) {
	tests := []struct {
		name             string
		baseURL          string
		isOllamaFallback bool
		ownedByList      []string
		serverHeader     string
		expected         string
	}{
		{
			name:             "ollama fallback",
			baseURL:          "http://127.0.0.1:9999",
			isOllamaFallback: true,
			expected:         "ollama",
		},
		{
			name:         "server header ollama",
			baseURL:      "http://127.0.0.1:9999",
			serverHeader: "Ollama/0.1.30",
			expected:     "ollama",
		},
		{
			name:         "server header llamacpp",
			baseURL:      "http://127.0.0.1:9999",
			serverHeader: "llama.cpp",
			expected:     "llamacpp",
		},
		{
			name:         "server header vllm",
			baseURL:      "http://127.0.0.1:9999",
			serverHeader: "vLLM-Server",
			expected:     "vllm",
		},
		{
			name:         "server header exo",
			baseURL:      "http://127.0.0.1:9999",
			serverHeader: "exo-runtime",
			expected:     "exo",
		},
		{
			name:         "server header colibri",
			baseURL:      "http://127.0.0.1:9999",
			serverHeader: "colibri-moe",
			expected:     "colibri",
		},
		{
			name:        "owned_by llamacpp",
			baseURL:     "http://127.0.0.1:9999",
			ownedByList: []string{"llamacpp"},
			expected:    "llamacpp",
		},
		{
			name:        "owned_by ollama",
			baseURL:     "http://127.0.0.1:9999",
			ownedByList: []string{"ollama"},
			expected:    "ollama",
		},
		{
			name:        "owned_by vllm",
			baseURL:     "http://127.0.0.1:9999",
			ownedByList: []string{"vllm"},
			expected:    "vllm",
		},
		{
			name:        "owned_by exo",
			baseURL:     "http://127.0.0.1:9999",
			ownedByList: []string{"exo"},
			expected:    "exo",
		},
		{
			name:        "owned_by colibri",
			baseURL:     "http://127.0.0.1:9999",
			ownedByList: []string{"colibri"},
			expected:    "colibri",
		},
		{
			name:     "port 11434 mapped to ollama",
			baseURL:  "http://127.0.0.1:11434",
			expected: "ollama",
		},
		{
			name:     "port 52415 mapped to exo",
			baseURL:  "http://127.0.0.1:52415",
			expected: "exo",
		},
		{
			name:     "port 8080 mapped to llamacpp",
			baseURL:  "http://127.0.0.1:8080",
			expected: "llamacpp",
		},
		{
			name:     "port 8000 mapped to colibri",
			baseURL:  "http://127.0.0.1:8000",
			expected: "colibri",
		},
		{
			name:     "unmatched port returns openai-generic",
			baseURL:  "http://127.0.0.1:12345",
			expected: "openai-generic",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := detectEngine(tc.baseURL, tc.isOllamaFallback, tc.ownedByList, tc.serverHeader)
			if got != tc.expected {
				t.Fatalf("expected engine %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestProbeGateway_OpenAISuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Server", "llama.cpp")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "meta-llama/Llama-3-8B-Instruct", "owned_by": "llamacpp"},
				{"id": "codellama/CodeLlama-7b-hf", "owned_by": "llamacpp"},
			},
		})
	}))
	defer ts.Close()

	res, err := ProbeGateway(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("unexpected probe error: %v", err)
	}
	if !res.Reachable {
		t.Fatalf("expected gateway to be reachable")
	}
	if res.Engine != "llamacpp" {
		t.Errorf("expected engine llamacpp, got %s", res.Engine)
	}
	if len(res.Models) != 2 || res.Models[0] != "meta-llama/Llama-3-8B-Instruct" {
		t.Errorf("unexpected models: %+v", res.Models)
	}
	if res.Latency <= 0 {
		t.Errorf("expected positive latency, got %v", res.Latency)
	}
}

func TestProbeGateway_OllamaFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Server", "Ollama")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{
					{"name": "llama3:latest", "model": "llama3:latest"},
					{"name": "", "model": "qwen2.5-coder:7b"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	res, err := ProbeGateway(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("unexpected probe error: %v", err)
	}
	if !res.Reachable {
		t.Fatalf("expected gateway to be reachable")
	}
	if res.Engine != "ollama" {
		t.Errorf("expected engine ollama, got %s", res.Engine)
	}
	if len(res.Models) != 2 || res.Models[0] != "llama3:latest" || res.Models[1] != "qwen2.5-coder:7b" {
		t.Errorf("unexpected models: %+v", res.Models)
	}
}

func TestProbeGateway_OllamaFallbackAfter500(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{
					{"name": "mistral:latest"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	res, err := ProbeGateway(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("unexpected probe error: %v", err)
	}
	if !res.Reachable {
		t.Fatalf("expected gateway to be reachable")
	}
	if len(res.Models) != 1 || res.Models[0] != "mistral:latest" {
		t.Errorf("unexpected models: %+v", res.Models)
	}
}

func TestProbeGateway_BothEndpointsNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts.Close()

	res, err := ProbeGateway(context.Background(), ts.URL)
	if err == nil {
		t.Fatalf("expected error when both endpoints return 404")
	}
	if res.Reachable {
		t.Fatalf("expected gateway to not be reachable")
	}
	if res.Error == "" {
		t.Fatalf("expected non-empty error message")
	}
}

func TestProbeGateway_FallbackNetworkError(t *testing.T) {
	// A server that closes connection immediately on /api/tags
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			http.NotFound(w, r)
			return
		}
		// Close hijack on /api/tags to trigger network error on fallback
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				_ = conn.Close()
				return
			}
		}
		http.Error(w, "closed", http.StatusBadGateway)
	}))
	defer ts.Close()

	res, err := ProbeGateway(context.Background(), ts.URL)
	if err == nil {
		t.Fatalf("expected fallback error")
	}
	if res.Reachable {
		t.Fatalf("expected unreachable on fallback network error")
	}
}

func TestProbeGateway_InvalidOpenAIJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("not valid json {"))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	res, err := ProbeGateway(context.Background(), ts.URL)
	if err == nil {
		t.Fatalf("expected json parsing error")
	}
	if res.Reachable {
		t.Fatalf("expected Reachable to be false on invalid JSON")
	}
}

func TestProbeGateway_InvalidOllamaJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("not valid json {"))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	res, err := ProbeGateway(context.Background(), ts.URL)
	if err == nil {
		t.Fatalf("expected json parsing error")
	}
	if res.Reachable {
		t.Fatalf("expected Reachable to be false on invalid JSON")
	}
}

func TestProbeGateway_TimeoutAndUnreachable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	res, err := ProbeGateway(ctx, ts.URL)
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if res.Reachable {
		t.Fatalf("expected gateway to be unreachable due to timeout")
	}
}

func TestProbeGateway_InvalidURL(t *testing.T) {
	res, err := ProbeGateway(context.Background(), "")
	if err == nil {
		t.Fatalf("expected error for empty URL")
	}
	if res.Reachable {
		t.Fatalf("expected unreachable for empty URL")
	}
}

func TestProbeGateway_ConnectionRefused(t *testing.T) {
	// Point to an unused local port
	res, err := ProbeGateway(context.Background(), "http://127.0.0.1:65432")
	if err == nil {
		t.Fatalf("expected connection error")
	}
	if res.Reachable {
		t.Fatalf("expected unreachable on refused connection")
	}
}

func TestProbeGateway_CustomTimeout(t *testing.T) {
	oldTimeout := GatewayProbeTimeout
	defer func() {
		GatewayProbeTimeout = oldTimeout
	}()
	GatewayProbeTimeout = 0 // test fallback to DefaultGatewayProbeTimeout

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"id": "default-timeout-model"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	res, err := ProbeGateway(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Reachable {
		t.Fatalf("expected gateway reachable")
	}
}

func TestProbeWellKnownGateways(t *testing.T) {
	var count atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"id": "wellknown-model"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	oldEndpoints := WellKnownGatewayEndpoints
	defer func() {
		WellKnownGatewayEndpoints = oldEndpoints
	}()

	WellKnownGatewayEndpoints = []string{
		ts.URL,
		"http://127.0.0.1:65431", // unreachable
		ts.URL,
	}

	results := ProbeWellKnownGateways(context.Background())
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// First should be reachable
	if !results[0].Reachable || len(results[0].Models) != 1 || results[0].Models[0] != "wellknown-model" {
		t.Errorf("unexpected result[0]: %+v", results[0])
	}
	// Second should be unreachable
	if results[1].Reachable {
		t.Errorf("expected result[1] to be unreachable: %+v", results[1])
	}
	// Third should be reachable
	if !results[2].Reachable {
		t.Errorf("expected result[2] to be reachable: %+v", results[2])
	}
}
