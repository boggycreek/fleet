// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package devcontainer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DefaultBaseImage is the fallback base OCI image if none is specified in devcontainer.json.
const DefaultBaseImage = "ubuntu:22.04"

// DefaultPlatformArtifactsImage is the OCI layer source for Fleet platform binaries.
const DefaultPlatformArtifactsImage = "fleet-artifacts:latest"

// DefaultEngineImage is the default OCI layer source for the cognitive engine harness.
const DefaultEngineImage = "fleet-native-engine:latest"

// BuildSpec describes build configuration inside devcontainer.json.
type BuildSpec struct {
	Dockerfile string            `json:"dockerfile,omitempty"`
	Context    string            `json:"context,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
	Target     string            `json:"target,omitempty"`
}

// Spec represents a parsed .devcontainer/devcontainer.json file.
type Spec struct {
	Name              string                 `json:"name,omitempty"`
	Image             string                 `json:"image,omitempty"`
	Build             *BuildSpec             `json:"build,omitempty"`
	ContainerUser     string                 `json:"containerUser,omitempty"`
	RemoteUser        string                 `json:"remoteUser,omitempty"`
	PostCreateCommand interface{}            `json:"postCreateCommand,omitempty"`
	PostStartCommand  interface{}            `json:"postStartCommand,omitempty"`
	PostAttachCommand interface{}            `json:"postAttachCommand,omitempty"`
	RunArgs           []string               `json:"runArgs,omitempty"`
	ForwardPorts      []interface{}          `json:"forwardPorts,omitempty"`
	ContainerEnv      map[string]string      `json:"containerEnv,omitempty"`
	RemoteEnv         map[string]string      `json:"remoteEnv,omitempty"`
	Customizations    map[string]interface{} `json:"customizations,omitempty"`
	Features          map[string]interface{} `json:"features,omitempty"`
}

// SanitizeResult holds the sanitized spec and security warnings produced during processing.
type SanitizeResult struct {
	Spec     *Spec    `json:"spec"`
	Warnings []string `json:"warnings"`
}

// StripJSONComments removes C-style single-line (//) and block (/* */) comments
// as well as trailing commas from JSON data.
func StripJSONComments(data []byte) []byte {
	var buf bytes.Buffer
	inString := false
	inSingleLineComment := false
	inBlockComment := false
	escaped := false

	for i := 0; i < len(data); i++ {
		ch := data[i]

		if inSingleLineComment {
			if ch == '\n' {
				inSingleLineComment = false
				buf.WriteByte(ch)
			}
			continue
		}

		if inBlockComment {
			if ch == '*' && i+1 < len(data) && data[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}

		if inString {
			buf.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}

		if ch == '"' {
			inString = true
			buf.WriteByte(ch)
			continue
		}

		if ch == '/' && i+1 < len(data) {
			if data[i+1] == '/' {
				inSingleLineComment = true
				i++
				continue
			} else if data[i+1] == '*' {
				inBlockComment = true
				i++
				continue
			}
		}

		buf.WriteByte(ch)
	}

	cleaned := buf.Bytes()

	// Strip trailing commas before closing braces/brackets
	reTrailingComma := regexp.MustCompile(`,(\s*[\}\]])`)
	cleaned = reTrailingComma.ReplaceAll(cleaned, []byte("$1"))

	return cleaned
}

// ParseSpec parses devcontainer JSON content into a Spec struct.
func ParseSpec(data []byte) (*Spec, error) {
	cleaned := StripJSONComments(data)
	var spec Spec
	if err := json.Unmarshal(cleaned, &spec); err != nil {
		return nil, fmt.Errorf("failed parsing devcontainer.json: %w", err)
	}
	return &spec, nil
}

// ParseFile reads and parses a devcontainer.json file at the given path.
func ParseFile(path string) (*Spec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed reading devcontainer file %s: %w", path, err)
	}
	return ParseSpec(data)
}

// SanitizeSpec applies security transformations to a DevContainer specification:
// - Audits and removes dangerous runArgs (e.g., --privileged, --cap-add=ALL, --net=host, --pid=host)
// - Audits command hooks for prohibited privilege escalation patterns
// - Returns a deep copy of the spec and a slice of audit warning strings.
func SanitizeSpec(spec *Spec) (*Spec, []string) {
	if spec == nil {
		return &Spec{}, nil
	}

	// Deep copy spec
	clonedData, _ := json.Marshal(spec)
	var copySpec Spec
	_ = json.Unmarshal(clonedData, &copySpec)

	var warnings []string

	// Prohibited runArgs flags
	prohibitedFlags := map[string]bool{
		"--privileged":        true,
		"--cap-add=all":       true,
		"--cap-add=sys_admin": true,
		"--net=host":          true,
		"--network=host":      true,
		"--pid=host":          true,
		"--ipc=host":          true,
	}

	var sanitizedRunArgs []string
	for _, arg := range copySpec.RunArgs {
		lowerArg := strings.ToLower(strings.TrimSpace(arg))
		if prohibitedFlags[lowerArg] || strings.HasPrefix(lowerArg, "--cap-add=all") {
			warnings = append(warnings, fmt.Sprintf("Sanitization: removed dangerous runArg %q", arg))
			continue
		}
		sanitizedRunArgs = append(sanitizedRunArgs, arg)
	}
	copySpec.RunArgs = sanitizedRunArgs

	// Audit lifecycle commands for root / escalation hooks
	checkCommand := func(cmdName string, cmd interface{}) {
		if cmd == nil {
			return
		}
		var str string
		switch v := cmd.(type) {
		case string:
			str = v
		case []interface{}:
			var parts []string
			for _, item := range v {
				parts = append(parts, fmt.Sprintf("%v", item))
			}
			str = strings.Join(parts, " ")
		default:
			str = fmt.Sprintf("%v", v)
		}

		lower := strings.ToLower(str)
		if strings.Contains(lower, "sudo") || strings.Contains(lower, "/etc/sudoers") || strings.Contains(lower, "chmod u+s") {
			warnings = append(warnings, fmt.Sprintf("Sanitization: flagged potential privilege escalation in %s: %q", cmdName, str))
		}
	}

	checkCommand("postCreateCommand", copySpec.PostCreateCommand)
	checkCommand("postStartCommand", copySpec.PostStartCommand)
	checkCommand("postAttachCommand", copySpec.PostAttachCommand)

	return &copySpec, warnings
}

// ResolveEngineImage resolves the target engine layer image reference for a given engine type.
func ResolveEngineImage(engineType string) string {
	clean := strings.TrimSpace(engineType)
	if clean == "" || clean == "default" || clean == "fleet-agent" || clean == "fleet-native-engine" {
		return DefaultEngineImage
	}
	if strings.HasPrefix(clean, "fleet-native-engine") {
		if !strings.Contains(clean, ":") {
			return clean + ":latest"
		}
		return clean
	}
	return fmt.Sprintf("fleet-native-engine-%s:latest", clean)
}

// GenerateContainerfile synthesizes a multi-stage Containerfile from a DevContainer spec.
func GenerateContainerfile(spec *Spec, engineType string) (string, error) {
	if spec == nil {
		spec = &Spec{}
	}

	sanitizedSpec, _ := SanitizeSpec(spec)

	// Determine base image
	baseImage := sanitizedSpec.Image
	if baseImage == "" {
		if sanitizedSpec.Build != nil && sanitizedSpec.Build.Dockerfile != "" {
			// If dockerfile is defined without explicit image, use build dockerfile reference if provided or default
			baseImage = DefaultBaseImage
		} else {
			baseImage = DefaultBaseImage
		}
	}

	engineImage := ResolveEngineImage(engineType)

	var buf bytes.Buffer
	buf.WriteString("# Generated by Fleet CLI (fleet image generate)\n")
	buf.WriteString("# Multi-stage Containerfile synthesized from devcontainer.json\n\n")

	// Stage 1: Project Base Layer
	buf.WriteString("# Stage 1: Project base layer\n")
	buf.WriteString(fmt.Sprintf("FROM %s AS base\n\n", baseImage))

	// Stage 2: Fleet Platform Artifacts Injection
	buf.WriteString("# Stage 2: Fleet platform artifacts\n")
	buf.WriteString(fmt.Sprintf("FROM %s AS platform-artifacts\n\n", DefaultPlatformArtifactsImage))

	// Stage 3: Cognitive Engine Harness Injection
	buf.WriteString("# Stage 3: Cognitive engine harness\n")
	buf.WriteString(fmt.Sprintf("FROM %s AS engine-harness\n\n", engineImage))

	// Stage 4: Entrypoint Normalization and Non-Root User Bounds
	buf.WriteString("# Stage 4: Final composition and security policy bounds\n")
	buf.WriteString("FROM base AS final\n\n")

	buf.WriteString("# Inject Fleet platform artifacts\n")
	buf.WriteString("COPY --from=platform-artifacts /opt/fleet /opt/fleet\n\n")

	buf.WriteString("# Inject Cognitive engine harness\n")
	buf.WriteString("COPY --from=engine-harness /opt/fleet/engine /opt/fleet/engine\n\n")

	// Environment variables
	buf.WriteString("# Environment PATH normalization\n")
	buf.WriteString("ENV PATH=\"/opt/fleet/bin:/opt/fleet/engine/bin:${PATH}\"\n\n")

	if len(sanitizedSpec.ContainerEnv) > 0 {
		buf.WriteString("# Container Environment Variables\n")
		// Sort keys for deterministic output
		var keys []string
		for k := range sanitizedSpec.ContainerEnv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := sanitizedSpec.ContainerEnv[k]
			buf.WriteString(fmt.Sprintf("ENV %s=%q\n", k, v))
		}
		buf.WriteString("\n")
	}

	// Enforce unprivileged agent user bounds (UID/GID 1000)
	buf.WriteString("# Non-root user setup and permission boundaries\n")
	buf.WriteString("RUN groupadd -g 1000 agent 2>/dev/null || true \\\n")
	buf.WriteString(" && useradd -u 1000 -g 1000 -m -s /bin/bash agent 2>/dev/null || true \\\n")
	buf.WriteString(" && mkdir -p /opt/fleet /opt/fleet/engine /home/agent \\\n")
	buf.WriteString(" && chown -R agent:agent /opt/fleet /opt/fleet/engine /home/agent\n\n")

	buf.WriteString("USER agent\n")
	buf.WriteString("WORKDIR /home/agent\n")
	buf.WriteString("ENTRYPOINT [\"/opt/fleet/bin/bpd\"]\n")

	return buf.String(), nil
}

// GenerateContainerfileFromFile reads a devcontainer.json file and generates a multi-stage Containerfile.
func GenerateContainerfileFromFile(devcontainerPath string, engineType string) (string, *SanitizeResult, error) {
	spec, err := ParseFile(devcontainerPath)
	if err != nil {
		return "", nil, err
	}

	sanitizedSpec, warnings := SanitizeSpec(spec)
	content, err := GenerateContainerfile(sanitizedSpec, engineType)
	if err != nil {
		return "", nil, err
	}

	res := &SanitizeResult{
		Spec:     sanitizedSpec,
		Warnings: warnings,
	}

	return content, res, nil
}

// FindDevcontainerFile locates a devcontainer.json file in standard candidate paths.
func FindDevcontainerFile(baseDir string) (string, error) {
	candidates := []string{
		filepath.Join(baseDir, ".devcontainer", "devcontainer.json"),
		filepath.Join(baseDir, "devcontainer.json"),
		filepath.Join(baseDir, ".devcontainer.json"),
	}

	for _, cand := range candidates {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand, nil
		}
	}

	return "", fmt.Errorf("no devcontainer.json found in %s", baseDir)
}
