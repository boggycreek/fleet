// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/boggycreek/sandbox/backplane/pkg/libbp"
)

func main() {
	os.Exit(Run(os.Args[1:], os.Stdout, os.Stderr))
}

// readPayload reads message payload data from the specified file path or stdin ('-')
func readPayload(path string, stdin io.Reader) ([]byte, error) {
	if path == "-" {
		if stdin == nil {
			return nil, fmt.Errorf("stdin is nil")
		}
		return io.ReadAll(stdin)
	}
	return os.ReadFile(path)
}

const maxSummaryRunes = 256

// extractSummary finds the first non-empty line of the payload as a summary, capped at maxSummaryRunes
func extractSummary(data []byte) (string, error) {
	for _, line := range bytes.Split(data, []byte("\n")) {
		trimmed := strings.TrimSpace(string(line))
		if trimmed != "" {
			runes := []rune(trimmed)
			if len(runes) > maxSummaryRunes {
				return string(runes[:maxSummaryRunes-3]) + "...", nil
			}
			return trimmed, nil
		}
	}
	return "", errors.New("file is empty or contains only whitespace")
}

// Run executes the CLI command with the provided args and I/O streams
func Run(args []string, stdout, stderr io.Writer) int {
	return RunWithIO(args, os.Stdin, stdout, stderr)
}

// RunWithIO executes the CLI command with custom stdin, stdout, and stderr
func RunWithIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return RunWithContext(sigCtx, args, stdin, stdout, stderr)
}

// RunWithContext executes the CLI command with an explicit context and custom I/O
func RunWithContext(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stdout)
		return 1
	}

	loadDefaultEnv()

	// Parse optional --profile flag from any argument position before subcommand dispatch
	var profileName string
	var remainingArgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--profile" || arg == "-profile" {
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "bp: --profile flag requires an argument")
				return 1
			}
			profileName = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--profile=") {
			profileName = strings.TrimPrefix(arg, "--profile=")
		} else if strings.HasPrefix(arg, "-profile=") {
			profileName = strings.TrimPrefix(arg, "-profile=")
		} else {
			remainingArgs = append(remainingArgs, arg)
		}
	}

	if len(remainingArgs) == 0 {
		printUsage(stdout)
		return 1
	}

	cmd := strings.ToLower(remainingArgs[0])
	cmdArgs := remainingArgs[1:]

	// Check for help --ai or --help --ai
	if (cmd == "help" || cmd == "-h" || cmd == "--help") && len(cmdArgs) > 0 && strings.ToLower(cmdArgs[0]) == "--ai" {
		printAIHelp(stdout)
		return 0
	}

	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		printUsage(stdout)
		return 0
	}

	// Dispatch profile management command without requiring a valid connection profile
	if cmd == "profile" {
		return handleProfile(cmdArgs, stdout, stderr)
	}

	// Resolve configuration with profile
	if profileName == "" {
		profileName = os.Getenv("BP_PROFILE")
	}
	if profileName == "" {
		profileName = "default"
	}

	cfg, err := libbp.LoadClientWithProfile(profileName)
	if err != nil {
		fmt.Fprintf(stderr, "bp error: %v\n", err)
		return 2
	}

	defaultTimeout := 30 * time.Second
	if cmd == "recv" {
		fs := flag.NewFlagSet("recv", flag.ContinueOnError)
		fs.SetOutput(stderr)
		blockSec := fs.Int("block", 0, "Block for N seconds waiting for messages")
		jsonOutput := fs.Bool("json", false, "Output in JSON format")
		if err := fs.Parse(cmdArgs); err != nil {
			return 1
		}
		if *blockSec > 0 {
			defaultTimeout = time.Duration(*blockSec)*time.Second + 15*time.Second
		}
		cmdCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
		return handleRecvParsed(cmdCtx, cfg, *blockSec, *jsonOutput, stdout, stderr)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	switch cmd {
	case "whoami":
		return handleWhoami(cfg, stdout)

	case "specialty":
		return handleSpecialty(cmdCtx, cfg, cmdArgs, stdout, stderr)

	case "say":
		return handleSay(cmdCtx, cfg, cmdArgs, stdin, stdout, stderr)

	case "post":
		return handlePost(cmdCtx, cfg, cmdArgs, stdin, stdout, stderr)

	case "cat", "get":
		return handleCat(cmdCtx, cfg, cmdArgs, stdout, stderr)

	case "tell":
		return handleTell(cmdCtx, cfg, cmdArgs, stdin, stdout, stderr)

	case "reply":
		return handleReply(cmdCtx, cfg, cmdArgs, stdout, stderr)

	case "human":
		return handleHuman(cmdCtx, cfg, cmdArgs, stdout, stderr)

	case "peers":
		return handlePeers(cmdCtx, cfg, cmdArgs, stdout, stderr)

	case "status":
		return handleStatus(cmdCtx, cfg, cmdArgs, stdout, stderr)

	case "finger":
		return handleFinger(cmdCtx, cfg, cmdArgs, stdout, stderr)

	case "liaison":
		return handleLiaison(cmdCtx, cfg, cmdArgs, stdout, stderr)

	case "interval":
		return handleInterval(cmdCtx, cfg, cmdArgs, stdout, stderr)

	case "ensemble":
		return handleEnsemble(cmdCtx, cfg, cmdArgs, stdin, stdout, stderr)

	case "state":
		return handleState(cmdArgs, stdout, stderr)

	default:
		fmt.Fprintf(stderr, "bp: unknown command %q (see 'bp help')\n", cmd)
		return 1
	}
}

func handleWhoami(cfg libbp.ClientConfig, stdout io.Writer) int {
	mode := cfg.Mode
	if mode == "" {
		mode = "agent"
		if cfg.AgentID == "operator" {
			mode = "human"
		}
	}
	pubKeyB64 := "none"
	if cfg.SigningKey != nil {
		pubKeyB64 = libbp.EncodePublicKeyBase64(cfg.SigningKey.Public().(ed25519.PublicKey))
	}

	fmt.Fprintf(stdout, "Agent ID:   %s\n", cfg.AgentID)
	fmt.Fprintf(stdout, "Mode:       %s\n", mode)
	fmt.Fprintf(stdout, "Host:       %s:%d\n", cfg.Host, cfg.Port)
	fmt.Fprintf(stdout, "Public Key: %s\n", pubKeyB64)
	return 0
}

func handleProfile(args []string, stdout, stderr io.Writer) int {
	sub := "list"
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}

	switch sub {
	case "list":
		home, _ := os.UserHomeDir()
		if home == "" {
			home = "/tmp"
		}
		xdgState := os.Getenv("XDG_STATE_HOME")
		if xdgState == "" {
			xdgState = filepath.Join(home, ".local", "state")
		}
		profilesDir := filepath.Join(xdgState, "bp", "profiles")

		entries, err := os.ReadDir(profilesDir)
		if err != nil || len(entries) == 0 {
			fmt.Fprintln(stdout, "Available profiles:")
			fmt.Fprintln(stdout, "  default (active fallback)")
			return 0
		}

		fmt.Fprintln(stdout, "Available profiles:")
		hasDefault := false
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".env") {
				name := strings.TrimSuffix(e.Name(), ".env")
				if name == "" {
					continue
				}
				if name == "default" {
					hasDefault = true
				}
				fmt.Fprintf(stdout, "  %s\n", name)
			}
		}
		if !hasDefault {
			fmt.Fprintln(stdout, "  default (operator fallback)")
		}
		return 0

	default:
		fmt.Fprintf(stderr, "bp: unknown profile subcommand %q (expected 'list')\n", sub)
		return 1
	}
}

func printUsage(out io.Writer) {
	fmt.Fprintln(out, `Usage: bp [--profile <name>] <command> [args...]

Commands:
  whoami                           Print current resolved identity, mode, and public key
  profile list                     List configured backplane connection profiles
  say <message> [--file <path>]    Broadcast message to the fleet (<id>:out)
  post <file> [to <agent>]         Publish long-form message with first line as summary
  cat <blob-key>                   Output parked payload blob to stdout (alias: get)
  tell <agent> [<msg>] [--file <path>] Direct point-to-point message (<peer>:inbox)
  reply <msgid> <message>          Reply in-thread to a specific message ID
  recv [--block <sec>] [--json]    Read new messages since last cursor
  human [--count <n>] [--json]     Read authoritative human operator broadcast log
  peers [--json]                   Discover active agents across the fleet
  status set <message>             Set ephemeral 300s presence status
  finger [agent]                   Show profile and capability metadata
  liaison get                      Show current fleet liaison
  liaison set <agent>              Appoint an agent as fleet liaison (operator only)
  interval [get]                   Show current backplane poll interval in seconds
  interval set <seconds>           Set shared poll interval in seconds (5-3600)
  specialty list [name] [--json]   List domain specialties and assigned agents
  specialty add <name> <agent>     Assign an agent to a domain specialty (operator only)
  specialty remove <name> <agent>  Remove an agent from a domain specialty (operator only)
  state get [<key>]                Read field from shared synaptic state (ADR 00040)
  state set <key> <val>            Update field in shared synaptic state (ADR 00040)
  state show                       Display entire shared synaptic state JSON (ADR 00040)
  state queue <push|pop|list>      Manage shared inter-lobe directive queue (ADR 00040)
  ensemble <cmd> [args...]         Manage and communicate in team-scoped ensemble channels
  help [--ai]                      Show this help message (or AI agent protocol instructions)`)
}

func printAIHelp(out io.Writer) {
	fmt.Fprintln(out, `# Valkey Backplane CLI ('bp') — Agent Execution & Protocol Guide

## Overview
The 'bp' CLI is the inter-agent and host-agent communication tool for the Agent Sandbox.
Every command is cryptographically authenticated using Ed25519 signatures and Valkey ACLs.

## Profile & Identity Resolution
- Execution is stateless and thread-safe.
- Specify profile via '--profile <name>' flag or 'BP_PROFILE=<name>' environment variable.
- Profiles are stored in '$XDG_STATE_HOME/bp/profiles/<name>.env'.
- Check active identity:
    bp [--profile <name>] whoami

## Message Passing Conventions
1. Broadcast ('bp say'):
   - Use 'bp say "<message>"' for short team-wide status updates, milestones, and discoveries.
   - For long logs or patches, park payload: 'bp say "<summary>" --file <path>'.
2. Direct Messaging ('bp tell'):
   - Send point-to-point directives: 'bp tell <agent> "<message>"'.
   - Attach files/diffs: 'bp tell <agent> "<message>" --file <path>'.
3. Threaded Replies ('bp reply'):
   - Quote citations when replying: 'bp reply <citation> <recipient> "<message>"' (e.g., 'bp reply alice#3 alice "acknowledged"').
4. Consuming Inboxes ('bp recv'):
   - Read unread incoming messages: 'bp recv [--block <sec>] [--json]'.
   - Messages are automatically verified and cursored.

## Shared State & Coordination (ADR 00040)
- Inspect cluster state: 'bp state show'
- Read / write state keys: 'bp state get <key>' / 'bp state set <key> <value>'
- Directive queue: 'bp state queue push "<task>"' / 'bp state queue pop'

## Agent Behavioral Rules
- Never snoop or guess private inboxes.
- Never impersonate other agents or the human operator.
- Always include citations in replies to preserve lineage.`)
}

func getClient(ctx context.Context, cfg libbp.ClientConfig, stderr io.Writer) (*libbp.Client, error) {
	client, err := libbp.Dial(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "bp error: %v\n", err)
		return nil, err
	}
	return client, nil
}

// parseFileFlag extracts the --file argument from args, returning filePath, remaining args, and success flag
func parseFileFlag(command string, args []string, stderr io.Writer) (string, []string, bool) {
	var filePath string
	var remaining []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			remaining = append(remaining, args[i+1:]...)
			break
		}
		if arg == "--file" || arg == "-file" {
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "%s: flag needs an argument: %s\n", command, arg)
				return "", nil, false
			}
			filePath = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--file=") {
			filePath = strings.TrimPrefix(arg, "--file=")
		} else if strings.HasPrefix(arg, "-file=") {
			filePath = strings.TrimPrefix(arg, "-file=")
		} else if strings.HasPrefix(arg, "-") && arg != "-" {
			fmt.Fprintf(stderr, "%s: flag provided but not defined: %s\n", command, arg)
			return "", nil, false
		} else {
			remaining = append(remaining, arg)
		}
	}
	return filePath, remaining, true
}

func handleSay(ctx context.Context, cfg libbp.ClientConfig, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	filePath, remaining, ok := parseFileFlag("say", args, stderr)
	if !ok {
		return 1
	}

	if len(remaining) == 0 && filePath == "" {
		fmt.Fprintln(stderr, "bp: say requires a message or --file")
		return 1
	}
	content := strings.Join(remaining, " ")

	client, err := getClient(ctx, cfg, stderr)
	if err != nil {
		return 1
	}
	defer client.Close()

	var blobKey string
	if filePath != "" {
		data, err := readPayload(filePath, stdin)
		if err != nil {
			fmt.Fprintf(stderr, "bp error: failed reading file %s: %v\n", filePath, err)
			return 1
		}
		bk, err := client.ParkBlob(ctx, data)
		if err != nil {
			fmt.Fprintf(stderr, "bp error: failed parking blob: %v\n", err)
			return 1
		}
		blobKey = bk
	}

	msg, err := client.Say(ctx, content, blobKey)
	if err != nil {
		fmt.Fprintf(stderr, "bp error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "[%s] %s\n", msg.Citation, msg.Content)
	return 0
}

func handlePost(ctx context.Context, cfg libbp.ClientConfig, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "bp: post requires <file> [to <agent>]")
		return 1
	}

	var filePath string
	var remaining []string

	if args[0] == "--file" {
		if len(args) < 2 {
			fmt.Fprintln(stderr, "bp: post flag --file needs an argument")
			return 1
		}
		filePath = args[1]
		remaining = args[2:]
	} else if strings.HasPrefix(args[0], "--file=") {
		filePath = strings.TrimPrefix(args[0], "--file=")
		remaining = args[1:]
	} else {
		filePath = args[0]
		remaining = args[1:]
	}

	var recipient string
	if len(remaining) > 0 {
		if remaining[0] == "to" {
			if len(remaining) != 2 {
				fmt.Fprintln(stderr, "bp: post to requires exactly <agent>")
				return 1
			}
			recipient = remaining[1]
		} else if len(remaining) == 1 {
			recipient = remaining[0]
		} else {
			fmt.Fprintln(stderr, "bp: post unexpected arguments after <file>")
			return 1
		}
	}

	data, err := readPayload(filePath, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "bp error: failed reading file %s: %v\n", filePath, err)
		return 1
	}

	summary, err := extractSummary(data)
	if err != nil {
		fmt.Fprintf(stderr, "bp error: %v\n", err)
		return 1
	}

	client, err := getClient(ctx, cfg, stderr)
	if err != nil {
		return 1
	}
	defer client.Close()

	msg, err := client.Post(ctx, recipient, summary, data)
	if err != nil {
		fmt.Fprintf(stderr, "bp error: %v\n", err)
		return 1
	}

	target := "fleet"
	if recipient != "" {
		target = recipient
	}
	fmt.Fprintf(stdout, "[%s -> %s] %s\n", msg.Citation, target, msg.Content)
	if msg.BlobPath != "" {
		fmt.Fprintf(stdout, "  └── Payload attached: %s\n", msg.BlobPath)
	}
	return 0
}

func handleCat(ctx context.Context, cfg libbp.ClientConfig, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "bp: cat requires <blob-key>")
		return 1
	}

	client, err := getClient(ctx, cfg, stderr)
	if err != nil {
		return 1
	}
	defer client.Close()

	data, err := client.GetBlob(ctx, args[0])
	if err != nil {
		fmt.Fprintf(stderr, "bp error: %v\n", err)
		return 1
	}

	_, _ = stdout.Write(data)
	return 0
}

func handleTell(ctx context.Context, cfg libbp.ClientConfig, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	filePath, remaining, ok := parseFileFlag("tell", args, stderr)
	if !ok {
		return 1
	}

	if len(remaining) == 0 || (len(remaining) < 2 && filePath == "") {
		fmt.Fprintln(stderr, "bp: tell requires <agent> <message>")
		return 1
	}
	recipient := remaining[0]
	var content string
	if len(remaining) > 1 {
		content = strings.Join(remaining[1:], " ")
	}

	client, err := getClient(ctx, cfg, stderr)
	if err != nil {
		return 1
	}
	defer client.Close()

	var blobKey string
	if filePath != "" {
		data, err := readPayload(filePath, stdin)
		if err != nil {
			fmt.Fprintf(stderr, "bp error: failed reading file %s: %v\n", filePath, err)
			return 1
		}
		bk, err := client.ParkBlob(ctx, data)
		if err != nil {
			fmt.Fprintf(stderr, "bp error: failed parking blob: %v\n", err)
			return 1
		}
		blobKey = bk
	}

	msg, err := client.TellWithBlob(ctx, recipient, content, blobKey)
	if err != nil {
		fmt.Fprintf(stderr, "bp error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "[%s -> %s] %s\n", msg.Citation, recipient, msg.Content)
	if msg.BlobPath != "" {
		fmt.Fprintf(stdout, "  └── Payload attached: %s\n", msg.BlobPath)
	}
	return 0
}

func handleReply(ctx context.Context, cfg libbp.ClientConfig, args []string, stdout, stderr io.Writer) int {
	if len(args) < 3 {
		fmt.Fprintln(stderr, "bp: reply requires <msgid> <recipient> <message>")
		return 1
	}
	msgID := args[0]
	recipient := args[1]
	content := strings.Join(args[2:], " ")

	client, err := getClient(ctx, cfg, stderr)
	if err != nil {
		return 1
	}
	defer client.Close()

	msg, err := client.Reply(ctx, msgID, recipient, content)
	if err != nil {
		fmt.Fprintf(stderr, "bp error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "[%s -> %s (re: %s)] %s\n", msg.Citation, recipient, msgID, msg.Content)
	return 0
}

func handleRecv(ctx context.Context, cfg libbp.ClientConfig, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("recv", flag.ContinueOnError)
	fs.SetOutput(stderr)
	blockSec := fs.Int("block", 0, "Block for N seconds waiting for messages")
	jsonOutput := fs.Bool("json", false, "Output in JSON format")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	return handleRecvParsed(ctx, cfg, *blockSec, *jsonOutput, stdout, stderr)
}

func handleRecvParsed(ctx context.Context, cfg libbp.ClientConfig, blockSec int, jsonOutput bool, stdout, stderr io.Writer) int {
	client, err := getClient(ctx, cfg, stderr)
	if err != nil {
		return 1
	}
	defer client.Close()

	messages, err := client.Recv(ctx, blockSec)
	if err != nil {
		fmt.Fprintf(stderr, "bp error: %v\n", err)
		return 1
	}

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(messages)
		return 0
	}

	for _, m := range messages {
		sigStatus := ""
		if m.IsSigned {
			if m.IsVerified {
				sigStatus = " [verified]"
			} else {
				sigStatus = " [UNVERIFIED]"
			}
		}

		target := ""
		if m.Destination != "" {
			target = fmt.Sprintf(" -> %s", m.Destination)
		}

		fmt.Fprintf(stdout, "%s [%s%s%s]: %s\n",
			time.UnixMilli(m.Timestamp).Format("15:04:05"),
			m.Citation,
			target,
			sigStatus,
			m.Content,
		)
		if m.BlobPath != "" {
			fmt.Fprintf(stdout, "  └── Payload attached: %s\n", m.BlobPath)
		}
	}
	return 0
}

func handleHuman(ctx context.Context, cfg libbp.ClientConfig, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("human", flag.ContinueOnError)
	fs.SetOutput(stderr)
	count := fs.Int("count", 20, "Number of recent human messages to fetch")
	jsonOutput := fs.Bool("json", false, "Output in JSON format")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	client, err := getClient(ctx, cfg, stderr)
	if err != nil {
		return 1
	}
	defer client.Close()

	messages, err := client.Human(ctx, *count)
	if err != nil {
		fmt.Fprintf(stderr, "bp error: %v\n", err)
		return 1
	}

	if *jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(messages)
		return 0
	}

	for _, m := range messages {
		fmt.Fprintf(stdout, "%s [%s]: %s\n",
			time.UnixMilli(m.Timestamp).Format("15:04:05"),
			m.Citation,
			m.Content,
		)
	}
	return 0
}

func handlePeers(ctx context.Context, cfg libbp.ClientConfig, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("peers", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOutput := fs.Bool("json", false, "Output in JSON format")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	client, err := getClient(ctx, cfg, stderr)
	if err != nil {
		return 1
	}
	defer client.Close()

	peers, err := client.Peers(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "bp error: %v\n", err)
		return 1
	}

	if *jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(peers)
		return 0
	}

	printfFmt := "%-18s %-12s %s\n"
	fmt.Fprintf(stdout, printfFmt, "AGENT_ID", "ROLE", "STATUS")
	for _, p := range peers {
		role := p.Role
		if role == "" {
			role = "agent"
		}
		if p.IsLiaison {
			role += " (liaison)"
		}
		status := p.Status
		if status == "" {
			status = "idle"
		}
		fmt.Fprintf(stdout, printfFmt, p.ID, role, status)
	}
	return 0
}

func handleStatus(ctx context.Context, cfg libbp.ClientConfig, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || strings.ToLower(args[0]) != "set" {
		fmt.Fprintln(stderr, "Usage: bp status set <message>")
		return 1
	}
	status := strings.Join(args[1:], " ")

	client, err := getClient(ctx, cfg, stderr)
	if err != nil {
		return 1
	}
	defer client.Close()

	if err := client.SetStatus(ctx, status); err != nil {
		fmt.Fprintf(stderr, "bp error: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "[%s] status set: %s\n", cfg.AgentID, status)
	return 0
}

func handleFinger(ctx context.Context, cfg libbp.ClientConfig, args []string, stdout, stderr io.Writer) int {
	targetAgent := cfg.AgentID
	if len(args) > 0 {
		targetAgent = args[0]
	}

	client, err := getClient(ctx, cfg, stderr)
	if err != nil {
		return 1
	}
	defer client.Close()

	finger, err := client.GetFinger(ctx, targetAgent)
	if err != nil || len(finger) == 0 {
		fmt.Fprintf(stdout, "[%s] No finger profile registered.\n", targetAgent)
		return 0
	}

	fmt.Fprintf(stdout, "--- Identity Profile: %s ---\n", targetAgent)
	for k, v := range finger {
		fmt.Fprintf(stdout, "  %-12s: %s\n", k, v)
	}
	return 0
}

func handleLiaison(ctx context.Context, cfg libbp.ClientConfig, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.ToLower(args[0]) == "get" {
		client, err := getClient(ctx, cfg, stderr)
		if err != nil {
			return 1
		}
		defer client.Close()

		liaison, err := client.GetLiaison(ctx)
		if err != nil || liaison == "" {
			fmt.Fprintln(stdout, "No liaison currently appointed.")
			return 0
		}
		fmt.Fprintf(stdout, "Current fleet liaison: %s\n", liaison)
		return 0
	}

	if strings.ToLower(args[0]) == "set" {
		if len(args) < 2 {
			fmt.Fprintln(stderr, "Usage: bp liaison set <agent>")
			return 1
		}
		agentID := args[1]
		client, err := getClient(ctx, cfg, stderr)
		if err != nil {
			return 1
		}
		defer client.Close()

		if err := client.SetLiaison(ctx, agentID); err != nil {
			fmt.Fprintf(stderr, "bp error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Liaison appointed: %s\n", agentID)
		return 0
	}

	fmt.Fprintln(stderr, "Usage: bp liaison <get|set <agent>>")
	return 1
}

func handleSpecialty(ctx context.Context, cfg libbp.ClientConfig, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: bp specialty <list [name] [--json] | add <name> <agent> | remove <name> <agent>>")
		return 1
	}

	sub := strings.ToLower(args[0])
	subArgs := args[1:]

	switch sub {
	case "list":
		return handleSpecialtyList(ctx, cfg, subArgs, stdout, stderr)
	case "add":
		return handleSpecialtyAdd(ctx, cfg, subArgs, stdout, stderr)
	case "remove":
		return handleSpecialtyRemove(ctx, cfg, subArgs, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "bp: unknown specialty subcommand %q (expected 'list', 'add', or 'remove')\n", sub)
		return 1
	}
}

func handleSpecialtyList(ctx context.Context, cfg libbp.ClientConfig, args []string, stdout, stderr io.Writer) int {
	var jsonOutput bool
	var remaining []string
	for _, arg := range args {
		if arg == "--json" || arg == "-json" {
			jsonOutput = true
		} else if strings.HasPrefix(arg, "-") && arg != "-" {
			fmt.Fprintf(stderr, "specialty list: flag provided but not defined: %s\n", arg)
			return 1
		} else {
			remaining = append(remaining, arg)
		}
	}

	var specialtyName string
	if len(remaining) > 0 {
		specialtyName = remaining[0]
	}

	client, err := getClient(ctx, cfg, stderr)
	if err != nil {
		return 1
	}
	defer client.Close()

	if specialtyName != "" {
		agents, err := client.GetSpecialtyAgents(ctx, specialtyName)
		if err != nil {
			fmt.Fprintf(stderr, "bp error: %v\n", err)
			return 1
		}

		if jsonOutput {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(agents)
			return 0
		}

		if len(agents) == 0 {
			fmt.Fprintf(stdout, "Specialty %q has no assigned agents.\n", specialtyName)
			return 0
		}

		fmt.Fprintf(stdout, "Specialty %q assigned agents:\n", specialtyName)
		for _, agent := range agents {
			fmt.Fprintf(stdout, "  - %s\n", agent)
		}
		return 0
	}

	// List all specialties
	specialties, err := client.GetAllSpecialties(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "bp error: %v\n", err)
		return 1
	}

	if jsonOutput {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(specialties)
		return 0
	}

	if len(specialties) == 0 {
		fmt.Fprintln(stdout, "No domain specialties configured.")
		return 0
	}

	printfFmt := "%-20s %s\n"
	fmt.Fprintf(stdout, printfFmt, "SPECIALTY", "AGENTS")
	for domain, agents := range specialties {
		agentList := strings.Join(agents, ", ")
		if agentList == "" {
			agentList = "(none)"
		}
		fmt.Fprintf(stdout, printfFmt, domain, agentList)
	}
	return 0
}

func handleSpecialtyAdd(ctx context.Context, cfg libbp.ClientConfig, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "Usage: bp specialty add <name> <agent>")
		return 1
	}

	specialty := args[0]
	agent := args[1]

	client, err := getClient(ctx, cfg, stderr)
	if err != nil {
		return 1
	}
	defer client.Close()

	if err := client.SetSpecialty(ctx, specialty, agent); err != nil {
		fmt.Fprintf(stderr, "bp error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Assigned agent %s to specialty %s\n", agent, specialty)
	return 0
}

func handleSpecialtyRemove(ctx context.Context, cfg libbp.ClientConfig, args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "Usage: bp specialty remove <name> <agent>")
		return 1
	}

	specialty := args[0]
	agent := args[1]

	client, err := getClient(ctx, cfg, stderr)
	if err != nil {
		return 1
	}
	defer client.Close()

	if err := client.RemoveSpecialty(ctx, specialty, agent); err != nil {
		fmt.Fprintf(stderr, "bp error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "Removed agent %s from specialty %s\n", agent, specialty)
	return 0
}

func handleInterval(ctx context.Context, cfg libbp.ClientConfig, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.ToLower(args[0]) == "get" {
		client, err := getClient(ctx, cfg, stderr)
		if err != nil {
			return 1
		}
		defer client.Close()

		interval, err := client.GetPollInterval(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "bp error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "%d\n", interval)
		return 0
	}

	if strings.ToLower(args[0]) == "set" {
		if len(args) < 2 {
			fmt.Fprintln(stderr, "Usage: bp interval set <seconds>")
			return 1
		}
		secs, err := strconv.Atoi(args[1])
		if err != nil || secs < 5 || secs > 3600 {
			fmt.Fprintln(stderr, "bp: interval must be an integer between 5 and 3600")
			return 1
		}

		client, err := getClient(ctx, cfg, stderr)
		if err != nil {
			return 1
		}
		defer client.Close()

		if err := client.SetPollInterval(ctx, secs); err != nil {
			fmt.Fprintf(stderr, "bp error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Poll interval set to %d seconds\n", secs)
		return 0
	}

	fmt.Fprintln(stderr, "Usage: bp interval [get|set <seconds>]")
	return 1
}

func handleEnsemble(ctx context.Context, cfg libbp.ClientConfig, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: bp ensemble <create|list|join|leave|say|recv> [args...]")
		return 1
	}

	sub := strings.ToLower(args[0])
	subArgs := args[1:]

	client, err := getClient(ctx, cfg, stderr)
	if err != nil {
		return 1
	}
	defer client.Close()

	switch sub {
	case "create":
		if len(subArgs) < 1 {
			fmt.Fprintln(stderr, "Usage: bp ensemble create <name>")
			return 1
		}
		name := subArgs[0]
		if err := libbp.ValidateEnsembleName(name); err != nil {
			fmt.Fprintf(stderr, "bp: %v\n", err)
			return 1
		}
		if err := client.JoinEnsemble(ctx, name); err != nil {
			fmt.Fprintf(stderr, "bp: failed creating ensemble %s: %v\n", name, err)
			return 1
		}
		msg, err := client.PublishEnsemble(ctx, name, fmt.Sprintf("Ensemble %s created by %s", name, cfg.AgentID))
		if err != nil {
			fmt.Fprintf(stderr, "bp: joined %s but announce failed: %v\n", name, err)
			return 1
		}
		fmt.Fprintf(stdout, "Created and joined ensemble %s (%s)\n", name, msg.Citation)
		return 0

	case "join":
		if len(subArgs) < 1 {
			fmt.Fprintln(stderr, "Usage: bp ensemble join <name>")
			return 1
		}
		name := subArgs[0]
		if err := libbp.ValidateEnsembleName(name); err != nil {
			fmt.Fprintf(stderr, "bp: %v\n", err)
			return 1
		}
		if err := client.JoinEnsemble(ctx, name); err != nil {
			fmt.Fprintf(stderr, "bp: failed joining ensemble %s: %v\n", name, err)
			return 1
		}
		fmt.Fprintf(stdout, "Joined ensemble %s\n", name)
		return 0

	case "leave":
		if len(subArgs) < 1 {
			fmt.Fprintln(stderr, "Usage: bp ensemble leave <name>")
			return 1
		}
		name := subArgs[0]
		if err := libbp.ValidateEnsembleName(name); err != nil {
			fmt.Fprintf(stderr, "bp: %v\n", err)
			return 1
		}
		if err := client.LeaveEnsemble(ctx, name); err != nil {
			fmt.Fprintf(stderr, "bp: failed leaving ensemble %s: %v\n", name, err)
			return 1
		}
		fmt.Fprintf(stdout, "Left ensemble %s\n", name)
		return 0

	case "list":
		ensembles, err := client.ListJoinedEnsembles(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "bp: failed listing ensembles: %v\n", err)
			return 1
		}
		if len(ensembles) == 0 {
			fmt.Fprintln(stdout, "No joined ensembles.")
			return 0
		}
		for _, e := range ensembles {
			fmt.Fprintf(stdout, "- %s\n", e)
		}
		return 0

	case "say":
		if len(subArgs) < 2 {
			fmt.Fprintln(stderr, "Usage: bp ensemble say <name> <message> [--file <path>]")
			return 1
		}
		name := subArgs[0]
		var filePath string
		var msgParts []string
		for i := 1; i < len(subArgs); i++ {
			if subArgs[i] == "--file" && i+1 < len(subArgs) {
				filePath = subArgs[i+1]
				i++
			} else {
				msgParts = append(msgParts, subArgs[i])
			}
		}
		content := strings.Join(msgParts, " ")
		var blobKey string
		if filePath != "" {
			data, err := os.ReadFile(filePath)
			if err != nil {
				fmt.Fprintf(stderr, "bp: error reading file: %v\n", err)
				return 1
			}
			blobKey, err = client.ParkBlob(ctx, data)
			if err != nil {
				fmt.Fprintf(stderr, "bp: failed parking blob: %v\n", err)
				return 1
			}
		}
		msg, err := client.PublishEnsemble(ctx, name, content, blobKey)
		if err != nil {
			fmt.Fprintf(stderr, "bp: failed sending to ensemble %s: %v\n", name, err)
			return 1
		}
		fmt.Fprintf(stdout, "Sent to ensemble %s: %s\n", name, msg.Citation)
		return 0

	case "recv":
		if len(subArgs) < 1 {
			fmt.Fprintln(stderr, "Usage: bp ensemble recv <name> [--last <id>]")
			return 1
		}
		name := subArgs[0]
		lastID := "0-0"
		for i := 1; i < len(subArgs); i++ {
			if subArgs[i] == "--last" && i+1 < len(subArgs) {
				lastID = subArgs[i+1]
				i++
			}
		}
		msgs, err := client.ReadEnsembleMessages(ctx, name, lastID, 50)
		if err != nil {
			fmt.Fprintf(stderr, "bp: failed reading ensemble %s: %v\n", name, err)
			return 1
		}
		for _, m := range msgs {
			fmt.Fprintf(stdout, "[%s] %s: %s\n", m.ID, m.Citation, m.Content)
		}
		return 0

	default:
		fmt.Fprintf(stderr, "bp: unknown ensemble command %q (see 'bp help')\n", sub)
		return 1
	}
}

func agentStatePath() string {
	if p := os.Getenv("AGENT_STATE_FILE"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/tmp"
	}
	return filepath.Join(home, ".agent", "state.json")
}

func agentQueuePath() string {
	if p := os.Getenv("AGENT_QUEUE_FILE"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/tmp"
	}
	return filepath.Join(home, ".agent", "queue.jsonl")
}

func handleState(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "bp: state requires subcommand (get, set, show, queue)")
		return 1
	}

	sub := strings.ToLower(args[0])
	stateFile := agentStatePath()
	queueFile := agentQueuePath()

	switch sub {
	case "get":
		data, err := os.ReadFile(stateFile)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintln(stderr, "bp: state file does not exist")
				return 1
			}
			fmt.Fprintf(stderr, "bp error: %v\n", err)
			return 1
		}
		if len(args) > 1 {
			var m map[string]interface{}
			if err := json.Unmarshal(data, &m); err != nil {
				fmt.Fprintf(stderr, "bp error: invalid state JSON: %v\n", err)
				return 1
			}
			key := args[1]
			if val, ok := m[key]; ok {
				fmt.Fprintf(stdout, "%v\n", val)
				return 0
			}
			fmt.Fprintf(stderr, "bp: state field %q not found\n", key)
			return 1
		}
		_, _ = stdout.Write(data)
		return 0

	case "set":
		if len(args) < 3 {
			fmt.Fprintln(stderr, "bp: state set requires <key> <value>")
			return 1
		}
		key := args[1]
		val := strings.Join(args[2:], " ")

		var m map[string]interface{}
		data, err := os.ReadFile(stateFile)
		if err == nil {
			_ = json.Unmarshal(data, &m)
		}
		if m == nil {
			m = make(map[string]interface{})
		}
		m[key] = val
		m["updated_at"] = time.Now().UTC().Format(time.RFC3339)

		if err := os.MkdirAll(filepath.Dir(stateFile), 0700); err != nil {
			fmt.Fprintf(stderr, "bp error: %v\n", err)
			return 1
		}
		outData, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "bp error: %v\n", err)
			return 1
		}
		if err := os.WriteFile(stateFile, append(outData, '\n'), 0600); err != nil {
			fmt.Fprintf(stderr, "bp error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "state.%s = %s\n", key, val)
		return 0

	case "show":
		data, err := os.ReadFile(stateFile)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintln(stderr, "bp: state file does not exist")
				return 1
			}
			fmt.Fprintf(stderr, "bp error: %v\n", err)
			return 1
		}
		_, _ = stdout.Write(data)
		return 0

	case "queue":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "bp: state queue requires subcommand (push, pop, list, clear)")
			return 1
		}
		qSub := strings.ToLower(args[1])
		switch qSub {
		case "push":
			if len(args) < 3 {
				fmt.Fprintln(stderr, "bp: state queue push requires <directive>")
				return 1
			}
			directive := strings.Join(args[2:], " ")
			if err := os.MkdirAll(filepath.Dir(queueFile), 0700); err != nil {
				fmt.Fprintf(stderr, "bp error: %v\n", err)
				return 1
			}
			f, err := os.OpenFile(queueFile, os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				fmt.Fprintf(stderr, "bp error: %v\n", err)
				return 1
			}
			defer f.Close()
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
				fmt.Fprintf(stderr, "bp error: flock failed: %v\n", err)
				return 1
			}
			defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			if _, err := f.Seek(0, io.SeekEnd); err != nil {
				fmt.Fprintf(stderr, "bp error: %v\n", err)
				return 1
			}
			if _, err := f.WriteString(directive + "\n"); err != nil {
				fmt.Fprintf(stderr, "bp error: %v\n", err)
				return 1
			}
			fmt.Fprintln(stdout, "queued directive")
			return 0

		case "pop":
			f, err := os.OpenFile(queueFile, os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				return 1
			}
			defer f.Close()
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
				return 1
			}
			defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

			data, err := io.ReadAll(f)
			if err != nil || len(bytes.TrimSpace(data)) == 0 {
				return 1
			}
			lines := bytes.Split(data, []byte("\n"))
			var firstLine []byte
			var remaining [][]byte
			for _, l := range lines {
				if len(bytes.TrimSpace(l)) > 0 {
					if firstLine == nil {
						firstLine = l
					} else {
						remaining = append(remaining, l)
					}
				}
			}
			if firstLine == nil {
				return 1
			}
			var newContent bytes.Buffer
			for _, r := range remaining {
				newContent.Write(r)
				newContent.WriteByte('\n')
			}
			if err := f.Truncate(0); err != nil {
				fmt.Fprintf(stderr, "bp error: %v\n", err)
				return 1
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				fmt.Fprintf(stderr, "bp error: %v\n", err)
				return 1
			}
			if _, err := f.Write(newContent.Bytes()); err != nil {
				fmt.Fprintf(stderr, "bp error: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "%s\n", string(firstLine))
			return 0

		case "list":
			f, err := os.OpenFile(queueFile, os.O_RDONLY, 0600)
			if err != nil {
				return 0
			}
			defer f.Close()
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
				return 0
			}
			defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_, _ = io.Copy(stdout, f)
			return 0

		case "clear":
			f, err := os.OpenFile(queueFile, os.O_CREATE|os.O_RDWR, 0600)
			if err == nil {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
				_ = f.Truncate(0)
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}
			_ = os.Remove(queueFile)
			fmt.Fprintln(stdout, "queue cleared")
			return 0

		default:
			fmt.Fprintf(stderr, "bp: unknown queue subcommand %q\n", qSub)
			return 1
		}

	default:
		fmt.Fprintf(stderr, "bp: unknown state subcommand %q\n", sub)
		return 1
	}
}

func loadDefaultEnv() {
	home, _ := os.UserHomeDir()
	if home == "" {
		home = "/tmp"
	}
	envPath := filepath.Join(home, ".local", "share", "agent-sandbox", ".env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if parts := strings.SplitN(line, "=", 2); len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			if os.Getenv(k) == "" && v != "" {
				_ = os.Setenv(k, v)
			}
		}
	}
}
