// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package lifecycle

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
)

// StepStatus indicates the outcome of an individual provisioning or deprovisioning step.
type StepStatus string

const (
	// StatusOK indicates the step completed successfully.
	StatusOK StepStatus = "OK"
	// StatusWarning indicates the step encountered a non-fatal warning or degraded state.
	StatusWarning StepStatus = "WARNING"
	// StatusError indicates the step failed with an error.
	StatusError StepStatus = "ERROR"
	// StatusSkipped indicates the step was skipped (e.g. an optional service was offline).
	StatusSkipped StepStatus = "SKIPPED"
)

// ProvisioningStep records the outcome and underlying error of a single lifecycle action.
type ProvisioningStep struct {
	Name    string     `json:"name"`
	Status  StepStatus `json:"status"`
	Message string     `json:"message"`
	Err     error      `json:"-"`
}

// ProvisioningReport tracks the cumulative results of multi-step agent provisioning or deprovisioning.
type ProvisioningReport struct {
	AgentName    string             `json:"agent_name"`
	Action       string             `json:"action"`
	Steps        []ProvisioningStep `json:"steps"`
	SuccessCount int                `json:"success_count"`
	WarningCount int                `json:"warning_count"`
	ErrorCount   int                `json:"error_count"`
	SkippedCount int                `json:"skipped_count"`
}

// NewProvisioningReport initializes a new report for an agent lifecycle action.
func NewProvisioningReport(agentName, action string) *ProvisioningReport {
	return &ProvisioningReport{
		AgentName: agentName,
		Action:    action,
		Steps:     make([]ProvisioningStep, 0),
	}
}

// AddStep appends a step result to the report and increments corresponding counters.
func (r *ProvisioningReport) AddStep(name string, status StepStatus, message string, err error) {
	r.Steps = append(r.Steps, ProvisioningStep{
		Name:    name,
		Status:  status,
		Message: message,
		Err:     err,
	})
	switch status {
	case StatusOK:
		r.SuccessCount++
	case StatusWarning:
		r.WarningCount++
	case StatusError:
		r.ErrorCount++
	case StatusSkipped:
		r.SkippedCount++
	}
}

// HasErrors returns true if one or more steps failed with StatusError.
func (r *ProvisioningReport) HasErrors() bool {
	return r.ErrorCount > 0
}

// HasWarnings returns true if one or more steps finished with StatusWarning.
func (r *ProvisioningReport) HasWarnings() bool {
	return r.WarningCount > 0
}

// FormatReport formats the provisioning report as human-readable text.
func FormatReport(report *ProvisioningReport) string {
	if report == nil || len(report.Steps) == 0 {
		return ""
	}

	var b bytes.Buffer
	for _, step := range report.Steps {
		symbol := "[✓]"
		switch step.Status {
		case StatusWarning:
			symbol = "[!]"
		case StatusSkipped:
			symbol = "[-]"
		case StatusError:
			symbol = "[✗]"
		}
		fmt.Fprintf(&b, "  %-3s %-28s : %s\n", symbol, step.Name, step.Message)
	}
	return b.String()
}

// WarnOnErr writes a warning message to w if err is non-nil. Returns true if a warning was emitted.
func WarnOnErr(w io.Writer, err error, step string) bool {
	if err != nil && w != nil {
		_, _ = fmt.Fprintf(w, "fleet warning: %s: %v\n", step, err)
		return true
	}
	return false
}

// IsOfflineError returns true if the error indicates a network connection failure or timeout.
func IsOfflineError(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "connection refused") ||
		strings.Contains(s, "no route to host") ||
		strings.Contains(s, "i/o timeout") ||
		strings.Contains(s, "context deadline exceeded") ||
		strings.Contains(s, "offline") ||
		strings.Contains(s, "dial tcp") ||
		strings.Contains(s, "connect: connection refused")
}
