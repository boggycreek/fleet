// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package libbpd

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
)

var defaultAgentRunnerPath = "/usr/local/bin/agent-runner"

// BackplaneClient defines operations required by Daemon to interact with Valkey.
type BackplaneClient interface {
	Recv(ctx context.Context, blockSeconds int) ([]*libbp.Message, error)
	GetPollInterval(ctx context.Context) (int, error)
	SetStatus(ctx context.Context, statusText string) error
	Close() error
}

// EnsembleClient represents backplane operations for ensemble channel discovery and message polling.
type EnsembleClient interface {
	ListJoinedEnsembles(ctx context.Context) ([]string, error)
	ReadEnsembleMessages(ctx context.Context, ensembleName, lastID string, count int) ([]*libbp.Message, error)
	GetCursor(ctx context.Context, streamKey string) (string, error)
	SetCursor(ctx context.Context, streamKey string, lastID string) error
}

// RunnerFunc represents a command execution function for running headless agent turns.
type RunnerFunc func(ctx context.Context, cmdName string, cmdArgs []string, stdin io.Reader, stdout, stderr io.Writer) error

// SleeperFunc represents a sleep function that respects context cancellation.
type SleeperFunc func(ctx context.Context, d time.Duration) error

// Config holds daemon configuration options.
type Config struct {
	AgentName              string
	StateDir               string
	DefaultIntervalSecs    int
	ClaudeTimeoutSecs      int
	MaxConsecutiveFailures int
	AttachLockFile         string
	RunnerCmd              string
}

// Daemon manages event-driven headless agent turn execution with Valkey streams.
type Daemon struct {
	cfg     *Config
	client  BackplaneClient
	runner  RunnerFunc
	sleeper SleeperFunc
	stdout  io.Writer
	stderr  io.Writer
}

func defaultRunner(ctx context.Context, cmdName string, cmdArgs []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, cmdName, cmdArgs...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func defaultSleeper(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// LoadConfigFromEnv reads configuration from environment variables with defaults.
func LoadConfigFromEnv() (*Config, error) {
	agentName := os.Getenv("AGENT_NAME")
	if agentName == "" {
		return nil, errors.New("AGENT_NAME environment variable is required")
	}

	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/tmp"
	}

	stateDir := os.Getenv("BPD_STATE_DIR")
	if stateDir == "" {
		stateDir = filepath.Join(home, ".bpd")
	}

	defaultInterval := 60
	if val := os.Getenv("BPD_DEFAULT_INTERVAL_SECS"); val != "" {
		if i, err := strconv.Atoi(val); err == nil && i > 0 {
			defaultInterval = i
		}
	}

	claudeTimeout := 600
	if val := os.Getenv("BPD_CLAUDE_TIMEOUT_SECS"); val != "" {
		if i, err := strconv.Atoi(val); err == nil && i > 0 {
			claudeTimeout = i
		}
	}

	maxFailures := 3
	if val := os.Getenv("BPD_MAX_CONSECUTIVE_FAILURES"); val != "" {
		if i, err := strconv.Atoi(val); err == nil && i > 0 {
			maxFailures = i
		}
	}

	attachLockFile := os.Getenv("BPD_ATTACH_LOCK_FILE")
	if attachLockFile == "" {
		attachLockFile = filepath.Join(home, ".bpd-attached")
	}

	runnerCmd := os.Getenv("BPD_RUNNER_CMD")
	if runnerCmd == "" {
		if fi, err := os.Stat(defaultAgentRunnerPath); err == nil && !fi.IsDir() {
			runnerCmd = defaultAgentRunnerPath
		} else {
			runnerCmd = "claude -p"
		}
	}

	return &Config{
		AgentName:              agentName,
		StateDir:               stateDir,
		DefaultIntervalSecs:    defaultInterval,
		ClaudeTimeoutSecs:      claudeTimeout,
		MaxConsecutiveFailures: maxFailures,
		AttachLockFile:         attachLockFile,
		RunnerCmd:              runnerCmd,
	}, nil
}

// NewDaemon initializes a new Daemon instance.
func NewDaemon(cfg *Config, client BackplaneClient, stdout, stderr io.Writer) (*Daemon, error) {
	if cfg == nil {
		return nil, errors.New("config cannot be nil")
	}
	if client == nil {
		return nil, errors.New("client cannot be nil")
	}
	if cfg.AgentName == "" {
		return nil, errors.New("config.AgentName cannot be empty")
	}
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	if err := os.MkdirAll(cfg.StateDir, 0700); err != nil {
		return nil, fmt.Errorf("failed creating state dir %s: %w", cfg.StateDir, err)
	}

	return &Daemon{
		cfg:     cfg,
		client:  client,
		runner:  defaultRunner,
		sleeper: defaultSleeper,
		stdout:  stdout,
		stderr:  stderr,
	}, nil
}

// SetRunner overrides the command runner for testing or custom execution environments.
func (d *Daemon) SetRunner(r RunnerFunc) {
	d.runner = r
}

// SetSleeper overrides the sleep function for testing.
func (d *Daemon) SetSleeper(s SleeperFunc) {
	d.sleeper = s
}

// SetDefaultAgentRunnerPath overrides the fallback runner path (used in unit testing).
func SetDefaultAgentRunnerPath(p string) {
	defaultAgentRunnerPath = p
}

// FormatMessage formats a backplane message into a human- and runner-readable text log line.
func FormatMessage(m *libbp.Message) string {
	ts := time.UnixMilli(m.Timestamp).Format("15:04:05")
	target := ""
	if m.Destination != "" {
		target = fmt.Sprintf(" -> %s", m.Destination)
	}
	sigStatus := ""
	if m.IsSigned {
		if m.IsVerified {
			sigStatus = " [verified]"
		} else {
			sigStatus = " [UNVERIFIED]"
		}
	}
	line := fmt.Sprintf("%s [%s%s%s]: %s\n", ts, m.Citation, target, sigStatus, m.Content)
	if m.BlobPath != "" {
		line += fmt.Sprintf("  └── Payload attached: %s\n", m.BlobPath)
	}
	return line
}

func generateUUID() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	if err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // RFC 4122 version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func (d *Daemon) sessionIDFile() string {
	return filepath.Join(d.cfg.StateDir, "session-id")
}

func (d *Daemon) pendingBatchFile() string {
	return filepath.Join(d.cfg.StateDir, "pending-batch.txt")
}

func (d *Daemon) consecutiveFailuresFile() string {
	return filepath.Join(d.cfg.StateDir, "consecutive-failures")
}

func (d *Daemon) getSessionID() (string, error) {
	data, err := os.ReadFile(d.sessionIDFile())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (d *Daemon) setSessionID(id string) error {
	return os.WriteFile(d.sessionIDFile(), []byte(id+"\n"), 0600)
}

func (d *Daemon) getConsecutiveFailures() int {
	data, err := os.ReadFile(d.consecutiveFailuresFile())
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return n
}

func (d *Daemon) setConsecutiveFailures(n int) error {
	return os.WriteFile(d.consecutiveFailuresFile(), []byte(strconv.Itoa(n)+"\n"), 0600)
}

func (d *Daemon) appendPendingBatch(text string) error {
	f, err := os.OpenFile(d.pendingBatchFile(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text)
	return err
}

func (d *Daemon) readPendingBatch() (string, error) {
	data, err := os.ReadFile(d.pendingBatchFile())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

func (d *Daemon) clearPendingBatch() error {
	return os.Remove(d.pendingBatchFile())
}

// Tick executes a single cycle of the daemon: checks attach lock, drains messages, and runs runner if needed.
// If optional blockSeconds is supplied and > 0, it blocks on Valkey streams via XREAD BLOCK.
func (d *Daemon) Tick(ctx context.Context, blockSeconds ...int) error {
	// 1. Advisory Attach Lock check
	if _, err := os.Stat(d.cfg.AttachLockFile); err == nil {
		fmt.Fprintf(d.stdout, "[bpd] Human is attached (%s exists); skipping tick\n", d.cfg.AttachLockFile)
		return nil
	}

	blockSec := 0
	if len(blockSeconds) > 0 && blockSeconds[0] > 0 {
		blockSec = blockSeconds[0]
	}

	// 2. Drain pending messages from backplane (optionally blocking via XREAD BLOCK)
	messages, err := d.client.Recv(ctx, blockSec)
	if err != nil {
		fmt.Fprintf(d.stderr, "[bpd] Error draining messages from backplane: %v\n", err)
	}

	// 2b. Drain messages from joined ensembles
	if ensembleClient, ok := d.client.(EnsembleClient); ok {
		ensembles, ensErr := ensembleClient.ListJoinedEnsembles(ctx)
		if ensErr == nil {
			for _, ens := range ensembles {
				streamKey := libbp.EnsembleOutKey(ens)
				cursor, _ := ensembleClient.GetCursor(ctx, streamKey)
				if cursor == "" {
					cursor = "0-0"
				}
				ensMsgs, readErr := ensembleClient.ReadEnsembleMessages(ctx, ens, cursor, 50)
				if readErr == nil && len(ensMsgs) > 0 {
					messages = append(messages, ensMsgs...)
					lastMsg := ensMsgs[len(ensMsgs)-1]
					_ = ensembleClient.SetCursor(ctx, streamKey, lastMsg.ID)
				}
			}
		}
	}

	if len(messages) > 0 {
		var buf bytes.Buffer
		for _, m := range messages {
			buf.WriteString(FormatMessage(m))
		}
		if err := d.appendPendingBatch(buf.String()); err != nil {
			fmt.Fprintf(d.stderr, "[bpd] Error writing to pending batch: %v\n", err)
			return err
		}
		fmt.Fprintf(d.stdout, "[bpd] Drained %d new message(s) into pending batch\n", len(messages))
	}

	// 3. Check durable pending batch
	batchContent, err := d.readPendingBatch()
	if err != nil {
		return fmt.Errorf("failed reading pending batch: %w", err)
	}
	if strings.TrimSpace(batchContent) == "" {
		return nil
	}

	// 4. Session management
	sessionID, _ := d.getSessionID()
	var sessionArgs []string
	if sessionID == "" {
		newID, err := generateUUID()
		if err != nil {
			return fmt.Errorf("failed generating session UUID: %w", err)
		}
		sessionID = newID
		if err := d.setSessionID(sessionID); err != nil {
			return fmt.Errorf("failed saving session-id: %w", err)
		}
		sessionArgs = []string{"--session-id", sessionID}
		fmt.Fprintf(d.stdout, "[bpd] Initialized new session: %s\n", sessionID)
	} else {
		sessionArgs = []string{"--resume", sessionID}
		fmt.Fprintf(d.stdout, "[bpd] Resuming existing session: %s\n", sessionID)
	}

	_ = os.Setenv("AGENT_SESSION_ID", sessionID)
	_ = os.Setenv("AGENT_NAME", d.cfg.AgentName)

	// 5. Build runner invocation
	runnerParts := strings.Fields(d.cfg.RunnerCmd)
	if len(runnerParts) == 0 {
		return errors.New("empty runner command configured")
	}
	cmdName := runnerParts[0]
	cmdArgs := append(runnerParts[1:], sessionArgs...)

	// 6. Invoke runner with configured timeout
	timeout := time.Duration(d.cfg.ClaudeTimeoutSecs) * time.Second
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	fmt.Fprintf(d.stdout, "[bpd] Executing runner: %s %s\n", cmdName, strings.Join(cmdArgs, " "))
	stdin := strings.NewReader(batchContent)
	runErr := d.runner(execCtx, cmdName, cmdArgs, stdin, d.stdout, d.stderr)

	nowStr := time.Now().UTC().Format(time.RFC3339)
	if runErr == nil {
		// Runner succeeded
		fmt.Fprintf(d.stdout, "[bpd] Runner completed successfully\n")
		_ = d.setConsecutiveFailures(0)
		_ = d.clearPendingBatch()
		statusMsg := fmt.Sprintf("ok (bpd, last processed %s)", nowStr)
		if err := d.client.SetStatus(ctx, statusMsg); err != nil {
			fmt.Fprintf(d.stderr, "[bpd] Failed updating backplane status: %v\n", err)
		}
		return nil
	}

	// Runner failed
	failures := d.getConsecutiveFailures() + 1
	_ = d.setConsecutiveFailures(failures)
	fmt.Fprintf(d.stderr, "[bpd] Runner failed (consecutive failures: %d): %v\n", failures, runErr)

	if failures >= d.cfg.MaxConsecutiveFailures {
		degradedMsg := fmt.Sprintf("DEGRADED: bpd has failed %d consecutive headless invocations (last error at %s: %v)", failures, nowStr, runErr)
		fmt.Fprintf(d.stderr, "[bpd] Transitioning to degraded status: %s\n", degradedMsg)
		if err := d.client.SetStatus(ctx, degradedMsg); err != nil {
			fmt.Fprintf(d.stderr, "[bpd] Failed setting degraded status: %v\n", err)
		}
	}
	return runErr
}

// Run executes the daemon loop with blocking stream polling until context is canceled.
func (d *Daemon) Run(ctx context.Context) error {
	fmt.Fprintf(d.stdout, "[bpd] Starting Backplane Daemon for agent %s\n", d.cfg.AgentName)
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(d.stdout, "[bpd] Stopping daemon (context canceled)")
			return nil
		default:
		}

		// Read dynamic poll interval
		intervalSecs, err := d.client.GetPollInterval(ctx)
		if err != nil || intervalSecs < libbp.MinPollInterval || intervalSecs > libbp.MaxPollInterval {
			intervalSecs = d.cfg.DefaultIntervalSecs
		}

		// Block on Valkey streams with intervalSecs
		if err := d.Tick(ctx, intervalSecs); err != nil {
			fmt.Fprintf(d.stderr, "[bpd] Error processing tick: %v\n", err)
			// Apply backoff sleep on error to prevent busy spinning if server is unreachable
			backoff := time.Duration(1) * time.Second
			if intervalSecs > 1 {
				backoff = time.Duration(intervalSecs) * time.Second
			}
			if sleepErr := d.sleeper(ctx, backoff); sleepErr != nil {
				if errors.Is(sleepErr, context.Canceled) {
					fmt.Fprintln(d.stdout, "[bpd] Stopping daemon (context canceled)")
					return nil
				}
				return sleepErr
			}
			continue
		}

		// If attach lock file exists, sleep until next check
		if _, err := os.Stat(d.cfg.AttachLockFile); err == nil {
			if err := d.sleeper(ctx, time.Duration(intervalSecs)*time.Second); err != nil {
				if errors.Is(err, context.Canceled) {
					fmt.Fprintln(d.stdout, "[bpd] Stopping daemon (context canceled)")
					return nil
				}
				return err
			}
		}
	}
}
