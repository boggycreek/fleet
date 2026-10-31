// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// MapPriorityToReasoning maps a task priority level to reasoning budget and effort string.
// - P0 (0): budget 0.99, effort "high"
// - P1 (1): budget 0.95, effort "high"
// - P2 (2): budget 0.75, effort "medium"
// - P3 (3): budget 0.50, effort "low"
// - P4 (4): budget 0.25, effort "low"
// - Default (< 0 or > 4): budget 0.50, effort "medium"
func MapPriorityToReasoning(priority int) (budget float64, effort string) {
	switch priority {
	case 0:
		return 0.99, "high"
	case 1:
		return 0.95, "high"
	case 2:
		return 0.75, "medium"
	case 3:
		return 0.50, "low"
	case 4:
		return 0.25, "low"
	default:
		return 0.50, "medium"
	}
}

// TaskPriorityResolver resolves the active task priority.
type TaskPriorityResolver interface {
	GetActiveTaskPriority(ctx context.Context) (priority int, found bool, err error)
}

// CommandRunner executes a command in a working directory and returns stdout.
type CommandRunner func(ctx context.Context, dir string, name string, args ...string) ([]byte, error)

// DefaultCommandRunner executes the command using os/exec.
func DefaultCommandRunner(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd.Output()
}

// beadsTaskItem represents the JSON structure of tasks emitted by bd list --json.
type beadsTaskItem struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Priority int    `json:"priority"`
}

// BeadsTaskResolver resolves active task priority by querying Beads tasks via the bd CLI.
type BeadsTaskResolver struct {
	mu           sync.RWMutex
	workspaceDir string
	runner       CommandRunner
}

// NewBeadsTaskResolver constructs a BeadsTaskResolver for a given workspace directory.
func NewBeadsTaskResolver(workspaceDir string) *BeadsTaskResolver {
	return &BeadsTaskResolver{
		workspaceDir: workspaceDir,
		runner:       DefaultCommandRunner,
	}
}

// WorkspaceDir returns the configured workspace directory.
func (b *BeadsTaskResolver) WorkspaceDir() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.workspaceDir
}

// SetCommandRunner allows overriding the command execution runner (e.g. for testing).
func (b *BeadsTaskResolver) SetCommandRunner(runner CommandRunner) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.runner = runner
}

// GetActiveTaskPriority executes `bd list --json` and inspects the tasks for an "in_progress" task.
// If found, returns its priority.
// If none found or error, returns found=false.
func (b *BeadsTaskResolver) GetActiveTaskPriority(ctx context.Context) (int, bool, error) {
	b.mu.RLock()
	runner := b.runner
	dir := b.workspaceDir
	b.mu.RUnlock()

	if runner == nil {
		runner = DefaultCommandRunner
	}

	out, err := runner(ctx, dir, "bd", "list", "--json")
	if err != nil {
		return 0, false, err
	}

	var items []beadsTaskItem
	if err := json.Unmarshal(out, &items); err != nil {
		// Attempt to extract JSON array if surrounded by log lines or prefixes (e.g. [WARNING], [INFO])
		parsed := false
		for i := 0; i < len(out); i++ {
			if out[i] == '[' {
				for j := len(out) - 1; j > i; j-- {
					if out[j] == ']' {
						if err2 := json.Unmarshal(out[i:j+1], &items); err2 == nil {
							parsed = true
							break
						}
					}
				}
				if parsed {
					break
				}
			}
		}
		if !parsed {
			return 0, false, fmt.Errorf("failed to parse bd list --json output: %w", err)
		}
	}

	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Status), "in_progress") {
			return item.Priority, true, nil
		}
	}

	return 0, false, nil
}

// StaticTaskResolver returns a static priority or error, useful for tests and manual priority overrides.
type StaticTaskResolver struct {
	Priority int
	Found    bool
	Err      error
}

// NewStaticTaskResolver constructs a StaticTaskResolver with the given priority and found state.
func NewStaticTaskResolver(priority int, found bool) *StaticTaskResolver {
	return &StaticTaskResolver{
		Priority: priority,
		Found:    found,
	}
}

// GetActiveTaskPriority returns the preconfigured priority, found status, and error.
func (s *StaticTaskResolver) GetActiveTaskPriority(ctx context.Context) (int, bool, error) {
	return s.Priority, s.Found, s.Err
}
