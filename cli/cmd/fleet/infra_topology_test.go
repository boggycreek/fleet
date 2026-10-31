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
)

func TestHandleInfraTopology_ListAndTable(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{DataHome: t.TempDir()}

	// 1. Bare `fleet infra topology`
	var stdout, stderr bytes.Buffer
	code := handleInfra(ctx, paths, []string{"topology"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Available 128k KV Cache Topology Presets") || !strings.Contains(out, "swarm") || !strings.Contains(out, "deep-context") {
		t.Errorf("expected table output, got: %s", out)
	}

	// 2. `fleet infra topology list`
	stdout.Reset()
	stderr.Reset()
	code = handleInfra(ctx, paths, []string{"topology", "list"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "Available 128k KV Cache Topology Presets") {
		t.Errorf("expected table on 'topology list', got: %s", stdout.String())
	}

	// 3. `fleet infra topology list --json`
	stdout.Reset()
	stderr.Reset()
	code = handleInfra(ctx, paths, []string{"topology", "list", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	var presets []config.TopologyPreset
	if err := json.Unmarshal(stdout.Bytes(), &presets); err != nil {
		t.Fatalf("failed decoding JSON: %v, raw: %s", err, stdout.String())
	}
	if len(presets) != 2 {
		t.Errorf("expected 2 presets in JSON, got %d", len(presets))
	}

	// 4. `fleet infra topology --json`
	stdout.Reset()
	stderr.Reset()
	code = handleInfra(ctx, paths, []string{"topology", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	presets = nil
	if err := json.Unmarshal(stdout.Bytes(), &presets); err != nil {
		t.Fatalf("failed decoding JSON: %v, raw: %s", err, stdout.String())
	}
	if len(presets) != 2 {
		t.Errorf("expected 2 presets in JSON, got %d", len(presets))
	}
}

func TestHandleInfraTopology_Help(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{DataHome: t.TempDir()}

	for _, flag := range []string{"help", "-h", "--help"} {
		var stdout, stderr bytes.Buffer
		code := handleInfra(ctx, paths, []string{"topology", flag}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("expected code 0 for flag %s, got %d", flag, code)
		}
		if !strings.Contains(stdout.String(), "Usage:") || !strings.Contains(stdout.String(), "infra topology") {
			t.Errorf("expected usage output for %s, got: %s", flag, stdout.String())
		}
	}
}

func TestHandleInfraTopology_Show(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{DataHome: t.TempDir()}

	// 1. Show swarm
	var stdout, stderr bytes.Buffer
	code := handleInfra(ctx, paths, []string{"topology", "show", "swarm"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d. stderr: %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Topology Preset: swarm (topology-a)") || !strings.Contains(out, "-np 4 -c 131072 -ctk q8_0 -ctv q8_0") {
		t.Errorf("expected detailed preset output, got: %s", out)
	}

	// 2. Show deep-context --json
	stdout.Reset()
	stderr.Reset()
	code = handleInfra(ctx, paths, []string{"topology", "show", "deep-context", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	var preset config.TopologyPreset
	if err := json.Unmarshal(stdout.Bytes(), &preset); err != nil {
		t.Fatalf("failed decoding JSON: %v", err)
	}
	if preset.ID != "topology-b" || preset.Slots != 2 {
		t.Errorf("unexpected preset JSON: %+v", preset)
	}

	// 3. Show missing argument
	stdout.Reset()
	stderr.Reset()
	code = handleInfra(ctx, paths, []string{"topology", "show"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("expected code 1 on missing show argument, got code %d, stderr: %s", code, stderr.String())
	}

	// 4. Show nonexistent
	stdout.Reset()
	stderr.Reset()
	code = handleInfra(ctx, paths, []string{"topology", "show", "nonexistent"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "unknown topology preset") {
		t.Errorf("expected error on unknown preset, got: %s", stderr.String())
	}
}

func TestHandleInfraTopology_Args(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{DataHome: t.TempDir()}

	// 1. Args swarm
	var stdout, stderr bytes.Buffer
	code := handleInfra(ctx, paths, []string{"topology", "args", "swarm"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	if strings.TrimSpace(stdout.String()) != "-np 4 -c 131072 -ctk q8_0 -ctv q8_0" {
		t.Errorf("unexpected args output: %q", stdout.String())
	}

	// 2. Args deep
	stdout.Reset()
	stderr.Reset()
	code = handleInfra(ctx, paths, []string{"topology", "args", "deep"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	if strings.TrimSpace(stdout.String()) != "-np 2 -c 131072 -ctk q8_0 -ctv q8_0" {
		t.Errorf("unexpected args output: %q", stdout.String())
	}

	// 3. Args missing argument
	stdout.Reset()
	stderr.Reset()
	code = handleInfra(ctx, paths, []string{"topology", "args"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("expected code 1 on missing args argument, got code %d, stderr: %s", code, stderr.String())
	}

	// 4. Args nonexistent
	stdout.Reset()
	stderr.Reset()
	code = handleInfra(ctx, paths, []string{"topology", "args", "nonexistent"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "unknown topology preset") {
		t.Errorf("expected error on unknown preset, got: %s", stderr.String())
	}
}

func TestHandleInfraTopology_DirectPresetName(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{DataHome: t.TempDir()}

	// 1. Direct name: swarm
	var stdout, stderr bytes.Buffer
	code := handleInfra(ctx, paths, []string{"topology", "swarm"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "Topology Preset: swarm") {
		t.Errorf("expected detail output on direct preset name: %s", stdout.String())
	}

	// 2. Direct name with --json: topology-b --json
	stdout.Reset()
	stderr.Reset()
	code = handleInfra(ctx, paths, []string{"topology", "topology-b", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	var preset config.TopologyPreset
	if err := json.Unmarshal(stdout.Bytes(), &preset); err != nil {
		t.Fatalf("failed decoding JSON: %v", err)
	}
	if preset.ID != "topology-b" {
		t.Errorf("unexpected preset: %+v", preset)
	}

	// 3. Unknown direct name
	stdout.Reset()
	stderr.Reset()
	code = handleInfra(ctx, paths, []string{"topology", "invalid-preset"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "unknown topology preset") {
		t.Errorf("expected error on invalid direct preset, got: %s", stderr.String())
	}
}
