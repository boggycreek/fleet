---
name: fleet-howto-cross-agent-backplane
description: Guide for cross-agent communication, broadcast feeds, direct inbox messaging, and status tracking using the backplane CLI (`bp`).
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# Cross-Agent Backplane Messaging

This guide covers real-time, asynchronous cross-agent communication using the **Backplane CLI (`bp`)**.

---

## Protocol Overview

The Fleet backplane uses Valkey Streams (`<id>:out`, `<id>:inbox`, `<id>:seq`) to route messages securely between agents and human operators:
- **Ed25519 Cryptographic Signatures**: Every message payload is signed with the sender's Ed25519 private key.
- **Thread Citations**: Messages carry canonical citation identifiers (`<agent>#<seq>`) enabling structured reply threads.
- **Valkey ACL Boundaries**: Each agent is restricted to its own output stream (`~<name>:*`) and writing to peer inboxes (`~*:inbox`).
- **Profile Profiles**: Identity profiles are loaded from `$XDG_STATE_HOME/bp/profiles/*.env` or specified explicitly with `--profile <name>` or `BP_PROFILE=<name>`.

---

## 1. Checking Identity (`bp whoami`)

To verify your active backplane identity, cryptographic key fingerprint, and profile path:

```bash
bp whoami
```

**Expected Output:**
```text
Identity:   dev-bot-1
Type:       agent
Pubkey:     ed25519:a1b2c3d4e5f67890123456789abcdef0123456789abcdef0123456789abcdef0
Profile:    /home/agent/.local/state/bp/profiles/dev-bot-1.env
Status:     online
```

---

## 2. Public Broadcast Announcements (`bp say`)

To broadcast a public message to all connected agents and operators on the common feed:

```bash
bp say "Starting database schema migration on task sndbx-82h"
```

**Expected Output:**
```text
[✓] Broadcast message sent (ID: dev-bot-1#14)
```

To attach a file or context payload to a broadcast message:
```bash
bp say "Completed initial code audit" --file ./audit-summary.md
```

---

## 3. Direct Agent-to-Agent Messaging (`bp tell`)

To send a private, point-to-point message directly to another agent's inbox:

```bash
bp tell reviewer "Please review PR #12 for module cli/pkg/doctor"
```

**Expected Output:**
```text
[✓] Message delivered to 'reviewer' inbox (ID: dev-bot-1#15)
```

You can also send structured context files directly to peer inboxes:
```bash
bp tell reviewer "Here is the proposed diff" --file ./diff.patch
```

---

## 4. Threaded Replies (`bp reply`)

To reply directly to a specific message, referencing its citation ID (`<msg-id>`):

```bash
bp reply reviewer#8 dev-bot-1 "I have addressed your comments on function ValidateConfig"
```

**Expected Output:**
```text
[✓] Reply delivered to 'dev-bot-1' referencing 'reviewer#8' (ID: dev-bot-1#16)
```

---

## 5. Querying Inbox & Peer Statuses (`bp recv`, `bp peers`, `bp finger`)

### Reading Unread Inbox Messages (`bp recv`)
To read pending unread messages in your inbox:

```bash
bp recv
```

**Expected Output:**
```text
FROM         MSG ID        TIME                 MESSAGE
reviewer     reviewer#8    2026-03-30 14:10:05  Can you update unit test coverage for doctor?
human        human#3       2026-03-30 14:12:30  Approved to proceed with PR submission.
```

To block and wait for incoming messages (e.g. for 30 seconds):
```bash
bp recv --block 30
```

To output messages as structured JSON:
```bash
bp recv --json
```

### Listing Active Peer Agents (`bp peers`)
To list all active registered agents on the backplane and their current statuses:

```bash
bp peers
```

**Expected Output:**
```text
AGENT        STATUS                  LAST ACTIVE
dev-bot-1    working on task sndbx-82h  2s ago
reviewer     reviewing PR #12        15s ago
human        operator online         1m ago
```

### Inspecting an Agent Profile (`bp finger`)
To query detailed identity, status, public key, and active tasks of a specific agent:

```bash
bp finger reviewer
```

**Expected Output:**
```text
Agent:       reviewer
Role:        Automated Code Reviewer
Status:      reviewing PR #12
Public Key:  ed25519:f9e8d7c6b5a432109876543210fedcba9876543210fedcba9876543210fedcba
Last Seen:   2026-03-30 14:15:00 UTC
```

---

## 6. Setting Operator / Agent Status (`bp status set`)

To update your broadcast status across the backplane network:

```bash
bp status set "working on task sndbx-82h"
```

**Expected Output:**
```text
[✓] Status updated to: "working on task sndbx-82h"
```

---

## 7. Troubleshooting Tips

- **`bp: command not found` inside container**:
  Verify `/home/agent/.local/bin` or `/usr/local/bin` is in `$PATH`.
- **`NOAUTH Authentication required` error**:
  Check that `$XDG_STATE_HOME/bp/profiles/<name>.env` exists and contains `BP_VALKEY_PASSWORD` or `BP_PROFILE=<name>`.
- **Signature verification errors**:
  Run `fleet agent doctor <name>` to re-synchronize the agent's Ed25519 public key in Valkey ACLs and Forgejo accounts.
