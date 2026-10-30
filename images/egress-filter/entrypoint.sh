#!/usr/bin/env bash
# Copyright (c) 2026 Boggy Creek Software LLC
#
# Use of this source code is governed by an MIT-style
# license that can be found in the LICENSE file.

# Network Egress Filter Sidecar Entrypoint (ADR 00032)
#
# Configures default-deny packet filtering via iptables and dynamic domain
# allowlisting via dnsmasq + ipset.

set -euo pipefail

# 1. Setup allowed-egress ipset
ipset create allowed-egress hash:ip -exist

# 2. Configure iptables firewall

# Flush existing rules and user-defined chains
iptables -F
iptables -X 2>/dev/null || true

# Set default policies to DROP
iptables -P INPUT DROP
iptables -P FORWARD DROP
iptables -P OUTPUT DROP

# Allow loopback (lo)
iptables -A INPUT -i lo -j ACCEPT
iptables -A OUTPUT -o lo -j ACCEPT

# Allow established and related connections
if iptables -A INPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT 2>/dev/null; then
  iptables -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
else
  iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
  iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
fi

# Allow traffic on backplane/infra network interface or subnet (10.89.0.0/16 and local subnet)
iptables -A INPUT -s 10.89.0.0/16 -j ACCEPT
iptables -A OUTPUT -d 10.89.0.0/16 -j ACCEPT

LOCAL_SUBNET=$(ip -4 route show dev eth0 2>/dev/null | grep -v default | awk '{print $1}' | head -n1 || true)
if [ -n "${LOCAL_SUBNET}" ] && [ "${LOCAL_SUBNET}" != "10.89.0.0/16" ]; then
  iptables -A INPUT -s "${LOCAL_SUBNET}" -j ACCEPT
  iptables -A OUTPUT -d "${LOCAL_SUBNET}" -j ACCEPT
fi

GATEWAY_IP=$(ip route show default 2>/dev/null | awk '{print $3}' || true)
if [ -n "${GATEWAY_IP}" ]; then
  iptables -A INPUT -s "${GATEWAY_IP}" -j ACCEPT
  iptables -A OUTPUT -d "${GATEWAY_IP}" -j ACCEPT
fi

# Detect upstream bridge DNS resolver
UPSTREAM_DNS=$(grep -v '^#' /etc/resolv.conf 2>/dev/null | grep nameserver | awk '{print $2}' | grep -v '^127\.' | head -n1 || true)
if [ -z "${UPSTREAM_DNS}" ]; then
  UPSTREAM_DNS="${GATEWAY_IP}"
fi

# Allow local DNS to port 53
iptables -A INPUT -p udp --dport 53 -j ACCEPT
iptables -A INPUT -p tcp --dport 53 -j ACCEPT
iptables -A OUTPUT -p udp --sport 53 -j ACCEPT
iptables -A OUTPUT -p tcp --sport 53 -j ACCEPT

# Allow inbound DNS replies from upstream resolvers
iptables -A INPUT -p udp --sport 53 -j ACCEPT
iptables -A INPUT -p tcp --sport 53 -j ACCEPT

# Allow outbound DNS to upstream resolvers (1.1.1.1:53, 8.8.8.8:53, and local bridge DNS)
iptables -A OUTPUT -p udp -d 1.1.1.1 --dport 53 -j ACCEPT
iptables -A OUTPUT -p tcp -d 1.1.1.1 --dport 53 -j ACCEPT
iptables -A OUTPUT -p udp -d 8.8.8.8 --dport 53 -j ACCEPT
iptables -A OUTPUT -p tcp -d 8.8.8.8 --dport 53 -j ACCEPT
if [ -n "${UPSTREAM_DNS}" ]; then
  iptables -A OUTPUT -p udp -d "${UPSTREAM_DNS}" --dport 53 -j ACCEPT
  iptables -A OUTPUT -p tcp -d "${UPSTREAM_DNS}" --dport 53 -j ACCEPT
fi

# Allow outbound TCP 80/443 to allowed-egress ipset
iptables -A OUTPUT -p tcp --dport 80 -m set --match-set allowed-egress dst -j ACCEPT
iptables -A OUTPUT -p tcp --dport 443 -m set --match-set allowed-egress dst -j ACCEPT

# Allow internal infrastructure service ports (Valkey, Forgejo/Gitea, SonarQube, PostgreSQL, SSH)
INFRA_PORTS="6379 3000 9000 5432 2222"
for port in ${INFRA_PORTS}; do
  iptables -A INPUT -p tcp --dport "${port}" -j ACCEPT
  iptables -A OUTPUT -p tcp --dport "${port}" -j ACCEPT
  iptables -A INPUT -p udp --dport "${port}" -j ACCEPT
  iptables -A OUTPUT -p udp --dport "${port}" -j ACCEPT
done

# Dynamically route internal infrastructure hostnames to the detected upstream bridge resolver
if [ -n "${UPSTREAM_DNS}" ]; then
  cat <<EOF > /etc/dnsmasq.d/internal-services.conf
server=/valkey/${UPSTREAM_DNS}
server=/fleet-valkey/${UPSTREAM_DNS}
server=/forgejo/${UPSTREAM_DNS}
server=/fleet-forgejo/${UPSTREAM_DNS}
server=/sonarqube/${UPSTREAM_DNS}
server=/fleet-sonarqube/${UPSTREAM_DNS}
server=/agent-sandbox-sonarqube/${UPSTREAM_DNS}
server=/postgres/${UPSTREAM_DNS}
server=/fleet-postgres/${UPSTREAM_DNS}
server=/llm-gateway/${UPSTREAM_DNS}
server=/host.containers.internal/${UPSTREAM_DNS}
server=/dns.podman/${UPSTREAM_DNS}
EOF
fi

# Dynamically add non-lo container interfaces to dnsmasq config if detected
for iface in $(ip -o link show 2>/dev/null | awk -F': ' '{print $2}' | cut -d'@' -f1 | grep -v '^lo$' || true); do
  if ! grep -q "^interface=${iface}" /etc/dnsmasq.d/egress.conf 2>/dev/null; then
    echo "interface=${iface}" >> /etc/dnsmasq.d/egress.conf
  fi
done

echo "[egress-filter] Initialized iptables and ipset. Starting dnsmasq..."

# Keep foreground with dnsmasq -k
exec dnsmasq -k
