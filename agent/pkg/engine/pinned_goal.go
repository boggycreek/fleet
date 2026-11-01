// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package engine

import (
	"fmt"
	"strings"
)

// TaskSnapshot represents an active task directive snapshot from the Beads issue graph.
// It captures the goal anchor so cognitive context is persistently maintained across turn compaction.
type TaskSnapshot struct {
	ID                 string   `json:"id"`
	Title              string   `json:"title"`
	Description        string   `json:"description,omitempty"`
	Constraints        []string `json:"constraints,omitempty"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	Status             string   `json:"status,omitempty"`
	Priority           int      `json:"priority,omitempty"`
}

// IsEmpty reports whether the TaskSnapshot contains no actionable task identifiers or title.
func (t *TaskSnapshot) IsEmpty() bool {
	if t == nil {
		return true
	}
	return strings.TrimSpace(t.ID) == "" && strings.TrimSpace(t.Title) == ""
}

// Clone creates a deep copy of the TaskSnapshot.
func (t *TaskSnapshot) Clone() *TaskSnapshot {
	if t == nil {
		return nil
	}
	cp := *t
	if len(t.Constraints) > 0 {
		cp.Constraints = make([]string, len(t.Constraints))
		copy(cp.Constraints, t.Constraints)
	}
	if len(t.AcceptanceCriteria) > 0 {
		cp.AcceptanceCriteria = make([]string, len(t.AcceptanceCriteria))
		copy(cp.AcceptanceCriteria, t.AcceptanceCriteria)
	}
	return &cp
}

// FormatPinnedGoal formats an active TaskSnapshot into a clear, structured system directive block.
// If task is nil or empty, an empty string is returned.
func FormatPinnedGoal(task *TaskSnapshot) string {
	if task == nil || task.IsEmpty() {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("=== ACTIVE TASK DIRECTIVE (PINNED GOAL) ===\n")
	if strings.TrimSpace(task.ID) != "" {
		sb.WriteString(fmt.Sprintf("Issue / Bead ID: %s\n", strings.TrimSpace(task.ID)))
	}
	if strings.TrimSpace(task.Title) != "" {
		sb.WriteString(fmt.Sprintf("Title: %s\n", strings.TrimSpace(task.Title)))
	}
	if strings.TrimSpace(task.Status) != "" {
		sb.WriteString(fmt.Sprintf("Status: %s\n", strings.TrimSpace(task.Status)))
	}
	if task.Priority >= 0 {
		sb.WriteString(fmt.Sprintf("Priority: P%d\n", task.Priority))
	}
	if strings.TrimSpace(task.Description) != "" {
		sb.WriteString(fmt.Sprintf("Description / Requirements:\n%s\n", strings.TrimSpace(task.Description)))
	}
	if len(task.Constraints) > 0 {
		sb.WriteString("Constraints:\n")
		for _, c := range task.Constraints {
			trimmed := strings.TrimSpace(c)
			if trimmed != "" {
				sb.WriteString(fmt.Sprintf("- %s\n", trimmed))
			}
		}
	}
	if len(task.AcceptanceCriteria) > 0 {
		sb.WriteString("Acceptance Criteria:\n")
		for _, ac := range task.AcceptanceCriteria {
			trimmed := strings.TrimSpace(ac)
			if trimmed != "" {
				sb.WriteString(fmt.Sprintf("- [ ] %s\n", trimmed))
			}
		}
	}
	sb.WriteString("=== END ACTIVE TASK DIRECTIVE ===")
	return sb.String()
}
