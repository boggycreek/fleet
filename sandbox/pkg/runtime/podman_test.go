// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/sandbox/pkg/config"
)

func TestPodmanHelpers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Network / Volume helpers
	_ = EnsureNetwork(ctx, "test-sndbx-net")
	_ = EnsureVolume(ctx, "test-sndbx-vol")

	// Struct instantiation & serialization tests
	info := &ContainerInfo{
		ID:    "12345",
		Names: []string{"test-container"},
		State: "running",
		Ports: []PortMapping{
			{HostPort: 32000, ContainerPort: 2222, HostIP: "127.0.0.1"},
		},
	}
	if info.State != "running" || len(info.Ports) != 1 {
		t.Errorf("unexpected ContainerInfo struct: %+v", info)
	}

	status := AgentStatus{
		Name:           "agent-test",
		Role:           "coder",
		Image:          "fleet-base:latest",
		ContainerName:  "fleet-agent-test",
		ContainerState: "stopped",
		SSHPort:        0,
		IDEConnect:     "-",
	}
	if status.Name != "agent-test" {
		t.Errorf("unexpected AgentStatus struct: %+v", status)
	}

	// Clean / Stop non-existent containers shouldn't crash
	_ = StopAgentContainer(ctx, "nonexistent-container-999")
	_ = CleanAgentContainer(ctx, "nonexistent-container-999")
	_ = DestroyAgentContainer(ctx, "nonexistent-container-999", "nonexistent-vol-999")

	// Inspect nonexistent
	_, _ = InspectAgentContainer(ctx, "nonexistent-container-999")
	_, _ = GetAgentSSHPort(ctx, "nonexistent-container-999")

	// Start container error check with nonexistent image
	cfg := &config.AgentConfig{
		Name:          "test-agent-run",
		ContainerName: "fleet-agent-nonexistent-test",
		VolumeName:    "fleet-agent-nonexistent-vol",
		Image:         "nonexistent-image-404:notfound",
	}
	paths := config.GetPaths()
	err := StartAgentContainer(ctx, cfg, paths, "127.0.0.1", 6379)
	if err == nil {
		t.Errorf("expected error starting container with nonexistent image")
	}

	// Start real container with alpine/sleep if podman works
	if _, err := exec.LookPath("podman"); err == nil {
		realCfg := &config.AgentConfig{
			Name:          "real-agent-test",
			ContainerName: fmt.Sprintf("test-podman-rt-%d", time.Now().UnixNano()),
			VolumeName:    fmt.Sprintf("test-vol-rt-%d", time.Now().UnixNano()),
			Image:         "docker.io/library/alpine:latest",
		}
		_ = CleanAgentContainer(ctx, realCfg.ContainerName)
		// Run alpine
		runCmd := exec.CommandContext(ctx, "podman", "run", "-d",
			"--name", realCfg.ContainerName,
			"-p", "127.0.0.1::2222",
			"docker.io/library/alpine:latest",
			"sleep", "30",
		)
		if err := runCmd.Run(); err == nil {
			// Query port
			port, err := GetAgentSSHPort(ctx, realCfg.ContainerName)
			if err == nil && port > 0 {
				_ = port
			}
			// Inspect
			inf, err := InspectAgentContainer(ctx, realCfg.ContainerName)
			if err == nil && inf != nil {
				_ = inf.State
			}
			// Test start on existing container branch in StartAgentContainer
			_ = StartAgentContainer(ctx, realCfg, paths, "127.0.0.1", 6379)

			_ = StopAgentContainer(ctx, realCfg.ContainerName)
			_ = CleanAgentContainer(ctx, realCfg.ContainerName)
			_ = DestroyAgentContainer(ctx, realCfg.ContainerName, realCfg.VolumeName)
		}
	}
}

func TestResolveAgentImage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Presets
	img, _ := ResolveAgentImage(ctx, "base")
	if img != "fleet-base:latest" {
		t.Errorf("expected fleet-base:latest, got %s", img)
	}
	img, _ = ResolveAgentImage(ctx, "")
	if img != "fleet-base:latest" {
		t.Errorf("expected empty to resolve to fleet-base:latest, got %s", img)
	}
	img, _ = ResolveAgentImage(ctx, "opencode")
	if img != "fleet-opencode:latest" {
		t.Errorf("expected opencode preset, got %s", img)
	}
	img, _ = ResolveAgentImage(ctx, "claude")
	if img != "fleet-claude:latest" {
		t.Errorf("expected claude preset, got %s", img)
	}
	img, _ = ResolveAgentImage(ctx, "agy")
	if img != "fleet-agy:latest" {
		t.Errorf("expected agy preset, got %s", img)
	}
	img, _ = ResolveAgentImage(ctx, "pig")
	if img != "fleet-pig:latest" {
		t.Errorf("expected pig preset, got %s", img)
	}
	img, _ = ResolveAgentImage(ctx, "egress")
	if img != "fleet-egress:latest" {
		t.Errorf("expected egress preset, got %s", img)
	}

	// 2. Remote OCI references
	img, isLocal := ResolveAgentImage(ctx, "quay.io/boggycreek/custom-bot:v1")
	if img != "quay.io/boggycreek/custom-bot:v1" || isLocal {
		t.Errorf("expected remote quay.io image not local, got %s (local: %v)", img, isLocal)
	}

	// 3. Untagged remote OCI reference
	img, _ = ResolveAgentImage(ctx, "ghcr.io/org/repo")
	if img != "ghcr.io/org/repo:latest" {
		t.Errorf("expected :latest appended to untagged remote OCI, got %s", img)
	}
}

func TestEgressFilterHelpers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. EgressContainerName testing
	if name := EgressContainerName("alice"); name != "fleet-agent-alice-egress" {
		t.Errorf("expected fleet-agent-alice-egress, got %s", name)
	}
	if name := EgressContainerName("fleet-agent-bob"); name != "fleet-agent-bob-egress" {
		t.Errorf("expected fleet-agent-bob-egress, got %s", name)
	}
	if name := EgressContainerName("fleet-charlie"); name != "fleet-charlie-egress" {
		t.Errorf("expected fleet-charlie-egress, got %s", name)
	}
	if name := EgressContainerName("fleet-agent-david-egress"); name != "fleet-agent-david-egress" {
		t.Errorf("expected fleet-agent-david-egress, got %s", name)
	}
	if name := EgressContainerName("sndbx-agent-bob"); name != "sndbx-agent-bob-egress" {
		t.Errorf("expected sndbx-agent-bob-egress, got %s", name)
	}

	// 2. StopEgressFilterContainer on nonexistent container
	_ = StopEgressFilterContainer(ctx, "nonexistent-egress-container")

	// 3. StartEgressFilterContainer failure on nonexistent image
	_ = StartEgressFilterContainer(ctx, "nonexistent-egress-target")
}

func TestMockedEgressFilter(t *testing.T) {
	ctx := context.Background()

	// 1. StartEgressFilterContainer: existing container starts successfully
	mockExisting := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			return exec.Command("true")
		}
		return exec.Command("true")
	}
	restore1 := SetExecCommandContextForTesting(mockExisting)
	err := StartEgressFilterContainer(ctx, "agent-test")
	restore1()
	if err != nil {
		t.Errorf("StartEgressFilterContainer existing failed: %v", err)
	}

	// 2. StartEgressFilterContainer: existing container fails to start
	mockExistingFail := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			return exec.Command("true")
		}
		if name == "podman" && len(args) > 0 && args[0] == "start" {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restore2 := SetExecCommandContextForTesting(mockExistingFail)
	err = StartEgressFilterContainer(ctx, "agent-test")
	restore2()
	if err == nil {
		t.Errorf("expected error when starting existing egress container fails")
	}

	// 3. StartEgressFilterContainer: new container creation succeeds
	mockNewSuccess := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			return exec.Command("false") // container doesn't exist
		}
		return exec.Command("true")
	}
	restore3 := SetExecCommandContextForTesting(mockNewSuccess)
	err = StartEgressFilterContainer(ctx, "agent-new")
	restore3()
	if err != nil {
		t.Errorf("StartEgressFilterContainer new failed: %v", err)
	}

	// 4. StartEgressFilterContainer: new container creation fails
	mockNewFail := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			return exec.Command("false") // container doesn't exist
		}
		if name == "podman" && len(args) > 0 && args[0] == "run" {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restore4 := SetExecCommandContextForTesting(mockNewFail)
	err = StartEgressFilterContainer(ctx, "agent-new-fail")
	restore4()
	if err == nil {
		t.Errorf("expected error when running new egress container fails")
	}

	// 5. GetAgentSSHPort fallback to egress container
	mockPortFallback := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "port" {
			// Fail for primary container, succeed for egress sidecar
			if strings.HasSuffix(args[1], "-egress") {
				return exec.Command("echo", "127.0.0.1:43210")
			}
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	restore5 := SetExecCommandContextForTesting(mockPortFallback)
	port, err := GetAgentSSHPort(ctx, "sndbx-agent-alice")
	restore5()
	if err != nil || port != 43210 {
		t.Errorf("expected fallback port 43210, got %d (err: %v)", port, err)
	}
}

func TestRootlessNetNSAutoHealing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. IsRootlessNetnsError helper
	if !IsRootlessNetnsError(fmt.Errorf("failed to mount runtime directory for rootless netns: no such file"), []byte("")) {
		t.Errorf("expected IsRootlessNetnsError to return true on netns error")
	}
	if !IsRootlessNetnsError(nil, []byte("Error: failed to mount runtime directory for rootless netns: no such file")) {
		t.Errorf("expected IsRootlessNetnsError to return true on stderr match")
	}
	if IsRootlessNetnsError(fmt.Errorf("normal failure"), []byte("regular error")) {
		t.Errorf("expected IsRootlessNetnsError to return false on non-netns error")
	}

	// 2. EnsureRootlessNetNS mock
	mockUnshareCalled := false
	mockNetns := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "unshare" && args[1] == "--rootless-netns" {
			mockUnshareCalled = true
			return exec.Command("true")
		}
		return exec.Command("true")
	}
	restore := SetExecCommandContextForTesting(mockNetns)
	err := EnsureRootlessNetNS(ctx)
	restore()
	if err != nil || !mockUnshareCalled {
		t.Errorf("EnsureRootlessNetNS failed or unshare not called: %v", err)
	}

	// 3. EnsureNetwork with rootless netns error and auto-heal
	unshareHealed := false
	mockNetHeal := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "network" && args[1] == "exists" {
			return exec.Command("false")
		}
		if name == "podman" && len(args) > 1 && args[0] == "network" && args[1] == "create" {
			if !unshareHealed {
				return exec.Command("sh", "-c", "echo 'failed to mount runtime directory for rootless netns' >&2; exit 127")
			}
			return exec.Command("true")
		}
		if name == "podman" && len(args) > 1 && args[0] == "unshare" {
			unshareHealed = true
			return exec.Command("true")
		}
		return exec.Command("true")
	}
	restoreNet := SetExecCommandContextForTesting(mockNetHeal)
	err = EnsureNetwork(ctx, "test-heal-net")
	restoreNet()
	if err != nil || !unshareHealed {
		t.Errorf("EnsureNetwork failed to auto-heal rootless netns: %v", err)
	}

	// 4. StartAgentContainer: existing container start auto-heals
	startRetried := false
	mockStartExisting := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			return exec.Command("true")
		}
		if name == "podman" && len(args) > 0 && args[0] == "start" {
			if !startRetried {
				return exec.Command("sh", "-c", "echo 'failed to mount runtime directory for rootless netns' >&2; exit 127")
			}
			return exec.Command("true")
		}
		if name == "podman" && len(args) > 0 && args[0] == "unshare" {
			startRetried = true
			return exec.Command("true")
		}
		return exec.Command("true")
	}
	restoreStart := SetExecCommandContextForTesting(mockStartExisting)
	cfg := &config.AgentConfig{Name: "existing-agent", ContainerName: "sndbx-agent-existing", VolumeName: "sndbx-vol"}
	err = StartAgentContainer(ctx, cfg, config.Paths{}, "127.0.0.1", 6379)
	restoreStart()
	if err != nil || !startRetried {
		t.Errorf("StartAgentContainer existing failed to auto-heal: %v", err)
	}

	// 5. StartAgentContainer: new container run auto-heals
	runRetried := false
	mockRunNew := func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 1 && args[0] == "container" && args[1] == "exists" {
			return exec.Command("false")
		}
		if name == "podman" && len(args) > 0 && args[0] == "run" {
			if !runRetried {
				return exec.Command("sh", "-c", "echo 'failed to mount runtime directory for rootless netns' >&2; exit 127")
			}
			return exec.Command("true")
		}
		if name == "podman" && len(args) > 0 && args[0] == "unshare" {
			runRetried = true
			return exec.Command("true")
		}
		return exec.Command("true")
	}
	restoreRun := SetExecCommandContextForTesting(mockRunNew)
	cfgNew := &config.AgentConfig{Name: "new-agent", ContainerName: "sndbx-agent-new", VolumeName: "sndbx-vol-new", Image: "alpine:latest"}
	err = StartAgentContainer(ctx, cfgNew, config.Paths{}, "127.0.0.1", 6379)
	restoreRun()
	if err != nil || !runRetried {
		t.Errorf("StartAgentContainer new failed to auto-heal: %v", err)
	}
}

func TestStartHostAgentContainer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	wsDir := filepath.Join(tmpDir, "workspace")
	_ = os.MkdirAll(wsDir, 0755)

	// 1. Nonexistent workspace path error
	cfgMissing := &config.AgentConfig{
		Name:          "host-missing",
		Runtime:       "host-container",
		WorkspacePath: filepath.Join(tmpDir, "missing-dir"),
	}
	if err := StartAgentContainer(ctx, cfgMissing, config.Paths{}, "127.0.0.1", 6379); err == nil {
		t.Errorf("expected error for nonexistent workspace path")
	}

	// 2. Existing container start
	var recordedCmds []string
	restoreMock := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		recordedCmds = append(recordedCmds, fmt.Sprintf("%s %s", name, strings.Join(args, " ")))
		if len(args) >= 3 && args[0] == "container" && args[1] == "exists" {
			return exec.Command("true")
		}
		return exec.Command("true")
	})
	defer restoreMock()

	cfgHost := &config.AgentConfig{
		Name:          "host-worker",
		Runtime:       "host-container",
		ContainerName: "fleet-agent-host-worker",
		VolumeName:    "fleet-agent-host-worker-home",
		WorkspacePath: wsDir,
		Image:         "fleet-tool-base:latest",
		ModelURL:      "http://localhost:8080/v1",
		ModelName:     "qwen2.5",
	}

	err := StartAgentContainer(ctx, cfgHost, config.Paths{}, "127.0.0.1", 6379)
	if err != nil {
		t.Errorf("StartAgentContainer failed on existing host container: %v", err)
	}
	if len(recordedCmds) == 0 || !strings.Contains(recordedCmds[len(recordedCmds)-1], "podman start fleet-agent-host-worker") {
		t.Errorf("expected podman start command, got: %v", recordedCmds)
	}

	// 3. New host container run with --net=host, keep-id, and workspace mount
	recordedCmds = nil
	SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		recordedCmds = append(recordedCmds, fmt.Sprintf("%s %s", name, strings.Join(args, " ")))
		if len(args) >= 3 && args[0] == "container" && args[1] == "exists" {
			return exec.Command("false")
		}
		return exec.Command("true")
	})

	err = StartAgentContainer(ctx, cfgHost, config.Paths{}, "127.0.0.1", 6379)
	if err != nil {
		t.Errorf("StartAgentContainer failed for new host container: %v", err)
	}

	var runCmd string
	for _, c := range recordedCmds {
		if strings.Contains(c, "podman run") {
			runCmd = c
			break
		}
	}
	if runCmd == "" {
		t.Fatalf("expected podman run command in: %v", recordedCmds)
	}
	if !strings.Contains(runCmd, "--network fleet-infra") {
		t.Errorf("expected --network fleet-infra in run command, got: %s", runCmd)
	}
	if !strings.Contains(runCmd, "--add-host llm-gateway:host-gateway") {
		t.Errorf("expected --add-host in run command, got: %s", runCmd)
	}
	if !strings.Contains(runCmd, "--userns=keep-id:uid=1000,gid=1000") {
		t.Errorf("expected keep-id userns mapping in run command, got: %s", runCmd)
	}
	expectedMount := fmt.Sprintf("-v %s:/home/agent/workspace:z", wsDir)
	if !strings.Contains(runCmd, expectedMount) {
		t.Errorf("expected workspace mount in run command, got: %s", runCmd)
	}
}

func TestStartAgentContainerSecrets(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var runArgs []string
	restoreMock := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) > 0 && args[0] == "container" && args[1] == "exists" {
			return exec.Command("false")
		}
		if name == "podman" && len(args) > 0 && args[0] == "run" {
			runArgs = args
		}
		return exec.Command("true")
	})
	defer restoreMock()

	cfg := &config.AgentConfig{
		Name:          "secret-agent",
		ContainerName: "fleet-agent-secret-agent",
		Password:      "supersecret-password",
		SigningKeyPEM: "supersecret-pem",
		ModelAPIKey:   "supersecret-openai",
	}

	err := StartAgentContainer(ctx, cfg, config.Paths{}, "127.0.0.1", 6379)
	if err != nil {
		t.Fatalf("StartAgentContainer failed: %v", err)
	}

	if len(runArgs) == 0 {
		t.Fatalf("expected podman run to be called")
	}

	for _, arg := range runArgs {
		if strings.Contains(arg, "supersecret") {
			t.Errorf("secret leaked in argv: %s", arg)
		}
	}
}

func TestValkeyACLPermissions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	paths := config.Paths{
		DataHome: tmpDir,
	}

	dummyPodman := filepath.Join(tmpDir, "podman")
	_ = os.WriteFile(dummyPodman, []byte("#!/bin/sh\nexit 0\n"), 0755)

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+oldPath)

	_ = RecreateValkeyContainer(ctx, paths, "admin", "human", "operator")

	valkeyConfigDir := filepath.Join(paths.DataHome, "valkey")
	aclFile := filepath.Join(valkeyConfigDir, "valkey-users.acl")

	dirInfo, err := os.Stat(valkeyConfigDir)
	if err != nil {
		t.Fatalf("failed to stat valkey config dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0700 {
		t.Errorf("expected valkey config dir to have permissions 0700, got %04o", dirInfo.Mode().Perm())
	}

	fileInfo, err := os.Stat(aclFile)
	if err != nil {
		t.Fatalf("failed to stat valkey users acl file: %v", err)
	}
	if fileInfo.Mode().Perm() != 0600 {
		t.Errorf("expected valkey users acl file to have permissions 0600, got %04o", fileInfo.Mode().Perm())
	}
}
