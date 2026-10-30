// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/runtime"
)

func TestHandleInfraInfo_Formatted(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{
		DataHome:  t.TempDir(),
		StateHome: t.TempDir(),
	}

	t.Setenv("ADMIN_BACKPLANE_PASSWORD", "secretadminpass")
	t.Setenv("HUMAN_BACKPLANE_PASSWORD", "secrethumanpass")
	t.Setenv("HUMAN_NAME", "operator")

	// 1. Default (masked)
	var stdout, stderr bytes.Buffer
	code := handleInfra(ctx, paths, []string{"info"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "[valkey]") || !strings.Contains(out, "[forgejo]") ||
		!strings.Contains(out, "[postgresql]") || !strings.Contains(out, "[sonarqube]") {
		t.Errorf("missing headers in infra info output:\n%s", out)
	}
	if strings.Contains(out, "secretadminpass") || strings.Contains(out, "secrethumanpass") {
		t.Errorf("default output leaked secret passwords:\n%s", out)
	}
	if !strings.Contains(out, "operator_password:  ********") {
		t.Errorf("expected masked operator_password in output:\n%s", out)
	}

	// 2. Show secrets with --show-secrets
	stdout.Reset()
	stderr.Reset()
	code = handleInfra(ctx, paths, []string{"info", "--show-secrets"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0 with --show-secrets, got %d", code)
	}
	outSecrets := stdout.String()
	if !strings.Contains(outSecrets, "secrethumanpass") || !strings.Contains(outSecrets, "secretadminpass") {
		t.Errorf("expected secrets to be revealed in output:\n%s", outSecrets)
	}

	// 3. Show secrets with -s shorthand
	stdout.Reset()
	stderr.Reset()
	code = handleInfra(ctx, paths, []string{"info", "-s"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0 with -s, got %d", code)
	}
	if !strings.Contains(stdout.String(), "secrethumanpass") {
		t.Errorf("expected secrets revealed with -s flag")
	}
}

func TestHandleInfraInfo_JSON(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{
		DataHome:  t.TempDir(),
		StateHome: t.TempDir(),
	}

	t.Setenv("ADMIN_BACKPLANE_PASSWORD", "secretadminpass")
	t.Setenv("HUMAN_BACKPLANE_PASSWORD", "secrethumanpass")

	// 1. JSON output masked
	var stdout, stderr bytes.Buffer
	code := handleInfra(ctx, paths, []string{"info", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}

	var info runtime.InfraStackInfo
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		t.Fatalf("failed to decode JSON output: %v\nOutput: %s", err, stdout.String())
	}
	if info.Valkey == nil || info.Valkey.OperatorPassword != "********" {
		t.Errorf("expected masked valkey password in JSON, got: %+v", info.Valkey)
	}
	if info.Forgejo == nil || info.Forgejo.AdminPassword != "********" {
		t.Errorf("expected masked forgejo password in JSON, got: %+v", info.Forgejo)
	}
	if info.Gitea == nil || info.Gitea.AdminPassword != "********" {
		t.Errorf("expected masked gitea alias password in JSON, got: %+v", info.Gitea)
	}

	// 2. JSON output with secrets
	stdout.Reset()
	stderr.Reset()
	code = handleInfra(ctx, paths, []string{"info", "--json", "--show-secrets"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}

	var infoRevealed runtime.InfraStackInfo
	if err := json.Unmarshal(stdout.Bytes(), &infoRevealed); err != nil {
		t.Fatalf("failed to decode revealed JSON: %v", err)
	}
	if infoRevealed.Valkey.OperatorPassword != "secrethumanpass" {
		t.Errorf("expected revealed password in JSON, got: %s", infoRevealed.Valkey.OperatorPassword)
	}
}

func TestHandleInfraInfo_ErrorsAndUsage(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{
		DataHome:  t.TempDir(),
		StateHome: t.TempDir(),
	}

	var stdout, stderr bytes.Buffer
	code := handleInfra(ctx, paths, []string{"info", "--invalid-flag"}, &stdout, &stderr)
	if code != 1 {
		t.Errorf("expected exit code 1 on unknown flag, got %d", code)
	}
	if !strings.Contains(stderr.String(), "unknown flag") {
		t.Errorf("expected unknown flag error message, got: %s", stderr.String())
	}
}
