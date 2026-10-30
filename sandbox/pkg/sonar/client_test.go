// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package sonar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewClientDefaults(t *testing.T) {
	client := NewClient(ClientConfig{})
	if client.baseURL != "http://127.0.0.1:9000" {
		t.Errorf("expected default baseURL http://127.0.0.1:9000, got %s", client.baseURL)
	}
	if client.httpClient.Timeout != 5*time.Second {
		t.Errorf("expected default timeout 5s, got %v", client.httpClient.Timeout)
	}
}

func TestGetSystemStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/system/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"test-id","version":"26.9.0","status":"UP"}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, Token: "mock-token"})
	status, err := client.GetSystemStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Status != "UP" || status.Version != "26.9.0" {
		t.Errorf("unexpected status struct: %+v", status)
	}
}

func TestGetSystemStatusErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL})
	_, err := client.GetSystemStatus(context.Background())
	if err == nil {
		t.Fatal("expected error on 500 response, got nil")
	}

	// Invalid URL
	badClient := NewClient(ClientConfig{BaseURL: "http://127.0.0.1:1"})
	_, err = badClient.GetSystemStatus(context.Background())
	if err == nil {
		t.Fatal("expected connection error, got nil")
	}

	// Bad JSON
	badJSONServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`invalid json`))
	}))
	defer badJSONServer.Close()
	badJSONClient := NewClient(ClientConfig{BaseURL: badJSONServer.URL})
	_, err = badJSONClient.GetSystemStatus(context.Background())
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
}

func TestGetProjectQualityGate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/qualitygates/project_status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"projectStatus":{"status":"OK","conditions":[{"metricKey":"new_coverage","status":"OK","actualValue":"91.2","errorThreshold":"80.0"}]}}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL})
	qg, err := client.GetProjectQualityGate(context.Background(), "my-proj")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if qg.Status != "OK" || len(qg.Conditions) != 1 {
		t.Errorf("unexpected quality gate status: %+v", qg)
	}
}

func TestGetProjectQualityGateErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL})
	_, err := client.GetProjectQualityGate(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Bad JSON
	badJSONServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer badJSONServer.Close()
	badJSONClient := NewClient(ClientConfig{BaseURL: badJSONServer.URL})
	_, err = badJSONClient.GetProjectQualityGate(context.Background(), "bad")
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
}

func TestGetIssues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/issues/search" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":1,"issues":[{"key":"iss-1","rule":"go:S100","severity":"MAJOR","component":"pkg/foo.go","line":42,"message":"Simplify code","type":"CODE_SMELL","status":"OPEN"}]}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL})
	resp, err := client.GetIssues(context.Background(), "my-proj", "MAJOR", "CODE_SMELL")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != 1 || len(resp.Issues) != 1 || resp.Issues[0].Line != 42 {
		t.Errorf("unexpected issues response: %+v", resp)
	}
}

func TestGetIssuesErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL})
	_, err := client.GetIssues(context.Background(), "my-proj", "", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	badJSONServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`corrupted`))
	}))
	defer badJSONServer.Close()
	badJSONClient := NewClient(ClientConfig{BaseURL: badJSONServer.URL})
	_, err = badJSONClient.GetIssues(context.Background(), "my-proj", "", "")
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
}

func TestCreateProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects/create" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"project":{"key":"fleet-tools","name":"Fleet Tools"}}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL})
	err := client.CreateProject(context.Background(), "fleet-tools", "Fleet Tools")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Test 400 Bad Request (already exists) is treated as success
	server400 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Already exists", http.StatusBadRequest)
	}))
	defer server400.Close()
	client400 := NewClient(ClientConfig{BaseURL: server400.URL})
	if err := client400.CreateProject(context.Background(), "fleet-tools", "Fleet Tools"); err != nil {
		t.Fatalf("expected 400 to be tolerated, got error: %v", err)
	}

	// Test 500 error
	server500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Failed", http.StatusInternalServerError)
	}))
	defer server500.Close()
	client500 := NewClient(ClientConfig{BaseURL: server500.URL})
	if err := client500.CreateProject(context.Background(), "fleet-tools", "Fleet Tools"); err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

func TestGetComponentMeasures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/measures/component" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"component":{"key":"my-proj","name":"My Project","measures":[{"metric":"coverage","value":"92.4"},{"metric":"duplicated_lines_density","value":"0.5"}]}}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL})
	measures, err := client.GetComponentMeasures(context.Background(), "my-proj", []string{"coverage", "duplicated_lines_density"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(measures) != 2 || measures[0].Metric != "coverage" || measures[0].Value != "92.4" {
		t.Errorf("unexpected measures: %+v", measures)
	}
}

func TestGetComponentMeasuresErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "failed", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL})
	_, err := client.GetComponentMeasures(context.Background(), "my-proj", []string{"coverage"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	badJSONServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`bad json`))
	}))
	defer badJSONServer.Close()
	badJSONClient := NewClient(ClientConfig{BaseURL: badJSONServer.URL})
	_, err = badJSONClient.GetComponentMeasures(context.Background(), "my-proj", []string{"coverage"})
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
}

func TestEnsureUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/create" {
			http.NotFound(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"user":{"login":"agent-bob"}}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		BaseURL:   server.URL,
		AdminUser: "admin",
		AdminPass: "secret",
	})
	err := client.EnsureUser(context.Background(), "agent-bob", "pass123", "Bob Agent", "bob@local.fleet")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Test 400 Bad Request (user already exists) is tolerated
	server400 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "user exists", http.StatusBadRequest)
	}))
	defer server400.Close()
	client400 := NewClient(ClientConfig{BaseURL: server400.URL})
	if err := client400.EnsureUser(context.Background(), "agent-bob", "pass123", "", ""); err != nil {
		t.Fatalf("expected 400 to be tolerated, got: %v", err)
	}

	// Test 500 error
	server500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer server500.Close()
	client500 := NewClient(ClientConfig{BaseURL: server500.URL})
	if err := client500.EnsureUser(context.Background(), "agent-bob", "pass123", "Bob", "bob@local.fleet"); err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

func TestGenerateAndRevokeUserToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user_tokens/generate":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"login":"agent-bob","name":"agent-bob-token","token":"sqa_mock_token_123"}`))
		case "/api/user_tokens/revoke":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL, AdminUser: "admin", AdminPass: "pass"})
	token, err := client.GenerateUserToken(context.Background(), "agent-bob", "agent-bob-token")
	if err != nil {
		t.Fatalf("unexpected error generating token: %v", err)
	}
	if token != "sqa_mock_token_123" {
		t.Errorf("expected token sqa_mock_token_123, got %s", token)
	}

	if err := client.RevokeUserToken(context.Background(), "agent-bob", "agent-bob-token"); err != nil {
		t.Fatalf("unexpected error revoking token: %v", err)
	}
}

func TestGenerateAndRevokeUserTokenErrors(t *testing.T) {
	server500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer server500.Close()

	client500 := NewClient(ClientConfig{BaseURL: server500.URL})
	_, err := client500.GenerateUserToken(context.Background(), "agent-bob", "token")
	if err == nil {
		t.Fatal("expected error on generate token 500")
	}
	if err := client500.RevokeUserToken(context.Background(), "agent-bob", "token"); err == nil {
		t.Fatal("expected error on revoke token 500")
	}

	badJSONServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer badJSONServer.Close()
	badClient := NewClient(ClientConfig{BaseURL: badJSONServer.URL})
	_, err = badClient.GenerateUserToken(context.Background(), "agent-bob", "token")
	if err == nil {
		t.Fatal("expected decode error on bad JSON token response")
	}
}

func TestDeactivateUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/deactivate" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL})
	if err := client.DeactivateUser(context.Background(), "agent-bob"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	server500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "failed", http.StatusInternalServerError)
	}))
	defer server500.Close()
	client500 := NewClient(ClientConfig{BaseURL: server500.URL})
	if err := client500.DeactivateUser(context.Background(), "agent-bob"); err == nil {
		t.Fatal("expected error on 500, got nil")
	} else if !strings.Contains(err.Error(), "POST /api/users/deactivate failed with HTTP 500") {
		t.Fatalf("expected error to contain method and path, got: %v", err)
	}
}

func TestChangePassword(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/change_password" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "invalid method", http.StatusMethodNotAllowed)
			return
		}
		_ = r.ParseForm()
		if r.FormValue("login") != "admin" || r.FormValue("password") != "NewPass123!" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL})
	if err := client.ChangePassword(context.Background(), "admin", "admin", "NewPass123!"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Failure case
	if err := client.ChangePassword(context.Background(), "admin", "admin", "wrong"); err == nil {
		t.Fatal("expected error on bad password, got nil")
	}
}

func TestProjectExists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects/search" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query().Get("projects")
		w.Header().Set("Content-Type", "application/json")
		if q == "existing-proj" {
			_, _ = w.Write([]byte(`{"components":[{"key":"existing-proj"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"components":[]}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL})
	exists, err := client.ProjectExists(context.Background(), "existing-proj")
	if err != nil || !exists {
		t.Fatalf("expected existing-proj to exist, got %v, err=%v", exists, err)
	}

	notExists, err := client.ProjectExists(context.Background(), "unknown-proj")
	if err != nil || notExists {
		t.Fatalf("expected unknown-proj not to exist, got %v, err=%v", notExists, err)
	}

	server500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer server500.Close()
	client500 := NewClient(ClientConfig{BaseURL: server500.URL})
	if _, err := client500.ProjectExists(context.Background(), "any"); err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

func TestDeleteProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/projects/delete" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		if r.FormValue("project") == "to-delete" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL})
	if err := client.DeleteProject(context.Background(), "to-delete"); err != nil {
		t.Fatalf("unexpected error deleting project: %v", err)
	}
	if err := client.DeleteProject(context.Background(), "unknown"); err == nil {
		t.Fatal("expected error on bad delete request")
	}
}

func TestGenerateProjectToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/user_tokens/revoke" {
			_ = r.ParseForm()
			if r.Form.Has("login") {
				http.Error(w, "login parameter should not be passed", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/api/user_tokens/generate" {
			_ = r.ParseForm()
			if r.FormValue("projectKey") == "proj-1" && r.FormValue("type") == "PROJECT_ANALYSIS_TOKEN" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"token":"sqp_token123"}`))
				return
			}
			http.Error(w, "bad token request", http.StatusBadRequest)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{BaseURL: server.URL})
	token, err := client.GenerateProjectToken(context.Background(), "proj-1", "token-name")
	if err != nil || token != "sqp_token123" {
		t.Fatalf("expected token sqp_token123, got %q, err=%v", token, err)
	}

	if _, err := client.GenerateProjectToken(context.Background(), "bad-proj", "token-name"); err == nil {
		t.Fatal("expected error on bad request, got nil")
	}
}
