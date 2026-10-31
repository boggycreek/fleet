---
name: fleet-backplane-audit-findings
description: Targeted protocol, cryptography, and concurrency audit report and hardening fixes for the backplane/ module.
---
<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# Backplane Module — Targeted Audit Findings & Security Review

This document provides the report for the comprehensive targeted review and adversarial audit of the `backplane/` module (`pkg/libbp`, `pkg/libbpd`, `cmd/bp`, and `cmd/bpd`).

---

## 1. Audit Summary & Focus Areas

| Area | Focus | Status | Key Resolution |
| :--- | :--- | :--- | :--- |
| **Concurrency & Deadlines** | Data race in `execInternal` during context cancellation | **Fixed** | Mutex protection for `c.conn`, `c.reader`, and `c.writer` when registering `context.AfterFunc` deadline callbacks. |
| **RESP Protocol Parser** | Unbounded memory buffering & missing bulk string trailing CRLF validation | **Fixed** | Replaced `ReadBytes` with `ReadSlice` in `readLine()` to strictly bound line length; added trailing CRLF (`\r\n`) validation on `TypeBulkString`. |
| **Cryptographic Validation** | Standard library panic on malformed public keys & unverified timestamps | **Fixed** | Guarded `VerifyPayload` against `len(pub) != ed25519.PublicKeySize`; checked empty senders and timestamp freshness thresholds in `VerifyMessage`. |
| **Isolation & Guard Rules** | Peer inbox isolation & destructive command protection | **Fixed** | Extended `AssertAllowed` in `guard.go` to forbid `XTRIM`, `DEL`, and `UNLINK` on peer `:inbox` and `:out` keys. |
| **Ensemble Helpers** | Team-scoped ensemble channel helper | **Added** | Added `TeamChannel(teamID string) string` helper (`team:<teamID>:out`) in `protocol.go`. |

---

## 2. Detailed Findings & Technical Impact

### 2.1 Concurrency & Data Race in `libbp.Client.execInternal`
* **Finding**: `c.conn` was accessed without acquiring `c.mu` in `execInternal` when registering `context.AfterFunc`. Concurrently, background calls to `c.markBroken()` or `c.Close()` modified `c.conn` under `c.mu.Lock()`, triggering data races detected by Go's `-race` instrumentation.
* **Impact**: Potential nil pointer dereference or race condition during in-flight context cancellation or network failure recovery.
* **Fix**: In `execInternal`, local references `conn`, `reader`, and `writer` are captured under `c.mu.Lock()`. The `context.AfterFunc` callback operates exclusively on the local `conn` variable, preventing field races on `c.conn`.

### 2.2 RESP Parser Trailing CRLF & Bounded Buffering
* **Finding**:
  1. `resp.Reader` read bulk string contents with `io.ReadFull(r.r, buf)` but did not verify that `buf[length] == '\r'` and `buf[length+1] == '\n'`. Corrupted or truncated streams were silently accepted.
  2. `readLine()` used `bufio.Reader.ReadBytes('\n')`, which buffered without bound if `\n` was omitted.
* **Impact**: Potential stream desynchronization or OOM crash on malformed/oversized TCP input.
* **Fix**: Added trailing `\r\n` validation returning `ErrProtocol` if missing. Switched `readLine()` to `ReadSlice('\n')` with `MaxLineLength` checks to bound allocation.

### 2.3 Cryptographic Public Key Validation & Panic Prevention
* **Finding**: `VerifyPayload` passed `pub` directly to `ed25519.Verify`. If `len(pub) != ed25519.PublicKeySize` (32 bytes), `ed25519.Verify` in the Go standard library panics.
* **Impact**: Denial of Service (DoS) if an unauthenticated peer registered a malformed public key or if an invalid identity was retrieved.
* **Fix**: Added `if len(pub) != ed25519.PublicKeySize { return false }` in `VerifyPayload`. In `VerifyMessage`, empty sender strings return `false` immediately.

### 2.4 Destructive Action Refusal on Peer Keys
* **Finding**: `AssertAllowed` prohibited `XADD MAXLEN/MINID` on peer inboxes, but did not explicitly block `XTRIM`, `DEL`, or `UNLINK` commands against peer `:inbox` or `:out` streams.
* **Impact**: An agent could attempt to trim or delete a peer agent's stream history.
* **Fix**: Extended `AssertAllowed` in `guard.go` to return `ErrDestructiveAction` when a non-operator agent attempts `XTRIM`, `DEL`, or `UNLINK` on peer `:inbox` or `:out` keys.

---

## 3. Verification & Test Coverage

* Monorepo statement test coverage maintained **>= 90.0%**.
* All tests pass cleanly under `go test ./... -v -race`.
