// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package libbp

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp/resp"
)

func TestSpecialtyValidationAndHelpers(t *testing.T) {
	// Test SpecialtyKey
	if key := SpecialtyKey("Databases"); key != "specialty:databases" {
		t.Errorf("expected specialty:databases, got %s", key)
	}

	// Valid names
	validNames := []string{"databases", "frontend-ui", "backend_api", "DB123", "ml-ops_1"}
	for _, name := range validNames {
		if err := ValidateSpecialtyName(name); err != nil {
			t.Errorf("expected valid name %q, got error: %v", name, err)
		}
	}

	// Invalid names
	invalidNames := []string{"", "   ", "db@local", "front end", "ui/ux", "specialty!"}
	for _, name := range invalidNames {
		if err := ValidateSpecialtyName(name); err == nil {
			t.Errorf("expected error for invalid name %q, got nil", name)
		}
	}
}

func TestSpecialtyClientOperations(t *testing.T) {
	srv, port := startMockValkeyServer(t)
	defer srv.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, ClientConfig{
		Host:     "127.0.0.1",
		Port:     port,
		Username: "operator",
		AgentID:  "operator",
	})
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer client.Close()

	// 1. Initial state: no specialties
	allSpecs, err := client.GetAllSpecialties(ctx)
	if err != nil {
		t.Fatalf("GetAllSpecialties error: %v", err)
	}
	if len(allSpecs) != 0 {
		t.Errorf("expected empty specialties map, got %v", allSpecs)
	}

	// 2. SetSpecialty validation errors
	if err := client.SetSpecialty(ctx, "invalid name!", "agent-1"); err == nil {
		t.Errorf("expected error setting invalid specialty name, got nil")
	}
	if err := client.SetSpecialty(ctx, "databases", "  "); err == nil {
		t.Errorf("expected error setting empty agent ID, got nil")
	}

	// 3. SetSpecialty success
	if err := client.SetSpecialty(ctx, "databases", "db-agent-1"); err != nil {
		t.Fatalf("SetSpecialty failed: %v", err)
	}
	if err := client.SetSpecialty(ctx, "databases", "db-agent-2"); err != nil {
		t.Fatalf("SetSpecialty failed: %v", err)
	}
	if err := client.SetSpecialty(ctx, "security", "sec-agent-1"); err != nil {
		t.Fatalf("SetSpecialty failed: %v", err)
	}

	// 4. GetSpecialtyAgents
	dbAgents, err := client.GetSpecialtyAgents(ctx, "databases")
	if err != nil {
		t.Fatalf("GetSpecialtyAgents error: %v", err)
	}
	if len(dbAgents) != 2 {
		t.Fatalf("expected 2 agents for databases, got %d", len(dbAgents))
	}

	// 5. GetAllSpecialties
	allSpecs, err = client.GetAllSpecialties(ctx)
	if err != nil {
		t.Fatalf("GetAllSpecialties error: %v", err)
	}
	if len(allSpecs) != 2 {
		t.Fatalf("expected 2 specialties, got %d", len(allSpecs))
	}
	if len(allSpecs["databases"]) != 2 || len(allSpecs["security"]) != 1 {
		t.Errorf("unexpected specialties map content: %+v", allSpecs)
	}

	// 6. RemoveSpecialty validation errors
	if err := client.RemoveSpecialty(ctx, "invalid name!", "agent-1"); err == nil {
		t.Errorf("expected error removing invalid specialty name, got nil")
	}
	if err := client.RemoveSpecialty(ctx, "databases", "  "); err == nil {
		t.Errorf("expected error removing empty agent ID, got nil")
	}

	// 7. RemoveSpecialty success
	if err := client.RemoveSpecialty(ctx, "databases", "db-agent-1"); err != nil {
		t.Fatalf("RemoveSpecialty failed: %v", err)
	}
	dbAgentsAfter, err := client.GetSpecialtyAgents(ctx, "databases")
	if err != nil {
		t.Fatalf("GetSpecialtyAgents error: %v", err)
	}
	if len(dbAgentsAfter) != 1 || dbAgentsAfter[0] != "db-agent-2" {
		t.Errorf("expected [db-agent-2], got %v", dbAgentsAfter)
	}

	// 8. Closed client errors
	client.Close()
	if err := client.SetSpecialty(ctx, "databases", "agent-1"); err == nil {
		t.Errorf("expected error on SetSpecialty after Close")
	}
	if err := client.RemoveSpecialty(ctx, "databases", "agent-1"); err == nil {
		t.Errorf("expected error on RemoveSpecialty after Close")
	}
	if _, err := client.GetSpecialtyAgents(ctx, "databases"); err == nil {
		t.Errorf("expected error on GetSpecialtyAgents after Close")
	}
	if _, err := client.GetAllSpecialties(ctx); err == nil {
		t.Errorf("expected error on GetAllSpecialties after Close")
	}
}

func TestSpecialtyACLEnforcement(t *testing.T) {
	// Create mock server that checks ACL permissions per username
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed listening: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	sets := make(map[string]map[string]bool)

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
				isAgent := false

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
					switch cmd {
					case "AUTH":
						if len(args) > 1 && strings.HasPrefix(args[1].String(), "agent") {
							isAgent = true
						}
						_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "OK"})
					case "PING":
						_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "PONG"})
					case "SADD", "SREM":
						if isAgent {
							_ = w.WriteValue(resp.Value{
								Type: resp.TypeError,
								Str:  "NOPERM this user has no permissions to run the 'sadd' command or its arguments",
							})
						} else {
							key := args[1].String()
							if sets[key] == nil {
								sets[key] = make(map[string]bool)
							}
							if cmd == "SADD" && len(args) > 2 {
								sets[key][args[2].String()] = true
							}
							_ = w.WriteValue(resp.Value{Type: resp.TypeInteger, Num: 1})
						}
					case "SMEMBERS":
						key := args[1].String()
						var arr []resp.Value
						for member := range sets[key] {
							arr = append(arr, resp.Value{Type: resp.TypeBulkString, Str: member})
						}
						_ = w.WriteValue(resp.Value{Type: resp.TypeArray, Array: arr})
					default:
						_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "OK"})
					}
				}
			}(conn)
		}
	}()
	defer listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Operator client
	opClient, err := Dial(ctx, ClientConfig{
		Host:     "127.0.0.1",
		Port:     port,
		Username: "operator",
		Password: "pass",
		AgentID:  "operator",
	})
	if err != nil {
		t.Fatalf("operator Dial failed: %v", err)
	}
	defer opClient.Close()

	// Operator can set specialty
	if err := opClient.SetSpecialty(ctx, "databases", "db-agent-1"); err != nil {
		t.Fatalf("operator SetSpecialty failed: %v", err)
	}

	// Agent client
	agentClient, err := Dial(ctx, ClientConfig{
		Host:     "127.0.0.1",
		Port:     port,
		Username: "agent-1",
		Password: "pass",
		AgentID:  "agent-1",
	})
	if err != nil {
		t.Fatalf("agent Dial failed: %v", err)
	}
	defer agentClient.Close()

	// Agent can read specialty
	agents, err := agentClient.GetSpecialtyAgents(ctx, "databases")
	if err != nil || len(agents) != 1 || agents[0] != "db-agent-1" {
		t.Fatalf("agent GetSpecialtyAgents failed: %v (agents: %v)", err, agents)
	}

	// Agent write attempt MUST be rejected with NOPERM
	err = agentClient.SetSpecialty(ctx, "databases", "rogue-agent")
	if err == nil {
		t.Fatalf("expected NOPERM error on agent SetSpecialty, got nil")
	}
	if !strings.Contains(err.Error(), "NOPERM") {
		t.Errorf("expected NOPERM error, got %v", err)
	}

	err = agentClient.RemoveSpecialty(ctx, "databases", "db-agent-1")
	if err == nil {
		t.Fatalf("expected NOPERM error on agent RemoveSpecialty, got nil")
	}
	if !strings.Contains(err.Error(), "NOPERM") {
		t.Errorf("expected NOPERM error, got %v", err)
	}
}
