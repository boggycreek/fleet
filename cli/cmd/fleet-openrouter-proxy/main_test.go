// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestMainFunction(t *testing.T) {
	t.Setenv("PORT", "0")
	t.Setenv("OPENROUTER_API_KEY", "sk-or-main-test-key")

	done := make(chan struct{})
	go func() {
		defer close(done)
		go func() {
			time.Sleep(100 * time.Millisecond)
			p, err := os.FindProcess(os.Getpid())
			if err == nil {
				_ = p.Signal(syscall.SIGINT)
			}
		}()
		main()
	}()

	select {
	case <-done:
		// main completed gracefully
	case <-time.After(3 * time.Second):
		t.Fatalf("main did not shut down gracefully in time")
	}
}
