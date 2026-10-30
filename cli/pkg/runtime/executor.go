// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package runtime

import (
	"context"
	"os/exec"
	"sync"
)

// CommandExecutor abstracts execution of external processes.
type CommandExecutor interface {
	CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd
}

// DefaultCommandExecutor executes commands using the standard library os/exec.
type DefaultCommandExecutor struct{}

// CommandContext implements CommandExecutor using exec.CommandContext.
func (DefaultCommandExecutor) CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, name, args...)
}

// CommandExecutorFunc adapts a plain function to the CommandExecutor interface.
type CommandExecutorFunc func(ctx context.Context, name string, args ...string) *exec.Cmd

// CommandContext invokes the underlying function.
func (f CommandExecutorFunc) CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return f(ctx, name, args...)
}

type executorContextKey struct{}

// WithCommandExecutor returns a copy of parent ctx with the CommandExecutor embedded.
func WithCommandExecutor(ctx context.Context, executor CommandExecutor) context.Context {
	return context.WithValue(ctx, executorContextKey{}, executor)
}

var (
	defaultExecutorMu sync.RWMutex
	defaultExecutor   CommandExecutor = DefaultCommandExecutor{}
)

// GetDefaultExecutor retrieves the package-level default CommandExecutor in a thread-safe manner.
func GetDefaultExecutor() CommandExecutor {
	defaultExecutorMu.RLock()
	defer defaultExecutorMu.RUnlock()
	if defaultExecutor == nil {
		return DefaultCommandExecutor{}
	}
	return defaultExecutor
}

// SetDefaultExecutor sets the package-level default CommandExecutor in a thread-safe manner and returns a restore function.
func SetDefaultExecutor(executor CommandExecutor) func() {
	defaultExecutorMu.Lock()
	orig := defaultExecutor
	if executor == nil {
		defaultExecutor = DefaultCommandExecutor{}
	} else {
		defaultExecutor = executor
	}
	defaultExecutorMu.Unlock()
	return func() {
		defaultExecutorMu.Lock()
		defaultExecutor = orig
		defaultExecutorMu.Unlock()
	}
}

// CommandExecutorFromContext retrieves the CommandExecutor from ctx, or falls back to the package-level default.
func CommandExecutorFromContext(ctx context.Context) CommandExecutor {
	if ctx != nil {
		if exec, ok := ctx.Value(executorContextKey{}).(CommandExecutor); ok && exec != nil {
			return exec
		}
	}
	return GetDefaultExecutor()
}

// CommandContext creates an *exec.Cmd using the CommandExecutor resolved from ctx or the package default.
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return CommandExecutorFromContext(ctx).CommandContext(ctx, name, args...)
}
