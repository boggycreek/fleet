// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package ide_test

import (
	"testing"

	"github.com/boggycreek/fleet/cli/pkg/ide"
)

// withFakeLookPath replaces the package-level lookPath function with a fake that
// returns success only for the given set of binary names. Returns a restore func.
func withFakeLookPath(installed map[string]string) func() {
	return ide.SetLookPathForTesting(func(name string) (string, error) {
		if path, ok := installed[name]; ok {
			return path, nil
		}
		return "", &notFoundError{name}
	})
}

type notFoundError struct{ name string }

func (e *notFoundError) Error() string { return "not found: " + e.name }

// --- Discover ---

func TestDiscover_OnlyInstalledReturned(t *testing.T) {
	restore := withFakeLookPath(map[string]string{
		"code":    "/usr/bin/code",
		"goland":  "/home/user/.local/share/JetBrains/Toolbox/scripts/goland",
		"pycharm": "/home/user/.local/share/JetBrains/Toolbox/scripts/pycharm",
	})
	defer restore()

	got := ide.Discover()
	if len(got) != 3 {
		t.Fatalf("expected 3 IDEs, got %d: %v", len(got), got)
	}
	names := map[string]bool{}
	for _, i := range got {
		names[i.Name] = true
	}
	for _, want := range []string{"code", "goland", "pycharm"} {
		if !names[want] {
			t.Errorf("expected %q in discovered IDEs, got: %v", want, got)
		}
	}
}

func TestDiscover_EmptyWhenNoneInstalled(t *testing.T) {
	restore := withFakeLookPath(map[string]string{})
	defer restore()

	got := ide.Discover()
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d IDEs", len(got))
	}
}

func TestDiscover_PathPopulated(t *testing.T) {
	restore := withFakeLookPath(map[string]string{
		"cursor": "/usr/local/bin/cursor",
	})
	defer restore()

	got := ide.Discover()
	if len(got) != 1 {
		t.Fatalf("expected 1 IDE, got %d", len(got))
	}
	if got[0].Path != "/usr/local/bin/cursor" {
		t.Errorf("expected path /usr/local/bin/cursor, got %q", got[0].Path)
	}
}

// --- Lookup ---

func TestLookup_InstalledIDE(t *testing.T) {
	restore := withFakeLookPath(map[string]string{
		"goland": "/home/user/.local/share/JetBrains/Toolbox/scripts/goland",
	})
	defer restore()

	found, installed := ide.Lookup("goland")
	if !installed {
		t.Fatal("expected goland to be reported as installed")
	}
	if found.Name != "goland" {
		t.Errorf("expected name goland, got %q", found.Name)
	}
	if found.Family != ide.FamilyJetBrains {
		t.Errorf("expected FamilyJetBrains, got %v", found.Family)
	}
}

func TestLookup_KnownButNotInstalled(t *testing.T) {
	restore := withFakeLookPath(map[string]string{})
	defer restore()

	found, installed := ide.Lookup("webstorm")
	if installed {
		t.Fatal("expected webstorm to be reported as not installed")
	}
	if found.Name != "webstorm" {
		t.Errorf("expected name webstorm, got %q", found.Name)
	}
}

func TestLookup_Unknown(t *testing.T) {
	restore := withFakeLookPath(map[string]string{})
	defer restore()

	_, installed := ide.Lookup("notanide")
	if installed {
		t.Fatal("expected notanide to not be found at all")
	}
}

func TestLookup_CaseInsensitive(t *testing.T) {
	restore := withFakeLookPath(map[string]string{
		"code": "/usr/bin/code",
	})
	defer restore()

	_, installed := ide.Lookup("CODE")
	if !installed {
		t.Error("expected case-insensitive match for CODE")
	}

	_, installed = ide.Lookup("  Code  ")
	if !installed {
		t.Error("expected trimmed whitespace match for '  Code  '")
	}
}

// --- NamesInstalled ---

func TestNamesInstalled(t *testing.T) {
	restore := withFakeLookPath(map[string]string{
		"code":     "/usr/bin/code",
		"webstorm": "/home/user/.local/share/JetBrains/Toolbox/scripts/webstorm",
	})
	defer restore()

	names := ide.NamesInstalled()
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d: %v", len(names), names)
	}
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["code"] {
		t.Error("expected code in NamesInstalled")
	}
	if !found["webstorm"] {
		t.Error("expected webstorm in NamesInstalled")
	}
}

// --- Family assignment correctness ---

func TestFamilyAssignment(t *testing.T) {
	vsCodeBinaries := []string{"code", "code-insiders", "cursor", "windsurf"}
	jetbrainsBinaries := []string{"goland", "clion", "webstorm", "pycharm", "idea", "phpstorm", "rider", "rubymine", "datagrip", "dataspell", "fleet", "aqua", "writerside", "mps"}

	fakeInstalled := map[string]string{}
	for _, b := range append(vsCodeBinaries, jetbrainsBinaries...) {
		fakeInstalled[b] = "/fake/" + b
	}
	restore := withFakeLookPath(fakeInstalled)
	defer restore()

	discovered := ide.Discover()
	families := map[string]ide.Family{}
	for _, i := range discovered {
		families[i.Name] = i.Family
	}

	for _, name := range vsCodeBinaries {
		if f, ok := families[name]; !ok || f != ide.FamilyVSCode {
			t.Errorf("%q: expected FamilyVSCode, got %v (ok=%v)", name, f, ok)
		}
	}
	for _, name := range jetbrainsBinaries {
		if f, ok := families[name]; !ok || f != ide.FamilyJetBrains {
			t.Errorf("%q: expected FamilyJetBrains, got %v (ok=%v)", name, f, ok)
		}
	}
}

func TestSSHHostTarget(t *testing.T) {
	if got := ide.SSHHostTarget("myagent"); got != "fleet-myagent" {
		t.Errorf("expected fleet-myagent, got %s", got)
	}
}
