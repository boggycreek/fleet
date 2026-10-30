// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/forgejo"
	"github.com/boggycreek/fleet/cli/pkg/sonar"
)

// EgressImage is the default OCI image for the network egress filter sidecar.
const EgressImage = "fleet-egress:latest"

// infraValkeyContainer, infraForgejoContainer, infraPostgresContainer, and infraSonarContainer are the expected shared infra container names.
// These are package-level vars so tests can override them with ephemeral container names.
var (
	infraValkeyContainer     = "fleet-valkey"
	infraForgejoContainer    = "fleet-forgejo"
	infraGiteaContainer      = infraForgejoContainer
	infraPostgresContainer   = "fleet-postgres"
	infraSonarContainer      = "fleet-sonarqube"
	infraOpenRouterContainer = "fleet-openrouter"
	infraNetwork             = "fleet-infra"
)

// SetInfraNetworkForTest overrides infraNetwork for unit testing.
func SetInfraNetworkForTest(name string) func() {
	orig := infraNetwork
	infraNetwork = name
	return func() {
		infraNetwork = orig
	}
}

// SetInfraValkeyContainerForTest overrides infraValkeyContainer for unit testing.
func SetInfraValkeyContainerForTest(name string) func() {
	orig := infraValkeyContainer
	infraValkeyContainer = name
	return func() {
		infraValkeyContainer = orig
	}
}

// SetInfraSonarContainerForTest overrides infraSonarContainer for unit testing.
func SetInfraSonarContainerForTest(name string) func() {
	orig := infraSonarContainer
	infraSonarContainer = name
	return func() {
		infraSonarContainer = orig
	}
}

// SetInfraForgejoContainerForTest overrides infraForgejoContainer for unit testing.
func SetInfraForgejoContainerForTest(name string) func() {
	orig := infraForgejoContainer
	origGitea := infraGiteaContainer
	infraForgejoContainer = name
	infraGiteaContainer = name
	return func() {
		infraForgejoContainer = orig
		infraGiteaContainer = origGitea
	}
}

// SetInfraOpenRouterContainerForTest overrides infraOpenRouterContainer for unit testing.
func SetInfraOpenRouterContainerForTest(name string) func() {
	orig := infraOpenRouterContainer
	infraOpenRouterContainer = name
	return func() {
		infraOpenRouterContainer = orig
	}
}

// ContainerMount describes a mount on a container.
type ContainerMount struct {
	Type        string `json:"Type"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
}

// ContainerInfo describes a container's runtime state
type ContainerInfo struct {
	ID      string           `json:"Id"`
	Names   []string         `json:"Names"`
	State   string           `json:"State"`
	Status  string           `json:"Status"`
	Created int64            `json:"Created"`
	Ports   []PortMapping    `json:"Ports"`
	Mounts  []ContainerMount `json:"Mounts"`
}

// PortMapping port mapping information
type PortMapping struct {
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	HostIP        string `json:"hostIP"`
}

// AgentStatus combines config and container state
type AgentStatus struct {
	Name           string `json:"name"`
	Role           string `json:"role"`
	Image          string `json:"image"`
	ContainerName  string `json:"container_name"`
	ContainerState string `json:"container_state"`
	SSHPort        int    `json:"ssh_port"`
	IDEConnect     string `json:"ide_connect"`
}

// EgressContainerName returns the companion egress filter container name for a container or agent name.
func EgressContainerName(name string) string {
	clean := strings.TrimSpace(name)
	if strings.HasSuffix(clean, "-egress") {
		return clean
	}
	if strings.HasPrefix(clean, "fleet-agent-") || strings.HasPrefix(clean, "fleet-") ||
		strings.HasPrefix(clean, "sndbx-agent-") || strings.HasPrefix(clean, "sndbx-") {
		return clean + "-egress"
	}
	return fmt.Sprintf("fleet-agent-%s-egress", clean)
}

// AgentPodName returns the dedicated Podman pod name for an agent or container name (ADR 00053).
func AgentPodName(name string) string {
	clean := strings.TrimSpace(name)
	clean = strings.TrimPrefix(clean, "fleet-agent-")
	clean = strings.TrimPrefix(clean, "fleet-")
	clean = strings.TrimPrefix(clean, "sndbx-agent-")
	clean = strings.TrimPrefix(clean, "sndbx-")
	clean = strings.TrimSuffix(clean, "-egress")
	clean = strings.TrimSuffix(clean, "-home")
	return fmt.Sprintf("fleet-%s", clean)
}

// AgentPodExists checks if a Podman pod exists by name.
func AgentPodExists(ctx context.Context, podName string) bool {
	cmd := CommandContext(ctx, "podman", "pod", "exists", podName)
	return cmd.Run() == nil
}

// EnsureAgentPod creates a Podman pod if it does not already exist (ADR 00053).
// Declares network fleet-infra and host port mapping 127.0.0.1::2222.
func EnsureAgentPod(ctx context.Context, podName string, addHosts ...string) error {
	if AgentPodExists(ctx, podName) {
		return nil
	}
	netName := infraNetwork
	_ = EnsureNetwork(ctx, netName)

	args := []string{
		"pod", "create",
		"--name", podName,
		"--network", netName,
		"-p", "127.0.0.1::2222",
	}
	for _, host := range addHosts {
		if host != "" {
			args = append(args, fmt.Sprintf("--add-host=%s", host))
		}
	}

	cmd := CommandContext(ctx, "podman", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		if IsRootlessNetnsError(err, out) {
			_ = EnsureRootlessNetNS(ctx)
			retryCmd := CommandContext(ctx, "podman", args...)
			if retryOut, retryErr := retryCmd.CombinedOutput(); retryErr != nil {
				return fmt.Errorf("failed creating pod %s: %v (%s)", podName, retryErr, string(retryOut))
			}
			return nil
		}
		return fmt.Errorf("failed creating pod %s: %v (%s)", podName, err, string(out))
	}
	return nil
}

// PodInfo summarizes the runtime state of a Podman pod (ADR 00053).
type PodInfo struct {
	ID    string        `json:"id"`
	Name  string        `json:"name"`
	State string        `json:"state"`
	Ports []PortMapping `json:"ports,omitempty"`
}

// InspectAgentPod returns state summary for an agent pod (ADR 00053).
func InspectAgentPod(ctx context.Context, podName string) (*PodInfo, error) {
	cmd := CommandContext(ctx, "podman", "pod", "inspect", podName)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	var raw struct {
		ID          string `json:"Id"`
		Name        string `json:"Name"`
		State       string `json:"State"`
		InfraConfig struct {
			PortBindings map[string][]struct {
				HostPort string `json:"HostPort"`
				HostIP   string `json:"HostIp"`
			} `json:"PortBindings"`
		} `json:"InfraConfig"`
	}
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		return nil, err
	}
	info := &PodInfo{
		ID:    raw.ID,
		Name:  raw.Name,
		State: raw.State,
	}
	for containerPortStr, bindings := range raw.InfraConfig.PortBindings {
		parts := strings.Split(containerPortStr, "/")
		cPort, _ := strconv.Atoi(parts[0])
		for _, b := range bindings {
			hPort, _ := strconv.Atoi(b.HostPort)
			info.Ports = append(info.Ports, PortMapping{
				HostIP:        b.HostIP,
				HostPort:      hPort,
				ContainerPort: cPort,
			})
		}
	}
	return info, nil
}

func getPodSSHPort(ctx context.Context, podName string) (int, error) {
	cmd := CommandContext(ctx, "podman", "pod", "inspect", podName)
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	var podInfo struct {
		InfraConfig struct {
			PortBindings map[string][]struct {
				HostPort string `json:"HostPort"`
			} `json:"PortBindings"`
		} `json:"InfraConfig"`
	}
	if err := json.Unmarshal(out, &podInfo); err != nil {
		return 0, err
	}
	if bindings, ok := podInfo.InfraConfig.PortBindings["2222/tcp"]; ok && len(bindings) > 0 {
		return strconv.Atoi(bindings[0].HostPort)
	}
	return 0, fmt.Errorf("no port 2222 mapping found in pod %s", podName)
}

// ResolveAgentImage resolves an image input according to ADR 00029 & ADR 00051:
// 1. Well-known presets (base, opencode, claude, agy, egress)
// 2. Local Podman store images (matching local tags, localhost/ prefix, or custom fleet-* tags)
// 3. Remote OCI image references
func ResolveAgentImage(ctx context.Context, input string) (image string, isLocal bool) {
	lower := strings.ToLower(strings.TrimSpace(input))
	if preset, ok := config.WellKnownImages[lower]; ok {
		// Check if the preset image exists locally
		checkCmd := CommandContext(ctx, "podman", "image", "exists", preset)
		return preset, checkCmd.Run() == nil
	}
	if lower == "" {
		preset := config.WellKnownImages["base"]
		checkCmd := CommandContext(ctx, "podman", "image", "exists", preset)
		return preset, checkCmd.Run() == nil
	}

	// Tier 2: Check local Podman storage
	candidates := []string{input}
	if !strings.Contains(input, ":") {
		candidates = append(candidates, input+":latest")
	}
	if !strings.HasPrefix(input, "localhost/") {
		candidates = append(candidates, "localhost/"+input)
		if !strings.Contains(input, ":") {
			candidates = append(candidates, "localhost/"+input+":latest")
		}
	}

	// Check custom composed image conventions (fleet-custom-<name>, fleet-image-<name>, fleet-<name>)
	if !strings.HasPrefix(input, "fleet-") {
		prefixes := []string{"fleet-custom-", "fleet-image-", "fleet-"}
		for _, pfx := range prefixes {
			cand := pfx + input
			candidates = append(candidates, cand)
			if !strings.Contains(cand, ":") {
				candidates = append(candidates, cand+":latest")
			}
			candidates = append(candidates, "localhost/"+cand)
			if !strings.Contains(cand, ":") {
				candidates = append(candidates, "localhost/"+cand+":latest")
			}
		}
	}

	for _, cand := range candidates {
		checkCmd := CommandContext(ctx, "podman", "image", "exists", cand)
		if err := checkCmd.Run(); err == nil {
			return cand, true
		}
	}

	// Tier 3: External OCI image reference
	resolved := input
	if !strings.Contains(resolved, ":") && !strings.Contains(resolved, "@") {
		resolved = resolved + ":latest"
	}
	checkCmd := CommandContext(ctx, "podman", "image", "exists", resolved)
	return resolved, checkCmd.Run() == nil
}

// EnsureNetwork creates the bridge network if it does not already exist
func EnsureNetwork(ctx context.Context, netName string) error {
	cmd := CommandContext(ctx, "podman", "network", "exists", netName)
	if err := cmd.Run(); err == nil {
		return nil
	}
	createCmd := CommandContext(ctx, "podman", "network", "create", netName)
	if out, err := createCmd.CombinedOutput(); err != nil {
		if IsRootlessNetnsError(err, out) {
			_ = EnsureRootlessNetNS(ctx)
			retryCmd := CommandContext(ctx, "podman", "network", "create", netName)
			return retryCmd.Run()
		}
		return err
	}
	return nil
}

// EnsureVolume creates a named volume if missing
func EnsureVolume(ctx context.Context, volName string) error {
	cmd := CommandContext(ctx, "podman", "volume", "exists", volName)
	if err := cmd.Run(); err == nil {
		return nil
	}
	createCmd := CommandContext(ctx, "podman", "volume", "create", volName)
	return createCmd.Run()
}

// ClearAgentKnownHosts removes any stale per-agent known hosts file.
// It removes ~/.ssh/known_hosts.fleet-%s and cleans up legacy known_hosts.sndbx-%s if present.
func ClearAgentKnownHosts(name string, paths config.Paths) {
	home, _ := os.UserHomeDir()
	if home != "" {
		_ = os.Remove(filepath.Join(home, ".ssh", fmt.Sprintf("known_hosts.fleet-%s", name)))
		_ = os.Remove(filepath.Join(home, ".ssh", fmt.Sprintf("known_hosts.sndbx-%s", name)))
	}
	if paths.SSHDir != "" {
		_ = os.Remove(filepath.Join(paths.SSHDir, fmt.Sprintf("known_hosts.fleet-%s", name)))
		_ = os.Remove(filepath.Join(paths.SSHDir, fmt.Sprintf("known_hosts.sndbx-%s", name)))
	}
}

// CleanAgentKnownHosts removes any stale per-agent known hosts file (alias for ClearAgentKnownHosts).
func CleanAgentKnownHosts(name string, paths config.Paths) {
	ClearAgentKnownHosts(name, paths)
}

// StartEgressFilterContainer starts or creates the companion egress filter sidecar container for an agent.
func StartEgressFilterContainer(ctx context.Context, agentOrContainerName string) error {
	egressName := EgressContainerName(agentOrContainerName)
	podName := AgentPodName(agentOrContainerName)

	// Check if container already exists
	checkCmd := CommandContext(ctx, "podman", "container", "exists", egressName)
	if err := checkCmd.Run(); err == nil {
		startCmd := CommandContext(ctx, "podman", "start", egressName)
		if out, err := startCmd.CombinedOutput(); err != nil {
			if IsRootlessNetnsError(err, out) {
				_ = EnsureRootlessNetNS(ctx)
				retryCmd := CommandContext(ctx, "podman", "start", egressName)
				if retryOut, retryErr := retryCmd.CombinedOutput(); retryErr == nil {
					return nil
				} else {
					return fmt.Errorf("failed starting existing egress filter container %s: %v (%s)", egressName, retryErr, string(retryOut))
				}
			}
			return fmt.Errorf("failed starting existing egress filter container %s: %v (%s)", egressName, err, string(out))
		}
		return nil
	}

	var args []string
	if AgentPodExists(ctx, podName) {
		args = []string{
			"run", "-d",
			"--pod", podName,
			"--name", egressName,
			"--hostname", egressName,
			"--cap-add", "NET_ADMIN",
			"--cap-add", "NET_RAW",
			EgressImage,
		}
	} else {
		netName := infraNetwork
		_ = EnsureNetwork(ctx, netName)
		args = []string{
			"run", "-d",
			"--name", egressName,
			"--hostname", egressName,
			"--network", netName,
			"--cap-add", "NET_ADMIN",
			"--cap-add", "NET_RAW",
			"-p", "127.0.0.1::2222",
			EgressImage,
		}
	}

	cmd := CommandContext(ctx, "podman", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		if IsRootlessNetnsError(err, out) {
			_ = EnsureRootlessNetNS(ctx)
			retryCmd := CommandContext(ctx, "podman", args...)
			if retryOut, retryErr := retryCmd.CombinedOutput(); retryErr == nil {
				return nil
			} else {
				return fmt.Errorf("failed running egress filter container %s: %v (%s)", egressName, retryErr, string(retryOut))
			}
		}
		return fmt.Errorf("failed running egress filter container %s: %v (%s)", egressName, err, string(out))
	}
	return nil
}

// StopEgressFilterContainer stops the companion egress filter sidecar container if running.
func StopEgressFilterContainer(ctx context.Context, agentOrContainerName string) error {
	egressName := EgressContainerName(agentOrContainerName)
	return StopAgentContainer(ctx, egressName)
}

func sonarContainerEnv(cfg *config.AgentConfig) (args []string, extraEnv []string) {
	if os.Getenv("SONAR_DISABLED") == "1" {
		return nil, nil
	}
	if cfg.SonarToken == "" && os.Getenv("SONAR_HOST_URL") == "" && os.Getenv("SONARQUBE_URL") == "" {
		return nil, nil
	}

	sonarURL := os.Getenv("SONARQUBE_URL")
	if sonarURL == "" {
		sonarURL = os.Getenv("SONAR_HOST_URL")
	}
	if sonarURL == "" {
		sonarURL = "http://sonarqube:9000"
	}

	args = append(args,
		"-e", fmt.Sprintf("SONAR_HOST_URL=%s", sonarURL),
		"-e", fmt.Sprintf("SONARQUBE_URL=%s", sonarURL),
	)

	if cfg.SonarToken != "" {
		// Pass -e SONAR_TOKEN without value so the secret is supplied via cmd.Env
		// rather than leaking in argv visible via ps / /proc/<pid>/cmdline.
		args = append(args, "-e", "SONAR_TOKEN")
		extraEnv = append(extraEnv, fmt.Sprintf("SONAR_TOKEN=%s", cfg.SonarToken))
	}

	return args, extraEnv
}

// StartAgentContainer runs or starts an agent container with Podman
func StartAgentContainer(ctx context.Context, cfg *config.AgentConfig, paths config.Paths, bpHost string, bpPort int) error {
	ClearAgentKnownHosts(cfg.Name, paths)
	if cfg.IsHostContainer() {
		return startHostAgentContainer(ctx, cfg, paths, bpHost, bpPort)
	}

	isLocalModelURL := func(murl string) bool {
		if murl == "" {
			return false
		}
		u, err := url.Parse(murl)
		if err != nil {
			return false
		}
		host := u.Hostname()
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	}

	usePod := cfg.UsePods()
	podName := cfg.GetPodName()
	if usePod {
		var addHosts []string
		if isLocalModelURL(cfg.ModelURL) {
			addHosts = append(addHosts, "llm-gateway:host-gateway")
		}
		if err := EnsureAgentPod(ctx, podName, addHosts...); err != nil {
			return err
		}
	}

	netName := infraNetwork
	_ = EnsureNetwork(ctx, netName)
	_ = EnsureVolume(ctx, cfg.VolumeName)

	// Check if container already exists
	checkCmd := CommandContext(ctx, "podman", "container", "exists", cfg.ContainerName)
	if err := checkCmd.Run(); err == nil {
		// Container exists, start if stopped
		startCmd := CommandContext(ctx, "podman", "start", cfg.ContainerName)
		if out, err := startCmd.CombinedOutput(); err != nil {
			if IsRootlessNetnsError(err, out) {
				_ = EnsureRootlessNetNS(ctx)
				retryCmd := CommandContext(ctx, "podman", "start", cfg.ContainerName)
				if retryOut, retryErr := retryCmd.CombinedOutput(); retryErr == nil {
					return nil
				} else {
					return fmt.Errorf("failed starting existing container %s: %v (%s)", cfg.ContainerName, retryErr, string(retryOut))
				}
			}
			return fmt.Errorf("failed starting existing container %s: %v (%s)", cfg.ContainerName, err, string(out))
		}
		return nil
	}

	// Build run arguments
	sshKeyPub := fmt.Sprintf("%s.pub", paths.GetSSHPrivateKey())
	var mounts []string
	if _, err := os.Stat(sshKeyPub); err == nil {
		mounts = append(mounts, "-v", fmt.Sprintf("%s:/tmp/host-keys/fleet.pub:ro", sshKeyPub))
	}
	mounts = append(mounts, "-v", fmt.Sprintf("%s:/home/agent:z", cfg.VolumeName))

	containerBPHost := bpHost
	if containerBPHost == "localhost" || containerBPHost == "127.0.0.1" || containerBPHost == "::1" || containerBPHost == "" {
		containerBPHost = infraValkeyContainer
	}

	var args []string
	if usePod {
		args = []string{
			"run", "-d",
			"--pod", podName,
			"--name", cfg.ContainerName,
			"--hostname", fmt.Sprintf("%s-sandbox", cfg.Name),
			"-e", fmt.Sprintf("AGENT_NAME=%s", cfg.Name),
			"-e", fmt.Sprintf("AGENT_ROLE=%s", cfg.Role),
			"-e", fmt.Sprintf("ROLE=%s", cfg.Role),
			"-e", fmt.Sprintf("BP_AGENT=%s", cfg.Name),
			"-e", "BP_PASSWORD",
			"-e", fmt.Sprintf("BP_HOST=%s", containerBPHost),
			"-e", fmt.Sprintf("BP_PORT=%d", bpPort),
			"-e", "BP_SIGNING_KEY_PEM",
			"-e", fmt.Sprintf("VALKEY_ADDR=%s:%d", containerBPHost, bpPort),
			"-e", "VALKEY_PASSWORD",
		}
	} else {
		args = []string{
			"run", "-d",
			"--name", cfg.ContainerName,
			"--hostname", fmt.Sprintf("%s-sandbox", cfg.Name),
			"--network", netName,
			"-p", "127.0.0.1::2222",
			"-e", fmt.Sprintf("AGENT_NAME=%s", cfg.Name),
			"-e", fmt.Sprintf("AGENT_ROLE=%s", cfg.Role),
			"-e", fmt.Sprintf("ROLE=%s", cfg.Role),
			"-e", fmt.Sprintf("BP_AGENT=%s", cfg.Name),
			"-e", "BP_PASSWORD",
			"-e", fmt.Sprintf("BP_HOST=%s", containerBPHost),
			"-e", fmt.Sprintf("BP_PORT=%d", bpPort),
			"-e", "BP_SIGNING_KEY_PEM",
			"-e", fmt.Sprintf("VALKEY_ADDR=%s:%d", containerBPHost, bpPort),
			"-e", "VALKEY_PASSWORD",
		}
		if isLocalModelURL(cfg.ModelURL) {
			args = append(args, "--add-host", "llm-gateway:host-gateway")
		}
	}

	extraEnv := []string{
		fmt.Sprintf("BP_PASSWORD=%s", cfg.Password),
		fmt.Sprintf("BP_SIGNING_KEY_PEM=%s", cfg.SigningKeyPEM),
		fmt.Sprintf("VALKEY_PASSWORD=%s", cfg.Password),
	}

	if cfg.Harness != "" {
		args = append(args, "-e", fmt.Sprintf("AGENT_HARNESS=%s", cfg.Harness))
	}

	humanName := os.Getenv("HUMAN_NAME")
	if humanName == "" {
		humanName = "operator"
	}
	args = append(args, "-e", fmt.Sprintf("HUMAN_NAME=%s", humanName))

	if cfg.ModelURL != "" {
		// Translate localhost / 127.0.0.1 to llm-gateway for container-to-host bridge access
		containerModelURL := cfg.ModelURL
		containerModelURL = strings.ReplaceAll(containerModelURL, "://localhost", "://llm-gateway")
		containerModelURL = strings.ReplaceAll(containerModelURL, "://127.0.0.1", "://llm-gateway")

		args = append(args,
			"-e", fmt.Sprintf("OPENAI_BASE_URL=%s", containerModelURL),
			"-e", fmt.Sprintf("OPENAI_API_BASE=%s", containerModelURL),
			"-e", fmt.Sprintf("MODEL_URL=%s", containerModelURL),
		)
	}

	if cfg.ModelName != "" {
		args = append(args,
			"-e", fmt.Sprintf("MODEL_NAME=%s", cfg.ModelName),
			"-e", fmt.Sprintf("OPENAI_MODEL=%s", cfg.ModelName),
			"-e", fmt.Sprintf("LLM_MODEL=%s", cfg.ModelName),
		)
	}

	if cfg.ModelAPIKey != "" {
		args = append(args, "-e", "OPENAI_API_KEY")
		extraEnv = append(extraEnv, fmt.Sprintf("OPENAI_API_KEY=%s", cfg.ModelAPIKey))
	} else if cfg.ModelURL != "" {
		// Default dummy key for endpoints that require header presence (e.g. LiteLLM/vLLM)
		args = append(args, "-e", "OPENAI_API_KEY")
		extraEnv = append(extraEnv, "OPENAI_API_KEY=local-openai-key")
	}

	sonarArgs, sonarEnv := sonarContainerEnv(cfg)
	if len(sonarArgs) > 0 {
		args = append(args, sonarArgs...)
	}

	args = append(args, mounts...)
	args = append(args, cfg.Image)

	cmd := CommandContext(ctx, "podman", args...)
	cmd.Env = append(cmd.Environ(), extraEnv...)
	if len(sonarEnv) > 0 {
		cmd.Env = append(cmd.Env, sonarEnv...)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		if IsRootlessNetnsError(err, out) {
			_ = EnsureRootlessNetNS(ctx)
			retryCmd := CommandContext(ctx, "podman", args...)
			retryCmd.Env = append(retryCmd.Environ(), extraEnv...)
			if len(sonarEnv) > 0 {
				retryCmd.Env = append(retryCmd.Env, sonarEnv...)
			}
			if retryOut, retryErr := retryCmd.CombinedOutput(); retryErr == nil {
				return nil
			} else {
				return fmt.Errorf("failed running podman container %s with image %s: %v (%s)", cfg.ContainerName, cfg.Image, retryErr, string(retryOut))
			}
		}
		return fmt.Errorf("failed running podman container %s with image %s: %v (%s)", cfg.ContainerName, cfg.Image, err, string(out))
	}
	return nil
}

// startHostAgentContainer starts or creates a host-connected container with workspace projection and keep-id mapping (ADR 00043/00044)
func startHostAgentContainer(ctx context.Context, cfg *config.AgentConfig, paths config.Paths, bpHost string, bpPort int) error {
	if cfg.WorkspacePath != "" {
		if _, err := os.Stat(cfg.WorkspacePath); err != nil {
			return fmt.Errorf("host workspace directory %q does not exist: %w", cfg.WorkspacePath, err)
		}
	}
	if cfg.VolumeName != "" {
		_ = EnsureVolume(ctx, cfg.VolumeName)
	}

	// Check if container already exists
	checkCmd := CommandContext(ctx, "podman", "container", "exists", cfg.ContainerName)
	if err := checkCmd.Run(); err == nil {
		// Container exists, start if stopped
		startCmd := CommandContext(ctx, "podman", "start", cfg.ContainerName)
		if out, err := startCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed starting existing host container %s: %v (%s)", cfg.ContainerName, err, string(out))
		}
		return nil
	}

	netName := infraNetwork
	_ = EnsureNetwork(ctx, netName)

	// Build run arguments for host-connected container
	sshKeyPub := fmt.Sprintf("%s.pub", paths.GetSSHPrivateKey())
	var mounts []string
	if _, err := os.Stat(sshKeyPub); err == nil {
		mounts = append(mounts, "-v", fmt.Sprintf("%s:/tmp/host-keys/fleet.pub:ro", sshKeyPub))
	}
	if cfg.VolumeName != "" {
		mounts = append(mounts, "-v", fmt.Sprintf("%s:/home/agent:z", cfg.VolumeName))
	}
	if cfg.WorkspacePath != "" {
		mounts = append(mounts, "-v", fmt.Sprintf("%s:/home/agent/workspace:z", cfg.WorkspacePath))
	}

	containerBPHost := bpHost
	if containerBPHost == "localhost" || containerBPHost == "127.0.0.1" || containerBPHost == "::1" || containerBPHost == "" {
		containerBPHost = infraValkeyContainer
	}

	isLocalModelURL := func(murl string) bool {
		if murl == "" {
			return false
		}
		u, err := url.Parse(murl)
		if err != nil {
			return false
		}
		host := u.Hostname()
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	}

	args := []string{
		"run", "-d", "-i", "-t",
		"--name", cfg.ContainerName,
		"--hostname", fmt.Sprintf("%s-sandbox", cfg.Name),
		"--network", netName,
		"-p", "127.0.0.1::2222",
		"--userns=keep-id:uid=1000,gid=1000",
		"-e", fmt.Sprintf("AGENT_NAME=%s", cfg.Name),
		"-e", fmt.Sprintf("AGENT_ROLE=%s", cfg.Role),
		"-e", fmt.Sprintf("ROLE=%s", cfg.Role),
		"-e", fmt.Sprintf("BP_AGENT=%s", cfg.Name),
		"-e", "BP_PASSWORD",
		"-e", fmt.Sprintf("BP_HOST=%s", containerBPHost),
		"-e", fmt.Sprintf("BP_PORT=%d", bpPort),
		"-e", "BP_SIGNING_KEY_PEM",
		"-e", fmt.Sprintf("VALKEY_ADDR=%s:%d", containerBPHost, bpPort),
		"-e", "VALKEY_PASSWORD",
	}

	if isLocalModelURL(cfg.ModelURL) {
		args = append(args, "--add-host", "llm-gateway:host-gateway")
	}

	extraEnv := []string{
		fmt.Sprintf("BP_PASSWORD=%s", cfg.Password),
		fmt.Sprintf("BP_SIGNING_KEY_PEM=%s", cfg.SigningKeyPEM),
		fmt.Sprintf("VALKEY_PASSWORD=%s", cfg.Password),
	}

	if cfg.Harness != "" {
		args = append(args, "-e", fmt.Sprintf("AGENT_HARNESS=%s", cfg.Harness))
	}

	humanName := os.Getenv("HUMAN_NAME")
	if humanName == "" {
		humanName = "operator"
	}
	args = append(args, "-e", fmt.Sprintf("HUMAN_NAME=%s", humanName))

	if cfg.ModelURL != "" {
		// Translate localhost / 127.0.0.1 to llm-gateway for container-to-host bridge access
		containerModelURL := cfg.ModelURL
		containerModelURL = strings.ReplaceAll(containerModelURL, "://localhost", "://llm-gateway")
		containerModelURL = strings.ReplaceAll(containerModelURL, "://127.0.0.1", "://llm-gateway")

		args = append(args,
			"-e", fmt.Sprintf("OPENAI_BASE_URL=%s", containerModelURL),
			"-e", fmt.Sprintf("OPENAI_API_BASE=%s", containerModelURL),
			"-e", fmt.Sprintf("MODEL_URL=%s", containerModelURL),
		)
	}

	if cfg.ModelName != "" {
		args = append(args,
			"-e", fmt.Sprintf("MODEL_NAME=%s", cfg.ModelName),
			"-e", fmt.Sprintf("OPENAI_MODEL=%s", cfg.ModelName),
			"-e", fmt.Sprintf("LLM_MODEL=%s", cfg.ModelName),
		)
	}

	if cfg.ModelAPIKey != "" {
		args = append(args, "-e", "OPENAI_API_KEY")
		extraEnv = append(extraEnv, fmt.Sprintf("OPENAI_API_KEY=%s", cfg.ModelAPIKey))
	} else if cfg.ModelURL != "" {
		args = append(args, "-e", "OPENAI_API_KEY")
		extraEnv = append(extraEnv, "OPENAI_API_KEY=local-openai-key")
	}

	sonarArgs, sonarEnv := sonarContainerEnv(cfg)
	if len(sonarArgs) > 0 {
		args = append(args, sonarArgs...)
	}

	args = append(args, mounts...)
	args = append(args, cfg.Image)

	cmd := CommandContext(ctx, "podman", args...)
	cmd.Env = append(cmd.Environ(), extraEnv...)
	if len(sonarEnv) > 0 {
		cmd.Env = append(cmd.Env, sonarEnv...)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed running host agent container %s: %v (%s)", cfg.ContainerName, err, string(out))
	}
	return nil
}

// StopAgentContainer stops a running container or pod (ADR 00053).
func StopAgentContainer(ctx context.Context, containerName string) error {
	podName := AgentPodName(containerName)
	if AgentPodExists(ctx, podName) {
		_ = CommandContext(ctx, "podman", "pod", "stop", podName).Run()
	}
	cmd := CommandContext(ctx, "podman", "stop", containerName)
	return cmd.Run()
}

// CleanAgentContainer removes a container or pod while keeping the named volume (ADR 00053).
func CleanAgentContainer(ctx context.Context, containerName string) error {
	_ = StopAgentContainer(ctx, containerName)

	podName := AgentPodName(containerName)
	if AgentPodExists(ctx, podName) {
		_ = CommandContext(ctx, "podman", "pod", "rm", "-f", podName).Run()
	}

	cmd := CommandContext(ctx, "podman", "rm", "-f", containerName)
	err := cmd.Run()

	// Clean companion egress filter container if present
	egressName := EgressContainerName(containerName)
	_ = StopAgentContainer(ctx, egressName)
	_ = CommandContext(ctx, "podman", "rm", "-f", egressName).Run()

	agentName := strings.TrimPrefix(containerName, "fleet-agent-")
	agentName = strings.TrimPrefix(agentName, "fleet-")
	agentName = strings.TrimPrefix(agentName, "sndbx-agent-")
	agentName = strings.TrimPrefix(agentName, "sndbx-")
	ClearAgentKnownHosts(agentName, config.GetPaths())
	return err
}

// DestroyAgentContainer removes container, pod, and persistent volume.
func DestroyAgentContainer(ctx context.Context, containerName, volumeName string) error {
	_ = CleanAgentContainer(ctx, containerName)
	cmd := CommandContext(ctx, "podman", "volume", "rm", "-f", volumeName)
	return cmd.Run()
}

// GetAgentSSHPort discovers the dynamically assigned host port mapping for port 2222.
// Inspects container port, companion egress container, or pod port mapping (ADR 00053).
func GetAgentSSHPort(ctx context.Context, containerName string) (int, error) {
	cmd := CommandContext(ctx, "podman", "port", containerName, "2222")
	out, err := cmd.Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		egressName := EgressContainerName(containerName)
		if egressName != containerName {
			cmdEgress := CommandContext(ctx, "podman", "port", egressName, "2222")
			if outEgress, errEgress := cmdEgress.Output(); errEgress == nil && len(strings.TrimSpace(string(outEgress))) > 0 {
				out = outEgress
				err = nil
			}
		}
	}
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		podName := AgentPodName(containerName)
		if AgentPodExists(ctx, podName) {
			if port, podErr := getPodSSHPort(ctx, podName); podErr == nil && port > 0 {
				return port, nil
			}
		}
	}
	if err != nil {
		return 0, err
	}
	// Output format typically: 127.0.0.1:41235 or :::41235
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return 0, fmt.Errorf("no port mapping found")
	}
	parts := strings.Split(lines[0], ":")
	portStr := parts[len(parts)-1]
	return strconv.Atoi(portStr)
}

// InspectAgentContainer returns state summary for a container
func InspectAgentContainer(ctx context.Context, containerName string) (*ContainerInfo, error) {
	cmd := CommandContext(ctx, "podman", "inspect", "--type", "container", containerName)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	var list []struct {
		ID    string   `json:"Id"`
		Name  string   `json:"Name"`
		Names []string `json:"Names"`
		State struct {
			Status  string `json:"Status"`
			Running bool   `json:"Running"`
		} `json:"State"`
		Mounts []ContainerMount `json:"Mounts"`
	}
	if err := json.Unmarshal(out.Bytes(), &list); err != nil || len(list) == 0 {
		return nil, fmt.Errorf("failed inspecting container")
	}

	state := list[0].State.Status
	if state == "" {
		if list[0].State.Running {
			state = "running"
		} else {
			state = "stopped"
		}
	}

	names := list[0].Names
	if len(names) == 0 && list[0].Name != "" {
		names = []string{strings.TrimPrefix(list[0].Name, "/")}
	}

	return &ContainerInfo{
		ID:     list[0].ID,
		Names:  names,
		State:  state,
		Mounts: list[0].Mounts,
	}, nil
}

func generateSecurePassword() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "fallback_secure_pass_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(b)
}

func ensureEnvPasswords(paths config.Paths, adminPass, humanPass *string) error {
	if *adminPass == "" {
		*adminPass = os.Getenv("FORGEJO_ADMIN_PASSWORD")
	}
	if *adminPass == "" {
		*adminPass = os.Getenv("GITEA_ADMIN_PASSWORD")
	}
	if *adminPass == "" {
		*adminPass = generateSecurePassword()
		if paths.EnvFile != "" {
			_ = paths.SetEnvVar("FORGEJO_ADMIN_PASSWORD", *adminPass)
		}
	}

	if *humanPass == "" {
		*humanPass = os.Getenv("HUMAN_PASSWORD")
	}
	if *humanPass == "" {
		*humanPass = generateSecurePassword()
		if paths.EnvFile != "" {
			_ = paths.SetEnvVar("HUMAN_PASSWORD", *humanPass)
		}
	}
	return nil
}

// StartInfraStack launches the shared Valkey and Gitea containers using Podman directly
func StartInfraStack(ctx context.Context, paths config.Paths, adminPass, humanPass, humanName string) error {
	_ = ensureEnvPasswords(paths, &adminPass, &humanPass)

	netName := infraNetwork
	if err := EnsureNetwork(ctx, netName); err != nil {
		return fmt.Errorf("failed ensuring network %s: %w", netName, err)
	}

	// Ensure volumes
	_ = EnsureVolume(ctx, "fleet-valkey-data")
	_ = EnsureVolume(ctx, "fleet-valkey-config")
	// Migrate legacy gitea volume if forgejo volume doesn't exist yet
	if CommandContext(ctx, "podman", "volume", "exists", "fleet-forgejo-data").Run() != nil {
		if CommandContext(ctx, "podman", "volume", "exists", "agent-sandbox-forgejo-data").Run() == nil {
			_ = CommandContext(ctx, "podman", "volume", "create", "fleet-forgejo-data").Run()
			_ = CommandContext(ctx, "podman", "run", "--rm",
				"-v", "agent-sandbox-forgejo-data:/src:z",
				"-v", "fleet-forgejo-data:/dst:z",
				"codeberg.org/forgejo/forgejo:9-rootless",
				"cp", "-a", "/src/.", "/dst/").Run()
		} else if CommandContext(ctx, "podman", "volume", "exists", "agent-sandbox-gitea-data").Run() == nil {
			_ = CommandContext(ctx, "podman", "volume", "create", "fleet-forgejo-data").Run()
			_ = CommandContext(ctx, "podman", "run", "--rm",
				"-v", "agent-sandbox-gitea-data:/src:z",
				"-v", "fleet-forgejo-data:/dst:z",
				"codeberg.org/forgejo/forgejo:9-rootless",
				"cp", "-a", "/src/.", "/dst/").Run()
		}
	}
	_ = EnsureVolume(ctx, "fleet-forgejo-data")
	_ = EnsureVolume(ctx, "fleet-postgres-data")
	_ = EnsureVolume(ctx, "fleet-sonarqube-data")
	_ = EnsureVolume(ctx, "fleet-sonarqube-extensions")
	_ = EnsureVolume(ctx, "fleet-sonarqube-logs")

	// Render Valkey ACL in config directory
	valkeyConfigDir := filepath.Join(paths.DataHome, "valkey")
	_ = os.MkdirAll(valkeyConfigDir, 0700)
	aclFile := filepath.Join(valkeyConfigDir, "valkey-users.acl")

	if humanName == "" {
		humanName = "operator"
	}

	aclContent := fmt.Sprintf(`user default off
user admin on >%s ~* &* +@all
user %s on >%s ~%s:* ~human:name ~liaison:current ~poll-interval ~identity:* ~specialty:* %%R~*:* &* +@all (+xadd ~*:inbox)
`, adminPass, humanName, humanPass, humanName)
	_ = os.WriteFile(aclFile, []byte(aclContent), 0600)
	_ = os.Chmod(aclFile, 0600)
	_ = os.Chmod(valkeyConfigDir, 0700)

	// 1. Start Valkey container if not already running
	valkeyContainer := infraValkeyContainer
	valkeyVol := "fleet-valkey-data"
	if valkeyContainer != "fleet-valkey" {
		valkeyVol = valkeyContainer + "-data"
	}
	_ = EnsureVolume(ctx, valkeyVol)

	valkeyPort := os.Getenv("BP_PORT")
	if valkeyPort == "" {
		valkeyPort = "6379"
	}

	valkeyArgs := []string{
		"run", "-d",
		"--name", valkeyContainer,
		"--hostname", "valkey",
		"--network", netName,
		"-p", fmt.Sprintf("127.0.0.1:%s:6379", valkeyPort),
		"-v", fmt.Sprintf("%s:/etc/valkey:z", valkeyConfigDir),
		"-v", fmt.Sprintf("%s:/data:z", valkeyVol),
		"docker.io/valkey/valkey:8.0-alpine",
		"valkey-server", "--aclfile", "/etc/valkey/valkey-users.acl", "--appendonly", "yes", "--port", "6379",
	}

	checkValkey := CommandContext(ctx, "podman", "container", "exists", valkeyContainer)
	if err := checkValkey.Run(); err != nil {
		cmd := CommandContext(ctx, "podman", valkeyArgs...)
		if out, err := cmd.CombinedOutput(); err != nil {
			if IsRootlessNetnsError(err, out) {
				_ = EnsureRootlessNetNS(ctx)
				retryCmd := CommandContext(ctx, "podman", valkeyArgs...)
				if retryOut, retryErr := retryCmd.CombinedOutput(); retryErr != nil {
					return fmt.Errorf("failed starting valkey container: %v (%s)", retryErr, string(retryOut))
				}
			} else {
				return fmt.Errorf("failed starting valkey container: %v (%s)", err, string(out))
			}
		}
	} else {
		recreate := false
		if info, err := InspectAgentContainer(ctx, valkeyContainer); err == nil && info != nil {
			for _, m := range info.Mounts {
				if m.Destination == "/etc/valkey" && filepath.Clean(m.Source) != filepath.Clean(valkeyConfigDir) {
					recreate = true
					break
				}
			}
		}
		if recreate {
			_ = CommandContext(ctx, "podman", "rm", "-f", valkeyContainer).Run()
			cmd := CommandContext(ctx, "podman", valkeyArgs...)
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("failed restarting valkey container: %v (%s)", err, string(out))
			}
		} else {
			startCmd := CommandContext(ctx, "podman", "start", valkeyContainer)
			if startOut, err := startCmd.CombinedOutput(); err != nil {
				_ = CommandContext(ctx, "podman", "rm", "-f", valkeyContainer).Run()
				if IsRootlessNetnsError(err, startOut) {
					_ = EnsureRootlessNetNS(ctx)
				}
				cmd := CommandContext(ctx, "podman", valkeyArgs...)
				if out, err := cmd.CombinedOutput(); err != nil {
					return fmt.Errorf("failed restarting valkey container: %v (%s)", err, string(out))
				}
			}
		}
	}

	// 2. Start Forgejo container if not already running
	forgejoContainer := infraForgejoContainer
	forgejoVol := "fleet-forgejo-data"
	if forgejoContainer != "fleet-forgejo" {
		forgejoVol = forgejoContainer + "-data"
		_ = EnsureVolume(ctx, forgejoVol)
	}
	forgejoArgs := []string{
		"run", "-d",
		"--name", forgejoContainer,
		"--hostname", "forgejo",
		"--network", netName,
		"--network-alias", "forgejo",
		"--network-alias", "gitea",
		"-p", "127.0.0.1:3000:3000",
		"-p", "127.0.0.1:2223:2222",
		"-e", "FORGEJO__server__DOMAIN=forgejo",
		"-e", "FORGEJO__server__HTTP_PORT=3000",
		"-e", "FORGEJO__server__ROOT_URL=http://forgejo:3000/",
		"-e", "FORGEJO__server__SSH_PORT=2222",
		"-e", "FORGEJO__server__SSH_LISTEN_PORT=2222",
		"-e", "FORGEJO__database__DB_TYPE=sqlite3",
		"-e", "FORGEJO__database__PATH=/var/lib/gitea/data/gitea.db",
		"-e", "FORGEJO__service__DISABLE_REGISTRATION=false",
		"-e", "FORGEJO__service__REQUIRE_SIGNIN_VIEW=false",
		"-e", "FORGEJO__security__INSTALL_LOCK=true",
		"-v", fmt.Sprintf("%s:/var/lib/gitea:z", forgejoVol),
		"codeberg.org/forgejo/forgejo:9-rootless",
	}

	checkForgejo := CommandContext(ctx, "podman", "container", "exists", forgejoContainer)
	if err := checkForgejo.Run(); err != nil {
		cmd := CommandContext(ctx, "podman", forgejoArgs...)
		if out, err := cmd.CombinedOutput(); err != nil {
			if IsRootlessNetnsError(err, out) {
				_ = EnsureRootlessNetNS(ctx)
				retryCmd := CommandContext(ctx, "podman", forgejoArgs...)
				if retryOut, retryErr := retryCmd.CombinedOutput(); retryErr != nil {
					return fmt.Errorf("failed starting forgejo container: %v (%s)", retryErr, string(retryOut))
				}
			} else {
				return fmt.Errorf("failed starting forgejo container: %v (%s)", err, string(out))
			}
		}
	} else {
		startCmd := CommandContext(ctx, "podman", "start", forgejoContainer)
		if startOut, err := startCmd.CombinedOutput(); err != nil {
			_ = CommandContext(ctx, "podman", "rm", "-f", forgejoContainer).Run()
			if IsRootlessNetnsError(err, startOut) {
				_ = EnsureRootlessNetNS(ctx)
			}
			cmd := CommandContext(ctx, "podman", forgejoArgs...)
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("failed restarting forgejo container: %v (%s)", err, string(out))
			}
		}
	}

	// 3. Bootstrap Forgejo admin user and default fleet organization
	_ = BootstrapForgejo(ctx, adminPass)

	// 4. Start PostgreSQL container if not already running
	postgresContainer := infraPostgresContainer
	postgresArgs := []string{
		"run", "-d",
		"--name", postgresContainer,
		"--hostname", "postgres",
		"--network", netName,
		"-p", "127.0.0.1:5432:5432",
		"-e", "POSTGRES_USER=sonar",
		"-e", "POSTGRES_PASSWORD=sonar",
		"-e", "POSTGRES_DB=sonar",
		"-v", "fleet-postgres-data:/var/lib/postgresql/data:z",
		"docker.io/library/postgres:16-alpine",
	}

	checkPostgres := CommandContext(ctx, "podman", "container", "exists", postgresContainer)
	if err := checkPostgres.Run(); err != nil {
		cmd := CommandContext(ctx, "podman", postgresArgs...)
		if out, err := cmd.CombinedOutput(); err != nil {
			if IsRootlessNetnsError(err, out) {
				_ = EnsureRootlessNetNS(ctx)
				retryCmd := CommandContext(ctx, "podman", postgresArgs...)
				if retryOut, retryErr := retryCmd.CombinedOutput(); retryErr != nil {
					return fmt.Errorf("failed starting postgres container: %v (%s)", retryErr, string(retryOut))
				}
			} else {
				return fmt.Errorf("failed starting postgres container: %v (%s)", err, string(out))
			}
		}
	} else {
		startCmd := CommandContext(ctx, "podman", "start", postgresContainer)
		if startOut, err := startCmd.CombinedOutput(); err != nil {
			_ = CommandContext(ctx, "podman", "rm", "-f", postgresContainer).Run()
			if IsRootlessNetnsError(err, startOut) {
				_ = EnsureRootlessNetNS(ctx)
			}
			cmd := CommandContext(ctx, "podman", postgresArgs...)
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("failed restarting postgres container: %v (%s)", err, string(out))
			}
		}
	}

	// 6. Start OpenRouter proxy container if key is present
	_ = StartOpenRouterContainer(ctx, paths)

	// 5. Start SonarQube container if image exists and not already running
	sonarContainer := infraSonarContainer
	sonarArgs := []string{
		"run", "-d",
		"--name", sonarContainer,
		"--hostname", "sonarqube",
		"--network", netName,
		"-p", "127.0.0.1:9000:9000",
		"-e", "SONAR_JDBC_URL=jdbc:postgresql://postgres:5432/sonar",
		"-e", "SONAR_JDBC_USERNAME=sonar",
		"-e", "SONAR_JDBC_PASSWORD=sonar",
		"-v", "fleet-sonarqube-data:/opt/sonarqube/data:z",
		"-v", "fleet-sonarqube-extensions:/opt/sonarqube/extensions:z",
		"-v", "fleet-sonarqube-logs:/opt/sonarqube/logs:z",
		"fleet-sonarqube:latest",
	}

	checkSonarImg := CommandContext(ctx, "podman", "image", "exists", "fleet-sonarqube:latest")
	if checkSonarImg.Run() == nil {
		checkSonar := CommandContext(ctx, "podman", "container", "exists", sonarContainer)
		if err := checkSonar.Run(); err != nil {
			cmd := CommandContext(ctx, "podman", sonarArgs...)
			if out, err := cmd.CombinedOutput(); err != nil {
				if IsRootlessNetnsError(err, out) {
					_ = EnsureRootlessNetNS(ctx)
					retryCmd := CommandContext(ctx, "podman", sonarArgs...)
					if retryOut, retryErr := retryCmd.CombinedOutput(); retryErr != nil {
						return fmt.Errorf("failed starting sonarqube container: %v (%s)", retryErr, string(retryOut))
					}
				} else {
					return fmt.Errorf("failed starting sonarqube container: %v (%s)", err, string(out))
				}
			}
		} else {
			startCmd := CommandContext(ctx, "podman", "start", sonarContainer)
			if startOut, err := startCmd.CombinedOutput(); err != nil {
				_ = CommandContext(ctx, "podman", "rm", "-f", sonarContainer).Run()
				if IsRootlessNetnsError(err, startOut) {
					_ = EnsureRootlessNetNS(ctx)
				}
				cmd := CommandContext(ctx, "podman", sonarArgs...)
				if out, err := cmd.CombinedOutput(); err != nil {
					return fmt.Errorf("failed restarting sonarqube container: %v (%s)", err, string(out))
				}
			}
		}
		_ = BootstrapSonar(ctx, adminPass)
	}

	return nil
}

// BootstrapForgejo initializes the admin user, default organization, and standard repositories in Forgejo
func BootstrapForgejo(ctx context.Context, adminPass string) error {
	if adminPass == "" {
		adminPass = os.Getenv("FORGEJO_ADMIN_PASSWORD")
	}
	if adminPass == "" {
		adminPass = os.Getenv("GITEA_ADMIN_PASSWORD")
	}
	if adminPass == "" {
		return fmt.Errorf("forgejo admin password is required but was empty")
	}

	forgejoURL := os.Getenv("FORGEJO_URL")
	if forgejoURL == "" {
		forgejoURL = os.Getenv("GITEA_URL")
	}
	if forgejoURL == "" {
		forgejoURL = "http://127.0.0.1:3000"
	}

	adminUser := os.Getenv("FORGEJO_ADMIN_USER")
	if adminUser == "" {
		adminUser = os.Getenv("GITEA_ADMIN_USER")
	}
	if adminUser == "" {
		adminUser = "forgejoadmin"
	}

	// Wait up to 10 seconds for Forgejo HTTP service to become responsive
	httpClient := &http.Client{Timeout: 1 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, forgejoURL+"/api/v1/version", nil)
		resp, err := httpClient.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			ready = true
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	if !ready {
		return fmt.Errorf("timed out waiting for forgejo service")
	}

	// Ensure admin user via container CLI (using forgejo CLI, fallback to gitea CLI if needed)
	createAdminCmd := CommandContext(ctx, "podman", "exec", "-i", infraForgejoContainer,
		"forgejo", "admin", "user", "create",
		"--admin",
		"--username", adminUser,
		"--password-stdin",
		"--email", fmt.Sprintf("%s@local.fleet", adminUser),
		"--must-change-password=false",
	)
	createAdminCmd.Stdin = strings.NewReader(adminPass + "\n")
	_ = createAdminCmd.Run()

	// Ensure password is sync'd in case user already existed with different password
	changePassCmd := CommandContext(ctx, "podman", "exec", "-i", infraForgejoContainer,
		"forgejo", "admin", "user", "change-password",
		"--username", adminUser,
		"--password-stdin",
		"--must-change-password=false",
	)
	changePassCmd.Stdin = strings.NewReader(adminPass + "\n")
	_ = changePassCmd.Run()

	// Also ensure legacy admin 'giteaadmin' has access if migrating
	if adminUser != "giteaadmin" {
		createLegacyAdminCmd := CommandContext(ctx, "podman", "exec", "-i", infraForgejoContainer,
			"forgejo", "admin", "user", "create",
			"--admin",
			"--username", "giteaadmin",
			"--password-stdin",
			"--email", "giteaadmin@local.fleet",
			"--must-change-password=false",
		)
		createLegacyAdminCmd.Stdin = strings.NewReader(adminPass + "\n")
		_ = createLegacyAdminCmd.Run()

		changeLegacyPassCmd := CommandContext(ctx, "podman", "exec", "-i", infraForgejoContainer,
			"forgejo", "admin", "user", "change-password",
			"--username", "giteaadmin",
			"--password-stdin",
			"--must-change-password=false",
		)
		changeLegacyPassCmd.Stdin = strings.NewReader(adminPass + "\n")
		_ = changeLegacyPassCmd.Run()
	}

	// Ensure fleet organization and repos via API
	client := forgejo.NewClient(forgejo.ClientConfig{
		BaseURL:   forgejoURL,
		AdminUser: adminUser,
		AdminPass: adminPass,
		Timeout:   3 * time.Second,
	})

	_ = client.EnsureOrg(ctx, "fleet")
	_ = client.EnsureRepo(ctx, "fleet", "tools", "Fleet shared utility repositories", true)
	_ = client.EnsureRepo(ctx, "fleet", "tasks", "Fleet task tracking backlog", true)

	return nil
}

// BootstrapGitea aliases BootstrapForgejo for backward compatibility.
func BootstrapGitea(ctx context.Context, adminPass string) error {
	return BootstrapForgejo(ctx, adminPass)
}

// BootstrapSonar transitions SonarQube default credentials and ensures the admin user is configured.
func BootstrapSonar(ctx context.Context, adminPass string) error {
	adminPass = config.FormatSonarAdminPassword(adminPass)

	sonarURL := os.Getenv("SONAR_HOST_URL")
	if sonarURL == "" {
		sonarURL = os.Getenv("SONARQUBE_URL")
	}
	if sonarURL == "" {
		sonarURL = "http://127.0.0.1:9000"
	}

	httpClient := &http.Client{Timeout: 1 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, sonarURL+"/api/system/status", nil)
		resp, err := httpClient.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			var status struct {
				Status string `json:"status"`
			}
			if decodeErr := json.NewDecoder(resp.Body).Decode(&status); decodeErr == nil && status.Status == "UP" {
				_ = resp.Body.Close()
				ready = true
				break
			}
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	if !ready {
		return fmt.Errorf("timed out waiting for sonarqube service")
	}

	// First, check if already authenticated with target adminPass
	checkClient := sonar.NewClient(sonar.ClientConfig{
		BaseURL:   sonarURL,
		AdminUser: "admin",
		AdminPass: adminPass,
		Timeout:   2 * time.Second,
	})
	if _, err := checkClient.GetSystemStatus(ctx); err == nil {
		// Test authentication with a protected endpoint
		reqAuth, _ := http.NewRequestWithContext(ctx, http.MethodGet, sonarURL+"/api/users/current", nil)
		reqAuth.SetBasicAuth("admin", adminPass)
		if respAuth, errAuth := httpClient.Do(reqAuth); errAuth == nil {
			_ = respAuth.Body.Close()
			if respAuth.StatusCode == http.StatusOK {
				return nil
			}
		}
	}

	// Try transitioning from default "admin:admin" to target adminPass
	defaultClient := sonar.NewClient(sonar.ClientConfig{
		BaseURL:   sonarURL,
		AdminUser: "admin",
		AdminPass: "admin",
		Timeout:   3 * time.Second,
	})
	_ = defaultClient.ChangePassword(ctx, "admin", "admin", adminPass)

	return nil
}

// StopInfraStack halts shared infrastructure containers
func StopInfraStack(ctx context.Context) error {
	_ = CommandContext(ctx, "podman", "stop", infraValkeyContainer).Run()
	_ = CommandContext(ctx, "podman", "stop", infraForgejoContainer).Run()
	_ = CommandContext(ctx, "podman", "stop", "agent-sandbox-gitea").Run()
	_ = CommandContext(ctx, "podman", "stop", infraPostgresContainer).Run()
	_ = CommandContext(ctx, "podman", "stop", infraSonarContainer).Run()
	_ = CommandContext(ctx, "podman", "stop", infraOpenRouterContainer).Run()
	return nil
}

// InspectInfraStack returns the runtime state of Valkey, Forgejo, PostgreSQL, and SonarQube containers
func InspectInfraStack(ctx context.Context) ([]ContainerInfo, error) {
	containers := []string{infraValkeyContainer, infraForgejoContainer, infraPostgresContainer, infraSonarContainer, infraOpenRouterContainer}
	var results []ContainerInfo
	for _, c := range containers {
		info, err := InspectAgentContainer(ctx, c)
		if err == nil && info != nil {
			results = append(results, *info)
		} else {
			results = append(results, ContainerInfo{
				ID:    c,
				State: "stopped",
			})
		}
	}
	return results, nil
}

// FormatSSHConfigBlock returns a standard OpenSSH host block string.
func FormatSSHConfigBlock(name string, port int, keyFile string) string {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/tmp"
	}
	knownHostsFile := filepath.Join(home, ".ssh", fmt.Sprintf("known_hosts.fleet-%s", name))
	return fmt.Sprintf("Host fleet-%s\n    HostName 127.0.0.1\n    Port %d\n    User agent\n    IdentityFile %s\n    IdentitiesOnly yes\n    UserKnownHostsFile %s\n    StrictHostKeyChecking accept-new\n", name, port, keyFile, knownHostsFile)
}

// SyncSSHConfigFile updates the managed SSH config file with Host blocks for all currently running agents.
func SyncSSHConfigFile(ctx context.Context, paths config.Paths) error {
	configs, err := config.ListAgentConfigs(paths)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	buf.WriteString("# Fleet Auto-Generated SSH Configuration\n")
	buf.WriteString("# Managed automatically by fleet. Do not edit manually.\n\n")

	ideKeyFile := paths.GetSSHPrivateKey()
	if ideKeyFile == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			home = "/tmp"
		}
		ideKeyFile = filepath.Join(home, ".ssh", "fleet")
	}
	buf.WriteString("Match host 127.0.0.1,localhost user agent\n")
	buf.WriteString(fmt.Sprintf("    IdentityFile %s\n", ideKeyFile))
	buf.WriteString("    IdentitiesOnly yes\n")
	buf.WriteString("    StrictHostKeyChecking accept-new\n\n")

	runningCount := 0
	for _, c := range configs {
		port, err := GetAgentSSHPort(ctx, c.ContainerName)
		if err == nil && port > 0 {
			if runningCount > 0 {
				buf.WriteString("\n")
			}
			buf.WriteString(FormatSSHConfigBlock(c.Name, port, ideKeyFile))
			runningCount++
		}
	}

	if paths.SSHConfigFile == "" {
		paths.SSHConfigFile = filepath.Join(paths.DataHome, "ssh_config")
	}
	if dir := filepath.Dir(paths.SSHConfigFile); dir != "" {
		_ = os.MkdirAll(dir, 0700)
	}
	return os.WriteFile(paths.SSHConfigFile, buf.Bytes(), 0600)
}

// UpdateSSHConfig updates the managed SSH config file (alias for SyncSSHConfigFile).
func UpdateSSHConfig(ctx context.Context, paths config.Paths) error {
	return SyncSSHConfigFile(ctx, paths)
}

// StartOpenRouterContainer starts or creates the dedicated fleet-openrouter-proxy container on fleet-infra
func StartOpenRouterContainer(ctx context.Context, paths config.Paths) error {
	apiKey := config.GetOpenRouterAPIKey(paths)
	if apiKey == "" {
		return nil
	}

	netName := infraNetwork
	if err := EnsureNetwork(ctx, netName); err != nil {
		return fmt.Errorf("failed ensuring network %s: %w", netName, err)
	}

	ctrName := infraOpenRouterContainer
	args := []string{
		"run", "-d",
		"--name", ctrName,
		"--hostname", ctrName,
		"--network", netName,
		"-p", "127.0.0.1:8080:8080",
		"-e", "OPENROUTER_API_KEY",
		"fleet-openrouter-proxy:latest",
	}

	checkCmd := CommandContext(ctx, "podman", "container", "exists", ctrName)
	if err := checkCmd.Run(); err != nil {
		cmd := CommandContext(ctx, "podman", args...)
		cmd.Env = append(cmd.Environ(), fmt.Sprintf("OPENROUTER_API_KEY=%s", apiKey))
		if out, err := cmd.CombinedOutput(); err != nil {
			if IsRootlessNetnsError(err, out) {
				_ = EnsureRootlessNetNS(ctx)
				retryCmd := CommandContext(ctx, "podman", args...)
				retryCmd.Env = append(retryCmd.Environ(), fmt.Sprintf("OPENROUTER_API_KEY=%s", apiKey))
				if retryOut, retryErr := retryCmd.CombinedOutput(); retryErr != nil {
					return fmt.Errorf("failed starting openrouter container: %v (%s)", retryErr, string(retryOut))
				}
			} else {
				return fmt.Errorf("failed starting openrouter container: %v (%s)", err, string(out))
			}
		}
	} else {
		startCmd := CommandContext(ctx, "podman", "start", ctrName)
		if startOut, err := startCmd.CombinedOutput(); err != nil {
			_ = CommandContext(ctx, "podman", "rm", "-f", ctrName).Run()
			if IsRootlessNetnsError(err, startOut) {
				_ = EnsureRootlessNetNS(ctx)
			}
			cmd := CommandContext(ctx, "podman", args...)
			cmd.Env = append(cmd.Environ(), fmt.Sprintf("OPENROUTER_API_KEY=%s", apiKey))
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("failed restarting openrouter container: %v (%s)", err, string(out))
			}
		}
	}
	return nil
}

// RecreateValkeyContainer stops, removes, and recreates the shared Valkey container with updated ACL and volume mounts.
func RecreateValkeyContainer(ctx context.Context, paths config.Paths, adminPass, humanPass, humanName string) error {
	_ = ensureEnvPasswords(paths, &adminPass, &humanPass)

	netName := infraNetwork
	if err := EnsureNetwork(ctx, netName); err != nil {
		return fmt.Errorf("failed ensuring network %s: %w", netName, err)
	}

	valkeyContainer := infraValkeyContainer
	valkeyVol := "fleet-valkey-data"
	if valkeyContainer != "fleet-valkey" {
		valkeyVol = valkeyContainer + "-data"
	}
	_ = EnsureVolume(ctx, valkeyVol)

	valkeyConfigDir := filepath.Join(paths.DataHome, "valkey")
	_ = os.MkdirAll(valkeyConfigDir, 0700)
	aclFile := filepath.Join(valkeyConfigDir, "valkey-users.acl")

	if humanName == "" {
		humanName = "operator"
	}

	aclContent := fmt.Sprintf(`user default off
user admin on >%s ~* &* +@all
user %s on >%s ~%s:* ~human:name ~liaison:current ~poll-interval ~identity:* ~specialty:* %%R~*:* &* +@all (+xadd ~*:inbox)
`, adminPass, humanName, humanPass, humanName)
	_ = os.WriteFile(aclFile, []byte(aclContent), 0600)
	_ = os.Chmod(aclFile, 0600)
	_ = os.Chmod(valkeyConfigDir, 0700)

	valkeyPort := os.Getenv("BP_PORT")
	if valkeyPort == "" {
		valkeyPort = "6379"
	}

	valkeyArgs := []string{
		"run", "-d",
		"--name", valkeyContainer,
		"--hostname", "valkey",
		"--network", netName,
		"-p", fmt.Sprintf("127.0.0.1:%s:6379", valkeyPort),
		"-v", fmt.Sprintf("%s:/etc/valkey:z", valkeyConfigDir),
		"-v", fmt.Sprintf("%s:/data:z", valkeyVol),
		"docker.io/valkey/valkey:8.0-alpine",
		"valkey-server", "--aclfile", "/etc/valkey/valkey-users.acl", "--appendonly", "yes", "--port", "6379",
	}

	_ = CommandContext(ctx, "podman", "rm", "-f", valkeyContainer).Run()

	cmd := CommandContext(ctx, "podman", valkeyArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		if IsRootlessNetnsError(err, out) {
			_ = EnsureRootlessNetNS(ctx)
			retryCmd := CommandContext(ctx, "podman", valkeyArgs...)
			if retryOut, retryErr := retryCmd.CombinedOutput(); retryErr != nil {
				return fmt.Errorf("failed starting valkey container: %v (%s)", retryErr, string(retryOut))
			}
		} else {
			return fmt.Errorf("failed starting valkey container: %v (%s)", err, string(out))
		}
	}
	return nil
}
