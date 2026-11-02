// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"crypto/tls"
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
  --tls-listen       Address to listen on for TLS/mTLS (env: VALKEY_GUARD_TLS_LISTEN)
  --tls-cert         Path to server certificate file (env: VALKEY_GUARD_TLS_CERT)
  --tls-key          Path to server private key file (env: VALKEY_GUARD_TLS_KEY)
  --tls-client-ca    Path to client CA certificate file for mutual TLS (env: VALKEY_GUARD_TLS_CLIENT_CA)
  --help, -h         Show help and usage information

Environment Variables:
  VALKEY_GUARD_LISTEN           Listen address (default: :6379)
  VALKEY_GUARD_TARGET           Upstream Valkey target (default: 127.0.0.1:6380)
  VALKEY_GUARD_MAX_PAYLOAD      Max payload bytes (default: 1048576)
  VALKEY_GUARD_MAX_STREAM_LEN   Max stream length (default: 1000)
  VALKEY_GUARD_TLS_LISTEN       Listen address for TLS/mTLS
  VALKEY_GUARD_TLS_CERT         Path to server certificate file
  VALKEY_GUARD_TLS_KEY          Path to server private key file
  VALKEY_GUARD_TLS_CLIENT_CA    Path to client CA certificate file for mutual TLS`)
}

// Config holds runtime configuration options for the valkey-guard CLI.
type Config struct {
	ListenAddr      string
	TargetAddr      string
	MaxPayload      int
	MaxStreamLen    int64
	TLSListenAddr   string
	TLSCertFile     string
	TLSKeyFile      string
	TLSClientCAFile string
	TLSConfig       *tls.Config
}

// LoadConfig parses flags and environment variables into Config.
func LoadConfig(args []string, stdout, stderr io.Writer) (*Config, bool, error) {
	cfg := &Config{
		TargetAddr:   valkeyguard.DefaultTargetAddr,
		MaxPayload:   valkeyguard.DefaultMaxPayloadBytes,
		MaxStreamLen: valkeyguard.DefaultMaxStreamLen,
	}

	var envListenSet bool
	if envListen, ok := os.LookupEnv("VALKEY_GUARD_LISTEN"); ok {
		cfg.ListenAddr = envListen
		envListenSet = true
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

	if envTLSListen := os.Getenv("VALKEY_GUARD_TLS_LISTEN"); envTLSListen != "" {
		cfg.TLSListenAddr = envTLSListen
	}
	if envTLSCert := os.Getenv("VALKEY_GUARD_TLS_CERT"); envTLSCert != "" {
		cfg.TLSCertFile = envTLSCert
	}
	if envTLSKey := os.Getenv("VALKEY_GUARD_TLS_KEY"); envTLSKey != "" {
		cfg.TLSKeyFile = envTLSKey
	}
	if envTLSClientCA := os.Getenv("VALKEY_GUARD_TLS_CLIENT_CA"); envTLSClientCA != "" {
		cfg.TLSClientCAFile = envTLSClientCA
	}

	fs := flag.NewFlagSet("valkey-guard", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var showHelp bool
	var flagListen string
	fs.StringVar(&flagListen, "listen", "", "Address to listen on (default: :6379)")
	fs.StringVar(&cfg.TargetAddr, "target", cfg.TargetAddr, "Upstream Valkey address")
	fs.IntVar(&cfg.MaxPayload, "max-payload", cfg.MaxPayload, "Maximum message payload bytes")
	fs.Int64Var(&cfg.MaxStreamLen, "max-stream-len", cfg.MaxStreamLen, "Maximum stream depth")
	fs.StringVar(&cfg.TLSListenAddr, "tls-listen", cfg.TLSListenAddr, "Address to listen on for TLS/mTLS")
	fs.StringVar(&cfg.TLSCertFile, "tls-cert", cfg.TLSCertFile, "Path to server certificate file")
	fs.StringVar(&cfg.TLSKeyFile, "tls-key", cfg.TLSKeyFile, "Path to server private key file")
	fs.StringVar(&cfg.TLSClientCAFile, "tls-client-ca", cfg.TLSClientCAFile, "Path to client CA certificate file for mutual TLS")
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

	var flagListenSet bool
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "listen" {
			flagListenSet = true
			cfg.ListenAddr = flagListen
		}
	})

	// When TLS flags are omitted, maintain default plaintext listener on :6379
	// If TLS is configured and plaintext listen was not explicitly set via flag or env var,
	// only run on TLS listener.
	if !flagListenSet && !envListenSet {
		if cfg.TLSListenAddr == "" && cfg.TLSCertFile == "" && cfg.TLSKeyFile == "" && cfg.TLSClientCAFile == "" {
			cfg.ListenAddr = valkeyguard.DefaultListenAddr
		}
	}

	if cfg.MaxPayload <= 0 {
		return nil, false, errors.New("max-payload must be greater than 0")
	}
	if cfg.MaxStreamLen <= 0 {
		return nil, false, errors.New("max-stream-len must be greater than 0")
	}

	// Validate TLS settings
	hasTLSListen := cfg.TLSListenAddr != ""
	hasTLSCert := cfg.TLSCertFile != ""
	hasTLSKey := cfg.TLSKeyFile != ""
	hasTLSClientCA := cfg.TLSClientCAFile != ""

	if hasTLSListen {
		if !hasTLSCert || !hasTLSKey {
			return nil, false, errors.New("tls-listen requires both --tls-cert and --tls-key to be specified")
		}
	} else if hasTLSCert || hasTLSKey || hasTLSClientCA {
		return nil, false, errors.New("tls-cert, tls-key, and tls-client-ca require --tls-listen to be specified")
	}

	if hasTLSListen {
		tlsConfig, err := valkeyguard.LoadServerTLSConfig(cfg.TLSCertFile, cfg.TLSKeyFile, cfg.TLSClientCAFile)
		if err != nil {
			return nil, false, fmt.Errorf("failed to load TLS configuration: %w", err)
		}
		cfg.TLSConfig = tlsConfig
	}

	if cfg.ListenAddr == "" && cfg.TLSListenAddr == "" {
		return nil, false, errors.New("at least one listener (--listen or --tls-listen) must be configured")
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

	var servers []*valkeyguard.ProxyServer

	if cfg.ListenAddr != "" {
		plaintextCfg := valkeyguard.ProxyConfig{
			ListenAddr: cfg.ListenAddr,
			TargetAddr: cfg.TargetAddr,
			Guard: valkeyguard.GuardConfig{
				MaxPayloadBytes: cfg.MaxPayload,
				MaxStreamLen:    cfg.MaxStreamLen,
			},
		}
		plaintextServer := valkeyguard.NewProxyServer(plaintextCfg)
		if err := plaintextServer.Start(ctx); err != nil {
			fmt.Fprintf(stderr, "valkey-guard server error: %v\n", err)
			return 1
		}
		servers = append(servers, plaintextServer)
		fmt.Fprintf(stdout, "valkey-guard listening on %s -> %s\n", plaintextServer.Addr(), cfg.TargetAddr)
	}

	if cfg.TLSListenAddr != "" {
		tlsCfg := valkeyguard.ProxyConfig{
			ListenAddr: cfg.TLSListenAddr,
			TargetAddr: cfg.TargetAddr,
			TLSConfig:  cfg.TLSConfig,
			Guard: valkeyguard.GuardConfig{
				MaxPayloadBytes: cfg.MaxPayload,
				MaxStreamLen:    cfg.MaxStreamLen,
			},
		}
		tlsServer := valkeyguard.NewProxyServer(tlsCfg)
		if err := tlsServer.Start(ctx); err != nil {
			for _, srv := range servers {
				_ = srv.Close()
			}
			fmt.Fprintf(stderr, "valkey-guard server error: %v\n", err)
			return 1
		}
		servers = append(servers, tlsServer)
		fmt.Fprintf(stdout, "valkey-guard listening on %s (TLS) -> %s\n", tlsServer.Addr(), cfg.TargetAddr)
	}

	stopCh := make(chan struct{}, len(servers))
	for _, srv := range servers {
		go func(s *valkeyguard.ProxyServer) {
			select {
			case <-ctx.Done():
			case <-s.Done():
				stopCh <- struct{}{}
			}
		}(srv)
	}

	select {
	case <-ctx.Done():
	case <-stopCh:
	}

	for _, srv := range servers {
		_ = srv.Close()
	}
	return 0
}

func main() {
	os.Exit(RunCLI(os.Args[1:], os.Stdout, os.Stderr))
}
