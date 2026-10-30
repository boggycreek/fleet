// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package harness

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
	"github.com/boggycreek/sandbox/backplane/pkg/libbp/resp"
)

// ValkeyHarness manages an isolated, authentic Valkey instance for integration testing
type ValkeyHarness struct {
	Port        int
	AdminPass   string
	HumanPass   string
	Agent1Pass  string
	Agent2Pass  string
	tempDir     string
	containerID string
	engine      string
	serverCmd   *exec.Cmd
	t           *testing.T
}

// StartValkeyHarness initializes and starts an isolated Valkey instance with ACLs
func StartValkeyHarness(t *testing.T) *ValkeyHarness {
	t.Helper()

	tempDir := t.TempDir()
	adminPass := "admin_secret_pass"
	humanPass := "human_secret_pass"
	agent1Pass := "agent1_secret_pass"
	agent2Pass := "agent2_secret_pass"

	// Find an available free local port
	port, err := getFreePort()
	if err != nil {
		t.Fatalf("failed finding free port for Valkey harness: %v", err)
	}

	// Render ACL file
	aclContent := fmt.Sprintf(`user default off
user admin on >%s ~* &* +@all
user operator on >%s ~operator:* ~human:name ~liaison:current ~poll-interval ~identity:* ~ensemble:* ~specialty:* %%R~*:* &* +@all (+xadd ~*:inbox)
user agent-1 on >%s ~agent-1:* ~identity:agent-1 ~poll-interval ~ensemble:* %%R~*:* %%R~specialty:* &* +@all -@admin -@dangerous (+xadd ~*:inbox)
user agent-2 on >%s ~agent-2:* ~identity:agent-2 ~poll-interval ~ensemble:* %%R~*:* %%R~specialty:* &* +@all -@admin -@dangerous (+xadd ~*:inbox)
`, adminPass, humanPass, agent1Pass, agent2Pass)

	aclPath := filepath.Join(tempDir, "valkey-users.acl")
	if err := os.WriteFile(aclPath, []byte(aclContent), 0644); err != nil {
		t.Fatalf("failed writing test ACL file: %v", err)
	}

	h := &ValkeyHarness{
		Port:       port,
		AdminPass:  adminPass,
		HumanPass:  humanPass,
		Agent1Pass: agent1Pass,
		Agent2Pass: agent2Pass,
		tempDir:    tempDir,
		t:          t,
	}

	// Determine start method: Podman/Docker or local valkey-server binary
	engine := detectContainerEngine()
	if engine != "" {
		h.engine = engine
		h.startContainer(aclPath)
	} else if _, err := exec.LookPath("valkey-server"); err == nil {
		h.startBinary(aclPath)
	} else if _, err := exec.LookPath("redis-server"); err == nil {
		h.startBinary(aclPath)
	} else {
		t.Skip("Skipping integration test: neither container engine (podman) nor valkey-server/redis-server found on PATH")
	}

	h.waitForReady()
	t.Cleanup(func() {
		h.Teardown()
	})
	return h
}

func (h *ValkeyHarness) startContainer(aclPath string) {
	containerName := fmt.Sprintf("test-valkey-%d", h.Port)
	image := "docker.io/valkey/valkey:8-alpine"

	cmd := exec.Command(h.engine, "run", "-d", "--rm",
		"--name", containerName,
		"-p", fmt.Sprintf("127.0.0.1:%d:6379", h.Port),
		"-v", fmt.Sprintf("%s:/etc/valkey/users.acl:ro", aclPath),
		image,
		"valkey-server", "--aclfile", "/etc/valkey/users.acl",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("failed starting valkey container with %s: %v (output: %s)", h.engine, err, string(out))
	}
	h.containerID = strings.TrimSpace(string(out))
}

func (h *ValkeyHarness) startBinary(aclPath string) {
	serverBin := "valkey-server"
	if _, err := exec.LookPath(serverBin); err != nil {
		serverBin = "redis-server"
	}

	cmd := exec.Command(serverBin,
		"--port", strconv.Itoa(h.Port),
		"--aclfile", aclPath,
		"--dir", h.tempDir,
	)

	if err := cmd.Start(); err != nil {
		h.t.Fatalf("failed starting %s binary: %v", serverBin, err)
	}
	h.serverCmd = cmd
}

func (h *ValkeyHarness) waitForReady() {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		client, err := libbp.Dial(ctx, libbp.ClientConfig{
			Host:     "127.0.0.1",
			Port:     h.Port,
			Username: "admin",
			Password: h.AdminPass,
		})
		cancel()
		if err == nil {
			val, err := client.Exec(context.Background(), "PING")
			client.Close()
			if err == nil && val.Type == resp.TypeSimpleString && val.Str == "PONG" {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for Valkey harness to become ready on port %d", h.Port)
}

// Teardown cleanly stops and removes the test Valkey container or process
func (h *ValkeyHarness) Teardown() {
	if h.containerID != "" && h.engine != "" {
		_ = exec.Command(h.engine, "stop", h.containerID).Run()
		_ = exec.Command(h.engine, "rm", "-f", h.containerID).Run()
	}
	if h.serverCmd != nil && h.serverCmd.Process != nil {
		_ = h.serverCmd.Process.Kill()
	}
}

// ClientFor returns an authenticated libbp.Client for the given identity
func (h *ValkeyHarness) ClientFor(id string) (*libbp.Client, error) {
	var user, pass string
	switch strings.ToLower(id) {
	case "admin":
		user, pass = "admin", h.AdminPass
	case "operator", "human":
		user, pass = "operator", h.HumanPass
	case "agent-1":
		user, pass = "agent-1", h.Agent1Pass
	case "agent-2":
		user, pass = "agent-2", h.Agent2Pass
	default:
		return nil, fmt.Errorf("unknown test identity %s", id)
	}

	priv, _, _ := libbp.GenerateKeypair()

	return libbp.Dial(context.Background(), libbp.ClientConfig{
		Host:       "127.0.0.1",
		Port:       h.Port,
		Username:   user,
		Password:   pass,
		AgentID:    id,
		SigningKey: priv,
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
