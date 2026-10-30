// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"os/exec"

	"github.com/boggycreek/fleet/cli/pkg/runtime"
)

// SetExecCommandContextForTesting configures command execution mocking for unit tests in a thread-safe manner.
func SetExecCommandContextForTesting(fn func(ctx context.Context, name string, args ...string) *exec.Cmd) func() {
	if fn == nil {
		return runtime.SetDefaultExecutor(runtime.DefaultCommandExecutor{})
	}
	return runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(fn))
}

// SetCommandExecutor sets the CommandExecutor for fleet commands and returns a restore function.
func SetCommandExecutor(executor runtime.CommandExecutor) func() {
	return runtime.SetDefaultExecutor(executor)
}
