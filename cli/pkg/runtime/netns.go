// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// netnsRegexPatterns defines high-signal regex signatures matching known
// Podman, slirp4netns, pasta, and netavark rootless network namespace failures.
var netnsRegexPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)failed to mount (?:runtime )?directory for rootless netns`),
	regexp.MustCompile(`(?i)rootless[- ]netns`),
	regexp.MustCompile(`(?i)runtime directory for rootless netns`),
	regexp.MustCompile(`(?i)(?:failed to (?:create|setup|set up|open|find|join)|error (?:configuring|setting up|creating)) (?:network namespace|netns)`),
	regexp.MustCompile(`(?i)(?:slirp4netns|pasta)(?::| failed|\.go:)`),
	regexp.MustCompile(`(?i)/run/user/\d+/netns`),
	regexp.MustCompile(`(?i)(?:^|\s)netns:\s*no such file or directory`),
	regexp.MustCompile(`(?i)failed to (?:open|find|join) netns`),
	regexp.MustCompile(`(?i)netavark.*(?:netns|network namespace)`),
	regexp.MustCompile(`(?i)cannot (?:open|mount|bind) (?:network namespace|netns)`),
}

// EnsureRootlessNetNS reconciles and initializes Podman's rootless network namespace
// runtime directory structure. In rootless mode, if the temporary runtime directory
// desynchronizes from the kernel netns mount, running 'podman unshare --rootless-netns true'
// forces Podman to recreate the internal mount namespace tree.
func EnsureRootlessNetNS(ctx context.Context) error {
	netnsDir := ""
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		netnsDir = filepath.Join(xdg, "netns")
	} else {
		netnsDir = fmt.Sprintf("/run/user/%d/netns", os.Getuid())
	}
	_ = os.MkdirAll(netnsDir, 0700)

	cmd := CommandContext(ctx, "podman", "unshare", "--rootless-netns", "true")
	return cmd.Run()
}

// IsRootlessNetnsError checks if a container start, run, or network error is due to a desynchronized
// rootless network namespace mount directory or rootless networking failure.
func IsRootlessNetnsError(err error, out []byte) bool {
	if err == nil && len(out) == 0 {
		return false
	}

	combined := string(out)
	if err != nil {
		if combined != "" {
			combined += " " + err.Error()
		} else {
			combined = err.Error()
		}
	}

	for _, pattern := range netnsRegexPatterns {
		if pattern.MatchString(combined) {
			return true
		}
	}

	return false
}
