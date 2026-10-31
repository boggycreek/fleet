// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseLlamaSlots_ArrayAndObject(t *testing.T) {
	// 1. Direct array JSON (Topology A: 4 slots @ 32768)
	arrayJSON := `[
		{"id": 0, "state": 0, "is_processing": false, "n_ctx": 32768},
		{"id": 1, "state": 1, "is_processing": true, "n_ctx": 32768},
		{"id": 2, "state": 0, "is_processing": false, "n_ctx": 32768},
		{"id": 3, "state": 0, "is_processing": false, "n_ctx": 32768}
	]`
	summaryA, err := ParseLlamaSlots([]byte(arrayJSON))
	if err != nil {
		t.Fatalf("unexpected error parsing array JSON: %v", err)
	}
	if summaryA.TotalSlots != 4 {
		t.Errorf("expected 4 total slots, got %d", summaryA.TotalSlots)
	}
	if summaryA.ActiveSlots != 1 {
		t.Errorf("expected 1 active slot, got %d", summaryA.ActiveSlots)
	}
	if summaryA.IdleSlots != 3 {
		t.Errorf("expected 3 idle slots, got %d", summaryA.IdleSlots)
	}
	if summaryA.ContextPerSlot != 32768 {
		t.Errorf("expected 32768 context per slot, got %d", summaryA.ContextPerSlot)
	}
	if summaryA.MatchedPreset == nil || summaryA.MatchedPreset.ID != "topology-a" {
		t.Errorf("expected matched preset topology-a, got %+v", summaryA.MatchedPreset)
	}

	// 2. Wrapped object JSON (Topology B: 2 slots @ 65536)
	wrapperJSON := `{
		"slots": [
			{"id": 0, "state": 0, "is_processing": false, "n_ctx": 65536},
			{"id": 1, "state": 0, "is_processing": false, "n_ctx": 65536}
		]
	}`
	summaryB, err := ParseLlamaSlots([]byte(wrapperJSON))
	if err != nil {
		t.Fatalf("unexpected error parsing wrapped JSON: %v", err)
	}
	if summaryB.TotalSlots != 2 {
		t.Errorf("expected 2 total slots, got %d", summaryB.TotalSlots)
	}
	if summaryB.ActiveSlots != 0 {
		t.Errorf("expected 0 active slots, got %d", summaryB.ActiveSlots)
	}
	if summaryB.IdleSlots != 2 {
		t.Errorf("expected 2 idle slots, got %d", summaryB.IdleSlots)
	}
	if summaryB.ContextPerSlot != 65536 {
		t.Errorf("expected 65536 context per slot, got %d", summaryB.ContextPerSlot)
	}
	if summaryB.MatchedPreset == nil || summaryB.MatchedPreset.ID != "topology-b" {
		t.Errorf("expected matched preset topology-b, got %+v", summaryB.MatchedPreset)
	}

	// 3. Custom topology (1 slot @ 16384)
	customJSON := `[{"id": 0, "state": 0, "is_processing": false, "n_ctx": 16384}]`
	summaryC, err := ParseLlamaSlots([]byte(customJSON))
	if err != nil {
		t.Fatalf("unexpected error parsing custom JSON: %v", err)
	}
	if summaryC.TotalSlots != 1 || summaryC.MatchedPreset != nil {
		t.Errorf("expected custom config with nil preset match, got %+v", summaryC)
	}

	// 4. Empty array
	emptySummary, err := ParseLlamaSlots([]byte(`[]`))
	if err != nil {
		t.Fatalf("unexpected error parsing empty JSON: %v", err)
	}
	if emptySummary.TotalSlots != 0 {
		t.Errorf("expected 0 slots for empty array, got %d", emptySummary.TotalSlots)
	}

	// 5. Invalid JSON
	if _, err := ParseLlamaSlots([]byte(`not-json`)); err == nil {
		t.Errorf("expected error parsing invalid JSON")
	}
}

func TestCheckLocalInferenceSlots_Scenarios(t *testing.T) {
	ctx := context.Background()

	// 1. Healthy Topology A (4 slots @ 32k)
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status": "ok"}`))
		case "/slots":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[
				{"id": 0, "state": 0, "is_processing": false, "n_ctx": 32768},
				{"id": 1, "state": 0, "is_processing": false, "n_ctx": 32768},
				{"id": 2, "state": 0, "is_processing": false, "n_ctx": 32768},
				{"id": 3, "state": 0, "is_processing": false, "n_ctx": 32768}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srvA.Close()

	t.Run("Topology A", func(t *testing.T) {
		t.Setenv("LLM_GATEWAY_URL", srvA.URL+"/v1")
		rep := &DoctorReport{AgentName: "shared-infra"}
		checkLocalInferenceSlots(ctx, rep)

		if len(rep.Checks) != 1 {
			t.Fatalf("expected 1 check, got %d", len(rep.Checks))
		}
		c := rep.Checks[0]
		if c.Status != StatusOK {
			t.Errorf("expected StatusOK, got %s", c.Status)
		}
		if !strings.Contains(c.Message, "Topology A (4 slots @ 32k context, 0 active, 4 idle)") {
			t.Errorf("unexpected message: %s", c.Message)
		}
	})

	// 2. Healthy Topology B (2 slots @ 64k)
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status": "ok"}`))
		case "/slots":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[
				{"id": 0, "state": 1, "is_processing": true, "n_ctx": 65536},
				{"id": 1, "state": 0, "is_processing": false, "n_ctx": 65536}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srvB.Close()

	t.Run("Topology B", func(t *testing.T) {
		t.Setenv("LLM_GATEWAY_URL", srvB.URL)
		rep := &DoctorReport{AgentName: "shared-infra"}
		checkLocalInferenceSlots(ctx, rep)

		if len(rep.Checks) != 1 {
			t.Fatalf("expected 1 check, got %d", len(rep.Checks))
		}
		c := rep.Checks[0]
		if c.Status != StatusOK {
			t.Errorf("expected StatusOK, got %s", c.Status)
		}
		if !strings.Contains(c.Message, "Topology B (2 slots @ 64k context, 1 active, 1 idle)") {
			t.Errorf("unexpected message: %s", c.Message)
		}
	})

	// 3. Custom Topology
	srvCustom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/slots":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id": 0, "state": 0, "is_processing": false, "n_ctx": 16384}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srvCustom.Close()

	t.Run("Custom Topology", func(t *testing.T) {
		t.Setenv("LLM_GATEWAY_URL", srvCustom.URL)
		rep := &DoctorReport{AgentName: "shared-infra"}
		checkLocalInferenceSlots(ctx, rep)

		c := rep.Checks[0]
		if c.Status != StatusOK || !strings.Contains(c.Message, "Custom (1 slots @ 16k context, 0 active, 1 idle)") {
			t.Errorf("unexpected check item: %+v", c)
		}
	})

	// 4. Zero slots configured
	srvEmpty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/slots":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer srvEmpty.Close()

	t.Run("Zero slots", func(t *testing.T) {
		t.Setenv("LLM_GATEWAY_URL", srvEmpty.URL)
		rep := &DoctorReport{AgentName: "shared-infra"}
		checkLocalInferenceSlots(ctx, rep)

		c := rep.Checks[0]
		if c.Status != StatusOK || !strings.Contains(c.Message, "0 slots configured") {
			t.Errorf("unexpected check item: %+v", c)
		}
	})

	// 5. Model loading (503)
	srvLoading := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status": "loading model"}`))
		}
	}))
	defer srvLoading.Close()

	t.Run("Model loading (503)", func(t *testing.T) {
		t.Setenv("LLM_GATEWAY_URL", srvLoading.URL)
		rep := &DoctorReport{AgentName: "shared-infra"}
		checkLocalInferenceSlots(ctx, rep)

		c := rep.Checks[0]
		if c.Status != StatusOK || !strings.Contains(c.Message, "loading (HTTP 503)") {
			t.Errorf("unexpected check item: %+v", c)
		}
	})

	// 6. /health unexpected status code (500)
	srvErrHealth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srvErrHealth.Close()

	t.Run("Health 500 error", func(t *testing.T) {
		t.Setenv("LLM_GATEWAY_URL", srvErrHealth.URL)
		rep := &DoctorReport{AgentName: "shared-infra"}
		checkLocalInferenceSlots(ctx, rep)

		c := rep.Checks[0]
		if c.Status != StatusWarning || !strings.Contains(c.Message, "unexpected /health HTTP status 500") {
			t.Errorf("unexpected check item: %+v", c)
		}
		if rep.WarningCount != 1 {
			t.Errorf("expected warning count 1, got %d", rep.WarningCount)
		}
	})

	// 7. /slots unexpected status code (500)
	srvErrSlots := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/slots":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srvErrSlots.Close()

	t.Run("Slots 500 error", func(t *testing.T) {
		t.Setenv("LLM_GATEWAY_URL", srvErrSlots.URL)
		rep := &DoctorReport{AgentName: "shared-infra"}
		checkLocalInferenceSlots(ctx, rep)

		c := rep.Checks[0]
		if c.Status != StatusWarning || !strings.Contains(c.Message, "unexpected /slots HTTP status 500") {
			t.Errorf("unexpected check item: %+v", c)
		}
		if rep.WarningCount != 1 {
			t.Errorf("expected warning count 1, got %d", rep.WarningCount)
		}
	})

	// 8. /slots unparseable JSON
	srvBadJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/slots":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`invalid-json-body`))
		}
	}))
	defer srvBadJSON.Close()

	t.Run("Slots bad JSON", func(t *testing.T) {
		t.Setenv("LLM_GATEWAY_URL", srvBadJSON.URL)
		rep := &DoctorReport{AgentName: "shared-infra"}
		checkLocalInferenceSlots(ctx, rep)

		c := rep.Checks[0]
		if c.Status != StatusWarning || !strings.Contains(c.Message, "unparseable /slots JSON") {
			t.Errorf("unexpected check item: %+v", c)
		}
	})

	// 9. Server offline / unreachable
	t.Run("Offline server", func(t *testing.T) {
		t.Setenv("LLM_GATEWAY_URL", "http://127.0.0.1:65432")
		rep := &DoctorReport{AgentName: "shared-infra"}
		checkLocalInferenceSlots(ctx, rep)

		c := rep.Checks[0]
		if c.Status != StatusOK || !strings.Contains(c.Message, "is not currently running on :11434") {
			t.Errorf("unexpected check item for offline server: %+v", c)
		}
		if rep.UnrepairableCount != 0 || rep.WarningCount != 0 {
			t.Errorf("offline server should not trigger warnings or unrepairable counts")
		}
	})

	// 10. Default candidate URLs fallback
	t.Run("Default candidate URLs fallback", func(t *testing.T) {
		t.Setenv("LLM_GATEWAY_URL", "")
		candidates := inferenceCandidateURLs()
		if len(candidates) != 3 {
			t.Errorf("expected 3 default candidate URLs, got %d", len(candidates))
		}
	})

	// 11. Multi-candidate endpoint probing fallback on non-200
	t.Run("Multi-candidate fallback on non-200", func(t *testing.T) {
		t.Setenv("LLM_GATEWAY_URL", srvErrHealth.URL+","+srvA.URL)
		rep := &DoctorReport{AgentName: "shared-infra"}
		checkLocalInferenceSlots(ctx, rep)

		if len(rep.Checks) != 1 {
			t.Fatalf("expected 1 check, got %d", len(rep.Checks))
		}
		c := rep.Checks[0]
		if c.Status != StatusOK {
			t.Errorf("expected StatusOK from healthy second candidate, got %s: %s", c.Status, c.Message)
		}
		if rep.WarningCount != 0 {
			t.Errorf("expected 0 warnings, got %d", rep.WarningCount)
		}
	})
}
