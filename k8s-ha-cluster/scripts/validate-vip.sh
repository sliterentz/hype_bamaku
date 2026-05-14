#!/usr/bin/env bash
set -Eeuo pipefail

VIP="${VIP:-${1:-}}"
DOMAIN="${DOMAIN:-${2:-}}"

if [ -z "$VIP" ]; then
  echo "Usage: VIP=192.168.1.12 DOMAIN=rifcloud.fantastickim.dev ./scripts/validate-vip.sh" >&2
  exit 2
fi

echo "🔌 VIP/API connectivity check on $(hostname)"
echo "VIP:    $VIP"
echo "DOMAIN: ${DOMAIN:-<none>}"

echo "🧭 Route"
ip -4 route get "$VIP" || true

echo "📍 DNS (best-effort)"
if [ -n "$DOMAIN" ]; then
  if command -v getent >/dev/null 2>&1; then
    getent hosts "$DOMAIN" || true
  elif command -v nslookup >/dev/null 2>&1; then
    nslookup "$DOMAIN" || true
  fi
fi

echo "🏓 Ping VIP"
ping -c 3 -W 2 "$VIP" || true

echo "🩺 API healthz"
curl -vk --connect-timeout 2 --max-time 4 "https://$VIP:6443/healthz" || true

echo "📋 Neighbor table"
ip neigh show | grep "$VIP" || true

