// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package codeintel

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// LSPTransport defines the abstraction for communicating with an LSP server over stdio.
type LSPTransport interface {
	io.ReadWriteCloser
}

type processTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (p *processTransport) Read(b []byte) (int, error) {
	return p.stdout.Read(b)
}

func (p *processTransport) Write(b []byte) (int, error) {
	return p.stdin.Write(b)
}

func (p *processTransport) Close() error {
	var errs []error
	if p.stdin != nil {
		if err := p.stdin.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if p.stdout != nil {
		if err := p.stdout.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_ = p.cmd.Wait()
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// LSPDriverConfig defines the configuration parameters for initializing an LSPDriver.
type LSPDriverConfig struct {
	Name             string            `json:"name"`
	Command          string            `json:"command"`
	Args             []string          `json:"args,omitempty"`
	Languages        []string          `json:"languages"`
	RootURI          string            `json:"root_uri,omitempty"`
	WorkspaceDir     string            `json:"workspace_dir,omitempty"`
	Initialization   map[string]any    `json:"initialization_options,omitempty"`
	TransportFactory func() (LSPTransport, error) `json:"-"`
}

// LSPDriver implements CodeIntelligenceProvider for Language Server Protocol servers over JSON-RPC 2.0.
type LSPDriver struct {
	cfg       LSPDriverConfig
	mu        sync.Mutex
	writeMu   sync.Mutex
	transport LSPTransport
	reader    *bufio.Reader
	seq       atomic.Int64
	pending   map[int64]chan lspResponse
	diags     map[string][]Diagnostic
	diagsMu   sync.RWMutex
	diagWait  map[string]chan struct{}
	running   bool
	closed    bool
}

type lspRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type lspResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *lspError       `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type lspError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// LSP Symbol Kinds defined by the protocol
const (
	lspSymbolFile          = 1
	lspSymbolModule        = 2
	lspSymbolNamespace     = 3
	lspSymbolPackage       = 4
	lspSymbolClass         = 5
	lspSymbolMethod        = 6
	lspSymbolProperty      = 7
	lspSymbolField         = 8
	lspSymbolConstructor   = 9
	lspSymbolEnum          = 10
	lspSymbolInterface     = 11
	lspSymbolFunction      = 12
	lspSymbolVariable      = 13
	lspSymbolConstant      = 14
	lspSymbolString        = 15
	lspSymbolNumber        = 16
	lspSymbolBoolean       = 17
	lspSymbolArray         = 18
	lspSymbolObject        = 19
	lspSymbolKey           = 20
	lspSymbolNull          = 21
	lspSymbolEnumMember    = 22
	lspSymbolStruct        = 23
	lspSymbolEvent         = 24
	lspSymbolOperator      = 25
	lspSymbolTypeParameter = 26
)

// LSP protocol structs
type lspPosition struct {
	Line      int `json:"line"`      // 0-based
	Character int `json:"character"` // 0-based
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type lspLocationLink struct {
	TargetURI            string   `json:"targetUri"`
	TargetRange          lspRange `json:"targetRange"`
	TargetSelectionRange lspRange `json:"targetSelectionRange"`
}

type lspDocumentSymbol struct {
	Name           string              `json:"name"`
	Detail         string              `json:"detail,omitempty"`
	Kind           int                 `json:"kind"`
	Range          lspRange            `json:"range"`
	SelectionRange lspRange            `json:"selectionRange"`
	Children       []lspDocumentSymbol `json:"children,omitempty"`
}

type lspSymbolInformation struct {
	Name          string      `json:"name"`
	Kind          int         `json:"kind"`
	Location      lspLocation `json:"location"`
	ContainerName string      `json:"containerName,omitempty"`
}

type lspDiagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity,omitempty"` // 1: Error, 2: Warning, 3: Info, 4: Hint
	Code     any      `json:"code,omitempty"`
	Source   string   `json:"source,omitempty"`
	Message  string   `json:"message"`
}

type lspPublishDiagnosticsParams struct {
	URI         string          `json:"uri"`
	Diagnostics []lspDiagnostic `json:"diagnostics"`
}

// NewLSPDriver constructs a new Language Server Protocol driver.
func NewLSPDriver(cfg LSPDriverConfig) *LSPDriver {
	if cfg.Name == "" {
		cfg.Name = "lsp"
	}
	return &LSPDriver{
		cfg:      cfg,
		pending:  make(map[int64]chan lspResponse),
		diags:    make(map[string][]Diagnostic),
		diagWait: make(map[string]chan struct{}),
	}
}

// Name returns the identifier of the provider.
func (d *LSPDriver) Name() string {
	return d.cfg.Name
}

// Languages returns supported file extensions or language identifiers.
func (d *LSPDriver) Languages() []string {
	return d.cfg.Languages
}

// ensureRunning initializes and starts the LSP server process if not already running.
func (d *LSPDriver) ensureRunning(ctx context.Context) error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return errors.New("lsp driver is closed")
	}
	if d.running {
		d.mu.Unlock()
		return nil
	}

	var transport LSPTransport
	if d.cfg.TransportFactory != nil {
		var err error
		transport, err = d.cfg.TransportFactory()
		if err != nil {
			d.mu.Unlock()
			return fmt.Errorf("failed to create lsp transport: %w", err)
		}
	} else {
		cmdPath := d.cfg.Command
		if cmdPath == "" {
			d.mu.Unlock()
			return errors.New("lsp command cannot be empty")
		}

		cmd := exec.CommandContext(ctx, cmdPath, d.cfg.Args...)
		if d.cfg.WorkspaceDir != "" {
			cmd.Dir = d.cfg.WorkspaceDir
		}

		stdin, err := cmd.StdinPipe()
		if err != nil {
			d.mu.Unlock()
			return fmt.Errorf("failed to open stdin pipe: %w", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			stdin.Close()
			d.mu.Unlock()
			return fmt.Errorf("failed to open stdout pipe: %w", err)
		}

		if err := cmd.Start(); err != nil {
			stdin.Close()
			stdout.Close()
			d.mu.Unlock()
			return fmt.Errorf("failed to start lsp process %q: %w", cmdPath, err)
		}

		transport = &processTransport{
			cmd:    cmd,
			stdin:  stdin,
			stdout: stdout,
		}
	}

	d.transport = transport
	d.reader = bufio.NewReader(transport)
	d.running = true
	d.mu.Unlock()

	// Launch background reader loop
	go d.readLoop()

	// Perform initialize handshake
	rootURI := d.cfg.RootURI
	if rootURI == "" {
		if d.cfg.WorkspaceDir != "" {
			abs, _ := filepath.Abs(d.cfg.WorkspaceDir)
			rootURI = pathToURI(abs)
		} else {
			cwd, _ := os.Getwd()
			rootURI = pathToURI(cwd)
		}
	}

	initParams := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   rootURI,
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"documentSymbol": map[string]any{
					"hierarchicalDocumentSymbolSupport": true,
				},
				"definition": map[string]any{
					"linkSupport": true,
				},
				"references": map[string]any{},
				"publishDiagnostics": map[string]any{
					"relatedInformation": true,
				},
			},
		},
	}
	if len(d.cfg.Initialization) > 0 {
		initParams["initializationOptions"] = d.cfg.Initialization
	}

	var initResult json.RawMessage
	if err := d.call(ctx, "initialize", initParams, &initResult); err != nil {
		_ = d.Close()
		return fmt.Errorf("lsp initialize handshake failed: %w", err)
	}

	// Send initialized notification
	if err := d.notify("initialized", map[string]any{}); err != nil {
		_ = d.Close()
		return fmt.Errorf("lsp initialized notification failed: %w", err)
	}

	return nil
}

// Close gracefully terminates the LSP server session.
func (d *LSPDriver) Close() error {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	wasRunning := d.running
	d.running = false
	transport := d.transport
	d.mu.Unlock()

	if wasRunning && transport != nil {
		// Attempt graceful LSP shutdown handshake with a short timeout
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		var res json.RawMessage
		_ = d.call(shutdownCtx, "shutdown", nil, &res)
		cancel()

		_ = d.notify("exit", nil)
		return transport.Close()
	}

	return nil
}

func (d *LSPDriver) readLoop() {
	for {
		payload, err := readLSPMessage(d.reader)
		if err != nil {
			// Transport closed or broken
			d.mu.Lock()
			for id, ch := range d.pending {
				delete(d.pending, id)
				close(ch)
			}
			d.mu.Unlock()
			return
		}

		var resp lspResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			continue
		}

		// Check if it's a notification from server
		if resp.Method != "" {
			d.handleNotification(resp.Method, resp.Params)
			continue
		}

		// Dispatched to pending request
		if resp.ID != nil {
			d.mu.Lock()
			ch, ok := d.pending[*resp.ID]
			if ok {
				delete(d.pending, *resp.ID)
			}
			d.mu.Unlock()

			if ok && ch != nil {
				ch <- resp
			}
		}
	}
}

func (d *LSPDriver) handleNotification(method string, params json.RawMessage) {
	if method == "textDocument/publishDiagnostics" {
		var p lspPublishDiagnosticsParams
		if err := json.Unmarshal(params, &p); err == nil {
			filePath := uriToPath(p.URI)
			var diags []Diagnostic
			for _, diag := range p.Diagnostics {
				sev := SeverityError
				switch diag.Severity {
				case 2:
					sev = SeverityWarning
				case 3, 4:
					sev = SeverityInfo
				}
				rule := diag.Source
				if diag.Code != nil {
					rule = fmt.Sprintf("%v", diag.Code)
				}
				diags = append(diags, Diagnostic{
					Message:  diag.Message,
					Severity: sev,
					Line:     diag.Range.Start.Line + 1,
					Col:      diag.Range.Start.Character + 1,
					EndLine:  diag.Range.End.Line + 1,
					EndCol:   diag.Range.End.Character + 1,
					Rule:     rule,
				})
			}

			d.diagsMu.Lock()
			d.diags[filePath] = diags
			if ch, ok := d.diagWait[filePath]; ok {
				close(ch)
				delete(d.diagWait, filePath)
			}
			d.diagsMu.Unlock()
		}
	}
}

func (d *LSPDriver) call(ctx context.Context, method string, params any, result any) error {
	id := d.seq.Add(1)
	req := lspRequest{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  params,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}

	ch := make(chan lspResponse, 1)
	d.mu.Lock()
	if d.transport == nil || !d.running && method != "initialize" {
		d.mu.Unlock()
		return errors.New("lsp driver not running")
	}
	d.pending[id] = ch
	transport := d.transport
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		delete(d.pending, id)
		d.mu.Unlock()
	}()

	d.writeMu.Lock()
	err = writeLSPMessage(transport, payload)
	d.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("failed to write lsp request: %w", err)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return errors.New("lsp transport closed while awaiting response")
		}
		if resp.Error != nil {
			return fmt.Errorf("lsp error (%d): %s", resp.Error.Code, resp.Error.Message)
		}
		if result != nil && len(resp.Result) > 0 && string(resp.Result) != "null" {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("failed to unmarshal lsp response: %w", err)
			}
		}
		return nil
	}
}

func (d *LSPDriver) notify(method string, params any) error {
	req := lspRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}

	d.mu.Lock()
	transport := d.transport
	d.mu.Unlock()

	if transport == nil {
		return errors.New("lsp transport not available")
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()
	return writeLSPMessage(transport, payload)
}

func (d *LSPDriver) didOpen(filePath string, content []byte) error {
	uri := pathToURI(filePath)
	langID := detectLanguageID(filePath)

	params := map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": langID,
			"version":    1,
			"text":       string(content),
		},
	}
	return d.notify("textDocument/didOpen", params)
}

// GetOutline extracts a hierarchical symbol outline from the file content or file path.
func (d *LSPDriver) GetOutline(ctx context.Context, filePath string, content []byte) (*Outline, error) {
	if err := d.ensureRunning(ctx); err != nil {
		return nil, err
	}

	if len(content) == 0 && filePath != "" {
		var err error
		content, err = os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read source file: %w", err)
		}
	}

	if err := d.didOpen(filePath, content); err != nil {
		return nil, err
	}

	uri := pathToURI(filePath)
	params := map[string]any{
		"textDocument": map[string]any{
			"uri": uri,
		},
	}

	var raw json.RawMessage
	if err := d.call(ctx, "textDocument/documentSymbol", params, &raw); err != nil {
		return nil, err
	}

	outline := &Outline{
		FilePath: filePath,
		Language: detectLanguageID(filePath),
		Symbols:  make([]SymbolNode, 0),
	}

	if len(raw) == 0 || string(raw) == "null" {
		return outline, nil
	}

	type symbolProbe struct {
		Range    *lspRange    `json:"range"`
		Location *lspLocation `json:"location"`
	}
	var probes []symbolProbe
	if err := json.Unmarshal(raw, &probes); err == nil {
		if len(probes) == 0 {
			return outline, nil
		}
		if probes[0].Range != nil {
			var docSymbols []lspDocumentSymbol
			if err := json.Unmarshal(raw, &docSymbols); err == nil {
				for _, s := range docSymbols {
					outline.Symbols = append(outline.Symbols, convertDocSymbol(s))
				}
				return outline, nil
			}
		}
		if probes[0].Location != nil {
			var symInfos []lspSymbolInformation
			if err := json.Unmarshal(raw, &symInfos); err == nil {
				for _, s := range symInfos {
					outline.Symbols = append(outline.Symbols, convertSymbolInfo(s))
				}
				return outline, nil
			}
		}
	}

	return outline, nil
}

// FindDefinition locates symbol definitions matching coordinates (1-based line and column).
func (d *LSPDriver) FindDefinition(ctx context.Context, filePath string, line, col int) ([]Location, error) {
	if err := d.ensureRunning(ctx); err != nil {
		return nil, err
	}

	uri := pathToURI(filePath)
	params := map[string]any{
		"textDocument": map[string]any{
			"uri": uri,
		},
		"position": lspPosition{
			Line:      line - 1,
			Character: col - 1,
		},
	}

	var raw json.RawMessage
	if err := d.call(ctx, "textDocument/definition", params, &raw); err != nil {
		return nil, err
	}

	return parseLocationsResponse(raw)
}

// FindReferences finds symbol usage occurrences matching coordinates (1-based line and column).
func (d *LSPDriver) FindReferences(ctx context.Context, filePath string, line, col int) ([]Location, error) {
	if err := d.ensureRunning(ctx); err != nil {
		return nil, err
	}

	uri := pathToURI(filePath)
	params := map[string]any{
		"textDocument": map[string]any{
			"uri": uri,
		},
		"position": lspPosition{
			Line:      line - 1,
			Character: col - 1,
		},
		"context": map[string]any{
			"includeDeclaration": true,
		},
	}

	var raw json.RawMessage
	if err := d.call(ctx, "textDocument/references", params, &raw); err != nil {
		return nil, err
	}

	return parseLocationsResponse(raw)
}

// ValidateSyntax parses the content and checks for language syntax errors via diagnostics.
func (d *LSPDriver) ValidateSyntax(ctx context.Context, filePath string, content []byte) ([]Diagnostic, error) {
	if err := d.ensureRunning(ctx); err != nil {
		return nil, err
	}

	if len(content) == 0 && filePath != "" {
		var err error
		content, err = os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read source file: %w", err)
		}
	}

	// Prepare diagnostic waiter
	waitCh := make(chan struct{})
	d.diagsMu.Lock()
	d.diagWait[filePath] = waitCh
	d.diagsMu.Unlock()

	if err := d.didOpen(filePath, content); err != nil {
		d.diagsMu.Lock()
		delete(d.diagWait, filePath)
		d.diagsMu.Unlock()
		return nil, err
	}

	// Wait briefly for server to publish diagnostics, or check existing
	select {
	case <-waitCh:
	case <-time.After(200 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	d.diagsMu.RLock()
	defer d.diagsMu.RUnlock()
	if diags, ok := d.diags[filePath]; ok {
		return diags, nil
	}

	return []Diagnostic{}, nil
}

// Convert DocumentSymbol to codeintel.SymbolNode
func convertDocSymbol(s lspDocumentSymbol) SymbolNode {
	kind := mapLSPKind(s.Kind)
	node := SymbolNode{
		Name:      s.Name,
		Kind:      kind,
		Signature: s.Detail,
		Line:      s.Range.Start.Line + 1,
		Col:       s.Range.Start.Character + 1,
		EndLine:   s.Range.End.Line + 1,
		EndCol:    s.Range.End.Character + 1,
	}
	if node.Signature == "" {
		node.Signature = s.Name
	}
	for _, child := range s.Children {
		node.Children = append(node.Children, convertDocSymbol(child))
	}
	return node
}

// Convert SymbolInformation to codeintel.SymbolNode
func convertSymbolInfo(s lspSymbolInformation) SymbolNode {
	kind := mapLSPKind(s.Kind)
	return SymbolNode{
		Name:      s.Name,
		Kind:      kind,
		Signature: s.Name,
		Line:      s.Location.Range.Start.Line + 1,
		Col:       s.Location.Range.Start.Character + 1,
		EndLine:   s.Location.Range.End.Line + 1,
		EndCol:    s.Location.Range.End.Character + 1,
	}
}

func parseLocationsResponse(raw json.RawMessage) ([]Location, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []Location{}, nil
	}

	// 1. Single Location
	var single lspLocation
	if err := json.Unmarshal(raw, &single); err == nil && single.URI != "" {
		return []Location{{
			FilePath: uriToPath(single.URI),
			Line:     single.Range.Start.Line + 1,
			Col:      single.Range.Start.Character + 1,
			EndLine:  single.Range.End.Line + 1,
			EndCol:   single.Range.End.Character + 1,
		}}, nil
	}

	// 2. Probe array items for Location vs LocationLink
	type locProbe struct {
		URI       string `json:"uri"`
		TargetURI string `json:"targetUri"`
	}
	var probes []locProbe
	if err := json.Unmarshal(raw, &probes); err == nil {
		if len(probes) == 0 {
			return []Location{}, nil
		}
		if probes[0].TargetURI != "" {
			var links []lspLocationLink
			if err := json.Unmarshal(raw, &links); err == nil {
				res := make([]Location, 0, len(links))
				for _, l := range links {
					rng := l.TargetSelectionRange
					if rng.Start.Line == 0 && rng.End.Line == 0 && rng.Start.Character == 0 && rng.End.Character == 0 {
						rng = l.TargetRange
					}
					res = append(res, Location{
						FilePath: uriToPath(l.TargetURI),
						Line:     rng.Start.Line + 1,
						Col:      rng.Start.Character + 1,
						EndLine:  rng.End.Line + 1,
						EndCol:   rng.End.Character + 1,
					})
				}
				return res, nil
			}
		}
		if probes[0].URI != "" {
			var locs []lspLocation
			if err := json.Unmarshal(raw, &locs); err == nil {
				res := make([]Location, 0, len(locs))
				for _, l := range locs {
					res = append(res, Location{
						FilePath: uriToPath(l.URI),
						Line:     l.Range.Start.Line + 1,
						Col:      l.Range.Start.Character + 1,
						EndLine:  l.Range.End.Line + 1,
						EndCol:   l.Range.End.Character + 1,
					})
				}
				return res, nil
			}
		}
	}

	return []Location{}, nil
}

func mapLSPKind(kind int) SymbolKind {
	switch kind {
	case lspSymbolPackage, lspSymbolModule:
		return SymbolKindPackage
	case lspSymbolClass, lspSymbolStruct:
		return SymbolKindStruct
	case lspSymbolInterface:
		return SymbolKindInterface
	case lspSymbolFunction, lspSymbolConstructor:
		return SymbolKindFunction
	case lspSymbolMethod:
		return SymbolKindMethod
	case lspSymbolConstant:
		return SymbolKindConstant
	case lspSymbolVariable, lspSymbolField, lspSymbolProperty:
		return SymbolKindVariable
	default:
		return SymbolKindType
	}
}

func pathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	abs = filepath.ToSlash(abs)
	if !strings.HasPrefix(abs, "/") {
		abs = "/" + abs
	}
	return "file://" + abs
}

func uriToPath(uri string) string {
	if strings.HasPrefix(uri, "file://") {
		parsed, err := url.Parse(uri)
		if err == nil {
			return filepath.FromSlash(parsed.Path)
		}
		return strings.TrimPrefix(uri, "file://")
	}
	return uri
}

func detectLanguageID(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	case ".json":
		return "json"
	case ".c":
		return "c"
	case ".cpp", ".cc", ".cxx":
		return "cpp"
	default:
		return strings.TrimPrefix(ext, ".")
	}
}

// readLSPMessage parses a JSON-RPC message with LSP Content-Length framing.
func readLSPMessage(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			// End of headers
			break
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "content-length:") {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				val := strings.TrimSpace(parts[1])
				if n, err := strconv.Atoi(val); err == nil {
					contentLength = n
				}
			}
		}
	}

	if contentLength < 0 {
		return nil, errors.New("missing Content-Length header in LSP message")
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}

	return body, nil
}

// writeLSPMessage encodes a JSON payload with standard Content-Length HTTP-style header framing in a single buffer write.
func writeLSPMessage(writer io.Writer, payload []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(payload))
	msg := make([]byte, 0, len(header)+len(payload))
	msg = append(msg, header...)
	msg = append(msg, payload...)
	_, err := writer.Write(msg)
	return err
}
