// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ChatMessage represents a single message in an OpenAI-compatible chat conversation.
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall represents a function call invocation requested by the model.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // always "function"
	Function ToolFunctionCall `json:"function"`
}

// ToolFunctionCall contains the function name and serialized JSON arguments.
type ToolFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatCompletionRequest encapsulates the payload sent to /v1/chat/completions.
type ChatCompletionRequest struct {
	Model           string           `json:"model"`
	Messages        []ChatMessage    `json:"messages"`
	Tools           []map[string]any `json:"tools,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	MaxTokens       *int             `json:"max_tokens,omitempty"`
	ReasoningBudget *float64         `json:"reasoning_budget,omitempty"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
}

// ChatCompletionResponse models the response from /v1/chat/completions.
type ChatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object,omitempty"`
	Created int64        `json:"created,omitempty"`
	Model   string       `json:"model,omitempty"`
	Choices []ChatChoice `json:"choices"`
	Usage   UsageInfo    `json:"usage,omitempty"`
	Error   *OpenAIError `json:"error,omitempty"`
}

// ChatChoice contains a generated completion alternative.
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// UsageInfo reports token consumption.
type UsageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OpenAIError represents an API error payload.
type OpenAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code,omitempty"`
}

// LLMClient executes HTTP requests against OpenAI-compatible inference endpoints.
type LLMClient struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewLLMClient constructs a new LLMClient.
func NewLLMClient(baseURL, apiKey, model string, timeout time.Duration) *LLMClient {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	trimmedURL := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(trimmedURL, "/chat/completions") {
		trimmedURL = trimmedURL + "/chat/completions"
	}

	return &LLMClient{
		baseURL: trimmedURL,
		apiKey:  apiKey,
		model:   model,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Model returns the configured default model name.
func (c *LLMClient) Model() string {
	return c.model
}

// BaseURL returns the configured endpoint URL.
func (c *LLMClient) BaseURL() string {
	return c.baseURL
}

// ChatCompletion sends a chat completion request to the LLM backend.
func (c *LLMClient) ChatCompletion(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if req.Model == "" {
		req.Model = c.model
	}
	if req.Model == "" {
		return nil, errors.New("model name cannot be empty")
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal completion request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp ChatCompletionResponse
		if jsonErr := json.Unmarshal(bodyBytes, &errResp); jsonErr == nil && errResp.Error != nil && errResp.Error.Message != "" {
			return nil, fmt.Errorf("api error (status %d): %s", resp.StatusCode, errResp.Error.Message)
		}
		return nil, fmt.Errorf("api error (status %d): %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var completionResp ChatCompletionResponse
	if err := json.Unmarshal(bodyBytes, &completionResp); err != nil {
		return nil, fmt.Errorf("failed to decode completion response: %w", err)
	}

	if len(completionResp.Choices) == 0 {
		return nil, errors.New("empty choices received from llm endpoint")
	}

	return &completionResp, nil
}
