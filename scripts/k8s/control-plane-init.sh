#!/usr/bin/env bash
set -euo pipefail

NODE_IP="${NODE_IP:?NODE_IP wajib}"
API_ENDPOINT="${API_ENDPOINT:?API_ENDPOINT wajib (IP/VIP)}"
POD_CIDR="${POD_CIDR:-10.244.0.0/16}"
SERVICE_CIDR="${SERVICE_CIDR:-10.96.0.0/12}"
CLUSTER_DOMAIN="${CLUSTER_DOMAIN:-cluster.local}"

sudo kubeadm init \
  --apiserver-advertise-address "${NODE_IP}" \
  --control-plane-endpoint "${API_ENDPOINT}:6443" \
  --pod-network-cidr "${POD_CIDR}" \
  --service-cidr "${SERVICE_CIDR}" \
  --service-dns-domain "${CLUSTER_DOMAIN}" \
  --upload-certs

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
