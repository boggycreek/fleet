// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package forgejo provides a REST client for Forgejo forge operations and administrative tasks.
package forgejo

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ClientConfig holds connection and authentication details for Forgejo API
type ClientConfig struct {
	BaseURL   string
	AdminUser string
	AdminPass string
	Token     string
	Timeout   time.Duration
}

// Client provides an HTTP client for Forgejo administrative and organizational operations
type Client struct {
	cfg        ClientConfig
	httpClient *http.Client
}

// NewClient constructs a new Forgejo API client
func NewClient(cfg ClientConfig) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://127.0.0.1:3000"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.AdminUser == "" {
		cfg.AdminUser = "forgejoadmin"
	}

	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// doRequest performs an authenticated HTTP request against Forgejo REST API
func (c *Client) doRequest(ctx context.Context, method, endpoint string, body interface{}) (statusCode int, respBody []byte, err error) {
	apiURL := fmt.Sprintf("%s/api/v1%s", c.cfg.BaseURL, endpoint)

	var bodyReader io.Reader
	if body != nil {
		buf, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return 0, nil, fmt.Errorf("failed marshaling request body: %w", marshalErr)
		}
		bodyReader = bytes.NewReader(buf)
	}

	req, reqErr := http.NewRequestWithContext(ctx, method, apiURL, bodyReader)
	if reqErr != nil {
		return 0, nil, fmt.Errorf("failed creating http request: %w", reqErr)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if c.cfg.Token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("token %s", c.cfg.Token))
	} else if c.cfg.AdminUser != "" && c.cfg.AdminPass != "" {
		req.SetBasicAuth(c.cfg.AdminUser, c.cfg.AdminPass)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("failed reading response body: %w", err)
	}

	return resp.StatusCode, respBody, nil
}

// EnsureUser creates a user in Forgejo if it does not already exist
func (c *Client) EnsureUser(ctx context.Context, username, password, email string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username cannot be empty")
	}
	if email == "" {
		email = fmt.Sprintf("%s@local.fleet", username)
	}

	// Check if user exists
	statusCode, _, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/users/%s", username), nil)
	if err == nil && statusCode == http.StatusOK {
		return nil // User already exists
	}

	// Create user via admin API
	payload := map[string]interface{}{
		"username":             username,
		"password":             password,
		"email":                email,
		"must_change_password": false,
		"send_notify":          false,
	}

	statusCode, respBody, err := c.doRequest(ctx, http.MethodPost, "/admin/users", payload)
	if err != nil {
		return err
	}
	if statusCode != http.StatusCreated && statusCode != http.StatusOK && statusCode != http.StatusConflict {
		return fmt.Errorf("failed creating forgejo user %s (status %d): %s", username, statusCode, string(respBody))
	}

	return nil
}

// DeleteUser purges a user account and associated repositories from Forgejo
func (c *Client) DeleteUser(ctx context.Context, username string, purge bool) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username cannot be empty")
	}

	endpoint := fmt.Sprintf("/admin/users/%s?purge=%t", username, purge)
	statusCode, respBody, err := c.doRequest(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	if statusCode != http.StatusNoContent && statusCode != http.StatusOK && statusCode != http.StatusNotFound {
		return fmt.Errorf("failed deleting forgejo user %s (status %d): %s", username, statusCode, string(respBody))
	}

	return nil
}

// AddUserSSHKey associates an SSH public key with a user account
func (c *Client) AddUserSSHKey(ctx context.Context, username, title, keyContent string) error {
	username = strings.TrimSpace(username)
	keyContent = strings.TrimSpace(keyContent)
	if username == "" || keyContent == "" {
		return fmt.Errorf("username and keyContent must not be empty")
	}
	if title == "" {
		title = fmt.Sprintf("%s-key", username)
	}

	payload := map[string]interface{}{
		"title":     title,
		"key":       keyContent,
		"read_only": false,
	}

	endpoint := fmt.Sprintf("/admin/users/%s/keys", username)
	statusCode, respBody, err := c.doRequest(ctx, http.MethodPost, endpoint, payload)
	if err != nil {
		return err
	}
	// Forgejo returns 201 Created on success, 422/409 if key already exists
	if statusCode != http.StatusCreated && statusCode != http.StatusOK && statusCode != http.StatusConflict && statusCode != http.StatusUnprocessableEntity {
		return fmt.Errorf("failed adding ssh key for %s (status %d): %s", username, statusCode, string(respBody))
	}

	return nil
}

// EnsureOrg creates an organization if it does not already exist
func (c *Client) EnsureOrg(ctx context.Context, orgName string) error {
	orgName = strings.TrimSpace(orgName)
	if orgName == "" {
		return fmt.Errorf("organization name cannot be empty")
	}

	// Check if org exists
	statusCode, _, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/orgs/%s", orgName), nil)
	if err == nil && statusCode == http.StatusOK {
		return nil
	}

	payload := map[string]interface{}{
		"username":   orgName,
		"visibility": "public",
	}

	statusCode, respBody, err := c.doRequest(ctx, http.MethodPost, "/orgs", payload)
	if err != nil {
		return err
	}
	if statusCode != http.StatusCreated && statusCode != http.StatusOK && statusCode != http.StatusConflict {
		return fmt.Errorf("failed creating organization %s (status %d): %s", orgName, statusCode, string(respBody))
	}

	_ = c.EnsureTeam(ctx, orgName, "contributors")
	return nil
}

// EnsureTeam ensures an organization team exists, creating it with write permissions if missing.
func (c *Client) EnsureTeam(ctx context.Context, orgName, teamName string) error {
	orgName = strings.TrimSpace(orgName)
	teamName = strings.TrimSpace(teamName)
	if orgName == "" || teamName == "" {
		return fmt.Errorf("orgName and teamName cannot be empty")
	}

	var teams []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	teamsStatus, teamsBody, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/orgs/%s/teams", orgName), nil)
	if err == nil && teamsStatus == http.StatusOK {
		if err := json.Unmarshal(teamsBody, &teams); err == nil {
			for _, t := range teams {
				if strings.EqualFold(t.Name, teamName) {
					return nil
				}
			}
		}
	}

	payload := map[string]interface{}{
		"name":                      teamName,
		"permission":                "write",
		"can_create_org_repo":       true,
		"includes_all_repositories": true,
		"units":                     []string{"repo.code", "repo.issues", "repo.pulls"},
	}
	statusCode, respBody, err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/orgs/%s/teams", orgName), payload)
	if err != nil {
		return err
	}
	if statusCode != http.StatusCreated && statusCode != http.StatusOK && statusCode != http.StatusConflict {
		return fmt.Errorf("failed creating team %s in org %s (status %d): %s", teamName, orgName, statusCode, string(respBody))
	}
	return nil
}

// AddOrgMember adds a user to an organization, falling back to team membership if direct org PUT returns 405.
func (c *Client) AddOrgMember(ctx context.Context, orgName, username string) error {
	orgName = strings.TrimSpace(orgName)
	username = strings.TrimSpace(username)
	if orgName == "" || username == "" {
		return fmt.Errorf("orgName and username cannot be empty")
	}

	endpoint := fmt.Sprintf("/orgs/%s/members/%s", orgName, username)
	statusCode, respBody, err := c.doRequest(ctx, http.MethodPut, endpoint, nil)
	if err != nil {
		return err
	}
	if statusCode == http.StatusNoContent || statusCode == http.StatusOK || statusCode == http.StatusCreated {
		return nil
	}

	if statusCode == http.StatusMethodNotAllowed {
		// Forgejo manages org members through teams. Resolve teams and add to an unprivileged team (Contributors, Agents, or first non-owners team).
		var teams []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
		teamsStatus, teamsBody, teamsErr := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/orgs/%s/teams", orgName), nil)
		if teamsErr != nil {
			return teamsErr
		}
		if teamsStatus == http.StatusOK {
			if err := json.Unmarshal(teamsBody, &teams); err == nil && len(teams) > 0 {
				var targetTeamID int64
				// Prioritize explicitly unprivileged teams
				for _, t := range teams {
					if strings.EqualFold(t.Name, "contributors") || strings.EqualFold(t.Name, "agents") || strings.EqualFold(t.Name, "members") {
						targetTeamID = t.ID
						break
					}
				}
				// If no named non-owner team found, pick the first non-owners team
				if targetTeamID == 0 {
					for _, t := range teams {
						if !strings.EqualFold(t.Name, "owners") {
							targetTeamID = t.ID
							break
						}
					}
				}
				if targetTeamID == 0 {
					_ = c.EnsureTeam(ctx, orgName, "contributors")
					var refreshedTeams []struct {
						ID   int64  `json:"id"`
						Name string `json:"name"`
					}
					st, body, e := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/orgs/%s/teams", orgName), nil)
					if e == nil && st == http.StatusOK && json.Unmarshal(body, &refreshedTeams) == nil {
						for _, t := range refreshedTeams {
							if strings.EqualFold(t.Name, "contributors") {
								targetTeamID = t.ID
								break
							}
						}
					}
				}
				if targetTeamID == 0 {
					return fmt.Errorf("cannot add %s to org %s: only administrative 'Owners' team exists", username, orgName)
				}
				teamEndpoint := fmt.Sprintf("/teams/%d/members/%s", targetTeamID, username)
				teamStatus, teamBody, teamErr := c.doRequest(ctx, http.MethodPut, teamEndpoint, nil)
				if teamErr != nil {
					return teamErr
				}
				if teamStatus == http.StatusNoContent || teamStatus == http.StatusOK || teamStatus == http.StatusCreated {
					return nil
				}
				return fmt.Errorf("failed adding member %s to team %d (status %d): %s", username, targetTeamID, teamStatus, string(teamBody))
			}
		}
	}

	return fmt.Errorf("failed adding member %s to org %s (status %d): %s", username, orgName, statusCode, string(respBody))
}

// EnsureRepo creates a repository under an organization or user namespace if missing
func (c *Client) EnsureRepo(ctx context.Context, orgName, repoName, description string, autoInit bool) error {
	orgName = strings.TrimSpace(orgName)
	repoName = strings.TrimSpace(repoName)
	if orgName == "" || repoName == "" {
		return fmt.Errorf("orgName and repoName cannot be empty")
	}

	// Check if repo exists
	statusCode, _, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s", orgName, repoName), nil)
	if err == nil && statusCode == http.StatusOK {
		return nil
	}

	payload := map[string]interface{}{
		"name":        repoName,
		"description": description,
		"private":     false,
		"auto_init":   autoInit,
	}

	endpoint := fmt.Sprintf("/orgs/%s/repos", orgName)
	statusCode, respBody, err := c.doRequest(ctx, http.MethodPost, endpoint, payload)
	if err != nil {
		return err
	}
	if statusCode != http.StatusCreated && statusCode != http.StatusOK && statusCode != http.StatusConflict {
		return fmt.Errorf("failed creating repository %s/%s (status %d): %s", orgName, repoName, statusCode, string(respBody))
	}

	return nil
}

// RepoExists checks if a repository exists under an organization or user namespace
func (c *Client) RepoExists(ctx context.Context, orgName, repoName string) (bool, error) {
	orgName = strings.TrimSpace(orgName)
	repoName = strings.TrimSpace(repoName)
	if orgName == "" || repoName == "" {
		return false, fmt.Errorf("orgName and repoName cannot be empty")
	}

	statusCode, _, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s", orgName, repoName), nil)
	if err != nil {
		return false, err
	}
	if statusCode == http.StatusOK {
		return true, nil
	}
	if statusCode == http.StatusNotFound {
		return false, nil
	}
	return false, fmt.Errorf("unexpected status %d checking repository %s/%s", statusCode, orgName, repoName)
}

// DeleteRepo deletes a repository under an organization or user namespace
func (c *Client) DeleteRepo(ctx context.Context, orgName, repoName string) error {
	orgName = strings.TrimSpace(orgName)
	repoName = strings.TrimSpace(repoName)
	if orgName == "" || repoName == "" {
		return fmt.Errorf("orgName and repoName cannot be empty")
	}

	statusCode, respBody, err := c.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/repos/%s/%s", orgName, repoName), nil)
	if err != nil {
		return err
	}
	if statusCode == http.StatusNoContent || statusCode == http.StatusOK || statusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("failed deleting repository %s/%s (status %d): %s", orgName, repoName, statusCode, string(respBody))
}

// User represents a Forgejo user object
type User struct {
	ID       int64  `json:"id"`
	UserName string `json:"username"`
	FullName string `json:"full_name,omitempty"`
	Email    string `json:"email,omitempty"`
}

// Label represents a Forgejo issue label
type Label struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// Issue represents a Forgejo issue or task
type Issue struct {
	ID        int64     `json:"id"`
	Index     int64     `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"` // "open" or "closed"
	User      *User     `json:"user,omitempty"`
	Assignees []*User   `json:"assignees,omitempty"`
	Labels    []*Label  `json:"labels,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// PRBranch represents branch ref info for pull requests
type PRBranch struct {
	Label string `json:"label"`
	Ref   string `json:"ref"`
	Sha   string `json:"sha"`
}

// PullRequest represents a Forgejo pull request
type PullRequest struct {
	ID        int64     `json:"id"`
	Index     int64     `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	Head      *PRBranch `json:"head,omitempty"`
	Base      *PRBranch `json:"base,omitempty"`
	User      *User     `json:"user,omitempty"`
	Merged    bool      `json:"merged"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// PullReview represents a code review on a pull request
type PullReview struct {
	ID        int64     `json:"id"`
	User      *User     `json:"user,omitempty"`
	State     string    `json:"state"` // "APPROVED", "REQUEST_CHANGES", "COMMENT"
	Body      string    `json:"body"`
	Submitted time.Time `json:"submitted_at,omitempty"`
}

// FileContent represents file metadata and content retrieved from Forgejo
type FileContent struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	SHA         string `json:"sha"`
	Size        int64  `json:"size"`
	Content     string `json:"content"`
	Encoding    string `json:"encoding,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
}

// CommitStatusState constants for commit status checks
const (
	StatePending = "pending"
	StateSuccess = "success"
	StateError   = "error"
	StateFailure = "failure"
	StateWarning = "warning"
)

// CommitStatus represents a Forgejo commit status check
type CommitStatus struct {
	ID          int64     `json:"id"`
	State       string    `json:"state"`
	TargetURL   string    `json:"target_url,omitempty"`
	Description string    `json:"description,omitempty"`
	Context     string    `json:"context,omitempty"`
	URL         string    `json:"url,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
}

// CreateCommitStatusOptions holds options for posting a commit status
type CreateCommitStatusOptions struct {
	State       string `json:"state"`
	TargetURL   string `json:"target_url,omitempty"`
	Description string `json:"description,omitempty"`
	Context     string `json:"context,omitempty"`
}

// ListIssues returns issues and tasks matching filters from a repository
func (c *Client) ListIssues(ctx context.Context, owner, repo, state string, page, limit int) ([]*Issue, error) {
	if owner == "" {
		owner = "fleet"
	}
	if repo == "" {
		repo = "tasks"
	}
	if state == "" {
		state = "open"
	}
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 20
	}

	endpoint := fmt.Sprintf("/repos/%s/%s/issues?state=%s&page=%d&limit=%d",
		url.PathEscape(owner), url.PathEscape(repo), url.QueryEscape(state), page, limit)

	statusCode, respBody, err := c.doRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("failed listing issues for %s/%s (status %d): %s", owner, repo, statusCode, string(respBody))
	}

	var issues []*Issue
	if err := json.Unmarshal(respBody, &issues); err != nil {
		return nil, fmt.Errorf("failed unmarshaling issues response: %w", err)
	}

	return issues, nil
}

// CreateIssue creates a new issue or task in a repository
func (c *Client) CreateIssue(ctx context.Context, owner, repo, title, body string, labels, assignees []string) (*Issue, error) {
	if owner == "" {
		owner = "fleet"
	}
	if repo == "" {
		repo = "tasks"
	}
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("issue title cannot be empty")
	}

	payload := map[string]interface{}{
		"title": title,
		"body":  body,
	}
	if len(labels) > 0 {
		payload["labels"] = labels
	}
	if len(assignees) > 0 {
		payload["assignees"] = assignees
	}

	endpoint := fmt.Sprintf("/repos/%s/%s/issues", url.PathEscape(owner), url.PathEscape(repo))
	statusCode, respBody, err := c.doRequest(ctx, http.MethodPost, endpoint, payload)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusCreated && statusCode != http.StatusOK {
		return nil, fmt.Errorf("failed creating issue in %s/%s (status %d): %s", owner, repo, statusCode, string(respBody))
	}

	var issue Issue
	if err := json.Unmarshal(respBody, &issue); err != nil {
		return nil, fmt.Errorf("failed unmarshaling issue response: %w", err)
	}

	return &issue, nil
}

// CreatePullRequest opens a new pull request in a repository
func (c *Client) CreatePullRequest(ctx context.Context, owner, repo, title, body, head, base string) (*PullRequest, error) {
	if owner == "" {
		owner = "fleet"
	}
	if repo == "" {
		return nil, fmt.Errorf("repository name cannot be empty")
	}
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("pull request title cannot be empty")
	}
	if strings.TrimSpace(head) == "" {
		return nil, fmt.Errorf("head branch cannot be empty")
	}
	if base == "" {
		base = "main"
	}

	payload := map[string]interface{}{
		"title": title,
		"body":  body,
		"head":  head,
		"base":  base,
	}

	endpoint := fmt.Sprintf("/repos/%s/%s/pulls", url.PathEscape(owner), url.PathEscape(repo))
	statusCode, respBody, err := c.doRequest(ctx, http.MethodPost, endpoint, payload)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusCreated && statusCode != http.StatusOK {
		return nil, fmt.Errorf("failed creating pull request in %s/%s (status %d): %s", owner, repo, statusCode, string(respBody))
	}

	var pr PullRequest
	if err := json.Unmarshal(respBody, &pr); err != nil {
		return nil, fmt.Errorf("failed unmarshaling pull request response: %w", err)
	}

	return &pr, nil
}

// GetPullRequest fetches details for a specific pull request
func (c *Client) GetPullRequest(ctx context.Context, owner, repo string, index int64) (*PullRequest, error) {
	if owner == "" {
		owner = "fleet"
	}
	if repo == "" {
		return nil, fmt.Errorf("repository name cannot be empty")
	}

	endpoint := fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(repo), index)
	statusCode, respBody, err := c.doRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("failed getting pull request #%d for %s/%s (status %d): %s", index, owner, repo, statusCode, string(respBody))
	}

	var pr PullRequest
	if err := json.Unmarshal(respBody, &pr); err != nil {
		return nil, fmt.Errorf("failed unmarshaling pull request response: %w", err)
	}

	return &pr, nil
}

// ReviewPullRequest submits an approval, change request, or comment review on a pull request
func (c *Client) ReviewPullRequest(ctx context.Context, owner, repo string, index int64, event, body string) (*PullReview, error) {
	if owner == "" {
		owner = "fleet"
	}
	if repo == "" {
		return nil, fmt.Errorf("repository name cannot be empty")
	}

	normalizedEvent := strings.ToUpper(strings.TrimSpace(event))
	if normalizedEvent == "" {
		normalizedEvent = "COMMENT"
	}

	payload := map[string]interface{}{
		"event": normalizedEvent,
		"body":  body,
	}

	endpoint := fmt.Sprintf("/repos/%s/%s/pulls/%d/reviews", url.PathEscape(owner), url.PathEscape(repo), index)
	statusCode, respBody, err := c.doRequest(ctx, http.MethodPost, endpoint, payload)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK && statusCode != http.StatusCreated {
		return nil, fmt.Errorf("failed reviewing pull request #%d in %s/%s (status %d): %s", index, owner, repo, statusCode, string(respBody))
	}

	var review PullReview
	if err := json.Unmarshal(respBody, &review); err != nil {
		return nil, fmt.Errorf("failed unmarshaling review response: %w", err)
	}

	return &review, nil
}

// GetFile retrieves file contents and metadata from a repository at a given ref
func (c *Client) GetFile(ctx context.Context, owner, repo, filePath, ref string) (*FileContent, error) {
	if owner == "" {
		owner = "fleet"
	}
	if repo == "" {
		return nil, fmt.Errorf("repository name cannot be empty")
	}
	filePath = strings.TrimPrefix(filePath, "/")
	if filePath == "" {
		return nil, fmt.Errorf("filePath cannot be empty")
	}
	if ref == "" {
		ref = "main"
	}

	endpoint := fmt.Sprintf("/repos/%s/%s/contents/%s?ref=%s",
		url.PathEscape(owner), url.PathEscape(repo), filePath, url.QueryEscape(ref))

	statusCode, respBody, err := c.doRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("failed reading file %s from %s/%s (status %d): %s", filePath, owner, repo, statusCode, string(respBody))
	}

	var file FileContent
	if err := json.Unmarshal(respBody, &file); err != nil {
		return nil, fmt.Errorf("failed unmarshaling file response: %w", err)
	}

	// Decode Base64 if encoded
	if strings.EqualFold(file.Encoding, "base64") && file.Content != "" {
		cleanB64 := strings.ReplaceAll(file.Content, "\n", "")
		if decoded, err := base64.StdEncoding.DecodeString(cleanB64); err == nil {
			file.Content = string(decoded)
		}
	}

	return &file, nil
}

// CreateCommitStatus creates or updates a commit status check on a commit SHA
func (c *Client) CreateCommitStatus(ctx context.Context, owner, repo, sha string, opt CreateCommitStatusOptions) (*CommitStatus, error) {
	if owner == "" {
		owner = "fleet"
	}
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return nil, fmt.Errorf("repository name cannot be empty")
	}
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return nil, fmt.Errorf("commit sha cannot be empty")
	}

	if opt.State == "" {
		opt.State = StatePending
	}
	if opt.Context == "" {
		opt.Context = "sonarqube/quality-gate"
	}

	endpoint := fmt.Sprintf("/repos/%s/%s/statuses/%s", url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(sha))
	statusCode, respBody, err := c.doRequest(ctx, http.MethodPost, endpoint, opt)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK && statusCode != http.StatusCreated {
		return nil, fmt.Errorf("failed creating commit status for %s/%s@%s (status %d): %s", owner, repo, sha, statusCode, string(respBody))
	}

	var status CommitStatus
	if err := json.Unmarshal(respBody, &status); err != nil {
		return nil, fmt.Errorf("failed unmarshaling commit status response: %w", err)
	}

	return &status, nil
}
