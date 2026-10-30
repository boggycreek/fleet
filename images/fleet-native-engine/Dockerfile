# Standalone Headless Native Agent Cognitive Engine OCI Layer (ADR 00049)
# Exporting cognitive engine binaries and default MCP tool harnesses under /opt/fleet/engine
#
# Copyright (c) 2026 Boggy Creek Software LLC
#
# Use of this source code is governed by an MIT-style
# license that can be found in the LICENSE file.

# Stage 1: Build static cognitive engine and MCP tool harness binaries
FROM docker.io/library/golang:1.27-bookworm AS builder
WORKDIR /src
COPY go.work ./
COPY agent/ ./agent/
COPY backplane/ ./backplane/
COPY mcp/ ./mcp/
COPY sandbox/ ./sandbox/

RUN mkdir -p /opt/fleet/engine && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /opt/fleet/engine/fleet-agent ./agent/cmd/fleet-agent && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /opt/fleet/engine/bp-mcp ./mcp/cmd/bp-mcp && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /opt/fleet/engine/forgejo-mcp ./mcp/cmd/forgejo-mcp && \
    ln -s forgejo-mcp /opt/fleet/engine/gitea-mcp && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /opt/fleet/engine/beads-mcp ./mcp/cmd/beads-mcp && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /opt/fleet/engine/sonar-mcp ./mcp/cmd/sonar-mcp && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /opt/fleet/engine/test-runner-mcp ./mcp/cmd/test-runner-mcp && \
    chmod +x /opt/fleet/engine/*

# Stage 2: Standalone static layer image
FROM scratch
COPY --from=builder /opt/fleet/engine /opt/fleet/engine
