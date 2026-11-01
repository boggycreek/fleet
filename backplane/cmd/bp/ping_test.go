// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	libbpResp "github.com/boggycreek/fleet/backplane/pkg/libbp/resp"
)

type pingTrackingMockServer struct {
	listener  net.Listener
	mu        sync.Mutex
	published []string // channels published to
	failPub   bool
}

func startPingTrackingMockServer(t *testing.T) (*pingTrackingMockServer, int) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed starting mock server: %v", err)
	}
	s := &pingTrackingMockServer{listener: listener}
	data := make(map[string]string)
	data["poll-interval"] = "60"

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := libbpResp.NewReader(c)
				w := libbpResp.NewWriter(c)
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
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeSimpleString, Str: "OK"})
					case "INCR":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeInteger, Num: 1})
					case "SET":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeSimpleString, Str: "OK"})
					case "XADD":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeBulkString, Str: "1700000000000-0"})
					case "PUBLISH":
						if s.failPub {
							_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeError, Str: "ERR publish simulated failure"})
						} else {
							if len(args) > 1 {
								s.published = append(s.published, args[1].String())
							}
							_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeInteger, Num: 1})
						}
					default:
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeSimpleString, Str: "OK"})
					}
					s.mu.Unlock()
				}
			}(conn)
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	return s, port
}

func (s *pingTrackingMockServer) close() {
	if s.listener != nil {
		_ = s.listener.Close()
	}
}

func (s *pingTrackingMockServer) getPublished() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := make([]string, len(s.published))
	copy(res, s.published)
	return res
}

func (s *pingTrackingMockServer) clearPublished() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.published = nil
}

func TestPubSubPingOnCommands(t *testing.T) {
	srv, port := startPingTrackingMockServer(t)
	defer srv.close()

	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", fmt.Sprintf("%d", port))
	t.Setenv("BP_MODE", "agent")
	t.Setenv("BP_AGENT", "agent-sender")
	t.Setenv("BP_PASSWORD", "secret")

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "post.txt")
	_ = os.WriteFile(filePath, []byte("Post Title\nBody content\n"), 0600)

	// 1. bp say -> should publish to ping:broadcast
	srv.clearPublished()
	code, out, errOut := runCLI([]string{"say", "hello world broadcast"})
	if code != 0 {
		t.Fatalf("say failed: code=%d out=%s err=%s", code, out, errOut)
	}
	pubs := srv.getPublished()
	if len(pubs) != 1 || pubs[0] != "ping:broadcast" {
		t.Fatalf("expected 1 ping to ping:broadcast, got: %v", pubs)
	}

	// 2. bp tell -> should publish to ping:agent-recipient
	srv.clearPublished()
	code, out, errOut = runCLI([]string{"tell", "agent-recipient", "hello direct"})
	if code != 0 {
		t.Fatalf("tell failed: code=%d out=%s err=%s", code, out, errOut)
	}
	pubs = srv.getPublished()
	if len(pubs) != 1 || pubs[0] != "ping:agent-recipient" {
		t.Fatalf("expected 1 ping to ping:agent-recipient, got: %v", pubs)
	}

	// 3. bp reply -> should publish to ping:agent-replied
	srv.clearPublished()
	code, out, errOut = runCLI([]string{"reply", "100-0", "agent-replied", "got your message"})
	if code != 0 {
		t.Fatalf("reply failed: code=%d out=%s err=%s", code, out, errOut)
	}
	pubs = srv.getPublished()
	if len(pubs) != 1 || pubs[0] != "ping:agent-replied" {
		t.Fatalf("expected 1 ping to ping:agent-replied, got: %v", pubs)
	}

	// 4. bp post (broadcast) -> should publish to ping:broadcast
	srv.clearPublished()
	code, out, errOut = runCLI([]string{"post", filePath})
	if code != 0 {
		t.Fatalf("post broadcast failed: code=%d out=%s err=%s", code, out, errOut)
	}
	pubs = srv.getPublished()
	if len(pubs) != 1 || pubs[0] != "ping:broadcast" {
		t.Fatalf("expected 1 ping to ping:broadcast, got: %v", pubs)
	}

	// 5. bp post (direct) -> should publish to ping:agent-recipient
	srv.clearPublished()
	code, out, errOut = runCLI([]string{"post", filePath, "to", "agent-recipient"})
	if code != 0 {
		t.Fatalf("post direct failed: code=%d out=%s err=%s", code, out, errOut)
	}
	pubs = srv.getPublished()
	if len(pubs) != 1 || pubs[0] != "ping:agent-recipient" {
		t.Fatalf("expected 1 ping to ping:agent-recipient, got: %v", pubs)
	}

	// 6. Test that errors during PublishPing are non-fatal
	srv.mu.Lock()
	srv.failPub = true
	srv.mu.Unlock()

	code, _, errOut = runCLI([]string{"say", "say during publish failure"})
	if code != 0 {
		t.Errorf("say should succeed even if publish ping fails, got code=%d err=%s", code, errOut)
	}

	code, _, errOut = runCLI([]string{"tell", "agent-recipient", "tell during publish failure"})
	if code != 0 {
		t.Errorf("tell should succeed even if publish ping fails, got code=%d err=%s", code, errOut)
	}

	code, _, errOut = runCLI([]string{"reply", "100-0", "agent-recipient", "reply during publish failure"})
	if code != 0 {
		t.Errorf("reply should succeed even if publish ping fails, got code=%d err=%s", code, errOut)
	}

	code, _, errOut = runCLI([]string{"post", filePath})
	if code != 0 {
		t.Errorf("post should succeed even if publish ping fails, got code=%d err=%s", code, errOut)
	}

	code, _, errOut = runCLI([]string{"post", filePath, "to", "agent-recipient"})
	if code != 0 {
		t.Errorf("post direct should succeed even if publish ping fails, got code=%d err=%s", code, errOut)
	}
}
