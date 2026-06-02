
#!/bin/bash
set -Eeuo pipefail

VIP="192.168.1.12"
INTERFACE="eth0"  # Sesuaikan dengan interface Anda
VIP_DOMAIN="rifcloud.fantastickim.dev"

echo "🔧 Deploying kube-vip manifest (pre-init)..."

# ✅ Generate kube-vip manifest
sudo ctr image pull ghcr.io/kube-vip/kube-vip:v1.1.2

sudo ctr run --rm --net-host ghcr.io/kube-vip/kube-vip:v1.1.2 vip /kube-vip manifest pod \
  --interface ${INTERFACE} \
  --address ${VIP} \
  --controlplane \
  --services \
  --arp \
  --leaderElection | sudo tee /etc/kubernetes/manifests/kube-vip.yaml

echo "✅ kube-vip manifest created"
echo "📋 Manifest location: /etc/kubernetes/manifests/kube-vip.yaml"

# ✅ Verify manifest
echo ""
echo "📋 Manifest content:"
sudo cat /etc/kubernetes/manifests/kube-vip.yaml | grep -E "image:|address:|interface:"

# ✅ Wait for kube-vip pod to be running
kubectl get pods -n kube-system -o wide | grep kube-vip

# ✅ Check if VIP is on this node
ip a | grep ${VIP} || echo "VIP not on this node"
kubectl --server=https://${VIP}:6443 get nodes

# ✅ Update kubeadm-config with VIP endpoint
kubectl -n kube-system get configmap kubeadm-config -o yaml > /tmp/kubeadm-config.yaml
sed -i -E "s|^([[:space:]]*)controlPlaneEndpoint:.*|\\1controlPlaneEndpoint: \"${VIP_DOMAIN}:6443\"|g" /tmp/kubeadm-config.yaml
kubectl apply -f /tmp/kubeadm-config.yaml

# Verify update
echo "📋 Verifying ConfigMap update..."
CURRENT_ENDPOINT=$(kubectl -n kube-system get configmap kubeadm-config -o jsonpath='{.data.ClusterConfiguration}' | tr '\\r' '\\n' | grep -m1 controlPlaneEndpoint || true)
echo "   Current endpoint in ConfigMap: $CURRENT_ENDPOINT"

if ! echo "$CURRENT_ENDPOINT" | grep -q "${VIP_DOMAIN}:6443"; then
    echo "⚠️ Warning: ConfigMap update may not have applied correctly"
    echo "   Expected: ${VIP_DOMAIN}:6443"
    echo "   Got: $CURRENT_ENDPOINT"
fi

echo "🔄 Updating kubeconfig..."
export KUBECONFIG=$HOME/.kube/config
kubectl config set-cluster kubernetes --server=https://${VIP_DOMAIN}:6443
echo "✅ ConfigMap updated successfully"