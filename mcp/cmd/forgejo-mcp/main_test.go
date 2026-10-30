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
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/fleet/cli/pkg/forgejo"
)

type mockForgejoClient struct {
	listIssuesFunc        func(ctx context.Context, owner, repo, state string, page, limit int) ([]*forgejo.Issue, error)
	createIssueFunc       func(ctx context.Context, owner, repo, title, body string, labels, assignees []string) (*forgejo.Issue, error)
	createPullRequestFunc func(ctx context.Context, owner, repo, title, body, head, base string) (*forgejo.PullRequest, error)
	getPullRequestFunc    func(ctx context.Context, owner, repo string, index int64) (*forgejo.PullRequest, error)
	reviewPullRequestFunc func(ctx context.Context, owner, repo string, index int64, event, body string) (*forgejo.PullReview, error)
	getFileFunc           func(ctx context.Context, owner, repo, filePath, ref string) (*forgejo.FileContent, error)
}

type mockGiteaClient = mockForgejoClient

func (m *mockGiteaClient) ListIssues(ctx context.Context, owner, repo, state string, page, limit int) ([]*forgejo.Issue, error) {
	if m.listIssuesFunc != nil {
		return m.listIssuesFunc(ctx, owner, repo, state, page, limit)
	}
	return []*forgejo.Issue{
		{
			ID:        1,
			Index:     101,
			Title:     "Test Task 1",
			Body:      "Task description",
			State:     "open",
			CreatedAt: time.Now(),
		},
	}, nil
}

func (m *mockGiteaClient) CreateIssue(ctx context.Context, owner, repo, title, body string, labels, assignees []string) (*forgejo.Issue, error) {
	if m.createIssueFunc != nil {
		return m.createIssueFunc(ctx, owner, repo, title, body, labels, assignees)
	}
	return &forgejo.Issue{
		ID:        2,
		Index:     102,
		Title:     title,
		Body:      body,
		State:     "open",
		CreatedAt: time.Now(),
	}, nil
}

func (m *mockGiteaClient) CreatePullRequest(ctx context.Context, owner, repo, title, body, head, base string) (*forgejo.PullRequest, error) {
	if m.createPullRequestFunc != nil {
		return m.createPullRequestFunc(ctx, owner, repo, title, body, head, base)
	}
	return &forgejo.PullRequest{
		ID:    10,
		Index: 1,
		Title: title,
		Body:  body,
		State: "open",
		Head:  &forgejo.PRBranch{Ref: head},
		Base:  &forgejo.PRBranch{Ref: base},
	}, nil
}

func (m *mockGiteaClient) GetPullRequest(ctx context.Context, owner, repo string, index int64) (*forgejo.PullRequest, error) {
	if m.getPullRequestFunc != nil {
		return m.getPullRequestFunc(ctx, owner, repo, index)
	}
	return &forgejo.PullRequest{
		ID:    10,
		Index: index,
		Title: "Existing PR",
		State: "open",
	}, nil
}

func (m *mockGiteaClient) ReviewPullRequest(ctx context.Context, owner, repo string, index int64, event, body string) (*forgejo.PullReview, error) {
	if m.reviewPullRequestFunc != nil {
		return m.reviewPullRequestFunc(ctx, owner, repo, index, event, body)
	}
	return &forgejo.PullReview{
		ID:        5,
		State:     event,
		Body:      body,
		Submitted: time.Now(),
	}, nil
}

func (m *mockGiteaClient) GetFile(ctx context.Context, owner, repo, filePath, ref string) (*forgejo.FileContent, error) {
	if m.getFileFunc != nil {
		return m.getFileFunc(ctx, owner, repo, filePath, ref)
	}
	return &forgejo.FileContent{
		Name:    filePath,
		Path:    filePath,
		Content: "package main\n\nfunc main() {}\n",
	}, nil
}

func TestMCPServerLifecycle(t *testing.T) {
	client := &mockGiteaClient{}

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"forge_list_tasks","arguments":{"owner":"fleet","repo":"tasks","state":"open","page":1,"limit":10}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"forge_create_task","arguments":{"owner":"fleet","repo":"tasks","title":"New task","body":"details","labels":["bug"],"assignees":["agent-1"]}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"forge_create_pull_request","arguments":{"owner":"fleet","repo":"tools","title":"Add tool","head":"feat/x","base":"main","body":"pr body"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"forge_review_pull_request","arguments":{"owner":"fleet","repo":"tools","index":1,"event":"APPROVE","body":"looks good"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"forge_read_file","arguments":{"owner":"fleet","repo":"tools","file_path":"README.md","ref":"main"}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"non_existent_method"}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"unknown_tool"}}`,
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"forge_create_task","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"forge_create_pull_request","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"forge_review_pull_request","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"forge_read_file","arguments":{}}}`,
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
	errClient := &mockGiteaClient{
		listIssuesFunc: func(_ context.Context, _, _, _ string, _, _ int) ([]*forgejo.Issue, error) {
			return nil, errors.New("list failure")
		},
		createIssueFunc: func(_ context.Context, _, _, _, _ string, _, _ []string) (*forgejo.Issue, error) {
			return nil, errors.New("create issue failure")
		},
		createPullRequestFunc: func(_ context.Context, _, _, _, _, _, _ string) (*forgejo.PullRequest, error) {
			return nil, errors.New("create pr failure")
		},
		reviewPullRequestFunc: func(_ context.Context, _ string, _ string, _ int64, _, _ string) (*forgejo.PullReview, error) {
			return nil, errors.New("review failure")
		},
		getFileFunc: func(_ context.Context, _, _, _, _ string) (*forgejo.FileContent, error) {
			return nil, errors.New("read file failure")
		},
	}

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"forge_list_tasks","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"forge_create_task","arguments":{"title":"task"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"forge_create_pull_request","arguments":{"repo":"tools","title":"pr","head":"feat/1"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"forge_review_pull_request","arguments":{"repo":"tools","index":1,"event":"APPROVE"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"forge_read_file","arguments":{"repo":"tools","file_path":"README.md"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":"invalid-params"}`,
	}

	input := strings.Join(requests, "\n") + "\n"
	inBuf := bytes.NewBufferString(input)
	outBuf := &bytes.Buffer{}

	server := NewMCPServer(errClient, inBuf, outBuf)
	_ = server.Serve(context.Background())

	output := outBuf.String()
	if !strings.Contains(output, "Error listing forge tasks") {
		t.Errorf("expected list error in output, got: %s", output)
	}
	if !strings.Contains(output, "Error creating forge task") {
		t.Errorf("expected create task error in output, got: %s", output)
	}
	if !strings.Contains(output, "Error creating pull request") {
		t.Errorf("expected create pr error in output, got: %s", output)
	}
	if !strings.Contains(output, "Error reviewing pull request") {
		t.Errorf("expected review error in output, got: %s", output)
	}
	if !strings.Contains(output, "Error reading file") {
		t.Errorf("expected read file error in output, got: %s", output)
	}
}

func TestMCPServerContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client := &mockGiteaClient{}
	server := NewMCPServer(client, bytes.NewBuffer(nil), &bytes.Buffer{})
	err := server.Serve(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestRunFunction(t *testing.T) {
	inBuf := bytes.NewBufferString("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n")
	outBuf := &bytes.Buffer{}

	err := Run(context.Background(), inBuf, outBuf)
	if err != nil {
		t.Fatalf("unexpected error running Run: %v", err)
	}
	if !strings.Contains(outBuf.String(), "forgejo-mcp") {
		t.Errorf("expected forgejo-mcp in output, got: %s", outBuf.String())
	}
}
