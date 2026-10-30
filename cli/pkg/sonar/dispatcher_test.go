// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package sonar

import (
	"context"
	"errors"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/forgejo"
)

type mockPoster struct {
	err  error
	last struct {
		owner string
		repo  string
		sha   string
		opt   forgejo.CreateCommitStatusOptions
	}
}

func (m *mockPoster) CreateCommitStatus(ctx context.Context, owner, repo, sha string, opt forgejo.CreateCommitStatusOptions) (*forgejo.CommitStatus, error) {
	if m.err != nil {
		return nil, m.err
	}
	m.last.owner = owner
	m.last.repo = repo
	m.last.sha = sha
	m.last.opt = opt
	return &forgejo.CommitStatus{
		ID:          1,
		State:       opt.State,
		TargetURL:   opt.TargetURL,
		Description: opt.Description,
		Context:     opt.Context,
	}, nil
}

func TestFormatQualityGateStatus(t *testing.T) {
	tests := []struct {
		name          string
		qg            *QualityGateStatus
		sonarBase     string
		projectKey    string
		expectedState string
		expectedDesc  string
		expectedURL   string
	}{
		{
			name:          "Passed Quality Gate",
			qg:            &QualityGateStatus{Status: "OK"},
			sonarBase:     "http://127.0.0.1:9000/",
			projectKey:    "fleet-tools",
			expectedState: forgejo.StateSuccess,
			expectedDesc:  "Quality Gate passed",
			expectedURL:   "http://127.0.0.1:9000/dashboard?id=fleet-tools",
		},
		{
			name:          "Warning Quality Gate",
			qg:            &QualityGateStatus{Status: "WARN"},
			sonarBase:     "http://127.0.0.1:9000",
			projectKey:    "fleet-app",
			expectedState: forgejo.StateWarning,
			expectedDesc:  "Quality Gate passed with warnings",
			expectedURL:   "http://127.0.0.1:9000/dashboard?id=fleet-app",
		},
		{
			name:          "Error Quality Gate",
			qg:            &QualityGateStatus{Status: "ERROR"},
			sonarBase:     "http://127.0.0.1:9000",
			projectKey:    "fleet-core",
			expectedState: forgejo.StateFailure,
			expectedDesc:  "Quality Gate failed",
			expectedURL:   "http://127.0.0.1:9000/dashboard?id=fleet-core",
		},
		{
			name:          "Nil Quality Gate",
			qg:            nil,
			sonarBase:     "http://127.0.0.1:9000",
			projectKey:    "",
			expectedState: forgejo.StatePending,
			expectedDesc:  "Quality Gate evaluation pending",
			expectedURL:   "http://127.0.0.1:9000",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opt := FormatQualityGateStatus(tc.qg, tc.sonarBase, tc.projectKey)
			if opt.State != tc.expectedState {
				t.Errorf("expected state %s, got %s", tc.expectedState, opt.State)
			}
			if opt.Description != tc.expectedDesc {
				t.Errorf("expected desc %s, got %s", tc.expectedDesc, opt.Description)
			}
			if opt.TargetURL != tc.expectedURL {
				t.Errorf("expected target URL %s, got %s", tc.expectedURL, opt.TargetURL)
			}
			if opt.Context != "sonarqube/quality-gate" {
				t.Errorf("expected context sonarqube/quality-gate, got %s", opt.Context)
			}
		})
	}
}

func TestDispatchQualityGateStatus(t *testing.T) {
	ctx := context.Background()

	// Nil poster check
	if _, err := DispatchQualityGateStatus(ctx, nil, "fleet", "tools", "sha123", "http://127.0.0.1:9000", "proj", &QualityGateStatus{Status: "OK"}); err == nil {
		t.Fatal("expected error on nil poster")
	}

	// Success check
	mp := &mockPoster{}
	qg := &QualityGateStatus{Status: "OK"}
	st, err := DispatchQualityGateStatus(ctx, mp, "fleet", "tools", "sha123", "http://127.0.0.1:9000", "fleet-tools", qg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.State != forgejo.StateSuccess || mp.last.sha != "sha123" {
		t.Errorf("unexpected status or mock record: %+v", mp.last)
	}

	// Poster error propagation check
	mpErr := &mockPoster{err: errors.New("poster error")}
	if _, err := DispatchQualityGateStatus(ctx, mpErr, "fleet", "tools", "sha123", "http://127.0.0.1:9000", "fleet-tools", qg); err == nil {
		t.Fatal("expected error propagation from poster")
	}
}

func TestParseWebhookAndDispatchWebhookStatus(t *testing.T) {
	ctx := context.Background()

	rawJSON := []byte(`{
		"serverUrl": "http://127.0.0.1:9000",
		"taskId": "AX123",
		"status": "SUCCESS",
		"revision": "abc1234567890def",
		"project": {
			"key": "fleet-tools",
			"name": "Fleet Tools",
			"url": "http://127.0.0.1:9000/dashboard?id=fleet-tools"
		},
		"qualityGate": {
			"name": "Sonar way",
			"status": "OK"
		}
	}`)

	payload, err := ParseWebhook(rawJSON)
	if err != nil {
		t.Fatalf("failed parsing webhook: %v", err)
	}
	if payload.Revision != "abc1234567890def" || payload.Project.Key != "fleet-tools" {
		t.Errorf("unexpected payload contents: %+v", payload)
	}

	// Invalid webhook JSON
	if _, err := ParseWebhook([]byte("invalid json")); err == nil {
		t.Fatal("expected error parsing bad json")
	}

	// DispatchWebhookStatus checks
	mp := &mockPoster{}
	st, err := DispatchWebhookStatus(ctx, mp, "fleet", "tools", payload)
	if err != nil {
		t.Fatalf("failed dispatching webhook status: %v", err)
	}
	if st.State != forgejo.StateSuccess || mp.last.sha != "abc1234567890def" {
		t.Errorf("unexpected status dispatched: %+v", st)
	}

	// Error conditions for DispatchWebhookStatus
	if _, err := DispatchWebhookStatus(ctx, nil, "fleet", "tools", payload); err == nil {
		t.Fatal("expected error on nil poster")
	}
	if _, err := DispatchWebhookStatus(ctx, mp, "fleet", "tools", nil); err == nil {
		t.Fatal("expected error on nil payload")
	}

	emptyShaPayload := &WebhookPayload{}
	if _, err := DispatchWebhookStatus(ctx, mp, "fleet", "tools", emptyShaPayload); err == nil {
		t.Fatal("expected error on empty revision sha")
	}
}
