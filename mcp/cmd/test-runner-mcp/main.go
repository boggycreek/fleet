// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package main implements the test-runner Model Context Protocol (MCP) server.
//
// This server exposes coverage-related operations over a verified
// (sandbox/pkg/sonar) SonarQube client, surfacing test coverage gaps for
// projects or components through the MCP JSON-RPC interface.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/boggycreek/fleet/cli/pkg/sonar"
	"github.com/boggycreek/fleet/mcp/pkg/mcp"
)

// JSONRPCMessage aliases mcp.JSONRPCMessage for testing compatibility.
type JSONRPCMessage = mcp.JSONRPCMessage

type (
	inputSchema = mcp.InputSchema
	property    = mcp.Property
)

// testRunnerTools is the set of tools registered on the test-runner server.
var testRunnerTools = []mcp.Tool{
	{
		Name:        "test_get_coverage_gaps",
		Description: "Query the SonarQube measures endpoint to surface test coverage gaps for a project or component",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"project_key": {
					Type:        "string",
					Description: "The unique key of the SonarQube project to retrieve coverage measures for",
				},
				"component_key": {
					Type:        "string",
					Description: "Optional component key. When set, coverage is queried for the specific component; when omitted, the project-level default is used.",
				},
			},
			Required: []string{"project_key"},
		},
	},
}

// MCPServer manages the STDIO JSON-RPC lifecycle for the test-runner server.
type MCPServer struct {
	*mcp.Server
	sonarClient *sonar.Client
}

// NewMCPServer creates a new test-runner MCP server instance backed by the
// given SonarQube client.
func NewMCPServer(client *sonar.Client, r io.Reader, w io.Writer) *MCPServer {
	s := &MCPServer{
		sonarClient: client,
	}
	s.Server = mcp.NewServer("test-runner-mcp", "0.1.0", r, w, testRunnerTools, s.executeTool)
	return s
}

// executeTool handles a single tool invocation. It leverages the verified
// sonar.Client.GetComponentMeasures method to surface test coverage gaps.
func (s *MCPServer) executeTool(ctx context.Context, params mcp.CallToolParams) mcp.CallToolResult {
	args := params.ParseArguments()

	projectKey, _ := args["project_key"].(string)
	if projectKey == "" {
		return mcp.ErrorResult("Missing required argument 'project_key'")
	}

	// Resolve the component key. When a specific component key is supplied it
	// is used directly; otherwise the project key serves as a sensible
	// fallback so coverage is still queryable at the project level.
	componentKey, _ := args["component_key"].(string)
	if componentKey == "" {
		componentKey = projectKey
	}

	// Retrieve per-component metric values via the verified SonarQube client.
	measures, err := s.sonarClient.GetComponentMeasures(ctx, componentKey, []string{"coverage"})
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("Error querying coverage measures: %v", err))
	}

	// Build a structured coverage-gap report. The measures are the raw
	// coverage values returned by SonarQube, which the operator uses to
	// assess test coverage gaps.
	report := map[string]any{
		"project_key":   projectKey,
		"component_key": componentKey,
		"component":     projectKey,
		"coverage_gaps": len(measures),
		"measures":      measures,
	}

	data, _ := json.MarshalIndent(report, "", "  ")
	return mcp.TextResult(string(data))
}

// test_run_suite is the entry point that executes the test-runner MCP server
// over STDIN/STDOUT. It runs the full test-coverage suite exposed by this
// server.
func test_run_suite(ctx context.Context, r io.Reader, w io.Writer) error {
	serverURL := os.Getenv("TEST_RUNNER_HOST_URL")
	if serverURL == "" {
		serverURL = os.Getenv("SONARQUBE_URL")
	}
	if serverURL == "" {
		serverURL = "http://127.0.0.1:9000"
	}
	token := os.Getenv("SONAR_TOKEN")

	client := sonar.NewClient(sonar.ClientConfig{
		BaseURL: serverURL,
		Token:   token,
	})

	server := NewMCPServer(client, r, w)
	return server.Serve(ctx)
}

// Run executes the test-runner MCP server reading from r and writing to w.
func Run(ctx context.Context, r io.Reader, w io.Writer) error {
	return test_run_suite(ctx, r, w)
}

func main() {
	if err := Run(context.Background(), os.Stdin, os.Stdout); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "test-runner-mcp server error: %v\n", err)
		os.Exit(1)
	}
}
