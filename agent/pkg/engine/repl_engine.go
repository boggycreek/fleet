// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/boggycreek/fleet/agent/pkg/runtime"
	"github.com/boggycreek/fleet/agent/pkg/tools"
	"github.com/boggycreek/fleet/backplane/pkg/libbp"
)

const (
	// DefaultPollInterval is the default idle wait interval before polling.
	DefaultPollInterval = 200 * time.Millisecond

	// DefaultMaxToolIterations is the maximum consecutive tool execution turns before forced conclusion.
	DefaultMaxToolIterations = 25
)

// TurnResponder delivers conversational replies back to message originators over the backplane.
type TurnResponder interface {
	SendReply(ctx context.Context, recipient, content string) error
}

// REPLEngine drives the multi-turn cognitive agent loop.
type REPLEngine struct {
	mu                 sync.RWMutex
	llm                *LLMClient
	registry           *tools.Registry
	responder          TurnResponder
	priorityResolver   TaskPriorityResolver
	agentID            string
	humanName          string
	workspaceDir       string
	role               string
	customInstructions string
	pollInterval       time.Duration
	maxTurns           int
	maxToolIterations  int
	history            []ChatMessage
	inboundQueue       []runtime.Event
	activeSender       string
}

// NewREPLEngine constructs a new REPLEngine instance.
func NewREPLEngine(llm *LLMClient, registry *tools.Registry, workspaceDir, role string) *REPLEngine {
	return &REPLEngine{
		llm:               llm,
		registry:          registry,
		priorityResolver:  NewBeadsTaskResolver(workspaceDir),
		workspaceDir:      workspaceDir,
		role:              role,
		pollInterval:      DefaultPollInterval,
		maxTurns:          DefaultMaxTurns,
		maxToolIterations: DefaultMaxToolIterations,
		history:           make([]ChatMessage, 0),
		inboundQueue:      make([]runtime.Event, 0),
	}
}

// SetPriorityResolver configures the task priority resolver for dynamic reasoning budget.
func (e *REPLEngine) SetPriorityResolver(r TaskPriorityResolver) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.priorityResolver = r
}

// PriorityResolver returns the configured TaskPriorityResolver.
func (e *REPLEngine) PriorityResolver() TaskPriorityResolver {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.priorityResolver
}

func (e *REPLEngine) getPriorityResolver() TaskPriorityResolver {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.priorityResolver
}

// SetResponder sets the TurnResponder for delivering conversational replies back to the sender.
func (e *REPLEngine) SetResponder(r TurnResponder) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.responder = r
}

// SetAgentID configures the engine agent identity.
func (e *REPLEngine) SetAgentID(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.agentID = id
}

// SetHumanName configures the human operator identity (e.g. "brian", "operator").
func (e *REPLEngine) SetHumanName(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.humanName = strings.ToLower(strings.TrimSpace(name))
}

func (e *REPLEngine) getHumanName() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.humanName != "" {
		return e.humanName
	}
	return "operator"
}

func (e *REPLEngine) setActiveSender(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.activeSender = s
}

func (e *REPLEngine) getActiveSender() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.activeSender
}

// isHumanSender checks if the sender matches known human operator identities.
func isHumanSender(sender, humanName string) bool {
	s := strings.ToLower(strings.TrimSpace(sender))
	if s == "" {
		return false
	}
	if s == "human" || s == "operator" || s == "brian" {
		return true
	}
	h := strings.ToLower(strings.TrimSpace(humanName))
	if h != "" && s == h {
		return true
	}
	return false
}

// isAutomatedNotification checks if the message content matches automated status / completion prefixes.
func isAutomatedNotification(content string) bool {
	lower := strings.ToLower(strings.TrimSpace(content))
	prefixes := []string{
		"completed directive:",
		"execution failed",
		"understood. directive queued",
		"status:",
		"online (container started)",
		"ok (",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

func extractEventInfo(payload any) (string, string) {
	if payload == nil {
		return "", ""
	}
	switch v := payload.(type) {
	case libbp.Message:
		return v.Sender, v.Content
	case *libbp.Message:
		if v != nil {
			return v.Sender, v.Content
		}
	case runtime.BackplaneEnvelope:
		return v.Sender, v.Payload
	case *runtime.BackplaneEnvelope:
		if v != nil {
			return v.Sender, v.Payload
		}
	}
	return "", ""
}

func extractEventSender(payload any) string {
	s, _ := extractEventInfo(payload)
	return s
}

func (e *REPLEngine) enqueueInbound(evt runtime.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.inboundQueue = append(e.inboundQueue, evt)
}

func (e *REPLEngine) drainInboundQueue() []runtime.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.inboundQueue) == 0 {
		return nil
	}
	events := make([]runtime.Event, len(e.inboundQueue))
	copy(events, e.inboundQueue)
	e.inboundQueue = e.inboundQueue[:0]
	return events
}

// ID returns the unique subsystem identifier.
func (e *REPLEngine) ID() string {
	return "repl-main"
}

// SetPollInterval sets the idle event wait interval.
func (e *REPLEngine) SetPollInterval(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if d > 0 {
		e.pollInterval = d
	}
}

// SetMaxTurns configures the history compaction threshold.
func (e *REPLEngine) SetMaxTurns(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n > 0 {
		e.maxTurns = n
	}
}

// SetMaxToolIterations configures the maximum tool calls per cognitive turn.
func (e *REPLEngine) SetMaxToolIterations(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n > 0 {
		e.maxToolIterations = n
	}
}

// SetCustomInstructions configures custom system instructions.
func (e *REPLEngine) SetCustomInstructions(instructions string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.customInstructions = instructions
}

// GetHistory returns a snapshot copy of the conversation history.
func (e *REPLEngine) GetHistory() []ChatMessage {
	e.mu.RLock()
	defer e.mu.RUnlock()
	copied := make([]ChatMessage, len(e.history))
	copy(copied, e.history)
	return copied
}

// SetHistory overwrites conversation history.
func (e *REPLEngine) SetHistory(history []ChatMessage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.history = make([]ChatMessage, len(history))
	copy(e.history, history)
}

// AppendMessage appends a message to conversation history.
func (e *REPLEngine) AppendMessage(msg ChatMessage) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.history = append(e.history, msg)
}

// hasPendingUserTurn checks if the conversation ends with an unfulfilled user or tool message.
func (e *REPLEngine) hasPendingUserTurn() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.history) == 0 {
		return false
	}
	lastRole := e.history[len(e.history)-1].Role
	return lastRole == "user" || lastRole == "tool"
}

// Start runs the REPL cognitive loop until ctx is canceled or emergency stop is received.
func (e *REPLEngine) Start(ctx context.Context, bus *runtime.EventBus, state *runtime.SharedState) error {
	if bus == nil || state == nil {
		return errors.New("event bus and shared state cannot be nil")
	}

	agentID := state.Read().AgentID
	if agentID == "" {
		agentID = "fleet-agent"
	}
	e.mu.Lock()
	if e.agentID == "" {
		e.agentID = agentID
	}
	// Initialize history with foundational system prompt if missing
	if len(e.history) == 0 || e.history[0].Role != "system" {
		sysPrompt := BuildSystemPrompt(agentID, e.role, e.workspaceDir, e.customInstructions)
		e.history = append([]ChatMessage{{
			Role:    "system",
			Content: sysPrompt,
		}}, e.history...)
	}
	e.mu.Unlock()

	_ = state.Update(func(s *runtime.AgentState) {
		s.Status = "idle"
		s.CurrentActivity = "Ready"
	})

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// 1. Drain pending events
		hasNewDirective := false
		for _, evt := range e.drainInboundQueue() {
			e.AppendMessage(FormatEventMessage(evt))
			if s, content := extractEventInfo(evt.Payload); s != "" {
				if isHumanSender(s, e.getHumanName()) && !isAutomatedNotification(content) {
					e.setActiveSender(s)
				}
			}
			hasNewDirective = true
		}
		for {
			evt, ok := bus.Poll()
			if !ok {
				break
			}
			if evt.Priority == runtime.P0_Control {
				_ = state.Update(func(s *runtime.AgentState) {
					s.Status = "cancelled"
					s.CurrentActivity = "Emergency stop received"
				})
				return nil
			}
			if evt.Priority == runtime.P1_HighPriority || evt.Priority == runtime.P2_StandardAsync {
				e.AppendMessage(FormatEventMessage(evt))
				if s, content := extractEventInfo(evt.Payload); s != "" {
					if isHumanSender(s, e.getHumanName()) && !isAutomatedNotification(content) {
						e.setActiveSender(s)
					}
				}
				hasNewDirective = true
			}
		}

		// 2. If we have work to do, execute cognitive turn
		if hasNewDirective || e.hasPendingUserTurn() {
			if err := e.executeTurn(ctx, bus, state); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				// Sleep brief backoff before retry on transient error
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(50 * time.Millisecond):
				}
			}
			continue
		}

		// 3. Idle wait for incoming events
		waitInterval := e.getPollInterval()
		waitCtx, cancel := context.WithTimeout(ctx, waitInterval)
		evt, err := bus.Next(waitCtx)
		cancel()

		if err != nil {
			// Timeout or cancellation; loop around
			continue
		}

		// Process arrived event
		if evt.Priority == runtime.P0_Control {
			_ = state.Update(func(s *runtime.AgentState) {
				s.Status = "cancelled"
				s.CurrentActivity = "Emergency stop received"
			})
			return nil
		}
		if evt.Priority == runtime.P1_HighPriority || evt.Priority == runtime.P2_StandardAsync {
			e.AppendMessage(FormatEventMessage(evt))
			if s, content := extractEventInfo(evt.Payload); s != "" {
				if isHumanSender(s, e.getHumanName()) && !isAutomatedNotification(content) {
					e.setActiveSender(s)
				}
			}
			_ = e.executeTurn(ctx, bus, state)
		}
	}
}

func (e *REPLEngine) getPollInterval() time.Duration {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.pollInterval
}

// executeTurn executes a multi-turn LLM reasoning and tool execution sequence.
func (e *REPLEngine) executeTurn(ctx context.Context, bus *runtime.EventBus, state *runtime.SharedState) error {
	_ = state.Update(func(s *runtime.AgentState) {
		s.Status = "thinking"
		s.CurrentActivity = "Reasoning..."
	})

	e.mu.RLock()
	maxIterations := e.maxToolIterations
	maxTurns := e.maxTurns
	e.mu.RUnlock()

	var (
		cachedBudget *float64
		cachedEffort string
	)
	if resolver := e.getPriorityResolver(); resolver != nil {
		if prio, found, err := resolver.GetActiveTaskPriority(ctx); err == nil && found {
			budget, effort := MapPriorityToReasoning(prio)
			cachedBudget = &budget
			cachedEffort = effort
			fmt.Printf("[repl] Active task priority P%d -> reasoning_budget=%.2f, reasoning_effort=%s\n", prio, budget, effort)
		}
	}

	didSendExternalMessage := false
	executedToolCalls := make(map[string]string)

	for iter := 0; iter < maxIterations; iter++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Check for emergency events interrupting current tool loop
		for {
			evt, ok := bus.Poll()
			if !ok {
				break
			}
			if evt.Priority == runtime.P0_Control {
				_ = state.Update(func(s *runtime.AgentState) {
					s.Status = "cancelled"
					s.CurrentActivity = "Emergency stop received during turn"
				})
				return nil
			}
			if evt.Priority == runtime.P1_HighPriority {
				// Inject high-priority directive immediately into history
				e.AppendMessage(FormatEventMessage(evt))
				if s, content := extractEventInfo(evt.Payload); s != "" {
					if isHumanSender(s, e.getHumanName()) && !isAutomatedNotification(content) {
						e.setActiveSender(s)
					}
				}
			} else if evt.Priority == runtime.P2_StandardAsync {
				// Buffer P2 events into inbound queue without dropping
				e.enqueueInbound(evt)
			}
		}

		// Prepare LLM request with historical observation pruning and history compaction
		history := e.GetHistory()
		history = PruneHistoricalObservations(history, DefaultKeepRecentTurns, DefaultObservationPruneThreshold)
		msgs := CompactHistory(history, maxTurns)
		var openAITools []map[string]any
		if e.registry != nil {
			openAITools = e.registry.ToOpenAITools()
		}

		req := ChatCompletionRequest{
			Model:           e.llm.Model(),
			Messages:        msgs,
			Tools:           openAITools,
			ReasoningBudget: cachedBudget,
			ReasoningEffort: cachedEffort,
		}

		resp, err := e.llm.ChatCompletion(ctx, req)
		if err != nil {
			_ = state.Update(func(s *runtime.AgentState) {
				s.Status = "error"
				s.CurrentActivity = fmt.Sprintf("LLM error: %v", err)
			})
			return err
		}

		if len(resp.Choices) == 0 {
			return errors.New("empty choices from llm")
		}

		assistantMsg := resp.Choices[0].Message
		assistantMsg.Role = "assistant"
		if len(assistantMsg.ToolCalls) == 0 {
			if fallbackCalls := parseFallbackToolCalls(assistantMsg.Content); len(fallbackCalls) > 0 {
				assistantMsg.ToolCalls = fallbackCalls
			}
		}
		if len(assistantMsg.ToolCalls) > 0 {
			fmt.Printf("[repl] Iteration %d: model requested %d tool call(s)\n", iter+1, len(assistantMsg.ToolCalls))
		} else {
			fmt.Printf("[repl] Iteration %d: final text: %s\n", iter+1, strings.TrimSpace(assistantMsg.Content))
		}
		e.AppendMessage(assistantMsg)

		// If no tools were invoked, turn execution is finished
		if len(assistantMsg.ToolCalls) == 0 {
			e.mu.RLock()
			responder := e.responder
			sender := e.activeSender
			agentID := e.agentID
			e.mu.RUnlock()
			humanName := e.getHumanName()

			if responder != nil && sender != "" && !didSendExternalMessage && strings.TrimSpace(assistantMsg.Content) != "" {
				if strings.ToLower(sender) != strings.ToLower(agentID) && isHumanSender(sender, humanName) {
					_ = responder.SendReply(ctx, sender, assistantMsg.Content)
				}
			}
			e.setActiveSender("")

			for _, queuedEvt := range e.drainInboundQueue() {
				e.AppendMessage(FormatEventMessage(queuedEvt))
			}
			_ = state.Update(func(s *runtime.AgentState) {
				s.Status = "idle"
				s.CurrentActivity = "Ready"
			})
			return nil
		}

		// Execute tool calls sequentially
		for _, toolCall := range assistantMsg.ToolCalls {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			toolName := toolCall.Function.Name
			toolKey := fmt.Sprintf("%s:%s", toolName, strings.TrimSpace(toolCall.Function.Arguments))
			if prevOutput, isDup := executedToolCalls[toolKey]; isDup && (toolName == "fleet_send_message" || toolName == "fleet_broadcast" || toolName == "fleet_read_inbox") {
				fmt.Printf("[repl] Iteration %d: skipping duplicate tool %s\n", iter+1, toolName)
				e.AppendMessage(ChatMessage{
					Role:       "tool",
					ToolCallID: toolCall.ID,
					Name:       toolName,
					Content:    prevOutput,
				})
				continue
			}

			fmt.Printf("[repl] Iteration %d: executing tool %s args=%s\n", iter+1, toolName, toolCall.Function.Arguments)
			if toolName == "fleet_send_message" || toolName == "fleet_broadcast" {
				didSendExternalMessage = true
			} else if toolName == "bash" {
				var bashArgs struct {
					Command string `json:"command"`
				}
				cmdToInspect := toolCall.Function.Arguments
				if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &bashArgs); err == nil && bashArgs.Command != "" {
					cmdToInspect = bashArgs.Command
				}
				trimmedCmd := strings.TrimSpace(cmdToInspect)
				if strings.HasPrefix(trimmedCmd, "bp tell") || strings.HasPrefix(trimmedCmd, "bp say") || strings.HasPrefix(trimmedCmd, "bp reply") ||
					strings.HasPrefix(trimmedCmd, "/usr/local/bin/bp tell") || strings.HasPrefix(trimmedCmd, "/usr/local/bin/bp say") || strings.HasPrefix(trimmedCmd, "/usr/local/bin/bp reply") {
					didSendExternalMessage = true
				}
			}

			_ = state.Update(func(s *runtime.AgentState) {
				s.Status = "executing_tool"
				s.CurrentActivity = fmt.Sprintf("Executing tool: %s", toolName)
			})

			// Single-writer lock workspace during tool execution
			state.LockWorkspace()
			output, execErr := "", error(nil)
			if e.registry != nil {
				output, execErr = e.registry.Execute(ctx, toolName, toolCall.Function.Arguments)
			} else {
				execErr = fmt.Errorf("tool registry is nil")
			}
			state.UnlockWorkspace()

			if execErr != nil {
				if strings.TrimSpace(output) != "" {
					output = fmt.Sprintf("Error: %v\nOutput: %s", execErr, strings.TrimSpace(output))
				} else {
					output = fmt.Sprintf("Error: %v", execErr)
				}
			} else if strings.TrimSpace(output) == "" {
				output = "(success)"
			}
			fmt.Printf("[repl] Iteration %d: tool %s output=%s\n", iter+1, toolName, TruncateToolOutput(strings.TrimSpace(output), 120))

			executedToolCalls[toolKey] = output
			truncatedOutput := TruncateToolOutput(output, MaxToolOutputLength)
			e.AppendMessage(ChatMessage{
				Role:       "tool",
				ToolCallID: toolCall.ID,
				Name:       toolName,
				Content:    truncatedOutput,
			})
		}
	}

	// Tool iteration limit reached: append assistant message so hasPendingUserTurn is false
	limitMsg := "Tool execution limit reached for this turn. Summary: max tool iterations reached. Execution paused until further instructions."
	e.AppendMessage(ChatMessage{
		Role:    "assistant",
		Content: limitMsg,
	})
	e.mu.RLock()
	responder := e.responder
	sender := e.activeSender
	agentID := e.agentID
	e.mu.RUnlock()
	humanName := e.getHumanName()

	if responder != nil && sender != "" && !didSendExternalMessage && isHumanSender(sender, humanName) && strings.ToLower(sender) != strings.ToLower(agentID) {
		_ = responder.SendReply(ctx, sender, limitMsg)
	}
	e.setActiveSender("")

	for _, queuedEvt := range e.drainInboundQueue() {
		e.AppendMessage(FormatEventMessage(queuedEvt))
	}

	_ = state.Update(func(s *runtime.AgentState) {
		s.Status = "idle"
		s.CurrentActivity = "Iteration limit reached; ready"
	})

	return nil
}

// parseFallbackToolCalls extracts function calls from raw content text when models
// (e.g., local LLMs under llama.cpp or Ollama) format tool calls as JSON in content
// rather than populating the OpenAI tool_calls structure.
func parseFallbackToolCalls(content string) []ToolCall {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}

	// Strip markdown code fences if present: ```json ... ``` or ``` ... ```
	if strings.HasPrefix(trimmed, "```") {
		lines := strings.Split(trimmed, "\n")
		if len(lines) >= 2 {
			lines = lines[1:]
			if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
				lines = lines[:len(lines)-1]
			}
			trimmed = strings.TrimSpace(strings.Join(lines, "\n"))
		}
	}

	// Helper to extract a single ToolCall from a map
	extractCall := func(m map[string]any, idx int) (ToolCall, bool) {
		name := ""
		if n, ok := m["name"].(string); ok {
			name = n
		} else if n, ok := m["tool"].(string); ok {
			name = n
		} else if n, ok := m["action"].(string); ok {
			name = n
		} else if fn, ok := m["function"].(map[string]any); ok {
			if n, ok := fn["name"].(string); ok {
				name = n
			}
		}
		if name == "" {
			return ToolCall{}, false
		}

		var argsStr string
		var rawArgs any
		if a, ok := m["arguments"]; ok {
			rawArgs = a
		} else if p, ok := m["parameters"]; ok {
			rawArgs = p
		} else if in, ok := m["input"]; ok {
			rawArgs = in
		} else if fn, ok := m["function"].(map[string]any); ok {
			if a, ok := fn["arguments"]; ok {
				rawArgs = a
			}
		}

		switch v := rawArgs.(type) {
		case string:
			argsStr = v
		case map[string]any, []any:
			if b, err := json.Marshal(v); err == nil {
				argsStr = string(b)
			}
		default:
			if rawArgs != nil {
				if b, err := json.Marshal(rawArgs); err == nil {
					argsStr = string(b)
				}
			} else {
				argsStr = "{}"
			}
		}

		return ToolCall{
			ID:   fmt.Sprintf("call-%d-%d", time.Now().UnixNano(), idx),
			Type: "function",
			Function: ToolFunctionCall{
				Name:      name,
				Arguments: argsStr,
			},
		}, true
	}

	// 1. Try streaming parser for single or concatenated (JSON lines / NDJSON) objects
	dec := json.NewDecoder(strings.NewReader(trimmed))
	var streamCalls []ToolCall
	for dec.More() {
		var obj map[string]any
		if err := dec.Decode(&obj); err == nil {
			if tc, ok := extractCall(obj, len(streamCalls)); ok {
				streamCalls = append(streamCalls, tc)
			}
		} else {
			break
		}
	}
	if len(streamCalls) > 0 {
		return streamCalls
	}

	// 2. Try parsing direct JSON array
	var arr []map[string]any
	if err := json.Unmarshal([]byte(trimmed), &arr); err == nil && len(arr) > 0 {
		var calls []ToolCall
		for i, item := range arr {
			if tc, ok := extractCall(item, i); ok {
				calls = append(calls, tc)
			}
		}
		if len(calls) > 0 {
			return calls
		}
	}

	// 3. Scan for embedded JSON line objects in text
	lines := strings.Split(trimmed, "\n")
	var lineCalls []ToolCall
	for _, line := range lines {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "{") && strings.HasSuffix(l, "}") {
			var lObj map[string]any
			if err := json.Unmarshal([]byte(l), &lObj); err == nil {
				if tc, ok := extractCall(lObj, len(lineCalls)); ok {
					lineCalls = append(lineCalls, tc)
				}
			}
		}
	}
	if len(lineCalls) > 0 {
		return lineCalls
	}

	// 4. Scan for embedded JSON object in text (e.g., surrounding reasoning)
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		sub := trimmed[start : end+1]
		var subObj map[string]any
		if err := json.Unmarshal([]byte(sub), &subObj); err == nil {
			if tc, ok := extractCall(subObj, 0); ok {
				return []ToolCall{tc}
			}
		}
	}

	// 5. Scan for blocks of the format: <tool_name>\n{ ... JSON arguments ... } OR embedded { "name": ..., "arguments": ... }
	var blockCalls []ToolCall
	for i := 0; i < len(content); i++ {
		if content[i] == '{' {
			jsonStr, nextIdx := findBalancedJSON(content, i)
			if nextIdx > i {
				// Normalize backticks inside JSON strings to escaped regular quotes or content
				normalizedJSON := jsonStr
				if strings.Contains(jsonStr, "`") {
					// Convert multi-line backtick string literal fields to escaped JSON strings
					normalizedJSON = normalizeJSONBackticks(jsonStr)
				}

				var obj map[string]any
				if err := json.Unmarshal([]byte(normalizedJSON), &obj); err == nil {
					// Check if this JSON object itself specifies a tool call (e.g. { "name": "write_file", "arguments": ... })
					if tc, ok := extractCall(obj, len(blockCalls)); ok && isKnownOrLikelyTool(tc.Function.Name) {
						blockCalls = append(blockCalls, tc)
						i = nextIdx - 1
						continue
					}
				} else if tc, ok := parseLooseToolCall(jsonStr, len(blockCalls)); ok && isKnownOrLikelyTool(tc.Function.Name) {
					blockCalls = append(blockCalls, tc)
					i = nextIdx - 1
					continue
				}

				// Otherwise check preceding token for tool name: <tool_name>\n{ ... }
				before := strings.TrimRight(content[:i], " \t\r\n`")
				lastWord := ""
				for j := len(before) - 1; j >= 0; j-- {
					ch := before[j]
					if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' {
						continue
					}
					lastWord = before[j+1:]
					break
				}
				if lastWord == "" && len(before) > 0 {
					lastWord = before
				}

				if lastWord != "" && isKnownOrLikelyTool(lastWord) {
					if err := json.Unmarshal([]byte(normalizedJSON), &obj); err == nil {
						blockCalls = append(blockCalls, ToolCall{
							ID:   fmt.Sprintf("call-%d-%d", time.Now().UnixNano(), len(blockCalls)),
							Type: "function",
							Function: ToolFunctionCall{
								Name:      lastWord,
								Arguments: normalizedJSON,
							},
						})
						i = nextIdx - 1
					}
				}
			}
		}
	}
	if len(blockCalls) > 0 {
		return blockCalls
	}

	return nil
}

func findBalancedJSON(s string, startIdx int) (string, int) {
	if startIdx >= len(s) || s[startIdx] != '{' {
		return "", -1
	}
	depth := 0
	inString := false
	inBacktick := false
	escape := false

	for i := startIdx; i < len(s); i++ {
		ch := s[i]
		if escape {
			escape = false
			continue
		}
		if ch == '\\' && inString {
			escape = true
			continue
		}
		if ch == '`' && !inString {
			inBacktick = !inBacktick
			continue
		}
		if ch == '"' && !inBacktick {
			inString = !inString
			continue
		}
		if !inString && !inBacktick {
			if ch == '{' {
				depth++
			} else if ch == '}' {
				depth--
				if depth == 0 {
					return s[startIdx : i+1], i + 1
				}
			}
		}
	}
	return "", -1
}

func isKnownOrLikelyTool(name string) bool {
	switch name {
	case "bash", "write_file", "read_file", "edit_file", "list_dir", "grep_search", "get_outline",
		"fleet_send_message", "fleet_broadcast", "fleet_read_inbox", "fleet_list_peers",
		"bd_ready", "bd_list", "bd_show", "bd_create", "bd_claim", "bd_close", "bd_update", "bd_sync",
		"forge_list_tasks", "forge_create_task", "forge_create_pull_request", "forge_review_pull_request", "forge_get_file":
		return true
	}
	return strings.HasPrefix(name, "bd_") || strings.HasPrefix(name, "fleet_") || strings.HasPrefix(name, "forge_")
}

// normalizeJSONBackticks converts raw multi-line strings enclosed in backticks (e.g. `package main...`)
// into valid escaped JSON double-quoted strings so json.Unmarshal can succeed.
// If the backtick is inside an existing double-quoted JSON string value, it is preserved.
func normalizeJSONBackticks(s string) string {
	var sb strings.Builder
	inBacktick := false
	inDoubleQuote := false
	isEscaped := false

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if inDoubleQuote {
			sb.WriteByte(ch)
			if isEscaped {
				isEscaped = false
			} else if ch == '\\' {
				isEscaped = true
			} else if ch == '"' {
				inDoubleQuote = false
			}
			continue
		}

		if ch == '"' && !inBacktick {
			inDoubleQuote = true
			sb.WriteByte(ch)
			continue
		}

		if ch == '`' {
			if !inBacktick {
				inBacktick = true
				sb.WriteByte('"')
			} else {
				inBacktick = false
				sb.WriteByte('"')
			}
			continue
		}

		if inBacktick {
			switch ch {
			case '\\':
				sb.WriteString("\\\\")
			case '"':
				sb.WriteString("\\\"")
			case '\n':
				sb.WriteString("\\n")
			case '\r':
				sb.WriteString("\\r")
			case '\t':
				sb.WriteString("\\t")
			default:
				sb.WriteByte(ch)
			}
		} else {
			sb.WriteByte(ch)
		}
	}
	return sb.String()
}

// parseLooseToolCall robustly parses tool calls where string values contain raw unescaped newlines or quotes.
func parseLooseToolCall(s string, idx int) (ToolCall, bool) {
	// Extract tool name
	nameKeyIdx := strings.Index(s, `"name"`)
	if nameKeyIdx < 0 {
		nameKeyIdx = strings.Index(s, `"tool"`)
	}
	if nameKeyIdx < 0 {
		nameKeyIdx = strings.Index(s, `"action"`)
	}
	if nameKeyIdx < 0 {
		return ToolCall{}, false
	}

	colonIdx := strings.Index(s[nameKeyIdx:], ":")
	if colonIdx < 0 {
		return ToolCall{}, false
	}
	valStart := nameKeyIdx + colonIdx + 1
	for valStart < len(s) && (s[valStart] == ' ' || s[valStart] == '\t' || s[valStart] == '\r' || s[valStart] == '\n' || s[valStart] == '"') {
		valStart++
	}
	valEnd := valStart
	for valEnd < len(s) && ((s[valEnd] >= 'a' && s[valEnd] <= 'z') || (s[valEnd] >= 'A' && s[valEnd] <= 'Z') || (s[valEnd] >= '0' && s[valEnd] <= '9') || s[valEnd] == '_') {
		valEnd++
	}
	toolName := s[valStart:valEnd]
	if toolName == "" || !isKnownOrLikelyTool(toolName) {
		return ToolCall{}, false
	}

	argsMap := make(map[string]any)

	// Extract path if present
	if pathIdx := strings.Index(s, `"path"`); pathIdx >= 0 {
		if cIdx := strings.Index(s[pathIdx:], ":"); cIdx >= 0 {
			start := pathIdx + cIdx + 1
			for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '"') {
				start++
			}
			end := strings.Index(s[start:], `"`)
			if end >= 0 {
				argsMap["path"] = s[start : start+end]
			}
		}
	}

	// Extract command if present
	if cmdIdx := strings.Index(s, `"command"`); cmdIdx >= 0 {
		if cIdx := strings.Index(s[cmdIdx:], ":"); cIdx >= 0 {
			start := cmdIdx + cIdx + 1
			for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '"') {
				start++
			}
			end := strings.Index(s[start:], `"`)
			if end >= 0 {
				argsMap["command"] = s[start : start+end]
			}
		}
	}

	// Extract content if present (multi-line tolerant)
	if contentIdx := strings.Index(s, `"content"`); contentIdx >= 0 {
		if cIdx := strings.Index(s[contentIdx:], ":"); cIdx >= 0 {
			start := contentIdx + cIdx + 1
			for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
				start++
			}
			if start < len(s) && (s[start] == '"' || s[start] == '`') {
				quoteChar := s[start]
				start++
				// Find closing quote before `}`
				lastClose := strings.LastIndex(s, "}")
				if lastClose > start {
					sub := s[start:lastClose]
					trimSub := strings.TrimRight(sub, " \t\r\n}")
					if strings.HasSuffix(trimSub, string(quoteChar)) {
						trimSub = trimSub[:len(trimSub)-1]
					}
					// Unescape escaped newlines if present
					contentVal := strings.ReplaceAll(trimSub, `\n`, "\n")
					contentVal = strings.ReplaceAll(contentVal, `\t`, "\t")
					contentVal = strings.ReplaceAll(contentVal, `\"`, `"`)
					argsMap["content"] = contentVal
				}
			}
		}
	}

	argsJSON, err := json.Marshal(argsMap)
	if err != nil {
		return ToolCall{}, false
	}

	return ToolCall{
		ID:   fmt.Sprintf("call-%d-%d", time.Now().UnixNano(), idx),
		Type: "function",
		Function: ToolFunctionCall{
			Name:      toolName,
			Arguments: string(argsJSON),
		},
	}, true
}
