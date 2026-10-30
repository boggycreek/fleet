// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/boggycreek/fleet/backplane/pkg/libbp"
	"github.com/boggycreek/fleet/backplane/pkg/libbpd"
)

func printUsage(out io.Writer) {
	fmt.Fprintln(out, `Usage: bpd

Backplane Daemon (bpd) headlessly manages event-driven agent turn execution.

Environment Variables:
  AGENT_NAME                     Name of the agent (required)
  BPD_STATE_DIR                  Local state directory (default: $HOME/.bpd)
  BPD_DEFAULT_INTERVAL_SECS      Default poll/block interval in seconds (default: 60)
  BPD_CLAUDE_TIMEOUT_SECS        Runner execution timeout in seconds (default: 600)
  BPD_MAX_CONSECUTIVE_FAILURES   Max failures before DEGRADED status (default: 3)
  BPD_ATTACH_LOCK_FILE           Advisory attach lock file (default: $HOME/.bpd-attached)
  BPD_RUNNER_CMD                 Runner command invocation (default: /usr/local/bin/agent-runner or claude -p)
  BP_HOST                        Backplane host (default: 127.0.0.1)
  BP_PORT                        Backplane port (default: 6379)
  BP_PASSWORD                    Backplane password`)
}

// RunCLI executes the bpd command-line entrypoint.
func RunCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		arg := strings.ToLower(args[0])
		if arg == "help" || arg == "-h" || arg == "--help" {
			printUsage(stdout)
			return 0
		}
	}

	cfg, err := libbpd.LoadConfigFromEnv()
	if err != nil {
		fmt.Fprintf(stderr, "bpd configuration error: %v\n", err)
		return 1
	}

	clientCfg := libbp.LoadClientFromEnv()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := libbp.Dial(ctx, clientCfg)
	if err != nil {
		fmt.Fprintf(stderr, "bpd connection error: %v\n", err)
		return 1
	}
	defer client.Close()

	daemon, err := libbpd.NewDaemon(cfg, client, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "bpd initialization error: %v\n", err)
		return 1
	}

	if err := daemon.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "bpd runtime error: %v\n", err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(RunCLI(os.Args[1:], os.Stdout, os.Stderr))
}
