---
name: fleet-testing-guidelines
description: >-
  Testing guidelines, >= 90% statement coverage gate rules, rootless Podman cleanup patterns, and containerized smoke test execution.
  Use when writing unit or integration tests, or troubleshooting test failures.
---

# Testing Guidelines & Coverage Gates

## Strict Coverage Threshold (>= 90.0%)
- Monorepo enforces **>= 90.0% atomic statement coverage** across all packages in `pkg/...` and `cmd/...`.
- Gate command: `make test-coverage` (outputs HTML to `coverage/coverage.html`).
- When introducing new packages or functions, corresponding unit tests must be added to maintain or improve coverage.

## Rootless Podman Test Cleanups & Deterministic Reset
- Rootless Podman creates files in test containers using subuid ranges.
- Standard `t.TempDir()` cleanups will fail with permission errors unless cleaned via `podman unshare`.
- Always register a cleanup hook in tests using temp dirs with Podman:
  ```go
  tmpDir := t.TempDir()
  t.Cleanup(func() {
      _ = exec.Command("podman", "unshare", "rm", "-rf", tmpDir).Run()
  })
  ```
- **Deterministic Tooling over Bespoke Commands**: Never run ad-hoc, error-prone shell commands (e.g. `rm -rf /run/...` or manual `kill`) to clean up test leftovers. Always use deterministic, documented Makefile targets:
  - `make clean-test-env`: Safely terminates orphaned test containers (`test-valkey-*`, `test-podman-*`, `infra-test-*`), test conmon/slirp4netns processes, and clears stale rootless netns descriptors in `/run/user/<uid>/netns`.
  - `make clean-all`: Runs both `clean` (artifacts) and `clean-test-env` (runtime state).

## Containerized Smoke Testing
- `make test-install`: Runs isolated installation and bootstrap smoke testing inside an ephemeral rootless container (`test/container-install/test-install.sh`) without mutating host environment.

Further reading:
- [ADR 00025 — Quality Gates and Coverage Enforcement](../adr/00025-QualityGatesAndCoverageEnforcement.md)
