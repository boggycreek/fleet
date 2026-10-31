// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/boggycreek/fleet/agent/pkg/runtime"
	"github.com/boggycreek/fleet/backplane/pkg/libbp"
)

const (
	// DefaultMaxTurns is the default conversation turn budget before compaction.
	DefaultMaxTurns = 40

	// MaxToolOutputLength is the maximum character length for a single tool output in context.
	MaxToolOutputLength = 4000
)

// BuildSystemPrompt constructs the foundational system instruction prompt for the agent.
func BuildSystemPrompt(agentID, role, workspaceDir, customInstructions string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("You are %s, an autonomous AI software engineering agent running inside an isolated sandbox.\n", agentID))
	if role != "" {
		sb.WriteString(fmt.Sprintf("Your specialized role is: %s.\n", role))
	}
	sb.WriteString(fmt.Sprintf("Your workspace directory is: %s.\n\n", workspaceDir))

	sb.WriteString("Operational Guidelines:\n")
	sb.WriteString("1. You have access to built-in tools (bash, read_file, write_file, edit_file, list_dir, grep_search) and MCP tools.\n")
	sb.WriteString("2. All file system operations must remain strictly inside your workspace boundary.\n")
	sb.WriteString("3. Use Beads (`bd`) for task tracking and status updates. Never close a task (`bd_close`) until tests have actually run and passed with real verification evidence.\n")
	sb.WriteString("4. Act autonomously: discover repository context, implement changes, write tests, and verify results directly using your tools.\n")
	sb.WriteString("5. When assigned to implement code or tests, immediately execute the implementation and verification tools (write_file, bash) in your workspace. Do NOT send empty acknowledgment messages saying what you plan to do; perform the work first and report the actual results.\n")
	sb.WriteString("6. When delegating work to a peer agent via `fleet_send_message`, conclude your turn after sending the delegation. Do NOT attempt to verify or close tasks in the same turn before the peer has responded with results.\n")
	sb.WriteString("7. Do not send repetitive duplicate messages or repeatedly poll in a loop. Deliver concise, substantive updates with real artifacts and outputs.\n")
	sb.WriteString("8. Autonomous Tool Execution: No external operator will run terminal commands or create files for you. When you need to write code or tests, invoke `write_file`. When you need to run tests, invoke `bash`. Never print code or commands in plain text expecting someone else to execute them—always invoke your tools directly.\n")
	sb.WriteString("9. Developer Best Practices & Tool Selection:\n")
	sb.WriteString("   - Codebase Discovery: Perform codebase discovery via `grep_search` and `list_dir` to locate relevant functions, symbols, and definitions before editing.\n")
	sb.WriteString("   - Targeted Line Inspection: For large files, use `read_file` with `start_line` and `end_line` parameters to inspect targeted line ranges and conserve context budget.\n")
	sb.WriteString("   - Precise File Edits: Prefer `edit_file` with precise search/replace blocks over whole-file rewrites to preserve formatting and reduce regressions.\n")
	sb.WriteString("   - Test-Driven Verification: Write tests alongside code. Execute tests and build commands directly via `bash` and verify they exit successfully before claiming task completion.\n")
	sb.WriteString("   - Test Failure Recovery: When a test or command fails, diagnose error traces and inspect target lines rather than blindly looping or repeating commands with `bash`.\n")
	sb.WriteString("   - Clean Source Control: When working in a git repository, write concise conventional commit messages (`feat:`, `fix:`, `test:`, `refactor:`) and maintain clean working branches.\n")

	if strings.TrimSpace(customInstructions) != "" {
		sb.WriteString("\nAdditional Instructions:\n")
		sb.WriteString(strings.TrimSpace(customInstructions))
		sb.WriteString("\n")
	}

	return sb.String()
}

// FormatEventMessage formats a runtime.Event into a ChatMessage for prompt injection.
func FormatEventMessage(event runtime.Event) ChatMessage {
	payloadStr := extractPayloadString(event.Payload)

	switch event.Priority {
	case runtime.P0_Control:
		return ChatMessage{
			Role:    "user",
			Content: fmt.Sprintf("[EMERGENCY CONTROL DIRECTIVE from %s]: %s", event.Source, payloadStr),
		}
	case runtime.P1_HighPriority:
		return ChatMessage{
			Role:    "user",
			Content: fmt.Sprintf("[URGENT DIRECTIVE from %s]: %s", event.Source, payloadStr),
		}
	case runtime.P2_StandardAsync:
		return ChatMessage{
			Role:    "user",
			Content: fmt.Sprintf("[NOTIFICATION from %s]: %s", event.Source, payloadStr),
		}
	default:
		return ChatMessage{
			Role:    "user",
			Content: fmt.Sprintf("[MESSAGE from %s]: %s", event.Source, payloadStr),
		}
	}
}

func extractPayloadString(payload any) string {
	if payload == nil {
		return ""
	}
	switch v := payload.(type) {
	case string:
		return v
	case runtime.BackplaneEnvelope:
		return fmt.Sprintf("From: %s | Payload: %s", v.Sender, v.Payload)
	case *runtime.BackplaneEnvelope:
		if v == nil {
			return ""
		}
		return fmt.Sprintf("From: %s | Payload: %s", v.Sender, v.Payload)
	case libbp.Message:
		return fmt.Sprintf("From: %s | Message: %s", v.Sender, v.Content)
	case *libbp.Message:
		if v == nil {
			return ""
		}
		return fmt.Sprintf("From: %s | Message: %s", v.Sender, v.Content)
	default:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", v)
	}
}

// TruncateToolOutput caps the size of individual tool output messages to preserve context.
func TruncateToolOutput(content string, maxLength int) string {
	if maxLength <= 0 {
		maxLength = MaxToolOutputLength
	}
	if len(content) <= maxLength {
		return content
	}
	half := maxLength / 2
	return content[:half] + "\n... [content truncated to fit context budget] ...\n" + content[len(content)-half:]
}

// CompactHistory prunes conversation history while preserving the system prompt
// and keeping tool call / tool response message pairs structurally valid.
func CompactHistory(history []ChatMessage, maxTurns int) []ChatMessage {
	if maxTurns <= 0 {
		maxTurns = DefaultMaxTurns
	}
	if len(history) <= maxTurns {
		// Truncate any over-sized tool outputs in-place
		result := make([]ChatMessage, len(history))
		for i, msg := range history {
			if msg.Role == "tool" {
				msg.Content = TruncateToolOutput(msg.Content, MaxToolOutputLength)
			}
			result[i] = msg
		}
		return result
	}

	var hasSystem bool
	var systemMsg ChatMessage
	nonSystemMsgs := history

	if len(history) > 0 && history[0].Role == "system" {
		hasSystem = true
		systemMsg = history[0]
		nonSystemMsgs = history[1:]
	}

	targetNonSystem := maxTurns
	if hasSystem {
		targetNonSystem = maxTurns - 1
	}

	if len(nonSystemMsgs) <= targetNonSystem {
		if hasSystem {
			return append([]ChatMessage{systemMsg}, nonSystemMsgs...)
		}
		return nonSystemMsgs
	}

	// Prune from start of nonSystemMsgs until we reach targetNonSystem.
	// We must ensure we do not orphan a "tool" message whose assistant "tool_calls" message was dropped.
	startIdx := len(nonSystemMsgs) - targetNonSystem

	// Advance startIdx if it lands on a "tool" message, until we find a "user" or "assistant" boundary.
	for startIdx < len(nonSystemMsgs) && nonSystemMsgs[startIdx].Role == "tool" {
		startIdx++
	}

	pruned := nonSystemMsgs[startIdx:]

	// Truncate tool outputs in retained messages
	result := make([]ChatMessage, 0, len(pruned)+1)
	if hasSystem {
		result = append(result, systemMsg)
	}
	for _, msg := range pruned {
		if msg.Role == "tool" {
			msg.Content = TruncateToolOutput(msg.Content, MaxToolOutputLength)
		}
		result = append(result, msg)
	}

	return result
}
