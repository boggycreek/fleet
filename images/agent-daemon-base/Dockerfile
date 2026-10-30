# Autonomous Fleet Base OCI Image (ADR 00043)
# Extends fleet-core with bpd supervisor, unprivileged OpenSSH, and tmux
FROM docker.io/library/golang:1.27-bookworm AS builder
WORKDIR /src
COPY go.work ./
COPY agent/ ./agent/
COPY backplane/ ./backplane/
COPY mcp/ ./mcp/
COPY cli/ ./cli/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /build/bpd ./backplane/cmd/bpd

FROM fleet-core:latest

USER root

# Install OpenSSH server and tmux
RUN apt-get update && apt-get install -y --no-install-recommends \
    openssh-server \
    tmux \
    && rm -rf /var/lib/apt/lists/*

# Configure OpenSSH daemon for unprivileged execution
COPY images/agent-base/sshd_config /etc/ssh/sshd_config
RUN mkdir -p /var/run/sshd && chmod 755 /var/run/sshd

# Install bpd, agent-runner, and fleet-tasks
COPY --from=builder /build/bpd /usr/local/bin/bpd
COPY images/agent-base/agent-runner /usr/local/bin/agent-runner
COPY images/agent-base/fleet-tasks /usr/local/bin/fleet-tasks
RUN chmod +x /usr/local/bin/bpd /usr/local/bin/agent-runner /usr/local/bin/fleet-tasks

# Install tmux configuration and entrypoint
COPY images/agent-base/tmux.conf /home/agent/.tmux.conf
COPY images/agent-base/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh && \
    chown agent:agent /home/agent/.tmux.conf

USER agent
WORKDIR /home/agent/workspace

EXPOSE 2222

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
