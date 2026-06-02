
#!/bin/bash
set -Eeuo pipefail

# ✅ Variables
VIP="192.168.1.12"
VIP_DOMAIN="rifcloud.fantastickim.dev"
NODE_IP="192.168.1.8"
NODE_NAME="k8s-ha-cp-1"
CP2_IP="192.168.1.9"
POD_CIDR="10.244.0.0/16"
SERVICE_CIDR="10.96.0.0/12"
K8S_VERSION="v1.35.0"

echo "🚀 Initializing Kubernetes HA Cluster"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "VIP: ${VIP} (${VIP_DOMAIN})"
echo "Node: ${NODE_NAME} @ ${NODE_IP}"
echo "Pod CIDR: ${POD_CIDR}"
echo "Service CIDR: ${SERVICE_CIDR}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# ✅ Pre-flight checks
echo ""
echo "🔍 Running pre-flight checks..."

# Check VIP is not already assigned
if ip addr show | grep -q "${VIP}"; then
    echo "⚠️  VIP ${VIP} is already assigned on this node"
    echo "   This is expected if kube-vip is running"
fi

# Check DNS resolution
echo "🔍 Testing DNS resolution..."
if command -v nslookup >/dev/null 2>&1; then
    RESOLVED_IP=$(nslookup "${VIP_DOMAIN}" 2>/dev/null | awk '/^Address: /{print $2; exit}' || true)
    if [ -n "$RESOLVED_IP" ]; then
        echo "   ${VIP_DOMAIN} resolves to ${RESOLVED_IP}"
        if [ "$RESOLVED_IP" != "$VIP" ]; then
            echo "⚠️  DNS resolves to different IP than VIP!"
        fi
    else
        echo "⚠️  ${VIP_DOMAIN} does not resolve (using VIP directly)"
    fi
fi

# Check ports are free
PORTS=(6443 2379 2380 10250 10251 10252)
PORT_CONFLICTS=false

for port in "${PORTS[@]}"; do
    if sudo netstat -tlnp 2>/dev/null | grep -q ":${port} "; then
        echo "❌ Port ${port} is already in use"
        sudo netstat -tlnp | grep ":${port} " || true
        PORT_CONFLICTS=true
    fi
done

if [ "$PORT_CONFLICTS" = true ]; then
    echo ""
    echo "❌ Port conflicts detected. Clean up required:"
    echo "   sudo systemctl stop kubelet"
    echo "   sudo kubeadm reset -f"
    exit 1
fi

# Check containerd
echo "🔍 Checking container runtime..."
if ! sudo systemctl is-active --quiet containerd; then
    echo "❌ containerd is not running"
    echo "   sudo systemctl start containerd"
    exit 1
fi

# Verify containerd config
if ! sudo grep -q "SystemdCgroup = true" /etc/containerd/config.toml 2>/dev/null; then
    echo "⚠️  containerd may not be configured for systemd cgroup"
    echo "   This could cause issues. Run prerequisite.sh first."
fi

echo "✅ Pre-flight checks passed"

# ✅ Initialize cluster with CORRECT control-plane-endpoint
echo ""
echo "🚀 Initializing cluster..."
echo "   Using VIP as control-plane-endpoint: ${VIP}:6443"

sudo kubeadm init \
  --control-plane-endpoint "${NODE_IP}:6443" \
  --apiserver-cert-extra-sans="${VIP_DOMAIN},${VIP},${NODE_IP},${CP2_IP}" \
  --upload-certs \
  --pod-network-cidr="${POD_CIDR}" \
  --service-cidr="${SERVICE_CIDR}" \
  --kubernetes-version="${K8S_VERSION}" \
  --v=5

# ✅ Configure kubectl
echo ""
echo "🔧 Configuring kubectl..."
mkdir -p $HOME/.kube
sudo cp -f /etc/kubernetes/admin.conf $HOME/.kube/config
sudo chown $(id -u):$(id -g) $HOME/.kube/config

# Set KUBECONFIG
export KUBECONFIG=$HOME/.kube/config

# ✅ Wait for API server to be ready
echo ""
echo "⏳ Waiting for API server to be ready..."
RETRY_COUNT=0
MAX_RETRIES=30

while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
    if kubectl cluster-info >/dev/null 2>&1; then
        echo "✅ API server is ready"
        break
    fi
    
    RETRY_COUNT=$((RETRY_COUNT + 1))
    echo "   Attempt ${RETRY_COUNT}/${MAX_RETRIES}: Waiting for API server..."
    sleep 2
    
    if [ $RETRY_COUNT -eq $MAX_RETRIES ]; then
        echo "❌ API server did not become ready in time"
        echo "📋 Checking kubelet status:"
        sudo systemctl status kubelet --no-pager || true
        echo "📋 Checking API server logs:"
        sudo journalctl -u kubelet -n 50 --no-pager || true
        exit 1
    fi
done

# ✅ Verify cluster
echo ""
echo "🔍 Verifying cluster..."

echo "📋 Cluster Info:"
kubectl cluster-info

echo ""
echo "📋 Nodes:"
kubectl get nodes

echo ""
echo "📋 System Pods:"
kubectl get pods -A

# ✅ Save join commands
echo ""
echo "💾 Saving join commands..."

# Get certificate key
CERT_KEY=$(sudo kubeadm init phase upload-certs --upload-certs 2>/dev/null | tail -1)

# Generate join command for control plane
JOIN_CMD=$(kubeadm token create --print-join-command)

# Save control plane join command
sudo bash -c "cat > /tmp/join-control-plane.sh" << EOF
#!/bin/bash

${JOIN_CMD} \\
  --control-plane \\
  --certificate-key ${CERT_KEY} \\
  --apiserver-advertise-address=${CP2_IP}
EOF

# Set proper permissions
sudo chmod +x /tmp/join-control-plane.sh
sudo chown $(id -u):$(id -g) /tmp/join-control-plane.sh

# Save worker join command
sudo bash -c "cat > /tmp/join-worker.sh" << EOF
#!/bin/bash
set -Eeuo pipefail

${JOIN_CMD}
EOF

# Set proper permissions
sudo chmod +x /tmp/join-worker.sh
sudo chown $(id -u):$(id -g) /tmp/join-worker.sh

echo ""
echo "✅ Cluster initialization complete!"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "📋 Join Commands Saved:"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Control Plane: /tmp/join-control-plane.sh"
echo "Worker Node:   /tmp/join-worker.sh"
echo ""
echo "📋 Certificate Key (valid for 2 hours):"
echo "${CERT_KEY}"
echo ""
echo "📋 Next Steps:"
echo "   1. Install CNI: kubectl apply -f <cni-manifest>"
echo "   2. Join CP2: scp /tmp/join-control-plane.sh user@${CP2_IP}:/tmp/"
echo "   3. On CP2: sudo bash /tmp/join-control-plane.sh"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"