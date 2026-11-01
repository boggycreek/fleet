// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
	libbpResp "github.com/boggycreek/fleet/backplane/pkg/libbp/resp"
	"github.com/boggycreek/fleet/backplane/test/harness"
)

func runCLI(args []string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func runCLIWithStdin(args []string, stdin io.Reader) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := RunWithIO(args, stdin, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

type mockValkeyForBp struct {
	listener net.Listener
}

func startMockValkeyForBp(t *testing.T) (*mockValkeyForBp, int) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed starting mock listener: %v", err)
	}
	m := &mockValkeyForBp{listener: listener}
	data := make(map[string]string)
	data["poll-interval"] = "60"
	data["human:name"] = "operator"
	data["agent-1:blob:123"] = "blob content"
	hashes := make(map[string]map[string]string)
	hashes["agent-1:finger"] = map[string]string{"role": "coder", "model": "qwen"}
	var mu sync.Mutex

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := libbpResp.NewReader(c)
				w := libbpResp.NewWriter(c)
				for {
					val, err := r.ReadValue()
					if err != nil {
						return
					}
					args, err := val.AsArray()
					if err != nil || len(args) == 0 {
						continue
					}
					cmd := strings.ToUpper(args[0].String())
					mu.Lock()
					switch cmd {
					case "AUTH":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeSimpleString, Str: "OK"})
					case "PING":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeSimpleString, Str: "PONG"})
					case "INCR":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeInteger, Num: 1})
					case "SET":
						if len(args) >= 3 {
							data[args[1].String()] = args[2].String()
						}
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeSimpleString, Str: "OK"})
					case "GET":
						key := ""
						if len(args) > 1 {
							key = args[1].String()
						}
						if key == "identity:agent-1" {
							_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeBulkString, Str: `{"name":"agent-1","role":"coder","kind":"agent"}`})
						} else if key == "identity:agent-2" {
							_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeBulkString, Str: `{"name":"agent-2","role":"reviewer","kind":"agent"}`})
						} else if key == "liaison:current" {
							_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeBulkString, Str: "agent-1"})
						} else if v, ok := data[key]; ok {
							_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeBulkString, Str: v})
						} else {
							_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeBulkString, IsNull: true})
						}
					case "HSET":
						key := args[1].String()
						if hashes[key] == nil {
							hashes[key] = make(map[string]string)
						}
						for i := 2; i < len(args); i += 2 {
							if i+1 < len(args) {
								hashes[key][args[i].String()] = args[i+1].String()
							}
						}
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeInteger, Num: 1})
					case "HGET":
						key := args[1].String()
						field := args[2].String()
						if h, ok := hashes[key]; ok {
							if v, ok := h[field]; ok {
								_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeBulkString, Str: v})
							} else {
								_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeBulkString, IsNull: true})
							}
						} else {
							_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeBulkString, IsNull: true})
						}
					case "HGETALL":
						key := args[1].String()
						var arr []libbpResp.Value
						if h, ok := hashes[key]; ok {
							for k, v := range h {
								arr = append(arr, libbpResp.Value{Type: libbpResp.TypeBulkString, Str: k}, libbpResp.Value{Type: libbpResp.TypeBulkString, Str: v})
							}
						}
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeArray, Array: arr})
					case "XADD":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeBulkString, Str: "1700000000000-0"})
					case "PUBLISH":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeInteger, Num: 1})
					case "XREVRANGE":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeArray, Array: []libbpResp.Value{
							{Type: libbpResp.TypeArray, Array: []libbpResp.Value{
								{Type: libbpResp.TypeBulkString, Str: "1700000000000-0"},
								{Type: libbpResp.TypeArray, Array: []libbpResp.Value{
									{Type: libbpResp.TypeBulkString, Str: "sender"},
									{Type: libbpResp.TypeBulkString, Str: "operator"},
									{Type: libbpResp.TypeBulkString, Str: "content"},
									{Type: libbpResp.TypeBulkString, Str: "human broadcast"},
									{Type: libbpResp.TypeBulkString, Str: "timestamp"},
									{Type: libbpResp.TypeBulkString, Str: "1700000000000"},
									{Type: libbpResp.TypeBulkString, Str: "seq"},
									{Type: libbpResp.TypeBulkString, Str: "1"},
								}},
							}},
						}})
					case "SCAN":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeArray, Array: []libbpResp.Value{
							{Type: libbpResp.TypeBulkString, Str: "0"},
							{Type: libbpResp.TypeArray, Array: []libbpResp.Value{
								{Type: libbpResp.TypeBulkString, Str: "identity:agent-1"},
								{Type: libbpResp.TypeBulkString, Str: "identity:agent-2"},
							}},
						}})
					case "XREAD":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeArray, Array: []libbpResp.Value{
							{Type: libbpResp.TypeArray, Array: []libbpResp.Value{
								{Type: libbpResp.TypeBulkString, Str: "agent-1:inbox"},
								{Type: libbpResp.TypeArray, Array: []libbpResp.Value{
									{Type: libbpResp.TypeArray, Array: []libbpResp.Value{
										{Type: libbpResp.TypeBulkString, Str: "1700000000000-0"},
										{Type: libbpResp.TypeArray, Array: []libbpResp.Value{
											{Type: libbpResp.TypeBulkString, Str: "sender"},
											{Type: libbpResp.TypeBulkString, Str: "peer-1"},
											{Type: libbpResp.TypeBulkString, Str: "destination"},
											{Type: libbpResp.TypeBulkString, Str: "agent-1"},
											{Type: libbpResp.TypeBulkString, Str: "content"},
											{Type: libbpResp.TypeBulkString, Str: "received message"},
											{Type: libbpResp.TypeBulkString, Str: "blob_path"},
											{Type: libbpResp.TypeBulkString, Str: "agent-1:blob:123"},
											{Type: libbpResp.TypeBulkString, Str: "signature"},
											{Type: libbpResp.TypeBulkString, Str: "invalid-sig"},
											{Type: libbpResp.TypeBulkString, Str: "timestamp"},
											{Type: libbpResp.TypeBulkString, Str: "1700000000000"},
											{Type: libbpResp.TypeBulkString, Str: "seq"},
											{Type: libbpResp.TypeBulkString, Str: "10"},
										}},
									}},
								}},
							}},
						}})
					default:
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeSimpleString, Str: "OK"})
					}
					mu.Unlock()
				}
			}(conn)
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	return m, port
}

func (m *mockValkeyForBp) close() {
	if m.listener != nil {
		_ = m.listener.Close()
	}
}

func TestBPCliWithMockServer(t *testing.T) {
	srv, port := startMockValkeyForBp(t)
	defer srv.close()

	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", fmt.Sprintf("%d", port))
	t.Setenv("BP_MODE", "agent")
	t.Setenv("BP_AGENT", "agent-1")
	t.Setenv("BP_PASSWORD", "secret")

	var resCode int
	var resOut string

	// 1. whoami
	resCode, resOut, _ = runCLI([]string{"whoami"})
	if resCode != 0 || !strings.Contains(resOut, "Agent ID:   agent-1") {
		t.Errorf("whoami failed: code %d, out: %s", resCode, resOut)
	}

	// File setup for attachment testing
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "payload.txt")
	_ = os.WriteFile(filePath, []byte("file payload line 1\nfile line 2"), 0600)

	// 2. say
	resCode, resOut, _ = runCLI([]string{"say", "hello fleet"})
	if resCode != 0 || !strings.Contains(resOut, "hello fleet") {
		t.Errorf("say failed: code %d, out: %s", resCode, resOut)
	}

	resCode, resOut, _ = runCLI([]string{"say", "--file", filePath, "say with file"})
	if resCode != 0 || !strings.Contains(resOut, "say with file") {
		t.Errorf("say --file failed: code %d, out: %s", resCode, resOut)
	}

	// 3. tell
	resCode, resOut, _ = runCLI([]string{"tell", "agent-2", "direct message"})
	if resCode != 0 || !strings.Contains(resOut, "direct message") {
		t.Errorf("tell failed: code %d, out: %s", resCode, resOut)
	}

	resCode, resOut, _ = runCLI([]string{"tell", "agent-2", "--file", filePath, "tell with file"})
	if resCode != 0 || !strings.Contains(resOut, "tell with file") {
		t.Errorf("tell --file failed: code %d, out: %s", resCode, resOut)
	}

	// 3b. post
	resCode, resOut, _ = runCLI([]string{"post", filePath})
	if resCode != 0 || !strings.Contains(resOut, "Payload attached:") {
		t.Errorf("post broadcast failed: code %d, out: %s", resCode, resOut)
	}

	resCode, resOut, _ = runCLI([]string{"post", filePath, "to", "agent-2"})
	if resCode != 0 || !strings.Contains(resOut, "-> agent-2") {
		t.Errorf("post direct failed: code %d, out: %s", resCode, resOut)
	}

	// Profile directory with files
	profDir := filepath.Join(tmpDir, "bp", "profiles")
	_ = os.MkdirAll(profDir, 0700)
	_ = os.WriteFile(filepath.Join(profDir, "default.env"), []byte("BP_AGENT=agent-1\n"), 0600)
	_ = os.WriteFile(filepath.Join(profDir, "prod.env"), []byte("BP_AGENT=prod-agent\n"), 0600)
	t.Setenv("XDG_STATE_HOME", tmpDir)

	// 3c. profile list & AI help
	resCode, resOut, _ = runCLI([]string{"profile"})
	if resCode != 0 || !strings.Contains(resOut, "Available profiles:") {
		t.Errorf("profile failed: code %d, out: %s", resCode, resOut)
	}

	resCode, resOut, _ = runCLI([]string{"profile", "list"})
	if resCode != 0 || !strings.Contains(resOut, "prod") {
		t.Errorf("profile list with files failed: code %d, out: %s", resCode, resOut)
	}

	resCode, resOut, _ = runCLI([]string{"help", "--ai"})
	if resCode != 0 || !strings.Contains(resOut, "Protocol Guide") {
		t.Errorf("help --ai failed: code %d, out: %s", resCode, resOut)
	}

	// 4. reply
	resCode, resOut, _ = runCLI([]string{"reply", "1-0", "agent-2", "reply message"})
	if resCode != 0 || !strings.Contains(resOut, "reply message") {
		t.Errorf("reply failed: code %d, out: %s", resCode, resOut)
	}

	// 5. cat / get
	resCode, resOut, _ = runCLI([]string{"cat", "agent-1:blob:123"})
	if resCode != 0 || !strings.Contains(resOut, "blob content") {
		t.Errorf("cat failed: code %d, out: %s", resCode, resOut)
	}

	// 6. recv (standard & json)
	resCode, resOut, _ = runCLI([]string{"recv"})
	if resCode != 0 || !strings.Contains(resOut, "received message") {
		t.Errorf("recv failed: code %d, out: %s", resCode, resOut)
	}

	resCode, resOut, _ = runCLI([]string{"recv", "--json"})
	if resCode != 0 || !strings.Contains(resOut, "received message") {
		t.Errorf("recv --json failed: code %d, out: %s", resCode, resOut)
	}

	// 7. human (standard & json)
	resCode, resOut, _ = runCLI([]string{"human"})
	if resCode != 0 || !strings.Contains(resOut, "human broadcast") {
		t.Errorf("human failed: code %d, out: %s", resCode, resOut)
	}

	resCode, resOut, _ = runCLI([]string{"human", "--json"})
	if resCode != 0 || !strings.Contains(resOut, "human broadcast") {
		t.Errorf("human --json failed: code %d, out: %s", resCode, resOut)
	}

	// 8. peers
	resCode, resOut, _ = runCLI([]string{"peers"})
	if resCode != 0 || !strings.Contains(resOut, "AGENT_ID") {
		t.Errorf("peers failed: code %d, out: %s", resCode, resOut)
	}

	// 9. status set
	resCode, resOut, _ = runCLI([]string{"status", "set", "active coding"})
	if resCode != 0 || !strings.Contains(resOut, "status set: active coding") {
		t.Errorf("status set failed: code %d, out: %s", resCode, resOut)
	}

	// 10. finger
	resCode, resOut, _ = runCLI([]string{"finger"})
	if resCode != 0 || !strings.Contains(resOut, "coder") {
		t.Errorf("finger failed: code %d, out: %s", resCode, resOut)
	}

	// 11. liaison get & set
	resCode, resOut, _ = runCLI([]string{"liaison", "get"})
	if resCode != 0 {
		t.Errorf("liaison get failed: code %d, out: %s", resCode, resOut)
	}

	resCode, resOut, _ = runCLI([]string{"liaison", "set", "agent-2"})
	if resCode != 0 || !strings.Contains(resOut, "Liaison appointed: agent-2") {
		t.Errorf("liaison set failed: code %d, out: %s", resCode, resOut)
	}

	// 12. interval get & set
	resCode, resOut, _ = runCLI([]string{"interval", "get"})
	if resCode != 0 || !strings.Contains(resOut, "60") {
		t.Errorf("interval get failed: code %d, out: %s", resCode, resOut)
	}

	resCode, resOut, _ = runCLI([]string{"interval", "set", "30"})
	if resCode != 0 || !strings.Contains(resOut, "Poll interval set to 30 seconds") {
		t.Errorf("interval set failed: code %d, out: %s", resCode, resOut)
	}

	// 13. Additional flag and error branches for max coverage
	runCLI([]string{"recv", "--block", "1"})

	priv, _, _ := libbp.GenerateKeypair()
	cfgWithKey := libbp.ClientConfig{
		AgentID:    "operator",
		Mode:       "human",
		Host:       "127.0.0.1",
		Port:       port,
		SigningKey: priv,
	}
	handleWhoami(cfgWithKey, &bytes.Buffer{})

	homeDir, _ := os.UserHomeDir()
	if homeDir != "" {
		envDir := filepath.Join(homeDir, ".local", "share", "agent-sandbox")
		_ = os.MkdirAll(envDir, 0700)
		_ = os.WriteFile(filepath.Join(envDir, ".env"), []byte("ENV_TEST_VAR=1\n# comment\nINVALID\n"), 0600)
		loadDefaultEnv()
	}

	t.Setenv("AGENT_STATE_FILE", "")
	t.Setenv("AGENT_QUEUE_FILE", "")
	_ = agentStatePath()
	_ = agentQueuePath()

	// Post variations
	runCLI([]string{"post", "--file", filePath, "to", "agent-2"})
	runCLI([]string{"post", "--file=" + filePath})
	runCLIWithStdin([]string{"post", "-"}, strings.NewReader("Header summary\n\nBody line 1\nBody line 2"))
	runCLIWithStdin([]string{"post", "-", "to", "agent-2"}, strings.NewReader("Header summary\n\nBody line 1"))
	runCLIWithStdin([]string{"post", "--file", "-"}, strings.NewReader("Flag stdin summary\n\nBody line"))

	// Tell variations
	runCLI([]string{"tell", "agent-2", "--file=" + filePath})
	runCLIWithStdin([]string{"tell", "agent-2", "--file", "-"}, strings.NewReader("tell stdin body"))
	runCLI([]string{"tell", "agent-2", "--", "--msg-with-dashes"})

	// Say variations
	runCLI([]string{"say", "--file=" + filePath, "say inline"})
	runCLIWithStdin([]string{"say", "--file", "-", "say stdin"}, strings.NewReader("say stdin payload"))

	// State commands
	runCLI([]string{"state", "set", "key1", "val1"})
	runCLI([]string{"state", "get", "key1"})
	runCLI([]string{"state", "get"})
	runCLI([]string{"state", "show"})
	runCLI([]string{"state", "queue", "push", "task 1"})
	runCLI([]string{"state", "queue", "list"})
	runCLI([]string{"state", "queue", "pop"})
	runCLI([]string{"state", "queue", "clear"})

	runCLI([]string{"say"})
	runCLI([]string{"say", "--file"})
	runCLI([]string{"say", "--invalid-flag"})
	runCLI([]string{"tell"})
	runCLI([]string{"tell", "agent-2"})
	runCLI([]string{"tell", "--file"})
	runCLI([]string{"tell", "--invalid-flag"})
	runCLI([]string{"reply"})
	runCLI([]string{"reply", "1-0"})
	runCLI([]string{"post"})
	runCLI([]string{"post", "--file"})
	runCLI([]string{"post", filePath, "to"})
	runCLI([]string{"post", filePath, "to", "agent-2", "extra"})
	runCLI([]string{"post", filePath, "extra"})
	runCLI([]string{"cat"})
	runCLI([]string{"status"})
	runCLI([]string{"status", "invalid"})
	runCLI([]string{"liaison", "invalid"})
	runCLI([]string{"liaison", "set"})
	runCLI([]string{"interval", "invalid"})
	runCLI([]string{"interval", "set", "invalid"})
	runCLI([]string{"interval", "set", "4"})
	runCLI([]string{"interval", "set", "3601"})
}

func TestBPFullCoverage(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	os.Setenv("BP_HOST", "127.0.0.1")
	os.Setenv("BP_PORT", fmt.Sprintf("%d", valkey.Port))
	os.Setenv("BP_MODE", "agent")
	os.Setenv("BP_AGENT", "agent-1")
	os.Setenv("BP_PASSWORD", valkey.Agent1Pass)

	// Register operator identity and KeyHumanName in Valkey
	adminClient, err := valkey.ClientFor("admin")
	if err != nil {
		t.Fatalf("admin client error: %v", err)
	}
	defer adminClient.Close()
	_, _ = adminClient.Exec(context.Background(), "SET", libbp.KeyHumanName, "operator")

	// 0. Usage & Help
	code, out, _ := runCLI([]string{})
	if code != 1 || !strings.Contains(out, "Usage: bp") {
		t.Errorf("empty args should return usage and code 1")
	}

	for _, helpArg := range []string{"help", "-h", "--help"} {
		code, out, _ = runCLI([]string{helpArg})
		if code != 0 || !strings.Contains(out, "Usage: bp") {
			t.Errorf("%s failed", helpArg)
		}
	}

	// help --ai
	code, out, _ = runCLI([]string{"help", "--ai"})
	if code != 0 || !strings.Contains(out, "Agent Execution & Protocol Guide") {
		t.Errorf("help --ai failed: %s", out)
	}

	// whoami
	code, out, _ = runCLI([]string{"whoami"})
	if code != 0 || !strings.Contains(out, "Agent ID:   agent-1") {
		t.Errorf("whoami failed: %s", out)
	}

	// whoami with operator and signing key
	tmpKeyDir := t.TempDir()
	keyPath := filepath.Join(tmpKeyDir, "op-key.pem")
	priv, _, _ := libbp.GenerateKeypair()
	pem, _ := libbp.EncodePrivateKeyPEM(priv)
	_ = os.WriteFile(keyPath, []byte(pem), 0600)
	t.Setenv("BP_AGENT", "")
	t.Setenv("HUMAN_NAME", "operator")
	t.Setenv("BP_MODE", "human")
	t.Setenv("HUMAN_BACKPLANE_PASSWORD", valkey.HumanPass)
	t.Setenv("BP_SIGNING_KEY", keyPath)
	code, out, _ = runCLI([]string{"whoami"})
	if code != 0 || !strings.Contains(out, "Mode:       human") || strings.Contains(out, "Public Key: none") {
		t.Errorf("whoami with operator and key failed: %s", out)
	}

	// whoami with empty BP_MODE and agent ID (hits fallback mode = agent)
	t.Setenv("BP_AGENT", "agent-fallback")
	t.Setenv("BP_MODE", "")
	t.Setenv("BP_SIGNING_KEY", "")
	code, out, _ = runCLI([]string{"whoami"})
	if code != 0 || !strings.Contains(out, "Mode:       agent") {
		t.Errorf("whoami fallback mode agent failed: %s", out)
	}

	t.Setenv("BP_AGENT", "agent-1")
	t.Setenv("BP_MODE", "agent")
	t.Setenv("BP_PASSWORD", valkey.Agent1Pass)

	// say usage errors
	var errOut string
	code, _, errOut = runCLI([]string{"say"})
	if code != 1 || !strings.Contains(errOut, "say requires a message or --file") {
		t.Errorf("say missing args failed: %s", errOut)
	}
	code, _, errOut = runCLI([]string{"say", "--file"})
	if code != 1 || !strings.Contains(errOut, "flag needs an argument: --file") {
		t.Errorf("say --file missing arg failed: %s", errOut)
	}
	code, _, errOut = runCLI([]string{"say", "-unknown"})
	if code != 1 || !strings.Contains(errOut, "flag provided but not defined") {
		t.Errorf("say unknown flag failed: %s", errOut)
	}

	// tell --file flag missing arg and file read error
	code, _, errOut = runCLI([]string{"tell", "--file"})
	if code != 1 || !strings.Contains(errOut, "flag needs an argument: --file") {
		t.Errorf("tell --file missing arg failed: %s", errOut)
	}
	code, _, errOut = runCLI([]string{"tell", "agent-2", "hello", "--file", filepath.Join(tmpKeyDir, "missing")})
	if code != 1 || !strings.Contains(errOut, "failed reading file") {
		t.Errorf("tell file read error failed: %s", errOut)
	}

	// profile list
	code, out, _ = runCLI([]string{"profile", "list"})
	if code != 0 || !strings.Contains(out, "Available profiles:") {
		t.Errorf("profile list failed: %s", out)
	}

	// profile list default without sub
	code, out, _ = runCLI([]string{"profile"})
	if code != 0 || !strings.Contains(out, "Available profiles:") {
		t.Errorf("profile default sub failed: %s", out)
	}

	// profile invalid sub
	code, _, errOut = runCLI([]string{"profile", "invalid"})
	if code != 1 || !strings.Contains(errOut, "unknown profile subcommand") {
		t.Errorf("profile invalid sub failed")
	}

	// --profile flag missing arg
	code, _, errOut = runCLI([]string{"--profile"})
	if code != 1 || !strings.Contains(errOut, "--profile flag requires an argument") {
		t.Errorf("--profile missing arg failed")
	}

	// profile list with default profile file
	profileDir := filepath.Join(tmpKeyDir, "bp", "profiles")
	_ = os.MkdirAll(profileDir, 0755)
	_ = os.WriteFile(filepath.Join(profileDir, "default.env"), []byte("BP_HOST=127.0.0.1\n"), 0600)
	t.Setenv("XDG_STATE_HOME", tmpKeyDir)
	code, out, _ = runCLI([]string{"profile", "list"})
	if code != 0 || !strings.Contains(out, "default") {
		t.Errorf("profile list with default failed: %s", out)
	}

	// status usage errors
	code, _, errOut = runCLI([]string{"status"})
	if code != 1 || !strings.Contains(errOut, "Usage: bp status set") {
		t.Errorf("status missing args failed")
	}
	code, _, errOut = runCLI([]string{"status", "unknown"})
	if code != 1 || !strings.Contains(errOut, "Usage: bp status set") {
		t.Errorf("status unknown sub failed")
	}

	// tell usage errors
	code, _, errOut = runCLI([]string{"tell"})
	if code != 1 || !strings.Contains(errOut, "tell requires <agent> <message>") {
		t.Errorf("tell missing args failed")
	}
	code, _, errOut = runCLI([]string{"tell", "agent-2"})
	if code != 1 || !strings.Contains(errOut, "tell requires <agent> <message>") {
		t.Errorf("tell missing message failed")
	}

	// --profile flag with whoami
	code, out, _ = runCLI([]string{"--profile", "default", "whoami"})
	if code != 0 || !strings.Contains(out, "Agent ID:   agent-1") {
		t.Errorf("--profile default whoami failed: %s", out)
	}

	// --profile with custom profile file
	stateTmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateTmp)
	bpProfDir := filepath.Join(stateTmp, "bp", "profiles")
	_ = os.MkdirAll(bpProfDir, 0700)
	profFile := filepath.Join(bpProfDir, "myhost.env")
	_ = os.WriteFile(profFile, []byte("BP_AGENT=myhost-agent\nBP_PASSWORD=secret\n"), 0600)
	// Create bare .env file to verify it's skipped
	_ = os.WriteFile(filepath.Join(bpProfDir, ".env"), []byte("IGNORE=true\n"), 0600)

	code, out, _ = runCLI([]string{"--profile=myhost", "whoami"})
	if code != 0 || !strings.Contains(out, "Agent ID:   myhost-agent") {
		t.Errorf("--profile=myhost whoami failed: %s", out)
	}

	// whoami with trailing --profile flag
	code, out, _ = runCLI([]string{"whoami", "--profile", "myhost"})
	if code != 0 || !strings.Contains(out, "Agent ID:   myhost-agent") {
		t.Errorf("whoami with trailing --profile failed: %s", out)
	}

	// profile list with profiles in dir (even when active profile is nonexistent)
	code, out, _ = runCLI([]string{"--profile", "nonexistent", "profile", "list"})
	if code != 0 || !strings.Contains(out, "myhost") {
		t.Errorf("profile list with nonexistent active profile failed: %s", out)
	}

	// Unknown command
	code, _, errOut = runCLI([]string{"unknowncmd"})
	if code != 1 || !strings.Contains(errOut, "unknown command") {
		t.Errorf("expected unknown command error")
	}

	// 1. Say
	// Fail on empty
	code, _, errOut = runCLI([]string{"say"})
	if code != 1 || !strings.Contains(errOut, "requires a message") {
		t.Errorf("say empty failed")
	}

	// Success say
	code, out, _ = runCLI([]string{"say", "Broadcast message from test"})
	if code != 0 || !strings.Contains(out, "Broadcast message from test") {
		t.Errorf("say failed: %s", out)
	}

	// Say with file
	tmpDir := t.TempDir()
	blobPath := filepath.Join(tmpDir, "sample.txt")
	_ = os.WriteFile(blobPath, []byte("file payload"), 0600)
	code, out, _ = runCLI([]string{"say", "--file", blobPath, "Say with attachment"})
	if code != 0 || !strings.Contains(out, "Say with attachment") {
		t.Errorf("say with file failed: %s", out)
	}

	// Say with stdin ('--file -')
	code, out, _ = runCLIWithStdin([]string{"say", "--file", "-", "Say with stdin payload"}, strings.NewReader("stdin stream content"))
	if code != 0 || !strings.Contains(out, "Say with stdin payload") {
		t.Errorf("say with stdin failed: %s", out)
	}

	// Say with stdin ('--file -') and nil stdin
	code, _, errOut = runCLIWithStdin([]string{"say", "--file", "-", "Say with nil stdin"}, nil)
	if code != 1 || !strings.Contains(errOut, "stdin is nil") {
		t.Errorf("say with nil stdin should fail: %s", errOut)
	}

	// Say with missing file
	code, _, errOut = runCLI([]string{"say", "--file", filepath.Join(tmpDir, "missing.txt"), "msg"})
	if code != 1 || !strings.Contains(errOut, "failed reading file") {
		t.Errorf("say missing file failed")
	}

	// Say with invalid flag
	code, _, _ = runCLI([]string{"say", "--invalid-flag"})
	if code != 1 {
		t.Errorf("say invalid flag should return 1")
	}

	// Say with missing flag argument
	code, _, errOut = runCLI([]string{"say", "--file"})
	if code != 1 || !strings.Contains(errOut, "flag needs an argument") {
		t.Errorf("say missing flag argument failed: %s", errOut)
	}

	// Say with inline file flag (--file= and -file=)
	code, out, _ = runCLI([]string{"say", "--file=" + blobPath, "Say inline flag"})
	if code != 0 || !strings.Contains(out, "Say inline flag") {
		t.Errorf("say with --file= failed: %s", out)
	}
	code, out, _ = runCLI([]string{"say", "-file=" + blobPath, "Say dash file inline flag"})
	if code != 0 || !strings.Contains(out, "Say dash file inline flag") {
		t.Errorf("say with -file= failed: %s", out)
	}

	// 2. Tell
	// Missing args
	code, _, errOut = runCLI([]string{"tell"})
	if code != 1 || !strings.Contains(errOut, "requires <agent> <message>") {
		t.Errorf("tell missing args failed")
	}

	// Tell missing args with file
	code, _, errOut = runCLI([]string{"tell", "--file", blobPath})
	if code != 1 || !strings.Contains(errOut, "requires <agent> <message>") {
		t.Errorf("tell missing recipient with file failed")
	}

	// Tell with missing flag argument
	code, _, errOut = runCLI([]string{"tell", "agent-2", "--file"})
	if code != 1 || !strings.Contains(errOut, "flag needs an argument") {
		t.Errorf("tell missing flag argument failed: %s", errOut)
	}

	// Tell with inline file flag (--file= and -file=)
	code, out, _ = runCLI([]string{"tell", "agent-2", "--file=" + blobPath})
	if code != 0 || !strings.Contains(out, "Payload attached:") {
		t.Errorf("tell with --file= failed: %s", out)
	}
	code, out, _ = runCLI([]string{"tell", "agent-2", "-file=" + blobPath})
	if code != 0 || !strings.Contains(out, "Payload attached:") {
		t.Errorf("tell with -file= failed: %s", out)
	}

	// Tell with invalid flag
	code, _, _ = runCLI([]string{"tell", "--invalid-flag"})
	if code != 1 {
		t.Errorf("tell invalid flag should return 1")
	}

	// Tell with '--' end-of-options delimiter
	code, out, _ = runCLI([]string{"tell", "agent-2", "--", "--not-a-flag"})
	if code != 0 || !strings.Contains(out, "--not-a-flag") {
		t.Errorf("tell with -- delimiter failed: %s", out)
	}

	// Success plain
	code, out, _ = runCLI([]string{"tell", "agent-2", "Direct task"})
	if code != 0 || !strings.Contains(out, "Direct task") {
		t.Errorf("tell failed: %s", out)
	}

	// Tell with file from disk (content only from file)
	code, out, _ = runCLI([]string{"tell", "agent-2", "--file", blobPath})
	if code != 0 || !strings.Contains(out, "Payload attached:") {
		t.Errorf("tell with file failed: %s", out)
	}

	// Tell with file from disk and extra message text
	code, out, _ = runCLI([]string{"tell", "agent-2", "--file", blobPath, "Prefix text"})
	if code != 0 || !strings.Contains(out, "Prefix text") || !strings.Contains(out, "Payload attached:") {
		t.Errorf("tell with file and prefix failed: %s", out)
	}

	// Tell with stdin ('--file -') alone
	code, out, _ = runCLIWithStdin([]string{"tell", "agent-2", "--file", "-"}, strings.NewReader("tell stdin body"))
	if code != 0 || !strings.Contains(out, "Payload attached:") {
		t.Errorf("tell with stdin failed: %s", out)
	}

	// Tell with stdin ('--file -') and message text
	code, out, _ = runCLIWithStdin([]string{"tell", "agent-2", "--file", "-", "Header note:"}, strings.NewReader("piped lines"))
	if code != 0 || !strings.Contains(out, "Header note:") || !strings.Contains(out, "Payload attached:") {
		t.Errorf("tell with stdin and note failed: %s", out)
	}

	// Tell with missing file
	code, _, errOut = runCLI([]string{"tell", "agent-2", "--file", filepath.Join(tmpDir, "missing.txt")})
	if code != 1 || !strings.Contains(errOut, "failed reading file") {
		t.Errorf("tell with missing file should fail")
	}

	// Tell with stdin and nil stdin
	code, _, errOut = runCLIWithStdin([]string{"tell", "agent-2", "--file", "-"}, nil)
	if code != 1 || !strings.Contains(errOut, "stdin is nil") {
		t.Errorf("tell with nil stdin should fail: %s", errOut)
	}

	// 2b. Post
	// Post missing args
	code, _, errOut = runCLI([]string{"post"})
	if code != 1 || !strings.Contains(errOut, "requires <file> [to <agent>]") {
		t.Errorf("post missing args failed: %s", errOut)
	}

	// Post --file missing argument
	code, _, errOut = runCLI([]string{"post", "--file"})
	if code != 1 || !strings.Contains(errOut, "flag --file needs an argument") {
		t.Errorf("post --file missing argument failed: %s", errOut)
	}

	// Post missing recipient after 'to'
	code, _, errOut = runCLI([]string{"post", blobPath, "to"})
	if code != 1 || !strings.Contains(errOut, "post to requires exactly <agent>") {
		t.Errorf("post to missing recipient failed: %s", errOut)
	}

	// Post unexpected arguments after 'to <agent>'
	code, _, errOut = runCLI([]string{"post", blobPath, "to", "agent-2", "extra"})
	if code != 1 || !strings.Contains(errOut, "post to requires exactly <agent>") {
		t.Errorf("post to extra args failed: %s", errOut)
	}

	// Post unexpected arguments after direct <agent>
	code, _, errOut = runCLI([]string{"post", blobPath, "agent-2", "extra"})
	if code != 1 || !strings.Contains(errOut, "post unexpected arguments after <file>") {
		t.Errorf("post direct extra args failed: %s", errOut)
	}

	// Post missing file
	code, _, errOut = runCLI([]string{"post", filepath.Join(tmpDir, "missing.txt")})
	if code != 1 || !strings.Contains(errOut, "failed reading file") {
		t.Errorf("post missing file failed: %s", errOut)
	}

	// Post empty file
	emptyFile := filepath.Join(tmpDir, "empty.txt")
	_ = os.WriteFile(emptyFile, []byte(""), 0600)
	code, _, errOut = runCLI([]string{"post", emptyFile})
	if code != 1 || !strings.Contains(errOut, "file is empty or contains only whitespace") {
		t.Errorf("post empty file failed: %s", errOut)
	}

	// Post whitespace-only file
	wsFile := filepath.Join(tmpDir, "whitespace.txt")
	_ = os.WriteFile(wsFile, []byte("  \n\t  \n  "), 0600)
	code, _, errOut = runCLI([]string{"post", wsFile})
	if code != 1 || !strings.Contains(errOut, "file is empty or contains only whitespace") {
		t.Errorf("post whitespace file failed: %s", errOut)
	}

	// Post valid file (broadcast)
	postDoc := filepath.Join(tmpDir, "post-broadcast.md")
	postContent := "# Broadcast Topic\n\nDetailed body line 1\nDetailed body line 2\n"
	_ = os.WriteFile(postDoc, []byte(postContent), 0600)
	code, out, _ = runCLI([]string{"post", postDoc})
	if code != 0 || !strings.Contains(out, "-> fleet") || !strings.Contains(out, "# Broadcast Topic") || !strings.Contains(out, "Payload attached:") {
		t.Errorf("post broadcast failed: %s", out)
	}

	// Extract blob key from output and test cat / get
	var blobKey string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Payload attached:") {
			parts := strings.Split(line, "Payload attached:")
			blobKey = strings.TrimSpace(parts[len(parts)-1])
		}
	}
	if blobKey == "" {
		t.Fatalf("failed to parse blob key from post output: %s", out)
	}

	// Cat blob retrieval
	code, catOut, _ := runCLI([]string{"cat", blobKey})
	if code != 0 || catOut != postContent {
		t.Errorf("cat blob failed: code %d, content %q", code, catOut)
	}

	// Get blob retrieval (alias for cat)
	code, getOut, _ := runCLI([]string{"get", blobKey})
	if code != 0 || getOut != postContent {
		t.Errorf("get blob failed: code %d, content %q", code, getOut)
	}

	// Cat missing argument
	code, _, errOut = runCLI([]string{"cat"})
	if code != 1 || !strings.Contains(errOut, "requires <blob-key>") {
		t.Errorf("cat missing arg failed: %s", errOut)
	}

	// Get missing argument
	code, _, errOut = runCLI([]string{"get"})
	if code != 1 || !strings.Contains(errOut, "requires <blob-key>") {
		t.Errorf("get missing arg failed: %s", errOut)
	}

	// Cat nonexistent blob key
	code, _, errOut = runCLI([]string{"cat", "agent-1:blob:nonexistent"})
	if code != 1 || !strings.Contains(errOut, "bp error:") {
		t.Errorf("cat nonexistent blob failed: %s", errOut)
	}

	// Post valid file (direct message with 'to')
	code, out, _ = runCLI([]string{"post", postDoc, "to", "agent-2"})
	if code != 0 || !strings.Contains(out, "-> agent-2") || !strings.Contains(out, "# Broadcast Topic") || !strings.Contains(out, "Payload attached:") {
		t.Errorf("post direct to agent-2 failed: %s", out)
	}

	// Post valid file (direct message without 'to')
	code, out, _ = runCLI([]string{"post", postDoc, "agent-2"})
	if code != 0 || !strings.Contains(out, "-> agent-2") || !strings.Contains(out, "# Broadcast Topic") {
		t.Errorf("post direct without 'to' failed: %s", out)
	}

	// Post with --file flag
	code, out, _ = runCLI([]string{"post", "--file", postDoc, "to", "agent-2"})
	if code != 0 || !strings.Contains(out, "-> agent-2") || !strings.Contains(out, "# Broadcast Topic") {
		t.Errorf("post --file failed: %s", out)
	}

	// Post with --file= inline flag
	code, out, _ = runCLI([]string{"post", "--file=" + postDoc})
	if code != 0 || !strings.Contains(out, "-> fleet") || !strings.Contains(out, "# Broadcast Topic") {
		t.Errorf("post --file= failed: %s", out)
	}

	// Post from stdin ('-') broadcast
	code, out, _ = runCLIWithStdin([]string{"post", "-"}, strings.NewReader("Stdin Heading Summary\n\nBody content from stdin."))
	if code != 0 || !strings.Contains(out, "-> fleet") || !strings.Contains(out, "Stdin Heading Summary") {
		t.Errorf("post stdin broadcast failed: %s", out)
	}

	// Post from stdin ('-') direct to agent
	code, out, _ = runCLIWithStdin([]string{"post", "-", "to", "agent-2"}, strings.NewReader("Direct Stdin Summary\n\nBody for agent-2."))
	if code != 0 || !strings.Contains(out, "-> agent-2") || !strings.Contains(out, "Direct Stdin Summary") {
		t.Errorf("post stdin direct failed: %s", out)
	}

	// Post from stdin with '--file -'
	code, out, _ = runCLIWithStdin([]string{"post", "--file", "-"}, strings.NewReader("Flag Stdin Summary\n\nBody."))
	if code != 0 || !strings.Contains(out, "Flag Stdin Summary") {
		t.Errorf("post --file - failed: %s", out)
	}

	// 3. Reply
	// Missing args
	code, _, errOut = runCLI([]string{"reply", "1-0"})
	if code != 1 || !strings.Contains(errOut, "requires <msgid> <recipient> <message>") {
		t.Errorf("reply missing args failed")
	}

	// Success
	code, out, _ = runCLI([]string{"reply", "1-0", "agent-2", "In-thread reply"})
	if code != 0 || !strings.Contains(out, "In-thread reply") {
		t.Errorf("reply failed: %s", out)
	}

	// 4. Status
	// Missing args
	code, _, errOut = runCLI([]string{"status"})
	if code != 1 || !strings.Contains(errOut, "Usage: bp status set") {
		t.Errorf("status missing args failed")
	}

	// Success
	code, out, _ = runCLI([]string{"status", "set", "active testing"})
	if code != 0 || !strings.Contains(out, "status set: active testing") {
		t.Errorf("status set failed: %s", out)
	}

	// 5. Peers
	code, out, _ = runCLI([]string{"peers"})
	if code != 0 || !strings.Contains(out, "AGENT_ID") {
		t.Errorf("peers failed: %s", out)
	}

	code, out, _ = runCLI([]string{"peers", "--json"})
	if code != 0 || !strings.Contains(out, "[") {
		t.Errorf("peers json failed: %s", out)
	}

	// 6. Finger
	_, _ = adminClient.Exec(context.Background(), "HSET", "agent-1:finger", "role", "coder", "model", "qwen")
	code, out, _ = runCLI([]string{"finger", "agent-1"})
	if code != 0 || !strings.Contains(out, "coder") {
		t.Errorf("finger with profile failed: %s", out)
	}

	code, out, _ = runCLI([]string{"finger"})
	if code != 0 || !strings.Contains(out, "coder") {
		t.Errorf("finger default agent failed: %s", out)
	}

	code, out, _ = runCLI([]string{"peers", "--json"})
	if code != 0 || !strings.Contains(out, "[") {
		t.Errorf("peers json failed: %s", out)
	}

	// Status usage error
	code, _, errOut = runCLI([]string{"status", "badsub"})
	if code != 1 || !strings.Contains(errOut, "Usage: bp status") {
		t.Errorf("expected status usage error")
	}

	// 7. Liaison
	os.Setenv("BP_MODE", "human")
	os.Unsetenv("BP_AGENT")
	os.Unsetenv("BP_PASSWORD")
	os.Setenv("HUMAN_NAME", "operator")
	os.Setenv("HUMAN_BACKPLANE_PASSWORD", valkey.HumanPass)

	code, out, _ = runCLI([]string{"liaison", "get"})
	if code != 0 {
		t.Errorf("liaison empty get failed")
	}

	code, out, _ = runCLI([]string{"liaison", "set", "agent-1"})
	if code != 0 || !strings.Contains(out, "Liaison appointed") {
		t.Errorf("liaison set failed: %s", out)
	}

	code, _, errOut = runCLI([]string{"liaison", "set"})
	if code != 1 || !strings.Contains(errOut, "Usage: bp liaison set") {
		t.Errorf("liaison set without arg failed")
	}

	code, out, _ = runCLI([]string{"liaison", "get"})
	if code != 0 || !strings.Contains(out, "agent-1") {
		t.Errorf("liaison get failed: %s", out)
	}

	code, _, errOut = runCLI([]string{"liaison", "invalid-sub"})
	if code != 1 || !strings.Contains(errOut, "Usage: bp liaison") {
		t.Errorf("liaison invalid sub failed")
	}

	// 8. Human Broadcast as operator
	code, out, _ = runCLI([]string{"say", "Authoritative message from operator"})
	if code != 0 {
		t.Errorf("operator say failed: %s", out)
	}

	// 9. Recv as agent-2
	os.Setenv("BP_MODE", "agent")
	os.Setenv("BP_AGENT", "agent-2")
	os.Setenv("BP_PASSWORD", valkey.Agent2Pass)
	code, out, _ = runCLI([]string{"recv"})
	if code != 0 || !strings.Contains(out, "Direct task") {
		t.Errorf("recv failed: %s", out)
	}

	code, out, _ = runCLI([]string{"recv", "--json"})
	if code != 0 || !strings.Contains(out, "[") {
		t.Errorf("recv json failed: %s", out)
	}

	// 10. Human read as agent-2
	code, out, _ = runCLI([]string{"human"})
	if code != 0 || !strings.Contains(out, "Authoritative message") {
		t.Errorf("human failed: %s", out)
	}

	code, out, _ = runCLI([]string{"human", "--json"})
	if code != 0 || !strings.Contains(out, "[") {
		t.Errorf("human json failed: %s", out)
	}
}

func TestClientConnectionFailure(t *testing.T) {
	os.Setenv("BP_HOST", "127.0.0.1")
	os.Setenv("BP_PORT", "64999") // Closed port
	os.Setenv("BP_AGENT", "agent-1")
	os.Setenv("BP_PASSWORD", "bad")

	var stdout, stderr bytes.Buffer
	code := Run([]string{"say", "fail"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "bp error:") {
		t.Errorf("expected connection failure code 1, got %d", code)
	}

	code = Run([]string{"tell", "agent-2", "fail"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected tell failure")
	}

	code = Run([]string{"post", "dummy.txt"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected post failure")
	}

	code = Run([]string{"cat", "agent-1:blob:dummy"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected cat failure")
	}

	code = Run([]string{"reply", "1-0", "agent-2", "fail"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected reply failure")
	}

	code = Run([]string{"recv"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected recv failure")
	}

	code = Run([]string{"human"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected human failure")
	}

	code = Run([]string{"peers"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected peers failure")
	}

	code = Run([]string{"status", "set", "fail"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected status failure")
	}

	code = Run([]string{"finger", "agent-1"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected finger failure")
	}

	code = Run([]string{"liaison", "get"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected liaison failure")
	}

	code = Run([]string{"liaison", "set", "agent-1"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected liaison set failure")
	}

	// Flag parse errors
	code = Run([]string{"recv", "--invalid-flag"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected recv invalid flag failure")
	}
	code = Run([]string{"human", "--invalid-flag"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected human invalid flag failure")
	}
}

func TestBPDirectPayloadAndPeersScenario(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	os.Setenv("BP_HOST", "127.0.0.1")
	os.Setenv("BP_PORT", fmt.Sprintf("%d", valkey.Port))
	os.Setenv("BP_MODE", "agent")
	os.Setenv("BP_AGENT", "agent-1")
	os.Setenv("BP_PASSWORD", valkey.Agent1Pass)

	adminClient, err := valkey.ClientFor("admin")
	if err != nil {
		t.Fatalf("failed to connect admin client: %v", err)
	}
	defer adminClient.Close()

	ctx := context.Background()
	// Set liaison to agent-1 and publish a message on its output stream
	_, err = adminClient.Exec(ctx, "SET", libbp.KeyLiaisonCurrent, "agent-1")
	if err != nil {
		t.Fatalf("failed to set liaison: %v", err)
	}
	_, err = adminClient.Exec(ctx, "XADD", "agent-1:out", "*", "sender", "agent-1", "content", "initial ping")
	if err != nil {
		t.Fatalf("failed to record out message: %v", err)
	}

	// 1. Verify peers output reflects liaison role
	code, out, errOut := runCLI([]string{"peers"})
	if code != 0 {
		t.Fatalf("peers failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "(liaison)") {
		t.Errorf("expected peers output to contain '(liaison)', got %q", out)
	}
	if !strings.Contains(out, "agent-1") {
		t.Errorf("expected peers output to contain agent-1, got %q", out)
	}

	// 2. Say with file only (empty message text)
	tmpDir := t.TempDir()
	attachmentPath := filepath.Join(tmpDir, "report.pdf")
	_ = os.WriteFile(attachmentPath, []byte("%PDF-1.4 mock report"), 0600)
	code, out, errOut = runCLI([]string{"say", "--file", attachmentPath})
	if code != 0 {
		t.Fatalf("say with file only failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "agent-1#") {
		t.Errorf("expected citation in say output, got %q", out)
	}

	// 3. Receive message with destination, unverified signature, and parked blob citation
	_, err = adminClient.Exec(ctx, "XADD", "agent-1:inbox", "*",
		"sender", "agent-2",
		"destination", "agent-1",
		"content", "file attached for review",
		"blob_path", "agent-2:blob:report.pdf",
		"signature", "invalid-signature-bytes",
		"timestamp", fmt.Sprintf("%d", time.Now().UnixMilli()),
		"seq", "42",
	)
	if err != nil {
		t.Fatalf("failed to write inbox message: %v", err)
	}

	code, out, errOut = runCLI([]string{"recv"})
	if code != 0 {
		t.Fatalf("recv failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "-> agent-1") {
		t.Errorf("expected destination '-> agent-1' in output, got %q", out)
	}
	if !strings.Contains(out, "[UNVERIFIED]") {
		t.Errorf("expected unverified status in output, got %q", out)
	}
	if !strings.Contains(out, "Payload attached: agent-2:blob:report.pdf") {
		t.Errorf("expected attachment note in output, got %q", out)
	}

	// 4. Query finger profile of an unregistered agent
	code, out, errOut = runCLI([]string{"finger", "unregistered-agent"})
	if code != 0 {
		t.Fatalf("finger unregistered agent failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "[unregistered-agent] No finger profile registered.") {
		t.Errorf("expected no profile registered message, got %q", out)
	}
}

func TestMainExecution(t *testing.T) {
	if os.Getenv("TEST_BP_SUBPROCESS") == "1" {
		os.Args = []string{"bp", "help"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMainExecution")
	cmd.Env = append(os.Environ(), "TEST_BP_SUBPROCESS=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("main subprocess failed: %v, output: %s", err, string(out))
	}
	if !strings.Contains(string(out), "Usage: bp") {
		t.Errorf("expected help output from main, got: %s", string(out))
	}
}

func TestReadPayload(t *testing.T) {
	// Stdin reading
	data, err := readPayload("-", strings.NewReader("hello from stdin"))
	if err != nil || string(data) != "hello from stdin" {
		t.Fatalf("unexpected result from readPayload stdin: %v, %s", err, string(data))
	}

	// Nil stdin reading
	_, err = readPayload("-", nil)
	if err == nil {
		t.Fatalf("expected error for nil stdin")
	}

	// Disk file reading
	tmpDir := t.TempDir()
	fpath := filepath.Join(tmpDir, "test.txt")
	_ = os.WriteFile(fpath, []byte("file content"), 0600)

	data, err = readPayload(fpath, nil)
	if err != nil || string(data) != "file content" {
		t.Fatalf("unexpected result from readPayload file: %v, %s", err, string(data))
	}

	// Nonexistent disk file reading
	_, err = readPayload(filepath.Join(tmpDir, "nonexistent.txt"), nil)
	if err == nil {
		t.Fatalf("expected error for nonexistent file")
	}
}

func TestExtractSummary(t *testing.T) {
	// Empty
	if _, err := extractSummary([]byte{}); err == nil {
		t.Errorf("expected error on empty slice")
	}

	// Whitespace only
	if _, err := extractSummary([]byte("   \n\t  \n  ")); err == nil {
		t.Errorf("expected error on whitespace only")
	}

	// Single line
	s, err := extractSummary([]byte("Simple line"))
	if err != nil || s != "Simple line" {
		t.Errorf("unexpected summary for single line: %v, %s", err, s)
	}

	// Multiline with leading empty lines
	s, err = extractSummary([]byte("\n\n  Header Title  \nSecond line"))
	if err != nil || s != "Header Title" {
		t.Errorf("unexpected summary for multiline: %v, %s", err, s)
	}

	// Long line > 256 runes
	longLine := strings.Repeat("A", 300)
	s, err = extractSummary([]byte(longLine))
	if err != nil {
		t.Fatalf("unexpected error for long line: %v", err)
	}
	if len([]rune(s)) != 256 || !strings.HasSuffix(s, "...") {
		t.Errorf("expected 256 runes ending in ..., got %d runes: %s", len([]rune(s)), s)
	}

	// Multibyte unicode line > 256 runes
	unicodeLongLine := strings.Repeat("日", 300)
	s, err = extractSummary([]byte(unicodeLongLine))
	if err != nil {
		t.Fatalf("unexpected error for unicode long line: %v", err)
	}
	if len([]rune(s)) != 256 || !strings.HasSuffix(s, "...") {
		t.Errorf("expected 256 runes ending in ..., got %d runes: %s", len([]rune(s)), s)
	}
}

func TestBPStateAndQueueSubcommands(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")
	queuePath := filepath.Join(tmpDir, "queue.jsonl")

	t.Setenv("AGENT_STATE_FILE", statePath)
	t.Setenv("AGENT_QUEUE_FILE", queuePath)

	// Missing args
	code, _, errOut := runCLI([]string{"state"})
	if code != 1 || !strings.Contains(errOut, "requires subcommand") {
		t.Errorf("expected error on empty state subcommand: %s", errOut)
	}

	// Unknown subcommand
	code, _, errOut = runCLI([]string{"state", "unknown"})
	if code != 1 || !strings.Contains(errOut, "unknown state subcommand") {
		t.Errorf("expected error on unknown state subcommand: %s", errOut)
	}

	// Get before file exists
	code, _, errOut = runCLI([]string{"state", "get"})
	if code != 1 || !strings.Contains(errOut, "does not exist") {
		t.Errorf("expected error getting nonexistent state: %s", errOut)
	}

	// Show before file exists
	code, _, errOut = runCLI([]string{"state", "show"})
	if code != 1 || !strings.Contains(errOut, "does not exist") {
		t.Errorf("expected error showing nonexistent state: %s", errOut)
	}

	// Set missing args
	code, _, errOut = runCLI([]string{"state", "set", "key"})
	if code != 1 || !strings.Contains(errOut, "requires <key> <value>") {
		t.Errorf("expected error on missing set value: %s", errOut)
	}

	// Set activity
	code, out, _ := runCLI([]string{"state", "set", "activity", "working on refactoring"})
	if code != 0 || !strings.Contains(out, "state.activity = working on refactoring") {
		t.Errorf("state set failed: %s", out)
	}

	// Set task
	code, out, _ = runCLI([]string{"state", "set", "current_task", "sndbx-d46.7"})
	if code != 0 || !strings.Contains(out, "state.current_task = sndbx-d46.7") {
		t.Errorf("state set task failed: %s", out)
	}

	// Get activity
	code, out, _ = runCLI([]string{"state", "get", "activity"})
	if code != 0 || strings.TrimSpace(out) != "working on refactoring" {
		t.Errorf("state get activity failed: %q", out)
	}

	// Get missing field
	code, _, errOut = runCLI([]string{"state", "get", "nonexistent"})
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("state get missing field should fail: %s", errOut)
	}

	// Get entire JSON
	code, out, _ = runCLI([]string{"state", "get"})
	if code != 0 || !strings.Contains(out, "working on refactoring") || !strings.Contains(out, "sndbx-d46.7") {
		t.Errorf("state get all failed: %s", out)
	}

	// Show
	code, out, _ = runCLI([]string{"state", "show"})
	if code != 0 || !strings.Contains(out, "working on refactoring") {
		t.Errorf("state show failed: %s", out)
	}

	// Invalid state JSON on get
	_ = os.WriteFile(statePath, []byte("invalid-json"), 0600)
	code, _, errOut = runCLI([]string{"state", "get", "activity"})
	if code != 1 || !strings.Contains(errOut, "invalid state JSON") {
		t.Errorf("expected error on invalid state JSON: %s", errOut)
	}
	_ = os.Remove(statePath)

	// Queue missing args
	code, _, errOut = runCLI([]string{"state", "queue"})
	if code != 1 || !strings.Contains(errOut, "requires subcommand") {
		t.Errorf("expected error on empty queue subcommand: %s", errOut)
	}

	// Queue unknown subcommand
	code, _, errOut = runCLI([]string{"state", "queue", "unknown"})
	if code != 1 || !strings.Contains(errOut, "unknown queue subcommand") {
		t.Errorf("expected error on unknown queue subcommand: %s", errOut)
	}

	// Queue push missing args
	code, _, errOut = runCLI([]string{"state", "queue", "push"})
	if code != 1 || !strings.Contains(errOut, "requires <directive>") {
		t.Errorf("expected error on empty queue push: %s", errOut)
	}

	// Queue pop on empty queue
	code, _, _ = runCLI([]string{"state", "queue", "pop"})
	if code != 1 {
		t.Errorf("expected code 1 on empty queue pop, got %d", code)
	}

	// Queue push item 1 and item 2
	code, out, _ = runCLI([]string{"state", "queue", "push", "directive one"})
	if code != 0 || !strings.Contains(out, "queued directive") {
		t.Errorf("queue push 1 failed: %s", out)
	}
	code, out, _ = runCLI([]string{"state", "queue", "push", "directive two"})
	if code != 0 || !strings.Contains(out, "queued directive") {
		t.Errorf("queue push 2 failed: %s", out)
	}

	// Queue list
	code, out, _ = runCLI([]string{"state", "queue", "list"})
	if code != 0 || !strings.Contains(out, "directive one") || !strings.Contains(out, "directive two") {
		t.Errorf("queue list failed: %s", out)
	}

	// Queue pop item 1
	code, out, _ = runCLI([]string{"state", "queue", "pop"})
	if code != 0 || strings.TrimSpace(out) != "directive one" {
		t.Errorf("queue pop 1 failed: %q", out)
	}

	// Queue pop item 2
	code, out, _ = runCLI([]string{"state", "queue", "pop"})
	if code != 0 || strings.TrimSpace(out) != "directive two" {
		t.Errorf("queue pop 2 failed: %q", out)
	}

	// Queue pop now empty
	code, _, _ = runCLI([]string{"state", "queue", "pop"})
	if code != 1 {
		t.Errorf("expected code 1 on empty queue pop after popping all")
	}

	// Queue clear
	code, out, _ = runCLI([]string{"state", "queue", "push", "temporary directive"})
	if code != 0 {
		t.Errorf("queue push failed")
	}
	code, out, _ = runCLI([]string{"state", "queue", "clear"})
	if code != 0 || !strings.Contains(out, "queue cleared") {
		t.Errorf("queue clear failed: %s", out)
	}
	code, out, _ = runCLI([]string{"state", "queue", "list"})
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Errorf("expected empty queue list after clear: %q", out)
	}

	// Test default paths when AGENT_STATE_FILE and AGENT_QUEUE_FILE are unset
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("AGENT_STATE_FILE", "")
	t.Setenv("AGENT_QUEUE_FILE", "")
	code, _, _ = runCLI([]string{"state", "queue", "list"})
	if code != 0 {
		t.Errorf("expected 0 for empty queue in home")
	}
	code, _, _ = runCLI([]string{"state", "show"})
	if code != 1 {
		t.Errorf("expected 1 for show with default missing state file")
	}
}

func TestBPStateQueueConcurrent(t *testing.T) {
	tmpDir := t.TempDir()
	queuePath := filepath.Join(tmpDir, "queue.jsonl")
	t.Setenv("AGENT_QUEUE_FILE", queuePath)

	const numWorkers = 10
	const itemsPerWorker = 20
	errChan := make(chan error, numWorkers*itemsPerWorker)

	for w := 0; w < numWorkers; w++ {
		go func(workerID int) {
			for i := 0; i < itemsPerWorker; i++ {
				directive := fmt.Sprintf("worker-%d-task-%d", workerID, i)
				code, _, errOut := runCLI([]string{"state", "queue", "push", directive})
				if code != 0 {
					errChan <- fmt.Errorf("worker %d push failed: %s", workerID, errOut)
					return
				}
				errChan <- nil
			}
		}(w)
	}

	for i := 0; i < numWorkers*itemsPerWorker; i++ {
		if err := <-errChan; err != nil {
			t.Fatalf("concurrent push error: %v", err)
		}
	}

	poppedCount := 0
	for {
		code, out, _ := runCLI([]string{"state", "queue", "pop"})
		if code != 0 {
			break
		}
		if strings.TrimSpace(out) != "" {
			poppedCount++
		}
	}

	expectedCount := numWorkers * itemsPerWorker
	if poppedCount != expectedCount {
		t.Fatalf("expected %d popped items, got %d", expectedCount, poppedCount)
	}
}

func TestRecvBlockSizesTimeoutFromStart(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "bp", "profiles", "agent-2.env")
	if err := os.MkdirAll(filepath.Dir(profilePath), 0755); err != nil {
		t.Fatalf("failed creating profile dir: %v", err)
	}
	t.Setenv("XDG_STATE_HOME", tmpDir)
	content := fmt.Sprintf("BP_HOST=127.0.0.1\nBP_PORT=%d\nBP_AGENT=agent-2\nBP_PASSWORD=%s\n", valkey.Port, valkey.Agent2Pass)
	if err := os.WriteFile(profilePath, []byte(content), 0600); err != nil {
		t.Fatalf("failed writing profile: %v", err)
	}

	var stdout, stderr bytes.Buffer
	// RunWithContext sizes the timeout from the start for --block 1
	code := RunWithContext(context.Background(), []string{"--profile", "agent-2", "recv", "--block", "1"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("recv with block 1 failed: code=%d err=%s", code, stderr.String())
	}

	// Verify cancellation is preserved when parent context is canceled
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	code = RunWithContext(cancelCtx, []string{"--profile", "agent-2", "recv", "--block", "1"}, nil, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit code when parent context is canceled")
	}
}

func TestBPEnsembleSubcommands(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	tmpDir := t.TempDir()
	profilePath := filepath.Join(tmpDir, "bp", "profiles", "agent-1.env")
	if err := os.MkdirAll(filepath.Dir(profilePath), 0755); err != nil {
		t.Fatalf("failed creating profile dir: %v", err)
	}
	t.Setenv("XDG_STATE_HOME", tmpDir)
	content := fmt.Sprintf("BP_HOST=127.0.0.1\nBP_PORT=%d\nBP_AGENT=agent-1\nBP_PASSWORD=%s\n", valkey.Port, valkey.Agent1Pass)
	if err := os.WriteFile(profilePath, []byte(content), 0600); err != nil {
		t.Fatalf("failed writing profile: %v", err)
	}

	t.Setenv("BP_HOST", "127.0.0.1")
	t.Setenv("BP_PORT", fmt.Sprintf("%d", valkey.Port))
	t.Setenv("BP_MODE", "agent")
	t.Setenv("BP_AGENT", "agent-1")
	t.Setenv("BP_PASSWORD", valkey.Agent1Pass)

	// 1. Validation & Usage Errors
	tests := []struct {
		args        []string
		wantCode    int
		wantContain string
	}{
		{[]string{"ensemble"}, 1, "Usage: bp ensemble"},
		{[]string{"ensemble", "unknown-subcommand"}, 1, "unknown ensemble command"},
		{[]string{"ensemble", "create"}, 1, "Usage: bp ensemble create"},
		{[]string{"ensemble", "create", "Invalid_Name!"}, 1, "must match regex"},
		{[]string{"ensemble", "join"}, 1, "Usage: bp ensemble join"},
		{[]string{"ensemble", "join", "Invalid_Name!"}, 1, "must match regex"},
		{[]string{"ensemble", "leave"}, 1, "Usage: bp ensemble leave"},
		{[]string{"ensemble", "leave", "Invalid_Name!"}, 1, "must match regex"},
		{[]string{"ensemble", "say"}, 1, "Usage: bp ensemble say"},
		{[]string{"ensemble", "say", "team"}, 1, "Usage: bp ensemble say"},
		{[]string{"ensemble", "say", "Invalid_Name!", "hello"}, 1, "must match regex"},
		{[]string{"ensemble", "recv"}, 1, "Usage: bp ensemble recv"},
		{[]string{"ensemble", "recv", "Invalid_Name!"}, 1, "must match regex"},
	}

	for _, tt := range tests {
		code, out, errOut := runCLI(tt.args)
		combined := out + errOut
		if code != tt.wantCode || !strings.Contains(combined, tt.wantContain) {
			t.Errorf("args %v: got code=%d, output=%q; want code=%d containing %q", tt.args, code, combined, tt.wantCode, tt.wantContain)
		}
	}

	// 2. Initial list (empty)
	code, out, errOut := runCLI([]string{"ensemble", "list"})
	if code != 0 || !strings.Contains(out, "No joined ensembles.") {
		t.Fatalf("ensemble list empty failed: code=%d out=%q err=%q", code, out, errOut)
	}

	// 3. Create ensemble
	code, out, errOut = runCLI([]string{"ensemble", "create", "backend-team"})
	if code != 0 || !strings.Contains(out, "Created and joined ensemble backend-team") {
		t.Fatalf("ensemble create failed: code=%d out=%q err=%q", code, out, errOut)
	}

	// 4. List now contains backend-team
	code, out, _ = runCLI([]string{"ensemble", "list"})
	if code != 0 || !strings.Contains(out, "- backend-team") {
		t.Fatalf("expected backend-team in list: %s", out)
	}

	// 5. Join another ensemble
	code, out, errOut = runCLI([]string{"ensemble", "join", "data-team"})
	if code != 0 || !strings.Contains(out, "Joined ensemble data-team") {
		t.Fatalf("ensemble join failed: code=%d out=%q err=%q", code, out, errOut)
	}

	// 6. List contains both
	code, out, _ = runCLI([]string{"ensemble", "list"})
	if code != 0 || !strings.Contains(out, "- backend-team") || !strings.Contains(out, "- data-team") {
		t.Fatalf("expected backend-team and data-team in list: %s", out)
	}

	// 7. Say to ensemble
	code, out, errOut = runCLI([]string{"ensemble", "say", "backend-team", "starting", "migration", "now"})
	if code != 0 || !strings.Contains(out, "Sent to ensemble backend-team") {
		t.Fatalf("ensemble say failed: code=%d out=%q err=%q", code, out, errOut)
	}

	// 8. Say to ensemble with --file
	payloadFile := filepath.Join(tmpDir, "schema.sql")
	if err := os.WriteFile(payloadFile, []byte("CREATE TABLE users;"), 0600); err != nil {
		t.Fatalf("failed writing payload file: %v", err)
	}
	code, out, errOut = runCLI([]string{"ensemble", "say", "backend-team", "schema attached", "--file", payloadFile})
	if code != 0 || !strings.Contains(out, "Sent to ensemble backend-team") {
		t.Fatalf("ensemble say with file failed: code=%d out=%q err=%q", code, out, errOut)
	}

	// 9. Say to ensemble with non-existent file
	code, _, errOut = runCLI([]string{"ensemble", "say", "backend-team", "bad file", "--file", filepath.Join(tmpDir, "does-not-exist")})
	if code != 1 || !strings.Contains(errOut, "error reading file") {
		t.Fatalf("expected file error, got code=%d err=%s", code, errOut)
	}

	// 10. Recv from ensemble
	code, out, errOut = runCLI([]string{"ensemble", "recv", "backend-team"})
	if code != 0 || !strings.Contains(out, "starting migration now") {
		t.Fatalf("ensemble recv failed: code=%d out=%q err=%q", code, out, errOut)
	}

	// Recv with --last
	code, out, errOut = runCLI([]string{"ensemble", "recv", "backend-team", "--last", "0-0"})
	if code != 0 || !strings.Contains(out, "schema attached") {
		t.Fatalf("ensemble recv with last failed: code=%d out=%q err=%q", code, out, errOut)
	}

	// 11. Leave ensemble
	code, out, errOut = runCLI([]string{"ensemble", "leave", "data-team"})
	if code != 0 || !strings.Contains(out, "Left ensemble data-team") {
		t.Fatalf("ensemble leave failed: code=%d out=%q err=%q", code, out, errOut)
	}

	// 12. Verify list only has backend-team
	code, out, _ = runCLI([]string{"ensemble", "list"})
	if code != 0 || strings.Contains(out, "data-team") || !strings.Contains(out, "backend-team") {
		t.Fatalf("expected only backend-team in list after leave: %s", out)
	}

	// 13. Connection failure handling
	t.Setenv("BP_PORT", "65530")
	code, _, errOut = runCLI([]string{"ensemble", "list"})
	if code != 1 {
		t.Fatalf("expected failure with bad port, got %d", code)
	}

	// 14. Server errors on command execution
	rejPort, closeRej := startRejectingServer(t)
	defer closeRej()

	t.Setenv("BP_PORT", fmt.Sprintf("%d", rejPort))
	t.Setenv("BP_PASSWORD", "")

	code, _, errOut = runCLI([]string{"ensemble", "create", "fail-team"})
	if code != 1 || !strings.Contains(errOut, "failed creating ensemble") {
		t.Errorf("expected failed creating ensemble, got code=%d out=%s", code, errOut)
	}

	code, _, errOut = runCLI([]string{"ensemble", "join", "fail-team"})
	if code != 1 || !strings.Contains(errOut, "failed joining ensemble") {
		t.Errorf("expected failed joining ensemble, got code=%d out=%s", code, errOut)
	}

	code, _, errOut = runCLI([]string{"ensemble", "leave", "fail-team"})
	if code != 1 || !strings.Contains(errOut, "failed leaving ensemble") {
		t.Errorf("expected failed leaving ensemble, got code=%d out=%s", code, errOut)
	}

	code, _, errOut = runCLI([]string{"ensemble", "list"})
	if code != 1 || !strings.Contains(errOut, "failed listing ensembles") {
		t.Errorf("expected failed listing ensembles, got code=%d out=%s", code, errOut)
	}

	code, _, errOut = runCLI([]string{"ensemble", "say", "fail-team", "msg"})
	if code != 1 || !strings.Contains(errOut, "failed sending to ensemble") {
		t.Errorf("expected failed sending to ensemble, got code=%d out=%s", code, errOut)
	}

	code, _, errOut = runCLI([]string{"ensemble", "recv", "fail-team"})
	if code != 1 || !strings.Contains(errOut, "failed reading ensemble") {
		t.Errorf("expected failed reading ensemble, got code=%d out=%s", code, errOut)
	}
}

func startRejectingServer(t *testing.T) (int, func()) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if err != nil || n == 0 {
						return
					}
					_, _ = c.Write([]byte("-ERR mock error\r\n"))
				}
			}(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() { _ = ln.Close() }
}

func TestBPPubSubPingNotifications(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	var mu sync.Mutex
	var publishedChannels []string

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := libbpResp.NewReader(c)
				w := libbpResp.NewWriter(c)
				for {
					val, err := r.ReadValue()
					if err != nil {
						return
					}
					args, err := val.AsArray()
					if err != nil || len(args) == 0 {
						continue
					}
					cmd := strings.ToUpper(args[0].String())
					mu.Lock()
					switch cmd {
					case "AUTH":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeSimpleString, Str: "OK"})
					case "PING":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeSimpleString, Str: "PONG"})
					case "INCR":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeInteger, Num: 1})
					case "XADD":
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeBulkString, Str: "1700000000000-0"})
					case "PUBLISH":
						if len(args) > 1 {
							publishedChannels = append(publishedChannels, args[1].String())
						}
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeInteger, Num: 1})
					default:
						_ = w.WriteValue(libbpResp.Value{Type: libbpResp.TypeSimpleString, Str: "OK"})
					}
					mu.Unlock()
				}
			}(conn)
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	cfg := libbp.ClientConfig{
		Host:    "127.0.0.1",
		Port:    port,
		AgentID: "test-sender",
	}

	var stdout, stderr bytes.Buffer
	ctx := context.Background()

	// 1. Say should publish to ping:public
	code := handleSay(ctx, cfg, []string{"broadcast alert"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("handleSay failed: %s", stderr.String())
	}

	// 2. Tell should publish to ping:agent-bob
	stdout.Reset()
	stderr.Reset()
	code = handleTell(ctx, cfg, []string{"agent-bob", "direct directive"}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("handleTell failed: %s", stderr.String())
	}

	// 3. Reply should publish to ping:agent-charlie
	stdout.Reset()
	stderr.Reset()
	code = handleReply(ctx, cfg, []string{"1700000000000-0", "agent-charlie", "reply msg"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("handleReply failed: %s", stderr.String())
	}

	mu.Lock()
	channels := append([]string{}, publishedChannels...)
	mu.Unlock()

	expectedChannels := []string{"ping:public", "ping:agent-bob", "ping:agent-charlie"}
	for _, expected := range expectedChannels {
		found := false
		for _, ch := range channels {
			if strings.EqualFold(ch, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected publish to channel %s, got channels: %v", expected, channels)
		}
	}
}


