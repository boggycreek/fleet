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
	"os/exec"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/runtime"
)

func TestCheckModelGateway_ReachableOpenAI(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Server", "llama.cpp")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"id": "meta-llama/Llama-3-8B-Instruct", "owned_by": "llamacpp"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.AgentConfig{
		Name:     "agent-test-openai",
		ModelURL: ts.URL,
	}
	report := &DoctorReport{AgentName: cfg.Name}

	checkModelGateway(context.Background(), cfg, report)

	if len(report.Checks) != 1 {
		t.Fatalf("expected 1 check item, got %d", len(report.Checks))
	}
	item := report.Checks[0]
	if item.Name != "Model Gateway" {
		t.Errorf("expected check name 'Model Gateway', got %q", item.Name)
	}
	if item.Status != StatusOK {
		t.Errorf("expected status StatusOK, got %s", item.Status)
	}
	if !strings.Contains(item.Message, "Gateway reachable at") {
		t.Errorf("expected message to contain 'Gateway reachable at', got %q", item.Message)
	}
	if !strings.Contains(item.Message, "engine: llamacpp") {
		t.Errorf("expected message to contain 'engine: llamacpp', got %q", item.Message)
	}
	if !strings.Contains(item.Message, "models: meta-llama/Llama-3-8B-Instruct") {
		t.Errorf("expected message to contain model name, got %q", item.Message)
	}
	if !strings.Contains(item.Message, "latency:") {
		t.Errorf("expected message to contain latency, got %q", item.Message)
	}
	if report.WarningCount != 0 {
		t.Errorf("expected warning count 0, got %d", report.WarningCount)
	}
}

func TestCheckModelGateway_ReachableOllama(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer ts.Close()

	cfg := &config.AgentConfig{
		Name:     "agent-test-ollama",
		ModelURL: ts.URL,
	}
	report := &DoctorReport{AgentName: cfg.Name}

	checkModelGateway(context.Background(), cfg, report)

	if len(report.Checks) != 1 {
		t.Fatalf("expected 1 check item, got %d", len(report.Checks))
	}
	item := report.Checks[0]
	if item.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", item.Status)
	}
	if !strings.Contains(item.Message, "engine: ollama") {
		t.Errorf("expected engine ollama in message, got %q", item.Message)
	}
	if !strings.Contains(item.Message, "models: llama3:latest") {
		t.Errorf("expected model name in message, got %q", item.Message)
	}
}

func TestCheckModelGateway_ReachableNoModels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	cfg := &config.AgentConfig{
		Name:     "agent-no-models",
		ModelURL: ts.URL,
	}
	report := &DoctorReport{AgentName: cfg.Name}

	checkModelGateway(context.Background(), cfg, report)

	if len(report.Checks) != 1 {
		t.Fatalf("expected 1 check item, got %d", len(report.Checks))
	}
	if !strings.Contains(report.Checks[0].Message, "models: none") {
		t.Errorf("expected 'models: none' in message, got %q", report.Checks[0].Message)
	}
}

func TestCheckModelGateway_Unreachable(t *testing.T) {
	cfg := &config.AgentConfig{
		Name:     "agent-test-unreachable",
		ModelURL: "http://127.0.0.1:65430",
	}
	report := &DoctorReport{AgentName: cfg.Name}

	checkModelGateway(context.Background(), cfg, report)

	if len(report.Checks) != 1 {
		t.Fatalf("expected 1 check item, got %d", len(report.Checks))
	}
	item := report.Checks[0]
	if item.Name != "Model Gateway" {
		t.Errorf("expected check name 'Model Gateway', got %q", item.Name)
	}
	if item.Status != StatusWarning {
		t.Errorf("expected status StatusWarning (WARN), got %s", item.Status)
	}
	if !strings.Contains(item.Message, "Gateway unreachable at") {
		t.Errorf("expected message to mention unreachable, got %q", item.Message)
	}
	if !strings.Contains(item.Message, "hint: Start local inference server or verify port") {
		t.Errorf("expected hint in message, got %q", item.Message)
	}
	if report.WarningCount != 1 {
		t.Errorf("expected warning count 1, got %d", report.WarningCount)
	}
}

func TestCheckModelGateway_EmptyModelURL_Skipped(t *testing.T) {
	cfg := &config.AgentConfig{
		Name: "agent-no-url",
	}
	report := &DoctorReport{AgentName: cfg.Name}

	checkModelGateway(context.Background(), cfg, report)

	if len(report.Checks) != 0 {
		t.Fatalf("expected check to be skipped when no ModelURL, got %d checks", len(report.Checks))
	}
}

func TestCheckModelGateway_ContainerInspectModelURL(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "qwen2.5-coder"}},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	restoreMock := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) >= 3 && args[0] == "container" && args[1] == "inspect" {
			envOutput := "FOO=bar\nMODEL_URL=" + ts.URL + "\nBAZ=qux\n"
			return exec.Command("printf", "%s", envOutput)
		}
		return exec.Command("true")
	}))
	defer restoreMock()

	cfg := &config.AgentConfig{
		Name:          "agent-container-env",
		ContainerName: "fleet-agent-container-env",
	}
	report := &DoctorReport{AgentName: cfg.Name}

	checkModelGateway(context.Background(), cfg, report)

	if len(report.Checks) != 1 {
		t.Fatalf("expected 1 check item from inspect resolution, got %d", len(report.Checks))
	}
	item := report.Checks[0]
	if item.Status != StatusOK {
		t.Errorf("expected StatusOK, got %s", item.Status)
	}
	if !strings.Contains(item.Message, "qwen2.5-coder") {
		t.Errorf("expected model name in message, got %q", item.Message)
	}
}

func TestCheckModelGateway_ContainerInspectOpenAIBaseURL_RewriteLLMGateway(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "rewritten-model"}},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	// ts.URL looks like http://127.0.0.1:<port>
	containerTarget := strings.ReplaceAll(ts.URL, "://127.0.0.1", "://llm-gateway")

	restoreMock := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) >= 3 && args[0] == "container" && args[1] == "inspect" {
			envOutput := "OPENAI_BASE_URL=" + containerTarget + "\n"
			return exec.Command("printf", "%s", envOutput)
		}
		return exec.Command("true")
	}))
	defer restoreMock()

	cfg := &config.AgentConfig{
		Name:          "agent-rewrite-gateway",
		ContainerName: "fleet-agent-rewrite-gateway",
	}
	report := &DoctorReport{AgentName: cfg.Name}

	checkModelGateway(context.Background(), cfg, report)

	if len(report.Checks) != 1 {
		t.Fatalf("expected 1 check item, got %d", len(report.Checks))
	}
	if report.Checks[0].Status != StatusOK {
		t.Errorf("expected StatusOK, got %s: %s", report.Checks[0].Status, report.Checks[0].Message)
	}
}

func TestResolveAgentModelURL_EdgeCases(t *testing.T) {
	ctx := context.Background()

	// Nil config
	if url := resolveAgentModelURL(ctx, nil); url != "" {
		t.Errorf("expected empty string for nil config, got %q", url)
	}

	// No-container agent does not inspect container
	noContCfg := &config.AgentConfig{
		Name:          "no-cont",
		Runtime:       "no-container",
		ContainerName: "ignored-container",
	}
	if url := resolveAgentModelURL(ctx, noContCfg); url != "" {
		t.Errorf("expected empty string for no-container agent, got %q", url)
	}

	// Baremetal agent does not inspect container
	bmCfg := &config.AgentConfig{
		Name:          "bm-agent",
		Runtime:       "baremetal",
		ContainerName: "ignored-container",
	}
	if url := resolveAgentModelURL(ctx, bmCfg); url != "" {
		t.Errorf("expected empty string for baremetal agent, got %q", url)
	}

	// Container inspect fails
	restoreMock := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("false")
	}))
	defer restoreMock()

	failCfg := &config.AgentConfig{
		Name:          "fail-agent",
		ContainerName: "fail-container",
	}
	if url := resolveAgentModelURL(ctx, failCfg); url != "" {
		t.Errorf("expected empty string on inspect failure, got %q", url)
	}
}
