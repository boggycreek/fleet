// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package main implements the Episodic Memory & Reflection Model Context Protocol (MCP) server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/boggycreek/sandbox/mcp/pkg/memory"
	"github.com/boggycreek/sandbox/mcp/pkg/mcp"
)

// JSONRPCMessage aliases mcp.JSONRPCMessage for testing compatibility.
type JSONRPCMessage = mcp.JSONRPCMessage

type (
	inputSchema = mcp.InputSchema
	property    = mcp.Property
)

var memoryTools = []mcp.Tool{
	{
		Name:        "store_memory",
		Description: "Stores a memory item with classification pillar, tags, Tier 1 abstract, Tier 2 summary, and full body",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"pillar": {
					Type:        "string",
					Description: "Classification pillar: 'episodic', 'semantic', 'procedural', 'prospective', or 'evaluative'",
				},
				"title": {
					Type:        "string",
					Description: "Memory item title or primary topic heading",
				},
				"tier1_abstract": {
					Type:        "string",
					Description: "Concise 1-2 sentence high-level summary abstract",
				},
				"tier2_summary": {
					Type:        "string",
					Description: "Detailed section-level summary",
				},
				"body": {
					Type:        "string",
					Description: "Full markdown text body of the memory document",
				},
				"tags": {
					Type:        "array",
					Description: "List of string tags for categorization",
				},
				"id": {
					Type:        "string",
					Description: "Optional custom memory identifier (auto-generated if omitted)",
				},
				"timestamp": {
					Type:        "string",
					Description: "Optional RFC3339 timestamp (defaults to current time if omitted)",
				},
			},
			Required: []string{"pillar", "tier1_abstract", "tier2_summary", "body"},
		},
	},
	{
		Name:        "recall_memory",
		Description: "Queries long-term memory by text query, optional pillar filter, and maximum tier disclosure level",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"query": {
					Type:        "string",
					Description: "Search query text to match across abstracts, summaries, and document bodies",
				},
				"pillar": {
					Type:        "string",
					Description: "Optional pillar filter: 'episodic', 'semantic', 'procedural', 'prospective', or 'evaluative'",
				},
				"max_tier": {
					Type:        "integer",
					Description: "Maximum tier disclosure level (1: abstract only, 2: abstract + summary, 3: full document body; default 3)",
				},
				"limit": {
					Type:        "integer",
					Description: "Maximum number of memories to recall (default 10)",
				},
			},
			Required: []string{"query"},
		},
	},
	{
		Name:        "get_memory",
		Description: "Retrieves a specific memory document by id or file path at specified tier disclosure level",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"id": {
					Type:        "string",
					Description: "Memory unique identifier (e.g. mem-12345)",
				},
				"path": {
					Type:        "string",
					Description: "Memory document relative file path (e.g. episodic/2026-10-30-setup.md)",
				},
				"max_tier": {
					Type:        "integer",
					Description: "Maximum tier disclosure level (1: abstract, 2: summary, 3: full body; default 3)",
				},
			},
		},
	},
	{
		Name:        "list_memories",
		Description: "Lists available memory items with Tier 1 abstracts",
		InputSchema: inputSchema{
			Type: "object",
			Properties: map[string]property{
				"pillar": {
					Type:        "string",
					Description: "Optional pillar filter: 'episodic', 'semantic', 'procedural', 'prospective', or 'evaluative'",
				},
				"tag": {
					Type:        "string",
					Description: "Optional tag filter",
				},
				"limit": {
					Type:        "integer",
					Description: "Maximum items to return",
				},
			},
		},
	},
}

// MCPServer manages the STDIO JSON-RPC lifecycle for cognitive memory operations.
type MCPServer struct {
	*mcp.Server
	store *memory.Store
}

// NewMCPServer creates a new memory MCP server instance backed by the given memory store.
func NewMCPServer(store *memory.Store, r io.Reader, w io.Writer) *MCPServer {
	s := &MCPServer{
		store: store,
	}
	s.Server = mcp.NewServer("memory-mcp", "0.1.0", r, w, memoryTools, s.executeTool)
	return s
}

func (s *MCPServer) executeTool(ctx context.Context, params mcp.CallToolParams) mcp.CallToolResult {
	args := params.ParseArguments()

	switch params.Name {
	case "store_memory":
		pillar, _ := args["pillar"].(string)
		tier1, _ := args["tier1_abstract"].(string)
		tier2, _ := args["tier2_summary"].(string)
		body, _ := args["body"].(string)

		if pillar == "" {
			return mcp.ErrorResult("Missing required argument 'pillar'")
		}
		if tier1 == "" {
			return mcp.ErrorResult("Missing required argument 'tier1_abstract'")
		}
		if tier2 == "" {
			return mcp.ErrorResult("Missing required argument 'tier2_summary'")
		}
		if body == "" {
			return mcp.ErrorResult("Missing required argument 'body'")
		}

		title, _ := args["title"].(string)
		id, _ := args["id"].(string)
		timestamp, _ := args["timestamp"].(string)
		tags := parseTagsArg(args["tags"])

		item := &memory.MemoryItem{
			ID:            id,
			Timestamp:     timestamp,
			Pillar:        pillar,
			Title:         title,
			Tags:          tags,
			Tier1Abstract: tier1,
			Tier2Summary:  tier2,
			Body:          body,
		}

		saved, err := s.store.Save(item)
		if err != nil {
			return mcp.ErrorResult(fmt.Sprintf("Failed to store memory: %v", err))
		}

		data, _ := json.MarshalIndent(saved, "", "  ")
		return mcp.TextResult(string(data))

	case "recall_memory":
		query, _ := args["query"].(string)
		pillar, _ := args["pillar"].(string)
		maxTier := parseIntArg(args["max_tier"], 3)
		limit := parseIntArg(args["limit"], 10)

		results := s.store.Recall(query, pillar, maxTier, limit)
		data, _ := json.MarshalIndent(results, "", "  ")
		return mcp.TextResult(string(data))

	case "get_memory":
		id, _ := args["id"].(string)
		path, _ := args["path"].(string)
		maxTier := parseIntArg(args["max_tier"], 3)

		key := id
		if key == "" {
			key = path
		}
		if key == "" {
			return mcp.ErrorResult("At least one of 'id' or 'path' must be provided")
		}

		item, err := s.store.Get(key, maxTier)
		if err != nil {
			return mcp.ErrorResult(err.Error())
		}

		data, _ := json.MarshalIndent(item, "", "  ")
		return mcp.TextResult(string(data))

	case "list_memories":
		pillar, _ := args["pillar"].(string)
		tag, _ := args["tag"].(string)
		limit := parseIntArg(args["limit"], 0)

		items := s.store.List(pillar, tag, limit)
		data, _ := json.MarshalIndent(items, "", "  ")
		return mcp.TextResult(string(data))

	default:
		return mcp.ErrorResult(fmt.Sprintf("Unknown tool: %s", params.Name))
	}
}

func parseTagsArg(val any) []string {
	if val == nil {
		return nil
	}
	switch v := val.(type) {
	case []any:
		var tags []string
		for _, elem := range v {
			if s, ok := elem.(string); ok && strings.TrimSpace(s) != "" {
				tags = append(tags, strings.TrimSpace(s))
			}
		}
		return tags
	case []string:
		return v
	case string:
		parts := strings.Split(v, ",")
		var tags []string
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				tags = append(tags, s)
			}
		}
		return tags
	}
	return nil
}

func parseIntArg(val any, defaultVal int) int {
	if val == nil {
		return defaultVal
	}
	switch v := val.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return defaultVal
}

// Run executes the memory MCP server reading from r and writing to w.
func Run(ctx context.Context, r io.Reader, w io.Writer) error {
	rootDir := os.Getenv("MEMORY_DIR")
	store, err := memory.NewStore(memory.StoreConfig{RootDir: rootDir})
	if err != nil {
		return fmt.Errorf("failed to initialize memory store: %w", err)
	}

	server := NewMCPServer(store, r, w)
	return server.Serve(ctx)
}

func main() {
	if err := Run(context.Background(), os.Stdin, os.Stdout); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "memory-mcp server error: %v\n", err)
		os.Exit(1)
	}
}
