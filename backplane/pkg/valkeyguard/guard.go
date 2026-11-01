// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package valkeyguard

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/boggycreek/fleet/backplane/pkg/libbp/resp"
)

const (
	// DefaultMaxPayloadBytes is 1MB (1,048,576 bytes)
	DefaultMaxPayloadBytes = 1048576

	// DefaultMaxStreamLen is the default MAXLEN limit (1000)
	DefaultMaxStreamLen = 1000
)

var (
	// ErrPayloadTooLarge indicates that an argument in the command exceeded MaxPayloadBytes
	ErrPayloadTooLarge = errors.New("ERR message payload size exceeds maximum limit (1048576 bytes)")

	// ErrInvalidCitation indicates that the citation field does not match the canonical sequence format
	ErrInvalidCitation = errors.New("ERR invalid citation format: expected <agent>#<seq>")

	// ErrIdentityMismatch indicates that the sender claim does not match the authenticated session username
	ErrIdentityMismatch = errors.New("ERR identity claim mismatch")

	// ErrPeerInboxTruncationForbidden indicates that MAXLEN or MINID was provided on a peer inbox
	ErrPeerInboxTruncationForbidden = errors.New("ERR forbidden: MAXLEN/MINID options disallowed on peer inbox")

	// ErrInvalidCommand indicates malformed command frame or arguments
	ErrInvalidCommand = errors.New("ERR invalid command format")
)

var (
	// Citation regex: ^[a-zA-Z0-9_-]+#[0-9]+$
	citationRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+#[0-9]+$`)
)

// GuardConfig specifies thresholds and configuration for wire guard rules.
type GuardConfig struct {
	MaxPayloadBytes int
	MaxStreamLen    int64
	// StripPeerInboxTrimming if true strips MAXLEN/MINID rather than returning an error.
	// Default is false (reject with error).
	StripPeerInboxTrimming bool
}

// DefaultGuardConfig returns the standard guard configuration.
func DefaultGuardConfig() GuardConfig {
	return GuardConfig{
		MaxPayloadBytes:        DefaultMaxPayloadBytes,
		MaxStreamLen:           DefaultMaxStreamLen,
		StripPeerInboxTrimming: false,
	}
}

// GuardEngine inspects incoming RESP command frames and applies security rules.
type GuardEngine struct {
	cfg GuardConfig
}

// Inspector is an alias for GuardEngine.
type Inspector = GuardEngine

// NewGuardEngine creates a new GuardEngine instance with the provided configuration.
func NewGuardEngine(cfg GuardConfig) *GuardEngine {
	if cfg.MaxPayloadBytes <= 0 {
		cfg.MaxPayloadBytes = DefaultMaxPayloadBytes
	}
	if cfg.MaxStreamLen <= 0 {
		cfg.MaxStreamLen = DefaultMaxStreamLen
	}
	return &GuardEngine{
		cfg: cfg,
	}
}

// Config returns the current GuardConfig.
func (g *GuardEngine) Config() GuardConfig {
	return g.cfg
}

// InspectionResult contains the outcome of inspecting and potentially rewriting a RESP command.
type InspectionResult struct {
	Original  resp.Value
	Rewritten resp.Value
	Args      []string
	Modified  bool
}

// FormatRESP returns the rewritten command serialized as a RESP Array of Bulk Strings.
func (res *InspectionResult) FormatRESP() resp.Value {
	return res.Rewritten
}

// Bytes returns the serialized wire bytes for the rewritten RESP command frame.
func (res *InspectionResult) Bytes() []byte {
	return resp.EncodeCommand(res.Args...)
}

// Inspect parses an incoming RESP command frame (typically a RESP array of bulk strings)
// and enforces guard rules 1-4.
func (g *GuardEngine) Inspect(val resp.Value, sessionUsername string) (*InspectionResult, error) {
	arr, err := val.AsArray()
	if err != nil {
		return nil, fmt.Errorf("%w: expected RESP array command frame: %v", ErrInvalidCommand, err)
	}
	if len(arr) == 0 {
		return nil, fmt.Errorf("%w: empty command array", ErrInvalidCommand)
	}

	args := make([]string, len(arr))
	for i, item := range arr {
		s, err := item.AsString()
		if err != nil {
			return nil, fmt.Errorf("%w: argument %d is not a valid string: %v", ErrInvalidCommand, i, err)
		}
		args[i] = s
	}

	rewrittenArgs, modified, err := g.InspectCommand(args, sessionUsername)
	if err != nil {
		return nil, err
	}

	var rewrittenVal resp.Value
	if modified {
		items := make([]resp.Value, len(rewrittenArgs))
		for i, s := range rewrittenArgs {
			items[i] = resp.Value{
				Type: resp.TypeBulkString,
				Str:  s,
			}
		}
		rewrittenVal = resp.Value{
			Type:  resp.TypeArray,
			Array: items,
		}
	} else {
		rewrittenVal = val
	}

	return &InspectionResult{
		Original:  val,
		Rewritten: rewrittenVal,
		Args:      rewrittenArgs,
		Modified:  modified,
	}, nil
}

// InspectCommand applies the guard rules to a slice of command arguments.
// It returns the rewritten arguments slice, a boolean indicating if changes were made, and an error if validation fails.
func (g *GuardEngine) InspectCommand(args []string, sessionUsername string) ([]string, bool, error) {
	if len(args) == 0 {
		return nil, false, fmt.Errorf("%w: empty command", ErrInvalidCommand)
	}

	// Rule 2 (Payload Size Capping): If any argument exceeds MaxPayloadBytes, reject immediately.
	for _, arg := range args {
		if len(arg) > g.cfg.MaxPayloadBytes {
			return nil, false, fmt.Errorf("%w", ErrPayloadTooLarge)
		}
	}

	cmdName := strings.ToUpper(strings.TrimSpace(args[0]))
	if cmdName != "XADD" {
		return args, false, nil
	}

	// XADD command parsing
	if len(args) < 2 {
		return nil, false, fmt.Errorf("%w: XADD requires a target stream key", ErrInvalidCommand)
	}

	targetStream := args[1]
	isPeerInbox := isPeerInboxStream(targetStream, sessionUsername)

	// Parse arguments following stream key:
	// Options: [NOMKSTREAM] [MAXLEN|MINID [=|~] threshold [LIMIT count]] <*|id> <field value [field value ...]>
	idx := 2
	var hasTrimming bool
	trimmingStart := -1
	trimmingEnd := -1
	var maxlenValIdx int = -1

	for idx < len(args) {
		upper := strings.ToUpper(strings.TrimSpace(args[idx]))
		if upper == "NOMKSTREAM" {
			idx++
			continue
		}
		if upper == "MAXLEN" || upper == "MINID" {
			hasTrimming = true
			trimmingStart = idx
			idx++
			// Optional [=|~]
			if idx < len(args) && (args[idx] == "=" || args[idx] == "~") {
				idx++
			}
			// Threshold value
			if idx < len(args) {
				if upper == "MAXLEN" {
					maxlenValIdx = idx
				}
				idx++
			}
			// Optional LIMIT count
			if idx < len(args) && strings.EqualFold(args[idx], "LIMIT") {
				idx++
				if idx < len(args) {
					idx++
				}
			}
			trimmingEnd = idx
			continue
		}
		break
	}

	// Rule 4 (Cross-Inbox History Defense):
	// If XADD targets a stream matching *:inbox where recipient != session username,
	// strip/reject any MAXLEN or MINID argument so external peers cannot truncate inbox history.
	if isPeerInbox && hasTrimming {
		if !g.cfg.StripPeerInboxTrimming {
			return nil, false, fmt.Errorf("%w", ErrPeerInboxTruncationForbidden)
		}
		// Strip trimming arguments
		newArgs := make([]string, 0, len(args)-(trimmingEnd-trimmingStart))
		newArgs = append(newArgs, args[:trimmingStart]...)
		newArgs = append(newArgs, args[trimmingEnd:]...)
		args = newArgs
		// Re-evaluate entry fields start position
		idx = trimmingStart
		hasTrimming = false
	}

	// Entry ID and Field-Value pairs
	// idx points to entry ID (<*|id>)
	if idx >= len(args) {
		return nil, false, fmt.Errorf("%w: XADD missing entry ID", ErrInvalidCommand)
	}
	// entryID := args[idx]
	idx++

	// Field-value pairs start from idx
	fields := args[idx:]
	if len(fields)%2 != 0 {
		return nil, false, fmt.Errorf("%w: XADD entry fields must be key-value pairs (got odd count)", ErrInvalidCommand)
	}

	// Rule 3 (Citation & Identity Validation):
	// Check fields for "citation" and "sender".
	var citationFound, senderFound bool
	var citationVal, senderVal string

	for i := 0; i < len(fields); i += 2 {
		k := strings.ToLower(fields[i])
		v := fields[i+1]
		if k == "citation" {
			citationFound = true
			citationVal = v
		} else if k == "sender" {
			senderFound = true
			senderVal = v
		}
	}

	if citationFound {
		if !citationRegex.MatchString(citationVal) {
			return nil, false, fmt.Errorf("%w", ErrInvalidCitation)
		}
	}

	if senderFound {
		normSession := strings.ToLower(strings.TrimSpace(sessionUsername))
		if normSession != "" && normSession != "admin" && normSession != "operator" {
			if !strings.EqualFold(strings.TrimSpace(senderVal), normSession) {
				return nil, false, fmt.Errorf("%w", ErrIdentityMismatch)
			}
		}
	}

	// Rule 1 (MAXLEN Injection & Clamping):
	// On XADD, if no MAXLEN or MINID argument is present, inject MAXLEN ~ <MaxStreamLen> immediately following the stream key.
	// If MAXLEN exceeds the configured ceiling, clamp it to <MaxStreamLen>.
	// (Note: Do not inject trimming on peer inbox if stripping is enabled or peer inbox rules apply).
	var modified bool
	if !isPeerInbox {
		if !hasTrimming {
			// Inject MAXLEN ~ <MaxStreamLen> immediately after stream key (index 2)
			newArgs := make([]string, 0, len(args)+3)
			newArgs = append(newArgs, args[:2]...)
			newArgs = append(newArgs, "MAXLEN", "~", strconv.FormatInt(g.cfg.MaxStreamLen, 10))
			newArgs = append(newArgs, args[2:]...)
			args = newArgs
			modified = true
		} else if maxlenValIdx != -1 {
			// Check if MAXLEN threshold exceeds configured MaxStreamLen
			valStr := args[maxlenValIdx]
			if val, err := strconv.ParseInt(valStr, 10, 64); err == nil {
				if val > g.cfg.MaxStreamLen {
					args[maxlenValIdx] = strconv.FormatInt(g.cfg.MaxStreamLen, 10)
					modified = true
				}
			}
		}
	}

	return args, modified, nil
}

// isPeer checks if the owner is different from callerID, with admin/operator bypass.
func isPeer(owner, callerID string) bool {
	caller := strings.ToLower(strings.TrimSpace(callerID))
	if caller == "" {
		return true
	}
	if caller == "admin" || caller == "operator" {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(owner), caller)
}

// isPeerInboxStream checks if key ends with :inbox and recipient != sessionUsername.
func isPeerInboxStream(key, callerID string) bool {
	lowerKey := strings.ToLower(strings.TrimSpace(key))
	if !strings.HasSuffix(lowerKey, ":inbox") {
		return false
	}
	owner := strings.TrimSuffix(lowerKey, ":inbox")
	return isPeer(owner, callerID)
}
