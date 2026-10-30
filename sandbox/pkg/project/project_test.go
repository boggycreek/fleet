// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package project

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boggycreek/sandbox/pkg/config"
	"github.com/boggycreek/sandbox/pkg/forgejo"
	"github.com/boggycreek/sandbox/pkg/sonar"
)

func TestNewManagerDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:    tmpDir,
		ProjectsDir: filepath.Join(tmpDir, "projects"),
	}

	t.Setenv("FORGEJO_URL", "http://127.0.0.1:3000")
	t.Setenv("SONAR_HOST_URL", "http://127.0.0.1:9000")
	t.Setenv("ADMIN_BACKPLANE_PASSWORD", "testpass")
	t.Setenv("SONAR_ADMIN_USER", "admin")

	mgr := NewManager(paths)
	if mgr == nil || mgr.forgejoBase != "http://127.0.0.1:3000" || mgr.sonarBase != "http://127.0.0.1:9000" {
		t.Fatalf("unexpected manager defaults: %+v", mgr)
	}
}

func setupMockServers(t *testing.T) (*httptest.Server, *httptest.Server, *forgejo.Client, *sonar.Client) {
	forgejoMux := http.NewServeMux()
	sonarMux := http.NewServeMux()

	// Forgejo endpoints
	forgejoMux.HandleFunc("/api/v1/orgs/factory", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":1,"username":"factory"}`))
	})
	forgejoMux.HandleFunc("/api/v1/orgs/fleet", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":2,"username":"fleet"}`))
	})
	forgejoMux.HandleFunc("/api/v1/orgs/fail-org", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "failed org", http.StatusInternalServerError)
	})
	forgejoMux.HandleFunc("/api/v1/orgs/factory/repos", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":10,"name":"app"}`))
	})
	forgejoMux.HandleFunc("/api/v1/orgs/fleet/repos", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":11,"name":"tools"}`))
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
	forgejoMux.HandleFunc("/api/v1/repos/factory/missing-repo", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	forgejoMux.HandleFunc("/api/v1/repos/factory/app/statuses/sha123", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":100,"state":"success","description":"Quality Gate passed"}`))
	})

	// SonarQube endpoints
	sonarMux.HandleFunc("/api/projects/create", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("project") == "fail-sonar" {
			http.Error(w, "create failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	sonarMux.HandleFunc("/api/projects/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("projects")
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(q, "missing") {
			_, _ = w.Write([]byte(`{"components":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"components":[{"key":"` + q + `"}]}`))
	})
	sonarMux.HandleFunc("/api/projects/delete", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	sonarMux.HandleFunc("/api/user_tokens/revoke", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	sonarMux.HandleFunc("/api/user_tokens/generate", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("projectKey") == "fail-token" {
			http.Error(w, "bad token", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"sqp_token_mock_123"}`))
	})
	sonarMux.HandleFunc("/api/qualitygates/project_status", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("projectKey")
		w.Header().Set("Content-Type", "application/json")
		switch key {
		case "factory-passed":
			_, _ = w.Write([]byte(`{"projectStatus":{"status":"OK"}}`))
		case "factory-failed":
			_, _ = w.Write([]byte(`{"projectStatus":{"status":"ERROR"}}`))
		case "factory-warn":
			_, _ = w.Write([]byte(`{"projectStatus":{"status":"WARN"}}`))
		default:
			_, _ = w.Write([]byte(`{"projectStatus":{"status":"NONE"}}`))
		}
	})

	forgejoServer := httptest.NewServer(forgejoMux)
	sonarServer := httptest.NewServer(sonarMux)
	t.Cleanup(func() {
		forgejoServer.Close()
		sonarServer.Close()
	})

	fClient := forgejo.NewClient(forgejo.ClientConfig{BaseURL: forgejoServer.URL})
	sClient := sonar.NewClient(sonar.ClientConfig{BaseURL: sonarServer.URL})
	return forgejoServer, sonarServer, fClient, sClient
}

func TestProjectLifecycle(t *testing.T) {
	gServer, sServer, gClient, sClient := setupMockServers(t)
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:    tmpDir,
		ProjectsDir: filepath.Join(tmpDir, "projects"),
	}

	mgr := NewManagerWithClients(paths, gClient, sClient, gServer.URL, sServer.URL)
	ctx := context.Background()

	// 1. Validation error on empty name
	if _, err := mgr.Create(ctx, "", "", "", ""); err == nil {
		t.Fatal("expected error on empty project name")
	}

	// 2. Error on Gitea org failure
	if _, err := mgr.Create(ctx, "app", "fail-org", "", ""); err == nil {
		t.Fatal("expected error on fail-org")
	}

	// 3. Error on Sonar create failure
	if _, err := mgr.Create(ctx, "app", "factory", "fail-sonar", ""); err == nil {
		t.Fatal("expected error on fail-sonar")
	}

	// 4. Successful creation with defaults
	proj, err := mgr.Create(ctx, "app", "", "", "sample description")
	if err != nil {
		t.Fatalf("failed creating project: %v", err)
	}
	if proj.Org != "factory" || proj.SonarKey != "factory-app" || proj.ScanStatus != "UNANALYZED" {
		t.Errorf("unexpected project details: %+v", proj)
	}
	if proj.AnalysisToken != "sqp_token_mock_123" {
		t.Errorf("expected token sqp_token_mock_123, got %s", proj.AnalysisToken)
	}

	// 5. Get project
	gotProj, err := mgr.Get(ctx, "factory", "app")
	if err != nil || gotProj.Name != "app" {
		t.Fatalf("Get failed: %v", err)
	}
	if _, err := mgr.Get(ctx, "factory", "nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent project")
	}

	// 6. List projects
	list, err := mgr.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("expected 1 project in list, got %d, err=%v", len(list), err)
	}

	// 7. FormatList formatting
	output := FormatList(list)
	if !strings.Contains(output, "● factory / app") || !strings.Contains(output, "Forgejo:") || !strings.Contains(output, "Scan Status: UNANALYZED") {
		t.Errorf("unexpected FormatList output:\n%s", output)
	}

	// 8. Delete without remote
	if err := mgr.Delete(ctx, "factory", "app", false); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	emptyList, err := mgr.List(ctx)
	if err != nil || len(emptyList) != 0 {
		t.Fatalf("expected empty list after deletion")
	}
	if emptyOutput := FormatList(emptyList); !strings.Contains(emptyOutput, "No projects registered") {
		t.Errorf("expected No projects registered message: %s", emptyOutput)
	}

	// 9. PostCommitStatus
	status, statusErr := mgr.PostCommitStatus(ctx, "factory", "app", "sha123", "factory-passed")
	if statusErr != nil || status.State != "success" {
		t.Fatalf("PostCommitStatus failed: %v, status: %+v", statusErr, status)
	}
	if _, err := mgr.PostCommitStatus(ctx, "factory", "", "sha123", "factory-passed"); err == nil {
		t.Fatal("expected error on empty repo in PostCommitStatus")
	}
	if _, err := mgr.PostCommitStatus(ctx, "factory", "app", "", "factory-passed"); err == nil {
		t.Fatal("expected error on empty sha in PostCommitStatus")
	}
	if _, err := mgr.PostCommitStatus(ctx, "", "app", "sha123", ""); err != nil {
		t.Fatalf("expected default org and sonarKey in PostCommitStatus to succeed: %v", err)
	}
}

func TestProjectLinkAndDeleteRemote(t *testing.T) {
	gServer, sServer, gClient, sClient := setupMockServers(t)
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:    tmpDir,
		ProjectsDir: filepath.Join(tmpDir, "projects"),
	}

	mgr := NewManagerWithClients(paths, gClient, sClient, gServer.URL, sServer.URL)
	ctx := context.Background()

	// Link validation
	if _, err := mgr.Link(ctx, "", ""); err == nil {
		t.Fatal("expected error on empty repo")
	}

	// Link with org/repo
	p1, err := mgr.Link(ctx, "fleet/tools", "fleet-tools")
	if err != nil || p1.Org != "fleet" || p1.Name != "tools" || p1.SonarKey != "fleet-tools" {
		t.Fatalf("Link failed: %v, %+v", err, p1)
	}

	// Link with bare repo (default org)
	p2, err := mgr.Link(ctx, "app", "")
	if err != nil || p2.Org != "factory" || p2.SonarKey != "factory-app" {
		t.Fatalf("Link bare failed: %v, %+v", err, p2)
	}

	// Delete with deleteRemote = true
	if err := mgr.Delete(ctx, "fleet", "tools", true); err != nil {
		t.Fatalf("Delete with remote failed: %v", err)
	}
	if err := mgr.Delete(ctx, "fleet", "tools", false); err == nil {
		t.Fatal("expected error deleting already deleted project")
	}
}

func TestProjectScanStatusAndCorruptFile(t *testing.T) {
	gServer, sServer, gClient, sClient := setupMockServers(t)
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:    tmpDir,
		ProjectsDir: filepath.Join(tmpDir, "projects"),
	}

	mgr := NewManagerWithClients(paths, gClient, sClient, gServer.URL, sServer.URL)
	ctx := context.Background()

	_ = paths.EnsureDirectories()
	// Write projects with different statuses
	for _, tc := range []struct {
		name     string
		sonarKey string
		expected string
	}{
		{"passed", "factory-passed", "PASSED"},
		{"failed", "factory-failed", "FAILED"},
		{"warn", "factory-warn", "WARN"},
	} {
		p := &Project{
			Name:     tc.name,
			Org:      "factory",
			SonarKey: tc.sonarKey,
		}
		_ = mgr.Save(p)
	}

	// Corrupt file in projects dir
	_ = os.WriteFile(filepath.Join(paths.ProjectsDir, "corrupt.json"), []byte("invalid json"), 0600)
	_ = os.WriteFile(filepath.Join(paths.ProjectsDir, "ignored.txt"), []byte("ignored"), 0600)

	list, err := mgr.List(ctx)
	if err != nil || len(list) != 3 {
		t.Fatalf("expected 3 valid projects, got %d, err=%v", len(list), err)
	}

	// Test Get on corrupt file
	_ = os.WriteFile(filepath.Join(paths.ProjectsDir, "factory__corrupt.json"), []byte("invalid"), 0600)
	if _, err := mgr.Get(ctx, "factory", "corrupt"); err == nil {
		t.Fatal("expected error getting corrupt project")
	}
}

func TestProjectDoctorAndAutoHeal(t *testing.T) {
	gServer, sServer, gClient, sClient := setupMockServers(t)
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:    tmpDir,
		ProjectsDir: filepath.Join(tmpDir, "projects"),
	}

	mgr := NewManagerWithClients(paths, gClient, sClient, gServer.URL, sServer.URL)
	ctx := context.Background()

	// 1. Healthy project
	p1, _ := mgr.Create(ctx, "app", "factory", "factory-app", "desc")
	rep1, err := mgr.Doctor(ctx, "factory", "app")
	if err != nil || rep1.ErrorCount != 0 || rep1.WarningCount != 0 {
		t.Fatalf("expected healthy doctor report: %+v, err=%v", rep1, err)
	}
	out1 := FormatDoctorReport(rep1)
	if !strings.Contains(out1, "[ OK ]") || !strings.Contains(out1, "Summary: 0 healed") {
		t.Errorf("unexpected doctor report output:\n%s", out1)
	}

	// 2. Doctor with missing Gitea repo and missing token
	p2 := &Project{
		Name:          "missing-repo",
		Org:           "factory",
		SonarKey:      "factory-missing-repo",
		AnalysisToken: "", // triggers auto-heal
	}
	_ = mgr.Save(p2)

	rep2, err := mgr.Doctor(ctx, "factory", "missing-repo")
	if err != nil || rep2.HealedCount != 3 {
		t.Fatalf("expected 3 healed items, got %d, err=%v", rep2.HealedCount, err)
	}
	out2 := FormatDoctorReport(rep2)
	if !strings.Contains(out2, "[HEAL]") {
		t.Errorf("expected [HEAL] in output: %s", out2)
	}

	// 3. Doctor with unrepairable / warning conditions
	p3 := &Project{
		Name:          "fail-repo",
		Org:           "fail-org",
		SonarKey:      "fail-sonar",
		AnalysisToken: "",
	}
	_ = mgr.Save(p3)
	rep3, err := mgr.Doctor(ctx, "fail-org", "fail-repo")
	if err != nil || rep3.ErrorCount == 0 {
		t.Fatalf("expected error count in doctor report: %+v", rep3)
	}
	out3 := FormatDoctorReport(rep3)
	if !strings.Contains(out3, "[FAIL]") {
		t.Errorf("expected [FAIL] in report output: %s", out3)
	}

	// 4. Doctor with failing token generation
	p4 := &Project{
		Name:          "app",
		Org:           "factory",
		SonarKey:      "fail-token",
		AnalysisToken: "",
	}
	_ = mgr.Save(p4)
	rep4, err := mgr.Doctor(ctx, "factory", "app")
	if err != nil || rep4.WarningCount == 0 {
		t.Fatalf("expected warning count for token failure: %+v", rep4)
	}
	out4 := FormatDoctorReport(rep4)
	if !strings.Contains(out4, "[WARN]") {
		t.Errorf("expected [WARN] in report output: %s", out4)
	}

	// 5. DoctorAll
	reports, err := mgr.DoctorAll(ctx)
	if err != nil || len(reports) < 2 {
		t.Fatalf("expected doctor reports, got %d, err=%v", len(reports), err)
	}

	// 6. Doctor on missing project metadata
	if _, err := mgr.Doctor(ctx, "factory", "nonexistent"); err == nil {
		t.Fatal("expected error on nonexistent project doctor")
	}
	_ = p1
}

func TestManagerEdgeCases(t *testing.T) {
	// 1. NewManager with alternative env vars
	t.Setenv("FORGEJO_URL", "")
	t.Setenv("FORGEJO_HOST_URL", "")
	t.Setenv("GITEA_URL", "")
	t.Setenv("GITEA_HOST_URL", "http://127.0.0.1:3001/")
	t.Setenv("ADMIN_BACKPLANE_PASSWORD", "")
	t.Setenv("SONAR_HOST_URL", "")
	t.Setenv("SONARQUBE_URL", "http://127.0.0.1:9001/")
	t.Setenv("SONAR_ADMIN_USER", "")

	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:    tmpDir,
		ProjectsDir: filepath.Join(tmpDir, "projects"),
	}
	mgr := NewManager(paths)
	if mgr.forgejoBase != "http://127.0.0.1:3001" || mgr.sonarBase != "http://127.0.0.1:9001" {
		t.Errorf("unexpected bases: forgejo=%s, sonar=%s", mgr.forgejoBase, mgr.sonarBase)
	}

	// 2. resolveScanStatus with unusual status
	customStatusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"projectStatus":{"status":"CUSTOM_STATUS"}}`))
	}))
	defer customStatusServer.Close()
	customSonar := sonar.NewClient(sonar.ClientConfig{BaseURL: customStatusServer.URL})
	mgrCustom := NewManagerWithClients(paths, nil, customSonar, "", "")
	if status := mgrCustom.resolveScanStatus(context.Background(), "any"); status != "CUSTOM_STATUS" {
		t.Errorf("expected CUSTOM_STATUS, got %s", status)
	}

	// 3. Save error with unwritable directory
	badPaths := config.Paths{
		DataHome:    "/proc/not/writable",
		ProjectsDir: "/proc/not/writable/projects",
	}
	badMgr := NewManagerWithClients(badPaths, nil, nil, "", "")
	if err := badMgr.Save(&Project{Name: "app", Org: "factory"}); err == nil {
		t.Errorf("expected error saving to unwritable directory")
	}

	// 4. List error with unwritable/nonexistent path
	if _, err := badMgr.List(context.Background()); err == nil {
		t.Errorf("expected error listing with unwritable path")
	}
}

func TestValidateNameAndUnreachableServices(t *testing.T) {
	// 1. ValidateName unit tests
	if err := ValidateName(""); err == nil {
		t.Errorf("expected error on empty name")
	}
	if err := ValidateName("valid-name_1.0"); err != nil {
		t.Errorf("expected valid-name to pass: %v", err)
	}
	if err := ValidateName("invalid/name"); err == nil {
		t.Errorf("expected error on slash")
	}
	if err := ValidateName(`invalid\name`); err == nil {
		t.Errorf("expected error on backslash")
	}
	if err := ValidateName(".."); err == nil {
		t.Errorf("expected error on traversal ..")
	}

	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:    tmpDir,
		ProjectsDir: filepath.Join(tmpDir, "projects"),
	}

	// 2. Unreachable Forgejo and SonarQube services in Doctor
	unreachableForgejo := forgejo.NewClient(forgejo.ClientConfig{BaseURL: "http://127.0.0.1:65511"})
	unreachableSonar := sonar.NewClient(sonar.ClientConfig{BaseURL: "http://127.0.0.1:65512"})
	mgr := NewManagerWithClients(paths, unreachableForgejo, unreachableSonar, "http://127.0.0.1:65511", "http://127.0.0.1:65512")

	// Save a project
	p := &Project{
		Name:          "offline-app",
		Org:           "factory",
		SonarKey:      "factory-offline-app",
		AnalysisToken: "tok",
	}
	_ = mgr.Save(p)

	rep, err := mgr.Doctor(context.Background(), "factory", "offline-app")
	if err != nil {
		t.Fatalf("unexpected doctor error: %v", err)
	}
	// Gitea and SonarQube should both be warnings (unreachable), not errors
	if rep.WarningCount < 2 || rep.ErrorCount > 0 {
		t.Errorf("expected at least 2 warnings and 0 errors for unreachable services: %+v", rep)
	}

	// 3. Create and Get with org/name syntax
	if _, err := mgr.Create(context.Background(), "org/name/extra", "", "", ""); err == nil {
		t.Errorf("expected error on invalid name with extra slash")
	}
	if _, err := mgr.Get(context.Background(), "", "org/name/extra"); err == nil {
		t.Errorf("expected error getting invalid name with extra slash")
	}
}
