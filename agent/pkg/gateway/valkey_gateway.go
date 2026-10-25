// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/boggycreek/sandbox/agent/pkg/runtime"
	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
	"github.com/boggycreek/sandbox/backplane/pkg/libbpd"
)

// BackplaneClient abstracts Valkey stream polling and messaging operations.
type BackplaneClient interface {
	Recv(ctx context.Context, blockSeconds int) ([]*libbp.Message, error)
	Tell(ctx context.Context, recipient string, content string) (*libbp.Message, error)
	Say(ctx context.Context, content string, blobPath string) (*libbp.Message, error)
	SetStatus(ctx context.Context, statusText string) error
	Close() error
}

// ValkeyGateway provides fast-reflex stream polling, instant P3 telemetry responses,
// and priority-classified event bus dispatching.
type ValkeyGateway struct {
	agentID        string
	client         BackplaneClient
	verifier       *Verifier
	pollTimeoutSec int
	mu             sync.RWMutex
	cancelFunc     context.CancelFunc
}

// NewValkeyGateway creates a new ValkeyGateway worker.
func NewValkeyGateway(agentID string, client BackplaneClient, verifier *Verifier, pollTimeoutSec int) *ValkeyGateway {
	if pollTimeoutSec <= 0 {
		pollTimeoutSec = 1
	}
	return &ValkeyGateway{
		agentID:        strings.ToLower(agentID),
		client:         client,
		verifier:       verifier,
		pollTimeoutSec: pollTimeoutSec,
	}
}

// ID implements runtime.Subsystem.
func (g *ValkeyGateway) ID() string {
	return "gateway-valkey"
}

// IsResilient implements runtime.ResilientSubsystem.
func (g *ValkeyGateway) IsResilient() bool {
	return true
}

// SetCancelFunc registers a cancellation hook triggered when P0 directives arrive.
func (g *ValkeyGateway) SetCancelFunc(cancel context.CancelFunc) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cancelFunc = cancel
}

// Start begins listening to incoming messages with blocking XREAD BLOCK and dispatches events until ctx is canceled.
func (g *ValkeyGateway) Start(ctx context.Context, bus *runtime.EventBus, state *runtime.SharedState) error {
	listener := libbpd.NewStreamListener(g.client, g.pollTimeoutSec)
	return listener.Listen(ctx, func(msgCtx context.Context, msg *libbp.Message) error {
		g.handleMessage(msgCtx, msg, bus, state)
		return nil
	})
}

func (g *ValkeyGateway) handleMessage(ctx context.Context, msg *libbp.Message, bus *runtime.EventBus, state *runtime.SharedState) {
	if msg == nil {
		return
	}

	// 1. Signature Verification
	if g.verifier != nil {
		if !msg.IsSigned || msg.Signature == "" {
			_ = bus.Publish(runtime.Event{
				Priority: runtime.P2_StandardAsync,
				Source:   "gateway:verifier",
				Target:   "*",
				Payload: map[string]any{
					"warning": "rejected unsigned message",
					"sender":  msg.Sender,
					"msg_id":  msg.ID,
				},
			})
			return
		}

		payload := msg.SigningPayload()
		if err := g.verifier.Verify(ctx, msg.Sender, payload, msg.Signature); err != nil {
			_ = bus.Publish(runtime.Event{
				Priority: runtime.P2_StandardAsync,
				Source:   "gateway:verifier",
				Target:   "*",
				Payload: map[string]any{
					"warning": "rejected unverified message signature",
					"sender":  msg.Sender,
					"msg_id":  msg.ID,
					"error":   err.Error(),
				},
			})
			return
		}
		msg.IsVerified = true
	}

	trimmedContent := strings.TrimSpace(msg.Content)

	// 2. P3 Fast Reflex: Instant telemetry and ping handling directly from memory
	if isP3, queryType := isP3Query(trimmedContent); isP3 {
		g.handleP3Reflex(ctx, queryType, msg, state)
		return
	}

	// 3. P0 Emergency Control Directives
	if isP0Directive(trimmedContent) {
		g.mu.RLock()
		cancel := g.cancelFunc
		g.mu.RUnlock()
		if cancel != nil {
			cancel()
		}

		_ = bus.Publish(runtime.Event{
			ID:        fmt.Sprintf("evt-p0-%s", msg.ID),
			Priority:  runtime.P0_Control,
			Source:    "valkey:inbox",
			Target:    "*",
			Payload:   msg,
			Timestamp: time.Now().UTC(),
		})
		return
	}

	// 4. P1 Direct User or Peer Tasks
	if g.isP1Message(msg) {
		_ = bus.Publish(runtime.Event{
			ID:        fmt.Sprintf("evt-p1-%s", msg.ID),
			Priority:  runtime.P1_HighPriority,
			Source:    "valkey:inbox",
			Target:    "repl-main",
			Payload:   msg,
			Timestamp: time.Now().UTC(),
		})
		return
	}

	// 5. P2 Standard Asynchronous Messages
	_ = bus.Publish(runtime.Event{
		ID:        fmt.Sprintf("evt-p2-%s", msg.ID),
		Priority:  runtime.P2_StandardAsync,
		Source:    "valkey:inbox",
		Target:    "*",
		Payload:   msg,
		Timestamp: time.Now().UTC(),
	})
}

func (g *ValkeyGateway) handleP3Reflex(ctx context.Context, queryType string, msg *libbp.Message, state *runtime.SharedState) {
	switch queryType {
	case "PING":
		_, _ = g.client.Tell(ctx, msg.Sender, "PONG")
	case "STATUS":
		st := state.Read()
		data, err := json.Marshal(st)
		if err == nil {
			_, _ = g.client.Tell(ctx, msg.Sender, string(data))
		}
	}
}

func isP3Query(content string) (bool, string) {
	upper := strings.ToUpper(content)
	if upper == "PING" {
		return true, "PING"
	}
	if upper == "STATUS" || upper == "?" {
		return true, "STATUS"
	}

	// Check JSON structures
	var req map[string]any
	if err := json.Unmarshal([]byte(content), &req); err == nil {
		if action, ok := req["action"].(string); ok {
			switch strings.ToLower(action) {
			case "ping":
				return true, "PING"
			case "status", "query_status":
				return true, "STATUS"
			}
		}
		if reqType, ok := req["type"].(string); ok {
			switch strings.ToLower(reqType) {
			case "ping":
				return true, "PING"
			case "status_query":
				return true, "STATUS"
			}
		}
	}

	return false, ""
}

func isP0Directive(content string) bool {
	upper := strings.ToUpper(content)
	if upper == "CANCEL" || upper == "HALT" || upper == "STOP" || upper == "KILL" {
		return true
	}

	var req map[string]any
	if err := json.Unmarshal([]byte(content), &req); err == nil {
		if action, ok := req["action"].(string); ok {
			act := strings.ToLower(action)
			if act == "cancel" || act == "halt" || act == "stop" {
				return true
			}
		}
	}
	return false
}

func isAutomatedNotification(content string) bool {
	lower := strings.ToLower(strings.TrimSpace(content))
	prefixes := []string{
		"completed directive:",
		"execution failed",
		"understood. directive queued",
		"status:",
		"online (container started)",
		"ok (",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

func (g *ValkeyGateway) isP1Message(msg *libbp.Message) bool {
	if isAutomatedNotification(msg.Content) {
		return false
	}
	// Directly addressed to this agent
	if strings.EqualFold(msg.Destination, g.agentID) {
		return true
	}
	// Direct human directive
	if strings.EqualFold(msg.Sender, "human") || strings.EqualFold(msg.Sender, "operator") {
		return true
	}
	return false
}
