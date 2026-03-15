#!/usr/bin/env bash
set -euo pipefail

NODE_IP="${NODE_IP:?NODE_IP wajib}"
API_ENDPOINT="${API_ENDPOINT:?API_ENDPOINT wajib (IP/VIP)}"
POD_CIDR="${POD_CIDR:-10.244.0.0/16}"
SERVICE_CIDR="${SERVICE_CIDR:-10.96.0.0/12}"
CLUSTER_DOMAIN="${CLUSTER_DOMAIN:-cluster.local}"

ADMIN_CONF="/etc/kubernetes/admin.conf"
MANIFEST_DIR="/etc/kubernetes/manifests"
ETCD_DIR="/var/lib/etcd"

# Check if already initialized
if [[ -f "$ADMIN_CONF" && -d "$MANIFEST_DIR" && -n "$(ls -A "$MANIFEST_DIR")" ]]; then
  echo "kubeadm already initialized; skipping init" >&2
  
  # Generate tokens again for idempotency (needed for join commands)
  # Note: This requires existing admin.conf to be valid
  JOIN_WORKER=$(sudo kubeadm token create --print-join-command)
  # Re-upload certs to get a fresh key
  CERT_KEY=$(sudo kubeadm init phase upload-certs --upload-certs | tail -n 1)
  JOIN_CONTROL_PLANE="${JOIN_WORKER} --control-plane --certificate-key ${CERT_KEY}"
  KUBECONFIG_B64=$(sudo base64 -w0 /etc/kubernetes/admin.conf)

  jq -n \
    --arg joinWorker "${JOIN_WORKER}" \
    --arg joinControlPlane "${JOIN_CONTROL_PLANE}" \
    --arg kubeconfigB64 "${KUBECONFIG_B64}" \
    '{joinWorker:$joinWorker, joinControlPlane:$joinControlPlane, kubeconfigB64:$kubeconfigB64}'
    
  exit 0
fi

# Optional rebuild guard: only if REBUILD=true do reset
if [[ "${REBUILD:-false}" == "true" ]]; then
  sudo kubeadm reset -f || true
  sudo systemctl stop kubelet || true
  sudo rm -rf /etc/kubernetes/pki "$MANIFEST_DIR" "$ETCD_DIR"
fi

sudo kubeadm init \
  --apiserver-advertise-address "${NODE_IP}" \
  --control-plane-endpoint "${API_ENDPOINT}:6443" \
  --pod-network-cidr "${POD_CIDR}" \
  --service-cidr "${SERVICE_CIDR}" \
  --service-dns-domain "${CLUSTER_DOMAIN}" \
  --upload-certs >&2

mkdir -p "$HOME/.kube"
sudo cp -f /etc/kubernetes/admin.conf "$HOME/.kube/config"
sudo chown "$(id -u):$(id -g)" "$HOME/.kube/config"

JOIN_WORKER=$(sudo kubeadm token create --print-join-command)
CERT_KEY=$(sudo kubeadm init phase upload-certs --upload-certs | tail -n 1)
JOIN_CONTROL_PLANE="${JOIN_WORKER} --control-plane --certificate-key ${CERT_KEY}"

KUBECONFIG_B64=$(sudo base64 -w0 /etc/kubernetes/admin.conf)

jq -n \
  --arg joinWorker "${JOIN_WORKER}" \
  --arg joinControlPlane "${JOIN_CONTROL_PLANE}" \
  --arg kubeconfigB64 "${KUBECONFIG_B64}" \
  '{joinWorker:$joinWorker, joinControlPlane:$joinControlPlane, kubeconfigB64:$kubeconfigB64}'
