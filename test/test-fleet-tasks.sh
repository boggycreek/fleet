#!/usr/bin/env bash
# Copyright (c) 2026 Boggy Creek Software LLC
#
# Use of this source code is governed by an MIT-style
# license that can be found in the LICENSE file.

# Smoke test for fleet-tasks per-repo git identity configuration.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
FLEET_TASKS_BIN="${REPO_DIR}/images/agent-base/fleet-tasks"

if [ ! -f "${FLEET_TASKS_BIN}" ]; then
  echo "Error: ${FLEET_TASKS_BIN} not found" >&2
  exit 1
fi

TEMP_HOME="$(mktemp -d)"
cleanup() {
  rm -rf "${TEMP_HOME}"
}
trap cleanup EXIT

export HOME="${TEMP_HOME}"
export FLEET_TASKS_DIR="${TEMP_HOME}/tasks"
export AGENT_NAME="test-agent-42"
export FORGEJO_URL="http://127.0.0.1:9999" # invalid port so clone fails gracefully and falls back to git init

# Mock bd if bd command is called during init
MOCK_BIN="${TEMP_HOME}/bin"
mkdir -p "${MOCK_BIN}"
cat << 'EOF' > "${MOCK_BIN}/bd"
#!/bin/bash
exit 0
EOF
chmod +x "${MOCK_BIN}/bd"
export PATH="${MOCK_BIN}:${PATH}"

# Set global git config to human operator / host identity
git config --global user.name "Human Operator"
git config --global user.email "human@example.com"

echo "==> Running fleet-tasks init..."
"${FLEET_TASKS_BIN}" init

echo "==> Verifying per-repo local git identity in ${FLEET_TASKS_DIR}..."
LOCAL_NAME="$(git -C "${FLEET_TASKS_DIR}" config --local user.name || true)"
LOCAL_EMAIL="$(git -C "${FLEET_TASKS_DIR}" config --local user.email || true)"

if [ "${LOCAL_NAME}" != "test-agent-42" ]; then
  echo "FAIL: Expected local user.name 'test-agent-42', got '${LOCAL_NAME}'" >&2
  exit 1
fi

if [ "${LOCAL_EMAIL}" != "test-agent-42@local.sndbx" ]; then
  echo "FAIL: Expected local user.email 'test-agent-42@local.sndbx', got '${LOCAL_EMAIL}'" >&2
  exit 1
fi

echo "==> Verifying global git identity remained unaffected..."
GLOBAL_NAME="$(git config --global user.name || true)"
GLOBAL_EMAIL="$(git config --global user.email || true)"

if [ "${GLOBAL_NAME}" != "Human Operator" ]; then
  echo "FAIL: Global user.name modified! Expected 'Human Operator', got '${GLOBAL_NAME}'" >&2
  exit 1
fi

if [ "${GLOBAL_EMAIL}" != "human@example.com" ]; then
  echo "FAIL: Global user.email modified! Expected 'human@example.com', got '${GLOBAL_EMAIL}'" >&2
  exit 1
fi

echo "==> Re-running fleet-tasks init to verify idempotency..."
"${FLEET_TASKS_BIN}" init

LOCAL_NAME="$(git -C "${FLEET_TASKS_DIR}" config --local user.name || true)"
LOCAL_EMAIL="$(git -C "${FLEET_TASKS_DIR}" config --local user.email || true)"

if [ "${LOCAL_NAME}" != "test-agent-42" ] || [ "${LOCAL_EMAIL}" != "test-agent-42@local.sndbx" ]; then
  echo "FAIL: Idempotency check failed" >&2
  exit 1
fi

echo "=========================================================="
echo " fleet-tasks per-repo git identity test passed!"
echo "=========================================================="
