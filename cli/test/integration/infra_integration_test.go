// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package integration

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
	"github.com/boggycreek/fleet/cli/test/harness"
)

func TestInfraLifecycleIntegration(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not installed or not in PATH; skipping infra lifecycle integration test")
	}

	infra := harness.StartEphemeralInfraHarness(t)
	// harness.StartEphemeralInfraHarness registers t.Cleanup(infra.Teardown)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. Verify Forgejo is online and fleet org exists
	t.Log("Verifying Forgejo REST API...")
	forgejoUserURL := fmt.Sprintf("http://127.0.0.1:%d/api/v1/orgs/fleet", infra.ForgejoHTTPPort)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, forgejoUserURL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("expected Forgejo fleet organization to exist (status: %v, err: %v)", resp.StatusCode, err)
	}
	if resp != nil {
		_ = resp.Body.Close()
	}

	// 2. Verify Valkey is reachable and pingable
	t.Log("Verifying Valkey backplane connection...")
	vClient, err := libbp.Dial(ctx, libbp.ClientConfig{
		Host:     "127.0.0.1",
		Port:     infra.ValkeyPort,
		Username: "admin",
		Password: infra.AdminPassword,
	})
	if err != nil {
		t.Fatalf("failed connecting to ephemeral Valkey: %v", err)
	}
	defer vClient.Close()

	// 3. Create agent in isolated environment and verify Forgejo & Valkey provisioning
	t.Log("Creating agent and testing Valkey & Forgejo user provisioning...")
	out, err := infra.ExecSndbx(ctx, "agent", "create", "infra-test-agent", "as", "base", "--role", "tester")
	if err != nil {
		t.Fatalf("'sndbx agent create' failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "created successfully") {
		t.Errorf("unexpected agent create output: %s", out)
	}

	// Verify agent user exists in Forgejo
	userURL := fmt.Sprintf("http://127.0.0.1:%d/api/v1/users/infra-test-agent", infra.ForgejoHTTPPort)
	reqUser, _ := http.NewRequestWithContext(ctx, http.MethodGet, userURL, nil)
	respUser, err := http.DefaultClient.Do(reqUser)
	if err != nil || respUser.StatusCode != http.StatusOK {
		t.Errorf("expected Forgejo user infra-test-agent to exist (status: %v, err: %v)", respUser.StatusCode, err)
	}
	if respUser != nil {
		_ = respUser.Body.Close()
	}

	// Verify agent identity in Valkey
	idRecord, err := vClient.GetIdentity(ctx, "infra-test-agent")
	if err != nil || idRecord.Name != "infra-test-agent" {
		t.Errorf("expected Valkey identity for infra-test-agent, got %+v (err: %v)", idRecord, err)
	}

	// 4. Test sndbx agent doctor on created agent
	t.Log("Testing 'sndbx agent doctor' on provisioned agent...")
	docOut, err := infra.ExecSndbx(ctx, "agent", "doctor", "infra-test-agent")
	if err != nil {
		t.Fatalf("'sndbx agent doctor' failed: %v\nOutput: %s", err, docOut)
	}
	if !strings.Contains(docOut, "Diagnosing agent \"infra-test-agent\"") {
		t.Errorf("unexpected doctor output: %s", docOut)
	}

	// 5. Test sndbx agent retire
	t.Log("Retiring agent via 'sndbx agent retire'...")
	retireOut, err := infra.ExecSndbx(ctx, "agent", "retire", "infra-test-agent")
	if err != nil {
		t.Fatalf("'sndbx agent retire' failed: %v\nOutput: %s", err, retireOut)
	}
	if !strings.Contains(retireOut, "retired and deprovisioned successfully") {
		t.Errorf("unexpected retire output: %s", retireOut)
	}

	// Verify agent was purged from Forgejo (404)
	reqAfter, _ := http.NewRequestWithContext(ctx, http.MethodGet, userURL, nil)
	respAfter, err := http.DefaultClient.Do(reqAfter)
	if err != nil || respAfter.StatusCode != http.StatusNotFound {
		t.Errorf("expected Forgejo user to be 404 after retire, got status %v (err: %v)", respAfter.StatusCode, err)
	}
	if respAfter != nil {
		_ = respAfter.Body.Close()
	}
}
