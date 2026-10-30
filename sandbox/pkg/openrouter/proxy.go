// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package openrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	DefaultListenAddr  = ":8080"
	DefaultUpstreamURL = "https://openrouter.ai/api/v1"
	DefaultReferer     = "https://github.com/boggycreek/fleet"
	DefaultTitle       = "Fleet Agent Platform"
	DefaultKeyPath     = "/etc/fleet/secrets/openrouter.key"
)

// Config configures the OpenRouter inference proxy.
type Config struct {
	ListenAddr  string
	UpstreamURL string
	APIKey      string
	KeyFile     string
	Referer     string
	Title       string
	Client      *http.Client
}

// Proxy is an OpenAI-compatible reverse proxy that injects OpenRouter authentication
// credentials on the server side, ensuring untrusted agent containers never receive raw keys.
type Proxy struct {
	cfg     Config
	server  *http.Server
	client  *http.Client
	mu      sync.RWMutex
	apiKey  string
	running bool
}

// NewProxy creates a new OpenRouter reverse proxy instance.
func NewProxy(cfg Config) (*Proxy, error) {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = DefaultListenAddr
	}
	if cfg.UpstreamURL == "" {
		cfg.UpstreamURL = DefaultUpstreamURL
	}
	if cfg.Referer == "" {
		cfg.Referer = DefaultReferer
	}
	if cfg.Title == "" {
		cfg.Title = DefaultTitle
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{
			Timeout: 120 * time.Second,
		}
	}

	p := &Proxy{
		cfg:    cfg,
		client: client,
	}

	// Resolve initial API key
	p.refreshKey()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", p.handleHealth)
	mux.HandleFunc("/livez", p.handleHealth)
	mux.HandleFunc("/", p.handleProxy)

	p.server = &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	return p, nil
}

// refreshKey attempts to read the API key from config, file, or environment.
func (p *Proxy) refreshKey() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cfg.APIKey != "" {
		p.apiKey = strings.TrimSpace(p.cfg.APIKey)
		return
	}

	keyFile := p.cfg.KeyFile
	if keyFile == "" {
		keyFile = DefaultKeyPath
	}

	if data, err := os.ReadFile(keyFile); err == nil && len(data) > 0 {
		p.apiKey = strings.TrimSpace(string(data))
		return
	}

	if envKey := os.Getenv("OPENROUTER_API_KEY"); envKey != "" {
		p.apiKey = strings.TrimSpace(envKey)
	}
}

// GetAPIKey returns the currently loaded API key (thread-safe).
func (p *Proxy) GetAPIKey() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.apiKey
}

// SetAPIKey dynamically updates the API key.
func (p *Proxy) SetAPIKey(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.apiKey = strings.TrimSpace(key)
}

func (p *Proxy) handleHealth(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	hasKey := p.apiKey != ""
	p.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":     "ok",
		"service":    "fleet-openrouter-proxy",
		"configured": hasKey,
	})
}

func (p *Proxy) handleProxy(w http.ResponseWriter, r *http.Request) {
	p.refreshKey()
	apiKey := p.GetAPIKey()
	if apiKey == "" {
		writeError(w, http.StatusUnauthorized, "proxy_missing_key",
			"OpenRouter API key is not configured. Provide OPENROUTER_API_KEY or configure ~/.local/share/fleet/secrets/openrouter.key")
		return
	}

	targetBase := strings.TrimRight(p.cfg.UpstreamURL, "/")
	reqPath := r.URL.Path

	// Normalize incoming path: if incoming is /v1/chat/completions and upstream ends in /v1, don't duplicate
	if strings.HasSuffix(targetBase, "/v1") && strings.HasPrefix(reqPath, "/v1/") {
		reqPath = strings.TrimPrefix(reqPath, "/v1")
	}

	destURLStr := targetBase + reqPath
	if r.URL.RawQuery != "" {
		destURLStr += "?" + r.URL.RawQuery
	}

	destURL, err := url.Parse(destURLStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "proxy_bad_url", fmt.Sprintf("invalid destination URL: %v", err))
		return
	}

	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, destURL.String(), r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "proxy_request_error", fmt.Sprintf("failed creating request: %v", err))
		return
	}

	// Copy headers
	for k, vv := range r.Header {
		for _, v := range vv {
			outReq.Header.Add(k, v)
		}
	}

	// Inject secure OpenRouter headers
	outReq.Header.Set("Authorization", "Bearer "+apiKey)
	if p.cfg.Referer != "" {
		outReq.Header.Set("HTTP-Referer", p.cfg.Referer)
	}
	if p.cfg.Title != "" {
		outReq.Header.Set("X-Title", p.cfg.Title)
	}
	outReq.Host = destURL.Host

	resp, err := p.client.Do(outReq)
	if err != nil {
		writeError(w, http.StatusBadGateway, "proxy_upstream_error", fmt.Sprintf("upstream connection failed: %v", err))
		return
	}
	defer resp.Body.Close()

	// Forward response headers
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream responses directly (supports Server-Sent Events / SSE tokens)
	flusher, isFlusher := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, rErr := resp.Body.Read(buf)
		if n > 0 {
			_, _ = w.Write(buf[:n])
			if isFlusher {
				flusher.Flush()
			}
		}
		if rErr != nil {
			if rErr != io.EOF && rErr != context.Canceled {
				// Body stream completed or client disconnected
			}
			break
		}
	}
}

func writeError(w http.ResponseWriter, statusCode int, errType, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"message": message,
			"type":    errType,
		},
	})
}

// Start runs the HTTP server. It blocks until Shutdown is called or an error occurs.
func (p *Proxy) Start() error {
	p.mu.Lock()
	p.running = true
	p.mu.Unlock()
	return p.server.ListenAndServe()
}

// Shutdown gracefully stops the proxy.
func (p *Proxy) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	p.running = false
	p.mu.Unlock()
	return p.server.Shutdown(ctx)
}

// Addr returns the configured or active listener address.
func (p *Proxy) Addr() string {
	return p.server.Addr
}
