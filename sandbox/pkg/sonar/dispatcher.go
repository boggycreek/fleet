// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package sonar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/boggycreek/sandbox/pkg/forgejo"
)

// CommitStatusPoster abstracts the Forgejo client for posting commit status checks.
type CommitStatusPoster interface {
	CreateCommitStatus(ctx context.Context, owner, repo, sha string, opt forgejo.CreateCommitStatusOptions) (*forgejo.CommitStatus, error)
}

// FormatQualityGateStatus formats a SonarQube QualityGateStatus into Forgejo CreateCommitStatusOptions.
func FormatQualityGateStatus(qg *QualityGateStatus, sonarBaseURL, projectKey string) forgejo.CreateCommitStatusOptions {
	targetURL := strings.TrimRight(sonarBaseURL, "/")
	if projectKey != "" {
		targetURL = fmt.Sprintf("%s/dashboard?id=%s", targetURL, url.QueryEscape(projectKey))
	}

	state := forgejo.StatePending
	desc := "Quality Gate evaluation pending"

	if qg != nil {
		switch strings.ToUpper(qg.Status) {
		case "OK", "PASSED":
			state = forgejo.StateSuccess
			desc = "Quality Gate passed"
		case "WARN", "WARNING":
			state = forgejo.StateWarning
			desc = "Quality Gate passed with warnings"
		case "ERROR", "FAILED":
			state = forgejo.StateFailure
			desc = "Quality Gate failed"
		}
	}

	return forgejo.CreateCommitStatusOptions{
		State:       state,
		TargetURL:   targetURL,
		Description: desc,
		Context:     "sonarqube/quality-gate",
	}
}

// DispatchQualityGateStatus evaluates Quality Gate for a project and dispatches status to Forgejo.
func DispatchQualityGateStatus(ctx context.Context, poster CommitStatusPoster, owner, repo, sha, sonarBaseURL, projectKey string, qg *QualityGateStatus) (*forgejo.CommitStatus, error) {
	if poster == nil {
		return nil, fmt.Errorf("commit status poster cannot be nil")
	}
	opt := FormatQualityGateStatus(qg, sonarBaseURL, projectKey)
	return poster.CreateCommitStatus(ctx, owner, repo, sha, opt)
}

// WebhookPayload represents SonarQube analysis completion webhook payload.
type WebhookPayload struct {
	ServerURL  string `json:"serverUrl"`
	TaskID     string `json:"taskId"`
	Status     string `json:"status"`
	AnalysedAt string `json:"analysedAt"`
	Revision   string `json:"revision"`
	Project    struct {
		Key  string `json:"key"`
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"project"`
	QualityGate struct {
		Name       string                 `json:"name"`
		Status     string                 `json:"status"`
		Conditions []QualityGateCondition `json:"conditions,omitempty"`
	} `json:"qualityGate"`
}

// ParseWebhook parses raw JSON byte slice into WebhookPayload struct.
func ParseWebhook(body []byte) (*WebhookPayload, error) {
	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed unmarshaling SonarQube webhook payload: %w", err)
	}
	return &payload, nil
}

// DispatchWebhookStatus dispatches commit status to Forgejo from a SonarQube WebhookPayload.
func DispatchWebhookStatus(ctx context.Context, poster CommitStatusPoster, owner, repo string, payload *WebhookPayload) (*forgejo.CommitStatus, error) {
	if poster == nil {
		return nil, fmt.Errorf("commit status poster cannot be nil")
	}
	if payload == nil {
		return nil, fmt.Errorf("webhook payload cannot be nil")
	}

	sha := strings.TrimSpace(payload.Revision)
	if sha == "" {
		return nil, fmt.Errorf("webhook payload does not contain commit revision (sha)")
	}

	qg := &QualityGateStatus{
		Status:     payload.QualityGate.Status,
		Conditions: payload.QualityGate.Conditions,
	}

	sonarBase := payload.ServerURL
	projectKey := payload.Project.Key
	if payload.Project.URL != "" && sonarBase == "" {
		sonarBase = payload.Project.URL
	}

	opt := FormatQualityGateStatus(qg, sonarBase, projectKey)
	if payload.Project.URL != "" {
		opt.TargetURL = payload.Project.URL
	}

	return poster.CreateCommitStatus(ctx, owner, repo, sha, opt)
}
