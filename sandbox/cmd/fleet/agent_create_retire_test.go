package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/boggycreek/sandbox/pkg/config"
)

func TestAgentCreateRollbackOnFailure(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	paths := config.GetPaths()
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("failed to ensure directories: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"message": "Internal Server Error"}`))
	}))
	defer ts.Close()

	t.Setenv("FORGEJO_URL", ts.URL)
	t.Setenv("FORGEJO_DISABLED", "0")

	code, out, errOut := runFleet([]string{"agent", "create", "fail-bot", "as", "base"})

	if code != 1 {
		t.Errorf("expected agent create to fail due to provisioning errors, got code %d. out: %s, errOut: %s", code, out, errOut)
	}

	if !strings.Contains(errOut, "rolling back partial state") {
		t.Errorf("expected rollback message in stderr, got: %s", errOut)
	}

	if _, err := config.LoadAgentConfig("fail-bot", paths); err == nil {
		t.Errorf("expected fail-bot config to be deleted after rollback")
	}
}

func TestAgentRetireForce(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	code, _, errOut := runFleet([]string{"agent", "retire", "missing-bot"})
	if code != 1 {
		t.Errorf("expected failure when retiring missing agent without force, got code %d", code)
	}
	if !strings.Contains(errOut, "error") {
		t.Errorf("expected error output, got: %s", errOut)
	}

	code, out, errOut := runFleet([]string{"agent", "retire", "missing-bot", "--force"})
	if !strings.Contains(errOut, "proceeding with forced cleanup") {
		t.Errorf("expected forced cleanup message, got: %s", errOut)
	}

	_ = out
}
