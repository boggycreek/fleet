# Standalone Static Platform Artifact OCI Layer (ADR 00048)
# Exporting core platform binaries and harness scripts under /opt/fleet
#
# Copyright (c) 2026 Boggy Creek Software LLC
#
# Use of this source code is governed by an MIT-style
# license that can be found in the LICENSE file.

# Stage 1: Build static platform binaries
FROM docker.io/library/golang:1.27-bookworm AS builder
WORKDIR /src
COPY go.work ./
COPY agent/ ./agent/
COPY backplane/ ./backplane/
COPY mcp/ ./mcp/
COPY cli/ ./cli/

RUN mkdir -p /opt/fleet && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /opt/fleet/bp ./backplane/cmd/bp && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /opt/fleet/bpd ./backplane/cmd/bpd && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /opt/fleet/fleet-agent ./agent/cmd/fleet-agent

# Copy agent-runner harness script
COPY images/agent-base/agent-runner /opt/fleet/agent-runner
RUN chmod +x /opt/fleet/agent-runner /opt/fleet/bp /opt/fleet/bpd /opt/fleet/fleet-agent

# Install Beads (bd) CLI binary statically
ARG BEADS_VERSION=1.3.0
RUN TARGETARCH=$(dpkg --print-architecture 2>/dev/null || echo "amd64") && \
    case "${TARGETARCH}" in \
        amd64) BEADS_ARCH="linux_amd64" ;; \
        arm64) BEADS_ARCH="linux_arm64" ;; \
        *) BEADS_ARCH="linux_amd64" ;; \
    esac && \
    curl -fsSL "https://github.com/gastownhall/beads/releases/download/v${BEADS_VERSION}/beads_${BEADS_VERSION}_${BEADS_ARCH}.tar.gz" | \
    tar -xzf - -C /opt/fleet bd && \
    chmod 755 /opt/fleet/bd

# Stage 2: Standalone static layer image
FROM scratch
COPY --from=builder /opt/fleet /opt/fleet
