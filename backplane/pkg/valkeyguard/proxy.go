// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package valkeyguard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/boggycreek/fleet/backplane/pkg/libbp/resp"
)

const (
	// DefaultListenAddr is the default proxy listening address.
	DefaultListenAddr = ":6379"

	// DefaultTargetAddr is the default upstream Valkey address.
	DefaultTargetAddr = "127.0.0.1:6380"

	// DefaultDialTimeout is the default connection timeout for connecting to upstream.
	DefaultDialTimeout = 5 * time.Second
)

// ProxyConfig contains configuration parameters for ProxyServer.
type ProxyConfig struct {
	ListenAddr  string
	TargetAddr  string
	DialTimeout time.Duration
	Guard       GuardConfig
}

// DefaultProxyConfig returns the standard proxy configuration.
func DefaultProxyConfig() ProxyConfig {
	return ProxyConfig{
		ListenAddr:  DefaultListenAddr,
		TargetAddr:  DefaultTargetAddr,
		DialTimeout: DefaultDialTimeout,
		Guard:       DefaultGuardConfig(),
	}
}

// ProxyServer is a transparent TCP reverse proxy that intercepts client connections,
// validates and enforces stream guard rules, and transparently relays traffic to upstream Valkey.
type ProxyServer struct {
	cfg      ProxyConfig
	engine   *GuardEngine
	listener net.Listener

	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   bool
	doneCh   chan struct{}
}

// NewProxyServer creates a new ProxyServer with the given configuration.
func NewProxyServer(cfg ProxyConfig) *ProxyServer {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = DefaultListenAddr
	}
	if cfg.TargetAddr == "" {
		cfg.TargetAddr = DefaultTargetAddr
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = DefaultDialTimeout
	}

	return &ProxyServer{
		cfg:    cfg,
		engine: NewGuardEngine(cfg.Guard),
		conns:  make(map[net.Conn]struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start begins listening on the configured ListenAddr and accepts connections in a background goroutine.
func (p *ProxyServer) Start(ctx context.Context) error {
	p.mu.Lock()
	if p.listener != nil {
		p.mu.Unlock()
		return errors.New("proxy server is already started")
	}
	if p.closed {
		p.mu.Unlock()
		return errors.New("proxy server is closed")
	}

	ln, err := net.Listen("tcp", p.cfg.ListenAddr)
	if err != nil {
		p.mu.Unlock()
		return fmt.Errorf("failed to listen on %s: %w", p.cfg.ListenAddr, err)
	}
	p.listener = ln
	p.mu.Unlock()

	go p.serve(ctx)
	return nil
}

// ListenAndServe starts the proxy and blocks until the context is canceled or the server is closed.
func (p *ProxyServer) ListenAndServe(ctx context.Context) error {
	if err := p.Start(ctx); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		_ = p.Close()
		return ctx.Err()
	case <-p.doneCh:
		return nil
	}
}

// Addr returns the network address the server is listening on, or nil if not listening.
func (p *ProxyServer) Addr() net.Addr {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listener != nil {
		return p.listener.Addr()
	}
	return nil
}

// Done returns a channel that is closed when the proxy server has completely stopped.
func (p *ProxyServer) Done() <-chan struct{} {
	return p.doneCh
}

// Close gracefully closes the listener and all active client connections.
func (p *ProxyServer) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true

	var lnErr error
	if p.listener != nil {
		lnErr = p.listener.Close()
	}

	for c := range p.conns {
		_ = c.Close()
	}
	p.conns = make(map[net.Conn]struct{})
	p.mu.Unlock()

	return lnErr
}

func (p *ProxyServer) registerConn(c net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	p.conns[c] = struct{}{}
	return true
}

func (p *ProxyServer) unregisterConn(c net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.conns, c)
}

func (p *ProxyServer) serve(ctx context.Context) {
	defer close(p.doneCh)

	// Close listener if context is canceled
	go func() {
		<-ctx.Done()
		_ = p.Close()
	}()

	for {
		clientConn, err := p.listener.Accept()
		if err != nil {
			p.mu.Lock()
			closed := p.closed
			p.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return
			}
			// Temporary accept error
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
				continue
			}
		}

		if !p.registerConn(clientConn) {
			_ = clientConn.Close()
			return
		}

		go func(conn net.Conn) {
			defer p.unregisterConn(conn)
			defer conn.Close()
			p.handleConnection(ctx, conn)
		}(clientConn)
	}
}

func (p *ProxyServer) handleConnection(ctx context.Context, clientConn net.Conn) {
	// Connect to upstream Valkey target
	dialer := net.Dialer{Timeout: p.cfg.DialTimeout}
	upstreamConn, err := dialer.DialContext(ctx, "tcp", p.cfg.TargetAddr)
	if err != nil {
		errMsg := fmt.Sprintf("-ERR failed connecting to upstream Valkey target: %v\r\n", err)
		_, _ = clientConn.Write([]byte(errMsg))
		return
	}
	defer upstreamConn.Close()

	// Upstream-to-client relay runs in background
	upstreamDone := make(chan struct{})
	go func() {
		defer close(upstreamDone)
		_, _ = io.Copy(clientConn, upstreamConn)
	}()

	// Client-to-upstream relay with RESP inspection and session state tracking
	clientReader := resp.NewReader(clientConn)
	var sessionUsername string

	for {
		val, err := clientReader.ReadValue()
		if err != nil {
			// Client disconnected or read error
			break
		}

		// Inspect val to detect AUTH or HELLO commands for session tracking
		sessionUsername = extractSessionUsername(val, sessionUsername)

		// Pass through GuardEngine
		res, guardErr := p.engine.Inspect(val, sessionUsername)
		if guardErr != nil {
			// Guard rule violation: write error frame directly to client without forwarding
			errStr := guardErr.Error()
			if !strings.HasPrefix(errStr, "ERR ") && !strings.HasPrefix(errStr, "WRONGPASS ") && !strings.HasPrefix(errStr, "NOAUTH ") {
				errStr = "ERR " + errStr
			}
			errFrame := fmt.Sprintf("-%s\r\n", errStr)
			if _, wErr := clientConn.Write([]byte(errFrame)); wErr != nil {
				break
			}
			continue
		}

		// Forward valid/rewritten frame to upstream
		var outBytes []byte
		if res.Modified {
			outBytes = res.Bytes()
		} else {
			// If not modified, use res.Bytes() which re-encodes the command
			// (or directly writes the serialized form)
			outBytes = res.Bytes()
		}

		if _, wErr := upstreamConn.Write(outBytes); wErr != nil {
			break
		}
	}

	// Unblock upstream-to-client copy by closing upstreamConn
	_ = upstreamConn.Close()
	<-upstreamDone
}

// extractSessionUsername checks for AUTH and HELLO commands in incoming RESP frames
// to update the active session's authenticated username.
func extractSessionUsername(val resp.Value, currentUsername string) string {
	arr, err := val.AsArray()
	if err != nil || len(arr) == 0 {
		return currentUsername
	}

	cmd, err := arr[0].AsString()
	if err != nil {
		return currentUsername
	}
	cmd = strings.ToUpper(strings.TrimSpace(cmd))

	switch cmd {
	case "AUTH":
		// AUTH <password> (default user)
		// AUTH <username> <password>
		if len(arr) == 2 {
			return "default"
		}
		if len(arr) >= 3 {
			user, err := arr[1].AsString()
			if err == nil && user != "" {
				return user
			}
		}
	case "HELLO":
		// HELLO [protover [AUTH username password] [SETNAME name]]
		for i := 1; i < len(arr); i++ {
			arg, err := arr[i].AsString()
			if err != nil {
				continue
			}
			if strings.EqualFold(arg, "AUTH") && i+2 < len(arr) {
				user, err := arr[i+1].AsString()
				if err == nil && user != "" {
					return user
				}
			}
		}
	}

	return currentUsername
}
