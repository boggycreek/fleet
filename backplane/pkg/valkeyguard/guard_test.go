// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package valkeyguard

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/backplane/pkg/libbp/resp"
)

func makeRESPArray(args ...string) resp.Value {
	vals := make([]resp.Value, len(args))
	for i, a := range args {
		vals[i] = resp.Value{
			Type: resp.TypeBulkString,
			Str:  a,
		}
	}
	return resp.Value{
		Type:  resp.TypeArray,
		Array: vals,
	}
}

func TestGuardEngine_Rule1_MAXLEN_InjectionAndClamping(t *testing.T) {
	engine := NewGuardEngine(GuardConfig{
		MaxPayloadBytes: 1048576,
		MaxStreamLen:    1000,
	})

	tests := []struct {
		name         string
		args         []string
		sessionUser  string
		expectedArgs []string
		expectMod    bool
	}{
		{
			name:         "XADD without MAXLEN or MINID injects MAXLEN ~ 1000",
			args:         []string{"XADD", "mystream", "*", "field", "value"},
			sessionUser:  "alice",
			expectedArgs: []string{"XADD", "mystream", "MAXLEN", "~", "1000", "*", "field", "value"},
			expectMod:    true,
		},
		{
			name:         "XADD with NOMKSTREAM without MAXLEN injects MAXLEN ~ 1000",
			args:         []string{"XADD", "mystream", "NOMKSTREAM", "*", "field", "value"},
			sessionUser:  "alice",
			expectedArgs: []string{"XADD", "mystream", "MAXLEN", "~", "1000", "NOMKSTREAM", "*", "field", "value"},
			expectMod:    true,
		},
		{
			name:         "XADD with MAXLEN within limit is unchanged",
			args:         []string{"XADD", "mystream", "MAXLEN", "~", "500", "*", "field", "value"},
			sessionUser:  "alice",
			expectedArgs: []string{"XADD", "mystream", "MAXLEN", "~", "500", "*", "field", "value"},
			expectMod:    false,
		},
		{
			name:         "XADD with MAXLEN = 500 within limit is unchanged",
			args:         []string{"XADD", "mystream", "MAXLEN", "=", "500", "*", "field", "value"},
			sessionUser:  "alice",
			expectedArgs: []string{"XADD", "mystream", "MAXLEN", "=", "500", "*", "field", "value"},
			expectMod:    false,
		},
		{
			name:         "XADD with MAXLEN exact without modifier within limit is unchanged",
			args:         []string{"XADD", "mystream", "MAXLEN", "500", "*", "field", "value"},
			sessionUser:  "alice",
			expectedArgs: []string{"XADD", "mystream", "MAXLEN", "500", "*", "field", "value"},
			expectMod:    false,
		},
		{
			name:         "XADD with MAXLEN exceeding limit is clamped",
			args:         []string{"XADD", "mystream", "MAXLEN", "~", "5000", "*", "field", "value"},
			sessionUser:  "alice",
			expectedArgs: []string{"XADD", "mystream", "MAXLEN", "~", "1000", "*", "field", "value"},
			expectMod:    true,
		},
		{
			name:         "XADD with MAXLEN = 1000000 with LIMIT is clamped",
			args:         []string{"XADD", "mystream", "MAXLEN", "~", "1000000", "LIMIT", "100", "*", "field", "value"},
			sessionUser:  "alice",
			expectedArgs: []string{"XADD", "mystream", "MAXLEN", "~", "1000", "LIMIT", "100", "*", "field", "value"},
			expectMod:    true,
		},
		{
			name:         "XADD with MINID is not modified",
			args:         []string{"XADD", "mystream", "MINID", "~", "1600000000000-0", "*", "field", "value"},
			sessionUser:  "alice",
			expectedArgs: []string{"XADD", "mystream", "MINID", "~", "1600000000000-0", "*", "field", "value"},
			expectMod:    false,
		},
		{
			name:         "XADD self inbox without MAXLEN injects MAXLEN ~ 1000",
			args:         []string{"XADD", "alice:inbox", "*", "field", "value"},
			sessionUser:  "alice",
			expectedArgs: []string{"XADD", "alice:inbox", "MAXLEN", "~", "1000", "*", "field", "value"},
			expectMod:    true,
		},
		{
			name:         "XADD with invalid non-numeric MAXLEN remains unchanged (no clamping crash)",
			args:         []string{"XADD", "mystream", "MAXLEN", "~", "notanumber", "*", "field", "value"},
			sessionUser:  "alice",
			expectedArgs: []string{"XADD", "mystream", "MAXLEN", "~", "notanumber", "*", "field", "value"},
			expectMod:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := engine.Inspect(makeRESPArray(tt.args...), tt.sessionUser)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(res.Args, tt.expectedArgs) {
				t.Errorf("expected args %v, got %v", tt.expectedArgs, res.Args)
			}
			if res.Modified != tt.expectMod {
				t.Errorf("expected modified %v, got %v", tt.expectMod, res.Modified)
			}

			// Verify FormatRESP and Bytes
			formatted := res.FormatRESP()
			if formatted.Type != resp.TypeArray || len(formatted.Array) != len(tt.expectedArgs) {
				t.Errorf("unexpected formatted RESP: %v", formatted)
			}
			b := res.Bytes()
			if len(b) == 0 {
				t.Errorf("empty serialized bytes")
			}
		})
	}
}

func TestGuardEngine_Rule2_PayloadSizeCapping(t *testing.T) {
	engine := NewGuardEngine(GuardConfig{
		MaxPayloadBytes: 1048576,
		MaxStreamLen:    1000,
	})

	// Exact limit: 1MB
	exactPayload := strings.Repeat("x", 1048576)
	res, err := engine.Inspect(makeRESPArray("SET", "key", exactPayload), "alice")
	if err != nil {
		t.Fatalf("expected payload at exact limit to pass, got error: %v", err)
	}
	if len(res.Args) != 3 {
		t.Fatalf("expected 3 args, got %d", len(res.Args))
	}

	// Over limit: 1MB + 1 byte
	oversizedPayload := strings.Repeat("x", 1048577)
	_, err = engine.Inspect(makeRESPArray("SET", "key", oversizedPayload), "alice")
	if err == nil {
		t.Fatalf("expected oversized payload to be rejected")
	}
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("expected ErrPayloadTooLarge, got: %v", err)
	}
	if !strings.Contains(err.Error(), "ERR message payload size exceeds maximum limit (1048576 bytes)") {
		t.Errorf("expected exact error message, got: %v", err)
	}

	// In XADD field
	_, err = engine.Inspect(makeRESPArray("XADD", "stream", "*", "data", oversizedPayload), "alice")
	if err == nil {
		t.Fatalf("expected oversized XADD field to be rejected")
	}
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("expected ErrPayloadTooLarge, got: %v", err)
	}
}

func TestGuardEngine_Rule3_CitationAndIdentityValidation(t *testing.T) {
	engine := NewGuardEngine(DefaultGuardConfig())

	tests := []struct {
		name        string
		args        []string
		sessionUser string
		expectErr   error
	}{
		{
			name:        "Valid citation and matching sender",
			args:        []string{"XADD", "stream", "*", "citation", "alice#1", "sender", "alice"},
			sessionUser: "alice",
			expectErr:   nil,
		},
		{
			name:        "Valid citation with hyphen and underscore",
			args:        []string{"XADD", "stream", "*", "citation", "test-agent_01#42", "sender", "test-agent_01"},
			sessionUser: "test-agent_01",
			expectErr:   nil,
		},
		{
			name:        "Valid citation without sender claim",
			args:        []string{"XADD", "stream", "*", "citation", "bob#100"},
			sessionUser: "alice",
			expectErr:   nil,
		},
		{
			name:        "Invalid citation missing hash",
			args:        []string{"XADD", "stream", "*", "citation", "alice1"},
			sessionUser: "alice",
			expectErr:   ErrInvalidCitation,
		},
		{
			name:        "Invalid citation non-numeric sequence",
			args:        []string{"XADD", "stream", "*", "citation", "alice#xyz"},
			sessionUser: "alice",
			expectErr:   ErrInvalidCitation,
		},
		{
			name:        "Invalid citation special characters",
			args:        []string{"XADD", "stream", "*", "citation", "alice!@##1"},
			sessionUser: "alice",
			expectErr:   ErrInvalidCitation,
		},
		{
			name:        "Sender claim matching session username case-insensitively",
			args:        []string{"XADD", "stream", "*", "sender", "Alice"},
			sessionUser: "alice",
			expectErr:   nil,
		},
		{
			name:        "Sender claim mismatch against session username",
			args:        []string{"XADD", "stream", "*", "sender", "bob"},
			sessionUser: "alice",
			expectErr:   ErrIdentityMismatch,
		},
		{
			name:        "Sender claim mismatch allowed for admin",
			args:        []string{"XADD", "stream", "*", "sender", "bob"},
			sessionUser: "admin",
			expectErr:   nil,
		},
		{
			name:        "Sender claim mismatch allowed for operator",
			args:        []string{"XADD", "stream", "*", "sender", "bob"},
			sessionUser: "operator",
			expectErr:   nil,
		},
		{
			name:        "Sender claim when session username is empty/unknown",
			args:        []string{"XADD", "stream", "*", "sender", "bob"},
			sessionUser: "",
			expectErr:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := engine.Inspect(makeRESPArray(tt.args...), tt.sessionUser)
			if tt.expectErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tt.expectErr)
				}
				if !errors.Is(err, tt.expectErr) {
					t.Errorf("expected error %v, got %v", tt.expectErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestGuardEngine_Rule4_CrossInboxHistoryDefense(t *testing.T) {
	engineReject := NewGuardEngine(GuardConfig{
		MaxPayloadBytes:        1048576,
		MaxStreamLen:           1000,
		StripPeerInboxTrimming: false,
	})

	engineStrip := NewGuardEngine(GuardConfig{
		MaxPayloadBytes:        1048576,
		MaxStreamLen:           1000,
		StripPeerInboxTrimming: true,
	})

	tests := []struct {
		name         string
		engine       *GuardEngine
		args         []string
		sessionUser  string
		expectErr    error
		expectedArgs []string
	}{
		{
			name:        "Peer inbox with MAXLEN rejects under default config",
			engine:      engineReject,
			args:        []string{"XADD", "bob:inbox", "MAXLEN", "~", "100", "*", "msg", "hello"},
			sessionUser: "alice",
			expectErr:   ErrPeerInboxTruncationForbidden,
		},
		{
			name:        "Peer inbox with MINID rejects under default config",
			engine:      engineReject,
			args:        []string{"XADD", "bob:inbox", "MINID", "~", "1600000000000-0", "*", "msg", "hello"},
			sessionUser: "alice",
			expectErr:   ErrPeerInboxTruncationForbidden,
		},
		{
			name:         "Peer inbox without trimming appends cleanly without injecting MAXLEN",
			engine:       engineReject,
			args:         []string{"XADD", "bob:inbox", "*", "msg", "hello"},
			sessionUser:  "alice",
			expectErr:    nil,
			expectedArgs: []string{"XADD", "bob:inbox", "*", "msg", "hello"},
		},
		{
			name:         "Peer inbox with MAXLEN strips trimming when configured",
			engine:       engineStrip,
			args:         []string{"XADD", "bob:inbox", "MAXLEN", "~", "100", "*", "msg", "hello"},
			sessionUser:  "alice",
			expectErr:    nil,
			expectedArgs: []string{"XADD", "bob:inbox", "*", "msg", "hello"},
		},
		{
			name:         "Peer inbox with MINID and LIMIT strips trimming when configured",
			engine:       engineStrip,
			args:         []string{"XADD", "bob:inbox", "MINID", "=", "1000", "LIMIT", "10", "*", "msg", "hello"},
			sessionUser:  "alice",
			expectErr:    nil,
			expectedArgs: []string{"XADD", "bob:inbox", "*", "msg", "hello"},
		},
		{
			name:         "Own inbox with MAXLEN is permitted and clamped if needed",
			engine:       engineReject,
			args:         []string{"XADD", "alice:inbox", "MAXLEN", "2000", "*", "msg", "hello"},
			sessionUser:  "alice",
			expectErr:    nil,
			expectedArgs: []string{"XADD", "alice:inbox", "MAXLEN", "1000", "*", "msg", "hello"},
		},
		{
			name:         "Admin writing to peer inbox with MAXLEN is treated as non-peer and allowed",
			engine:       engineReject,
			args:         []string{"XADD", "bob:inbox", "MAXLEN", "500", "*", "msg", "hello"},
			sessionUser:  "admin",
			expectErr:    nil,
			expectedArgs: []string{"XADD", "bob:inbox", "MAXLEN", "500", "*", "msg", "hello"},
		},
		{
			name:         "Operator writing to peer inbox with MAXLEN is treated as non-peer and allowed",
			engine:       engineReject,
			args:         []string{"XADD", "bob:inbox", "MAXLEN", "500", "*", "msg", "hello"},
			sessionUser:  "operator",
			expectErr:    nil,
			expectedArgs: []string{"XADD", "bob:inbox", "MAXLEN", "500", "*", "msg", "hello"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := tt.engine.Inspect(makeRESPArray(tt.args...), tt.sessionUser)
			if tt.expectErr != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tt.expectErr)
				}
				if !errors.Is(err, tt.expectErr) {
					t.Errorf("expected error %v, got %v", tt.expectErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !reflect.DeepEqual(res.Args, tt.expectedArgs) {
					t.Errorf("expected args %v, got %v", tt.expectedArgs, res.Args)
				}
			}
		})
	}
}

func TestGuardEngine_EdgeCasesAndMalformedFrames(t *testing.T) {
	engine := NewGuardEngine(DefaultGuardConfig())

	// Non-array RESP value
	_, err := engine.Inspect(resp.Value{Type: resp.TypeSimpleString, Str: "PING"}, "alice")
	if err == nil || !errors.Is(err, ErrInvalidCommand) {
		t.Errorf("expected ErrInvalidCommand for non-array, got %v", err)
	}

	// Empty array
	_, err = engine.Inspect(resp.Value{Type: resp.TypeArray, Array: []resp.Value{}}, "alice")
	if err == nil || !errors.Is(err, ErrInvalidCommand) {
		t.Errorf("expected ErrInvalidCommand for empty array, got %v", err)
	}

	// Array with invalid string element
	_, err = engine.Inspect(resp.Value{
		Type: resp.TypeArray,
		Array: []resp.Value{
			{Type: resp.TypeArray, Array: []resp.Value{}},
		},
	}, "alice")
	if err == nil || !errors.Is(err, ErrInvalidCommand) {
		t.Errorf("expected ErrInvalidCommand for array item non-string, got %v", err)
	}

	// Non-XADD commands pass through unmodified
	res, err := engine.Inspect(makeRESPArray("PING"), "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Modified || len(res.Args) != 1 || res.Args[0] != "PING" {
		t.Errorf("expected unmodified PING command, got %v", res)
	}

	// Empty command via InspectCommand
	_, _, err = engine.InspectCommand([]string{}, "alice")
	if err == nil || !errors.Is(err, ErrInvalidCommand) {
		t.Errorf("expected ErrInvalidCommand on empty slice, got %v", err)
	}

	// XADD with no stream key
	_, err = engine.Inspect(makeRESPArray("XADD"), "alice")
	if err == nil || !errors.Is(err, ErrInvalidCommand) {
		t.Errorf("expected ErrInvalidCommand for XADD missing stream, got %v", err)
	}

	// XADD with missing entry ID
	_, err = engine.Inspect(makeRESPArray("XADD", "stream"), "alice")
	if err == nil || !errors.Is(err, ErrInvalidCommand) {
		t.Errorf("expected ErrInvalidCommand for XADD missing entry ID, got %v", err)
	}

	// XADD with odd number of fields
	_, err = engine.Inspect(makeRESPArray("XADD", "stream", "*", "field1"), "alice")
	if err == nil || !errors.Is(err, ErrInvalidCommand) {
		t.Errorf("expected ErrInvalidCommand for odd field count, got %v", err)
	}

	// NewGuardEngine with defaults fallback
	e0 := NewGuardEngine(GuardConfig{})
	if e0.Config().MaxPayloadBytes != DefaultMaxPayloadBytes || e0.Config().MaxStreamLen != DefaultMaxStreamLen {
		t.Errorf("expected default config fallback, got %+v", e0.Config())
	}
}
