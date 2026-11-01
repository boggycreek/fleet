// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package valkeyguard

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp/resp"
)

// mockUpstreamValkey implements a minimal mock Valkey server on a loopback TCP listener.
type mockUpstreamValkey struct {
	listener net.Listener
	mu       sync.Mutex
	commands [][]string
	closed   bool
}

func startMockUpstream(t *testing.T) *mockUpstreamValkey {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed starting mock Valkey upstream listener: %v", err)
	}

	m := &mockUpstreamValkey{listener: ln}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go m.handleConn(conn)
		}
	}()
	return m
}

func (m *mockUpstreamValkey) Addr() string {
	return m.listener.Addr().String()
}

func (m *mockUpstreamValkey) Close() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	if m.listener != nil {
		_ = m.listener.Close()
	}
}

func (m *mockUpstreamValkey) getCommands() [][]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([][]string, len(m.commands))
	copy(copied, m.commands)
	return copied
}

func (m *mockUpstreamValkey) handleConn(c net.Conn) {
	defer c.Close()
	r := resp.NewReader(c)

	for {
		val, err := r.ReadValue()
		if err != nil {
			return
		}

		arr, err := val.AsArray()
		if err != nil || len(arr) == 0 {
			_, _ = c.Write([]byte("-ERR protocol error\r\n"))
			continue
		}

		cmdArgs := make([]string, len(arr))
		for i, a := range arr {
			cmdArgs[i], _ = a.AsString()
		}

		m.mu.Lock()
		m.commands = append(m.commands, cmdArgs)
		m.mu.Unlock()

		cmdName := strings.ToUpper(cmdArgs[0])
		switch cmdName {
		case "PING":
			_, _ = c.Write([]byte("+PONG\r\n"))
		case "AUTH":
			_, _ = c.Write([]byte("+OK\r\n"))
		case "HELLO":
			_, _ = c.Write([]byte("%1\r\n+proto\r\n:3\r\n"))
		case "XADD":
			_, _ = c.Write([]byte("$15\r\n1700000000000-0\r\n"))
		case "SET":
			_, _ = c.Write([]byte("+OK\r\n"))
		default:
			_, _ = c.Write([]byte("+OK\r\n"))
		}
	}
}

func TestProxyServer_LifecycleAndDefaults(t *testing.T) {
	cfg := DefaultProxyConfig()
	if cfg.ListenAddr != DefaultListenAddr || cfg.TargetAddr != DefaultTargetAddr {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}

	p := NewProxyServer(ProxyConfig{
		ListenAddr: "127.0.0.1:0",
		TargetAddr: "127.0.0.1:6380",
	})

	if p.Addr() != nil {
		t.Errorf("expected nil Addr before start")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}

	if p.Addr() == nil {
		t.Fatalf("expected non-nil Addr after start")
	}

	// Double start should fail
	if err := p.Start(ctx); err == nil {
		t.Errorf("expected error on double Start()")
	}

	// Close proxy
	if err := p.Close(); err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}

	// Double close should be idempotent
	if err := p.Close(); err != nil {
		t.Errorf("unexpected error on second Close(): %v", err)
	}

	// Start after close should fail
	if err := p.Start(ctx); err == nil {
		t.Errorf("expected error starting closed proxy")
	}
}

func TestProxyServer_ListenAndServe_ContextCancellation(t *testing.T) {
	p := NewProxyServer(ProxyConfig{
		ListenAddr: "127.0.0.1:0",
		TargetAddr: "127.0.0.1:6380",
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		errCh <- p.ListenAndServe(ctx)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	err := <-errCh
	if err != nil && err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	select {
	case <-p.Done():
	case <-time.After(1 * time.Second):
		t.Errorf("timeout waiting for proxy done channel")
	}
}

func TestProxyServer_UpstreamDialFailure(t *testing.T) {
	// Target invalid port where no listener exists
	p := NewProxyServer(ProxyConfig{
		ListenAddr:  "127.0.0.1:0",
		TargetAddr:  "127.0.0.1:54321",
		DialTimeout: 100 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer p.Close()

	conn, err := net.Dial("tcp", p.Addr().String())
	if err != nil {
		t.Fatalf("failed dialing proxy: %v", err)
	}
	defer conn.Close()

	r := resp.NewReader(conn)
	val, err := r.ReadValue()
	if err != nil {
		t.Fatalf("failed reading error from proxy: %v", err)
	}
	if val.Type != resp.TypeError || !strings.Contains(val.Str, "failed connecting to upstream") {
		t.Errorf("expected upstream connection error, got %v", val)
	}
}

func TestProxyServer_ForwardingAndGuardRules(t *testing.T) {
	mockUpstream := startMockUpstream(t)
	defer mockUpstream.Close()

	p := NewProxyServer(ProxyConfig{
		ListenAddr: "127.0.0.1:0",
		TargetAddr: mockUpstream.Addr(),
		Guard: GuardConfig{
			MaxPayloadBytes: 1024,
			MaxStreamLen:    100,
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer p.Close()

	clientConn, err := net.Dial("tcp", p.Addr().String())
	if err != nil {
		t.Fatalf("failed dialing proxy: %v", err)
	}
	defer clientConn.Close()

	w := resp.NewWriter(clientConn)
	r := resp.NewReader(clientConn)

	// 1. Send PING
	if err := w.WriteCommand("PING"); err != nil {
		t.Fatalf("failed writing PING: %v", err)
	}
	val, err := r.ReadValue()
	if err != nil {
		t.Fatalf("failed reading PONG: %v", err)
	}
	if val.Type != resp.TypeSimpleString || val.Str != "PONG" {
		t.Errorf("expected PONG, got %v", val)
	}

	// 2. Send AUTH agent-1 secret
	if err := w.WriteCommand("AUTH", "agent-1", "secret"); err != nil {
		t.Fatalf("failed writing AUTH: %v", err)
	}
	val, err = r.ReadValue()
	if err != nil || val.Str != "OK" {
		t.Fatalf("expected OK from AUTH, got %v (err: %v)", val, err)
	}

	// 3. Send XADD without MAXLEN (should inject MAXLEN ~ 100)
	if err := w.WriteCommand("XADD", "agent-1:out", "*", "msg", "hello"); err != nil {
		t.Fatalf("failed writing XADD: %v", err)
	}
	val, err = r.ReadValue()
	if err != nil || val.Type != resp.TypeBulkString {
		t.Fatalf("expected bulk string message ID, got %v (err: %v)", val, err)
	}

	// 4. Send XADD with sender mismatch against session user (agent-1)
	if err := w.WriteCommand("XADD", "agent-1:out", "*", "sender", "agent-2", "msg", "impersonate"); err != nil {
		t.Fatalf("failed writing XADD: %v", err)
	}
	val, err = r.ReadValue()
	if err != nil {
		t.Fatalf("failed reading error: %v", err)
	}
	if val.Type != resp.TypeError || !strings.Contains(val.Str, "identity claim mismatch") {
		t.Errorf("expected identity claim mismatch error, got %v", val)
	}

	// 5. Send XADD with invalid citation
	if err := w.WriteCommand("XADD", "agent-1:out", "*", "citation", "invalid#cit#123", "msg", "test"); err != nil {
		t.Fatalf("failed writing XADD: %v", err)
	}
	val, err = r.ReadValue()
	if err != nil || val.Type != resp.TypeError || !strings.Contains(val.Str, "invalid citation format") {
		t.Errorf("expected invalid citation error, got %v", val)
	}

	// 6. Send payload exceeding MaxPayloadBytes (1024)
	bigPayload := strings.Repeat("A", 2048)
	if err := w.WriteCommand("SET", "key", bigPayload); err != nil {
		t.Fatalf("failed writing SET with big payload: %v", err)
	}
	val, err = r.ReadValue()
	if err != nil || val.Type != resp.TypeError || !strings.Contains(val.Str, "exceeds maximum limit") {
		t.Errorf("expected payload too large error, got %v", val)
	}

	// 7. Send XADD to peer inbox with MAXLEN (forbidden)
	if err := w.WriteCommand("XADD", "agent-2:inbox", "MAXLEN", "10", "*", "msg", "hi"); err != nil {
		t.Fatalf("failed writing XADD to peer inbox with MAXLEN: %v", err)
	}
	val, err = r.ReadValue()
	if err != nil || val.Type != resp.TypeError || !strings.Contains(val.Str, "disallowed on peer inbox") {
		t.Errorf("expected forbidden peer inbox truncation error, got %v", val)
	}

	// Verify upstream recorded commands
	cmds := mockUpstream.getCommands()
	// Should contain PING, AUTH, and the rewritten XADD (with MAXLEN ~ 100), but NOT the rejected commands
	var foundInjectedXADD bool
	for _, c := range cmds {
		if len(c) > 0 && c[0] == "XADD" {
			if len(c) >= 5 && c[1] == "agent-1:out" && c[2] == "MAXLEN" && c[3] == "~" && c[4] == "100" {
				foundInjectedXADD = true
			}
		}
	}
	if !foundInjectedXADD {
		t.Errorf("expected upstream to receive rewritten XADD with MAXLEN ~ 100, got cmds: %v", cmds)
	}
}

func TestProxyServer_HelloSessionExtraction(t *testing.T) {
	mockUpstream := startMockUpstream(t)
	defer mockUpstream.Close()

	p := NewProxyServer(ProxyConfig{
		ListenAddr: "127.0.0.1:0",
		TargetAddr: mockUpstream.Addr(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer p.Close()

	clientConn, err := net.Dial("tcp", p.Addr().String())
	if err != nil {
		t.Fatalf("failed dialing proxy: %v", err)
	}
	defer clientConn.Close()

	w := resp.NewWriter(clientConn)
	r := resp.NewReader(clientConn)

	// Send HELLO 3 AUTH bob secret SETNAME myclient
	if err := w.WriteCommand("HELLO", "3", "AUTH", "bob", "secret", "SETNAME", "myclient"); err != nil {
		t.Fatalf("failed writing HELLO: %v", err)
	}
	val, err := r.ReadValue()
	if err != nil {
		t.Fatalf("failed reading HELLO response: %v", err)
	}

	// Now try to post with sender claim alice (should fail identity check because session is bob)
	if err := w.WriteCommand("XADD", "bob:out", "*", "sender", "alice", "msg", "spoof"); err != nil {
		t.Fatalf("failed writing XADD: %v", err)
	}
	val, err = r.ReadValue()
	if err != nil || val.Type != resp.TypeError || !strings.Contains(val.Str, "identity claim mismatch") {
		t.Errorf("expected identity claim mismatch for alice claim on bob session, got %v", val)
	}

	// Now post with sender claim bob (should succeed)
	if err := w.WriteCommand("XADD", "bob:out", "*", "sender", "bob", "msg", "valid"); err != nil {
		t.Fatalf("failed writing XADD: %v", err)
	}
	val, err = r.ReadValue()
	if err != nil || val.Type != resp.TypeBulkString {
		t.Errorf("expected success for bob sender claim, got %v (err: %v)", val, err)
	}
}

func TestExtractSessionUsername(t *testing.T) {
	tests := []struct {
		name     string
		val      resp.Value
		current  string
		expected string
	}{
		{
			name:     "Non-array value",
			val:      resp.Value{Type: resp.TypeSimpleString, Str: "AUTH"},
			current:  "current",
			expected: "current",
		},
		{
			name:     "Empty array",
			val:      resp.Value{Type: resp.TypeArray, Array: []resp.Value{}},
			current:  "current",
			expected: "current",
		},
		{
			name: "AUTH password (default user)",
			val: resp.Value{
				Type: resp.TypeArray,
				Array: []resp.Value{
					{Type: resp.TypeBulkString, Str: "AUTH"},
					{Type: resp.TypeBulkString, Str: "password123"},
				},
			},
			current:  "",
			expected: "default",
		},
		{
			name: "AUTH user password",
			val: resp.Value{
				Type: resp.TypeArray,
				Array: []resp.Value{
					{Type: resp.TypeBulkString, Str: "AUTH"},
					{Type: resp.TypeBulkString, Str: "alice"},
					{Type: resp.TypeBulkString, Str: "secret"},
				},
			},
			current:  "current",
			expected: "alice",
		},
		{
			name: "HELLO 3 AUTH user password",
			val: resp.Value{
				Type: resp.TypeArray,
				Array: []resp.Value{
					{Type: resp.TypeBulkString, Str: "HELLO"},
					{Type: resp.TypeBulkString, Str: "3"},
					{Type: resp.TypeBulkString, Str: "AUTH"},
					{Type: resp.TypeBulkString, Str: "charlie"},
					{Type: resp.TypeBulkString, Str: "pass"},
				},
			},
			current:  "current",
			expected: "charlie",
		},
		{
			name: "HELLO without AUTH preserves current",
			val: resp.Value{
				Type: resp.TypeArray,
				Array: []resp.Value{
					{Type: resp.TypeBulkString, Str: "HELLO"},
					{Type: resp.TypeBulkString, Str: "3"},
				},
			},
			current:  "existing",
			expected: "existing",
		},
		{
			name: "Other command preserves current",
			val: resp.Value{
				Type: resp.TypeArray,
				Array: []resp.Value{
					{Type: resp.TypeBulkString, Str: "GET"},
					{Type: resp.TypeBulkString, Str: "key"},
				},
			},
			current:  "existing",
			expected: "existing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSessionUsername(tt.val, tt.current)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestProxyServer_ConcurrentClients(t *testing.T) {
	mockUpstream := startMockUpstream(t)
	defer mockUpstream.Close()

	p := NewProxyServer(ProxyConfig{
		ListenAddr: "127.0.0.1:0",
		TargetAddr: mockUpstream.Addr(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("failed to start proxy: %v", err)
	}
	defer p.Close()

	const numClients = 10
	var wg sync.WaitGroup
	wg.Add(numClients)

	for i := 0; i < numClients; i++ {
		go func(clientID int) {
			defer wg.Done()
			conn, err := net.Dial("tcp", p.Addr().String())
			if err != nil {
				t.Errorf("client %d dial failed: %v", clientID, err)
				return
			}
			defer conn.Close()

			w := resp.NewWriter(conn)
			r := resp.NewReader(conn)

			user := fmt.Sprintf("agent-%d", clientID)
			if err := w.WriteCommand("AUTH", user, "secret"); err != nil {
				t.Errorf("client %d auth write failed: %v", clientID, err)
				return
			}
			val, err := r.ReadValue()
			if err != nil || val.Str != "OK" {
				t.Errorf("client %d auth response error: %v", clientID, err)
				return
			}

			if err := w.WriteCommand("PING"); err != nil {
				t.Errorf("client %d ping write failed: %v", clientID, err)
				return
			}
			val, err = r.ReadValue()
			if err != nil || val.Str != "PONG" {
				t.Errorf("client %d ping response error: %v", clientID, err)
				return
			}
		}(i)
	}

	wg.Wait()
}
