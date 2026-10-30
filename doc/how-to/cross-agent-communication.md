# How-To: Cross-Agent Communication via Backplane (`bp`)

This guide explains how autonomous agents and human operators communicate across the Fleet platform using the `bp` backplane messaging CLI and Valkey Streams fabric.

---

## Architecture Overview

Fleet uses an asynchronous, cryptographically authenticated messaging fabric powered by Valkey Streams (`fleet-valkey`):

- **Message Streams**: Each agent possesses an isolated inbox stream (`<id>:inbox`) and broadcast stream (`<id>:out`).
- **Cryptographic Authentication**: Every backplane message is signed using Ed25519 private keys and verified against public keys stored in Valkey (`identity:<id>`).
- **Access Control Lists (ACLs)**: Valkey ACL rules prevent agents from snooping on peer inboxes or impersonating other identities.
- **Connection Profiles**: `bp` resolves connection profiles statelessly from `$XDG_STATE_HOME/bp/profiles/<name>.env` or via `--profile <name>`.

---

## 1. Identity & Profile Resolution

Before issuing messaging commands, verify your active identity and connection parameters:

```bash
bp whoami
```

**Expected Output:**

```text
Agent ID:   operator
Mode:       human
Host:       127.0.0.1:6379
Public Key: 4zX9vL...
```

To view available connection profiles on your host:

```bash
bp profile list
```

To execute a command using a specific agent profile:

```bash
bp --profile coder whoami
```

---

## 2. Discovering Fleet Peers

List all active agents and their presence status across the fleet:

```bash
bp peers
```

**Expected Output:**

```text
AGENT_ID           ROLE         STATUS
coder              agent        working
auditor            agent        idle
operator           human        active
```

To format peer data as JSON for script consumption:

```bash
bp peers --json
```

---

## 3. Fleet-Wide Broadcast Messaging (`bp say`)

To broadcast a status update or milestone to all agents monitoring the backplane:

```bash
bp say "Completed refactoring pkg/config; running test coverage gates now."
```

**Expected Output:**

```text
[operator#1] Completed refactoring pkg/config; running test coverage gates now.
```

### Attaching Payloads & Files

For long log outputs, code diffs, or structured JSON, park the file payload and broadcast a summary reference:

```bash
bp say "Coverage report updated" --file ./coverage.txt
```

---

## 4. Direct Point-to-Point Messaging (`bp tell`)

Send a targeted directive or inquiry directly to a specific agent's inbox (`<peer>:inbox`):

```bash
bp tell auditor "Please review PR #42 in local Forgejo repo."
```

**Expected Output:**

```text
[operator#2 -> auditor] Please review PR #42 in local Forgejo repo.
```

### Direct Message with File Payload

To send a patch file or error trace directly to an agent:

```bash
bp tell coder "Review build error log" --file ./build.log
```

**Expected Output:**

```text
[operator#3 -> coder] Review build error log
  └── Payload attached: blob:valkey:a1b2c3d4
```

---

## 5. Reading Incoming Messages (`bp recv`)

Agents and scripts poll for unread messages using `bp recv`. Messages are verified automatically against the sender's public key:

```bash
bp recv
```

**Expected Output:**

```text
14:20:05 [coder#1 -> operator] [verified]: Refactoring complete, tests passing with 92.4% coverage.
```

### Blocking / Long-Polling

To wait for incoming messages for up to 30 seconds:

```bash
bp recv --block 30
```

To output messages as structured JSON:

```bash
bp recv --json
```

---

## 6. Threaded Replies & Citations (`bp reply`)

When responding to an incoming message, quote the message ID citation to preserve conversational lineage:

```bash
bp reply coder#1 coder "Great work! Submitting PR now."
```

**Expected Output:**

```text
[operator#4 -> coder (re: coder#1)] Great work! Submitting PR now.
```

---

## 7. Ephemeral Presence Status (`bp status`)

Set a 300-second ephemeral status message visible in `bp peers`:

```bash
bp status set "Running benchmark test suite"
```

**Expected Output:**

```text
[operator] status set: Running benchmark test suite
```

---

## 8. Reading Human Operator Log (`bp human`)

Agents query historical human operator broadcasts to stay aligned with overall mission directives:

```bash
bp human --count 10
```

---

## 9. In-Container Integration & MCP Adapter

Inside running agent containers:
- **`bpd` Daemon**: Polls `<agent>:inbox` in the background and dispatches turns to the deep-focus worker.
- **Model Context Protocol (`bp-mcp`)**: Provides schema-validated tools for cognitive LLM engines (`send_message`, `read_inbox`, `broadcast_status`).

---

## Summary Command Reference

| Action | Command |
| :--- | :--- |
| Check Identity | `bp whoami` |
| List Peers | `bp peers` |
| Broadcast Message | `bp say "<msg>" [--file <path>]` |
| Direct Message | `bp tell <agent> "<msg>" [--file <path>]` |
| Threaded Reply | `bp reply <msgid> <agent> "<msg>"` |
| Read Inbox | `bp recv [--block <sec>] [--json]` |
| Set Status | `bp status set "<msg>"` |
