// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/boggycreek/fleet/agent/pkg/engine"
	"github.com/boggycreek/fleet/agent/pkg/runtime"
	"github.com/boggycreek/fleet/agent/pkg/tools"
)

// MockLLMHandler is a function that produces a ChatCompletionResponse for an incoming request.
// turn is the 1-indexed turn count of requests dispatched during scenario execution.
type MockLLMHandler func(req engine.ChatCompletionRequest, turn int) (engine.ChatCompletionResponse, error)

// Result captures the execution outcome, performance telemetry, and errors of a scenario run.
type Result struct {
	ScenarioName     string                         `json:"scenario_name"`
	Success          bool                           `json:"success"`
	Turns            int                            `json:"turns"`
	ToolCallCounts   map[string]int                 `json:"tool_call_counts"`
	Duration         time.Duration                  `json:"duration"`
	Error            string                         `json:"error,omitempty"`
	Output           string                         `json:"output,omitempty"`
	CapturedRequests []engine.ChatCompletionRequest `json:"captured_requests,omitempty"`
}

// Scenario encapsulates an isolated agent evaluation benchmark.
type Scenario struct {
	Name             string
	Description      string
	Prompt           string
	Role             string
	Setup            func(workspaceDir string) error
	LLMHandler       MockLLMHandler
	PriorityResolver engine.TaskPriorityResolver
	Validate         func(ctx context.Context, workspaceDir string, res *Result) error
}

// HarnessConfig configures the evaluation test harness execution parameters.
type HarnessConfig struct {
	MaxTurns           int
	MaxToolIterations  int
	Timeout            time.Duration
	Role               string
	CustomInstructions string
}

// Harness manages isolated workspace environments and executes evaluation scenarios.
type Harness struct {
	cfg HarnessConfig
}

// NewHarness instantiates a new evaluation harness with defaults applied.
func NewHarness(cfg HarnessConfig) *Harness {
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = 20
	}
	if cfg.MaxToolIterations <= 0 {
		cfg.MaxToolIterations = 10
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Role == "" {
		cfg.Role = "Software Engineer"
	}
	return &Harness{cfg: cfg}
}

// RunScenario executes a scenario inside an isolated temporary workspace.
func (h *Harness) RunScenario(ctx context.Context, s Scenario) (*Result, error) {
	workspaceDir, err := os.MkdirTemp("", "fleet-eval-ws-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary workspace: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(workspaceDir)
	}()

	startTime := time.Now()
	res := &Result{
		ScenarioName:   s.Name,
		ToolCallCounts: make(map[string]int),
	}

	if s.Setup != nil {
		if err := s.Setup(workspaceDir); err != nil {
			res.Success = false
			res.Error = fmt.Sprintf("scenario setup failed: %v", err)
			res.Duration = time.Since(startTime)
			return res, nil
		}
	}

	var (
		reqMu        sync.Mutex
		turnCount    int32
		capturedReqs []engine.ChatCompletionRequest
	)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		currentTurn := int(atomic.AddInt32(&turnCount, 1))

		var req engine.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("failed to decode request: %v", err), http.StatusBadRequest)
			return
		}

		reqMu.Lock()
		capturedReqs = append(capturedReqs, req)
		reqMu.Unlock()

		if s.LLMHandler == nil {
			http.Error(w, "no mock LLM handler configured for scenario", http.StatusInternalServerError)
			return
		}

		resp, err := s.LLMHandler(req, currentTurn)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	reg := tools.NewRegistry()
	if err := tools.RegisterBuiltinTools(reg, workspaceDir); err != nil {
		return nil, fmt.Errorf("failed to register builtin tools: %w", err)
	}

	role := s.Role
	if role == "" {
		role = h.cfg.Role
	}

	llm := engine.NewLLMClient(ts.URL, "eval-key", "eval-model", h.cfg.Timeout)
	eng := engine.NewREPLEngine(llm, reg, workspaceDir, role)
	eng.SetPollInterval(10 * time.Millisecond)
	eng.SetMaxTurns(h.cfg.MaxTurns)
	eng.SetMaxToolIterations(h.cfg.MaxToolIterations)
	if h.cfg.CustomInstructions != "" {
		eng.SetCustomInstructions(h.cfg.CustomInstructions)
	}
	if s.PriorityResolver != nil {
		eng.SetPriorityResolver(s.PriorityResolver)
	}

	bus := runtime.NewEventBus()
	defer bus.Close()

	stateFile := filepath.Join(workspaceDir, "eval-agent-state.json")
	state := runtime.NewSharedState(stateFile, "eval-agent")

	eng.AppendMessage(engine.ChatMessage{Role: "user", Content: s.Prompt})

	scenarioCtx, cancel := context.WithTimeout(ctx, h.cfg.Timeout)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- eng.Start(scenarioCtx, bus, state)
	}()

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	done := false
	for !done {
		select {
		case <-scenarioCtx.Done():
			done = true
		case <-ticker.C:
			st := state.Read()
			turns := int(atomic.LoadInt32(&turnCount))
			if turns > 0 && (st.Status == "idle" || st.Status == "cancelled" || st.Status == "error") {
				done = true
			}
		}
	}
	cancel()
	_ = <-errChan

	reqMu.Lock()
	res.CapturedRequests = append([]engine.ChatCompletionRequest(nil), capturedReqs...)
	reqMu.Unlock()
	res.Turns = int(atomic.LoadInt32(&turnCount))
	res.Duration = time.Since(startTime)

	history := eng.GetHistory()
	for _, msg := range history {
		if msg.Role == "assistant" {
			if len(msg.ToolCalls) > 0 {
				for _, tc := range msg.ToolCalls {
					res.ToolCallCounts[tc.Function.Name]++
				}
			} else {
				res.Output = msg.Content
			}
		}
	}

	finalState := state.Read()
	if finalState.Status == "error" && res.Error == "" {
		res.Error = finalState.CurrentActivity
	}

	if s.Validate != nil {
		if valErr := s.Validate(ctx, workspaceDir, res); valErr != nil {
			res.Success = false
			res.Error = valErr.Error()
		} else if finalState.Status == "error" {
			res.Success = false
		} else {
			res.Success = true
		}
	} else {
		res.Success = (res.Error == "" && finalState.Status != "error")
	}

	return res, nil
}

// RunScenarios runs a slice of scenarios sequentially and collects their results.
func (h *Harness) RunScenarios(ctx context.Context, scenarios []Scenario) ([]*Result, error) {
	if len(scenarios) == 0 {
		return nil, errors.New("scenarios list cannot be empty")
	}
	var results []*Result
	for _, s := range scenarios {
		res, err := h.RunScenario(ctx, s)
		if err != nil {
			return results, err
		}
		results = append(results, res)
	}
	return results, nil
}
