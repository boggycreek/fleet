---
name: fleet-howto-index
description: Index and navigation guide for practical user workflows and operational how-to guides in Fleet.
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# Fleet Operational How-To Guides

Welcome to the Fleet How-To documentation directory. While [`doc/ai/`](../ai/README.md) provides deep architectural specifications for AI agents and [`doc/adr/`](../adr/) records architectural decision records, these guides offer practical, scenario-oriented, copy-pasteable workflows for human operators, contributors, and platform administrators.

---

## Guide Index

| Guide | Description | Key Commands Covered |
| :--- | :--- | :--- |
| **[Getting Started Quickstart](getting-started.md)** | Install Fleet, start background infrastructure, and launch your first autonomous agent. | `./install.sh`, `fleet infra up`, `fleet agent create`, `start` |
| **[Agent Lifecycle Operations](agent-lifecycle.md)** | Create, provision, inspect, attach, clean, and retire agent instances safely. | `fleet agent create`, `start`, `list`, `tmux`, `ssh`, `clean`, `retire` |
| **[Baremetal Agent Setup](baremetal-agent-setup.md)** | Enroll and deploy physical machines via one-time tokens, `install-agent.sh`, and systemd user services. | `fleet agent create --baremetal`, `install-agent.sh`, `systemctl --user` |
| **[IDE & Remote Development Integration](ide-remote-dev.md)** | Connect desktop IDEs (VS Code, Cursor, JetBrains Gateway) and configure OpenSSH stanzas. | `fleet agent open`, `fleet agent ssh-config`, `Include` config |
| **[Shared Infrastructure Management](shared-infrastructure.md)** | Manage shared Valkey 8 backplane, Forgejo 9 forge, and 128k KV cache topology presets. | `fleet infra up`, `list`, `doctor`, `down`, `fleet infra topology` |
| **[Cross-Agent Backplane Messaging](cross-agent-backplane.md)** | Broadcast messages, send direct peer requests, reply to threads, and set status with `bp`. | `bp whoami`, `say`, `tell`, `reply`, `recv`, `peers`, `finger`, `status set` |
| **[Diagnostics & Automated Self-Healing](diagnostics-and-healing.md)** | Inspect agent health and understand automatic defect remediation across SSH, Valkey, and Forgejo. | `fleet agent doctor <name>`, status checks & auto-healers |

---

## Core Conventions

- **Host CLI (`fleet` / `ft`)**: Administer agent containers, shared infrastructure, and IDE remote connections on the host machine.
- **Messaging CLI (`bp`)**: Interact with the asynchronous Valkey communication bus from either the host machine or inside agent containers.
- **Rootless Podman Isolation**: Agents run inside rootless Podman containers on the `fleet-infra` bridge network, protecting host credentials while enabling autonomous shell execution.

---

## Related Documentation

- **[AI Agent Knowledge Base](../ai/README.md)**: Deep protocol specifications, architecture guidelines, and coding agent skills.
- **[Architectural Decision Records (ADRs)](../adr/)**: Formal records of technical choices, design trade-offs, and platform evolution.
- **[Contributor Guide](../../CONTRIBUTING.md)**: Development setup (`setup.sh`), quality gates (`make check`), and task tracking.
