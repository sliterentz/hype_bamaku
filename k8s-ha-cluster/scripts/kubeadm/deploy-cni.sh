#!/bin/bash
set -e

POD_CIDR="10.244.0.0/16"
CALICO_VERSION="v3.31.5"
TIMEOUT="-300"

echo "🌐 Installing Calico CNI"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Pod CIDR: ${POD_CIDR}"
echo "Calico Version: ${CALICO_VERSION}"
echo "Timeout: ${TIMEOUT}s"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# ✅ Pre-flight checks
echo ""
echo "🔍 Pre-flight checks..."

# Check kubectl access
if ! kubectl cluster-info &>/dev/null; then
    echo "❌ Cannot connect to Kubernetes cluster"
    echo "   Ensure KUBECONFIG is set or ~/.kube/config exists"
    exit 1
fi

# ✅ Check and fix DNS configuration
echo ""
echo "🔧 Checking DNS configuration..."

# Check if IPv6 is enabled but not properly configured
if ip -6 addr show | grep -q "inet6" && ! ip -6 route | grep -q "default"; then
    echo "⚠️  IPv6 detected but no default route"
    echo "   Disabling IPv6 for Calico compatibility..."
    
    # Disable IPv6 temporarily
    sudo sysctl -w net.ipv6.conf.all.disable_ipv6=1
    sudo sysctl -w net.ipv6.conf.default.disable_ipv6=1
fi

# Verify localhost resolution
if ! getent hosts localhost | grep -q "127.0.0.1"; then
    echo "⚠️  localhost not resolving to 127.0.0.1"
    echo "   Adding localhost entry to /etc/hosts..."
    
    if ! grep -q "^127.0.0.1.*localhost" /etc/hosts; then
        echo "127.0.0.1 localhost localhost.localdomain" | sudo tee -a /etc/hosts
    fi
fi

echo "✅ DNS configuration checked"

# ✅ Check kernel modules
echo ""
echo "🧩 Checking kernel modules..."
REQUIRED_MODULES=(
    "ip_tables"
    "ip6_tables"
    "iptable_filter"
    "iptable_nat"
    "xt_set"
    "ip_set"
    "ip_set_hash_ip"
    "ip_set_hash_net"
    "xt_mark"
    "xt_multiport"
    "xt_rpfilter"
    "xt_sctp"
    "xt_tcpudp"
)

for module in "${REQUIRED_MODULES[@]}"; do
    if ! lsmod | grep -q "^${module}"; then
        echo "   Loading module: ${module}"
        sudo modprobe "${module}" 2>/dev/null || echo "   ⚠️  Failed to load ${module}"
    fi
done

# Verify critical modules
if ! lsmod | grep -q "ip_tables"; then
    echo "❌ Critical module ip_tables not loaded"
    exit 1
fi

echo "✅ Kernel modules loaded"

# Check if CNI already installed
# ✅ Check existing Calico installation
if kubectl get pods -n kube-system -l k8s-app=calico-node &>/dev/null; then
    EXISTING_PODS=$(kubectl get pods -n kube-system -l k8s-app=calico-node --no-headers 2>/dev/null | wc -l)
    if [ "${EXISTING_PODS}" -gt 0 ]; then
        echo ""
        echo "⚠️  Calico appears to be already installed (${EXISTING_PODS} pods found)"
        echo ""
        echo "📋 Current Calico status:"
        kubectl get pods -n kube-system -l k8s-app=calico-node -o wide
        echo ""
        
        # Check for CrashLoopBackOff
        CRASH_PODS=$(kubectl get pods -n kube-system -l k8s-app=calico-node -o jsonpath='{.items[?(@.status.containerStatuses[0].state.waiting.reason=="CrashLoopBackOff")].metadata.name}')
        
        if [ -n "${CRASH_PODS}" ]; then
            echo "❌ Detected CrashLoopBackOff pods. Reinstalling..."
            
            # Show logs before cleanup
            for pod in ${CRASH_PODS}; do
                echo ""
                echo "📋 Logs from ${pod}:"
                kubectl logs ${pod} -n kube-system --tail=30 || true
            done
            
            # Force cleanup
            echo ""
            echo "🗑️  Removing existing Calico installation..."
            kubectl delete -f https://raw.githubusercontent.com/projectcalico/calico/${CALICO_VERSION}/manifests/calico.yaml --ignore-not-found=true --wait=false
            
            # Wait for cleanup
            echo "⏳ Waiting for cleanup..."
            sleep 15
            
            # Force delete stuck pods
            kubectl delete pods -n kube-system -l k8s-app=calico-node --force --grace-period=0 2>/dev/null || true
            sleep 5
        else
            read -p "Do you want to reinstall? (yes/no): " -r
            if [[ ! $REPLY =~ ^[Yy][Ee][Ss]$ ]]; then
                echo "ℹ️  Skipping installation"
                exit 0
            fi
            
            kubectl delete -f https://raw.githubusercontent.com/projectcalico/calico/${CALICO_VERSION}/manifests/calico.yaml --ignore-not-found=true
            sleep 10
        fi
    fi
fi

# Check node status
echo ""
echo "📋 Current node status:"
kubectl get nodes -o wide

# Download Calico manifest
curl -O https://raw.githubusercontent.com/projectcalico/calico/v3.31.5/manifests/calico.yaml

# Update pod CIDR
sed -i "s|# - name: CALICO_IPV4POOL_CIDR|- name: CALICO_IPV4POOL_CIDR|g" calico.yaml
sed -i "s|#   value: \"192.168.0.0/16\"|  value: \"${POD_CIDR}\"|g" calico.yaml

# Apply manifest
kubectl apply -f calico.yaml

echo "✅ Calico CNI installed"
echo ""
echo "⏳ Waiting for Calico pods to be ready..."
kubectl wait --for=condition=ready pod -l k8s-app=calico-node -n kube-system --timeout=300s

echo "✅ Calico is ready"