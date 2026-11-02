# Fleet — Hardware, Model & Architectural Evaluations

This directory contains empirical research spikes, benchmark reports, and exploratory evaluations covering local inference engines, models, topologies, and networking.

---

## Evaluations & Benchmarks Index

| Evaluation | Focus Area | Key Findings / Outcome |
| :--- | :--- | :--- |
| **[Colibri MoE Inference](colibri-moe-inference.md)** | Model Architecture | Mixture-of-Experts inference latency, memory footprint, and multi-agent routing trade-offs. |
| **[Distributed Inference with Exo](distributed-inference-exo.md)** | Distributed Compute | Setup runbook and performance analysis for pooling LAN compute nodes using `exo`. |
| **[FreeBSD Host Support](freebsd-host-support.md)** | Host OS Compatibility | FreeBSD 14.x/15.x host compatibility, bhyve microVMs, ZFS dataset volumes, and pf.conf anchor networking. |
| **[Multi-Machine Network Federation](multi-machine-network-federation.md)** | LAN Networking | Multi-machine local network Fleet deployment, federated backplane relaying, and gateway DNS resolution. |
| **[NVIDIA OpenShell Evaluation](nvidia-openshell-evaluation.md)** | Sandboxing | Comparative analysis of NVIDIA OpenShell container sandboxing vs. Podman rootless isolation. |
| **[Valkey Stream Guard Evaluation](valkey-stream-guard-evaluation.md)** | Stream Security | Security evaluation of Valkey stream consumer groups and ACL enforcement. |

---

## Contribution Rule
When completing an architectural spike or benchmark:
- Record findings here in `doc/ai/evaluations/<topic>.md`.
- Register an entry in this index table.
- Do **not** place benchmark reports into `doc/ai/` root or `doc/ai/guidelines/`.
