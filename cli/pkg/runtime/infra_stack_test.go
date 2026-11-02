// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/fleet/cli/pkg/config"
)

// TestStopInfraStack exercises StopInfraStack with ephemeral container names.
func TestStopInfraStack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pid := os.Getpid()
	testValkeyName := fmt.Sprintf("unit-test-valkey-stop-%d", pid)
	testValkeyGuardName := fmt.Sprintf("unit-test-valkey-guard-stop-%d", pid)
	testForgejoName := fmt.Sprintf("unit-test-forgejo-stop-%d", pid)
	testPostgresName := fmt.Sprintf("unit-test-postgres-stop-%d", pid)
	testSonarName := fmt.Sprintf("unit-test-sonar-stop-%d", pid)

	origValkey := infraValkeyContainer
	origValkeyGuard := infraValkeyGuardContainer
	origForgejo := infraForgejoContainer
	origPostgres := infraPostgresContainer
	origSonar := infraSonarContainer
	infraValkeyContainer = testValkeyName
	infraValkeyGuardContainer = testValkeyGuardName
	infraForgejoContainer = testForgejoName
	infraGiteaContainer = testForgejoName
	infraPostgresContainer = testPostgresName
	infraSonarContainer = testSonarName
	defer func() {
		infraValkeyContainer = origValkey
		infraValkeyGuardContainer = origValkeyGuard
		infraForgejoContainer = origForgejo
		infraGiteaContainer = origForgejo
		infraPostgresContainer = origPostgres
		infraSonarContainer = origSonar
	}()

	startOut, err := CommandContext(ctx, "podman", "run", "-d", "--name", testValkeyName,
		"docker.io/valkey/valkey:8-alpine",
	).CombinedOutput()
	if err != nil {
		t.Skipf("Cannot start ephemeral valkey container for stop test: %v (%s)", err, string(startOut))
	}
	defer func() {
		_ = exec.Command("podman", "rm", "-f", testValkeyName).Run()
		_ = exec.Command("podman", "rm", "-f", testValkeyGuardName).Run()
		_ = exec.Command("podman", "rm", "-f", testForgejoName).Run()
		_ = exec.Command("podman", "rm", "-f", testPostgresName).Run()
		_ = exec.Command("podman", "rm", "-f", testSonarName).Run()
	}()

	if err := StopInfraStack(ctx); err != nil {
		t.Errorf("StopInfraStack returned unexpected error: %v", err)
	}

	list, err := InspectInfraStack(ctx)
	if err != nil {
		t.Errorf("InspectInfraStack failed: %v", err)
	}
	if len(list) == 0 {
		t.Errorf("InspectInfraStack returned empty list")
	}
}

// TestInfraStackOrchestrationScenarios tests failure and recovery branches of StartInfraStack.
func TestInfraStackOrchestrationScenarios(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	paths := config.Paths{DataHome: tmpDir}

	// 1. EnsureNetwork error
	mockFailNet := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "network" {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restore1 := SetExecCommandContextForTesting(mockFailNet)
	err := StartInfraStack(ctx, paths, "p1", "p2", "u1")
	restore1()
	if err == nil {
		t.Errorf("expected error when network fails")
	}

	// 2. Valkey doesn't exist, run fails
	mockValkeyRunFail := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			return exec.Command("false")
		}
		if name == "podman" && len(args) > 0 && args[0] == "run" && strings.Contains(args[3], "valkey") {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restore2 := SetExecCommandContextForTesting(mockValkeyRunFail)
	err = StartInfraStack(ctx, paths, "p1", "p2", "u1")
	restore2()
	if err == nil {
		t.Errorf("expected error when valkey run fails")
	}

	// 3. Valkey exists, start fails, restart fails
	mockValkeyRestartFail := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			return exec.Command("true")
		}
		if name == "podman" && len(args) > 0 && args[0] == "start" {
			return exec.Command("false")
		}
		if name == "podman" && len(args) > 0 && args[0] == "run" {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restore3 := SetExecCommandContextForTesting(mockValkeyRestartFail)
	err = StartInfraStack(ctx, paths, "p1", "p2", "u1")
	restore3()
	if err == nil {
		t.Errorf("expected error when valkey restart fails")
	}

	// 3b. Valkey Guard doesn't exist, run fails
	mockValkeyGuardRunFail := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			if strings.Contains(args[2], "valkey-guard") {
				return exec.Command("false")
			}
			return exec.Command("true")
		}
		if name == "podman" && len(args) > 0 && args[0] == "run" && strings.Contains(args[3], "valkey-guard") {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restore3b := SetExecCommandContextForTesting(mockValkeyGuardRunFail)
	err = StartInfraStack(ctx, paths, "p1", "p2", "u1")
	restore3b()
	if err == nil {
		t.Errorf("expected error when valkey-guard run fails")
	}

	// 3c. Valkey Guard exists, start fails, restart fails
	mockValkeyGuardRestartFail := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			return exec.Command("true")
		}
		if name == "podman" && len(args) > 1 && args[0] == "start" && strings.Contains(args[1], "valkey-guard") {
			return exec.Command("false")
		}
		if name == "podman" && len(args) > 0 && args[0] == "run" && strings.Contains(args[3], "valkey-guard") {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restore3c := SetExecCommandContextForTesting(mockValkeyGuardRestartFail)
	err = StartInfraStack(ctx, paths, "p1", "p2", "u1")
	restore3c()
	if err == nil {
		t.Errorf("expected error when valkey-guard restart fails")
	}

	// 4. Forgejo doesn't exist, run fails
	mockForgejoRunFail := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			if strings.Contains(args[2], "forgejo") || strings.Contains(args[2], "gitea") {
				return exec.Command("false")
			}
			return exec.Command("true")
		}
		if name == "podman" && len(args) > 0 && args[0] == "run" && (strings.Contains(args[3], "forgejo") || strings.Contains(args[3], "gitea")) {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restore4 := SetExecCommandContextForTesting(mockForgejoRunFail)
	err = StartInfraStack(ctx, paths, "p1", "p2", "u1")
	restore4()
	if err == nil {
		t.Errorf("expected error when forgejo run fails")
	}

	// 5. Forgejo exists, start fails, restart fails
	mockForgejoRestartFail := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			return exec.Command("true")
		}
		if name == "podman" && len(args) > 1 && args[0] == "start" && (strings.Contains(args[1], "forgejo") || strings.Contains(args[1], "gitea")) {
			return exec.Command("false")
		}
		if name == "podman" && len(args) > 0 && args[0] == "run" && (strings.Contains(args[3], "forgejo") || strings.Contains(args[3], "gitea")) {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restore5 := SetExecCommandContextForTesting(mockForgejoRestartFail)
	err = StartInfraStack(ctx, paths, "p1", "p2", "u1")
	restore5()
	if err == nil {
		t.Errorf("expected error when forgejo restart fails")
	}

	// 6. Postgres doesn't exist, run fails
	mockPostgresRunFail := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			if strings.Contains(args[2], "postgres") {
				return exec.Command("false")
			}
			return exec.Command("true")
		}
		if name == "podman" && len(args) > 0 && args[0] == "run" && strings.Contains(args[3], "postgres") {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restore5b := SetExecCommandContextForTesting(mockPostgresRunFail)
	err = StartInfraStack(ctx, paths, "p1", "p2", "u1")
	restore5b()
	if err == nil {
		t.Errorf("expected error when postgres run fails")
	}

	// 7. Postgres exists, start fails, restart fails
	mockPostgresRestartFail := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			return exec.Command("true")
		}
		if name == "podman" && len(args) > 1 && args[0] == "start" && strings.Contains(args[1], "postgres") {
			return exec.Command("false")
		}
		if name == "podman" && len(args) > 0 && args[0] == "run" && strings.Contains(args[3], "postgres") {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restore5c := SetExecCommandContextForTesting(mockPostgresRestartFail)
	err = StartInfraStack(ctx, paths, "p1", "p2", "u1")
	restore5c()
	if err == nil {
		t.Errorf("expected error when postgres restart fails")
	}

	// 8. Sonarqube image exists, container doesn't exist, run succeeds
	mockSonarRunSuccess := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "image" && args[1] == "exists" {
			return exec.Command("true")
		}
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			if strings.Contains(args[2], "sonar") {
				return exec.Command("false")
			}
			return exec.Command("true")
		}
		return exec.Command("true")
	}
	restore6 := SetExecCommandContextForTesting(mockSonarRunSuccess)
	err = StartInfraStack(ctx, paths, "p1", "p2", "u1")
	restore6()
	if err != nil {
		t.Errorf("unexpected error in sonar run success: %v", err)
	}

	// 9. Rootless NetNS error branches on Valkey, Gitea, Postgres, Sonar
	mockNetnsRetry := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 0 && args[0] == "run" {
			return exec.Command("sh", "-c", "echo 'failed to mount runtime directory for rootless netns' >&2; exit 127")
		}
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			return exec.Command("false")
		}
		if name == "podman" && len(args) > 1 && args[0] == "image" && args[1] == "exists" {
			return exec.Command("true")
		}
		return exec.Command("true")
	}
	restore9 := SetExecCommandContextForTesting(mockNetnsRetry)
	_ = StartInfraStack(ctx, paths, "p1", "p2", "u1")
	restore9()

	// 10. Valkey exists with mount mismatch → triggers recreate branch
	mockMountMismatch := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			return exec.Command("true")
		}
		if name == "podman" && len(args) > 2 && args[0] == "inspect" && args[1] == "--type" && args[2] == "container" {
			return exec.Command("sh", "-c", `echo '[{"Id":"123","Name":"valkey","State":{"Status":"running","Running":true},"Mounts":[{"Destination":"/etc/valkey","Source":"/stale/path"}]}]'`)
		}
		return exec.Command("true")
	}
	restore10 := SetExecCommandContextForTesting(mockMountMismatch)
	_ = StartInfraStack(ctx, paths, "p1", "p2", "u1")
	restore10()
}

func TestBootstrapForgejoEdgeCases(t *testing.T) {
	// 1. Canceled context returns context error
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	err := BootstrapForgejo(canceledCtx, "pass")
	if err == nil {
		t.Errorf("expected canceled context error from BootstrapForgejo")
	}
	if errLegacy := BootstrapGitea(canceledCtx, "pass"); errLegacy == nil {
		t.Errorf("expected canceled context error from BootstrapGitea alias")
	}

	// 2. StartInfraStack with empty credentials (exercises defaults fallback)
	mockRun := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("true")
	}
	restore := SetExecCommandContextForTesting(mockRun)
	defer restore()

	paths := config.Paths{DataHome: t.TempDir()}
	if err := StartInfraStack(context.Background(), paths, "", "", ""); err != nil {
		t.Errorf("expected StartInfraStack with empty credentials to succeed with mocks: %v", err)
	}
}

func TestRecreateValkeyContainerScenarios(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{DataHome: t.TempDir()}

	// 1. EnsureNetwork fails
	mockFailNet := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "network" {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restore1 := SetExecCommandContextForTesting(mockFailNet)
	if err := RecreateValkeyContainer(ctx, paths, "p1", "p2", "u1"); err == nil {
		t.Errorf("expected error when network fails")
	}
	restore1()

	// 2. Podman run fails on valkey
	mockFailRun := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 0 && args[0] == "run" && strings.Contains(strings.Join(args, " "), "valkey-server") {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restore2 := SetExecCommandContextForTesting(mockFailRun)
	if err := RecreateValkeyContainer(ctx, paths, "p1", "p2", "u1"); err == nil {
		t.Errorf("expected error when run fails")
	}
	restore2()

	// 2b. Podman run fails on valkey-guard
	mockFailGuardRun := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 0 && args[0] == "run" && strings.Contains(strings.Join(args, " "), "valkey-guard") {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restore2b := SetExecCommandContextForTesting(mockFailGuardRun)
	if err := RecreateValkeyContainer(ctx, paths, "p1", "p2", "u1"); err == nil {
		t.Errorf("expected error when guard run fails")
	}
	restore2b()

	// 3. Podman run succeeds with empty credentials (fallback defaults)
	mockSuccess := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.Command("true")
	}
	restore3 := SetExecCommandContextForTesting(mockSuccess)
	if err := RecreateValkeyContainer(ctx, paths, "", "", ""); err != nil {
		t.Errorf("expected success with mocks: %v", err)
	}
	restore3()
}

func TestBootstrapSonar(t *testing.T) {
	// 1. Canceled context returns context error
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	err := BootstrapSonar(canceledCtx, "pass")
	if err == nil {
		t.Errorf("expected canceled context error from BootstrapSonar")
	}

	// 2. Successful already-configured scenario
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/system/status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"UP"}`))
		case "/api/users/current":
			user, pass, ok := r.BasicAuth()
			if ok && user == "admin" && pass == "AlreadyConfiguredPass" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	t.Setenv("SONAR_HOST_URL", server.URL)
	if err := BootstrapSonar(context.Background(), "AlreadyConfiguredPass"); err != nil {
		t.Errorf("expected BootstrapSonar to succeed on already-authenticated instance: %v", err)
	}

	// 3. Transition from default "admin:admin" to new password
	var changedPassword bool
	transitionServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/system/status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"UP"}`))
		case "/api/users/current":
			user, pass, ok := r.BasicAuth()
			if ok && user == "admin" && pass == "Admin_lowercasenewpass" && changedPassword {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		case "/api/users/change_password":
			user, pass, ok := r.BasicAuth()
			_ = r.ParseForm()
			if ok && user == "admin" && pass == "admin" {
				if r.FormValue("password") == "Admin_lowercasenewpass" {
					changedPassword = true
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
			w.WriteHeader(http.StatusBadRequest)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer transitionServer.Close()

	t.Setenv("SONAR_HOST_URL", transitionServer.URL)
	if err := BootstrapSonar(context.Background(), "lowercasenewpass"); err != nil {
		t.Errorf("expected BootstrapSonar to succeed transitioning password: %v", err)
	}
	if !changedPassword {
		t.Errorf("expected change_password to be called")
	}
}
