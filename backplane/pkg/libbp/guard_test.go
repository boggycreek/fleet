// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package libbp

import (
	"errors"
	"testing"
)

func TestGuardAllowedCommands(t *testing.T) {
	// Standard allowed commands
	allowed := []struct {
		cmd  string
		args []string
	}{
		{"XADD", []string{"agent-1:out", "*", "content", "hello"}},
		{"XADD", []string{"agent-2:inbox", "*", "content", "hello"}},
		{"XREAD", []string{"STREAMS", "agent-1:inbox", "0-0"}},
		{"INCR", []string{"agent-1:seq"}},
		{"SET", []string{"agent-1:status", "busy", "EX", "300"}},
		{"GET", []string{"agent-2:status"}},
		{"HSET", []string{"agent-1:finger", "role", "coder"}},
		{"HGETALL", []string{"agent-2:finger"}},
		{"SCAN", []string{"0", "MATCH", "*:out"}},
	}

	for _, tc := range allowed {
		if err := AssertAllowed("agent-1", tc.cmd, tc.args...); err != nil {
			t.Errorf("expected command %s %v to be allowed, got error: %v", tc.cmd, tc.args, err)
		}
	}
}

func TestGuardDangerousCommands(t *testing.T) {
	dangerous := []string{
		"EXPIRE", "PEXPIRE", "EXPIREAT", "PEXPIREAT",
		"RENAME", "RENAMENX", "MSET", "MSETNX",
		"HDEL", "XSETID", "EVAL", "EVALSHA",
		"FCALL", "FCALL_RO", "FLUSHALL", "FLUSHDB",
		"SHUTDOWN", "DEBUG", "CONFIG", "KEYS",
		"expire", "rename", "mset", "flushall", "Shutdown", "keys",
	}

	for _, cmd := range dangerous {
		err := AssertAllowed("agent-1", cmd)
		if !errors.Is(err, ErrForbiddenCommand) {
			t.Errorf("expected ErrForbiddenCommand for %s, got: %v", cmd, err)
		}
	}

	// Even admin or operator cannot run dangerous commands via backplane client
	for _, adminUser := range []string{"admin", "ADMIN", "operator", "OPERATOR"} {
		for _, cmd := range dangerous {
			err := AssertAllowed(adminUser, cmd)
			if !errors.Is(err, ErrForbiddenCommand) {
				t.Errorf("expected ErrForbiddenCommand for %s running %s, got: %v", adminUser, cmd, err)
			}
		}
	}

	// Unknown or unlisted commands are also forbidden under strict allowlisting
	unknown := []string{"CLIENT", "SLAVEOF", "REPLICAOF", "CLUSTER", "BGREWRITEAOF"}
	for _, cmd := range unknown {
		err := AssertAllowed("agent-1", cmd)
		if !errors.Is(err, ErrForbiddenCommand) {
			t.Errorf("expected ErrForbiddenCommand for unlisted command %s, got: %v", cmd, err)
		}
	}
}

func TestGuardDestructivePeerInboxTrimming(t *testing.T) {
	tests := []struct {
		name     string
		callerID string
		cmd      string
		args     []string
		wantErr  bool
	}{
		{
			name:     "XADD MAXLEN on peer inbox",
			callerID: "agent-1",
			cmd:      "XADD",
			args:     []string{"agent-2:inbox", "MAXLEN", "10", "*", "field", "val"},
			wantErr:  true,
		},
		{
			name:     "xadd maxlen lowercase on peer inbox",
			callerID: "agent-1",
			cmd:      "xadd",
			args:     []string{"agent-2:inbox", "maxlen", "10", "*", "field", "val"},
			wantErr:  true,
		},
		{
			name:     "XADD MINID on peer inbox",
			callerID: "agent-1",
			cmd:      "XADD",
			args:     []string{"agent-2:inbox", "MINID", "1000", "*", "field", "val"},
			wantErr:  true,
		},
		{
			name:     "XADD minid mixed case on peer inbox",
			callerID: "agent-1",
			cmd:      "XADD",
			args:     []string{"AGENT-2:INBOX", "minID", "1000", "*", "field", "val"},
			wantErr:  true,
		},
		{
			name:     "XADD LIMIT on peer inbox",
			callerID: "agent-1",
			cmd:      "XADD",
			args:     []string{"agent-2:inbox", "LIMIT", "100", "*", "field", "val"},
			wantErr:  true,
		},
		{
			name:     "XADD limit lowercase on peer inbox",
			callerID: "agent-1",
			cmd:      "XADD",
			args:     []string{"agent-2:inbox", "limit", "100", "*", "field", "val"},
			wantErr:  true,
		},
		{
			name:     "XADD without trimming on peer inbox allowed",
			callerID: "agent-1",
			cmd:      "XADD",
			args:     []string{"agent-2:inbox", "*", "field", "val"},
			wantErr:  false,
		},
		{
			name:     "XADD MAXLEN on own inbox allowed",
			callerID: "agent-1",
			cmd:      "XADD",
			args:     []string{"agent-1:inbox", "MAXLEN", "1000", "*", "field", "val"},
			wantErr:  false,
		},
		{
			name:     "XADD MAXLEN on own inbox case-insensitive allowed",
			callerID: "AGENT-1",
			cmd:      "xadd",
			args:     []string{"agent-1:inbox", "MAXLEN", "1000", "*", "field", "val"},
			wantErr:  false,
		},
		{
			name:     "XADD without args rejected",
			callerID: "agent-1",
			cmd:      "XADD",
			args:     nil,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AssertAllowed(tt.callerID, tt.cmd, tt.args...)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("expected nil error, got: %v", err)
				}
			}
		})
	}
}

func TestGuardPeerOutboxWriteProtection(t *testing.T) {
	tests := []struct {
		name     string
		callerID string
		cmd      string
		args     []string
		wantErr  bool
	}{
		{
			name:     "XADD write to peer outbox rejected",
			callerID: "agent-1",
			cmd:      "XADD",
			args:     []string{"agent-2:out", "*", "content", "fake message"},
			wantErr:  true,
		},
		{
			name:     "xadd write to peer outbox case-insensitive rejected",
			callerID: "agent-1",
			cmd:      "xadd",
			args:     []string{"AGENT-2:OUT", "*", "content", "fake message"},
			wantErr:  true,
		},
		{
			name:     "XADD write to own outbox allowed",
			callerID: "agent-1",
			cmd:      "XADD",
			args:     []string{"agent-1:out", "*", "content", "my broadcast"},
			wantErr:  false,
		},
		{
			name:     "XADD write to own outbox case-insensitive allowed",
			callerID: "Agent-1",
			cmd:      "XADD",
			args:     []string{"AGENT-1:OUT", "*", "content", "my broadcast"},
			wantErr:  false,
		},
		{
			name:     "Empty callerID writing to outbox rejected",
			callerID: "",
			cmd:      "XADD",
			args:     []string{"agent-1:out", "*", "content", "anonymous write"},
			wantErr:  true,
		},
		{
			name:     "Whitespace callerID writing to outbox rejected",
			callerID: "   ",
			cmd:      "XADD",
			args:     []string{"agent-1:out", "*", "content", "whitespace write"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AssertAllowed(tt.callerID, tt.cmd, tt.args...)
			if tt.wantErr {
				if !errors.Is(err, ErrDestructiveAction) {
					t.Errorf("expected ErrDestructiveAction, got: %v", err)
				}
			} else {
				if err != nil {
					t.Errorf("expected nil, got: %v", err)
				}
			}
		})
	}
}

func TestGuardPeerIdentityProtection(t *testing.T) {
	tests := []struct {
		name     string
		callerID string
		cmd      string
		args     []string
		wantErr  bool
	}{
		// XADD on identity
		{
			name:     "XADD to peer identity rejected",
			callerID: "agent-1",
			cmd:      "XADD",
			args:     []string{"identity:agent-2", "*", "key", "val"},
			wantErr:  true,
		},
		{
			name:     "xadd to peer identity case-insensitive rejected",
			callerID: "agent-1",
			cmd:      "xadd",
			args:     []string{"IDENTITY:AGENT-2", "*", "key", "val"},
			wantErr:  true,
		},
		// SET on identity
		{
			name:     "SET on peer identity rejected",
			callerID: "agent-1",
			cmd:      "SET",
			args:     []string{"identity:agent-2", `{"name":"agent-2"}`},
			wantErr:  true,
		},
		{
			name:     "set on peer identity case-insensitive rejected",
			callerID: "agent-1",
			cmd:      "set",
			args:     []string{"IDENTITY:AGENT-2", `{"name":"agent-2"}`},
			wantErr:  true,
		},
		{
			name:     "SET on own identity allowed",
			callerID: "agent-1",
			cmd:      "SET",
			args:     []string{"identity:agent-1", `{"name":"agent-1"}`},
			wantErr:  false,
		},
		{
			name:     "SET on own identity case-insensitive allowed",
			callerID: "AGENT-1",
			cmd:      "SET",
			args:     []string{"identity:agent-1", `{"name":"agent-1"}`},
			wantErr:  false,
		},
		{
			name:     "SET on peer identity with empty callerID rejected",
			callerID: "",
			cmd:      "SET",
			args:     []string{"identity:agent-1", `{"name":"agent-1"}`},
			wantErr:  true,
		},
		// HSET on identity
		{
			name:     "HSET on peer identity rejected",
			callerID: "agent-1",
			cmd:      "HSET",
			args:     []string{"identity:agent-2", "pubkey", "malicious-key"},
			wantErr:  true,
		},
		{
			name:     "hset on peer identity case-insensitive rejected",
			callerID: "agent-1",
			cmd:      "hset",
			args:     []string{"IDENTITY:AGENT-2", "pubkey", "malicious-key"},
			wantErr:  true,
		},
		{
			name:     "HSET on own identity allowed",
			callerID: "agent-1",
			cmd:      "HSET",
			args:     []string{"identity:agent-1", "pubkey", "valid-key"},
			wantErr:  false,
		},
		{
			name:     "HSET on non-identity key allowed",
			callerID: "agent-1",
			cmd:      "HSET",
			args:     []string{"agent-1:finger", "role", "coder"},
			wantErr:  false,
		},
		{
			name:     "HSET on peer identity with empty callerID rejected",
			callerID: "",
			cmd:      "HSET",
			args:     []string{"identity:agent-1", "pubkey", "valid-key"},
			wantErr:  true,
		},
		// HSETNX on identity
		{
			name:     "HSETNX on peer identity rejected",
			callerID: "agent-1",
			cmd:      "HSETNX",
			args:     []string{"identity:agent-2", "pubkey", "malicious-key"},
			wantErr:  true,
		},
		{
			name:     "hsetnx on peer identity case-insensitive rejected",
			callerID: "agent-1",
			cmd:      "hsetnx",
			args:     []string{"IDENTITY:AGENT-2", "pubkey", "malicious-key"},
			wantErr:  true,
		},
		{
			name:     "HSETNX on own identity allowed",
			callerID: "agent-1",
			cmd:      "HSETNX",
			args:     []string{"identity:agent-1", "pubkey", "valid-key"},
			wantErr:  false,
		},
		{
			name:     "HSETNX on peer identity with empty callerID rejected",
			callerID: "",
			cmd:      "HSETNX",
			args:     []string{"identity:agent-1", "pubkey", "valid-key"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AssertAllowed(tt.callerID, tt.cmd, tt.args...)
			if tt.wantErr {
				if !errors.Is(err, ErrDestructiveAction) {
					t.Errorf("expected ErrDestructiveAction, got: %v", err)
				}
			} else {
				if err != nil {
					t.Errorf("expected nil, got: %v", err)
				}
			}
		})
	}
}

func TestGuardDestructiveStreamCommands(t *testing.T) {
	cmds := []string{"XTRIM", "XDEL", "DEL", "UNLINK", "xtrim", "xdel", "del", "unlink"}

	for _, cmd := range cmds {
		t.Run(cmd+" on peer inbox rejected", func(t *testing.T) {
			err := AssertAllowed("agent-1", cmd, "agent-2:inbox")
			if !errors.Is(err, ErrDestructiveAction) {
				t.Errorf("expected ErrDestructiveAction, got: %v", err)
			}
		})

		t.Run(cmd+" on peer outbox rejected", func(t *testing.T) {
			err := AssertAllowed("agent-1", cmd, "agent-2:out")
			if !errors.Is(err, ErrDestructiveAction) {
				t.Errorf("expected ErrDestructiveAction, got: %v", err)
			}
		})

		t.Run(cmd+" on peer identity rejected", func(t *testing.T) {
			err := AssertAllowed("agent-1", cmd, "identity:agent-2")
			if !errors.Is(err, ErrDestructiveAction) {
				t.Errorf("expected ErrDestructiveAction, got: %v", err)
			}
		})

		t.Run(cmd+" on own inbox allowed", func(t *testing.T) {
			err := AssertAllowed("agent-1", cmd, "agent-1:inbox")
			if err != nil {
				t.Errorf("expected allowed for own stream, got: %v", err)
			}
		})

		t.Run(cmd+" on own outbox allowed", func(t *testing.T) {
			err := AssertAllowed("agent-1", cmd, "agent-1:out")
			if err != nil {
				t.Errorf("expected allowed for own outbox, got: %v", err)
			}
		})

		t.Run(cmd+" on own identity allowed", func(t *testing.T) {
			err := AssertAllowed("agent-1", cmd, "identity:agent-1")
			if err != nil {
				t.Errorf("expected allowed for own identity, got: %v", err)
			}
		})

		t.Run(cmd+" with empty callerID rejected on peer stream", func(t *testing.T) {
			err := AssertAllowed("", cmd, "agent-1:inbox")
			if !errors.Is(err, ErrDestructiveAction) {
				t.Errorf("expected ErrDestructiveAction for empty callerID, got: %v", err)
			}
		})
	}

	// DEL / UNLINK multi-key check: one peer key among normal keys should reject
	t.Run("DEL multi-key with peer target rejects", func(t *testing.T) {
		err := AssertAllowed("agent-1", "DEL", "agent-1:temp", "agent-2:out", "agent-1:temp2")
		if !errors.Is(err, ErrDestructiveAction) {
			t.Errorf("expected ErrDestructiveAction for multi-key DEL, got: %v", err)
		}
	})

	t.Run("UNLINK multi-key with peer target rejects", func(t *testing.T) {
		err := AssertAllowed("agent-1", "UNLINK", "agent-1:temp", "identity:agent-2")
		if !errors.Is(err, ErrDestructiveAction) {
			t.Errorf("expected ErrDestructiveAction for multi-key UNLINK, got: %v", err)
		}
	})

	t.Run("DEL multi-key with only own keys succeeds", func(t *testing.T) {
		err := AssertAllowed("agent-1", "DEL", "agent-1:temp", "agent-1:inbox", "agent-1:out")
		if err != nil {
			t.Errorf("expected nil for DEL with own keys, got: %v", err)
		}
	})
}

func TestGuardAdminBypassForPeerKeys(t *testing.T) {
	// Administrator and Operator accounts are permitted to register identities and perform backplane provisioning
	adminCommands := []struct {
		cmd  string
		args []string
	}{
		{"SET", []string{"identity:agent-1", `{"name":"agent-1"}`}},
		{"HSET", []string{"identity:agent-2", "role", "coder"}},
		{"HSETNX", []string{"identity:agent-3", "role", "analyst"}},
		{"SET", []string{"agent-1:inbox", "custom-data"}},
		{"SET", []string{"agent-1:out", "custom-data"}},
		{"HSET", []string{"agent-1:inbox", "field", "val"}},
		{"HSETNX", []string{"agent-1:out", "field", "val"}},
		{"XADD", []string{"agent-1:out", "*", "field", "value"}},
		{"DEL", []string{"identity:deprovisioned-agent"}},
		{"DEL", []string{"agent-1:inbox"}},
		{"UNLINK", []string{"agent-1:out"}},
		{"XTRIM", []string{"agent-1:inbox", "MAXLEN", "100"}},
		{"XDEL", []string{"agent-1:out", "100-0"}},
	}

	adminCallers := []string{"admin", "ADMIN", "operator", "OPERATOR", " operator "}

	for _, caller := range adminCallers {
		for _, tc := range adminCommands {
			if err := AssertAllowed(caller, tc.cmd, tc.args...); err != nil {
				t.Errorf("caller %q should be allowed to run %s %v, got: %v", caller, tc.cmd, tc.args, err)
			}
		}
	}
}

func TestGuardSetHSetPeerStreamProtection(t *testing.T) {
	cmds := []string{"SET", "HSET", "HSETNX"}
	peerTargets := []string{"agent-2:inbox", "AGENT-2:INBOX", "agent-2:out", "AGENT-2:OUT"}

	for _, cmd := range cmds {
		for _, target := range peerTargets {
			t.Run(cmd+" on peer target "+target+" rejected", func(t *testing.T) {
				var args []string
				if cmd == "SET" {
					args = []string{target, "value"}
				} else {
					args = []string{target, "field", "value"}
				}
				err := AssertAllowed("agent-1", cmd, args...)
				if !errors.Is(err, ErrDestructiveAction) {
					t.Errorf("expected ErrDestructiveAction for %s on %s, got: %v", cmd, target, err)
				}
			})
		}
	}
}

func TestGuardACLCommand(t *testing.T) {
	// Admin and operator should be permitted to run ACL commands
	for _, adminCaller := range []string{"admin", "ADMIN", "operator", "OPERATOR"} {
		err := AssertAllowed(adminCaller, "ACL", "SETUSER", "agent-1", "on")
		if err != nil {
			t.Errorf("expected ACL command to be allowed for %s, got: %v", adminCaller, err)
		}
	}

	// Normal agents must be forbidden from running ACL commands
	for _, agentCaller := range []string{"agent-1", "agent-2", "", "unknown"} {
		err := AssertAllowed(agentCaller, "ACL", "SETUSER", "agent-1", "on")
		if !errors.Is(err, ErrForbiddenCommand) {
			t.Errorf("expected ErrForbiddenCommand for %s running ACL, got: %v", agentCaller, err)
		}
	}
}

func TestGuardEdgeCases(t *testing.T) {
	// Empty command or empty args
	if err := AssertAllowed("agent-1", ""); err != nil {
		t.Errorf("expected nil for empty command, got: %v", err)
	}
	if err := AssertAllowed("agent-1", "SET"); err != nil {
		t.Errorf("expected nil for SET with no args, got: %v", err)
	}
	if err := AssertAllowed("agent-1", "HSET"); err != nil {
		t.Errorf("expected nil for HSET with no args, got: %v", err)
	}
	if err := AssertAllowed("agent-1", "HSETNX"); err != nil {
		t.Errorf("expected nil for HSETNX with no args, got: %v", err)
	}
	if err := AssertAllowed("agent-1", "DEL"); err != nil {
		t.Errorf("expected nil for DEL with no args, got: %v", err)
	}
	if err := AssertAllowed("agent-1", "XTRIM"); err != nil {
		t.Errorf("expected nil for XTRIM with no args, got: %v", err)
	}
}
