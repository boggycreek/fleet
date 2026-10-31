// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/fleet/agent/pkg/engine"
)

func TestNewHarnessDefaults(t *testing.T) {
	h := NewHarness(HarnessConfig{})
	if h.cfg.MaxTurns != 20 {
		t.Errorf("expected default MaxTurns 20, got %d", h.cfg.MaxTurns)
	}
	if h.cfg.MaxToolIterations != 10 {
		t.Errorf("expected default MaxToolIterations 10, got %d", h.cfg.MaxToolIterations)
	}
	if h.cfg.Timeout != 30*time.Second {
		t.Errorf("expected default Timeout 30s, got %v", h.cfg.Timeout)
	}
	if h.cfg.Role != "Software Engineer" {
		t.Errorf("expected default Role 'Software Engineer', got %q", h.cfg.Role)
	}
}

func TestHarnessRunTDDScenario(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	h := NewHarness(HarnessConfig{
		Timeout: 15 * time.Second,
	})

	scenario := NewTDDScenario()
	res, err := h.RunScenario(ctx, scenario)
	if err != nil {
		t.Fatalf("RunScenario unexpected error: %v", err)
	}

	if !res.Success {
		t.Fatalf("scenario failed: error=%s", res.Error)
	}
	if res.Turns != 3 {
		t.Errorf("expected 3 turns, got %d", res.Turns)
	}
	if res.ToolCallCounts["write_file"] != 1 {
		t.Errorf("expected 1 write_file call, got %d", res.ToolCallCounts["write_file"])
	}
	if res.ToolCallCounts["bash"] != 1 {
		t.Errorf("expected 1 bash call, got %d", res.ToolCallCounts["bash"])
	}
	if !strings.Contains(res.Output, "Implemented Add function") {
		t.Errorf("unexpected output: %s", res.Output)
	}
}

func TestHarnessRunBugRepairScenario(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	h := NewHarness(HarnessConfig{
		Timeout: 15 * time.Second,
	})

	scenario := NewBugRepairScenario()
	res, err := h.RunScenario(ctx, scenario)
	if err != nil {
		t.Fatalf("RunScenario unexpected error: %v", err)
	}

	if !res.Success {
		t.Fatalf("scenario failed: error=%s", res.Error)
	}
	if res.Turns != 5 {
		t.Errorf("expected 5 turns, got %d", res.Turns)
	}
	if res.ToolCallCounts["bash"] != 2 {
		t.Errorf("expected 2 bash calls, got %d", res.ToolCallCounts["bash"])
	}
	if res.ToolCallCounts["read_file"] != 1 {
		t.Errorf("expected 1 read_file call, got %d", res.ToolCallCounts["read_file"])
	}
	if res.ToolCallCounts["edit_file"] != 1 {
		t.Errorf("expected 1 edit_file call, got %d", res.ToolCallCounts["edit_file"])
	}
	if !strings.Contains(res.Output, "repaired Multiply function") {
		t.Errorf("unexpected output: %s", res.Output)
	}
}

func TestHarnessRunReasoningBudgetScenario(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	h := NewHarness(HarnessConfig{
		Timeout: 10 * time.Second,
	})

	testCases := []struct {
		prio       int
		wantBudget float64
		wantEffort string
	}{
		{prio: 0, wantBudget: 0.99, wantEffort: "high"},
		{prio: 1, wantBudget: 0.95, wantEffort: "high"},
		{prio: 2, wantBudget: 0.75, wantEffort: "medium"},
	}

	for _, tc := range testCases {
		t.Run(tc.wantEffort, func(t *testing.T) {
			scenario := NewReasoningBudgetScenario(tc.prio)
			res, err := h.RunScenario(ctx, scenario)
			if err != nil {
				t.Fatalf("RunScenario failed: %v", err)
			}
			if !res.Success {
				t.Fatalf("scenario failed: error=%s", res.Error)
			}
			if len(res.CapturedRequests) == 0 {
				t.Fatal("expected at least one captured request")
			}
			req := res.CapturedRequests[0]
			if req.ReasoningBudget == nil || *req.ReasoningBudget != tc.wantBudget {
				t.Errorf("got ReasoningBudget %v, want %v", req.ReasoningBudget, tc.wantBudget)
			}
			if req.ReasoningEffort != tc.wantEffort {
				t.Errorf("got ReasoningEffort %q, want %q", req.ReasoningEffort, tc.wantEffort)
			}
		})
	}
}

func TestHarnessRunScenariosBatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h := NewHarness(HarnessConfig{
		Timeout: 15 * time.Second,
	})

	scenarios := DefaultScenarios()
	results, err := h.RunScenarios(ctx, scenarios)
	if err != nil {
		t.Fatalf("RunScenarios failed: %v", err)
	}
	if len(results) != len(scenarios) {
		t.Fatalf("expected %d results, got %d", len(scenarios), len(results))
	}
	for i, res := range results {
		if !res.Success {
			t.Errorf("scenario %d (%s) failed: %s", i, res.ScenarioName, res.Error)
		}
	}
}

func TestHarnessRunScenariosEmpty(t *testing.T) {
	h := NewHarness(HarnessConfig{})
	_, err := h.RunScenarios(context.Background(), nil)
	if err == nil {
		t.Error("expected error when running empty scenarios list")
	}
}

func TestHarnessSetupFailure(t *testing.T) {
	h := NewHarness(HarnessConfig{})
	scenario := Scenario{
		Name: "failing-setup",
		Setup: func(workspaceDir string) error {
			return errors.New("simulated setup error")
		},
	}

	res, err := h.RunScenario(context.Background(), scenario)
	if err != nil {
		t.Fatalf("unexpected harness error: %v", err)
	}
	if res.Success {
		t.Error("expected scenario to fail on setup error")
	}
	if !strings.Contains(res.Error, "simulated setup error") {
		t.Errorf("unexpected error message: %s", res.Error)
	}
}

func TestHarnessValidationFailure(t *testing.T) {
	h := NewHarness(HarnessConfig{Timeout: 5 * time.Second})
	scenario := Scenario{
		Name:   "validation-failure",
		Prompt: "Echo done",
		LLMHandler: func(req engine.ChatCompletionRequest, turn int) (engine.ChatCompletionResponse, error) {
			return MockAssistantText("turn-1", "all done"), nil
		},
		Validate: func(ctx context.Context, workspaceDir string, res *Result) error {
			return errors.New("assertion failed: missing expected artifact")
		},
	}

	res, err := h.RunScenario(context.Background(), scenario)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Success {
		t.Error("expected scenario to fail validation")
	}
	if !strings.Contains(res.Error, "assertion failed") {
		t.Errorf("unexpected error message: %s", res.Error)
	}
}

func TestHarnessNoLLMHandler(t *testing.T) {
	h := NewHarness(HarnessConfig{Timeout: 3 * time.Second})
	scenario := Scenario{
		Name:       "nil-handler",
		Prompt:     "Do something",
		LLMHandler: nil,
	}

	res, err := h.RunScenario(context.Background(), scenario)
	if err != nil {
		t.Fatalf("unexpected harness error: %v", err)
	}
	if res.Success {
		t.Error("expected failure with nil LLM handler")
	}
}

func TestHarnessCustomInstructionsAndRole(t *testing.T) {
	h := NewHarness(HarnessConfig{
		Role:               "CustomRole",
		CustomInstructions: "Always be concise.",
		Timeout:            5 * time.Second,
	})

	var capturedRole string
	scenario := Scenario{
		Name:   "custom-instructions",
		Prompt: "Test custom settings",
		LLMHandler: func(req engine.ChatCompletionRequest, turn int) (engine.ChatCompletionResponse, error) {
			if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
				capturedRole = req.Messages[0].Content
			}
			return MockAssistantText("turn-1", "verified"), nil
		},
	}

	res, err := h.RunScenario(context.Background(), scenario)
	if err != nil {
		t.Fatalf("unexpected harness error: %v", err)
	}
	if !res.Success {
		t.Errorf("scenario failed: %s", res.Error)
	}
	if !strings.Contains(capturedRole, "CustomRole") {
		t.Errorf("system prompt missing CustomRole: %s", capturedRole)
	}
	if !strings.Contains(capturedRole, "Always be concise.") {
		t.Errorf("system prompt missing custom instructions: %s", capturedRole)
	}
}

func TestHarnessContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	h := NewHarness(HarnessConfig{Timeout: 5 * time.Second})
	scenario := Scenario{
		Name:   "canceled-scenario",
		Prompt: "Will not finish",
		LLMHandler: func(req engine.ChatCompletionRequest, turn int) (engine.ChatCompletionResponse, error) {
			time.Sleep(100 * time.Millisecond)
			return MockAssistantText("turn-1", "done"), nil
		},
	}

	res, err := h.RunScenario(ctx, scenario)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestScenarioValidationBranches(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// 1. TDD Scenario Validation Branches
	tdd := NewTDDScenario()

	// Missing write_file
	if err := tdd.Validate(ctx, tmpDir, &Result{ToolCallCounts: map[string]int{}}); err == nil {
		t.Error("expected error on missing write_file")
	}

	// Missing bash
	if err := tdd.Validate(ctx, tmpDir, &Result{ToolCallCounts: map[string]int{"write_file": 1}}); err == nil {
		t.Error("expected error on missing bash")
	}

	// Missing calc.go file
	if err := tdd.Validate(ctx, tmpDir, &Result{ToolCallCounts: map[string]int{"write_file": 1, "bash": 1}}); err == nil {
		t.Error("expected error on missing calc.go")
	}

	// Missing Add signature in calc.go
	_ = os.WriteFile(filepath.Join(tmpDir, "calc.go"), []byte("package mathpkg\n"), 0644)
	if err := tdd.Validate(ctx, tmpDir, &Result{ToolCallCounts: map[string]int{"write_file": 1, "bash": 1}}); err == nil {
		t.Error("expected error on missing Add function in calc.go")
	}

	// Test failure during verification
	_ = os.WriteFile(filepath.Join(tmpDir, "calc.go"), []byte("package mathpkg\nfunc Add(a, b int) int { return 0 }\n"), 0644)
	_ = tdd.Setup(tmpDir)
	if err := tdd.Validate(ctx, tmpDir, &Result{ToolCallCounts: map[string]int{"write_file": 1, "bash": 1}}); err == nil {
		t.Error("expected error when go test fails")
	}

	// 2. Bug Repair Scenario Validation Branches
	bug := NewBugRepairScenario()
	tmpBug := t.TempDir()

	// Bash < 2
	if err := bug.Validate(ctx, tmpBug, &Result{ToolCallCounts: map[string]int{"bash": 1}}); err == nil {
		t.Error("expected error on bash < 2")
	}

	// Missing read_file
	if err := bug.Validate(ctx, tmpBug, &Result{ToolCallCounts: map[string]int{"bash": 2}}); err == nil {
		t.Error("expected error on missing read_file")
	}

	// Missing edit_file
	if err := bug.Validate(ctx, tmpBug, &Result{ToolCallCounts: map[string]int{"bash": 2, "read_file": 1}}); err == nil {
		t.Error("expected error on missing edit_file")
	}

	// Missing calc.go file
	if err := bug.Validate(ctx, tmpBug, &Result{ToolCallCounts: map[string]int{"bash": 2, "read_file": 1, "edit_file": 1}}); err == nil {
		t.Error("expected error on missing calc.go")
	}

	// calc.go still contains buggy addition
	_ = os.WriteFile(filepath.Join(tmpBug, "calc.go"), []byte("package calc\nfunc Multiply(a,b int) int { return a + b }\n"), 0644)
	if err := bug.Validate(ctx, tmpBug, &Result{ToolCallCounts: map[string]int{"bash": 2, "read_file": 1, "edit_file": 1}}); err == nil {
		t.Error("expected error when calc.go still has addition")
	}

	// calc.go missing multiplication logic
	_ = os.WriteFile(filepath.Join(tmpBug, "calc.go"), []byte("package calc\nfunc Multiply(a,b int) int { return 0 }\n"), 0644)
	if err := bug.Validate(ctx, tmpBug, &Result{ToolCallCounts: map[string]int{"bash": 2, "read_file": 1, "edit_file": 1}}); err == nil {
		t.Error("expected error when calc.go missing multiplication")
	}

	// Test fails
	_ = bug.Setup(tmpBug)
	_ = os.WriteFile(filepath.Join(tmpBug, "calc.go"), []byte("package calc\nfunc Multiply(a,b int) int { return a * b + 1 }\n"), 0644)
	if err := bug.Validate(ctx, tmpBug, &Result{ToolCallCounts: map[string]int{"bash": 2, "read_file": 1, "edit_file": 1}}); err == nil {
		t.Error("expected error when post-repair test fails")
	}

	// 3. Reasoning Budget Scenario Validation Branches
	prioScenario := NewReasoningBudgetScenario(1)

	// No captured requests
	if err := prioScenario.Validate(ctx, tmpDir, &Result{}); err == nil {
		t.Error("expected error on empty CapturedRequests")
	}

	// Nil reasoning budget
	if err := prioScenario.Validate(ctx, tmpDir, &Result{CapturedRequests: []engine.ChatCompletionRequest{{}}}); err == nil {
		t.Error("expected error on nil reasoning budget")
	}

	// Mismatched budget
	wrongBudget := 0.50
	if err := prioScenario.Validate(ctx, tmpDir, &Result{CapturedRequests: []engine.ChatCompletionRequest{
		{ReasoningBudget: &wrongBudget, ReasoningEffort: "high"},
	}}); err == nil {
		t.Error("expected error on mismatched budget")
	}

	// Mismatched effort
	p1Budget := 0.95
	if err := prioScenario.Validate(ctx, tmpDir, &Result{CapturedRequests: []engine.ChatCompletionRequest{
		{ReasoningBudget: &p1Budget, ReasoningEffort: "low"},
	}}); err == nil {
		t.Error("expected error on mismatched effort")
	}
}
