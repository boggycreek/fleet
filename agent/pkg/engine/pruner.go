// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package engine

import (
	"fmt"
	"strings"
)

const (
	// DefaultObservationPruneThreshold is the character count threshold above which historical tool outputs are pruned.
	DefaultObservationPruneThreshold = 500

	// DefaultObservationPruneLines is the line count threshold above which historical tool outputs are pruned.
	DefaultObservationPruneLines = 15

	// DefaultObservationHeadLines is the number of initial lines preserved in a pruned observation.
	DefaultObservationHeadLines = 3

	// DefaultObservationTailLines is the number of trailing lines preserved in a pruned observation.
	DefaultObservationTailLines = 3

	// DefaultKeepRecentTurns is the number of most recent conversational turns whose tool observations are kept intact.
	DefaultKeepRecentTurns = 1
)

// PruneHistoricalObservations prunes bulky tool observation payloads from older turns in the conversation history,
// preserving full tool outputs for the most recent keepRecentTurns turns.
// For historical tool messages exceeding maxChars or DefaultObservationPruneLines:
// - Tool metadata (Role, ToolCallID, Name) is preserved intact.
// - The first N lines (DefaultObservationHeadLines) and last M lines (DefaultObservationTailLines) are preserved.
// - An omission marker reporting the count of pruned lines and bytes is inserted between them.
// If keepRecentTurns <= 0, DefaultKeepRecentTurns is used.
// If maxChars <= 0, DefaultObservationPruneThreshold is used.
func PruneHistoricalObservations(messages []ChatMessage, keepRecentTurns int, maxChars int) []ChatMessage {
	if len(messages) == 0 {
		return nil
	}
	if keepRecentTurns <= 0 {
		keepRecentTurns = DefaultKeepRecentTurns
	}
	if maxChars <= 0 {
		maxChars = DefaultObservationPruneThreshold
	}

	// Identify turn boundaries.
	// In conversation history, a turn represents an assistant reasoning/tool-execution cycle
	// or user/system message. Specifically, groups of tool response messages (Role == "tool")
	// belong to the tool execution round of their corresponding assistant message.
	// We want to identify tool message indices and determine which turns they belong to.
	// Working backwards from the end of messages, identify the most recent `keepRecentTurns` tool groups.
	// A tool group is a contiguous or turn-bounded sequence of "tool" messages.
	// Alternatively, we can define turns by looking at assistant messages that initiated tool calls,
	// or simply the last keepRecentTurns occurrences of tool observation batches.
	//
	// Let's analyze conversational turn structure:
	// Turn 1:
	//   user -> directive
	//   assistant -> tool_calls: [read_file, list_dir]
	//   tool -> output 1
	//   tool -> output 2
	//   assistant -> "Done with step 1"
	// Turn 2:
	//   user -> "Now do step 2"
	//   assistant -> tool_calls: [bash]
	//   tool -> output 3
	//   assistant -> "Done with step 2"
	// Current Turn:
	//   user -> "Now do step 3"
	//   assistant -> tool_calls: [bash]
	//   tool -> output 4 (latest observation, currently being evaluated)
	//
	// In a single multi-iteration cognitive turn inside executeTurn:
	// iteration 1: assistant -> tool_calls; tool outputs appended
	// iteration 2: assistant sees previous iteration's tool outputs!
	// If keepRecentTurns == 1, the latest batch of tool outputs (the ones just executed before the next LLM call)
	// MUST be preserved in full so the model can inspect immediate diagnostics.
	//
	// Let's define "recent turns" for tool messages:
	// We scan backwards from len(messages)-1.
	// We count distinct tool phases / batches encountered from the end.
	// A tool batch is a sequence of one or more "tool" messages without an intervening assistant/user message
	// from another turn.
	// More precisely, each transition from non-tool to tool as we walk backwards starts a new recent tool turn.
	// If we encounter a tool message, and the previous (forward) or next (backward) message is also a tool message,
	// they are part of the same tool turn/batch.
	// Once we transition from tool to non-tool, that completes 1 tool turn.
	// Any tool message belonging to the last `keepRecentTurns` tool turns is protected (recent).
	// Any tool message before that is historical and subject to pruning.

	isProtected := markProtectedToolMessages(messages, keepRecentTurns)

	result := make([]ChatMessage, len(messages))
	for i, msg := range messages {
		if msg.Role == "tool" && !isProtected[i] {
			msg.Content = PruneObservationPayload(msg.Content, maxChars, DefaultObservationPruneLines, DefaultObservationHeadLines, DefaultObservationTailLines)
		}
		result[i] = msg
	}

	return result
}

// markProtectedToolMessages marks which tool messages fall within the most recent keepRecentTurns turns.
func markProtectedToolMessages(messages []ChatMessage, keepRecentTurns int) []bool {
	protected := make([]bool, len(messages))
	turnsSeen := 0
	inToolBatch := false

	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "tool" {
			if !inToolBatch {
				turnsSeen++
				inToolBatch = true
			}
			if turnsSeen <= keepRecentTurns {
				protected[i] = true
			}
		} else {
			inToolBatch = false
		}
	}

	return protected
}

// PruneObservationPayload prunes a single tool observation string if it exceeds maxChars or maxLines.
func PruneObservationPayload(content string, maxChars, maxLines, headLines, tailLines int) string {
	if maxChars <= 0 {
		maxChars = DefaultObservationPruneThreshold
	}
	if maxLines <= 0 {
		maxLines = DefaultObservationPruneLines
	}
	if headLines <= 0 {
		headLines = DefaultObservationHeadLines
	}
	if tailLines <= 0 {
		tailLines = DefaultObservationTailLines
	}

	hasTrailingNewline := strings.HasSuffix(content, "\n")
	trimmed := strings.TrimSuffix(content, "\n")
	lines := strings.Split(trimmed, "\n")
	lineCount := len(lines)
	charCount := len(content)

	// Check if pruning is required
	if charCount <= maxChars && lineCount <= maxLines {
		return content
	}

	// If total lines are less than or equal to headLines + tailLines, but chars exceed maxChars,
	// we still prune to satisfy context budget if possible.
	if lineCount <= headLines+tailLines {
		// Just truncate with standard budget marker if character count is large
		return TruncateToolOutput(content, maxChars)
	}

	head := lines[:headLines]
	tail := lines[lineCount-tailLines:]
	prunedLines := lines[headLines : lineCount-tailLines]

	prunedLineCount := len(prunedLines)
	// Calculate pruned byte count including newlines
	prunedByteCount := 0
	for _, l := range prunedLines {
		prunedByteCount += len(l) + 1 // +1 for the newline
	}

	marker := fmt.Sprintf("\n... [pruned %d lines / %d bytes for context efficiency] ...\n", prunedLineCount, prunedByteCount)

	var sb strings.Builder
	sb.WriteString(strings.Join(head, "\n"))
	sb.WriteString(marker)
	sb.WriteString(strings.Join(tail, "\n"))
	if hasTrailingNewline {
		sb.WriteString("\n")
	}

	return sb.String()
}
