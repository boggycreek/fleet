// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package devcontainer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDevContainer(t *testing.T) {
	t.Run("Valid Clean JSON", func(t *testing.T) {
		jsonStr := `{
			"name": "Go Dev Container",
			"image": "mcr.microsoft.com/devcontainers/go:1.21",
			"containerUser": "vscode",
			"remoteUser": "vscode"
		}`

		spec, err := ParseDevContainer([]byte(jsonStr))
		if err != nil {
			t.Fatalf("unexpected error parsing valid json: %v", err)
		}
		if spec.Name != "Go Dev Container" {
			t.Errorf("expected name 'Go Dev Container', got %q", spec.Name)
		}
		if spec.Image != "mcr.microsoft.com/devcontainers/go:1.21" {
			t.Errorf("expected image 'mcr.microsoft.com/devcontainers/go:1.21', got %q", spec.Image)
		}
	})

	t.Run("JSON with Comments and Trailing Commas", func(t *testing.T) {
		jsonCStr := `{
			// Single line comment
			"name": "Commented Spec", /* Multi-line
			comment */
			"containerUser": "agent",
			"capAdd": [
				"SYS_PTRACE", // trailing comma next line
			],
			"runArgs": [
				"--cap-add=SYS_ADMIN",
			],
		}`

		spec, err := ParseDevContainer([]byte(jsonCStr))
		if err != nil {
			t.Fatalf("unexpected error parsing JSONC: %v", err)
		}
		if spec.Name != "Commented Spec" {
			t.Errorf("expected name 'Commented Spec', got %q", spec.Name)
		}
		if len(spec.CapAdd) != 1 || spec.CapAdd[0] != "SYS_PTRACE" {
			t.Errorf("unexpected capAdd: %v", spec.CapAdd)
		}
	})

	t.Run("Malformed JSON", func(t *testing.T) {
		malformed := `{
			"name": "Invalid Spec",
			"image": "foo",,,
		}`

		_, err := ParseDevContainer([]byte(malformed))
		if err == nil {
			t.Fatal("expected error parsing malformed json, got nil")
		}
	})

	t.Run("ParseFromFile", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "devcontainer.json")
		content := `{"name": "File Test", "containerUser": "agent"}`
		if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}

		spec, err := ParseDevContainerFile(filePath)
		if err != nil {
			t.Fatalf("failed to parse file: %v", err)
		}
		if spec.Name != "File Test" {
			t.Errorf("expected name 'File Test', got %q", spec.Name)
		}

		_, err = ParseDevContainerFile(filepath.Join(tmpDir, "nonexistent.json"))
		if err == nil {
			t.Fatal("expected error reading nonexistent file, got nil")
		}
	})
}

func TestAudit(t *testing.T) {
	t.Run("Nil Spec", func(t *testing.T) {
		report := Audit(nil)
		if !report.HasCritical() {
			t.Error("expected report to have critical diagnostic for nil spec")
		}
	})

	t.Run("Clean Spec", func(t *testing.T) {
		spec := &DevContainerSpec{
			Name:          "Clean Spec",
			Image:         "golang:1.21",
			ContainerUser: "agent",
			RemoteUser:    "agent",
			CapAdd:        []string{"NET_BIND_SERVICE"},
			RunArgs:       []string{"--read-only"},
			Mounts: []any{
				"source=/tmp/data,target=/data,type=bind",
				map[string]any{"source": "/var/app", "target": "/app"},
			},
		}

		report := Audit(spec)
		if report.HasCritical() {
			t.Errorf("expected clean spec to have no critical findings, got: %v", report.Diagnostics)
		}
	})

	t.Run("Dangerous Spec Audit", func(t *testing.T) {
		trueVal := true
		spec := &DevContainerSpec{
			Name:          "Dangerous Spec",
			Privileged:    &trueVal,
			ContainerUser: "root",
			RemoteUser:    "0",
			CapAdd:        []string{"SYS_ADMIN", "CAP_NET_ADMIN"},
			RunArgs: []string{
				"--privileged",
				"--net=host",
				"--ipc", "host",
				"--pid=host",
				"--userns=host",
				"--cap-add=SYS_PTRACE",
				"--cap-add", "ALL",
				"-v=/var/run/docker.sock:/var/run/docker.sock",
				"--volume", "/root:/root",
				"--mount", "source=~/.ssh,target=/home/agent/.ssh",
			},
			Mounts: []any{
				"source=/var/run/podman/podman.sock,target=/var/run/podman/podman.sock",
				map[string]any{"src": "/etc", "target": "/host-etc"},
			},
		}

		report := Audit(spec)
		if !report.HasCritical() {
			t.Fatal("expected dangerous spec to produce critical diagnostics")
		}

		codes := make(map[string]bool)
		for _, d := range report.Diagnostics {
			codes[d.Code] = true
		}

		expectedCodes := []string{
			"PRIVILEGED_MODE",
			"PRIVILEGED_RUN_ARG",
			"DANGEROUS_CAPABILITY",
			"HOST_NAMESPACE_LEAKAGE",
			"PROHIBITED_HOST_BIND",
			"ROOT_CONTAINER_USER",
			"ROOT_REMOTE_USER",
		}

		for _, code := range expectedCodes {
			if !codes[code] {
				t.Errorf("expected diagnostic code %s in report", code)
			}
		}
	})
}

func TestSanitize(t *testing.T) {
	t.Run("Nil Spec", func(t *testing.T) {
		sanitized, report := Sanitize(nil)
		if sanitized != nil {
			t.Error("expected nil sanitized spec for nil input")
		}
		if !report.HasCritical() {
			t.Error("expected critical diagnostic for nil spec")
		}
	})

	t.Run("Dangerous Spec Sanitization", func(t *testing.T) {
		trueVal := true
		spec := &DevContainerSpec{
			Name:          "Dangerous Spec",
			Privileged:    &trueVal,
			ContainerUser: "root",
			RemoteUser:    "",
			CapAdd:        []string{"SYS_ADMIN", "CHOWN"},
			RunArgs: []string{
				"--privileged",
				"--net=host",
				"--ipc", "host",
				"--cap-add=SYS_PTRACE",
				"--cap-add", "SYS_ADMIN",
				"--cap-add", "CHOWN",
				"-v=/var/run/docker.sock:/var/run/docker.sock",
				"-v=/safe/path:/safe/path",
				"--volume", "/etc:/etc",
				"--mount=source=$HOME/.ssh,target=/root/.ssh",
				"--security-opt", "no-new-privileges",
			},
			Mounts: []any{
				"source=/var/run/podman/podman.sock,target=/var/run/podman/podman.sock",
				map[string]any{"source": "/home/user/project", "target": "/workspace"},
			},
			Features: map[string]any{
				"ghcr.io/devcontainers/features/go:1": map[string]any{"version": "1.21"},
			},
			Customizations: map[string]any{
				"vscode": map[string]any{"extensions": []string{"golang.Go"}},
			},
		}

		sanitized, report := Sanitize(spec)
		if sanitized == nil {
			t.Fatal("expected non-nil sanitized spec")
		}

		// Verify user normalization
		if sanitized.ContainerUser != DefaultUnprivilegedUser {
			t.Errorf("expected containerUser to be normalized to 'agent', got %q", sanitized.ContainerUser)
		}
		if sanitized.RemoteUser != DefaultUnprivilegedUser {
			t.Errorf("expected remoteUser to be normalized to 'agent', got %q", sanitized.RemoteUser)
		}

		// Verify privileged mode stripped
		if sanitized.Privileged != nil {
			t.Errorf("expected Privileged to be nil, got %v", *sanitized.Privileged)
		}

		// Verify CapAdd stripped
		if len(sanitized.CapAdd) != 1 || sanitized.CapAdd[0] != "CHOWN" {
			t.Errorf("expected CapAdd to contain only ['CHOWN'], got %v", sanitized.CapAdd)
		}

		// Verify Mounts stripped
		if len(sanitized.Mounts) != 1 {
			t.Errorf("expected 1 safe mount, got %d: %v", len(sanitized.Mounts), sanitized.Mounts)
		}

		// Verify RunArgs stripped
		for _, arg := range sanitized.RunArgs {
			if arg == "--privileged" || arg == "--net=host" || arg == "--ipc" || arg == "host" || strings.Contains(arg, "SYS_ADMIN") || strings.Contains(arg, "docker.sock") || strings.Contains(arg, "/etc") {
				t.Errorf("unexpected unsafe arg in sanitized runArgs: %s", arg)
			}
		}

		// Check features and customizations retained
		if len(sanitized.Features) != 1 {
			t.Errorf("expected features to be retained, got: %v", sanitized.Features)
		}
		if len(sanitized.Customizations) != 1 {
			t.Errorf("expected customizations to be retained, got: %v", sanitized.Customizations)
		}

		// Verify that auditing sanitized output produces no Critical findings
		sanitizedReport := Audit(sanitized)
		if sanitizedReport.HasCritical() {
			t.Errorf("sanitized spec still contains critical findings: %v", sanitizedReport.Diagnostics)
		}

		if len(report.Diagnostics) == 0 {
			t.Error("expected sanitization diagnostic report entries")
		}
	})
}
