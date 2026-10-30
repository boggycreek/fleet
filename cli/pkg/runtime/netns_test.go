// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package runtime

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

func TestStructuredRootlessNetnsErrorDetection(t *testing.T) {
	// 1. Nil / Empty inputs
	if IsRootlessNetnsError(nil, nil) {
		t.Errorf("expected false for nil error and nil output")
	}
	if IsRootlessNetnsError(nil, []byte("")) {
		t.Errorf("expected false for nil error and empty output")
	}

	// 2. Exact and variant pattern matches (case-insensitive)
	patterns := []struct {
		name string
		err  error
		out  string
	}{
		{
			name: "classic lowercase runtime dir mount failure",
			err:  errors.New("exit status 127"),
			out:  "Error: failed to mount runtime directory for rootless netns: no such file or directory",
		},
		{
			name: "uppercase mount runtime failure",
			err:  errors.New("FAILED TO MOUNT RUNTIME DIRECTORY FOR ROOTLESS NETNS"),
			out:  "",
		},
		{
			name: "rootless-netns hyphenated",
			err:  nil,
			out:  "cannot setup rootless-netns bridge",
		},
		{
			name: "slirp4netns failed",
			err:  errors.New("slirp4netns failed with exit code 1"),
			out:  "",
		},
		{
			name: "slirp4netns prefix with colon",
			err:  nil,
			out:  "slirp4netns: failed to setup tap network device",
		},
		{
			name: "pasta failed",
			err:  errors.New("pasta failed"),
			out:  "",
		},
		{
			name: "pasta prefix with colon",
			err:  nil,
			out:  "pasta: cannot configure namespace interfaces",
		},
		{
			name: "path error in /run/user/<uid>/netns",
			err:  nil,
			out:  "stat /run/user/1000/netns: no such file or directory",
		},
		{
			name: "netns: no such file or directory standalone",
			err:  nil,
			out:  "podman: netns: no such file or directory",
		},
		{
			name: "failed to setup network namespace",
			err:  errors.New("failed to setup network namespace: permission denied"),
			out:  "",
		},
		{
			name: "error configuring network namespace",
			err:  errors.New("error configuring network namespace"),
			out:  "",
		},
		{
			name: "failed to open netns",
			err:  nil,
			out:  "failed to open netns: failed to join",
		},
		{
			name: "netavark failure",
			err:  nil,
			out:  "netavark: failed to configure netns for container",
		},
		{
			name: "cannot open network namespace",
			err:  nil,
			out:  "cannot open network namespace /proc/123/ns/net",
		},
	}

	for _, tc := range patterns {
		t.Run(tc.name, func(t *testing.T) {
			if !IsRootlessNetnsError(tc.err, []byte(tc.out)) {
				t.Errorf("expected IsRootlessNetnsError=true for tc %q (err=%v, out=%s)", tc.name, tc.err, tc.out)
			}
		})
	}

	// 3. Negative tests: non-netns errors must return false
	negativeCases := []struct {
		name string
		err  error
		out  string
	}{
		{
			name: "image not found",
			err:  errors.New("exit status 125"),
			out:  "Error: short-name \"foo\" did not resolve to an alias and no search registries found",
		},
		{
			name: "port in use",
			err:  errors.New("exit status 125"),
			out:  "Error: address already in use: bind to 0.0.0.0:2222 failed",
		},
		{
			name: "port already allocated",
			err:  errors.New("exit status 125"),
			out:  "Error: rootlessport: port is already allocated",
		},
		{
			name: "network not found",
			err:  errors.New("exit status 125"),
			out:  "Error: network not found: fleet-infra",
		},
		{
			name: "generic io error",
			err:  errors.New("connection reset by peer"),
			out:  "stream closed unexpectedly",
		},
		{
			name: "unrelated command failure",
			err:  errors.New("exit status 1"),
			out:  "unknown flag: --invalid",
		},
		{
			name: "exit status 127 without netns pattern",
			err:  errors.New("exit status 127"),
			out:  "command not found: foobar",
		},
		{
			name: "container bridge failure without netns keyword",
			err:  errors.New("exit status 1"),
			out:  "bridge foo already exists",
		},
	}

	for _, tc := range negativeCases {
		t.Run(tc.name, func(t *testing.T) {
			if IsRootlessNetnsError(tc.err, []byte(tc.out)) {
				t.Errorf("expected IsRootlessNetnsError=false for %q (err=%v, out=%s)", tc.name, tc.err, tc.out)
			}
		})
	}
}

func TestEnsureRootlessNetNSExecution(t *testing.T) {
	var executed []string
	restore := SetExecCommandContextForTesting(func(ctx context.Context, name string, args ...string) *exec.Cmd {
		executed = append(executed, fmt.Sprintf("%s %s", name, args))
		return exec.Command("true")
	})
	defer restore()

	if err := EnsureRootlessNetNS(context.Background()); err != nil {
		t.Errorf("EnsureRootlessNetNS returned error: %v", err)
	}

	if len(executed) == 0 {
		t.Fatalf("expected podman unshare command to be executed")
	}
}
