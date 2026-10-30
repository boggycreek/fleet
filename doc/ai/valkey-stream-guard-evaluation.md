---
name: fleet-valkey-stream-guard-evaluation
description: >-
  Architectural evaluation of server-side Valkey stream guard mechanisms, comparing transparent Go proxy sidecar and native C module approaches for wire-level RESP inspection and DoS protection.
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# Server-Side Valkey Stream Guard & Wire Inspection Architectural Evaluation

This document provides a progressive disclosure architectural analysis of server-side wire-level inspection mechanisms for Fleet's Valkey backplane (`fleet-valkey`). It addresses Beads spike **sndbx-ey6**.

---

## Executive Summary & Threat Model

Fleet isolates backplane interactions across containerized agents and host processes using Valkey Streams ([ADR 00042](00042-UniversalCryptographicBackplaneFabricAndStatelessIdentityProfiles.md)). While native Valkey ACLs govern key patterns (`~<agent>:*`, `~*:inbox`) and allowed command sets (`+xadd`), native ACLs operate strictly at command headers and key names.

### Key Threat Vectors
1. **Unbounded Memory Exhaustion DoS**: Native ACLs cannot enforce stream trimming arguments (`MAXLEN ~ <limit>`). An untrusted or rogue agent container process can emit high-frequency `XADD` commands without length limits, exhausting host memory and crashing `fleet-valkey`.
2. **Client-Side Guard Bypass**: Client-side validation in `backplane/pkg/libbp/guard.go` (`AssertAllowed`) can be bypassed if an attacker inside an unprivileged agent sandbox executes a custom script or binary issuing raw RESP socket commands directly to Valkey.
3. **Payload Bloat & Buffer Flooding**: Without server-side wire length limits, large binary payloads or giant strings can be pushed into streams, degrading pub/sub latency across the backplane fabric.
4. **Peer Inbox History Truncation**: Agents granted `+xadd ~*:inbox` to write to peer inboxes could maliciously pass `MAXLEN 0` or `MINID` parameters to truncate or erase the recipient's inbox state.

To eliminate these vulnerabilities, Fleet requires **server-side wire-level inspection** that parses and validates all RESP commands before execution.

---

## Comparative Architectural Analysis

We evaluated two candidate mechanisms for enforcing server-side wire inspection:
1. **Option A**: Transparent Stream Guard Proxy Sidecar (Pure Go)
2. **Option B**: Custom Valkey C Module (`module load valkey-guard.so`)

### Architectural Comparison Matrix

| Evaluation Dimension | Option A: Go Transparent Proxy Sidecar | Option B: Custom Valkey C Module |
| :--- | :--- | :--- |
| **Execution Latency** | **0.1–0.3 ms** per command (Local loopback socket proxying) | **<0.02 ms** per command (Direct in-process function call) |
| **CPU / Memory Footprint** | ~8–15 MB RSS; minimal Go runtime allocator overhead | ~1–2 MB RSS shared within `fleet-valkey` process |
| **Go Monorepo Alignment** | **100% compliant**. Zero external dependencies; uses Go standard library (`net`, `bytes`, `io`). | **Non-compliant**. Requires external C compiler, CGO, and C build toolchain. |
| **Build & CI Pipeline** | Standard `go build`; integrated into native Fleet release build pipeline. | Requires cross-compiling `.so` dynamic libraries for x86_64 and arm64 targets. |
| **Fault Isolation & Safety** | **Isolated Process Domain**. Proxy panic or buffer error does not crash `fleet-valkey`. | **Shared Process Memory**. Memory corruption or segfault in C module crashes Valkey. |
| **Wire Inspection Capability** | Complete frame parsing, payload size capping, parameter injection (`MAXLEN ~ 1000`). | Inspects `argv` array via command filters or implements custom commands. |
| **Deployment Topology** | Runs as sidecar binary in `fleet-valkey` container or host bridge network. | Dynamic module loaded via `valkey.conf` (`loadmodule /path/to/module.so`). |
| **Maintainability & Testing** | Unit-tested via Go `net.Pipe()`; profilers and race detection natively available. | Debugged via GDB/Valgrind; complex C memory management and concurrency audits. |

### Comparative Benchmark & Projection Summary

```
                       ┌──────────────────────────────────────────────┐
                       │           End-to-End Latency Profile         │
                       └──────────────────────────────────────────────┘

Direct Connection (No Guard):
[ Agent ] ────────────────────── TCP/Unix Socket ──────────────────────> [ Valkey Engine ]
          < 0.05 ms total

Option A (Go Proxy Sidecar):
[ Agent ] ──> [ RESP Parser ] ──> [ Guard Rules ] ──> [ Rewritten Frame ] ──> [ Valkey Engine ]
          0.02 ms          0.05 ms            0.03 ms                0.05 ms   = 0.15 ms total

Option B (Valkey C Module):
[ Agent ] ────────────────────── TCP/Unix Socket ──────────────────────> [ C Module Filter ] ──> [ Valkey Engine ]
          < 0.05 ms                                                      0.01 ms            = 0.06 ms total
```

### Recommendation
While Option B offers lower execution latency (~0.06 ms vs ~0.15 ms), **Option A (Transparent Go Proxy Sidecar)** is selected as the canonical architecture for Fleet. A total latency overhead of ~0.15 ms is negligible for asynchronous backplane event processing (<10 ms target), whereas Option A preserves Fleet's strict zero-dependency Go monorepo rule, process isolation, and cross-platform maintainability.

---

## Wire Inspection & Guard Rules Specification

The proxy intercepts incoming RESP frames and enforces four mandatory wire boundary rules:

```
  Incoming RESP Frame
           │
           ▼
 ┌───────────────────┐
 │ Parse RESP Array  │ ──> [ Reject Invalid Syntax ]
 └─────────┬─────────┘
           │
           ▼
 ┌───────────────────┐
 │ Payload Size Check│ ──> Payload > 1 MB? ──> [ Return -ERR Payload Too Large ]
 └─────────┬─────────┘
           │
           ▼
 ┌───────────────────┐
 │ Command Extraction│
 └─────────┬─────────┘
           │
     ┌─────┴───────────────────────────────────┐
     │ Command == "XADD"                       │ Command != "XADD"
     ▼                                         ▼
 ┌──────────────────────────────┐    ┌──────────────────────────┐
 │ Target Key == Peer Inbox?    │    │ Inspect Dangerous Cmds   │
 │   - Strips/Blocks MAXLEN     │    │ (FLUSHALL, SHUTDOWN, etc)│
 ├──────────────────────────────┤    └─────────────┬────────────┘
 │ Target Key == General Stream │                  │
 │   - Inject MAXLEN ~ 1000     │                  │
 │   - Verify Citation <a>#<s>  │                  │
 └─────────┬────────────────────┘                  │
           │                                       │
           └───────────────────┬───────────────────┘
                               │
                               ▼
                   ┌───────────────────────┐
                   │ Forward Valid Frame   │
                   │ to fleet-valkey       │
                   └───────────────────────┘
```

### Rule 1: Mandatory `MAXLEN` Injection & Clamping
- **Condition**: Command is `XADD <stream> [MAXLEN|MINID ...] <ID> <field> <value> ...`.
- **Behavior**:
  - If no `MAXLEN` or `MINID` is specified, the proxy rewrites the RESP command array to inject `MAXLEN` `~` `1000` immediately following the stream key argument.
  - If `MAXLEN` is explicitly provided but exceeds `1000`, the proxy clamps the value to `1000`.

### Rule 2: Payload Size Capping (1 MB Threshold)
- **Condition**: Any string or bulk byte argument within a RESP frame.
- **Behavior**:
  - The proxy tracks byte counts during frame buffering.
  - If any field or aggregated message entry exceeds `1,048,576 bytes` (1 MB), parsing halts and an error frame is returned immediately: `-ERR message payload size exceeds maximum limit (1048576 bytes)`.

### Rule 3: Canonical Citation & Sender Claim Validation
- **Condition**: `XADD` entries containing a `citation` field.
- **Behavior**:
  - Validates that `citation` conforms to regex `^[a-zA-Z0-9_-]+#[0-9]+$` (`<agent>#<seq>`).
  - Cross-references the `sender` claim against the authenticated ACL username of the active connection session. If sender claim != ACL username, proxy returns `-ERR identity claim mismatch`.

### Rule 4: Cross-Inbox History Defense
- **Condition**: `XADD` targeting a stream key matching `*:inbox` where recipient owner != authenticated connection user.
- **Behavior**:
  - Strips or rejects any `MAXLEN` or `MINID` arguments, ensuring external callers can only append (`+xadd`) and cannot truncate the target agent's inbox history.

---

## Deployment Topology & Implementation Plan

### Runtime Topology
1. **Container Integration**: The Go proxy binary (`fleet-valkey-guard`) is deployed alongside Valkey inside the `fleet-valkey` container or as an infrastructure sidecar on the `fleet-infra` network.
2. **Port Binding**:
   - Internal Valkey Engine listens on `127.0.0.1:6379` (unexposed to bridge network).
   - Stream Guard Proxy listens on `0.0.0.0:6379` (exposed to `fleet-infra` network).
3. **Protocol Engine**: Reuses and extends `backplane/pkg/libbp` RESP parser utilities to perform zero-allocation frame scanning and buffer mutation.

---

## Summary & Next Steps

This evaluation establishes the **Transparent Go RESP Stream Guard Proxy Sidecar** as the authoritative server-side protection mechanism for Fleet backplane messaging. The corresponding Architecture Decision Record is documented in **[ADR 00054](../adr/00054-ServerSideValkeyStreamGuardWireInspection.md)**.
