// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
	"github.com/boggycreek/sandbox/backplane/test/harness"
)

func TestBackplaneEndToEndIntegration(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 1. Initialize Clients
	agent1, err := valkey.ClientFor("agent-1")
	if err != nil {
		t.Fatalf("failed connecting agent-1: %v", err)
	}
	defer agent1.Close()

	agent2, err := valkey.ClientFor("agent-2")
	if err != nil {
		t.Fatalf("failed connecting agent-2: %v", err)
	}
	defer agent2.Close()

	operator, err := valkey.ClientFor("operator")
	if err != nil {
		t.Fatalf("failed connecting operator: %v", err)
	}
	defer operator.Close()

	// Set human name in Valkey
	if _, err := operator.Exec(ctx, "SET", libbp.KeyHumanName, "operator"); err != nil {
		t.Fatalf("failed setting human name: %v", err)
	}

	// 2. Broadcast Announcement (agent-1 say)
	sayMsg, err := agent1.Say(ctx, "agent-1 is online and ready for tasks", "")
	if err != nil {
		t.Fatalf("agent-1 Say error: %v", err)
	}
	if sayMsg.Sender != "agent-1" || sayMsg.Citation != "agent-1#1" {
		t.Errorf("unexpected say message citation: %s", sayMsg.Citation)
	}

	// 3. Point-to-Point Task Assignment (operator -> agent-1 tell)
	assignMsg, err := operator.Tell(ctx, "agent-1", "Please implement the database gateway")
	if err != nil {
		t.Fatalf("operator Tell error: %v", err)
	}
	if assignMsg.Destination != "agent-1" {
		t.Errorf("unexpected destination: %s", assignMsg.Destination)
	}

	// 4. Threaded Direct Reply (agent-1 -> operator reply)
	replyMsg, err := agent1.Reply(ctx, assignMsg.ID, "operator", "Understood, starting database gateway implementation")
	if err != nil {
		t.Fatalf("agent-1 Reply error: %v", err)
	}
	if replyMsg.ReplyTo != assignMsg.ID {
		t.Errorf("unexpected reply-to ID: %s", replyMsg.ReplyTo)
	}

	// 5. Ingestion (agent-1 recv)
	recvMsgs, err := agent1.Recv(ctx, 0)
	if err != nil {
		t.Fatalf("agent-1 Recv error: %v", err)
	}
	if len(recvMsgs) == 0 {
		t.Fatalf("expected agent-1 to receive messages, got 0")
	}

	foundAssignment := false
	for _, m := range recvMsgs {
		if strings.Contains(m.Content, "database gateway") {
			foundAssignment = true
			break
		}
	}
	if !foundAssignment {
		t.Errorf("expected to find assignment message in agent-1 inbox")
	}

	// 6. Peers Discovery
	peers, err := agent1.Peers(ctx)
	if err != nil {
		t.Fatalf("Peers error: %v", err)
	}
	if len(peers) == 0 {
		t.Errorf("expected to discover peers, got 0")
	}

	// 7. Human Broadcast Stream
	_, err = operator.Say(ctx, "Operator global broadcast: team sync in 10 mins", "")
	if err != nil {
		t.Fatalf("operator broadcast error: %v", err)
	}

	humanMsgs, err := agent2.Human(ctx, 10)
	if err != nil {
		t.Fatalf("agent2 Human error: %v", err)
	}
	if len(humanMsgs) == 0 {
		t.Errorf("expected human broadcast messages, got 0")
	}
}

func TestValkeyACLEnforcement(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	agent1, err := valkey.ClientFor("agent-1")
	if err != nil {
		t.Fatalf("failed connecting agent-1: %v", err)
	}
	defer agent1.Close()

	// 1. Forbidden Write to Peer Broadcast Outbox (agent-1 cannot write to agent-2:out)
	_, err = agent1.Exec(ctx, "XADD", "agent-2:out", "*", "content", "unauthorized injection")
	if err == nil {
		t.Errorf("SECURITY FAILURE: agent-1 was unexpectedly allowed to write to agent-2:out")
	} else if !strings.Contains(strings.ToUpper(err.Error()), "NOPERM") && !strings.Contains(err.Error(), "permission") {
		t.Logf("Write to peer outbox correctly rejected: %v", err)
	}

	// 2. Forbidden Write to Admin keys
	_, err = agent1.Exec(ctx, "SET", "admin:key", "val")
	if err == nil {
		t.Errorf("SECURITY FAILURE: agent-1 was unexpectedly allowed to write to admin:key")
	}

	// 3. Allowed Append to Peer Inbox via ACL Selector
	val, err := agent1.Exec(ctx, "XADD", "agent-2:inbox", "*", "sender", "agent-1", "content", "authorized delivery")
	if err != nil {
		t.Errorf("agent-1 should be allowed to XADD to agent-2:inbox via selector, got: %v", err)
	}
	if val.IsNull {
		t.Errorf("expected XADD message id, got nil")
	}
}

func TestClientGuardrailEnforcement(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	agent1, err := valkey.ClientFor("agent-1")
	if err != nil {
		t.Fatalf("failed connecting agent-1: %v", err)
	}
	defer agent1.Close()

	// Guardrail should intercept and reject destructive MAXLEN against peer inbox BEFORE reaching server
	_, err = agent1.Exec(ctx, "XADD", "agent-2:inbox", "MAXLEN", "1", "*", "content", "attempted truncate")
	if err == nil {
		t.Errorf("GUARDRAIL FAILURE: client guard did not reject MAXLEN on peer inbox")
	} else if !strings.Contains(err.Error(), "destructive action") {
		t.Errorf("expected destructive action error, got: %v", err)
	}
}

func TestCryptographicSigningIntegration(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Setup keypair for agent-1
	priv, pub, err := libbp.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair error: %v", err)
	}
	pubB64 := libbp.EncodePublicKeyBase64(pub)

	agent1, err := libbp.Dial(ctx, libbp.ClientConfig{
		Host:       "127.0.0.1",
		Port:       valkey.Port,
		Username:   "agent-1",
		Password:   valkey.Agent1Pass,
		AgentID:    "agent-1",
		SigningKey: priv,
	})
	if err != nil {
		t.Fatalf("agent1 Dial error: %v", err)
	}
	defer agent1.Close()

	agent2, err := valkey.ClientFor("agent-2")
	if err != nil {
		t.Fatalf("agent2 ClientFor error: %v", err)
	}
	defer agent2.Close()

	// Publish identity record with public key
	rec := libbp.IdentityRecord{
		Name:   "agent-1",
		Role:   "backend-coder",
		Kind:   "agent",
		PubKey: pubB64,
	}
	if err := agent1.RegisterIdentity(ctx, rec); err != nil {
		t.Fatalf("RegisterIdentity error: %v", err)
	}

	// Send signed message from agent-1 to agent-2
	signedMsg, err := agent1.Tell(ctx, "agent-2", "Cryptographically signed instruction")
	if err != nil {
		t.Fatalf("Tell error: %v", err)
	}
	if !signedMsg.IsSigned || signedMsg.Signature == "" {
		t.Fatalf("expected message to be signed")
	}

	// Agent-2 reads and verifies message
	recvMsgs, err := agent2.Recv(ctx, 0)
	if err != nil {
		t.Fatalf("agent2 Recv error: %v", err)
	}

	var targetMsg *libbp.Message
	for _, m := range recvMsgs {
		if m.ID == signedMsg.ID {
			targetMsg = m
			break
		}
	}

	if targetMsg == nil {
		t.Fatalf("agent-2 did not receive signed message")
	}

	if !agent2.VerifyMessage(ctx, targetMsg, false) {
		t.Errorf("VerifyMessage failed to verify valid cryptographic signature")
	}

	// Tampered content test
	targetMsg.Content = "Forged / altered instruction"
	if agent2.VerifyMessage(ctx, targetMsg, false) {
		t.Errorf("SECURITY FAILURE: VerifyMessage verified tampered content")
	}
}

func TestBpCLISubprocess(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	tmpDir := t.TempDir()
	bpBin := filepath.Join(tmpDir, "bp")

	// Compile bp binary
	buildCmd := exec.Command("go", "build", "-o", bpBin, "../../../backplane/cmd/bp")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed building bp binary: %v (output: %s)", err, string(out))
	}

	runBP := func(env []string, args ...string) (string, error) {
		cmd := exec.Command(bpBin, args...)
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	agent1Env := []string{
		"BP_HOST=127.0.0.1",
		"BP_PORT=" + strconv.Itoa(valkey.Port),
		"BP_AGENT=agent-1",
		"BP_PASSWORD=" + valkey.Agent1Pass,
	}

	agent2Env := []string{
		"BP_HOST=127.0.0.1",
		"BP_PORT=" + strconv.Itoa(valkey.Port),
		"BP_AGENT=agent-2",
		"BP_PASSWORD=" + valkey.Agent2Pass,
	}

	// 1. bp say
	out, err := runBP(agent1Env, "say", "CLI broadcast test message")
	if err != nil {
		t.Fatalf("bp say failed: %v (output: %s)", err, out)
	}
	if !strings.Contains(out, "CLI broadcast test message") {
		t.Errorf("unexpected bp say output: %s", out)
	}

	// 2. bp status set
	out, err = runBP(agent1Env, "status", "set", "busy on feature-1")
	if err != nil {
		t.Fatalf("bp status set failed: %v (output: %s)", err, out)
	}

	// 3. bp tell
	out, err = runBP(agent1Env, "tell", "agent-2", "Direct task message from CLI")
	if err != nil {
		t.Fatalf("bp tell failed: %v (output: %s)", err, out)
	}
	if !strings.Contains(out, "Direct task message from CLI") {
		t.Errorf("unexpected bp tell output: %s", out)
	}

	// 4. bp recv (from agent-2)
	out, err = runBP(agent2Env, "recv")
	if err != nil {
		t.Fatalf("bp recv failed: %v (output: %s)", err, out)
	}
	if !strings.Contains(out, "Direct task message from CLI") {
		t.Errorf("bp recv did not receive message: %s", out)
	}

	// 5. bp peers
	out, err = runBP(agent1Env, "peers")
	if err != nil {
		t.Fatalf("bp peers failed: %v (output: %s)", err, out)
	}
	if !strings.Contains(out, "agent-1") {
		t.Errorf("bp peers did not list agent-1: %s", out)
	}
}
