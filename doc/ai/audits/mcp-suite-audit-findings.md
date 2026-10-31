---
name: fleet-mcp-suite-audit-findings
description: >-
  Adversarial security audit, input sanitization assessment, JSON-RPC 2.0 protocol compliance review,
  and hardening remediation report for the Fleet MCP server suite.
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# Fleet MCP Suite — Adversarial Audit & Security Remediation Report

This report documents the targeted security audit, adversarial threat analysis, input sanitization review, and JSON-RPC 2.0 spec compliance verification conducted across the Model Context Protocol (MCP) server suite in the `mcp/` module.

---

## 1. Executive Summary

| Component | Scope / Area | Initial Risk | Post-Audit Status | Coverage |
|---|---|---|---|---|
| `mcp/pkg/mcp` | Shared JSON-RPC 2.0 engine | High | Hardened & Compliant | 95.2% |
| `mcp/cmd/beads-mcp` | Beads task tracking tool | Critical | Hardened against Flag/Path Injection | 92.3% |
| `mcp/cmd/forgejo-mcp` | Forgejo / Gitea REST API | High | Hardened against API Traversal / SSRF | 94.1% |
| `mcp/cmd/sonar-mcp` | SonarQube Quality Gate | Medium | Hardened against Enum/URL Manipulation | 95.8% |
| `mcp/cmd/bp-mcp` | Backplane messaging | Medium | Hardened against Control Chars & Bounds | 91.5% |
| `mcp/cmd/test-runner-mcp` | Test execution / coverage | Medium | Hardened against Key/URL Injection | 95.1% |

**Overall Result:** All identified vulnerability vectors and compliance gaps have been remediated. The `mcp/` test suite achieves **93.8% aggregate statement coverage** with zero race condition detections under `go test -race`.

---

## 2. Audit Focus Areas & Findings

### Focus Area A: Input Sanitization & Injection Defense

1. **CLI Flag & Control Character Injection (`beads-mcp`)**
   - **Vulnerability:** Tool arguments such as `id` or `parent` in `bd_show`, `bd_claim`, `bd_close`, and `bd_create` were passed directly to `exec.CommandContext`. A malicious caller supplying `id = "--exec=id"` could inject unexpected command flags into the `bd` CLI.
   - **Remediation:** Added `sanitizePositionalArg` validation rejecting any argument starting with `-` or containing forbidden control characters (`\x00`, `\r`, `\n`). Validated filter enums (`status`, `priority`, `type`) against strict allowlists. Cleaned task directory paths using `filepath.Clean`.

2. **API Endpoint Path Traversal (`forgejo-mcp`)**
   - **Vulnerability:** In `forge_read_file`, relative file paths were cleaned only via `strings.TrimPrefix(filePath, "/")` before constructing REST URL endpoints (`/repos/{owner}/{repo}/contents/{filePath}`). Traversal sequences (`../../etc/passwd`) could escape the target path.
   - **Remediation:** Implemented `sanitizeFilePath` using `path.Clean` and strict traversal checks (`..`, `../`, `/../`). Implemented `validateName` for `owner` and `repo` parameters rejecting path separators and traversal segments. Validated `state` and `event` enums.

3. **Metric Key & Project Key Injection (`sonar-mcp` & `test-runner-mcp`)**
   - **Vulnerability:** Unfiltered project keys or metric key strings containing spaces, control characters, or path segments could produce malformed API queries.
   - **Remediation:** Added `validateProjectKey` / `validateKey` checking for whitespace and forbidden characters. Enforced upper-case enum checks for SonarQube `severity` and `issue_type` parameters.

4. **URL Scheme & SSRF Protection (`forgejo-mcp`, `sonar-mcp`, `test-runner-mcp`)**
   - **Vulnerability:** Environment variable host URLs (`FORGEJO_URL`, `SONAR_HOST_URL`, `TEST_RUNNER_HOST_URL`) were passed to client constructors without validating scheme protocols.
   - **Remediation:** Enforced `url.Parse` verification requiring explicit `http` or `https` schemes before initializing HTTP client connections.

---

### Focus Area B: JSON-RPC 2.0 Protocol Compliance

1. **Explicit Response `"id"` Member Formatting**
   - **Spec Deficiency:** `JSONRPCMessage` used `ID any json:"id,omitempty"`. When `id` was `nil` (e.g. for Parse Error or Invalid Request before request ID resolution), `json.Marshal` omitted the `id` field entirely, generating invalid response objects missing the required `id` member.
   - **Remediation:** Introduced a dedicated internal `jsonRPCResponse` struct with `ID any json:"id"` (without `omitempty`), guaranteeing JSON-RPC responses explicitly format `"id": null` or `"id": <value>` per Section 5 of the JSON-RPC 2.0 specification.

2. **Protocol Version Validation (`jsonrpc == "2.0"`)**
   - **Spec Deficiency:** Inbound requests with missing or non-"2.0" `jsonrpc` versions were processed without version validation.
   - **Remediation:** Enforced `req.JSONRPC == "2.0"` validation in `Server.Serve`, returning `CodeInvalidRequest` (`-32600`) on version mismatches.

3. **Notification Stream Handling**
   - **Spec Compliance:** Ensured notification methods (`notifications/initialized` or methods prefixed with `notifications/`) suppress response transmission per JSON-RPC 2.0 rules.

---

### Focus Area C: Resource & Buffer Bounds

1. **Unbounded STDIO Line Reader Guard**
   - **Risk:** `Server.Serve` previously invoked `bufio.Reader.ReadBytes('\n')` without length bounds, creating OOM vulnerability if an incoming stream sent unbounded bytes without newline delimiters.
   - **Remediation:** Replaced unbuffered line reading with `readLineBounded`, enforcing a strict **10MB maximum line length** per request message. Exceeding lines are safely drained and return a `CodeInvalidRequest` error.

2. **Process Execution Output Bounding**
   - **Risk:** Capturing `stdout` and `stderr` in `beads-mcp` via unbounded `bytes.Buffer` posed memory exhaustion risks if `bd` produced massive terminal output.
   - **Remediation:** Implemented `limitedBuffer` capping process output capture to **10MB max**.

3. **Pagination & Read Bounds**
   - **Remediation:** Capped `forgejo-mcp` pagination `limit` to maximum 100 items. Bounded `bp-mcp` `block_seconds` (0–300s) and inbox `count` (0–500 messages).

---

## 3. Verification & Test Coverage Summary

Negative test cases and boundary validations were added across all packages in `mcp/`:

```
ok  github.com/boggycreek/sandbox/mcp/pkg/mcp         coverage: 95.2% of statements
ok  github.com/boggycreek/sandbox/mcp/cmd/beads-mcp       coverage: 92.3% of statements
ok  github.com/boggycreek/sandbox/mcp/cmd/bp-mcp          coverage: 91.5% of statements
ok  github.com/boggycreek/sandbox/mcp/cmd/forgejo-mcp     coverage: 94.1% of statements
ok  github.com/boggycreek/sandbox/mcp/cmd/sonar-mcp       coverage: 95.8% of statements
ok  github.com/boggycreek/sandbox/mcp/cmd/test-runner-mcp coverage: 95.1% of statements
```

- **Race Detector Status:** Clean (`go test -race ./...` passed with 0 data races).
- **Monorepo Coverage Gate:** All packages exceed the mandatory >= 90.0% statement coverage target.
