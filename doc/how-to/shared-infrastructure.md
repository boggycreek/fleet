---
name: fleet-howto-shared-infrastructure
description: Guide for managing shared background infrastructure services (Valkey 8 backplane and Forgejo 9 git forge).
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# Shared Infrastructure Management

This guide covers managing shared background infrastructure services in Fleet: the **Valkey 8** message backplane and the **Forgejo 9** local Git forge.

---

## Shared Services Overview

Fleet relies on two long-running shared service containers operating on the rootless Podman network `fleet-infra`:

1. **Valkey 8 (`fleet-valkey`)**:
   - Provides the cross-agent messaging bus built on Redis Streams.
   - Listens on host port `6379` (bound to `127.0.0.1`).
   - Uses ACL authorization (`fleet-infra` network and per-agent user ACLs).
2. **Forgejo 9 (`fleet-forgejo`)**:
   - Provides host-local Git repository hosting, user accounts, organizational access (`fleet`), and pull requests.
   - Listens on host HTTP port `3000` (`http://127.0.0.1:3000`) and SSH port `2222`.
   - Hosts pre-seeded shared repositories: `fleet/tools.git` and `fleet/tasks.git`.

---

## 1. Starting Shared Services (`fleet infra up`)

To start or provision the shared Valkey and Forgejo containers:

```bash
fleet infra up
```

**Expected Output:**
```text
==> Provisioning shared infrastructure...
[✓] Podman bridge network 'fleet-infra' verified
[✓] Valkey 8 container 'fleet-valkey' started on port 6379
[✓] Forgejo 9 container 'fleet-forgejo' started on port 3000
[✓] Pre-seeded organization 'fleet' and system repositories
[✓] Shared infrastructure stack is healthy and ready.
```

---

## 2. Checking Infrastructure Health & Ports (`fleet infra list`)

To inspect the status, health, container IDs, and port mappings of shared infrastructure services:

```bash
fleet infra list
```

**Expected Output:**
```text
SERVICE        CONTAINER        STATUS    PORTS                   NETWORK
fleet-valkey   fleet-valkey     running   127.0.0.1:6379->6379    fleet-infra
fleet-forgejo  fleet-forgejo    running   127.0.0.1:3000->3000    fleet-infra
                                          127.0.0.1:2223->2222
```

---

## 3. Diagnosing and Auto-Healing Infrastructure (`fleet infra doctor`)

`fleet infra doctor` runs system health checks on the shared infrastructure stack, automatically detecting and repairing defects such as stale container state, network bridge errors, missing default repositories, or corrupted Valkey ACL configuration.

```bash
fleet infra doctor
```

**Expected Output:**
```text
==> Running shared infrastructure diagnostics...
[✓] Check Network 'fleet-infra': StatusOK
[✓] Check Container 'fleet-valkey': StatusOK
[⚡] Check Valkey ACL System: StatusHealed (re-established default admin ACL rule)
[✓] Check Container 'fleet-forgejo': StatusOK
[✓] Check Forgejo Repositories ('fleet/tools.git', 'fleet/tasks.git'): StatusOK
==> Infrastructure diagnosis complete. All checks passed.
```

### Diagnostic Status Indicators
- **`[✓] StatusOK`**: Component is fully operational.
- **`[⚡] StatusHealed`**: Defect detected and automatically repaired by `doctor`.
- **`[-] StatusWarning`**: Non-fatal anomaly (e.g. high resource usage).
- **`[✗] StatusError`**: Unrecoverable error requiring operator intervention.

---

## 4. Stopping Infrastructure (`fleet infra down`)

To halt shared infrastructure containers (preserves underlying Podman data volumes `fleet-valkey-data` and `fleet-forgejo-data`):

```bash
fleet infra down
```

**Expected Output:**
```text
==> Stopping shared infrastructure containers...
[✓] Container 'fleet-valkey' stopped
[✓] Container 'fleet-forgejo' stopped
[i] Persistent volumes preserved. Run 'fleet infra up' to restart services.
```

---

## 5. Troubleshooting Tips

- **Forgejo return HTTP 500 or connection refused**:
  Verify container status with `fleet infra list`. If offline or unhealthy, run `fleet infra doctor` to attempt automated repair.
- **Valkey ACL permission errors during `bp` commands**:
  Run `fleet infra doctor` followed by `fleet agent doctor <name>` to re-synchronize Valkey ACL profiles and environment files.
- **Port 6379 or 3000 already in use on host**:
  Check for host processes occupying ports using `lsof -i :6379` or `lsof -i :3000`. Stop host redis or forgejo instances before calling `fleet infra up`.
