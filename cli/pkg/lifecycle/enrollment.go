// Copyright (c) 2026 Boggy Creek Software LLC
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package lifecycle

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultEnrollmentPort is the default port for the ephemeral enrollment HTTP server.
const DefaultEnrollmentPort = 8765

// DefaultEnrollmentTTL is the default lifetime before an unclaimed enrollment server shuts down.
const DefaultEnrollmentTTL = 15 * time.Minute

// EnrollmentSecrets contains the sensitive environment and credentials delivered to the baremetal agent.
type EnrollmentSecrets struct {
	AgentName              string `json:"agent_name"`
	AgentRole              string `json:"agent_role"`
	AgentSigningKeyPEM     string `json:"agent_signing_key_pem"`
	ValkeyHost             string `json:"valkey_host"`
	ValkeyPort             int    `json:"valkey_port"`
	ValkeyPassword         string `json:"valkey_password"`
	ForgejoURL             string `json:"forgejo_url"`
	OpenAIBaseURL          string `json:"openai_base_url"`
	OpenAIModel            string `json:"openai_model,omitempty"`
	OpenAIAPIKey           string `json:"openai_api_key,omitempty"`
	ControllerSSHPublicKey string `json:"controller_ssh_pub_key,omitempty"`
}

// FormatEnv formats the secrets payload as a dotenv string for agent.env.
func (s EnrollmentSecrets) FormatEnv() string {
	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("AGENT_NAME=%q\n", s.AgentName))
	buf.WriteString(fmt.Sprintf("AGENT_ROLE=%q\n", s.AgentRole))
	buf.WriteString(fmt.Sprintf("BP_AGENT=%q\n", s.AgentName))
	buf.WriteString(fmt.Sprintf("BP_PASSWORD=%q\n", s.ValkeyPassword))
	buf.WriteString(fmt.Sprintf("BP_HOST=%q\n", s.ValkeyHost))
	buf.WriteString(fmt.Sprintf("BP_PORT=\"%d\"\n", s.ValkeyPort))
	buf.WriteString(fmt.Sprintf("VALKEY_ADDR=\"%s:%d\"\n", s.ValkeyHost, s.ValkeyPort))
	buf.WriteString(fmt.Sprintf("VALKEY_PASSWORD=%q\n", s.ValkeyPassword))
	if s.ForgejoURL != "" {
		buf.WriteString(fmt.Sprintf("FORGEJO_URL=%q\n", s.ForgejoURL))
	}
	if s.OpenAIBaseURL != "" {
		buf.WriteString(fmt.Sprintf("OPENAI_BASE_URL=%q\n", s.OpenAIBaseURL))
	}
	if s.OpenAIModel != "" {
		buf.WriteString(fmt.Sprintf("OPENAI_MODEL=%q\n", s.OpenAIModel))
	}
	if s.OpenAIAPIKey != "" {
		buf.WriteString(fmt.Sprintf("OPENAI_API_KEY=%q\n", s.OpenAIAPIKey))
	}
	if s.ControllerSSHPublicKey != "" {
		buf.WriteString(fmt.Sprintf("CONTROLLER_SSH_PUB_KEY=%q\n", s.ControllerSSHPublicKey))
	}
	if s.AgentSigningKeyPEM != "" {
		buf.WriteString(fmt.Sprintf("BP_SIGNING_KEY_PEM=%q\n", s.AgentSigningKeyPEM))
		b64Key := base64.StdEncoding.EncodeToString([]byte(s.AgentSigningKeyPEM))
		buf.WriteString(fmt.Sprintf("BP_SIGNING_KEY_B64=%q\n", b64Key))
	}
	return buf.String()
}

// EnrollmentConfig specifies configuration for the ephemeral enrollment server.
type EnrollmentConfig struct {
	AgentName     string
	Token         string
	Secrets       EnrollmentSecrets
	Port          int
	ListenAddr    string
	TTL           time.Duration
	ServerURL     string
	InstallScript string
	BinDirs       []string
	Listener      net.Listener
}

// EnrollmentServer manages the lifecycle of the single-use secret distribution server.
type EnrollmentServer struct {
	agentName     string
	token         string
	secrets       EnrollmentSecrets
	ttl           time.Duration
	serverURL     string
	installScript string
	binDirs       []string

	listener   net.Listener
	httpServer *http.Server
	addr       string
	port       int

	mu           sync.Mutex
	tokenBurned  bool
	collected    chan struct{}
	stopped      chan struct{}
	completed    chan struct{}
	collectOnce  sync.Once
	stopOnce     sync.Once
	completeOnce sync.Once
	timer        *time.Timer
}

// GenerateEnrollmentToken creates a cryptographically secure random one-time enrollment token.
func GenerateEnrollmentToken() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed generating enrollment token: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// DetectHostLANIP attempts to determine the primary outbound LAN IPv4 address of the controller host.
func DetectHostLANIP(targetHost string) string {
	if envHost := os.Getenv("FLEET_CONTROLLER_HOST"); envHost != "" {
		return envHost
	}

	target := "8.8.8.8:80"
	if targetHost != "" && targetHost != "localhost" && targetHost != "127.0.0.1" {
		target = net.JoinHostPort(targetHost, "22")
	}

	conn, err := net.DialTimeout("udp", target, 500*time.Millisecond)
	if err == nil {
		defer func() { _ = conn.Close() }()
		if localAddr, ok := conn.LocalAddr().(*net.UDPAddr); ok && localAddr.IP != nil {
			ip := localAddr.IP.String()
			if ip != "127.0.0.1" && ip != "::1" && ip != "" {
				return ip
			}
		}
	}

	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
					if ip4 := ipNet.IP.To4(); ip4 != nil {
						return ip4.String()
					}
				}
			}
		}
	}

	return "127.0.0.1"
}

// RewriteURLForLAN replaces localhost/127.0.0.1/container aliases in a URL with the controller LAN IP.
func RewriteURLForLAN(rawURL string, lanHost string) string {
	if rawURL == "" || lanHost == "" || lanHost == "127.0.0.1" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	hostname := u.Hostname()
	switch strings.ToLower(hostname) {
	case "localhost", "127.0.0.1", "llm-gateway", "fleet-openrouter", "forgejo", "valkey":
		port := u.Port()
		if port != "" {
			u.Host = net.JoinHostPort(lanHost, port)
		} else {
			u.Host = lanHost
		}
		return u.String()
	default:
		return rawURL
	}
}

// NewEnrollmentServer initializes a new EnrollmentServer.
func NewEnrollmentServer(cfg EnrollmentConfig) (*EnrollmentServer, error) {
	name := strings.ToLower(strings.TrimSpace(cfg.AgentName))
	if name == "" {
		return nil, fmt.Errorf("agent name cannot be empty")
	}

	token := cfg.Token
	if token == "" {
		var err error
		token, err = GenerateEnrollmentToken()
		if err != nil {
			return nil, err
		}
	}

	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultEnrollmentTTL
	}

	srv := &EnrollmentServer{
		agentName:     name,
		token:         token,
		secrets:       cfg.Secrets,
		ttl:           ttl,
		serverURL:     cfg.ServerURL,
		installScript: cfg.InstallScript,
		binDirs:       cfg.BinDirs,
		listener:      cfg.Listener,
		collected:     make(chan struct{}),
		stopped:       make(chan struct{}),
		completed:     make(chan struct{}),
	}

	if srv.installScript == "" {
		srv.installScript = loadDefaultInstallScript()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/install/", srv.handleInstall)
	mux.HandleFunc("/api/v1/enroll/", srv.handleEnroll)
	mux.HandleFunc("/api/v1/complete/", srv.handleComplete)
	mux.HandleFunc("/bin/", srv.handleBinary)

	srv.httpServer = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	if cfg.Listener != nil {
		srv.listener = cfg.Listener
		srv.addr = cfg.Listener.Addr().String()
		if tcpAddr, ok := cfg.Listener.Addr().(*net.TCPAddr); ok {
			srv.port = tcpAddr.Port
		}
	} else {
		port := cfg.Port
		if port <= 0 {
			port = DefaultEnrollmentPort
		}
		listenAddr := cfg.ListenAddr
		if listenAddr == "" {
			listenAddr = fmt.Sprintf(":%d", port)
		}

		ln, err := net.Listen("tcp", listenAddr)
		if err != nil && port == DefaultEnrollmentPort && cfg.ListenAddr == "" {
			// Fallback to random free port if default port is in use
			ln, err = net.Listen("tcp", ":0")
		}
		if err != nil {
			return nil, fmt.Errorf("failed listening for enrollment server: %w", err)
		}

		srv.listener = ln
		srv.addr = ln.Addr().String()
		if tcpAddr, ok := ln.Addr().(*net.TCPAddr); ok {
			srv.port = tcpAddr.Port
		}
	}

	return srv, nil
}

// Start begins serving requests on the configured listener in a background goroutine.
func (s *EnrollmentServer) Start() {
	s.mu.Lock()
	s.timer = time.AfterFunc(s.ttl, func() {
		_ = s.Shutdown(context.Background())
	})
	s.mu.Unlock()

	go func() {
		defer func() {
			s.stopOnce.Do(func() {
				close(s.stopped)
			})
		}()

		if err := s.httpServer.Serve(s.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			_ = s.Shutdown(context.Background())
		}
	}()
}

// Port returns the TCP port on which the enrollment server is listening.
func (s *EnrollmentServer) Port() int {
	return s.port
}

// Token returns the one-time token required for secret enrollment.
func (s *EnrollmentServer) Token() string {
	return s.token
}

// TokenBurned returns true if the secret token has been consumed.
func (s *EnrollmentServer) TokenBurned() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokenBurned
}

// URL returns the base HTTP URL of the enrollment server.
func (s *EnrollmentServer) URL(host string) string {
	if s.serverURL != "" {
		return s.serverURL
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%d", host, s.port)
}

// InstallCommand returns the exact curl command to execute on the baremetal machine.
func (s *EnrollmentServer) InstallCommand(host string) string {
	return fmt.Sprintf("curl -sSL %s/install/%s?token=%s | bash", s.URL(host), s.agentName, s.token)
}

// Wait blocks until the secrets are collected, the context is cancelled, or the server stops.
func (s *EnrollmentServer) Wait(ctx context.Context) error {
	select {
	case <-s.collected:
		return nil
	case <-ctx.Done():
		_ = s.Shutdown(context.Background())
		return ctx.Err()
	case <-s.stopped:
		s.mu.Lock()
		burned := s.tokenBurned
		s.mu.Unlock()
		if burned {
			return nil
		}
		return fmt.Errorf("enrollment timed out before credentials were collected")
	}
}

// Completed returns true if the agent node has signaled setup completion.
func (s *EnrollmentServer) Completed() bool {
	select {
	case <-s.completed:
		return true
	default:
		return false
	}
}

// WaitForCompletion blocks until the agent signals completion via /api/v1/complete,
// the context is cancelled, the server stops, or a 60-second grace period elapses.
func (s *EnrollmentServer) WaitForCompletion(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()

	select {
	case <-s.completed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stopped:
		if err := ctx.Err(); err != nil {
			return err
		}
		return nil
	case <-timer.C:
		if err := ctx.Err(); err != nil {
			return err
		}
		return nil
	}
}

// Shutdown gracefully stops the enrollment HTTP server.
func (s *EnrollmentServer) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()

	defer s.stopOnce.Do(func() {
		close(s.stopped)
	})

	shutdownCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	return s.httpServer.Shutdown(shutdownCtx)
}

func (s *EnrollmentServer) handleInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	reqAgent := strings.TrimPrefix(r.URL.Path, "/install/")
	reqAgent = strings.Trim(reqAgent, "/")
	if !strings.EqualFold(reqAgent, s.agentName) {
		http.Error(w, "Agent not found", http.StatusNotFound)
		return
	}

	content := s.installScript
	queryToken := r.URL.Query().Get("token")
	if queryToken != "" {
		content = strings.ReplaceAll(content, "__FLEET_ENROLL_TOKEN__", queryToken)
	}

	hostHeader := r.Host
	if hostHeader == "" {
		hostHeader = fmt.Sprintf("127.0.0.1:%d", s.port)
	}
	serverURL := fmt.Sprintf("http://%s", hostHeader)
	content = strings.ReplaceAll(content, "__FLEET_ENROLL_SERVER__", serverURL)
	content = strings.ReplaceAll(content, "__FLEET_AGENT_NAME__", s.agentName)

	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, content)
}

func (s *EnrollmentServer) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	reqAgent := strings.TrimPrefix(r.URL.Path, "/api/v1/enroll/")
	reqAgent = strings.Trim(reqAgent, "/")
	if !strings.EqualFold(reqAgent, s.agentName) {
		http.Error(w, "Agent not found", http.StatusNotFound)
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
		http.Error(w, "Unauthorized: invalid or missing enrollment token", http.StatusUnauthorized)
		return
	}

	s.mu.Lock()
	if s.tokenBurned {
		s.mu.Unlock()
		http.Error(w, "Gone: token has already been burned", http.StatusGone)
		return
	}
	s.tokenBurned = true
	s.mu.Unlock()

	// Format response
	format := strings.ToLower(r.URL.Query().Get("format"))
	accept := strings.ToLower(r.Header.Get("Accept"))

	var payload []byte
	var contentType string

	if format == "json" || strings.Contains(accept, "application/json") {
		var err error
		payload, err = json.MarshalIndent(s.secrets, "", "  ")
		if err != nil {
			http.Error(w, "Internal error formatting secrets", http.StatusInternalServerError)
			return
		}
		contentType = "application/json"
	} else {
		payload = []byte(s.secrets.FormatEnv())
		contentType = "text/plain; charset=utf-8"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Trigger collection notification
	s.collectOnce.Do(func() {
		close(s.collected)
	})

	// Keep server running with a 5-minute grace period before shutdown so binary downloads complete
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(5*time.Minute, func() {
		_ = s.Shutdown(context.Background())
	})
	s.mu.Unlock()
}

func (s *EnrollmentServer) handleComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	reqAgent := strings.TrimPrefix(r.URL.Path, "/api/v1/complete/")
	reqAgent = strings.Trim(reqAgent, "/")
	if !strings.EqualFold(reqAgent, s.agentName) {
		http.Error(w, "Agent not found", http.StatusNotFound)
		return
	}

	s.completeOnce.Do(func() {
		close(s.completed)
	})

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "OK\n")
}

func (s *EnrollmentServer) handleBinary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	binName := strings.TrimPrefix(r.URL.Path, "/bin/")
	binName = filepath.Base(binName)
	if binName != "fleet-agent" && binName != "bp" {
		http.Error(w, "Invalid binary name", http.StatusBadRequest)
		return
	}

	// Search binary directories
	dirs := s.binDirs
	if len(dirs) == 0 {
		if home, err := os.UserHomeDir(); err == nil {
			dirs = append(dirs, filepath.Join(home, ".local", "bin"))
		}
		if cwd, err := os.Getwd(); err == nil {
			dirs = append(dirs, filepath.Join(cwd, "bin"))
		}
	}

	for _, d := range dirs {
		if d == "" {
			continue
		}
		candidate := filepath.Join(d, binName)
		// #nosec G304 -- strictly controlled binary file path
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() && fi.Mode()&0111 != 0 {
			f, err := os.Open(candidate)
			if err != nil {
				continue
			}
			defer func() { _ = f.Close() }()
			w.Header().Set("Content-Type", "application/octet-stream")
			http.ServeContent(w, r, binName, fi.ModTime(), f)
			return
		}
	}

	http.Error(w, "Binary not hosted on controller", http.StatusNotFound)
}

func loadDefaultInstallScript() string {
	// Try reading install-agent.sh from repository if accessible
	for _, path := range []string{
		"install-agent.sh",
		"../../install-agent.sh",
		"../../../install-agent.sh",
	} {
		// #nosec G304 -- loading fixed repo install script
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return string(data)
		}
	}

	// Embedded fallback template
	return `#!/usr/bin/env bash
# Copyright (c) 2026 Boggy Creek Software LLC
set -euo pipefail
DEFAULT_SERVER="__FLEET_ENROLL_SERVER__"
DEFAULT_TOKEN="__FLEET_ENROLL_TOKEN__"
DEFAULT_NAME="__FLEET_AGENT_NAME__"
ENROLL_SERVER="${ENROLL_SERVER:-$DEFAULT_SERVER}"
ENROLL_TOKEN="${ENROLL_TOKEN:-$DEFAULT_TOKEN}"
AGENT_NAME="${AGENT_NAME:-$DEFAULT_NAME}"
TARGET_USER="${TARGET_USER:-$AGENT_NAME}"

if [[ "${EUID}" -eq 0 ]]; then
    echo "==> Running as root/sudo: auto-provisioning dedicated user '${TARGET_USER}'..."
    if ! id -u "${TARGET_USER}" >/dev/null 2>&1; then
        useradd -m -s /bin/bash -c "Fleet Autonomous Agent (${AGENT_NAME})" "${TARGET_USER}"
    fi
    USER_HOME=$(getent passwd "${TARGET_USER}" | cut -d: -f6)
    chmod 0700 "${USER_HOME}"
    if [[ -n "${SUDO_USER:-}" ]]; then
        SUDO_HOME=$(getent passwd "${SUDO_USER}" | cut -d: -f6)
        if [[ -f "${SUDO_HOME}/.ssh/authorized_keys" ]]; then
            mkdir -p "${USER_HOME}/.ssh"
            cp "${SUDO_HOME}/.ssh/authorized_keys" "${USER_HOME}/.ssh/authorized_keys"
            chmod 0700 "${USER_HOME}/.ssh"
            chmod 0600 "${USER_HOME}/.ssh/authorized_keys"
            chown -R "${TARGET_USER}:${TARGET_USER}" "${USER_HOME}/.ssh"
        fi
    fi
    if command -v loginctl >/dev/null 2>&1; then
        loginctl enable-linger "${TARGET_USER}" || true
    fi
    TMP_SCRIPT=$(mktemp "${USER_HOME}/.fleet-install-XXXXXX.sh" 2>/dev/null || mktemp /tmp/fleet-install-XXXXXX.sh)
    if [[ -f "$0" ]]; then
        cp "$0" "${TMP_SCRIPT}"
    else
        curl -sSf "${ENROLL_SERVER}/install/${AGENT_NAME}?token=${ENROLL_TOKEN}" -o "${TMP_SCRIPT}"
    fi
    chmod 0755 "${TMP_SCRIPT}"
    chown -h "${TARGET_USER}:${TARGET_USER}" "${TMP_SCRIPT}"
    # shellcheck disable=SC2016
    printf '%s\n%s\n%s\n' "${ENROLL_SERVER}" "${ENROLL_TOKEN}" "${AGENT_NAME}" | \
      su - "${TARGET_USER}" -s /bin/bash -- -c \
        'read -r ENROLL_SERVER; read -r ENROLL_TOKEN; read -r AGENT_NAME; export ENROLL_SERVER ENROLL_TOKEN AGENT_NAME; "$1"' \
        -- "${TMP_SCRIPT}"
    rm -f "${TMP_SCRIPT}"
    exit 0
fi

BIN_DIR="${HOME}/.local/bin"
CONFIG_DIR="${HOME}/.config/fleet"
SYSTEMD_DIR="${HOME}/.config/systemd/user"
WORKSPACE_DIR="${HOME}/workspace"
mkdir -p "${BIN_DIR}" "${CONFIG_DIR}" "${SYSTEMD_DIR}" "${WORKSPACE_DIR}"
chmod 0700 "${CONFIG_DIR}"
ENV_FILE="${CONFIG_DIR}/agent.env"
echo "==> Collecting secrets from ${ENROLL_SERVER}..."
curl -sSf "${ENROLL_SERVER}/api/v1/enroll/${AGENT_NAME}?token=${ENROLL_TOKEN}" -o "${ENV_FILE}"
chmod 0600 "${ENV_FILE}"
if grep -q "^CONTROLLER_SSH_PUB_KEY=" "${ENV_FILE}"; then
    CONTROLLER_PUB_KEY=$(grep "^CONTROLLER_SSH_PUB_KEY=" "${ENV_FILE}" | cut -d'"' -f2)
    if [[ -n "${CONTROLLER_PUB_KEY}" ]]; then
        mkdir -p "${HOME}/.ssh"
        chmod 0700 "${HOME}/.ssh"
        touch "${HOME}/.ssh/authorized_keys"
        chmod 0600 "${HOME}/.ssh/authorized_keys"
        if ! grep -qF "${CONTROLLER_PUB_KEY}" "${HOME}/.ssh/authorized_keys" 2>/dev/null; then
            echo "${CONTROLLER_PUB_KEY}" >> "${HOME}/.ssh/authorized_keys"
        fi
    fi
fi
KEY_FILE="${CONFIG_DIR}/signing-key.pem"
if grep -q "^BP_SIGNING_KEY_B64=" "${ENV_FILE}"; then
    grep "^BP_SIGNING_KEY_B64=" "${ENV_FILE}" | cut -d'"' -f2 | base64 -d > "${KEY_FILE}"
    chmod 0600 "${KEY_FILE}"
    if ! grep -q "^BP_SIGNING_KEY=" "${ENV_FILE}"; then
        echo "BP_SIGNING_KEY=${KEY_FILE}" >> "${ENV_FILE}"
    fi
elif grep -q "BP_SIGNING_KEY_PEM=" "${ENV_FILE}"; then
    sed -n '/BP_SIGNING_KEY_PEM="/,/^"/p' "${ENV_FILE}" | sed 's/BP_SIGNING_KEY_PEM="//' | sed 's/"$//' > "${KEY_FILE}"
    chmod 0600 "${KEY_FILE}"
    if ! grep -q "^BP_SIGNING_KEY=" "${ENV_FILE}"; then
        echo "BP_SIGNING_KEY=${KEY_FILE}" >> "${ENV_FILE}"
    fi
fi
echo "WORKSPACE_DIR=${WORKSPACE_DIR}" >> "${ENV_FILE}"
for bin in fleet-agent bp; do
    if [[ ! -x "${BIN_DIR}/${bin}" ]]; then
        curl -sSf -o "${BIN_DIR}/${bin}" "${ENROLL_SERVER}/bin/${bin}" 2>/dev/null || true
        [[ -f "${BIN_DIR}/${bin}" ]] && chmod 0755 "${BIN_DIR}/${bin}"
    fi
done
SERVICE_FILE="${SYSTEMD_DIR}/fleet-agent.service"
cat > "${SERVICE_FILE}" <<EOF
[Unit]
Description=Fleet Autonomous Coding Agent Daemon (${AGENT_NAME})
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
EnvironmentFile=%h/.config/fleet/agent.env
ExecStart=%h/.local/bin/fleet-agent
Restart=always
RestartSec=5s
StandardOutput=journal
StandardError=journal
[Install]
WantedBy=default.target
EOF
if command -v systemctl >/dev/null 2>&1; then
    export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
    export DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-unix:path=${XDG_RUNTIME_DIR}/bus}"
    systemctl --user daemon-reload
    systemctl --user enable --now fleet-agent.service
    echo "==> fleet-agent.service enabled and started!"
fi
curl -sSf -X POST "${ENROLL_SERVER}/api/v1/complete/${AGENT_NAME}" >/dev/null 2>&1 || curl -sSf "${ENROLL_SERVER}/api/v1/complete/${AGENT_NAME}" >/dev/null 2>&1 || true
`
}
