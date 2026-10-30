---
adr: "00054"
title: "Server-Side Valkey Stream Guard and Wire Inspection"
topic: "Security, Backplane & Messaging"
theme: "THEME-SECURITY"
status: "accepted"
version: "v0.1.0-alpha"
as_built: true
tags:
  - valkey
  - backplane
  - security
  - proxy
  - wire-inspection
  - xadd
  - maxlen
  - streaming
executive_summary: "Evaluates and defines a server-side Valkey stream guard architecture using a transparent RESP proxy sidecar. Enforces wire-level inspection and strict boundary limits (MAXLEN capping on XADD, payload byte size thresholds, and canonical citation verification) to prevent DoS attacks and bypass of client-side guards by untrusted agent processes."
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# 00054. Server-Side Valkey Stream Guard and Wire Inspection

## Context

In Fleet's multi-agent architecture ([ADR 00042](00042-UniversalCryptographicBackplaneFabricAndStatelessIdentityProfiles.md)), agent sandboxes and host-native cognitive processes communicate over a shared Valkey backplane message bus. Access isolation was originally governed by native Valkey ACL rules (e.g., `~<agent>:*` for private key-space and `~*:inbox` with `+xadd` for peer-to-peer messaging).

However, native Valkey ACLs operate strictly at the key and command level. They lack depth-aware argument and subargument inspection capabilities:
1. **Unbounded Stream Growth (Memory Exhaustion DoS)**: Valkey ACLs cannot force `XADD` commands to include stream length bounds (`MAXLEN ~ 1000`). A rogue or compromised agent process can issue unconstrained `XADD` calls, leading to memory exhaustion on `fleet-valkey`.
2. **Payload Boundary Violations**: ACLs cannot enforce byte size caps on individual stream entries or message payloads.
3. **Cross-Inbox History Alteration**: ACLs allowing `+xadd ~*:inbox` cannot prevent an agent from passing `MAXLEN 0` or `MINID` arguments on cross-inbox writes, which would truncate or erase another agent's message inbox history.
4. **Client Guard Bypass**: While client-side safety checks are implemented in `backplane/pkg/libbp/guard.go` (`AssertAllowed`), an untrusted agent container process with direct TCP or socket access to Valkey can issue raw RESP (REdis Serialization Protocol) commands bypassing client-side validation.

This issue investigates and defines a server-side stream guard mechanism to enforce wire-level inspection, payload safety bounds, and argument constraints on all backplane traffic (Beads issue `sndbx-ey6`).

---

## Comparative Architecture Evaluation

We evaluated two candidate architectural approaches for enforcing server-side wire-level guardrails:

### Option A: Transparent Stream Guard Proxy Sidecar (Pure Go)
A lightweight, zero-dependency Go proxy service positioned between agent containers and `fleet-valkey`. The proxy parses incoming RESP (RESP2/RESP3) protocol frames, validates command arguments and subarguments against safety guard rules, injects missing safety parameters (e.g., `MAXLEN ~ 1000`), and forwards compliant frames to `fleet-valkey`.

### Option B: Custom Valkey C Module
A native C module loaded directly into Valkey (`module load valkey-stream-guard.so`) that hooks into Valkey's command filter API or implements custom stream commands (e.g., `XADD.GUARDED`).

### Evaluation Matrix

| Metric / Dimension | Option A: Transparent Go Proxy Sidecar | Option B: Custom Valkey C Module |
| :--- | :--- | :--- |
| **Latency Overhead** | ~0.1–0.3 ms (TCP/Unix socket loopback proxying) | <0.02 ms (In-process C function call) |
| **CPU / Memory Overhead** | Negligible (~8–15 MB RAM, minimal Go runtime GC pressure) | Minimal (~1–2 MB RAM shared inside Valkey process) |
| **Build & Toolchain Complexity** | Low (Compiles cleanly within Go monorepo; zero CGO or C toolchain deps) | High (Requires C compiler, Valkey module headers, multi-arch cross-compilation) |
| **Monorepo Architecture Alignment** | High (Conforms strictly to zero external dependency Go standard library rule) | Low (Breaks Go monorepo uniformity; requires maintaining C codebase and dynamic libraries) |
| **Process Isolation & Resilience** | High (Proxy crash or panic does not crash `fleet-valkey` process; restartable sidecar) | Low (Memory flaw or crash in C module immediately crashes the entire `fleet-valkey` instance) |
| **Wire Inspection Depth** | Complete (Full RESP frame parsing, payload size checking, argument mutation/injection) | High (Command filter hooks inspect argv array; custom commands replace std `XADD`) |
| **Container Image Footprint** | Static binary (~5 MB) appended to `fleet-valkey` or run as sidecar | Dynamic library (`.so`) mounted into standard Valkey container |
| **Maintainability & Debugging** | Excellent (Native Go testing, `net.Pipe` unit tests, standard Go profilers) | Complex (Requires Valgrind, GDB, C memory management audits) |

### Decision Rationale
Option A (Go Proxy Sidecar) is selected. Although Option B provides sub-millisecond latency advantages, Fleet's backplane latency budget (<10 ms) easily accommodates a 0.2 ms proxy overhead. Furthermore, Option A strictly preserves Fleet's core architecture requirement: zero-dependency Go static compilation, process fault isolation, and simplified cross-platform maintainability without CGO or external C toolchain dependencies.

---

## Guard Rules & Wire Boundary Constraints

The server-side proxy enforces four mandatory wire boundary guard rules on all incoming RESP frames:

```
  ┌──────────────────────┐         RESP Frames         ┌────────────────────────┐
  │  Agent Container /   │ ──────────────────────────> │ Transparent Stream     │
  │  Untrusted Process   │                             │ Guard Proxy Sidecar    │
  └──────────────────────┘                             └───────────┬────────────┘
                                                                   │
                                                   1. Enforce MAXLEN ~ 1000 on XADD
                                                   2. Cap Payload Byte Size (<= 1MB)
                                                   3. Verify Citation (<agent>#<seq>)
                                                   4. Block MAXLEN/MINID on Peer Inboxes
                                                                   │
                                                                   ▼ Validated Frame
                                                       ┌────────────────────────┐
                                                       │  fleet-valkey Server   │
                                                       └────────────────────────┘
```

### 1. Mandatory `MAXLEN` Stream Bound Enforcement
- Every `XADD` command targeting any stream key (`*`) must be constrained by stream trimming parameters.
- If an incoming `XADD` frame omits `MAXLEN` or `MINID`, the proxy automatically rewrites the RESP command array before forwarding, injecting `MAXLEN ~ 1000` (or the system-configured maximum stream depth).
- If an `XADD` frame explicitly requests a `MAXLEN` exceeding the permitted upper bound (e.g. `MAXLEN 1000000`), the proxy overrides the parameter down to the maximum bound (`MAXLEN 1000`), preventing memory exhaustion attacks.

### 2. Message Payload Byte Size Capping
- The proxy measures total RESP frame size and individual field payload byte lengths during stream parsing.
- Incoming field payloads exceeding the threshold (default: **1 MB** per `XADD` invocation) are immediately rejected at the proxy boundary before forwarding to Valkey, returning a standard RESP error: `-ERR message payload size exceeds maximum limit (1048576 bytes)`.

### 3. Wire-Level Canonical Citation & Identity Verification
- For backplane message entries (containing `citation`, `sender`, or `payload` fields):
  - The proxy validates that the `citation` field matches the canonical sequence format: `<agent>#<seq>` (where `<agent>` is alphanumeric/hyphenated and `<seq>` is a positive monotonic integer).
  - The proxy verifies that the `sender` claim matches the authenticated Valkey ACL user identity associated with the active connection session, preventing identity spoofing on shared streams.

### 4. Defense-in-Depth Cross-Inbox Protection
- When writing to peer inbox streams (`~*:inbox`), callers are strictly limited to appending new messages.
- Any attempt to pass `MAXLEN` or `MINID` options on an `XADD` call targeting a peer inbox key (where target stream suffix is `:inbox` and owner != authenticated caller) is rejected by the proxy with `-ERR forbidden: MAXLEN/MINID options disallowed on peer inbox`. This guarantees peer inbox history cannot be truncated or destroyed by external agents.

---

## Decision (What)

1. Formally adopt the **Transparent Go RESP Stream Guard Proxy Sidecar** architecture for server-side wire-level inspection.
2. Embed the proxy functionality into Fleet's backplane runtime infrastructure, intercepting agent TCP/Unix socket connections prior to `fleet-valkey`.
3. Standardize wire boundary constraints across all backplane traffic: mandatory `MAXLEN ~ 1000` injection, 1 MB payload byte capping, canonical citation verification (`<agent>#<seq>`), and peer inbox truncation defense.

---

## Status

Accepted.

---

## Consequences

### Positive
- **Complete DoS Protection**: Prevents rogue or buggy agent processes from causing unbounded memory consumption on `fleet-valkey`.
- **Integrity Guarantee**: Untrusted containers cannot bypass safety checks by issuing raw RESP socket traffic.
- **Zero CGO Overhead**: Preserves Fleet's pure Go monorepo architecture and static binary release strategy.
- **Fault Tolerance**: Isolates guard logic in a sidecar process; proxy panics or restarts do not corrupt or crash the underlying Valkey engine.

### Negative / Trade-offs
- **Proxy Latency Overhead**: Introduces a minor framing latency overhead (~0.1–0.3 ms per command).
- **Protocol Parser Maintenance**: The proxy must maintain a lightweight, conformant RESP2/RESP3 stream parser in `pkg/libbp` to inspect command argument arrays efficiently.
