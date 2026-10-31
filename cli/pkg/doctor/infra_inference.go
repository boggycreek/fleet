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
	"os"
	"strings"
	"time"

	"github.com/boggycreek/fleet/cli/pkg/config"
)

// LlamaSlot represents an individual inference slot reported by llama-server's /slots endpoint.
type LlamaSlot struct {
	ID           int  `json:"id"`
	State        int  `json:"state"`
	IsProcessing bool `json:"is_processing"`
	NCtx         int  `json:"n_ctx"`
}

// LlamaSlotSummary holds aggregate status across all llama-server inference slots.
type LlamaSlotSummary struct {
	TotalSlots     int
	ActiveSlots    int
	IdleSlots      int
	ContextPerSlot int
	MatchedPreset  *config.TopologyPreset
}

// ParseLlamaSlots decodes the JSON payload from llama-server's /slots endpoint.
// It supports both top-level arrays `[{"id": 0, ...}]` and wrapped objects `{"slots": [...]}`.
func ParseLlamaSlots(data []byte) (LlamaSlotSummary, error) {
	var slots []LlamaSlot
	if err := json.Unmarshal(data, &slots); err != nil {
		var wrapper struct {
			Slots []LlamaSlot `json:"slots"`
		}
		if errWrap := json.Unmarshal(data, &wrapper); errWrap != nil {
			return LlamaSlotSummary{}, fmt.Errorf("failed to parse /slots JSON: %w", err)
		}
		slots = wrapper.Slots
	}

	summary := LlamaSlotSummary{
		TotalSlots: len(slots),
	}

	for _, s := range slots {
		if s.IsProcessing || s.State != 0 {
			summary.ActiveSlots++
		} else {
			summary.IdleSlots++
		}
		if s.NCtx > summary.ContextPerSlot {
			summary.ContextPerSlot = s.NCtx
		}
	}

	if summary.TotalSlots > 0 {
		summary.MatchedPreset = config.MatchTopologyPreset(summary.TotalSlots, summary.ContextPerSlot)
	}

	return summary, nil
}

// inferenceHTTPClient is package-level to permit testing with mock HTTP transports or timeouts.
var inferenceHTTPClient = &http.Client{Timeout: 1 * time.Second}

// inferenceCandidateURLs resolves endpoints to probe for a local llama-server.
func inferenceCandidateURLs() []string {
	if env := strings.TrimSpace(os.Getenv("LLM_GATEWAY_URL")); env != "" {
		cleaned := strings.TrimRight(env, "/")
		cleaned = strings.TrimSuffix(cleaned, "/v1")
		return []string{cleaned}
	}
	return []string{
		"http://127.0.0.1:11434",
		"http://localhost:11434",
		"http://llm-gateway:11434",
	}
}

// checkLocalInferenceSlots probes the host or gateway for a running llama-server instance.
// It verifies /health and inspects slot allocation via /slots.
func checkLocalInferenceSlots(ctx context.Context, report *DoctorReport) {
	candidates := inferenceCandidateURLs()

	for _, targetURL := range candidates {
		// 1. Check /health
		healthURL := targetURL + "/health"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err != nil {
			continue
		}

		resp, err := inferenceHTTPClient.Do(req)
		if err != nil {
			continue
		}

		if resp.StatusCode == http.StatusServiceUnavailable {
			_ = resp.Body.Close()
			report.Checks = append(report.Checks, CheckItem{
				Name:    "Local LLM Inference Slots",
				Status:  StatusOK,
				Message: fmt.Sprintf("Local LLM inference server responsive at %s, but model is currently loading (HTTP 503)", targetURL),
			})
			return
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			report.Checks = append(report.Checks, CheckItem{
				Name:    "Local LLM Inference Slots",
				Status:  StatusWarning,
				Message: fmt.Sprintf("Local LLM inference server at %s returned unexpected /health HTTP status %d", targetURL, resp.StatusCode),
			})
			report.WarningCount++
			return
		}
		_ = resp.Body.Close()

		// 2. Query /slots
		slotsURL := targetURL + "/slots"
		slotsReq, err := http.NewRequestWithContext(ctx, http.MethodGet, slotsURL, nil)
		if err != nil {
			report.Checks = append(report.Checks, CheckItem{
				Name:    "Local LLM Inference Slots",
				Status:  StatusWarning,
				Message: fmt.Sprintf("Failed creating request for %s: %v", slotsURL, err),
			})
			report.WarningCount++
			return
		}

		slotsResp, err := inferenceHTTPClient.Do(slotsReq)
		if err != nil {
			report.Checks = append(report.Checks, CheckItem{
				Name:    "Local LLM Inference Slots",
				Status:  StatusWarning,
				Message: fmt.Sprintf("Local LLM inference server at %s /slots unreachable: %v", targetURL, err),
			})
			report.WarningCount++
			return
		}

		if slotsResp.StatusCode != http.StatusOK {
			_ = slotsResp.Body.Close()
			report.Checks = append(report.Checks, CheckItem{
				Name:    "Local LLM Inference Slots",
				Status:  StatusWarning,
				Message: fmt.Sprintf("Local LLM inference server at %s returned unexpected /slots HTTP status %d", targetURL, slotsResp.StatusCode),
			})
			report.WarningCount++
			return
		}

		body, err := io.ReadAll(io.LimitReader(slotsResp.Body, 1024*1024))
		_ = slotsResp.Body.Close()
		if err != nil {
			report.Checks = append(report.Checks, CheckItem{
				Name:    "Local LLM Inference Slots",
				Status:  StatusWarning,
				Message: fmt.Sprintf("Failed reading /slots payload from %s: %v", targetURL, err),
			})
			report.WarningCount++
			return
		}

		summary, err := ParseLlamaSlots(body)
		if err != nil {
			report.Checks = append(report.Checks, CheckItem{
				Name:    "Local LLM Inference Slots",
				Status:  StatusWarning,
				Message: fmt.Sprintf("Local LLM inference server at %s returned unparseable /slots JSON: %v", targetURL, err),
			})
			report.WarningCount++
			return
		}

		if summary.TotalSlots == 0 {
			report.Checks = append(report.Checks, CheckItem{
				Name:    "Local LLM Inference Slots",
				Status:  StatusOK,
				Message: fmt.Sprintf("Local LLM inference server responsive at %s (0 slots configured)", targetURL),
			})
			return
		}

		var topologyLabel string
		if summary.MatchedPreset != nil {
			switch summary.MatchedPreset.ID {
			case "topology-a":
				topologyLabel = "Topology A"
			case "topology-b":
				topologyLabel = "Topology B"
			default:
				topologyLabel = summary.MatchedPreset.Name
			}
		} else {
			topologyLabel = "Custom"
		}

		ctxStr := config.FormatContextK(summary.ContextPerSlot)
		msg := fmt.Sprintf("Local LLM inference slots healthy: %s (%d slots @ %s context, %d active, %d idle)",
			topologyLabel, summary.TotalSlots, ctxStr, summary.ActiveSlots, summary.IdleSlots)

		report.Checks = append(report.Checks, CheckItem{
			Name:    "Local LLM Inference Slots",
			Status:  StatusOK,
			Message: msg,
		})
		return
	}

	// Server is not running on probed endpoints
	report.Checks = append(report.Checks, CheckItem{
		Name:    "Local LLM Inference Slots",
		Status:  StatusOK,
		Message: "Host LLM inference server (llama-server) is not currently running on :11434; optional for local inference",
	})
}
