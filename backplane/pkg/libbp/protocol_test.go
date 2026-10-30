// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package libbp

import (
	"reflect"
	"testing"
)

func TestKeyHelpers(t *testing.T) {
	if OutKey("Agent-1") != "agent-1:out" {
		t.Errorf("OutKey failed: %s", OutKey("Agent-1"))
	}
	if InboxKey("Agent-2") != "agent-2:inbox" {
		t.Errorf("InboxKey failed: %s", InboxKey("Agent-2"))
	}
	if SeqKey("Agent-1") != "agent-1:seq" {
		t.Errorf("SeqKey failed: %s", SeqKey("Agent-1"))
	}
	if CursorKey("Agent-1") != "agent-1:cursor" {
		t.Errorf("CursorKey failed: %s", CursorKey("Agent-1"))
	}
	if StatusKey("Agent-1") != "agent-1:status" {
		t.Errorf("StatusKey failed: %s", StatusKey("Agent-1"))
	}
	if FingerKey("Agent-1") != "agent-1:finger" {
		t.Errorf("FingerKey failed: %s", FingerKey("Agent-1"))
	}
	if TeamChannel("Alpha") != "team:alpha:out" {
		t.Errorf("TeamChannel failed: %s", TeamChannel("Alpha"))
	}
	if BlobKey("Agent-1", 1726000000) != "agent-1:blob:1726000000" {
		t.Errorf("BlobKey failed: %s", BlobKey("Agent-1", 1726000000))
	}
	if IdentityKey("Agent-1") != "identity:agent-1" {
		t.Errorf("IdentityKey failed: %s", IdentityKey("Agent-1"))
	}
}

func TestMessageSerializationAndParsing(t *testing.T) {
	msg := &Message{
		ID:          "1726170000000-0",
		Sender:      "Agent-1",
		Destination: "Agent-2",
		Content:     "Hello from agent 1",
		BlobPath:    "agent-1:blob:123",
		ReplyTo:     "1726160000000-0",
		Timestamp:   1726170000000,
		Seq:         42,
		Citation:    "agent-1#42",
		Signature:   "dGVzdHNpZw==",
	}

	fields := msg.ToFieldValues()
	if len(fields) == 0 {
		t.Fatalf("ToFieldValues returned empty slice")
	}

	fieldMap := make(map[string]string)
	for i := 0; i < len(fields); i += 2 {
		fieldMap[fields[i]] = fields[i+1]
	}

	parsed := MessageFromFields("1726170000000-0", fieldMap)
	if parsed.Sender != "agent-1" || parsed.Destination != "agent-2" || parsed.Content != "Hello from agent 1" {
		t.Errorf("parsed message mismatch: %+v", parsed)
	}
	if parsed.Seq != 42 || parsed.Citation != "agent-1#42" || !parsed.IsSigned {
		t.Errorf("parsed metadata mismatch: %+v", parsed)
	}

	// Test FormatCitation without seq
	if FormatCitation("agent-1", 0) != "agent-1" {
		t.Errorf("expected agent-1, got %s", FormatCitation("agent-1", 0))
	}
}

func TestSigningPayload(t *testing.T) {
	msg := &Message{
		Timestamp:   1700000000,
		Seq:         42,
		Sender:      "Agent-1",
		Destination: "Agent-2",
		ReplyTo:     "msg-0",
		Content:     "payload",
		BlobPath:    "blob-1",
	}

	expected := "10:1700000000\n2:42\n7:agent-1\n7:agent-2\n5:msg-0\n7:payload\n6:blob-1"
	if string(msg.SigningPayload()) != expected {
		t.Errorf("got %q, expected %q", string(msg.SigningPayload()), expected)
	}
}

func TestParseIdentityRecord(t *testing.T) {
	raw := `{"name":"agent-1","role":"reviewer","kind":"agent","pubkey":"abc123=="}`
	rec, err := ParseIdentityRecord(raw)
	if err != nil {
		t.Fatalf("ParseIdentityRecord error: %v", err)
	}
	expected := &IdentityRecord{
		Name:   "agent-1",
		Role:   "reviewer",
		Kind:   "agent",
		PubKey: "abc123==",
	}
	if !reflect.DeepEqual(rec, expected) {
		t.Errorf("got %+v, expected %+v", rec, expected)
	}

	if _, err := ParseIdentityRecord("{invalid-json"); err == nil {
		t.Errorf("expected error for invalid json, got nil")
	}
}

func TestEnsembleKeysAndValidation(t *testing.T) {
	if got := EnsembleOutKey("Alpha-Team"); got != "ensemble:alpha-team:out" {
		t.Errorf("EnsembleOutKey: got %q, want 'ensemble:alpha-team:out'", got)
	}
	if got := AgentEnsemblesKey("Agent-1"); got != "agent-1:ensembles" {
		t.Errorf("AgentEnsemblesKey: got %q, want 'agent-1:ensembles'", got)
	}
	if got := EnsembleCursorKey("Agent-1", "Alpha-Team"); got != "agent-1:cursor:ensemble:alpha-team" {
		t.Errorf("EnsembleCursorKey: got %q, want 'agent-1:cursor:ensemble:alpha-team'", got)
	}

	validNames := []string{"frontend", "backend-api", "team-alpha-1", "ensemble"}
	for _, name := range validNames {
		if err := ValidateEnsembleName(name); err != nil {
			t.Errorf("ValidateEnsembleName(%q) unexpected error: %v", name, err)
		}
	}

	invalidNames := []string{
		"",
		" ",
		"-invalid",
		"123-team",
		"team_alpha",
		"team@alpha",
		"team.alpha",
		"this-name-is-way-too-long-because-it-exceeds-the-maximum-allowed-length-of-sixty-four-characters",
	}
	for _, name := range invalidNames {
		if err := ValidateEnsembleName(name); err == nil {
			t.Errorf("ValidateEnsembleName(%q) expected error, got nil", name)
		}
	}
}

