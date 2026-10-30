// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/config"
)

func setupMockServersForCLI(t *testing.T) (*httptest.Server, *httptest.Server) {
	forgejoMux := http.NewServeMux()
	sonarMux := http.NewServeMux()

	// Forgejo endpoints
	forgejoMux.HandleFunc("/api/v1/orgs/factory", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1,"username":"factory"}`))
	})
	forgejoMux.HandleFunc("/api/v1/orgs/custom", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":2,"username":"custom"}`))
	})
	forgejoMux.HandleFunc("/api/v1/orgs/factory/repos", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":10,"name":"app"}`))
	})
	forgejoMux.HandleFunc("/api/v1/orgs/custom/repos", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":11,"name":"app"}`))
	})
	forgejoMux.HandleFunc("/api/v1/repos/factory/app", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":10,"name":"app"}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	forgejoMux.HandleFunc("/api/v1/repos/custom/app", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":11,"name":"app"}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	forgejoMux.HandleFunc("/api/v1/repos/factory/fail", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	// SonarQube endpoints
	sonarMux.HandleFunc("/api/projects/create", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("project") == "fail-key" {
			http.Error(w, "create failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	sonarMux.HandleFunc("/api/projects/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("projects")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"components":[{"key":"` + q + `"}]}`))
	})
	sonarMux.HandleFunc("/api/projects/delete", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	sonarMux.HandleFunc("/api/user_tokens/revoke", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	sonarMux.HandleFunc("/api/user_tokens/generate", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"sqp_mock_cli_token"}`))
	})
	sonarMux.HandleFunc("/api/qualitygates/project_status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"projectStatus":{"status":"NONE"}}`))
	})

	fServer := httptest.NewServer(forgejoMux)
	sServer := httptest.NewServer(sonarMux)
	t.Cleanup(func() {
		fServer.Close()
		sServer.Close()
	})
	return fServer, sServer
}

func TestHandleProjectGeneral(t *testing.T) {
	paths := config.Paths{
		DataHome:    t.TempDir(),
		ProjectsDir: filepath.Join(t.TempDir(), "projects"),
	}

	var stdout, stderr bytes.Buffer
	ctx := context.Background()

	// 1. No arguments
	if code := handleProject(ctx, paths, nil, &stdout, &stderr); code != 1 {
		t.Errorf("expected 1 on no args, got %d", code)
	}

	// 2. Help
	stdout.Reset()
	if code := handleProject(ctx, paths, []string{"help"}, &stdout, &stderr); code != 0 {
		t.Errorf("expected 0 on help, got %d", code)
	}
	if !strings.Contains(stdout.String(), "Commands:") {
		t.Errorf("expected help usage output, got %s", stdout.String())
	}

	// 3. Unknown subcommand
	stderr.Reset()
	if code := handleProject(ctx, paths, []string{"invalid"}, &stdout, &stderr); code != 1 {
		t.Errorf("expected 1 on invalid subcommand, got %d", code)
	}
}

func TestHandleProjectCreateAndList(t *testing.T) {
	gServer, sServer := setupMockServersForCLI(t)
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:    tmpDir,
		ProjectsDir: filepath.Join(tmpDir, "projects"),
	}

	t.Setenv("GITEA_URL", gServer.URL)
	t.Setenv("SONAR_HOST_URL", sServer.URL)

	var stdout, stderr bytes.Buffer
	ctx := context.Background()

	// 1. Create with empty args
	if code := handleProject(ctx, paths, []string{"create"}, &stdout, &stderr); code != 1 {
		t.Errorf("expected error on empty create args, got %d", code)
	}

	// 2. Create success
	stdout.Reset()
	stderr.Reset()
	if code := handleProject(ctx, paths, []string{"create", "app", "--description", "App project"}, &stdout, &stderr); code != 0 {
		t.Fatalf("create failed (exit %d): %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Created project factory / app") {
		t.Errorf("unexpected create output: %s", stdout.String())
	}

	// 3. Create with custom org and key
	stdout.Reset()
	stderr.Reset()
	if code := handleProject(ctx, paths, []string{"create", "app", "--org", "custom", "--key", "custom-app", "-d", "Custom App"}, &stdout, &stderr); code != 0 {
		t.Fatalf("custom create failed (exit %d): %s", code, stderr.String())
	}

	// 4. Create error on backend failure
	stderr.Reset()
	if code := handleProject(ctx, paths, []string{"create", "fail", "--key", "fail-key"}, &stdout, &stderr); code != 1 {
		t.Errorf("expected error on backend failure, got %d", code)
	}

	// 5. List text mode
	stdout.Reset()
	if code := handleProject(ctx, paths, []string{"list"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list failed: %s", stderr.String())
	}
	outList := stdout.String()
	if !strings.Contains(outList, "● custom / app") || !strings.Contains(outList, "● factory / app") {
		t.Errorf("unexpected list output:\n%s", outList)
	}

	// 6. List json mode
	stdout.Reset()
	if code := handleProject(ctx, paths, []string{"list", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list --json failed: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), `"sonar_key": "custom-app"`) {
		t.Errorf("expected json output: %s", stdout.String())
	}
}

func TestHandleProjectDoctor(t *testing.T) {
	gServer, sServer := setupMockServersForCLI(t)
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:    tmpDir,
		ProjectsDir: filepath.Join(tmpDir, "projects"),
	}

	t.Setenv("GITEA_URL", gServer.URL)
	t.Setenv("SONAR_HOST_URL", sServer.URL)

	var stdout, stderr bytes.Buffer
	ctx := context.Background()

	// 1. Doctor on empty registered projects
	if code := handleProject(ctx, paths, []string{"doctor"}, &stdout, &stderr); code != 0 {
		t.Errorf("expected 0 on empty doctor, got %d", code)
	}
	if !strings.Contains(stdout.String(), "No projects registered") {
		t.Errorf("expected No projects registered: %s", stdout.String())
	}

	// Create project
	_ = handleProject(ctx, paths, []string{"create", "app"}, &stdout, &stderr)

	// 2. Doctor on existing project by name
	stdout.Reset()
	if code := handleProject(ctx, paths, []string{"doctor", "app"}, &stdout, &stderr); code != 0 {
		t.Errorf("expected 0 on healthy doctor, got %d", code)
	}
	if !strings.Contains(stdout.String(), "Project Diagnostics: factory / app") {
		t.Errorf("unexpected doctor output: %s", stdout.String())
	}

	// 3. Doctor all
	stdout.Reset()
	if code := handleProject(ctx, paths, []string{"doctor"}, &stdout, &stderr); code != 0 {
		t.Errorf("expected 0 on doctor all, got %d", code)
	}

	// 4. Doctor on missing project name
	stderr.Reset()
	if code := handleProject(ctx, paths, []string{"doctor", "missing-target"}, &stdout, &stderr); code != 1 {
		t.Errorf("expected 1 on missing target doctor, got %d", code)
	}
}

func TestHandleProjectLinkAndDelete(t *testing.T) {
	gServer, sServer := setupMockServersForCLI(t)
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:    tmpDir,
		ProjectsDir: filepath.Join(tmpDir, "projects"),
	}

	t.Setenv("GITEA_URL", gServer.URL)
	t.Setenv("SONAR_HOST_URL", sServer.URL)

	var stdout, stderr bytes.Buffer
	ctx := context.Background()

	// 1. Link empty
	if code := handleProject(ctx, paths, []string{"link"}, &stdout, &stderr); code != 1 {
		t.Errorf("expected error on empty link, got %d", code)
	}

	// 2. Link success
	stdout.Reset()
	if code := handleProject(ctx, paths, []string{"link", "factory/app", "factory-app"}, &stdout, &stderr); code != 0 {
		t.Fatalf("link failed: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Linked project factory / app") {
		t.Errorf("unexpected link output: %s", stdout.String())
	}

	// 3. Delete missing name
	if code := handleProject(ctx, paths, []string{"delete"}, &stdout, &stderr); code != 1 {
		t.Errorf("expected error on empty delete, got %d", code)
	}

	// 4. Delete unlinking without force
	stdout.Reset()
	if code := handleProject(ctx, paths, []string{"delete", "app"}, &stdout, &stderr); code != 0 {
		t.Fatalf("delete failed: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "unlinked (metadata removed)") {
		t.Errorf("unexpected delete output: %s", stdout.String())
	}

	// Re-create and delete with --force
	_ = handleProject(ctx, paths, []string{"create", "app"}, &stdout, &stderr)
	stdout.Reset()
	if code := handleProject(ctx, paths, []string{"delete", "app", "--force"}, &stdout, &stderr); code != 0 {
		t.Fatalf("delete --force failed: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "deleted (including remote assets)") {
		t.Errorf("unexpected delete force output: %s", stdout.String())
	}
}

func TestMainRunWithProjectDomain(t *testing.T) {
	gServer, sServer := setupMockServersForCLI(t)
	tmpDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("GITEA_URL", gServer.URL)
	t.Setenv("SONAR_HOST_URL", sServer.URL)

	var stdout, stderr bytes.Buffer
	code := Run([]string{"project", "help"}, &stdout, &stderr)
	if code != 0 {
		t.Errorf("expected 0 for 'fleet project help', got %d", code)
	}
	if !strings.Contains(stdout.String(), "Commands:") {
		t.Errorf("expected usage in output: %s", stdout.String())
	}
}
