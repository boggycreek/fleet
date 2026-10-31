---
name: fleet-shell-and-ci-audit-findings
description: Targeted security, error handling, and portability audit findings for Fleet's shell scripts, workstation installer, developer setup, and GitHub Actions CI toolchains
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# Targeted Shell Script, Installer, and CI Toolchain Security Review

## Executive Summary

A comprehensive security, error handling, and portability audit was conducted across Fleet's host installation script (`install.sh`), multi-OS developer environment setup (`setup.sh`), maintenance and quality gate utilities (`scripts/`), and GitHub Actions release/deployment workflows (`.github/workflows/`).

The audit evaluated five critical operational surface areas:
1. **Supply Chain & Download Integrity**: Cryptographic hash verification on remote downloads, fallback mechanisms, and provenance handling.
2. **POSIX & Shell Correctness**: Elimination of word-splitting hazards, unbound variable traps, subshell overheads, and full ShellCheck static analysis compliance.
3. **Privilege & Path Separation**: Unprivileged/rootless execution boundaries, temporary file isolation (`mktemp`), restrictive umasking (`0700`/`0600`), and secret exposure window elimination.
4. **Platform Portability**: Compatibility across macOS (Darwin/BSD toolchains) and Linux distributions (Debian/Ubuntu, Fedora/RHEL).
5. **CI/CD Workflow Hardening**: GitHub Actions action versioning, checksum accumulation determinism, and minimal permission scoping.

---

## Audited Scope & Findings Summary

| Component | Category | Severity / Finding | Hardening Implemented |
| :--- | :--- | :--- | :--- |
| `install.sh` | Supply Chain | High: Unverified prebuilt native CLI binaries downloaded from GitHub Releases. | SHA256 checksum validation against release `checksums.txt` prior to installation; secure fallback to source build if verification fails. |
| `install.sh` | Shell Safety | Medium: Unbound variable failure on `${BASH_SOURCE[0]}` when script is executed via `curl \| bash`. | Default parameter expansion `${BASH_SOURCE[0]:-}` to prevent crashes under `set -u`. |
| `install.sh` / `setup.sh` | Privilege / Files | Medium: Secret `.env` and SSH config temporary files created before restrictive permissions were applied. | Enforced `(umask 077 ...)` during file creation so secret files are created directly with `0600`/`0700` perms. |
| `setup.sh` | Shell Safety | Low: Unbound variable `${SUDO}` during early `--doctor` execution. | Safe variable parameter expansion `${SUDO:-}` in `run_privileged()`. |
| `setup.sh` | Quality / UX | Low: Non-interactive flag (`-y`) did not propagate to package manager session environments. | Exported `DEBIAN_FRONTEND=noninteractive` and `NONINTERACTIVE=1` when `-y`/`--non-interactive` is supplied. |
| `clean-test-env.sh` | Portability | Medium: Non-portable `xargs -r` flag breaks execution under BSD/macOS `xargs`. | Removed redundant `-r` flag (inputs are already guarded with `if [ -n ... ]`), ensuring macOS compatibility. |
| `deadcode-check.sh` | Quality Gate | High: Missing `jq` dependency caused deadcode findings to silently report `[PASS]` exit 0. | Added Python 3 JSON parsing fallback and explicit fail-fast exit if neither parser is present. |
| `build-sonarqube-image.sh` | File Safety | Low: Default `/tmp` mktemp path without restrictive umasking. | Switched to `(umask 077 && mktemp -d "${TMPDIR:-/tmp}/...")` and safe trap cleanup handling. |
| `.github/workflows/*` | CI Security | Medium: Unsupported action version tags (`checkout@v7`, `upload-artifact@v7`, etc.) and checksum file self-hash race condition. | Updated to standard action releases (`checkout@v4`, `setup-go@v5`, `upload-artifact@v4`, `download-artifact@v4`) and added `rm -f checksums.txt` prior to calculation. |

---

## Detailed Audit Findings & Hardening Details

### 1. Supply Chain & Download Integrity

#### Unverified Prebuilt Binary Downloads in `install.sh`
* **Finding**: `install.sh` previously fetched release binaries (`fleet`, `bp`, `bpd`, `sonar-mcp`) directly over HTTPS to temporary `.tmp` files and moved them into `$PATH` (`~/.local/bin`) without validating their SHA256 hashes against a cryptographic manifest.
* **Impact**: In the event of an upstream release asset replacement or network proxy manipulation, unverified native binaries could be executed on the developer host.
* **Remediation**: `install.sh` now fetches `checksums.txt` from the release endpoint first. Each downloaded binary is hashed using `sha256sum` or `shasum -a 256` and verified against the manifest before being promoted to `~/.local/bin/`. If verification fails or the manifest is unavailable, the installer logs a warning, cleans up temporary files, and falls back to compiling from local source or installing the bootstrap dispatcher.

#### XDG Go SDK & Beads (bd) Installation in `setup.sh`
* **Finding**: `setup.sh` already implemented SHA256 manifest verification for Go SDK tarballs (`go.dev/dl`) and Beads releases (`gastownhall/beads`).
* **Hardening**: Reinforced `mktemp -d` directory creation to execute under `umask 077` and respect `${TMPDIR:-/tmp}`, eliminating potential multi-user shared directory collision or inspectability risks during extraction.

---

### 2. POSIX & Shell Correctness

#### Unbound Variable Failure on Pipe-to-Bash Execution
* **Finding**: The installer supports execution via `curl -fsSL ... | bash`. Under `set -u`, referencing `BASH_SOURCE[0]` when executing from standard input caused immediate termination due to an unbound variable error.
* **Remediation**: Refactored to `${BASH_SOURCE[0]:-}` across all entrypoints.

#### Silent Pass Defect in `deadcode-check.sh`
* **Finding**: When `deadcode -json` produced unreachable function results, `deadcode-check.sh` attempted to parse the JSON using `jq`. If `jq` was not installed on the system, `DEAD_ENTRIES` collapsed to an empty string, causing the script to enter its `else` branch and report `[PASS] No unreachable dead code introduced by changes.` with exit code 0.
* **Impact**: Silent bypass of dead code reachability quality gates in environments without `jq`.
* **Remediation**: Implemented a two-tier JSON parser strategy: `jq` (primary) and `python3` (fallback standard library). If dead code JSON is present but neither parser is installed, the script fails fast with exit code 1, demanding a parser to evaluate the gate.

#### ShellCheck Compliance Audit
* **Result**: Executed ShellCheck (v0.10.0) across `install.sh`, `setup.sh`, `scripts/clean-test-env.sh`, `scripts/deadcode-check.sh`, and `scripts/build-sonarqube-image.sh`.
* **Fixes Applied**:
  * Addressed SC2034 by propagating `NON_INTERACTIVE` into `DEBIAN_FRONTEND` and `NONINTERACTIVE` package manager environment flags.
  * Corrected SC2235 subshell overhead `([ -f ... ] || [ -f ... ])` to grouped test list `{ [ -f ... ] || [ -f ... ]; }`.
  * Annotated necessary `sed` stream formatting directives (`SC2001`) and Homebrew literal command string output (`SC2016`).

---

### 3. Privilege Separation & Path Safety

#### Exposure Window During Secret File Creation
* **Finding**: Files containing generated passwords (`.env`) or SSH configurations (`~/.ssh/config`) were created using standard file redirection (`cat > file`), followed by a subsequent `chmod 600` call.
* **Impact**: Between file creation and `chmod 600` execution, default system umask (e.g. `0022`) left secret data world-readable / group-readable.
* **Remediation**: Wrapped all secret file generation blocks in subshells enforcing restrictive umask:
  ```bash
  (
    umask 077
    cat > "${ENV_FILE}" <<EOF
    ...
  EOF
  )
  chmod 600 "${ENV_FILE}"
  ```

#### BSD / macOS Compatibility in Ephemeral Cleanup
* **Finding**: `scripts/clean-test-env.sh` utilized `xargs -r -n 1` to clean up containers, networks, and volumes. BSD `xargs` on macOS does not recognize `-r` (`--no-run-if-empty`) and throws an error.
* **Remediation**: Removed `-r` flag from `xargs` calls. Since every invocation is already guarded by an explicit non-empty check (`if [ -n "${TEST_CONTAINERS}" ]`), removing `-r` restores full portability across macOS and Linux without risk of executing empty commands.

---

## CI/CD Workflow Security Audit

### Action Versioning & Asset Checksums in `release.yml`
* **Findings**:
  * `.github/workflows/release.yml` and `pages.yml` contained invalid action versions (`checkout@v7`, `upload-artifact@v7`, `download-artifact@v8`).
  * `publish-release` job calculated `sha256sum * > checksums.txt` inside `all-assets/`. On workflow re-runs or pre-existing asset directories, `checksums.txt` could be included in its own checksum calculation.
* **Remediation**:
  * Pin to standard supported action versions: `actions/checkout@v4`, `actions/setup-go@v5`, `actions/upload-artifact@v4`, `actions/download-artifact@v4`.
  * Explicitly remove any existing `checksums.txt` prior to running `sha256sum * > checksums.txt` to ensure deterministic manifest generation.

---

## Verification & Testing

1. **ShellCheck Verification**: All modified shell scripts pass ShellCheck with 0 errors and 0 warnings:
   ```bash
   shellcheck install.sh setup.sh scripts/clean-test-env.sh scripts/deadcode-check.sh scripts/build-sonarqube-image.sh
   ```
2. **Setup Doctor**: Executed `./setup.sh --doctor` to confirm system health and XDG compliance.
