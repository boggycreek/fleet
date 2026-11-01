// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/boggycreek/fleet/backplane/pkg/valkeyguard"
)

func printUsage(out io.Writer) {
	fmt.Fprintln(out, `Usage: valkey-guard [options]

Valkey Stream Guard transparent TCP reverse proxy. Intercepts RESP command frames,
enforces payload limits, canonical citations, MAXLEN capping on XADD, and peer inbox protection.

Options:
  --listen           Address to listen on (default: :6379, env: VALKEY_GUARD_LISTEN)
  --target           Upstream Valkey address (default: 127.0.0.1:6380, env: VALKEY_GUARD_TARGET)
  --max-payload      Maximum message payload bytes (default: 1048576, env: VALKEY_GUARD_MAX_PAYLOAD)
  --max-stream-len   Maximum stream depth for XADD (default: 1000, env: VALKEY_GUARD_MAX_STREAM_LEN)
  --help, -h         Show help and usage information

Environment Variables:
  VALKEY_GUARD_LISTEN           Listen address (default: :6379)
  VALKEY_GUARD_TARGET           Upstream Valkey target (default: 127.0.0.1:6380)
  VALKEY_GUARD_MAX_PAYLOAD      Max payload bytes (default: 1048576)
  VALKEY_GUARD_MAX_STREAM_LEN   Max stream length (default: 1000)`)
}

// Config holds runtime configuration options for the valkey-guard CLI.
type Config struct {
	ListenAddr   string
	TargetAddr   string
	MaxPayload   int
	MaxStreamLen int64
}

// LoadConfig parses flags and environment variables into Config.
func LoadConfig(args []string, stdout, stderr io.Writer) (*Config, bool, error) {
	cfg := &Config{
		ListenAddr:   valkeyguard.DefaultListenAddr,
		TargetAddr:   valkeyguard.DefaultTargetAddr,
		MaxPayload:   valkeyguard.DefaultMaxPayloadBytes,
		MaxStreamLen: valkeyguard.DefaultMaxStreamLen,
	}

	if envListen := os.Getenv("VALKEY_GUARD_LISTEN"); envListen != "" {
		cfg.ListenAddr = envListen
	}
	if envTarget := os.Getenv("VALKEY_GUARD_TARGET"); envTarget != "" {
		cfg.TargetAddr = envTarget
	}
	if envMaxPayload := os.Getenv("VALKEY_GUARD_MAX_PAYLOAD"); envMaxPayload != "" {
		val, err := strconv.Atoi(envMaxPayload)
		if err != nil || val <= 0 {
			return nil, false, fmt.Errorf("invalid VALKEY_GUARD_MAX_PAYLOAD: %s", envMaxPayload)
		}
		cfg.MaxPayload = val
	}
	if envMaxStream := os.Getenv("VALKEY_GUARD_MAX_STREAM_LEN"); envMaxStream != "" {
		val, err := strconv.ParseInt(envMaxStream, 10, 64)
		if err != nil || val <= 0 {
			return nil, false, fmt.Errorf("invalid VALKEY_GUARD_MAX_STREAM_LEN: %s", envMaxStream)
		}
		cfg.MaxStreamLen = val
	}

	fs := flag.NewFlagSet("valkey-guard", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var showHelp bool
	fs.StringVar(&cfg.ListenAddr, "listen", cfg.ListenAddr, "Address to listen on")
	fs.StringVar(&cfg.TargetAddr, "target", cfg.TargetAddr, "Upstream Valkey address")
	fs.IntVar(&cfg.MaxPayload, "max-payload", cfg.MaxPayload, "Maximum message payload bytes")
	fs.Int64Var(&cfg.MaxStreamLen, "max-stream-len", cfg.MaxStreamLen, "Maximum stream depth")
	fs.BoolVar(&showHelp, "help", false, "Show help")
	fs.BoolVar(&showHelp, "h", false, "Show help")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return nil, true, nil
		}
		return nil, false, err
	}

	if showHelp {
		printUsage(stdout)
		return nil, true, nil
	}

	if cfg.MaxPayload <= 0 {
		return nil, false, errors.New("max-payload must be greater than 0")
	}
	if cfg.MaxStreamLen <= 0 {
		return nil, false, errors.New("max-stream-len must be greater than 0")
	}

	return cfg, false, nil
}

// RunCLI executes the valkey-guard command-line entrypoint.
func RunCLI(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return RunCLIContext(ctx, args, stdout, stderr)
}

// RunCLIContext executes the valkey-guard CLI with an explicit context.
func RunCLIContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cfg, isHelp, err := LoadConfig(args, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "valkey-guard configuration error: %v\n", err)
		return 1
	}
	if isHelp {
		return 0
	}

	proxyCfg := valkeyguard.ProxyConfig{
		ListenAddr: cfg.ListenAddr,
		TargetAddr: cfg.TargetAddr,
		Guard: valkeyguard.GuardConfig{
			MaxPayloadBytes: cfg.MaxPayload,
			MaxStreamLen:    cfg.MaxStreamLen,
		},
	}

	server := valkeyguard.NewProxyServer(proxyCfg)
	if err := server.Start(ctx); err != nil {
		fmt.Fprintf(stderr, "valkey-guard server error: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "valkey-guard listening on %s -> %s\n", server.Addr(), cfg.TargetAddr)

	select {
	case <-ctx.Done():
		_ = server.Close()
		return 0
	case <-server.Done():
		return 0
	}
}

func main() {
	os.Exit(RunCLI(os.Args[1:], os.Stdout, os.Stderr))
}
