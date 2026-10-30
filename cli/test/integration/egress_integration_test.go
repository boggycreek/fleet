// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package integration

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/boggycreek/fleet/cli/test/harness"
)

// TestEgressFilterInternalDNSAndTraffic verifies that an agent or container sharing
// the network namespace of an egress-filter sidecar can resolve and reach internal
// infrastructure services (Valkey, Forgejo/Gitea, SonarQube) while non-allowlisted
// external domains are dropped (sndbx-86m).
func TestEgressFilterInternalDNSAndTraffic(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not installed or not in PATH; skipping egress filter integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// Ensure fleet-egress image is built and available
	if err := exec.CommandContext(ctx, "podman", "image", "exists", "fleet-egress:latest").Run(); err != nil {
		t.Log("fleet-egress:latest image not found; building from images/egress-filter...")
		buildCmd := exec.CommandContext(ctx, "podman", "build", "-t", "fleet-egress:latest", "-f", "images/egress-filter/Dockerfile", ".")
		if out, err := buildCmd.CombinedOutput(); err != nil {
			t.Fatalf("failed building fleet-egress image: %v (%s)", err, string(out))
		}
	}

	// 1. Start ephemeral infra stack (Valkey + Forgejo on an isolated network)
	infra := harness.StartEphemeralInfraHarness(t)

	// 2. Start a mock SonarQube container on the infra network
	sonarName := fmt.Sprintf("test-sonar-%d", time.Now().UnixNano())
	startSonar := exec.CommandContext(ctx, "podman", "run", "-d",
		"--name", sonarName,
		"--network", infra.NetworkName,
		"--network-alias", "sonarqube",
		"--network-alias", "fleet-sonarqube",
		"docker.io/library/alpine:latest",
		"nc", "-l", "-p", "9000",
	)
	if out, err := startSonar.CombinedOutput(); err != nil {
		t.Fatalf("failed starting mock SonarQube container: %v (%s)", err, string(out))
	}
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", sonarName).Run()
	})

	// 3. Start egress-filter sidecar container on the infra network
	egressName := fmt.Sprintf("test-egress-%d", time.Now().UnixNano())
	startEgress := exec.CommandContext(ctx, "podman", "run", "-d",
		"--name", egressName,
		"--network", infra.NetworkName,
		"--cap-add", "NET_ADMIN",
		"--cap-add", "NET_RAW",
		"fleet-egress:latest",
	)
	if out, err := startEgress.CombinedOutput(); err != nil {
		t.Fatalf("failed starting egress-filter sidecar: %v (%s)", err, string(out))
	}
	t.Cleanup(func() {
		_ = exec.Command("podman", "rm", "-f", egressName).Run()
	})

	// Wait up to 5 seconds for dnsmasq to initialize inside egress container
	time.Sleep(2 * time.Second)

	// Helper to execute commands inside a test container joined to egress-filter's netns
	runInNetns := func(cmdArgs ...string) (string, error) {
		args := append([]string{
			"run", "--rm",
			"--network", fmt.Sprintf("container:%s", egressName),
			"docker.io/library/alpine:latest",
		}, cmdArgs...)
		cmd := exec.CommandContext(ctx, "podman", args...)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}

	// 4. Verify internal DNS resolution for internal infrastructure hostnames
	t.Log("Verifying internal DNS resolution through egress filter...")
	internalHostnames := []string{"valkey", "forgejo", "sonarqube"}
	for _, host := range internalHostnames {
		out, err := runInNetns("nslookup", host, "127.0.0.1")
		if err != nil {
			t.Errorf("DNS lookup for internal service %q failed: %v\nOutput: %s", host, err, out)
		} else if strings.Contains(out, "NXDOMAIN") || !strings.Contains(out, "Address:") {
			t.Errorf("expected internal service %q to resolve to an IP, got:\n%s", host, out)
		} else {
			t.Logf("DNS lookup for %q resolved successfully: %s", host, strings.ReplaceAll(out, "\n", " "))
		}
	}

	// 5. Verify TCP network reachability to internal infrastructure services
	t.Log("Verifying TCP connectivity to internal services...")
	// Valkey port 6379
	if out, err := runInNetns("nc", "-z", "-w", "3", "valkey", "6379"); err != nil {
		t.Errorf("failed connecting to valkey:6379 from egress netns: %v (%s)", err, out)
	}
	// Forgejo port 3000
	if out, err := runInNetns("nc", "-z", "-w", "3", "forgejo", "3000"); err != nil {
		t.Errorf("failed connecting to forgejo:3000 from egress netns: %v (%s)", err, out)
	}
	// SonarQube port 9000
	if out, err := runInNetns("nc", "-z", "-w", "3", "sonarqube", "9000"); err != nil {
		t.Errorf("failed connecting to sonarqube:9000 from egress netns: %v (%s)", err, out)
	}

	// 6. Verify non-allowlisted external domain is blocked / returns NXDOMAIN
	t.Log("Verifying non-allowlisted domain is dropped...")
	outBlocked, errBlocked := runInNetns("nslookup", "unauthorized-external-test-domain.com", "127.0.0.1")
	if errBlocked == nil && !strings.Contains(outBlocked, "NXDOMAIN") {
		t.Errorf("expected non-allowlisted domain to be blocked by catch-all, but query succeeded:\n%s", outBlocked)
	} else {
		t.Logf("non-allowlisted domain correctly blocked (returned NXDOMAIN / error)")
	}

	// 7. Verify allowlisted domain resolves
	t.Log("Verifying allowlisted domain resolves...")
	outAllowed, errAllowed := runInNetns("nslookup", "github.com", "127.0.0.1")
	if errAllowed != nil {
		t.Logf("note: allowlisted lookup for github.com returned error (may be offline / CI): %v (%s)", errAllowed, outAllowed)
	} else if !strings.Contains(outAllowed, "Address:") {
		t.Errorf("expected github.com to resolve, got: %s", outAllowed)
	} else {
		t.Logf("allowlisted domain github.com resolved successfully")
	}
}
