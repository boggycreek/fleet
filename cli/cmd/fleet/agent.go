// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Package main implements the host-side management CLI for Agent Sandbox.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goRuntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
	"github.com/boggycreek/fleet/cli/pkg/config"
	"github.com/boggycreek/fleet/cli/pkg/devcontainer"
	"github.com/boggycreek/fleet/cli/pkg/doctor"
	"github.com/boggycreek/fleet/cli/pkg/ide"
	"github.com/boggycreek/fleet/cli/pkg/lifecycle"
	"github.com/boggycreek/fleet/cli/pkg/plugin"
	"github.com/boggycreek/fleet/cli/pkg/runtime"
)

var defaultStdin io.Reader = os.Stdin

// SetStdinForTesting replaces defaultStdin for testing and returns a restore function.
func SetStdinForTesting(r io.Reader) func() {
	orig := defaultStdin
	defaultStdin = r
	return func() { defaultStdin = orig }
}

var systemURILauncher = func() string {
	if goRuntime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}

// SetSystemURILauncherForTesting allows tests to mock the system URL launcher command.
func SetSystemURILauncherForTesting(fn func() string) func() {
	orig := systemURILauncher
	systemURILauncher = fn
	return func() { systemURILauncher = orig }
}

var isTerminal = func(r io.Reader) bool {
	if r == nil {
		return false
	}
	if term, ok := r.(interface{ IsTerminal() bool }); ok {
		return term.IsTerminal()
	}
	if f, ok := r.(*os.File); ok {
		st, err := f.Stat()
		if err != nil {
			return false
		}
		return (st.Mode() & os.ModeCharDevice) != 0
	}
	return false
}

// SetIsTerminalForTesting replaces the package-level isTerminal checker and returns a restore function.
func SetIsTerminalForTesting(fn func(io.Reader) bool) func() {
	orig := isTerminal
	isTerminal = fn
	return func() { isTerminal = orig }
}

func handleAgent(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Usage: %s agent <create|provision|start|tmux|open|ssh|ssh-config|keys|doctor|stop|list|clean|retire|deprovision|clone|remote>\n", bin)
		return 1
	}

	sub := strings.ToLower(args[0])
	subArgs := args[1:]

	switch sub {
	case "help", "-h", "--help":
		fmt.Fprintf(stdout, `Usage: %s agent <command> [args...]

Commands:
  create <name> [as <type|oci>] [--image <type|oci>] [--role <role>] [--host] [--no-container] [--baremetal --ssh-host <host>] [--pwd <path>] [--model-url <url>] [--model-name <name|preset>] [--model-key <key>]
    Provision a new named agent (container sandbox, host-connected container with --host, no-container with --no-container, or baremetal physical machine with --baremetal). Supports model presets (lfm-3b, lfm-7b, lfm-40b-moe).

  provision <name>
    Provision or re-provision agent infrastructure, keys, Valkey ACLs, Forgejo accounts, and SonarQube credentials.

  start <name>
    Start the agent container with Podman.

  connect <name>
    Attach directly to a host-connected tool container shell.

  tmux <name>
    Attach interactively to the agent container's tmux supervisor.

  open <name> [in <ide>] [--no-launch]
    Launch desktop IDE remote development environment (VS Code, GoLand, CLion, WebStorm, PyCharm, etc.).

  ssh <name>
    Connect directly via SSH to the agent's unprivileged environment.

  ssh-config [name] [--all]
    Generate OpenSSH host configuration stanza(s) for IDE remote development.

  keys [name]
    Inspect cryptographic signing keys, public keys, and infrastructure credentials for agents.

  doctor <name>
    Diagnose configuration, cryptographic keys, storage, and infrastructure provisioning, and auto-heal defects.

  stop [name] [--all]
    Stop a running agent container (or all agents with --all).

  list [--json]
    List all configured agents, container states, images, and SSH endpoints.

  clean <name>
    Remove the agent container while preserving its home directory volume.

  retire <name> [--force]
    Fully decommission agent across the system (container, volume, local secrets, Valkey ACLs, and Forgejo account).

  deprovision <name> [--force]
    Alias to retire: fully deprovision agent across container, volume, local secrets, Valkey ACLs, and Forgejo account.

  clone <name> <repo-url> [dir]
    Clone a git repository directly into the agent container's workspace.

  remote <name> [get|list|set <remote-name> <url>]
    Inspect or configure git remotes inside the agent container's workspace.`, binaryName())
		return 0
	case "tmux":
		return handleAgentTmux(ctx, paths, subArgs, stdout, stderr)
	case "connect":
		return handleAgentConnect(ctx, paths, subArgs, stdout, stderr)
	case "ssh":
		return handleAgentSSH(ctx, paths, subArgs, stdout, stderr)
	case "create", "provision", "start", "open", "ssh-config", "sshconfig", "keys", "doctor", "stop", "list", "clean", "retire", "deprovision", "clone", "remote":
		opCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
		defer cancel()
		switch sub {
		case "create":
			return handleAgentCreate(opCtx, paths, subArgs, stdout, stderr)
		case "provision":
			return handleAgentProvision(opCtx, paths, subArgs, stdout, stderr)
		case "start":
			return handleAgentStart(opCtx, paths, subArgs, stdout, stderr)
		case "open":
			return handleAgentOpen(opCtx, paths, subArgs, defaultStdin, stdout, stderr)
		case "ssh-config", "sshconfig":
			return handleAgentSSHConfig(opCtx, paths, subArgs, stdout, stderr)
		case "keys":
			return handleAgentKeys(opCtx, paths, subArgs, stdout, stderr)
		case "doctor":
			return handleAgentDoctor(opCtx, paths, subArgs, stdout, stderr)
		case "stop":
			return handleAgentStop(opCtx, paths, subArgs, stdout, stderr)
		case "list":
			return handleAgentList(opCtx, paths, subArgs, stdout, stderr)
		case "clean":
			return handleAgentClean(opCtx, paths, subArgs, stdout, stderr)
		case "retire":
			return handleAgentRetire(opCtx, paths, subArgs, stdout, stderr)
		case "deprovision":
			return handleAgentDeprovision(opCtx, paths, subArgs, stdout, stderr)
		case "clone":
			return handleAgentClone(opCtx, paths, subArgs, stdout, stderr)
		case "remote":
			return handleAgentRemote(opCtx, paths, subArgs, stdout, stderr)
		}
		return 1
	default:
		fmt.Fprintf(stderr, "%s agent: unknown command %q\n", bin, sub)
		return 1
	}
}

func handleAgentCreate(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Usage: %s agent create <name> [as <type|oci>] [--image <type|oci>] [--role <role>] [--pod] [--host] [--no-container] [--pwd <path>] [--model-url <url>] [--model-name <name>] [--model-key <key>]\n", bin)
		return 1
	}

	agentName := args[0]
	var image string
	var role string
	var modelURL string
	var modelName string
	var modelKey string

	// Check natural language 'as <type>'
	if len(args) >= 3 && strings.ToLower(args[1]) == "as" {
		image = args[2]
	}

	// Flag parsing for overrides
	isHostContainer := false
	isNoContainer := false
	isBaremetal := false
	var sshHost string
	sshPort := 22
	var sshUser string
	enrollPort := 8765
	var controllerHost string
	noEnroll := false
	usePod := os.Getenv("FLEET_ENABLE_PODS") == "1"
	var pwdPath string
	var dockerfilePath string
	var engineType string
	fs := flag.NewFlagSet("agent create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&image, "image", image, "OCI image or preset (base, tool, core, native, opencode, claude, agy, pig)")
	fs.StringVar(&dockerfilePath, "dockerfile", "", "Path to custom Dockerfile or Containerfile to build")
	fs.StringVar(&dockerfilePath, "f", "", "Path to custom Dockerfile or Containerfile to build (shorthand)")
	fs.StringVar(&engineType, "engine", "fleet-agent", "Cognitive engine harness for custom composed image (default, claude, opencode, pig, agy)")
	fs.StringVar(&role, "role", "coding-agent", "Role metadata description")
	fs.StringVar(&modelURL, "model-url", os.Getenv("OPENAI_BASE_URL"), "OpenAI-compatible inference endpoint URL")
	fs.StringVar(&modelName, "model-name", os.Getenv("OPENAI_MODEL"), "Target model name or preset (lfm-3b, lfm-7b, lfm-40b-moe)")
	fs.StringVar(&modelKey, "model-key", os.Getenv("OPENAI_API_KEY"), "Model API key (optional)")
	fs.StringVar(&modelKey, "model-api-key", os.Getenv("OPENAI_API_KEY"), "Model API key (optional)")
	fs.BoolVar(&usePod, "pod", usePod, "Co-locate agent and sidecars in a Podman pod (ADR 00053)")
	fs.BoolVar(&usePod, "use-pod", usePod, "Co-locate agent and sidecars in a Podman pod (ADR 00053)")
	fs.BoolVar(&isHostContainer, "host", false, "Provision host-connected container agent with workspace projection")
	fs.BoolVar(&isNoContainer, "no-container", false, "Provision agent without a container (direct host execution)")
	fs.BoolVar(&isBaremetal, "baremetal", false, "Provision agent directly on baremetal physical host")
	fs.StringVar(&sshHost, "ssh-host", "", "Target baremetal machine IP or hostname for SSH access")
	fs.IntVar(&sshPort, "ssh-port", 22, "Target baremetal machine SSH port")
	fs.StringVar(&sshUser, "ssh-user", "", "Target baremetal machine SSH user (default: current host user)")
	fs.IntVar(&enrollPort, "enroll-port", 8765, "Ephemeral enrollment server HTTP port")
	fs.StringVar(&controllerHost, "controller-host", "", "Controller host IP or LAN hostname accessible from baremetal node")
	fs.BoolVar(&noEnroll, "no-enroll", false, "Skip waiting for ephemeral enrollment connection")
	fs.StringVar(&pwdPath, "pwd", "", "Host working directory to project into container workspace (default: current directory)")

	var flagArgs []string
	if len(args) >= 3 && strings.ToLower(args[1]) == "as" {
		flagArgs = args[3:]
	} else if len(args) > 1 {
		flagArgs = args[1:]
	}
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}

	if isBaremetal && (isHostContainer || isNoContainer) {
		fmt.Fprintf(stderr, "%s error: cannot combine --baremetal with --host or --no-container\n", bin)
		return 1
	}
	if isBaremetal && strings.TrimSpace(sshHost) == "" {
		fmt.Fprintf(stderr, "%s error: --ssh-host <host> is required for baremetal agents\n", bin)
		return 1
	}

	if isHostContainer && isNoContainer {
		fmt.Fprintf(stderr, "%s error: cannot specify both --host and --no-container\n", bin)
		return 1
	}

	var absPwd string
	if isHostContainer {
		if pwdPath == "" {
			cwd, err := os.Getwd()
			if err != nil {
				fmt.Fprintf(stderr, "%s error resolving working directory: %v\n", bin, err)
				return 1
			}
			pwdPath = cwd
		}
		var err error
		absPwd, err = filepath.Abs(pwdPath)
		if err != nil {
			fmt.Fprintf(stderr, "%s error resolving absolute path for %q: %v\n", bin, pwdPath, err)
			return 1
		}
		st, err := os.Stat(absPwd)
		if err != nil || !st.IsDir() {
			fmt.Fprintf(stderr, "%s error: workspace directory %q does not exist or is not a directory\n", bin, absPwd)
			return 1
		}
		realPwd, err := filepath.EvalSymlinks(absPwd)
		if err != nil {
			fmt.Fprintf(stderr, "%s error resolving symlinks for %q: %v\n", bin, absPwd, err)
			return 1
		}
		cleanPwd := filepath.Clean(realPwd)

		// Disallow projecting root, system paths, or user home directly
		homeDir, _ := os.UserHomeDir()
		if cleanPwd == "/" {
			fmt.Fprintf(stderr, "%s error: cannot project root filesystem '/' as workspace directory\n", bin)
			return 1
		}
		if homeDir != "" && cleanPwd == filepath.Clean(homeDir) {
			fmt.Fprintf(stderr, "%s error: projecting user home directory %q directly is not permitted (specify a project subdirectory via --pwd)\n", bin, cleanPwd)
			return 1
		}
		for _, sysDir := range []string{"/etc", "/usr", "/bin", "/sbin", "/var", "/lib", "/dev", "/proc", "/sys", "/boot"} {
			if cleanPwd == sysDir || strings.HasPrefix(cleanPwd, sysDir+"/") {
				fmt.Fprintf(stderr, "%s error: cannot project system directory %q as workspace directory\n", bin, cleanPwd)
				return 1
			}
		}
		absPwd = cleanPwd
	}

	contextDir := absPwd
	if contextDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			contextDir = cwd
		}
	}

	// Discover local Dockerfile or devcontainer conventions if custom build requested or default empty
	localDockerfile, localDevcontainer := findLocalAgentDockerfile(contextDir)

	shouldBuildCustom := dockerfilePath != "" ||
		strings.EqualFold(image, "devcontainer") ||
		strings.EqualFold(image, "auto") ||
		strings.EqualFold(image, "custom")

	if !shouldBuildCustom && image == "" && (localDockerfile != "" || localDevcontainer != "") {
		shouldBuildCustom = true
	}

	resolvedImage := image
	if shouldBuildCustom && !isNoContainer && !isBaremetal {
		targetDockerfile := dockerfilePath
		if targetDockerfile == "" {
			if localDockerfile != "" {
				targetDockerfile = localDockerfile
			} else if localDevcontainer != "" {
				genOut := filepath.Join(filepath.Dir(localDevcontainer), "Containerfile.fleet")
				content, res, genErr := devcontainer.GenerateContainerfileFromFile(localDevcontainer, engineType)
				if genErr != nil {
					fmt.Fprintf(stderr, "%s error generating Containerfile from %s: %v\n", bin, localDevcontainer, genErr)
					return 1
				}
				if res != nil && len(res.Warnings) > 0 {
					for _, w := range res.Warnings {
						fmt.Fprintf(stderr, "%s warning: %s\n", bin, w)
					}
				}
				_ = os.MkdirAll(filepath.Dir(genOut), 0755)
				if writeErr := os.WriteFile(genOut, []byte(content), 0644); writeErr != nil {
					fmt.Fprintf(stderr, "%s error saving Containerfile: %v\n", bin, writeErr)
					return 1
				}
				targetDockerfile = genOut
			}
		}

		if targetDockerfile == "" {
			fmt.Fprintf(stderr, "%s error: no Dockerfile or .devcontainer found in %s to build custom image\n", bin, contextDir)
			return 1
		}

		customTag := fmt.Sprintf("fleet-custom-%s:latest", agentName)
		fmt.Fprintf(stdout, "Building custom composed image %s using %s...\n", customTag, targetDockerfile)
		buildCmd := runtime.CommandContext(ctx, "podman", "build", "-t", customTag, "-f", targetDockerfile, contextDir)
		buildCmd.Stdout = stdout
		buildCmd.Stderr = stderr
		if err := buildCmd.Run(); err != nil {
			fmt.Fprintf(stderr, "%s error: failed building custom composed image %s: %v\n", bin, customTag, err)
			return 1
		}
		resolvedImage = customTag
		image = customTag
	} else if image == "" {
		if isHostContainer {
			image = "tool"
		} else if isBaremetal {
			image = "baremetal"
		} else {
			image = "base"
		}
		resolvedImage = image
	}

	if !shouldBuildCustom && !isNoContainer && !isBaremetal {
		var isLocal bool
		resolvedImage, isLocal = runtime.ResolveAgentImage(ctx, image)
		if resolvedImage == "" {
			fmt.Fprintf(stderr, "%s error: unable to resolve image %q\n", bin, image)
			return 1
		}
		_ = isLocal
	}

	cfg, err := config.NewAgentConfig(agentName, resolvedImage, role, modelURL, modelName, modelKey)
	if err != nil {
		fmt.Fprintf(stderr, "%s error: %v\n", bin, err)
		return 1
	}
	cfg.UsePod = usePod
	cfg.PodName = runtime.AgentPodName(agentName)

	if engineType != "" && engineType != "fleet-agent" && engineType != "default" {
		cfg.Harness = engineType
	}
	lowerHarness := strings.ToLower(strings.TrimSpace(image))
	switch lowerHarness {
	case "agy", "claude", "opencode", "pig":
		cfg.Harness = lowerHarness
	}

	if isHostContainer {
		cfg.Runtime = "host-container"
		cfg.WorkspacePath = absPwd
		cfg.ContainerName = fmt.Sprintf("fleet-agent-%s", cfg.Name)
		cfg.VolumeName = fmt.Sprintf("fleet-agent-%s-home", cfg.Name)
	} else if isNoContainer {
		cfg.Runtime = "no-container"
		cfg.HostOnly = true
		cfg.ContainerName = ""
		cfg.VolumeName = ""
		cfg.Image = "no-container"
	} else if isBaremetal {
		cfg.Runtime = "baremetal"
		cfg.Baremetal = true
		cfg.SSHHost = strings.TrimSpace(sshHost)
		if sshPort <= 0 {
			sshPort = 22
		}
		cfg.SSHPort = sshPort
		cleanUser := strings.TrimSpace(sshUser)
		if cleanUser == "" {
			cleanUser = os.Getenv("USER")
			if cleanUser == "" {
				cleanUser = "agent"
			}
		}
		cfg.SSHUser = cleanUser
		cfg.ContainerName = ""
		cfg.VolumeName = ""
		cfg.Image = "baremetal"
	}

	if err := config.SaveAgentConfig(cfg, paths); err != nil {
		fmt.Fprintf(stderr, "%s error saving config: %v\n", bin, err)
		return 1
	}

	report := lifecycle.ProvisionAgent(ctx, cfg, paths)

	fmt.Fprintf(stdout, "Agent %q created successfully.\n", cfg.Name)
	if cfg.IsNoContainer() {
		fmt.Fprintf(stdout, "  Runtime:    no-container (direct host execution, no container)\n")
		fmt.Fprintf(stdout, "  Role:       %s\n", cfg.Role)
		fmt.Fprintf(stdout, "  BP Profile: %s\n", filepath.Join(paths.BPProfilesDir, fmt.Sprintf("%s.env", cfg.Name)))
		fmt.Fprintln(stdout, "\nWARNING: No-container agent runs directly on the host without container isolation. Full access to host filesystem and credentials is permitted.")
	} else if cfg.IsBaremetal() {
		fmt.Fprintf(stdout, "  Runtime:    baremetal (physical host execution)\n")
		fmt.Fprintf(stdout, "  SSH Target: %s@%s:%d\n", cfg.SSHUser, cfg.SSHHost, cfg.SSHPort)
		fmt.Fprintf(stdout, "  Role:       %s\n", cfg.Role)
		if cfg.ModelURL != "" {
			fmt.Fprintf(stdout, "  Model URL:  %s\n", cfg.ModelURL)
		}
		if cfg.ModelName != "" {
			fmt.Fprintf(stdout, "  Model Name: %s\n", cfg.ModelName)
		}
		fmt.Fprintf(stdout, "  BP Profile: %s\n", filepath.Join(paths.BPProfilesDir, fmt.Sprintf("%s.env", cfg.Name)))
	} else if cfg.IsHostContainer() {
		fmt.Fprintf(stdout, "  Runtime:    host-container (host workspace projection)\n")
		fmt.Fprintf(stdout, "  Workspace:  %s\n", cfg.WorkspacePath)
		fmt.Fprintf(stdout, "  Image:      %s\n", cfg.Image)
		fmt.Fprintf(stdout, "  Role:       %s\n", cfg.Role)
		if cfg.ModelURL != "" {
			fmt.Fprintf(stdout, "  Model URL:  %s\n", cfg.ModelURL)
		}
		if cfg.ModelName != "" {
			fmt.Fprintf(stdout, "  Model Name: %s\n", cfg.ModelName)
		}
		fmt.Fprintf(stdout, "  Container:  %s\n", cfg.ContainerName)
		fmt.Fprintf(stdout, "  Volume:     %s\n", cfg.VolumeName)
		fmt.Fprintf(stdout, "  BP Profile: %s\n", filepath.Join(paths.BPProfilesDir, fmt.Sprintf("%s.env", cfg.Name)))
	} else {
		fmt.Fprintf(stdout, "  Image:      %s\n", cfg.Image)
		fmt.Fprintf(stdout, "  Role:       %s\n", cfg.Role)
		if cfg.ModelURL != "" {
			fmt.Fprintf(stdout, "  Model URL:  %s\n", cfg.ModelURL)
		}
		if cfg.ModelName != "" {
			fmt.Fprintf(stdout, "  Model Name: %s\n", cfg.ModelName)
		}
		fmt.Fprintf(stdout, "  Container:  %s\n", cfg.ContainerName)
		fmt.Fprintf(stdout, "  Volume:     %s\n", cfg.VolumeName)
	}
	if formatted := lifecycle.FormatReport(report); formatted != "" {
		fmt.Fprintln(stdout, "Infrastructure Provisioning:")
		fmt.Fprint(stdout, formatted)
	}
	if report.HasErrors() {
		fmt.Fprintf(stderr, "%s error: agent %q provisioned with errors; rolling back partial state\n", bin, cfg.Name)
		_ = lifecycle.DeprovisionAgent(ctx, cfg, paths)
		return 1
	}
	if cfg.IsNoContainer() {
		fmt.Fprintf(stdout, "To use:   bp --profile %s tell <recipient> <msg>\n", cfg.Name)
	} else if cfg.IsBaremetal() {
		if noEnroll {
			fmt.Fprintf(stdout, "\nBaremetal agent %q provisioned (enrollment skipped via --no-enroll).\n", cfg.Name)
			return 0
		}

		lanHost := controllerHost
		if lanHost == "" {
			lanHost = lifecycle.DetectHostLANIP(cfg.SSHHost)
		}

		valkeyHost := lanHost
		if v := os.Getenv("VALKEY_HOST"); v != "" {
			valkeyHost = v
		} else if v := os.Getenv("BP_HOST"); v != "" && v != "127.0.0.1" && v != "localhost" {
			valkeyHost = v
		}

		valkeyPort := 6379
		if p := os.Getenv("BP_PORT"); p != "" {
			if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
				valkeyPort = parsed
			}
		}

		forgejoURL := os.Getenv("FORGEJO_URL")
		if forgejoURL == "" {
			forgejoURL = os.Getenv("GITEA_URL")
		}
		if forgejoURL == "" {
			forgejoURL = fmt.Sprintf("http://%s:3000", lanHost)
		} else {
			forgejoURL = lifecycle.RewriteURLForLAN(forgejoURL, lanHost)
		}

		modelURL := cfg.ModelURL
		if modelURL == "" {
			modelURL = os.Getenv("OPENAI_BASE_URL")
		}
		if modelURL == "" {
			modelURL = fmt.Sprintf("http://%s:8080/v1", lanHost)
		} else {
			modelURL = lifecycle.RewriteURLForLAN(modelURL, lanHost)
		}

		var controllerSSHPubKey string
		pubKeyCandidates := []string{
			paths.IDEKeyFile + ".pub",
		}
		if home, err := os.UserHomeDir(); err == nil {
			pubKeyCandidates = append(pubKeyCandidates, filepath.Join(home, ".ssh", "fleet.pub"))
		}
		for _, pubPath := range pubKeyCandidates {
			if data, err := os.ReadFile(pubPath); err == nil && len(bytes.TrimSpace(data)) > 0 {
				controllerSSHPubKey = strings.TrimSpace(string(data))
				break
			}
		}

		secrets := lifecycle.EnrollmentSecrets{
			AgentName:              cfg.Name,
			AgentRole:              cfg.Role,
			AgentSigningKeyPEM:     cfg.SigningKeyPEM,
			ControllerSSHPublicKey: controllerSSHPubKey,
			ValkeyHost:             valkeyHost,
			ValkeyPort:             valkeyPort,
			ValkeyPassword:         cfg.Password,
			ForgejoURL:             forgejoURL,
			OpenAIBaseURL:          modelURL,
			OpenAIModel:            cfg.ModelName,
			OpenAIAPIKey:           cfg.ModelAPIKey,
		}

		enrollServer, err := lifecycle.NewEnrollmentServer(lifecycle.EnrollmentConfig{
			AgentName: cfg.Name,
			Secrets:   secrets,
			Port:      enrollPort,
		})
		if err != nil {
			fmt.Fprintf(stderr, "%s error starting enrollment server: %v\n", bin, err)
			return 1
		}
		enrollServer.Start()
		defer func() { _ = enrollServer.Shutdown(context.Background()) }()

		installCmd := enrollServer.InstallCommand(lanHost)
		fmt.Fprintf(stdout, "\n============================================================\n")
		fmt.Fprintf(stdout, " BAREMETAL ENROLLMENT ACTIVE (Single-Use Token)\n")
		fmt.Fprintf(stdout, "============================================================\n")
		fmt.Fprintf(stdout, "Controller listening at: %s (TTL: 15m)\n", enrollServer.URL(lanHost))
		fmt.Fprintf(stdout, "To bootstrap and enroll on %s, run:\n\n", cfg.SSHHost)
		fmt.Fprintf(stdout, "  %s\n\n", installCmd)
		fmt.Fprintf(stdout, "Waiting for baremetal agent to collect credentials...\n")

		if err := enrollServer.Wait(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				fmt.Fprintf(stderr, "\n%s: enrollment timed out or cancelled\n", bin)
				return 1
			}
			fmt.Fprintf(stderr, "\n%s enrollment error: %v\n", bin, err)
			return 1
		}

		fmt.Fprintf(stdout, "[✓] Credentials collected and token burned successfully!\n")
		_ = enrollServer.WaitForCompletion(ctx)
		_ = enrollServer.Shutdown(context.Background())
		fmt.Fprintf(stdout, "Agent %q is now ready.\n", cfg.Name)
		fmt.Fprintf(stdout, "  SSH:   %s agent ssh %s\n", bin, cfg.Name)
		fmt.Fprintf(stdout, "  IDE:   %s agent open %s in vscode\n", bin, cfg.Name)
	} else {
		fmt.Fprintf(stdout, "To start: %s agent start %s\n", bin, cfg.Name)
	}
	return 0
}

func handleAgentStart(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Usage: %s agent start <name>\n", bin)
		return 1
	}

	name := args[0]
	cfg, err := config.LoadAgentConfig(name, paths)
	if err != nil {
		fmt.Fprintf(stderr, "%s error: %v\n", bin, err)
		return 1
	}

	if cfg.IsNoContainer() {
		fmt.Fprintf(stdout, "Agent %q is a no-container agent (no container required).\n", cfg.Name)
		fmt.Fprintf(stdout, "Use directly via: bp --profile %s <command>\n", cfg.Name)
		return 0
	}

	if cfg.IsBaremetal() {
		fmt.Fprintf(stdout, "Agent %q is a baremetal agent running via systemd on %s.\n", cfg.Name, cfg.SSHHost)
		fmt.Fprintf(stdout, "Manage on target machine via: systemctl --user status fleet-agent\n")
		return 0
	}

	bpCfg := libbp.LoadClientFromEnv()
	if err := runtime.StartAgentContainer(ctx, cfg, paths, bpCfg.Host, bpCfg.Port); err != nil {
		fmt.Fprintf(stderr, "%s error: %v\n", bin, err)
		return 1
	}

	if cfg.IsHostContainer() {
		lifecycle.WarnOnErr(stderr, syncAllSSHConfigs(ctx, paths), "sync ssh config")
		fmt.Fprintf(stdout, "Host agent %q started (%s) with workspace %s.\n", cfg.Name, cfg.ContainerName, cfg.WorkspacePath)
		fmt.Fprintf(stdout, "Connect: %s agent connect %s\n", bin, cfg.Name)
		fmt.Fprintf(stdout, "SSH:     %s agent ssh %s\n", bin, cfg.Name)
		fmt.Fprintf(stdout, "IDE:     %s agent open %s\n", bin, cfg.Name)
		return 0
	}

	lifecycle.WarnOnErr(stderr, syncAllSSHConfigs(ctx, paths), "sync ssh config")

	fmt.Fprintf(stdout, "Agent %q started (%s).\n", cfg.Name, cfg.ContainerName)
	fmt.Fprintf(stdout, "SSH:   %s agent ssh %s\n", bin, cfg.Name)
	fmt.Fprintf(stdout, "IDE:   %s agent open %s\n", bin, cfg.Name)
	fmt.Fprintf(stdout, "Tmux:  %s agent tmux %s\n", bin, cfg.Name)
	return 0
}

func handleAgentTmux(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Usage: %s agent tmux <name>\n", bin)
		return 1
	}
	name := args[0]
	cfg, err := config.LoadAgentConfig(name, paths)
	if err != nil {
		fmt.Fprintf(stderr, "%s error: %v\n", bin, err)
		return 1
	}

	if cfg.IsNoContainer() {
		fmt.Fprintf(stderr, "%s error: agent %q is a no-container agent (no container or tmux session)\n", bin, cfg.Name)
		return 1
	}

	if cfg.IsBaremetal() {
		fmt.Fprintf(stderr, "%s error: agent %q is a baremetal agent (runs headless daemon on %s)\n", bin, cfg.Name, cfg.SSHHost)
		return 1
	}
	if cfg.IsHostContainer() {
		fmt.Fprintf(stderr, "%s error: agent %q is a host tool container without tmux (connect via '%s agent connect %s')\n", bin, cfg.Name, bin, cfg.Name)
		return 1
	}

	cmd := runtime.CommandContext(ctx, "podman", "exec", "-it", cfg.ContainerName, "tmux", "attach")
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stderr, "%s tmux error: %v\n", bin, err)
		return 1
	}
	return 0
}

func handleAgentConnect(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Notice: '%s agent connect' is deprecated. Please use '%s agent tmux <name>' instead.\n", bin, bin)
		fmt.Fprintf(stderr, "Usage: %s agent connect <name>\n", bin)
		return 1
	}
	name := args[0]
	cfg, err := config.LoadAgentConfig(name, paths)
	if err == nil && cfg.IsBaremetal() {
		fmt.Fprintf(stderr, "%s error: agent %q is a baremetal agent (use 'ssh' to connect)\n", bin, cfg.Name)
		return 1
	}
	if err == nil && cfg.IsHostContainer() {
		info, err := runtime.InspectAgentContainer(ctx, cfg.ContainerName)
		if err != nil || info == nil || info.State != "running" {
			fmt.Fprintf(stderr, "%s error: container %s is not running (start via: %s agent start %s)\n", bin, cfg.ContainerName, bin, cfg.Name)
			return 1
		}
		cmd := runtime.CommandContext(ctx, "podman", "exec", "-it", "--user", "1000", cfg.ContainerName, "/bin/bash")
		cmd.Stdin = os.Stdin
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(stderr, "%s connect error: %v\n", bin, err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stderr, "Notice: '%s agent connect' is deprecated. Please use '%s agent tmux %s' instead.\n", bin, bin, args[0])
	return handleAgentTmux(ctx, paths, args, stdout, stderr)
}

func handleAgentOpen(ctx context.Context, paths config.Paths, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	bin := binaryName()
	if stdin == nil {
		stdin = defaultStdin
	}
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Usage: %s agent open <name> [in <ide>] [--no-launch]\n", bin)
		return 1
	}

	name := args[0]
	var ideName string
	var ideProvided bool
	var noLaunch bool

	var flagArgs []string
	raw := args[1:]
	for i := 0; i < len(raw); i++ {
		if strings.ToLower(raw[i]) == "in" {
			if i+1 >= len(raw) || strings.HasPrefix(raw[i+1], "-") {
				fmt.Fprintf(stderr, "Usage: %s agent open <name> [in <ide>] [--no-launch]\n", bin)
				return 1
			}
			ideProvided = true
			ideName = raw[i+1]
			i++
			continue
		}
		flagArgs = append(flagArgs, raw[i])
	}

	fs := flag.NewFlagSet("agent open", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&noLaunch, "no-launch", false, "Prepare SSH configuration without launching IDE")
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}

	if len(fs.Args()) > 0 {
		fmt.Fprintf(stderr, "Usage: %s agent open <name> [in <ide>] [--no-launch]\n", bin)
		return 1
	}

	var target ide.IDE
	if ideProvided {
		ideName = strings.ToLower(strings.TrimSpace(ideName))
		var installed bool
		target, installed = ide.Lookup(ideName)
		installedNames := ide.NamesInstalled()
		help := "(none detected)"
		if len(installedNames) > 0 {
			help = strings.Join(installedNames, ", ")
		}
		if target.Name == "" {
			fmt.Fprintf(stderr, "%s error: unknown IDE %q. Installed IDEs: %s\n", bin, ideName, help)
			return 1
		}
		if !installed {
			fmt.Fprintf(stderr, "%s error: %s (%s) is not installed on this host. Installed IDEs: %s\n",
				bin, target.DisplayName, target.BinaryName, help)
			return 1
		}
	} else {
		discovered := ide.Discover()
		if len(discovered) == 0 {
			fmt.Fprintf(stderr, "%s error: no supported IDEs detected on PATH (supported: code, cursor, goland, clion, webstorm, pycharm, idea, ...)\n", bin)
			return 1
		}

		if !isTerminal(stdin) {
			names := make([]string, 0, len(discovered))
			for _, d := range discovered {
				names = append(names, d.Name)
			}
			fmt.Fprintf(stderr, "%s error: target IDE is required. Specify 'in <ide>' with one of: %s\n", bin, strings.Join(names, ", "))
			return 1
		}

		fmt.Fprintf(stdout, "Select an IDE to open agent %q:\n", name)
		for i, d := range discovered {
			fmt.Fprintf(stdout, "  %d) %s (%s)\n", i+1, d.DisplayName, d.Path)
		}
		fmt.Fprintf(stdout, "Enter selection [1-%d]: ", len(discovered))

		scanner := bufio.NewScanner(stdin)
		if !scanner.Scan() {
			fmt.Fprintf(stderr, "%s error: invalid selection\n", bin)
			return 1
		}
		input := strings.TrimSpace(scanner.Text())
		choice, err := strconv.Atoi(input)
		if err != nil || choice < 1 || choice > len(discovered) {
			fmt.Fprintf(stderr, "%s error: invalid selection\n", bin)
			return 1
		}
		target = discovered[choice-1]
	}

	cfg, err := config.LoadAgentConfig(name, paths)
	if err != nil {
		fmt.Fprintf(stderr, "%s error: %v\n", bin, err)
		return 1
	}

	if cfg.IsNoContainer() {
		fmt.Fprintf(stderr, "%s error: agent %q is a no-container agent (runs directly on host, no container SSH IDE forwarding)\n", bin, cfg.Name)
		return 1
	}

	var port int
	if cfg.IsBaremetal() {
		port = cfg.SSHPort
		if port <= 0 {
			port = 22
		}
	} else {
		var err error
		port, err = runtime.GetAgentSSHPort(ctx, cfg.ContainerName)
		if err != nil || port <= 0 {
			bpCfg := libbp.LoadClientFromEnv()
			if startErr := runtime.StartAgentContainer(ctx, cfg, paths, bpCfg.Host, bpCfg.Port); startErr != nil {
				fmt.Fprintf(stderr, "%s error starting agent container %s: %v\n", bin, cfg.ContainerName, startErr)
				return 1
			}
			port, err = runtime.GetAgentSSHPort(ctx, cfg.ContainerName)
			if err != nil || port <= 0 {
				fmt.Fprintf(stderr, "%s error discovering SSH port for %s: %v\n", bin, cfg.Name, err)
				return 1
			}
		}
	}

	if err := syncAllSSHConfigs(ctx, paths); err != nil {
		fmt.Fprintf(stderr, "%s error syncing SSH config: %v\n", bin, err)
		return 1
	}

	hostAlias := ide.SSHHostTarget(name)

	// Build connection descriptors for each IDE family.
	vsCodeURI := fmt.Sprintf("vscode-remote://ssh-remote+%s/home/agent/workspace", hostAlias)
	jbSSHURI := fmt.Sprintf("ssh://agent@localhost:%d/home/agent/workspace", port)
	jbGatewayURI := fmt.Sprintf("jetbrains://gateway/ssh/environment?h=127.0.0.1&u=agent&p=%d&launchIde=true&projectHint=/home/agent/workspace", port)
	jbConnectURI := fmt.Sprintf("jetbrains-gateway://connect#host=127.0.0.1&port=%d&user=agent&type=ssh&projectPath=/home/agent/workspace", port)

	if cfg.IsBaremetal() {
		sshUser := cfg.SSHUser
		if sshUser == "" {
			if u := os.Getenv("USER"); u != "" {
				sshUser = u
			} else {
				sshUser = "agent"
			}
		}
		sshHost := cfg.SSHHost
		if sshHost == "" {
			sshHost = "127.0.0.1"
		}
		remoteWorkspace := fmt.Sprintf("/home/%s/workspace", sshUser)
		vsCodeURI = fmt.Sprintf("vscode-remote://ssh-remote+%s%s", hostAlias, remoteWorkspace)
		jbSSHURI = fmt.Sprintf("ssh://%s@%s:%d%s", sshUser, sshHost, port, remoteWorkspace)
		jbGatewayURI = fmt.Sprintf("jetbrains://gateway/ssh/environment?h=%s&u=%s&p=%d&launchIde=true&projectHint=%s", sshHost, sshUser, port, remoteWorkspace)
		jbConnectURI = fmt.Sprintf("jetbrains-gateway://connect#host=%s&port=%d&user=%s&type=ssh&projectPath=%s", sshHost, port, sshUser, remoteWorkspace)
	}

	if noLaunch {
		fmt.Fprintf(stdout, "SSH host alias prepared: %s (Port: %d)\n", hostAlias, port)
		switch target.Family {
		case ide.FamilyVSCode:
			fmt.Fprintf(stdout, "%s Remote URI: %s\n", target.DisplayName, vsCodeURI)
			fmt.Fprintf(stdout, "Launch command: %s --file-uri %s\n", target.BinaryName, vsCodeURI)
		case ide.FamilyJetBrains:
			fmt.Fprintf(stdout, "Connect via %s with Host %q (Port: %d).\n", target.DisplayName, hostAlias, port)
			fmt.Fprintf(stdout, "JetBrains URI: %s\n", jbGatewayURI)
			fmt.Fprintf(stdout, "Gateway URI: %s\n", jbConnectURI)
			fmt.Fprintf(stdout, "SSH URI: %s\n", jbSSHURI)
			fmt.Fprintf(stdout, "Launch command: %s\n", target.BinaryName)
		}
		return 0
	}

	switch target.Family {
	case ide.FamilyVSCode:
		fmt.Fprintf(stdout, "Opening %s in %s (Host: %s)...\n", name, target.DisplayName, hostAlias)
		cmd := runtime.CommandContext(ctx, target.Path, "--file-uri", vsCodeURI)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(stderr, "%s error launching %s: %v\n", bin, target.DisplayName, err)
			return 1
		}
		// Best-effort: install the Claude Code extension if VS Code server is running.
		checkServer := runtime.CommandContext(ctx, "podman", "exec", cfg.ContainerName, "sh", "-c", "test -d /home/agent/.vscode-server")
		if err := checkServer.Run(); err == nil {
			installCmd := runtime.CommandContext(ctx, "podman", "exec", cfg.ContainerName, "sh", "-c",
				`find /home/agent/.vscode-server/bin -name "code-server" -o -name "code" 2>/dev/null | head -n 1 | xargs -r -I {} {} --install-extension anthropic.claude-code`)
			lifecycle.WarnOnErr(stderr, installCmd.Run(), "install claude-code extension")
		}

	case ide.FamilyJetBrains:
		fmt.Fprintf(stdout, "Opening %s in %s via Remote Development...\n", name, target.DisplayName)
		fmt.Fprintf(stdout, "JetBrains URI: %s\n", jbGatewayURI)
		fmt.Fprintf(stdout, "Connect via %s -> Remote Development -> SSH (Host: %s, Port: %d).\n", target.DisplayName, hostAlias, port)

		launcher := systemURILauncher()
		cmd := runtime.CommandContext(ctx, launcher, jbGatewayURI)
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Run(); err != nil {
			// Fallback: direct IDE binary execution
			fallbackCmd := runtime.CommandContext(ctx, target.Path)
			fallbackCmd.Stdout = stdout
			fallbackCmd.Stderr = stderr
			if fallbackErr := fallbackCmd.Run(); fallbackErr != nil {
				fmt.Fprintf(stderr, "%s error launching %s: %v\n", bin, target.DisplayName, fallbackErr)
				return 1
			}
		}
	}

	return 0
}

func syncAllSSHConfigs(ctx context.Context, paths config.Paths) error {
	if err := runtime.SyncSSHConfigFile(ctx, paths); err != nil {
		return err
	}
	_ = plugin.EnsureSSHConfigManagedHosts(ctx, paths, "")
	_ = plugin.SyncToolboxSSHSettings(ctx, paths, "")
	return nil
}

func handleAgentSSH(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Usage: %s agent ssh <name>\n", bin)
		return 1
	}
	name := args[0]
	cfg, err := config.LoadAgentConfig(name, paths)
	if err != nil {
		fmt.Fprintf(stderr, "%s error: %v\n", bin, err)
		return 1
	}

	if cfg.IsNoContainer() {
		fmt.Fprintf(stderr, "%s error: agent %q is a no-container agent (runs directly on host, no SSH connection required)\n", bin, cfg.Name)
		return 1
	}

	var sshArgs []string
	if cfg.IsBaremetal() {
		sshHost := cfg.SSHHost
		if sshHost == "" {
			sshHost = "127.0.0.1"
		}
		port := cfg.SSHPort
		if port <= 0 {
			port = 22
		}
		sshUser := cfg.SSHUser
		if sshUser == "" {
			sshUser = os.Getenv("USER")
			if sshUser == "" {
				sshUser = "agent"
			}
		}
		sshArgs = []string{
			"-i", paths.IDEKeyFile,
			"-p", fmt.Sprintf("%d", port),
			"-o", "StrictHostKeyChecking=accept-new",
			fmt.Sprintf("%s@%s", sshUser, sshHost),
		}
	} else {
		port, err := runtime.GetAgentSSHPort(ctx, cfg.ContainerName)
		if err != nil {
			fmt.Fprintf(stderr, "%s error discovering SSH port for %s: %v\n", bin, cfg.Name, err)
			return 1
		}

		sshArgs = []string{
			"-i", paths.IDEKeyFile,
			"-p", fmt.Sprintf("%d", port),
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"agent@localhost",
		}
	}

	cmd := runtime.CommandContext(ctx, "ssh", sshArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return 1
	}
	return 0
}

func handleAgentSSHConfig(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	var targetAgent string
	for _, a := range args {
		if a != "--all" && !strings.HasPrefix(a, "-") && targetAgent == "" {
			targetAgent = a
		}
	}

	if targetAgent != "" {
		cfg, err := config.LoadAgentConfig(targetAgent, paths)
		if err != nil {
			fmt.Fprintf(stderr, "%s error: %v\n", bin, err)
			return 1
		}
		if cfg.IsBaremetal() {
			fmt.Fprint(stdout, runtime.FormatBaremetalSSHConfigBlock(cfg, paths.IDEKeyFile))
			return 0
		}
		port, err := runtime.GetAgentSSHPort(ctx, cfg.ContainerName)
		if err != nil || port <= 0 {
			fmt.Fprintf(stderr, "%s error: agent %q is not running or SSH port is unavailable\n", bin, cfg.Name)
			return 1
		}
		printSSHConfigBlock(stdout, cfg.Name, port, paths.IDEKeyFile)
		return 0
	}

	configs, err := config.ListAgentConfigs(paths)
	if err != nil {
		fmt.Fprintf(stderr, "%s error: %v\n", bin, err)
		return 1
	}

	runningCount := 0
	for _, c := range configs {
		if c.IsBaremetal() {
			if runningCount > 0 {
				fmt.Fprintln(stdout)
			}
			fmt.Fprint(stdout, runtime.FormatBaremetalSSHConfigBlock(c, paths.IDEKeyFile))
			runningCount++
			continue
		}
		port, err := runtime.GetAgentSSHPort(ctx, c.ContainerName)
		if err == nil && port > 0 {
			if runningCount > 0 {
				fmt.Fprintln(stdout)
			}
			printSSHConfigBlock(stdout, c.Name, port, paths.IDEKeyFile)
			runningCount++
		}
	}

	return 0
}

func printSSHConfigBlock(w io.Writer, name string, port int, keyFile string) {
	fmt.Fprint(w, runtime.FormatSSHConfigBlock(name, port, keyFile))
}

func handleAgentDoctor(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Usage: %s agent doctor <name>\n", bin)
		return 1
	}

	name := args[0]
	report, err := doctor.DiagnoseAndHealAgent(ctx, name, paths)
	if err != nil && report == nil {
		fmt.Fprintf(stderr, "%s doctor error: %v\n", bin, err)
		return 1
	}

	fmt.Fprint(stdout, doctor.FormatDoctorReport(report))
	if report.UnrepairableCount > 0 {
		return 1
	}
	return 0
}

func handleAgentStop(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Usage: %s agent stop <name> [--all]\n", bin)
		return 1
	}

	if args[0] == "--all" {
		configs, _ := config.ListAgentConfigs(paths)
		for _, c := range configs {
			if c.IsNoContainer() || c.IsBaremetal() {
				continue
			}
			lifecycle.WarnOnErr(stderr, runtime.StopAgentContainer(ctx, c.ContainerName), "stop container "+c.ContainerName)
			fmt.Fprintf(stdout, "Stopped %s\n", c.Name)
		}
		lifecycle.WarnOnErr(stderr, syncAllSSHConfigs(ctx, paths), "sync ssh config")
		return 0
	}

	name := args[0]
	cfg, err := config.LoadAgentConfig(name, paths)
	if err != nil {
		fmt.Fprintf(stderr, "%s error: %v\n", bin, err)
		return 1
	}

	if cfg.IsNoContainer() {
		fmt.Fprintf(stdout, "Agent %q is a no-container agent (no container to stop).\n", cfg.Name)
		return 0
	}

	if cfg.IsBaremetal() {
		fmt.Fprintf(stdout, "Agent %q is a baremetal agent running via systemd on %s.\n", cfg.Name, cfg.SSHHost)
		fmt.Fprintf(stdout, "Stop on target machine via: systemctl --user stop fleet-agent\n")
		return 0
	}

	if err := runtime.StopAgentContainer(ctx, cfg.ContainerName); err != nil {
		fmt.Fprintf(stderr, "%s error stopping %s: %v\n", bin, cfg.Name, err)
		return 1
	}
	lifecycle.WarnOnErr(stderr, syncAllSSHConfigs(ctx, paths), "sync ssh config")
	fmt.Fprintf(stdout, "Agent %q stopped.\n", cfg.Name)
	return 0
}

func handleAgentClean(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Usage: %s agent clean <name>\n", bin)
		return 1
	}
	name := args[0]
	cfg, err := config.LoadAgentConfig(name, paths)
	if err != nil {
		fmt.Fprintf(stderr, "%s error: %v\n", bin, err)
		return 1
	}

	if cfg.IsNoContainer() {
		fmt.Fprintf(stderr, "%s error: agent %q is a no-container agent (no container or home volume to clean; use '%s agent retire %s' to deprovision)\n", bin, cfg.Name, bin, cfg.Name)
		return 1
	}

	if cfg.IsBaremetal() {
		fmt.Fprintf(stdout, "Agent %q is a baremetal agent (no container to clean).\n", cfg.Name)
		return 0
	}

	if err := runtime.CleanAgentContainer(ctx, cfg.ContainerName); err != nil {
		fmt.Fprintf(stderr, "%s error cleaning %s: %v\n", bin, cfg.Name, err)
		return 1
	}
	lifecycle.WarnOnErr(stderr, syncAllSSHConfigs(ctx, paths), "sync ssh config")
	fmt.Fprintf(stdout, "Agent container %q removed (home volume preserved).\n", cfg.ContainerName)
	return 0
}

func handleAgentRetire(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Usage: %s agent retire <name> [--force]\n", bin)
		return 1
	}

	var name string
	force := false
	for _, arg := range args {
		if arg == "--force" || arg == "-f" {
			force = true
		} else if name == "" {
			name = strings.ToLower(strings.TrimSpace(arg))
		}
	}

	if name == "" {
		fmt.Fprintf(stderr, "Usage: %s agent retire <name> [--force]\n", bin)
		return 1
	}
	cfg, err := config.LoadAgentConfig(name, paths)
	if err != nil {
		if force {
			// If forcing, synthesize a minimal config so deprovisioning proceeds
			cfg = &config.AgentConfig{
				Name:          name,
				ContainerName: fmt.Sprintf("fleet-agent-%s", name),
				VolumeName:    fmt.Sprintf("fleet-agent-%s-home", name),
			}
			fmt.Fprintf(stderr, "%s warning: agent %q config not found, proceeding with forced cleanup\n", bin, name)
		} else {
			fmt.Fprintf(stderr, "%s error: %v\n", bin, err)
			return 1
		}
	}

	report := lifecycle.DeprovisionAgent(ctx, cfg, paths)

	if report.HasErrors() {
		fmt.Fprintf(stdout, "Agent %q retired with errors.\n", name)
	} else {
		fmt.Fprintf(stdout, "Agent %q retired and deprovisioned successfully.\n", name)
	}
	fmt.Fprint(stdout, lifecycle.FormatReport(report))
	if report.HasErrors() {
		fmt.Fprintf(stderr, "%s warning: agent %q deprovisioned with errors\n", bin, name)
		if !force {
			return 1
		}
	}
	return 0
}

func handleAgentProvision(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Usage: %s agent provision <name>\n", bin)
		return 1
	}

	name := strings.ToLower(strings.TrimSpace(args[0]))
	cfg, err := config.LoadAgentConfig(name, paths)
	if err != nil {
		fmt.Fprintf(stderr, "%s error: %v\n", bin, err)
		return 1
	}

	report := lifecycle.ProvisionAgent(ctx, cfg, paths)
	fmt.Fprintf(stdout, "Provisioning report for agent %q:\n", cfg.Name)
	if formatted := lifecycle.FormatReport(report); formatted != "" {
		fmt.Fprint(stdout, formatted)
	}
	if report.HasErrors() {
		fmt.Fprintf(stderr, "%s warning: agent %q provisioned with errors; run '%s agent doctor %s' to diagnose\n", bin, cfg.Name, bin, cfg.Name)
		return 1
	}
	fmt.Fprintf(stdout, "Agent %q is fully provisioned.\n", cfg.Name)
	return 0
}

func handleAgentDeprovision(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	return handleAgentRetire(ctx, paths, args, stdout, stderr)
}

func handleAgentKeys(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	var targetAgent string
	if len(args) > 0 {
		targetAgent = strings.ToLower(strings.TrimSpace(args[0]))
	}

	configs, err := config.ListAgentConfigs(paths)
	if err != nil {
		fmt.Fprintf(stderr, "%s error listing agents: %v\n", bin, err)
		return 1
	}

	found := false
	for _, cfg := range configs {
		if targetAgent != "" && cfg.Name != targetAgent {
			continue
		}
		found = true
		secretKeyPath := filepath.Join(paths.SecretsDir, cfg.Name, "signing-key.pem")
		keyStatus := "missing"
		if fi, err := os.Stat(secretKeyPath); err == nil && !fi.IsDir() {
			keyStatus = fmt.Sprintf("present (%s)", secretKeyPath)
		}

		fmt.Fprintf(stdout, "Agent %q:\n", cfg.Name)
		fmt.Fprintf(stdout, "  Signing Key: %s\n", keyStatus)
		fmt.Fprintf(stdout, "  Public Key:  %s\n", cfg.PublicKeyB64)
		if cfg.Password != "" {
			fmt.Fprintf(stdout, "  Valkey ACL:  user configured\n")
		} else {
			fmt.Fprintf(stdout, "  Valkey ACL:  no password set\n")
		}
		if cfg.SonarToken != "" {
			fmt.Fprintf(stdout, "  SonarQube:   analysis token generated\n")
		}
		fmt.Fprintln(stdout)
	}

	if targetAgent != "" && !found {
		fmt.Fprintf(stderr, "%s error: agent %q not found\n", bin, targetAgent)
		return 1
	}
	return 0
}

func handleAgentList(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	jsonOutput := false
	for _, a := range args {
		if a == "--json" {
			jsonOutput = true
		}
	}

	configs, err := config.ListAgentConfigs(paths)
	if err != nil {
		fmt.Fprintf(stderr, "%s error: %v\n", bin, err)
		return 1
	}

	statuses := make([]runtime.AgentStatus, 0, len(configs))
	for _, c := range configs {
		if c.IsNoContainer() {
			statuses = append(statuses, runtime.AgentStatus{
				Name:           c.Name,
				Role:           c.Role,
				Image:          "no-container",
				ContainerName:  "-",
				ContainerState: "HOST",
				SSHPort:        0,
				IDEConnect:     "-",
			})
			continue
		}

		if c.IsBaremetal() {
			port := c.SSHPort
			if port <= 0 {
				port = 22
			}
			user := c.SSHUser
			if user == "" {
				user = "agent"
			}
			statuses = append(statuses, runtime.AgentStatus{
				Name:           c.Name,
				Role:           c.Role,
				Image:          "baremetal",
				ContainerName:  "-",
				ContainerState: "BAREMETAL",
				SSHPort:        port,
				IDEConnect:     fmt.Sprintf("%s@%s:%d", user, c.SSHHost, port),
			})
			continue
		}

		state := "stopped"
		info, err := runtime.InspectAgentContainer(ctx, c.ContainerName)
		if err == nil && info != nil {
			state = info.State
		}

		sshPort := 0
		ideConnect := ""
		if state == "running" {
			if p, err := runtime.GetAgentSSHPort(ctx, c.ContainerName); err == nil && p > 0 {
				sshPort = p
				ideConnect = fmt.Sprintf("agent@localhost:%d", p)
			} else if c.IsHostContainer() {
				ideConnect = fmt.Sprintf("host:%s", c.WorkspacePath)
			}
		} else if c.IsHostContainer() {
			ideConnect = c.WorkspacePath
		}

		statuses = append(statuses, runtime.AgentStatus{
			Name:           c.Name,
			Role:           c.Role,
			Image:          c.Image,
			ContainerName:  c.ContainerName,
			ContainerState: state,
			SSHPort:        sshPort,
			IDEConnect:     ideConnect,
		})
	}

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(statuses)
		return 0
	}

	fmtFmt := "%-16s %-14s %-12s %-26s %s\n"
	fmt.Fprintf(stdout, fmtFmt, "AGENT_NAME", "ROLE", "STATUS", "IMAGE", "IDE_SSH")
	for _, s := range statuses {
		ide := s.IDEConnect
		if ide == "" {
			ide = "-"
		}
		fmt.Fprintf(stdout, fmtFmt, s.Name, s.Role, s.ContainerState, s.Image, ide)
	}
	return 0
}

func handleAgentClone(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) < 2 {
		fmt.Fprintf(stderr, "Usage: %s agent clone <name> <repo-url> [dir]\n", bin)
		return 1
	}

	name := strings.ToLower(strings.TrimSpace(args[0]))
	repoURL := strings.TrimSpace(args[1])
	targetDir := ""
	if len(args) >= 3 {
		targetDir = strings.TrimSpace(args[2])
	}

	cfg, err := config.LoadAgentConfig(name, paths)
	if err != nil {
		fmt.Fprintf(stderr, "%s error: %v\n", bin, err)
		return 1
	}

	if cfg.IsHost() {
		fmt.Fprintf(stderr, "%s error: agent %q is a host agent (runs directly on host filesystem; use host 'git clone' directly)\n", bin, cfg.Name)
		return 1
	}

	// Verify container is running or start it
	checkRunning := runtime.CommandContext(ctx, "podman", "container", "inspect", "--format", "{{.State.Running}}", cfg.ContainerName)
	out, err := checkRunning.Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		bpCfg := libbp.LoadClientFromEnv()
		if startErr := runtime.StartAgentContainer(ctx, cfg, paths, bpCfg.Host, bpCfg.Port); startErr != nil {
			fmt.Fprintf(stderr, "%s error starting agent container %s: %v\n", bin, cfg.ContainerName, startErr)
			return 1
		}
	}

	// Validate targetDir if provided
	if targetDir != "" {
		cleaned := filepath.Clean(filepath.Join("/home/agent/workspace", targetDir))
		if !strings.HasPrefix(cleaned, "/home/agent/workspace") {
			fmt.Fprintf(stderr, "%s error: target directory must remain within /home/agent/workspace\n", bin)
			return 1
		}
	}

	cloneArgs := []string{"exec", "--user", "1000", "-w", "/home/agent/workspace", cfg.ContainerName, "git", "clone", "--", repoURL}
	if targetDir != "" {
		cloneArgs = append(cloneArgs, targetDir)
	}

	cmd := runtime.CommandContext(ctx, "podman", cloneArgs...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stderr, "%s error: git clone failed: %v\n", bin, err)
		return 1
	}

	fmt.Fprintf(stdout, "Repository %s successfully cloned into %s workspace.\n", repoURL, cfg.Name)
	return 0
}

func handleAgentRemote(ctx context.Context, paths config.Paths, args []string, stdout, stderr io.Writer) int {
	bin := binaryName()
	if len(args) == 0 {
		fmt.Fprintf(stderr, "Usage: %s agent remote <name> [get|list|set <remote-name> <url>] [--dir <dir>]\n", bin)
		return 1
	}

	name := strings.ToLower(strings.TrimSpace(args[0]))
	cfg, err := config.LoadAgentConfig(name, paths)
	if err != nil {
		fmt.Fprintf(stderr, "%s error: %v\n", bin, err)
		return 1
	}

	if cfg.IsHost() {
		fmt.Fprintf(stderr, "%s error: agent %q is a host agent (runs directly on host filesystem; use host 'git remote' directly)\n", bin, cfg.Name)
		return 1
	}

	// Verify container is running or start it
	checkRunning := runtime.CommandContext(ctx, "podman", "container", "inspect", "--format", "{{.State.Running}}", cfg.ContainerName)
	out, err := checkRunning.Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		bpCfg := libbp.LoadClientFromEnv()
		if startErr := runtime.StartAgentContainer(ctx, cfg, paths, bpCfg.Host, bpCfg.Port); startErr != nil {
			fmt.Fprintf(stderr, "%s error starting agent container %s: %v\n", bin, cfg.ContainerName, startErr)
			return 1
		}
	}

	subCmd := "list"
	targetDir := ""
	var cleanArgs []string
	for i := 1; i < len(args); i++ {
		if args[i] == "--dir" && i+1 < len(args) {
			targetDir = args[i+1]
			i++
		} else if strings.HasPrefix(args[i], "--dir=") {
			targetDir = strings.TrimPrefix(args[i], "--dir=")
		} else {
			cleanArgs = append(cleanArgs, args[i])
		}
	}

	if len(cleanArgs) >= 1 {
		subCmd = strings.ToLower(strings.TrimSpace(cleanArgs[0]))
	}

	// Auto-detect git repo in workspace or immediate subdirectories if not specified
	workDir := "/home/agent/workspace"
	if targetDir != "" {
		cleaned := filepath.Clean(filepath.Join("/home/agent/workspace", targetDir))
		if !strings.HasPrefix(cleaned, "/home/agent/workspace") {
			fmt.Fprintf(stderr, "%s error: target directory must remain within /home/agent/workspace\n", bin)
			return 1
		}
		workDir = cleaned
	} else {
		// Test if /home/agent/workspace is a git repo
		checkGit := runtime.CommandContext(ctx, "podman", "exec", "--user", "1000", "-w", "/home/agent/workspace", cfg.ContainerName, "git", "rev-parse", "--is-inside-work-tree")
		if err := checkGit.Run(); err != nil {
			// Find first subdirectory with .git
			findGit := runtime.CommandContext(ctx, "podman", "exec", "--user", "1000", "-w", "/home/agent/workspace", cfg.ContainerName, "sh", "-c", "find . -maxdepth 2 -name .git -type d | head -n 1")
			if out, err := findGit.Output(); err == nil && len(strings.TrimSpace(string(out))) > 0 {
				detected := strings.TrimSpace(string(out))
				detected = strings.TrimSuffix(detected, "/.git")
				detected = strings.TrimPrefix(detected, "./")
				if detected != "" && detected != "." {
					workDir = filepath.Join("/home/agent/workspace", detected)
				}
			}
		}
	}

	var gitArgs []string
	switch subCmd {
	case "list":
		gitArgs = []string{"exec", "--user", "1000", "-w", workDir, cfg.ContainerName, "git", "remote", "-v"}
	case "get":
		remoteName := "origin"
		if len(cleanArgs) >= 2 {
			remoteName = strings.TrimSpace(cleanArgs[1])
		}
		gitArgs = []string{"exec", "--user", "1000", "-w", workDir, cfg.ContainerName, "git", "remote", "get-url", "--", remoteName}
	case "set":
		if len(cleanArgs) < 3 {
			fmt.Fprintf(stderr, "Usage: %s agent remote <name> set <remote-name> <url> [--dir <dir>]\n", bin)
			return 1
		}
		remoteName := strings.TrimSpace(cleanArgs[1])
		remoteURL := strings.TrimSpace(cleanArgs[2])
		// Try set-url first, if that fails, add
		setURLCmd := runtime.CommandContext(ctx, "podman", "exec", "--user", "1000", "-w", workDir, cfg.ContainerName, "git", "remote", "set-url", "--", remoteName, remoteURL)
		if err := setURLCmd.Run(); err != nil {
			gitArgs = []string{"exec", "--user", "1000", "-w", workDir, cfg.ContainerName, "git", "remote", "add", "--", remoteName, remoteURL}
		} else {
			fmt.Fprintf(stdout, "Updated remote %q to %s (in %s)\n", remoteName, remoteURL, workDir)
			return 0
		}
	default:
		fmt.Fprintf(stderr, "%s agent remote: unknown subcommand %q\n", bin, subCmd)
		return 1
	}

	cmd := runtime.CommandContext(ctx, "podman", gitArgs...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stderr, "%s error: git remote operation failed: %v\n", bin, err)
		return 1
	}
	return 0
}

func findLocalAgentDockerfile(dir string) (string, string) {
	if dir == "" {
		return "", ""
	}
	candidates := []string{
		filepath.Join(dir, ".fleet", "Dockerfile"),
		filepath.Join(dir, ".sndbx", "Dockerfile"),
		filepath.Join(dir, ".devcontainer", "Containerfile.fleet"),
		filepath.Join(dir, ".devcontainer", "Dockerfile"),
		filepath.Join(dir, "Containerfile"),
		filepath.Join(dir, "Dockerfile"),
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, ""
		}
	}
	if devPath, err := devcontainer.FindDevcontainerFile(dir); err == nil {
		return "", devPath
	}
	return "", ""
}
