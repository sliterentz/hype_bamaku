#!/usr/bin/env bash
set -Eeuo pipefail

VIP_INTERFACE="${VIP_INTERFACE:-${1:-}}"
VIP_ADDRESS="${VIP_ADDRESS:-${2:-}}"
PEERS="${PEERS:-${3:-}}"

fail() {
  echo "❌ $*" >&2
  exit 1
}

warn() {
  echo "⚠️  $*" >&2
}

echo "🔎 Pre-flight checks on $(hostname)"

echo "🔐 Sudo (non-interactive)"
if ! sudo -n true 2>/dev/null; then
  fail "Passwordless sudo is required for bootstrap/preflight"
fi

echo "📦 Container runtime"
sudo -n systemctl is-active --quiet containerd || fail "containerd is not active"
containerd --version || true
if command -v ctr >/dev/null 2>&1; then
  echo "📋 containerd socket permissions:"
  sudo -n ls -l /run/containerd/containerd.sock || true
  echo "📋 executing user: $(id)"
  echo "🔍 Validating ctr access to containerd..."
  CTR_OK=false
  CTR_LAST=""
  for i in {1..15}; do
    if CTR_LAST=$(sudo -n ctr version 2>&1); then
      CTR_OK=true
      break
    fi
    echo "   Attempt $i/15: $CTR_LAST"
    sleep 2
  done
  if [ "$CTR_OK" != true ]; then
    fail "ctr cannot connect to /run/containerd/containerd.sock"
  fi
else
  warn "ctr not found (containerd tooling missing)"
fi

echo "🧠 CPU/Memory/Disk"
CPU=$(nproc || echo 0)
MEM_MB=$(free -m | awk '/Mem:/ {print $2}' || echo 0)
DISK_GB=$(df -BG / | awk 'NR==2 {gsub(/G/,"",$4); print $4}' || echo 0)
echo "   CPU: ${CPU} cores"
echo "   Mem: ${MEM_MB} MiB"
echo "   Disk free (/): ${DISK_GB} GiB"
[ "${CPU}" -ge 2 ] || fail "CPU < 2 cores"
[ "${MEM_MB}" -ge 2048 ] || fail "Memory < 2GiB"
[ "${DISK_GB}" -ge 20 ] || fail "Disk free < 5GiB"

echo "🧩 Kernel modules"
MODS="br_netfilter ip_vs ip_vs_rr ip_vs_wrr ip_vs_sh nf_conntrack"
for m in $MODS; do
  if ! lsmod | awk '{print $1}' | grep -qx "$m"; then
    sudo modprobe "$m" || warn "failed to modprobe $m"
  fi
done
lsmod | awk '{print $1}' | grep -qx br_netfilter || fail "br_netfilter not loaded"

echo "🔁 Swap"
if swapon --show | tail -n +2 | grep -q .; then
  swapon --show || true
  fail "swap is enabled"
fi

echo "🛡️  SELinux/AppArmor"
if command -v getenforce >/dev/null 2>&1; then
  SE=$(getenforce || true)
  echo "   SELinux: $SE"
  [ "$SE" != "Enforcing" ] || fail "SELinux is Enforcing"
fi
if command -v aa-status >/dev/null 2>&1; then
  aa-status || true
fi

echo "⏱️  Time sync"
if command -v timedatectl >/dev/null 2>&1; then
  NTPSYNC=$(timedatectl show -p NTPSynchronized --value 2>/dev/null || echo "")
  if [ "$NTPSYNC" = "no" ]; then
    warn "NTP not synchronized"
  fi
fi

if [ -n "$VIP_INTERFACE" ] && [ -n "$VIP_ADDRESS" ]; then
  echo "🌐 VIP config"
  ip link show "$VIP_INTERFACE" >/dev/null 2>&1 || fail "VIP interface not found: $VIP_INTERFACE"
  if ip addr | grep -w "$VIP_ADDRESS" >/dev/null 2>&1; then
    warn "VIP already present on this node: $VIP_ADDRESS"
  fi
fi

if [ -n "$PEERS" ]; then
  echo "🔌 Basic network reachability"
  for p in $PEERS; do
    ping -c 1 -W 2 "$p" >/dev/null 2>&1 || warn "ping failed to $p"
  done
fi

echo "✅ Pre-flight checks passed"
