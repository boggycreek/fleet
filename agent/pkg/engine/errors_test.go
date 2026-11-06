// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClassifyFailure(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		errType    string
		errMsg     string
		want       FailureClass
	}{
		// 400 Bad Request
		{
			name:       "400 caller fault",
			statusCode: 400,
			errType:    "invalid_request_error",
			errMsg:     "temperature must be between 0 and 2",
			want:       FailureClassCallerFault,
		},
		// 401 Unauthorized
		{
			name:       "401 invalid api key",
			statusCode: 401,
			errType:    "invalid_api_key",
			errMsg:     "Incorrect API key provided",
			want:       FailureClassCredential,
		},
		// 402 Payment Required
		{
			name:       "402 payment required",
			statusCode: 402,
			errType:    "",
			errMsg:     "Payment required to access service",
			want:       FailureClassQuota,
		},
		// 403 Forbidden
		{
			name:       "403 bare forbidden",
			statusCode: 403,
			errType:    "",
			errMsg:     "Forbidden",
			want:       FailureClassCredential,
		},
		{
			name:       "403 model forbidden message",
			statusCode: 403,
			errType:    "permission_denied",
			errMsg:     "You do not have permission to access model claude-3-opus",
			want:       FailureClassModelAccess,
		},
		{
			name:       "403 model forbidden type",
			statusCode: 403,
			errType:    "model_forbidden",
			errMsg:     "Access denied",
			want:       FailureClassModelAccess,
		},
		// 404 Not Found
		{
			name:       "404 model not found",
			statusCode: 404,
			errType:    "not_found_error",
			errMsg:     "The model `custom-gpt` does not exist",
			want:       FailureClassModelAccess,
		},
		{
			name:       "404 generic not found",
			statusCode: 404,
			errType:    "",
			errMsg:     "404 page not found",
			want:       FailureClassModelAccess,
		},
		// 429 Rate Limit
		{
			name:       "429 standard rate limit",
			statusCode: 429,
			errType:    "rate_limit_exceeded",
			errMsg:     "Rate limit reached for requests per minute",
			want:       FailureClassRateLimit,
		},
		{
			name:       "429 insufficient quota in type",
			statusCode: 429,
			errType:    "insufficient_quota",
			errMsg:     "You have exceeded your current quota",
			want:       FailureClassQuota,
		},
		{
			name:       "429 quota exceeded in message",
			statusCode: 429,
			errType:    "requests",
			errMsg:     "Quota exceeded, please recharge your account",
			want:       FailureClassQuota,
		},
		{
			name:       "429 out of credits in message",
			statusCode: 429,
			errType:    "",
			errMsg:     "Account is out of credits",
			want:       FailureClassQuota,
		},
		{
			name:       "429 credit balance in message",
			statusCode: 429,
			errType:    "",
			errMsg:     "Credit balance is too low",
			want:       FailureClassQuota,
		},
		{
			name:       "429 balance exhausted in message",
			statusCode: 429,
			errType:    "",
			errMsg:     "Your balance has been exhausted",
			want:       FailureClassQuota,
		},
		// 5xx Transient
		{
			name:       "500 internal server error",
			statusCode: 500,
			errType:    "server_error",
			errMsg:     "Internal server error occurred",
			want:       FailureClassTransient,
		},
		{
			name:       "502 bad gateway",
			statusCode: 502,
			errType:    "",
			errMsg:     "Bad Gateway",
			want:       FailureClassTransient,
		},
		{
			name:       "503 service unavailable",
			statusCode: 503,
			errType:    "",
			errMsg:     "Service Unavailable",
			want:       FailureClassTransient,
		},
		{
			name:       "504 gateway timeout",
			statusCode: 504,
			errType:    "",
			errMsg:     "Gateway Timeout",
			want:       FailureClassTransient,
		},
		{
			name:       "521 web server is down",
			statusCode: 521,
			errType:    "",
			errMsg:     "Web server is down",
			want:       FailureClassTransient,
		},
		// Other 4xx
		{
			name:       "422 unprocessable entity",
			statusCode: 422,
			errType:    "",
			errMsg:     "Unprocessable entity",
			want:       FailureClassCallerFault,
		},
		// Keywords across status codes
		{
			name:       "400 with model does not exist",
			statusCode: 400,
			errType:    "",
			errMsg:     "model 'xyz' does not exist",
			want:       FailureClassModelAccess,
		},
		{
			name:       "400 with model_not_found type",
			statusCode: 400,
			errType:    "model_not_found",
			errMsg:     "unknown model",
			want:       FailureClassModelAccess,
		},
		{
			name:       "400 with access to model message",
			statusCode: 400,
			errType:    "",
			errMsg:     "you lack access to model deepseek-r1",
			want:       FailureClassModelAccess,
		},
		{
			name:       "400 with permission denied for model message",
			statusCode: 400,
			errType:    "",
			errMsg:     "permission denied for model gpt-4",
			want:       FailureClassModelAccess,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyFailure(tt.statusCode, tt.errType, tt.errMsg)
			if got != tt.want {
				t.Errorf("ClassifyFailure(%d, %q, %q) = %v; want %v", tt.statusCode, tt.errType, tt.errMsg, got, tt.want)
			}
		})
	}
}

func TestAPIErrorHelpers(t *testing.T) {
	// Nil receiver checks
	var nilErr *APIError
	if nilErr.Error() != "<nil>" {
		t.Errorf("expected <nil>, got %s", nilErr.Error())
	}
	if nilErr.IsTransient() {
		t.Error("expected false for nil.IsTransient()")
	}
	if nilErr.IsRateLimit() {
		t.Error("expected false for nil.IsRateLimit()")
	}
	if nilErr.IsCredential() {
		t.Error("expected false for nil.IsCredential()")
	}
	if nilErr.IsQuota() {
		t.Error("expected false for nil.IsQuota()")
	}
	if nilErr.IsModelAccess() {
		t.Error("expected false for nil.IsModelAccess()")
	}
	if nilErr.IsCallerFault() {
		t.Error("expected false for nil.IsCallerFault()")
	}

	// Instance checks
	classes := []struct {
		err            *APIError
		isTransient    bool
		isRateLimit    bool
		isCredential   bool
		isQuota        bool
		isModelAccess  bool
		isCallerFault  bool
		expectedErrStr string
	}{
		{
			err: &APIError{
				StatusCode: 503,
				Type:       "service_unavailable",
				Message:    "overloaded",
				Class:      FailureClassTransient,
			},
			isTransient:    true,
			expectedErrStr: "api error (status 503, class transient, type service_unavailable): overloaded",
		},
		{
			err: &APIError{
				StatusCode: 429,
				Message:    "too many requests",
				Class:      FailureClassRateLimit,
			},
			isRateLimit:    true,
			expectedErrStr: "api error (status 429, class rate_limit): too many requests",
		},
		{
			err: &APIError{
				StatusCode: 401,
				Message:    "invalid auth",
				Class:      FailureClassCredential,
			},
			isCredential:   true,
			expectedErrStr: "api error (status 401, class credential): invalid auth",
		},
		{
			err: &APIError{
				StatusCode: 402,
				Message:    "insufficient funds",
				Class:      FailureClassQuota,
			},
			isQuota:        true,
			expectedErrStr: "api error (status 402, class quota): insufficient funds",
		},
		{
			err: &APIError{
				StatusCode: 404,
				Message:    "model missing",
				Class:      FailureClassModelAccess,
			},
			isModelAccess:  true,
			expectedErrStr: "api error (status 404, class model_access): model missing",
		},
		{
			err: &APIError{
				StatusCode: 400,
				Message:    "bad prompt format",
				Class:      FailureClassCallerFault,
			},
			isCallerFault:  true,
			expectedErrStr: "api error (status 400, class caller_fault): bad prompt format",
		},
	}

	for _, c := range classes {
		t.Run(string(c.err.Class), func(t *testing.T) {
			if got := c.err.IsTransient(); got != c.isTransient {
				t.Errorf("IsTransient() = %v, want %v", got, c.isTransient)
			}
			if got := c.err.IsRateLimit(); got != c.isRateLimit {
				t.Errorf("IsRateLimit() = %v, want %v", got, c.isRateLimit)
			}
			if got := c.err.IsCredential(); got != c.isCredential {
				t.Errorf("IsCredential() = %v, want %v", got, c.isCredential)
			}
			if got := c.err.IsQuota(); got != c.isQuota {
				t.Errorf("IsQuota() = %v, want %v", got, c.isQuota)
			}
			if got := c.err.IsModelAccess(); got != c.isModelAccess {
				t.Errorf("IsModelAccess() = %v, want %v", got, c.isModelAccess)
			}
			if got := c.err.IsCallerFault(); got != c.isCallerFault {
				t.Errorf("IsCallerFault() = %v, want %v", got, c.isCallerFault)
			}
			if c.err.Error() != c.expectedErrStr {
				t.Errorf("Error() = %q, want %q", c.err.Error(), c.expectedErrStr)
			}
		})
	}
}

func TestAPIErrorJSON(t *testing.T) {
	err := &APIError{
		StatusCode: 429,
		Type:       "rate_limit_error",
		Message:    "RPM limit reached",
		Class:      FailureClassRateLimit,
	}

	data, jsonErr := json.Marshal(err)
	if jsonErr != nil {
		t.Fatalf("failed to marshal APIError: %v", jsonErr)
	}

	var parsed APIError
	if unmarshalErr := json.Unmarshal(data, &parsed); unmarshalErr != nil {
		t.Fatalf("failed to unmarshal APIError: %v", unmarshalErr)
	}

	if parsed.StatusCode != 429 || parsed.Type != "rate_limit_error" || parsed.Class != FailureClassRateLimit || parsed.Message != "RPM limit reached" {
		t.Errorf("unexpected deserialized APIError: %+v", parsed)
	}
}

func TestLLMClientReturnsStructuredAPIError(t *testing.T) {
	cases := []struct {
		name           string
		handler        http.HandlerFunc
		wantStatusCode int
		wantClass      FailureClass
		wantType       string
		wantMsgSnippet string
	}{
		{
			name: "400 with OpenAI JSON error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(ChatCompletionResponse{
					Error: &OpenAIError{
						Message: "invalid temperature value",
						Type:    "invalid_request_error",
					},
				})
			},
			wantStatusCode: 400,
			wantClass:      FailureClassCallerFault,
			wantType:       "invalid_request_error",
			wantMsgSnippet: "invalid temperature value",
		},
		{
			name: "401 with OpenAI JSON error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(ChatCompletionResponse{
					Error: &OpenAIError{
						Message: "invalid api key",
						Type:    "authentication_error",
					},
				})
			},
			wantStatusCode: 401,
			wantClass:      FailureClassCredential,
			wantType:       "authentication_error",
			wantMsgSnippet: "invalid api key",
		},
		{
			name: "404 model not found",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(ChatCompletionResponse{
					Error: &OpenAIError{
						Message: "model 'foo' does not exist",
						Type:    "model_not_found",
					},
				})
			},
			wantStatusCode: 404,
			wantClass:      FailureClassModelAccess,
			wantType:       "model_not_found",
			wantMsgSnippet: "does not exist",
		},
		{
			name: "429 rate limit exceeded",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(ChatCompletionResponse{
					Error: &OpenAIError{
						Message: "Rate limit reached",
						Type:    "rate_limit_exceeded",
					},
				})
			},
			wantStatusCode: 429,
			wantClass:      FailureClassRateLimit,
			wantType:       "rate_limit_exceeded",
			wantMsgSnippet: "Rate limit reached",
		},
		{
			name: "429 insufficient quota",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(ChatCompletionResponse{
					Error: &OpenAIError{
						Message: "You have exceeded your current quota",
						Type:    "insufficient_quota",
					},
				})
			},
			wantStatusCode: 429,
			wantClass:      FailureClassQuota,
			wantType:       "insufficient_quota",
			wantMsgSnippet: "exceeded your current quota",
		},
		{
			name: "503 plain text response",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "upstream service unavailable", http.StatusServiceUnavailable)
			},
			wantStatusCode: 503,
			wantClass:      FailureClassTransient,
			wantType:       "",
			wantMsgSnippet: "upstream service unavailable",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(tc.handler)
			defer ts.Close()

			client := NewLLMClient(ts.URL, "test-key", "test-model", 2*time.Second)
			_, err := client.ChatCompletion(context.Background(), ChatCompletionRequest{Model: "test-model"})
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected errors.As to match *APIError, got error: %v (type %T)", err, err)
			}

			if apiErr.StatusCode != tc.wantStatusCode {
				t.Errorf("got StatusCode %d, want %d", apiErr.StatusCode, tc.wantStatusCode)
			}
			if apiErr.Class != tc.wantClass {
				t.Errorf("got Class %v, want %v", apiErr.Class, tc.wantClass)
			}
			if apiErr.Type != tc.wantType {
				t.Errorf("got Type %q, want %q", apiErr.Type, tc.wantType)
			}
			if !strings.Contains(apiErr.Message, tc.wantMsgSnippet) {
				t.Errorf("expected Message to contain %q, got %q", tc.wantMsgSnippet, apiErr.Message)
			}
		})
	}
}
