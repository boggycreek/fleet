// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package devcontainer

import (
	"fmt"
	"path/filepath"
	"strings"
)

// List of Linux capabilities deemed dangerous in dev containers.
var dangerousCapabilities = map[string]bool{
	"ALL":          true,
	"SYS_ADMIN":    true,
	"NET_ADMIN":    true,
	"SYS_PTRACE":   true,
	"SYS_RAWIO":    true,
	"SYS_MODULE":   true,
	"DAC_OVERRIDE": true,
	"SYS_BOOT":     true,
	"NET_RAW":      true,
	"SYS_CHROOT":   true,
}

// List of host paths considered dangerous for binds.
var prohibitedHostPaths = []string{
	"/var/run/docker.sock",
	"/var/run/podman/podman.sock",
	"/run/podman/podman.sock",
	"/run/docker.sock",
	"/",
	"/etc",
	"/root",
	"~/.ssh",
	"$HOME/.ssh",
}

// Audit inspects a DevContainerSpec and generates a DiagnosticReport of security findings.
func Audit(spec *DevContainerSpec) DiagnosticReport {
	report := DiagnosticReport{Diagnostics: make([]Diagnostic, 0)}

	if spec == nil {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Severity: SeverityCritical,
			Code:     "NIL_SPEC",
			Target:   "spec",
			Message:  "DevContainerSpec is nil",
		})
		return report
	}

	// 1. Check Privileged Mode
	if spec.Privileged != nil && *spec.Privileged {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Severity: SeverityCritical,
			Code:     "PRIVILEGED_MODE",
			Target:   "privileged",
			Message:  "Container is configured with privileged: true",
		})
	}

	for _, arg := range spec.RunArgs {
		if arg == "--privileged" {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Severity: SeverityCritical,
				Code:     "PRIVILEGED_RUN_ARG",
				Target:   "runArgs",
				Message:  "Container runArgs includes --privileged flag",
			})
		}
	}

	// 2. Check CapAdd capabilities
	for _, capName := range spec.CapAdd {
		normalized := normalizeCap(capName)
		if dangerousCapabilities[normalized] {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Severity: SeverityCritical,
				Code:     "DANGEROUS_CAPABILITY",
				Target:   "capAdd",
				Message:  fmt.Sprintf("Container requests dangerous Linux capability: %s", capName),
			})
		}
	}

	// Check capabilities in runArgs (--cap-add)
	for i, arg := range spec.RunArgs {
		var capVal string
		if strings.HasPrefix(arg, "--cap-add=") {
			capVal = strings.TrimPrefix(arg, "--cap-add=")
		} else if arg == "--cap-add" && i+1 < len(spec.RunArgs) {
			capVal = spec.RunArgs[i+1]
		}
		if capVal != "" {
			normalized := normalizeCap(capVal)
			if dangerousCapabilities[normalized] {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{
					Severity: SeverityCritical,
					Code:     "DANGEROUS_CAPABILITY",
					Target:   "runArgs",
					Message:  fmt.Sprintf("runArgs requests dangerous Linux capability via --cap-add: %s", capVal),
				})
			}
		}
	}

	// 3. Check Host Namespace Leakage in runArgs
	namespaceFlags := map[string]string{
		"--net=host":    "network namespace (host)",
		"--ipc=host":    "IPC namespace (host)",
		"--pid=host":    "PID namespace (host)",
		"--userns=host": "user namespace (host)",
	}

	for i, arg := range spec.RunArgs {
		if msg, ok := namespaceFlags[arg]; ok {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Severity: SeverityCritical,
				Code:     "HOST_NAMESPACE_LEAKAGE",
				Target:   "runArgs",
				Message:  fmt.Sprintf("runArgs shares host %s", msg),
			})
		}
		// Also handle space-separated args, e.g., --net host, --ipc host
		if (arg == "--net" || arg == "--ipc" || arg == "--pid" || arg == "--userns" || arg == "--network") && i+1 < len(spec.RunArgs) {
			if spec.RunArgs[i+1] == "host" {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{
					Severity: SeverityCritical,
					Code:     "HOST_NAMESPACE_LEAKAGE",
					Target:   "runArgs",
					Message:  fmt.Sprintf("runArgs shares host %s namespace (%s host)", arg, arg),
				})
			}
		}
	}

	// 4. Check Prohibited Host Mounts in mounts field
	for _, m := range spec.Mounts {
		src := extractMountSource(m)
		if isProhibitedHostPath(src) {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Severity: SeverityCritical,
				Code:     "PROHIBITED_HOST_BIND",
				Target:   "mounts",
				Message:  fmt.Sprintf("Mount binds sensitive host path: %s", src),
			})
		}
	}

	// Check Prohibited Host Mounts in runArgs (-v, --volume, --mount)
	for i, arg := range spec.RunArgs {
		var mountStr string
		if strings.HasPrefix(arg, "-v=") || strings.HasPrefix(arg, "--volume=") {
			parts := strings.SplitN(arg, "=", 2)
			mountStr = parts[1]
		} else if (arg == "-v" || arg == "--volume") && i+1 < len(spec.RunArgs) {
			mountStr = spec.RunArgs[i+1]
		} else if strings.HasPrefix(arg, "--mount=") {
			mountStr = strings.TrimPrefix(arg, "--mount=")
		} else if arg == "--mount" && i+1 < len(spec.RunArgs) {
			mountStr = spec.RunArgs[i+1]
		}

		if mountStr != "" {
			src := extractSourceFromVolumeOrMountStr(mountStr)
			if isProhibitedHostPath(src) {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{
					Severity: SeverityCritical,
					Code:     "PROHIBITED_HOST_BIND",
					Target:   "runArgs",
					Message:  fmt.Sprintf("runArgs binds sensitive host path: %s", src),
				})
			}
		}
	}

	// 5. Enforce Non-Root User Bounds
	if isRootUser(spec.ContainerUser) {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Severity: SeverityWarning,
			Code:     "ROOT_CONTAINER_USER",
			Target:   "containerUser",
			Message:  fmt.Sprintf("containerUser is set to root/0 or empty (%q)", spec.ContainerUser),
		})
	}
	if isRootUser(spec.RemoteUser) {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Severity: SeverityWarning,
			Code:     "ROOT_REMOTE_USER",
			Target:   "remoteUser",
			Message:  fmt.Sprintf("remoteUser is set to root/0 or empty (%q)", spec.RemoteUser),
		})
	}

	return report
}

func normalizeCap(c string) string {
	c = strings.ToUpper(strings.TrimSpace(c))
	c = strings.TrimPrefix(c, "CAP_")
	return c
}

func isRootUser(u string) bool {
	u = strings.TrimSpace(strings.ToLower(u))
	return u == "" || u == "root" || u == "0"
}

func extractMountSource(m any) string {
	switch v := m.(type) {
	case string:
		return extractSourceFromVolumeOrMountStr(v)
	case map[string]any:
		if src, ok := v["source"].(string); ok {
			return src
		}
		if src, ok := v["src"].(string); ok {
			return src
		}
	}
	return ""
}

func extractSourceFromVolumeOrMountStr(s string) string {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "source=") || strings.Contains(s, "src=") {
		parts := strings.Split(s, ",")
		for _, p := range parts {
			kv := strings.SplitN(p, "=", 2)
			if len(kv) == 2 && (kv[0] == "source" || kv[0] == "src") {
				return kv[1]
			}
		}
	}
	// Check standard -v /host/path:/container/path
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		return parts[0]
	}
	return s
}

func isProhibitedHostPath(path string) bool {
	if path == "" {
		return false
	}
	clean := filepath.Clean(path)

	// Check socket patterns
	if strings.HasSuffix(clean, "docker.sock") || strings.HasSuffix(clean, "podman.sock") {
		return true
	}

	// Check exact matches or prefix matches for sensitive directories
	sensitivePaths := []string{
		"/",
		"/etc",
		"/root",
		"/var/run",
		"/run",
	}

	for _, p := range sensitivePaths {
		if clean == p {
			return true
		}
	}

	// Check SSH path patterns
	if strings.HasSuffix(clean, ".ssh") || strings.Contains(clean, "/.ssh") || strings.HasPrefix(path, "~/.ssh") || strings.HasPrefix(path, "$HOME/.ssh") {
		return true
	}

	return false
}
