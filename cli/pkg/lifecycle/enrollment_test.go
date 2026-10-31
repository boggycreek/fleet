// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package lifecycle

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateEnrollmentToken(t *testing.T) {
	tok1, err := GenerateEnrollmentToken()
	if err != nil {
		t.Fatalf("unexpected error generating token: %v", err)
	}
	if len(tok1) != 32 {
		t.Errorf("expected 32 hex chars, got %d (%s)", len(tok1), tok1)
	}

	tok2, err := GenerateEnrollmentToken()
	if err != nil {
		t.Fatalf("unexpected error generating second token: %v", err)
	}
	if tok1 == tok2 {
		t.Errorf("expected tokens to be distinct, got %s twice", tok1)
	}
}

func TestEnrollmentSecretsFormatEnv(t *testing.T) {
	sec := EnrollmentSecrets{
		AgentName:          "dell-bot",
		AgentRole:          "baremetal-worker",
		AgentSigningKeyPEM: "-----BEGIN PRIVATE KEY-----\nMIIEvgIBADANBgkqhkiG9w0BAQEFAASC\n-----END PRIVATE KEY-----",
		ValkeyHost:         "192.168.1.100",
		ValkeyPort:         6379,
		ValkeyPassword:     "secretpass123",
		ForgejoURL:         "http://192.168.1.100:3000",
		OpenAIBaseURL:      "http://192.168.1.100:8080/v1",
		OpenAIModel:            "qwen2.5-coder",
		OpenAIAPIKey:           "sk-local",
		ControllerSSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI controller@fleet",
	}

	envStr := sec.FormatEnv()
	if !strings.Contains(envStr, `AGENT_NAME="dell-bot"`) {
		t.Errorf("expected AGENT_NAME in envStr: %s", envStr)
	}
	if !strings.Contains(envStr, `BP_HOST="192.168.1.100"`) {
		t.Errorf("expected BP_HOST in envStr: %s", envStr)
	}
	if !strings.Contains(envStr, `BP_PORT="6379"`) {
		t.Errorf("expected BP_PORT in envStr: %s", envStr)
	}
	if !strings.Contains(envStr, `VALKEY_ADDR="192.168.1.100:6379"`) {
		t.Errorf("expected VALKEY_ADDR in envStr: %s", envStr)
	}
	if !strings.Contains(envStr, `FORGEJO_URL="http://192.168.1.100:3000"`) {
		t.Errorf("expected FORGEJO_URL in envStr: %s", envStr)
	}
	if !strings.Contains(envStr, `OPENAI_BASE_URL="http://192.168.1.100:8080/v1"`) {
		t.Errorf("expected OPENAI_BASE_URL in envStr: %s", envStr)
	}
	if !strings.Contains(envStr, `BP_SIGNING_KEY_PEM=`) {
		t.Errorf("expected BP_SIGNING_KEY_PEM in envStr: %s", envStr)
	}
	if !strings.Contains(envStr, `BP_SIGNING_KEY_B64=`) {
		t.Errorf("expected BP_SIGNING_KEY_B64 in envStr: %s", envStr)
	}
	if !strings.Contains(envStr, `CONTROLLER_SSH_PUB_KEY="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI controller@fleet"`) {
		t.Errorf("expected CONTROLLER_SSH_PUB_KEY in envStr: %s", envStr)
	}
}

func TestEnrollmentServerLifecycleAndEndpoints(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed creating test listener: %v", err)
	}

	secrets := EnrollmentSecrets{
		AgentName:      "test-baremetal",
		AgentRole:      "node-tester",
		ValkeyHost:     "127.0.0.1",
		ValkeyPort:     6379,
		ValkeyPassword: "testpassword",
		OpenAIBaseURL:  "http://127.0.0.1:8080/v1",
	}

	srv, err := NewEnrollmentServer(EnrollmentConfig{
		AgentName: "test-baremetal",
		Secrets:   secrets,
		Listener:  ln,
		TTL:       5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("failed initializing enrollment server: %v", err)
	}

	srv.Start()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	if srv.Port() <= 0 {
		t.Errorf("expected positive port, got %d", srv.Port())
	}
	if srv.Token() == "" {
		t.Errorf("expected non-empty token")
	}
	if srv.TokenBurned() {
		t.Errorf("expected token not burned initially")
	}

	serverURL := srv.URL("127.0.0.1")
	cmd := srv.InstallCommand("127.0.0.1")
	if !strings.Contains(cmd, "curl -sSL") || !strings.Contains(cmd, srv.Token()) {
		t.Errorf("unexpected install command: %s", cmd)
	}

	client := &http.Client{Timeout: 3 * time.Second}

	// 1. GET /install/test-baremetal
	installURL := srv.URL("127.0.0.1") + "/install/test-baremetal?token=" + srv.Token()
	resp, err := client.Get(installURL)
	if err != nil {
		t.Fatalf("failed GET install script: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK from install endpoint, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), srv.Token()) {
		t.Errorf("expected script to contain token, got: %s", string(body))
	}
	if !strings.Contains(string(body), "test-baremetal") {
		t.Errorf("expected script to contain agent name, got: %s", string(body))
	}

	// GET /install/wrong-agent -> 404
	respWrong, err := client.Get(srv.URL("127.0.0.1") + "/install/unknown-node")
	if err == nil {
		_ = respWrong.Body.Close()
		if respWrong.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404 for wrong agent install, got %d", respWrong.StatusCode)
		}
	}

	// 2. GET /api/v1/enroll/test-baremetal with bad token -> 401
	badTokenURL := srv.URL("127.0.0.1") + "/api/v1/enroll/test-baremetal?token=invalid-token"
	respBad, err := client.Get(badTokenURL)
	if err != nil {
		t.Fatalf("failed GET bad token: %v", err)
	}
	_ = respBad.Body.Close()
	if respBad.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 Unauthorized for bad token, got %d", respBad.StatusCode)
	}

	// 3. GET /api/v1/enroll/wrong-agent -> 404
	respWrongAgent, err := client.Get(srv.URL("127.0.0.1") + "/api/v1/enroll/unknown?token=" + srv.Token())
	if err != nil {
		t.Fatalf("failed GET wrong agent: %v", err)
	}
	_ = respWrongAgent.Body.Close()
	if respWrongAgent.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for wrong agent enroll, got %d", respWrongAgent.StatusCode)
	}

	// 4. Valid enrollment -> 200 OK and delivers secrets
	validURL := srv.URL("127.0.0.1") + "/api/v1/enroll/test-baremetal?token=" + srv.Token()
	respValid, err := client.Get(validURL)
	if err != nil {
		t.Fatalf("failed GET valid enrollment: %v", err)
	}
	validBody, _ := io.ReadAll(respValid.Body)
	_ = respValid.Body.Close()
	if respValid.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for valid enroll, got %d", respValid.StatusCode)
	}
	if !strings.Contains(string(validBody), `AGENT_NAME="test-baremetal"`) {
		t.Errorf("expected AGENT_NAME in response, got: %s", string(validBody))
	}
	if !strings.Contains(string(validBody), `BP_PASSWORD="testpassword"`) {
		t.Errorf("expected BP_PASSWORD in response, got: %s", string(validBody))
	}

	// 5. Verify Wait completes successfully
	if err := srv.Wait(ctx); err != nil {
		t.Fatalf("expected Wait() to succeed after enrollment, got error: %v", err)
	}
	if !srv.TokenBurned() {
		t.Errorf("expected token to be marked burned")
	}

	// Verify Completed() is initially false
	if srv.Completed() {
		t.Errorf("expected Completed() to be false before complete endpoint called")
	}

	// 5a. Complete endpoint for wrong agent -> 404
	respWrongComplete, err := client.Post(srv.URL("127.0.0.1")+"/api/v1/complete/wrong-node", "text/plain", nil)
	if err != nil {
		t.Fatalf("failed POST wrong complete: %v", err)
	}
	_ = respWrongComplete.Body.Close()
	if respWrongComplete.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for wrong agent complete, got %d", respWrongComplete.StatusCode)
	}

	// 5b. Complete endpoint with disallowed method -> 405
	reqDelete, _ := http.NewRequest(http.MethodDelete, srv.URL("127.0.0.1")+"/api/v1/complete/test-baremetal", nil)
	respMethodNotAllowed, err := client.Do(reqDelete)
	if err != nil {
		t.Fatalf("failed DELETE complete: %v", err)
	}
	_ = respMethodNotAllowed.Body.Close()
	if respMethodNotAllowed.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for DELETE complete, got %d", respMethodNotAllowed.StatusCode)
	}

	// 5c. Valid complete call via POST
	respComplete, err := client.Post(srv.URL("127.0.0.1")+"/api/v1/complete/test-baremetal", "text/plain", nil)
	if err != nil {
		t.Fatalf("failed POST complete: %v", err)
	}
	completeBody, _ := io.ReadAll(respComplete.Body)
	_ = respComplete.Body.Close()
	if respComplete.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for complete endpoint, got %d", respComplete.StatusCode)
	}
	if !strings.Contains(string(completeBody), "OK") {
		t.Errorf("expected OK in complete response, got %s", string(completeBody))
	}

	// 5d. Valid complete call via GET
	respCompleteGet, err := client.Get(srv.URL("127.0.0.1") + "/api/v1/complete/test-baremetal")
	if err != nil {
		t.Fatalf("failed GET complete: %v", err)
	}
	_ = respCompleteGet.Body.Close()
	if respCompleteGet.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for GET complete, got %d", respCompleteGet.StatusCode)
	}

	if !srv.Completed() {
		t.Errorf("expected Completed() to be true after complete endpoint call")
	}

	// 5e. Verify WaitForCompletion returns nil cleanly
	if err := srv.WaitForCompletion(ctx); err != nil {
		t.Errorf("expected WaitForCompletion() to return nil, got %v", err)
	}

	// 6. Token burned -> subsequent request returns 410 Gone (or connection closed)
	// (Server may already be shut down or return 410)
	respBurned, err := client.Get(validURL)
	if err == nil {
		_ = respBurned.Body.Close()
		if respBurned.StatusCode != http.StatusGone && respBurned.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected 410 Gone or closed connection for burned token, got %d", respBurned.StatusCode)
		}
	}
	_ = serverURL
}

func TestEnrollmentServerJSONFormat(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed creating test listener: %v", err)
	}

	secrets := EnrollmentSecrets{
		AgentName:      "json-agent",
		AgentRole:      "json-role",
		ValkeyHost:     "10.0.0.1",
		ValkeyPort:     6379,
		ValkeyPassword: "pw",
	}

	srv, err := NewEnrollmentServer(EnrollmentConfig{
		AgentName: "json-agent",
		Token:     "test-token-1234",
		Secrets:   secrets,
		Listener:  ln,
	})
	if err != nil {
		t.Fatalf("failed creating server: %v", err)
	}

	srv.Start()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// Test ?format=json
	urlWithFormat := srv.URL("127.0.0.1") + "/api/v1/enroll/json-agent?token=test-token-1234&format=json"
	req, _ := http.NewRequest(http.MethodGet, urlWithFormat, nil)
	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected application/json content-type, got %s", ct)
	}

	var parsed EnrollmentSecrets
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		t.Fatalf("failed decoding json response: %v", err)
	}
	if parsed.AgentName != "json-agent" || parsed.AgentRole != "json-role" {
		t.Errorf("unexpected parsed secrets: %+v", parsed)
	}
}

func TestEnrollmentServerBinaryServing(t *testing.T) {
	tmpDir := t.TempDir()
	fakeAgent := filepath.Join(tmpDir, "fleet-agent")
	if err := os.WriteFile(fakeAgent, []byte("#!/bin/sh\necho agent"), 0755); err != nil {
		t.Fatalf("failed writing fake agent: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed creating listener: %v", err)
	}

	srv, err := NewEnrollmentServer(EnrollmentConfig{
		AgentName: "bin-agent",
		Token:     "tok",
		BinDirs:   []string{tmpDir},
		Listener:  ln,
	})
	if err != nil {
		t.Fatalf("failed creating server: %v", err)
	}

	srv.Start()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	client := &http.Client{Timeout: 2 * time.Second}

	// 1. Fetch valid hosted binary
	resp, err := client.Get(srv.URL("127.0.0.1") + "/bin/fleet-agent")
	if err != nil {
		t.Fatalf("failed fetching fleet-agent: %v", err)
	}
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK for hosted binary, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(data), "echo agent") {
		t.Errorf("unexpected binary content: %s", string(data))
	}

	// 2. Fetch unhosted binary -> 404
	respUnhosted, err := client.Get(srv.URL("127.0.0.1") + "/bin/bp")
	if err == nil {
		_ = respUnhosted.Body.Close()
		if respUnhosted.StatusCode != http.StatusNotFound {
			t.Errorf("expected 404 for unhosted bp, got %d", respUnhosted.StatusCode)
		}
	}

	// 3. Fetch invalid binary name -> 400
	respInvalid, err := client.Get(srv.URL("127.0.0.1") + "/bin/malicious-script.sh")
	if err == nil {
		_ = respInvalid.Body.Close()
		if respInvalid.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400 for invalid binary name, got %d", respInvalid.StatusCode)
		}
	}

	// 4. Burn token via enrollment, verify binary still downloads successfully (grace period)
	enrollResp, err := client.Get(srv.URL("127.0.0.1") + "/api/v1/enroll/bin-agent?token=tok")
	if err != nil {
		t.Fatalf("failed enroll: %v", err)
	}
	_ = enrollResp.Body.Close()
	if enrollResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 enroll, got %d", enrollResp.StatusCode)
	}
	if !srv.TokenBurned() {
		t.Errorf("expected token to be burned")
	}

	// Verify binary download still succeeds after token is burned
	respAfterBurn, err := client.Get(srv.URL("127.0.0.1") + "/bin/fleet-agent")
	if err != nil {
		t.Fatalf("failed fetching fleet-agent after token burned: %v", err)
	}
	dataAfter, _ := io.ReadAll(respAfterBurn.Body)
	_ = respAfterBurn.Body.Close()
	if respAfterBurn.StatusCode != http.StatusOK || !strings.Contains(string(dataAfter), "echo agent") {
		t.Errorf("expected 200 OK for binary after token burned, got %d", respAfterBurn.StatusCode)
	}
}

func TestEnrollmentServerTTLAndTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed creating listener: %v", err)
	}

	srv, err := NewEnrollmentServer(EnrollmentConfig{
		AgentName: "ttl-agent",
		Token:     "tok",
		TTL:       50 * time.Millisecond,
		Listener:  ln,
	})
	if err != nil {
		t.Fatalf("failed creating server: %v", err)
	}

	srv.Start()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err = srv.Wait(ctx)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error from Wait(), got: %v", err)
	}
}

func TestEnrollmentServerContextCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed creating listener: %v", err)
	}

	srv, err := NewEnrollmentServer(EnrollmentConfig{
		AgentName: "cancel-agent",
		Token:     "tok",
		TTL:       5 * time.Minute,
		Listener:  ln,
	})
	if err != nil {
		t.Fatalf("failed creating server: %v", err)
	}

	srv.Start()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err = srv.Wait(ctx)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}

	errComplete := srv.WaitForCompletion(ctx)
	if errComplete != context.Canceled {
		t.Errorf("expected context.Canceled from WaitForCompletion, got: %v", errComplete)
	}
}

func TestDetectHostLANIPAndRewriteURLForLAN(t *testing.T) {
	// DetectHostLANIP with env override
	t.Setenv("FLEET_CONTROLLER_HOST", "192.168.10.55")
	ip := DetectHostLANIP("")
	if ip != "192.168.10.55" {
		t.Errorf("expected env override IP 192.168.10.55, got %s", ip)
	}

	// Without env override
	t.Setenv("FLEET_CONTROLLER_HOST", "")
	ipAuto := DetectHostLANIP("127.0.0.1")
	if ipAuto == "" {
		t.Errorf("expected non-empty IP from auto-detection")
	}

	// RewriteURLForLAN
	cases := []struct {
		input    string
		lanHost  string
		expected string
	}{
		{"http://localhost:8080/v1", "192.168.1.50", "http://192.168.1.50:8080/v1"},
		{"http://127.0.0.1:3000", "192.168.1.50", "http://192.168.1.50:3000"},
		{"http://llm-gateway:11434/v1", "192.168.1.50", "http://192.168.1.50:11434/v1"},
		{"http://fleet-openrouter:8080/v1", "192.168.1.50", "http://192.168.1.50:8080/v1"},
		{"http://api.openai.com/v1", "192.168.1.50", "http://api.openai.com/v1"},
		{"", "192.168.1.50", ""},
		{"http://localhost:8080", "127.0.0.1", "http://localhost:8080"},
	}

	for _, tc := range cases {
		out := RewriteURLForLAN(tc.input, tc.lanHost)
		if out != tc.expected {
			t.Errorf("RewriteURLForLAN(%q, %q) = %q; expected %q", tc.input, tc.lanHost, out, tc.expected)
		}
	}
}

func TestNewEnrollmentServerValidation(t *testing.T) {
	// Empty name
	_, err := NewEnrollmentServer(EnrollmentConfig{AgentName: ""})
	if err == nil {
		t.Errorf("expected error for empty agent name")
	}

	// Valid creation with defaults
	srv, err := NewEnrollmentServer(EnrollmentConfig{
		AgentName: "alpha",
		Port:      0, // triggers free port selection or default
	})
	if err != nil {
		t.Fatalf("unexpected error creating enrollment server: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	if srv.Port() <= 0 {
		t.Errorf("expected valid port, got %d", srv.Port())
	}

	// Test URL helper branches
	urlEmptyHost := srv.URL("")
	if !strings.Contains(urlEmptyHost, "127.0.0.1") {
		t.Errorf("expected 127.0.0.1 in empty host URL, got %s", urlEmptyHost)
	}

	srvCustomURL, err := NewEnrollmentServer(EnrollmentConfig{
		AgentName: "custom-url-node",
		ServerURL: "http://custom-domain.local:9999",
		Port:      0,
	})
	if err != nil {
		t.Fatalf("unexpected error creating custom URL server: %v", err)
	}
	defer func() { _ = srvCustomURL.Shutdown(context.Background()) }()
	if u := srvCustomURL.URL("10.0.0.1"); u != "http://custom-domain.local:9999" {
		t.Errorf("expected custom ServerURL override, got %s", u)
	}
}

func TestEnrollmentServerMethodNotAllowed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed creating listener: %v", err)
	}

	srv, err := NewEnrollmentServer(EnrollmentConfig{
		AgentName: "post-node",
		Token:     "tok",
		Listener:  ln,
	})
	if err != nil {
		t.Fatalf("failed creating server: %v", err)
	}

	srv.Start()
	defer func() { _ = srv.Shutdown(context.Background()) }()

	client := &http.Client{Timeout: 2 * time.Second}

	// 1. POST /install/post-node -> 405
	resp, err := client.Post(srv.URL("127.0.0.1")+"/install/post-node", "text/plain", strings.NewReader("bad"))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for POST /install/, got %d", resp.StatusCode)
	}

	// 2. POST /api/v1/enroll/post-node -> 405
	resp, err = client.Post(srv.URL("127.0.0.1")+"/api/v1/enroll/post-node?token=tok", "text/plain", strings.NewReader("bad"))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for POST /api/v1/enroll/, got %d", resp.StatusCode)
	}

	// 3. POST /bin/fleet-agent -> 405
	resp, err = client.Post(srv.URL("127.0.0.1")+"/bin/fleet-agent", "text/plain", strings.NewReader("bad"))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for POST /bin/, got %d", resp.StatusCode)
	}
}

func TestLoadDefaultInstallScript(t *testing.T) {
	script := loadDefaultInstallScript()
	if !strings.Contains(script, "fleet-agent") || !strings.Contains(script, "__FLEET_ENROLL_TOKEN__") {
		t.Errorf("unexpected install script content: %s", script)
	}
	if !strings.Contains(script, "read -r ENROLL_TOKEN") {
		t.Errorf("expected script to read ENROLL_TOKEN from stdin")
	}
	if !strings.Contains(script, "/api/v1/complete/") {
		t.Errorf("expected script to notify /api/v1/complete/ endpoint")
	}
}
