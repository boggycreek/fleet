// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package eval

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/boggycreek/fleet/agent/pkg/engine"
)

// MockAssistantText creates a ChatCompletionResponse returning assistant text content.
func MockAssistantText(id, content string) engine.ChatCompletionResponse {
	return engine.ChatCompletionResponse{
		ID:    id,
		Model: "eval-model",
		Choices: []engine.ChatChoice{
			{
				Index: 0,
				Message: engine.ChatMessage{
					Role:    "assistant",
					Content: content,
				},
				FinishReason: "stop",
			},
		},
	}
}

// MockAssistantToolCall creates a ChatCompletionResponse requesting a single tool invocation.
func MockAssistantToolCall(id, toolName, toolArgs string) engine.ChatCompletionResponse {
	return engine.ChatCompletionResponse{
		ID:    id,
		Model: "eval-model",
		Choices: []engine.ChatChoice{
			{
				Index: 0,
				Message: engine.ChatMessage{
					Role: "assistant",
					ToolCalls: []engine.ToolCall{
						{
							ID:   fmt.Sprintf("call-%s", id),
							Type: "function",
							Function: engine.ToolFunctionCall{
								Name:      toolName,
								Arguments: toolArgs,
							},
						},
					},
				},
				FinishReason: "tool_calls",
			},
		},
	}
}

// NewTDDScenario constructs a TDD code generation and verification benchmark scenario.
func NewTDDScenario() Scenario {
	return Scenario{
		Name:        "tdd-code-generation",
		Description: "Implements a Go function to satisfy an existing unit test and verifies via go test",
		Prompt:      "Implement the Add function in calc.go to make the existing unit test pass, and verify with go test.",
		Role:        "Software Engineer",
		Setup: func(workspaceDir string) error {
			goModContent := "module mathpkg\n\ngo 1.22\n"
			if err := os.WriteFile(filepath.Join(workspaceDir, "go.mod"), []byte(goModContent), 0644); err != nil {
				return err
			}

			testContent := `package mathpkg

import "testing"

func TestAdd(t *testing.T) {
	if Add(15, 25) != 40 {
		t.Fatalf("expected 40, got %d", Add(15, 25))
	}
}
`
			return os.WriteFile(filepath.Join(workspaceDir, "calc_test.go"), []byte(testContent), 0644)
		},
		LLMHandler: func(req engine.ChatCompletionRequest, turn int) (engine.ChatCompletionResponse, error) {
			switch turn {
			case 1:
				// Write implementation
				writeArgs := `{"path":"calc.go","content":"package mathpkg\n\n// Add sums two integers.\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"}`
				return MockAssistantToolCall("tdd-turn-1", "write_file", writeArgs), nil
			case 2:
				// Execute verification command
				bashArgs := `{"command":"go test -v ./..."}`
				return MockAssistantToolCall("tdd-turn-2", "bash", bashArgs), nil
			default:
				// Final report
				return MockAssistantText("tdd-turn-3", "Implemented Add function and verified all unit tests pass."), nil
			}
		},
		Validate: func(ctx context.Context, workspaceDir string, res *Result) error {
			if res.ToolCallCounts["write_file"] < 1 {
				return errors.New("expected at least one write_file tool invocation")
			}
			if res.ToolCallCounts["bash"] < 1 {
				return errors.New("expected at least one bash tool invocation")
			}

			calcPath := filepath.Join(workspaceDir, "calc.go")
			data, err := os.ReadFile(calcPath)
			if err != nil {
				return fmt.Errorf("calc.go was not created: %w", err)
			}
			if !strings.Contains(string(data), "func Add(a, b int) int") {
				return errors.New("calc.go does not contain Add function signature")
			}

			cmd := exec.CommandContext(ctx, "go", "test", "-v", "./...")
			cmd.Dir = workspaceDir
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("verification test execution failed: %s (%w)", string(out), err)
			}
			return nil
		},
	}
}

// NewBugRepairScenario constructs a bug diagnosis, line inspection, and repair benchmark scenario.
func NewBugRepairScenario() Scenario {
	return Scenario{
		Name:        "bug-diagnosis-and-repair",
		Description: "Diagnoses failing tests, inspects source lines, surgically repairs bug with edit_file, and verifies fix",
		Prompt:      "Run tests, diagnose the failure, inspect lines in calc.go, and repair the Multiply function.",
		Role:        "Software Engineer",
		Setup: func(workspaceDir string) error {
			goModContent := "module calc\n\ngo 1.22\n"
			if err := os.WriteFile(filepath.Join(workspaceDir, "go.mod"), []byte(goModContent), 0644); err != nil {
				return err
			}

			buggyCode := `package calc

// Multiply computes the product of two integers.
func Multiply(a, b int) int {
	return a + b // BUG: addition instead of multiplication
}
`
			if err := os.WriteFile(filepath.Join(workspaceDir, "calc.go"), []byte(buggyCode), 0644); err != nil {
				return err
			}

			testCode := `package calc

import "testing"

func TestMultiply(t *testing.T) {
	if Multiply(6, 7) != 42 {
		t.Fatalf("expected 42, got %d", Multiply(6, 7))
	}
}
`
			return os.WriteFile(filepath.Join(workspaceDir, "calc_test.go"), []byte(testCode), 0644)
		},
		LLMHandler: func(req engine.ChatCompletionRequest, turn int) (engine.ChatCompletionResponse, error) {
			switch turn {
			case 1:
				// Initial test run fails
				return MockAssistantToolCall("bug-turn-1", "bash", `{"command":"go test -v ./..."}`), nil
			case 2:
				// Inspect buggy lines via read_file with line ranges
				return MockAssistantToolCall("bug-turn-2", "read_file", `{"path":"calc.go","start_line":1,"end_line":8}`), nil
			case 3:
				// Precise edit_file replacing bug
				editArgs := `{"path":"calc.go","old_text":"return a + b // BUG: addition instead of multiplication","new_text":"return a * b"}`
				return MockAssistantToolCall("bug-turn-3", "edit_file", editArgs), nil
			case 4:
				// Verification test run passes
				return MockAssistantToolCall("bug-turn-4", "bash", `{"command":"go test -v ./..."}`), nil
			default:
				// Final report
				return MockAssistantText("bug-turn-5", "Diagnosed error trace, inspected source lines, repaired Multiply function with edit_file, and verified test suite passes."), nil
			}
		},
		Validate: func(ctx context.Context, workspaceDir string, res *Result) error {
			if res.ToolCallCounts["bash"] < 2 {
				return fmt.Errorf("expected at least 2 bash executions (initial and verification), got %d", res.ToolCallCounts["bash"])
			}
			if res.ToolCallCounts["read_file"] < 1 {
				return errors.New("expected read_file line inspection tool invocation")
			}
			if res.ToolCallCounts["edit_file"] < 1 {
				return errors.New("expected edit_file tool invocation")
			}

			calcPath := filepath.Join(workspaceDir, "calc.go")
			data, err := os.ReadFile(calcPath)
			if err != nil {
				return fmt.Errorf("failed to read repaired file: %w", err)
			}
			content := string(data)
			if strings.Contains(content, "return a + b") {
				return errors.New("repaired file still contains buggy addition logic")
			}
			if !strings.Contains(content, "return a * b") {
				return errors.New("repaired file does not contain expected multiplication logic")
			}

			cmd := exec.CommandContext(ctx, "go", "test", "-v", "./...")
			cmd.Dir = workspaceDir
			out, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("post-repair verification tests failed: %s (%w)", string(out), err)
			}
			return nil
		},
	}
}

// NewReasoningBudgetScenario constructs a scenario testing dynamic reasoning budget allocation based on Beads priority.
func NewReasoningBudgetScenario(priority int) Scenario {
	return Scenario{
		Name:             fmt.Sprintf("reasoning-budget-p%d", priority),
		Description:      fmt.Sprintf("Verifies engine adapts reasoning budget and effort for active Beads priority P%d", priority),
		Prompt:           fmt.Sprintf("Inspect task directive and report reasoning allocation for priority P%d.", priority),
		Role:             "Software Engineer",
		PriorityResolver: &engine.StaticTaskResolver{Priority: priority, Found: true},
		LLMHandler: func(req engine.ChatCompletionRequest, turn int) (engine.ChatCompletionResponse, error) {
			return MockAssistantText("prio-turn-1", fmt.Sprintf("Completed analysis with allocated reasoning for P%d.", priority)), nil
		},
		Validate: func(ctx context.Context, workspaceDir string, res *Result) error {
			if len(res.CapturedRequests) == 0 {
				return errors.New("expected at least one captured LLM request")
			}
			lastReq := res.CapturedRequests[len(res.CapturedRequests)-1]

			wantBudget, wantEffort := engine.MapPriorityToReasoning(priority)
			if lastReq.ReasoningBudget == nil {
				return fmt.Errorf("expected ReasoningBudget to be non-nil for priority P%d", priority)
			}
			if *lastReq.ReasoningBudget != wantBudget {
				return fmt.Errorf("ReasoningBudget mismatch: got %v, want %v", *lastReq.ReasoningBudget, wantBudget)
			}
			if lastReq.ReasoningEffort != wantEffort {
				return fmt.Errorf("ReasoningEffort mismatch: got %q, want %q", lastReq.ReasoningEffort, wantEffort)
			}
			return nil
		},
	}
}

// DefaultScenarios returns the standard evaluation scenario benchmark suite.
func DefaultScenarios() []Scenario {
	return []Scenario{
		NewTDDScenario(),
		NewBugRepairScenario(),
		NewReasoningBudgetScenario(1),
	}
}
