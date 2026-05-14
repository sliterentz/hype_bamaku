#!/usr/bin/env bash
set -Eeuo pipefail

JOIN_FILE="${JOIN_FILE:-/tmp/k8s-join-command.txt}"
KUBECONFIG_PATH="${KUBECONFIG_PATH:-$HOME/.kube/config}"

fail() {
  echo "❌ $*" >&2
  exit 1
}

echo "🔑 Validating join artifacts on $(hostname)"

echo "📄 kubeconfig"
[ -f "$KUBECONFIG_PATH" ] || fail "kubeconfig not found: $KUBECONFIG_PATH"
export KUBECONFIG="$KUBECONFIG_PATH"
kubectl version --short >/dev/null 2>&1 || fail "kubectl cannot access cluster using kubeconfig"
SERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null || echo "")
echo "   server: $SERVER"

echo "🔐 Certificates"
[ -f /etc/kubernetes/pki/ca.crt ] || fail "missing /etc/kubernetes/pki/ca.crt"
[ -f /etc/kubernetes/pki/ca.key ] || echo "⚠️  /etc/kubernetes/pki/ca.key not present (may be expected)"
openssl x509 -in /etc/kubernetes/pki/ca.crt -noout -subject -dates >/dev/null 2>&1 || fail "invalid ca.crt"

echo "🎫 Join command"
[ -f "$JOIN_FILE" ] || fail "join command file not found: $JOIN_FILE"
JOIN_CMD=$(sudo cat "$JOIN_FILE" | tr -d '\r')
echo "$JOIN_CMD" | grep -q "kubeadm join" || fail "join command does not contain kubeadm join"
echo "$JOIN_CMD" | grep -q "--token" || fail "join command missing --token"
echo "$JOIN_CMD" | grep -q "--discovery-token-ca-cert-hash" || fail "join command missing CA hash"
echo "$JOIN_CMD" | grep -q "--certificate-key" || fail "join command missing certificate key"

TOKEN=$(echo "$JOIN_CMD" | sed -n 's/.*--token \([^ ]*\).*/\1/p')
if [ -n "$TOKEN" ]; then
  if ! echo "$TOKEN" | grep -qE '^[a-z0-9]{6}\.[a-z0-9]{16}$'; then
    fail "token format invalid: $TOKEN"
  fi
fi

echo "✅ Join artifacts validation passed"

