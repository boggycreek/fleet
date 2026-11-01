// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
	libbpResp "github.com/boggycreek/fleet/backplane/pkg/libbp/resp"
)

type mockEnsembleServer struct {
	listener  net.Listener
	ensembles []string
	messages  []string
	mu        sync.Mutex
}

func startMockEnsembleServer(t *testing.T) (*mockEnsembleServer, int) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed starting mock server: %v", err)
	}
	s := &mockEnsembleServer{
		listener:  listener,
		ensembles: []string{"dev-team", "qa-team"},
	}

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
					case "SADD":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeInteger, Num: 1})
					case "SREM":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeInteger, Num: 1})
					case "SMEMBERS":
						var arr []libbpResp.Value
						for _, e := range s.ensembles {
							arr = append(arr, libbpResp.Value{Type: libbpResp.TypeBulkString, Str: e})
						}
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeArray, Array: arr})
					case "XADD":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeBulkString, Str: "1700000000000-0"})
					case "XREAD":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeArray, Array: []libbpResp.Value{
							{Type: libbpResp.TypeArray, Array: []libbpResp.Value{
								{Type: libbpResp.TypeBulkString, Str: "ensemble:dev-team:out"},
								{Type: libbpResp.TypeArray, Array: []libbpResp.Value{
									{Type: libbpResp.TypeArray, Array: []libbpResp.Value{
										{Type: libbpResp.TypeBulkString, Str: "1700000000000-0"},
										{Type: libbpResp.TypeArray, Array: []libbpResp.Value{
											{Type: libbpResp.TypeBulkString, Str: "sender"},
											{Type: libbpResp.TypeBulkString, Str: "agent-1"},
											{Type: libbpResp.TypeBulkString, Str: "content"},
											{Type: libbpResp.TypeBulkString, Str: "ensemble message content"},
											{Type: libbpResp.TypeBulkString, Str: "timestamp"},
											{Type: libbpResp.TypeBulkString, Str: "1700000000000"},
											{Type: libbpResp.TypeBulkString, Str: "seq"},
											{Type: libbpResp.TypeBulkString, Str: "1"},
										}},
									}},
								}},
							}},
						}})
					case "PUBLISH":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeInteger, Num: 1})
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

func (s *mockEnsembleServer) close() {
	if s.listener != nil {
		_ = s.listener.Close()
	}
}

func TestBPEnsembleMockCoverage(t *testing.T) {
	srv, port := startMockEnsembleServer(t)
	defer srv.close()

	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", fmt.Sprintf("%d", port))
	t.Setenv("BP_MODE", "agent")
	t.Setenv("BP_AGENT", "agent-1")
	t.Setenv("BP_PASSWORD", "secret")

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "schema.sql")
	_ = os.WriteFile(filePath, []byte("CREATE TABLE test;"), 0600)

	// Ensemble list
	code, out, _ := runCLI([]string{"ensemble", "list"})
	if code != 0 || !strings.Contains(out, "dev-team") {
		t.Errorf("ensemble list failed: code=%d out=%s", code, out)
	}

	// Ensemble create
	code, out, _ = runCLI([]string{"ensemble", "create", "new-team"})
	if code != 0 || !strings.Contains(out, "Created and joined ensemble new-team") {
		t.Errorf("ensemble create failed: code=%d out=%s", code, out)
	}

	// Ensemble join
	code, out, _ = runCLI([]string{"ensemble", "join", "qa-team"})
	if code != 0 || !strings.Contains(out, "Joined ensemble qa-team") {
		t.Errorf("ensemble join failed: code=%d out=%s", code, out)
	}

	// Ensemble leave
	code, out, _ = runCLI([]string{"ensemble", "leave", "qa-team"})
	if code != 0 || !strings.Contains(out, "Left ensemble qa-team") {
		t.Errorf("ensemble leave failed: code=%d out=%s", code, out)
	}

	// Ensemble say
	code, out, _ = runCLI([]string{"ensemble", "say", "dev-team", "hello team"})
	if code != 0 || !strings.Contains(out, "Sent to ensemble dev-team") {
		t.Errorf("ensemble say failed: code=%d out=%s", code, out)
	}

	// Ensemble say with file
	code, out, _ = runCLI([]string{"ensemble", "say", "dev-team", "file msg", "--file", filePath})
	if code != 0 || !strings.Contains(out, "Sent to ensemble dev-team") {
		t.Errorf("ensemble say with file failed: code=%d out=%s", code, out)
	}

	// Ensemble recv
	code, out, _ = runCLI([]string{"ensemble", "recv", "dev-team"})
	if code != 0 || !strings.Contains(out, "ensemble message content") {
		t.Errorf("ensemble recv failed: code=%d out=%s", code, out)
	}

	// Ensemble recv with last
	code, out, _ = runCLI([]string{"ensemble", "recv", "dev-team", "--last", "1700000000000-0"})
	if code != 0 || !strings.Contains(out, "ensemble message content") {
		t.Errorf("ensemble recv with last failed: code=%d out=%s", code, out)
	}

	// Validation and errors
	runCLI([]string{"ensemble"})
	runCLI([]string{"ensemble", "unknown"})
	runCLI([]string{"ensemble", "create"})
	runCLI([]string{"ensemble", "create", "Bad_Name!"})
	runCLI([]string{"ensemble", "join"})
	runCLI([]string{"ensemble", "join", "Bad_Name!"})
	runCLI([]string{"ensemble", "leave"})
	runCLI([]string{"ensemble", "leave", "Bad_Name!"})
	runCLI([]string{"ensemble", "say"})
	runCLI([]string{"ensemble", "say", "dev-team"})
	runCLI([]string{"ensemble", "say", "dev-team", "--file", filepath.Join(tmpDir, "missing.txt")})
	runCLI([]string{"ensemble", "recv"})

	// Empty ensembles list
	srv.mu.Lock()
	srv.ensembles = nil
	srv.mu.Unlock()
	code, out, _ = runCLI([]string{"ensemble", "list"})
	if code != 0 || !strings.Contains(out, "No joined ensembles.") {
		t.Errorf("ensemble empty list failed: code=%d out=%s", code, out)
	}
}

func TestAdditionalMainCoverage(t *testing.T) {
	srv, port := startMockValkeyForBp(t)
	defer srv.close()

	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", fmt.Sprintf("%d", port))
	t.Setenv("BP_MODE", "agent")
	t.Setenv("BP_AGENT", "agent-1")
	t.Setenv("BP_PASSWORD", "secret")

	// 1. handlePeers json & flag error
	runCLI([]string{"peers", "--json"})
	runCLI([]string{"peers", "--invalid-flag"})

	// 2. handleRecv invalid flag
	runCLI([]string{"recv", "--invalid-flag"})

	// 3. handleHuman invalid flag
	runCLI([]string{"human", "--invalid-flag"})

	// 4. handleProfile invalid subcommand
	runCLI([]string{"profile", "unknown"})

	// 5. handleSpecialty invalid subcommand
	runCLI([]string{"specialty", "unknown"})
	runCLI([]string{"specialty", "list", "--invalid-flag"})

	// 6. Direct handleRecv call
	cfg := libbp.ClientConfig{
		Host:     "127.0.0.1",
		Port:     port,
		Mode:     "agent",
		AgentID:  "agent-1",
		Password: "secret",
	}
	var stdout, stderr strings.Builder
	handleRecv(context.Background(), cfg, []string{"--block=0"}, &stdout, &stderr)
}
