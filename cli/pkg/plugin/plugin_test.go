// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/config"
	sndbxRuntime "github.com/boggycreek/fleet/cli/pkg/runtime"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)
	args := os.Args
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return
	}
	cmd, cmdArgs := args[0], args[1:]
	switch cmd {
	case "echo":
		fmt.Println(strings.Join(cmdArgs, " "))
	case "true":
		os.Exit(0)
	case "false":
		os.Exit(1)
	default:
		os.Exit(127)
	}
}

func helperCommand(ctx context.Context, cmd string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", cmd}
	cs = append(cs, args...)
	var execCmd *exec.Cmd
	if ctx != nil {
		execCmd = exec.CommandContext(ctx, os.Args[0], cs...)
	} else {
		execCmd = exec.Command(os.Args[0], cs...)
	}
	execCmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	return execCmd
}

func TestGenerateGatewayPluginXML(t *testing.T) {
	xml := GenerateGatewayPluginXML()
	if !strings.Contains(xml, JetBrainsPluginID) {
		t.Fatalf("expected XML to contain %s, got: %s", JetBrainsPluginID, xml)
	}
	if !strings.Contains(xml, "<gatewayConnector") {
		t.Fatalf("expected XML to declare gatewayConnector extension")
	}
	if !strings.Contains(xml, "<gatewayConnectionProvider") {
		t.Fatalf("expected XML to declare gatewayConnectionProvider extension")
	}
}

func TestGenerateGatewayPluginJAR(t *testing.T) {
	data, err := GenerateGatewayPluginJAR()
	if err != nil {
		t.Fatalf("unexpected error generating JAR: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("expected non-empty JAR bytes")
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("failed to open generated JAR as zip: %v", err)
	}

	var foundManifest, foundPluginXML bool
	for _, f := range zr.File {
		if f.Name == "META-INF/MANIFEST.MF" {
			foundManifest = true
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("failed to read manifest: %v", err)
			}
			content, _ := io.ReadAll(rc)
			_ = rc.Close()
			if !strings.Contains(string(content), "Manifest-Version:") {
				t.Errorf("manifest missing version: %s", string(content))
			}
		}
		if f.Name == "META-INF/plugin.xml" {
			foundPluginXML = true
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("failed to read plugin.xml: %v", err)
			}
			content, _ := io.ReadAll(rc)
			_ = rc.Close()
			if !strings.Contains(string(content), JetBrainsPluginID) {
				t.Errorf("plugin.xml missing ID: %s", string(content))
			}
		}
	}

	if !foundManifest {
		t.Errorf("expected META-INF/MANIFEST.MF in generated JAR")
	}
	if !foundPluginXML {
		t.Errorf("expected META-INF/plugin.xml in generated JAR")
	}
}

func TestGenerateToolboxPluginManifestAndJAR(t *testing.T) {
	extJson := GenerateToolboxExtensionJSON()
	if !strings.Contains(extJson, ToolboxPluginID) {
		t.Errorf("expected extension.json to contain %s", ToolboxPluginID)
	}
	if !strings.Contains(extJson, ToolboxPluginAPIVersion) {
		t.Errorf("expected extension.json to contain %s", ToolboxPluginAPIVersion)
	}

	iconSvg := GenerateToolboxPluginIconSVG()
	if !strings.Contains(iconSvg, "<svg") {
		t.Errorf("expected SVG icon")
	}

	data, err := GenerateToolboxPluginJAR()
	if err != nil {
		t.Fatalf("unexpected error generating toolbox JAR: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("failed to open generated toolbox JAR as zip: %v", err)
	}

	var foundExt, foundIcon, foundService bool
	for _, f := range zr.File {
		if f.Name == "extension.json" {
			foundExt = true
		}
		if f.Name == "pluginIcon.svg" {
			foundIcon = true
		}
		if f.Name == "META-INF/services/com.jetbrains.toolbox.api.remoteDev.RemoteDevExtension" {
			foundService = true
		}
	}
	if !foundExt || !foundIcon || !foundService {
		t.Errorf("toolbox JAR missing expected files (ext: %v, icon: %v, service: %v)", foundExt, foundIcon, foundService)
	}
}

func TestFindJetBrainsPluginDirs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	dirs := FindJetBrainsPluginDirs()
	_ = dirs

	roots := GetDefaultJetBrainsSearchRoots()
	_ = roots

	_ = GetDefaultToolboxPluginDir()
	_ = GetDefaultToolboxSSHSettingsPath()

	// Create mock IDE directories
	mockRoot := filepath.Join(tmpDir, "JetBrains")
	_ = os.MkdirAll(filepath.Join(mockRoot, "JetBrainsGateway2024.1"), 0755)
	_ = os.MkdirAll(filepath.Join(mockRoot, "WebStorm2026.2"), 0755)
	_ = os.MkdirAll(filepath.Join(mockRoot, "GoLand2026.2"), 0755)
	_ = os.MkdirAll(filepath.Join(mockRoot, "CLion2026.2"), 0755)
	_ = os.MkdirAll(filepath.Join(mockRoot, "PyCharm2026.2"), 0755)
	_ = os.MkdirAll(filepath.Join(mockRoot, "IntelliJIdea2026.2"), 0755)
	_ = os.MkdirAll(filepath.Join(mockRoot, "IdeaIC2026.2"), 0755)
	_ = os.MkdirAll(filepath.Join(mockRoot, "DataGrip2026.2"), 0755)
	// Create a non-matching directory and a file
	_ = os.MkdirAll(filepath.Join(mockRoot, "OtherApp"), 0755)
	_ = os.WriteFile(filepath.Join(mockRoot, "regular_file.txt"), []byte("hi"), 0644)

	found := FindJetBrainsPluginDirsFrom([]string{mockRoot, "/nonexistent/root"})
	if len(found) != 8 {
		t.Errorf("expected 8 matching directories, got %d: %v", len(found), found)
	}
}

func TestEnsureAndRemoveSSHConfigInclude(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	hostSSHConfig := filepath.Join(tmpDir, "config")
	targetSSHConfig := filepath.Join(tmpDir, "agent-sandbox", "ssh_config")

	// 1. Initial creation when hostSSHConfig does not exist
	linked, err := EnsureSSHConfigInclude(targetSSHConfig, hostSSHConfig)
	if err != nil {
		t.Fatalf("EnsureSSHConfigInclude failed: %v", err)
	}
	if !linked {
		t.Fatalf("expected linked=true on fresh create")
	}

	content, err := os.ReadFile(hostSSHConfig)
	if err != nil {
		t.Fatalf("reading host ssh config: %v", err)
	}
	if !strings.Contains(string(content), "Include "+targetSSHConfig) {
		t.Fatalf("expected Include directive in host ssh config: %s", string(content))
	}

	// 2. Second invocation should be idempotent (linked=false)
	linkedAgain, err := EnsureSSHConfigInclude(targetSSHConfig, hostSSHConfig)
	if err != nil {
		t.Fatalf("idempotent EnsureSSHConfigInclude failed: %v", err)
	}
	if linkedAgain {
		t.Fatalf("expected linked=false on subsequent invocation")
	}

	// 3. Unrelated include should not match and should be preserved
	matchHostConfig := filepath.Join(tmpDir, "config_match")
	_ = os.WriteFile(matchHostConfig, []byte("Host *\n  Include ~/.ssh/work/ssh_config\n"), 0600)
	linkedUnrelated, err := EnsureSSHConfigInclude(targetSSHConfig, matchHostConfig)
	if err != nil || !linkedUnrelated {
		t.Fatalf("expected linked=true when only unrelated include exists: %v", err)
	}
	matchBytes, _ := os.ReadFile(matchHostConfig)
	if !strings.Contains(string(matchBytes), "Include ~/.ssh/work/ssh_config") || !strings.Contains(string(matchBytes), "Include "+targetSSHConfig) {
		t.Fatalf("expected both unrelated and target includes: %s", string(matchBytes))
	}

	// 3b. Match with ~ expansion (tmpDir is HOME)
	tildeHostConfig := filepath.Join(tmpDir, "config_tilde")
	_ = os.WriteFile(tildeHostConfig, []byte("Host *\n  include ~/agent-sandbox/ssh_config\n"), 0600)
	linkedTilde, err := EnsureSSHConfigInclude(targetSSHConfig, tildeHostConfig)
	if err != nil || linkedTilde {
		t.Fatalf("expected linked=false when ~/agent-sandbox/ssh_config exists: %v", err)
	}

	// 4. Remove include directive
	unlinked, err := RemoveSSHConfigInclude(targetSSHConfig, hostSSHConfig)
	if err != nil {
		t.Fatalf("RemoveSSHConfigInclude failed: %v", err)
	}
	if !unlinked {
		t.Fatalf("expected unlinked=true")
	}
	contentAfter, _ := os.ReadFile(hostSSHConfig)
	if strings.Contains(string(contentAfter), "Include "+targetSSHConfig) {
		t.Errorf("expected Include directive to be removed: %s", string(contentAfter))
	}

	// 5. Remove again when not present
	unlinked2, err := RemoveSSHConfigInclude(targetSSHConfig, hostSSHConfig)
	if err != nil || unlinked2 {
		t.Fatalf("expected unlinked=false when already removed")
	}

	// 6. Remove on nonexistent file
	unlinkedNonexistent, err := RemoveSSHConfigInclude(targetSSHConfig, filepath.Join(tmpDir, "does-not-exist"))
	if err != nil || unlinkedNonexistent {
		t.Fatalf("expected no error and unlinked=false on nonexistent file")
	}

	// 7. Error creating directory (invalid path)
	_, err = EnsureSSHConfigInclude(targetSSHConfig, "/dev/null/impossible/config")
	if err == nil {
		t.Fatalf("expected error on invalid host config path")
	}

	// 8. Default hostSSHConfigFile resolution
	_, _ = EnsureSSHConfigInclude(targetSSHConfig, "")
	_, _ = RemoveSSHConfigInclude(targetSSHConfig, "")
}

func TestEnsureAndRemoveSSHConfigManagedHosts(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	hostSSHConfig := filepath.Join(tmpDir, "config")
	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
		IDEKeyFile:    filepath.Join(tmpDir, "data", "id_ed25519"),
	}
	_ = paths.EnsureDirectories()

	ctx := context.Background()

	// 1. Initial creation
	err := EnsureSSHConfigManagedHosts(ctx, paths, hostSSHConfig)
	if err != nil {
		t.Fatalf("EnsureSSHConfigManagedHosts failed: %v", err)
	}
	data, _ := os.ReadFile(hostSSHConfig)
	if !strings.Contains(string(data), ManagedSSHConfigHeader) {
		t.Fatalf("expected header in config: %s", string(data))
	}

	// 2. Second invocation replaces existing block
	err = EnsureSSHConfigManagedHosts(ctx, paths, hostSSHConfig)
	if err != nil {
		t.Fatalf("EnsureSSHConfigManagedHosts second run failed: %v", err)
	}

	// 3. Remove managed hosts
	err = RemoveSSHConfigManagedHosts(hostSSHConfig)
	if err != nil {
		t.Fatalf("RemoveSSHConfigManagedHosts failed: %v", err)
	}
	dataAfter, _ := os.ReadFile(hostSSHConfig)
	if strings.Contains(string(dataAfter), ManagedSSHConfigHeader) {
		t.Fatalf("expected header to be removed: %s", string(dataAfter))
	}

	// 4. Remove on nonexistent file
	err = RemoveSSHConfigManagedHosts(filepath.Join(tmpDir, "not-found"))
	if err != nil {
		t.Fatalf("expected no error on nonexistent file: %v", err)
	}

	// 5. Default host config path resolution
	_ = EnsureSSHConfigManagedHosts(ctx, paths, "")
	_ = RemoveSSHConfigManagedHosts("")
}

func TestSyncAndRemoveToolboxSSHSettings(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	toolboxPluginsDir := filepath.Join(tmpDir, "Toolbox", "plugins")
	_ = os.MkdirAll(toolboxPluginsDir, 0755)
	settingsFile := filepath.Join(toolboxPluginsDir, "ssh", "settings.json")

	paths := config.Paths{
		DataHome:  filepath.Join(tmpDir, "data"),
		AgentsDir: filepath.Join(tmpDir, "data", "agents"),
	}
	_ = paths.EnsureDirectories()

	ctx := context.Background()

	// Initial pre-existing settings with another remote
	initSettings := map[string]any{
		"envsJson":                 `{"remotes":[{"host":"sidekick","port":0,"userName":"brian","shouldUseSystemSshAgent":true,"originalConnectionString":"brian@sidekick"}]}`,
		"CONNECTION_STRINGS_CACHE": `{"brian@sidekick":{"host":"sidekick","port":0,"userName":"brian","shouldUseSystemSshAgent":true,"originalConnectionString":"brian@sidekick"}}`,
	}
	initBytes, _ := json.MarshalIndent(initSettings, "", "    ")
	_ = os.MkdirAll(filepath.Dir(settingsFile), 0755)
	_ = os.WriteFile(settingsFile, initBytes, 0600)

	// Test Sync
	err := SyncToolboxSSHSettings(ctx, paths, settingsFile)
	if err != nil {
		t.Fatalf("SyncToolboxSSHSettings failed: %v", err)
	}

	content, _ := os.ReadFile(settingsFile)
	if !strings.Contains(string(content), "sidekick") {
		t.Fatalf("expected preserved non-sndbx remotes: %s", string(content))
	}

	// Test Remove
	err = RemoveToolboxSSHSettings(settingsFile)
	if err != nil {
		t.Fatalf("RemoveToolboxSSHSettings failed: %v", err)
	}

	// Test with nonexistent file
	_ = RemoveToolboxSSHSettings(filepath.Join(tmpDir, "nonexistent", "settings.json"))

	// Test default resolution
	_ = SyncToolboxSSHSettings(ctx, paths, "")
	_ = RemoveToolboxSSHSettings("")
}

func TestInstallAndRemoveGatewayPlugin(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
	}
	_ = paths.EnsureDirectories()

	ctx := context.Background()
	var out bytes.Buffer

	res, err := InstallGatewayPlugin(ctx, paths, &out)
	if err != nil {
		t.Fatalf("InstallGatewayPlugin error: %v", err)
	}
	if res.PluginID != PluginIDGateway {
		t.Errorf("expected PluginID=%s, got %s", PluginIDGateway, res.PluginID)
	}
	if len(res.InstalledPaths) == 0 {
		t.Fatalf("expected at least 1 installed path")
	}

	centralJar := filepath.Join(paths.DataHome, "plugins", "jetbrains-gateway", "fleet-gateway", "lib", "fleet-gateway.jar")
	if _, err := os.Stat(centralJar); err != nil {
		t.Errorf("central gateway jar not found: %v", err)
	}

	remRes, err := RemoveGatewayPlugin(ctx, paths, &out)
	if err != nil {
		t.Fatalf("RemoveGatewayPlugin error: %v", err)
	}
	if remRes.PluginID != PluginIDGateway {
		t.Errorf("expected PluginID=%s, got %s", PluginIDGateway, remRes.PluginID)
	}
	if len(remRes.RemovedPaths) == 0 {
		t.Errorf("expected at least 1 removed path")
	}

	badPaths := config.Paths{
		DataHome: "/dev/null/impossible",
	}
	_, err = InstallGatewayPlugin(ctx, badPaths, &out)
	if err == nil {
		t.Errorf("expected error with bad DataHome path")
	}
}

func TestInstallAndRemoveToolboxPlugin(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
	}
	_ = paths.EnsureDirectories()

	ctx := context.Background()
	var out bytes.Buffer

	res, err := InstallToolboxPlugin(ctx, paths, &out)
	if err != nil {
		t.Fatalf("InstallToolboxPlugin error: %v", err)
	}
	if res.PluginID != PluginIDToolbox {
		t.Errorf("expected PluginID=%s, got %s", PluginIDToolbox, res.PluginID)
	}
	if len(res.InstalledPaths) == 0 {
		t.Fatalf("expected at least 1 installed path")
	}

	centralExt := filepath.Join(paths.DataHome, "plugins", "jetbrains-toolbox", "fleet", "extension.json")
	if _, err := os.Stat(centralExt); err != nil {
		t.Errorf("central toolbox extension.json not found: %v", err)
	}

	remRes, err := RemoveToolboxPlugin(ctx, paths, &out)
	if err != nil {
		t.Fatalf("RemoveToolboxPlugin error: %v", err)
	}
	if remRes.PluginID != PluginIDToolbox {
		t.Errorf("expected PluginID=%s, got %s", PluginIDToolbox, remRes.PluginID)
	}

	badPaths := config.Paths{
		DataHome: "/dev/null/impossible",
	}
	_, err = InstallToolboxPlugin(ctx, badPaths, &out)
	if err == nil {
		t.Errorf("expected error with bad DataHome path")
	}
}

func TestInstallAndRemoveVSCodePlugin(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
	}
	_ = paths.EnsureDirectories()

	ctx := context.Background()
	var out bytes.Buffer

	origExec := ExecCommandContext
	defer func() { ExecCommandContext = origExec }()

	ExecCommandContext = func(ctx context.Context, command string, args ...string) *exec.Cmd {
		return helperCommand(ctx, "true")
	}

	res, err := InstallVSCodePlugin(ctx, paths, &out)
	if err != nil {
		t.Fatalf("InstallVSCodePlugin error: %v", err)
	}
	if res.PluginID != PluginIDVSCode {
		t.Errorf("expected PluginID=%s, got %s", PluginIDVSCode, res.PluginID)
	}

	ExecCommandContext = func(ctx context.Context, command string, args ...string) *exec.Cmd {
		return helperCommand(ctx, "false")
	}

	res2, err := InstallVSCodePlugin(ctx, paths, &out)
	if err != nil {
		t.Fatalf("InstallVSCodePlugin with missing code error: %v", err)
	}
	if !strings.Contains(res2.Message, "not found in PATH") {
		t.Errorf("expected message to note missing code CLI: %s", res2.Message)
	}

	remRes, err := RemoveVSCodePlugin(ctx, paths, &out)
	if err != nil {
		t.Fatalf("RemoveVSCodePlugin error: %v", err)
	}
	if remRes.PluginID != PluginIDVSCode {
		t.Errorf("expected PluginID=%s, got %s", PluginIDVSCode, remRes.PluginID)
	}
}

func TestListPlugins(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
	}
	_ = paths.EnsureDirectories()

	plugins := ListPlugins(paths)
	if len(plugins) != 3 {
		t.Fatalf("expected 3 plugins, got %d", len(plugins))
	}
	found := make(map[string]bool)
	for _, p := range plugins {
		found[p.ID] = true
	}
	if !found[PluginIDGateway] || !found[PluginIDToolbox] || !found[PluginIDVSCode] {
		t.Fatalf("missing expected plugins: %+v", found)
	}
}

func TestPluginPathResolutionAndJARGeneration(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("APPDATA", filepath.Join(tmpDir, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(tmpDir, "AppData", "Local"))

	roots := GetDefaultJetBrainsSearchRoots()
	if len(roots) == 0 {
		t.Errorf("expected search roots, got empty")
	}

	tbDir := GetDefaultToolboxPluginDir()
	if tbDir == "" {
		t.Errorf("expected toolbox plugin dir, got empty")
	}

	sshPath := GetDefaultToolboxSSHSettingsPath()
	if sshPath == "" {
		t.Errorf("expected toolbox ssh path, got empty")
	}

	tbJar, err := GenerateToolboxPluginJAR()
	if err != nil || len(tbJar) == 0 {
		t.Errorf("GenerateToolboxPluginJAR failed: %v", err)
	}

	gwJar, err := GenerateGatewayPluginJAR()
	if err != nil || len(gwJar) == 0 {
		t.Errorf("GenerateGatewayPluginJAR failed: %v", err)
	}

	dirs := FindJetBrainsPluginDirsFrom([]string{filepath.Join(tmpDir, "nonexistent-dir")})
	if len(dirs) != 0 {
		t.Errorf("expected empty dirs for nonexistent root, got %v", dirs)
	}
}

func TestListPluginsInstalledState(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	tbDir := GetDefaultToolboxPluginDir()

	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SecretsDir:    filepath.Join(tmpDir, "data", "secrets"),
		SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
	}
	_ = paths.EnsureDirectories()

	// Create central gateway jar
	centralGatewayJar := filepath.Join(paths.DataHome, "plugins", "jetbrains-gateway", "fleet-gateway", "lib", "fleet-gateway.jar")
	_ = os.MkdirAll(filepath.Dir(centralGatewayJar), 0755)
	_ = os.WriteFile(centralGatewayJar, []byte("fake-jar"), 0644)

	// Create central toolbox dir and active toolbox dir
	centralToolboxDir := filepath.Join(paths.DataHome, "plugins", "jetbrains-toolbox", "fleet")
	_ = os.MkdirAll(centralToolboxDir, 0755)
	_ = os.MkdirAll(tbDir, 0755)

	plugins := ListPlugins(paths)
	for _, p := range plugins {
		if (p.ID == PluginIDGateway || p.ID == PluginIDToolbox) && !p.Installed {
			t.Errorf("expected plugin %s to be marked installed", p.ID)
		}
	}
}

func TestSSHConfigIncludeLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	hostSSHConfig := filepath.Join(tmpDir, "ssh_config")
	targetInclude := filepath.Join(tmpDir, "target_config")

	// Missing host file -> creates with Include directive
	added, err := EnsureSSHConfigInclude(targetInclude, hostSSHConfig)
	if err != nil || !added {
		t.Errorf("EnsureSSHConfigInclude failed to create host config: %v", err)
	}

	// Already present -> idempotent no-op
	added, err = EnsureSSHConfigInclude(targetInclude, hostSSHConfig)
	if err != nil || added {
		t.Errorf("EnsureSSHConfigInclude should not re-add existing include: %v", err)
	}

	// Remove include
	removed, err := RemoveSSHConfigInclude(targetInclude, hostSSHConfig)
	if err != nil || !removed {
		t.Errorf("RemoveSSHConfigInclude failed: %v", err)
	}

	// Remove when not present
	removed, err = RemoveSSHConfigInclude(targetInclude, hostSSHConfig)
	if err != nil || removed {
		t.Errorf("RemoveSSHConfigInclude should return false when not present: %v", err)
	}

	// Remove on nonexistent file
	removed, err = RemoveSSHConfigInclude(targetInclude, filepath.Join(tmpDir, "nonexistent"))
	if err != nil || removed {
		t.Errorf("RemoveSSHConfigInclude on nonexistent file should return false: %v", err)
	}
}

func TestSSHConfigManagedHostsLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SecretsDir:    filepath.Join(tmpDir, "data", "secrets"),
		SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
	}
	_ = paths.EnsureDirectories()

	cfg := &config.AgentConfig{Name: "agent1", ContainerName: "sndbx-agent1"}
	_ = config.SaveAgentConfig(cfg, paths)

	managedHostFile := filepath.Join(tmpDir, "managed_ssh_config")
	ctx := context.Background()

	// Missing host config -> creates with header and block
	err := EnsureSSHConfigManagedHosts(ctx, paths, managedHostFile)
	if err != nil {
		t.Errorf("EnsureSSHConfigManagedHosts failed to create file: %v", err)
	}

	// Update existing block
	err = EnsureSSHConfigManagedHosts(ctx, paths, managedHostFile)
	if err != nil {
		t.Errorf("EnsureSSHConfigManagedHosts failed to update: %v", err)
	}

	// Remove managed hosts
	err = RemoveSSHConfigManagedHosts(managedHostFile)
	if err != nil {
		t.Errorf("RemoveSSHConfigManagedHosts failed: %v", err)
	}

	// Remove when already removed
	err = RemoveSSHConfigManagedHosts(managedHostFile)
	if err != nil {
		t.Errorf("RemoveSSHConfigManagedHosts should return nil when block absent: %v", err)
	}

	// Remove on missing file
	err = RemoveSSHConfigManagedHosts(filepath.Join(tmpDir, "nonexistent"))
	if err != nil {
		t.Errorf("RemoveSSHConfigManagedHosts on nonexistent file should return nil: %v", err)
	}
}

func TestToolboxSSHSettingsLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SecretsDir:    filepath.Join(tmpDir, "data", "secrets"),
		SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
	}
	_ = paths.EnsureDirectories()

	toolboxPluginsDir := filepath.Join(tmpDir, "tb_plugins")
	settingsFile := filepath.Join(toolboxPluginsDir, "ssh", "settings.json")
	_ = os.MkdirAll(toolboxPluginsDir, 0755)
	ctx := context.Background()

	// Settings file creates new JSON
	err := SyncToolboxSSHSettings(ctx, paths, settingsFile)
	if err != nil {
		t.Errorf("SyncToolboxSSHSettings failed to create settings: %v", err)
	}

	// Re-sync with existing entries
	err = SyncToolboxSSHSettings(ctx, paths, settingsFile)
	if err != nil {
		t.Errorf("SyncToolboxSSHSettings re-sync failed: %v", err)
	}

	// Sync with invalid JSON file should reset and recreate
	_ = os.WriteFile(settingsFile, []byte("{invalid-json"), 0644)
	err = SyncToolboxSSHSettings(ctx, paths, settingsFile)
	if err != nil {
		t.Errorf("SyncToolboxSSHSettings failed on invalid json: %v", err)
	}

	// RemoveToolboxSSHSettings on valid file
	err = RemoveToolboxSSHSettings(settingsFile)
	if err != nil {
		t.Errorf("RemoveToolboxSSHSettings failed: %v", err)
	}

	// Remove on nonexistent file
	err = RemoveToolboxSSHSettings(filepath.Join(tmpDir, "nonexistent.json"))
	if err != nil {
		t.Errorf("RemoveToolboxSSHSettings on nonexistent should return nil: %v", err)
	}

	// Remove on invalid JSON file
	_ = os.WriteFile(settingsFile, []byte("{bad-json"), 0644)
	err = RemoveToolboxSSHSettings(settingsFile)
	if err != nil {
		t.Errorf("RemoveToolboxSSHSettings on invalid json returned error: %v", err)
	}
}

func TestSSHConfigIncludeNonDestructiveAndAtomic(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	hostSSHConfig := filepath.Join(tmpDir, "config")
	targetSSHConfig := filepath.Join(tmpDir, "agent-sandbox", "ssh_config")

	// 1. Host config contains custom user formatting, comments, and host block
	userContent := `# OpenSSH Configuration
# Custom user comment

Host legacy-host
    HostName 10.0.0.1
    User admin

# Another custom comment
`
	if err := os.WriteFile(hostSSHConfig, []byte(userContent), 0640); err != nil {
		t.Fatalf("failed writing initial config: %v", err)
	}

	linked, err := EnsureSSHConfigInclude(targetSSHConfig, hostSSHConfig)
	if err != nil {
		t.Fatalf("EnsureSSHConfigInclude failed: %v", err)
	}
	if !linked {
		t.Fatalf("expected linked=true when directive newly added")
	}

	// Verify backup file was created with original content
	bakPath := hostSSHConfig + ".bak"
	bakContent, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("expected backup file %s to exist: %v", bakPath, err)
	}
	if string(bakContent) != userContent {
		t.Fatalf("expected backup file to match original user content")
	}

	// Verify file mode was preserved
	fi, err := os.Stat(hostSSHConfig)
	if err != nil {
		t.Fatalf("failed to stat host config: %v", err)
	}
	if fi.Mode().Perm() != 0640 {
		t.Errorf("expected permissions 0640 preserved, got %o", fi.Mode().Perm())
	}

	content, err := os.ReadFile(hostSSHConfig)
	if err != nil {
		t.Fatalf("failed reading host config: %v", err)
	}
	contentStr := string(content)

	// Verify header and include exist at top, and user content is preserved verbatim
	expectedPrefix := fmt.Sprintf("%s\nInclude %s\n\n", sshConfigCommentHeader, targetSSHConfig)
	if !strings.HasPrefix(contentStr, expectedPrefix) {
		t.Fatalf("expected config to begin with include block, got:\n%s", contentStr)
	}
	remainder := strings.TrimPrefix(contentStr, expectedPrefix)
	if remainder != userContent {
		t.Fatalf("expected user content to remain untouched, got:\n%s\nexpected:\n%s", remainder, userContent)
	}

	// 2. Idempotent call when already linked
	linkedAgain, err := EnsureSSHConfigInclude(targetSSHConfig, hostSSHConfig)
	if err != nil || linkedAgain {
		t.Fatalf("expected linked=false on second invocation, got %v (err: %v)", linkedAgain, err)
	}
	contentAgain, _ := os.ReadFile(hostSSHConfig)
	if string(contentAgain) != contentStr {
		t.Errorf("expected identical content after idempotent EnsureSSHConfigInclude")
	}

	// 3. Remove include directive cleans up header and include, leaving user content untouched
	unlinked, err := RemoveSSHConfigInclude(targetSSHConfig, hostSSHConfig)
	if err != nil || !unlinked {
		t.Fatalf("RemoveSSHConfigInclude failed: unlinked=%v, err=%v", unlinked, err)
	}

	contentAfterRemove, _ := os.ReadFile(hostSSHConfig)
	afterStr := string(contentAfterRemove)

	if strings.Contains(afterStr, sshConfigCommentHeader) {
		t.Errorf("expected comment header to be removed, found in:\n%s", afterStr)
	}
	if strings.Contains(afterStr, targetSSHConfig) {
		t.Errorf("expected include directive to be removed in:\n%s", afterStr)
	}
	if afterStr != userContent {
		t.Errorf("expected user content to be preserved exactly after removal, got:\n%s\nexpected:\n%s", afterStr, userContent)
	}

	// 4. Removing when only the include block was present leaves zero residue (empty file)
	soloConfig := filepath.Join(tmpDir, "solo_config")
	if _, err := EnsureSSHConfigInclude(targetSSHConfig, soloConfig); err != nil {
		t.Fatalf("failed creating solo config: %v", err)
	}
	if _, err := RemoveSSHConfigInclude(targetSSHConfig, soloConfig); err != nil {
		t.Fatalf("failed removing solo config: %v", err)
	}
	soloAfter, _ := os.ReadFile(soloConfig)
	if len(strings.TrimSpace(string(soloAfter))) != 0 {
		t.Errorf("expected solo config to be empty after remove, got:\n%s", string(soloAfter))
	}
}

func TestToolboxPathsAndDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	pluginDir := GetDefaultToolboxPluginDir()
	if pluginDir == "" {
		t.Errorf("expected non-empty toolbox plugin dir")
	}
	settingsPath := GetDefaultToolboxSSHSettingsPath()
	if settingsPath == "" {
		t.Errorf("expected non-empty toolbox settings path")
	}

	t.Setenv("HOME", "")
	_ = GetDefaultToolboxPluginDir()
	_ = GetDefaultToolboxSSHSettingsPath()
}

func TestSyncToolboxSSHSettings_AdvancedBranches(t *testing.T) {
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:      tmpDir,
		AgentsDir:     filepath.Join(tmpDir, "agents"),
		SSHConfigFile: filepath.Join(tmpDir, "ssh_config"),
	}
	_ = paths.EnsureDirectories()

	// 1. Empty settingsPath with no home
	t.Setenv("HOME", "")
	err := SyncToolboxSSHSettings(context.Background(), paths, "")
	if err != nil {
		t.Errorf("expected nil error on empty settings path, got: %v", err)
	}

	// 2. Non-existent toolbox plugins directory returns nil without error
	nonExistent := filepath.Join(tmpDir, "does-not-exist", "ssh", "settings.json")
	if err := SyncToolboxSSHSettings(context.Background(), paths, nonExistent); err != nil {
		t.Errorf("expected nil on non-existent toolbox dir, got: %v", err)
	}

	// 3. Existing settings with existing fleet- and legacy sndbx- remotes
	toolboxDir := filepath.Join(tmpDir, "Toolbox", "plugins")
	sshDir := filepath.Join(toolboxDir, "ssh")
	if err := os.MkdirAll(sshDir, 0755); err != nil {
		t.Fatalf("failed creating ssh dir: %v", err)
	}
	settingsFile := filepath.Join(sshDir, "settings.json")

	initialJSON := `{
		"envsJson": "{\"remotes\":[{\"host\":\"fleet-old\"},{\"host\":\"sndbx-legacy\"},{\"host\":\"other-remote\"}]}",
		"CONNECTION_STRINGS_CACHE": "{\"agent@fleet-old\":{},\"agent@sndbx-legacy\":{},\"user@other\":{}}",
		"TBX_DEPLOYMENT_TARGET_ENVagent@fleet-old": "1",
		"TBX_AUTO_CONNECT_ENVagent@sndbx-legacy": "false",
		"otherKey": "preserved"
	}`
	if err := os.WriteFile(settingsFile, []byte(initialJSON), 0644); err != nil {
		t.Fatalf("failed writing settings file: %v", err)
	}

	if err := SyncToolboxSSHSettings(context.Background(), paths, settingsFile); err != nil {
		t.Fatalf("SyncToolboxSSHSettings failed: %v", err)
	}

	content, _ := os.ReadFile(settingsFile)
	strContent := string(content)
	if strings.Contains(strContent, "fleet-old") || strings.Contains(strContent, "sndbx-legacy") {
		t.Errorf("expected old fleet and sndbx entries to be purged, got:\n%s", strContent)
	}
	if !strings.Contains(strContent, "other-remote") || !strings.Contains(strContent, "otherKey") {
		t.Errorf("expected external entries to be preserved, got:\n%s", strContent)
	}

	// 4. RemoveToolboxSSHSettings on empty, non-existent, and populated settings
	_ = RemoveToolboxSSHSettings("")
	_ = RemoveToolboxSSHSettings(filepath.Join(tmpDir, "missing", "settings.json"))
	if err := RemoveToolboxSSHSettings(settingsFile); err != nil {
		t.Fatalf("RemoveToolboxSSHSettings failed: %v", err)
	}

	// 5. Corrupt JSON in RemoveToolboxSSHSettings returns nil without error
	_ = os.WriteFile(settingsFile, []byte("invalid-json{"), 0644)
	if err := RemoveToolboxSSHSettings(settingsFile); err != nil {
		t.Errorf("expected nil on invalid JSON in RemoveToolboxSSHSettings, got: %v", err)
	}
}

func TestGatewayPlugin_LegacyCleanupAndErrors(t *testing.T) {
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
	}
	_ = paths.EnsureDirectories()

	// Pre-create legacy sndbx-gateway directory to test migration/removal
	legacyCentral := filepath.Join(paths.DataHome, "plugins", "jetbrains-gateway", "sndbx-gateway")
	_ = os.MkdirAll(legacyCentral, 0755)
	_ = os.WriteFile(filepath.Join(legacyCentral, "old.jar"), []byte("legacy"), 0644)

	var out bytes.Buffer
	res, err := InstallGatewayPlugin(context.Background(), paths, &out)
	if err != nil {
		t.Fatalf("InstallGatewayPlugin failed: %v", err)
	}
	if _, err := os.Stat(legacyCentral); !os.IsNotExist(err) {
		t.Errorf("expected legacyCentral to be cleaned up on install")
	}

	// Recreate legacy directory to test cleanup on RemoveGatewayPlugin
	_ = os.MkdirAll(legacyCentral, 0755)
	remRes, err := RemoveGatewayPlugin(context.Background(), paths, &out)
	if err != nil {
		t.Fatalf("RemoveGatewayPlugin failed: %v", err)
	}
	hasLegacy := false
	for _, p := range remRes.RemovedPaths {
		if strings.Contains(p, "sndbx-gateway") {
			hasLegacy = true
			break
		}
	}
	if !hasLegacy {
		t.Errorf("expected legacyCentral to be included in removed paths")
	}
	_ = res
}

func TestNormalizeSSHPathAndHelpers(t *testing.T) {
	if got := normalizeSSHPath(""); got != "" {
		t.Errorf("expected empty string, got: %q", got)
	}
	home, _ := os.UserHomeDir()
	if got := normalizeSSHPath("~"); got != home {
		t.Errorf("expected home %q, got: %q", home, got)
	}
	if got := normalizeSSHPath("~/some/path"); got != filepath.Join(home, "some", "path") {
		t.Errorf("expected ~/some/path, got: %q", got)
	}
	if got := normalizeSSHPath("$HOME/some/path"); got != filepath.Join(home, "some", "path") {
		t.Errorf("expected $HOME/some/path, got: %q", got)
	}
	if got := normalizeSSHPath("/absolute/path"); got != "/absolute/path" {
		t.Errorf("expected /absolute/path, got: %q", got)
	}

	// isTargetInclude edge cases
	if isTargetInclude("", "/target") {
		t.Errorf("empty line should not match")
	}
	if isTargetInclude("# comment", "/target") {
		t.Errorf("comment line should not match")
	}
	if isTargetInclude("Include", "/target") {
		t.Errorf("single word include should not match")
	}
	if isTargetInclude("other /target", "/target") {
		t.Errorf("non-include command should not match")
	}
	if !isTargetInclude("Include \"/target\"", "/target") {
		t.Errorf("quoted target should match")
	}

	roots := GetDefaultJetBrainsSearchRoots()
	if len(roots) == 0 {
		t.Errorf("expected search roots on linux/unix")
	}
}

func TestEnsureSSHConfigManagedHosts_Branches(t *testing.T) {
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:  tmpDir,
		AgentsDir: filepath.Join(tmpDir, "agents"),
	}
	_ = paths.EnsureDirectories()

	hostConfig := filepath.Join(tmpDir, "ssh_config")
	// 1. Initial creation with legacy block
	initial := "# --- BEGIN AGENT SANDBOX MANAGED HOSTS ---\nold\n# --- END AGENT SANDBOX MANAGED HOSTS ---\n"
	_ = os.WriteFile(hostConfig, []byte(initial), 0600)

	ctx := context.Background()
	if err := EnsureSSHConfigManagedHosts(ctx, paths, hostConfig); err != nil {
		t.Fatalf("EnsureSSHConfigManagedHosts failed: %v", err)
	}

	content, _ := os.ReadFile(hostConfig)
	if !strings.Contains(string(content), ManagedSSHConfigHeader) {
		t.Errorf("expected modern header to replace legacy block")
	}

	// 2. Second run replaces existing modern block
	if err := EnsureSSHConfigManagedHosts(ctx, paths, hostConfig); err != nil {
		t.Fatalf("second EnsureSSHConfigManagedHosts failed: %v", err)
	}

	// 3. RemoveSSHConfigManagedHosts
	if err := RemoveSSHConfigManagedHosts(hostConfig); err != nil {
		t.Fatalf("RemoveSSHConfigManagedHosts failed: %v", err)
	}
	contentAfterRemove, _ := os.ReadFile(hostConfig)
	if strings.Contains(string(contentAfterRemove), ManagedSSHConfigHeader) {
		t.Errorf("expected managed block to be removed")
	}

	// 4. Default hostSSHConfigFile with empty argument
	t.Setenv("HOME", tmpDir)
	_ = EnsureSSHConfigManagedHosts(ctx, paths, "")
	_ = RemoveSSHConfigManagedHosts("")
}

func TestEnsureSSHConfigManagedHosts_WithAgentsAndPort(t *testing.T) {
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:   tmpDir,
		AgentsDir:  filepath.Join(tmpDir, "agents"),
		SecretsDir: filepath.Join(tmpDir, "secrets"),
	}
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	cfg, err := config.NewAgentConfig("worker1", "base", "worker")
	if err != nil {
		t.Fatalf("NewAgentConfig failed: %v", err)
	}
	if err := config.SaveAgentConfig(cfg, paths); err != nil {
		t.Fatalf("SaveAgentConfig failed: %v", err)
	}

	mockExec := sndbxRuntime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) >= 2 && args[0] == "port" {
			return helperCommand(ctx, "echo", "0.0.0.0:42222")
		}
		return helperCommand(ctx, "false")
	})
	ctx := sndbxRuntime.WithCommandExecutor(context.Background(), mockExec)

	hostConfig := filepath.Join(tmpDir, "ssh_config")
	// Test file without trailing newline
	if err := os.WriteFile(hostConfig, []byte("Host existing-host\n    User tester"), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := EnsureSSHConfigManagedHosts(ctx, paths, hostConfig); err != nil {
		t.Fatalf("EnsureSSHConfigManagedHosts failed: %v", err)
	}

	content, err := os.ReadFile(hostConfig)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	strContent := string(content)
	if !strings.Contains(strContent, "worker1") || !strings.Contains(strContent, "42222") {
		t.Errorf("expected worker1 and port 42222 in ssh config, got:\n%s", strContent)
	}
}

func TestSyncToolboxSSHSettings_WithRunningAgents(t *testing.T) {
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:   tmpDir,
		AgentsDir:  filepath.Join(tmpDir, "agents"),
		SecretsDir: filepath.Join(tmpDir, "secrets"),
	}
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	cfg, err := config.NewAgentConfig("bot1", "base", "worker")
	if err != nil {
		t.Fatalf("NewAgentConfig failed: %v", err)
	}
	if err := config.SaveAgentConfig(cfg, paths); err != nil {
		t.Fatalf("SaveAgentConfig failed: %v", err)
	}

	mockExec := sndbxRuntime.CommandExecutorFunc(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name == "podman" && len(args) >= 2 && args[0] == "port" {
			return helperCommand(ctx, "echo", "0.0.0.0:42222")
		}
		return helperCommand(ctx, "false")
	})
	ctx := sndbxRuntime.WithCommandExecutor(context.Background(), mockExec)

	settingsDir := filepath.Join(tmpDir, "Toolbox", "plugins", "ssh")
	if err := os.MkdirAll(settingsDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := SyncToolboxSSHSettings(ctx, paths, settingsPath); err != nil {
		t.Fatalf("SyncToolboxSSHSettings failed: %v", err)
	}

	content, _ := os.ReadFile(settingsPath)
	strContent := string(content)
	if !strings.Contains(strContent, "fleet-bot1") || !strings.Contains(strContent, "TBX_DEPLOYMENT_TARGET_ENVagent@fleet-bot1") {
		t.Errorf("expected running agent bot1 in toolbox settings, got:\n%s", strContent)
	}
}

func TestRemoveToolboxSSHSettings_TargetedRemovals(t *testing.T) {
	tmpDir := t.TempDir()
	settingsPath := filepath.Join(tmpDir, "settings.json")
	initialJSON := `{
		"envsJson": "{\"remotes\":[{\"host\":\"fleet-alpha\"},{\"host\":\"sndbx-beta\"},{\"host\":\"keep-me\"}]}",
		"CONNECTION_STRINGS_CACHE": "{\"agent@fleet-alpha\":{},\"agent@sndbx-beta\":{},\"agent@keep-me\":{}}",
		"TBX_DEPLOYMENT_TARGET_ENVagent@fleet-alpha": "1",
		"TBX_DEPLOYMENT_TARGET_ENVagent@sndbx-beta": "1",
		"TBX_AUTO_CONNECT_ENVagent@fleet-alpha": "false",
		"TBX_AUTO_CONNECT_ENVagent@sndbx-beta": "false",
		"otherKey": "preserved"
	}`
	if err := os.WriteFile(settingsPath, []byte(initialJSON), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := RemoveToolboxSSHSettings(settingsPath); err != nil {
		t.Fatalf("RemoveToolboxSSHSettings failed: %v", err)
	}

	content, _ := os.ReadFile(settingsPath)
	strContent := string(content)
	if strings.Contains(strContent, "fleet-alpha") || strings.Contains(strContent, "sndbx-beta") {
		t.Errorf("expected fleet and sndbx entries to be removed, got:\n%s", strContent)
	}
	if !strings.Contains(strContent, "keep-me") || !strings.Contains(strContent, "otherKey") {
		t.Errorf("expected keep-me and otherKey to be preserved, got:\n%s", strContent)
	}
}

func TestRemoveGatewayAndToolbox_DiscoveredAndLegacy(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	jbDir := filepath.Join(tmpHome, ".local", "share", "JetBrains", "JetBrainsGateway2024.1")
	if err := os.MkdirAll(filepath.Join(jbDir, "sndbx-gateway"), 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(jbDir, "fleet-gateway", "lib"), 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	jarFile := filepath.Join(jbDir, "fleet-gateway", "lib", "fleet-gateway.jar")
	if err := os.WriteFile(jarFile, []byte("jar"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	paths := config.Paths{
		DataHome:      filepath.Join(tmpHome, "data"),
		SSHConfigFile: filepath.Join(tmpHome, "data", "ssh_config"),
	}
	if err := paths.EnsureDirectories(); err != nil {
		t.Fatalf("EnsureDirectories failed: %v", err)
	}

	legacyToolboxDir := filepath.Join(paths.DataHome, "plugins", "jetbrains-toolbox", "sndbx")
	if err := os.MkdirAll(legacyToolboxDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	activeToolboxDir := filepath.Join(tmpHome, ".local", "share", "JetBrains", "Toolbox", "plugins", "fleet")
	if err := os.MkdirAll(activeToolboxDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	// 1. ListPlugins sees the discovered jar and active toolbox dir
	plugins := ListPlugins(paths)
	var gwFound, tbFound bool
	for _, p := range plugins {
		if p.ID == PluginIDGateway && p.Installed {
			gwFound = true
		}
		if p.ID == PluginIDToolbox && p.Installed {
			tbFound = true
		}
	}
	if !gwFound {
		t.Errorf("expected ListPlugins to find installed gateway plugin at %s", jarFile)
	}
	if !tbFound {
		t.Errorf("expected ListPlugins to find installed toolbox plugin at %s", activeToolboxDir)
	}

	// 2. RemoveGatewayPlugin cleans up discovered legacy dir
	var buf bytes.Buffer
	remGW, err := RemoveGatewayPlugin(context.Background(), paths, &buf)
	if err != nil {
		t.Fatalf("RemoveGatewayPlugin failed: %v", err)
	}
	foundLegacyGW := false
	for _, p := range remGW.RemovedPaths {
		if strings.Contains(p, "sndbx-gateway") {
			foundLegacyGW = true
		}
	}
	if !foundLegacyGW {
		t.Errorf("expected legacy sndbx-gateway in discovered IDE to be removed")
	}

	// 3. RemoveToolboxPlugin cleans up legacy central dir
	remTB, err := RemoveToolboxPlugin(context.Background(), paths, &buf)
	if err != nil {
		t.Fatalf("RemoveToolboxPlugin failed: %v", err)
	}
	foundLegacyTB := false
	for _, p := range remTB.RemovedPaths {
		if strings.Contains(p, "sndbx") {
			foundLegacyTB = true
		}
	}
	if !foundLegacyTB {
		t.Errorf("expected legacy central toolbox directory to be removed")
	}
}

func TestWriteSSHConfigFileAtomic_EdgeCases(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Directory creation failure
	if err := writeSSHConfigFileAtomic("/dev/null/forbidden/config", []byte("data"), 0600); err == nil {
		t.Errorf("expected error when writing to impossible directory")
	}

	// 2. Initial write without backup, followed by second write creating backup
	target := filepath.Join(tmpDir, "ssh_config")
	if err := writeSSHConfigFileAtomic(target, []byte("version1"), 0); err != nil {
		t.Fatalf("first writeSSHConfigFileAtomic failed: %v", err)
	}
	if err := writeSSHConfigFileAtomic(target, []byte("version2"), 0600); err != nil {
		t.Fatalf("second writeSSHConfigFileAtomic failed: %v", err)
	}

	bak := target + ".bak"
	bakContent, err := os.ReadFile(bak)
	if err != nil || string(bakContent) != "version1" {
		t.Errorf("expected backup file with version1, got err=%v, content=%q", err, string(bakContent))
	}

	// 3. Rename failure when target is a non-empty directory
	dirTarget := filepath.Join(tmpDir, "dir_as_target")
	if err := os.MkdirAll(filepath.Join(dirTarget, "subfile"), 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := writeSSHConfigFileAtomic(dirTarget, []byte("version3"), 0600); err == nil {
		t.Errorf("expected error when target is a non-empty directory")
	}
}

func TestEnsureAndRemoveSSHConfigInclude_Errors(t *testing.T) {
	tmpDir := t.TempDir()

	// Reading error when hostSSHConfigFile is a directory
	if _, err := EnsureSSHConfigInclude("/target", tmpDir); err == nil {
		t.Errorf("expected EnsureSSHConfigInclude to fail when hostSSHConfigFile is a directory")
	}
	if _, err := RemoveSSHConfigInclude("/target", tmpDir); err == nil {
		t.Errorf("expected RemoveSSHConfigInclude to fail when hostSSHConfigFile is a directory")
	}
}

type stepFailWriter struct {
	writeCount int
	failAt     int
}

func (s *stepFailWriter) Write(p []byte) (n int, err error) {
	s.writeCount++
	if s.writeCount >= s.failAt {
		return 0, fmt.Errorf("simulated write failure at write %d", s.writeCount)
	}
	return len(p), nil
}

func TestPlatformPathResolutions(t *testing.T) {
	origGOOS := currentGOOS
	origHome := userHomeDir
	defer func() {
		currentGOOS = origGOOS
		userHomeDir = origHome
	}()

	fakeHome := filepath.Join(string(filepath.Separator), "home", "testhome")
	userHomeDir = func() (string, error) {
		return fakeHome, nil
	}

	// 1. Darwin
	currentGOOS = "darwin"
	darwinRoots := GetDefaultJetBrainsSearchRoots()
	if len(darwinRoots) != 1 || !strings.Contains(darwinRoots[0], filepath.Join("Library", "Application Support", "JetBrains")) {
		t.Errorf("unexpected darwin roots: %v", darwinRoots)
	}
	darwinTB := GetDefaultToolboxPluginDir()
	if !strings.Contains(darwinTB, filepath.Join("Library", "Application Support", "JetBrains", "Toolbox", "plugins", "fleet")) {
		t.Errorf("unexpected darwin toolbox dir: %s", darwinTB)
	}
	darwinSSH := GetDefaultToolboxSSHSettingsPath()
	if !strings.Contains(darwinSSH, filepath.Join("Library", "Application Support", "JetBrains", "Toolbox", "plugins", "ssh", "settings.json")) {
		t.Errorf("unexpected darwin toolbox ssh path: %s", darwinSSH)
	}

	// 2. Windows with APPDATA / LOCALAPPDATA
	currentGOOS = "windows"
	t.Setenv("APPDATA", filepath.Join("C:", "Users", "tester", "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join("C:", "Users", "tester", "AppData", "Local"))

	winRoots := GetDefaultJetBrainsSearchRoots()
	if len(winRoots) != 1 || !strings.Contains(winRoots[0], filepath.Join("AppData", "Roaming", "JetBrains")) {
		t.Errorf("unexpected windows search roots with APPDATA: %v", winRoots)
	}
	winTB := GetDefaultToolboxPluginDir()
	if !strings.Contains(winTB, filepath.Join("AppData", "Local", "JetBrains", "Toolbox", "plugins", "fleet")) {
		t.Errorf("unexpected windows toolbox dir with LOCALAPPDATA: %s", winTB)
	}
	winSSH := GetDefaultToolboxSSHSettingsPath()
	if !strings.Contains(winSSH, filepath.Join("AppData", "Local", "JetBrains", "Toolbox", "plugins", "ssh", "settings.json")) {
		t.Errorf("unexpected windows ssh path with LOCALAPPDATA: %s", winSSH)
	}

	// 3. Windows without APPDATA / LOCALAPPDATA
	t.Setenv("APPDATA", "")
	t.Setenv("LOCALAPPDATA", "")
	winRootsNoApp := GetDefaultJetBrainsSearchRoots()
	if len(winRootsNoApp) != 0 {
		t.Errorf("expected 0 windows roots without APPDATA, got: %v", winRootsNoApp)
	}
	winTBNoApp := GetDefaultToolboxPluginDir()
	if !strings.Contains(winTBNoApp, filepath.Join(fakeHome, "AppData", "Local", "JetBrains", "Toolbox", "plugins", "fleet")) {
		t.Errorf("unexpected windows toolbox dir without LOCALAPPDATA: %s", winTBNoApp)
	}
	winSSHNoApp := GetDefaultToolboxSSHSettingsPath()
	if !strings.Contains(winSSHNoApp, filepath.Join(fakeHome, "AppData", "Local", "JetBrains", "Toolbox", "plugins", "ssh", "settings.json")) {
		t.Errorf("unexpected windows ssh path without LOCALAPPDATA: %s", winSSHNoApp)
	}

	// 4. Linux / Default Unix
	currentGOOS = "linux"
	linuxRoots := GetDefaultJetBrainsSearchRoots()
	if len(linuxRoots) != 2 || !strings.Contains(linuxRoots[0], ".local") || !strings.Contains(linuxRoots[1], ".config") {
		t.Errorf("unexpected linux search roots: %v", linuxRoots)
	}
	linuxTB := GetDefaultToolboxPluginDir()
	if !strings.Contains(linuxTB, filepath.Join(".local", "share", "JetBrains", "Toolbox", "plugins", "fleet")) {
		t.Errorf("unexpected linux toolbox dir: %s", linuxTB)
	}
	linuxSSH := GetDefaultToolboxSSHSettingsPath()
	if !strings.Contains(linuxSSH, filepath.Join(".local", "share", "JetBrains", "Toolbox", "plugins", "ssh", "settings.json")) {
		t.Errorf("unexpected linux toolbox ssh path: %s", linuxSSH)
	}

	// 5. UserHomeDir error path across all functions
	userHomeDir = func() (string, error) {
		return "", fmt.Errorf("simulated home dir error")
	}
	if roots := GetDefaultJetBrainsSearchRoots(); roots != nil {
		t.Errorf("expected nil roots on home dir error, got: %v", roots)
	}
	if tb := GetDefaultToolboxPluginDir(); tb != "" {
		t.Errorf("expected empty toolbox dir on home dir error, got: %s", tb)
	}
	if ssh := GetDefaultToolboxSSHSettingsPath(); ssh != "" {
		t.Errorf("expected empty ssh path on home dir error, got: %s", ssh)
	}
}

func TestGenerateJAR_ErrorPaths(t *testing.T) {
	origNewZipWriter := newZipWriter
	defer func() {
		newZipWriter = origNewZipWriter
	}()

	// 1. Closed writer causing Create error
	newZipWriter = func(w io.Writer) *zip.Writer {
		zw := zip.NewWriter(w)
		_ = zw.Close()
		return zw
	}

	if _, err := GenerateGatewayPluginJAR(); err == nil {
		t.Errorf("expected error from GenerateGatewayPluginJAR when zip.Writer is closed")
	}
	if _, err := GenerateToolboxPluginJAR(); err == nil {
		t.Errorf("expected error from GenerateToolboxPluginJAR when zip.Writer is closed")
	}

	// 2. Step writer failing on write
	stepWriter := &stepFailWriter{failAt: 1}
	newZipWriter = func(w io.Writer) *zip.Writer {
		return zip.NewWriter(stepWriter)
	}
	if _, err := GenerateGatewayPluginJAR(); err == nil {
		t.Errorf("expected error from GenerateGatewayPluginJAR at step 1")
	}
	if _, err := GenerateToolboxPluginJAR(); err == nil {
		t.Errorf("expected error from GenerateToolboxPluginJAR at step 1")
	}
}

func TestInstallGatewayPlugin_DiscoveredIDEAndErrors(t *testing.T) {
	tmpDir := t.TempDir()
	paths := config.Paths{
		DataHome:      filepath.Join(tmpDir, "data"),
		AgentsDir:     filepath.Join(tmpDir, "data", "agents"),
		SSHConfigFile: filepath.Join(tmpDir, "data", "ssh_config"),
	}
	_ = paths.EnsureDirectories()

	// Create mock discovered IDE directory
	origHome := userHomeDir
	origGOOS := currentGOOS
	defer func() {
		userHomeDir = origHome
		currentGOOS = origGOOS
	}()
	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	currentGOOS = "linux"

	ideRoot := filepath.Join(tmpDir, ".local", "share", "JetBrains")
	mockIDE := filepath.Join(ideRoot, "JetBrainsGateway2024.1")
	_ = os.MkdirAll(mockIDE, 0755)

	var out bytes.Buffer
	res, err := InstallGatewayPlugin(context.Background(), paths, &out)
	if err != nil {
		t.Fatalf("InstallGatewayPlugin failed: %v", err)
	}
	if len(res.InstalledPaths) < 2 {
		t.Errorf("expected central and discovered IDE jar installed, got: %v", res.InstalledPaths)
	}

	// Unwritable central directory jar (file exists as a directory where jar should be)
	conflictDir := filepath.Join(paths.DataHome, "plugins", "jetbrains-gateway", "fleet-gateway", "lib", "fleet-gateway.jar")
	_ = os.Remove(conflictDir)
	_ = os.MkdirAll(filepath.Join(conflictDir, "sub"), 0755)
	if _, err := InstallGatewayPlugin(context.Background(), paths, &out); err == nil {
		t.Errorf("expected error when central jar path is an unoverwritable directory")
	}

	// Error generating JAR
	origNewZipWriter := newZipWriter
	defer func() { newZipWriter = origNewZipWriter }()
	newZipWriter = func(w io.Writer) *zip.Writer {
		zw := zip.NewWriter(w)
		_ = zw.Close()
		return zw
	}
	if _, err := InstallGatewayPlugin(context.Background(), paths, &out); err == nil {
		t.Errorf("expected error when JAR generation fails")
	}
}
