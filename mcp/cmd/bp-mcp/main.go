// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package main implements the Fleet Backplane Model Context Protocol (MCP) server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
	"github.com/boggycreek/fleet/mcp/pkg/mcp"
)

// JSONRPCMessage aliases mcp.JSONRPCMessage for testing compatibility.
type JSONRPCMessage = mcp.JSONRPCMessage

type (
	inputSchema = mcp.InputSchema
	property    = mcp.Property
)

// BPClient defines the interface required by the Backplane MCP server
type BPClient interface {
	Say(ctx context.Context, content string, blobPath string) (*libbp.Message, error)
	Tell(ctx context.Context, recipient string, content string) (*libbp.Message, error)
	Reply(ctx context.Context, replyToMsgID string, recipient string, content string) (*libbp.Message, error)
	Recv(ctx context.Context, blockSeconds int) ([]*libbp.Message, error)
	Peers(ctx context.Context) ([]*libbp.Peer, error)
	SetStatus(ctx context.Context, statusText string) error
	Close() error
}

var bpTools = []mcp.Tool{
	{
		Name:        "fleet_send_message",
		Description: "Send a direct, signed point-to-point message or threaded reply to another agent or human operator inbox",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"recipient": {
					Type:        "string",
					Description: "Target agent name or operator ID (e.g. opencode-1, operator)",
				},
				"message": {
					Type:        "string",
					Description: "Content of the message",
				},
				"reply_to": {
					Type:        "string",
					Description: "Optional citation ID or message ID being replied to (e.g. agent-1#42 or stream ID)",
				},
				"file_path": {
					Type:        "string",
					Description: "Optional local file path to attach as blob",
				},
			},
			Required: []string{"recipient", "message"},
		},
	},
	{
		Name:        "fleet_broadcast",
		Description: "Broadcast a public signed message to the entire fleet public feed",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"message": {
					Type:        "string",
					Description: "Content of the broadcast announcement",
				},
				"file_path": {
					Type:        "string",
					Description: "Optional local file path to attach as a public blob",
				},
			},
			Required: []string{"message"},
		},
	},
	{
		Name:        "fleet_read_inbox",
		Description: "Read new pending messages from agent inbox and peer broadcast feeds",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"block_seconds": {
					Type:        "integer",
					Description: "Optional blocking timeout in seconds (0 for non-blocking)",
				},
				"count": {
					Type:        "integer",
					Description: "Maximum number of messages to return (default: all pending)",
				},
			},
		},
	},
	{
		Name:        "fleet_list_peers",
		Description: "List all known and active peer agents in the fleet, their status, roles, and liaison status",
		InputSchema: inputSchema{
			Type: "object",
		},
	},
	{
		Name:        "fleet_set_status",
		Description: "Update current agent operational status broadcasted to the fleet",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"status": {
					Type:        "string",
					Description: "Agent status text (e.g. idle, working: task-123, blocked: awaiting human input)",
				},
			},
			Required: []string{"status"},
		},
	},
}

// MCPServer manages the STDIO JSON-RPC lifecycle for Fleet Backplane messaging
type MCPServer struct {
	*mcp.Server
	bpClient BPClient
}

// NewMCPServer creates a new Backplane MCP server instance
func NewMCPServer(client BPClient, r io.Reader, w io.Writer) *MCPServer {
	s := &MCPServer{
		bpClient: client,
	}
	s.Server = mcp.NewServer("bp-mcp", "0.1.0", r, w, bpTools, s.executeTool)
	return s
}

func sanitizeRecipient(r string) (string, error) {
	trimmed := strings.TrimSpace(r)
	if trimmed == "" {
		return "", fmt.Errorf("recipient cannot be empty")
	}
	if strings.ContainsAny(trimmed, " \t\r\n\x00") {
		return "", fmt.Errorf("recipient contains forbidden whitespace or control characters: %q", r)
	}
	return trimmed, nil
}

func sanitizeFilePath(fp string) (string, error) {
	if fp == "" {
		return "", nil
	}
	if strings.Contains(fp, "\x00") {
		return "", fmt.Errorf("file_path contains forbidden null bytes")
	}
	return filepath.Clean(strings.TrimSpace(fp)), nil
}

func (s *MCPServer) executeTool(ctx context.Context, params mcp.CallToolParams) mcp.CallToolResult {
	args := params.ParseArguments()

	switch params.Name {
	case "fleet_send_message":
		recipient, _ := args["recipient"].(string)
		message, _ := args["message"].(string)
		replyTo, _ := args["reply_to"].(string)

		cleanRecipient, err := sanitizeRecipient(recipient)
		if err != nil {
			return mcp.ErrorResult(err.Error())
		}
		if strings.TrimSpace(message) == "" || strings.Contains(message, "\x00") {
			return mcp.ErrorResult("Missing or invalid argument 'message'")
		}

		var sentMsg *libbp.Message

		if strings.TrimSpace(replyTo) != "" {
			if strings.Contains(replyTo, "\x00") {
				return mcp.ErrorResult("reply_to contains forbidden null bytes")
			}
			sentMsg, err = s.bpClient.Reply(ctx, strings.TrimSpace(replyTo), cleanRecipient, message)
		} else {
			sentMsg, err = s.bpClient.Tell(ctx, cleanRecipient, message)
		}

		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Error sending fleet message: %v", err))
		}

		data, _ := json.MarshalIndent(sentMsg, "", "  ")
		return mcp.TextResult(string(data))

	case "fleet_broadcast":
		message, _ := args["message"].(string)
		filePath, _ := args["file_path"].(string)

		if strings.TrimSpace(message) == "" || strings.Contains(message, "\x00") {
			return mcp.ErrorResult("Missing or invalid argument 'message'")
		}
		cleanFP, err := sanitizeFilePath(filePath)
		if err != nil {
			return mcp.ErrorResult(err.Error())
		}

		sentMsg, err := s.bpClient.Say(ctx, message, cleanFP)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Error broadcasting fleet message: %v", err))
		}

		data, _ := json.MarshalIndent(sentMsg, "", "  ")
		return mcp.TextResult(string(data))

	case "fleet_read_inbox":
		var blockSeconds int
		if bsVal, ok := args["block_seconds"].(float64); ok {
			blockSeconds = int(bsVal)
		}
		if blockSeconds < 0 {
			blockSeconds = 0
		}
		if blockSeconds > 300 {
			blockSeconds = 300
		}

		var maxCount int
		if mcVal, ok := args["count"].(float64); ok {
			maxCount = int(mcVal)
		}
		if maxCount < 0 {
			maxCount = 0
		}
		if maxCount > 500 {
			maxCount = 500
		}

		msgs, err := s.bpClient.Recv(ctx, blockSeconds)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Error reading fleet inbox: %v", err))
		}

		if maxCount > 0 && len(msgs) > maxCount {
			msgs = msgs[:maxCount]
		}

		data, _ := json.MarshalIndent(msgs, "", "  ")
		return mcp.TextResult(string(data))

	case "fleet_list_peers":
		peers, err := s.bpClient.Peers(ctx)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Error listing fleet peers: %v", err))
		}

		data, _ := json.MarshalIndent(peers, "", "  ")
		return mcp.TextResult(string(data))

	case "fleet_set_status":
		status, _ := args["status"].(string)
		if strings.TrimSpace(status) == "" || strings.Contains(status, "\x00") {
			return mcp.ErrorResult("Missing or invalid argument 'status'")
		}

		cleanStatus := strings.TrimSpace(status)
		if err := s.bpClient.SetStatus(ctx, cleanStatus); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Error updating fleet status: %v", err))
		}

		return mcp.TextResult(fmt.Sprintf("Status updated to %q", cleanStatus))

	default:
		return mcp.ErrorResult(fmt.Sprintf("Unknown tool: %s", params.Name))
	}
}

// Run executes the MCP server reading from r and writing to w
func Run(ctx context.Context, r io.Reader, w io.Writer) error {
	cfg := libbp.LoadClientFromEnv()
	client, err := libbp.Dial(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed connecting to backplane: %w", err)
	}
	defer func() {
		_ = client.Close()
	}()

	server := NewMCPServer(client, r, w)
	return server.Serve(ctx)
}

func main() {
	if err := Run(context.Background(), os.Stdin, os.Stdout); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "bp-mcp server error: %v\n", err)
		os.Exit(1)
	}
}
