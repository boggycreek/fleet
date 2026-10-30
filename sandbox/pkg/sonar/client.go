// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package sonar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ClientConfig holds configuration parameters for the SonarQube API client.
type ClientConfig struct {
	BaseURL   string
	Token     string
	AdminUser string
	AdminPass string
	Timeout   time.Duration
}

// Client interacts with the SonarQube REST API.
type Client struct {
	baseURL    string
	token      string
	adminUser  string
	adminPass  string
	httpClient *http.Client
}

// SystemStatus represents the runtime status of the SonarQube instance.
type SystemStatus struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Status  string `json:"status"` // "UP", "STARTING", "DOWN", "RESTARTING"
}

// QualityGateStatus represents project quality gate evaluation results.
type QualityGateStatus struct {
	Status     string                 `json:"status"` // "OK", "WARN", "ERROR"
	Conditions []QualityGateCondition `json:"conditions,omitempty"`
}

// QualityGateCondition defines an individual threshold condition evaluation.
type QualityGateCondition struct {
	MetricKey      string `json:"metricKey"`
	Status         string `json:"status"` // "OK", "WARN", "ERROR"
	ActualValue    string `json:"actualValue"`
	ErrorThreshold string `json:"errorThreshold"`
}

type qualityGateResponseWrapper struct {
	ProjectStatus QualityGateStatus `json:"projectStatus"`
}

// Issue represents a static code analysis finding.
type Issue struct {
	Key       string `json:"key"`
	Rule      string `json:"rule"`
	Severity  string `json:"severity"` // "BLOCKER", "CRITICAL", "MAJOR", "MINOR", "INFO"
	Component string `json:"component"`
	Line      int    `json:"line"`
	Message   string `json:"message"`
	Type      string `json:"type"` // "BUG", "VULNERABILITY", "CODE_SMELL", "SECURITY_HOTSPOT"
	Status    string `json:"status"`
}

// IssuesResponse wraps the list of findings returned by the issues search API.
type IssuesResponse struct {
	Total  int     `json:"total"`
	Issues []Issue `json:"issues"`
}

// Measure represents a metric measure.
type Measure struct {
	Metric string `json:"metric"`
	Value  string `json:"value"`
}

type componentMeasuresWrapper struct {
	Component struct {
		Key      string    `json:"key"`
		Name     string    `json:"name"`
		Measures []Measure `json:"measures"`
	} `json:"component"`
}

type generateTokenResponse struct {
	Login string `json:"login"`
	Name  string `json:"name"`
	Token string `json:"token"`
}

// NewClient instantiates a SonarQube API client.
func NewClient(cfg ClientConfig) *Client {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:9000"
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	return &Client{
		baseURL:   baseURL,
		token:     cfg.Token,
		adminUser: cfg.AdminUser,
		adminPass: cfg.AdminPass,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

const maxResponseBody = 10 << 20 // 10MB limit

func (c *Client) doRequest(ctx context.Context, method, path string, query url.Values, body io.Reader, result any, acceptableStatuses ...int) error {
	reqURL := c.baseURL + path
	if len(query) > 0 {
		reqURL = fmt.Sprintf("%s?%s", reqURL, query.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return fmt.Errorf("failed creating %s %s request: %w", method, path, err)
	}

	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	c.authenticate(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed executing %s %s request: %w", method, path, err)
	}
	defer resp.Body.Close()

	if !isAcceptableStatus(resp.StatusCode, acceptableStatuses...) {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		return fmt.Errorf("%s %s failed with HTTP %d: %s", method, path, resp.StatusCode, string(bodyBytes))
	}

	if result != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(result); err != nil {
			return fmt.Errorf("failed decoding %s %s response: %w", method, path, err)
		}
	}

	return nil
}

func isAcceptableStatus(status int, acceptableStatuses ...int) bool {
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return true
	}
	for _, code := range acceptableStatuses {
		if status == code {
			return true
		}
	}
	return false
}

// GetSystemStatus queries the health and operational state of SonarQube.
func (c *Client) GetSystemStatus(ctx context.Context) (*SystemStatus, error) {
	var status SystemStatus
	if err := c.doRequest(ctx, http.MethodGet, "/api/system/status", nil, nil, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// GetProjectQualityGate retrieves the Quality Gate status for a project.
func (c *Client) GetProjectQualityGate(ctx context.Context, projectKey string) (*QualityGateStatus, error) {
	query := url.Values{}
	query.Set("projectKey", projectKey)
	var wrapper qualityGateResponseWrapper
	if err := c.doRequest(ctx, http.MethodGet, "/api/qualitygates/project_status", query, nil, &wrapper); err != nil {
		return nil, err
	}
	return &wrapper.ProjectStatus, nil
}

// GetIssues retrieves static analysis issues matching the given filters.
func (c *Client) GetIssues(ctx context.Context, projectKey string, severity string, issueType string) (*IssuesResponse, error) {
	query := url.Values{}
	if projectKey != "" {
		query.Set("projectKeys", projectKey)
	}
	if severity != "" {
		query.Set("severities", severity)
	}
	if issueType != "" {
		query.Set("types", issueType)
	}
	var issuesResp IssuesResponse
	if err := c.doRequest(ctx, http.MethodGet, "/api/issues/search", query, nil, &issuesResp); err != nil {
		return nil, err
	}
	return &issuesResp, nil
}

// CreateProject registers a new project in SonarQube.
func (c *Client) CreateProject(ctx context.Context, projectKey, name string) error {
	params := url.Values{}
	params.Set("project", projectKey)
	params.Set("name", name)
	return c.doRequest(ctx, http.MethodPost, "/api/projects/create", nil, strings.NewReader(params.Encode()), nil, http.StatusBadRequest)
}

// GetComponentMeasures retrieves requested metric values for a project or component.
func (c *Client) GetComponentMeasures(ctx context.Context, componentKey string, metricKeys []string) ([]Measure, error) {
	query := url.Values{}
	query.Set("component", componentKey)
	query.Set("metricKeys", strings.Join(metricKeys, ","))
	var wrapper componentMeasuresWrapper
	if err := c.doRequest(ctx, http.MethodGet, "/api/measures/component", query, nil, &wrapper); err != nil {
		return nil, err
	}
	return wrapper.Component.Measures, nil
}

// EnsureUser creates a user account in SonarQube if it does not already exist.
func (c *Client) EnsureUser(ctx context.Context, login, password, name, email string) error {
	params := url.Values{}
	params.Set("login", login)
	params.Set("password", password)
	if name == "" {
		name = login
	}
	params.Set("name", name)
	if email != "" {
		params.Set("email", email)
	}
	return c.doRequest(ctx, http.MethodPost, "/api/users/create", nil, strings.NewReader(params.Encode()), nil, http.StatusBadRequest)
}

// GenerateUserToken creates an analysis or user token for the specified user login.
func (c *Client) GenerateUserToken(ctx context.Context, login, tokenName string) (string, error) {
	_ = c.RevokeUserToken(ctx, login, tokenName)

	params := url.Values{}
	params.Set("login", login)
	params.Set("name", tokenName)
	var tokenResp generateTokenResponse
	if err := c.doRequest(ctx, http.MethodPost, "/api/user_tokens/generate", nil, strings.NewReader(params.Encode()), &tokenResp); err != nil {
		return "", err
	}
	return tokenResp.Token, nil
}

// RevokeUserToken revokes a user token by name.
func (c *Client) RevokeUserToken(ctx context.Context, login, tokenName string) error {
	params := url.Values{}
	if login != "" {
		params.Set("login", login)
	}
	params.Set("name", tokenName)
	return c.doRequest(ctx, http.MethodPost, "/api/user_tokens/revoke", nil, strings.NewReader(params.Encode()), nil, http.StatusBadRequest, http.StatusNotFound)
}

// DeactivateUser deactivates a user account in SonarQube.
func (c *Client) DeactivateUser(ctx context.Context, login string) error {
	params := url.Values{}
	params.Set("login", login)
	return c.doRequest(ctx, http.MethodPost, "/api/users/deactivate", nil, strings.NewReader(params.Encode()), nil, http.StatusBadRequest, http.StatusNotFound)
}

// ChangePassword updates a user password, optionally verifying the previous password.
func (c *Client) ChangePassword(ctx context.Context, login, previousPassword, newPassword string) error {
	params := url.Values{}
	params.Set("login", login)
	if previousPassword != "" {
		params.Set("previousPassword", previousPassword)
	}
	params.Set("password", newPassword)
	return c.doRequest(ctx, http.MethodPost, "/api/users/change_password", nil, strings.NewReader(params.Encode()), nil, http.StatusNoContent)
}

type projectSearchResponse struct {
	Components []struct {
		Key string `json:"key"`
	} `json:"components"`
}

// ProjectExists checks if a project with the given key exists in SonarQube.
func (c *Client) ProjectExists(ctx context.Context, projectKey string) (bool, error) {
	query := url.Values{}
	query.Set("projects", projectKey)
	var resp projectSearchResponse
	if err := c.doRequest(ctx, http.MethodGet, "/api/projects/search", query, nil, &resp); err != nil {
		return false, err
	}
	for _, comp := range resp.Components {
		if comp.Key == projectKey {
			return true, nil
		}
	}
	return false, nil
}

// DeleteProject deletes a project in SonarQube by key.
func (c *Client) DeleteProject(ctx context.Context, projectKey string) error {
	params := url.Values{}
	params.Set("project", projectKey)
	return c.doRequest(ctx, http.MethodPost, "/api/projects/delete", nil, strings.NewReader(params.Encode()), nil, http.StatusNotFound)
}

// GenerateProjectToken creates a scoped project analysis token for the specified projectKey.
func (c *Client) GenerateProjectToken(ctx context.Context, projectKey, tokenName string) (string, error) {
	_ = c.RevokeUserToken(ctx, "", tokenName)

	params := url.Values{}
	params.Set("name", tokenName)
	params.Set("projectKey", projectKey)
	params.Set("type", "PROJECT_ANALYSIS_TOKEN")
	var resp generateTokenResponse
	if err := c.doRequest(ctx, http.MethodPost, "/api/user_tokens/generate", nil, strings.NewReader(params.Encode()), &resp); err != nil {
		return "", err
	}
	return resp.Token, nil
}

func (c *Client) authenticate(req *http.Request) {
	if c.token != "" {
		req.SetBasicAuth(c.token, "")
	} else if c.adminUser != "" {
		req.SetBasicAuth(c.adminUser, c.adminPass)
	}
}
