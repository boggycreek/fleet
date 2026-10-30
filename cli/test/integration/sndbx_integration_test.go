// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/fleet/backplane/test/harness"
)

func TestSndbxCLIEndToEnd(t *testing.T) {
	valkey := harness.StartValkeyHarness(t)
	tmpDir := t.TempDir()

	env := []string{
		fmt.Sprintf("XDG_DATA_HOME=%s", tmpDir),
		fmt.Sprintf("XDG_CONFIG_HOME=%s", tmpDir),
		"BP_HOST=127.0.0.1",
		fmt.Sprintf("BP_PORT=%d", valkey.Port),
		fmt.Sprintf("ADMIN_BACKPLANE_PASSWORD=%s", valkey.AdminPass),
		fmt.Sprintf("HUMAN_BACKPLANE_PASSWORD=%s", valkey.HumanPass),
		"HUMAN_NAME=operator",
		"GITEA_URL=http://127.0.0.1:65531",
		"GITEA_DISABLED=1",
		"SONAR_HOST_URL=http://127.0.0.1:65532",
		"SONARQUBE_URL=http://127.0.0.1:65532",
		"SONAR_DISABLED=1",
	}

	// Compile fleet binary to a temporary path
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "fleet")
	cmd := exec.Command("go", "build", "-o", binPath, "github.com/boggycreek/fleet/cli/cmd/fleet")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed building fleet binary: %v (%s)", err, string(out))
	}

	runFleet := func(ctx context.Context, args ...string) (string, error) {
		execCmd := exec.CommandContext(ctx, binPath, args...)
		execCmd.Env = append(os.Environ(), env...)
		res, err := execCmd.CombinedOutput()
		return string(res), err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Create Agent via fleet CLI
	outStr, err := runFleet(ctx, "agent", "create", "test-bot", "as", "base", "--role", "tester")
	if err != nil {
		t.Fatalf("fleet agent create failed: %v (%s)", err, outStr)
	}
	if !strings.Contains(outStr, "Agent \"test-bot\" created successfully") {
		t.Errorf("unexpected create output: %s", outStr)
	}

	// 2. Doctor agent
	docStr, err := runFleet(ctx, "agent", "doctor", "test-bot")
	if err != nil {
		t.Fatalf("fleet agent doctor failed: %v (%s)", err, docStr)
	}
	if !strings.Contains(docStr, "Diagnosing agent \"test-bot\"") {
		t.Errorf("unexpected doctor output: %s", docStr)
	}

	// 3. List agents via fleet CLI
	listStr, err := runFleet(ctx, "agent", "list")
	if err != nil {
		t.Fatalf("fleet agent list failed: %v (%s)", err, listStr)
	}
	if !strings.Contains(listStr, "test-bot") {
		t.Errorf("expected test-bot in agent list: %s", listStr)
	}

	// 4. Retire agent via fleet CLI
	retireStr, err := runFleet(ctx, "agent", "retire", "test-bot", "--force")
	if err != nil {
		t.Fatalf("fleet agent retire failed: %v (%s)", err, retireStr)
	}
	if !strings.Contains(retireStr, "retired and deprovisioned successfully") {
		t.Errorf("unexpected retire output: %s", retireStr)
	}
}
