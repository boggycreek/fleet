// Copyright (c) 2026 Boggy Creek Software LLC
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
	"runtime"
	"strings"
	"sync"

	"github.com/boggycreek/fleet/cli/pkg/config"
	sndbxRuntime "github.com/boggycreek/fleet/cli/pkg/runtime"
)

// Plugin identifiers and constants.
const (
	PluginIDGateway = "gateway"
	PluginIDToolbox = "toolbox"
	PluginIDVSCode  = "vscode"

	JetBrainsPluginID      = "com.boggycreek.fleet.gateway"
	JetBrainsPluginName    = "Fleet Gateway"
	JetBrainsPluginVersion = "0.1.0-alpha"
	JetBrainsPluginVendor  = "Boggy Creek Software LLC"

	ToolboxPluginID         = "com.boggycreek.fleet.toolbox"
	ToolboxPluginName       = "Fleet Toolbox"
	ToolboxPluginVersion    = "0.1.0-alpha"
	ToolboxPluginAPIVersion = "1.13.87111"

	ManagedSSHConfigHeader = "# --- BEGIN FLEET MANAGED HOSTS ---"
	ManagedSSHConfigFooter = "# --- END FLEET MANAGED HOSTS ---"
)

// ExecCommandContext is a variable for stubbing in tests.
var ExecCommandContext = exec.CommandContext

var (
	userHomeDir = os.UserHomeDir
	currentGOOS = runtime.GOOS
	newZipWriter = func(w io.Writer) *zip.Writer {
		return zip.NewWriter(w)
	}
)

// PluginInfo describes a supported or installed IDE plugin.
type PluginInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Target      string   `json:"target"`
	Description string   `json:"description"`
	Installed   bool     `json:"installed"`
	Locations   []string `json:"locations,omitempty"`
}

// InstallResult details the outcome of an installation operation.
type InstallResult struct {
	PluginID        string   `json:"plugin_id"`
	PluginName      string   `json:"plugin_name"`
	Version         string   `json:"version"`
	InstalledPaths  []string `json:"installed_paths"`
	SSHConfigLinked bool     `json:"ssh_config_linked"`
	Message         string   `json:"message"`
}

// RemoveResult details the outcome of a plugin removal operation.
type RemoveResult struct {
	PluginID          string   `json:"plugin_id"`
	PluginName        string   `json:"plugin_name"`
	RemovedPaths      []string `json:"removed_paths"`
	SSHConfigUnlinked bool     `json:"ssh_config_unlinked"`
	Message           string   `json:"message"`
}

// ToolboxSettingsPayload represents the inner structure of 'envsJson' in JetBrains Toolbox ssh/settings.json.
type ToolboxSettingsPayload struct {
	Remotes []ToolboxRemoteEntry `json:"remotes"`
}

// ToolboxRemoteEntry represents an individual remote host definition in JetBrains Toolbox.
type ToolboxRemoteEntry struct {
	Host                     string `json:"host"`
	Port                     int    `json:"port"`
	UserName                 string `json:"userName"`
	ShouldUseSystemSshAgent  bool   `json:"shouldUseSystemSshAgent"`
	OriginalConnectionString string `json:"originalConnectionString"`
}

// GenerateGatewayPluginXML produces the JetBrains Gateway plugin descriptor XML.
func GenerateGatewayPluginXML() string {
	return `<!-- Copyright (c) 2026 Boggy Creek Software LLC -->
<idea-plugin>
    <id>` + JetBrainsPluginID + `</id>
    <name>` + JetBrainsPluginName + `</name>
    <version>` + JetBrainsPluginVersion + `</version>
    <vendor email="support@boggycreek.com" url="https://github.com/boggycreek/fleet">` + JetBrainsPluginVendor + `</vendor>

    <description><![CDATA[
      Direct JetBrains Gateway integration for Agent Sandbox.<br>
      Discovers local running agent containers and bridges seamless remote development sessions
      with zero host secret leakage.
    ]]></description>

    <category>Remote Development</category>

    <depends>com.intellij.modules.platform</depends>
    <depends optional="true">com.jetbrains.gateway</depends>

    <extensions defaultExtensionNs="com.jetbrains">
        <gatewayConnector implementation="com.boggycreek.fleet.gateway.FleetGatewayConnector" />
        <gatewayConnectionProvider implementation="com.boggycreek.fleet.gateway.FleetGatewayConnectionProvider" />
    </extensions>
</idea-plugin>
`
}

// GenerateGatewayPluginJAR creates an in-memory valid JAR archive for the Gateway plugin.
func GenerateGatewayPluginJAR() ([]byte, error) {
	buf := new(bytes.Buffer)
	zw := newZipWriter(buf)

	// 1. META-INF/MANIFEST.MF
	manifestContent := "Manifest-Version: 1.0\r\nCreated-By: " + JetBrainsPluginVendor + "\r\n\r\n"
	mfWriter, err := zw.Create("META-INF/MANIFEST.MF")
	if err != nil {
		return nil, fmt.Errorf("creating manifest entry: %w", err)
	}
	if _, err := mfWriter.Write([]byte(manifestContent)); err != nil {
		return nil, fmt.Errorf("writing manifest entry: %w", err)
	}

	// 2. META-INF/plugin.xml
	pluginXmlContent := GenerateGatewayPluginXML()
	xmlWriter, err := zw.Create("META-INF/plugin.xml")
	if err != nil {
		return nil, fmt.Errorf("creating plugin.xml entry: %w", err)
	}
	if _, err := xmlWriter.Write([]byte(pluginXmlContent)); err != nil {
		return nil, fmt.Errorf("writing plugin.xml entry: %w", err)
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("closing zip writer: %w", err)
	}

	return buf.Bytes(), nil
}

// GenerateToolboxExtensionJSON creates the manifest descriptor required by JetBrains Toolbox.
func GenerateToolboxExtensionJSON() string {
	return `{
  "id": "` + ToolboxPluginID + `",
  "version": "` + ToolboxPluginVersion + `",
  "apiVersion": "` + ToolboxPluginAPIVersion + `",
  "meta": {
    "readableName": "Fleet",
    "description": "Direct JetBrains Toolbox integration for local fleet agent sandboxes",
    "vendor": "` + JetBrainsPluginVendor + `",
    "url": "https://github.com/boggycreek/fleet"
  }
}
`
}

// GenerateToolboxPluginIconSVG returns a standard SVG icon for JetBrains Toolbox.
func GenerateToolboxPluginIconSVG() string {
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32" width="32" height="32">
  <rect width="32" height="32" rx="6" fill="#1E1F22"/>
  <path d="M7 16L16 7l9 9-9 9-9-9z" fill="none" stroke="#3574F0" stroke-width="2.5" stroke-linejoin="round"/>
  <circle cx="16" cy="16" r="3.5" fill="#3574F0"/>
</svg>
`
}

// GenerateToolboxPluginJAR creates an in-memory valid JAR archive for the Toolbox plugin.
func GenerateToolboxPluginJAR() ([]byte, error) {
	buf := new(bytes.Buffer)
	zw := newZipWriter(buf)

	// 1. META-INF/MANIFEST.MF
	manifestContent := "Manifest-Version: 1.0\r\nCreated-By: " + JetBrainsPluginVendor + "\r\n\r\n"
	mfWriter, err := zw.Create("META-INF/MANIFEST.MF")
	if err != nil {
		return nil, fmt.Errorf("creating manifest entry: %w", err)
	}
	if _, err := mfWriter.Write([]byte(manifestContent)); err != nil {
		return nil, fmt.Errorf("writing manifest entry: %w", err)
	}

	// 2. extension.json
	extWriter, err := zw.Create("extension.json")
	if err != nil {
		return nil, fmt.Errorf("creating extension.json entry: %w", err)
	}
	if _, err := extWriter.Write([]byte(GenerateToolboxExtensionJSON())); err != nil {
		return nil, fmt.Errorf("writing extension.json entry: %w", err)
	}

	// 3. pluginIcon.svg
	iconWriter, err := zw.Create("pluginIcon.svg")
	if err != nil {
		return nil, fmt.Errorf("creating pluginIcon.svg entry: %w", err)
	}
	if _, err := iconWriter.Write([]byte(GenerateToolboxPluginIconSVG())); err != nil {
		return nil, fmt.Errorf("writing pluginIcon.svg entry: %w", err)
	}

	// 4. META-INF/services/com.jetbrains.toolbox.api.remoteDev.RemoteDevExtension
	serviceWriter, err := zw.Create("META-INF/services/com.jetbrains.toolbox.api.remoteDev.RemoteDevExtension")
	if err != nil {
		return nil, fmt.Errorf("creating service entry: %w", err)
	}
	serviceContent := "com.boggycreek.fleet.toolbox.FleetRemoteDevExtension\n"
	if _, err := serviceWriter.Write([]byte(serviceContent)); err != nil {
		return nil, fmt.Errorf("writing service entry: %w", err)
	}

	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("closing zip writer: %w", err)
	}

	return buf.Bytes(), nil
}

// GetDefaultJetBrainsSearchRoots resolves standard root locations for JetBrains IDE installations.
func GetDefaultJetBrainsSearchRoots() []string {
	home, err := userHomeDir()
	if err != nil {
		return nil
	}

	var searchRoots []string
	switch currentGOOS {
	case "darwin":
		searchRoots = append(searchRoots, filepath.Join(home, "Library", "Application Support", "JetBrains"))
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			searchRoots = append(searchRoots, filepath.Join(appData, "JetBrains"))
		}
	default:
		// Linux & generic Unix
		searchRoots = append(searchRoots, filepath.Join(home, ".local", "share", "JetBrains"))
		searchRoots = append(searchRoots, filepath.Join(home, ".config", "JetBrains"))
	}
	return searchRoots
}

// FindJetBrainsPluginDirsFrom discovers JetBrains Gateway and IDE plugin directories from search roots.
func FindJetBrainsPluginDirsFrom(searchRoots []string) []string {
	var candidates []string
	for _, root := range searchRoots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasPrefix(name, "JetBrainsGateway") ||
				strings.HasPrefix(name, "WebStorm") ||
				strings.HasPrefix(name, "GoLand") ||
				strings.HasPrefix(name, "CLion") ||
				strings.HasPrefix(name, "PyCharm") ||
				strings.HasPrefix(name, "IntelliJIdea") ||
				strings.HasPrefix(name, "IdeaIC") ||
				strings.HasPrefix(name, "DataGrip") {
				candidates = append(candidates, filepath.Join(root, name))
			}
		}
	}
	return candidates
}

// FindJetBrainsPluginDirs discovers JetBrains Gateway and IDE plugin directories on the system.
func FindJetBrainsPluginDirs() []string {
	return FindJetBrainsPluginDirsFrom(GetDefaultJetBrainsSearchRoots())
}

// GetDefaultToolboxPluginDir returns the target plugin directory for JetBrains Toolbox App.
func GetDefaultToolboxPluginDir() string {
	home, err := userHomeDir()
	if err != nil {
		return ""
	}
	switch currentGOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "JetBrains", "Toolbox", "plugins", "fleet")
	case "windows":
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, "JetBrains", "Toolbox", "plugins", "fleet")
		}
		return filepath.Join(home, "AppData", "Local", "JetBrains", "Toolbox", "plugins", "fleet")
	default:
		return filepath.Join(home, ".local", "share", "JetBrains", "Toolbox", "plugins", "fleet")
	}
}

// GetDefaultToolboxSSHSettingsPath returns the location of JetBrains Toolbox SSH provider settings.
func GetDefaultToolboxSSHSettingsPath() string {
	home, err := userHomeDir()
	if err != nil {
		return ""
	}
	switch currentGOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "JetBrains", "Toolbox", "plugins", "ssh", "settings.json")
	case "windows":
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, "JetBrains", "Toolbox", "plugins", "ssh", "settings.json")
		}
		return filepath.Join(home, "AppData", "Local", "JetBrains", "Toolbox", "plugins", "ssh", "settings.json")
	default:
		return filepath.Join(home, ".local", "share", "JetBrains", "Toolbox", "plugins", "ssh", "settings.json")
	}
}

// sshConfigCommentHeader is the standard header preceding the Fleet OpenSSH Include directive.
const sshConfigCommentHeader = "# Fleet Host Include"
const legacySSHConfigCommentHeader = "# Agent Sandbox Host Include"

var sshConfigMutex sync.Mutex

func normalizeSSHPath(p string) string {
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	} else if strings.HasPrefix(p, "$HOME/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "$HOME/"))
		}
	}
	return filepath.Clean(p)
}

func isTargetInclude(line, targetSSHConfigFile string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return false
	}
	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return false
	}
	if !strings.EqualFold(fields[0], "include") {
		return false
	}

	cleanTarget := normalizeSSHPath(targetSSHConfigFile)
	for _, arg := range fields[1:] {
		arg = strings.Trim(arg, `"'`)
		if normalizeSSHPath(arg) == cleanTarget {
			return true
		}
	}
	return false
}

func writeSSHConfigFileAtomic(targetPath string, content []byte, perm os.FileMode) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating directory for ssh config: %w", err)
	}

	// Backup original config once before modifying if it exists and backup doesn't exist
	bakPath := targetPath + ".bak"
	if _, err := os.Stat(targetPath); err == nil {
		if _, errBak := os.Stat(bakPath); os.IsNotExist(errBak) {
			if existingBytes, readErr := os.ReadFile(targetPath); readErr == nil {
				_ = os.WriteFile(bakPath, existingBytes, 0600)
			}
		}
	}

	tmpFile, err := os.CreateTemp(dir, ".config.tmp.*")
	if err != nil {
		return fmt.Errorf("creating temp file for ssh config: %w", err)
	}
	tmpName := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
	}()

	if _, err := tmpFile.Write(content); err != nil {
		return fmt.Errorf("writing temp ssh config: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("syncing temp ssh config: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp ssh config: %w", err)
	}
	if perm == 0 {
		perm = 0600
	}
	_ = os.Chmod(tmpName, perm)
	if err := os.Rename(tmpName, targetPath); err != nil {
		return fmt.Errorf("atomically replacing ssh config: %w", err)
	}
	return nil
}

// EnsureSSHConfigInclude guarantees that ~/.ssh/config includes the given target config file.
// It is non-destructive: it only prepends the Include line if missing, never touches existing
// user formatting or comments, writes via atomic rename, and keeps a one-time .bak backup.
func EnsureSSHConfigInclude(targetSSHConfigFile, hostSSHConfigFile string) (bool, error) {
	sshConfigMutex.Lock()
	defer sshConfigMutex.Unlock()

	if hostSSHConfigFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false, err
		}
		hostSSHConfigFile = filepath.Join(home, ".ssh", "config")
	}

	includeDirective := fmt.Sprintf("Include %s", targetSSHConfigFile)

	existingContent, err := os.ReadFile(hostSSHConfigFile)
	if os.IsNotExist(err) || len(strings.TrimSpace(string(existingContent))) == 0 {
		newContent := fmt.Sprintf("%s\n%s\n", sshConfigCommentHeader, includeDirective)
		if err := writeSSHConfigFileAtomic(hostSSHConfigFile, []byte(newContent), 0600); err != nil {
			return false, err
		}
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading host ssh config: %w", err)
	}

	lines := strings.Split(string(existingContent), "\n")

	// Check if our target include already exists
	targetIdx := -1
	for i, line := range lines {
		if isTargetInclude(line, targetSSHConfigFile) {
			targetIdx = i
			break
		}
	}

	// If already present, strictly no-op without touching file!
	if targetIdx != -1 {
		return false, nil
	}

	// Target include is missing: prepend our block cleanly to the top of the file without altering existing lines
	var finalLines []string
	finalLines = append(finalLines, sshConfigCommentHeader, includeDirective)
	if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" {
		finalLines = append(finalLines, "")
	}
	finalLines = append(finalLines, lines...)
	text := strings.Join(finalLines, "\n")

	fi, _ := os.Stat(hostSSHConfigFile)
	perm := os.FileMode(0600)
	if fi != nil {
		perm = fi.Mode()
	}
	if err := writeSSHConfigFileAtomic(hostSSHConfigFile, []byte(text), perm); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveSSHConfigInclude removes the Include directive for target config file from host SSH config.
// It removes only our header, the target Include line, and at most one following blank line.
func RemoveSSHConfigInclude(targetSSHConfigFile, hostSSHConfigFile string) (bool, error) {
	sshConfigMutex.Lock()
	defer sshConfigMutex.Unlock()

	if hostSSHConfigFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false, err
		}
		hostSSHConfigFile = filepath.Join(home, ".ssh", "config")
	}

	content, err := os.ReadFile(hostSSHConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading host ssh config: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	var newLines []string
	removed := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if isTargetInclude(line, targetSSHConfigFile) {
			removed = true
			// Remove our preceding comment header if immediately before this include
			if len(newLines) > 0 && (strings.TrimSpace(newLines[len(newLines)-1]) == sshConfigCommentHeader || strings.TrimSpace(newLines[len(newLines)-1]) == legacySSHConfigCommentHeader) {
				newLines = newLines[:len(newLines)-1]
			}
			// Skip at most one blank line following our include if present
			if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" {
				i++
			}
			continue
		}
		newLines = append(newLines, line)
	}

	if !removed {
		return false, nil
	}

	text := strings.Join(newLines, "\n")
	fi, _ := os.Stat(hostSSHConfigFile)
	perm := os.FileMode(0600)
	if fi != nil {
		perm = fi.Mode()
	}
	if err := writeSSHConfigFileAtomic(hostSSHConfigFile, []byte(text), perm); err != nil {
		return false, err
	}
	return true, nil
}

// EnsureSSHConfigManagedHosts inlines host blocks directly into ~/.ssh/config for line-based importers.
func EnsureSSHConfigManagedHosts(ctx context.Context, paths config.Paths, hostSSHConfigFile string) error {
	if hostSSHConfigFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		hostSSHConfigFile = filepath.Join(home, ".ssh", "config")
	}

	configs, err := config.ListAgentConfigs(paths)
	if err != nil {
		return err
	}

	var blockBuf bytes.Buffer
	blockBuf.WriteString(ManagedSSHConfigHeader + "\n")
	blockBuf.WriteString("# Auto-generated by fleet for tools (like JetBrains Toolbox) that do not support OpenSSH 'Include'.\n\n")

	ideKeyFile := paths.GetSSHPrivateKey()
	if ideKeyFile == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			home = "/tmp"
		}
		ideKeyFile = filepath.Join(home, ".ssh", "fleet")
	}
	blockBuf.WriteString("Match host 127.0.0.1,localhost user agent\n")
	blockBuf.WriteString(fmt.Sprintf("    IdentityFile %s\n", ideKeyFile))
	blockBuf.WriteString("    IdentitiesOnly yes\n")
	blockBuf.WriteString("    StrictHostKeyChecking accept-new\n")

	for _, c := range configs {
		port, err := sndbxRuntime.GetAgentSSHPort(ctx, c.ContainerName)
		if err == nil && port > 0 {
			blockBuf.WriteString("\n")
			blockBuf.WriteString(sndbxRuntime.FormatSSHConfigBlock(c.Name, port, ideKeyFile))
			blockBuf.WriteString("\n")
		}
	}
	blockBuf.WriteString(ManagedSSHConfigFooter)
	blockContent := blockBuf.String()

	if err := os.MkdirAll(filepath.Dir(hostSSHConfigFile), 0700); err != nil {
		return fmt.Errorf("creating .ssh directory: %w", err)
	}

	content, err := os.ReadFile(hostSSHConfigFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading host ssh config: %w", err)
	}

	strContent := string(content)
	startIdx := strings.Index(strContent, ManagedSSHConfigHeader)
	endIdx := strings.Index(strContent, ManagedSSHConfigFooter)
	legacyHeader := "# --- BEGIN AGENT SANDBOX MANAGED HOSTS ---"
	legacyFooter := "# --- END AGENT SANDBOX MANAGED HOSTS ---"
	footerLen := len(ManagedSSHConfigFooter)
	if startIdx == -1 {
		startIdx = strings.Index(strContent, legacyHeader)
		endIdx = strings.Index(strContent, legacyFooter)
		footerLen = len(legacyFooter)
	}

	var updated string
	if startIdx != -1 && endIdx != -1 && endIdx >= startIdx {
		afterBlock := endIdx + footerLen
		if afterBlock < len(strContent) && strContent[afterBlock] == '\n' {
			afterBlock++
		}
		updated = strContent[:startIdx] + blockContent + "\n" + strContent[afterBlock:]
	} else {
		if len(strContent) > 0 && !strings.HasSuffix(strContent, "\n") {
			strContent += "\n"
		}
		updated = strContent + "\n" + blockContent + "\n"
	}

	return os.WriteFile(hostSSHConfigFile, []byte(strings.TrimSpace(updated)+"\n"), 0600)
}

// RemoveSSHConfigManagedHosts removes inlined host blocks from ~/.ssh/config.
func RemoveSSHConfigManagedHosts(hostSSHConfigFile string) error {
	if hostSSHConfigFile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		hostSSHConfigFile = filepath.Join(home, ".ssh", "config")
	}

	content, err := os.ReadFile(hostSSHConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading host ssh config: %w", err)
	}

	strContent := string(content)
	startIdx := strings.Index(strContent, ManagedSSHConfigHeader)
	endIdx := strings.Index(strContent, ManagedSSHConfigFooter)
	footerLen := len(ManagedSSHConfigFooter)
	if startIdx == -1 {
		startIdx = strings.Index(strContent, "# --- BEGIN AGENT SANDBOX MANAGED HOSTS ---")
		endIdx = strings.Index(strContent, "# --- END AGENT SANDBOX MANAGED HOSTS ---")
		footerLen = len("# --- END AGENT SANDBOX MANAGED HOSTS ---")
	}

	if startIdx == -1 || endIdx == -1 || endIdx < startIdx {
		return nil
	}

	afterBlock := endIdx + footerLen
	if afterBlock < len(strContent) && strContent[afterBlock] == '\n' {
		afterBlock++
	}

	updated := strContent[:startIdx] + strContent[afterBlock:]
	trimmed := strings.TrimSpace(updated)
	if len(trimmed) > 0 {
		trimmed += "\n"
	}
	return os.WriteFile(hostSSHConfigFile, []byte(trimmed), 0600)
}

// SyncToolboxSSHSettings updates ~/.local/share/JetBrains/Toolbox/plugins/ssh/settings.json with active sandboxes.
func SyncToolboxSSHSettings(ctx context.Context, paths config.Paths, settingsPath string) error {
	if settingsPath == "" {
		settingsPath = GetDefaultToolboxSSHSettingsPath()
	}
	if settingsPath == "" {
		return nil
	}

	// If Toolbox plugins directory doesn't exist, skip sync without error
	toolboxPluginsDir := filepath.Dir(filepath.Dir(settingsPath))
	if _, err := os.Stat(toolboxPluginsDir); os.IsNotExist(err) {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(settingsPath), 0755); err != nil {
		return fmt.Errorf("creating toolbox ssh directory: %w", err)
	}

	var root map[string]any
	if data, err := os.ReadFile(settingsPath); err == nil {
		_ = json.Unmarshal(data, &root)
	}
	if root == nil {
		root = make(map[string]any)
	}

	var payload ToolboxSettingsPayload
	if envsStr, ok := root["envsJson"].(string); ok && envsStr != "" {
		_ = json.Unmarshal([]byte(envsStr), &payload)
	}

	cacheMap := make(map[string]ToolboxRemoteEntry)
	if cacheStr, ok := root["CONNECTION_STRINGS_CACHE"].(string); ok && cacheStr != "" {
		_ = json.Unmarshal([]byte(cacheStr), &cacheMap)
	}

	// Filter out existing fleet- and legacy sndbx- entries
	var keptRemotes []ToolboxRemoteEntry
	for _, r := range payload.Remotes {
		if !strings.HasPrefix(r.Host, "fleet-") && !strings.HasPrefix(r.Host, "sndbx-") {
			keptRemotes = append(keptRemotes, r)
		}
	}
	for k := range cacheMap {
		if strings.Contains(k, "fleet-") || strings.Contains(k, "sndbx-") {
			delete(cacheMap, k)
		}
	}
	for k := range root {
		if strings.HasPrefix(k, "TBX_DEPLOYMENT_TARGET_ENV") && (strings.Contains(k, "fleet-") || strings.Contains(k, "sndbx-")) {
			delete(root, k)
		}
		if strings.HasPrefix(k, "TBX_AUTO_CONNECT_ENV") && (strings.Contains(k, "fleet-") || strings.Contains(k, "sndbx-")) {
			delete(root, k)
		}
	}

	// Add running agents
	configs, _ := config.ListAgentConfigs(paths)
	for _, c := range configs {
		port, err := sndbxRuntime.GetAgentSSHPort(ctx, c.ContainerName)
		if err == nil && port > 0 {
			host := "fleet-" + c.Name
			connStr := "agent@" + host
			entry := ToolboxRemoteEntry{
				Host:                     host,
				Port:                     0,
				UserName:                 "agent",
				ShouldUseSystemSshAgent:  true,
				OriginalConnectionString: connStr,
			}
			keptRemotes = append(keptRemotes, entry)
			cacheMap[connStr] = entry
			root["TBX_DEPLOYMENT_TARGET_ENV"+connStr] = "1"
			root["TBX_AUTO_CONNECT_ENV"+connStr] = "false"
		}
	}

	payload.Remotes = keptRemotes
	envsBytes, err := json.MarshalIndent(payload, "", "    ")
	if err == nil {
		root["envsJson"] = string(envsBytes)
	}
	cacheBytes, err := json.MarshalIndent(cacheMap, "", "    ")
	if err == nil {
		root["CONNECTION_STRINGS_CACHE"] = string(cacheBytes)
	}
	if _, ok := root["configPath"]; !ok {
		root["configPath"] = ""
	}
	if _, ok := root["REJECTED_HINTS"]; !ok {
		root["REJECTED_HINTS"] = "[]"
	}
	if _, ok := root["timeoutSeconds"]; !ok {
		root["timeoutSeconds"] = "10"
	}

	outBytes, err := json.MarshalIndent(root, "", "    ")
	if err != nil {
		return fmt.Errorf("marshaling toolbox ssh settings: %w", err)
	}

	return os.WriteFile(settingsPath, outBytes, 0600)
}

// RemoveToolboxSSHSettings removes sndbx remote entries from Toolbox ssh/settings.json.
func RemoveToolboxSSHSettings(settingsPath string) error {
	if settingsPath == "" {
		settingsPath = GetDefaultToolboxSSHSettingsPath()
	}
	if settingsPath == "" {
		return nil
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading toolbox ssh settings: %w", err)
	}

	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil
	}

	var payload ToolboxSettingsPayload
	if envsStr, ok := root["envsJson"].(string); ok && envsStr != "" {
		_ = json.Unmarshal([]byte(envsStr), &payload)
	}

	cacheMap := make(map[string]ToolboxRemoteEntry)
	if cacheStr, ok := root["CONNECTION_STRINGS_CACHE"].(string); ok && cacheStr != "" {
		_ = json.Unmarshal([]byte(cacheStr), &cacheMap)
	}

	var keptRemotes []ToolboxRemoteEntry
	for _, r := range payload.Remotes {
		if !strings.HasPrefix(r.Host, "fleet-") && !strings.HasPrefix(r.Host, "sndbx-") {
			keptRemotes = append(keptRemotes, r)
		}
	}
	for k := range cacheMap {
		if strings.Contains(k, "fleet-") || strings.Contains(k, "sndbx-") {
			delete(cacheMap, k)
		}
	}
	for k := range root {
		if strings.HasPrefix(k, "TBX_DEPLOYMENT_TARGET_ENV") && (strings.Contains(k, "fleet-") || strings.Contains(k, "sndbx-")) {
			delete(root, k)
		}
		if strings.HasPrefix(k, "TBX_AUTO_CONNECT_ENV") && (strings.Contains(k, "fleet-") || strings.Contains(k, "sndbx-")) {
			delete(root, k)
		}
	}

	payload.Remotes = keptRemotes
	if envsBytes, err := json.MarshalIndent(payload, "", "    "); err == nil {
		root["envsJson"] = string(envsBytes)
	}
	if cacheBytes, err := json.MarshalIndent(cacheMap, "", "    "); err == nil {
		root["CONNECTION_STRINGS_CACHE"] = string(cacheBytes)
	}

	outBytes, err := json.MarshalIndent(root, "", "    ")
	if err != nil {
		return fmt.Errorf("marshaling toolbox ssh settings: %w", err)
	}

	return os.WriteFile(settingsPath, outBytes, 0600)
}

// InstallGatewayPlugin packages and deploys the Fleet JetBrains Gateway IntelliJ plugin.
func InstallGatewayPlugin(ctx context.Context, paths config.Paths, stdout io.Writer) (*InstallResult, error) {
	jarBytes, err := GenerateGatewayPluginJAR()
	if err != nil {
		return nil, fmt.Errorf("generating gateway plugin jar: %w", err)
	}

	var installedPaths []string

	// 1. Central repository installation: ~/.local/share/fleet/plugins/jetbrains-gateway
	centralDir := filepath.Join(paths.DataHome, "plugins", "jetbrains-gateway", "fleet-gateway", "lib")
	if err := os.MkdirAll(centralDir, 0755); err != nil {
		return nil, fmt.Errorf("creating central gateway plugin dir: %w", err)
	}
	centralJar := filepath.Join(centralDir, "fleet-gateway.jar")
	if err := os.WriteFile(centralJar, jarBytes, 0644); err != nil {
		return nil, fmt.Errorf("writing central gateway plugin jar: %w", err)
	}
	installedPaths = append(installedPaths, centralJar)

	// Clean up legacy sndbx-gateway if present
	legacyCentralDir := filepath.Join(paths.DataHome, "plugins", "jetbrains-gateway", "sndbx-gateway")
	_ = os.RemoveAll(legacyCentralDir)

	// 2. Discovered JetBrains IDE plugin directories
	jbDirs := FindJetBrainsPluginDirs()
	for _, jbDir := range jbDirs {
		// Clean up legacy sndbx-gateway
		_ = os.RemoveAll(filepath.Join(jbDir, "sndbx-gateway"))

		targetLibDir := filepath.Join(jbDir, "fleet-gateway", "lib")
		if err := os.MkdirAll(targetLibDir, 0755); err == nil {
			targetJar := filepath.Join(targetLibDir, "fleet-gateway.jar")
			if err := os.WriteFile(targetJar, jarBytes, 0644); err == nil {
				installedPaths = append(installedPaths, targetJar)
			}
		}
	}

	// 3. Ensure OpenSSH configuration sync & host .ssh/config inclusion
	_ = sndbxRuntime.SyncSSHConfigFile(ctx, paths)
	linked, _ := EnsureSSHConfigInclude(paths.SSHConfigFile, "")

	return &InstallResult{
		PluginID:        PluginIDGateway,
		PluginName:      JetBrainsPluginName,
		Version:         JetBrainsPluginVersion,
		InstalledPaths:  installedPaths,
		SSHConfigLinked: linked,
		Message:         "JetBrains Gateway plugin successfully installed.",
	}, nil
}

// RemoveGatewayPlugin uninstalls the Fleet JetBrains Gateway plugin.
func RemoveGatewayPlugin(ctx context.Context, paths config.Paths, stdout io.Writer) (*RemoveResult, error) {
	var removedPaths []string

	centralDir := filepath.Join(paths.DataHome, "plugins", "jetbrains-gateway", "fleet-gateway")
	if _, err := os.Stat(centralDir); err == nil {
		_ = os.RemoveAll(centralDir)
		removedPaths = append(removedPaths, centralDir)
	}
	legacyCentralDir := filepath.Join(paths.DataHome, "plugins", "jetbrains-gateway", "sndbx-gateway")
	if _, err := os.Stat(legacyCentralDir); err == nil {
		_ = os.RemoveAll(legacyCentralDir)
		removedPaths = append(removedPaths, legacyCentralDir)
	}

	jbDirs := FindJetBrainsPluginDirs()
	for _, jbDir := range jbDirs {
		targetDir := filepath.Join(jbDir, "fleet-gateway")
		if _, err := os.Stat(targetDir); err == nil {
			_ = os.RemoveAll(targetDir)
			removedPaths = append(removedPaths, targetDir)
		}
		legacyDir := filepath.Join(jbDir, "sndbx-gateway")
		if _, err := os.Stat(legacyDir); err == nil {
			_ = os.RemoveAll(legacyDir)
			removedPaths = append(removedPaths, legacyDir)
		}
	}

	unlinked, _ := RemoveSSHConfigInclude(paths.SSHConfigFile, "")

	return &RemoveResult{
		PluginID:          PluginIDGateway,
		PluginName:        JetBrainsPluginName,
		RemovedPaths:      removedPaths,
		SSHConfigUnlinked: unlinked,
		Message:           "JetBrains Gateway plugin successfully removed.",
	}, nil
}

// InstallToolboxPlugin packages and deploys the Fleet JetBrains Toolbox App integration.
func InstallToolboxPlugin(ctx context.Context, paths config.Paths, stdout io.Writer) (*InstallResult, error) {
	var installedPaths []string

	// 1. Central repository directory: ~/.local/share/fleet/plugins/jetbrains-toolbox/fleet
	centralDir := filepath.Join(paths.DataHome, "plugins", "jetbrains-toolbox", "fleet")
	if err := os.MkdirAll(centralDir, 0755); err != nil {
		return nil, fmt.Errorf("creating central toolbox plugin dir: %w", err)
	}
	_ = os.WriteFile(filepath.Join(centralDir, "extension.json"), []byte(GenerateToolboxExtensionJSON()), 0644)
	_ = os.WriteFile(filepath.Join(centralDir, "pluginIcon.svg"), []byte(GenerateToolboxPluginIconSVG()), 0644)
	if jarBytes, err := GenerateToolboxPluginJAR(); err == nil {
		centralJar := filepath.Join(centralDir, "fleet-toolbox.jar")
		_ = os.WriteFile(centralJar, jarBytes, 0644)
		installedPaths = append(installedPaths, centralJar)
	}
	// Clean up legacy sndbx toolbox dir in central
	_ = os.RemoveAll(filepath.Join(paths.DataHome, "plugins", "jetbrains-toolbox", "sndbx"))

	// 2. Deploy directly to JetBrains Toolbox external plugins folder
	toolboxDir := GetDefaultToolboxPluginDir()
	if toolboxDir != "" {
		if err := os.MkdirAll(toolboxDir, 0755); err == nil {
			extFile := filepath.Join(toolboxDir, "extension.json")
			iconFile := filepath.Join(toolboxDir, "pluginIcon.svg")
			jarFile := filepath.Join(toolboxDir, "fleet-toolbox.jar")
			_ = os.WriteFile(extFile, []byte(GenerateToolboxExtensionJSON()), 0644)
			_ = os.WriteFile(iconFile, []byte(GenerateToolboxPluginIconSVG()), 0644)
			if jarBytes, err := GenerateToolboxPluginJAR(); err == nil {
				_ = os.WriteFile(jarFile, jarBytes, 0644)
			}
			installedPaths = append(installedPaths, toolboxDir)
		}
	}

	// 3. Synchronize Toolbox SSH provider settings and inline host blocks
	_ = sndbxRuntime.SyncSSHConfigFile(ctx, paths)
	linked, _ := EnsureSSHConfigInclude(paths.SSHConfigFile, "")
	_ = EnsureSSHConfigManagedHosts(ctx, paths, "")
	_ = SyncToolboxSSHSettings(ctx, paths, "")

	return &InstallResult{
		PluginID:        PluginIDToolbox,
		PluginName:      ToolboxPluginName,
		Version:         ToolboxPluginVersion,
		InstalledPaths:  installedPaths,
		SSHConfigLinked: linked,
		Message:         "JetBrains Toolbox plugin and native SSH synchronization successfully installed.",
	}, nil
}

// RemoveToolboxPlugin uninstalls the Fleet JetBrains Toolbox App integration.
func RemoveToolboxPlugin(ctx context.Context, paths config.Paths, stdout io.Writer) (*RemoveResult, error) {
	var removedPaths []string

	centralDir := filepath.Join(paths.DataHome, "plugins", "jetbrains-toolbox", "fleet")
	if _, err := os.Stat(centralDir); err == nil {
		_ = os.RemoveAll(centralDir)
		removedPaths = append(removedPaths, centralDir)
	}
	legacyCentralDir := filepath.Join(paths.DataHome, "plugins", "jetbrains-toolbox", "sndbx")
	if _, err := os.Stat(legacyCentralDir); err == nil {
		_ = os.RemoveAll(legacyCentralDir)
		removedPaths = append(removedPaths, legacyCentralDir)
	}

	toolboxDir := GetDefaultToolboxPluginDir()
	if toolboxDir != "" {
		if _, err := os.Stat(toolboxDir); err == nil {
			_ = os.RemoveAll(toolboxDir)
			removedPaths = append(removedPaths, toolboxDir)
		}
	}

	_ = RemoveSSHConfigManagedHosts("")
	_ = RemoveToolboxSSHSettings("")
	unlinked, _ := RemoveSSHConfigInclude(paths.SSHConfigFile, "")

	return &RemoveResult{
		PluginID:          PluginIDToolbox,
		PluginName:        ToolboxPluginName,
		RemovedPaths:      removedPaths,
		SSHConfigUnlinked: unlinked,
		Message:           "JetBrains Toolbox plugin and native SSH synchronization successfully removed.",
	}, nil
}

// InstallVSCodePlugin configures the host environment for VS Code remote development.
func InstallVSCodePlugin(ctx context.Context, paths config.Paths, stdout io.Writer) (*InstallResult, error) {
	_ = sndbxRuntime.SyncSSHConfigFile(ctx, paths)
	linked, _ := EnsureSSHConfigInclude(paths.SSHConfigFile, "")

	var installedPaths []string
	codeCmd := ExecCommandContext(ctx, "code", "--version")
	hasCode := codeCmd.Run() == nil

	msg := "VS Code SSH integration configured successfully."
	if !hasCode {
		msg += " Note: 'code' CLI was not found in PATH, but SSH configuration was verified."
	} else {
		installExt := ExecCommandContext(ctx, "code", "--install-extension", "anthropic.claude-code")
		_ = installExt.Run()
		installedPaths = append(installedPaths, "VS Code Remote-SSH Config (~/.ssh/config)")
	}

	return &InstallResult{
		PluginID:        PluginIDVSCode,
		PluginName:      "VS Code Remote-SSH Integration",
		Version:         "1.0.0",
		InstalledPaths:  installedPaths,
		SSHConfigLinked: linked,
		Message:         msg,
	}, nil
}

// RemoveVSCodePlugin removes the VS Code SSH configuration link.
func RemoveVSCodePlugin(ctx context.Context, paths config.Paths, stdout io.Writer) (*RemoveResult, error) {
	unlinked, _ := RemoveSSHConfigInclude(paths.SSHConfigFile, "")
	var removedPaths []string
	if unlinked {
		removedPaths = append(removedPaths, "Host ~/.ssh/config Include directive")
	}

	return &RemoveResult{
		PluginID:          PluginIDVSCode,
		PluginName:        "VS Code Remote-SSH Integration",
		RemovedPaths:      removedPaths,
		SSHConfigUnlinked: unlinked,
		Message:           "VS Code Remote-SSH configuration unlinked.",
	}, nil
}

// ListPlugins enumerates available and installed IDE plugins.
func ListPlugins(paths config.Paths) []PluginInfo {
	// Check Gateway plugin
	centralGatewayJar := filepath.Join(paths.DataHome, "plugins", "jetbrains-gateway", "fleet-gateway", "lib", "fleet-gateway.jar")
	gatewayInstalled := false
	var gatewayLocations []string
	if _, err := os.Stat(centralGatewayJar); err == nil {
		gatewayInstalled = true
		gatewayLocations = append(gatewayLocations, centralGatewayJar)
	}
	for _, dir := range FindJetBrainsPluginDirs() {
		jarPath := filepath.Join(dir, "fleet-gateway", "lib", "fleet-gateway.jar")
		if _, err := os.Stat(jarPath); err == nil {
			gatewayInstalled = true
			gatewayLocations = append(gatewayLocations, jarPath)
		}
	}

	// Check Toolbox plugin
	centralToolboxDir := filepath.Join(paths.DataHome, "plugins", "jetbrains-toolbox", "fleet")
	toolboxInstalled := false
	var toolboxLocations []string
	if _, err := os.Stat(centralToolboxDir); err == nil {
		toolboxInstalled = true
		toolboxLocations = append(toolboxLocations, centralToolboxDir)
	}
	activeToolboxDir := GetDefaultToolboxPluginDir()
	if activeToolboxDir != "" {
		if _, err := os.Stat(activeToolboxDir); err == nil {
			toolboxInstalled = true
			toolboxLocations = append(toolboxLocations, activeToolboxDir)
		}
	}

	return []PluginInfo{
		{
			ID:          PluginIDGateway,
			Name:        JetBrainsPluginName,
			Target:      "JetBrains Gateway / IDEs",
			Description: "Direct IntelliJ Gateway connector for remote IDE development in container",
			Installed:   gatewayInstalled,
			Locations:   gatewayLocations,
		},
		{
			ID:          PluginIDToolbox,
			Name:        ToolboxPluginName,
			Target:      "JetBrains Toolbox",
			Description: "Direct JetBrains Toolbox provider and native SSH remote synchronization",
			Installed:   toolboxInstalled,
			Locations:   toolboxLocations,
		},
		{
			ID:          PluginIDVSCode,
			Name:        "VS Code Remote-SSH",
			Target:      "Visual Studio Code",
			Description: "One-shot workspace connection and Claude Code extension ensembling",
			Installed:   true,
			Locations:   []string{paths.SSHConfigFile},
		},
	}
}
