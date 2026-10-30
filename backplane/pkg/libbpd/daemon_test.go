// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package libbpd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
	"github.com/boggycreek/fleet/backplane/test/harness"
)

type mockBackplaneClient struct {
	mu           sync.Mutex
	recvMessages []*libbp.Message
	recvErr      error
	pollInterval int
	pollErr      error
	lastStatus   string
	statusErr    error
	recvCalled   int
	statusCalled int
	lastBlockSec int

	ensembles       []string
	ensemblesErr    error
	ensembleMsgs    map[string][]*libbp.Message
	ensembleErr     error
	cursors         map[string]string
	setCursorCalled map[string]string
}

func (m *mockBackplaneClient) Recv(ctx context.Context, blockSeconds int) ([]*libbp.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recvCalled++
	m.lastBlockSec = blockSeconds
	if m.recvErr != nil {
		return nil, m.recvErr
	}
	msgs := m.recvMessages
	m.recvMessages = nil // Drain
	return msgs, nil
}

func (m *mockBackplaneClient) ListJoinedEnsembles(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ensemblesErr != nil {
		return nil, m.ensemblesErr
	}
	return m.ensembles, nil
}

func (m *mockBackplaneClient) ReadEnsembleMessages(ctx context.Context, ensembleName, lastID string, count int) ([]*libbp.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ensembleErr != nil {
		return nil, m.ensembleErr
	}
	if m.ensembleMsgs == nil {
		return nil, nil
	}
	msgs := m.ensembleMsgs[ensembleName]
	delete(m.ensembleMsgs, ensembleName)
	return msgs, nil
}

func (m *mockBackplaneClient) GetCursor(ctx context.Context, streamKey string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cursors == nil {
		return "0-0", nil
	}
	return m.cursors[streamKey], nil
}

func (m *mockBackplaneClient) SetCursor(ctx context.Context, streamKey string, lastID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.setCursorCalled == nil {
		m.setCursorCalled = make(map[string]string)
	}
	m.setCursorCalled[streamKey] = lastID
	return nil
}

func (m *mockBackplaneClient) GetPollInterval(ctx context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pollErr != nil {
		return 0, m.pollErr
	}
	if m.pollInterval == 0 {
		return 60, nil
	}
	return m.pollInterval, nil
}

func (m *mockBackplaneClient) SetStatus(ctx context.Context, statusText string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statusCalled++
	m.lastStatus = statusText
	return m.statusErr
}

func (m *mockBackplaneClient) Close() error {
	return nil
}

func TestLoadConfigFromEnv(t *testing.T) {
	// 1. Missing AGENT_NAME
	os.Unsetenv("AGENT_NAME")
	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatalf("expected error when AGENT_NAME is not set")
	}

	// 2. Default values
	os.Setenv("AGENT_NAME", "coder-1")
	os.Unsetenv("BPD_STATE_DIR")
	os.Unsetenv("BPD_DEFAULT_INTERVAL_SECS")
	os.Unsetenv("BPD_CLAUDE_TIMEOUT_SECS")
	os.Unsetenv("BPD_MAX_CONSECUTIVE_FAILURES")
	os.Unsetenv("BPD_ATTACH_LOCK_FILE")
	os.Unsetenv("BPD_RUNNER_CMD")

	oldRunnerPath := defaultAgentRunnerPath
	SetDefaultAgentRunnerPath("/nonexistent/test/agent-runner")
	defer func() { SetDefaultAgentRunnerPath(oldRunnerPath) }()

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv failed: %v", err)
	}
	if cfg.AgentName != "coder-1" {
		t.Errorf("expected AgentName 'coder-1', got %q", cfg.AgentName)
	}
	if cfg.DefaultIntervalSecs != 60 {
		t.Errorf("expected default interval 60, got %d", cfg.DefaultIntervalSecs)
	}
	if cfg.ClaudeTimeoutSecs != 600 {
		t.Errorf("expected timeout 600, got %d", cfg.ClaudeTimeoutSecs)
	}
	if cfg.MaxConsecutiveFailures != 3 {
		t.Errorf("expected max failures 3, got %d", cfg.MaxConsecutiveFailures)
	}
	if cfg.RunnerCmd != "claude -p" {
		t.Errorf("expected runner 'claude -p', got %q", cfg.RunnerCmd)
	}

	// 3. Custom values
	os.Setenv("BPD_STATE_DIR", "/tmp/custom-bpd")
	os.Setenv("BPD_DEFAULT_INTERVAL_SECS", "30")
	os.Setenv("BPD_CLAUDE_TIMEOUT_SECS", "120")
	os.Setenv("BPD_MAX_CONSECUTIVE_FAILURES", "5")
	os.Setenv("BPD_ATTACH_LOCK_FILE", "/tmp/custom-lock")
	os.Setenv("BPD_RUNNER_CMD", "custom-runner --flag")

	cfg2, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv with custom vars failed: %v", err)
	}
	if cfg2.StateDir != "/tmp/custom-bpd" {
		t.Errorf("expected state dir /tmp/custom-bpd, got %q", cfg2.StateDir)
	}
	if cfg2.DefaultIntervalSecs != 30 {
		t.Errorf("expected interval 30, got %d", cfg2.DefaultIntervalSecs)
	}
	if cfg2.ClaudeTimeoutSecs != 120 {
		t.Errorf("expected timeout 120, got %d", cfg2.ClaudeTimeoutSecs)
	}
	if cfg2.MaxConsecutiveFailures != 5 {
		t.Errorf("expected max failures 5, got %d", cfg2.MaxConsecutiveFailures)
	}
	if cfg2.AttachLockFile != "/tmp/custom-lock" {
		t.Errorf("expected lock /tmp/custom-lock, got %q", cfg2.AttachLockFile)
	}
	if cfg2.RunnerCmd != "custom-runner --flag" {
		t.Errorf("expected runner 'custom-runner --flag', got %q", cfg2.RunnerCmd)
	}

	// Test fallback when runner exists
	tmpRunner := filepath.Join(t.TempDir(), "agent-runner")
	_ = os.WriteFile(tmpRunner, []byte("#!/bin/sh\n"), 0755)
	SetDefaultAgentRunnerPath(tmpRunner)
	os.Unsetenv("BPD_RUNNER_CMD")
	cfg3, _ := LoadConfigFromEnv()
	if cfg3.RunnerCmd != tmpRunner {
		t.Errorf("expected runner %q, got %q", tmpRunner, cfg3.RunnerCmd)
	}
}

func TestNewDaemonValidation(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	if _, err := NewDaemon(nil, &mockBackplaneClient{}, stdout, stderr); err == nil {
		t.Error("expected error with nil config")
	}

	cfg := &Config{AgentName: "test", StateDir: t.TempDir()}
	if _, err := NewDaemon(cfg, nil, stdout, stderr); err == nil {
		t.Error("expected error with nil client")
	}

	cfgEmptyAgent := &Config{AgentName: "", StateDir: t.TempDir()}
	if _, err := NewDaemon(cfgEmptyAgent, &mockBackplaneClient{}, stdout, stderr); err == nil {
		t.Error("expected error with empty agent name")
	}

	d, err := NewDaemon(cfg, &mockBackplaneClient{}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error creating daemon: %v", err)
	}
	if d.stdout == nil || d.stderr == nil {
		t.Error("expected stdout/stderr fallback to os.Stdout/os.Stderr")
	}
}

func TestFormatMessage(t *testing.T) {
	msg := &libbp.Message{
		Citation:    "alice#1",
		Destination: "bob",
		Content:     "Status query",
		Timestamp:   1700000000000,
		IsSigned:    true,
		IsVerified:  true,
		BlobPath:    "blobs/sha256-test",
	}

	formatted := FormatMessage(msg)
	if !strings.Contains(formatted, "[alice#1 -> bob [verified]]: Status query") {
		t.Errorf("unexpected formatted message: %s", formatted)
	}
	if !strings.Contains(formatted, "Payload attached: blobs/sha256-test") {
		t.Errorf("missing attached payload in formatted message: %s", formatted)
	}

	unverifiedMsg := &libbp.Message{
		Citation:   "alice#2",
		Content:    "Hello",
		Timestamp:  1700000000000,
		IsSigned:   true,
		IsVerified: false,
	}
	formattedUnverified := FormatMessage(unverifiedMsg)
	if !strings.Contains(formattedUnverified, "[UNVERIFIED]") {
		t.Errorf("expected UNVERIFIED tag: %s", formattedUnverified)
	}
}

func TestDaemonTickDrainAndExecute(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{
		AgentName:              "test-agent",
		StateDir:               tmpDir,
		DefaultIntervalSecs:    60,
		ClaudeTimeoutSecs:      10,
		MaxConsecutiveFailures: 3,
		AttachLockFile:         filepath.Join(tmpDir, "attached"),
		RunnerCmd:              "dummy-runner",
	}

	client := &mockBackplaneClient{
		recvMessages: []*libbp.Message{
			{Citation: "host#1", Content: "Run tests", Timestamp: time.Now().UnixMilli()},
		},
	}

	var executedCmd string
	var executedArgs []string
	var executedStdin string

	runner := func(ctx context.Context, cmdName string, cmdArgs []string, stdin io.Reader, stdout, stderr io.Writer) error {
		executedCmd = cmdName
		executedArgs = cmdArgs
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(stdin)
		executedStdin = buf.String()
		return nil
	}

	d, err := NewDaemon(cfg, client, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}
	d.SetRunner(runner)

	ctx := context.Background()
	if err := d.Tick(ctx, 30); err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	if client.lastBlockSec != 30 {
		t.Errorf("expected lastBlockSec 30, got %d", client.lastBlockSec)
	}
	if executedCmd != "dummy-runner" {
		t.Errorf("expected executedCmd dummy-runner, got %q", executedCmd)
	}
	if len(executedArgs) != 2 || executedArgs[0] != "--session-id" {
		t.Errorf("expected session-id flag in args, got %v", executedArgs)
	}
	if !strings.Contains(executedStdin, "Run tests") {
		t.Errorf("expected stdin to contain 'Run tests', got %q", executedStdin)
	}
	if client.statusCalled == 0 || !strings.Contains(client.lastStatus, "ok (bpd") {
		t.Errorf("expected status update with 'ok', got %q", client.lastStatus)
	}

	// Second tick: should resume session
	client.recvMessages = []*libbp.Message{
		{Citation: "host#2", Content: "Next step", Timestamp: time.Now().UnixMilli()},
	}
	if err := d.Tick(ctx); err != nil {
		t.Fatalf("second Tick failed: %v", err)
	}
	if len(executedArgs) != 2 || executedArgs[0] != "--resume" {
		t.Errorf("expected --resume flag on second invocation, got %v", executedArgs)
	}
}

func TestDaemonTickAttachLock(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "attached")
	_ = os.WriteFile(lockPath, []byte("locked"), 0600)

	cfg := &Config{
		AgentName:      "test-agent",
		StateDir:       tmpDir,
		AttachLockFile: lockPath,
		RunnerCmd:      "dummy",
	}

	client := &mockBackplaneClient{
		recvMessages: []*libbp.Message{{Content: "ignored"}},
	}

	runnerCalled := false
	runner := func(_ context.Context, _ string, _ []string, _ io.Reader, _, _ io.Writer) error {
		runnerCalled = true
		return nil
	}

	d, _ := NewDaemon(cfg, client, &bytes.Buffer{}, &bytes.Buffer{})
	d.SetRunner(runner)

	if err := d.Tick(context.Background()); err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	if client.recvCalled > 0 {
		t.Error("expected Recv NOT to be called when attach lock exists")
	}
	if runnerCalled {
		t.Error("expected runner NOT to be called when attach lock exists")
	}
}

func TestDaemonTickFailureAndDegradedStatus(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{
		AgentName:              "fail-agent",
		StateDir:               tmpDir,
		MaxConsecutiveFailures: 2,
		AttachLockFile:         filepath.Join(tmpDir, "lock"),
		RunnerCmd:              "failing-runner",
	}

	client := &mockBackplaneClient{
		recvMessages: []*libbp.Message{{Content: "fail task"}},
	}

	expectedErr := errors.New("command crashed")
	runner := func(_ context.Context, _ string, _ []string, _ io.Reader, _, _ io.Writer) error {
		return expectedErr
	}

	d, _ := NewDaemon(cfg, client, &bytes.Buffer{}, &bytes.Buffer{})
	d.SetRunner(runner)

	ctx := context.Background()

	// Failure 1
	err := d.Tick(ctx)
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected %v, got %v", expectedErr, err)
	}
	if d.getConsecutiveFailures() != 1 {
		t.Errorf("expected 1 consecutive failure, got %d", d.getConsecutiveFailures())
	}

	// Failure 2 (triggers degraded)
	client.recvMessages = nil // will read existing pending batch
	err = d.Tick(ctx)
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected %v, got %v", expectedErr, err)
	}
	if d.getConsecutiveFailures() != 2 {
		t.Errorf("expected 2 consecutive failures, got %d", d.getConsecutiveFailures())
	}
	if !strings.Contains(client.lastStatus, "DEGRADED: bpd has failed 2 consecutive") {
		t.Errorf("expected DEGRADED status, got %q", client.lastStatus)
	}
}

func TestDaemonRunContextCanceled(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{
		AgentName:           "loop-agent",
		StateDir:            tmpDir,
		DefaultIntervalSecs: 1,
		AttachLockFile:      filepath.Join(tmpDir, "lock"),
		RunnerCmd:           "dummy",
	}

	client := &mockBackplaneClient{}
	d, _ := NewDaemon(cfg, client, &bytes.Buffer{}, &bytes.Buffer{})
	d.SetRunner(func(_ context.Context, _ string, _ []string, _ io.Reader, _, _ io.Writer) error { return nil })

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled immediately

	if err := d.Run(ctx); err != nil {
		t.Fatalf("expected nil return on canceled context, got %v", err)
	}
}

func TestDaemonRunWithAttachLockSleep(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "lock")
	_ = os.WriteFile(lockPath, []byte("locked"), 0600)

	cfg := &Config{
		AgentName:           "lock-sleep-agent",
		StateDir:            tmpDir,
		DefaultIntervalSecs: 1,
		AttachLockFile:      lockPath,
		RunnerCmd:           "dummy",
	}

	client := &mockBackplaneClient{}
	d, _ := NewDaemon(cfg, client, &bytes.Buffer{}, &bytes.Buffer{})
	d.SetRunner(func(_ context.Context, _ string, _ []string, _ io.Reader, _, _ io.Writer) error { return nil })

	sleepCalls := 0
	d.SetSleeper(func(ctx context.Context, d time.Duration) error {
		sleepCalls++
		return context.Canceled
	})

	err := d.Run(context.Background())
	if err != nil {
		t.Fatalf("expected nil when sleeper returns context.Canceled, got %v", err)
	}
	if sleepCalls != 1 {
		t.Errorf("expected 1 sleep call, got %d", sleepCalls)
	}
}

func TestDaemonIntegrationWithValkeyHarness(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	agentClient, err := libbp.Dial(ctx, libbp.ClientConfig{
		Host:     "127.0.0.1",
		Port:     valkey.Port,
		AgentID:  "agent-1",
		Username: "agent-1",
		Password: valkey.Agent1Pass,
	})
	if err != nil {
		t.Fatalf("failed dialing Valkey: %v", err)
	}
	defer agentClient.Close()

	operatorClient, err := libbp.Dial(ctx, libbp.ClientConfig{
		Host:     "127.0.0.1",
		Port:     valkey.Port,
		AgentID:  "operator",
		Username: "operator",
		Password: valkey.HumanPass,
	})
	if err != nil {
		t.Fatalf("failed dialing Valkey as operator: %v", err)
	}
	defer operatorClient.Close()

	tmpDir := t.TempDir()
	cfg := &Config{
		AgentName:              "agent-1",
		StateDir:               tmpDir,
		DefaultIntervalSecs:    1,
		ClaudeTimeoutSecs:      5,
		MaxConsecutiveFailures: 3,
		AttachLockFile:         filepath.Join(tmpDir, "lock"),
		RunnerCmd:              "dummy-runner",
	}

	var batchReceived string
	runner := func(_ context.Context, _ string, _ []string, stdin io.Reader, _, _ io.Writer) error {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(stdin)
		batchReceived = buf.String()
		return nil
	}

	daemon, err := NewDaemon(cfg, agentClient, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("failed creating daemon: %v", err)
	}
	daemon.SetRunner(runner)

	// Post message to agent-1 inbox
	_, err = operatorClient.Tell(ctx, "agent-1", "Integration test directive")
	if err != nil {
		t.Fatalf("failed sending message: %v", err)
	}

	// Tick daemon with blocking read
	if err := daemon.Tick(ctx, 2); err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	if !strings.Contains(batchReceived, "Integration test directive") {
		t.Errorf("expected batch to contain directive, got %q", batchReceived)
	}
}

func TestDefaultRunnerAndSleeper(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := defaultRunner(ctx, "echo", []string{"hello"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("defaultRunner failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "hello") {
		t.Errorf("expected stdout 'hello', got %q", stdout.String())
	}

	sleepCtx, sleepCancel := context.WithCancel(context.Background())
	sleepCancel()
	if err := defaultSleeper(sleepCtx, 5*time.Second); !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled from defaultSleeper, got %v", err)
	}

	if err := defaultSleeper(context.Background(), 1*time.Millisecond); err != nil {
		t.Errorf("expected nil from defaultSleeper, got %v", err)
	}
}

func TestDaemonRunIntervalFallbacksAndSleeperError(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{
		AgentName:           "agent-intervals",
		StateDir:            tmpDir,
		DefaultIntervalSecs: 60,
		AttachLockFile:      filepath.Join(tmpDir, "lock"),
		RunnerCmd:           "echo done",
	}

	client := &mockBackplaneClient{pollInterval: 2} // below MinPollInterval
	d, err := NewDaemon(cfg, client, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var ticks []int
	d.SetRunner(func(_ context.Context, _ string, _ []string, _ io.Reader, _, _ io.Writer) error {
		return nil
	})

	// Override client to record interval
	client.pollErr = errors.New("valkey err")
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_ = d.Run(ctx)
	_ = ticks

	// Test sleeper error propagation when attach lock present
	_ = os.WriteFile(cfg.AttachLockFile, []byte("lock"), 0600)
	d.SetSleeper(func(_ context.Context, _ time.Duration) error {
		return errors.New("custom sleeper error")
	})
	err = d.Run(context.Background())
	if err == nil || err.Error() != "custom sleeper error" {
		t.Errorf("expected custom sleeper error, got %v", err)
	}

	// Test Tick error branch in Run with sleeper returning context.Canceled and custom error
	_ = os.Remove(cfg.AttachLockFile)
	client.statusErr = errors.New("status update failed in tick")
	client.recvErr = errors.New("recv failed")
	// Make getSessionID fail or runner fail to make Tick fail?
	// Notice in Tick: if runner returns error, Tick increments consecutive failures and sets status degraded, returning error if max consecutive failures reached!
	d.cfg.MaxConsecutiveFailures = 1
	d.SetRunner(func(_ context.Context, _ string, _ []string, _ io.Reader, _, _ io.Writer) error {
		return errors.New("runner failed")
	})
	// Trigger pending batch so runner is called and fails
	_ = d.appendPendingBatch("trigger run\n")

	d.SetSleeper(func(_ context.Context, _ time.Duration) error {
		return context.Canceled
	})
	if err := d.Run(context.Background()); err != nil {
		t.Errorf("expected nil on sleeper context.Canceled, got %v", err)
	}

	_ = d.appendPendingBatch("trigger run again\n")
	d.SetSleeper(func(_ context.Context, _ time.Duration) error {
		return errors.New("backoff error")
	})
	if err := d.Run(context.Background()); err == nil || err.Error() != "backoff error" {
		t.Errorf("expected 'backoff error', got %v", err)
	}
}

func TestDaemonFileHelpersEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{AgentName: "edge", StateDir: tmpDir}
	d, _ := NewDaemon(cfg, &mockBackplaneClient{}, &bytes.Buffer{}, &bytes.Buffer{})

	// getSessionID when missing
	sid, err := d.getSessionID()
	if err != nil || sid != "" {
		t.Errorf("expected empty sid, got %q, err: %v", sid, err)
	}

	// getConsecutiveFailures when missing
	fails := d.getConsecutiveFailures()
	if fails != 0 {
		t.Errorf("expected 0 failures, got %d", fails)
	}

	// getConsecutiveFailures with invalid integer
	_ = os.WriteFile(d.consecutiveFailuresFile(), []byte("invalid-int\n"), 0600)
	fails = d.getConsecutiveFailures()
	if fails != 0 {
		t.Errorf("expected 0 failures on parse error, got %d", fails)
	}

	// readPendingBatch when missing
	batch, err := d.readPendingBatch()
	if err != nil || batch != "" {
		t.Errorf("expected empty batch, got %q, err: %v", batch, err)
	}

	// appendPendingBatch and readPendingBatch
	_ = d.appendPendingBatch("test line\n")
	batch, err = d.readPendingBatch()
	if err != nil || !strings.Contains(batch, "test line") {
		t.Errorf("expected 'test line', got %q, err: %v", batch, err)
	}
	_ = d.clearPendingBatch()
}

func TestDaemonTickEnsembleDraining(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{
		AgentName:              "test-agent",
		StateDir:               tmpDir,
		DefaultIntervalSecs:    60,
		ClaudeTimeoutSecs:      10,
		MaxConsecutiveFailures: 3,
		AttachLockFile:         filepath.Join(tmpDir, "attached"),
		RunnerCmd:              "dummy-runner",
	}

	client := &mockBackplaneClient{
		ensembles: []string{"team-dev"},
		ensembleMsgs: map[string][]*libbp.Message{
			"team-dev": {
				{ID: "100-0", Citation: "lead#1", Content: "ensemble ping", Timestamp: time.Now().UnixMilli()},
			},
		},
	}

	var executedStdin string
	runner := func(ctx context.Context, cmdName string, cmdArgs []string, stdin io.Reader, stdout, stderr io.Writer) error {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(stdin)
		executedStdin = buf.String()
		return nil
	}

	d, err := NewDaemon(cfg, client, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}
	d.SetRunner(runner)

	ctx := context.Background()
	if err := d.Tick(ctx); err != nil {
		t.Fatalf("Tick failed: %v", err)
	}

	if !strings.Contains(executedStdin, "ensemble ping") {
		t.Errorf("expected executedStdin to contain ensemble ping, got: %q", executedStdin)
	}

	if client.setCursorCalled[libbp.EnsembleOutKey("team-dev")] != "100-0" {
		t.Errorf("expected cursor to be set to 100-0, got %q", client.setCursorCalled[libbp.EnsembleOutKey("team-dev")])
	}

	// Test error cases in ensemble polling
	client.ensemblesErr = errors.New("valkey disconnected")
	if err := d.Tick(ctx); err != nil {
		t.Errorf("expected graceful handling of ensemblesErr, got error: %v", err)
	}

	client.ensemblesErr = nil
	client.ensembles = []string{"team-dev"}
	client.ensembleErr = errors.New("read stream failed")
	if err := d.Tick(ctx); err != nil {
		t.Errorf("expected graceful handling of ensembleErr, got error: %v", err)
	}
}

