// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"net"
	"os"
	"testing"
	"time"
)

func TestRun_SuccessAndShutdown(t *testing.T) {
	// Find free port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed finding free port: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, []string{"-port", addr, "-upstream", "http://127.0.0.1:59999/v1"})
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("expected clean shutdown, got error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for run to shutdown")
	}
}

func TestRun_EnvVars(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed finding free port: %v", err)
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	_ = listener.Close()

	t.Setenv("PORT", port)
	t.Setenv("OPENROUTER_UPSTREAM_URL", "http://127.0.0.1:59999/v1")
	t.Setenv("OPENROUTER_API_KEY", "env-key-test")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, nil)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("expected clean shutdown, got error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for run to shutdown")
	}
}

func TestRun_FlagParseError(t *testing.T) {
	err := run(context.Background(), []string{"-invalid-flag-xyz"})
	if err == nil {
		t.Fatal("expected error parsing invalid flag, got nil")
	}
}

func TestFlagPortHasColon(t *testing.T) {
	if !flagPortHasColon(":8080") {
		t.Error("expected true for :8080")
	}
	if flagPortHasColon("8080") {
		t.Error("expected false for 8080")
	}
}

func TestMainFunction(t *testing.T) {
	origArgs := os.Args
	origExit := exitFunc
	defer func() {
		os.Args = origArgs
		exitFunc = origExit
	}()

	var called bool
	exitFunc = func(format string, v ...any) {
		called = true
	}

	os.Args = []string{"fleet-openrouter", "-invalid-flag"}
	main()

	if !called {
		t.Error("expected exitFunc to be called on invalid flag")
	}
}
