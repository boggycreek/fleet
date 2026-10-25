# Acknowledgements & Open Source Credits

The **Fleet** platform is developed by **Boggy Creek Software LLC**. We believe deeply in the open-source ethos and acknowledge that this project is made possible by the incredible work of open-source software creators, maintainers, foundations, and communities worldwide.

---

## Zero-Dependency Core Architecture

The native binaries across the workspace (`fleet`, `bp`, `bpd`, `bp-mcp`, `forgejo-mcp`, `gitea-mcp`, `beads-mcp`, `sonar-mcp`) and internal Go libraries (`sandbox/pkg/...`, `backplane/pkg/...`, `mcp/pkg/...`) are engineered intentionally with **zero external Go module dependencies**. We build exclusively on the standard library of the **Go Programming Language**, maintained by the Go Authors and Google:

- **[The Go Programming Language](https://go.dev/)** — BSD-3-Clause License  
  *Provided the robust concurrency model, cryptographic primitives (`crypto/ed25519`), networking, and tooling enabling reliable, cross-platform static binaries.*

---

## Foundational Open Source Projects & Communities

We express our sincere gratitude to the following open-source projects, tools, and platforms that form the pillars of Fleet:

### 1. Container Runtime, Java Environment & Process Isolation
- **[Podman](https://podman.io/)** (Red Hat / Containers Community) — Apache-2.0 License  
  *Provides daemonless, rootless OCI container orchestration, unprivileged user namespaces (`subuid`/`subgid`), and secure container perimeter enforcement without requiring a host daemon.*
- **[Conmon & crun](https://github.com/containers)** (Containers Community / Red Hat) — Apache-2.0 & GPL-2.0 Licenses  
  *Provides lightweight OCI container monitoring and low-level container execution.*
- **[Debian GNU/Linux](https://www.debian.org/)** (The Debian Project) — Debian Free Software Guidelines (DFSG)  
  *Serves as the solid, predictable base operating system layer for our `agent-sandbox-base` container image (`images/agent-base`).*
- **[Deno](https://deno.land/)** (Deno Land Inc. / The Deno Authors) — MIT License  
  *Modern, secure JavaScript and TypeScript runtime bundled into the base agent image for headless automation.*
- **[Eclipse Temurin OpenJDK](https://adoptium.net/)** (Eclipse Adoptium / Eclipse Foundation) — GPL-2.0-with-classpath-exception License
  *Provides the OpenJDK Java runtime environment powering the SonarQube Community Build image (`images/infra/sonarqube`).*

### 2. High-Performance Messaging & Shared Infrastructure
- **[Valkey](https://valkey.io/)** (Linux Foundation / Valkey Community) — BSD-3-Clause License  
  *Powers our high-performance, real-time agent backplane bus (`bp`), Valkey Streams, pub/sub channels, and granular per-agent ACL security boundaries.*
- **[Forgejo](https://forgejo.org/)** (Forgejo Authors / Codeberg e.V.) — MIT License
  *Provides lightweight, community-governed self-hosted Git repository hosting, forge REST APIs, and automated SSH key management for local multi-agent codebases.*
- **[Gitea](https://about.gitea.com/)** (Gitea Authors / Community) — MIT License  
  *Provides lightweight self-hosted Git forge capabilities and backward compatibility.*
- **[PostgreSQL](https://www.postgresql.org/)** (The PostgreSQL Global Development Group) — PostgreSQL License  
  *Provides industrial-grade relational database storage for shared infrastructure services.*
- **[SonarQube Community Build](https://www.sonarsource.com/products/sonarqube/)** (SonarSource) — LGPL-3.0 License  
  *Provides continuous automated code quality inspection, static analysis, and security vulnerability scanning for autonomous agent work.*

### 3. Agent Backlog & Graph Issue Tracking
- **[Beads (`bd`)](https://github.com/gastownhall/beads)** (Gas Town Hall / Steve Yegge) — MIT / Apache-2.0 Licenses  
  *Provides durable, graph-based issue tracking, blocker resolution, and multi-agent task coordination.*
- **[Dolt](https://www.dolthub.com/)** (DoltHub, Inc.) — Apache-2.0 License  
  *Provides the world's first version-controlled SQL relational database that powers distributed, peer-to-peer Beads issue synchronization.*

### 4. In-Container Session & Connectivity Tooling
- **[OpenSSH](https://www.openssh.com/)** (The OpenBSD Project) — BSD-style Licenses  
  *Enables unprivileged, zero-trust cryptographic SSH access into isolated agent sandboxes.*
- **[tmux](https://github.com/tmux/tmux)** (Nicholas Marriott & Contributors) — ISC License  
  *Provides terminal multiplexing, supervisor session management, and live operator inspection inside sandbox containers.*
- **[Git](https://git-scm.com/)** (Linus Torvalds, Junio C Hamano & Git Community) — GPL-2.0 License  
  *The ubiquitous version control system underpinning all agent version tracking and patch workflows.*

### 5. Developer Ecosystem & IDE Providers
- **[Visual Studio Code](https://code.visualstudio.com/) & [VSCodium](https://vscodium.com/)** — MIT License  
  *Enables desktop Remote-SSH development and agent pair-programming.*
- **[JetBrains](https://www.jetbrains.com/) Gateway & Toolbox** — JetBrains API Ecosystem  
  *Enables remote IDE backend attaching to in-container agent development environments.*

### 6. Developer Quality Gates, Security & Composition Analysis
- **[golangci-lint](https://golangci-lint.run/)** (golangci-lint Authors) — GPL-3.0 License  
  *Fast, extensible Go linters runner ensuring strict codebase quality standards.*
- **[govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)** (The Go Authors) — BSD-3-Clause License  
  *Official Go vulnerability analysis scanner against the national Go Vulnerability Database.*
- **[gosec](https://github.com/securego/gosec)** (Secure Go Community) — Apache-2.0 License  
  *Go AST security scanner inspecting source trees for security vulnerabilities.*
- **[deadcode](https://pkg.go.dev/golang.org/x/tools/cmd/deadcode)** (The Go Authors) — BSD-3-Clause License  
  *Reachability analysis detecting dead and unreachable code across the monorepo.*
- **[ShellCheck](https://www.shellcheck.net/)** (Vidar Holen) — GPL-3.0 License  
  *Static analysis tool for shell script safety and POSIX/bash portability.*
- **[Syft](https://github.com/anchore/syft)** (Anchore, Inc.) — Apache-2.0 License  
  *CLI generator for Software Bill of Materials (SBOM) in SPDX and CycloneDX specifications.*

---

## Legal Notices & Third-Party Licenses

For formal legal copyright notices, SPDX license identifiers, and redistributable third-party license texts, please consult **[NOTICES.md](NOTICES.md)**.
