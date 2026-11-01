// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
	"github.com/boggycreek/fleet/backplane/pkg/libbp/resp"
)

type mockPingServer struct {
	listener  net.Listener
	mu        sync.Mutex
	published []string
	channels  []string
	failPing  bool
}

func startMockPingServer(t *testing.T) (*mockPingServer, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	m := &mockPingServer{
		listener:  listener,
		published: make([]string, 0),
		channels:  make([]string, 0),
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := resp.NewReader(c)
				w := resp.NewWriter(c)
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
					m.mu.Lock()
					switch cmd {
					case "AUTH":
						_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "OK"})
					case "PING":
						_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "PONG"})
					case "INCR":
						_ = w.WriteValue(resp.Value{Type: resp.TypeInteger, Num: 1})
					case "XADD":
						_ = w.WriteValue(resp.Value{Type: resp.TypeBulkString, Str: "1700000000000-0"})
					case "PUBLISH":
						if m.failPing {
							_ = w.WriteValue(resp.Value{Type: resp.TypeError, Str: "ERR publish failed"})
						} else {
							ch := ""
							payload := ""
							if len(args) > 1 {
								ch = args[1].String()
							}
							if len(args) > 2 {
								payload = args[2].String()
							}
							m.channels = append(m.channels, ch)
							m.published = append(m.published, payload)
							_ = w.WriteValue(resp.Value{Type: resp.TypeInteger, Num: 1})
						}
					default:
						_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "OK"})
					}
					m.mu.Unlock()
				}
			}(conn)
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	return m, port
}

func (m *mockPingServer) close() {
	if m.listener != nil {
		_ = m.listener.Close()
	}
}

func TestWakeupPingsOnSayTellReply(t *testing.T) {
	srv, port := startMockPingServer(t)
	defer srv.close()

	cfg := libbp.ClientConfig{
		Host:           "127.0.0.1",
		Port:           port,
		AgentID:        "agent-sender",
		ConnectTimeout: 2 * time.Second,
	}

	ctx := context.Background()

	// 1. bp say -> publishes to ping:broadcast (PingChannel)
	{
		var stdout, stderr bytes.Buffer
		code := handleSay(ctx, cfg, []string{"hello", "fleet"}, nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("handleSay failed: %s", stderr.String())
		}

		srv.mu.Lock()
		if len(srv.channels) != 1 || len(srv.published) != 1 {
			srv.mu.Unlock()
			t.Fatalf("expected 1 published ping for say, got channels=%v", srv.channels)
		}
		expectedChannel := "ping:broadcast"
		if srv.channels[0] != expectedChannel {
			t.Errorf("expected say channel %q, got %q", expectedChannel, srv.channels[0])
		}
		if srv.channels[0] != libbp.PingKey(libbp.PingChannel) {
			t.Errorf("expected channel to match PingKey(PingChannel)")
		}
		var payload libbp.PingPayload
		if err := json.Unmarshal([]byte(srv.published[0]), &payload); err != nil {
			t.Fatalf("failed unmarshaling ping payload: %v", err)
		}
		if payload.Type != "ping" || payload.Sender != "agent-sender" {
			t.Errorf("unexpected payload: %+v", payload)
		}
		srv.mu.Unlock()
	}

	// 2. bp tell -> publishes to target agent
	{
		var stdout, stderr bytes.Buffer
		code := handleTell(ctx, cfg, []string{"agent-receiver", "direct", "message"}, nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("handleTell failed: %s", stderr.String())
		}

		srv.mu.Lock()
		if len(srv.channels) != 2 || len(srv.published) != 2 {
			srv.mu.Unlock()
			t.Fatalf("expected 2 published pings, got channels=%v", srv.channels)
		}
		expectedChannel := libbp.PingKey("agent-receiver")
		if srv.channels[1] != expectedChannel {
			t.Errorf("expected tell channel %q, got %q", expectedChannel, srv.channels[1])
		}
		var payload libbp.PingPayload
		if err := json.Unmarshal([]byte(srv.published[1]), &payload); err != nil {
			t.Fatalf("failed unmarshaling ping payload: %v", err)
		}
		if payload.Stream != "agent-receiver:inbox" || payload.Sender != "agent-sender" {
			t.Errorf("unexpected payload: %+v", payload)
		}
		srv.mu.Unlock()
	}

	// 3. bp reply -> publishes to recipient
	{
		var stdout, stderr bytes.Buffer
		code := handleReply(ctx, cfg, []string{"1700000000000-0", "agent-receiver", "reply", "text"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("handleReply failed: %s", stderr.String())
		}

		srv.mu.Lock()
		if len(srv.channels) != 3 || len(srv.published) != 3 {
			srv.mu.Unlock()
			t.Fatalf("expected 3 published pings, got channels=%v", srv.channels)
		}
		expectedChannel := libbp.PingKey("agent-receiver")
		if srv.channels[2] != expectedChannel {
			t.Errorf("expected reply channel %q, got %q", expectedChannel, srv.channels[2])
		}
		var payload libbp.PingPayload
		if err := json.Unmarshal([]byte(srv.published[2]), &payload); err != nil {
			t.Fatalf("failed unmarshaling ping payload: %v", err)
		}
		if payload.Stream != "agent-receiver:inbox" || payload.Sender != "agent-sender" {
			t.Errorf("unexpected payload: %+v", payload)
		}
		srv.mu.Unlock()
	}
}

func TestWakeupPingsFailureIsNonFatal(t *testing.T) {
	srv, port := startMockPingServer(t)
	defer srv.close()

	srv.mu.Lock()
	srv.failPing = true
	srv.mu.Unlock()

	cfg := libbp.ClientConfig{
		Host:           "127.0.0.1",
		Port:           port,
		AgentID:        "agent-sender",
		ConnectTimeout: 2 * time.Second,
	}

	ctx := context.Background()

	// Say with ping error should still succeed (return 0) and log warning to stderr
	{
		var stdout, stderr bytes.Buffer
		code := handleSay(ctx, cfg, []string{"say", "test"}, nil, &stdout, &stderr)
		if code != 0 {
			t.Errorf("expected code 0 on ping failure, got %d", code)
		}
		if !strings.Contains(stderr.String(), "warning: failed publishing wakeup ping") {
			t.Errorf("expected warning in stderr, got: %s", stderr.String())
		}
	}

	// Tell with ping error should still succeed (return 0) and log warning to stderr
	{
		var stdout, stderr bytes.Buffer
		code := handleTell(ctx, cfg, []string{"agent-receiver", "tell", "test"}, nil, &stdout, &stderr)
		if code != 0 {
			t.Errorf("expected code 0 on ping failure, got %d", code)
		}
		if !strings.Contains(stderr.String(), "warning: failed publishing wakeup ping") {
			t.Errorf("expected warning in stderr, got: %s", stderr.String())
		}
	}

	// Reply with ping error should still succeed (return 0) and log warning to stderr
	{
		var stdout, stderr bytes.Buffer
		code := handleReply(ctx, cfg, []string{"1700000000000-0", "agent-receiver", "reply", "test"}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("expected code 0 on ping failure, got %d", code)
		}
		if !strings.Contains(stderr.String(), "warning: failed publishing wakeup ping") {
			t.Errorf("expected warning in stderr, got: %s", stderr.String())
		}
	}
}
