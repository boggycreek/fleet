// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package engine

import (
	"fmt"
	"net/http"
	"strings"
)

// FailureClass categorizes LLM provider and network failures for deterministic
// retry, backoff, and fallback handling.
type FailureClass string

const (
	FailureClassTransient   FailureClass = "transient"    // 5xx, network drops, timeouts -> backoff & retry
	FailureClassRateLimit   FailureClass = "rate_limit"   // 429 -> slide window & backoff
	FailureClassCredential  FailureClass = "credential"   // 401, bare 403 -> invalid auth key, no backoff
	FailureClassQuota       FailureClass = "quota"        // 402, insufficient_quota -> out of credits
	FailureClassModelAccess FailureClass = "model_access" // 404 model not found or forbidden
	FailureClassCallerFault FailureClass = "caller_fault" // 400 bad request -> prompt/format defect, do not retry
)

// APIError represents a structured, categorized failure returned by an LLM inference endpoint.
type APIError struct {
	StatusCode int          `json:"status_code"`
	Type       string       `json:"type,omitempty"`
	Message    string       `json:"message"`
	Class      FailureClass `json:"class"`
}

// Error formats the APIError as a string including status code, class, and message.
func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Type != "" {
		return fmt.Sprintf("api error (status %d, class %s, type %s): %s", e.StatusCode, e.Class, e.Type, e.Message)
	}
	return fmt.Sprintf("api error (status %d, class %s): %s", e.StatusCode, e.Class, e.Message)
}

// IsTransient returns true if the failure is transient (e.g. 5xx, timeout, server crash) and should be retried.
func (e *APIError) IsTransient() bool {
	return e != nil && e.Class == FailureClassTransient
}

// IsRateLimit returns true if the error is due to rate limiting (429).
func (e *APIError) IsRateLimit() bool {
	return e != nil && e.Class == FailureClassRateLimit
}

// IsCredential returns true if the error is due to missing or invalid credentials (401 or auth-related 403).
func (e *APIError) IsCredential() bool {
	return e != nil && e.Class == FailureClassCredential
}

// IsQuota returns true if credits or quota are exhausted (402 or insufficient_quota).
func (e *APIError) IsQuota() bool {
	return e != nil && e.Class == FailureClassQuota
}

// IsModelAccess returns true if the model does not exist or access is forbidden (404 or model permission denied).
func (e *APIError) IsModelAccess() bool {
	return e != nil && e.Class == FailureClassModelAccess
}

// IsCallerFault returns true if the request was malformed or rejected due to caller defect (400).
func (e *APIError) IsCallerFault() bool {
	return e != nil && e.Class == FailureClassCallerFault
}

// ClassifyFailure categorizes an HTTP response from an LLM inference endpoint into a FailureClass
// based on HTTP status code, provider error type, and error message content.
func ClassifyFailure(statusCode int, errType, errMsg string) FailureClass {
	lowerType := strings.ToLower(errType)
	lowerMsg := strings.ToLower(errMsg)

	// Check message/type content first for specific sub-categories
	if strings.Contains(lowerMsg, "insufficient_quota") ||
		strings.Contains(lowerType, "insufficient_quota") ||
		strings.Contains(lowerMsg, "quota exceeded") ||
		strings.Contains(lowerMsg, "out of credits") ||
		strings.Contains(lowerMsg, "credit balance") ||
		strings.Contains(lowerMsg, "balance has been exhausted") {
		return FailureClassQuota
	}

	if strings.Contains(lowerMsg, "model not found") ||
		strings.Contains(lowerType, "model_not_found") ||
		strings.Contains(lowerMsg, "does not exist") ||
		strings.Contains(lowerMsg, "access to model") ||
		strings.Contains(lowerMsg, "model_access") ||
		strings.Contains(lowerMsg, "permission denied for model") {
		return FailureClassModelAccess
	}

	switch statusCode {
	case http.StatusBadRequest: // 400
		return FailureClassCallerFault

	case http.StatusUnauthorized: // 401
		return FailureClassCredential

	case http.StatusPaymentRequired: // 402
		return FailureClassQuota

	case http.StatusForbidden: // 403
		if strings.Contains(lowerMsg, "model") || strings.Contains(lowerType, "model") {
			return FailureClassModelAccess
		}
		return FailureClassCredential

	case http.StatusNotFound: // 404
		return FailureClassModelAccess

	case http.StatusTooManyRequests: // 429
		return FailureClassRateLimit

	case http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable, // 503
		http.StatusGatewayTimeout:      // 504
		return FailureClassTransient

	default:
		if statusCode >= 500 {
			return FailureClassTransient
		}
		if statusCode >= 400 {
			return FailureClassCallerFault
		}
		return FailureClassTransient
	}
}
