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
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/boggycreek/fleet/agent/pkg/runtime"
	"github.com/boggycreek/fleet/agent/pkg/tools"
)

type mockMemoryRetriever struct {
	mu            sync.Mutex
	lastQuery     string
	lastLimit     int
	returnResults []string
	returnErr     error
	delay         time.Duration
}

func (m *mockMemoryRetriever) SearchMemories(ctx context.Context, query string, limit int) ([]string, error) {
	m.mu.Lock()
	m.lastQuery = query
	m.lastLimit = limit
	delay := m.delay
	m.mu.Unlock()

	if delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.returnErr != nil {
		return nil, m.returnErr
	}
	return m.returnResults, nil
}

func (m *mockMemoryRetriever) getLastQuery() (string, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastQuery, m.lastLimit
}

func TestFormatEpisodicMemories(t *testing.T) {
	// Nil and empty cases
	if got := FormatEpisodicMemories(nil); got != "" {
		t.Errorf("expected empty string for nil memories, got %q", got)
	}
	if got := FormatEpisodicMemories([]string{}); got != "" {
		t.Errorf("expected empty string for empty slice, got %q", got)
	}
	if got := FormatEpisodicMemories([]string{"", "   ", "\t\n"}); got != "" {
		t.Errorf("expected empty string for blank slice, got %q", got)
	}

	// Valid formatting with prefix normalization
	memories := []string{
		"Always run make check before committing.",
		"- Zero external dependencies in internal Go packages.",
		"* Pass sensitive environment variables via cmd.Env.",
	}

	formatted := FormatEpisodicMemories(memories)
	if !strings.Contains(formatted, EpisodicMemoriesDirectivePrefix) {
		t.Errorf("missing prefix in formatted memories: %s", formatted)
	}
	if !strings.Contains(formatted, EpisodicMemoriesDirectiveSuffix) {
		t.Errorf("missing suffix in formatted memories: %s", formatted)
	}
	if !strings.Contains(formatted, "- Always run make check before committing.") {
		t.Errorf("missing formatted item 1: %s", formatted)
	}
	if !strings.Contains(formatted, "- Zero external dependencies in internal Go packages.") {
		t.Errorf("missing formatted item 2: %s", formatted)
	}
	if strings.Contains(formatted, "- - Zero external") {
		t.Errorf("duplicate dash detected in item 2: %s", formatted)
	}
	if !strings.Contains(formatted, "- Pass sensitive environment variables via cmd.Env.") {
		t.Errorf("missing asterisk-normalized item 3: %s", formatted)
	}
	if strings.Contains(formatted, "* Pass") {
		t.Errorf("asterisk not converted in item 3: %s", formatted)
	}
}

func TestBuildSystemPromptWithGoalAndMemories(t *testing.T) {
	task := &TaskSnapshot{
		ID:    "sndbx-z0m.3",
		Title: "Dynamic episodic memory injection",
	}
	memories := []string{"Memory item 1", "Memory item 2"}

	// 1. Without goal, without memories
	p1 := BuildSystemPrompt("agent-01", "coder", "/ws", "")
	if strings.Contains(p1, PinnedTaskDirectivePrefix) || strings.Contains(p1, EpisodicMemoriesDirectivePrefix) {
		t.Errorf("unexpected directives in bare prompt: %s", p1)
	}

	// 2. With goal, without memories
	p2 := BuildSystemPromptWithGoal("agent-01", "coder", "/ws", "", task)
	if !strings.Contains(p2, PinnedTaskDirectivePrefix) {
		t.Errorf("expected pinned goal in p2: %s", p2)
	}
	if strings.Contains(p2, EpisodicMemoriesDirectivePrefix) {
		t.Errorf("did not expect memories in p2: %s", p2)
	}

	// 3. With memories, without goal
	p3 := BuildSystemPromptWithGoalAndMemories("agent-01", "coder", "/ws", "", nil, memories)
	if strings.Contains(p3, PinnedTaskDirectivePrefix) {
		t.Errorf("did not expect pinned goal in p3: %s", p3)
	}
	if !strings.Contains(p3, EpisodicMemoriesDirectivePrefix) {
		t.Errorf("expected memories in p3: %s", p3)
	}
	if !strings.Contains(p3, "- Memory item 1") || !strings.Contains(p3, "- Memory item 2") {
		t.Errorf("missing memory items in p3: %s", p3)
	}

	// 4. With both goal and memories via BuildSystemPromptWithGoal variadic
	p4 := BuildSystemPromptWithGoal("agent-01", "coder", "/ws", "", task, memories)
	if !strings.Contains(p4, PinnedTaskDirectivePrefix) {
		t.Errorf("expected pinned goal in p4: %s", p4)
	}
	if !strings.Contains(p4, EpisodicMemoriesDirectivePrefix) {
		t.Errorf("expected memories in p4: %s", p4)
	}

	// 5. BuildSystemPromptWithGoalAndMemories explicit
	p5 := BuildSystemPromptWithGoalAndMemories("agent-01", "coder", "/ws", "", task, memories)
	if p4 != p5 {
		t.Errorf("expected p4 and p5 to match:\np4=%s\np5=%s", p4, p5)
	}
}

func TestCompactHistory_EpisodicMemoriesImmunity(t *testing.T) {
	memories := []string{"Preserve architectural note"}
	formattedBlock := FormatEpisodicMemories(memories)

	// Standalone message containing episodic memories directive
	history := []ChatMessage{
		{Role: "system", Content: "base system prompt"},
		{Role: "user", Content: formattedBlock},
		{Role: "user", Content: "turn 1"},
		{Role: "assistant", Content: "turn 1 reply"},
		{Role: "user", Content: "turn 2"},
		{Role: "assistant", Content: "turn 2 reply"},
		{Role: "user", Content: "turn 3"},
		{Role: "assistant", Content: "turn 3 reply"},
	}

	compacted := CompactHistory(history, 2)
	hasMemoriesDirective := false
	for _, m := range compacted {
		if strings.Contains(m.Content, EpisodicMemoriesDirectivePrefix) {
			hasMemoriesDirective = true
			break
		}
	}
	if !hasMemoriesDirective {
		t.Errorf("expected standalone episodic memories directive preserved across compaction: %+v", compacted)
	}
}

func TestREPLEngine_MemoryRetriever_Integration(t *testing.T) {
	var capturedRequests []ChatCompletionRequest
	var mu sync.Mutex
	var callCount int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		var req ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
			mu.Lock()
			capturedRequests = append(capturedRequests, req)
			mu.Unlock()
		}

		resp := ChatCompletionResponse{
			ID:    "resp-mem",
			Model: "test-model",
			Choices: []ChatChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Memory integrated response.",
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

	// Initial retriever is nil
	if eng.MemoryRetriever() != nil {
		t.Errorf("expected initial retriever to be nil")
	}
	if eng.GetRetrievedMemories() != nil {
		t.Errorf("expected initial retrieved memories to be nil")
	}

	mockRetriever := &mockMemoryRetriever{
		returnResults: []string{
			"Do not pass passwords in argv.",
			"Use umask 077 for secret files.",
		},
	}
	eng.SetMemoryRetriever(mockRetriever)
	if eng.MemoryRetriever() != mockRetriever {
		t.Errorf("expected MemoryRetriever() to return configured mock")
	}

	task := &TaskSnapshot{
		ID:          "sndbx-z0m.3",
		Title:       "Security review for credential handling",
		Description: "Review scripts for argv exposures",
	}
	eng.SetPinnedTask(task)

	bus := runtime.NewEventBus()
	state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-01")
	eng.AppendMessage(ChatMessage{Role: "user", Content: "Start turn"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- eng.Start(ctx, bus, state)
	}()

	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if atomic.LoadInt32(&callCount) >= 1 && state.Read().Status == "idle" {
			break
		}
	}

	cancel()
	_ = <-errChan

	// Verify query passed to retriever
	q, limit := mockRetriever.getLastQuery()
	if q != "Security review for credential handling" {
		t.Errorf("expected query %q, got %q", "Security review for credential handling", q)
	}
	if limit != DefaultMemoryRetrievalLimit {
		t.Errorf("expected limit %d, got %d", DefaultMemoryRetrievalLimit, limit)
	}

	// Verify retrieved memories in engine
	memories := eng.GetRetrievedMemories()
	if len(memories) != 2 || memories[0] != "Do not pass passwords in argv." {
		t.Errorf("unexpected retrieved memories: %+v", memories)
	}

	// Verify prompt sent to LLM
	mu.Lock()
	defer mu.Unlock()
	if len(capturedRequests) == 0 {
		t.Fatal("expected captured requests")
	}
	req := capturedRequests[0]
	if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
		t.Fatalf("expected system prompt at index 0")
	}
	sysPrompt := req.Messages[0].Content
	if !strings.Contains(sysPrompt, EpisodicMemoriesDirectivePrefix) {
		t.Errorf("missing episodic memories block in system prompt: %s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "- Do not pass passwords in argv.") {
		t.Errorf("missing memory 1 in system prompt: %s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, "- Use umask 077 for secret files.") {
		t.Errorf("missing memory 2 in system prompt: %s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, PinnedTaskDirectivePrefix) {
		t.Errorf("missing pinned goal block alongside memories: %s", sysPrompt)
	}
}

func TestREPLEngine_MemoryRetriever_GracefulDegradation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatCompletionResponse{
			ID:    "resp-degrade",
			Model: "test-model",
			Choices: []ChatChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Graceful turn finished.",
					},
					FinishReason: "stop",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	testCases := []struct {
		name      string
		retriever MemoryRetriever
		task      *TaskSnapshot
	}{
		{
			name:      "NilRetriever",
			retriever: nil,
			task:      &TaskSnapshot{ID: "task-1", Title: "Task 1"},
		},
		{
			name: "ErrorRetriever",
			retriever: &mockMemoryRetriever{
				returnErr: errors.New("valkey memory connection dropped"),
			},
			task: &TaskSnapshot{ID: "task-2", Title: "Task 2"},
		},
		{
			name: "TimeoutRetriever",
			retriever: &mockMemoryRetriever{
				returnErr: context.DeadlineExceeded,
			},
			task: &TaskSnapshot{ID: "task-3", Title: "Task 3"},
		},
		{
			name: "EmptyResultsRetriever",
			retriever: &mockMemoryRetriever{
				returnResults: []string{},
			},
			task: &TaskSnapshot{ID: "task-4", Title: "Task 4"},
		},
		{
			name: "WhitespaceResultsRetriever",
			retriever: &mockMemoryRetriever{
				returnResults: []string{"   ", "\n\t"},
			},
			task: &TaskSnapshot{ID: "task-5", Title: "Task 5"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			llm := NewLLMClient(ts.URL, "key", "test-model", 5*time.Second)
			eng := NewREPLEngine(llm, nil, tmpDir, "coder")
			eng.SetPollInterval(10 * time.Millisecond)
			if tc.retriever != nil {
				eng.SetMemoryRetriever(tc.retriever)
			}
			eng.SetPinnedTask(tc.task)

			bus := runtime.NewEventBus()
			state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-01")
			eng.AppendMessage(ChatMessage{Role: "user", Content: "Run turn"})

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

			// System prompt must have task but NOT episodic memories
			hist := eng.GetHistory()
			if len(hist) == 0 || hist[0].Role != "system" {
				t.Fatalf("expected system prompt at index 0")
			}
			if strings.Contains(hist[0].Content, EpisodicMemoriesDirectivePrefix) {
				t.Errorf("did not expect episodic memories directive block on degradation: %s", hist[0].Content)
			}
			if !strings.Contains(hist[0].Content, tc.task.Title) {
				t.Errorf("expected pinned task preserved: %s", hist[0].Content)
			}
		})
	}
}

func TestREPLEngine_MemoryRetriever_FallbackToUserTurn(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatCompletionResponse{
			ID:    "resp-user-fallback",
			Model: "test-model",
			Choices: []ChatChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Fallback success.",
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
	eng := NewREPLEngine(llm, nil, tmpDir, "coder")
	eng.SetPollInterval(10 * time.Millisecond)

	mockRetriever := &mockMemoryRetriever{
		returnResults: []string{"Note for user prompt"},
	}
	eng.SetMemoryRetriever(mockRetriever)

	// No pinned task set! Pinned task is nil.
	bus := runtime.NewEventBus()
	state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-01")
	eng.AppendMessage(ChatMessage{Role: "user", Content: "How to handle ZFS datasets?"})

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

	q, _ := mockRetriever.getLastQuery()
	if q != "How to handle ZFS datasets?" {
		t.Errorf("expected query to fall back to user message, got %q", q)
	}

	hist := eng.GetHistory()
	if !strings.Contains(hist[0].Content, "Note for user prompt") {
		t.Errorf("expected memory injected into system prompt via user fallback: %s", hist[0].Content)
	}
}

func TestREPLEngine_MemoryRetriever_DescriptionQueryWhenTitleEmpty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatCompletionResponse{
			ID:    "resp-desc",
			Model: "test-model",
			Choices: []ChatChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Desc query success.",
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
	eng := NewREPLEngine(llm, nil, tmpDir, "coder")
	eng.SetPollInterval(10 * time.Millisecond)

	mockRetriever := &mockMemoryRetriever{
		returnResults: []string{"Description note"},
	}
	eng.SetMemoryRetriever(mockRetriever)

	task := &TaskSnapshot{
		ID:          "task-desc-only",
		Title:       "", // empty title
		Description: "Detailed description of task requirements",
	}
	eng.SetPinnedTask(task)

	bus := runtime.NewEventBus()
	state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-01")
	eng.AppendMessage(ChatMessage{Role: "user", Content: "Execute task"})

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

	q, _ := mockRetriever.getLastQuery()
	if q != "Detailed description of task requirements" {
		t.Errorf("expected query to use task description when title is empty, got %q", q)
	}
}

func TestREPLEngine_SetPinnedTask_DynamicMemoryRefresh(t *testing.T) {
	tmpDir := t.TempDir()
	eng := NewREPLEngine(nil, nil, tmpDir, "coder")

	mockRetriever := &mockMemoryRetriever{
		returnResults: []string{"Note from SetPinnedTask"},
	}
	eng.SetMemoryRetriever(mockRetriever)

	// Set initial system prompt in history
	eng.SetHistory([]ChatMessage{
		{Role: "system", Content: BuildSystemPrompt("agent-01", "coder", tmpDir, "")},
	})

	// Call SetPinnedTask
	task := &TaskSnapshot{
		ID:    "sndbx-z0m.3",
		Title: "Dynamic SetPinnedTask Refresh",
	}
	eng.SetPinnedTask(task)

	hist := eng.GetHistory()
	if !strings.Contains(hist[0].Content, "Note from SetPinnedTask") {
		t.Errorf("expected dynamic memory refresh in system prompt on SetPinnedTask: %s", hist[0].Content)
	}
	if !strings.Contains(hist[0].Content, "Dynamic SetPinnedTask Refresh") {
		t.Errorf("expected task title in system prompt: %s", hist[0].Content)
	}

	// Update with error retriever
	errRetriever := &mockMemoryRetriever{
		returnErr: errors.New("query failed"),
	}
	eng.SetMemoryRetriever(errRetriever)
	updatedTask := &TaskSnapshot{
		ID:    "sndbx-z0m.3",
		Title: "Second Pinned Task",
	}
	eng.SetPinnedTask(updatedTask)

	hist2 := eng.GetHistory()
	if strings.Contains(hist2[0].Content, "Note from SetPinnedTask") {
		t.Errorf("expected previous memories cleared when query fails: %s", hist2[0].Content)
	}
	if !strings.Contains(hist2[0].Content, "Second Pinned Task") {
		t.Errorf("expected second pinned task present: %s", hist2[0].Content)
	}
}

func TestREPLEngine_QueryEpisodicMemories_EmptyContext(t *testing.T) {
	tmpDir := t.TempDir()
	eng := NewREPLEngine(nil, nil, tmpDir, "coder")

	// 1. Retriever nil -> returns nil
	if res := eng.queryEpisodicMemories(context.Background()); res != nil {
		t.Errorf("expected nil when retriever is nil, got %+v", res)
	}

	mockRetriever := &mockMemoryRetriever{
		returnResults: []string{"some memory"},
	}
	eng.SetMemoryRetriever(mockRetriever)

	// 2. Both task and history empty -> returns nil
	if res := eng.queryEpisodicMemories(context.Background()); res != nil {
		t.Errorf("expected nil when no query source exists, got %+v", res)
	}

	// 3. User message is a pinned task directive -> ignored, returns nil
	eng.SetHistory([]ChatMessage{
		{Role: "user", Content: PinnedTaskDirectivePrefix + "\nfoo"},
	})
	if res := eng.queryEpisodicMemories(context.Background()); res != nil {
		t.Errorf("expected nil when only pinned task directive in history, got %+v", res)
	}
}
