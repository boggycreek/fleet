// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckLocalModelGateways_NoneDetected(t *testing.T) {
	oldEndpoints := WellKnownGatewayEndpoints
	defer func() {
		WellKnownGatewayEndpoints = oldEndpoints
	}()

	// Point to closed/unreachable ports
	WellKnownGatewayEndpoints = []string{
		"http://127.0.0.1:65431",
		"http://127.0.0.1:65432",
	}

	report := &DoctorReport{AgentName: "shared-infrastructure"}
	checkLocalModelGateways(context.Background(), report)

	if len(report.Checks) != 1 {
		t.Fatalf("expected 1 check item, got %d", len(report.Checks))
	}
	item := report.Checks[0]
	if item.Name != "Local Model Gateways" {
		t.Errorf("expected check name 'Local Model Gateways', got %q", item.Name)
	}
	if item.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", item.Status)
	}
	if item.Message != "INFO: No local LLM inference engines detected on default ports" {
		t.Errorf("unexpected message: %q", item.Message)
	}
	if report.WarningCount != 0 || report.UnrepairableCount != 0 {
		t.Errorf("informational note should not increment warning or unrepairable counts")
	}
}

func TestCheckLocalModelGateways_DiscoveredEngines(t *testing.T) {
	// Server 1: llama.cpp with 2 models
	tsLlama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Server", "llama.cpp")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"id": "model-a"},
					{"id": "model-b"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer tsLlama.Close()

	// Server 2: Ollama with 1 model
	tsOllama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Server", "Ollama")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{
					{"name": "llama3:latest"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer tsOllama.Close()

	oldEndpoints := WellKnownGatewayEndpoints
	defer func() {
		WellKnownGatewayEndpoints = oldEndpoints
	}()

	WellKnownGatewayEndpoints = []string{
		tsLlama.URL,
		"http://127.0.0.1:65431", // unreachable
		tsOllama.URL,
	}

	report := &DoctorReport{AgentName: "shared-infrastructure"}
	checkLocalModelGateways(context.Background(), report)

	if len(report.Checks) != 2 {
		t.Fatalf("expected 2 discovered healthy gateways, got %d: %+v", len(report.Checks), report.Checks)
	}

	foundLlama := false
	foundOllama := false

	for _, c := range report.Checks {
		if c.Name != "Local Model Gateways" {
			t.Errorf("expected check name 'Local Model Gateways', got %q", c.Name)
		}
		if c.Status != StatusOK {
			t.Errorf("expected status StatusOK, got %s", c.Status)
		}
		if strings.Contains(c.Message, "llamacpp") && strings.Contains(c.Message, "2 active models") {
			foundLlama = true
		}
		if strings.Contains(c.Message, "ollama") && strings.Contains(c.Message, "1 active model") {
			foundOllama = true
		}
	}

	if !foundLlama {
		t.Errorf("expected llamacpp check with 2 active models, got %+v", report.Checks)
	}
	if !foundOllama {
		t.Errorf("expected ollama check with 1 active model, got %+v", report.Checks)
	}
}

func TestCheckLocalModelGateways_DiscoveredEngineNoModels(t *testing.T) {
	tsEmpty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer tsEmpty.Close()

	oldEndpoints := WellKnownGatewayEndpoints
	defer func() {
		WellKnownGatewayEndpoints = oldEndpoints
	}()

	WellKnownGatewayEndpoints = []string{
		tsEmpty.URL,
	}

	report := &DoctorReport{AgentName: "shared-infrastructure"}
	checkLocalModelGateways(context.Background(), report)

	if len(report.Checks) != 1 {
		t.Fatalf("expected 1 check item, got %d", len(report.Checks))
	}
	if !strings.Contains(report.Checks[0].Message, "0 active models") {
		t.Errorf("expected '0 active models' in message, got %q", report.Checks[0].Message)
	}
}
