// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
	"github.com/boggycreek/sandbox/backplane/test/harness"
)

func TestMainCoverage_ProfileFlagVariants(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ctx := context.Background()

	// 1. Single-dash -profile=
	code := RunWithContext(ctx, []string{"-profile=default", "help"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0 for -profile=default help, got %d", code)
	}

	// 2. --profile provided without any remaining command
	stdout.Reset()
	stderr.Reset()
	code = RunWithContext(ctx, []string{"--profile=default"}, nil, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected code 1 when only --profile is provided, got %d", code)
	}

	// 3. Invalid profile name that fails to load
	stdout.Reset()
	stderr.Reset()
	code = RunWithContext(ctx, []string{"--profile=nonexistent_profile_404_fail", "whoami"}, nil, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected code 2 when profile cannot be loaded, got %d", code)
	}
}

func TestMainCoverage_HandleWhoami(t *testing.T) {
	t.Run("EmptyModeOperator", func(t *testing.T) {
		var out bytes.Buffer
		cfg := libbp.ClientConfig{
			AgentID: "operator",
			Mode:    "",
			Host:    "127.0.0.1",
			Port:    6379,
		}
		code := handleWhoami(cfg, &out)
		if code != 0 {
			t.Fatalf("expected 0, got %d", code)
		}
		if !strings.Contains(out.String(), "Mode:       human") {
			t.Errorf("expected human mode, got: %s", out.String())
		}
	})

	t.Run("WithSigningKey", func(t *testing.T) {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("failed generating ed25519 key: %v", err)
		}
		var out bytes.Buffer
		cfg := libbp.ClientConfig{
			AgentID:    "signed-agent",
			Mode:       "agent",
			SigningKey: priv,
			Host:       "127.0.0.1",
			Port:       6379,
		}
		code := handleWhoami(cfg, &out)
		if code != 0 {
			t.Fatalf("expected 0, got %d", code)
		}
		pubB64 := libbp.EncodePublicKeyBase64(pub)
		if !strings.Contains(out.String(), pubB64) {
			t.Errorf("expected public key %s in output, got: %s", pubB64, out.String())
		}
	})
}

func TestMainCoverage_HandleProfile(t *testing.T) {
	t.Run("NoHomeDirectory", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("XDG_STATE_HOME", "")
		var out, errOut bytes.Buffer
		code := handleProfile([]string{"list"}, &out, &errOut)
		if code != 0 {
			t.Fatalf("expected 0, got %d", code)
		}
		if !strings.Contains(out.String(), "Available profiles:") {
			t.Errorf("expected output, got: %s", out.String())
		}
	})

	t.Run("HasDefaultProfileFile", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", tmpDir)
		profilesDir := filepath.Join(tmpDir, "bp", "profiles")
		if err := os.MkdirAll(profilesDir, 0700); err != nil {
			t.Fatalf("failed creating profiles dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(profilesDir, "default.env"), []byte("BP_HOST=127.0.0.1\n"), 0600); err != nil {
			t.Fatalf("failed writing default.env: %v", err)
		}
		if err := os.WriteFile(filepath.Join(profilesDir, "custom.env"), []byte("BP_HOST=127.0.0.1\n"), 0600); err != nil {
			t.Fatalf("failed writing custom.env: %v", err)
		}

		var out, errOut bytes.Buffer
		code := handleProfile([]string{"list"}, &out, &errOut)
		if code != 0 {
			t.Fatalf("expected 0, got %d", code)
		}
		if !strings.Contains(out.String(), "  default\n") {
			t.Errorf("expected '  default\\n' in profiles list, got: %s", out.String())
		}
		if !strings.Contains(out.String(), "  custom\n") {
			t.Errorf("expected '  custom\\n' in profiles list, got: %s", out.String())
		}
	})
}

func TestMainCoverage_AgentPathsAndDefaultEnv(t *testing.T) {
	t.Run("AgentPathsWithNoHome", func(t *testing.T) {
		t.Setenv("HOME", "")
		t.Setenv("AGENT_STATE_FILE", "")
		t.Setenv("AGENT_QUEUE_FILE", "")

		sPath := agentStatePath()
		expectedState := filepath.Join("/tmp", ".agent", "state.json")
		if sPath != expectedState {
			t.Errorf("expected state path %s, got %s", expectedState, sPath)
		}

		qPath := agentQueuePath()
		expectedQueue := filepath.Join("/tmp", ".agent", "queue.jsonl")
		if qPath != expectedQueue {
			t.Errorf("expected queue path %s, got %s", expectedQueue, qPath)
		}
	})

	t.Run("LoadDefaultEnv_NoHome", func(t *testing.T) {
		t.Setenv("HOME", "")
		loadDefaultEnv()
	})

	t.Run("LoadDefaultEnv_WithFile", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			os.Unsetenv("TEST_COVERAGE_KEY_FOO")
		})
		t.Setenv("HOME", tmpDir)
		envDir := filepath.Join(tmpDir, ".local", "share", "agent-sandbox")
		if err := os.MkdirAll(envDir, 0700); err != nil {
			t.Fatalf("failed creating env dir: %v", err)
		}
		envContent := "# Comment line\nTEST_COVERAGE_KEY_FOO=bar\nEMPTY_KEY=\nINVALID_LINE_NO_EQUALS\n"
		if err := os.WriteFile(filepath.Join(envDir, ".env"), []byte(envContent), 0600); err != nil {
			t.Fatalf("failed writing .env: %v", err)
		}

		loadDefaultEnv()
		if os.Getenv("TEST_COVERAGE_KEY_FOO") != "bar" {
			t.Errorf("expected TEST_COVERAGE_KEY_FOO=bar, got %q", os.Getenv("TEST_COVERAGE_KEY_FOO"))
		}
	})
}

func TestMainCoverage_HandleStateErrorBranches(t *testing.T) {
	t.Run("StateGet_DirectoryError", func(t *testing.T) {
		tmpDir := t.TempDir()
		stateDir := filepath.Join(tmpDir, "state_is_dir")
		_ = os.Mkdir(stateDir, 0700)
		t.Setenv("AGENT_STATE_FILE", stateDir)

		var stdout, stderr bytes.Buffer
		code := handleState([]string{"get"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1 on state get dir error, got %d", code)
		}
		if !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error, got: %s", stderr.String())
		}
	})

	t.Run("StateSet_MkdirAllError", func(t *testing.T) {
		tmpDir := t.TempDir()
		blockingFile := filepath.Join(tmpDir, "file_blocking")
		_ = os.WriteFile(blockingFile, []byte("content"), 0600)
		t.Setenv("AGENT_STATE_FILE", filepath.Join(blockingFile, "state.json"))

		var stdout, stderr bytes.Buffer
		code := handleState([]string{"set", "k", "v"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1 on mkdir failure, got %d", code)
		}
		if !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error, got: %s", stderr.String())
		}
	})

	t.Run("StateSet_WriteFileError", func(t *testing.T) {
		tmpDir := t.TempDir()
		stateDir := filepath.Join(tmpDir, "state_dir")
		_ = os.Mkdir(stateDir, 0700)
		t.Setenv("AGENT_STATE_FILE", stateDir)

		var stdout, stderr bytes.Buffer
		code := handleState([]string{"set", "k", "v"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1 on write failure, got %d", code)
		}
		if !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error, got: %s", stderr.String())
		}
	})

	t.Run("StateShow_DirectoryError", func(t *testing.T) {
		tmpDir := t.TempDir()
		stateDir := filepath.Join(tmpDir, "state_is_dir")
		_ = os.Mkdir(stateDir, 0700)
		t.Setenv("AGENT_STATE_FILE", stateDir)

		var stdout, stderr bytes.Buffer
		code := handleState([]string{"show"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1 on state show dir error, got %d", code)
		}
		if !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error, got: %s", stderr.String())
		}
	})

	t.Run("QueuePush_MkdirAllError", func(t *testing.T) {
		tmpDir := t.TempDir()
		blockingFile := filepath.Join(tmpDir, "file_blocking_queue")
		_ = os.WriteFile(blockingFile, []byte("content"), 0600)
		t.Setenv("AGENT_QUEUE_FILE", filepath.Join(blockingFile, "queue.jsonl"))

		var stdout, stderr bytes.Buffer
		code := handleState([]string{"queue", "push", "test directive"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1 on queue push mkdir error, got %d", code)
		}
	})

	t.Run("QueuePush_OpenFileError", func(t *testing.T) {
		tmpDir := t.TempDir()
		queueDir := filepath.Join(tmpDir, "queue_is_dir")
		_ = os.Mkdir(queueDir, 0700)
		t.Setenv("AGENT_QUEUE_FILE", queueDir)

		var stdout, stderr bytes.Buffer
		code := handleState([]string{"queue", "push", "test directive"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1 on queue push open error, got %d", code)
		}
	})

	t.Run("QueuePop_OpenFileError", func(t *testing.T) {
		tmpDir := t.TempDir()
		queueDir := filepath.Join(tmpDir, "queue_is_dir")
		_ = os.Mkdir(queueDir, 0700)
		t.Setenv("AGENT_QUEUE_FILE", queueDir)

		var stdout, stderr bytes.Buffer
		code := handleState([]string{"queue", "pop"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1 on queue pop open error, got %d", code)
		}
	})

	t.Run("QueuePop_WhitespaceOnlyFile", func(t *testing.T) {
		tmpDir := t.TempDir()
		queueFile := filepath.Join(tmpDir, "queue.jsonl")
		_ = os.WriteFile(queueFile, []byte("   \n\n   \t\n"), 0600)
		t.Setenv("AGENT_QUEUE_FILE", queueFile)

		var stdout, stderr bytes.Buffer
		code := handleState([]string{"queue", "pop"}, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected code 1 on queue pop whitespace file, got %d", code)
		}
	})
}

func TestMainCoverage_HandleRecvDirect(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	cfg := libbp.ClientConfig{
		Host:     "127.0.0.1",
		Port:     valkey.Port,
		Mode:     "agent",
		AgentID:  "agent-1",
		Password: valkey.Agent1Pass,
	}

	var stdout, stderr bytes.Buffer
	ctx := context.Background()

	// 1. Invalid flag parsing
	code := handleRecv(ctx, cfg, []string{"--invalid-unknown-flag"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 on invalid flag, got %d", code)
	}

	// 2. Valid invocation with json output
	stdout.Reset()
	stderr.Reset()
	code = handleRecv(ctx, cfg, []string{"--block=0", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("expected code 0 on valid recv, got %d, err: %s", code, stderr.String())
	}
}

func TestMainCoverage_CanceledContextAndErrors(t *testing.T) {
	cfg := libbp.ClientConfig{
		Host:    "127.0.0.1",
		Port:    6379,
		Mode:    "agent",
		AgentID: "agent-1",
	}

	tmpFile := filepath.Join(t.TempDir(), "payload.txt")
	_ = os.WriteFile(tmpFile, []byte("Test payload content\n"), 0600)

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel() // pre-canceled

	t.Run("Say_CanceledWithFile", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := handleSay(canceledCtx, cfg, []string{"--file", tmpFile}, nil, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error, got code %d, stderr: %s", code, stderr.String())
		}
	})

	t.Run("Say_CanceledWithMessage", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := handleSay(canceledCtx, cfg, []string{"hello world"}, nil, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error on say, got code %d, stderr: %s", code, stderr.String())
		}
	})

	t.Run("Tell_CanceledWithFile", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := handleTell(canceledCtx, cfg, []string{"--file", tmpFile, "agent-2"}, nil, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error on tell, got code %d, stderr: %s", code, stderr.String())
		}
	})

	t.Run("Tell_CanceledWithMessage", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := handleTell(canceledCtx, cfg, []string{"agent-2", "hello"}, nil, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error on tell, got code %d, stderr: %s", code, stderr.String())
		}
	})

	t.Run("Post_BadPort", func(t *testing.T) {
		badCfg := cfg
		badCfg.Port = 1
		ctx, cancelTimeout := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelTimeout()
		var stdout, stderr bytes.Buffer
		code := handlePost(ctx, badCfg, []string{"--file", tmpFile}, nil, &stdout, &stderr)
		if code != 1 {
			t.Errorf("expected code 1 on bad client, got %d", code)
		}
	})

	t.Run("Post_Canceled", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := handlePost(canceledCtx, cfg, []string{"--file", tmpFile}, nil, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error on post, got code %d, stderr: %s", code, stderr.String())
		}
	})

	t.Run("Reply_Canceled", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		// args: <message-id> <recipient> <response>
		code := handleReply(canceledCtx, cfg, []string{"12345-0", "agent-2", "response"}, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error on reply, got code %d, stderr: %s", code, stderr.String())
		}
	})

	t.Run("RecvParsed_Canceled", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := handleRecvParsed(canceledCtx, cfg, 0, false, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error on recv, got code %d, stderr: %s", code, stderr.String())
		}
	})

	t.Run("Human_Canceled", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := handleHuman(canceledCtx, cfg, []string{"set", "alice"}, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error on human set, got code %d, stderr: %s", code, stderr.String())
		}
	})

	t.Run("Peers_Canceled", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := handlePeers(canceledCtx, cfg, []string{}, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error on peers, got code %d, stderr: %s", code, stderr.String())
		}
	})

	t.Run("PeersJSON_Canceled", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := handlePeers(canceledCtx, cfg, []string{"--json"}, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error on peers json, got code %d, stderr: %s", code, stderr.String())
		}
	})

	t.Run("Status_Canceled", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := handleStatus(canceledCtx, cfg, []string{"set", "online"}, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error on status, got code %d, stderr: %s", code, stderr.String())
		}
	})

	t.Run("Liaison_Canceled", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := handleLiaison(canceledCtx, cfg, []string{}, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error on liaison, got code %d, stderr: %s", code, stderr.String())
		}
	})

	t.Run("IntervalGet_Canceled", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := handleInterval(canceledCtx, cfg, []string{"get"}, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error on interval get, got code %d, stderr: %s", code, stderr.String())
		}
	})

	t.Run("IntervalSet_Canceled", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := handleInterval(canceledCtx, cfg, []string{"set", "30"}, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "bp error:") {
			t.Errorf("expected bp error on interval set, got code %d, stderr: %s", code, stderr.String())
		}
	})
}

func TestMainCoverage_RecvVerifiedMessage(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed generating ed25519 key: %v", err)
	}

	cfg := libbp.ClientConfig{
		Host:       "127.0.0.1",
		Port:       valkey.Port,
		Mode:       "agent",
		AgentID:    "agent-1",
		Password:   valkey.Agent1Pass,
		SigningKey: priv,
	}

	// Register agent-1's identity and public key in Valkey so messages are verified
	adminClient, err := valkey.ClientFor("admin")
	if err != nil {
		t.Fatalf("admin client error: %v", err)
	}
	defer adminClient.Close()
	_ = adminClient.RegisterIdentity(context.Background(), libbp.IdentityRecord{
		Name:   "agent-1",
		PubKey: libbp.EncodePublicKeyBase64(pub),
	})

	client, err := libbp.Dial(context.Background(), cfg)
	if err != nil {
		t.Fatalf("failed dialing: %v", err)
	}
	defer client.Close()
	_, err = client.Tell(context.Background(), "agent-2", "signed and verified message")
	if err != nil {
		t.Fatalf("failed to tell signed message: %v", err)
	}

	cfgAgent2 := libbp.ClientConfig{
		Host:     "127.0.0.1",
		Port:     valkey.Port,
		Mode:     "agent",
		AgentID:  "agent-2",
		Password: valkey.Agent2Pass,
	}

	var stdout, stderr bytes.Buffer
	code := handleRecvParsed(context.Background(), cfgAgent2, 0, false, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("failed handleRecvParsed: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "[verified]") {
		t.Errorf("expected [verified] in output, got: %s", stdout.String())
	}
}
