// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package main implements the SonarQube Model Context Protocol (MCP) server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/boggycreek/fleet/cli/pkg/sonar"
	"github.com/boggycreek/fleet/mcp/pkg/mcp"
)

// JSONRPCMessage aliases mcp.JSONRPCMessage for testing compatibility.
type JSONRPCMessage = mcp.JSONRPCMessage

type (
	inputSchema = mcp.InputSchema
	property    = mcp.Property
)

var sonarTools = []mcp.Tool{
	{
		Name:        "sonar_status",
		Description: "Check SonarQube server operational status and version",
		InputSchema: inputSchema{
			Type: "object",
		},
	},
	{
		Name:        "sonar_quality_gate",
		Description: "Retrieve Quality Gate pass/fail status and condition metrics for a project",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"project_key": {
					Type:        "string",
					Description: "The unique key of the SonarQube project",
				},
			},
			Required: []string{"project_key"},
		},
	},
	{
		Name:        "sonar_issues",
		Description: "List mechanical code issues, bugs, vulnerabilities, and code smells",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"project_key": {
					Type:        "string",
					Description: "The unique key of the project",
				},
				"severity": {
					Type:        "string",
					Description: "Filter by severity (BLOCKER, CRITICAL, MAJOR, MINOR, INFO)",
				},
				"issue_type": {
					Type:        "string",
					Description: "Filter by type (BUG, VULNERABILITY, CODE_SMELL, SECURITY_HOTSPOT)",
				},
			},
			Required: []string{"project_key"},
		},
	},
	{
		Name:        "sonar_measures",
		Description: "Retrieve project code metrics such as test coverage, duplicated lines, and complexity",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"project_key": {
					Type:        "string",
					Description: "The unique key of the project",
				},
				"metric_keys": {
					Type:        "string",
					Description: "Comma-separated metric keys (e.g. coverage,duplicated_lines_density,cognitive_complexity)",
				},
			},
			Required: []string{"project_key"},
		},
	},
	{
		Name:        "sonar_create_project",
		Description: "Create or register a project space in SonarQube",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"project_key": {
					Type:        "string",
					Description: "The unique key for the project",
				},
				"name": {
					Type:        "string",
					Description: "Human-readable project display name",
				},
			},
			Required: []string{"project_key", "name"},
		},
	},
}

// MCPServer manages the STDIO JSON-RPC lifecycle for SonarQube integration
type MCPServer struct {
	*mcp.Server
	sonarClient *sonar.Client
}

// NewMCPServer creates a new SonarQube MCP server instance
func NewMCPServer(client *sonar.Client, r io.Reader, w io.Writer) *MCPServer {
	s := &MCPServer{
		sonarClient: client,
	}
	s.Server = mcp.NewServer("sonar-mcp", "0.1.0", r, w, sonarTools, s.executeTool)
	return s
}

func (s *MCPServer) executeTool(ctx context.Context, params mcp.CallToolParams) mcp.CallToolResult {
	args := params.ParseArguments()

	switch params.Name {
	case "sonar_status":
		status, err := s.sonarClient.GetSystemStatus(ctx)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Error checking SonarQube status: %v", err))
		}
		data, _ := json.MarshalIndent(status, "", "  ")
		return mcp.TextResult(string(data))

	case "sonar_quality_gate":
		projectKey, _ := args["project_key"].(string)
		if projectKey == "" {
			return mcp.ErrorResult("Missing required argument 'project_key'")
		}
		qg, err := s.sonarClient.GetProjectQualityGate(ctx, projectKey)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Error retrieving quality gate: %v", err))
		}
		data, _ := json.MarshalIndent(qg, "", "  ")
		return mcp.TextResult(string(data))

	case "sonar_issues":
		projectKey, _ := args["project_key"].(string)
		if projectKey == "" {
			return mcp.ErrorResult("Missing required argument 'project_key'")
		}
		severity, _ := args["severity"].(string)
		issueType, _ := args["issue_type"].(string)
		issues, err := s.sonarClient.GetIssues(ctx, projectKey, severity, issueType)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Error querying issues: %v", err))
		}
		data, _ := json.MarshalIndent(issues, "", "  ")
		return mcp.TextResult(string(data))

	case "sonar_measures":
		projectKey, _ := args["project_key"].(string)
		if projectKey == "" {
			return mcp.ErrorResult("Missing required argument 'project_key'")
		}
		metricKeysStr, _ := args["metric_keys"].(string)
		var metricKeys []string
		if metricKeysStr != "" {
			for _, m := range strings.Split(metricKeysStr, ",") {
				if t := strings.TrimSpace(m); t != "" {
					metricKeys = append(metricKeys, t)
				}
			}
		}
		if len(metricKeys) == 0 {
			metricKeys = []string{"coverage", "duplicated_lines_density", "cognitive_complexity", "bugs", "vulnerabilities", "code_smells"}
		}
		measures, err := s.sonarClient.GetComponentMeasures(ctx, projectKey, metricKeys)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Error querying measures: %v", err))
		}
		data, _ := json.MarshalIndent(measures, "", "  ")
		return mcp.TextResult(string(data))

	case "sonar_create_project":
		projectKey, _ := args["project_key"].(string)
		name, _ := args["name"].(string)
		if projectKey == "" || name == "" {
			return mcp.ErrorResult("Missing required arguments 'project_key' and 'name'")
		}
		if err := s.sonarClient.CreateProject(ctx, projectKey, name); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Error creating project: %v", err))
		}
		return mcp.TextResult(fmt.Sprintf("Project %s (%s) successfully ensured", projectKey, name))

	default:
		return mcp.ErrorResult(fmt.Sprintf("Unknown tool: %s", params.Name))
	}
}

// Run executes the MCP server reading from r and writing to w
func Run(ctx context.Context, r io.Reader, w io.Writer) error {
	sonarURL := os.Getenv("SONAR_HOST_URL")
	if sonarURL == "" {
		sonarURL = os.Getenv("SONARQUBE_URL")
	}
	if sonarURL == "" {
		sonarURL = "http://127.0.0.1:9000"
	}
	sonarToken := os.Getenv("SONAR_TOKEN")

	client := sonar.NewClient(sonar.ClientConfig{
		BaseURL: sonarURL,
		Token:   sonarToken,
	})

	server := NewMCPServer(client, r, w)
	return server.Serve(ctx)
}

func main() {
	if err := Run(context.Background(), os.Stdin, os.Stdout); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "sonar-mcp server error: %v\n", err)
		os.Exit(1)
	}
}
