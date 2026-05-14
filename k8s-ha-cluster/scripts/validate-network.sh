#!/usr/bin/env bash
set -Eeuo pipefail

TARGETS="${TARGETS:-${1:-}}"
PORTS="${PORTS:-${2:-"6443 2379 2380 10250 10251 10252 10255"}}"
TIMEOUT_S="${TIMEOUT_S:-2}"

if [ -z "$TARGETS" ]; then
  echo "Usage: TARGETS='ip1 ip2' ./scripts/validate-network.sh" >&2
  exit 2
fi

check_port() {
  local host="$1"
  local port="$2"
  if command -v nc >/dev/null 2>&1; then
    nc -z -w "$TIMEOUT_S" "$host" "$port" >/dev/null 2>&1
    return $?
  fi
  if command -v timeout >/dev/null 2>&1; then
    timeout "$TIMEOUT_S" bash -c "</dev/tcp/$host/$port" >/dev/null 2>&1
    return $?
  fi
  bash -c "</dev/tcp/$host/$port" >/dev/null 2>&1
}

echo "🔌 Network port validation"
echo "Targets: $TARGETS"
echo "Ports:   $PORTS"

FAILS=0
for h in $TARGETS; do
  echo "\n== $h =="
  for p in $PORTS; do
    if check_port "$h" "$p"; then
      echo "✅ $h:$p"
    else
      echo "❌ $h:$p"
      FAILS=$((FAILS+1))
    fi
  done
done

if [ "$FAILS" -gt 0 ]; then
  echo "\n❌ Network validation failed: $FAILS port checks failed" >&2
  exit 1
fi

echo "\n✅ Network validation passed"

