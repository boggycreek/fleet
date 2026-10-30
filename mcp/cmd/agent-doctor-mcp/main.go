// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package main implements the in-container Environment & Doctor Model Context Protocol (MCP) server.
//
// This server exposes environment connectivity verification, runtime resource inspection,
// and human operator escalation assistance over the MCP JSON-RPC interface.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
	"github.com/boggycreek/sandbox/mcp/pkg/mcp"
)

// JSONRPCMessage aliases mcp.JSONRPCMessage for testing compatibility.
type JSONRPCMessage = mcp.JSONRPCMessage

type (
	inputSchema = mcp.InputSchema
	property    = mcp.Property
)

// BPClient defines the minimal interface required for backplane human escalation.
type BPClient interface {
	Tell(ctx context.Context, recipient string, content string) (*libbp.Message, error)
	Close() error
}

// ConnectivityChecker defines the interface for testing endpoint reachability.
type ConnectivityChecker interface {
	CheckTCP(ctx context.Context, address string, timeout time.Duration) (time.Duration, error)
	CheckHTTP(ctx context.Context, targetURL string, timeout time.Duration) (int, time.Duration, error)
}

// DefaultConnectivityChecker implements real network dialing.
type DefaultConnectivityChecker struct{}

func (d *DefaultConnectivityChecker) CheckTCP(ctx context.Context, address string, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	latency := time.Since(start)
	if err != nil {
		return latency, err
	}
	_ = conn.Close()
	return latency, nil
}

func (d *DefaultConnectivityChecker) CheckHTTP(ctx context.Context, targetURL string, timeout time.Duration) (int, time.Duration, error) {
	start := time.Now()
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return 0, time.Since(start), err
	}
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return 0, latency, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, latency, nil
}

var agentDoctorTools = []mcp.Tool{
	{
		Name:        "env_check_connectivity",
		Description: "Verify network and service connectivity to local backplane, git forge, LLM gateway, or custom endpoints",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"endpoints": {
					Type:        "array",
					Description: "Optional list of additional HTTP URLs or host:port TCP addresses to test",
				},
				"timeout_ms": {
					Type:        "integer",
					Description: "Timeout per check in milliseconds (default: 2000)",
				},
			},
		},
	},
	{
		Name:        "env_inspect_resources",
		Description: "Inspect agent runtime resources, OS architecture, sanitized environment variables, and working directories",
		InputSchema: inputSchema{
			Type: "object",
		},
	},
	{
		Name:        "env_request_host_assistance",
		Description: "Escalate an issue to the human operator via the Valkey backplane messaging bus",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"reason": {
					Type:        "string",
					Description: "Clear explanation of why operator intervention is required",
				},
				"context": {
					Type:        "string",
					Description: "Optional additional diagnostic context or error logs",
				},
			},
			Required: []string{"reason"},
		},
	},
}

// MCPServer manages the STDIO JSON-RPC lifecycle for the agent-doctor server.
type MCPServer struct {
	*mcp.Server
	bpClient BPClient
	checker  ConnectivityChecker
}

// NewMCPServer creates a new agent-doctor MCP server instance.
func NewMCPServer(bpClient BPClient, checker ConnectivityChecker, r io.Reader, w io.Writer) *MCPServer {
	if checker == nil {
		checker = &DefaultConnectivityChecker{}
	}
	s := &MCPServer{
		bpClient: bpClient,
		checker:  checker,
	}
	s.Server = mcp.NewServer("agent-doctor-mcp", "0.1.0", r, w, agentDoctorTools, s.executeTool)
	return s
}

// EndpointResult contains the diagnostic reachability result for a single target.
type EndpointResult struct {
	Target    string `json:"target"`
	Protocol  string `json:"protocol"`
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms"`
	Code      int    `json:"code,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (s *MCPServer) executeTool(ctx context.Context, params mcp.CallToolParams) mcp.CallToolResult {
	args := params.ParseArguments()

	switch params.Name {
	case "env_check_connectivity":
		timeout := 2 * time.Second
		if toVal, ok := args["timeout_ms"].(float64); ok && toVal > 0 {
			timeout = time.Duration(toVal) * time.Millisecond
		}

		targets := []string{}
		// Default well-known targets from environment
		bpHost := os.Getenv("BP_HOST")
		if bpHost == "" {
			bpHost = "127.0.0.1"
		}
		bpPort := os.Getenv("BP_PORT")
		if bpPort == "" {
			bpPort = "6379"
		}
		targets = append(targets, net.JoinHostPort(bpHost, bpPort))

		forgejoURL := os.Getenv("FORGEJO_URL")
		if forgejoURL == "" {
			forgejoURL = os.Getenv("GITEA_URL")
		}
		if forgejoURL != "" {
			targets = append(targets, forgejoURL)
		}

		llmGateway := os.Getenv("LLM_GATEWAY_URL")
		if llmGateway == "" {
			llmGateway = "http://llm-gateway:11434"
		}
		targets = append(targets, llmGateway)

		// Append custom endpoints if specified
		if customList, ok := args["endpoints"].([]any); ok {
			for _, item := range customList {
				if str, ok := item.(string); ok && str != "" {
					targets = append(targets, str)
				}
			}
		}

		var results []EndpointResult
		for _, target := range targets {
			res := EndpointResult{Target: target}
			if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
				res.Protocol = "HTTP"
				code, lat, err := s.checker.CheckHTTP(ctx, target, timeout)
				res.LatencyMs = lat.Milliseconds()
				res.Code = code
				if err != nil {
					res.Status = "UNREACHABLE"
					res.Error = err.Error()
				} else {
					res.Status = "OK"
				}
			} else {
				res.Protocol = "TCP"
				lat, err := s.checker.CheckTCP(ctx, target, timeout)
				res.LatencyMs = lat.Milliseconds()
				if err != nil {
					res.Status = "UNREACHABLE"
					res.Error = err.Error()
				} else {
					res.Status = "OK"
				}
			}
			results = append(results, res)
		}

		data, _ := json.MarshalIndent(results, "", "  ")
		return mcp.TextResult(string(data))

	case "env_inspect_resources":
		wd, _ := os.Getwd()
		hostname, _ := os.Hostname()

		sensitiveKeys := []string{"PASSWORD", "SECRET", "KEY", "TOKEN", "AUTH", "CREDENTIAL"}
		sanitizedEnv := map[string]string{}
		for _, envLine := range os.Environ() {
			parts := strings.SplitN(envLine, "=", 2)
			if len(parts) == 2 {
				k, v := parts[0], parts[1]
				isSensitive := false
				upperKey := strings.ToUpper(k)
				for _, sKey := range sensitiveKeys {
					if strings.Contains(upperKey, sKey) {
						isSensitive = true
						break
					}
				}
				if isSensitive {
					sanitizedEnv[k] = "[REDACTED]"
				} else {
					sanitizedEnv[k] = v
				}
			}
		}

		report := map[string]any{
			"agent": map[string]any{
				"name":      os.Getenv("BP_AGENT"),
				"hostname":  hostname,
				"workspace": wd,
			},
			"system": map[string]any{
				"os":         runtime.GOOS,
				"arch":       runtime.GOARCH,
				"num_cpu":    runtime.NumCPU(),
				"go_version": runtime.Version(),
			},
			"environment": sanitizedEnv,
		}

		data, _ := json.MarshalIndent(report, "", "  ")
		return mcp.TextResult(string(data))

	case "env_request_host_assistance":
		reason, _ := args["reason"].(string)
		if reason == "" {
			return mcp.ErrorResult("Missing required argument 'reason'")
		}
		contextInfo, _ := args["context"].(string)

		if s.bpClient == nil {
			return mcp.ErrorResult("Backplane client not configured; cannot request human assistance")
		}

		agentName := os.Getenv("BP_AGENT")
		if agentName == "" {
			agentName = "agent"
		}

		messageContent := fmt.Sprintf("[HUMAN ASSISTANCE REQUESTED by %s]\nReason: %s", agentName, reason)
		if contextInfo != "" {
			messageContent += fmt.Sprintf("\nContext:\n%s", contextInfo)
		}

		recipient := os.Getenv("HUMAN_NAME")
		if recipient == "" {
			recipient = "operator"
		}

		msg, err := s.bpClient.Tell(ctx, recipient, messageContent)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Failed dispatching escalation to operator: %v", err))
		}

		res := map[string]any{
			"status":    "dispatched",
			"recipient": recipient,
			"reason":    reason,
			"msg_id":    msg.ID,
		}
		data, _ := json.MarshalIndent(res, "", "  ")
		return mcp.TextResult(string(data))

	default:
		return mcp.ErrorResult(fmt.Sprintf("Unknown tool: %s", params.Name))
	}
}

// Run executes the agent-doctor MCP server reading from r and writing to w.
func Run(ctx context.Context, r io.Reader, w io.Writer) error {
	cfg := libbp.LoadClientFromEnv()
	var bpClient BPClient
	if client, err := libbp.Dial(ctx, cfg); err == nil {
		bpClient = client
		defer func() {
			_ = client.Close()
		}()
	}

	checker := &DefaultConnectivityChecker{}
	server := NewMCPServer(bpClient, checker, r, w)
	return server.Serve(ctx)
}

func main() {
	if err := Run(context.Background(), os.Stdin, os.Stdout); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "agent-doctor-mcp server error: %v\n", err)
		os.Exit(1)
	}
}
