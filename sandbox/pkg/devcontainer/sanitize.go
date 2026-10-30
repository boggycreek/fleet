// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package devcontainer

import (
	"fmt"
	"strings"
)

// DefaultUnprivilegedUser is the fallback non-root user for sanitized configurations.
const DefaultUnprivilegedUser = "agent"

// Sanitize creates a sanitized copy of a DevContainerSpec and reports actions taken.
func Sanitize(spec *DevContainerSpec) (*DevContainerSpec, DiagnosticReport) {
	report := DiagnosticReport{Diagnostics: make([]Diagnostic, 0)}

	if spec == nil {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Severity: SeverityCritical,
			Code:     "NIL_SPEC",
			Target:   "spec",
			Message:  "Cannot sanitize nil DevContainerSpec",
		})
		return nil, report
	}

	sanitized := &DevContainerSpec{
		Name:          spec.Name,
		Image:         spec.Image,
		Build:         spec.Build,
		ContainerUser: spec.ContainerUser,
		RemoteUser:    spec.RemoteUser,
	}

	// 1. Sanitize Privileged
	if spec.Privileged != nil && *spec.Privileged {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Severity: SeverityInfo,
			Code:     "SANITIZED_PRIVILEGED",
			Target:   "privileged",
			Message:  "Stripped privileged: true setting",
		})
		sanitized.Privileged = nil
	} else {
		sanitized.Privileged = spec.Privileged
	}

	// 2. Sanitize CapAdd
	if len(spec.CapAdd) > 0 {
		cleanCapAdd := make([]string, 0, len(spec.CapAdd))
		for _, capName := range spec.CapAdd {
			normalized := normalizeCap(capName)
			if dangerousCapabilities[normalized] {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{
					Severity: SeverityInfo,
					Code:     "SANITIZED_CAPABILITY",
					Target:   "capAdd",
					Message:  fmt.Sprintf("Stripped dangerous capability from capAdd: %s", capName),
				})
			} else {
				cleanCapAdd = append(cleanCapAdd, capName)
			}
		}
		sanitized.CapAdd = cleanCapAdd
	}

	// 3. Sanitize RunArgs
	if len(spec.RunArgs) > 0 {
		cleanRunArgs := make([]string, 0, len(spec.RunArgs))
		for i := 0; i < len(spec.RunArgs); i++ {
			arg := spec.RunArgs[i]

			// Privileged flag
			if arg == "--privileged" {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{
					Severity: SeverityInfo,
					Code:     "SANITIZED_RUN_ARG",
					Target:   "runArgs",
					Message:  "Stripped --privileged flag from runArgs",
				})
				continue
			}

			// Host namespace flags
			if isHostNamespaceArg(arg) {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{
					Severity: SeverityInfo,
					Code:     "SANITIZED_NAMESPACE",
					Target:   "runArgs",
					Message:  fmt.Sprintf("Stripped host namespace flag from runArgs: %s", arg),
				})
				continue
			}

			if isHostNamespaceFlag(arg) && i+1 < len(spec.RunArgs) && spec.RunArgs[i+1] == "host" {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{
					Severity: SeverityInfo,
					Code:     "SANITIZED_NAMESPACE",
					Target:   "runArgs",
					Message:  fmt.Sprintf("Stripped host namespace flag from runArgs: %s host", arg),
				})
				i++ // skip next arg ("host")
				continue
			}

			// Cap add flags
			if strings.HasPrefix(arg, "--cap-add=") {
				capVal := strings.TrimPrefix(arg, "--cap-add=")
				if dangerousCapabilities[normalizeCap(capVal)] {
					report.Diagnostics = append(report.Diagnostics, Diagnostic{
						Severity: SeverityInfo,
						Code:     "SANITIZED_CAPABILITY",
						Target:   "runArgs",
						Message:  fmt.Sprintf("Stripped dangerous capability from runArgs: %s", arg),
					})
					continue
				}
			} else if arg == "--cap-add" && i+1 < len(spec.RunArgs) {
				capVal := spec.RunArgs[i+1]
				if dangerousCapabilities[normalizeCap(capVal)] {
					report.Diagnostics = append(report.Diagnostics, Diagnostic{
						Severity: SeverityInfo,
						Code:     "SANITIZED_CAPABILITY",
						Target:   "runArgs",
						Message:  fmt.Sprintf("Stripped dangerous capability from runArgs: --cap-add %s", capVal),
					})
					i++ // skip next arg
					continue
				}
			}

			// Volume / Mount flags
			var mountStr string
			skipCount := 0
			if strings.HasPrefix(arg, "-v=") || strings.HasPrefix(arg, "--volume=") || strings.HasPrefix(arg, "--mount=") {
				parts := strings.SplitN(arg, "=", 2)
				mountStr = parts[1]
			} else if (arg == "-v" || arg == "--volume" || arg == "--mount") && i+1 < len(spec.RunArgs) {
				mountStr = spec.RunArgs[i+1]
				skipCount = 1
			}

			if mountStr != "" {
				src := extractSourceFromVolumeOrMountStr(mountStr)
				if isProhibitedHostPath(src) {
					report.Diagnostics = append(report.Diagnostics, Diagnostic{
						Severity: SeverityInfo,
						Code:     "SANITIZED_MOUNT",
						Target:   "runArgs",
						Message:  fmt.Sprintf("Stripped prohibited host bind from runArgs: %s", mountStr),
					})
					i += skipCount
					continue
				}
			}

			cleanRunArgs = append(cleanRunArgs, arg)
		}
		sanitized.RunArgs = cleanRunArgs
	}

	// 4. Sanitize Mounts
	if len(spec.Mounts) > 0 {
		cleanMounts := make([]any, 0, len(spec.Mounts))
		for _, m := range spec.Mounts {
			src := extractMountSource(m)
			if isProhibitedHostPath(src) {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{
					Severity: SeverityInfo,
					Code:     "SANITIZED_MOUNT",
					Target:   "mounts",
					Message:  fmt.Sprintf("Dropped prohibited host mount: %s", src),
				})
			} else {
				cleanMounts = append(cleanMounts, m)
			}
		}
		sanitized.Mounts = cleanMounts
	}

	// 5. Sanitize SecurityOpt (preserve clean security options)
	if len(spec.SecurityOpt) > 0 {
		sanitized.SecurityOpt = append([]string{}, spec.SecurityOpt...)
	}

	// 6. Sanitize Features & Customizations
	if spec.Features != nil {
		sanitized.Features = make(map[string]any)
		for k, v := range spec.Features {
			sanitized.Features[k] = v
		}
	}
	if spec.Customizations != nil {
		sanitized.Customizations = make(map[string]any)
		for k, v := range spec.Customizations {
			sanitized.Customizations[k] = v
		}
	}

	// 7. Normalize Users
	if isRootUser(sanitized.ContainerUser) {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Severity: SeverityInfo,
			Code:     "NORMALIZED_USER",
			Target:   "containerUser",
			Message:  fmt.Sprintf("Normalized containerUser from %q to %q", sanitized.ContainerUser, DefaultUnprivilegedUser),
		})
		sanitized.ContainerUser = DefaultUnprivilegedUser
	}
	if isRootUser(sanitized.RemoteUser) {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Severity: SeverityInfo,
			Code:     "NORMALIZED_USER",
			Target:   "remoteUser",
			Message:  fmt.Sprintf("Normalized remoteUser from %q to %q", sanitized.RemoteUser, DefaultUnprivilegedUser),
		})
		sanitized.RemoteUser = DefaultUnprivilegedUser
	}

	return sanitized, report
}

func isHostNamespaceArg(arg string) bool {
	return arg == "--net=host" || arg == "--network=host" || arg == "--ipc=host" || arg == "--pid=host" || arg == "--userns=host"
}

func isHostNamespaceFlag(arg string) bool {
	return arg == "--net" || arg == "--network" || arg == "--ipc" || arg == "--pid" || arg == "--userns"
}
