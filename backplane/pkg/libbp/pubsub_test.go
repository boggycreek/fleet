// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package libbp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp/resp"
)

// mockPubSubServer handles PUBLISH, SUBSCRIBE, and AUTH commands
type mockPubSubServer struct {
	listener     net.Listener
	mu           sync.Mutex
	subs         map[string][]net.Conn
	subConns     []net.Conn
	published    []string
	authFail     bool
	subFail      bool
	closed       bool
	subscribed   chan string
	subWriteErr  bool
	authWriteErr bool
	badFrame     bool
}

func startMockPubSubServer(t *testing.T) (*mockPubSubServer, int) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed starting mock pubsub server: %v", err)
	}

	srv := &mockPubSubServer{
		listener:   listener,
		subs:       make(map[string][]net.Conn),
		subscribed: make(chan string, 10),
	}

	go srv.serve()
	port := listener.Addr().(*net.TCPAddr).Port
	return srv, port
}

func (s *mockPubSubServer) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.listener != nil {
		_ = s.listener.Close()
	}
	for _, c := range s.subConns {
		_ = c.Close()
	}
}

func (s *mockPubSubServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *mockPubSubServer) handleConn(conn net.Conn) {
	r := resp.NewReader(conn)
	w := resp.NewWriter(conn)

	defer func() {
		_ = conn.Close()
		s.mu.Lock()
		for ch, conns := range s.subs {
			var updated []net.Conn
			for _, c := range conns {
				if c != conn {
					updated = append(updated, c)
				}
			}
			s.subs[ch] = updated
		}
		s.mu.Unlock()
	}()

	for {
		val, err := r.ReadValue()
		if err != nil {
			return
		}
		args, err := val.AsArray()
		if err != nil || len(args) == 0 {
			continue
		}

		cmd := strings.ToUpper(args[0].String())
		s.mu.Lock()
		switch cmd {
		case "AUTH":
			if s.authWriteErr {
				_ = conn.Close()
				s.mu.Unlock()
				return
			}
			if s.authFail {
				_ = w.WriteValue(resp.Value{Type: resp.TypeError, Str: "ERR invalid password"})
			} else {
				_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "OK"})
			}

		case "PUBLISH":
			channel := args[1].String()
			payload := args[2].String()
			s.published = append(s.published, payload)
			conns := append([]net.Conn(nil), s.subs[channel]...)
			bad := s.badFrame
			s.mu.Unlock()

			// Fan out to subscribers
			for _, c := range conns {
				sw := resp.NewWriter(c)
				if bad {
					// Send non-array and wrong kind frames to test skipping
					_ = sw.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "not-array"})
					_ = sw.WriteValue(resp.Value{
						Type: resp.TypeArray,
						Array: []resp.Value{
							{Type: resp.TypeBulkString, Str: "pong"},
							{Type: resp.TypeBulkString, Str: channel},
							{Type: resp.TypeBulkString, Str: "payload"},
						},
					})
				}
				_ = sw.WriteValue(resp.Value{
					Type: resp.TypeArray,
					Array: []resp.Value{
						{Type: resp.TypeBulkString, Str: "message"},
						{Type: resp.TypeBulkString, Str: channel},
						{Type: resp.TypeBulkString, Str: payload},
					},
				})
			}

			s.mu.Lock()
			_ = w.WriteValue(resp.Value{Type: resp.TypeInteger, Num: int64(len(conns))})

		case "SUBSCRIBE":
			if s.subWriteErr {
				_ = conn.Close()
				s.mu.Unlock()
				return
			}
			if s.subFail {
				_ = w.WriteValue(resp.Value{Type: resp.TypeError, Str: "ERR subscription rejected"})
				s.mu.Unlock()
				return
			}
			s.subConns = append(s.subConns, conn)
			for i := 1; i < len(args); i++ {
				channel := args[i].String()
				s.subs[channel] = append(s.subs[channel], conn)
				_ = w.WriteValue(resp.Value{
					Type: resp.TypeArray,
					Array: []resp.Value{
						{Type: resp.TypeBulkString, Str: "subscribe"},
						{Type: resp.TypeBulkString, Str: channel},
						{Type: resp.TypeInteger, Num: int64(i)},
					},
				})
				select {
				case s.subscribed <- channel:
				default:
				}
			}

		case "PING":
			_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "PONG"})

		default:
			_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "OK"})
		}
		s.mu.Unlock()
	}
}

func TestPublishPingSuccess(t *testing.T) {
	srv, port := startMockPubSubServer(t)
	defer srv.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := ClientConfig{
		Host:    "127.0.0.1",
		Port:    port,
		AgentID: "agent-1",
	}

	client, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer client.Close()

	if err := client.PublishPing(ctx, "agent-2"); err != nil {
		t.Fatalf("PublishPing failed: %v", err)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.published) != 1 {
		t.Fatalf("expected 1 published ping, got %d", len(srv.published))
	}

	var payload PingPayload
	if err := json.Unmarshal([]byte(srv.published[0]), &payload); err != nil {
		t.Fatalf("failed unmarshaling ping payload: %v", err)
	}

	if payload.Type != "ping" {
		t.Errorf("expected type 'ping', got %q", payload.Type)
	}
	if payload.Stream != "agent-2:inbox" {
		t.Errorf("expected stream 'agent-2:inbox', got %q", payload.Stream)
	}
	if payload.Sender != "agent-1" {
		t.Errorf("expected sender 'agent-1', got %q", payload.Sender)
	}
	if payload.TS <= 0 {
		t.Errorf("expected positive timestamp, got %d", payload.TS)
	}
}

func TestPublishPingValidationAndErrors(t *testing.T) {
	srv, port := startMockPubSubServer(t)
	defer srv.close()

	ctx := context.Background()
	cfg := ClientConfig{
		Host:    "127.0.0.1",
		Port:    port,
		AgentID: "agent-1",
	}

	client, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer client.Close()

	// 1. Empty recipient
	if err := client.PublishPing(ctx, ""); err == nil {
		t.Errorf("expected error for empty recipient, got nil")
	}
	if err := client.PublishPing(ctx, "   "); err == nil {
		t.Errorf("expected error for whitespace recipient, got nil")
	}

	// 2. Pre-cancelled context
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := client.PublishPing(cancelCtx, "agent-2"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	// 3. Closed client
	_ = client.Close()
	if err := client.PublishPing(ctx, "agent-2"); err == nil {
		t.Errorf("expected error on closed client, got nil")
	}
}

func TestSubscribePingEndToEnd(t *testing.T) {
	srv, port := startMockPubSubServer(t)
	defer srv.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfgPub := ClientConfig{
		Host:    "127.0.0.1",
		Port:    port,
		AgentID: "agent-sender",
	}
	pubClient, err := Dial(ctx, cfgPub)
	if err != nil {
		t.Fatalf("Dial pubClient failed: %v", err)
	}
	defer pubClient.Close()

	cfgSub := ClientConfig{
		Host:     "127.0.0.1",
		Port:     port,
		AgentID:  "agent-receiver",
		Password: "secret-password",
	}
	subClient, err := Dial(ctx, cfgSub)
	if err != nil {
		t.Fatalf("Dial subClient failed: %v", err)
	}
	defer subClient.Close()

	// Subscribe to ping:agent-receiver and ping:ensemble:fuzzers
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()

	srv.mu.Lock()
	srv.badFrame = true
	srv.mu.Unlock()

	ch, err := subClient.SubscribePing(subCtx, "ping:agent-receiver", "ping:ensemble:fuzzers")
	if err != nil {
		t.Fatalf("SubscribePing failed: %v", err)
	}

	// Wait for subscription confirmations
	select {
	case <-srv.subscribed:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for subscription confirmation")
	}

	// Publish ping to recipient
	if err := pubClient.PublishPing(ctx, "agent-receiver"); err != nil {
		t.Fatalf("PublishPing failed: %v", err)
	}

	// Receive ping
	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatalf("channel closed unexpectedly")
		}
		var p PingPayload
		if err := json.Unmarshal([]byte(msg), &p); err != nil {
			t.Fatalf("failed unmarshaling ping: %v", err)
		}
		if p.Sender != "agent-sender" || p.Stream != "agent-receiver:inbox" {
			t.Errorf("unexpected ping payload: %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for ping message")
	}

	// Cancel context and verify channel closes
	subCancel()
	select {
	case _, ok := <-ch:
		if ok {
			for range ch {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for channel close after context cancel")
	}
}

func TestSubscribePingValidationAndErrors(t *testing.T) {
	srv, port := startMockPubSubServer(t)
	defer srv.close()

	ctx := context.Background()
	cfg := ClientConfig{
		Host:     "127.0.0.1",
		Port:     port,
		AgentID:  "agent-sub",
		Password: "test-password",
	}
	client, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer client.Close()

	// 1. Pre-cancelled context
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := client.SubscribePing(cancelCtx, "ping:test"); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	// 2. Empty channels
	if _, err := client.SubscribePing(ctx); err == nil {
		t.Errorf("expected error for empty channels, got nil")
	}
	if _, err := client.SubscribePing(ctx, "   "); err == nil {
		t.Errorf("expected error for whitespace channel, got nil")
	}

	// 3. Dial failure (unreachable port)
	badClient := &Client{cfg: ClientConfig{Host: "127.0.0.1", Port: 64998, ConnectTimeout: 50 * time.Millisecond}}
	if _, err := badClient.SubscribePing(ctx, "ping:test"); err == nil {
		t.Errorf("expected dial error to unreachable port, got nil")
	}

	// 4. Auth failure
	srv.mu.Lock()
	srv.authFail = true
	srv.mu.Unlock()
	if _, err := client.SubscribePing(ctx, "ping:test"); err == nil {
		t.Errorf("expected auth error, got nil")
	}

	// 4b. Auth write/read error (server closes on auth)
	srv.mu.Lock()
	srv.authFail = false
	srv.authWriteErr = true
	srv.mu.Unlock()
	if _, err := client.SubscribePing(ctx, "ping:test"); err == nil {
		t.Errorf("expected auth write error, got nil")
	}

	srv.mu.Lock()
	srv.authWriteErr = false
	srv.subFail = true
	srv.mu.Unlock()

	// 5. Subscription confirmation failure
	if _, err := client.SubscribePing(ctx, "ping:test"); err == nil {
		t.Errorf("expected subscription error, got nil")
	}

	// 5b. Subscription write error (server closes on subscribe)
	srv.mu.Lock()
	srv.subFail = false
	srv.subWriteErr = true
	srv.mu.Unlock()
	if _, err := client.SubscribePing(ctx, "ping:test"); err == nil {
		t.Errorf("expected subscribe write error, got nil")
	}

	// 6. Subscribe with password only (no username/agentID)
	srv.mu.Lock()
	srv.subWriteErr = false
	srv.mu.Unlock()
	pwOnlyClient := &Client{cfg: ClientConfig{Host: "127.0.0.1", Port: port, Password: "test-password"}}
	ch, err := pwOnlyClient.SubscribePing(ctx, "ping:pwonly")
	if err != nil {
		t.Fatalf("expected success with password only, got %v", err)
	}
	if ch == nil {
		t.Fatal("expected non-nil channel")
	}
}

func TestSubscribePingDisconnect(t *testing.T) {
	srv, port := startMockPubSubServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := ClientConfig{
		Host:    "127.0.0.1",
		Port:    port,
		AgentID: "agent-dc",
	}
	client, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer client.Close()

	ch, err := client.SubscribePing(ctx, "ping:agent-dc")
	if err != nil {
		t.Fatalf("SubscribePing failed: %v", err)
	}

	// Close mock server while subscription is active
	srv.close()

	// Read loop should terminate and close the channel
	select {
	case _, ok := <-ch:
		if ok {
			for range ch {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for channel close on server disconnect")
	}
}

func TestGuardPublishAndSubscribe(t *testing.T) {
	// 1. Agent publishing to ping:* allowed
	if err := AssertAllowed("agent-1", "PUBLISH", "ping:agent-2", "{}"); err != nil {
		t.Errorf("expected PUBLISH ping:agent-2 allowed, got: %v", err)
	}
	if err := AssertAllowed("agent-1", "publish", "PING:ENSEMBLE:TEAM", "{}"); err != nil {
		t.Errorf("expected case-insensitive PUBLISH allowed, got: %v", err)
	}

	// 2. Agent publishing to non-ping channel forbidden
	if err := AssertAllowed("agent-1", "PUBLISH", "chat:general", "{}"); !errors.Is(err, ErrForbiddenCommand) {
		t.Errorf("expected ErrForbiddenCommand for chat:general, got: %v", err)
	}

	// 3. PUBLISH without channel forbidden
	if err := AssertAllowed("agent-1", "PUBLISH"); !errors.Is(err, ErrForbiddenCommand) {
		t.Errorf("expected ErrForbiddenCommand for PUBLISH with no args, got: %v", err)
	}

	// 4. Admin / Operator publishing to any channel allowed
	for _, admin := range []string{"admin", "operator"} {
		if err := AssertAllowed(admin, "PUBLISH", "system:events", "{}"); err != nil {
			t.Errorf("expected %s allowed to publish to any channel, got: %v", admin, err)
		}
	}

	// 5. SUBSCRIBE and UNSUBSCRIBE allowed
	if err := AssertAllowed("agent-1", "SUBSCRIBE", "ping:agent-1"); err != nil {
		t.Errorf("expected SUBSCRIBE allowed, got: %v", err)
	}
	if err := AssertAllowed("agent-1", "UNSUBSCRIBE", "ping:agent-1"); err != nil {
		t.Errorf("expected UNSUBSCRIBE allowed, got: %v", err)
	}
}
