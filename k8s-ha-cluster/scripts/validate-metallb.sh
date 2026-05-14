#!/usr/bin/env bash
set -Eeuo pipefail

KUBECONFIG_PATH="${KUBECONFIG_PATH:-$HOME/.kube/config}"

fail() {
  echo "❌ $*" >&2
  exit 1
}

export KUBECONFIG="$KUBECONFIG_PATH"

echo "📋 Cluster readiness"
kubectl get nodes -o wide
NOT_READY=$(kubectl get nodes --no-headers | awk '$2!="Ready" {print $1}' | tr '\n' ' ')
if [ -n "$NOT_READY" ]; then
  fail "nodes not Ready: $NOT_READY"
fi

echo "🧩 Control plane components"
kubectl get pods -n kube-system -o wide | egrep 'kube-apiserver|kube-controller-manager|kube-scheduler|etcd|coredns|kube-proxy' || true

echo "🔀 kube-proxy"
kubectl -n kube-system get ds kube-proxy >/dev/null 2>&1 || fail "kube-proxy daemonset missing"
kubectl -n kube-system rollout status ds/kube-proxy --timeout=180s

echo "🔩 MetalLB"
kubectl get ns metallb-system >/dev/null 2>&1 || fail "metallb-system namespace missing"
kubectl -n metallb-system get pods -o wide
kubectl -n metallb-system get ipaddresspool,l2advertisement,bgppeer 2>/dev/null || true

echo "✅ MetalLB validation checks completed"

