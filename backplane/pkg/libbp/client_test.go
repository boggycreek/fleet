// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package libbp

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/boggycreek/sandbox/backplane/pkg/libbp/resp"
)

// mockValkeyServer provides an in-memory RESP TCP server for unit testing
type mockValkeyServer struct {
	listener net.Listener
	mu       sync.Mutex
	data     map[string]string
	hashes   map[string]map[string]string
	streams  map[string][]map[string]string
	sets     map[string]map[string]struct{}
	seqs     map[string]int64
	closed   bool
	authFail bool
	t        *testing.T
}

func startMockValkeyServer(t *testing.T) (*mockValkeyServer, int) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed starting mock server: %v", err)
	}

	srv := &mockValkeyServer{
		listener: listener,
		data:     make(map[string]string),
		hashes:   make(map[string]map[string]string),
		streams:  make(map[string][]map[string]string),
		sets:     make(map[string]map[string]struct{}),
		seqs:     make(map[string]int64),
		t:        t,
	}

	go srv.serve()
	port := listener.Addr().(*net.TCPAddr).Port
	return srv, port
}

func (s *mockValkeyServer) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.listener != nil {
		_ = s.listener.Close()
	}
}

func (s *mockValkeyServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *mockValkeyServer) handleConn(conn net.Conn) {
	defer conn.Close()
	r := resp.NewReader(conn)
	w := resp.NewWriter(conn)

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
		s.mu.Lock()

		switch cmd {
		case "AUTH":
			if s.authFail {
				_ = w.WriteValue(resp.Value{Type: resp.TypeError, Str: "ERR invalid password"})
			} else {
				_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "OK"})
			}

		case "PING":
			_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "PONG"})

		case "INCR":
			key := args[1].String()
			s.seqs[key]++
			_ = w.WriteValue(resp.Value{Type: resp.TypeInteger, Num: s.seqs[key]})

		case "SET":
			key := args[1].String()
			val := args[2].String()
			s.data[key] = val
			_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "OK"})

		case "GET":
			key := args[1].String()
			if v, ok := s.data[key]; ok {
				_ = w.WriteValue(resp.Value{Type: resp.TypeBulkString, Str: v})
			} else {
				_ = w.WriteValue(resp.Value{Type: resp.TypeBulkString, IsNull: true})
			}

		case "HSET":
			key := args[1].String()
			if s.hashes[key] == nil {
				s.hashes[key] = make(map[string]string)
			}
			for i := 2; i < len(args); i += 2 {
				if i+1 < len(args) {
					s.hashes[key][args[i].String()] = args[i+1].String()
				}
			}
			_ = w.WriteValue(resp.Value{Type: resp.TypeInteger, Num: 1})

		case "HGET":
			key := args[1].String()
			field := args[2].String()
			if m, ok := s.hashes[key]; ok {
				if v, ok := m[field]; ok {
					_ = w.WriteValue(resp.Value{Type: resp.TypeBulkString, Str: v})
				} else {
					_ = w.WriteValue(resp.Value{Type: resp.TypeBulkString, IsNull: true})
				}
			} else {
				_ = w.WriteValue(resp.Value{Type: resp.TypeBulkString, IsNull: true})
			}

		case "HGETALL":
			key := args[1].String()
			m := s.hashes[key]
			var arr []resp.Value
			for k, v := range m {
				arr = append(arr, resp.Value{Type: resp.TypeBulkString, Str: k}, resp.Value{Type: resp.TypeBulkString, Str: v})
			}
			_ = w.WriteValue(resp.Value{Type: resp.TypeArray, Array: arr})

		case "XADD":
			streamKey := args[1].String()
			fields := make(map[string]string)
			for i := 3; i < len(args); i += 2 {
				if i+1 < len(args) {
					fields[args[i].String()] = args[i+1].String()
				}
			}
			msgID := fmt.Sprintf("%d-0", time.Now().UnixMilli())
			fields["_id"] = msgID
			s.streams[streamKey] = append(s.streams[streamKey], fields)
			_ = w.WriteValue(resp.Value{Type: resp.TypeBulkString, Str: msgID})

		case "XREVRANGE":
			streamKey := args[1].String()
			stream := s.streams[streamKey]
			var arr []resp.Value
			for i := len(stream) - 1; i >= 0; i-- {
				item := stream[i]
				var fieldKVs []resp.Value
				for k, v := range item {
					if k != "_id" {
						fieldKVs = append(fieldKVs, resp.Value{Type: resp.TypeBulkString, Str: k}, resp.Value{Type: resp.TypeBulkString, Str: v})
					}
				}
				entry := resp.Value{
					Type: resp.TypeArray,
					Array: []resp.Value{
						{Type: resp.TypeBulkString, Str: item["_id"]},
						{Type: resp.TypeArray, Array: fieldKVs},
					},
				}
				arr = append(arr, entry)
			}
			_ = w.WriteValue(resp.Value{Type: resp.TypeArray, Array: arr})

		case "SCAN":
			// Return all known *:out stream keys
			var keysArr []resp.Value
			for k := range s.streams {
				if strings.HasSuffix(k, ":out") {
					keysArr = append(keysArr, resp.Value{Type: resp.TypeBulkString, Str: k})
				}
			}
			_ = w.WriteValue(resp.Value{
				Type: resp.TypeArray,
				Array: []resp.Value{
					{Type: resp.TypeBulkString, Str: "0"},
					{Type: resp.TypeArray, Array: keysArr},
				},
			})

		case "SADD":
			key := args[1].String()
			if s.sets[key] == nil {
				s.sets[key] = make(map[string]struct{})
			}
			var added int64
			for i := 2; i < len(args); i++ {
				m := args[i].String()
				if _, exists := s.sets[key][m]; !exists {
					s.sets[key][m] = struct{}{}
					added++
				}
			}
			_ = w.WriteValue(resp.Value{Type: resp.TypeInteger, Num: added})

		case "SREM":
			key := args[1].String()
			var rem int64
			if s.sets[key] != nil {
				for i := 2; i < len(args); i++ {
					m := args[i].String()
					if _, exists := s.sets[key][m]; exists {
						delete(s.sets[key], m)
						rem++
					}
				}
			}
			_ = w.WriteValue(resp.Value{Type: resp.TypeInteger, Num: rem})

		case "SMEMBERS":
			key := args[1].String()
			var arr []resp.Value
			if s.sets[key] != nil {
				for m := range s.sets[key] {
					arr = append(arr, resp.Value{Type: resp.TypeBulkString, Str: m})
				}
			}
			_ = w.WriteValue(resp.Value{Type: resp.TypeArray, Array: arr})

		case "XREAD":
			// Minimal mock XREAD response
			var streamArr []resp.Value
			for streamKey, entries := range s.streams {
				var entryArr []resp.Value
				for _, item := range entries {
					var fieldKVs []resp.Value
					for k, v := range item {
						if k != "_id" {
							fieldKVs = append(fieldKVs, resp.Value{Type: resp.TypeBulkString, Str: k}, resp.Value{Type: resp.TypeBulkString, Str: v})
						}
					}
					entryArr = append(entryArr, resp.Value{
						Type: resp.TypeArray,
						Array: []resp.Value{
							{Type: resp.TypeBulkString, Str: item["_id"]},
							{Type: resp.TypeArray, Array: fieldKVs},
						},
					})
				}
				if len(entryArr) > 0 {
					streamArr = append(streamArr, resp.Value{
						Type: resp.TypeArray,
						Array: []resp.Value{
							{Type: resp.TypeBulkString, Str: streamKey},
							{Type: resp.TypeArray, Array: entryArr},
						},
					})
				}
			}
			_ = w.WriteValue(resp.Value{Type: resp.TypeArray, Array: streamArr})

		default:
			_ = w.WriteValue(resp.Value{Type: resp.TypeSimpleString, Str: "OK"})
		}
		s.mu.Unlock()
	}
}

func TestClientOperations(t *testing.T) {
	srv, port := startMockValkeyServer(t)
	defer srv.close()

	priv, pub, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair error: %v", err)
	}
	pemStr, _ := EncodePrivateKeyPEM(priv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := ClientConfig{
		Host:          "127.0.0.1",
		Port:          port,
		Username:      "agent-1",
		Password:      "test-pw",
		AgentID:       "agent-1",
		SigningKey:    priv,
		SigningKeyPEM: pemStr,
	}

	client, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer client.Close()

	// 1. Identity Registration & Verification
	pubB64 := EncodePublicKeyBase64(pub)
	rec := IdentityRecord{
		Name:   "agent-1",
		Role:   "tester",
		Kind:   "agent",
		PubKey: pubB64,
	}
	if err := client.RegisterIdentity(ctx, rec); err != nil {
		t.Fatalf("RegisterIdentity error: %v", err)
	}

	fetchedRec, err := client.GetIdentity(ctx, "agent-1")
	if err != nil {
		t.Fatalf("GetIdentity error: %v", err)
	}
	if fetchedRec.Role != "tester" {
		t.Errorf("expected tester role, got %s", fetchedRec.Role)
	}

	// 2. Say (Broadcast)
	sayMsg, err := client.Say(ctx, "Broadcasting online status", "")
	if err != nil {
		t.Fatalf("Say error: %v", err)
	}
	if sayMsg.Content != "Broadcasting online status" || sayMsg.Sender != "agent-1" || !sayMsg.IsSigned {
		t.Errorf("unexpected say message: %+v", sayMsg)
	}

	// 3. Tell (Point-to-Point)
	tellMsg, err := client.Tell(ctx, "agent-2", "Direct message to peer")
	if err != nil {
		t.Fatalf("Tell error: %v", err)
	}
	if tellMsg.Destination != "agent-2" || tellMsg.Content != "Direct message to peer" {
		t.Errorf("unexpected tell message: %+v", tellMsg)
	}

	// 3b. TellWithBlob
	tellBlobMsg, err := client.TellWithBlob(ctx, "agent-2", "Direct with blob", "agent-1:blob:123")
	if err != nil {
		t.Fatalf("TellWithBlob error: %v", err)
	}
	if tellBlobMsg.Destination != "agent-2" || tellBlobMsg.BlobPath != "agent-1:blob:123" {
		t.Errorf("unexpected tellBlob message: %+v", tellBlobMsg)
	}

	// 3c. Post broadcast
	postBroadcast, err := client.Post(ctx, "", "Broadcast post summary", []byte("full broadcast payload"))
	if err != nil {
		t.Fatalf("Post broadcast error: %v", err)
	}
	if postBroadcast.Content != "Broadcast post summary" || postBroadcast.BlobPath == "" {
		t.Errorf("unexpected post broadcast message: %+v", postBroadcast)
	}

	// 3d. Post direct to agent
	postDirect, err := client.Post(ctx, "agent-2", "Direct post summary", []byte("full direct payload"))
	if err != nil {
		t.Fatalf("Post direct error: %v", err)
	}
	if postDirect.Destination != "agent-2" || postDirect.Content != "Direct post summary" || postDirect.BlobPath == "" {
		t.Errorf("unexpected post direct message: %+v", postDirect)
	}

	// 4. Reply (Threaded direct message)
	replyMsg, err := client.Reply(ctx, tellMsg.ID, "agent-2", "Reply to direct message")
	if err != nil {
		t.Fatalf("Reply error: %v", err)
	}
	if replyMsg.ReplyTo != tellMsg.ID || replyMsg.Destination != "agent-2" {
		t.Errorf("unexpected reply message: %+v", replyMsg)
	}

	// 5. Status updates
	if err := client.SetStatus(ctx, "executing test"); err != nil {
		t.Fatalf("SetStatus error: %v", err)
	}
	status, err := client.GetStatus(ctx, "agent-1")
	if err != nil || status != "executing test" {
		t.Errorf("GetStatus mismatch: %s (err: %v)", status, err)
	}

	// 6. Finger profile
	fingerMap := map[string]string{"role": "developer", "lang": "go"}
	if err := client.SetFinger(ctx, fingerMap); err != nil {
		t.Fatalf("SetFinger error: %v", err)
	}
	fetchedFinger, err := client.GetFinger(ctx, "agent-1")
	if err != nil || fetchedFinger["role"] != "developer" {
		t.Errorf("GetFinger mismatch: %+v (err: %v)", fetchedFinger, err)
	}

	// 7. Park Blob & Retrieve Blob
	blobData := []byte("large file content dataset")
	blobKey, err := client.ParkBlob(ctx, blobData)
	if err != nil {
		t.Fatalf("ParkBlob error: %v", err)
	}
	fetchedBlob, err := client.GetBlob(ctx, blobKey)
	if err != nil || string(fetchedBlob) != string(blobData) {
		t.Errorf("GetBlob mismatch: %s (err: %v)", string(fetchedBlob), err)
	}

	// 8. Liaison set & get
	if err := client.SetLiaison(ctx, "agent-1"); err != nil {
		t.Fatalf("SetLiaison error: %v", err)
	}
	liaison, err := client.GetLiaison(ctx)
	if err != nil || liaison != "agent-1" {
		t.Errorf("GetLiaison mismatch: %s (err: %v)", liaison, err)
	}

	// 9. Peers discovery
	peers, err := client.Peers(ctx)
	if err != nil {
		t.Fatalf("Peers error: %v", err)
	}
	if len(peers) == 0 {
		t.Errorf("expected at least 1 peer, got 0")
	}

	// 10. Human broadcast log
	// Populate human name and message
	_, _ = client.Exec(ctx, "SET", KeyHumanName, "operator")
	humanClient, _ := Dial(ctx, ClientConfig{Host: "127.0.0.1", Port: port, Username: "operator", AgentID: "operator"})
	if humanClient != nil {
		_, _ = humanClient.Say(ctx, "Human broadcast announcement", "")
		humanClient.Close()
	}

	humanMsgs, err := client.Human(ctx, 10)
	if err != nil {
		t.Fatalf("Human error: %v", err)
	}
	if len(humanMsgs) == 0 {
		t.Errorf("expected messages from Human(), got 0")
	}

	// 11. Recv messages
	recvMsgs, err := client.Recv(ctx, 1)
	if err != nil {
		t.Fatalf("Recv error: %v", err)
	}
	if len(recvMsgs) == 0 {
		t.Errorf("expected messages from Recv, got 0")
	}

	// 12. Verify message signature checks
	for _, m := range recvMsgs {
		if m.Sender == "agent-1" && m.IsSigned {
			if !client.VerifyMessage(ctx, m) {
				t.Errorf("failed verifying valid message signature: %+v", m)
			}
		}
	}

	// Verify nil / unsigned message cases
	if client.VerifyMessage(ctx, nil) {
		t.Errorf("expected false for nil message verification")
	}
	if client.VerifyMessage(ctx, &Message{}) {
		t.Errorf("expected false for unsigned message verification")
	}
	if client.VerifyMessage(ctx, &Message{Sender: "unknown-agent", IsSigned: true, Signature: "dGVzdA==", Timestamp: time.Now().UnixMilli()}) {
		t.Errorf("expected false for unknown agent key verification")
	}

	// Double close test
	if err := client.Close(); err != nil {
		t.Errorf("unexpected close error: %v", err)
	}
	if _, err := client.Exec(ctx, "PING"); err == nil {
		t.Errorf("expected error executing on closed client, got nil")
	}
}

func TestClientDialErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Connection refused
	if _, err := Dial(ctx, ClientConfig{Host: "127.0.0.1", Port: 64999}); err == nil {
		t.Errorf("expected connection error to unused port, got nil")
	}

	// Auth failure
	srv, port := startMockValkeyServer(t)
	srv.authFail = true
	defer srv.close()

	if _, err := Dial(ctx, ClientConfig{Host: "127.0.0.1", Port: port, Password: "bad-password"}); err == nil {
		t.Errorf("expected auth error, got nil")
	}
}

func TestLoadClientFromEnv(t *testing.T) {
	os.Setenv("BP_HOST", "127.0.0.1")
	os.Setenv("BP_PORT", "6380")
	os.Setenv("BP_MODE", "agent")
	os.Setenv("BP_AGENT", "agent-env-test")
	os.Setenv("BP_PASSWORD", "secret123")
	os.Setenv("BP_SIGNING_KEY_PEM", "")

	cfg := LoadClientFromEnv()
	if cfg.Host != "127.0.0.1" || cfg.Port != 6380 || cfg.AgentID != "agent-env-test" || cfg.Password != "secret123" {
		t.Errorf("unexpected LoadClientFromEnv for agent: %+v", cfg)
	}

	os.Setenv("BP_MODE", "human")
	os.Setenv("HUMAN_NAME", "admin-user")
	os.Setenv("HUMAN_BACKPLANE_PASSWORD", "human-pass")
	cfgHuman := LoadClientFromEnv()
	if cfgHuman.AgentID != "admin-user" || cfgHuman.Password != "human-pass" {
		t.Errorf("unexpected LoadClientFromEnv for human: %+v", cfgHuman)
	}

	// Test fallback when HUMAN_NAME is empty
	os.Unsetenv("HUMAN_NAME")
	cfgFallback := LoadClientFromEnv()
	if cfgFallback.Username != "operator" {
		t.Errorf("expected fallback to operator, got %s", cfgFallback.Username)
	}

	// Test BP_SIGNING_KEY file path in LoadClientFromEnv
	tmpDir := t.TempDir()
	keyFile := fmt.Sprintf("%s/key.pem", tmpDir)
	priv, _, _ := GenerateKeypair()
	pemStr, _ := EncodePrivateKeyPEM(priv)
	_ = os.WriteFile(keyFile, []byte(pemStr), 0600)
	os.Setenv("BP_SIGNING_KEY", keyFile)
	cfgKeyPath := LoadClientFromEnv()
	if cfgKeyPath.SigningKey == nil {
		t.Errorf("expected signing key to load from BP_SIGNING_KEY file path")
	}

	// Test fallback to AGENT_NAME, AGENT_PASSWORD, and default port 6379
	os.Setenv("BP_MODE", "agent")
	os.Setenv("BP_PORT", "not-a-number")
	os.Setenv("BP_AGENT", "")
	os.Setenv("AGENT_NAME", "agent-fallback-id")
	os.Setenv("BP_PASSWORD", "")
	os.Setenv("AGENT_PASSWORD", "agent-fallback-pw")
	os.Unsetenv("BP_SIGNING_KEY")
	os.Unsetenv("BP_SIGNING_KEY_PEM")
	cfgFallbackAgent := LoadClientFromEnv()
	if cfgFallbackAgent.AgentID != "agent-fallback-id" || cfgFallbackAgent.Password != "agent-fallback-pw" || cfgFallbackAgent.Port != 6379 {
		t.Errorf("unexpected LoadClientFromEnv agent fallback: %+v", cfgFallbackAgent)
	}

	// Test Fail-Closed on conflicting BP_MODE=human and BP_AGENT
	os.Setenv("BP_MODE", "human")
	os.Setenv("BP_AGENT", "conflicting-agent")
	os.Setenv("HUMAN_NAME", "admin-user")
	if _, err := LoadClientWithProfile("default"); err == nil {
		t.Errorf("expected fail-closed error for conflicting BP_MODE=human and BP_AGENT")
	}
	os.Unsetenv("BP_MODE")
	os.Unsetenv("BP_AGENT")

	// Test LoadClientWithProfile from file with quotes
	os.Unsetenv("BP_PORT")
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	profilesDir := filepath.Join(stateDir, "bp", "profiles")
	_ = os.MkdirAll(profilesDir, 0700)
	testProfilePath := filepath.Join(profilesDir, "testprof.env")
	profileEnv := "BP_AGENT=\"profile-agent\"\nBP_PASSWORD='prof-pass'\nBP_PORT=\"6399\"\n"
	_ = os.WriteFile(testProfilePath, []byte(profileEnv), 0600)

	// Even with ambient BP_MODE=human and HUMAN_NAME exported, named profile must NOT be hijacked
	t.Setenv("BP_MODE", "human")
	t.Setenv("HUMAN_NAME", "brian-operator")

	cfgProf, err := LoadClientWithProfile("testprof")
	if err != nil {
		t.Fatalf("LoadClientWithProfile failed: %v", err)
	}
	if cfgProf.AgentID != "profile-agent" || cfgProf.Password != "prof-pass" || cfgProf.Port != 6399 || cfgProf.Mode != "agent" {
		t.Errorf("unexpected profile config (hijacked by ambient env?): %+v", cfgProf)
	}
	os.Unsetenv("BP_MODE")
	os.Unsetenv("HUMAN_NAME")

	// Quoted BP_SIGNING_KEY that does not exist should return error
	testKeyFailPath := filepath.Join(profilesDir, "keyfail.env")
	keyFailEnv := "BP_AGENT=agent-fail\nBP_SIGNING_KEY=\"/nonexistent/key.pem\"\n"
	_ = os.WriteFile(testKeyFailPath, []byte(keyFailEnv), 0600)
	if _, err := LoadClientWithProfile("keyfail"); err == nil {
		t.Errorf("expected error for nonexistent BP_SIGNING_KEY in profile")
	}

	// Missing profile error
	if _, err := LoadClientWithProfile("nonexistent-profile"); err == nil {
		t.Errorf("expected error for missing non-default profile")
	}
}

func TestClientEdgeCases(t *testing.T) {
	srv, port := startMockValkeyServer(t)
	defer srv.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Dial with password only (no username)
	clientPwOnly, err := Dial(ctx, ClientConfig{Host: "127.0.0.1", Port: port, Password: "test"})
	if err != nil {
		t.Fatalf("Dial with password only failed: %v", err)
	}
	clientPwOnly.Close()

	// Dial with defaults (Host="", Port=0)
	// (Will fail to connect to 6379 unless running, so we test validation path)
	_, _ = Dial(ctx, ClientConfig{Host: "", Port: 0, ConnectTimeout: 10 * time.Millisecond})

	client, err := Dial(ctx, ClientConfig{Host: "127.0.0.1", Port: port, AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	defer client.Close()

	// Missing blob
	if _, err := client.GetBlob(ctx, "nonexistent:blob:0"); err == nil {
		t.Errorf("expected error for missing blob, got nil")
	}

	// Missing status
	status, err := client.GetStatus(ctx, "nonexistent-agent")
	if err != nil || status != "" {
		t.Errorf("expected empty status, got %s (err: %v)", status, err)
	}

	// Missing identity
	if _, err := client.GetIdentity(ctx, "nonexistent"); err == nil {
		t.Errorf("expected error getting missing identity, got nil")
	}

	// Cursor defaults
	cur, err := client.GetCursor(ctx, "nonexistent-stream")
	if err != nil || cur != "0-0" {
		t.Errorf("expected default cursor 0-0, got %s", cur)
	}

	// Empty liaison
	_, _ = client.Exec(ctx, "SET", KeyLiaisonCurrent, "")
	liaison, err := client.GetLiaison(ctx)
	if err != nil || liaison != "" {
		t.Errorf("expected empty liaison, got %s", liaison)
	}

	// Guard against dangerous commands in Exec
	if _, err := client.Exec(ctx, "SHUTDOWN"); err == nil {
		t.Errorf("expected error from dangerous SHUTDOWN command")
	}

	// VerifyMessage with cached public key and corrupt signature
	priv, pub, _ := GenerateKeypair()
	pubB64 := EncodePublicKeyBase64(pub)
	_ = client.RegisterIdentity(ctx, IdentityRecord{Name: "agent-1", PubKey: pubB64})
	msg := &Message{
		Sender:    "agent-1",
		Content:   "signed message",
		Timestamp: time.Now().UnixMilli(),
		Seq:       1,
		IsSigned:  true,
	}
	msg.Signature = SignPayload(priv, msg.SigningPayload())
	if !client.VerifyMessage(ctx, msg) {
		t.Errorf("expected VerifyMessage to succeed for signed message")
	}

	// Corrupted signature
	msg.Signature = "AAAA"
	if client.VerifyMessage(ctx, msg) {
		t.Errorf("expected VerifyMessage to fail for bad signature")
	}
}

func TestDialWithSigningKeyPEM(t *testing.T) {
	srv, port := startMockValkeyServer(t)
	defer srv.close()

	priv, _, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair failed: %v", err)
	}
	pemStr, err := EncodePrivateKeyPEM(priv)
	if err != nil {
		t.Fatalf("EncodePrivateKeyPEM failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, ClientConfig{
		Host:          "127.0.0.1",
		Port:          port,
		Username:      "agent-1",
		SigningKeyPEM: pemStr,
	})
	if err != nil {
		t.Fatalf("Dial with SigningKeyPEM failed: %v", err)
	}
	defer client.Close()

	if client.cfg.SigningKey == nil {
		t.Errorf("expected SigningKey to be populated from SigningKeyPEM")
	}
}

func TestClientClosedOperations(t *testing.T) {
	srv, port := startMockValkeyServer(t)
	defer srv.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, ClientConfig{
		Host:     "127.0.0.1",
		Port:     port,
		Username: "agent-1",
		AgentID:  "agent-1",
	})
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}

	// Close client
	if err := client.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// All client operations should return error when client is closed
	if _, err := client.NextSeq(ctx); err == nil {
		t.Errorf("expected error on NextSeq after Close")
	}
	if _, err := client.Say(ctx, "test", ""); err == nil {
		t.Errorf("expected error on Say after Close")
	}
	if _, err := client.Tell(ctx, "agent-2", "test"); err == nil {
		t.Errorf("expected error on Tell after Close")
	}
	if _, err := client.TellWithBlob(ctx, "agent-2", "test", ""); err == nil {
		t.Errorf("expected error on TellWithBlob after Close")
	}
	if _, err := client.Post(ctx, "agent-2", "test", []byte("data")); err == nil {
		t.Errorf("expected error on Post after Close")
	}
	if _, err := client.Reply(ctx, "100-0", "agent-2", "test"); err == nil {
		t.Errorf("expected error on Reply after Close")
	}
	if _, err := client.Recv(ctx, 0); err == nil {
		t.Errorf("expected error on Recv after Close")
	}
	if _, err := client.Human(ctx, 0); err == nil {
		t.Errorf("expected error on Human after Close")
	}
	if _, err := client.Peers(ctx); err == nil {
		t.Errorf("expected error on Peers after Close")
	}
	if err := client.SetStatus(ctx, "idle"); err == nil {
		t.Errorf("expected error on SetStatus after Close")
	}
	if err := client.SetFinger(ctx, map[string]string{"k": "v"}); err == nil {
		t.Errorf("expected error on SetFinger after Close")
	}
	if _, err := client.GetFinger(ctx, "agent-1"); err == nil {
		t.Errorf("expected error on GetFinger after Close")
	}
	if _, err := client.ParkBlob(ctx, []byte("data")); err == nil {
		t.Errorf("expected error on ParkBlob after Close")
	}
	if err := client.SetLiaison(ctx, "agent-1"); err == nil {
		t.Errorf("expected error on SetLiaison after Close")
	}
	if err := client.RegisterIdentity(ctx, IdentityRecord{Name: "agent-1"}); err == nil {
		t.Errorf("expected error on RegisterIdentity after Close")
	}
}

func TestPeersEmpty(t *testing.T) {
	server, port := startMockValkeyServer(t)
	defer server.close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client, err := Dial(ctx, ClientConfig{Host: "127.0.0.1", Port: port, AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer client.Close()

	peers, err := client.Peers(ctx)
	if err != nil {
		t.Fatalf("Peers error: %v", err)
	}
	if peers == nil {
		t.Errorf("expected non-nil empty slice, got nil")
	}
	if len(peers) != 0 {
		t.Errorf("expected len(peers) == 0, got %d", len(peers))
	}
}

func TestVerifyMessageSecurity(t *testing.T) {
	srv, port := startMockValkeyServer(t)
	defer srv.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Dial(ctx, ClientConfig{Host: "127.0.0.1", Port: port, AgentID: "agent-1"})
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer client.Close()

	priv, pub, _ := GenerateKeypair()
	pubB64 := EncodePublicKeyBase64(pub)
	_ = client.RegisterIdentity(ctx, IdentityRecord{Name: "agent-sec", PubKey: pubB64})

	baseMsg := func() *Message {
		return &Message{
			Sender:    "agent-sec",
			Content:   "original",
			Timestamp: time.Now().UnixMilli(),
			Seq:       1,
			IsSigned:  true,
		}
	}

	// 1. Changing m.Seq invalidates the signature
	msgSeq := baseMsg()
	msgSeq.Signature = SignPayload(priv, msgSeq.SigningPayload())
	if !client.VerifyMessage(ctx, msgSeq, false) {
		t.Errorf("expected valid signature")
	}
	msgSeq.Seq = 2
	if client.VerifyMessage(ctx, msgSeq, false) {
		t.Errorf("changing Seq did not invalidate signature")
	}

	// 2. Newlines in Content cannot spoof adjacent fields
	// E.g., someone tries to append fields in Content
	msgSpoof := baseMsg()
	msgSpoof.Content = "hello\n6:blob-2" // attempting to inject BlobPath
	msgSpoof.Signature = SignPayload(priv, msgSpoof.SigningPayload())
	if !client.VerifyMessage(ctx, msgSpoof, false) {
		t.Errorf("expected valid signature on spoof attempt (since payload includes lengths)")
	}
	// Verify that parsing the payload back wouldn't treat the injected blob-2 as real, but since
	// we just test VerifyMessage here, it's sufficient to show lengths prevent ambiguity.
	// We check if altering the BlobPath while keeping Content the same breaks the signature.
	msgSpoof.Content = "hello"
	msgSpoof.BlobPath = "blob-2"
	if client.VerifyMessage(ctx, msgSpoof, false) {
		t.Errorf("expected signature to fail when fields are shifted")
	}

	// 3. Stale or future timestamps are rejected by VerifyMessage
	msgFuture := baseMsg()
	msgFuture.Timestamp = time.Now().UnixMilli() + 600000 // +10 mins
	msgFuture.Signature = SignPayload(priv, msgFuture.SigningPayload())
	if client.VerifyMessage(ctx, msgFuture, false) {
		t.Errorf("expected future timestamp to be rejected")
	}
	if !client.VerifyMessage(ctx, msgFuture, true) { // skipFreshness = true
		t.Errorf("expected future timestamp to be accepted when skipFreshness is true")
	}

	msgStale := baseMsg()
	msgStale.Timestamp = time.Now().UnixMilli() - 90000000 // > 24 hours
	msgStale.Signature = SignPayload(priv, msgStale.SigningPayload())
	if client.VerifyMessage(ctx, msgStale, false) {
		t.Errorf("expected stale timestamp to be rejected")
	}
	if !client.VerifyMessage(ctx, msgStale, true) { // skipFreshness = true
		t.Errorf("expected stale timestamp to be accepted when skipFreshness is true")
	}
}

func TestClientEnsembleOperations(t *testing.T) {
	srv, port := startMockValkeyServer(t)
	defer srv.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	priv, pub, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair error: %v", err)
	}
	pemStr, _ := EncodePrivateKeyPEM(priv)
	pubB64 := EncodePublicKeyBase64(pub)

	cfg := ClientConfig{
		Host:          "127.0.0.1",
		Port:          port,
		AgentID:       "agent-1",
		SigningKeyPEM: pemStr,
	}

	client, err := Dial(ctx, cfg)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	defer client.Close()

	_ = client.RegisterIdentity(ctx, IdentityRecord{Name: "agent-1", PubKey: pubB64})

	// Validation errors
	if err := client.JoinEnsemble(ctx, "Invalid_Name!"); err == nil {
		t.Errorf("expected error joining with invalid name, got nil")
	}
	if err := client.LeaveEnsemble(ctx, "Invalid_Name!"); err == nil {
		t.Errorf("expected error leaving with invalid name, got nil")
	}
	if _, err := client.PublishEnsemble(ctx, "Invalid_Name!", "msg"); err == nil {
		t.Errorf("expected error publishing with invalid name, got nil")
	}
	if _, err := client.ReadEnsembleMessages(ctx, "Invalid_Name!", "0-0", 10); err == nil {
		t.Errorf("expected error reading with invalid name, got nil")
	}

	// Join ensemble
	if err := client.JoinEnsemble(ctx, "team-alpha"); err != nil {
		t.Fatalf("JoinEnsemble failed: %v", err)
	}
	if err := client.JoinEnsemble(ctx, "team-beta"); err != nil {
		t.Fatalf("JoinEnsemble failed: %v", err)
	}

	// List joined
	joined, err := client.ListJoinedEnsembles(ctx)
	if err != nil {
		t.Fatalf("ListJoinedEnsembles failed: %v", err)
	}
	if len(joined) != 2 || joined[0] != "team-alpha" || joined[1] != "team-beta" {
		t.Errorf("unexpected joined ensembles: %v", joined)
	}

	// Publish to ensemble without blob
	msg, err := client.PublishEnsemble(ctx, "team-alpha", "hello team")
	if err != nil {
		t.Fatalf("PublishEnsemble failed: %v", err)
	}
	if msg.Destination != "ensemble:team-alpha" {
		t.Errorf("got destination %q, want 'ensemble:team-alpha'", msg.Destination)
	}

	// Publish to ensemble with blob
	blobKey, err := client.ParkBlob(ctx, []byte("ensemble blob data"))
	if err != nil {
		t.Fatalf("ParkBlob failed: %v", err)
	}
	msgBlob, err := client.PublishEnsemble(ctx, "team-alpha", "hello with blob", blobKey)
	if err != nil {
		t.Fatalf("PublishEnsemble with blob failed: %v", err)
	}
	if msgBlob.BlobPath != blobKey {
		t.Errorf("got blob path %q, want %q", msgBlob.BlobPath, blobKey)
	}

	// Read messages
	msgs, err := client.ReadEnsembleMessages(ctx, "team-alpha", "0-0", 50)
	if err != nil {
		t.Fatalf("ReadEnsembleMessages failed: %v", err)
	}
	if len(msgs) == 0 {
		t.Errorf("expected read messages, got 0")
	}

	// Leave ensemble
	if err := client.LeaveEnsemble(ctx, "team-beta"); err != nil {
		t.Fatalf("LeaveEnsemble failed: %v", err)
	}
	joinedAfter, err := client.ListJoinedEnsembles(ctx)
	if err != nil {
		t.Fatalf("ListJoinedEnsembles after leave failed: %v", err)
	}
	if len(joinedAfter) != 1 || joinedAfter[0] != "team-alpha" {
		t.Errorf("unexpected joined ensembles after leave: %v", joinedAfter)
	}
}

