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

	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
	"github.com/boggycreek/sandbox/backplane/pkg/libbp/resp"
)

type mockSpecialtyServer struct {
	listener net.Listener
	mu       sync.Mutex
	sets     map[string]map[string]bool
	failAll  bool
	t        *testing.T
}

func startMockSpecialtyServer(t *testing.T) (*mockSpecialtyServer, int) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed starting mock specialty server: %v", err)
	}

	srv := &mockSpecialtyServer{
		listener: listener,
		sets:     make(map[string]map[string]bool),
		t:        t,
	}

	go srv.serve()
	port := listener.Addr().(*net.TCPAddr).Port
	return srv, port
}

func (s *mockSpecialtyServer) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		_ = s.listener.Close()
	}
}

func (s *mockSpecialtyServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *mockSpecialtyServer) handleConn(conn net.Conn) {
	defer conn.Close()
	r := resp.NewReader(conn)
	w := resp.NewWriter(conn)

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

		if s.failAll && cmd != "AUTH" && cmd != "PING" {
			_ = w.WriteValue(resp.Value{Type: resp.TypeError, Str: "ERR internal server failure"})
			s.mu.Unlock()
			continue
		}

		switch cmd {
		case "AUTH":
			_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "OK"})

		case "PING":
			_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "PONG"})

		case "SADD":
			key := args[1].String()
			if s.sets[key] == nil {
				s.sets[key] = make(map[string]bool)
			}
			added := 0
			for i := 2; i < len(args); i++ {
				v := args[i].String()
				if !s.sets[key][v] {
					s.sets[key][v] = true
					added++
				}
			}
			_ = w.WriteValue(resp.Value{Type: resp.TypeInteger, Num: int64(added)})

		case "SREM":
			key := args[1].String()
			removed := 0
			if m, ok := s.sets[key]; ok {
				for i := 2; i < len(args); i++ {
					v := args[i].String()
					if m[v] {
						delete(m, v)
						removed++
					}
				}
			}
			_ = w.WriteValue(resp.Value{Type: resp.TypeInteger, Num: int64(removed)})

		case "SMEMBERS":
			key := args[1].String()
			m := s.sets[key]
			var arr []resp.Value
			for member := range m {
				arr = append(arr, resp.Value{Type: resp.TypeBulkString, Str: member})
			}
			_ = w.WriteValue(resp.Value{Type: resp.TypeArray, Array: arr})

		case "SCAN":
			var keysArr []resp.Value
			for k := range s.sets {
				if strings.HasPrefix(k, "specialty:") {
					keysArr = append(keysArr, resp.Value{Type: resp.TypeBulkString, Str: k})
				}
			}
			_ = w.WriteValue(resp.Value{
				Type: resp.TypeArray,
				Array: []resp.Value{
					{Type: resp.TypeBulkString, Str: "0"},
					{Type: resp.TypeArray, Array: keysArr},
				},
			})

		default:
			_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "OK"})
		}
		s.mu.Unlock()
	}
}

func TestBPSpecialtySubcommands(t *testing.T) {
	srv, port := startMockSpecialtyServer(t)
	defer srv.close()

	cfg := libbp.ClientConfig{
		Host:     "127.0.0.1",
		Port:     port,
		Username: "operator",
		AgentID:  "operator",
	}

	ctx := context.Background()

	// 1. Initial list - empty
	var stdout, stderr bytes.Buffer
	code := handleSpecialty(ctx, cfg, []string{"list"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0 for initial list, got %d (err: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No domain specialties configured.") {
		t.Errorf("expected no domain specialties message, got: %s", stdout.String())
	}

	// Initial list --json
	stdout.Reset()
	stderr.Reset()
	code = handleSpecialty(ctx, cfg, []string{"list", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	var emptyMap map[string][]string
	if err := json.Unmarshal(stdout.Bytes(), &emptyMap); err != nil {
		t.Fatalf("invalid json output: %v", err)
	}

	// 2. Add specialties
	stdout.Reset()
	stderr.Reset()
	code = handleSpecialty(ctx, cfg, []string{"add", "databases", "db-agent-1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0 for specialty add, got %d (err: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Assigned agent db-agent-1 to specialty databases") {
		t.Errorf("unexpected stdout: %s", stdout.String())
	}

	// Add second agent
	stdout.Reset()
	stderr.Reset()
	_ = handleSpecialty(ctx, cfg, []string{"add", "databases", "db-agent-2"}, &stdout, &stderr)

	// Add another specialty
	stdout.Reset()
	stderr.Reset()
	_ = handleSpecialty(ctx, cfg, []string{"add", "security", "sec-agent-1"}, &stdout, &stderr)

	// 3. List all specialties plain text
	stdout.Reset()
	stderr.Reset()
	code = handleSpecialty(ctx, cfg, []string{"list"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	outStr := stdout.String()
	if !strings.Contains(outStr, "SPECIALTY") || !strings.Contains(outStr, "databases") || !strings.Contains(outStr, "security") {
		t.Errorf("unexpected specialty list output:\n%s", outStr)
	}

	// List all specialties --json
	stdout.Reset()
	stderr.Reset()
	code = handleSpecialty(ctx, cfg, []string{"list", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	var specMap map[string][]string
	if err := json.Unmarshal(stdout.Bytes(), &specMap); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(specMap) != 2 || len(specMap["databases"]) != 2 {
		t.Errorf("unexpected specMap: %+v", specMap)
	}

	// 4. List specific specialty
	stdout.Reset()
	stderr.Reset()
	code = handleSpecialty(ctx, cfg, []string{"list", "databases"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "Specialty \"databases\" assigned agents:") {
		t.Errorf("unexpected output: %s", stdout.String())
	}

	// List specific specialty --json
	stdout.Reset()
	stderr.Reset()
	code = handleSpecialty(ctx, cfg, []string{"list", "databases", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	var agents []string
	if err := json.Unmarshal(stdout.Bytes(), &agents); err != nil {
		t.Fatalf("invalid json array: %v", err)
	}
	if len(agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(agents))
	}

	// List nonexistent specialty
	stdout.Reset()
	stderr.Reset()
	code = handleSpecialty(ctx, cfg, []string{"list", "nonexistent"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "Specialty \"nonexistent\" has no assigned agents.") {
		t.Errorf("unexpected output: %s", stdout.String())
	}

	// 5. Remove specialty
	stdout.Reset()
	stderr.Reset()
	code = handleSpecialty(ctx, cfg, []string{"remove", "databases", "db-agent-1"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0 for remove, got %d (err: %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Removed agent db-agent-1 from specialty databases") {
		t.Errorf("unexpected stdout: %s", stdout.String())
	}

	// 6. Error handling & edge cases
	// No subcommands
	stdout.Reset()
	stderr.Reset()
	if code := handleSpecialty(ctx, cfg, []string{}, &stdout, &stderr); code != 1 {
		t.Errorf("expected code 1 for no args, got %d", code)
	}

	// Unknown subcommand
	stdout.Reset()
	stderr.Reset()
	if code := handleSpecialty(ctx, cfg, []string{"unknown"}, &stdout, &stderr); code != 1 {
		t.Errorf("expected code 1 for unknown subcommand, got %d", code)
	}

	// Missing args for add
	stdout.Reset()
	stderr.Reset()
	if code := handleSpecialty(ctx, cfg, []string{"add", "databases"}, &stdout, &stderr); code != 1 {
		t.Errorf("expected code 1 for missing add args, got %d", code)
	}

	// Missing args for remove
	stdout.Reset()
	stderr.Reset()
	if code := handleSpecialty(ctx, cfg, []string{"remove", "databases"}, &stdout, &stderr); code != 1 {
		t.Errorf("expected code 1 for missing remove args, got %d", code)
	}

	// Invalid specialty domain name in add
	stdout.Reset()
	stderr.Reset()
	if code := handleSpecialty(ctx, cfg, []string{"add", "invalid domain!", "agent-1"}, &stdout, &stderr); code != 1 {
		t.Errorf("expected code 1 for invalid domain, got %d", code)
	}

	// Flag error in list
	stdout.Reset()
	stderr.Reset()
	if code := handleSpecialty(ctx, cfg, []string{"list", "-unknownflag"}, &stdout, &stderr); code != 1 {
		t.Errorf("expected code 1 for unknown flag, got %d", code)
	}

	// Connection failure to closed port
	stdout.Reset()
	stderr.Reset()
	badCfg := libbp.ClientConfig{Host: "127.0.0.1", Port: 64998}
	if code := handleSpecialty(ctx, badCfg, []string{"list"}, &stdout, &stderr); code != 1 {
		t.Errorf("expected code 1 for connection failure, got %d", code)
	}

	// Server error paths
	srvErr, errPort := startMockSpecialtyServer(t)
	srvErr.failAll = true
	defer srvErr.close()
	errCfg := libbp.ClientConfig{
		Host:     "127.0.0.1",
		Port:     errPort,
		Username: "operator",
	}

	stdout.Reset()
	stderr.Reset()
	if code := handleSpecialty(ctx, errCfg, []string{"list"}, &stdout, &stderr); code != 1 {
		t.Errorf("expected code 1 for server error in list all, got %d", code)
	}

	stdout.Reset()
	stderr.Reset()
	if code := handleSpecialty(ctx, errCfg, []string{"list", "databases"}, &stdout, &stderr); code != 1 {
		t.Errorf("expected code 1 for server error in list single, got %d", code)
	}

	stdout.Reset()
	stderr.Reset()
	if code := handleSpecialty(ctx, errCfg, []string{"add", "databases", "agent-1"}, &stdout, &stderr); code != 1 {
		t.Errorf("expected code 1 for server error in add, got %d", code)
	}

	stdout.Reset()
	stderr.Reset()
	if code := handleSpecialty(ctx, errCfg, []string{"remove", "databases", "agent-1"}, &stdout, &stderr); code != 1 {
		t.Errorf("expected code 1 for server error in remove, got %d", code)
	}
}
