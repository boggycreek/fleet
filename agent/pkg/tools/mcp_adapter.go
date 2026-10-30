// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/boggycreek/fleet/agent/pkg/runtime"
	"github.com/boggycreek/fleet/mcp/pkg/mcp"
)

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// MCPClient connects to an MCP server over STDIO JSON-RPC 2.0.
type MCPClient struct {
	reader  *bufio.Reader
	writer  io.Writer
	closer  io.Closer
	cmd     *exec.Cmd
	stderr  *safeBuffer
	reqSeq  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan *mcp.JSONRPCMessage
	closed  bool
}

// NewMCPClient creates an MCPClient using arbitrary input/output streams.
func NewMCPClient(r io.Reader, w io.Writer, closer io.Closer) *MCPClient {
	c := &MCPClient{
		reader:  bufio.NewReader(r),
		writer:  w,
		closer:  closer,
		pending: make(map[int64]chan *mcp.JSONRPCMessage),
	}
	go c.readLoop()
	return c
}

// Stderr returns any stderr output captured from the managed MCP subprocess.
func (c *MCPClient) Stderr() string {
	if c.stderr == nil {
		return ""
	}
	return c.stderr.String()
}

// NewProcessMCPClient spawns an MCP server subprocess and connects over STDIO.
func NewProcessMCPClient(ctx context.Context, command string, args ...string) (*MCPClient, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to open stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("failed to open stdout pipe: %w", err)
	}

	stderrBuf := &safeBuffer{}
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		errDetail := strings.TrimSpace(stderrBuf.String())
		if errDetail != "" {
			return nil, fmt.Errorf("failed to start MCP process %s: %w (stderr: %s)", command, err, errDetail)
		}
		return nil, fmt.Errorf("failed to start MCP process %s: %w", command, err)
	}

	runtime.RegisterChildPID(cmd.Process.Pid)

	client := NewMCPClient(stdout, stdin, stdin)
	client.cmd = cmd
	client.stderr = stderrBuf
	return client, nil
}

func (c *MCPClient) readLoop() {
	for {
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			c.closePending(err)
			return
		}

		trimmed := strings.TrimSpace(string(line))
		if trimmed == "" {
			continue
		}

		var resp mcp.JSONRPCMessage
		if err := json.Unmarshal([]byte(trimmed), &resp); err != nil {
			continue
		}

		if resp.ID != nil {
			var id int64
			switch v := resp.ID.(type) {
			case float64:
				id = int64(v)
			case int64:
				id = v
			}

			c.mu.Lock()
			ch, exists := c.pending[id]
			if exists {
				delete(c.pending, id)
			}
			c.mu.Unlock()

			if exists && ch != nil {
				ch <- &resp
			}
		}
	}
}

func (c *MCPClient) closePending(_ error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

// Request sends a JSON-RPC 2.0 call and awaits the response.
func (c *MCPClient) Request(ctx context.Context, method string, params any) (*mcp.JSONRPCMessage, error) {
	reqID := c.reqSeq.Add(1)

	var rawParams json.RawMessage
	if params != nil {
		bytes, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal params: %w", err)
		}
		rawParams = bytes
	}

	req := mcp.JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  method,
		Params:  rawParams,
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	respChan := make(chan *mcp.JSONRPCMessage, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("mcp client is closed")
	}
	c.pending[reqID] = respChan
	c.mu.Unlock()

	c.mu.Lock()
	_, writeErr := c.writer.Write(append(reqBytes, '\n'))
	c.mu.Unlock()
	if writeErr != nil {
		c.mu.Lock()
		delete(c.pending, reqID)
		c.mu.Unlock()
		if c.stderr != nil {
			if stderrStr := strings.TrimSpace(c.stderr.String()); stderrStr != "" {
				return nil, fmt.Errorf("failed to write request: %w (stderr: %s)", writeErr, stderrStr)
			}
		}
		return nil, fmt.Errorf("failed to write request: %w", writeErr)
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, reqID)
		c.mu.Unlock()
		return nil, ctx.Err()
	case resp, ok := <-respChan:
		if !ok || resp == nil {
			if c.stderr != nil {
				if stderrStr := strings.TrimSpace(c.stderr.String()); stderrStr != "" {
					return nil, fmt.Errorf("connection closed before response received (stderr: %s)", stderrStr)
				}
			}
			return nil, errors.New("connection closed before response received")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("json-rpc error (%d): %s", resp.Error.Code, resp.Error.Message)
		}
		return resp, nil
	}
}

// Notify sends a JSON-RPC 2.0 notification without expecting a response.
func (c *MCPClient) Notify(method string, params any) error {
	var rawParams json.RawMessage
	if params != nil {
		bytes, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("failed to marshal params: %w", err)
		}
		rawParams = bytes
	}

	msg := mcp.JSONRPCMessage{
		JSONRPC: "2.0",
		Method:  method,
		Params:  rawParams,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return errors.New("mcp client is closed")
	}
	_, err = c.writer.Write(append(data, '\n'))
	return err
}

// Initialize performs the standard MCP handshake sequence.
func (c *MCPClient) Initialize(ctx context.Context) error {
	initParams := map[string]any{
		"protocolVersion": mcp.ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]string{
			"name":    "fleet-agent",
			"version": "1.0.0",
		},
	}

	_, err := c.Request(ctx, "initialize", initParams)
	if err != nil {
		return fmt.Errorf("initialize handshake failed: %w", err)
	}

	return c.Notify("notifications/initialized", nil)
}

// DiscoverTools queries the MCP server for available tools and returns Tool instances.
func (c *MCPClient) DiscoverTools(ctx context.Context) ([]Tool, error) {
	resp, err := c.Request(ctx, "tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("tools/list failed: %w", err)
	}

	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tools/list result: %w", err)
	}

	var listResult struct {
		Tools []mcp.Tool `json:"tools"`
	}
	if err := json.Unmarshal(resultBytes, &listResult); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tools/list: %w", err)
	}

	adaptedTools := make([]Tool, len(listResult.Tools))
	for i, mcpTool := range listResult.Tools {
		adaptedTools[i] = &MCPToolAdapter{
			client:  c,
			mcpTool: mcpTool,
		}
	}
	return adaptedTools, nil
}

// Close terminates the connection and subprocess if managed.
func (c *MCPClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	var firstErr error
	if c.closer != nil {
		if err := c.closer.Close(); err != nil {
			firstErr = err
		}
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = runtime.WaitManagedCmd(c.cmd)
		runtime.UnregisterChildPID(c.cmd.Process.Pid)
	}
	return firstErr
}

// MCPToolAdapter wraps an MCP tool into the native Tool interface.
type MCPToolAdapter struct {
	client  *MCPClient
	mcpTool mcp.Tool
}

func (a *MCPToolAdapter) Name() string        { return a.mcpTool.Name }
func (a *MCPToolAdapter) Description() string { return a.mcpTool.Description }

func (a *MCPToolAdapter) Parameters() map[string]any {
	bytes, err := json.Marshal(a.mcpTool.InputSchema)
	if err != nil {
		return map[string]any{"type": "object"}
	}
	var schema map[string]any
	_ = json.Unmarshal(bytes, &schema)
	if schema == nil {
		schema = map[string]any{"type": "object"}
	}
	return schema
}

func (a *MCPToolAdapter) Execute(ctx context.Context, args map[string]any) (string, error) {
	callParams := map[string]any{
		"name":      a.mcpTool.Name,
		"arguments": args,
	}

	resp, err := a.client.Request(ctx, "tools/call", callParams)
	if err != nil {
		return "", err
	}

	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return "", fmt.Errorf("failed to encode tool call result: %w", err)
	}

	var callResult mcp.CallToolResult
	if err := json.Unmarshal(resultBytes, &callResult); err != nil {
		return string(resultBytes), nil
	}

	var texts []string
	for _, block := range callResult.Content {
		if block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	combined := strings.Join(texts, "\n")

	if callResult.IsError {
		return combined, fmt.Errorf("tool returned error: %s", combined)
	}
	return combined, nil
}
