---
name: fleet-infographics-index
description: Index and visual walkthrough gallery for Fleet platform architecture, security perimeters, and workflows.
---

<!--
Copyright (c) 2026 Boggy Creek Software LLC

Use of this source code is governed by an MIT-style
license that can be found in the LICENSE file.
-->

# Fleet Visual Walkthroughs & Infographics

Welcome to the **Fleet Infographics & Visual Gallery**. This directory contains diagram-driven architecture overviews, sequence walkthroughs, and visual cheat sheets designed for visual learners, platform operators, and new contributors.

---

## Visual Guides & Presentations

| Presentation / Infographic | Topic & Focus Area | Key Visual Diagrams Included |
| :--- | :--- | :--- |
| **[Fleet Architecture & Capabilities Overview](fleet-architecture-overview.md)** | **Core Flagship Presentation**: Security perimeter, container hierarchy, cognitive loop, and IDE ensembling. | • Physical Host vs Container Sandbox Boundary<br>• Layered OCI Container Hierarchy (5 tiers)<br>• In-Container Cognitive Engine & Syntax Gate<br>• Ed25519 Cross-Agent Messaging Sequence<br>• Zero-Touch OpenSSH IDE Remote Attach Flow<br>• Two-Plane Task Tracking Matrix |

---

## Visual Convention Standards

All visual infographics in Fleet adhere to these principles:
- **Mermaid Standard**: Diagrams use standard Mermaid (`flowchart TB/LR`, `sequenceDiagram`, `stateDiagram-v2`) compatible with GitHub markdown rendering.
- **Color-Coded Semantic Domains**:
  - 🔵 **Blue / Slate (`#1e293b`)**: Host physical machine and developer desktop.
  - 🟢 **Green / Emerald (`#064e3b`)**: Unprivileged agent container sandboxes.
  - 🟣 **Purple / Indigo (`#1e1b4b`)**: Shared infrastructure (`fleet-valkey`, `fleet-forgejo`).
  - 🔴 **Rose / Magenta (`#831843`)**: Cognitive engine, LLM inference, and reasoning loops.
- **Copy-Pasteable Quick References**: Every infographic pairs structural diagrams with practical CLI cheat sheets.

---

## Related Documentation

- **[Practical How-To Guides](../how-to/README.md)**: Scenario-based task walkthroughs.
- **[AI Agent Knowledge Base](../ai/README.md)**: Deep architectural specs and protocol rules.
- **[Architectural Decision Records (ADRs)](../adr/README.md)**: Historical technical decisions.
