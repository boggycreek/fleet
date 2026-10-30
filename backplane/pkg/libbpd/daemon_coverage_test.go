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
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
)

func TestLoadConfigFromEnv_NoHome(t *testing.T) {
	t.Setenv("AGENT_NAME", "no-home-agent")
	t.Setenv("HOME", "")
	t.Setenv("BPD_STATE_DIR", "")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join("/tmp", ".bpd")
	if cfg.StateDir != expected {
		t.Errorf("expected StateDir %q, got %q", expected, cfg.StateDir)
	}
}

func TestNewDaemon_StateDirError(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "blocking-file")
	if err := os.WriteFile(filePath, []byte("block"), 0600); err != nil {
		t.Fatalf("failed creating blocking file: %v", err)
	}

	cfg := &Config{
		AgentName: "blocked-agent",
		StateDir:  filepath.Join(filePath, "child-dir"),
	}
	_, err := NewDaemon(cfg, &mockBackplaneClient{}, nil, nil)
	if err == nil {
		t.Fatal("expected error creating state dir under file, got nil")
	}
}

func TestDaemonFileHelpers_DirErrors(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{AgentName: "err-agent", StateDir: tmpDir}
	d, err := NewDaemon(cfg, &mockBackplaneClient{}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("NewDaemon failed: %v", err)
	}

	// Make session-id a directory
	if err := os.Mkdir(d.sessionIDFile(), 0700); err != nil {
		t.Fatalf("failed creating dir: %v", err)
	}
	if _, err := d.getSessionID(); err == nil {
		t.Error("expected getSessionID error when sessionIDFile is a directory")
	}

	// Make pending-batch.txt a directory
	if err := os.Mkdir(d.pendingBatchFile(), 0700); err != nil {
		t.Fatalf("failed creating dir: %v", err)
	}
	if _, err := d.readPendingBatch(); err == nil {
		t.Error("expected readPendingBatch error when pendingBatchFile is a directory")
	}
	if err := d.appendPendingBatch("foo"); err == nil {
		t.Error("expected appendPendingBatch error when pendingBatchFile is a directory")
	}
}

func TestDaemonTick_CoverageBranches(t *testing.T) {
	t.Run("RecvError_LogsAndContinues", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &Config{
			AgentName: "recv-err-agent",
			StateDir:  tmpDir,
			RunnerCmd: "echo ok",
		}
		var stderr bytes.Buffer
		client := &mockBackplaneClient{recvErr: errors.New("simulated recv error")}
		d, _ := NewDaemon(cfg, client, &bytes.Buffer{}, &stderr)
		if err := d.Tick(context.Background()); err != nil {
			t.Fatalf("expected nil when no pending batch, got: %v", err)
		}
		if !bytes.Contains(stderr.Bytes(), []byte("Error draining messages")) {
			t.Errorf("expected stderr to contain error draining messages, got: %s", stderr.String())
		}
	})

	t.Run("AppendPendingBatchError", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &Config{
			AgentName: "append-err-agent",
			StateDir:  tmpDir,
			RunnerCmd: "echo ok",
		}
		client := &mockBackplaneClient{
			recvMessages: []*libbp.Message{
				{Citation: "agent-1", Content: "hello"},
			},
		}
		var stderr bytes.Buffer
		d, _ := NewDaemon(cfg, client, &bytes.Buffer{}, &stderr)
		// Make pendingBatchFile a directory to force appendPendingBatch error
		if err := os.Mkdir(d.pendingBatchFile(), 0700); err != nil {
			t.Fatalf("failed creating dir: %v", err)
		}

		err := d.Tick(context.Background())
		if err == nil {
			t.Fatal("expected Tick to return error when appendPendingBatch fails")
		}
		if !bytes.Contains(stderr.Bytes(), []byte("Error writing to pending batch")) {
			t.Errorf("expected stderr log, got: %s", stderr.String())
		}
	})

	t.Run("ReadPendingBatchError", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &Config{
			AgentName: "read-err-agent",
			StateDir:  tmpDir,
			RunnerCmd: "echo ok",
		}
		client := &mockBackplaneClient{}
		d, _ := NewDaemon(cfg, client, &bytes.Buffer{}, &bytes.Buffer{})
		// Make pendingBatchFile a directory to force readPendingBatch error
		if err := os.Mkdir(d.pendingBatchFile(), 0700); err != nil {
			t.Fatalf("failed creating dir: %v", err)
		}

		err := d.Tick(context.Background())
		if err == nil || !bytes.Contains([]byte(err.Error()), []byte("failed reading pending batch")) {
			t.Fatalf("expected failed reading pending batch error, got: %v", err)
		}
	})

	t.Run("SetSessionIDError", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &Config{
			AgentName: "session-err-agent",
			StateDir:  tmpDir,
			RunnerCmd: "echo ok",
		}
		client := &mockBackplaneClient{}
		d, _ := NewDaemon(cfg, client, &bytes.Buffer{}, &bytes.Buffer{})
		// Write pending batch so Tick proceeds past empty batch check
		_ = os.WriteFile(d.pendingBatchFile(), []byte("work to do\n"), 0600)
		// Make session-id a directory so setSessionID fails
		if err := os.Mkdir(d.sessionIDFile(), 0700); err != nil {
			t.Fatalf("failed creating dir: %v", err)
		}

		err := d.Tick(context.Background())
		if err == nil || !bytes.Contains([]byte(err.Error()), []byte("failed saving session-id")) {
			t.Fatalf("expected failed saving session-id error, got: %v", err)
		}
	})

	t.Run("EmptyRunnerCommand", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &Config{
			AgentName: "empty-runner-agent",
			StateDir:  tmpDir,
			RunnerCmd: "   ",
		}
		client := &mockBackplaneClient{}
		d, _ := NewDaemon(cfg, client, &bytes.Buffer{}, &bytes.Buffer{})
		_ = os.WriteFile(d.pendingBatchFile(), []byte("work to do\n"), 0600)

		err := d.Tick(context.Background())
		if err == nil || err.Error() != "empty runner command configured" {
			t.Fatalf("expected empty runner command error, got: %v", err)
		}
	})

	t.Run("SetStatusErrorOnSuccess", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &Config{
			AgentName: "status-err-agent",
			StateDir:  tmpDir,
			RunnerCmd: "dummy-runner",
		}
		var stderr bytes.Buffer
		client := &mockBackplaneClient{statusErr: errors.New("network dropped")}
		d, _ := NewDaemon(cfg, client, &bytes.Buffer{}, &stderr)
		_ = os.WriteFile(d.pendingBatchFile(), []byte("work\n"), 0600)
		d.SetRunner(func(_ context.Context, _ string, _ []string, _ io.Reader, _, _ io.Writer) error {
			return nil
		})

		err := d.Tick(context.Background())
		if err != nil {
			t.Fatalf("expected nil from Tick even if SetStatus fails, got: %v", err)
		}
		if !bytes.Contains(stderr.Bytes(), []byte("Failed updating backplane status")) {
			t.Errorf("expected stderr log about status update failure, got: %s", stderr.String())
		}
	})

	t.Run("SetStatusErrorOnDegraded", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &Config{
			AgentName:              "degraded-err-agent",
			StateDir:               tmpDir,
			RunnerCmd:              "dummy-runner",
			MaxConsecutiveFailures: 1,
		}
		var stderr bytes.Buffer
		client := &mockBackplaneClient{statusErr: errors.New("network dropped")}
		d, _ := NewDaemon(cfg, client, &bytes.Buffer{}, &stderr)
		_ = os.WriteFile(d.pendingBatchFile(), []byte("work\n"), 0600)
		d.SetRunner(func(_ context.Context, _ string, _ []string, _ io.Reader, _, _ io.Writer) error {
			return errors.New("runner failed permanently")
		})

		err := d.Tick(context.Background())
		if err == nil {
			t.Fatal("expected runner error from Tick")
		}
		if !bytes.Contains(stderr.Bytes(), []byte("Failed setting degraded status")) {
			t.Errorf("expected stderr log about degraded status failure, got: %s", stderr.String())
		}
	})
}

func TestDaemonRun_ErrorBackoffAndContinue(t *testing.T) {
	t.Run("TickError_IntervalSecsLe1_CanceledSleeper", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &Config{
			AgentName:           "run-err-agent1",
			StateDir:            tmpDir,
			DefaultIntervalSecs: 1,
			RunnerCmd:           "dummy",
		}
		client := &mockBackplaneClient{pollInterval: 1}
		var stdout, stderr bytes.Buffer
		d, _ := NewDaemon(cfg, client, &stdout, &stderr)
		// Make pendingBatchFile a directory so Tick fails immediately
		_ = os.Mkdir(d.pendingBatchFile(), 0700)

		var capturedDuration time.Duration
		d.SetSleeper(func(_ context.Context, d time.Duration) error {
			capturedDuration = d
			return context.Canceled
		})

		err := d.Run(context.Background())
		if err != nil {
			t.Fatalf("expected nil on context.Canceled from sleeper, got: %v", err)
		}
		if capturedDuration != 1*time.Second {
			t.Errorf("expected backoff duration 1s, got %v", capturedDuration)
		}
		if !bytes.Contains(stdout.Bytes(), []byte("Stopping daemon (context canceled)")) {
			t.Errorf("expected stopping daemon log, got: %s", stdout.String())
		}
	})

	t.Run("TickError_IntervalSecsGt1_SleeperError", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &Config{
			AgentName:           "run-err-agent2",
			StateDir:            tmpDir,
			DefaultIntervalSecs: 5,
			RunnerCmd:           "dummy",
		}
		client := &mockBackplaneClient{pollInterval: 5}
		var stdout, stderr bytes.Buffer
		d, _ := NewDaemon(cfg, client, &stdout, &stderr)
		// Make pendingBatchFile a directory so Tick fails
		_ = os.Mkdir(d.pendingBatchFile(), 0700)

		var capturedDuration time.Duration
		d.SetSleeper(func(_ context.Context, d time.Duration) error {
			capturedDuration = d
			return errors.New("fatal sleeper error")
		})

		err := d.Run(context.Background())
		if err == nil || err.Error() != "fatal sleeper error" {
			t.Fatalf("expected fatal sleeper error, got: %v", err)
		}
		if capturedDuration != 5*time.Second {
			t.Errorf("expected backoff duration 5s, got %v", capturedDuration)
		}
	})

	t.Run("TickError_ContinueLoopThenExit", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := &Config{
			AgentName:           "run-continue-agent",
			StateDir:            tmpDir,
			DefaultIntervalSecs: 1,
			RunnerCmd:           "dummy",
		}
		client := &mockBackplaneClient{pollInterval: 1}
		d, _ := NewDaemon(cfg, client, &bytes.Buffer{}, &bytes.Buffer{})
		// Make pendingBatchFile a directory so Tick fails
		_ = os.Mkdir(d.pendingBatchFile(), 0700)

		sleepCount := 0
		d.SetSleeper(func(_ context.Context, _ time.Duration) error {
			sleepCount++
			if sleepCount == 1 {
				return nil // continue
			}
			return context.Canceled
		})

		err := d.Run(context.Background())
		if err != nil {
			t.Fatalf("expected nil return on context.Canceled, got: %v", err)
		}
		if sleepCount != 2 {
			t.Errorf("expected sleeper to be called 2 times, got %d", sleepCount)
		}
	})
}

type transientErrorReceiver struct{}

func (r *transientErrorReceiver) Recv(ctx context.Context, blockSeconds int) ([]*libbp.Message, error) {
	return nil, errors.New("network transient err")
}

type staticMessagesReceiver struct{}

func (r *staticMessagesReceiver) Recv(ctx context.Context, blockSeconds int) ([]*libbp.Message, error) {
	return []*libbp.Message{
		{Citation: "a1", Content: "m1"},
		{Citation: "a2", Content: "m2"},
	}, nil
}

func TestStreamListener_ContextCanceledDuringBackoffAndMessageLoop(t *testing.T) {
	t.Run("ContextCanceledDuringBackoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		recv := &transientErrorReceiver{}
		l := NewStreamListener(recv, 1)
		l.SetBackoff(100 * time.Millisecond)

		timer := time.AfterFunc(10*time.Millisecond, cancel)
		defer timer.Stop()

		err := l.Listen(ctx, func(_ context.Context, _ *libbp.Message) error {
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
	})

	t.Run("ContextCanceledDuringMessageIteration", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		recv := &staticMessagesReceiver{}
		l := NewStreamListener(recv, 1)

		handledCount := 0
		err := l.Listen(ctx, func(mCtx context.Context, msg *libbp.Message) error {
			handledCount++
			cancel() // cancel after handling first message
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got: %v", err)
		}
		if handledCount != 1 {
			t.Errorf("expected exactly 1 message handled before cancel check, got %d", handledCount)
		}
	})
}
