// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boggycreek/sandbox/pkg/config"
)

type fleetMockHTTPClient struct {
	doFunc func(req *http.Request) (*http.Response, error)
}

func (m *fleetMockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return m.doFunc(req)
}

func TestFleetExtraCoverage(t *testing.T) {
	t.Setenv("SONAR_DISABLED", "1")
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	paths := config.GetPaths()
	_ = paths.EnsureDirectories()
	_ = os.WriteFile(paths.EnvFile, []byte("BP_HOST=127.0.0.1\nBP_PORT=6379\nFORGEJO_ADMIN_PASS=pass\n"), 0600)

	// 1. Infra edge cases
	code, _, errOut := runFleet([]string{"infra"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage error on bare 'fleet infra', got code %d: %s", code, errOut)
	}

	// 2. Project edge cases
	code, _, errOut = runFleet([]string{"project"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage error on bare 'fleet project', got code %d: %s", code, errOut)
	}

	mockGitea := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "nonexistent") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message": "not found"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id": 1, "name": "proj-one", "full_name": "factory/proj-one"}]`))
	}))
	defer mockGitea.Close()

	mockSonar := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"UP"}`))
	}))
	defer mockSonar.Close()

	t.Setenv("GITEA_URL", mockGitea.URL)
	t.Setenv("FORGEJO_URL", mockGitea.URL)
	t.Setenv("SONAR_HOST_URL", mockSonar.URL)

	// Project list without --json
	code, out, _ := runFleet([]string{"project", "list"})
	if code != 0 {
		t.Errorf("expected project list (table) to succeed, got %d: %s", code, out)
	}

	// Create project for doctor
	code, _, errOut = runFleet([]string{"project", "create", "factory/proj-one"})
	if code != 0 {
		t.Errorf("expected project create to succeed, got %d: %s", code, errOut)
	}

	// Project doctor on specific project
	code, _, errOut = runFleet([]string{"project", "doctor", "proj-one", "--org", "factory"})
	if code != 0 {
		t.Errorf("expected project doctor on valid project to return 0, got %d: %s", code, errOut)
	}
	code, _, errOut = runFleet([]string{"project", "doctor", "nonexistent", "--org", "factory"})
	if code != 1 {
		t.Errorf("expected project doctor on nonexistent project to return 1, got %d: %s", code, errOut)
	}

	// Project link missing arg
	code, _, errOut = runFleet([]string{"project", "link"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage error for project link with no args, got code %d: %s", code, errOut)
	}

	// 3. Agent edge cases & subcommand validations
	// Create a host agent and a container agent
	hostCfg := &config.AgentConfig{
		Name:          "cov-host-agent",
		Role:          "coder",
		Runtime:       "host-container",
		WorkspacePath: filepath.Join(tmpDir, "host-ws"),
	}
	_ = config.SaveAgentConfig(hostCfg, paths)

	ctrCfg := &config.AgentConfig{
		Name:          "cov-ctr-agent",
		Role:          "tester",
		Runtime:       "container",
		ContainerName: "cov-ctr-agent-cnt",
		VolumeName:    "cov-ctr-agent-vol",
		SigningKeyPEM: "mock-signing-key",
		PublicKeyB64:  "mock-pubkey-base64",
	}
	_ = config.SaveAgentConfig(ctrCfg, paths)

	// Mock podman to emulate inspect and port for list
	restoreExec := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) >= 2 && args[0] == "port" {
			return exec.Command("echo", "127.0.0.1:2222")
		}
		if name == "podman" && len(args) >= 3 && args[0] == "container" && args[1] == "inspect" {
			return exec.Command("echo", "true")
		}
		if name == "podman" && len(args) >= 6 && args[len(args)-1] == "--is-inside-work-tree" {
			return exec.Command("false")
		}
		if name == "podman" && len(args) >= 6 && strings.Contains(args[len(args)-1], "find . -maxdepth 2") {
			return exec.Command("echo", "./subrepo/.git")
		}
		return exec.Command("true")
	})
	defer restoreExec()

	// Agent list with host-container and container agents
	_, _, _ = runFleet([]string{"agent", "list"})
	_, _, _ = runFleet([]string{"agent", "list", "--json"})

	// Agent clone validations
	code, _, errOut = runFleet([]string{"agent", "clone"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage error on bare 'agent clone', got code %d: %s", code, errOut)
	}
	code, _, errOut = runFleet([]string{"agent", "clone", "cov-host-agent", "http://repo.git"})
	if code != 1 || !strings.Contains(errOut, "is a host agent") {
		t.Errorf("expected host agent error on 'agent clone', got code %d: %s", code, errOut)
	}
	code, _, errOut = runFleet([]string{"agent", "clone", "cov-ctr-agent", "http://repo.git", "../escaped"})
	if code != 1 || !strings.Contains(errOut, "target directory must remain within") {
		t.Errorf("expected directory traversal error on 'agent clone', got code %d: %s", code, errOut)
	}

	// Agent remote validations
	code, _, errOut = runFleet([]string{"agent", "remote"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage error on bare 'agent remote', got code %d: %s", code, errOut)
	}
	code, _, errOut = runFleet([]string{"agent", "remote", "cov-host-agent", "list"})
	if code != 1 || !strings.Contains(errOut, "is a host agent") {
		t.Errorf("expected host agent error on 'agent remote', got code %d: %s", code, errOut)
	}
	code, _, errOut = runFleet([]string{"agent", "remote", "cov-ctr-agent", "set"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage error on 'agent remote set', got code %d: %s", code, errOut)
	}
	code, _, errOut = runFleet([]string{"agent", "remote", "cov-ctr-agent", "set", "origin"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage error on 'agent remote set' with missing url, got code %d: %s", code, errOut)
	}
	code, _, errOut = runFleet([]string{"agent", "remote", "cov-ctr-agent", "--dir", "../escaped", "list"})
	if code != 1 || !strings.Contains(errOut, "target directory must remain within") {
		t.Errorf("expected directory traversal error on 'agent remote', got code %d: %s", code, errOut)
	}
	code, out, errOut = runFleet([]string{"agent", "remote", "cov-ctr-agent", "--dir=subpath", "list"})
	if code != 0 {
		t.Errorf("expected agent remote with --dir=subpath to succeed, got %d: %s", code, errOut)
	}

	// Agent doctor validations
	code, _, errOut = runFleet([]string{"agent", "doctor"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage error on bare 'agent doctor'")
	}
	code, out, errOut = runFleet([]string{"agent", "doctor", "nonexistent"})
	if code != 1 {
		t.Errorf("expected code 1 on agent doctor nonexistent, got %d (out: %s, err: %s)", code, out, errOut)
	}

	// Agent clean validations
	code, _, errOut = runFleet([]string{"agent", "clean"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage error on bare 'agent clean'")
	}
	code, _, errOut = runFleet([]string{"agent", "clean", "nonexistent"})
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("expected not found error on agent clean nonexistent")
	}

	// Agent retire validations
	code, _, errOut = runFleet([]string{"agent", "retire"})
	if code != 1 || !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected usage error on bare 'agent retire'")
	}
	code, _, errOut = runFleet([]string{"agent", "retire", "nonexistent"})
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("expected not found error on agent retire nonexistent")
	}

	// Agent ssh-config nonexistent
	code, _, errOut = runFleet([]string{"agent", "ssh-config", "nonexistent"})
	if code != 1 || !strings.Contains(errOut, "not found") {
		t.Errorf("expected not found error on agent ssh-config nonexistent")
	}

	// Agent keys on agent with generated keys
	code, out, _ = runFleet([]string{"agent", "keys", "cov-ctr-agent"})
	if code != 0 || !strings.Contains(out, "Public Key:") {
		t.Errorf("expected agent keys to display public key, got %d: %s", code, out)
	}

	// 4. Update with existing git repo and prebuilt release download mock
	repoDir := filepath.Join(tmpDir, "repo")
	_ = os.MkdirAll(filepath.Join(repoDir, ".git"), 0755)
	pUpdate := paths
	pUpdate.DataHome = tmpDir

	// Mock httpClientForDownload for prebuilt release binaries download
	oldHTTPClient := httpClientForDownload
	defer func() { httpClientForDownload = oldHTTPClient }()

	httpClientForDownload = &fleetMockHTTPClient{
		doFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString("prebuilt-binary-payload")),
			}, nil
		},
	}

	binDir := filepath.Join(tmpDir, "bin")
	_ = os.MkdirAll(binDir, 0755)
	var stdoutBuf, stderrBuf strings.Builder
	bgCtx := context.Background()
	err := installOrBuildBinaries(bgCtx, repoDir, binDir, &stdoutBuf, &stderrBuf)
	if err != nil {
		t.Fatalf("expected installOrBuildBinaries to succeed via download mock: %v", err)
	}
	if !strings.Contains(stdoutBuf.String(), "Installed prebuilt native CLI binaries") {
		t.Errorf("expected prebuilt download success message, got: %s", stdoutBuf.String())
	}

	// Update command execution with repo existing
	var updateOut, updateErr strings.Builder
	updateCode := handleUpdate(bgCtx, pUpdate, nil, &updateOut, &updateErr)
	if updateCode != 0 {
		t.Logf("handleUpdate code: %d, out: %s, err: %s", updateCode, updateOut.String(), updateErr.String())
	}
}
