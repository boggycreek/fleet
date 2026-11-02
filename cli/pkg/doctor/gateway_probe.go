// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultGatewayProbeTimeout specifies the default timeout for probing an LLM gateway.
const DefaultGatewayProbeTimeout = 1 * time.Second

// GatewayProbeTimeout allows overriding the default probe timeout.
var GatewayProbeTimeout = DefaultGatewayProbeTimeout

// WellKnownGatewayEndpoints defines common local model gateway endpoints probed by default.
var WellKnownGatewayEndpoints = []string{
	"http://127.0.0.1:11434", // Ollama
	"http://127.0.0.1:8080",  // llama.cpp / vLLM
	"http://127.0.0.1:52415", // exo
	"http://127.0.0.1:8000",  // colibri / vLLM
}

// GatewayProbeResult summarizes reachability, latency, and available models for a gateway.
type GatewayProbeResult struct {
	URL       string        `json:"url"`
	Engine    string        `json:"engine"`
	Reachable bool          `json:"reachable"`
	Latency   time.Duration `json:"latency"`
	Models    []string      `json:"models"`
	Error     string        `json:"error,omitempty"`
}

// openAIModelsResponse matches the standard OpenAI GET /v1/models JSON payload.
type openAIModelsResponse struct {
	Data []struct {
		ID      string `json:"id"`
		OwnedBy string `json:"owned_by"`
	} `json:"data"`
}

// ollamaTagsResponse matches Ollama's native GET /api/tags JSON payload.
type ollamaTagsResponse struct {
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

// gatewayHTTPClient allows testing with customized transports or timeouts.
var gatewayHTTPClient = &http.Client{}

// normalizeGatewayBaseURL cleans up an endpoint URL to ensure scheme and strip path suffixes.
func normalizeGatewayBaseURL(rawURL string) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", fmt.Errorf("empty endpoint URL")
	}

	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		trimmed = "http://" + trimmed
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid endpoint URL: %w", err)
	}

	path := strings.TrimRight(u.Path, "/")
	path = strings.TrimSuffix(path, "/models")
	path = strings.TrimSuffix(path, "/v1")
	path = strings.TrimSuffix(path, "/tags")
	path = strings.TrimSuffix(path, "/api")
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""

	normalized := strings.TrimRight(u.String(), "/")
	return normalized, nil
}

// detectEngine attempts to infer the inference engine based on response details and port.
func detectEngine(baseURL string, isOllamaFallback bool, ownedByList []string, serverHeader string) string {
	serverLower := strings.ToLower(serverHeader)
	if strings.Contains(serverLower, "ollama") || isOllamaFallback {
		return "ollama"
	}
	if strings.Contains(serverLower, "llamacpp") || strings.Contains(serverLower, "llama.cpp") {
		return "llamacpp"
	}
	if strings.Contains(serverLower, "vllm") {
		return "vllm"
	}
	if strings.Contains(serverLower, "exo") {
		return "exo"
	}
	if strings.Contains(serverLower, "colibri") {
		return "colibri"
	}

	for _, ob := range ownedByList {
		obLower := strings.ToLower(ob)
		switch {
		case strings.Contains(obLower, "llamacpp") || strings.Contains(obLower, "llama.cpp"):
			return "llamacpp"
		case strings.Contains(obLower, "ollama"):
			return "ollama"
		case strings.Contains(obLower, "vllm"):
			return "vllm"
		case strings.Contains(obLower, "exo"):
			return "exo"
		case strings.Contains(obLower, "colibri"):
			return "colibri"
		}
	}

	u, err := url.Parse(baseURL)
	if err == nil {
		port := u.Port()
		switch port {
		case "11434":
			return "ollama"
		case "52415":
			return "exo"
		case "8080":
			return "llamacpp"
		case "8000":
			return "colibri"
		}
	}

	return "openai-generic"
}

// ProbeGateway checks whether a model inference server is reachable and responding.
// It first attempts GET /v1/models (OpenAI-compatible). If that returns 404 or fails,
// it falls back to GET /api/tags (Ollama native API).
func ProbeGateway(ctx context.Context, endpointURL string) (*GatewayProbeResult, error) {
	baseURL, err := normalizeGatewayBaseURL(endpointURL)
	if err != nil {
		return &GatewayProbeResult{
			URL:       endpointURL,
			Reachable: false,
			Error:     err.Error(),
		}, err
	}

	result := &GatewayProbeResult{
		URL:       baseURL,
		Reachable: false,
		Models:    []string{},
	}

	// Enforce timeout if not already set on context
	reqCtx := ctx
	var cancel context.CancelFunc
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		timeout := GatewayProbeTimeout
		if timeout <= 0 {
			timeout = DefaultGatewayProbeTimeout
		}
		reqCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	start := time.Now()

	// 1. Attempt OpenAI-compatible endpoint: GET /v1/models
	openAIURL := baseURL + "/v1/models"
	modelsReq, reqErr := http.NewRequestWithContext(reqCtx, http.MethodGet, openAIURL, nil)
	if reqErr != nil {
		result.Latency = time.Since(start)
		result.Error = reqErr.Error()
		return result, reqErr
	}

	resp, doErr := gatewayHTTPClient.Do(modelsReq)
	if doErr == nil && resp.StatusCode == http.StatusOK {
		result.Reachable = true
		result.Latency = time.Since(start)
		serverHeader := resp.Header.Get("Server")

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
		_ = resp.Body.Close()
		if readErr != nil {
			result.Reachable = false
			result.Error = fmt.Sprintf("failed reading /v1/models response: %v", readErr)
			return result, readErr
		}

		var parsed openAIModelsResponse
		if jsonErr := json.Unmarshal(body, &parsed); jsonErr != nil {
			result.Reachable = false
			result.Error = fmt.Sprintf("invalid /v1/models JSON: %v", jsonErr)
			return result, jsonErr
		}

		var ownedByList []string
		for _, m := range parsed.Data {
			if m.ID != "" {
				result.Models = append(result.Models, m.ID)
			}
			if m.OwnedBy != "" {
				ownedByList = append(ownedByList, m.OwnedBy)
			}
		}

		result.Engine = detectEngine(baseURL, false, ownedByList, serverHeader)
		return result, nil
	}

	var firstErrSummary string
	if resp != nil {
		firstErrSummary = fmt.Sprintf("HTTP %d from /v1/models", resp.StatusCode)
		_ = resp.Body.Close()
	} else if doErr != nil {
		firstErrSummary = doErr.Error()
	}

	// 2. Fall back to Ollama native endpoint: GET /api/tags
	ollamaURL := baseURL + "/api/tags"
	tagsReq, tagsReqErr := http.NewRequestWithContext(reqCtx, http.MethodGet, ollamaURL, nil)
	if tagsReqErr != nil {
		result.Latency = time.Since(start)
		result.Error = fmt.Sprintf("%s; fallback request error: %v", firstErrSummary, tagsReqErr)
		return result, tagsReqErr
	}

	tagsResp, tagsDoErr := gatewayHTTPClient.Do(tagsReq)
	if tagsDoErr == nil && tagsResp.StatusCode == http.StatusOK {
		result.Reachable = true
		result.Latency = time.Since(start)
		serverHeader := tagsResp.Header.Get("Server")

		body, readErr := io.ReadAll(io.LimitReader(tagsResp.Body, 4*1024*1024))
		_ = tagsResp.Body.Close()
		if readErr != nil {
			result.Reachable = false
			result.Error = fmt.Sprintf("failed reading /api/tags response: %v", readErr)
			return result, readErr
		}

		var parsed ollamaTagsResponse
		if jsonErr := json.Unmarshal(body, &parsed); jsonErr != nil {
			result.Reachable = false
			result.Error = fmt.Sprintf("invalid /api/tags JSON: %v", jsonErr)
			return result, jsonErr
		}

		for _, m := range parsed.Models {
			name := m.Name
			if name == "" {
				name = m.Model
			}
			if name != "" {
				result.Models = append(result.Models, name)
			}
		}

		result.Engine = detectEngine(baseURL, true, nil, serverHeader)
		return result, nil
	}

	result.Latency = time.Since(start)
	if tagsResp != nil {
		_ = tagsResp.Body.Close()
		result.Error = fmt.Sprintf("%s; fallback returned HTTP %d from /api/tags", firstErrSummary, tagsResp.StatusCode)
		return result, fmt.Errorf("%s", result.Error)
	}

	if tagsDoErr != nil {
		result.Error = fmt.Sprintf("%s; fallback error: %v", firstErrSummary, tagsDoErr)
		return result, tagsDoErr
	}

	result.Error = firstErrSummary
	return result, fmt.Errorf("%s", firstErrSummary)
}

// ProbeWellKnownGateways probes well-known local loopback endpoints concurrently.
func ProbeWellKnownGateways(ctx context.Context) []*GatewayProbeResult {
	results := make([]*GatewayProbeResult, len(WellKnownGatewayEndpoints))
	var wg sync.WaitGroup

	for i, ep := range WellKnownGatewayEndpoints {
		wg.Add(1)
		go func(idx int, endpoint string) {
			defer wg.Done()
			res, _ := ProbeGateway(ctx, endpoint)
			results[idx] = res
		}(i, ep)
	}

	wg.Wait()
	return results
}
