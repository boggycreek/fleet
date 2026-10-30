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
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
	"github.com/boggycreek/fleet/backplane/test/harness"
)

type mockBPClient struct {
	sayFunc       func(ctx context.Context, content string, blobPath string) (*libbp.Message, error)
	tellFunc      func(ctx context.Context, recipient string, content string) (*libbp.Message, error)
	replyFunc     func(ctx context.Context, replyToMsgID string, recipient string, content string) (*libbp.Message, error)
	recvFunc      func(ctx context.Context, blockSeconds int) ([]*libbp.Message, error)
	peersFunc     func(ctx context.Context) ([]*libbp.Peer, error)
	setStatusFunc func(ctx context.Context, statusText string) error
	closeFunc     func() error
}

func (m *mockBPClient) Say(ctx context.Context, content, blobPath string) (*libbp.Message, error) {
	if m.sayFunc != nil {
		return m.sayFunc(ctx, content, blobPath)
	}
	return &libbp.Message{
		ID:        "1-0",
		Sender:    "test-agent",
		Content:   content,
		BlobPath:  blobPath,
		Timestamp: time.Now().UnixMilli(),
		Seq:       1,
		Citation:  "test-agent#1",
	}, nil
}

func (m *mockBPClient) Tell(ctx context.Context, recipient, content string) (*libbp.Message, error) {
	if m.tellFunc != nil {
		return m.tellFunc(ctx, recipient, content)
	}
	return &libbp.Message{
		ID:          "2-0",
		Sender:      "test-agent",
		Destination: recipient,
		Content:     content,
		Timestamp:   time.Now().UnixMilli(),
		Seq:         2,
		Citation:    "test-agent#2",
	}, nil
}

func (m *mockBPClient) Reply(ctx context.Context, replyToMsgID, recipient, content string) (*libbp.Message, error) {
	if m.replyFunc != nil {
		return m.replyFunc(ctx, replyToMsgID, recipient, content)
	}
	return &libbp.Message{
		ID:          "3-0",
		Sender:      "test-agent",
		Destination: recipient,
		Content:     content,
		ReplyTo:     replyToMsgID,
		Timestamp:   time.Now().UnixMilli(),
		Seq:         3,
		Citation:    "test-agent#3",
	}, nil
}

func (m *mockBPClient) Recv(ctx context.Context, blockSeconds int) ([]*libbp.Message, error) {
	if m.recvFunc != nil {
		return m.recvFunc(ctx, blockSeconds)
	}
	return []*libbp.Message{
		{
			ID:        "10-0",
			Sender:    "peer-agent",
			Content:   "Hello from peer",
			Timestamp: time.Now().UnixMilli(),
			Seq:       1,
			Citation:  "peer-agent#1",
		},
		{
			ID:        "11-0",
			Sender:    "peer-agent-2",
			Content:   "Second message",
			Timestamp: time.Now().UnixMilli(),
			Seq:       2,
			Citation:  "peer-agent-2#2",
		},
	}, nil
}

func (m *mockBPClient) Peers(ctx context.Context) ([]*libbp.Peer, error) {
	if m.peersFunc != nil {
		return m.peersFunc(ctx)
	}
	return []*libbp.Peer{
		{
			ID:        "opencode-1",
			Status:    "working: task-1",
			Role:      "coder",
			IsLiaison: false,
		},
		{
			ID:        "operator",
			Status:    "active",
			Role:      "human",
			IsLiaison: true,
		},
	}, nil
}

func (m *mockBPClient) SetStatus(ctx context.Context, statusText string) error {
	if m.setStatusFunc != nil {
		return m.setStatusFunc(ctx, statusText)
	}
	return nil
}

func (m *mockBPClient) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

func TestMCPServerLifecycle(t *testing.T) {
	client := &mockBPClient{}

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fleet_send_message","arguments":{"recipient":"opencode-1","message":"hello"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fleet_send_message","arguments":{"recipient":"opencode-1","message":"reply text","reply_to":"opencode-1#10"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"fleet_broadcast","arguments":{"message":"broadcast announcement","file_path":"/tmp/data.txt"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"fleet_read_inbox","arguments":{"block_seconds":0,"count":1}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"fleet_read_inbox","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"fleet_list_peers","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"fleet_set_status","arguments":{"status":"working: testing"}}}`,
		`{"jsonrpc":"2.0","id":10,"method":"non_existent_method"}`,
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"unknown_tool"}}`,
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"fleet_send_message","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"fleet_broadcast","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":14,"method":"tools/call","params":{"name":"fleet_set_status","arguments":{}}}`,
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
	if len(lines) < 10 {
		t.Fatalf("expected at least 10 responses, got %d: %s", len(lines), output)
	}

	// Verify initialize response
	var initResp JSONRPCMessage
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("failed unmarshaling init response: %v", err)
	}
	if initResp.ID != float64(1) {
		t.Errorf("expected ID 1, got %v", initResp.ID)
	}

	// Verify tools/list response
	var toolsResp JSONRPCMessage
	if err := json.Unmarshal([]byte(lines[1]), &toolsResp); err != nil {
		t.Fatalf("failed unmarshaling tools/list response: %v", err)
	}
	toolsMap, ok := toolsResp.Result.(map[string]any)
	if !ok || len(toolsMap["tools"].([]any)) != 5 {
		t.Errorf("expected 5 tools in tools/list, got: %+v", toolsMap)
	}
}

func TestMCPServerClientErrors(t *testing.T) {
	errClient := &mockBPClient{
		sayFunc: func(_ context.Context, _ string, _ string) (*libbp.Message, error) {
			return nil, errors.New("say failure")
		},
		tellFunc: func(_ context.Context, _ string, _ string) (*libbp.Message, error) {
			return nil, errors.New("tell failure")
		},
		replyFunc: func(_ context.Context, _ string, _ string, _ string) (*libbp.Message, error) {
			return nil, errors.New("reply failure")
		},
		recvFunc: func(_ context.Context, _ int) ([]*libbp.Message, error) {
			return nil, errors.New("recv failure")
		},
		peersFunc: func(_ context.Context) ([]*libbp.Peer, error) {
			return nil, errors.New("peers failure")
		},
		setStatusFunc: func(_ context.Context, _ string) error {
			return errors.New("status failure")
		},
	}

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"fleet_send_message","arguments":{"recipient":"agent","message":"m"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"fleet_send_message","arguments":{"recipient":"agent","message":"m","reply_to":"agent#1"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fleet_broadcast","arguments":{"message":"b"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fleet_read_inbox","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"fleet_list_peers","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"fleet_set_status","arguments":{"status":"idle"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":"invalid-params"}`,
	}

	input := strings.Join(requests, "\n") + "\n"
	inBuf := bytes.NewBufferString(input)
	outBuf := &bytes.Buffer{}

	server := NewMCPServer(errClient, inBuf, outBuf)
	_ = server.Serve(context.Background())

	output := outBuf.String()
	if !strings.Contains(output, "Error sending fleet message") {
		t.Errorf("expected send error in output, got: %s", output)
	}
	if !strings.Contains(output, "Error broadcasting fleet message") {
		t.Errorf("expected broadcast error in output, got: %s", output)
	}
	if !strings.Contains(output, "Error reading fleet inbox") {
		t.Errorf("expected read inbox error in output, got: %s", output)
	}
	if !strings.Contains(output, "Error listing fleet peers") {
		t.Errorf("expected list peers error in output, got: %s", output)
	}
	if !strings.Contains(output, "Error updating fleet status") {
		t.Errorf("expected update status error in output, got: %s", output)
	}
}

func TestMCPServerContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := &mockBPClient{}
	server := NewMCPServer(client, bytes.NewBuffer(nil), &bytes.Buffer{})
	err := server.Serve(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestRunFunctionDialError(t *testing.T) {
	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", "1") // Unreachable port

	err := Run(context.Background(), bytes.NewBuffer(nil), &bytes.Buffer{})
	if err == nil {
		t.Errorf("expected error connecting to unreachable backplane, got nil")
	}
}

func TestRunSuccess(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", fmt.Sprintf("%d", valkey.Port))
	t.Setenv("BP_AGENT", "agent-1")
	t.Setenv("BP_PASSWORD", valkey.Agent1Pass)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := Run(ctx, bytes.NewBuffer(nil), &bytes.Buffer{})
	if err != nil {
		t.Errorf("expected Run with empty input to return nil, got %v", err)
	}
}

func TestMainFunction(t *testing.T) {
	_ = t
	if os.Getenv("TEST_RUN_MAIN") == "1" {
		main()
		return
	}
	// #nosec G204 -- test runner executing itself
	cmd := exec.Command(os.Args[0], "-test.run=TestMainFunction")
	cmd.Env = append(os.Environ(), "TEST_RUN_MAIN=1", "BP_HOST=127.0.0.1", "BP_PORT=1")
	_ = cmd.Run()
}
