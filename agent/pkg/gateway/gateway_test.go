// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/boggycreek/sandbox/agent/pkg/runtime"
	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
)

// --- Verifier Tests ---

func TestVerifier(t *testing.T) {
	priv, pub, err := libbp.GenerateKeypair()
	if err != nil {
		t.Fatalf("failed to generate keypair: %v", err)
	}

	resolver := NewMemoryKeyResolver()
	resolver.SetKey("alice", pub)

	verifier := NewVerifier(resolver)
	payload := []byte("build agent binary")
	sig := libbp.SignPayload(priv, payload)

	ctx := context.Background()

	// 1. Valid signature
	if err := verifier.Verify(ctx, "alice", payload, sig); err != nil {
		t.Fatalf("expected valid signature, got error: %v", err)
	}

	// 2. Cached key works
	if err := verifier.Verify(ctx, "alice", payload, sig); err != nil {
		t.Fatalf("expected cached key verification to succeed, got: %v", err)
	}

	// 3. Corrupt signature
	if err := verifier.Verify(ctx, "alice", payload, "corrupt-sig"); !errors.Is(err, ErrSignatureVerificationFailed) {
		t.Errorf("expected ErrSignatureVerificationFailed, got: %v", err)
	}

	// 4. Missing signature
	if err := verifier.Verify(ctx, "alice", payload, ""); !errors.Is(err, ErrSignatureVerificationFailed) {
		t.Errorf("expected ErrSignatureVerificationFailed on empty sig, got: %v", err)
	}

	// 5. Unknown sender
	if err := verifier.Verify(ctx, "bob", payload, sig); !errors.Is(err, ErrPublicKeyNotFound) {
		t.Errorf("expected ErrPublicKeyNotFound, got: %v", err)
	}

	// 6. Nil resolver
	nilVerifier := NewVerifier(nil)
	if err := nilVerifier.Verify(ctx, "alice", payload, sig); !errors.Is(err, ErrPublicKeyNotFound) {
		t.Errorf("expected ErrPublicKeyNotFound with nil resolver, got: %v", err)
	}
}

type mockIdentityFetcher struct {
	records map[string]*libbp.IdentityRecord
	err     error
}

func (m *mockIdentityFetcher) GetIdentity(_ context.Context, id string) (*libbp.IdentityRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	rec, ok := m.records[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return rec, nil
}

func TestBackplaneKeyResolver(t *testing.T) {
	_, pub, err := libbp.GenerateKeypair()
	if err != nil {
		t.Fatalf("failed to generate keypair: %v", err)
	}
	pubB64 := libbp.EncodePublicKeyBase64(pub)

	fetcher := &mockIdentityFetcher{
		records: map[string]*libbp.IdentityRecord{
			"agent-1": {Name: "agent-1", PubKey: pubB64},
			"empty":   {Name: "empty", PubKey: ""},
			"invalid": {Name: "invalid", PubKey: "not-base64---"},
		},
	}

	resolver := NewBackplaneKeyResolver(fetcher)
	ctx := context.Background()

	// 1. Success
	resolved, err := resolver.ResolvePublicKey(ctx, "agent-1")
	if err != nil {
		t.Fatalf("expected successful resolution, got: %v", err)
	}
	if !pub.Equal(resolved) {
		t.Errorf("resolved key does not match original key")
	}

	// 2. Not found
	if _, err := resolver.ResolvePublicKey(ctx, "agent-unknown"); !errors.Is(err, ErrPublicKeyNotFound) {
		t.Errorf("expected ErrPublicKeyNotFound for unknown identity, got: %v", err)
	}

	// 3. Empty public key
	if _, err := resolver.ResolvePublicKey(ctx, "empty"); !errors.Is(err, ErrPublicKeyNotFound) {
		t.Errorf("expected ErrPublicKeyNotFound for empty pubkey, got: %v", err)
	}

	// 4. Invalid base64
	if _, err := resolver.ResolvePublicKey(ctx, "invalid"); err == nil {
		t.Error("expected error for invalid base64 pubkey")
	}

	// 5. Nil fetcher
	nilResolver := NewBackplaneKeyResolver(nil)
	if _, err := nilResolver.ResolvePublicKey(ctx, "agent-1"); !errors.Is(err, ErrPublicKeyNotFound) {
		t.Errorf("expected ErrPublicKeyNotFound with nil fetcher, got: %v", err)
	}
}

// --- Mock Backplane Client ---

type mockBackplaneClient struct {
	mu            sync.Mutex
	inboxMessages []*libbp.Message
	toldMessages  []struct {
		recipient string
		content   string
	}
	recvError error
	status    string
	closed    bool
}

func (m *mockBackplaneClient) Recv(ctx context.Context, blockSeconds int) ([]*libbp.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.recvError != nil {
		err := m.recvError
		m.recvError = nil // reset for next poll
		return nil, err
	}

	if len(m.inboxMessages) == 0 {
		// Wait or return empty
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
			return nil, nil
		}
	}

	msgs := m.inboxMessages
	m.inboxMessages = nil
	return msgs, nil
}

func (m *mockBackplaneClient) Tell(ctx context.Context, recipient string, content string) (*libbp.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toldMessages = append(m.toldMessages, struct {
		recipient string
		content   string
	}{recipient, content})
	return &libbp.Message{ID: "msg-1", Destination: recipient, Content: content}, nil
}

func (m *mockBackplaneClient) Say(ctx context.Context, content string, blobPath string) (*libbp.Message, error) {
	return &libbp.Message{ID: "say-1", Content: content}, nil
}

func (m *mockBackplaneClient) SetStatus(ctx context.Context, statusText string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = statusText
	return nil
}

func (m *mockBackplaneClient) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockBackplaneClient) push(msg *libbp.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inboxMessages = append(m.inboxMessages, msg)
}

// --- Gateway Tests ---

func TestValkeyGatewayP3FastReflex(t *testing.T) {
	client := &mockBackplaneClient{}
	gateway := NewValkeyGateway("agent-1", client, nil, 1)

	if gateway.ID() != "gateway-valkey" || !gateway.IsResilient() {
		t.Fatalf("unexpected ID or resilience: %s, %v", gateway.ID(), gateway.IsResilient())
	}

	bus := runtime.NewEventBus()
	defer bus.Close()
	state := runtime.NewSharedState("", "agent-1")
	_ = state.Update(func(s *runtime.AgentState) {
		s.Status = "working"
		s.CurrentTaskID = "task-99"
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = gateway.Start(ctx, bus, state)
	}()

	// 1. Send PING message
	client.push(&libbp.Message{
		ID:      "m1",
		Sender:  "operator",
		Content: "PING",
	})

	// 2. Send STATUS query
	client.push(&libbp.Message{
		ID:      "m2",
		Sender:  "peer-agent",
		Content: "STATUS",
	})

	// 3. Send JSON ping
	client.push(&libbp.Message{
		ID:      "m3",
		Sender:  "peer-agent",
		Content: `{"action":"ping"}`,
	})

	// 4. Send JSON status query
	client.push(&libbp.Message{
		ID:      "m4",
		Sender:  "peer-agent",
		Content: `{"type":"status_query"}`,
	})

	time.Sleep(100 * time.Millisecond)

	// Verify responses
	client.mu.Lock()
	tolds := client.toldMessages
	client.mu.Unlock()

	if len(tolds) != 4 {
		t.Fatalf("expected 4 fast-reflex responses, got %d", len(tolds))
	}
	if tolds[0].recipient != "operator" || tolds[0].content != "PONG" {
		t.Errorf("unexpected PING response: %+v", tolds[0])
	}

	var statusResp runtime.AgentState
	if err := json.Unmarshal([]byte(tolds[1].content), &statusResp); err != nil {
		t.Fatalf("failed to unmarshal status response: %v", err)
	}
	if statusResp.AgentID != "agent-1" || statusResp.Status != "working" {
		t.Errorf("unexpected status content: %+v", statusResp)
	}

	// Crucial: Event bus should have received ZERO events! (fast reflex does not invoke REPL)
	if bus.TotalQueueDepth() != 0 {
		t.Errorf("expected 0 events on event bus, got %d", bus.TotalQueueDepth())
	}
}

func TestValkeyGatewayP0Cancellation(t *testing.T) {
	client := &mockBackplaneClient{}
	gateway := NewValkeyGateway("agent-1", client, nil, 1)

	var canceled atomic.Bool
	gateway.SetCancelFunc(func() {
		canceled.Store(true)
	})

	bus := runtime.NewEventBus()
	defer bus.Close()
	state := runtime.NewSharedState("", "agent-1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = gateway.Start(ctx, bus, state)
	}()

	client.push(&libbp.Message{
		ID:      "m-cancel",
		Sender:  "human",
		Content: "CANCEL",
	})

	time.Sleep(50 * time.Millisecond)

	if !canceled.Load() {
		t.Fatal("expected cancelFunc to be invoked on P0 CANCEL directive")
	}

	evt, ok := bus.Poll()
	if !ok || evt.Priority != runtime.P0_Control {
		t.Fatalf("expected P0_Control event on bus, got: %+v", evt)
	}
}

func TestValkeyGatewayP1AndP2Dispatch(t *testing.T) {
	client := &mockBackplaneClient{}
	gateway := NewValkeyGateway("agent-1", client, nil, 1)

	bus := runtime.NewEventBus()
	defer bus.Close()
	state := runtime.NewSharedState("", "agent-1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = gateway.Start(ctx, bus, state)
	}()

	// P1: Addressed to agent-1
	client.push(&libbp.Message{
		ID:          "m-p1",
		Sender:      "human",
		Destination: "agent-1",
		Content:     "please implement feature X",
	})

	// P2: Broadcast chatter
	client.push(&libbp.Message{
		ID:          "m-p2",
		Sender:      "agent-2",
		Destination: "",
		Content:     "finished test run 14",
	})

	time.Sleep(50 * time.Millisecond)

	p01, ok := bus.Poll()
	if !ok || p01.Priority != runtime.P1_HighPriority || p01.Target != "repl-main" {
		t.Fatalf("expected P1_HighPriority event, got: %+v", p01)
	}

	p02, ok := bus.Poll()
	if !ok || p02.Priority != runtime.P2_StandardAsync || p02.Target != "*" {
		t.Fatalf("expected P2_StandardAsync event, got: %+v", p02)
	}
}

func TestValkeyGatewaySignatureVerification(t *testing.T) {
	priv, pub, _ := libbp.GenerateKeypair()
	resolver := NewMemoryKeyResolver()
	resolver.SetKey("trusted-peer", pub)
	verifier := NewVerifier(resolver)

	client := &mockBackplaneClient{}
	gateway := NewValkeyGateway("agent-1", client, verifier, 1)

	bus := runtime.NewEventBus()
	defer bus.Close()
	state := runtime.NewSharedState("", "agent-1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = gateway.Start(ctx, bus, state)
	}()

	// 0. Unsigned message -> rejected
	client.push(&libbp.Message{
		ID:       "m-unsigned",
		Sender:   "trusted-peer",
		Content:  "unsigned message",
		IsSigned: false,
	})

	// 1. Signed message with invalid signature -> rejected
	client.push(&libbp.Message{
		ID:        "m-bad",
		Sender:    "trusted-peer",
		Content:   "bad message",
		Signature: "invalid-signature",
		IsSigned:  true,
	})

	// 2. Signed message over raw content alone -> rejected
	rawContent := "raw content only"
	rawSig := libbp.SignPayload(priv, []byte(rawContent))
	client.push(&libbp.Message{
		ID:        "m-raw-sig",
		Sender:    "trusted-peer",
		Content:   rawContent,
		Signature: rawSig,
		IsSigned:  true,
	})

	// 3. Signed message with valid envelope signature -> accepted
	validMsg := &libbp.Message{
		ID:        "m-good",
		Sender:    "trusted-peer",
		Content:   "good message",
		IsSigned:  true,
	}
	validSig := libbp.SignPayload(priv, validMsg.SigningPayload())
	validMsg.Signature = validSig
	client.push(validMsg)

	time.Sleep(50 * time.Millisecond)

	// First event should be warning for unsigned message
	e0, ok := bus.Poll()
	if !ok || e0.Source != "gateway:verifier" {
		t.Fatalf("expected verifier warning event for unsigned message, got: %+v", e0)
	}

	// Second event should be warning for bad message
	e1, ok := bus.Poll()
	if !ok || e1.Source != "gateway:verifier" {
		t.Fatalf("expected verifier warning event for bad sig, got: %+v", e1)
	}

	// Third event should be warning for raw content signature
	e2, ok := bus.Poll()
	if !ok || e2.Source != "gateway:verifier" {
		t.Fatalf("expected verifier warning event for raw content sig, got: %+v", e2)
	}
	if warn, ok := e2.Payload.(map[string]any)["warning"].(string); !ok || warn != "rejected unverified message signature" {
		t.Fatalf("unexpected warning message for raw content sig: %v", e2.Payload)
	}

	// Fourth event should be the good message
	e3, ok := bus.Poll()
	if !ok || e3.Source != "valkey:inbox" {
		t.Fatalf("expected inbox message event, got: %+v", e3)
	}
}

func TestValkeyGatewayRecvErrorRecovery(t *testing.T) {
	client := &mockBackplaneClient{
		recvError: errors.New("simulated network blip"),
	}
	gateway := NewValkeyGateway("agent-1", client, nil, 1)

	bus := runtime.NewEventBus()
	defer bus.Close()
	state := runtime.NewSharedState("", "agent-1")

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- gateway.Start(ctx, bus, state)
	}()

	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("gateway did not exit upon cancellation")
	}
}

func TestGatewayEdgeCases(t *testing.T) {
	// 1. NewValkeyGateway with default pollTimeoutSec
	gwDefault := NewValkeyGateway("Agent-Default", nil, nil, 0)
	if gwDefault.pollTimeoutSec != 1 || gwDefault.agentID != "agent-default" {
		t.Errorf("expected default pollTimeoutSec=1 and lowercase agentID, got %d, %s", gwDefault.pollTimeoutSec, gwDefault.agentID)
	}

	// 2. Start with already canceled context
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	bus := runtime.NewEventBus()
	defer bus.Close()
	state := runtime.NewSharedState("", "agent-1")
	client := &mockBackplaneClient{}
	gw := NewValkeyGateway("agent-1", client, nil, 1)
	if err := gw.Start(canceledCtx, bus, state); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	// 3. handleMessage with nil message
	gw.handleMessage(context.Background(), nil, bus, state)

	// 4. isP0Directive with JSON action=cancel and halt
	if !isP0Directive(`{"action":"cancel"}`) {
		t.Error("expected true for JSON action=cancel")
	}
	if !isP0Directive(`{"action":"halt"}`) {
		t.Error("expected true for JSON action=halt")
	}
	if isP0Directive(`{"action":"other"}`) {
		t.Error("expected false for JSON action=other")
	}
	if isP0Directive("random text") {
		t.Error("expected false for random text")
	}

	// 5. isP3Query with JSON action=query_status and action=ping
	isP3, qType := isP3Query(`{"action":"query_status"}`)
	if !isP3 || qType != "STATUS" {
		t.Errorf("expected query_status to be P3 STATUS, got %v, %s", isP3, qType)
	}
	isP3, qType = isP3Query(`{"action":"ping"}`)
	if !isP3 || qType != "PING" {
		t.Errorf("expected ping action to be P3 PING, got %v, %s", isP3, qType)
	}

	// 6. isAutomatedNotification
	if !isAutomatedNotification("Completed directive: tests passed") {
		t.Error("expected true for 'Completed directive:'")
	}
	if !isAutomatedNotification("Execution failed (code 1)") {
		t.Error("expected true for 'Execution failed'")
	}
	if !isAutomatedNotification("Understood. Directive queued") {
		t.Error("expected true for 'Understood. Directive queued'")
	}
	if !isAutomatedNotification("Status: working on task") {
		t.Error("expected true for 'Status:'")
	}
	if !isAutomatedNotification("Online (container started)") {
		t.Error("expected true for 'Online (container started)'")
	}
	if isAutomatedNotification("Please implement string compression") {
		t.Error("expected false for regular user directive")
	}

	// 7. isP1Message
	if gw.isP1Message(&libbp.Message{Destination: "agent-1", Content: "Completed directive: done"}) {
		t.Error("expected false for automated completion addressed to agent-1")
	}
	if !gw.isP1Message(&libbp.Message{Destination: "agent-1", Content: "Please write a test"}) {
		t.Error("expected true for regular message addressed to agent-1")
	}
	if !gw.isP1Message(&libbp.Message{Sender: "operator", Content: "Hello"}) {
		t.Error("expected true for message from operator")
	}
	if gw.isP1Message(&libbp.Message{Destination: "agent-2", Sender: "peer", Content: "Hello"}) {
		t.Error("expected false for unaddressed message from peer")
	}
}
