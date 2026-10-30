// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package main implements the Forgejo Fleet Forge Model Context Protocol (MCP) server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/boggycreek/fleet/cli/pkg/forgejo"
	"github.com/boggycreek/fleet/mcp/pkg/mcp"
)

// JSONRPCMessage aliases mcp.JSONRPCMessage for testing compatibility.
type JSONRPCMessage = mcp.JSONRPCMessage

type (
	inputSchema = mcp.InputSchema
	property    = mcp.Property
)

// ForgejoClient defines the interface required by the Forgejo Forge MCP server
type ForgejoClient interface {
	ListIssues(ctx context.Context, owner, repo, state string, page, limit int) ([]*forgejo.Issue, error)
	CreateIssue(ctx context.Context, owner, repo, title, body string, labels, assignees []string) (*forgejo.Issue, error)
	CreatePullRequest(ctx context.Context, owner, repo, title, body, head, base string) (*forgejo.PullRequest, error)
	GetPullRequest(ctx context.Context, owner, repo string, index int64) (*forgejo.PullRequest, error)
	ReviewPullRequest(ctx context.Context, owner, repo string, index int64, event, body string) (*forgejo.PullReview, error)
	GetFile(ctx context.Context, owner, repo, filePath, ref string) (*forgejo.FileContent, error)
}

// GiteaClient is an alias for ForgejoClient for backward compatibility
type GiteaClient = ForgejoClient

var forgejoTools = []mcp.Tool{
	{
		Name:        "forge_list_tasks",
		Description: "List tasks, issues, and backlog items from a Forgejo fleet forge repository",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"owner": {
					Type:        "string",
					Description: "Repository owner or organization (default: fleet)",
				},
				"repo": {
					Type:        "string",
					Description: "Repository name (default: tasks)",
				},
				"state": {
					Type:        "string",
					Description: "Issue state filter: 'open', 'closed', or 'all' (default: open)",
				},
				"page": {
					Type:        "integer",
					Description: "Page number (default: 1)",
				},
				"limit": {
					Type:        "integer",
					Description: "Page size (default: 50)",
				},
			},
		},
	},
	{
		Name:        "forge_create_task",
		Description: "Create a new task, backlog item, or bug issue in a Gitea fleet repository",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"owner": {
					Type:        "string",
					Description: "Repository owner or organization (default: fleet)",
				},
				"repo": {
					Type:        "string",
					Description: "Repository name (default: tasks)",
				},
				"title": {
					Type:        "string",
					Description: "Task title or summary",
				},
				"body": {
					Type:        "string",
					Description: "Detailed markdown description of the task requirements or issue details",
				},
				"labels": {
					Type:        "array",
					Description: "Array of label names to assign",
				},
				"assignees": {
					Type:        "array",
					Description: "Array of agent usernames to assign to this task",
				},
			},
			Required: []string{"title"},
		},
	},
	{
		Name:        "forge_create_pull_request",
		Description: "Create a new pull request in a Gitea repository to submit code changes for review",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"owner": {
					Type:        "string",
					Description: "Repository owner or organization (default: fleet)",
				},
				"repo": {
					Type:        "string",
					Description: "Repository name",
				},
				"title": {
					Type:        "string",
					Description: "Pull request title",
				},
				"body": {
					Type:        "string",
					Description: "Detailed description of changes, rationale, and testing performed",
				},
				"head": {
					Type:        "string",
					Description: "Name of the branch containing the changes (e.g. feat/my-feature)",
				},
				"base": {
					Type:        "string",
					Description: "Target base branch to merge into (default: main)",
				},
			},
			Required: []string{"repo", "title", "head"},
		},
	},
	{
		Name:        "forge_review_pull_request",
		Description: "Submit a peer review with approval, change requests, or comments on a pull request",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"owner": {
					Type:        "string",
					Description: "Repository owner or organization (default: fleet)",
				},
				"repo": {
					Type:        "string",
					Description: "Repository name",
				},
				"index": {
					Type:        "integer",
					Description: "Pull request number / index",
				},
				"event": {
					Type:        "string",
					Description: "Review action: 'APPROVE', 'REQUEST_CHANGES', or 'COMMENT' (default: COMMENT)",
				},
				"body": {
					Type:        "string",
					Description: "Review feedback comments or critique",
				},
			},
			Required: []string{"repo", "index"},
		},
	},
	{
		Name:        "forge_read_file",
		Description: "Read raw or decoded source file contents from a repository at a specific branch or commit",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"owner": {
					Type:        "string",
					Description: "Repository owner or organization (default: fleet)",
				},
				"repo": {
					Type:        "string",
					Description: "Repository name",
				},
				"file_path": {
					Type:        "string",
					Description: "Relative file path inside the repository",
				},
				"ref": {
					Type:        "string",
					Description: "Branch name, tag, or commit SHA (default: main)",
				},
			},
			Required: []string{"repo", "file_path"},
		},
	},
}

// MCPServer manages the STDIO JSON-RPC lifecycle for Forgejo Fleet Forge integration
type MCPServer struct {
	*mcp.Server
	forgejoClient ForgejoClient
}

// giteaTools aliases forgejoTools for backward compatibility
var giteaTools = forgejoTools

// NewMCPServer creates a new Forgejo Forge MCP server instance
func NewMCPServer(client ForgejoClient, r io.Reader, w io.Writer) *MCPServer {
	s := &MCPServer{
		forgejoClient: client,
	}
	s.Server = mcp.NewServer("forgejo-mcp", "0.1.0", r, w, forgejoTools, s.executeTool)
	return s
}

func (s *MCPServer) executeTool(ctx context.Context, params mcp.CallToolParams) mcp.CallToolResult {
	args := params.ParseArguments()

	owner, _ := args["owner"].(string)
	if owner == "" {
		owner = "fleet"
	}
	repo, _ := args["repo"].(string)

	switch params.Name {
	case "forge_list_tasks":
		if repo == "" {
			repo = "tasks"
		}
		state, _ := args["state"].(string)
		var page, limit int
		if pVal, ok := args["page"].(float64); ok {
			page = int(pVal)
		}
		if lVal, ok := args["limit"].(float64); ok {
			limit = int(lVal)
		}

		issues, err := s.forgejoClient.ListIssues(ctx, owner, repo, state, page, limit)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Error listing forge tasks: %v", err))
		}

		data, _ := json.MarshalIndent(issues, "", "  ")
		return mcp.TextResult(string(data))

	case "forge_create_task":
		if repo == "" {
			repo = "tasks"
		}
		title, _ := args["title"].(string)
		body, _ := args["body"].(string)
		if strings.TrimSpace(title) == "" {
			return mcp.ErrorResult("Missing required argument 'title'")
		}

		var labels []string
		if rawLabels, ok := args["labels"].([]any); ok {
			labels = make([]string, 0, len(rawLabels))
			for _, l := range rawLabels {
				if ls, ok := l.(string); ok && ls != "" {
					labels = append(labels, ls)
				}
			}
		}

		var assignees []string
		if rawAssignees, ok := args["assignees"].([]any); ok {
			assignees = make([]string, 0, len(rawAssignees))
			for _, a := range rawAssignees {
				if as, ok := a.(string); ok && as != "" {
					assignees = append(assignees, as)
				}
			}
		}

		issue, err := s.forgejoClient.CreateIssue(ctx, owner, repo, title, body, labels, assignees)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Error creating forge task: %v", err))
		}

		data, _ := json.MarshalIndent(issue, "", "  ")
		return mcp.TextResult(string(data))

	case "forge_create_pull_request":
		title, _ := args["title"].(string)
		head, _ := args["head"].(string)
		base, _ := args["base"].(string)
		body, _ := args["body"].(string)

		if repo == "" || strings.TrimSpace(title) == "" || strings.TrimSpace(head) == "" {
			return mcp.ErrorResult("Missing required arguments ('repo', 'title', 'head')")
		}

		pr, err := s.forgejoClient.CreatePullRequest(ctx, owner, repo, title, body, head, base)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Error creating pull request: %v", err))
		}

		data, _ := json.MarshalIndent(pr, "", "  ")
		return mcp.TextResult(string(data))

	case "forge_review_pull_request":
		var index int64
		if idxVal, ok := args["index"].(float64); ok {
			index = int64(idxVal)
		}
		if repo == "" || index <= 0 {
			return mcp.ErrorResult("Missing required arguments ('repo', 'index')")
		}

		event, _ := args["event"].(string)
		body, _ := args["body"].(string)

		review, err := s.forgejoClient.ReviewPullRequest(ctx, owner, repo, index, event, body)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Error reviewing pull request #%d: %v", index, err))
		}

		data, _ := json.MarshalIndent(review, "", "  ")
		return mcp.TextResult(string(data))

	case "forge_read_file":
		filePath, _ := args["file_path"].(string)
		ref, _ := args["ref"].(string)

		if repo == "" || filePath == "" {
			return mcp.ErrorResult("Missing required arguments ('repo', 'file_path')")
		}

		file, err := s.forgejoClient.GetFile(ctx, owner, repo, filePath, ref)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Error reading file %s: %v", filePath, err))
		}

		data, _ := json.MarshalIndent(file, "", "  ")
		return mcp.TextResult(string(data))

	default:
		return mcp.ErrorResult(fmt.Sprintf("Unknown tool: %s", params.Name))
	}
}

// Run executes the MCP server reading from r and writing to w
func Run(ctx context.Context, r io.Reader, w io.Writer) error {
	baseURL := os.Getenv("FORGEJO_URL")
	if baseURL == "" {
		baseURL = os.Getenv("GITEA_URL")
	}
	if baseURL == "" {
		baseURL = os.Getenv("FORGEJO_HOST_URL")
	}
	if baseURL == "" {
		baseURL = os.Getenv("GITEA_HOST_URL")
	}
	if baseURL == "" {
		baseURL = "http://forgejo:3000"
	}

	token := os.Getenv("FORGEJO_TOKEN")
	if token == "" {
		token = os.Getenv("GITEA_TOKEN")
	}
	adminUser := os.Getenv("FORGEJO_ADMIN_USER")
	if adminUser == "" {
		adminUser = os.Getenv("GITEA_ADMIN_USER")
	}
	if adminUser == "" {
		adminUser = "forgejoadmin"
	}
	adminPass := os.Getenv("FORGEJO_ADMIN_PASSWORD")
	if adminPass == "" {
		adminPass = os.Getenv("GITEA_ADMIN_PASSWORD")
	}
	if adminPass == "" {
		adminPass = os.Getenv("ADMIN_BACKPLANE_PASSWORD")
	}

	client := forgejo.NewClient(forgejo.ClientConfig{
		BaseURL:   baseURL,
		Token:     token,
		AdminUser: adminUser,
		AdminPass: adminPass,
	})

	server := NewMCPServer(client, r, w)
	return server.Serve(ctx)
}

func main() {
	if err := Run(context.Background(), os.Stdin, os.Stdout); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "forgejo-mcp server error: %v\n", err)
		os.Exit(1)
	}
}
