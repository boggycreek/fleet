// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package engine

import (
	"context"
	"errors"
	"testing"
)

func TestMapPriorityToReasoning(t *testing.T) {
	tests := []struct {
		name       string
		priority   int
		wantBudget float64
		wantEffort string
	}{
		{
			name:       "P0 Critical/Spike",
			priority:   0,
			wantBudget: 0.99,
			wantEffort: "high",
		},
		{
			name:       "P1 High Architecture",
			priority:   1,
			wantBudget: 0.95,
			wantEffort: "high",
		},
		{
			name:       "P2 Major Feature/Refactor",
			priority:   2,
			wantBudget: 0.75,
			wantEffort: "medium",
		},
		{
			name:       "P3 Standard Bug/Test",
			priority:   3,
			wantBudget: 0.50,
			wantEffort: "low",
		},
		{
			name:       "P4 Docs/Formatting",
			priority:   4,
			wantBudget: 0.25,
			wantEffort: "low",
		},
		{
			name:       "Negative Priority Default",
			priority:   -1,
			wantBudget: 0.50,
			wantEffort: "medium",
		},
		{
			name:       "Large Priority Default",
			priority:   5,
			wantBudget: 0.50,
			wantEffort: "medium",
		},
		{
			name:       "Arbitrary High Priority Default",
			priority:   99,
			wantBudget: 0.50,
			wantEffort: "medium",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBudget, gotEffort := MapPriorityToReasoning(tt.priority)
			if gotBudget != tt.wantBudget {
				t.Errorf("MapPriorityToReasoning(%d) gotBudget = %v, want %v", tt.priority, gotBudget, tt.wantBudget)
			}
			if gotEffort != tt.wantEffort {
				t.Errorf("MapPriorityToReasoning(%d) gotEffort = %q, want %q", tt.priority, gotEffort, tt.wantEffort)
			}
		})
	}
}

func TestBeadsTaskResolver_Success(t *testing.T) {
	jsonOutput := `[
		{"id": "sndbx-1", "title": "Done task", "status": "closed", "priority": 3},
		{"id": "sndbx-2", "title": "Active feature", "status": "in_progress", "priority": 1},
		{"id": "sndbx-3", "title": "Future task", "status": "open", "priority": 4}
	]`

	resolver := NewBeadsTaskResolver("/workspace/test")
	if resolver.WorkspaceDir() != "/workspace/test" {
		t.Fatalf("expected workspace dir /workspace/test, got %s", resolver.WorkspaceDir())
	}

	resolver.SetCommandRunner(func(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
		if dir != "/workspace/test" {
			t.Errorf("expected dir /workspace/test, got %s", dir)
		}
		if name != "bd" || len(args) != 2 || args[0] != "list" || args[1] != "--json" {
			t.Errorf("unexpected command: %s %v", name, args)
		}
		return []byte(jsonOutput), nil
	})

	ctx := context.Background()
	prio, found, err := resolver.GetActiveTaskPriority(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if prio != 1 {
		t.Errorf("expected priority 1, got %d", prio)
	}
}

func TestBeadsTaskResolver_CaseInsensitiveStatus(t *testing.T) {
	jsonOutput := `[
		{"id": "sndbx-99", "status": "IN_PROGRESS", "priority": 0}
	]`

	resolver := NewBeadsTaskResolver("")
	resolver.SetCommandRunner(func(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
		return []byte(jsonOutput), nil
	})

	prio, found, err := resolver.GetActiveTaskPriority(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || prio != 0 {
		t.Errorf("expected found=true and prio=0, got found=%v, prio=%d", found, prio)
	}
}

func TestBeadsTaskResolver_SurroundingText(t *testing.T) {
	outputWithLogs := `Checking database refs...
	[
		{"id": "sndbx-wcp", "status": "in_progress", "priority": 2}
	]
	NOTE: bd --json output format will change in v2.0.`

	resolver := NewBeadsTaskResolver("")
	resolver.SetCommandRunner(func(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
		return []byte(outputWithLogs), nil
	})

	prio, found, err := resolver.GetActiveTaskPriority(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || prio != 2 {
		t.Errorf("expected found=true, prio=2; got found=%v, prio=%d", found, prio)
	}
}

func TestBeadsTaskResolver_LogPrefixesWithBrackets(t *testing.T) {
	outputWithLogBrackets := `[WARNING] Dolt remote unreachable, falling back to local cache
[INFO] Scanning workspace issues
[
	{"id": "sndbx-wcp", "status": "in_progress", "priority": 1}
]
[INFO] Command completed in 12ms`

	resolver := NewBeadsTaskResolver("")
	resolver.SetCommandRunner(func(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
		return []byte(outputWithLogBrackets), nil
	})

	prio, found, err := resolver.GetActiveTaskPriority(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || prio != 1 {
		t.Errorf("expected found=true, prio=1; got found=%v, prio=%d", found, prio)
	}
}

func TestBeadsTaskResolver_MalformedPrefixBracketsBeforeValidArray(t *testing.T) {
	// Tests that candidate slice reuse does not corrupt items
	outputWithMalformedFirstBracket := `[not-valid-json]
[also [broken]
[
	{"id": "sndbx-multi", "status": "in_progress", "priority": 0}
]`

	resolver := NewBeadsTaskResolver("")
	resolver.SetCommandRunner(func(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
		return []byte(outputWithMalformedFirstBracket), nil
	})

	prio, found, err := resolver.GetActiveTaskPriority(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || prio != 0 {
		t.Errorf("expected found=true, prio=0; got found=%v, prio=%d", found, prio)
	}
}

func TestBeadsTaskResolver_NoActiveTask(t *testing.T) {
	jsonOutput := `[
		{"id": "sndbx-1", "status": "closed", "priority": 1},
		{"id": "sndbx-2", "status": "open", "priority": 2}
	]`

	resolver := NewBeadsTaskResolver("")
	resolver.SetCommandRunner(func(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
		return []byte(jsonOutput), nil
	})

	prio, found, err := resolver.GetActiveTaskPriority(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Errorf("expected found=false, got prio=%d", prio)
	}
}

func TestBeadsTaskResolver_EmptyList(t *testing.T) {
	resolver := NewBeadsTaskResolver("")
	resolver.SetCommandRunner(func(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
		return []byte("[]"), nil
	})

	prio, found, err := resolver.GetActiveTaskPriority(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Errorf("expected found=false, got prio=%d", prio)
	}
}

func TestBeadsTaskResolver_CommandError(t *testing.T) {
	resolver := NewBeadsTaskResolver("")
	resolver.SetCommandRunner(func(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
		return nil, errors.New("exec: 'bd': executable file not found")
	})

	prio, found, err := resolver.GetActiveTaskPriority(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if found {
		t.Errorf("expected found=false on command error, got prio=%d", prio)
	}
}

func TestBeadsTaskResolver_InvalidJSON(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{
			name:   "non-json garbage",
			output: "Fatal error: database locked",
		},
		{
			name:   "bracketed syntax error",
			output: "[{invalid-json:}]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewBeadsTaskResolver("")
			resolver.SetCommandRunner(func(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
				return []byte(tt.output), nil
			})

			prio, found, err := resolver.GetActiveTaskPriority(context.Background())
			if err == nil {
				t.Fatalf("expected parse error for %s, got nil", tt.name)
			}
			if found {
				t.Errorf("expected found=false on parse error, got prio=%d", prio)
			}
		})
	}
}

func TestDefaultCommandRunner(t *testing.T) {
	ctx := context.Background()
	// Test executing a standard echo command
	out, err := DefaultCommandRunner(ctx, "", "echo", "fleet-test")
	if err != nil {
		t.Fatalf("DefaultCommandRunner echo failed: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty output from echo")
	}

	// Test executing with a directory
	tmpDir := t.TempDir()
	outDir, errDir := DefaultCommandRunner(ctx, tmpDir, "pwd")
	if errDir == nil && len(outDir) == 0 {
		t.Fatal("expected non-empty output from pwd")
	}
}

func TestBeadsTaskResolver_NilRunnerFallback(t *testing.T) {
	resolver := NewBeadsTaskResolver("")
	resolver.SetCommandRunner(nil)
	// Calling GetActiveTaskPriority will fallback to DefaultCommandRunner.
	// Since bd might not be runnable in empty dir, it will return an error or result, but not panic.
	_, _, _ = resolver.GetActiveTaskPriority(context.Background())
}

func TestStaticTaskResolver(t *testing.T) {
	res := NewStaticTaskResolver(1, true)
	prio, found, err := res.GetActiveTaskPriority(context.Background())
	if err != nil || !found || prio != 1 {
		t.Errorf("StaticTaskResolver returned prio=%d, found=%v, err=%v", prio, found, err)
	}

	// Test error and not found
	customErr := errors.New("static error")
	resErr := &StaticTaskResolver{
		Priority: 0,
		Found:    false,
		Err:      customErr,
	}
	prio, found, err = resErr.GetActiveTaskPriority(context.Background())
	if !errors.Is(err, customErr) {
		t.Errorf("expected customErr, got %v", err)
	}
	if found {
		t.Errorf("expected found=false, got prio=%d", prio)
	}
}
