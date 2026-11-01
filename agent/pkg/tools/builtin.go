// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package tools

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/boggycreek/fleet/agent/pkg/codeintel"
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

func parseOptionalInt(val any) (int, bool) {
	switch v := val.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case float32:
		return int(v), true
	default:
		return 0, false
	}
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
			"start_line": map[string]any{
				"type":        "integer",
				"description": "Starting line number (1-indexed, optional)",
			},
			"end_line": map[string]any{
				"type":        "integer",
				"description": "Ending line number (1-indexed, inclusive, optional)",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Starting line number (1-indexed, legacy)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of lines to read (legacy)",
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

	if len(content) == 0 {
		return "", nil
	}

	startLine, hasStart := parseOptionalInt(args["start_line"])
	endLine, hasEnd := parseOptionalInt(args["end_line"])

	if (hasStart && startLine > 0) || (hasEnd && endLine > 0) {
		raw := string(content)
		raw = strings.TrimSuffix(raw, "\r\n")
		raw = strings.TrimSuffix(raw, "\n")
		lines := strings.Split(raw, "\n")

		if !hasStart || startLine <= 0 {
			startLine = 1
		}
		if !hasEnd || endLine <= 0 {
			endLine = len(lines)
		}
		if startLine > endLine {
			return "", fmt.Errorf("start_line (%d) cannot be greater than end_line (%d)", startLine, endLine)
		}
		if startLine > len(lines) {
			return "", fmt.Errorf("start_line (%d) exceeds file line count (%d)", startLine, len(lines))
		}
		if endLine > len(lines) {
			endLine = len(lines)
		}

		var b strings.Builder
		for i := startLine; i <= endLine; i++ {
			if i > startLine {
				b.WriteByte('\n')
			}
			b.WriteString(fmt.Sprintf("%4d | %s", i, lines[i-1]))
		}
		return b.String(), nil
	}

	lines := strings.Split(string(content), "\n")
	start := 0
	if offset, ok := parseOptionalInt(args["offset"]); ok && offset > 1 {
		start = offset - 1
		if start >= len(lines) {
			return "", nil
		}
	}

	end := len(lines)
	if limit, ok := parseOptionalInt(args["limit"]); ok && limit > 0 {
		end = start + limit
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
	intelReg     *codeintel.Registry
}

func (t *EditFileTool) getIntelRegistry() *codeintel.Registry {
	if t.intelReg != nil {
		return t.intelReg
	}
	return codeintel.NewDefaultRegistry()
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

func (t *EditFileTool) Execute(ctx context.Context, args map[string]any) (string, error) {
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

	// Pre-validation syntax gate: reject edits that introduce syntax errors
	relPath, relErr := filepath.Rel(t.workspaceDir, resolved)
	if relErr != nil || relPath == "" {
		relPath = path
	}

	reg := t.getIntelRegistry()
	diags, valErr := reg.ValidateSyntax(ctx, resolved, []byte(modified))
	if valErr != nil {
		return "", fmt.Errorf("syntax validation failed: %w", valErr)
	}
	for _, diag := range diags {
		if diag.Severity == codeintel.SeverityError {
			return "", fmt.Errorf("edit rejected: syntax error in %s at line %d: %s", relPath, diag.Line, diag.Message)
		}
	}

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

// GrepSearchTool searches for a regular expression pattern across files in the workspace directory.
type GrepSearchTool struct {
	workspaceDir string
}

func (t *GrepSearchTool) Name() string { return "grep_search" }
func (t *GrepSearchTool) Description() string {
	return "Search for a regular expression pattern across files in the workspace directory"
}
func (t *GrepSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regular expression pattern to search for",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Relative directory or file path to search within (default: '.')",
			},
			"case_sensitive": map[string]any{
				"type":        "boolean",
				"description": "Whether the regex search is case sensitive (default: false)",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum number of matching lines to return (default: 100)",
			},
		},
		"required": []string{"pattern"},
	}
}

var errMaxResultsReached = errors.New("max results reached")

const maxGrepLineLength = 4096

func (t *GrepSearchTool) Execute(ctx context.Context, args map[string]any) (string, error) {
	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return "", errors.New("pattern argument is required and cannot be empty")
	}

	searchPath := "."
	if p, ok := args["path"].(string); ok && strings.TrimSpace(p) != "" {
		searchPath = strings.TrimSpace(p)
	}

	resolvedPath, err := ResolveWorkspacePath(t.workspaceDir, searchPath)
	if err != nil {
		return "", err
	}

	caseSensitive := false
	if cs, ok := args["case_sensitive"].(bool); ok {
		caseSensitive = cs
	}

	maxResults := 100
	if mr, ok := parseOptionalInt(args["max_results"]); ok && mr > 0 {
		maxResults = mr
	}

	regexPattern := pattern
	if !caseSensitive {
		regexPattern = "(?i)" + regexPattern
	}
	re, err := regexp.Compile(regexPattern)
	if err != nil {
		return "", fmt.Errorf("invalid regular expression: %w", err)
	}

	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("failed to stat path %s: %w", searchPath, err)
	}

	var results []string

	if !info.IsDir() {
		rel, err := filepath.Rel(t.workspaceDir, resolvedPath)
		if err != nil {
			rel = resolvedPath
		}
		_ = searchFile(resolvedPath, rel, re, maxResults, &results)
		return strings.Join(results, "\n"), nil
	}

	err = filepath.WalkDir(resolvedPath, func(p string, d os.DirEntry, err error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if (strings.HasPrefix(name, ".") && p != resolvedPath) || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(t.workspaceDir, p)
		if err != nil {
			rel = p
		}
		if err := searchFile(p, rel, re, maxResults, &results); err != nil {
			if errors.Is(err, errMaxResultsReached) {
				return err
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errMaxResultsReached) {
		return "", err
	}

	return strings.Join(results, "\n"), nil
}

func isBinaryFile(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	return bytes.IndexByte(buf[:n], 0) != -1, nil
}

func searchFile(filePath, relPath string, re *regexp.Regexp, maxResults int, currentResults *[]string) error {
	isBin, err := isBinaryFile(filePath)
	if err != nil || isBin {
		return nil
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	lineNum := 1
	for {
		if len(*currentResults) >= maxResults {
			return errMaxResultsReached
		}
		var lineBuf []byte
		var readErr error
		for {
			chunk, isPrefix, err := reader.ReadLine()
			if len(chunk) > 0 {
				if len(lineBuf) < maxGrepLineLength {
					remaining := maxGrepLineLength - len(lineBuf)
					if len(chunk) > remaining {
						lineBuf = append(lineBuf, chunk[:remaining]...)
					} else {
						lineBuf = append(lineBuf, chunk...)
					}
				}
			}
			if err != nil {
				readErr = err
				break
			}
			if !isPrefix {
				break
			}
		}
		if len(lineBuf) > 0 || readErr == nil {
			cleanLine := strings.TrimRight(string(lineBuf), "\r\n")
			if re.MatchString(cleanLine) {
				*currentResults = append(*currentResults, fmt.Sprintf("%s:%d: %s", relPath, lineNum, cleanLine))
			}
			lineNum++
		}
		if readErr != nil {
			break
		}
	}
	return nil
}

// GetOutlineTool extracts a structural code outline from a source file using the code intelligence provider.
type GetOutlineTool struct {
	workspaceDir string
	intelReg     *codeintel.Registry
}

func (t *GetOutlineTool) getIntelRegistry() *codeintel.Registry {
	if t.intelReg != nil {
		return t.intelReg
	}
	return codeintel.NewDefaultRegistry()
}

func (t *GetOutlineTool) Name() string { return "get_outline" }
func (t *GetOutlineTool) Description() string {
	return "Extract a structural code outline (classes, functions, interfaces, signatures) from a source file using the code intelligence provider"
}
func (t *GetOutlineTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative or workspace-scoped path of the source file to outline",
			},
		},
		"required": []string{"path"},
	}
}

func (t *GetOutlineTool) Execute(ctx context.Context, args map[string]any) (string, error) {
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

	relPath, err := filepath.Rel(t.workspaceDir, resolved)
	if err != nil || relPath == "" {
		relPath = path
	}

	reg := t.getIntelRegistry()
	outline, err := reg.GetOutline(ctx, resolved, content)
	if err != nil {
		return "", fmt.Errorf("failed to extract outline: %w", err)
	}

	outline.FilePath = relPath
	return codeintel.FormatOutlineMarkdown(outline), nil
}

// RegisterBuiltinTools registers bash, read_file, write_file, edit_file, list_dir, grep_search, and get_outline to registry.
func RegisterBuiltinTools(r *Registry, workspaceDir string) error {
	if r == nil {
		return errors.New("registry cannot be nil")
	}
	intelReg, _ := codeintel.LoadRegistry(workspaceDir)
	if intelReg == nil {
		intelReg = codeintel.NewDefaultRegistry()
	}
	tools := []Tool{
		&BashTool{workspaceDir: workspaceDir},
		&ReadFileTool{workspaceDir: workspaceDir},
		&WriteFileTool{workspaceDir: workspaceDir},
		&EditFileTool{workspaceDir: workspaceDir, intelReg: intelReg},
		&ListDirTool{workspaceDir: workspaceDir},
		&GrepSearchTool{workspaceDir: workspaceDir},
		&GetOutlineTool{workspaceDir: workspaceDir, intelReg: intelReg},
	}
	for _, tool := range tools {
		if err := r.Register(tool); err != nil {
			return err
		}
	}
	return nil
}
