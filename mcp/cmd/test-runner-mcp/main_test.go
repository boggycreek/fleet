// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/sonar"
	"github.com/boggycreek/fleet/mcp/pkg/mcp"
)

// newTestRunnerServer builds a test-runner MCP server backed by a mocked
// SonarQube measures endpoint.
func newTestRunnerServer(t *testing.T, handler http.HandlerFunc) *MCPServer {
	t.Helper()

	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	client := sonar.NewClient(sonar.ClientConfig{BaseURL: ts.URL})
	return NewMCPServer(client, bytes.NewReader(nil), &bytes.Buffer{})
}

// mockMeasuresHandler serves the SonarQube measures endpoint. It validates
// that the request targets the correct path, carries the expected component
// key, and requests the coverage metric, returning the supplied body.
func mockMeasuresHandler(t *testing.T, body string) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path != "/api/measures/component" {
			t.Errorf("unexpected path %q, want /api/measures/component", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("component") == "" {
			t.Errorf("expected component query param, got request: %s", r.URL.RawQuery)
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("metricKeys") != "coverage" {
			t.Errorf("expected metricKeys=coverage, got request: %s", r.URL.RawQuery)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}
}

// textFromResult extracts the text content of a successful tool result.
func textFromResult(result mcp.CallToolResult) string {
	content := result.Content
	if len(content) == 0 {
		return ""
	}
	return content[0].Text
}

// TestTestRunSuiteArgumentParsing verifies that a required project_key
// argument is validated correctly when missing.
func TestTestRunSuiteArgumentParsing(t *testing.T) {
	server := newTestRunnerServer(t, func(w http.ResponseWriter, r *http.Request) {
		// The mock will never be reached without a valid project_key.
		t.Error("unexpected request without required project_key")
		http.NotFound(w, r)
	})

	params := mcp.CallToolParams{
		Name:      "test_get_coverage_gaps",
		Arguments: json.RawMessage("{}"),
	}

	result := server.executeTool(context.Background(), params)
	if !strings.Contains(textFromResult(result), "Missing required argument") {
		t.Errorf("expected missing-argument error in response, got: %s", textFromResult(result))
	}
}

// TestTestRunSuiteDefaultComponentKey verifies that when no component_key is
// supplied, the server falls back to the project_key as the component key
// passed to the SonarQube measures client.
func TestTestRunSuiteDefaultComponentKey(t *testing.T) {
	body := `{"component":{"key":"my-project","name":"My Project","measures":[{"metric": "coverage","value":"75.0"}]}}`

	server := newTestRunnerServer(t, mockMeasuresHandler(t, body))

	params := mcp.CallToolParams{
		Name:      "test_get_coverage_gaps",
		Arguments: json.RawMessage(`{"project_key":"my-project"}`),
	}

	result := server.executeTool(context.Background(), params)

	var report map[string]any
	if err := json.Unmarshal([]byte(textFromResult(result)), &report); err != nil {
		t.Fatalf("failed to unmarshal report: %v, got: %s", err, textFromResult(result))
	}

	if report["project_key"] != "my-project" {
		t.Errorf("expected project_key my-project, got %v", report["project_key"])
	}
	if report["component_key"] != "my-project" {
		t.Errorf("expected component_key my-project (fallback), got %v", report["component_key"])
	}
	measuresVal, _ := report["measures"].([]any)
	if len(measuresVal) != 1 {
		t.Errorf("expected 1 measure, got %d", len(measuresVal))
	}
	if int(report["coverage_gaps"].(float64)) != 1 {
		t.Errorf("expected coverage_gaps 1, got %v", report["coverage_gaps"])
	}
}

// TestTestRunSuiteSpecificComponentKey verifies that an explicit
// component_key argument overrides the project-level fallback and is used
// directly as the component key for the measures query.
func TestTestRunSuiteSpecificComponentKey(t *testing.T) {
	body := `{"component":{"key":"detail-12","name":"Detail","measures":[{"metric": "coverage","value":"92.5"}]}}`

	server := newTestRunnerServer(t, mockMeasuresHandler(t, body))

	params := mcp.CallToolParams{
		Name:      "test_get_coverage_gaps",
		Arguments: json.RawMessage(`{"project_key":"proj-01","component_key":"detail-12"}`),
	}

	result := server.executeTool(context.Background(), params)

	var report map[string]any
	if err := json.Unmarshal([]byte(textFromResult(result)), &report); err != nil {
		t.Fatalf("failed to unmarshal report: %v, got: %s", err, textFromResult(result))
	}

	if report["component_key"] != "detail-12" {
		t.Errorf("expected component_key detail-12, got %v", report["component_key"])
	}
	if report["project_key"] != "proj-01" {
		t.Errorf("expected project_key proj-01, got %v", report["project_key"])
	}
}

// TestTestRunSuiteCoverageValues verifies that the measured coverage values
// are marshaled into the structured JSON report correctly.
func TestTestRunSuiteCoverageValues(t *testing.T) {
	body := `{"component":{"key":"proj-01","name":"Proj","measures":[
		{"metric": "coverage","value":"80.0"},
		{"metric":"cognitive_complexity","value":"45.0"}
	]}}`

	server := newTestRunnerServer(t, mockMeasuresHandler(t, body))

	params := mcp.CallToolParams{
		Name:      "test_get_coverage_gaps",
		Arguments: json.RawMessage(`{"project_key":"proj-01"}`),
	}

	result := server.executeTool(context.Background(), params)

	if !strings.Contains(textFromResult(result), `"coverage_gaps": 2`) {
		t.Errorf("expected coverage_gaps 2 in response, got: %s", textFromResult(result))
	}
	if !strings.Contains(textFromResult(result), `"metric": "coverage"`) {
		t.Errorf("expected coverage measure present, got: %s", textFromResult(result))
	}

	var report map[string]any
	if err := json.Unmarshal([]byte(textFromResult(result)), &report); err != nil {
		t.Fatalf("failed to unmarshal report from coverage response: %v, got: %s", err, textFromResult(result))
	}
	measuresVal, ok := report["measures"].([]any)
	if !ok || len(measuresVal) != 2 {
		t.Fatalf("expected 2 measures in report, got %v", report["measures"])
	}
}

// TestTestRunSuiteBackendError verifies that a non-200 response from the
// SonarQube measures endpoint produces an error result instead of a panic.
func TestTestRunSuiteBackendError(t *testing.T) {
	server := newTestRunnerServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	params := mcp.CallToolParams{
		Name:      "test_get_coverage_gaps",
		Arguments: json.RawMessage(`{"project_key":"proj-01"}`),
	}

	result := server.executeTool(context.Background(), params)

	if !strings.Contains(textFromResult(result), "Error querying coverage measures") {
		t.Errorf("expected backend error message in response, got: %s", textFromResult(result))
	}
	if !result.IsError {
		t.Errorf("expected error result to be flagged as IsError")
	}
}

// TestTestRunSuiteContextCancel verifies that the server returns the context
// error when the request context is cancelled.
func TestTestRunSuiteContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := sonar.NewClient(sonar.ClientConfig{BaseURL: "http://127.0.0.1:1"})
	server := NewMCPServer(client, bytes.NewReader(nil), &bytes.Buffer{})
	err := server.Serve(ctx)
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("expected context canceled error, got: %v", err)
	}
}

// TestRunFunction verifies the Run entry point executes the server without
// error and produces coverage-related output for the test tool call.
func TestRunFunction(t *testing.T) {
	body := `{"component":{"key":"proj-01","name":"Proj","measures":[{"metric": "coverage","value":"88.0"}]}}`

	// Build requests that exercise the initialize handshake and the
	// coverage-gaps tool call against the mocked measures endpoint.
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"test_get_coverage_gaps","arguments":{"project_key":"proj-01"}}}`,
	}
	input := strings.Join(requests, "\n") + "\n"
	inBuf := bytes.NewBufferString(input)
	outBuf := &bytes.Buffer{}

	ts := httptest.NewServer(mockMeasuresHandler(t, body))
	defer ts.Close()

	err := Run(context.Background(), inBuf, outBuf)
	if err != nil {
		t.Fatalf("unexpected Run error: %v", err)
	}

	output := outBuf.String()
	if !strings.Contains(output, "test-runner-mcp") {
		t.Errorf("expected test-runner-mcp in output, got: %s", output)
	}
	if !strings.Contains(output, "coverage") {
		t.Errorf("expected coverage content in output, got: %s", output)
	}
}

func TestTestRunnerMCPSecurityValidation(t *testing.T) {
	client := sonar.NewClient(sonar.ClientConfig{BaseURL: "http://127.0.0.1:9000"})
	server := NewMCPServer(client, bytes.NewBuffer(nil), &bytes.Buffer{})

	// 1. Invalid project_key
	resKey := server.executeTool(context.Background(), mcp.CallToolParams{
		Name:      "test_get_coverage_gaps",
		Arguments: json.RawMessage(`{"project_key":"bad proj key"}`),
	})
	if !resKey.IsError || !strings.Contains(resKey.Content[0].Text, "forbidden characters") {
		t.Errorf("expected project_key validation error, got: %+v", resKey)
	}

	// 2. Invalid component_key
	resComp := server.executeTool(context.Background(), mcp.CallToolParams{
		Name:      "test_get_coverage_gaps",
		Arguments: json.RawMessage(`{"project_key":"proj","component_key":"../traversal"}`),
	})
	if !resComp.IsError || !strings.Contains(resComp.Content[0].Text, "forbidden characters") {
		t.Errorf("expected component_key validation error, got: %+v", resComp)
	}

	// 3. Invalid URL scheme in test_run_suite / Run
	t.Setenv("TEST_RUNNER_HOST_URL", "ftp://invalid-scheme.local")
	err := Run(context.Background(), bytes.NewBuffer(nil), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "invalid TEST_RUNNER_HOST_URL scheme") {
		t.Errorf("expected URL scheme error, got: %v", err)
	}
}
