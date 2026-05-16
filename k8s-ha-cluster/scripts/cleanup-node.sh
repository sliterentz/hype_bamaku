#!/bin/bash
# File: scripts/cleanup-node.sh

set -Eeuo pipefail

echo "🧹 Kubernetes Node Cleanup Script"
echo "=================================="
echo ""

# Confirm action
read -p "⚠️  This will remove ALL Kubernetes data from this node. Continue? (yes/no): " confirm
if [ "$confirm" != "yes" ]; then
    echo "❌ Cleanup cancelled"
    exit 0
fi

echo ""
echo "🛑 Stopping services..."
sudo systemctl stop kubelet || true
sudo systemctl stop containerd || true

echo "🔪 Killing processes..."
sudo pkill -9 kubelet || true
sudo pkill -9 etcd || true
sudo pkill -9 kube-apiserver || true
sudo pkill -9 kube-controller || true
sudo pkill -9 kube-scheduler || true

echo "🗑️  Removing directories..."
sudo rm -rf /etc/kubernetes/
sudo rm -rf /var/lib/kubelet/
sudo rm -rf /var/lib/etcd/
sudo rm -rf /var/lib/cni/
sudo rm -rf /etc/cni/
sudo rm -rf /var/lib/containerd/

echo "🔥 Cleaning iptables..."
sudo iptables -F && sudo iptables -t nat -F && sudo iptables -t mangle -F && sudo iptables -X || true

echo "🌐 Removing network interfaces..."
sudo ip link delete cni0 2>/dev/null || true
sudo ip link delete flannel.1 2>/dev/null || true

echo "🔪 Freeing ports..."
sudo fuser -k 2379/tcp 2>/dev/null || true
sudo fuser -k 2380/tcp 2>/dev/null || true
sudo fuser -k 6443/tcp 2>/dev/null || true
sudo fuser -k 10250/tcp 2>/dev/null || true

echo "🔄 Restarting containerd..."
sudo systemctl daemon-reload
sudo systemctl start containerd
sudo systemctl enable containerd

sleep 5

echo ""
echo "✅ Cleanup completed!"
echo ""
echo "📋 Port status:"
sudo ss -tulpn | grep -E ':(2379|2380|6443|10250)' || echo "All ports are free ✅"