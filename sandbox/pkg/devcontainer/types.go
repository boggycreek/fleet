// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package devcontainer

// Severity indicates the risk level or urgency of a diagnostic finding.
type Severity string

const (
	SeverityCritical Severity = "Critical"
	SeverityWarning  Severity = "Warning"
	SeverityInfo     Severity = "Info"
)

// Diagnostic represents a single security audit or screening finding.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Target   string   `json:"target"`
	Message  string   `json:"message"`
}

// DiagnosticReport contains the set of diagnostic findings from security screening.
type DiagnosticReport struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// HasCritical returns true if the report contains any Critical severity diagnostics.
func (r *DiagnosticReport) HasCritical() bool {
	for _, d := range r.Diagnostics {
		if d.Severity == SeverityCritical {
			return true
		}
	}
	return false
}

// BuildOptions represents build configuration if specified as an object or string.
type BuildOptions struct {
	Dockerfile string            `json:"dockerfile,omitempty"`
	Context    string            `json:"context,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
	Target     string            `json:"target,omitempty"`
}

// MountSpec represents a structured container mount declaration.
type MountSpec struct {
	Source string `json:"source,omitempty"`
	Target string `json:"target,omitempty"`
	Type   string `json:"type,omitempty"`
}

// DevContainerSpec represents the configuration fields of a devcontainer.json file.
type DevContainerSpec struct {
	Name           string         `json:"name,omitempty"`
	Image          string         `json:"image,omitempty"`
	Build          any            `json:"build,omitempty"`
	RunArgs        []string       `json:"runArgs,omitempty"`
	ContainerUser  string         `json:"containerUser,omitempty"`
	RemoteUser     string         `json:"remoteUser,omitempty"`
	Privileged     *bool          `json:"privileged,omitempty"`
	CapAdd         []string       `json:"capAdd,omitempty"`
	SecurityOpt    []string       `json:"securityOpt,omitempty"`
	Mounts         []any          `json:"mounts,omitempty"`
	Features       map[string]any `json:"features,omitempty"`
	Customizations map[string]any `json:"customizations,omitempty"`
}
