// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMCPTypesAndHelpers(t *testing.T) {
	// TextResult
	res := TextResult("hello world")
	if res.IsError {
		t.Errorf("expected IsError to be false, got true")
	}
	if len(res.Content) != 1 || res.Content[0].Text != "hello world" || res.Content[0].Type != "text" {
		t.Errorf("unexpected content in TextResult: %+v", res.Content)
	}

	// ErrorResult
	errRes := ErrorResult("something failed")
	if !errRes.IsError {
		t.Errorf("expected IsError to be true, got false")
	}
	if len(errRes.Content) != 1 || errRes.Content[0].Text != "something failed" || errRes.Content[0].Type != "text" {
		t.Errorf("unexpected content in ErrorResult: %+v", errRes.Content)
	}

	// ParseArguments
	pEmpty := CallToolParams{}
	argsEmpty := pEmpty.ParseArguments()
	if argsEmpty == nil || len(argsEmpty) != 0 {
		t.Errorf("expected empty non-nil map, got %+v", argsEmpty)
	}

	pValid := CallToolParams{
		Arguments: json.RawMessage(`{"key":"value","num":42}`),
	}
	argsValid := pValid.ParseArguments()
	if argsValid["key"] != "value" || argsValid["num"] != float64(42) {
		t.Errorf("unexpected parsed arguments: %+v", argsValid)
	}
}

func TestServerLifecycle(t *testing.T) {
	tools := []Tool{
		{
			Name:        "test_tool",
			Description: "A test tool",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"msg": {
						Type:        "string",
						Description: "Input message",
					},
				},
				Required: []string{"msg"},
			},
		},
	}

	handler := func(ctx context.Context, params CallToolParams) CallToolResult {
		if params.Name == "test_tool" {
			args := params.ParseArguments()
			if msg, ok := args["msg"].(string); ok && msg != "" {
				return TextResult("echo: " + msg)
			}
			return ErrorResult("missing required msg parameter")
		}
		return ErrorResult("unknown tool: " + params.Name)
	}

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"test_tool","arguments":{"msg":"ping"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"test_tool","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"unregistered_tool"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":"invalid-params-not-object"}`,
		`{"jsonrpc":"2.0","id":7,"method":"unknown_method"}`,
		`{broken json`,
		``,
		`   `,
	}

	inBuf := bytes.NewBufferString(strings.Join(requests, "\n") + "\n")
	outBuf := &bytes.Buffer{}

	server := NewServer("test-mcp", "1.0.0", inBuf, outBuf, tools, handler)
	if server.Name() != "test-mcp" {
		t.Errorf("expected name 'test-mcp', got %q", server.Name())
	}
	if server.Version() != "1.0.0" {
		t.Errorf("expected version '1.0.0', got %q", server.Version())
	}
	if len(server.Tools()) != 1 || server.Tools()[0].Name != "test_tool" {
		t.Errorf("unexpected tools: %+v", server.Tools())
	}

	err := server.Serve(context.Background())
	if err != nil {
		t.Fatalf("unexpected Serve error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
	if len(lines) != 8 {
		t.Fatalf("expected 8 response lines, got %d:\n%s", len(lines), outBuf.String())
	}

	// 1. initialize
	var initResp JSONRPCMessage
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("failed unmarshaling init response: %v", err)
	}
	if initResp.ID != float64(1) {
		t.Errorf("expected ID 1, got %v", initResp.ID)
	}
	initMap, ok := initResp.Result.(map[string]any)
	if !ok || initMap["protocolVersion"] != ProtocolVersion {
		t.Errorf("unexpected init result: %+v", initMap)
	}

	// 2. tools/list
	var toolsResp JSONRPCMessage
	if err := json.Unmarshal([]byte(lines[1]), &toolsResp); err != nil {
		t.Fatalf("failed unmarshaling tools response: %v", err)
	}
	toolsMap, ok := toolsResp.Result.(map[string]any)
	if !ok || len(toolsMap["tools"].([]any)) != 1 {
		t.Errorf("unexpected tools list result: %+v", toolsMap)
	}

	// 3. tools/call success
	var callResp JSONRPCMessage
	if err := json.Unmarshal([]byte(lines[2]), &callResp); err != nil {
		t.Fatalf("failed unmarshaling call response: %v", err)
	}
	callMap, ok := callResp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected call result: %+v", callResp.Result)
	}
	content := callMap["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "echo: ping" {
		t.Errorf("unexpected call content: %+v", content)
	}

	// 4. tools/call error from handler
	var errCallResp JSONRPCMessage
	if err := json.Unmarshal([]byte(lines[3]), &errCallResp); err != nil {
		t.Fatalf("failed unmarshaling call error response: %v", err)
	}
	errCallMap, ok := errCallResp.Result.(map[string]any)
	if !ok || errCallMap["isError"] != true {
		t.Errorf("expected isError true, got %+v", errCallMap)
	}

	// 5. tools/call unknown tool from handler
	var unkToolResp JSONRPCMessage
	if err := json.Unmarshal([]byte(lines[4]), &unkToolResp); err != nil {
		t.Fatalf("failed unmarshaling unknown tool response: %v", err)
	}

	// 6. tools/call invalid params
	var invParamResp JSONRPCMessage
	if err := json.Unmarshal([]byte(lines[5]), &invParamResp); err != nil {
		t.Fatalf("failed unmarshaling invalid params response: %v", err)
	}
	if invParamResp.Error == nil || invParamResp.Error.Code != CodeInvalidParams {
		t.Errorf("expected CodeInvalidParams, got %+v", invParamResp.Error)
	}

	// 7. unknown method
	var unkMethodResp JSONRPCMessage
	if err := json.Unmarshal([]byte(lines[6]), &unkMethodResp); err != nil {
		t.Fatalf("failed unmarshaling unknown method response: %v", err)
	}
	if unkMethodResp.Error == nil || unkMethodResp.Error.Code != CodeMethodNotFound {
		t.Errorf("expected CodeMethodNotFound, got %+v", unkMethodResp.Error)
	}

	// 8. parse error
	var parseErrResp JSONRPCMessage
	if err := json.Unmarshal([]byte(lines[7]), &parseErrResp); err != nil {
		t.Fatalf("failed unmarshaling parse error response: %v", err)
	}
	if parseErrResp.Error == nil || parseErrResp.Error.Code != CodeParseError {
		t.Errorf("expected CodeParseError, got %+v", parseErrResp.Error)
	}
}

func TestServerNilHandler(t *testing.T) {
	inBuf := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"any_tool"}}` + "\n")
	outBuf := &bytes.Buffer{}

	server := NewServer("nil-handler", "0.1", inBuf, outBuf, nil, nil)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("unexpected Serve error: %v", err)
	}

	var resp JSONRPCMessage
	if err := json.Unmarshal(outBuf.Bytes(), &resp); err != nil {
		t.Fatalf("failed unmarshaling response: %v", err)
	}
	resultMap, ok := resp.Result.(map[string]any)
	if !ok || resultMap["isError"] != true {
		t.Errorf("expected isError=true when nil handler is called: %+v", resp)
	}
}

func TestServerContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	inBuf := bytes.NewBufferString("")
	outBuf := &bytes.Buffer{}

	server := NewServer("test", "0.1", inBuf, outBuf, nil, nil)
	err := server.Serve(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

type errWriter struct{}

func (e *errWriter) Write(p []byte) (int, error) {
	return 0, fmt.Errorf("write error")
}

func TestServerSendErrors(t *testing.T) {
	server := NewServer("test", "0.1", bytes.NewBuffer(nil), &errWriter{}, nil, nil)

	if err := server.SendResult(1, "ok"); err == nil {
		t.Errorf("expected error from SendResult on failing writer")
	}

	if err := server.SendError(1, CodeInternalError, "fail"); err == nil {
		t.Errorf("expected error from SendError on failing writer")
	}

	// Marshaling failure on invalid channel type
	validWriter := &bytes.Buffer{}
	validServer := NewServer("test", "0.1", bytes.NewBuffer(nil), validWriter, nil, nil)
	badValue := make(chan int)
	if err := validServer.SendResult(1, badValue); err == nil {
		t.Errorf("expected JSON marshal error for unmarshallable type")
	}
}

type errReader struct{}

func (e *errReader) Read(p []byte) (int, error) {
	return 0, fmt.Errorf("simulated read error")
}

func TestServerReadError(t *testing.T) {
	server := NewServer("test", "0.1", &errReader{}, &bytes.Buffer{}, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := server.Serve(ctx)
	if err == nil || !strings.Contains(err.Error(), "simulated read error") {
		t.Errorf("expected simulated read error, got %v", err)
	}
}

func TestServerJSONRPCComplianceAndBounds(t *testing.T) {
	// 1. Check jsonrpc version validation
	invalidVerInput := bytes.NewBufferString(`{"jsonrpc":"1.0","id":1,"method":"tools/list"}` + "\n")
	outBuf := &bytes.Buffer{}
	server := NewServer("test", "0.1", invalidVerInput, outBuf, nil, nil)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("unexpected Serve error: %v", err)
	}
	var errResp JSONRPCMessage
	if err := json.Unmarshal(outBuf.Bytes(), &errResp); err != nil {
		t.Fatalf("failed unmarshaling error response: %v", err)
	}
	if errResp.Error == nil || errResp.Error.Code != CodeInvalidRequest {
		t.Errorf("expected CodeInvalidRequest for jsonrpc != '2.0', got %+v", errResp.Error)
	}

	// 2. Check null ID output formatting
	parseErrInput := bytes.NewBufferString(`{bad json` + "\n")
	outBuf.Reset()
	server = NewServer("test", "0.1", parseErrInput, outBuf, nil, nil)
	_ = server.Serve(context.Background())
	if !strings.Contains(outBuf.String(), `"id":null`) {
		t.Errorf("expected explicit '\"id\":null' in error response, got %s", outBuf.String())
	}

	// 3. Line length limit error
	largeLine := strings.Repeat("a", maxLineLength+10) + "\n"
	outBuf.Reset()
	server = NewServer("test", "0.1", bytes.NewBufferString(largeLine), outBuf, nil, nil)
	_ = server.Serve(context.Background())
	if !strings.Contains(outBuf.String(), "exceeds maximum limit") {
		t.Errorf("expected max limit error response, got %s", outBuf.String())
	}
}
