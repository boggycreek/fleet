#!/usr/bin/env bash
# Copyright (c) 2026 Boggy Creek Software LLC
#
# Use of this source code is governed by an MIT-style
# license that can be found in the LICENSE file.

set -euo pipefail

# Configurable defaults (can be populated dynamically by the enrollment server)
DEFAULT_SERVER="__FLEET_ENROLL_SERVER__"
DEFAULT_TOKEN="__FLEET_ENROLL_TOKEN__"
DEFAULT_NAME="__FLEET_AGENT_NAME__"

ENROLL_SERVER="${ENROLL_SERVER:-$DEFAULT_SERVER}"
ENROLL_TOKEN="${ENROLL_TOKEN:-$DEFAULT_TOKEN}"
AGENT_NAME="${AGENT_NAME:-$DEFAULT_NAME}"

usage() {
    cat <<EOF
Usage: $(basename "$0") [options]

Bootstraps and enrolls a baremetal Fleet agent node.

Options:
  --server <url>     Enrollment server base URL (e.g. http://192.168.1.100:8765)
  --token <token>    One-time secret enrollment token
  --name <name>      Target agent name
  -h, --help         Show this help message
EOF
    exit 1
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --server)
            ENROLL_SERVER="$2"
            shift 2
            ;;
        --token)
            ENROLL_TOKEN="$2"
            shift 2
            ;;
        --name)
            AGENT_NAME="$2"
            shift 2
            ;;
        -h|--help)
            usage
            ;;
        *)
            echo "Unknown argument: $1" >&2
            usage
            ;;
    esac
done

if [[ -z "$ENROLL_SERVER" || "$ENROLL_SERVER" == "__FLEET_ENROLL_SERVER__" ]]; then
    echo "Error: --server is required (e.g. --server http://192.168.1.100:8765)" >&2
    exit 1
fi

if [[ -z "$ENROLL_TOKEN" || "$ENROLL_TOKEN" == "__FLEET_ENROLL_TOKEN__" ]]; then
    echo "Error: --token is required" >&2
    exit 1
fi

if [[ -z "$AGENT_NAME" || "$AGENT_NAME" == "__FLEET_AGENT_NAME__" ]]; then
    echo "Error: --name is required" >&2
    exit 1
fi

echo "==> Enrolling Fleet agent '${AGENT_NAME}' from controller at ${ENROLL_SERVER}..."

# 1. Prepare directory confinement in unprivileged $HOME
BIN_DIR="${HOME}/.local/bin"
CONFIG_DIR="${HOME}/.config/fleet"
SYSTEMD_DIR="${HOME}/.config/systemd/user"
WORKSPACE_DIR="${HOME}/workspace"
STATE_DIR="${HOME}/.local/state/fleet"

mkdir -p "${BIN_DIR}" "${CONFIG_DIR}" "${SYSTEMD_DIR}" "${WORKSPACE_DIR}" "${STATE_DIR}"
chmod 0700 "${CONFIG_DIR}"

# 2. Collect secrets payload from enrollment server (burns token)
ENV_FILE="${CONFIG_DIR}/agent.env"
echo "==> Collecting secrets payload from enrollment server..."
HTTP_STATUS=$(curl -sS -w "%{http_code}" -o "${ENV_FILE}" \
    "${ENROLL_SERVER}/api/v1/enroll/${AGENT_NAME}?token=${ENROLL_TOKEN}")

if [[ "${HTTP_STATUS}" -ne 200 ]]; then
    echo "Error: Enrollment failed with HTTP status ${HTTP_STATUS}" >&2
    if [[ -f "${ENV_FILE}" ]]; then
        cat "${ENV_FILE}" >&2
        rm -f "${ENV_FILE}"
    fi
    exit 1
fi
chmod 0600 "${ENV_FILE}"

# Extract signing key PEM if present in agent.env
KEY_FILE="${CONFIG_DIR}/signing-key.pem"
if grep -q "BP_SIGNING_KEY_PEM=" "${ENV_FILE}"; then
    sed -n '/BP_SIGNING_KEY_PEM="/,/^"/p' "${ENV_FILE}" | \
        sed 's/BP_SIGNING_KEY_PEM="//' | sed 's/"$//' > "${KEY_FILE}"
    chmod 0600 "${KEY_FILE}"
    if ! grep -q "^BP_SIGNING_KEY=" "${ENV_FILE}"; then
        echo "BP_SIGNING_KEY=${KEY_FILE}" >> "${ENV_FILE}"
    fi
fi

if ! grep -q "^WORKSPACE_DIR=" "${ENV_FILE}"; then
    echo "WORKSPACE_DIR=${WORKSPACE_DIR}" >> "${ENV_FILE}"
fi

echo "==> Agent secrets securely stored at ${ENV_FILE}"

# 3. Download or verify binaries (fleet-agent and bp)
ARCH=$(uname -m)
case "$ARCH" in
    x86_64) GOARCH="amd64" ;;
    aarch64|arm64) GOARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

OS=$(uname -s | tr '[:upper:]' '[:lower:]')

download_binary() {
    local bin_name="$1"
    local target="${BIN_DIR}/${bin_name}"
    
    if [[ -x "${target}" ]]; then
        echo "==> Using existing binary at ${target}"
        return 0
    fi

    echo "==> Downloading ${bin_name} from enrollment server..."
    if curl -sSf -o "${target}" "${ENROLL_SERVER}/bin/${bin_name}" 2>/dev/null; then
        chmod 0755 "${target}"
        echo "==> Installed ${bin_name} to ${target}"
        return 0
    fi

    echo "==> Enrollment server does not host binary. Fetching from GitHub releases..."
    local gh_url="https://github.com/boggycreek/fleet/releases/latest/download/${bin_name}-${OS}-${GOARCH}"
    if curl -sSLf -o "${target}" "${gh_url}" 2>/dev/null; then
        chmod 0755 "${target}"
        echo "==> Installed ${bin_name} from GitHub to ${target}"
        return 0
    fi

    echo "Warning: Unable to automatically download ${bin_name}. Ensure it is placed in ${BIN_DIR}." >&2
}

download_binary "fleet-agent"
download_binary "bp"

# Ensure ~/.local/bin is in PATH for login shells
if [[ ":$PATH:" != *":${BIN_DIR}:"* ]]; then
    PROFILE_FILE="${HOME}/.profile"
    [[ -f "${HOME}/.bashrc" ]] && PROFILE_FILE="${HOME}/.bashrc"
    if ! grep -q "${BIN_DIR}" "${PROFILE_FILE}" 2>/dev/null; then
        echo "export PATH=\"\${HOME}/.local/bin:\$PATH\"" >> "${PROFILE_FILE}"
    fi
fi

# 4. Install systemd --user service
SERVICE_FILE="${SYSTEMD_DIR}/fleet-agent.service"
cat > "${SERVICE_FILE}" <<EOF
[Unit]
Description=Fleet Autonomous Coding Agent Daemon (${AGENT_NAME})
Documentation=https://github.com/boggycreek/fleet
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
LimitNOFILE=65536

[Install]
WantedBy=default.target
EOF

echo "==> Installed systemd user service: ${SERVICE_FILE}"

# 5. Enable and start user service
if command -v systemctl >/dev/null 2>&1; then
    echo "==> Reloading systemd user daemon and starting fleet-agent service..."
    systemctl --user daemon-reload
    systemctl --user enable --now fleet-agent.service

    if systemctl --user is-active --quiet fleet-agent.service; then
        echo "==> [✓] fleet-agent.service is active and running!"
    else
        echo "==> [!] fleet-agent.service was started. Check status: systemctl --user status fleet-agent"
    fi
else
    echo "Notice: systemctl not found. Service installed at ${SERVICE_FILE}."
fi

cat <<EOF

============================================================
 Fleet Baremetal Agent Enrollment Complete!
============================================================
 Agent Name:   ${AGENT_NAME}
 Config File:  ${ENV_FILE}
 Workspace:    ${WORKSPACE_DIR}
 Service:      ${SERVICE_FILE}

 Useful Commands:
   Inspect service: systemctl --user status fleet-agent
   Live logs:       journalctl --user -u fleet-agent -f
   Stop service:    systemctl --user stop fleet-agent
   Restart:         systemctl --user restart fleet-agent

 Tip for headless machines (Dell 7559 / Debian):
   To allow the user service to continue running after logout:
   loginctl enable-linger \${USER}
============================================================
EOF
