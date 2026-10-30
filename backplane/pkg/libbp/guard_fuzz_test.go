// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package libbp

import (
	"strings"
	"testing"
)

func FuzzAssertAllowed(f *testing.F) {
	// Seed corpus with valid, borderline, and potentially dangerous inputs
	f.Add("agent-1", "XADD", "agent-2:inbox MAXLEN 10 * field val")
	f.Add("agent-1", "XADD", "agent-2:out * field val")
	f.Add("agent-1", "XADD", "identity:agent-2 * field val")
	f.Add("agent-1", "SET", "identity:agent-2 payload")
	f.Add("agent-1", "HSET", "identity:agent-1 pubkey val")
	f.Add("agent-1", "HSETNX", "identity:agent-2 pubkey val")
	f.Add("agent-1", "DEL", "agent-2:inbox agent-1:out")
	f.Add("agent-1", "UNLINK", "identity:agent-2")
	f.Add("agent-1", "XTRIM", "agent-2:inbox MAXLEN 10")
	f.Add("agent-1", "XDEL", "agent-2:out 1-0")
	f.Add("admin", "SET", "identity:agent-1 payload")
	f.Add("admin", "FLUSHALL", "")
	f.Add("", "XADD", "agent-1:inbox * field val")
	f.Add("agent-1", "GET", "poll-interval")
	f.Add("agent-1", "SET", "poll-interval 120")
	f.Add("   ", "", "")
	f.Add("agent-1", "xadd", "AGENT-2:INBOX maxlen 10 * foo bar")

	f.Fuzz(func(t *testing.T, callerID, cmd, rawArgs string) {
		var args []string
		if rawArgs != "" {
			args = strings.Fields(rawArgs)
		}

		// Ensure no panics occur with any permutation of arguments
		_ = AssertAllowed(callerID, cmd, args...)
		_ = AssertAllowed(callerID, cmd)
		if len(args) > 0 {
			_ = AssertAllowed(callerID, cmd, args[0])
		}
	})
}
