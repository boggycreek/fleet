// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/sonar"
)

func TestMCPServerLifecycle(t *testing.T) {
	// Mock SonarQube backend
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/system/status":
			_, _ = w.Write([]byte(`{"id":"test-sonar","version":"26.9.0","status":"UP"}`))
		case r.URL.Path == "/api/qualitygates/project_status":
			_, _ = w.Write([]byte(`{"projectStatus":{"status":"OK","conditions":[]}}`))
		case r.URL.Path == "/api/issues/search":
			_, _ = w.Write([]byte(`{"total":0,"issues":[]}`))
		case r.URL.Path == "/api/measures/component":
			_, _ = w.Write([]byte(`{"component":{"key":"test-proj","name":"Test","measures":[{"metric":"coverage","value":"95.0"}]}}`))
		case r.URL.Path == "/api/projects/create":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := sonar.NewClient(sonar.ClientConfig{BaseURL: ts.URL})

	// Feed JSON-RPC requests via in-memory buffer
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"sonar_status"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"sonar_quality_gate","arguments":{"project_key":"test-proj"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"sonar_issues","arguments":{"project_key":"test-proj","severity":"MAJOR"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"sonar_measures","arguments":{"project_key":"test-proj","metric_keys":"coverage"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"sonar_create_project","arguments":{"project_key":"test-proj","name":"Test Project"}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"non_existent_method"}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"unknown_tool"}}`,
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"sonar_quality_gate","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"sonar_issues","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"sonar_measures","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"sonar_create_project","arguments":{}}}`,
		`{invalid json`,
		``,
	}

	input := strings.Join(requests, "\n") + "\n"
	inBuf := bytes.NewBufferString(input)
	outBuf := &bytes.Buffer{}

	server := NewMCPServer(client, inBuf, outBuf)
	err := server.Serve(context.Background())
	if err != nil {
		t.Fatalf("unexpected Serve error: %v", err)
	}

	output := outBuf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 8 {
		t.Fatalf("expected at least 8 responses, got %d: %s", len(lines), output)
	}

	// Verify initialize response
	var initResp JSONRPCMessage
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("failed unmarshaling init response: %v", err)
	}
	if initResp.ID != float64(1) {
		t.Errorf("expected ID 1, got %v", initResp.ID)
	}
}

func TestMCPServerBackendErrors(t *testing.T) {
	badClient := sonar.NewClient(sonar.ClientConfig{BaseURL: "http://127.0.0.1:1"})

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"sonar_status"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sonar_quality_gate","arguments":{"project_key":"proj"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"sonar_issues","arguments":{"project_key":"proj"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"sonar_measures","arguments":{"project_key":"proj"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"sonar_create_project","arguments":{"project_key":"proj","name":"P"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":"invalid-call-params"}`,
	}

	input := strings.Join(requests, "\n") + "\n"
	inBuf := bytes.NewBufferString(input)
	outBuf := &bytes.Buffer{}

	server := NewMCPServer(badClient, inBuf, outBuf)
	_ = server.Serve(context.Background())

	output := outBuf.String()
	if !strings.Contains(output, "Error") {
		t.Errorf("expected errors in output, got: %s", output)
	}
}

func TestMCPServerContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := sonar.NewClient(sonar.ClientConfig{})
	server := NewMCPServer(client, bytes.NewBuffer(nil), &bytes.Buffer{})
	err := server.Serve(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestRunFunction(t *testing.T) {
	inBuf := bytes.NewBufferString("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n")
	outBuf := &bytes.Buffer{}

	err := Run(context.Background(), inBuf, outBuf)
	if err != nil {
		t.Fatalf("unexpected error running Run: %v", err)
	}
	if !strings.Contains(outBuf.String(), "sonar-mcp") {
		t.Errorf("expected sonar-mcp in output, got: %s", outBuf.String())
	}
}
