// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package engine

import (
	"context"
	"encoding/json"
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

func TestTaskSnapshot_IsEmptyAndClone(t *testing.T) {
	// Nil snapshot
	var nilSnap *TaskSnapshot
	if !nilSnap.IsEmpty() {
		t.Error("expected nil snapshot to be empty")
	}
	if nilSnap.Clone() != nil {
		t.Error("expected clone of nil snapshot to be nil")
	}

	// Empty struct
	emptySnap := &TaskSnapshot{}
	if !emptySnap.IsEmpty() {
		t.Error("expected empty struct to be empty")
	}
	if FormatPinnedGoal(emptySnap) != "" {
		t.Errorf("expected empty string for empty snapshot, got %q", FormatPinnedGoal(emptySnap))
	}

	// Valid task snapshot
	snap := &TaskSnapshot{
		ID:                 "sndbx-z0m.2",
		Title:              "Implement goal-pinned prompt assembly",
		Description:        "Persistent Beads task graph state pinning.",
		Constraints:        []string{"Zero external module dependencies", "Hermetic tests"},
		AcceptanceCriteria: []string{"Pinned in prompt assembly", "Immune to history compaction"},
		Status:             "in_progress",
		Priority:           1,
	}

	if snap.IsEmpty() {
		t.Error("expected non-empty snapshot")
	}

	clone := snap.Clone()
	if clone == nil {
		t.Fatal("expected non-nil clone")
	}
	if clone.ID != snap.ID || clone.Title != snap.Title || len(clone.Constraints) != 2 || len(clone.AcceptanceCriteria) != 2 {
		t.Fatalf("clone does not match original: %+v", clone)
	}

	// Ensure deep copy of slices
	clone.Constraints[0] = "Mutated constraint"
	if snap.Constraints[0] == "Mutated constraint" {
		t.Error("expected deep copy of constraints slice")
	}
	clone.AcceptanceCriteria[0] = "Mutated criterion"
	if snap.AcceptanceCriteria[0] == "Mutated criterion" {
		t.Error("expected deep copy of acceptance criteria slice")
	}
}

func TestFormatPinnedGoal(t *testing.T) {
	if got := FormatPinnedGoal(nil); got != "" {
		t.Errorf("expected empty string for nil, got %q", got)
	}

	task := &TaskSnapshot{
		ID:                 "sndbx-z0m.2",
		Title:              "Goal pinning",
		Description:        "Ensure task details survive compaction.",
		Constraints:        []string{"Maintain >= 90% test coverage", "Conventional commits"},
		AcceptanceCriteria: []string{"All tests pass", "Make check clean"},
		Status:             "in_progress",
		Priority:           0,
	}

	formatted := FormatPinnedGoal(task)
	if !strings.Contains(formatted, PinnedTaskDirectivePrefix) {
		t.Errorf("missing header prefix in formatted goal: %s", formatted)
	}
	if !strings.Contains(formatted, "Issue / Bead ID: sndbx-z0m.2") {
		t.Errorf("missing issue ID in formatted goal: %s", formatted)
	}
	if !strings.Contains(formatted, "Title: Goal pinning") {
		t.Errorf("missing title in formatted goal: %s", formatted)
	}
	if !strings.Contains(formatted, "Status: in_progress") {
		t.Errorf("missing status in formatted goal: %s", formatted)
	}
	if !strings.Contains(formatted, "Priority: P0") {
		t.Errorf("missing priority in formatted goal: %s", formatted)
	}
	if !strings.Contains(formatted, "Description / Requirements:\nEnsure task details survive compaction.") {
		t.Errorf("missing description in formatted goal: %s", formatted)
	}
	if !strings.Contains(formatted, "- Maintain >= 90% test coverage") {
		t.Errorf("missing constraint in formatted goal: %s", formatted)
	}
	if !strings.Contains(formatted, "- [ ] All tests pass") {
		t.Errorf("missing acceptance criterion in formatted goal: %s", formatted)
	}
	if !strings.Contains(formatted, "=== END ACTIVE TASK DIRECTIVE ===") {
		t.Errorf("missing end marker in formatted goal: %s", formatted)
	}

	// Partial task without description or criteria
	partial := &TaskSnapshot{
		ID:    "sndbx-1",
		Title: "Minimal task",
	}
	partialFormatted := FormatPinnedGoal(partial)
	if !strings.Contains(partialFormatted, "Issue / Bead ID: sndbx-1") {
		t.Errorf("missing ID in partial: %s", partialFormatted)
	}
	if strings.Contains(partialFormatted, "Constraints:") {
		t.Error("unexpected constraints section in minimal task")
	}
}

func TestBuildSystemPromptWithGoal(t *testing.T) {
	// Without goal
	promptWithout := BuildSystemPromptWithGoal("agent-01", "coder", "/ws", "custom rule", nil)
	if strings.Contains(promptWithout, PinnedTaskDirectivePrefix) {
		t.Errorf("did not expect pinned directive in prompt without goal: %s", promptWithout)
	}
	if !strings.Contains(promptWithout, "You are agent-01") {
		t.Errorf("missing agent id in prompt: %s", promptWithout)
	}

	// With goal
	task := &TaskSnapshot{
		ID:          "sndbx-z0m.2",
		Title:       "Test goal injection",
		Description: "Detailed task objective.",
	}
	promptWith := BuildSystemPromptWithGoal("agent-01", "coder", "/ws", "custom rule", task)
	if !strings.Contains(promptWith, PinnedTaskDirectivePrefix) {
		t.Errorf("expected pinned directive in prompt: %s", promptWith)
	}
	if !strings.Contains(promptWith, "Issue / Bead ID: sndbx-z0m.2") {
		t.Errorf("expected issue ID in prompt: %s", promptWith)
	}
	if !strings.Contains(promptWith, "custom rule") {
		t.Errorf("expected custom instructions in prompt: %s", promptWith)
	}
}

func TestCompactHistory_ImmunityOfPinnedGoal(t *testing.T) {
	// 1. System prompt containing pinned goal directive
	task := &TaskSnapshot{
		ID:    "sndbx-z0m.2",
		Title: "Pinned Directive Anchor",
	}
	sysPromptWithGoal := BuildSystemPromptWithGoal("agent-01", "coder", "/ws", "", task)

	history := []ChatMessage{
		{Role: "system", Content: sysPromptWithGoal},
		{Role: "user", Content: "turn 1"},
		{Role: "assistant", Content: "turn 1 reply"},
		{Role: "user", Content: "turn 2"},
		{Role: "assistant", Content: "turn 2 reply"},
		{Role: "user", Content: "turn 3"},
		{Role: "assistant", Content: "turn 3 reply"},
		{Role: "user", Content: "turn 4"},
		{Role: "assistant", Content: "turn 4 reply"},
	}

	// Compact history aggressively down to 2 turns
	compacted := CompactHistory(history, 2)
	if len(compacted) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(compacted))
	}
	if compacted[0].Role != "system" {
		t.Errorf("expected system prompt at index 0, got %s", compacted[0].Role)
	}
	if !strings.Contains(compacted[0].Content, PinnedTaskDirectivePrefix) {
		t.Errorf("expected pinned goal preserved in system prompt: %s", compacted[0].Content)
	}
	if !strings.Contains(compacted[0].Content, "sndbx-z0m.2") {
		t.Errorf("expected issue ID in system prompt: %s", compacted[0].Content)
	}

	// 2. Standalone pinned directive message among conversational turns
	directiveMsg := ChatMessage{
		Role:    "user",
		Content: FormatPinnedGoal(task),
	}
	historyWithStandalone := []ChatMessage{
		{Role: "system", Content: "base system prompt"},
		directiveMsg,
		{Role: "user", Content: "turn 1"},
		{Role: "assistant", Content: "turn 1 reply"},
		{Role: "user", Content: "turn 2"},
		{Role: "assistant", Content: "turn 2 reply"},
		{Role: "user", Content: "turn 3"},
		{Role: "assistant", Content: "turn 3 reply"},
	}

	// Compact down to 3 turns
	compactedStandalone := CompactHistory(historyWithStandalone, 3)
	hasDirective := false
	for _, m := range compactedStandalone {
		if strings.Contains(m.Content, PinnedTaskDirectivePrefix) {
			hasDirective = true
			break
		}
	}
	if !hasDirective {
		t.Errorf("expected standalone pinned directive message preserved across compaction: %+v", compactedStandalone)
	}
}

func TestREPLEngine_SetPinnedTask_AndDynamicPromptUpdate(t *testing.T) {
	var capturedRequests []ChatCompletionRequest
	var mu sync.Mutex
	var callCount int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step := atomic.AddInt32(&callCount, 1)
		var req ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
			mu.Lock()
			capturedRequests = append(capturedRequests, req)
			mu.Unlock()
		}

		resp := ChatCompletionResponse{
			ID:    "resp-test",
			Model: "test-model",
			Choices: []ChatChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    "assistant",
						Content: "Done step.",
					},
					FinishReason: "stop",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
		_ = step
	}))
	defer ts.Close()

	tmpDir := t.TempDir()
	llm := NewLLMClient(ts.URL, "key", "test-model", 5*time.Second)
	reg := tools.NewRegistry()
	eng := NewREPLEngine(llm, reg, tmpDir, "coder")
	eng.SetPollInterval(10 * time.Millisecond)

	// Verify initial GetPinnedTask is nil
	if eng.GetPinnedTask() != nil {
		t.Errorf("expected initial pinned task to be nil, got: %+v", eng.GetPinnedTask())
	}

	// Set initial pinned task
	initialTask := &TaskSnapshot{
		ID:          "sndbx-z0m.2",
		Title:       "Initial Task Directive",
		Description: "Initial requirements.",
		Status:      "in_progress",
		Priority:    1,
	}
	eng.SetPinnedTask(initialTask)

	gotTask := eng.GetPinnedTask()
	if gotTask == nil || gotTask.ID != "sndbx-z0m.2" {
		t.Fatalf("unexpected GetPinnedTask result: %+v", gotTask)
	}

	bus := runtime.NewEventBus()
	state := runtime.NewSharedState(filepath.Join(tmpDir, "state.json"), "agent-01")
	eng.AppendMessage(ChatMessage{Role: "user", Content: "Start turn 1"})

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

	// Verify request 1 contained the initial task directive in the system prompt
	mu.Lock()
	if len(capturedRequests) == 0 {
		mu.Unlock()
		t.Fatal("expected at least 1 request")
	}
	req1 := capturedRequests[0]
	mu.Unlock()

	if len(req1.Messages) == 0 || req1.Messages[0].Role != "system" {
		t.Fatalf("expected system prompt at index 0 in req1: %+v", req1.Messages)
	}
	if !strings.Contains(req1.Messages[0].Content, "Initial Task Directive") {
		t.Errorf("expected initial task directive in system prompt: %s", req1.Messages[0].Content)
	}

	// Update pinned task dynamically mid-lifecycle
	updatedTask := &TaskSnapshot{
		ID:          "sndbx-z0m.3",
		Title:       "Updated Mid-Flight Task Directive",
		Description: "Updated requirements.",
		Status:      "in_progress",
		Priority:    0,
	}
	eng.SetPinnedTask(updatedTask)

	// Send next turn directive
	_ = bus.Publish(runtime.Event{
		Priority:  runtime.P1_HighPriority,
		Source:    "operator",
		Payload:   "Continue with updated task",
		Timestamp: time.Now(),
	})

	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		if atomic.LoadInt32(&callCount) >= 2 && state.Read().Status == "idle" {
			break
		}
	}

	cancel()
	_ = <-errChan

	// Verify request 2 contained the updated task directive
	mu.Lock()
	defer mu.Unlock()
	if len(capturedRequests) < 2 {
		t.Fatalf("expected at least 2 requests, got %d", len(capturedRequests))
	}
	req2 := capturedRequests[1]
	if !strings.Contains(req2.Messages[0].Content, "Updated Mid-Flight Task Directive") {
		t.Errorf("expected updated task directive in system prompt: %s", req2.Messages[0].Content)
	}
	if !strings.Contains(req2.Messages[0].Content, "sndbx-z0m.3") {
		t.Errorf("expected updated issue ID sndbx-z0m.3 in system prompt: %s", req2.Messages[0].Content)
	}
}
