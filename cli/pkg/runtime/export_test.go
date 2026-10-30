// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package runtime

import (
	"context"
	"os/exec"
)

// SetExecCommandContextForTesting configures command execution mocking for unit tests in a thread-safe manner.
func SetExecCommandContextForTesting(fn func(ctx context.Context, name string, args ...string) *exec.Cmd) func() {
	if fn == nil {
		return SetDefaultExecutor(DefaultCommandExecutor{})
	}
	return SetDefaultExecutor(CommandExecutorFunc(fn))
}
