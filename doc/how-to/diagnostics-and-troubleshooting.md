# How-To: Diagnostics and Troubleshooting

This guide explains how to use Fleet's built-in self-healing diagnostic commands (`fleet agent doctor` and `fleet infra doctor`) to audit system health, detect misconfigurations, and repair defects automatically.

---

## The Doctor Subsystem & Self-Healing Model

Fleet adopts an automated **diagnose-and-repair** model. When an issue occurs, the `doctor` command inspects configuration, storage, network namespaces, keys, and services—applying automated repairs for known failure modes.

### Status Symbols

- **`[✓] StatusOK`**: Component is healthy and operating within specification.
- **`[⚡] StatusHealed`**: A defect was detected and automatically repaired by Fleet.
- **`[-] StatusWarning`**: Non-fatal warning (e.g. image not yet cached locally).
- **`[✗] StatusError`**: Unrepairable defect requiring manual operator intervention.

---

## 1. Agent Diagnostics & Healing (`fleet agent doctor`)

Run `fleet agent doctor <name>` to audit a specific agent instance:

```bash
fleet agent doctor coder
```

**Example Output:**

```text
Fleet Diagnostic Doctor for Agent "coder"
========================================
  [✓] Config File: Present (~/.local/share/fleet/agents/coder.json)
  [⚡] Signing Keys: Repaired missing Ed25519 keypair
  [✓] SSH Keypair: Valid (~/.ssh/fleet)
  [✓] Managed SSH Config: Included in ~/.ssh/config
  [✓] Rootless Netns: Permission 0700 verified (/run/user/1000/netns)
  [✓] Podman Volume: Present (fleet-agent-coder-home)
  [✓] Container Image: Present (fleet-opencode:latest)
  [✓] Valkey ACLs: User 'coder' verified
  [✓] Forgejo Account: Verified user 'coder' and SSH key
  [✓] Container State: Running (fleet-agent-coder)
  [✓] SSH Port: Active (port 38472)

Diagnosis: 1 defect automatically healed. Agent is fully operational.
```

### What `fleet agent doctor` Audits & Repairs

1. **Configuration Integrity**: Verifies agent JSON file presence and `0600` permissions.
2. **Cryptographic Keypairs**: Auto-generates missing Ed25519 message-signing keys and SSH access keys.
3. **OpenSSH Configuration**: Verifies and repairs `Include ~/.local/share/fleet/ssh_config` in `~/.ssh/config`.
4. **Rootless Netns Mounts**: Auto-heals stale or corrupted rootless network namespace paths (`/run/user/$UID/netns`).
5. **Valkey ACLs & Passwords**: Restores missing backplane credentials and Valkey user ACL permissions.
6. **Forgejo Account & SSH Keys**: Ensures the agent account exists in local Forgejo and syncs public SSH keys.

---

## 2. Infrastructure Diagnostics & Healing (`fleet infra doctor`)

Run `fleet infra doctor` to inspect shared infrastructure (Valkey, Forgejo, PostgreSQL, bridge network):

```bash
fleet infra doctor
```

**Example Output:**

```text
Fleet Shared Infrastructure Diagnostic Doctor
=============================================
  [✓] Rootless Netns Directory: Verified (/run/user/1000/netns)
  [✓] Infrastructure Bridge Network: Present (fleet-infra)
  [✓] Persistent Volumes: Verified (fleet-valkey-data, fleet-forgejo-data, fleet-postgres-data)
  [✓] Valkey Container: Running (fleet-valkey)
  [✓] Valkey PING Response: PONG
  [✓] Forgejo Container: Running (fleet-forgejo)
  [✓] Forgejo HTTP Gateway: Healthy (http://127.0.0.1:3000)

Diagnosis: All shared infrastructure components are healthy.
```

---

## 3. Troubleshooting Common Issues

### Issue A: Rootless Netns Mount Failure (Exit Code 127)

**Symptom**: Podman fails with error `failed to mount runtime directory for rootless netns`.

**Solution**: Fleet runtime hooks intercept this condition automatically. Run `fleet infra doctor` or `fleet agent doctor <name>` to repair netns directory permissions and mounts:

```bash
fleet infra doctor
```

### Issue B: Container SSH Port Unreachable

**Symptom**: `fleet agent open` or `ssh fleet-<name>` fails with connection refused.

**Solution**:
1. Check if the agent container is running:
   ```bash
   fleet agent list
   ```
2. If stopped, start the agent:
   ```bash
   fleet agent start coder
   ```
3. Run `fleet agent doctor coder` to sync SSH configuration and verify port mapping.

### Issue C: Backplane Message Authentication Error

**Symptom**: `bp tell` or `bp say` returns `bp error: unauthorized` or signature verification failure.

**Solution**:
1. Verify active connection profile:
   ```bash
   bp whoami
   ```
2. Re-provision agent keys and Valkey ACLs:
   ```bash
   fleet agent doctor coder
   ```

### Issue D: Stale CLI Binaries or OCI Images

**Symptom**: CLI flags missing or image version mismatch after updating the codebase.

**Solution**: Run `fleet update` to execute an atomic three-stage update (git pull, native CLI build, OCI image rebuild):

```bash
fleet update
```

---

## Quick Reference Commands

| Goal | Command |
| :--- | :--- |
| Diagnose & heal agent | `fleet agent doctor <name>` |
| Diagnose & heal infrastructure | `fleet infra doctor` |
| View agent status & SSH ports | `fleet agent list` |
| View shared infrastructure status | `fleet infra list` |
| Update Fleet CLI & OCI images | `fleet update` |
