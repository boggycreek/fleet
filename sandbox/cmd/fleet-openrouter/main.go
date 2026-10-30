// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/boggycreek/sandbox/pkg/openrouter"
)

var exitFunc = log.Fatalf

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		exitFunc("fleet-openrouter error: %v", err)
	}
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("fleet-openrouter", flag.ContinueOnError)
	port := fs.String("port", "", "Port to listen on (default: $PORT or 8080)")
	upstream := fs.String("upstream", "", "OpenRouter upstream URL (default: $OPENROUTER_UPSTREAM_URL)")
	keyFile := fs.String("key-file", "", "Path to OpenRouter key file (default: $OPENROUTER_KEY_FILE)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	listenPort := *port
	if listenPort == "" {
		listenPort = os.Getenv("PORT")
	}
	if listenPort == "" {
		listenPort = "8080"
	}

	listenAddr := listenPort
	if !flagPortHasColon(listenAddr) {
		listenAddr = ":" + listenAddr
	}

	upstreamURL := *upstream
	if upstreamURL == "" {
		upstreamURL = os.Getenv("OPENROUTER_UPSTREAM_URL")
	}
	if upstreamURL == "" {
		upstreamURL = openrouter.DefaultUpstreamURL
	}

	kf := *keyFile
	if kf == "" {
		kf = os.Getenv("OPENROUTER_KEY_FILE")
	}

	apiKey := os.Getenv("OPENROUTER_API_KEY")

	proxy, err := openrouter.NewProxy(openrouter.Config{
		ListenAddr:  listenAddr,
		UpstreamURL: upstreamURL,
		APIKey:      apiKey,
		KeyFile:     kf,
	})
	if err != nil {
		return fmt.Errorf("failed creating proxy: %w", err)
	}

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("Starting fleet-openrouter on %s (upstream: %s)", listenAddr, upstreamURL)
		if err := proxy.Start(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-sigCtx.Done():
		log.Println("Received termination signal, shutting down fleet-openrouter...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return proxy.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

func flagPortHasColon(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return true
		}
	}
	return false
}
