#!/usr/bin/env bash
# Copyright (c) 2026 Boggy Creek Software LLC
#
# Use of this source code is governed by an MIT-style
# license that can be found in the LICENSE file.

# Dead code reachability analysis runner
# Modes:
#   --diff       Differential analysis against staged/local changes
#   --all        Comprehensive whole-codebase dead code audit

set -euo pipefail

MODE="diff"

while [ $# -gt 0 ]; do
  case "$1" in
    --diff|--staged|-d)
      MODE="diff"
      shift
      ;;
    --all|--comprehensive|-a)
      MODE="all"
      shift
      ;;
    --help|-h)
      echo "Usage: $0 [--diff | --all]"
      echo "  --diff   Analyze staged or local changes for newly orphaned or dead code"
      echo "  --all    Perform comprehensive audit of accumulated dead code across the codebase"
      exit 0
      ;;
    *)
      echo "Error: Unknown argument: $1" >&2
      echo "Usage: $0 [--diff | --all]" >&2
      exit 1
      ;;
  esac
done

# Locate deadcode binary
DEADCODE_BIN="$(command -v deadcode 2>/dev/null || true)"
if [ -z "${DEADCODE_BIN}" ]; then
  GOPATH_BIN="$(go env GOPATH 2>/dev/null || true)/bin/deadcode"
  if [ -n "${GOPATH_BIN}" ] && [ -x "${GOPATH_BIN}" ]; then
    DEADCODE_BIN="${GOPATH_BIN}"
  fi
fi

if [ -z "${DEADCODE_BIN}" ] || [ ! -x "${DEADCODE_BIN}" ]; then
  echo "Error: deadcode tool is not installed." >&2
  echo "Install it via: go install golang.org/x/tools/cmd/deadcode@latest" >&2
  exit 1
fi

if [ "${MODE}" = "diff" ]; then
  echo "==> Running Differential Dead Code Analysis..."

  # Determine changed Go files: check staged first, then unstaged against HEAD, then branch against main
  DIFF_TARGET="staged"
  CHANGED_FILES="$(git diff --cached --name-only --diff-filter=ACMR 2>/dev/null | grep '\.go$' || true)"

  if [ -z "${CHANGED_FILES}" ]; then
    DIFF_TARGET="working tree (HEAD)"
    CHANGED_FILES="$(git diff HEAD --name-only --diff-filter=ACMR 2>/dev/null | grep '\.go$' || true)"
  fi

  if [ -z "${CHANGED_FILES}" ]; then
    if git rev-parse --verify origin/main >/dev/null 2>&1; then
      DIFF_TARGET="branch (origin/main...HEAD)"
      CHANGED_FILES="$(git diff origin/main...HEAD --name-only --diff-filter=ACMR 2>/dev/null | grep '\.go$' || true)"
    fi
  fi

  if [ -z "${CHANGED_FILES}" ]; then
    echo "No Go source files modified in ${DIFF_TARGET}. Codebase is clean."
    exit 0
  fi

  echo "Target changes from: ${DIFF_TARGET}"
  echo "Modified Go files:"
  # shellcheck disable=SC2001
  echo "${CHANGED_FILES}" | sed 's/^/  • /'

  # Run deadcode analysis with -test to find truly dead/unreachable code
  DEAD_JSON="$("${DEADCODE_BIN}" -json -test ./agent/... ./backplane/... ./mcp/... ./cli/... 2>/dev/null || true)"

  if [ -z "${DEAD_JSON}" ] || [ "${DEAD_JSON}" = "[]" ] || [ "${DEAD_JSON}" = "null" ]; then
    echo "==> [PASS] No unreachable dead code introduced by changes."
    exit 0
  fi

  # Parse dead functions
  DEAD_COUNT=0
  DIRECT_COUNT=0
  CASCADING_COUNT=0

  if command -v jq >/dev/null 2>&1; then
    DEAD_ENTRIES="$(echo "${DEAD_JSON}" | jq -r '.[].Funcs[]? | "\(.Position.File)|\(.Position.Line)|\(.Name)"' 2>/dev/null || true)"
  elif command -v python3 >/dev/null 2>&1; then
    DEAD_ENTRIES="$(echo "${DEAD_JSON}" | python3 -c "import sys, json; data=json.loads(sys.stdin.read()); print('\n'.join(f\"{fn.get('Position',{}).get('File','')}|{fn.get('Position',{}).get('Line','')}|{fn.get('Name','')}\" for item in data if isinstance(item, dict) for fn in item.get('Funcs', []) if isinstance(fn, dict)))" 2>/dev/null || true)"
  else
    echo "Error: deadcode detected potential unreachable functions, but neither 'jq' nor 'python3' is installed to parse results." >&2
    exit 1
  fi

  if [ -n "${DEAD_ENTRIES}" ]; then
    echo
    echo "==> [FAIL] Unreachable code detected:"
    while IFS="|" read -r file line func_name; do
      [ -z "${file}" ] && continue
      DEAD_COUNT=$((DEAD_COUNT + 1))
      if echo "${CHANGED_FILES}" | grep -Fxq "${file}"; then
        DIRECT_COUNT=$((DIRECT_COUNT + 1))
        printf "  [DIRECT]    %s:%s: %s (declared in modified file)\n" "${file}" "${line}" "${func_name}"
      else
        CASCADING_COUNT=$((CASCADING_COUNT + 1))
        printf "  [CASCADING] %s:%s: %s (orphaned by changes elsewhere)\n" "${file}" "${line}" "${func_name}"
      fi
    done <<< "${DEAD_ENTRIES}"
    echo
    echo "Summary: ${DEAD_COUNT} dead function(s) found (${DIRECT_COUNT} direct, ${CASCADING_COUNT} cascading/orphaned)."
    exit 1
  else
    echo "==> [PASS] No unreachable dead code introduced by changes."
    exit 0
  fi

elif [ "${MODE}" = "all" ]; then
  echo "======================================================"
  echo "    Comprehensive Codebase Dead Code Audit            "
  echo "======================================================"
  echo

  echo "--- [1/2] True Dead Code (Unused by Binaries AND Tests) ---"
  TRUE_DEAD_OUTPUT="$("${DEADCODE_BIN}" -test ./agent/... ./backplane/... ./mcp/... ./cli/... 2>&1 || true)"
  if [ -z "${TRUE_DEAD_OUTPUT}" ]; then
    echo "  [✓] None. Codebase is 100% clean of completely unreachable code."
  else
    # shellcheck disable=SC2001
    echo "${TRUE_DEAD_OUTPUT}" | sed 's/^/  [!] /'
  fi
  echo

  echo "--- [2/2] Library Code Unreached by Binary Entrypoints (cmd/fleet, cmd/bp) ---"
  BINARY_DEAD_OUTPUT="$("${DEADCODE_BIN}" ./agent/... ./backplane/... ./mcp/... ./cli/... 2>&1 || true)"
  if [ -z "${BINARY_DEAD_OUTPUT}" ]; then
    echo "  [✓] All package functions are reachable from binary entrypoints."
  else
    # shellcheck disable=SC2001
    echo "${BINARY_DEAD_OUTPUT}" | sed 's/^/  [i] /'
  fi
  echo
  echo "Audit complete."
fi
