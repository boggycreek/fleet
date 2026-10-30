// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Paths holds XDG-compliant filesystem paths for the sandbox
type Paths struct {
	DataHome      string
	StateHome     string
	AgentsDir     string
	ProjectsDir   string
	SecretsDir    string
	BinDir        string
	EnvFile       string
	SSHDir        string
	IDEKeyFile    string
	SSHPrivateKey string
	SSHConfigFile string
	BPProfilesDir string
}

// GetSSHPrivateKey returns SSHPrivateKey if set, or IDEKeyFile as fallback.
func (p Paths) GetSSHPrivateKey() string {
	if p.SSHPrivateKey != "" {
		return p.SSHPrivateKey
	}
	return p.IDEKeyFile
}

// GetPaths returns standard paths resolved against XDG environment variables
func GetPaths() Paths {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/tmp"
	}

	xdgData := os.Getenv("XDG_DATA_HOME")
	if xdgData == "" {
		xdgData = filepath.Join(home, ".local", "share")
	}

	xdgState := os.Getenv("XDG_STATE_HOME")
	if xdgState == "" {
		xdgState = filepath.Join(home, ".local", "state")
	}

	dataHome := filepath.Join(xdgData, "fleet")
	sshDir := filepath.Join(home, ".ssh")
	bpProfilesDir := filepath.Join(xdgState, "bp", "profiles")
	sshPrivateKey := filepath.Join(sshDir, "fleet")

	return Paths{
		DataHome:      dataHome,
		StateHome:     xdgState,
		AgentsDir:     filepath.Join(dataHome, "agents"),
		ProjectsDir:   filepath.Join(dataHome, "projects"),
		SecretsDir:    filepath.Join(dataHome, "secrets"),
		BinDir:        filepath.Join(home, ".local", "bin"),
		EnvFile:       filepath.Join(dataHome, ".env"),
		SSHDir:        sshDir,
		IDEKeyFile:    sshPrivateKey,
		SSHPrivateKey: sshPrivateKey,
		SSHConfigFile: filepath.Join(dataHome, "ssh_config"),
		BPProfilesDir: bpProfilesDir,
	}
}

// EnsureDirectories creates required data and secrets directories
func (p Paths) EnsureDirectories() error {
	for _, dir := range []string{p.DataHome, p.AgentsDir, p.ProjectsDir, p.SecretsDir, p.BinDir, p.SSHDir, p.BPProfilesDir} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	return nil
}

// LoadEnv loads environment variables from .env if present and not already set
func (p Paths) LoadEnv() {
	data, err := os.ReadFile(p.EnvFile)
	if err != nil {
		return
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			if os.Getenv(k) == "" && v != "" {
				_ = os.Setenv(k, v)
			}
		}
	}
}

// FormatSonarAdminPassword returns a password complying with SonarQube's policy (at least 1 uppercase letter).
func FormatSonarAdminPassword(pass string) string {
	pass = strings.TrimSpace(pass)
	if pass == "" {
		return "Admin_backplane_pass"
	}
	hasUpper := false
	for _, r := range pass {
		if r >= 'A' && r <= 'Z' {
			hasUpper = true
			break
		}
	}
	if !hasUpper {
		return "Admin_" + pass
	}
	return pass
}

// GetSonarAdminPassword retrieves the configured SonarQube admin password from environment,
// falling back to formatted ADMIN_BACKPLANE_PASSWORD.
func GetSonarAdminPassword() string {
	if p := os.Getenv("SONAR_ADMIN_PASSWORD"); p != "" {
		return FormatSonarAdminPassword(p)
	}
	if p := os.Getenv("ADMIN_BACKPLANE_PASSWORD"); p != "" {
		return FormatSonarAdminPassword(p)
	}
	return "Admin_backplane_pass"
}

// GetDefaultProjectOrg returns the configured default project organization ("factory" by default).
func GetDefaultProjectOrg() string {
	if org := strings.TrimSpace(os.Getenv("DEFAULT_PROJECT_ORG")); org != "" {
		return org
	}
	return "factory"
}

// SetEnvVar updates or appends a key=value pair to the .env file.
func (p Paths) SetEnvVar(key, value string) error {
	data, err := os.ReadFile(p.EnvFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	lines := strings.Split(string(data), "\n")
	found := false
	var newLines []string
	prefix := key + "="
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			newLines = append(newLines, fmt.Sprintf("%s=%s", key, value))
			found = true
		} else {
			newLines = append(newLines, line)
		}
	}
	if !found {
		if len(newLines) > 0 && strings.TrimSpace(newLines[len(newLines)-1]) != "" {
			newLines = append(newLines, "")
		}
		newLines = append(newLines, fmt.Sprintf("%s=%s", key, value))
	}

	content := strings.Join(newLines, "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	_ = os.MkdirAll(filepath.Dir(p.EnvFile), 0700)

	tmpFile, err := os.CreateTemp(filepath.Dir(p.EnvFile), ".env.*")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()

	// Clean up temporary file if it still exists (e.g. rename failed or error occurred)
	defer os.Remove(tmpName)

	if _, err := tmpFile.Write([]byte(content)); err != nil {
		tmpFile.Close()
		return err
	}

	if err := tmpFile.Chmod(0600); err != nil {
		tmpFile.Close()
		return err
	}

	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return err
	}

	if err := tmpFile.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpName, p.EnvFile); err != nil {
		return err
	}

	_ = os.Setenv(key, value)
	return nil
}

// ResolveRepoDir returns the absolute path to the local repository checkout.
// It checks FLEET_REPO, AGENT_SANDBOX_REPO, FLEET_ROOT, SANDBOX_ROOT, the parent of the binary,
// the current working directory, and the standard ~/.local/share/fleet/repo fallback.
func (p Paths) ResolveRepoDir() string {
	// 1. Explicit environment variable override
	if repo := os.Getenv("FLEET_REPO"); repo != "" {
		if fi, err := os.Stat(filepath.Join(repo, "Makefile")); err == nil && !fi.IsDir() {
			return repo
		}
	}
	if repo := os.Getenv("AGENT_SANDBOX_REPO"); repo != "" {
		if fi, err := os.Stat(filepath.Join(repo, "Makefile")); err == nil && !fi.IsDir() {
			return repo
		}
	}
	if repo := os.Getenv("FLEET_ROOT"); repo != "" {
		if fi, err := os.Stat(filepath.Join(repo, "Makefile")); err == nil && !fi.IsDir() {
			return repo
		}
	}
	if repo := os.Getenv("SANDBOX_ROOT"); repo != "" {
		if fi, err := os.Stat(filepath.Join(repo, "Makefile")); err == nil && !fi.IsDir() {
			return repo
		}
	}

	// 2. Current working directory
	if cwd, err := os.Getwd(); err == nil {
		if fi, err := os.Stat(filepath.Join(cwd, "Makefile")); err == nil && !fi.IsDir() {
			return cwd
		}
	}

	// 3. Relative to the executing binary (e.g. repo/bin/fleet -> repo)
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		parent := filepath.Dir(exeDir)
		if fi, err := os.Stat(filepath.Join(parent, "Makefile")); err == nil && !fi.IsDir() {
			return parent
		}
	}

	// 4. Default standard XDG repository location
	standardRepo := filepath.Join(p.DataHome, "repo")
	if fi, err := os.Stat(filepath.Join(standardRepo, "Makefile")); err == nil && !fi.IsDir() {
		return standardRepo
	}

	// 5. Parent directories of current working directory
	if cwd, err := os.Getwd(); err == nil {
		dir := cwd
		for {
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
			if fi, err := os.Stat(filepath.Join(dir, "Makefile")); err == nil && !fi.IsDir() {
				return dir
			}
		}
	}

	// Fallback to current working directory if none found
	cwd, _ := os.Getwd()
	return cwd
}
