// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
	"github.com/boggycreek/fleet/backplane/test/harness"
)

func TestBPIntervalSubcommand(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	os.Setenv("BP_HOST", "127.0.0.1")
	os.Setenv("BP_PORT", fmt.Sprintf("%d", valkey.Port))
	os.Setenv("BP_MODE", "agent")
	os.Setenv("BP_AGENT", "agent-1")
	os.Setenv("BP_PASSWORD", valkey.Agent1Pass)

	// 1. bp interval (default 60)
	code, out, errOut := runCLI([]string{"interval"})
	if code != 0 {
		t.Fatalf("bp interval failed: code=%d err=%s", code, errOut)
	}
	if strings.TrimSpace(out) != "60" {
		t.Errorf("expected default 60, got %q", out)
	}

	// 2. bp interval get
	code, out, errOut = runCLI([]string{"interval", "get"})
	if code != 0 {
		t.Fatalf("bp interval get failed: code=%d err=%s", code, errOut)
	}
	if strings.TrimSpace(out) != "60" {
		t.Errorf("expected 60, got %q", out)
	}

	// 3. bp interval set 120
	code, out, errOut = runCLI([]string{"interval", "set", "120"})
	if code != 0 {
		t.Fatalf("bp interval set 120 failed: code=%d err=%s", code, errOut)
	}
	if !strings.Contains(out, "120") {
		t.Errorf("expected '120' in set output, got %q", out)
	}

	// Verify with bp interval get
	code, out, errOut = runCLI([]string{"interval", "get"})
	if code != 0 || strings.TrimSpace(out) != "120" {
		t.Errorf("expected 120 after setting, got code=%d out=%q", code, out)
	}

	// 4. Boundary minimum: 5
	code, _, errOut = runCLI([]string{"interval", "set", "5"})
	if code != 0 {
		t.Errorf("bp interval set 5 failed: %s", errOut)
	}
	code, out, _ = runCLI([]string{"interval"})
	if code != 0 || strings.TrimSpace(out) != "5" {
		t.Errorf("expected 5 after setting, got %q", out)
	}

	// 5. Boundary maximum: 3600
	code, _, errOut = runCLI([]string{"interval", "set", "3600"})
	if code != 0 {
		t.Errorf("bp interval set 3600 failed: %s", errOut)
	}
	code, out, _ = runCLI([]string{"interval"})
	if code != 0 || strings.TrimSpace(out) != "3600" {
		t.Errorf("expected 3600 after setting, got %q", out)
	}

	// 6. Below minimum: 4
	code, _, errOut = runCLI([]string{"interval", "set", "4"})
	if code != 1 || !strings.Contains(errOut, "between 5 and 3600") {
		t.Errorf("expected error for interval 4, got code=%d err=%q", code, errOut)
	}

	// 7. Above maximum: 3601
	code, _, errOut = runCLI([]string{"interval", "set", "3601"})
	if code != 1 || !strings.Contains(errOut, "between 5 and 3600") {
		t.Errorf("expected error for interval 3601, got code=%d err=%q", code, errOut)
	}

	// 8. Non-integer argument
	code, _, errOut = runCLI([]string{"interval", "set", "not-a-number"})
	if code != 1 || !strings.Contains(errOut, "between 5 and 3600") {
		t.Errorf("expected error for non-integer interval, got code=%d err=%q", code, errOut)
	}

	// 9. Missing argument for set
	code, _, errOut = runCLI([]string{"interval", "set"})
	if code != 1 || !strings.Contains(errOut, "Usage: bp interval set <seconds>") {
		t.Errorf("expected usage error for missing set arg, got code=%d err=%q", code, errOut)
	}

	// 10. Unknown interval subcommand
	code, _, errOut = runCLI([]string{"interval", "unknown-sub"})
	if code != 1 || !strings.Contains(errOut, "Usage: bp interval") {
		t.Errorf("expected usage error for unknown subcommand, got code=%d err=%q", code, errOut)
	}

	// 11. Help mentions interval
	var helpOut bytes.Buffer
	printUsage(&helpOut)
	if !strings.Contains(helpOut.String(), "interval") {
		t.Errorf("expected bp help usage to contain 'interval'")
	}
}

func TestBPIntervalConnectionFailure(t *testing.T) {
	os.Setenv("BP_HOST", "127.0.0.1")
	os.Setenv("BP_PORT", "64999") // Unused port
	os.Setenv("BP_AGENT", "agent-1")
	os.Setenv("BP_PASSWORD", "bad")

	code, _, errOut := runCLI([]string{"interval"})
	if code != 1 || !strings.Contains(errOut, "bp error:") {
		t.Errorf("expected connection failure on bp interval, got code=%d err=%s", code, errOut)
	}

	code, _, errOut = runCLI([]string{"interval", "set", "60"})
	if code != 1 || !strings.Contains(errOut, "bp error:") {
		t.Errorf("expected connection failure on bp interval set, got code=%d err=%s", code, errOut)
	}
}

func TestBPIntervalHandlerErrors(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	defer valkey.Teardown()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled context causes command failure

	cfg := libbp.ClientConfig{
		Host:     "127.0.0.1",
		Port:     valkey.Port,
		Username: "agent-1",
		Password: valkey.Agent1Pass,
		AgentID:  "agent-1",
	}

	var stdout, stderr bytes.Buffer
	code := handleInterval(ctx, cfg, []string{"set", "100"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected code 1 on canceled ctx, got %d", code)
	}
}
