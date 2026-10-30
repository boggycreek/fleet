// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// ToolHandler defines the callback signature for executing a tool invocation.
type ToolHandler func(ctx context.Context, params CallToolParams) CallToolResult

// Server manages the STDIO JSON-RPC lifecycle for an MCP server.
type Server struct {
	name        string
	version     string
	tools       []Tool
	toolHandler ToolHandler
	reader      *bufio.Reader
	writer      io.Writer
	mu          sync.Mutex
}

// NewServer creates a new MCP server instance with the given metadata, I/O streams, tools, and execution handler.
func NewServer(name, version string, r io.Reader, w io.Writer, tools []Tool, handler ToolHandler) *Server {
	return &Server{
		name:        name,
		version:     version,
		tools:       tools,
		toolHandler: handler,
		reader:      bufio.NewReader(r),
		writer:      w,
	}
}

// Tools returns the registered tool definitions.
func (s *Server) Tools() []Tool {
	return s.tools
}

// Name returns the server name.
func (s *Server) Name() string {
	return s.name
}

// Version returns the server version.
func (s *Server) Version() string {
	return s.version
}

const maxLineLength = 10 << 20 // 10MB max request line length

// jsonRPCResponse is used internally to guarantee that the "id" field is explicitly serialized
// as "id": null (or "id": <value>) in JSON-RPC 2.0 response objects.
type jsonRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id"`
	Result  any           `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

func readLineBounded(r *bufio.Reader, maxLen int) ([]byte, error) {
	var line []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			if len(line) > 0 && errors.Is(err, io.EOF) {
				return line, nil
			}
			return nil, err
		}
		line = append(line, chunk...)
		if len(line) > maxLen {
			for isPrefix {
				_, isPrefix, err = r.ReadLine()
				if err != nil {
					break
				}
			}
			return nil, fmt.Errorf("request message line exceeds maximum limit of %d bytes", maxLen)
		}
		if !isPrefix {
			break
		}
	}
	return line, nil
}

// Serve handles incoming JSON-RPC requests until context cancellation or EOF.
func (s *Server) Serve(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := readLineBounded(s.reader, maxLineLength)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			if strings.Contains(err.Error(), "exceeds maximum limit") {
				_ = s.SendError(nil, CodeInvalidRequest, err.Error())
				continue
			}
			return err
		}

		trimmed := strings.TrimSpace(string(line))
		if trimmed == "" {
			continue
		}

		var req JSONRPCMessage
		if err := json.Unmarshal([]byte(trimmed), &req); err != nil {
			_ = s.SendError(nil, CodeParseError, "Parse error")
			continue
		}

		if req.JSONRPC != "2.0" {
			_ = s.SendError(req.ID, CodeInvalidRequest, "Invalid Request: jsonrpc must be '2.0'")
			continue
		}

		s.handleMessage(ctx, &req)
	}
}

func (s *Server) handleMessage(ctx context.Context, req *JSONRPCMessage) {
	if strings.HasPrefix(req.Method, "notifications/") || req.Method == "notifications/initialized" {
		// No response required for notifications
		return
	}

	switch req.Method {
	case "initialize":
		res := map[string]any{
			"protocolVersion": ProtocolVersion,
			"serverInfo": map[string]string{
				"name":    s.name,
				"version": s.version,
			},
			"capabilities": map[string]any{
				"tools": map[string]bool{},
			},
		}
		_ = s.SendResult(req.ID, res)

	case "tools/list":
		_ = s.SendResult(req.ID, map[string]any{"tools": s.tools})

	case "tools/call":
		var params CallToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			_ = s.SendError(req.ID, CodeInvalidParams, "Invalid params")
			return
		}

		var result CallToolResult
		if s.toolHandler != nil {
			result = s.toolHandler(ctx, params)
		} else {
			result = ErrorResult(fmt.Sprintf("No handler registered for tool: %s", params.Name))
		}
		_ = s.SendResult(req.ID, result)

	default:
		_ = s.SendError(req.ID, CodeMethodNotFound, fmt.Sprintf("Method not found: %s", req.Method))
	}
}

// SendResult serializes and writes a successful JSON-RPC response.
func (s *Server) SendResult(id, result any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = s.writer.Write(append(data, '\n'))
	return err
}

// SendError serializes and writes a JSON-RPC error response.
func (s *Server) SendError(id any, code int, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	msg := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = s.writer.Write(append(data, '\n'))
	return err
}
