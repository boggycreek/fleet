// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package harness

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
	"github.com/boggycreek/sandbox/backplane/pkg/libbp/resp"
	"github.com/boggycreek/sandbox/pkg/forgejo"
)

// EphemeralInfraHarness manages isolated Valkey and Forgejo containers for end-to-end integration tests.
type EphemeralInfraHarness struct {
	ValkeyPort      int
	ForgejoHTTPPort int
	ForgejoSSHPort  int
	GiteaHTTPPort   int // Compatibility alias
	GiteaSSHPort    int // Compatibility alias
	AdminPassword   string
	HumanPassword   string
	HumanName       string
	NetworkName     string
	ValkeyName      string
	ForgejoName     string
	GiteaName       string // Compatibility alias
	ValkeyVolName   string
	ForgejoVolName  string
	GiteaVolName    string // Compatibility alias
	DataHome        string
	BinPath         string
	engine          string
	t               *testing.T
	teardownOnce    sync.Once
}

// StartEphemeralInfraHarness starts an isolated Valkey and Forgejo stack with unique ephemeral names and dynamic ports.
func StartEphemeralInfraHarness(t *testing.T) *EphemeralInfraHarness {
	t.Helper()

	engine := detectContainerEngine()
	if engine == "" {
		t.Skip("Podman container engine not found on PATH; skipping ephemeral infra integration test")
	}

	dataHome := t.TempDir()

	valkeyPort, err := getFreePort()
	if err != nil {
		t.Fatalf("failed finding free port for test valkey: %v", err)
	}

	forgejoHTTPPort, err := getFreePort()
	if err != nil {
		t.Fatalf("failed finding free port for test forgejo HTTP: %v", err)
	}

	forgejoSSHPort, err := getFreePort()
	if err != nil {
		t.Fatalf("failed finding free port for test forgejo SSH: %v", err)
	}

	suffix := fmt.Sprintf("%d-%d", os.Getpid(), valkeyPort)
	h := &EphemeralInfraHarness{
		ValkeyPort:      valkeyPort,
		ForgejoHTTPPort: forgejoHTTPPort,
		ForgejoSSHPort:  forgejoSSHPort,
		GiteaHTTPPort:   forgejoHTTPPort,
		GiteaSSHPort:    forgejoSSHPort,
		AdminPassword:   "test_admin_backplane_pass",
		HumanPassword:   "test_human_backplane_pass",
		HumanName:       "tester",
		NetworkName:     fmt.Sprintf("test-infra-net-%s", suffix),
		ValkeyName:      fmt.Sprintf("test-infra-valkey-%s", suffix),
		ForgejoName:     fmt.Sprintf("test-infra-forgejo-%s", suffix),
		GiteaName:       fmt.Sprintf("test-infra-forgejo-%s", suffix),
		ValkeyVolName:   fmt.Sprintf("test-infra-valkey-data-%s", suffix),
		ForgejoVolName:  fmt.Sprintf("test-infra-forgejo-data-%s", suffix),
		GiteaVolName:    fmt.Sprintf("test-infra-forgejo-data-%s", suffix),
		DataHome:        dataHome,
		engine:          engine,
		t:               t,
	}

	// Register automatic teardown
	t.Cleanup(func() {
		h.Teardown()
	})

	// Build isolated fleet binary in temp dir
	binDir := t.TempDir()
	h.BinPath = filepath.Join(binDir, "fleet")
	buildCmd := exec.Command("go", "build", "-o", h.BinPath, "github.com/boggycreek/sandbox/cmd/fleet")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed building test fleet binary: %v (%s)", err, string(out))
	}

	// Initialize ephemeral infrastructure
	h.startContainers()
	h.waitForReady()

	return h
}

func (h *EphemeralInfraHarness) startContainers() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Create isolated bridge network
	_ = exec.CommandContext(ctx, h.engine, "network", "create", h.NetworkName).Run()

	// 2. Create isolated volumes
	_ = exec.CommandContext(ctx, h.engine, "volume", "create", h.ValkeyVolName).Run()
	_ = exec.CommandContext(ctx, h.engine, "volume", "create", h.GiteaVolName).Run()

	// 3. Write Valkey ACL file
	valkeyConfigDir := filepath.Join(h.DataHome, "valkey")
	_ = os.MkdirAll(valkeyConfigDir, 0700)
	aclFile := filepath.Join(valkeyConfigDir, "valkey-users.acl")

	aclContent := fmt.Sprintf(`user default off
user admin on >%s ~* &* +@all
user %s on >%s ~%s:* ~human:name ~liaison:current ~poll-interval ~identity:* ~specialty:* %%R~*:* &* +@all (+xadd ~*:inbox)
`, h.AdminPassword, h.HumanName, h.HumanPassword, h.HumanName)
	if err := os.WriteFile(aclFile, []byte(aclContent), 0600); err != nil {
		h.t.Fatalf("failed writing test valkey ACL file: %v", err)
	}

	// 4. Start ephemeral Valkey container
	valkeyArgs := []string{
		"run", "-d",
		"--name", h.ValkeyName,
		"--hostname", "valkey",
		"--network", h.NetworkName,
		"-p", fmt.Sprintf("127.0.0.1:%d:6379", h.ValkeyPort),
		"-v", fmt.Sprintf("%s:/etc/valkey:z", valkeyConfigDir),
		"-v", fmt.Sprintf("%s:/data:z", h.ValkeyVolName),
		"docker.io/valkey/valkey:8.0-alpine",
		"valkey-server", "--aclfile", "/etc/valkey/valkey-users.acl", "--appendonly", "yes", "--port", "6379",
	}
	if out, err := exec.CommandContext(ctx, h.engine, valkeyArgs...).CombinedOutput(); err != nil {
		h.t.Fatalf("failed starting ephemeral Valkey container: %v (%s)", err, string(out))
	}

	// 5. Start ephemeral Forgejo container
	forgejoArgs := []string{
		"run", "-d",
		"--name", h.ForgejoName,
		"--hostname", "forgejo",
		"--network", h.NetworkName,
		"--network-alias", "forgejo",
		"--network-alias", "gitea",
		"-p", fmt.Sprintf("127.0.0.1:%d:3000", h.ForgejoHTTPPort),
		"-p", fmt.Sprintf("127.0.0.1:%d:2222", h.ForgejoSSHPort),
		"-e", "FORGEJO__server__DOMAIN=forgejo",
		"-e", "FORGEJO__server__HTTP_PORT=3000",
		"-e", "FORGEJO__server__ROOT_URL=http://forgejo:3000/",
		"-e", "FORGEJO__server__SSH_PORT=2222",
		"-e", "FORGEJO__server__SSH_LISTEN_PORT=2222",
		"-e", "FORGEJO__database__DB_TYPE=sqlite3",
		"-e", "FORGEJO__database__PATH=/var/lib/gitea/data/gitea.db",
		"-e", "FORGEJO__service__DISABLE_REGISTRATION=false",
		"-e", "FORGEJO__service__REQUIRE_SIGNIN_VIEW=false",
		"-e", "FORGEJO__security__INSTALL_LOCK=true",
		"-v", fmt.Sprintf("%s:/var/lib/gitea:z", h.ForgejoVolName),
		"codeberg.org/forgejo/forgejo:9-rootless",
	}
	if out, err := exec.CommandContext(ctx, h.engine, forgejoArgs...).CombinedOutput(); err != nil {
		h.t.Fatalf("failed starting ephemeral Forgejo container: %v (%s)", err, string(out))
	}
}

func (h *EphemeralInfraHarness) waitForReady() {
	// Wait for Valkey
	deadline := time.Now().Add(15 * time.Second)
	valkeyReady := false
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		client, err := libbp.Dial(ctx, libbp.ClientConfig{
			Host:     "127.0.0.1",
			Port:     h.ValkeyPort,
			Username: "admin",
			Password: h.AdminPassword,
		})
		cancel()
		if err == nil {
			val, err := client.Exec(context.Background(), "PING")
			client.Close()
			if err == nil && val.Type == resp.TypeSimpleString && val.Str == "PONG" {
				valkeyReady = true
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !valkeyReady {
		h.t.Fatalf("timed out waiting for test Valkey container on port %d", h.ValkeyPort)
	}

	// Wait for Forgejo
	forgejoURL := fmt.Sprintf("http://127.0.0.1:%d", h.ForgejoHTTPPort)
	httpClient := &http.Client{Timeout: 1 * time.Second}
	deadlineForgejo := time.Now().Add(25 * time.Second)
	forgejoReady := false
	for time.Now().Before(deadlineForgejo) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, forgejoURL+"/api/v1/version", nil)
		resp, err := httpClient.Do(req)
		cancel()
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			forgejoReady = true
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(300 * time.Millisecond)
	}
	if !forgejoReady {
		h.t.Fatalf("timed out waiting for test Forgejo container on port %d", h.ForgejoHTTPPort)
	}

	// Bootstrap Forgejo admin users & fleet org
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_ = exec.CommandContext(ctx, h.engine, "exec", h.ForgejoName,
		"forgejo", "admin", "user", "create",
		"--admin",
		"--username", "forgejoadmin",
		"--password", h.AdminPassword,
		"--email", "forgejoadmin@local.fleet",
		"--must-change-password=false",
	).Run()

	_ = exec.CommandContext(ctx, h.engine, "exec", h.ForgejoName,
		"forgejo", "admin", "user", "create",
		"--admin",
		"--username", "giteaadmin",
		"--password", h.AdminPassword,
		"--email", "giteaadmin@local.fleet",
		"--must-change-password=false",
	).Run()

	client := forgejo.NewClient(forgejo.ClientConfig{
		BaseURL:   forgejoURL,
		AdminUser: "forgejoadmin",
		AdminPass: h.AdminPassword,
		Timeout:   3 * time.Second,
	})

	_ = client.EnsureOrg(ctx, "fleet")
	_ = client.EnsureRepo(ctx, "fleet", "tools", "Fleet shared tools", true)
	_ = client.EnsureRepo(ctx, "fleet", "tasks", "Fleet shared tasks", true)
}

// Env returns an environment variable slice configured for this ephemeral harness.
func (h *EphemeralInfraHarness) Env() []string {
	return []string{
		fmt.Sprintf("XDG_DATA_HOME=%s", h.DataHome),
		fmt.Sprintf("XDG_CONFIG_HOME=%s", h.DataHome),
		"BP_HOST=127.0.0.1",
		fmt.Sprintf("BP_PORT=%d", h.ValkeyPort),
		fmt.Sprintf("ADMIN_BACKPLANE_PASSWORD=%s", h.AdminPassword),
		fmt.Sprintf("HUMAN_BACKPLANE_PASSWORD=%s", h.HumanPassword),
		fmt.Sprintf("HUMAN_NAME=%s", h.HumanName),
		fmt.Sprintf("FORGEJO_URL=http://127.0.0.1:%d", h.ForgejoHTTPPort),
		fmt.Sprintf("FORGEJO_ADMIN_PASSWORD=%s", h.AdminPassword),
		fmt.Sprintf("GITEA_URL=http://127.0.0.1:%d", h.ForgejoHTTPPort),
		fmt.Sprintf("GITEA_ADMIN_PASSWORD=%s", h.AdminPassword),
		"SONAR_HOST_URL=http://127.0.0.1:65532",
		"SONARQUBE_URL=http://127.0.0.1:65532",
		"SONAR_DISABLED=1",
	}
}

// ExecFleet runs the test fleet binary with the harness environment.
func (h *EphemeralInfraHarness) ExecFleet(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, h.BinPath, args...)
	cmd.Env = append(os.Environ(), h.Env()...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ExecSndbx runs the test fleet binary (backward compatibility alias).
func (h *EphemeralInfraHarness) ExecSndbx(ctx context.Context, args ...string) (string, error) {
	return h.ExecFleet(ctx, args...)
}

// Teardown stops and forcefully removes all ephemeral containers, volumes, networks, and directories.
func (h *EphemeralInfraHarness) Teardown() {
	h.teardownOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if h.engine != "" {
			_ = exec.CommandContext(ctx, h.engine, "stop", h.ValkeyName, h.ForgejoName).Run()
			_ = exec.CommandContext(ctx, h.engine, "rm", "-f", h.ValkeyName, h.ForgejoName).Run()
			_ = exec.CommandContext(ctx, h.engine, "volume", "rm", "-f", h.ValkeyVolName, h.ForgejoVolName).Run()
			_ = exec.CommandContext(ctx, h.engine, "network", "rm", "-f", h.NetworkName).Run()
		}
	})
}

func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func detectContainerEngine() string {
	if _, err := exec.LookPath("podman"); err == nil {
		return "podman"
	}
	return ""
}
