// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package runtime

import (
	"context"
	"os/exec"
	"sync/atomic"
	"testing"
)

func TestDefaultCommandExecutor(t *testing.T) {
	executor := DefaultCommandExecutor{}
	ctx := context.Background()
	cmd := executor.CommandContext(ctx, "echo", "hello")
	if cmd == nil {
		t.Fatalf("expected non-nil *exec.Cmd")
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed running echo: %v", err)
	}
	if string(out) != "hello\n" {
		t.Errorf("unexpected output: %q", string(out))
	}
}

func TestCommandExecutorFunc(t *testing.T) {
	called := false
	fn := CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		called = true
		return exec.Command("true")
	})

	cmd := fn.CommandContext(context.Background(), "test")
	if !called || cmd == nil {
		t.Errorf("expected CommandExecutorFunc to be invoked")
	}
}

func TestWithCommandExecutorContextInjection(t *testing.T) {
	ctx := context.Background()

	// Default without injection
	defExec := CommandExecutorFromContext(ctx)
	if defExec == nil {
		t.Errorf("expected default executor from empty context")
	}

	// Injected custom executor
	var calledName string
	mockExec := CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		calledName = name
		return exec.Command("true")
	})

	injectedCtx := WithCommandExecutor(ctx, mockExec)
	resolved := CommandExecutorFromContext(injectedCtx)
	cmd := resolved.CommandContext(injectedCtx, "my-custom-bin")
	if cmd == nil || calledName != "my-custom-bin" {
		t.Errorf("expected resolved executor to be mockExec, calledName=%q", calledName)
	}

	// CommandContext package helper uses context executor
	cmd2 := CommandContext(injectedCtx, "another-bin")
	if cmd2 == nil || calledName != "another-bin" {
		t.Errorf("expected CommandContext to use injected executor, calledName=%q", calledName)
	}
}

func TestThreadSafeSetDefaultExecutor(t *testing.T) {
	var count int64
	mock := CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		atomic.AddInt64(&count, 1)
		return exec.Command("true")
	})

	restore := SetDefaultExecutor(mock)
	defer restore()

	exec := GetDefaultExecutor()
	_ = exec.CommandContext(context.Background(), "test")
	if atomic.LoadInt64(&count) != 1 {
		t.Errorf("expected count=1, got %d", count)
	}

	// Reset with nil sets DefaultCommandExecutor
	restoreNil := SetDefaultExecutor(nil)
	defer restoreNil()

	active := GetDefaultExecutor()
	if _, ok := active.(DefaultCommandExecutor); !ok {
		t.Errorf("expected DefaultCommandExecutor when passing nil")
	}
}

func TestSetExecCommandContextForTestingHelper(t *testing.T) {
	var invoked bool
	restore := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		invoked = true
		return exec.Command("true")
	})
	defer restore()

	cmd := CommandContext(context.Background(), "check")
	if cmd == nil || !invoked {
		t.Errorf("expected SetExecCommandContextForTesting to mock execution")
	}

	// nil restores default
	restoreNil := SetExecCommandContextForTesting(nil)
	defer restoreNil()
}

func TestParallelExecutionWithContextInjectedExecutor(t *testing.T) {
	// Demonstrates that WithCommandExecutor allows completely parallel-safe test execution
	// without any cross-test interference or global state mutation.
	t.Run("worker-A", func(t *testing.T) {
		t.Parallel()
		var aCalls int
		mockA := CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			aCalls++
			return exec.Command("true")
		})
		ctxA := WithCommandExecutor(context.Background(), mockA)

		for i := 0; i < 20; i++ {
			_ = CommandContext(ctxA, "podman", "info").Run()
		}
		if aCalls != 20 {
			t.Errorf("worker A expected 20 calls, got %d", aCalls)
		}
	})

	t.Run("worker-B", func(t *testing.T) {
		t.Parallel()
		var bCalls int
		mockB := CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			bCalls++
			return exec.Command("true")
		})
		ctxB := WithCommandExecutor(context.Background(), mockB)

		for i := 0; i < 20; i++ {
			_ = CommandContext(ctxB, "podman", "version").Run()
		}
		if bCalls != 20 {
			t.Errorf("worker B expected 20 calls, got %d", bCalls)
		}
	})
}
