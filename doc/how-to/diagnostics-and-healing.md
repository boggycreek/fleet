---
name: fleet-howto-diagnostics-and-healing
description: Operational guide for agent diagnostics and automated self-healing mechanisms (`fleet agent doctor`).
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# Diagnostics & Automated Self-Healing

This guide covers running agent diagnostics and understanding Fleet's automated self-healing subsystems using `fleet agent doctor <name>`.

---

## Architecture Philosophy

Fleet relies on automated diagnostic checks and self-healing to eliminate manual configuration drift and repair environmental defects without requiring full agent re-creation:
- **`fleet agent doctor <name>`**: Inspects and heals individual agent state, keypairs, permissions, Valkey ACLs, and Forgejo accounts.
- **`fleet infra doctor`**: Inspects and heals shared Valkey backplane streams, network bridges, and default Forgejo repositories (see [Shared Infrastructure Management](shared-infrastructure.md)).

---

## 1. Running Agent Diagnostics (`fleet agent doctor`)

Execute `fleet agent doctor <name>` (or `ft agent doctor <name>`) against any provisioned agent:

```bash
fleet agent doctor dev-bot-1
```

**Expected Output:**
```text
==> Running diagnostics for agent 'dev-bot-1'...
[✓] Configuration File (~/.local/share/fleet/agents/dev-bot-1.json): StatusOK
[⚡] Directory Permissions (~/.ssh/fleet): StatusHealed (repaired mode from 0755 to 0600)
[✓] Ed25519 Identity Keypair: StatusOK
[✓] SSH Config Include Directive (~/.ssh/config): StatusOK
[⚡] Valkey ACL Entry (~dev-bot-1:*): StatusHealed (re-established stream access rules)
[✓] Forgejo User Account ('dev-bot-1'): StatusOK
[✓] Forgejo Repositories ('fleet/tools.git', 'fleet/tasks.git'): StatusOK
[✓] Podman Volume ('fleet-dev-bot-1-home'): StatusOK
[✓] Podman Container Status ('fleet-dev-bot-1'): StatusOK
==> Agent 'dev-bot-1' health check complete. All systems operational.
```

---

## 2. Core Diagnostic Checks & Auto-Healing Mechanisms

`fleet agent doctor` performs 8 primary diagnostic check categories and applies self-healing handlers when defects are discovered:

| Check Category | Inspected Artifact | Auto-Healing Action when Defect Detected |
| :--- | :--- | :--- |
| **1. Configuration Integrity** | `~/.local/share/fleet/agents/<name>.json` | Re-generates missing JSON configuration fields or validates JSON schema. |
| **2. File & Directory Permissions** | `~/.ssh/fleet`, `~/.local/share/fleet/` | Automatically resets overly permissive file modes (e.g. `0755` -> `0600` for private keys, `0700` for SSH directories). |
| **3. Keypair Verification** | Ed25519 private key (`~/.local/share/fleet/keys/<name>`) & `~/.ssh/fleet` | Re-extracts missing public key from private key or re-generates missing host keypairs. |
| **4. SSH Config Integration** | `~/.ssh/config` & `~/.local/share/fleet/ssh_config` | Injects missing `Include ~/.local/share/fleet/ssh_config` into `~/.ssh/config` and updates host stanza (`Host fleet-<name>`). |
| **5. Valkey ACL Provisioning** | Valkey server ACL rule set (`~<name>:*` read/write, `~*:inbox` write) | Re-applies ACL user configuration and updates profile secret in `$XDG_STATE_HOME/bp/profiles/<name>.env`. |
| **6. Forgejo Account & SSH Key** | Forgejo REST API user `'<name>'` & public SSH key | Re-creates missing user account on `fleet-forgejo` and registers the agent's public SSH key. |
| **7. Forgejo System Repositories** | `fleet/tools.git` & `fleet/tasks.git` memberships | Fork/collaborator permissions re-assigned for agent to grant read/write access. |
| **8. Podman Volume & Runtime** | `fleet-<name>-home` volume & container network `fleet-infra` | Re-attaches persistent volume and recreates container if network bridge binding was dropped. |

---

## 3. Interpreting Doctor Status Flags

- **`[✓] StatusOK`**: The check passed cleanly without requiring action.
- **`[⚡] StatusHealed`**: A defect was detected and successfully repaired by `doctor`.
- **`[-] StatusWarning`**: Non-fatal warning (e.g. OCI base image missing from local store; will pull automatically on `start`).
- **`[✗] StatusError`**: Unrecoverable error requiring manual operator attention (e.g. physical disk full, Podman daemon unavailable).

---

## 4. Troubleshooting Tips

- **Permission Denied on SSH Keys**:
  If SSH connections fail with `Bad owner or permissions on ~/.ssh/config`, run `fleet agent doctor <name>` to apply standard `0600` / `0700` permissions automatically.
- **Agent cannot push to Forgejo repository**:
  Run `fleet agent doctor <name>` to verify that Forgejo user accounts, organization memberships (`fleet`), and repository collaborator rights are linked properly.
- **Automated diagnosis in CI / Scripts**:
  Run `fleet agent doctor <name>` before running autonomous agent tasks to guarantee that all dependencies and backplane credentials are fresh and healthy.
