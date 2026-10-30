// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
	"github.com/boggycreek/fleet/backplane/pkg/libbp/resp"
	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/runtime"
)

func TestDoctor_ConfigSigningKeyPEM_Restoration(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	paths := config.Paths{
		DataHome:   filepath.Join(tmpDir, "data"),
		AgentsDir:  filepath.Join(tmpDir, "data", "agents"),
		SecretsDir: filepath.Join(tmpDir, "data", "secrets"),
	}
	_ = paths.EnsureDirectories()

	cfg, err := config.NewAgentConfig("key-restore-agent", "base", "coder")
	if err != nil {
		t.Fatalf("failed creating agent config: %v", err)
	}
	savedPEM := cfg.SigningKeyPEM

	// Save with key intact
	if err := config.SaveAgentConfig(cfg, paths); err != nil {
		t.Fatalf("failed saving initial config: %v", err)
	}

	// Now simulate corrupted/empty SigningKeyPEM in agent json file
	cfg.SigningKeyPEM = ""
	cfgJSONPath := filepath.Join(paths.AgentsDir, fmt.Sprintf("%s.json", cfg.Name))
	data, _ := json.MarshalIndent(cfg, "", "  ")
	_ = os.WriteFile(cfgJSONPath, data, 0600)

	report := &DoctorReport{AgentName: cfg.Name}
	repairedCfg, err := checkAndHealConfig(ctx, paths, cfg.Name, report)
	if err != nil {
		t.Fatalf("checkAndHealConfig failed: %v", err)
	}
	if repairedCfg.SigningKeyPEM != savedPEM {
		t.Errorf("expected SigningKeyPEM restored from disk, got %q", repairedCfg.SigningKeyPEM)
	}
	if report.HealedCount == 0 {
		t.Errorf("expected report to record healed signing key in config")
	}
}

func TestDoctor_DirectoryPermissions_0700(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SecretsDir:    filepath.Join(tmpDir, "data", "secrets"),
		BPProfilesDir: filepath.Join(tmpDir, "data", "profiles"),
	}
	_ = paths.EnsureDirectories()

	cfg, err := config.NewAgentConfig("perm-agent", "base", "coder")
	if err != nil {
		t.Fatalf("failed creating agent config: %v", err)
	}
	_ = config.SaveAgentConfig(cfg, paths)

	t.Run("SecretsDirAndKeyDirPermissions_0700", func(t *testing.T) {
		keyPath := filepath.Join(paths.SecretsDir, cfg.Name, "signing-key.pem")
		keyDir := filepath.Dir(keyPath)

		// Set to 0755
		_ = os.Chmod(paths.SecretsDir, 0755)
		_ = os.Chmod(keyDir, 0755)
		_ = os.Chmod(keyPath, 0600)

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealSigningKey(ctx, cfg, paths, report)

		if report.HealedCount == 0 {
			t.Errorf("expected directory permissions to be healed")
		}

		if fi, err := os.Stat(paths.SecretsDir); err != nil || fi.Mode().Perm() != 0700 {
			t.Errorf("expected SecretsDir mode 0700, got %o", fi.Mode().Perm())
		}
		if fi, err := os.Stat(keyDir); err != nil || fi.Mode().Perm() != 0700 {
			t.Errorf("expected agent secrets dir mode 0700, got %o", fi.Mode().Perm())
		}
	})

	t.Run("BPProfilesDirPermissions_0700", func(t *testing.T) {
		// Set BPProfilesDir to 0755
		_ = os.Chmod(paths.BPProfilesDir, 0755)

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealBPProfile(ctx, cfg, paths, report)

		if report.HealedCount == 0 {
			t.Errorf("expected BPProfilesDir to be healed")
		}
		if fi, err := os.Stat(paths.BPProfilesDir); err != nil || fi.Mode().Perm() != 0700 {
			t.Errorf("expected BPProfilesDir mode 0700, got %o", fi.Mode().Perm())
		}
	})
}

func TestDoctor_HostSSH_KeyPermissionsAndDerivationFailure(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	sshDir := filepath.Join(tmpDir, "ssh")
	_ = os.MkdirAll(sshDir, 0700)
	keyPath := filepath.Join(sshDir, "fleet_key")
	pubPath := keyPath + ".pub"

	// Generate real ed25519 key
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", "fleet-test", "-f", keyPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed generating ssh key: %v (%s)", err, string(out))
	}

	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		SSHDir:        sshDir,
		IDEKeyFile:    keyPath,
		SSHPrivateKey: keyPath,
		SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
	}
	_ = paths.EnsureDirectories()

	t.Run("PrivateKeyPermission_0600", func(t *testing.T) {
		_ = os.Chmod(keyPath, 0644)
		report := &DoctorReport{AgentName: "test-agent"}
		checkAndHealHostSSH(ctx, paths, report)

		if report.HealedCount == 0 {
			t.Errorf("expected private key permissions to be healed")
		}
		if fi, err := os.Stat(keyPath); err != nil || fi.Mode().Perm() != 0600 {
			t.Errorf("expected private key mode 0600, got %o", fi.Mode().Perm())
		}
	})

	t.Run("PublicKeyDerivationFailure_Unrepairable", func(t *testing.T) {
		// Remove .pub
		_ = os.Remove(pubPath)
		// Corrupt the private key so ssh-keygen -y fails
		_ = os.WriteFile(keyPath, []byte("invalid-private-key-data"), 0600)

		report := &DoctorReport{AgentName: "test-agent"}
		checkAndHealHostSSH(ctx, paths, report)

		if report.UnrepairableCount == 0 {
			t.Errorf("expected derivation failure to be reported as unrepairable error")
		}
		if len(report.Checks) == 0 || report.Checks[0].Status != StatusError {
			t.Errorf("expected StatusError on derivation failure, got %+v", report.Checks)
		}
	})
}

func TestDoctor_PodmanStorage_EnsureVolumeFailure(t *testing.T) {
	ctx := context.Background()

	t.Run("ContainerVolumeFailure", func(t *testing.T) {
		cfg := &config.AgentConfig{
			Name:       "vol-fail-agent",
			VolumeName: "fleet-vol-fail",
		}
		restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "podman" && len(args) >= 2 && args[0] == "volume" && args[1] == "exists" {
				return exec.Command("false")
			}
			if name == "podman" && len(args) >= 2 && args[0] == "volume" && args[1] == "create" {
				return exec.Command("false")
			}
			return exec.Command("true")
		}))
		defer restore()

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealPodmanStorage(ctx, cfg, report)

		if report.UnrepairableCount == 0 {
			t.Errorf("expected unrepairable error when EnsureVolume fails")
		}
		if len(report.Checks) == 0 || report.Checks[len(report.Checks)-1].Status != StatusError {
			t.Errorf("expected StatusError on EnsureVolume failure, got %+v", report.Checks)
		}
	})

	t.Run("HostContainerVolumeFailure", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Cleanup(func() {
			_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
		})

		cfg := &config.AgentConfig{
			Name:          "host-vol-fail-agent",
			Runtime:       "host-container",
			WorkspacePath: tmpDir,
			VolumeName:    "fleet-host-vol-fail",
		}
		restore := runtime.SetDefaultExecutor(runtime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
			if name == "podman" && len(args) >= 2 && args[0] == "volume" && args[1] == "exists" {
				return exec.Command("false")
			}
			if name == "podman" && len(args) >= 2 && args[0] == "volume" && args[1] == "create" {
				return exec.Command("false")
			}
			return exec.Command("true")
		}))
		defer restore()

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealPodmanStorage(ctx, cfg, report)

		if report.UnrepairableCount == 0 {
			t.Errorf("expected unrepairable error when EnsureVolume fails for host-container")
		}
		if len(report.Checks) == 0 || report.Checks[len(report.Checks)-1].Status != StatusError {
			t.Errorf("expected StatusError on EnsureVolume failure for host-container, got %+v", report.Checks)
		}
	})
}

func TestDoctor_SonarQube_TokenGenerationFailure(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	paths := config.Paths{
		DataHome:  filepath.Join(tmpDir, "data"),
		AgentsDir: filepath.Join(tmpDir, "data", "agents"),
	}
	_ = paths.EnsureDirectories()

	cfg := &config.AgentConfig{
		Name:     "sonar-token-fail",
		Password: "pass",
	}

	sonarFailServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/system/status":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"UP"}`))
		case "/api/users/create":
			w.WriteHeader(http.StatusOK)
		case "/api/user_tokens/generate":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":[{"msg":"database error"}]}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer sonarFailServer.Close()

	t.Setenv("SONAR_HOST_URL", sonarFailServer.URL)
	t.Setenv("SONAR_ADMIN_USER", "admin")
	t.Setenv("SONAR_ADMIN_PASSWORD", "admin")

	report := &DoctorReport{AgentName: cfg.Name}
	checkAndHealSonar(ctx, cfg, paths, report)

	if report.UnrepairableCount == 0 {
		t.Errorf("expected token generation failure to be reported as unrepairable error")
	}
	if len(report.Checks) == 0 || report.Checks[0].Status != StatusError {
		t.Errorf("expected StatusError on token generation failure, got %+v", report.Checks)
	}
}

func TestDoctor_Forgejo_MutationErrors(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	t.Cleanup(func() {
		_ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
	})

	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SSHPrivateKey: filepath.Join(tmpDir, "ssh", "key"),
		IDEKeyFile:    filepath.Join(tmpDir, "ssh", "key"),
	}
	_ = os.MkdirAll(filepath.Dir(paths.SSHPrivateKey), 0700)
	_ = os.WriteFile(paths.SSHPrivateKey+".pub", []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA mock-pubkey"), 0644)

	cfg := &config.AgentConfig{
		Name:     "fj-mutation-agent",
		Password: "testpassword",
	}

	t.Run("EnsureUserFailure", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/version" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"version":"1.22.0"}`))
				return
			}
			if r.URL.Path == "/api/v1/users/"+cfg.Name {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if r.URL.Path == "/api/v1/admin/users" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		t.Setenv("FORGEJO_URL", ts.URL)
		t.Setenv("FORGEJO_ADMIN_USER", "admin")
		t.Setenv("FORGEJO_ADMIN_PASSWORD", "admin")

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealForgejo(ctx, cfg, paths, report)

		if report.UnrepairableCount == 0 {
			t.Errorf("expected EnsureUser failure to increment UnrepairableCount")
		}
		if len(report.Checks) == 0 || report.Checks[0].Status != StatusError {
			t.Errorf("expected StatusError on EnsureUser failure, got %+v", report.Checks)
		}
	})

	t.Run("AddUserSSHKeyFailure", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/version" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"version":"1.22.0"}`))
				return
			}
			if r.URL.Path == "/api/v1/users/"+cfg.Name {
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.URL.Path == "/api/v1/admin/users/"+cfg.Name+"/keys" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		t.Setenv("FORGEJO_URL", ts.URL)
		t.Setenv("FORGEJO_ADMIN_USER", "admin")
		t.Setenv("FORGEJO_ADMIN_PASSWORD", "admin")

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealForgejo(ctx, cfg, paths, report)

		if report.UnrepairableCount == 0 {
			t.Errorf("expected AddUserSSHKey failure to increment UnrepairableCount")
		}
		if len(report.Checks) == 0 || report.Checks[0].Status != StatusError {
			t.Errorf("expected StatusError on AddUserSSHKey failure, got %+v", report.Checks)
		}
	})

	t.Run("AddOrgMemberFailure", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/version" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"version":"1.22.0"}`))
				return
			}
			if r.URL.Path == "/api/v1/users/"+cfg.Name {
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.URL.Path == "/api/v1/admin/users/"+cfg.Name+"/keys" {
				w.WriteHeader(http.StatusCreated)
				return
			}
			if strings.HasPrefix(r.URL.Path, "/api/v1/orgs/fleet/members") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		t.Setenv("FORGEJO_URL", ts.URL)
		t.Setenv("FORGEJO_ADMIN_USER", "admin")
		t.Setenv("FORGEJO_ADMIN_PASSWORD", "admin")

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealForgejo(ctx, cfg, paths, report)

		if report.UnrepairableCount == 0 {
			t.Errorf("expected AddOrgMember failure to increment UnrepairableCount")
		}
		if len(report.Checks) == 0 || report.Checks[0].Status != StatusError {
			t.Errorf("expected StatusError on AddOrgMember failure, got %+v", report.Checks)
		}
	})
}

func TestDoctor_Valkey_MutationsAndErrors(t *testing.T) {
	ctx := context.Background()
	paths := config.Paths{}
	cfg := &config.AgentConfig{
		Name:         "valkey-mut-agent",
		Role:         "coding-agent",
		Password:     "agentpass",
		PublicKeyB64: "dGVzdC1wdWJrZXk=",
	}

	startCustomMockValkey := func(t *testing.T, handler func(cmd string, args []resp.Value) resp.Value) (int, func()) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed starting mock valkey: %v", err)
		}
		port := ln.Addr().(*net.TCPAddr).Port

		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				go func(c net.Conn) {
					defer c.Close()
					r := resp.NewReader(c)
					w := resp.NewWriter(c)
					for {
						val, err := r.ReadValue()
						if err != nil {
							return
						}
						arr, err := val.AsArray()
						if err != nil || len(arr) == 0 {
							continue
						}
						cmd := strings.ToUpper(arr[0].String())
						respVal := handler(cmd, arr)
						_ = w.WriteValue(respVal)
					}
				}(conn)
			}
		}()
		return port, func() { _ = ln.Close() }
	}

	t.Run("ACLFail_Error", func(t *testing.T) {
		port, cleanup := startCustomMockValkey(t, func(cmd string, args []resp.Value) resp.Value {
			switch cmd {
			case "AUTH":
				return resp.Value{Type: resp.TypeSimpleString, Str: "OK"}
			case "ACL":
				if len(args) > 1 && strings.ToUpper(args[1].String()) == "SETUSER" {
					return resp.Value{Type: resp.TypeError, Str: "ERR syntax error"}
				}
				return resp.Value{Type: resp.TypeNull}
			default:
				return resp.Value{Type: resp.TypeSimpleString, Str: "OK"}
			}
		})
		defer cleanup()

		t.Setenv("BP_HOST", "127.0.0.1")
		t.Setenv("BP_PORT", fmt.Sprintf("%d", port))
		t.Setenv("ADMIN_BACKPLANE_PASSWORD", "mockpass")

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealValkey(ctx, cfg, paths, report)

		if report.UnrepairableCount == 0 {
			t.Errorf("expected ACL failure to increment UnrepairableCount")
		}
		if len(report.Checks) == 0 || report.Checks[0].Status != StatusError {
			t.Errorf("expected StatusError on ACL failure, got %+v", report.Checks)
		}
	})

	t.Run("RegisterIdentityFail_Error", func(t *testing.T) {
		port, cleanup := startCustomMockValkey(t, func(cmd string, args []resp.Value) resp.Value {
			switch cmd {
			case "AUTH":
				return resp.Value{Type: resp.TypeSimpleString, Str: "OK"}
			case "ACL":
				return resp.Value{Type: resp.TypeSimpleString, Str: "OK"}
			case "SET":
				return resp.Value{Type: resp.TypeError, Str: "ERR read-only replica"}
			default:
				return resp.Value{Type: resp.TypeSimpleString, Str: "OK"}
			}
		})
		defer cleanup()

		t.Setenv("BP_HOST", "127.0.0.1")
		t.Setenv("BP_PORT", fmt.Sprintf("%d", port))
		t.Setenv("ADMIN_BACKPLANE_PASSWORD", "mockpass")

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealValkey(ctx, cfg, paths, report)

		if report.UnrepairableCount == 0 {
			t.Errorf("expected RegisterIdentity failure to increment UnrepairableCount")
		}
		if len(report.Checks) == 0 || report.Checks[0].Status != StatusError {
			t.Errorf("expected StatusError on RegisterIdentity failure, got %+v", report.Checks)
		}
	})

	t.Run("HealedPath_MissingState", func(t *testing.T) {
		port, cleanup := startCustomMockValkey(t, func(cmd string, args []resp.Value) resp.Value {
			switch cmd {
			case "AUTH":
				return resp.Value{Type: resp.TypeSimpleString, Str: "OK"}
			case "ACL":
				if len(args) > 1 && strings.ToUpper(args[1].String()) == "GETUSER" {
					return resp.Value{Type: resp.TypeNull}
				}
				return resp.Value{Type: resp.TypeSimpleString, Str: "OK"}
			case "GET":
				return resp.Value{Type: resp.TypeNull}
			default:
				return resp.Value{Type: resp.TypeSimpleString, Str: "OK"}
			}
		})
		defer cleanup()

		t.Setenv("BP_HOST", "127.0.0.1")
		t.Setenv("BP_PORT", fmt.Sprintf("%d", port))
		t.Setenv("ADMIN_BACKPLANE_PASSWORD", "mockpass")

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealValkey(ctx, cfg, paths, report)

		if report.HealedCount == 0 {
			t.Errorf("expected Valkey ACL & identity to be healed")
		}
		if len(report.Checks) == 0 || report.Checks[0].Status != StatusHealed {
			t.Errorf("expected StatusHealed, got %+v", report.Checks)
		}
	})

	t.Run("IntactState_StatusOK", func(t *testing.T) {
		idRec := libbp.IdentityRecord{
			Name:   cfg.Name,
			Role:   cfg.Role,
			Kind:   "agent",
			PubKey: cfg.PublicKeyB64,
		}
		idJSON, _ := json.Marshal(idRec)

		port, cleanup := startCustomMockValkey(t, func(cmd string, args []resp.Value) resp.Value {
			switch cmd {
			case "AUTH":
				return resp.Value{Type: resp.TypeSimpleString, Str: "OK"}
			case "ACL":
				if len(args) > 1 && strings.ToUpper(args[1].String()) == "GETUSER" {
					// Return non-empty array for existing user
					return resp.Value{Type: resp.TypeArray, Array: []resp.Value{
						{Type: resp.TypeBulkString, Str: "flags"},
						{Type: resp.TypeArray, Array: []resp.Value{{Type: resp.TypeBulkString, Str: "on"}}},
					}}
				}
				return resp.Value{Type: resp.TypeSimpleString, Str: "OK"}
			case "GET":
				return resp.Value{Type: resp.TypeBulkString, Str: string(idJSON)}
			default:
				return resp.Value{Type: resp.TypeSimpleString, Str: "OK"}
			}
		})
		defer cleanup()

		t.Setenv("BP_HOST", "127.0.0.1")
		t.Setenv("BP_PORT", fmt.Sprintf("%d", port))
		t.Setenv("ADMIN_BACKPLANE_PASSWORD", "mockpass")

		report := &DoctorReport{AgentName: cfg.Name}
		checkAndHealValkey(ctx, cfg, paths, report)

		if len(report.Checks) == 0 || report.Checks[0].Status != StatusOK {
			t.Errorf("expected StatusOK for intact Valkey state, got %+v", report.Checks)
		}
	})
}
