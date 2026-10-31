// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/boggycreek/fleet/agent/pkg/runtime"
	"github.com/boggycreek/fleet/agent/pkg/tools"
	"github.com/boggycreek/fleet/backplane/pkg/libbp"
)

// --- LLMClient Tests ---

func TestLLMClientSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Errorf("unexpected authorization header: %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content-type: %s", r.Header.Get("Content-Type"))
		}

		var req ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		resp := ChatCompletionResponse{
			ID:    "chatcmpl-test",
			Model: req.Model,
			Choices: []ChatChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Hello from mock model!",
					},
					FinishReason: "stop",
				},
			},
			Usage: UsageInfo{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client := NewLLMClient(ts.URL, "test-api-key", "test-model-4b", 5*time.Second)
	if client.Model() != "test-model-4b" {
		t.Errorf("unexpected model: %s", client.Model())
	}
	if !strings.HasSuffix(client.BaseURL(), "/chat/completions") {
		t.Errorf("expected URL to end with /chat/completions: %s", client.BaseURL())
	}

	ctx := context.Background()
	req := ChatCompletionRequest{
		Messages: []ChatMessage{
			{Role: "user", Content: "Hello"},
		},
	}
	resp, err := client.ChatCompletion(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "Hello from mock model!" {
		t.Fatalf("unexpected response choices: %+v", resp.Choices)
	}
}

func TestLLMClientErrors(t *testing.T) {
	ctx := context.Background()

	// 1. Empty model error
	emptyClient := NewLLMClient("http://localhost", "", "", 0)
	_, err := emptyClient.ChatCompletion(ctx, ChatCompletionRequest{})
	if err == nil || !strings.Contains(err.Error(), "model name cannot be empty") {
		t.Errorf("expected empty model error, got: %v", err)
	}

	// 2. HTTP 400 with OpenAI error JSON
	ts400 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(ChatCompletionResponse{
			Error: &OpenAIError{
				Message: "invalid parameter temperature",
				Type:    "invalid_request_error",
			},
		})
	}))
	defer ts400.Close()

	client400 := NewLLMClient(ts400.URL, "", "test-model", 2*time.Second)
	_, err = client400.ChatCompletion(ctx, ChatCompletionRequest{Model: "test-model"})
	if err == nil || !strings.Contains(err.Error(), "invalid parameter temperature") {
		t.Errorf("expected 400 error message, got: %v", err)
	}

	// 3. HTTP 500 plain text
	ts500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal crash", http.StatusInternalServerError)
	}))
	defer ts500.Close()

	client500 := NewLLMClient(ts500.URL, "", "test-model", 2*time.Second)
	_, err = client500.ChatCompletion(ctx, ChatCompletionRequest{Model: "test-model"})
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Errorf("expected 500 status error, got: %v", err)
	}

	// 4. Invalid JSON body
	tsBadJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not valid json"))
	}))
	defer tsBadJSON.Close()

	clientBad := NewLLMClient(tsBadJSON.URL, "", "test-model", 2*time.Second)
	_, err = clientBad.ChatCompletion(ctx, ChatCompletionRequest{Model: "test-model"})
	if err == nil || !strings.Contains(err.Error(), "failed to decode completion response") {
		t.Errorf("expected decode error, got: %v", err)
	}

	// 5. Empty choices in 200 OK response
	tsEmptyChoices := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ChatCompletionResponse{
			Choices: []ChatChoice{},
		})
	}))
	defer tsEmptyChoices.Close()

	clientEmpty := NewLLMClient(tsEmptyChoices.URL, "", "test-model", 2*time.Second)
	_, err = clientEmpty.ChatCompletion(ctx, ChatCompletionRequest{Model: "test-model"})
	if err == nil || !strings.Contains(err.Error(), "empty choices received") {
		t.Errorf("expected empty choices error, got: %v", err)
	}
}

// --- Prompt & Context Tests ---

func TestPromptAssemblyAndFormatting(t *testing.T) {
	prompt := BuildSystemPrompt("agent-01", "Software Engineer", "/home/agent/workspace", "Focus on clean code.")
	if !strings.Contains(prompt, "You are agent-01") {
		t.Errorf("missing agent id in prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "Software Engineer") {
		t.Errorf("missing role in prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "Focus on clean code.") {
		t.Errorf("missing custom instructions: %s", prompt)
	}

	// Test event formatting
	p0 := FormatEventMessage(runtime.Event{
		Priority: runtime.P0_Control,
		Source:   "operator",
		Payload:  "Emergency stop",
	})
	if !strings.Contains(p0.Content, "[EMERGENCY CONTROL DIRECTIVE") {
		t.Errorf("unexpected p0 formatting: %s", p0.Content)
	}

	p1 := FormatEventMessage(runtime.Event{
		Priority: runtime.P1_HighPriority,
		Source:   "user",
		Payload:  "Refactor module X",
	})
	if !strings.Contains(p1.Content, "[URGENT DIRECTIVE") {
		t.Errorf("unexpected p1 formatting: %s", p1.Content)
	}

	p2 := FormatEventMessage(runtime.Event{
		Priority: runtime.P2_StandardAsync,
		Source:   "peer-02",
		Payload:  runtime.BackplaneEnvelope{Sender: "peer-02", Payload: "PR is ready"},
	})
	if !strings.Contains(p2.Content, "[NOTIFICATION") || !strings.Contains(p2.Content, "PR is ready") {
		t.Errorf("unexpected p2 formatting: %s", p2.Content)
	}

	pOther := FormatEventMessage(runtime.Event{
		Priority: runtime.PriorityLevel(99),
		Source:   "telemetry",
		Payload:  map[string]string{"ping": "pong"},
	})
	if !strings.Contains(pOther.Content, "[MESSAGE from telemetry]") {
		t.Errorf("unexpected fallback formatting: %s", pOther.Content)
	}

	emptyPayload := extractPayloadString(nil)
	if emptyPayload != "" {
		t.Errorf("expected empty string for nil payload, got %q", emptyPayload)
	}

	envelopePtr := extractPayloadString(&runtime.BackplaneEnvelope{Sender: "bob", Payload: "hi"})
	if !strings.Contains(envelopePtr, "From: bob") {
		t.Errorf("unexpected envelope pointer string: %s", envelopePtr)
	}

	var nilEnv *runtime.BackplaneEnvelope
	if extractPayloadString(nilEnv) != "" {
		t.Errorf("expected empty string for nil envelope pointer")
	}

	msgVal := extractPayloadString(libbp.Message{Sender: "alice", Content: "hello world"})
	if msgVal != "From: alice | Message: hello world" {
		t.Errorf("unexpected Message value string: %s", msgVal)
	}

	msgPtr := extractPayloadString(&libbp.Message{Sender: "alice", Content: "hello world"})
	if msgPtr != "From: alice | Message: hello world" {
		t.Errorf("unexpected Message pointer string: %s", msgPtr)
	}

	var nilMsg *libbp.Message
	if extractPayloadString(nilMsg) != "" {
		t.Errorf("expected empty string for nil Message pointer")
	}
}

func TestTruncateAndCompactHistory(t *testing.T) {
	// 1. TruncateToolOutput
	shortText := "short output"
	if TruncateToolOutput(shortText, 50) != shortText {
		t.Errorf("expected short text untouched")
	}
	longText := strings.Repeat("A", 100)
	truncated := TruncateToolOutput(longText, 20)
	if !strings.Contains(truncated, "[content truncated to fit context budget]") {
		t.Errorf("expected truncation marker in %s", truncated)
	}

	// 2. CompactHistory within limit
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
	}
	compacted := CompactHistory(msgs, 5)
	if len(compacted) != 3 {
		t.Errorf("expected all 3 messages preserved, got %d", len(compacted))
	}

	// 3. CompactHistory exceeding limit: preserves system prompt at index 0
	manyMsgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "tool", Content: "t2"},
		{Role: "user", Content: "u3"},
	}
	compactedPruned := CompactHistory(manyMsgs, 4)
	if compactedPruned[0].Role != "system" {
		t.Errorf("expected system prompt at index 0, got %s", compactedPruned[0].Role)
	}
	// Tool message pairing guard check: ensure we didn't start with orphan tool message
	for i, m := range compactedPruned {
		if m.Role == "tool" && i == 1 {
			t.Errorf("orphan tool message retained at start of history")
		}
	}

	// 4. CompactHistory without system prompt
	noSys := []ChatMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
	}
	cNoSys := CompactHistory(noSys, 2)
	if len(cNoSys) != 2 || cNoSys[0].Content != "a1" {
		t.Errorf("unexpected pruning without system prompt: %+v", cNoSys)
	}
}

// --- REPLEngine Tests ---

func TestREPLEngineConfiguration(t *testing.T) {
	llm := NewLLMClient("http://mock", "key", "model", 0)
	reg := tools.NewRegistry()
	eng := NewREPLEngine(llm, reg, "/tmp/ws", "coder")

	if eng.ID() != "repl-main" {
		t.Errorf("unexpected ID: %s", eng.ID())
	}

	eng.SetPollInterval(10 * time.Millisecond)
	eng.SetMaxTurns(50)
	eng.SetMaxToolIterations(10)
	eng.SetCustomInstructions("custom rule")

	hist := []ChatMessage{{Role: "user", Content: "hi"}}
	eng.SetHistory(hist)
	got := eng.GetHistory()
	if len(got) != 1 || got[0].Content != "hi" {
		t.Errorf("unexpected history: %+v", got)
	}

	// Start validation with nil bus or state
	if err := eng.Start(context.Background(), nil, nil); err == nil {
		t.Error("expected error with nil bus/state")
	}
}

func TestREPLEngineSimpleTurn(t *testing.T) {
	var requestCount int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		resp := ChatCompletionResponse{
			ID:    "resp-1",
			Model: "test-model",
			Choices: []ChatChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Task completed successfully.",
					},
					FinishReason: "stop",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	llm := NewLLMClient(ts.URL, "key", "test-model", 5*time.Second)
	reg := tools.NewRegistry()
	eng := NewREPLEngine(llm, reg, tmpDir, "coder")
	eng.SetPollInterval(10 * time.Millisecond)

	bus := runtime.NewEventBus()
	state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-01")

	// Append a user task
	eng.AppendMessage(ChatMessage{Role: "user", Content: "Do something."})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- eng.Start(ctx, bus, state)
	}()

	// Wait until turn finishes and state is idle
	var finalState runtime.AgentState
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		finalState = state.Read()
		if finalState.Status == "idle" && atomic.LoadInt32(&requestCount) > 0 {
			break
		}
	}

	cancel()
	_ = <-errChan

	if finalState.Status != "idle" {
		t.Errorf("expected final status idle, got %s", finalState.Status)
	}

	history := eng.GetHistory()
	if len(history) < 2 {
		t.Fatalf("expected at least system, user, assistant messages, got %d", len(history))
	}
	lastMsg := history[len(history)-1]
	if lastMsg.Role != "assistant" || lastMsg.Content != "Task completed successfully." {
		t.Errorf("unexpected last message: %+v", lastMsg)
	}
}

func TestREPLEngineToolExecution(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "sample.txt")
	_ = os.WriteFile(testFile, []byte("Content of sample file"), 0644)

	var callStep int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step := atomic.AddInt32(&callStep, 1)
		var resp ChatCompletionResponse

		if step == 1 {
			// First call: request read_file tool call
			resp = ChatCompletionResponse{
				ID:    "resp-call-tool",
				Model: "test-model",
				Choices: []ChatChoice{
					{
						Index: 0,
						Message: ChatMessage{
							Role: "assistant",
							ToolCalls: []ToolCall{
								{
									ID:   "call_123",
									Type: "function",
									Function: ToolFunctionCall{
										Name:      "read_file",
										Arguments: `{"path": "sample.txt"}`,
									},
								},
							},
						},
						FinishReason: "tool_calls",
					},
				},
			}
		} else {
			// Second call: return final completion after seeing tool output
			resp = ChatCompletionResponse{
				ID:    "resp-final",
				Model: "test-model",
				Choices: []ChatChoice{
					{
						Index: 0,
						Message: ChatMessage{
							Role:    "assistant",
							Content: "File read successfully.",
						},
						FinishReason: "stop",
					},
				},
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	llm := NewLLMClient(ts.URL, "key", "test-model", 5*time.Second)
	reg := tools.NewRegistry()
	_ = tools.RegisterBuiltinTools(reg, tmpDir)

	eng := NewREPLEngine(llm, reg, tmpDir, "coder")
	eng.SetPollInterval(10 * time.Millisecond)

	bus := runtime.NewEventBus()
	state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-01")

	eng.AppendMessage(ChatMessage{Role: "user", Content: "Read sample.txt please."})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- eng.Start(ctx, bus, state)
	}()

	// Wait for tool execution to finish
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if atomic.LoadInt32(&callStep) >= 2 && state.Read().Status == "idle" {
			break
		}
	}

	cancel()
	_ = <-errChan

	history := eng.GetHistory()
	// History should contain: system, user, assistant(tool_call), tool(result), assistant(final)
	hasToolResult := false
	for _, m := range history {
		if m.Role == "tool" && strings.Contains(m.Content, "Content of sample file") {
			hasToolResult = true
			break
		}
	}
	if !hasToolResult {
		t.Errorf("tool output not found in history: %+v", history)
	}
}

func TestREPLEngineEventInteractions(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatCompletionResponse{
			ID:    "resp-evt",
			Model: "test-model",
			Choices: []ChatChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Processed directive.",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	llm := NewLLMClient(ts.URL, "key", "test-model", 5*time.Second)
	reg := tools.NewRegistry()
	eng := NewREPLEngine(llm, reg, tmpDir, "coder")
	eng.SetPollInterval(10 * time.Millisecond)

	bus := runtime.NewEventBus()
	state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-01")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- eng.Start(ctx, bus, state)
	}()

	time.Sleep(30 * time.Millisecond)

	// 1. Send P1 Urgent Directive
	_ = bus.Publish(runtime.Event{
		Priority:  runtime.P1_HighPriority,
		Source:    "commander",
		Target:    "repl-main",
		Payload:   "Urgent: switch branch",
		Timestamp: time.Now(),
	})

	// Wait for processing
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if state.Read().Status == "idle" {
			break
		}
	}

	// 2. Send P0 Emergency stop
	_ = bus.Publish(runtime.Event{
		Priority:  runtime.P0_Control,
		Source:    "operator",
		Target:    "repl-main",
		Payload:   "EMERGENCY HALT",
		Timestamp: time.Now(),
	})

	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("unexpected error on emergency stop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("engine did not stop on P0 event")
	}

	if state.Read().Status != "cancelled" {
		t.Errorf("expected state cancelled, got %s", state.Read().Status)
	}
}

func TestREPLEngineIterationLimit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always return a tool call to trigger iteration limit
		resp := ChatCompletionResponse{
			ID:    "resp-loop",
			Model: "test-model",
			Choices: []ChatChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role: "assistant",
						ToolCalls: []ToolCall{
							{
								ID:   "call_loop",
								Type: "function",
								Function: ToolFunctionCall{
									Name:      "test_loop",
									Arguments: "{}",
								},
							},
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	llm := NewLLMClient(ts.URL, "key", "test-model", 5*time.Second)
	reg := tools.NewRegistry()
	eng := NewREPLEngine(llm, reg, tmpDir, "coder")
	eng.SetPollInterval(10 * time.Millisecond)
	eng.SetMaxToolIterations(2) // low threshold for test

	bus := runtime.NewEventBus()
	state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-01")

	eng.AppendMessage(ChatMessage{Role: "user", Content: "Start infinite loop"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- eng.Start(ctx, bus, state)
	}()

	// Wait for iteration limit to trigger and return to idle
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if state.Read().Status == "idle" {
			break
		}
	}

	cancel()
	_ = <-errChan

	// Verify limit message was appended to history
	hasLimitMsg := false
	for _, m := range eng.GetHistory() {
		if strings.Contains(m.Content, "Tool execution limit reached") {
			hasLimitMsg = true
			break
		}
	}
	if !hasLimitMsg {
		t.Error("expected iteration limit notification in history")
	}
}

func TestREPLEngineP2BufferingDuringTurn(t *testing.T) {
	bus := runtime.NewEventBus()
	defer bus.Close()

	var turnCount int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&turnCount, 1)
		if count == 1 {
			// First turn: invoke a dummy tool and concurrently publish P2 event to bus
			_ = bus.Publish(runtime.Event{
				Priority:  runtime.P2_StandardAsync,
				Source:    "peer-agent",
				Target:    "*",
				Payload:   "async notification while tools run",
				Timestamp: time.Now(),
			})
			resp := ChatCompletionResponse{
				ID: "resp-turn-1",
				Choices: []ChatChoice{
					{
						Message: ChatMessage{
							Role: "assistant",
							ToolCalls: []ToolCall{
								{
									ID:       "tc-1",
									Type:     "function",
									Function: ToolFunctionCall{Name: "read_file", Arguments: `{"path":"sample.txt"}`},
								},
							},
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// Second turn: conclude turn
		resp := ChatCompletionResponse{
			ID: "resp-turn-2",
			Choices: []ChatChoice{
				{
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Finished tool run.",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmpDir, "sample.txt"), []byte("sample content"), 0644)
	llm := NewLLMClient(ts.URL, "key", "test-model", 5*time.Second)
	reg := tools.NewRegistry()
	_ = tools.RegisterBuiltinTools(reg, tmpDir)
	eng := NewREPLEngine(llm, reg, tmpDir, "coder")
	eng.SetPollInterval(10 * time.Millisecond)

	state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-01")
	eng.AppendMessage(ChatMessage{Role: "user", Content: "Start turn"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- eng.Start(ctx, bus, state)
	}()

	// Wait for processing to complete
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if atomic.LoadInt32(&turnCount) >= 2 && state.Read().Status == "idle" {
			break
		}
	}
	cancel()
	_ = <-errChan

	// Verify that the P2 async notification was NOT dropped and exists in history
	hasP2Msg := false
	for _, m := range eng.GetHistory() {
		if strings.Contains(m.Content, "async notification while tools run") {
			hasP2Msg = true
			break
		}
	}
	if !hasP2Msg {
		t.Errorf("expected P2 buffered event in history, got history: %+v", eng.GetHistory())
	}
}

func TestEngineEdgeCasesAndErrors(t *testing.T) {
	// TruncateToolOutput default length
	longText := strings.Repeat("B", 10000)
	truncDef := TruncateToolOutput(longText, 0)
	if !strings.Contains(truncDef, "[content truncated") {
		t.Error("expected default truncation")
	}

	// CompactHistory default turns
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u"},
	}
	compactDef := CompactHistory(msgs, 0)
	if len(compactDef) != 2 {
		t.Errorf("expected 2 messages with default turns, got %d", len(compactDef))
	}

	// LLM error during executeTurn
	tsErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server boom", http.StatusInternalServerError)
	}))
	defer tsErr.Close()

	tmpDir := t.TempDir()
	llmErr := NewLLMClient(tsErr.URL, "k", "m", 5*time.Second)
	engErr := NewREPLEngine(llmErr, nil, tmpDir, "coder")
	engErr.SetPollInterval(10 * time.Millisecond)

	bus := runtime.NewEventBus()
	state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-err")
	engErr.AppendMessage(ChatMessage{Role: "user", Content: "cause error"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = engErr.Start(ctx, bus, state)
	}()

	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if state.Read().Status == "error" {
			break
		}
	}
	if state.Read().Status != "error" {
		t.Errorf("expected status error, got %s", state.Read().Status)
	}

	// Tool execution failure handled
	var callStep int32
	tsToolErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step := atomic.AddInt32(&callStep, 1)
		var resp ChatCompletionResponse
		if step == 1 {
			resp = ChatCompletionResponse{
				Choices: []ChatChoice{
					{
						Message: ChatMessage{
							Role: "assistant",
							ToolCalls: []ToolCall{
								{
									ID:   "call_bad",
									Type: "function",
									Function: ToolFunctionCall{
										Name:      "non_existent_tool",
										Arguments: "{}",
									},
								},
							},
						},
					},
				},
			}
		} else {
			resp = ChatCompletionResponse{
				Choices: []ChatChoice{
					{
						Message: ChatMessage{
							Role:    "assistant",
							Content: "Recovered from tool error.",
						},
					},
				},
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer tsToolErr.Close()

	llmTool := NewLLMClient(tsToolErr.URL, "k", "m", 5*time.Second)
	reg := tools.NewRegistry()
	engTool := NewREPLEngine(llmTool, reg, tmpDir, "coder")
	engTool.SetPollInterval(10 * time.Millisecond)
	stateTool := runtime.NewSharedState(filepath.Join(tmpDir, "state2.json"), "agent-tool")
	engTool.AppendMessage(ChatMessage{Role: "user", Content: "run bad tool"})

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	go func() {
		_ = engTool.Start(ctx2, bus, stateTool)
	}()

	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if atomic.LoadInt32(&callStep) >= 2 && stateTool.Read().Status == "idle" {
			break
		}
	}

	// Verify tool error message is in history
	hasErr := false
	for _, m := range engTool.GetHistory() {
		if m.Role == "tool" && strings.Contains(m.Content, "Error:") {
			hasErr = true
			break
		}
	}
	if !hasErr {
		t.Error("expected tool error recorded in history")
	}
}

func TestParseFallbackToolCalls(t *testing.T) {
	// 1. Empty string
	if calls := parseFallbackToolCalls(""); len(calls) != 0 {
		t.Errorf("expected 0 calls for empty content, got %d", len(calls))
	}

	// 2. Direct JSON object with map arguments
	obj1 := `{"name": "bash", "arguments": {"command": "bp tell brian hi"}}`
	calls1 := parseFallbackToolCalls(obj1)
	if len(calls1) != 1 || calls1[0].Function.Name != "bash" || !strings.Contains(calls1[0].Function.Arguments, "bp tell brian hi") {
		t.Errorf("unexpected parse result for obj1: %+v", calls1)
	}

	// 3. Markdown fenced JSON with parameters
	fenced := "```json\n{\"tool\": \"read_file\", \"parameters\": {\"path\": \"README.md\"}}\n```"
	calls2 := parseFallbackToolCalls(fenced)
	if len(calls2) != 1 || calls2[0].Function.Name != "read_file" || !strings.Contains(calls2[0].Function.Arguments, "README.md") {
		t.Errorf("unexpected parse result for fenced: %+v", calls2)
	}

	// 4. Action / input format
	actionFmt := `{"action": "write_file", "input": {"path": "test.txt", "content": "data"}}`
	calls3 := parseFallbackToolCalls(actionFmt)
	if len(calls3) != 1 || calls3[0].Function.Name != "write_file" || !strings.Contains(calls3[0].Function.Arguments, "test.txt") {
		t.Errorf("unexpected parse result for actionFmt: %+v", calls3)
	}

	// 5. Function wrapper format
	fnFmt := `{"function": {"name": "edit_file", "arguments": "{\"path\": \"main.go\"}"}}`
	calls4 := parseFallbackToolCalls(fnFmt)
	if len(calls4) != 1 || calls4[0].Function.Name != "edit_file" || calls4[0].Function.Arguments != `{"path": "main.go"}` {
		t.Errorf("unexpected parse result for fnFmt: %+v", calls4)
	}

	// 6. JSON array format
	arrFmt := `[{"name": "bash", "arguments": {"command": "ls"}}, {"name": "bash", "arguments": {"command": "pwd"}}]`
	calls5 := parseFallbackToolCalls(arrFmt)
	if len(calls5) != 2 || calls5[0].Function.Name != "bash" || calls5[1].Function.Name != "bash" {
		t.Errorf("unexpected parse result for arrFmt: %+v", calls5)
	}

	// 7. Embedded in conversational text
	embedded := "I will list the directory.\n{\"name\": \"bash\", \"arguments\": {\"command\": \"ls -la\"}}\nDone."
	calls6 := parseFallbackToolCalls(embedded)
	if len(calls6) != 1 || calls6[0].Function.Name != "bash" || !strings.Contains(calls6[0].Function.Arguments, "ls -la") {
		t.Errorf("unexpected parse result for embedded: %+v", calls6)
	}

	// 8. Plain conversational text (no tool call)
	plain := "Hello! How can I assist you with the Agent Sandbox today?"
	calls7 := parseFallbackToolCalls(plain)
	if len(calls7) != 0 {
		t.Errorf("expected 0 calls for plain text, got %d", len(calls7))
	}

	// 9. Concatenated JSON lines (multi-tool invocation)
	ndjson := "{\"name\": \"write_file\", \"arguments\": {\"path\": \"a.txt\", \"content\": \"hello\"}}\n{\"name\": \"bash\", \"arguments\": {\"command\": \"cat a.txt\"}}"
	calls8 := parseFallbackToolCalls(ndjson)
	if len(calls8) != 2 || calls8[0].Function.Name != "write_file" || calls8[1].Function.Name != "bash" {
		t.Errorf("unexpected parse result for ndjson: %+v", calls8)
	}

	// 10. Markdown / text-wrapped tool blocks (<name>\n{ ... })
	blockFmt := "Here is my plan:\n```bash\nwrite_file\n{\n  \"path\": \"pkg/test.go\",\n  \"content\": \"package pkg\"\n}\n```\nAnd then:\nbash\n{\n  \"command\": \"go test ./...\"\n}\n"
	calls9 := parseFallbackToolCalls(blockFmt)
	if len(calls9) != 2 || calls9[0].Function.Name != "write_file" || calls9[1].Function.Name != "bash" {
		t.Errorf("unexpected parse result for blockFmt: %+v", calls9)
	}

	// 11. Embedded tool call inside markdown block with raw backtick string (like Alice's response)
	backtickBlock := "First step:\n```bash\n{\n  \"name\": \"write_file\",\n  \"arguments\": {\n    \"content\": `package hexutil\nfunc Encode() {}`,\n    \"path\": \"pkg/hexutil/hexutil.go\"\n  }\n}\n```\n"
	calls10 := parseFallbackToolCalls(backtickBlock)
	if len(calls10) != 1 || calls10[0].Function.Name != "write_file" || !strings.Contains(calls10[0].Function.Arguments, "pkg/hexutil/hexutil.go") {
		t.Errorf("unexpected parse result for backtickBlock: %+v", calls10)
	}

	// 12. Model output with unescaped quotes in content field: \t"encoding/hex\"
	aliceSnippet := "Let's execute these steps now.\n\n---\n\n{\"name\": \"write_file\", \"arguments\": {\"path\": \"/home/agent/workspace/hexutil/encode.go\", \"content\": \"package hexutil\\n\\nimport (\\n\\t\"encoding/hex\\\"\\n)\\n\\n// Encode converts a byte slice to a hex string.\\nfunc Encode(data []byte) string {\\n\\treturn hex.EncodeToString(data)\\n}\"}}"
	calls11 := parseFallbackToolCalls(aliceSnippet)
	t.Logf("calls11: len=%d %+v", len(calls11), calls11)
}

type mockTurnResponder struct {
	mu      sync.Mutex
	replies []struct {
		recipient string
		content   string
	}
}

func (m *mockTurnResponder) SendReply(_ context.Context, recipient, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.replies = append(m.replies, struct {
		recipient string
		content   string
	}{recipient: recipient, content: content})
	return nil
}

func (m *mockTurnResponder) getReplies() []struct {
	recipient string
	content   string
} {
	m.mu.Lock()
	defer m.mu.Unlock()
	copied := make([]struct {
		recipient string
		content   string
	}, len(m.replies))
	copy(copied, m.replies)
	return copied
}

func TestTurnResponderAutoReply(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatCompletionResponse{
			ID:    "resp-autoreply",
			Model: "test-model",
			Choices: []ChatChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Here is the answer to your question.",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	llm := NewLLMClient(ts.URL, "key", "test-model", 5*time.Second)
	reg := tools.NewRegistry()
	eng := NewREPLEngine(llm, reg, tmpDir, "coder")
	eng.SetPollInterval(10 * time.Millisecond)

	responder := &mockTurnResponder{}
	eng.SetResponder(responder)
	eng.SetAgentID("agent-alice")

	bus := runtime.NewEventBus()
	state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-alice")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = eng.Start(ctx, bus, state)
	}()

	time.Sleep(30 * time.Millisecond)

	// Send message from sender "brian"
	msg := &libbp.Message{
		ID:      "msg-1",
		Sender:  "brian",
		Content: "What is the status?",
	}
	_ = bus.Publish(runtime.Event{
		Priority:  runtime.P1_HighPriority,
		Source:    "valkey:inbox",
		Target:    "repl-main",
		Payload:   msg,
		Timestamp: time.Now(),
	})

	var replies []struct {
		recipient string
		content   string
	}
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		replies = responder.getReplies()
		if len(replies) > 0 {
			break
		}
	}

	if len(replies) != 1 {
		t.Fatalf("expected 1 auto-reply, got %d", len(replies))
	}
	if replies[0].recipient != "brian" {
		t.Errorf("expected recipient 'brian', got %s", replies[0].recipient)
	}
	if replies[0].content != "Here is the answer to your question." {
		t.Errorf("unexpected content: %s", replies[0].content)
	}
}

func TestTurnResponderSuppression(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			// First call returns a tool that sends external message
			resp := ChatCompletionResponse{
				ID:    "resp-tool",
				Model: "test-model",
				Choices: []ChatChoice{
					{
						Index: 0,
						Message: ChatMessage{
							Role: "assistant",
							ToolCalls: []ToolCall{
								{
									ID:   "call_bash",
									Type: "function",
									Function: ToolFunctionCall{
										Name:      "bash",
										Arguments: "{\"command\": \"bp tell brian 'already replied'\"}",
									},
								},
							},
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		// Second call returns final text
		resp := ChatCompletionResponse{
			ID:    "resp-final",
			Model: "test-model",
			Choices: []ChatChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Done executing command.",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	llm := NewLLMClient(ts.URL, "key", "test-model", 5*time.Second)
	reg := tools.NewRegistry()
	_ = tools.RegisterBuiltinTools(reg, tmpDir)
	eng := NewREPLEngine(llm, reg, tmpDir, "coder")
	eng.SetPollInterval(10 * time.Millisecond)

	responder := &mockTurnResponder{}
	eng.SetResponder(responder)
	eng.SetAgentID("agent-alice")

	bus := runtime.NewEventBus()
	state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-alice")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = eng.Start(ctx, bus, state)
	}()

	time.Sleep(30 * time.Millisecond)

	msg := &libbp.Message{
		ID:      "msg-2",
		Sender:  "brian",
		Content: "Run the comms check.",
	}
	_ = bus.Publish(runtime.Event{
		Priority:  runtime.P1_HighPriority,
		Source:    "valkey:inbox",
		Target:    "repl-main",
		Payload:   msg,
		Timestamp: time.Now(),
	})

	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if state.Read().Status == "idle" && callCount >= 2 {
			break
		}
	}

	// Because tool executed bp tell, responder.SendReply should be suppressed
	replies := responder.getReplies()
	if len(replies) != 0 {
		t.Errorf("expected 0 auto-replies due to external message suppression, got %d", len(replies))
	}
}

func TestExtractEventSender(t *testing.T) {
	if s := extractEventSender(nil); s != "" {
		t.Errorf("expected empty sender for nil, got %s", s)
	}
	if s := extractEventSender("string payload"); s != "" {
		t.Errorf("expected empty sender for string, got %s", s)
	}
	msg := libbp.Message{Sender: "alice"}
	if s := extractEventSender(msg); s != "alice" {
		t.Errorf("expected alice, got %s", s)
	}
	msgPtr := &libbp.Message{Sender: "bob"}
	if s := extractEventSender(msgPtr); s != "bob" {
		t.Errorf("expected bob, got %s", s)
	}
	var nilMsg *libbp.Message
	if s := extractEventSender(nilMsg); s != "" {
		t.Errorf("expected empty for nilMsg, got %s", s)
	}
	env := runtime.BackplaneEnvelope{Sender: "carol"}
	if s := extractEventSender(env); s != "carol" {
		t.Errorf("expected carol, got %s", s)
	}
	envPtr := &runtime.BackplaneEnvelope{Sender: "dave"}
	if s := extractEventSender(envPtr); s != "dave" {
		t.Errorf("expected dave, got %s", s)
	}
	var nilEnv *runtime.BackplaneEnvelope
	if s := extractEventSender(nilEnv); s != "" {
		t.Errorf("expected empty for nilEnv, got %s", s)
	}
}

func TestExtractEventInfo(t *testing.T) {
	s, c := extractEventInfo(nil)
	if s != "" || c != "" {
		t.Errorf("expected empty info for nil, got %s, %s", s, c)
	}
	msg := libbp.Message{Sender: "alice", Content: "hello"}
	s, c = extractEventInfo(msg)
	if s != "alice" || c != "hello" {
		t.Errorf("expected alice/hello, got %s/%s", s, c)
	}
	msgPtr := &libbp.Message{Sender: "bob", Content: "world"}
	s, c = extractEventInfo(msgPtr)
	if s != "bob" || c != "world" {
		t.Errorf("expected bob/world, got %s/%s", s, c)
	}
	env := runtime.BackplaneEnvelope{Sender: "carol", Payload: "p-env"}
	s, c = extractEventInfo(env)
	if s != "carol" || c != "p-env" {
		t.Errorf("expected carol/p-env, got %s/%s", s, c)
	}
	envPtr := &runtime.BackplaneEnvelope{Sender: "dave", Payload: "p-ptr"}
	s, c = extractEventInfo(envPtr)
	if s != "dave" || c != "p-ptr" {
		t.Errorf("expected dave/p-ptr, got %s/%s", s, c)
	}
}

func TestIsHumanSender(t *testing.T) {
	if isHumanSender("", "brian") {
		t.Error("expected false for empty sender")
	}
	if !isHumanSender("human", "brian") {
		t.Error("expected true for 'human'")
	}
	if !isHumanSender("operator", "brian") {
		t.Error("expected true for 'operator'")
	}
	if !isHumanSender("brian", "brian") {
		t.Error("expected true for 'brian' matching humanName")
	}
	if !isHumanSender("BRIAN", "brian") {
		t.Error("expected true for case-insensitive match")
	}
	if isHumanSender("pig-verifier", "brian") {
		t.Error("expected false for peer agent 'pig-verifier'")
	}
	if isHumanSender("alice", "brian") {
		t.Error("expected false for peer agent 'alice'")
	}
}

func TestIsAutomatedNotification(t *testing.T) {
	prefixes := []string{
		"completed directive: task finished",
		"Completed directive: done",
		"execution failed (exit 1)",
		"understood. directive queued for execution",
		"status: running tests",
		"online (container started)",
		"ok (all green)",
	}
	for _, p := range prefixes {
		if !isAutomatedNotification(p) {
			t.Errorf("expected true for %q", p)
		}
	}
	if isAutomatedNotification("please run the test suite") {
		t.Error("expected false for regular user prompt")
	}
}

func TestTurnResponder_PeerCompletionSuppressed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatCompletionResponse{
			ID:    "resp-peer-ack",
			Model: "test-model",
			Choices: []ChatChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Understood. The test suite has been verified.",
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	llm := NewLLMClient(ts.URL, "key", "test-model", 5*time.Second)
	eng := NewREPLEngine(llm, nil, tmpDir, "coordinator")
	eng.SetPollInterval(10 * time.Millisecond)
	eng.SetHumanName("brian")
	eng.SetAgentID("agent-alice")

	responder := &mockTurnResponder{}
	eng.SetResponder(responder)

	bus := runtime.NewEventBus()
	state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-alice")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = eng.Start(ctx, bus, state)
	}()

	time.Sleep(30 * time.Millisecond)

	// Simulate peer pig-verifier sending a completion notice
	msg := &libbp.Message{
		ID:          "pig-verifier#51",
		Sender:      "pig-verifier",
		Destination: "agent-alice",
		Content:     "Completed directive: Understood. I will now proceed with verifying the test suite",
		ReplyTo:     "agent-alice#149",
	}
	_ = bus.Publish(runtime.Event{
		Priority:  runtime.P2_StandardAsync,
		Source:    "valkey:inbox",
		Target:    "*",
		Payload:   msg,
		Timestamp: time.Now(),
	})

	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if state.Read().Status == "idle" {
			break
		}
	}

	// Verify that TurnResponder did NOT auto-reply to pig-verifier
	replies := responder.getReplies()
	if len(replies) != 0 {
		t.Fatalf("expected 0 auto-replies to peer completion notice, got %d: %+v", len(replies), replies)
	}
}

type mockSendTool struct {
	execCount *int
}

func (m mockSendTool) Name() string               { return "fleet_send_message" }
func (m mockSendTool) Description() string        { return "test" }
func (m mockSendTool) Parameters() map[string]any { return nil }
func (m mockSendTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	*m.execCount++
	return `{"id":"msg-1"}`, nil
}

func TestREPLEngineDuplicateToolCallDeduplication(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var resp ChatCompletionResponse
		if callCount == 1 {
			// First completion returns 2 identical fleet_send_message calls in the same turn
			resp = ChatCompletionResponse{
				ID:    "resp-dup-tools",
				Model: "test-model",
				Choices: []ChatChoice{
					{
						Index: 0,
						Message: ChatMessage{
							Role: "assistant",
							ToolCalls: []ToolCall{
								{
									ID:   "call-1",
									Type: "function",
									Function: ToolFunctionCall{
										Name:      "fleet_send_message",
										Arguments: `{"recipient":"bob","message":"test"}`,
									},
								},
								{
									ID:   "call-2",
									Type: "function",
									Function: ToolFunctionCall{
										Name:      "fleet_send_message",
										Arguments: `{"recipient":"bob","message":"test"}`,
									},
								},
							},
						},
						FinishReason: "tool_calls",
					},
				},
			}
		} else {
			resp = ChatCompletionResponse{
				ID:    "resp-final",
				Model: "test-model",
				Choices: []ChatChoice{
					{
						Index: 0,
						Message: ChatMessage{
							Role:    "assistant",
							Content: "Done.",
						},
						FinishReason: "stop",
					},
				},
			}
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	llm := NewLLMClient(ts.URL, "key", "test-model", 5*time.Second)
	reg := tools.NewRegistry()
	execCount := 0
	_ = reg.Register(mockSendTool{execCount: &execCount})

	eng := NewREPLEngine(llm, reg, tmpDir, "coder")
	eng.SetPollInterval(10 * time.Millisecond)

	bus := runtime.NewEventBus()
	state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-01")

	eng.AppendMessage(ChatMessage{Role: "user", Content: "Send a message."})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- eng.Start(ctx, bus, state)
	}()

	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if state.Read().Status == "idle" {
			break
		}
	}

	cancel()
	select {
	case err := <-errChan:
		if err != nil && err != context.Canceled {
			t.Fatalf("unexpected Start error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("engine did not stop after cancel")
	}

	if execCount != 1 {
		t.Errorf("expected fleet_send_message to be executed exactly once due to deduplication, got %d", execCount)
	}
}

func TestREPLEngine_ReasoningBudget_DynamicMapping(t *testing.T) {
	var capturedRequests []ChatCompletionRequest
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		capturedRequests = append(capturedRequests, req)
		mu.Unlock()

		resp := ChatCompletionResponse{
			ID:    "resp-priority",
			Model: "test-model",
			Choices: []ChatChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Task finished.",
					},
					FinishReason: "stop",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	testCases := []struct {
		name       string
		priority   int
		wantBudget float64
		wantEffort string
	}{
		{"P0", 0, 0.99, "high"},
		{"P1", 1, 0.95, "high"},
		{"P2", 2, 0.75, "medium"},
		{"P3", 3, 0.50, "low"},
		{"P4", 4, 0.25, "low"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mu.Lock()
			capturedRequests = nil
			mu.Unlock()

			tmpDir := t.TempDir()
			llm := NewLLMClient(ts.URL, "key", "test-model", 5*time.Second)
			eng := NewREPLEngine(llm, nil, tmpDir, "coder")
			eng.SetPollInterval(10 * time.Millisecond)
			eng.SetPriorityResolver(NewStaticTaskResolver(tc.priority, true))

			bus := runtime.NewEventBus()
			state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-test")
			eng.AppendMessage(ChatMessage{Role: "user", Content: "Run priority task"})

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			errChan := make(chan error, 1)
			go func() {
				errChan <- eng.Start(ctx, bus, state)
			}()

			for i := 0; i < 50; i++ {
				time.Sleep(20 * time.Millisecond)
				if state.Read().Status == "idle" {
					break
				}
			}

			cancel()
			<-errChan

			mu.Lock()
			defer mu.Unlock()
			if len(capturedRequests) == 0 {
				t.Fatal("expected at least one request to be captured")
			}
			lastReq := capturedRequests[len(capturedRequests)-1]
			if lastReq.ReasoningBudget == nil {
				t.Fatalf("expected ReasoningBudget to be non-nil for priority %d", tc.priority)
			}
			if *lastReq.ReasoningBudget != tc.wantBudget {
				t.Errorf("got ReasoningBudget %v, want %v", *lastReq.ReasoningBudget, tc.wantBudget)
			}
			if lastReq.ReasoningEffort != tc.wantEffort {
				t.Errorf("got ReasoningEffort %q, want %q", lastReq.ReasoningEffort, tc.wantEffort)
			}
		})
	}
}

func TestREPLEngine_ReasoningBudget_NotFoundAndError(t *testing.T) {
	var capturedRequests []ChatCompletionRequest
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		capturedRequests = append(capturedRequests, req)
		mu.Unlock()

		resp := ChatCompletionResponse{
			ID:    "resp-test",
			Model: "test-model",
			Choices: []ChatChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Done.",
					},
					FinishReason: "stop",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	t.Run("NotFound", func(t *testing.T) {
		mu.Lock()
		capturedRequests = nil
		mu.Unlock()

		tmpDir := t.TempDir()
		llm := NewLLMClient(ts.URL, "key", "test-model", 5*time.Second)
		eng := NewREPLEngine(llm, nil, tmpDir, "coder")
		eng.SetPollInterval(10 * time.Millisecond)
		eng.SetPriorityResolver(NewStaticTaskResolver(0, false))

		bus := runtime.NewEventBus()
		state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-test")
		eng.AppendMessage(ChatMessage{Role: "user", Content: "Hello"})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		errChan := make(chan error, 1)
		go func() {
			errChan <- eng.Start(ctx, bus, state)
		}()

		for i := 0; i < 50; i++ {
			time.Sleep(20 * time.Millisecond)
			if state.Read().Status == "idle" {
				break
			}
		}
		cancel()
		<-errChan

		mu.Lock()
		defer mu.Unlock()
		if len(capturedRequests) == 0 {
			t.Fatal("expected captured requests")
		}
		req := capturedRequests[0]
		if req.ReasoningBudget != nil {
			t.Errorf("expected ReasoningBudget to be nil when not found, got %v", *req.ReasoningBudget)
		}
		if req.ReasoningEffort != "" {
			t.Errorf("expected ReasoningEffort to be empty, got %q", req.ReasoningEffort)
		}
	})

	t.Run("ErrorIgnored", func(t *testing.T) {
		mu.Lock()
		capturedRequests = nil
		mu.Unlock()

		tmpDir := t.TempDir()
		llm := NewLLMClient(ts.URL, "key", "test-model", 5*time.Second)
		eng := NewREPLEngine(llm, nil, tmpDir, "coder")
		eng.SetPollInterval(10 * time.Millisecond)
		eng.SetPriorityResolver(&StaticTaskResolver{
			Priority: 1,
			Found:    false,
			Err:      errors.New("beads db locked"),
		})

		bus := runtime.NewEventBus()
		state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-test")
		eng.AppendMessage(ChatMessage{Role: "user", Content: "Hello"})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		errChan := make(chan error, 1)
		go func() {
			errChan <- eng.Start(ctx, bus, state)
		}()

		for i := 0; i < 50; i++ {
			time.Sleep(20 * time.Millisecond)
			if state.Read().Status == "idle" {
				break
			}
		}
		cancel()
		<-errChan

		mu.Lock()
		defer mu.Unlock()
		if len(capturedRequests) == 0 {
			t.Fatal("expected captured requests")
		}
		req := capturedRequests[0]
		if req.ReasoningBudget != nil {
			t.Errorf("expected ReasoningBudget to be nil on error, got %v", *req.ReasoningBudget)
		}
	})
}

func TestREPLEngine_PriorityResolver_Lifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	llm := NewLLMClient("http://localhost:8080", "key", "test-model", 5*time.Second)
	eng := NewREPLEngine(llm, nil, tmpDir, "coder")

	// Default resolver should be BeadsTaskResolver
	res := eng.PriorityResolver()
	if res == nil {
		t.Fatal("expected non-nil default PriorityResolver")
	}
	beadsRes, ok := res.(*BeadsTaskResolver)
	if !ok {
		t.Fatalf("expected BeadsTaskResolver, got %T", res)
	}
	if beadsRes.WorkspaceDir() != tmpDir {
		t.Errorf("expected workspaceDir %s, got %s", tmpDir, beadsRes.WorkspaceDir())
	}

	// Override resolver
	staticRes := NewStaticTaskResolver(2, true)
	eng.SetPriorityResolver(staticRes)
	if eng.PriorityResolver() != staticRes {
		t.Errorf("expected PriorityResolver to be %v, got %v", staticRes, eng.PriorityResolver())
	}
}

func TestREPLEngine_GetActiveSender(t *testing.T) {
	eng := NewREPLEngine(nil, nil, "", "coder")
	eng.setActiveSender("alice")
	if eng.getActiveSender() != "alice" {
		t.Errorf("expected getActiveSender() to return 'alice', got %q", eng.getActiveSender())
	}
}

func TestChatCompletionRequest_ReasoningFieldsJSON(t *testing.T) {
	budget := 0.75
	req := ChatCompletionRequest{
		Model:           "test-model",
		ReasoningBudget: &budget,
		ReasoningEffort: "medium",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal request: %v", err)
	}

	if raw["reasoning_budget"] != 0.75 {
		t.Errorf("expected reasoning_budget=0.75, got %v", raw["reasoning_budget"])
	}
	if raw["reasoning_effort"] != "medium" {
		t.Errorf("expected reasoning_effort='medium', got %v", raw["reasoning_effort"])
	}

	// Test omitempty when nil/empty
	reqEmpty := ChatCompletionRequest{Model: "test"}
	dataEmpty, _ := json.Marshal(reqEmpty)
	var rawEmpty map[string]any
	_ = json.Unmarshal(dataEmpty, &rawEmpty)
	if _, exists := rawEmpty["reasoning_budget"]; exists {
		t.Error("expected reasoning_budget to be omitted when nil")
	}
	if _, exists := rawEmpty["reasoning_effort"]; exists {
		t.Error("expected reasoning_effort to be omitted when empty")
	}
}
