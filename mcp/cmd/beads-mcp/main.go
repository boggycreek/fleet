// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package main implements the Beads Graph Issue Tracker Model Context Protocol (MCP) server.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/boggycreek/fleet/mcp/pkg/mcp"
)

// JSONRPCMessage aliases mcp.JSONRPCMessage for testing compatibility.
type JSONRPCMessage = mcp.JSONRPCMessage

type (
	inputSchema = mcp.InputSchema
	property    = mcp.Property
)

// CommandRunner executes commands against the beads CLI
type CommandRunner interface {
	Run(ctx context.Context, dir string, args ...string) (stdout, stderr []byte, err error)
}

// OSCommandRunner is the production implementation executing the native bd binary
type OSCommandRunner struct {
	BinaryPath string
}

// Run executes the bd CLI command in the specified directory
func (r *OSCommandRunner) Run(ctx context.Context, dir string, args ...string) (stdout, stderr []byte, err error) {
	bin := r.BinaryPath
	if bin == "" {
		bin = os.Getenv("BEADS_BIN")
	}
	if bin == "" {
		bin = "bd"
	}

	// #nosec G204 - beads-mcp executes the configured beads binary with tool arguments
	cmd := exec.CommandContext(ctx, bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	cmdErr := cmd.Run()
	return stdoutBuf.Bytes(), stderrBuf.Bytes(), cmdErr
}

var beadsTools = []mcp.Tool{
	{
		Name:        "bd_ready",
		Description: "Surface unblocked tasks and issues ready to be worked on from the Beads dependency graph",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"dir": {
					Type:        "string",
					Description: "Task repository directory path (optional, defaults to ~/tasks)",
				},
				"json": {
					Type:        "boolean",
					Description: "Return ready items formatted as JSON objects",
				},
			},
		},
	},
	{
		Name:        "bd_list",
		Description: "List issues and tasks in the Beads dependency graph",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"dir": {
					Type:        "string",
					Description: "Task repository directory path (optional, defaults to ~/tasks)",
				},
				"all": {
					Type:        "boolean",
					Description: "Include closed and deferred issues",
				},
				"status": {
					Type:        "string",
					Description: "Filter by status: 'open', 'in_progress', 'closed', 'deferred', 'blocked'",
				},
				"priority": {
					Type:        "string",
					Description: "Filter by priority: 'P0', 'P1', 'P2', 'P3', 'P4'",
				},
				"json": {
					Type:        "boolean",
					Description: "Return list formatted as JSON objects",
				},
			},
		},
	},
	{
		Name:        "bd_show",
		Description: "Inspect detailed issue specifications, dependencies, acceptance criteria, and history",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"id": {
					Type:        "string",
					Description: "Issue or task identifier (e.g. task-12)",
				},
				"dir": {
					Type:        "string",
					Description: "Task repository directory path (optional, defaults to ~/tasks)",
				},
				"json": {
					Type:        "boolean",
					Description: "Return details formatted as JSON object",
				},
			},
			Required: []string{"id"},
		},
	},
	{
		Name:        "bd_create",
		Description: "Create a new task, feature, bug, or epic in the Beads issue graph",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"title": {
					Type:        "string",
					Description: "Issue title or summary",
				},
				"type": {
					Type:        "string",
					Description: "Issue type: 'task', 'feature', 'bug', 'chore', 'epic' (default: task)",
				},
				"priority": {
					Type:        "string",
					Description: "Priority tier: 'P0', 'P1', 'P2', 'P3', 'P4' (default: P2)",
				},
				"description": {
					Type:        "string",
					Description: "Detailed task description, acceptance criteria, or technical design",
				},
				"parent": {
					Type:        "string",
					Description: "Parent issue ID if creating a child subtask",
				},
				"dir": {
					Type:        "string",
					Description: "Task repository directory path (optional, defaults to ~/tasks)",
				},
			},
			Required: []string{"title"},
		},
	},
	{
		Name:        "bd_claim",
		Description: "Atomically claim an issue or task for the current agent",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"id": {
					Type:        "string",
					Description: "Issue or task identifier to claim (e.g. task-12)",
				},
				"dir": {
					Type:        "string",
					Description: "Task repository directory path (optional, defaults to ~/tasks)",
				},
			},
			Required: []string{"id"},
		},
	},
	{
		Name:        "bd_close",
		Description: "Close a completed task or issue with a completion reason",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"id": {
					Type:        "string",
					Description: "Issue or task identifier to close (e.g. task-12)",
				},
				"reason": {
					Type:        "string",
					Description: "Reason for closing the issue or commit reference",
				},
				"dir": {
					Type:        "string",
					Description: "Task repository directory path (optional, defaults to ~/tasks)",
				},
			},
			Required: []string{"id"},
		},
	},
	{
		Name:        "bd_sync",
		Description: "Synchronize local Dolt issue tracker commits with the upstream remote",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"dir": {
					Type:        "string",
					Description: "Task repository directory path (optional, defaults to ~/tasks)",
				},
			},
		},
	},
}

// MCPServer manages the STDIO JSON-RPC lifecycle for Beads issue tracker integration
type MCPServer struct {
	*mcp.Server
	runner     CommandRunner
	defaultDir string
}

// NewMCPServer creates a new Beads MCP server instance
func NewMCPServer(runner CommandRunner, defaultDir string, r io.Reader, w io.Writer) *MCPServer {
	if defaultDir == "" {
		defaultDir = os.Getenv("FLEET_TASKS_DIR")
	}
	if defaultDir == "" {
		if _, err := os.Stat("/home/agent/tasks"); err == nil {
			defaultDir = "/home/agent/tasks"
		}
	}

	s := &MCPServer{
		runner:     runner,
		defaultDir: defaultDir,
	}
	s.Server = mcp.NewServer("beads-mcp", "0.1.0", r, w, beadsTools, s.executeTool)
	return s
}

// Serve handles incoming JSON-RPC requests.
func (s *MCPServer) Serve(ctx context.Context) error {
	return s.Server.Serve(ctx)
}

func (s *MCPServer) executeTool(ctx context.Context, params mcp.CallToolParams) mcp.CallToolResult {
	argsMap := params.ParseArguments()

	dir, _ := argsMap["dir"].(string)
	if dir == "" {
		dir = s.defaultDir
	}

	var args []string

	switch params.Name {
	case "bd_ready":
		args = append(args, "ready")
		if jsonVal, ok := argsMap["json"].(bool); ok && jsonVal {
			args = append(args, "--json")
		}

	case "bd_list":
		args = append(args, "list")
		if allVal, ok := argsMap["all"].(bool); ok && allVal {
			args = append(args, "--all")
		}
		if statusVal, ok := argsMap["status"].(string); ok && statusVal != "" {
			args = append(args, "--status", statusVal)
		}
		if prioVal, ok := argsMap["priority"].(string); ok && prioVal != "" {
			args = append(args, "--priority", prioVal)
		}
		if jsonVal, ok := argsMap["json"].(bool); ok && jsonVal {
			args = append(args, "--json")
		}

	case "bd_show":
		id, _ := argsMap["id"].(string)
		if id == "" {
			return mcp.ErrorResult("Missing required argument 'id'")
		}
		args = append(args, "show", id)
		if jsonVal, ok := argsMap["json"].(bool); ok && jsonVal {
			args = append(args, "--json")
		}

	case "bd_create":
		title, _ := argsMap["title"].(string)
		if title == "" {
			return mcp.ErrorResult("Missing required argument 'title'")
		}
		args = append(args, "create", title)
		if typeVal, ok := argsMap["type"].(string); ok && typeVal != "" {
			args = append(args, "--type", typeVal)
		}
		if prioVal, ok := argsMap["priority"].(string); ok && prioVal != "" {
			args = append(args, "--priority", prioVal)
		}
		if descVal, ok := argsMap["description"].(string); ok && descVal != "" {
			args = append(args, "--description", descVal)
		}
		if parentVal, ok := argsMap["parent"].(string); ok && parentVal != "" {
			args = append(args, "--parent", parentVal)
		}

	case "bd_claim":
		id, _ := argsMap["id"].(string)
		if id == "" {
			return mcp.ErrorResult("Missing required argument 'id'")
		}
		args = append(args, "update", id, "--claim")

	case "bd_close":
		id, _ := argsMap["id"].(string)
		if id == "" {
			return mcp.ErrorResult("Missing required argument 'id'")
		}
		args = append(args, "close", id)
		if reasonVal, ok := argsMap["reason"].(string); ok && reasonVal != "" {
			args = append(args, "--reason", reasonVal)
		}

	case "bd_sync":
		args = append(args, "sync")

	default:
		return mcp.ErrorResult(fmt.Sprintf("Unknown tool: %s", params.Name))
	}

	stdout, stderr, err := s.runner.Run(ctx, dir, args...)
	if err != nil {
		errText := strings.TrimSpace(string(stderr))
		if errText == "" {
			errText = err.Error()
		}
		return mcp.ErrorResult(fmt.Sprintf("Error executing 'bd %s': %s", strings.Join(args, " "), errText))
	}

	// For mutating operations, attempt best-effort sync with remote forge
	if params.Name == "bd_create" || params.Name == "bd_claim" || params.Name == "bd_close" {
		_, _, _ = s.runner.Run(ctx, dir, "sync")
	}

	outText := strings.TrimSpace(string(stdout))
	if outText == "" {
		outText = "OK"
	}
	return mcp.TextResult(outText)
}

// Run executes the MCP server reading from r and writing to w
func Run(ctx context.Context, r io.Reader, w io.Writer) error {
	runner := &OSCommandRunner{}
	defaultDir := os.Getenv("FLEET_TASKS_DIR")
	server := NewMCPServer(runner, defaultDir, r, w)
	return server.Serve(ctx)
}

func main() {
	if err := Run(context.Background(), os.Stdin, os.Stdout); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "beads-mcp server error: %v\n", err)
		os.Exit(1)
	}
}
