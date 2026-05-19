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
REQUIRED_CERTS=(
  "/etc/kubernetes/pki/ca.crt"
  "/etc/kubernetes/pki/ca.key"
  "/etc/kubernetes/pki/etcd/ca.crt"
  "/etc/kubernetes/pki/etcd/ca.key"
  "/etc/kubernetes/pki/front-proxy-ca.crt"
  "/etc/kubernetes/pki/front-proxy-ca.key"
  "/etc/kubernetes/pki/sa.pub"
  "/etc/kubernetes/pki/sa.key"
)

check_mode() {
  local f="$1"
  local expected="$2"
  local actual=""
  actual=$(sudo stat -c '%a' "$f" 2>/dev/null || true)
  if [ -n "$actual" ] && [ "$actual" != "$expected" ]; then
    fail "invalid mode for $f (expected $expected got $actual)"
  fi
}

for f in "${REQUIRED_CERTS[@]}"; do
  sudo test -s "$f" || fail "missing/empty $f"
done

sudo openssl x509 -in /etc/kubernetes/pki/ca.crt -noout -subject -dates >/dev/null 2>&1 || fail "invalid ca.crt"
sudo openssl x509 -in /etc/kubernetes/pki/etcd/ca.crt -noout -subject -dates >/dev/null 2>&1 || fail "invalid etcd/ca.crt"
sudo openssl x509 -in /etc/kubernetes/pki/front-proxy-ca.crt -noout -subject -dates >/dev/null 2>&1 || fail "invalid front-proxy-ca.crt"

sudo openssl pkey -in /etc/kubernetes/pki/ca.key -noout >/dev/null 2>&1 || fail "invalid ca.key"
sudo openssl pkey -in /etc/kubernetes/pki/etcd/ca.key -noout >/dev/null 2>&1 || fail "invalid etcd/ca.key"
sudo openssl pkey -in /etc/kubernetes/pki/front-proxy-ca.key -noout >/dev/null 2>&1 || fail "invalid front-proxy-ca.key"
sudo openssl pkey -in /etc/kubernetes/pki/sa.key -noout >/dev/null 2>&1 || fail "invalid sa.key"

check_mode /etc/kubernetes/pki/ca.key 600
check_mode /etc/kubernetes/pki/etcd/ca.key 600
check_mode /etc/kubernetes/pki/front-proxy-ca.key 600
check_mode /etc/kubernetes/pki/sa.key 600

check_mode /etc/kubernetes/pki/ca.crt 644
check_mode /etc/kubernetes/pki/etcd/ca.crt 644
check_mode /etc/kubernetes/pki/front-proxy-ca.crt 644
check_mode /etc/kubernetes/pki/sa.pub 644

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
