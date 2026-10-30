// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathsEnvironmentResolution(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Custom XDG_DATA_HOME
	t.Setenv("XDG_DATA_HOME", filepath.Join(tmpDir, "custom-data"))
	paths := GetPaths()
	expectedData := filepath.Join(tmpDir, "custom-data", "fleet")
	if paths.DataHome != expectedData {
		t.Errorf("expected DataHome %q, got %q", expectedData, paths.DataHome)
	}
	if paths.SSHConfigFile != filepath.Join(expectedData, "ssh_config") {
		t.Errorf("expected SSHConfigFile %q, got %q", filepath.Join(expectedData, "ssh_config"), paths.SSHConfigFile)
	}
	if !strings.HasSuffix(paths.SSHPrivateKey, filepath.Join(".ssh", "fleet")) {
		t.Errorf("expected SSHPrivateKey to end with .ssh/fleet, got %q", paths.SSHPrivateKey)
	}
	if paths.GetSSHPrivateKey() != paths.SSHPrivateKey {
		t.Errorf("expected GetSSHPrivateKey to return SSHPrivateKey")
	}

	// 2. Unset XDG_DATA_HOME uses ~/.local/share/fleet
	os.Unsetenv("XDG_DATA_HOME")
	defaultPaths := GetPaths()
	if !strings.Contains(defaultPaths.DataHome, ".local/share/fleet") && !strings.Contains(defaultPaths.DataHome, "fleet") {
		t.Errorf("unexpected default DataHome: %s", defaultPaths.DataHome)
	}
}

func TestPathsLoadEnv(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	envContent := strings.Join([]string{
		"# Comment line to ignore",
		"",
		"TEST_PATHS_KEY1=alpha",
		"TEST_PATHS_KEY2 = beta",
		"INVALID_LINE_WITHOUT_EQUALS",
		"# Another comment",
		"TEST_PATHS_EMPTY=",
	}, "\n")

	if err := os.WriteFile(envPath, []byte(envContent), 0600); err != nil {
		t.Fatalf("failed to write mock .env: %v", err)
	}

	p := Paths{EnvFile: envPath}
	t.Cleanup(func() {
		_ = os.Unsetenv("TEST_PATHS_KEY1")
		_ = os.Unsetenv("TEST_PATHS_KEY2")
	})
	p.LoadEnv()

	if val := os.Getenv("TEST_PATHS_KEY1"); val != "alpha" {
		t.Errorf("expected TEST_PATHS_KEY1='alpha', got %q", val)
	}
	if val := os.Getenv("TEST_PATHS_KEY2"); val != "beta" {
		t.Errorf("expected TEST_PATHS_KEY2='beta', got %q", val)
	}

	// Non-existent env file should not panic or error
	pMissing := Paths{EnvFile: filepath.Join(tmpDir, "nonexistent.env")}
	pMissing.LoadEnv()
}

func TestPathsResolveRepoDir(t *testing.T) {
	tmpDir := t.TempDir()

	// 1a. FLEET_REPO env override
	repo1a := filepath.Join(tmpDir, "repo-fleet-env")
	if err := os.MkdirAll(repo1a, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo1a, "Makefile"), []byte("# test"), 0644); err != nil {
		t.Fatalf("write Makefile failed: %v", err)
	}

	t.Setenv("FLEET_REPO", repo1a)
	p := Paths{DataHome: filepath.Join(tmpDir, "data")}
	if r := p.ResolveRepoDir(); r != repo1a {
		t.Errorf("expected %q from FLEET_REPO, got %q", repo1a, r)
	}

	// 1b. AGENT_SANDBOX_REPO env fallback
	os.Unsetenv("FLEET_REPO")
	repo1 := filepath.Join(tmpDir, "repo-env")
	if err := os.MkdirAll(repo1, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo1, "Makefile"), []byte("# test"), 0644); err != nil {
		t.Fatalf("write Makefile failed: %v", err)
	}

	t.Setenv("AGENT_SANDBOX_REPO", repo1)
	if r := p.ResolveRepoDir(); r != repo1 {
		t.Errorf("expected %q from AGENT_SANDBOX_REPO, got %q", repo1, r)
	}

	// 2a. FLEET_ROOT env override
	os.Unsetenv("AGENT_SANDBOX_REPO")
	repo2a := filepath.Join(tmpDir, "repo-fleet-root")
	if err := os.MkdirAll(repo2a, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo2a, "Makefile"), []byte("# test"), 0644); err != nil {
		t.Fatalf("write Makefile failed: %v", err)
	}

	t.Setenv("FLEET_ROOT", repo2a)
	if r := p.ResolveRepoDir(); r != repo2a {
		t.Errorf("expected %q from FLEET_ROOT, got %q", repo2a, r)
	}

	// 2b. SANDBOX_ROOT env fallback
	os.Unsetenv("FLEET_ROOT")
	repo2 := filepath.Join(tmpDir, "repo-root")
	if err := os.MkdirAll(repo2, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo2, "Makefile"), []byte("# test"), 0644); err != nil {
		t.Fatalf("write Makefile failed: %v", err)
	}

	t.Setenv("SANDBOX_ROOT", repo2)
	if r := p.ResolveRepoDir(); r != repo2 {
		t.Errorf("expected %q from SANDBOX_ROOT, got %q", repo2, r)
	}

	// 3. Standard fallback under DataHome/repo
	os.Unsetenv("SANDBOX_ROOT")
	standardRepo := filepath.Join(p.DataHome, "repo")
	if err := os.MkdirAll(standardRepo, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(standardRepo, "Makefile"), []byte("# test"), 0644); err != nil {
		t.Fatalf("write Makefile failed: %v", err)
	}
	if r := p.ResolveRepoDir(); r != standardRepo {
		if _, err := os.Stat(filepath.Join(r, "go.work")); err != nil {
			if _, err2 := os.Stat(filepath.Join(r, "Makefile")); err2 != nil {
				t.Errorf("expected standard repo or workspace root with go.work/Makefile, got %q", r)
			}
		}
	}

	// 4. Resolve from deep child subdirectory of isolated mock directory hierarchy
	mockRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(mockRoot, "Makefile"), []byte("# mock"), 0644); err != nil {
		t.Fatalf("write mock Makefile failed: %v", err)
	}
	childDir := filepath.Join(mockRoot, "deep", "nested", "sub")
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	origWd, err := os.Getwd()
	if err == nil {
		t.Cleanup(func() { _ = os.Chdir(origWd) })
		if err := os.Chdir(childDir); err == nil {
			pEmpty := Paths{}
			resolved := pEmpty.ResolveRepoDir()
			_ = os.Chdir(origWd)
			if resolved != mockRoot {
				t.Errorf("expected upward resolution to %q, got %q", mockRoot, resolved)
			}
		}
	}
}

func TestPathsEnsureDirectoriesErrors(t *testing.T) {
	tmpDir := t.TempDir()
	// Block directory creation by creating a file with the same name
	blockedPath := filepath.Join(tmpDir, "blocked_dir")
	if err := os.WriteFile(blockedPath, []byte("file blocker"), 0600); err != nil {
		t.Fatalf("failed to write blocker file: %v", err)
	}

	p := Paths{
		DataHome: blockedPath,
	}
	if err := p.EnsureDirectories(); err == nil {
		t.Errorf("expected EnsureDirectories to fail when path is a regular file")
	}
}

func TestFormatAndGetSonarAdminPassword(t *testing.T) {
	if got := FormatSonarAdminPassword(""); got != "Admin_backplane_pass" {
		t.Errorf("expected default password, got %s", got)
	}
	if got := FormatSonarAdminPassword("alllowercase123"); got != "Admin_alllowercase123" {
		t.Errorf("expected prepended Admin_, got %s", got)
	}
	if got := FormatSonarAdminPassword("AlreadyHasUpper"); got != "AlreadyHasUpper" {
		t.Errorf("expected unchanged, got %s", got)
	}

	t.Setenv("SONAR_ADMIN_PASSWORD", "customUpper123")
	t.Setenv("ADMIN_BACKPLANE_PASSWORD", "fallbacklower")
	if got := GetSonarAdminPassword(); got != "customUpper123" {
		t.Errorf("expected customUpper123, got %s", got)
	}

	t.Setenv("SONAR_ADMIN_PASSWORD", "")
	if got := GetSonarAdminPassword(); got != "Admin_fallbacklower" {
		t.Errorf("expected Admin_fallbacklower, got %s", got)
	}

	t.Setenv("ADMIN_BACKPLANE_PASSWORD", "")
	if got := GetSonarAdminPassword(); got != "Admin_backplane_pass" {
		t.Errorf("expected Admin_backplane_pass, got %s", got)
	}
}

func TestPathsSetEnvVar(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, "test.env")
	p := Paths{EnvFile: envPath}

	t.Cleanup(func() {
		_ = os.Unsetenv("KEY1")
		_ = os.Unsetenv("KEY2")
	})

	checkPerms := func(expected os.FileMode) {
		info, err := os.Stat(envPath)
		if err != nil {
			t.Fatalf("failed to stat env file: %v", err)
		}
		if info.Mode().Perm() != expected {
			t.Errorf("expected file mode %v, got %v", expected, info.Mode().Perm())
		}
	}

	if err := p.SetEnvVar("KEY1", "val1"); err != nil {
		t.Fatalf("SetEnvVar failed: %v", err)
	}
	if os.Getenv("KEY1") != "val1" {
		t.Errorf("expected env var set, got %s", os.Getenv("KEY1"))
	}
	checkPerms(0600)

	// Update existing
	if err := p.SetEnvVar("KEY1", "newval"); err != nil {
		t.Fatalf("SetEnvVar update failed: %v", err)
	}
	if os.Getenv("KEY1") != "newval" {
		t.Errorf("expected updated env var, got %s", os.Getenv("KEY1"))
	}
	checkPerms(0600)

	// Append second
	if err := p.SetEnvVar("KEY2", "val2"); err != nil {
		t.Fatalf("SetEnvVar second append failed: %v", err)
	}
	checkPerms(0600)

	content, _ := os.ReadFile(envPath)
	contentStr := string(content)
	if !strings.Contains(contentStr, "KEY1=newval") || !strings.Contains(contentStr, "KEY2=val2") {
		t.Errorf("unexpected env content:\n%s", contentStr)
	}
}

func TestGetDefaultProjectOrg(t *testing.T) {
	t.Setenv("DEFAULT_PROJECT_ORG", "")
	if got := GetDefaultProjectOrg(); got != "factory" {
		t.Errorf("expected factory by default, got %s", got)
	}

	t.Setenv("DEFAULT_PROJECT_ORG", "custom-org")
	if got := GetDefaultProjectOrg(); got != "custom-org" {
		t.Errorf("expected custom-org, got %s", got)
	}
}

func TestPathsProjectsDir(t *testing.T) {
	p := GetPaths()
	if !strings.HasSuffix(p.ProjectsDir, filepath.Join("fleet", "projects")) {
		t.Errorf("expected ProjectsDir to end with fleet/projects, got %s", p.ProjectsDir)
	}
}

func TestPathsFallbackAndHome(t *testing.T) {
	// 1. GetSSHPrivateKey returns IDEKeyFile when SSHPrivateKey is empty
	p := Paths{IDEKeyFile: "/tmp/ide_key"}
	if key := p.GetSSHPrivateKey(); key != "/tmp/ide_key" {
		t.Errorf("expected IDEKeyFile fallback, got %q", key)
	}

	// 2. GetPaths with HOME unset uses /tmp
	t.Setenv("HOME", "")
	paths := GetPaths()
	if !strings.HasPrefix(paths.DataHome, "/tmp") && !strings.Contains(paths.DataHome, "fleet") {
		t.Errorf("expected /tmp fallback when HOME is unset, got %+v", paths)
	}
}

func TestPathsSetEnvVar_Branches(t *testing.T) {
	tmpDir := t.TempDir()

	t.Cleanup(func() {
		_ = os.Unsetenv("KEY2")
	})

	// 1. EnvFile is a directory -> ReadFile returns error
	dirEnv := Paths{EnvFile: tmpDir}
	if err := dirEnv.SetEnvVar("KEY", "VAL"); err == nil {
		t.Errorf("expected error when EnvFile is a directory")
	}

	// 2. Existing file without trailing newline
	envFile := filepath.Join(tmpDir, "no_trailing.env")
	if err := os.WriteFile(envFile, []byte("KEY1=val1"), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	p := Paths{EnvFile: envFile}
	if err := p.SetEnvVar("KEY2", "val2"); err != nil {
		t.Fatalf("SetEnvVar failed: %v", err)
	}
	data, _ := os.ReadFile(envFile)
	if !strings.Contains(string(data), "KEY2=val2") {
		t.Errorf("unexpected content: %q", string(data))
	}

	// 3. EnvFile parent directory is not writable -> CreateTemp fails
	roDir := filepath.Join(tmpDir, "ro_dir")
	if err := os.MkdirAll(roDir, 0500); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	defer func() { _ = os.Chmod(roDir, 0700) }()
	failCreate := Paths{EnvFile: filepath.Join(roDir, "test.env")}
	if err := failCreate.SetEnvVar("KEY", "VAL"); err == nil {
		t.Errorf("expected error when directory is not writable")
	}
}

func TestPathsResolveRepoDir_EdgeCases(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	// Clear repo env vars
	t.Setenv("FLEET_REPO", "")
	t.Setenv("AGENT_SANDBOX_REPO", "")
	t.Setenv("FLEET_ROOT", "")
	t.Setenv("SANDBOX_ROOT", "")

	tmpDir := t.TempDir()

	// 1. Current working directory has Makefile
	cwdWithMakefile := filepath.Join(tmpDir, "cwd_with_makefile")
	_ = os.MkdirAll(cwdWithMakefile, 0755)
	_ = os.WriteFile(filepath.Join(cwdWithMakefile, "Makefile"), []byte("all:"), 0644)
	if err := os.Chdir(cwdWithMakefile); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	p := Paths{DataHome: filepath.Join(tmpDir, "data")}
	repo := p.ResolveRepoDir()
	_ = os.Chdir(origDir)
	if repo != cwdWithMakefile {
		t.Errorf("expected %q, got %q", cwdWithMakefile, repo)
	}

	// 2. No Makefile in cwd or any parent directory (in /tmp)
	emptySubdir := filepath.Join(tmpDir, "empty_sub", "deep")
	_ = os.MkdirAll(emptySubdir, 0755)
	if err := os.Chdir(emptySubdir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	repoFallback := p.ResolveRepoDir()
	_ = os.Chdir(origDir)
	if repoFallback != emptySubdir {
		t.Errorf("expected fallback to %q, got %q", emptySubdir, repoFallback)
	}
}
