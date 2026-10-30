// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package openrouterproxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultUpstreamURL = "https://openrouter.ai/api/v1"
	DefaultPort        = 8080
	DefaultReferer     = "https://github.com/boggycreek/fleet"
	DefaultTitle       = "Fleet Agent Platform"
)

// Config holds runtime options for the OpenRouter inference gateway proxy.
type Config struct {
	APIKey      string
	UpstreamURL string
	Port        int
	Referer     string
	Title       string
}

// LoadConfigFromEnv builds a Config using environment variables with defaults.
func LoadConfigFromEnv() Config {
	upstream := os.Getenv("OPENROUTER_UPSTREAM_URL")
	if upstream == "" {
		upstream = os.Getenv("OPENROUTER_BASE_URL")
	}
	if upstream == "" {
		upstream = DefaultUpstreamURL
	}

	port := DefaultPort
	if portStr := os.Getenv("PORT"); portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p >= 0 {
			port = p
		}
	}

	referer := os.Getenv("OPENROUTER_REFERER")
	if referer == "" {
		referer = DefaultReferer
	}

	title := os.Getenv("OPENROUTER_TITLE")
	if title == "" {
		title = DefaultTitle
	}

	return Config{
		APIKey:      strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")),
		UpstreamURL: strings.TrimRight(upstream, "/"),
		Port:        port,
		Referer:     referer,
		Title:       title,
	}
}

// NewHandler constructs an http.Handler that proxies OpenAI-compatible requests to OpenRouter
// while injecting the Bearer API key and Fleet platform metadata headers.
func NewHandler(cfg Config) (http.Handler, error) {
	if cfg.UpstreamURL == "" {
		cfg.UpstreamURL = DefaultUpstreamURL
	}
	cfg.UpstreamURL = strings.TrimRight(cfg.UpstreamURL, "/")

	targetURL, err := url.Parse(cfg.UpstreamURL)
	if err != nil {
		return nil, fmt.Errorf("invalid upstream URL %q: %w", cfg.UpstreamURL, err)
	}

	if cfg.Referer == "" {
		cfg.Referer = DefaultReferer
	}
	if cfg.Title == "" {
		cfg.Title = DefaultTitle
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = targetURL.Scheme
			req.URL.Host = targetURL.Host
			req.Host = targetURL.Host

			basePath := targetURL.Path
			reqPath := req.URL.Path

			var finalPath string
			if strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(reqPath, "/v1/") {
				finalPath = basePath + strings.TrimPrefix(reqPath, "/v1")
			} else if strings.HasSuffix(basePath, "/v1") && reqPath == "/v1" {
				finalPath = basePath
			} else {
				finalPath = strings.TrimRight(basePath, "/") + "/" + strings.TrimLeft(reqPath, "/")
			}
			req.URL.Path = finalPath

			if cfg.APIKey != "" {
				req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
			}

			if req.Header.Get("HTTP-Referer") == "" {
				req.Header.Set("HTTP-Referer", cfg.Referer)
			}
			if req.Header.Get("X-Title") == "" {
				req.Header.Set("X-Title", cfg.Title)
			}
		},
		FlushInterval: 100 * time.Millisecond,
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		keyStatus := "configured"
		if cfg.APIKey == "" {
			keyStatus = "missing"
		}
		_, _ = fmt.Fprintf(w, `{"status":"ok","openrouter_key":"%s"}`, keyStatus)
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	})

	return mux, nil
}

// StartServer starts an HTTP server serving the OpenRouter reverse proxy on the specified port.
func StartServer(ctx context.Context, cfg Config) (*http.Server, error) {
	handler, err := NewHandler(cfg)
	if err != nil {
		return nil, err
	}

	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	return srv, nil
}
