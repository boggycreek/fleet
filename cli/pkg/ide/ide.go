// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package ide provides dynamic discovery of locally installed IDE launchers that
// support SSH remote development connections (VS Code family and JetBrains family).
package ide

import (
	"fmt"
	"os/exec"
	"strings"
)

// Family identifies the broad category of an IDE, which determines how remote
// connections are opened (URI scheme, CLI flags, etc.).
type Family int

const (
	// FamilyVSCode covers VS Code, VS Code Insiders, Cursor, and Windsurf — all
	// support the vscode-remote://ssh-remote+<host>/<path> URI scheme via --file-uri.
	FamilyVSCode Family = iota
	// FamilyJetBrains covers all IntelliJ-platform IDEs managed by JetBrains
	// Toolbox. Remote development connects via the prepared OpenSSH host alias.
	FamilyJetBrains
)

// IDE describes a discovered IDE launcher on the host machine.
type IDE struct {
	// Name is the canonical short name used in "fleet agent open <agent> in <name>".
	Name string
	// DisplayName is a human-readable label shown in help and error messages.
	DisplayName string
	// BinaryName is the executable name resolved by which(1).
	BinaryName string
	// Path is the absolute resolved path returned by exec.LookPath.
	Path string
	// Family determines how remote connections are opened.
	Family Family
}

// knownIDEs is the full catalogue of IDE launchers fleet knows about, ordered by
// family and then approximate popularity. Discovery is performed by resolving each
// BinaryName against the host PATH at runtime.
var knownIDEs = []IDE{
	// VS Code family — all use vscode-remote:// URI scheme
	{Name: "code", DisplayName: "VS Code", BinaryName: "code", Family: FamilyVSCode},
	{Name: "code-insiders", DisplayName: "VS Code Insiders", BinaryName: "code-insiders", Family: FamilyVSCode},
	{Name: "cursor", DisplayName: "Cursor", BinaryName: "cursor", Family: FamilyVSCode},
	{Name: "windsurf", DisplayName: "Windsurf", BinaryName: "windsurf", Family: FamilyVSCode},

	// JetBrains family — connects via SSH host alias fleet-<name>
	{Name: "goland", DisplayName: "GoLand", BinaryName: "goland", Family: FamilyJetBrains},
	{Name: "clion", DisplayName: "CLion", BinaryName: "clion", Family: FamilyJetBrains},
	{Name: "webstorm", DisplayName: "WebStorm", BinaryName: "webstorm", Family: FamilyJetBrains},
	{Name: "pycharm", DisplayName: "PyCharm", BinaryName: "pycharm", Family: FamilyJetBrains},
	{Name: "idea", DisplayName: "IntelliJ IDEA", BinaryName: "idea", Family: FamilyJetBrains},
	{Name: "phpstorm", DisplayName: "PhpStorm", BinaryName: "phpstorm", Family: FamilyJetBrains},
	{Name: "rider", DisplayName: "Rider", BinaryName: "rider", Family: FamilyJetBrains},
	{Name: "rubymine", DisplayName: "RubyMine", BinaryName: "rubymine", Family: FamilyJetBrains},
	{Name: "datagrip", DisplayName: "DataGrip", BinaryName: "datagrip", Family: FamilyJetBrains},
	{Name: "dataspell", DisplayName: "DataSpell", BinaryName: "dataspell", Family: FamilyJetBrains},
	{Name: "fleet", DisplayName: "Fleet", BinaryName: "fleet", Family: FamilyJetBrains},
	{Name: "aqua", DisplayName: "Aqua", BinaryName: "aqua", Family: FamilyJetBrains},
	{Name: "writerside", DisplayName: "Writerside", BinaryName: "writerside", Family: FamilyJetBrains},
	{Name: "mps", DisplayName: "MPS", BinaryName: "mps", Family: FamilyJetBrains},
}

// lookPath is the function used to resolve a binary name to an absolute path.
// It is a variable so tests can inject a fake resolver without touching the OS.
var lookPath = exec.LookPath

// SetLookPathForTesting replaces the package-level lookPath resolver with fn and
// returns a function that restores the original. For use in tests only.
func SetLookPathForTesting(fn func(string) (string, error)) func() {
	orig := lookPath
	lookPath = fn
	return func() { lookPath = orig }
}

// Discover returns the subset of knownIDEs whose BinaryName is resolvable on
// the host PATH. Results are ordered as in knownIDEs (VS Code family first).
func Discover() []IDE {
	var found []IDE
	for _, candidate := range knownIDEs {
		path, err := lookPath(candidate.BinaryName)
		if err == nil && path != "" {
			candidate.Path = path
			found = append(found, candidate)
		}
	}
	return found
}

// Lookup returns the IDE matching name (case-insensitive), searching first among
// discovered (installed) IDEs and then the full catalogue. The second return value
// reports whether the IDE is actually installed on the host.
func Lookup(name string) (IDE, bool) {
	lower := strings.ToLower(strings.TrimSpace(name))
	// First pass: only installed IDEs
	for _, candidate := range knownIDEs {
		if candidate.Name == lower {
			path, err := lookPath(candidate.BinaryName)
			if err == nil && path != "" {
				candidate.Path = path
				return candidate, true
			}
			// Known but not installed
			return candidate, false
		}
	}
	return IDE{}, false
}

// NamesInstalled returns a sorted slice of short names for every IDE currently
// installed on the host. Suitable for help text and error messages.
func NamesInstalled() []string {
	discovered := Discover()
	names := make([]string, 0, len(discovered))
	for _, ide := range discovered {
		names = append(names, ide.Name)
	}
	return names
}

// SSHHostTarget returns the OpenSSH host target alias for an agent (fleet-<name>).
func SSHHostTarget(agentName string) string {
	return fmt.Sprintf("fleet-%s", agentName)
}
