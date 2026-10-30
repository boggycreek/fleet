// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package lifecycle

import (
	"bytes"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
)

func TestProvisioningReportAndSteps(t *testing.T) {
	report := NewProvisioningReport("test-agent", "provision")
	if report.AgentName != "test-agent" || report.Action != "provision" {
		t.Fatalf("unexpected report init: %+v", report)
	}
	if report.HasErrors() {
		t.Errorf("expected HasErrors to be false on empty report")
	}
	if report.HasWarnings() {
		t.Errorf("expected HasWarnings to be false on empty report")
	}

	// 1. Add StatusOK step
	report.AddStep("Step 1", StatusOK, "All systems nominal", nil)
	if report.SuccessCount != 1 || report.HasErrors() || report.HasWarnings() {
		t.Errorf("unexpected counts after StatusOK: %+v", report)
	}

	// 2. Add StatusWarning step
	report.AddStep("Step 2", StatusWarning, "Degraded performance", errors.New("slow network"))
	if report.WarningCount != 1 || !report.HasWarnings() || report.HasErrors() {
		t.Errorf("unexpected counts after StatusWarning: %+v", report)
	}

	// 3. Add StatusSkipped step
	report.AddStep("Step 3", StatusSkipped, "Optional dependency offline", errors.New("offline"))
	if report.SkippedCount != 1 || report.HasErrors() {
		t.Errorf("unexpected counts after StatusSkipped: %+v", report)
	}

	// 4. Add StatusError step
	report.AddStep("Step 4", StatusError, "Failed to execute", errors.New("hard failure"))
	if report.ErrorCount != 1 || !report.HasErrors() {
		t.Errorf("unexpected counts after StatusError: %+v", report)
	}

	// Format report
	formatted := FormatReport(report)
	if !strings.Contains(formatted, "[✓]") ||
		!strings.Contains(formatted, "[!]") ||
		!strings.Contains(formatted, "[-]") ||
		!strings.Contains(formatted, "[✗]") {
		t.Errorf("FormatReport missing expected status symbols:\n%s", formatted)
	}
	if !strings.Contains(formatted, "Step 1") || !strings.Contains(formatted, "Failed to execute") {
		t.Errorf("FormatReport missing step names or messages:\n%s", formatted)
	}

	// Format empty / nil report
	if FormatReport(nil) != "" {
		t.Errorf("expected empty string for nil report")
	}
	if FormatReport(&ProvisioningReport{}) != "" {
		t.Errorf("expected empty string for empty report")
	}
}

func TestWarnOnErr(t *testing.T) {
	// Case 1: nil error
	var b bytes.Buffer
	if WarnOnErr(&b, nil, "step a") {
		t.Errorf("expected WarnOnErr to return false on nil error")
	}
	if b.Len() > 0 {
		t.Errorf("expected no output on nil error, got: %s", b.String())
	}

	// Case 2: nil writer
	if WarnOnErr(nil, errors.New("err"), "step b") {
		t.Errorf("expected WarnOnErr to return false on nil writer")
	}

	// Case 3: valid error and writer
	b.Reset()
	if !WarnOnErr(&b, errors.New("disk full"), "writing cache") {
		t.Errorf("expected WarnOnErr to return true on non-nil error")
	}
	if !strings.Contains(b.String(), "fleet warning: writing cache: disk full") {
		t.Errorf("unexpected output from WarnOnErr: %s", b.String())
	}
}

func TestIsOfflineError(t *testing.T) {
	if IsOfflineError(nil) {
		t.Errorf("expected nil error to not be offline")
	}

	// net.OpError
	opErr := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: os.ErrDeadlineExceeded,
	}
	if !IsOfflineError(opErr) {
		t.Errorf("expected OpError to be recognized as offline")
	}

	// String matches
	matches := []string{
		"dial tcp 127.0.0.1:80: connection refused",
		"connect: connection refused",
		"no route to host",
		"i/o timeout",
		"context deadline exceeded",
		"service offline",
		"dial tcp: lookup gitea: no such host",
	}
	for _, m := range matches {
		if !IsOfflineError(errors.New(m)) {
			t.Errorf("expected %q to be recognized as offline", m)
		}
	}

	// Non-matching
	nonMatches := []string{
		"invalid credentials",
		"user already exists",
		"permission denied",
		"unknown command",
	}
	for _, nm := range nonMatches {
		if IsOfflineError(errors.New(nm)) {
			t.Errorf("expected %q to NOT be recognized as offline", nm)
		}
	}
}
