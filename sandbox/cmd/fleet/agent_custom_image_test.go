// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boggycreek/sandbox/pkg/config"
	"github.com/boggycreek/sandbox/pkg/runtime"
)

func TestFindLocalAgentDockerfile(t *testing.T) {
	tmpDir := t.TempDir()

	// Empty dir
	df, dc := findLocalAgentDockerfile(tmpDir)
	if df != "" || dc != "" {
		t.Errorf("expected empty results for empty dir, got df=%q, dc=%q", df, dc)
	}

	// .fleet/Dockerfile
	fleetDir := filepath.Join(tmpDir, ".fleet")
	_ = os.MkdirAll(fleetDir, 0755)
	fleetDf := filepath.Join(fleetDir, "Dockerfile")
	_ = os.WriteFile(fleetDf, []byte("FROM alpine\n"), 0644)

	df, dc = findLocalAgentDockerfile(tmpDir)
	if df != fleetDf {
		t.Errorf("expected %q, got %q", fleetDf, df)
	}
	_ = os.Remove(fleetDf)

	// .sndbx/Dockerfile
	sndbxDir := filepath.Join(tmpDir, ".sndbx")
	_ = os.MkdirAll(sndbxDir, 0755)
	sndbxDf := filepath.Join(sndbxDir, "Dockerfile")
	_ = os.WriteFile(sndbxDf, []byte("FROM debian\n"), 0644)

	df, dc = findLocalAgentDockerfile(tmpDir)
	if df != sndbxDf {
		t.Errorf("expected %q, got %q", sndbxDf, df)
	}
	_ = os.Remove(sndbxDf)

	// .devcontainer/devcontainer.json
	devDir := filepath.Join(tmpDir, ".devcontainer")
	_ = os.MkdirAll(devDir, 0755)
	devJSON := filepath.Join(devDir, "devcontainer.json")
	_ = os.WriteFile(devJSON, []byte(`{"name":"test"}`), 0644)

	df, dc = findLocalAgentDockerfile(tmpDir)
	if dc != devJSON {
		t.Errorf("expected devcontainer %q, got %q", devJSON, dc)
	}
}

func TestAgentCreate_ExplicitDockerfile(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("BP_PORT", "65530")
	t.Setenv("FORGEJO_URL", "http://127.0.0.1:65531")
	t.Setenv("GITEA_URL", "http://127.0.0.1:65531")
	t.Setenv("SONAR_HOST_URL", "http://127.0.0.1:65532")
	defer func() { _ = os.Setenv("HOME", origHome) }()

	paths := config.GetPaths()
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("failed ensuring directories: %v", err)
	}

	customDf := filepath.Join(tmpDir, "MyCustom.Dockerfile")
	if err := os.WriteFile(customDf, []byte("FROM ubuntu:22.04\nRUN echo hello\n"), 0644); err != nil {
		t.Fatalf("failed writing custom dockerfile: %v", err)
	}

	var podmanBuildCalled bool
	restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 0 && args[0] == "build" {
			podmanBuildCalled = true
			return exec.Command("true")
		}
		return exec.Command("true")
	}))
	defer restore()

	code, stdout, stderr := runFleet([]string{
		"agent", "create", "builder-bot",
		"--dockerfile", customDf,
		"--role", "custom-builder",
	})

	if code != 0 {
		t.Fatalf("agent create with --dockerfile failed with code %d: %s", code, stderr)
	}
	if !podmanBuildCalled {
		t.Error("expected podman build to be invoked")
	}
	if !strings.Contains(stdout, "fleet-custom-builder-bot:latest") {
		t.Errorf("expected output to contain tagged image, got: %s", stdout)
	}

	cfg, err := config.LoadAgentConfig("builder-bot", paths)
	if err != nil {
		t.Fatalf("failed loading agent config: %v", err)
	}
	if cfg.Image != "fleet-custom-builder-bot:latest" {
		t.Errorf("expected config image to be fleet-custom-builder-bot:latest, got: %s", cfg.Image)
	}
}

func TestAgentCreate_DevcontainerSynthesis(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("BP_PORT", "65530")
	t.Setenv("FORGEJO_URL", "http://127.0.0.1:65531")
	t.Setenv("GITEA_URL", "http://127.0.0.1:65531")
	t.Setenv("SONAR_HOST_URL", "http://127.0.0.1:65532")
	defer func() { _ = os.Setenv("HOME", origHome) }()

	paths := config.GetPaths()
	_ = paths.EnsureDirectories()

	// Setup workspace with .devcontainer/devcontainer.json
	wsDir := filepath.Join(tmpDir, "workspace")
	devDir := filepath.Join(wsDir, ".devcontainer")
	_ = os.MkdirAll(devDir, 0755)
	devJSON := filepath.Join(devDir, "devcontainer.json")
	_ = os.WriteFile(devJSON, []byte(`{"name":"Dev Env","image":"golang:1.24"}`), 0644)

	var podmanBuildCalled bool
	restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 0 && args[0] == "build" {
			podmanBuildCalled = true
			return exec.Command("true")
		}
		return exec.Command("true")
	}))
	defer restore()

	code, _, stderr := runFleet([]string{
		"agent", "create", "dev-bot", "as", "devcontainer",
		"--host",
		"--pwd", wsDir,
		"--engine", "claude",
	})

	if code != 0 {
		t.Fatalf("agent create with devcontainer failed with code %d: %s", code, stderr)
	}
	if !podmanBuildCalled {
		t.Error("expected podman build to be invoked for devcontainer")
	}

	cfg, err := config.LoadAgentConfig("dev-bot", paths)
	if err != nil {
		t.Fatalf("failed loading agent config: %v", err)
	}
	if cfg.Image != "fleet-custom-dev-bot:latest" {
		t.Errorf("expected custom tag, got: %s", cfg.Image)
	}
	if cfg.Harness != "claude" {
		t.Errorf("expected harness claude, got: %s", cfg.Harness)
	}
}

func TestAgentCreate_LocalSndbxDockerfile(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("BP_PORT", "65530")
	t.Setenv("FORGEJO_URL", "http://127.0.0.1:65531")
	t.Setenv("GITEA_URL", "http://127.0.0.1:65531")
	t.Setenv("SONAR_HOST_URL", "http://127.0.0.1:65532")
	defer func() { _ = os.Setenv("HOME", origHome) }()

	paths := config.GetPaths()
	_ = paths.EnsureDirectories()

	wsDir := filepath.Join(tmpDir, "sndbx-proj")
	sndbxDir := filepath.Join(wsDir, ".sndbx")
	_ = os.MkdirAll(sndbxDir, 0755)
	_ = os.WriteFile(filepath.Join(sndbxDir, "Dockerfile"), []byte("FROM alpine\n"), 0644)

	var buildArgs []string
	restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 0 && args[0] == "build" {
			buildArgs = args
			return exec.Command("true")
		}
		return exec.Command("true")
	}))
	defer restore()

	code, _, stderr := runFleet([]string{
		"agent", "create", "sndbx-agent", "as", "auto",
		"--host",
		"--pwd", wsDir,
	})

	if code != 0 {
		t.Fatalf("agent create auto failed with code %d: %s", code, stderr)
	}
	if len(buildArgs) == 0 {
		t.Fatal("expected podman build call")
	}
}

func TestResolveAgentImage_CustomCandidates(t *testing.T) {
	ctx := context.Background()

	// Probes fleet-custom-myimage
	restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) >= 3 && args[0] == "image" && args[1] == "exists" {
			if args[2] == "fleet-custom-myimage:latest" {
				return exec.Command("true")
			}
		}
		return exec.Command("false")
	}))
	defer restore()

	img, isLocal := runtime.ResolveAgentImage(ctx, "myimage")
	if img != "fleet-custom-myimage:latest" || !isLocal {
		t.Errorf("expected fleet-custom-myimage:latest (local), got %s (local: %v)", img, isLocal)
	}
}
