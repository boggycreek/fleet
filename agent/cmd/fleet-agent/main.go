// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/boggycreek/fleet/agent/pkg/engine"
	"github.com/boggycreek/fleet/agent/pkg/gateway"
	"github.com/boggycreek/fleet/agent/pkg/runtime"
	"github.com/boggycreek/fleet/agent/pkg/tools"
	"github.com/boggycreek/fleet/backplane/pkg/libbp"
)

// Config encapsulates runtime parameters for fleet-agent.
type Config struct {
	AgentName          string
	AgentRole          string
	WorkspaceDir       string
	StateFile          string
	OpenAIBaseURL      string
	OpenAIAPIKey       string
	OpenAIModel        string
	ValkeyAddr         string
	ValkeyPassword     string
	MCPBinaries        []string
	CustomInstructions string
	LogFormat          string
}

// LogEntry models structured NDJSON output.
type LogEntry struct {
	Timestamp string         `json:"time"`
	Level     string         `json:"level"`
	Message   string         `json:"msg"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// Logger formats output as NDJSON or plain text.
type Logger struct {
	out       io.Writer
	logFormat string
}

// NewLogger creates a new Logger.
func NewLogger(out io.Writer, logFormat string) *Logger {
	if out == nil {
		out = os.Stdout
	}
	return &Logger{out: out, logFormat: logFormat}
}

// Log emits a log entry.
func (l *Logger) Log(level, msg string, fields map[string]any) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if l.logFormat == "text" {
		var fieldStr string
		if len(fields) > 0 {
			b, _ := json.Marshal(fields)
			fieldStr = " " + string(b)
		}
		_, _ = fmt.Fprintf(l.out, "[%s] %s: %s%s\n", now, level, msg, fieldStr)
		return
	}

	entry := LogEntry{
		Timestamp: now,
		Level:     level,
		Message:   msg,
		Fields:    fields,
	}
	bytes, _ := json.Marshal(entry)
	_, _ = fmt.Fprintln(l.out, string(bytes))
}

// LoadConfig parses flags and environment variables.
func LoadConfig(args []string) (*Config, error) {
	fs := flag.NewFlagSet("fleet-agent", flag.ContinueOnError)

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "fleet-agent"
	}

	cwd, _ := os.Getwd()
	defaultWorkspace := os.Getenv("WORKSPACE_DIR")
	if defaultWorkspace == "" {
		defaultWorkspace = "/home/agent/workspace"
		if _, err := os.Stat(defaultWorkspace); err != nil && cwd != "" {
			defaultWorkspace = cwd
		}
	}

	name := fs.String("name", getEnvOr("AGENT_NAME", hostname), "Agent identity name")
	role := fs.String("role", getEnvOr("AGENT_ROLE", "General Software Engineer"), "Specialized agent role")
	workspace := fs.String("workspace", defaultWorkspace, "Path to agent workspace directory")
	stateFile := fs.String("state-file", getEnvOr("STATE_FILE", ""), "Path to agent shared state file")
	modelURL := fs.String("model-url", getEnvOr("OPENAI_BASE_URL", "http://llm-gateway:11434/v1"), "OpenAI-compatible LLM endpoint URL")
	modelKey := fs.String("model-key", getEnvOr("OPENAI_API_KEY", ""), "OpenAI API authorization key")
	modelName := fs.String("model-name", getEnvOr("OPENAI_MODEL", "qwen2.5-coder:7b"), "Model name identifier")

	defaultValkeyAddr := os.Getenv("VALKEY_ADDR")
	if defaultValkeyAddr == "" && os.Getenv("BP_HOST") != "" {
		bpPort := os.Getenv("BP_PORT")
		if bpPort == "" {
			bpPort = "6379"
		}
		defaultValkeyAddr = net.JoinHostPort(os.Getenv("BP_HOST"), bpPort)
	}

	defaultValkeyPass := os.Getenv("VALKEY_PASSWORD")
	if defaultValkeyPass == "" {
		defaultValkeyPass = os.Getenv("BP_PASSWORD")
	}

	valkeyAddr := fs.String("valkey-addr", defaultValkeyAddr, "Valkey server address (host:port)")
	valkeyPass := fs.String("valkey-password", defaultValkeyPass, "Valkey authentication password")
	mcpBins := fs.String("mcp-binaries", getEnvOr("MCP_BINARIES", ""), "Comma-separated paths to MCP executable binaries")
	logFmt := fs.String("log-format", getEnvOr("LOG_FORMAT", "ndjson"), "Log format (ndjson or text)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	resolvedStateFile := *stateFile
	if resolvedStateFile == "" {
		resolvedStateFile = filepath.Join(*workspace, ".agent_state.json")
	}

	var mcpList []string
	if strings.TrimSpace(*mcpBins) != "" {
		parts := strings.Split(*mcpBins, ",")
		mcpList = make([]string, 0, len(parts))
		for _, b := range parts {
			trimmed := strings.TrimSpace(b)
			if trimmed != "" {
				mcpList = append(mcpList, trimmed)
			}
		}
	} else {
		// Auto-discover standard in-container MCP binaries if present
		standardBins := []string{
			"/usr/local/bin/beads-mcp",
			"/usr/local/bin/bp-mcp",
			"/usr/local/bin/forgejo-mcp",
			"/usr/local/bin/sonar-mcp",
		}
		if _, err := os.Stat("/usr/local/bin/forgejo-mcp"); err != nil {
			if _, err := os.Stat("/usr/local/bin/gitea-mcp"); err == nil {
				standardBins = append(standardBins, "/usr/local/bin/gitea-mcp")
			}
		}
		for _, binPath := range standardBins {
			if info, err := os.Stat(binPath); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
				mcpList = append(mcpList, binPath)
			}
		}
	}

	return &Config{
		AgentName:      *name,
		AgentRole:      *role,
		WorkspaceDir:   *workspace,
		StateFile:      resolvedStateFile,
		OpenAIBaseURL:  *modelURL,
		OpenAIAPIKey:   *modelKey,
		OpenAIModel:    *modelName,
		ValkeyAddr:     *valkeyAddr,
		ValkeyPassword: *valkeyPass,
		MCPBinaries:    mcpList,
		LogFormat:      *logFmt,
	}, nil
}

func getEnvOr(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

// StartZombieReaper reaps orphaned child processes asynchronously if running as PID 1.
func StartZombieReaper(ctx context.Context, logger *Logger) {
	// Only run zombie reaper when running as container PID 1 or if explicitly forced for testing
	if os.Getpid() != 1 && os.Getenv("FORCE_ZOMBIE_REAPER") != "1" {
		return
	}

	sigCh := make(chan os.Signal, 32)
	signal.Notify(sigCh, syscall.SIGCHLD)

	go func() {
		defer signal.Stop(sigCh)
		for {
			select {
			case <-ctx.Done():
				return
			case <-sigCh:
				// Reap terminated child processes non-blockingly
				for {
					var ws syscall.WaitStatus
					pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
					if err != nil || pid <= 0 {
						break
					}
					isManaged := runtime.IsManagedChildPID(pid)
					runtime.RecordReapedExit(pid, ws.ExitStatus())
					if logger != nil {
						logger.Log("DEBUG", "Reaped child process", map[string]any{
							"pid":      pid,
							"exitCode": ws.ExitStatus(),
							"managed":  isManaged,
						})
					}
				}
			}
		}
	}()
}

// Run executes the agent runtime with configured subsystems.
func Run(ctx context.Context, cfg *Config, out io.Writer) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	logger := NewLogger(out, cfg.LogFormat)
	logger.Log("INFO", "Starting fleet-agent runtime", map[string]any{
		"agentName":    cfg.AgentName,
		"role":         cfg.AgentRole,
		"workspaceDir": cfg.WorkspaceDir,
		"model":        cfg.OpenAIModel,
		"modelURL":     cfg.OpenAIBaseURL,
		"pid":          os.Getpid(),
	})

	// Start zombie reaper
	StartZombieReaper(runCtx, logger)

	// Initialize runtime core
	bus := runtime.NewEventBus()
	state := runtime.NewSharedState(cfg.StateFile, cfg.AgentName)
	supervisor := runtime.NewSupervisor(bus, state)

	// Initialize tools registry and register built-in workspace tools
	reg := tools.NewRegistry()
	if err := tools.RegisterBuiltinTools(reg, cfg.WorkspaceDir); err != nil {
		logger.Log("WARN", "Failed to register builtin tools", map[string]any{"error": err.Error()})
	}

	// Register any configured MCP server binaries
	var mcpClients []*tools.MCPClient
	defer func() {
		for _, c := range mcpClients {
			_ = c.Close()
		}
	}()

	for _, binPath := range cfg.MCPBinaries {
		mcpClient, err := tools.NewProcessMCPClient(runCtx, binPath)
		if err != nil {
			logger.Log("WARN", "Failed to start MCP server", map[string]any{"binary": binPath, "error": err.Error()})
			continue
		}
		initCtx, initCancel := context.WithTimeout(runCtx, 5*time.Second)
		if err := mcpClient.Initialize(initCtx); err != nil {
			initCancel()
			_ = mcpClient.Close()
			logger.Log("WARN", "Failed to initialize MCP client", map[string]any{"binary": binPath, "error": err.Error()})
			continue
		}
		initCancel()

		discCtx, discCancel := context.WithTimeout(runCtx, 5*time.Second)
		mcpTools, err := mcpClient.DiscoverTools(discCtx)
		discCancel()
		if err != nil {
			_ = mcpClient.Close()
			logger.Log("WARN", "Failed to discover MCP tools", map[string]any{"binary": binPath, "error": err.Error()})
			continue
		}

		mcpClients = append(mcpClients, mcpClient)
		for _, t := range mcpTools {
			_ = reg.Register(t)
		}
		logger.Log("INFO", "Registered MCP tools", map[string]any{"binary": binPath, "count": len(mcpTools)})
	}

	// Initialize LLM Client & REPL Engine
	llmClient := engine.NewLLMClient(cfg.OpenAIBaseURL, cfg.OpenAIAPIKey, cfg.OpenAIModel, 90*time.Second)
	replEngine := engine.NewREPLEngine(llmClient, reg, cfg.WorkspaceDir, cfg.AgentRole)
	if cfg.CustomInstructions != "" {
		replEngine.SetCustomInstructions(cfg.CustomInstructions)
	}
	humanName := getEnvOr("HUMAN_NAME", "operator")
	replEngine.SetHumanName(humanName)
	_ = supervisor.Register(replEngine)

	// Initialize Valkey Gateway if address is configured
	if cfg.ValkeyAddr != "" {
		host, portStr, err := net.SplitHostPort(cfg.ValkeyAddr)
		port := 6379
		if err == nil {
			if p, convErr := strconv.Atoi(portStr); convErr == nil {
				port = p
			}
		} else {
			host = cfg.ValkeyAddr
		}
		dialCtx, dialCancel := context.WithTimeout(runCtx, 5*time.Second)
		bpClient, dialErr := libbp.Dial(dialCtx, libbp.ClientConfig{
			Host:     host,
			Port:     port,
			AgentID:  cfg.AgentName,
			Username: cfg.AgentName,
			Password: cfg.ValkeyPassword,
		})
		dialCancel()
		if dialErr != nil {
			logger.Log("WARN", "Failed to connect to Valkey", map[string]any{"addr": cfg.ValkeyAddr, "error": dialErr.Error()})
		} else {
			defer bpClient.Close()
			resolver := gateway.NewBackplaneKeyResolver(bpClient)
			verifier := gateway.NewVerifier(resolver)
			gw := gateway.NewValkeyGateway(cfg.AgentName, bpClient, verifier, 1)
			gw.SetCancelFunc(cancel)
			_ = supervisor.Register(gw)
			replEngine.SetResponder(&backplaneResponder{client: bpClient})
			replEngine.SetAgentID(cfg.AgentName)
			logger.Log("INFO", "Configured Valkey gateway subsystem with verified signatures", map[string]any{"addr": cfg.ValkeyAddr})
		}
	}

	logger.Log("INFO", "Starting supervisor workers", map[string]any{"workerCount": len(supervisor.Subsystems())})

	// Run supervisor until context cancellation or worker error
	err := supervisor.Start(runCtx)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		logger.Log("ERROR", "Supervisor exited with error", map[string]any{"error": err.Error()})
		return err
	}

	logger.Log("INFO", "fleet-agent runtime shut down cleanly", nil)
	return nil
}

// backplaneResponder delivers cognitive turn responses directly over the backplane.
type backplaneResponder struct {
	client *libbp.Client
}

func (r *backplaneResponder) SendReply(ctx context.Context, recipient, content string) error {
	if r == nil || r.client == nil || strings.TrimSpace(recipient) == "" || strings.TrimSpace(content) == "" {
		return nil
	}
	_, err := r.client.Tell(ctx, recipient, content)
	return err
}

func runMain(ctx context.Context, args []string, out io.Writer) error {
	cfg, err := LoadConfig(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("error parsing configuration: %w", err)
	}
	return Run(ctx, cfg, out)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := runMain(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
