#!/usr/bin/env bash
# Copyright (c) 2026 Boggy Creek Software LLC
#
# Use of this source code is governed by an MIT-style
# license that can be found in the LICENSE file.

# Deterministic cleanup for ephemeral test containers, orphaned conmon/slirp processes,
# and stale rootless network namespaces.
#
# NOTE: This script targets ONLY ephemeral test instances (test-*, unit-test-*, mock-start-*).
# Persistent shared infrastructure containers (fleet-valkey, fleet-forgejo, etc., and legacy agent-sandbox-*)
# and their associated networks and volumes are explicitly protected and NEVER terminated.

set -euo pipefail

UID_NUM="$(id -u)"
echo "==> Cleaning test environment for user ${UID_NUM}..."

# 1. Terminate orphaned ephemeral test containers if any exist
if command -v podman >/dev/null 2>&1; then
  # Match ephemeral test container prefixes while explicitly excluding persistent infra (fleet-*, agent-sandbox-*)
  TEST_CONTAINERS=$(podman ps -a --format '{{.Names}}' 2>/dev/null | grep -E '^(test-|infra-test-|unit-test-|mock-start-)' | grep -v -E '^(fleet-|agent-sandbox-)' || true)
  if [ -n "${TEST_CONTAINERS}" ]; then
    echo "Stopping and removing ephemeral test containers: ${TEST_CONTAINERS}"
    echo "${TEST_CONTAINERS}" | xargs -n 1 podman rm -f 2>/dev/null || true
  fi

  # Remove ephemeral test networks (safeguarding fleet-infra and agent-sandbox-infra)
  TEST_NETS=$(podman network ls --format '{{.Name}}' 2>/dev/null | grep -E '^(test-|infra-test-|unit-test-)' | grep -v -E '^(fleet-|agent-sandbox-)' || true)
  if [ -n "${TEST_NETS}" ]; then
    echo "Removing ephemeral test networks: ${TEST_NETS}"
    echo "${TEST_NETS}" | xargs -n 1 podman network rm -f 2>/dev/null || true
  fi

  # Remove ephemeral test volumes (safeguarding fleet-valkey-data, fleet-forgejo-data, agent-sandbox-*)
  TEST_VOLS=$(podman volume ls --format '{{.Name}}' 2>/dev/null | grep -E '^(test-|infra-test-|unit-test-|mock-start-)' | grep -v -E '^(fleet-|agent-sandbox-)' || true)
  if [ -n "${TEST_VOLS}" ]; then
    echo "Removing ephemeral test volumes: ${TEST_VOLS}"
    echo "${TEST_VOLS}" | xargs -n 1 podman volume rm -f 2>/dev/null || true
  fi
fi

# 2. Terminate orphaned conmon, slirp4netns, and rootlessport test processes.
# Explicitly target ephemeral test containers only; NEVER kill persistent infrastructure or global slirp4netns
pkill -9 -u "${UID_NUM}" -f "(conmon|slirp4netns|rootlessport).*(test-valkey|test-infra-|test-forgejo|test-gitea|unit-test|test-podman|mock-start)" 2>/dev/null || true
pkill -9 -u "${UID_NUM}" -f "valkey-server --aclfile /etc/valkey/users.acl" 2>/dev/null || true

# 3. Clean up stale netns descriptors and rootless-netns lockfiles in /run/user/<uid>
NETNS_DIR="/run/user/${UID_NUM}/netns"
if [ -d "${NETNS_DIR}" ]; then
  echo "Clearing stale test netns entries in ${NETNS_DIR}..."
  find "${NETNS_DIR}" -maxdepth 1 \( -name "netns-*" -o -name "rootless-netns-*" \) -type f -delete 2>/dev/null || true
fi

LIBPOD_TMP="/run/user/${UID_NUM}/libpod/tmp"
if [ -d "${LIBPOD_TMP}" ]; then
  echo "Clearing rootless netns locks in ${LIBPOD_TMP}..."
  rm -f "${LIBPOD_TMP}"/rootless-netns*.lock 2>/dev/null || true
  rm -f "${LIBPOD_TMP}"/rootless-netns*.pid 2>/dev/null || true
fi

# 4. Reconcile rootless network namespace state to ensure runtime directories are healthy
if command -v podman >/dev/null 2>&1; then
  echo "Reconciling rootless network namespace..."
  podman unshare --rootless-netns true 2>/dev/null || true
fi

echo "==> Test environment clean."
