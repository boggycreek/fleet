# Derivative OCI Image: Native Agent Sandbox (ADR 00041)
# Extends neutral agent-sandbox-base with fleet-agent runtime and full development toolchain
#
# Copyright (c) 2026 Boggy Creek Software LLC
#
# Use of this source code is governed by an MIT-style
# license that can be found in the LICENSE file.

# Stage 1: Build static fleet-agent binary
FROM docker.io/library/golang:1.27-bookworm AS builder
WORKDIR /src
COPY go.work ./
COPY agent/ ./agent/
COPY backplane/ ./backplane/
COPY mcp/ ./mcp/
COPY cli/ ./cli/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /build/fleet-agent ./agent/cmd/fleet-agent

# Stage 2: Native Agent OCI Image based on fleet-daemon-base
FROM fleet-daemon-base:latest

USER root

# Install Python and build-essential development dependencies
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3 \
    python3-pip \
    python3-venv \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

# Install Go compiler toolchain for in-container development workflows
ARG GO_VERSION=1.27.1
RUN TARGETARCH=$(dpkg --print-architecture) && \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" | tar -xz -C /usr/local && \
    ln -s /usr/local/go/bin/go /usr/local/bin/go && \
    ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt

# Install native agent runtime binary from builder
COPY --from=builder /build/fleet-agent /usr/local/bin/fleet-agent
RUN chmod +x /usr/local/bin/fleet-agent

# Copy native entrypoint
COPY images/agents/native/native-entrypoint.sh /usr/local/bin/native-entrypoint.sh
RUN chmod +x /usr/local/bin/native-entrypoint.sh

# Ensure home directory ownership belongs to agent
RUN chown -R agent:agent /home/agent

USER agent
WORKDIR /home/agent/workspace

ENTRYPOINT ["/usr/local/bin/native-entrypoint.sh"]
