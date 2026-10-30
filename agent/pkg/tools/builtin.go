// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/boggycreek/fleet/agent/pkg/runtime"
)

var (
	// ErrPathEscapesWorkspace is returned when a path traversal attempt is detected.
	ErrPathEscapesWorkspace = errors.New("path escapes workspace perimeter")
)

// ResolveWorkspacePath securely verifies that targetPath resides inside workspaceDir.
func ResolveWorkspacePath(workspaceDir, targetPath string) (string, error) {
	if strings.TrimSpace(targetPath) == "" {
		return "", errors.New("target path cannot be empty")
	}
	if workspaceDir == "" {
		workspaceDir = "."
	}
	cleanWorkspace, err := filepath.Abs(workspaceDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace directory: %w", err)
	}
	if evalWs, err := filepath.EvalSymlinks(cleanWorkspace); err == nil {
		cleanWorkspace = evalWs
	}

	var fullPath string
	if filepath.IsAbs(targetPath) {
		fullPath = filepath.Clean(targetPath)
	} else {
		fullPath = filepath.Join(cleanWorkspace, targetPath)
	}
	fullPath, err = filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve target path: %w", err)
	}

	rel, err := filepath.Rel(cleanWorkspace, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
		return "", fmt.Errorf("%w: %s is outside %s", ErrPathEscapesWorkspace, targetPath, cleanWorkspace)
	}

	// Check if path or any existing ancestor resolves via symlink outside workspace
	checkPath := fullPath
	for {
		if realPath, err := filepath.EvalSymlinks(checkPath); err == nil {
			realRel, err := filepath.Rel(cleanWorkspace, realPath)
			if err != nil || strings.HasPrefix(realRel, "..") || realRel == ".." {
				return "", fmt.Errorf("%w: symlink resolves outside %s", ErrPathEscapesWorkspace, cleanWorkspace)
			}
			break
		}
		parent := filepath.Dir(checkPath)
		if parent == checkPath {
			break
		}
		checkPath = parent
	}

	return fullPath, nil
}

// --- Builtin Tools ---

// BashTool executes commands in the workspace environment.
type BashTool struct {
	workspaceDir string
}

func (t *BashTool) Name() string { return "bash" }
func (t *BashTool) Description() string {
	return "Execute a shell command inside the workspace directory"
}
func (t *BashTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The bash shell command to execute",
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Execution timeout in seconds (default: 60)",
			},
		},
		"required": []string{"command"},
	}
}

func (t *BashTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	cmdStr, ok := args["command"].(string)
	if !ok || strings.TrimSpace(cmdStr) == "" {
		return "", errors.New("command argument is required")
	}

	timeout := 60 * time.Second
	if val, ok := args["timeout_seconds"].(float64); ok && val > 0 {
		timeout = time.Duration(val) * time.Second
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "bash", "-c", cmdStr)
	cmd.Dir = t.workspaceDir

	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("command start failed: %w", err)
	}

	runtime.RegisterChildPID(cmd.Process.Pid)
	defer runtime.UnregisterChildPID(cmd.Process.Pid)

	err := runtime.WaitManagedCmd(cmd)
	output := outBuf.String()

	const maxOutput = 10000
	if len(output) > maxOutput {
		output = output[:maxOutput] + "\n... [output truncated]"
	}

	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			return output, fmt.Errorf("command timed out after %v", timeout)
		}
		return output, fmt.Errorf("command failed: %w", err)
	}

	return output, nil
}

// ReadFileTool reads files inside the workspace.
type ReadFileTool struct {
	workspaceDir string
}

func (t *ReadFileTool) Name() string { return "read_file" }
func (t *ReadFileTool) Description() string {
	return "Read the contents of a file within the workspace"
}
func (t *ReadFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative or workspace-scoped path of the file to read",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Starting line number (1-indexed)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of lines to read",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ReadFileTool) Execute(_ context.Context, args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return "", errors.New("path argument is required")
	}

	resolved, err := ResolveWorkspacePath(t.workspaceDir, path)
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	start := 0
	if offset, ok := args["offset"].(float64); ok && offset > 1 {
		start = int(offset) - 1
		if start >= len(lines) {
			return "", nil
		}
	}

	end := len(lines)
	if limit, ok := args["limit"].(float64); ok && limit > 0 {
		end = start + int(limit)
		if end > len(lines) {
			end = len(lines)
		}
	}

	return strings.Join(lines[start:end], "\n"), nil
}

// WriteFileTool writes content to a file inside the workspace.
type WriteFileTool struct {
	workspaceDir string
}

func (t *WriteFileTool) Name() string { return "write_file" }
func (t *WriteFileTool) Description() string {
	return "Write content to a file within the workspace, creating parent directories if needed"
}
func (t *WriteFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative or workspace-scoped path of the file to write",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "File content to write",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *WriteFileTool) Execute(_ context.Context, args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return "", errors.New("path argument is required")
	}
	content, ok := args["content"].(string)
	if !ok {
		return "", errors.New("content argument is required")
	}

	resolved, err := ResolveWorkspacePath(t.workspaceDir, path)
	if err != nil {
		return "", err
	}

	parentDir := filepath.Dir(resolved)
	if fi, statErr := os.Stat(parentDir); statErr == nil && !fi.IsDir() && fi.Size() == 0 {
		_ = os.Remove(parentDir)
	}

	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create parent directories: %w", err)
	}

	if err := os.WriteFile(resolved, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return fmt.Sprintf("Wrote %d bytes to %s", len(content), path), nil
}

// EditFileTool performs exact string replacement on a file within the workspace.
type EditFileTool struct {
	workspaceDir string
}

func (t *EditFileTool) Name() string { return "edit_file" }
func (t *EditFileTool) Description() string {
	return "Perform exact string replacement in a file within the workspace"
}
func (t *EditFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative or workspace-scoped path of the file to edit",
			},
			"old_text": map[string]any{
				"type":        "string",
				"description": "Exact text chunk to replace",
			},
			"new_text": map[string]any{
				"type":        "string",
				"description": "Replacement text chunk",
			},
		},
		"required": []string{"path", "old_text", "new_text"},
	}
}

func (t *EditFileTool) Execute(_ context.Context, args map[string]any) (string, error) {
	path, ok := args["path"].(string)
	if !ok || strings.TrimSpace(path) == "" {
		return "", errors.New("path argument is required")
	}
	oldText, ok := args["old_text"].(string)
	if !ok || oldText == "" {
		return "", errors.New("old_text argument is required and cannot be empty")
	}
	newText, ok := args["new_text"].(string)
	if !ok {
		return "", errors.New("new_text argument is required")
	}

	resolved, err := ResolveWorkspacePath(t.workspaceDir, path)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	content := string(data)
	count := strings.Count(content, oldText)
	if count == 0 {
		return "", fmt.Errorf("target old_text not found in %s", path)
	}
	if count > 1 {
		return "", fmt.Errorf("target old_text is ambiguous (%d occurrences found in %s)", count, path)
	}

	modified := strings.Replace(content, oldText, newText, 1)
	if err := os.WriteFile(resolved, []byte(modified), 0644); err != nil {
		return "", fmt.Errorf("failed to write edited file: %w", err)
	}

	return fmt.Sprintf("Successfully edited %s", path), nil
}

// ListDirTool lists files and directories within a directory inside the workspace.
type ListDirTool struct {
	workspaceDir string
}

func (t *ListDirTool) Name() string { return "list_dir" }
func (t *ListDirTool) Description() string {
	return "List entries (files and directories) within a workspace directory"
}
func (t *ListDirTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative directory path to list (default: workspace root '.')",
			},
		},
	}
}

func (t *ListDirTool) Execute(_ context.Context, args map[string]any) (string, error) {
	relPath := "."
	if p, ok := args["path"].(string); ok && strings.TrimSpace(p) != "" {
		relPath = strings.TrimSpace(p)
	}

	resolved, err := ResolveWorkspacePath(t.workspaceDir, relPath)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("failed to stat directory %s: %w", relPath, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", relPath)
	}

	entries, err := os.ReadDir(resolved)
	if err != nil {
		return "", fmt.Errorf("failed to read directory %s: %w", relPath, err)
	}

	if len(entries) == 0 {
		return "(empty directory)", nil
	}

	var sb strings.Builder
	for _, entry := range entries {
		info, err := entry.Info()
		entryType := "FILE"
		size := int64(0)
		if entry.IsDir() {
			entryType = "DIR "
		} else if err == nil {
			size = info.Size()
		}
		sb.WriteString(fmt.Sprintf("[%s] %-10d %s\n", entryType, size, entry.Name()))
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// RegisterBuiltinTools registers bash, read_file, write_file, edit_file, and list_dir to registry.
func RegisterBuiltinTools(r *Registry, workspaceDir string) error {
	if r == nil {
		return errors.New("registry cannot be nil")
	}
	tools := []Tool{
		&BashTool{workspaceDir: workspaceDir},
		&ReadFileTool{workspaceDir: workspaceDir},
		&WriteFileTool{workspaceDir: workspaceDir},
		&EditFileTool{workspaceDir: workspaceDir},
		&ListDirTool{workspaceDir: workspaceDir},
	}
	for _, tool := range tools {
		if err := r.Register(tool); err != nil {
			return err
		}
	}
	return nil
}
