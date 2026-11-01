// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package libbp

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var specialtyNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// ValidateSpecialtyName checks that a specialty domain name is non-empty and valid.
func ValidateSpecialtyName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return errors.New("specialty domain name cannot be empty")
	}
	if !specialtyNameRegex.MatchString(trimmed) {
		return fmt.Errorf("invalid specialty domain name %q: must contain only alphanumeric characters, hyphens, and underscores", name)
	}
	return nil
}

// Well-known Valkey keys and TTLs
const (
	KeyHumanName      = "human:name"
	KeyLiaisonCurrent = "liaison:current"
	KeyPollInterval   = "poll-interval"
	StatusTTLSeconds  = 300   // 5 minutes
	BlobTTLSeconds    = 86400 // 24 hours

	DefaultPollInterval = 60
	MinPollInterval     = 5
	MaxPollInterval     = 3600
)

// Key generation helper functions
func OutKey(id string) string    { return fmt.Sprintf("%s:out", strings.ToLower(id)) }
func InboxKey(id string) string  { return fmt.Sprintf("%s:inbox", strings.ToLower(id)) }
func SeqKey(id string) string    { return fmt.Sprintf("%s:seq", strings.ToLower(id)) }
func CursorKey(id string) string { return fmt.Sprintf("%s:cursor", strings.ToLower(id)) }
func StatusKey(id string) string { return fmt.Sprintf("%s:status", strings.ToLower(id)) }
func FingerKey(id string) string { return fmt.Sprintf("%s:finger", strings.ToLower(id)) }
func TeamChannel(team string) string { return fmt.Sprintf("team:%s:out", strings.ToLower(team)) }
func BlobKey(id string, ts int64) string {
	return fmt.Sprintf("%s:blob:%d", strings.ToLower(id), ts)
}
func IdentityKey(id string) string { return fmt.Sprintf("identity:%s", strings.ToLower(id)) }
func SpecialtyKey(domain string) string {
	return fmt.Sprintf("specialty:%s", strings.ToLower(strings.TrimSpace(domain)))
}
func PingKey(recipient string) string {
	return fmt.Sprintf("ping:%s", strings.ToLower(strings.TrimSpace(recipient)))
}

// EnsembleNameRegex enforces strict ensemble name format (e.g. ^[a-z][a-z0-9-]*$)
var EnsembleNameRegex = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ValidateEnsembleName validates that an ensemble name conforms to strict lowercase alphanumeric and hyphen format.
func ValidateEnsembleName(name string) error {
	trimmed := strings.ToLower(strings.TrimSpace(name))
	if trimmed == "" {
		return fmt.Errorf("ensemble name cannot be empty")
	}
	if len(trimmed) > 64 {
		return fmt.Errorf("ensemble name %q exceeds maximum length of 64 characters", trimmed)
	}
	if !EnsembleNameRegex.MatchString(trimmed) {
		return fmt.Errorf("invalid ensemble name %q: must match regex ^[a-z][a-z0-9-]*$", trimmed)
	}
	return nil
}

func EnsembleOutKey(name string) string {
	return fmt.Sprintf("ensemble:%s:out", strings.ToLower(strings.TrimSpace(name)))
}

func AgentEnsemblesKey(agentID string) string {
	return fmt.Sprintf("%s:ensembles", strings.ToLower(strings.TrimSpace(agentID)))
}

func EnsembleCursorKey(agentID, ensembleName string) string {
	return fmt.Sprintf("%s:cursor:ensemble:%s", strings.ToLower(strings.TrimSpace(agentID)), strings.ToLower(strings.TrimSpace(ensembleName)))
}

// Message represents a backplane stream item
type Message struct {
	ID          string `json:"id"`
	Sender      string `json:"sender"`
	Destination string `json:"destination,omitempty"`
	Content     string `json:"content"`
	BlobPath    string `json:"blob_path,omitempty"`
	ReplyTo     string `json:"reply_to,omitempty"`
	Timestamp   int64  `json:"timestamp"`
	Seq         int64  `json:"seq"`
	Citation    string `json:"citation"`
	Signature   string `json:"signature,omitempty"`
	IsSigned    bool   `json:"is_signed"`
	IsVerified  bool   `json:"is_verified"`
}

// IdentityRecord stored in identity:<id>
type IdentityRecord struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Kind      string `json:"kind"` // "human" or "agent"
	PubKey    string `json:"pubkey,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
}

// Peer represents an active or known agent in the fleet
type Peer struct {
	ID        string    `json:"id"`
	Status    string    `json:"status,omitempty"`
	Role      string    `json:"role,omitempty"`
	LastSeen  time.Time `json:"last_seen,omitempty"`
	IsLiaison bool      `json:"is_liaison"`
}

// SigningPayload generates the canonical byte slice for signing & verification
func (m *Message) SigningPayload() []byte {
	tsStr := strconv.FormatInt(m.Timestamp, 10)
	seqStr := strconv.FormatInt(m.Seq, 10)
	senderStr := strings.ToLower(m.Sender)
	destStr := strings.ToLower(m.Destination)

	return []byte(fmt.Sprintf("%d:%s\n%d:%s\n%d:%s\n%d:%s\n%d:%s\n%d:%s\n%d:%s",
		len(tsStr), tsStr,
		len(seqStr), seqStr,
		len(senderStr), senderStr,
		len(destStr), destStr,
		len(m.ReplyTo), m.ReplyTo,
		len(m.Content), m.Content,
		len(m.BlobPath), m.BlobPath,
	))
}

// FormatCitation returns a memorable citation handle (e.g. <id>#14)
func FormatCitation(sender string, seq int64) string {
	if seq <= 0 {
		return strings.ToLower(sender)
	}
	return fmt.Sprintf("%s#%d", strings.ToLower(sender), seq)
}

// ToFieldValues converts a Message struct into string key-value pairs for XADD
func (m *Message) ToFieldValues() []string {
	kvs := []string{
		"sender", strings.ToLower(m.Sender),
		"content", m.Content,
		"timestamp", strconv.FormatInt(m.Timestamp, 10),
		"seq", strconv.FormatInt(m.Seq, 10),
	}
	if m.Destination != "" {
		kvs = append(kvs, "destination", strings.ToLower(m.Destination))
	}
	if m.BlobPath != "" {
		kvs = append(kvs, "blob_path", m.BlobPath)
	}
	if m.ReplyTo != "" {
		kvs = append(kvs, "reply_to", m.ReplyTo)
	}
	if m.Signature != "" {
		kvs = append(kvs, "signature", m.Signature)
	}
	return kvs
}

// MessageFromFields reconstructs a Message from stream field-value pairs
func MessageFromFields(id string, fields map[string]string) *Message {
	msg := &Message{
		ID:          id,
		Sender:      fields["sender"],
		Destination: fields["destination"],
		Content:     fields["content"],
		BlobPath:    fields["blob_path"],
		ReplyTo:     fields["reply_to"],
		Signature:   fields["signature"],
	}

	if ts, err := strconv.ParseInt(fields["timestamp"], 10, 64); err == nil {
		msg.Timestamp = ts
	}
	if seq, err := strconv.ParseInt(fields["seq"], 10, 64); err == nil {
		msg.Seq = seq
	}
	if msg.Signature != "" {
		msg.IsSigned = true
	}
	msg.Citation = FormatCitation(msg.Sender, msg.Seq)
	return msg
}

// ParseIdentityRecord parses JSON string into IdentityRecord
func ParseIdentityRecord(raw string) (*IdentityRecord, error) {
	var rec IdentityRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}
