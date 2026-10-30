#!/usr/bin/env bash
# Copyright (c) 2026 Boggy Creek Software LLC
#
# Use of this source code is governed by an MIT-style
# license that can be found in the LICENSE file.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Search locations for SonarQube Community Edition distribution archive
SEARCH_PATHS=(
    "${1:-}"
    "${HOME}/Downloads"
    "${REPO_DIR}"
    "/tmp"
)

ZIP_FILE=""
for p in "${SEARCH_PATHS[@]}"; do
    if [ -n "${p}" ] && [ -f "${p}" ] && [[ "${p}" == *sonar*.zip ]]; then
        ZIP_FILE="${p}"
        break
    elif [ -n "${p}" ] && [ -d "${p}" ]; then
        FOUND=$(find "${p}" -maxdepth 2 -name "sonarqube-*.zip" -type f 2>/dev/null | head -n 1 || true)
        if [ -n "${FOUND}" ] && [ -f "${FOUND}" ]; then
            ZIP_FILE="${FOUND}"
            break
        fi
    fi
done

if [ -z "${ZIP_FILE}" ] || [ ! -f "${ZIP_FILE}" ]; then
    echo "ERROR: SonarQube distribution zip archive not found." >&2
    echo "Please download SonarQube Server Community Edition zip or specify the path:" >&2
    echo "  $0 /path/to/sonarqube-*.zip" >&2
    exit 1
fi

echo "==> Using SonarQube distribution archive: ${ZIP_FILE}"

BUILD_TMP="$(umask 077 && mktemp -d "${TMPDIR:-/tmp}/sonarqube-build-XXXXXX")"
cleanup() {
    echo "==> Cleaning temporary build workspace..."
    if [ -n "${BUILD_TMP:-}" ] && [ -d "${BUILD_TMP}" ]; then
        rm -rf "${BUILD_TMP}"
    fi
}
trap cleanup EXIT

echo "==> Extracting SonarQube distribution archive..."
unzip -q "${ZIP_FILE}" -d "${BUILD_TMP}"

EXTRACTED_DIR=$(find "${BUILD_TMP}" -mindepth 1 -maxdepth 1 -type d -name "sonarqube*" | head -n 1)
if [ -z "${EXTRACTED_DIR}" ] || [ ! -d "${EXTRACTED_DIR}" ]; then
    echo "ERROR: Failed finding extracted sonarqube directory in ${BUILD_TMP}" >&2
    exit 1
fi

DIR_BASENAME="$(basename "${EXTRACTED_DIR}")"
echo "==> Building fleet-sonarqube:latest via Podman..."
podman build \
    -t fleet-sonarqube:latest \
    --build-arg SONARQUBE_DIR="${DIR_BASENAME}" \
    -f "${REPO_DIR}/images/infra/sonarqube/Dockerfile" \
    "${BUILD_TMP}"

echo "==> SonarQube image fleet-sonarqube:latest successfully built!"
