// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package forgejo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestForgejoClientSuite(t *testing.T) {
	// Mock Forgejo API server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		case r.Method == http.MethodGet && path == "/api/v1/users/existing-user":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"username":"existing-user"}`))

		case r.Method == http.MethodGet && path == "/api/v1/users/new-user":
			w.WriteHeader(http.StatusNotFound)

		case r.Method == http.MethodGet && path == "/api/v1/users/conflict-user":
			w.WriteHeader(http.StatusNotFound)

		case r.Method == http.MethodPost && path == "/api/v1/admin/users":
			var payload map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["username"] == "fail-user" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"server error"}`))
				return
			}
			if payload["username"] == "conflict-user" {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"message":"user already exists"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":10,"username":"new-user"}`))

		case r.Method == http.MethodDelete && path == "/api/v1/admin/users/existing-user":
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodDelete && path == "/api/v1/admin/users/fail-delete":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"delete failed"}`))

		case r.Method == http.MethodPost && path == "/api/v1/admin/users/test-agent/keys":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":1,"title":"test-agent-key"}`))

		case r.Method == http.MethodPost && path == "/api/v1/admin/users/conflict-key-user/keys":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"key already exists"}`))

		case r.Method == http.MethodPost && path == "/api/v1/admin/users/fail-key-user/keys":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"invalid key"}`))

		case r.Method == http.MethodGet && path == "/api/v1/orgs/existing-org":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"username":"existing-org"}`))

		case r.Method == http.MethodGet && path == "/api/v1/orgs/new-org":
			w.WriteHeader(http.StatusNotFound)

		case r.Method == http.MethodGet && path == "/api/v1/orgs/conflict-org":
			w.WriteHeader(http.StatusNotFound)

		case r.Method == http.MethodPost && path == "/api/v1/orgs":
			var payload map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["username"] == "conflict-org" {
				w.WriteHeader(http.StatusConflict)
				return
			}
			if payload["username"] == "fail-org-create" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":2,"username":"new-org"}`))

		case r.Method == http.MethodPut && path == "/api/v1/orgs/fleet/members/test-agent":
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodPut && path == "/api/v1/orgs/method-not-allowed-org/members/test-agent":
			w.WriteHeader(http.StatusMethodNotAllowed)

		case r.Method == http.MethodGet && path == "/api/v1/orgs/method-not-allowed-org/teams":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":98,"name":"Owners"},{"id":99,"name":"Members"}]`))

		case r.Method == http.MethodPut && path == "/api/v1/teams/99/members/test-agent":
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodPut && path == "/api/v1/orgs/team-fail-org/members/test-agent":
			w.WriteHeader(http.StatusMethodNotAllowed)

		case r.Method == http.MethodGet && path == "/api/v1/orgs/team-fail-org/teams":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":101,"name":"Members"}]`))

		case r.Method == http.MethodPut && path == "/api/v1/teams/101/members/test-agent":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"team error"}`))

		case r.Method == http.MethodPut && path == "/api/v1/orgs/fail-org/members/test-agent":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"org not found"}`))

		case r.Method == http.MethodGet && path == "/api/v1/repos/fleet/existing-repo":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"existing-repo"}`))

		case r.Method == http.MethodGet && path == "/api/v1/repos/fleet/new-repo":
			w.WriteHeader(http.StatusNotFound)

		case r.Method == http.MethodGet && path == "/api/v1/repos/fleet/conflict-repo":
			w.WriteHeader(http.StatusNotFound)

		case r.Method == http.MethodDelete && path == "/api/v1/repos/fleet/to-delete":
			w.WriteHeader(http.StatusNoContent)

		case r.Method == http.MethodDelete && path == "/api/v1/repos/fleet/fail-repo":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"delete failed"}`))

		case r.Method == http.MethodPost && path == "/api/v1/orgs/fleet/repos":
			var payload map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["name"] == "conflict-repo" {
				w.WriteHeader(http.StatusConflict)
				return
			}
			if payload["name"] == "fail-repo" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":100,"name":"new-repo"}`))

		case r.Method == http.MethodGet && path == "/api/v1/repos/fleet/tasks/issues":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id":1,"number":101,"title":"Task 1","state":"open"}]`))

		case r.Method == http.MethodGet && path == "/api/v1/repos/fleet/fail-repo/issues":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"failed"}`))

		case r.Method == http.MethodPost && path == "/api/v1/repos/fleet/tasks/issues":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":2,"number":102,"title":"Created Task","state":"open"}`))

		case r.Method == http.MethodPost && path == "/api/v1/repos/fleet/fail-repo/issues":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"failed"}`))

		case r.Method == http.MethodPost && path == "/api/v1/repos/fleet/tools/pulls":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":5,"number":1,"title":"PR Title","state":"open"}`))

		case r.Method == http.MethodPost && path == "/api/v1/repos/fleet/fail-repo/pulls":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"failed"}`))

		case r.Method == http.MethodGet && path == "/api/v1/repos/fleet/tools/pulls/1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":5,"number":1,"title":"PR Title","state":"open"}`))

		case r.Method == http.MethodGet && path == "/api/v1/repos/fleet/fail-repo/pulls/1":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))

		case r.Method == http.MethodPost && path == "/api/v1/repos/fleet/tools/pulls/1/reviews":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":12,"state":"APPROVED","body":"looks good"}`))

		case r.Method == http.MethodPost && path == "/api/v1/repos/fleet/fail-repo/pulls/1/reviews":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"review failed"}`))

		case r.Method == http.MethodGet && path == "/api/v1/repos/fleet/tools/contents/README.md":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"README.md","path":"README.md","encoding":"base64","content":"IyBUb29scyBSZXBvCg=="}`))

		case r.Method == http.MethodGet && path == "/api/v1/repos/fleet/tools/contents/raw.txt":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"name":"raw.txt","path":"raw.txt","content":"plain raw text content"}`))

		case r.Method == http.MethodGet && path == "/api/v1/repos/fleet/tools/contents/bad-json.txt":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`not valid json`))

		case r.Method == http.MethodGet && path == "/api/v1/repos/fleet/fail-repo/contents/README.md":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"file not found"}`))

		case r.Method == http.MethodPost && path == "/api/v1/repos/fleet/tools/statuses/sha123":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":42,"state":"success","target_url":"http://127.0.0.1:9000/dashboard?id=fleet-tools","description":"Quality Gate passed","context":"sonarqube/quality-gate"}`))

		case r.Method == http.MethodPost && path == "/api/v1/repos/fleet/fail-repo/statuses/sha123":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"commit status error"}`))

		case r.Method == http.MethodPost && path == "/api/v1/repos/fleet/tools/statuses/badjson":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`not valid json`))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Default constructor
	defaultClient := NewClient(ClientConfig{})
	if defaultClient.cfg.BaseURL != "http://127.0.0.1:3000" {
		t.Errorf("expected default baseURL, got %s", defaultClient.cfg.BaseURL)
	}

	client := NewClient(ClientConfig{
		BaseURL:   ts.URL,
		AdminUser: "admin",
		AdminPass: "secret",
		Token:     "mock-token",
	})

	// Basic auth client (Token empty)
	basicAuthClient := NewClient(ClientConfig{
		BaseURL:   ts.URL,
		AdminUser: "admin",
		AdminPass: "secret",
	})

	// 1. EnsureUser
	if err := client.EnsureUser(ctx, "existing-user", "pass", ""); err != nil {
		t.Errorf("EnsureUser existing-user failed: %v", err)
	}
	if err := basicAuthClient.EnsureUser(ctx, "new-user", "pass", "custom@local.fleet"); err != nil {
		t.Errorf("EnsureUser new-user failed: %v", err)
	}
	if err := client.EnsureUser(ctx, "conflict-user", "pass", ""); err != nil {
		t.Errorf("EnsureUser conflict-user should succeed: %v", err)
	}
	if err := client.EnsureUser(ctx, "fail-user", "pass", ""); err == nil {
		t.Errorf("expected error on fail-user")
	}
	if err := client.EnsureUser(ctx, "", "pass", ""); err == nil {
		t.Errorf("expected error on empty username")
	}

	// 1b. DeleteUser
	if err := client.DeleteUser(ctx, "existing-user", true); err != nil {
		t.Errorf("DeleteUser existing-user failed: %v", err)
	}
	if err := client.DeleteUser(ctx, "fail-delete", true); err == nil {
		t.Errorf("expected error on fail-delete")
	}
	if err := client.DeleteUser(ctx, "", true); err == nil {
		t.Errorf("expected error on empty username in DeleteUser")
	}

	// 2. AddUserSSHKey
	if err := client.AddUserSSHKey(ctx, "test-agent", "", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA test"); err != nil {
		t.Errorf("AddUserSSHKey failed: %v", err)
	}
	if err := client.AddUserSSHKey(ctx, "conflict-key-user", "title", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA test"); err != nil {
		t.Errorf("AddUserSSHKey conflict should succeed: %v", err)
	}
	if err := client.AddUserSSHKey(ctx, "fail-key-user", "title", "ssh-bad"); err == nil {
		t.Errorf("expected error on fail-key-user")
	}
	if err := client.AddUserSSHKey(ctx, "", "", ""); err == nil {
		t.Errorf("expected error on empty key args")
	}

	// 3. EnsureOrg
	if err := client.EnsureOrg(ctx, "existing-org"); err != nil {
		t.Errorf("EnsureOrg existing-org failed: %v", err)
	}
	if err := client.EnsureOrg(ctx, "new-org"); err != nil {
		t.Errorf("EnsureOrg new-org failed: %v", err)
	}
	if err := client.EnsureOrg(ctx, "conflict-org"); err != nil {
		t.Errorf("EnsureOrg conflict-org should succeed: %v", err)
	}
	if err := client.EnsureOrg(ctx, "fail-org-create"); err == nil {
		t.Errorf("expected error on fail-org-create")
	}
	if err := client.EnsureOrg(ctx, ""); err == nil {
		t.Errorf("expected error on empty org name")
	}

	// 4. AddOrgMember
	if err := client.AddOrgMember(ctx, "fleet", "test-agent"); err != nil {
		t.Errorf("AddOrgMember failed: %v", err)
	}
	if err := client.AddOrgMember(ctx, "method-not-allowed-org", "test-agent"); err != nil {
		t.Errorf("AddOrgMember with 405 team fallback failed: %v", err)
	}
	if err := client.AddOrgMember(ctx, "team-fail-org", "test-agent"); err == nil {
		t.Errorf("expected error on team-fail-org member add")
	}
	if err := client.AddOrgMember(ctx, "fail-org", "test-agent"); err == nil {
		t.Errorf("expected error on fail-org member add")
	}
	if err := client.AddOrgMember(ctx, "", ""); err == nil {
		t.Errorf("expected error on empty member add args")
	}

	// 5. EnsureRepo
	if err := client.EnsureRepo(ctx, "fleet", "existing-repo", "", true); err != nil {
		t.Errorf("EnsureRepo existing failed: %v", err)
	}
	if err := client.EnsureRepo(ctx, "fleet", "new-repo", "tools repo", true); err != nil {
		t.Errorf("EnsureRepo new failed: %v", err)
	}
	if err := client.EnsureRepo(ctx, "fleet", "conflict-repo", "", true); err != nil {
		t.Errorf("EnsureRepo conflict should succeed: %v", err)
	}
	if err := client.EnsureRepo(ctx, "fleet", "fail-repo", "", true); err == nil {
		t.Errorf("expected error on fail-repo")
	}
	if err := client.EnsureRepo(ctx, "", "", "", true); err == nil {
		t.Errorf("expected error on empty repo args")
	}

	// 5b. RepoExists & DeleteRepo
	exists, err := client.RepoExists(ctx, "fleet", "existing-repo")
	if err != nil || !exists {
		t.Errorf("expected existing-repo to exist: %v, %v", exists, err)
	}
	notExists, err := client.RepoExists(ctx, "fleet", "new-repo")
	if err != nil || notExists {
		t.Errorf("expected new-repo not to exist: %v, %v", notExists, err)
	}
	if _, err := client.RepoExists(ctx, "", ""); err == nil {
		t.Errorf("expected error on empty repo args in RepoExists")
	}

	if err := client.DeleteRepo(ctx, "fleet", "to-delete"); err != nil {
		t.Errorf("DeleteRepo to-delete failed: %v", err)
	}
	if err := client.DeleteRepo(ctx, "fleet", "fail-repo"); err == nil {
		t.Errorf("expected DeleteRepo fail-repo to error")
	}
	if err := client.DeleteRepo(ctx, "", ""); err == nil {
		t.Errorf("expected DeleteRepo with empty args to error")
	}

	// 6. ListIssues
	issues, listErr := client.ListIssues(ctx, "fleet", "tasks", "open", 1, 10)
	if listErr != nil || len(issues) != 1 {
		t.Errorf("ListIssues failed: %v (len: %d)", listErr, len(issues))
	}
	if _, err = client.ListIssues(ctx, "fleet", "fail-repo", "open", 1, 10); err == nil {
		t.Errorf("expected error on ListIssues fail-repo")
	}
	// Default params check
	if _, err = client.ListIssues(ctx, "", "", "", 0, 0); err != nil {
		t.Errorf("ListIssues with defaults failed: %v", err)
	}

	// 7. CreateIssue
	issue, createErr := client.CreateIssue(ctx, "fleet", "tasks", "New Task", "Task Body", []string{"bug"}, []string{"agent"})
	if createErr != nil || issue.ID != 2 {
		t.Errorf("CreateIssue failed: %v", createErr)
	}
	if _, err = client.CreateIssue(ctx, "fleet", "fail-repo", "Task", "Body", nil, nil); err == nil {
		t.Errorf("expected error on CreateIssue fail-repo")
	}
	if _, err = client.CreateIssue(ctx, "", "", "", "", nil, nil); err == nil {
		t.Errorf("expected error on empty title in CreateIssue")
	}

	// 8. CreatePullRequest
	pr, prErr := client.CreatePullRequest(ctx, "fleet", "tools", "New PR", "PR Body", "feat/1", "main")
	if prErr != nil || pr.ID != 5 {
		t.Errorf("CreatePullRequest failed: %v", prErr)
	}
	if _, err = client.CreatePullRequest(ctx, "fleet", "fail-repo", "PR", "Body", "feat/1", "main"); err == nil {
		t.Errorf("expected error on CreatePullRequest fail-repo")
	}
	if _, err = client.CreatePullRequest(ctx, "", "", "PR", "Body", "feat/1", ""); err == nil {
		t.Errorf("expected error on empty repo in CreatePullRequest")
	}
	if _, err = client.CreatePullRequest(ctx, "fleet", "tools", "", "Body", "feat/1", ""); err == nil {
		t.Errorf("expected error on empty title in CreatePullRequest")
	}
	if _, err = client.CreatePullRequest(ctx, "fleet", "tools", "PR", "Body", "", ""); err == nil {
		t.Errorf("expected error on empty head in CreatePullRequest")
	}
	if _, err = client.CreatePullRequest(ctx, "", "tools", "PR", "Body", "feat/1", ""); err != nil {
		t.Errorf("expected default owner in CreatePullRequest to succeed: %v", err)
	}

	// 9. GetPullRequest
	prGet, getErr := client.GetPullRequest(ctx, "fleet", "tools", 1)
	if getErr != nil || prGet.ID != 5 {
		t.Errorf("GetPullRequest failed: %v", getErr)
	}
	if _, err = client.GetPullRequest(ctx, "fleet", "fail-repo", 1); err == nil {
		t.Errorf("expected error on GetPullRequest fail-repo")
	}
	if _, err = client.GetPullRequest(ctx, "fleet", "", 1); err == nil {
		t.Errorf("expected error on empty repo in GetPullRequest")
	}
	if _, err = client.GetPullRequest(ctx, "", "tools", 1); err != nil {
		t.Errorf("expected default owner in GetPullRequest to succeed: %v", err)
	}

	// 10. ReviewPullRequest
	review, revErr := client.ReviewPullRequest(ctx, "fleet", "tools", 1, "APPROVE", "looks good")
	if revErr != nil || review.ID != 12 {
		t.Errorf("ReviewPullRequest failed: %v", revErr)
	}
	if _, err = client.ReviewPullRequest(ctx, "fleet", "fail-repo", 1, "APPROVE", ""); err == nil {
		t.Errorf("expected error on ReviewPullRequest fail-repo")
	}
	if _, err = client.ReviewPullRequest(ctx, "fleet", "", 1, "", ""); err == nil {
		t.Errorf("expected error on empty repo in ReviewPullRequest")
	}
	if _, err = client.ReviewPullRequest(ctx, "", "tools", 1, "", ""); err != nil {
		t.Errorf("expected default owner and event in ReviewPullRequest to succeed: %v", err)
	}

	// 11. GetFile
	file, fileErr := client.GetFile(ctx, "fleet", "tools", "/README.md", "main")
	if fileErr != nil || !strings.Contains(file.Content, "# Tools Repo") {
		t.Errorf("GetFile README.md failed: %v, content: %s", fileErr, file.Content)
	}
	rawFile, rawErr := client.GetFile(ctx, "fleet", "tools", "raw.txt", "")
	if rawErr != nil || rawFile.Content != "plain raw text content" {
		t.Errorf("GetFile raw.txt failed: %v", rawErr)
	}
	if _, err = client.GetFile(ctx, "fleet", "tools", "bad-json.txt", ""); err == nil {
		t.Errorf("expected error on GetFile bad-json.txt")
	}
	if _, err = client.GetFile(ctx, "fleet", "fail-repo", "README.md", "main"); err == nil {
		t.Errorf("expected error on GetFile fail-repo")
	}
	if _, err = client.GetFile(ctx, "fleet", "", "README.md", "main"); err == nil {
		t.Errorf("expected error on empty repo in GetFile")
	}
	if _, err = client.GetFile(ctx, "fleet", "tools", "", "main"); err == nil {
		t.Errorf("expected error on empty filePath in GetFile")
	}
	if _, err = client.GetFile(ctx, "", "tools", "raw.txt", ""); err != nil {
		t.Errorf("expected default owner and ref in GetFile to succeed: %v", err)
	}

	// 12. CreateCommitStatus
	status, statusErr := client.CreateCommitStatus(ctx, "fleet", "tools", "sha123", CreateCommitStatusOptions{State: StateSuccess})
	if statusErr != nil || status.ID != 42 || status.State != "success" {
		t.Errorf("CreateCommitStatus failed: %v, status: %+v", statusErr, status)
	}
	if _, err = client.CreateCommitStatus(ctx, "fleet", "", "sha123", CreateCommitStatusOptions{}); err == nil {
		t.Errorf("expected error on empty repo in CreateCommitStatus")
	}
	if _, err = client.CreateCommitStatus(ctx, "fleet", "tools", "", CreateCommitStatusOptions{}); err == nil {
		t.Errorf("expected error on empty sha in CreateCommitStatus")
	}
	if _, err = client.CreateCommitStatus(ctx, "fleet", "fail-repo", "sha123", CreateCommitStatusOptions{}); err == nil {
		t.Errorf("expected error on fail-repo in CreateCommitStatus")
	}
	if _, err = client.CreateCommitStatus(ctx, "fleet", "tools", "badjson", CreateCommitStatusOptions{}); err == nil {
		t.Errorf("expected error on bad json in CreateCommitStatus")
	}
	if _, err = client.CreateCommitStatus(ctx, "", "tools", "sha123", CreateCommitStatusOptions{}); err != nil {
		t.Errorf("expected default owner and option defaults in CreateCommitStatus to succeed: %v", err)
	}

	// Network / URL error handling
	badClient := NewClient(ClientConfig{BaseURL: "http://127.0.0.1:64999", Timeout: 10 * time.Millisecond})
	_ = badClient.EnsureUser(ctx, "test", "pass", "")
	_ = badClient.AddUserSSHKey(ctx, "test", "key", "ssh-ed25519 AAA")
	_ = badClient.EnsureOrg(ctx, "org")
	_ = badClient.AddOrgMember(ctx, "org", "user")
	_ = badClient.EnsureRepo(ctx, "org", "repo", "", false)
	_, _ = badClient.ListIssues(ctx, "fleet", "tasks", "open", 1, 10)
	_, _ = badClient.CreateIssue(ctx, "fleet", "tasks", "title", "body", nil, nil)
	_, _ = badClient.CreatePullRequest(ctx, "fleet", "tools", "title", "body", "head", "base")
	_, _ = badClient.GetPullRequest(ctx, "fleet", "tools", 1)
	_, _ = badClient.ReviewPullRequest(ctx, "fleet", "tools", 1, "APPROVE", "")
	_, _ = badClient.GetFile(ctx, "fleet", "tools", "README.md", "main")
	_, _ = badClient.CreateCommitStatus(ctx, "fleet", "tools", "sha123", CreateCommitStatusOptions{})
}
