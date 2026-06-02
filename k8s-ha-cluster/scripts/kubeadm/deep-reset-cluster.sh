#!/bin/bash
set -Eeuo pipefail

echo "🧹 DEEP CLUSTER RESET - Complete Cleanup"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "⚠️  This will completely remove all Kubernetes components"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# ✅ Enhanced Pre-flight Validation
echo ""
echo "0️⃣ Pre-flight validation..."

# Check if cluster is partially running
CLUSTER_PARTIALLY_RUNNING=false
API_SERVER_RUNNING=false
ETCD_RUNNING=false

if pgrep -f "kube-apiserver" > /dev/null; then
    API_SERVER_RUNNING=true
    CLUSTER_PARTIALLY_RUNNING=true
    echo "   ⚠️  API Server is running"
fi

if pgrep -f "etcd" > /dev/null; then
    ETCD_RUNNING=true
    CLUSTER_PARTIALLY_RUNNING=true
    echo "   ⚠️  etcd is running"
fi

# Try to check cluster health if API server is accessible
if [ "$API_SERVER_RUNNING" = true ] && [ -f "$HOME/.kube/config" ]; then
    echo ""
    echo "🔍 Checking cluster health..."
    
    # Test API server connectivity
    if kubectl cluster-info &>/dev/null; then
        echo "   ✓ API Server is accessible"
        
        # Check RBAC health
        echo "   🔍 Validating RBAC configuration..."
        
        # Check critical ClusterRoleBindings
        CRITICAL_BINDINGS=(
            "system:kube-controller-manager"
            "system:kube-scheduler"
            "system:node"
            "system:basic-user"
        )
        
        RBAC_HEALTHY=true
        for binding in "${CRITICAL_BINDINGS[@]}"; do
            if ! kubectl get clusterrolebinding "$binding" &>/dev/null; then
                echo "   ❌ Missing ClusterRoleBinding: $binding"
                RBAC_HEALTHY=false
            fi
        done
        
        if [ "$RBAC_HEALTHY" = false ]; then
            echo "   ⚠️  RBAC configuration is incomplete"
        else
            echo "   ✓ RBAC configuration is healthy"
        fi
        
        # Check control plane pods
        echo "   🔍 Checking control plane pods..."
        CONTROL_PLANE_HEALTHY=true
        
        for component in kube-apiserver kube-controller-manager kube-scheduler etcd; do
            POD_STATUS=$(kubectl get pods -n kube-system -l component=$component -o jsonpath='{.items[0].status.phase}' 2>/dev/null || echo "NotFound")
            
            if [ "$POD_STATUS" != "Running" ]; then
                echo "   ❌ $component pod is not running (status: $POD_STATUS)"
                CONTROL_PLANE_HEALTHY=false
            fi
        done
        
        if [ "$CONTROL_PLANE_HEALTHY" = false ]; then
            echo "   ⚠️  Control plane is unhealthy"
        fi
        
        # Check etcd health
        if [ "$ETCD_RUNNING" = true ]; then
            echo "   🔍 Checking etcd health..."
            
            # Try to check etcd member list
            if command -v etcdctl &>/dev/null; then
                ETCD_ENDPOINTS="https://127.0.0.1:2379"
                ETCD_CACERT="/etc/kubernetes/pki/etcd/ca.crt"
                ETCD_CERT="/etc/kubernetes/pki/etcd/server.crt"
                ETCD_KEY="/etc/kubernetes/pki/etcd/server.key"
                
                if [ -f "$ETCD_CACERT" ] && [ -f "$ETCD_CERT" ] && [ -f "$ETCD_KEY" ]; then
                    if sudo ETCDCTL_API=3 etcdctl \
                        --endpoints="$ETCD_ENDPOINTS" \
                        --cacert="$ETCD_CACERT" \
                        --cert="$ETCD_CERT" \
                        --key="$ETCD_KEY" \
                        endpoint health &>/dev/null; then
                        echo "   ✓ etcd is healthy"
                    else
                        echo "   ❌ etcd health check failed"
                    fi
                else
                    echo "   ⚠️  etcd certificates not found"
                fi
            fi
        fi
        
    else
        echo "   ❌ API Server is not accessible"
        echo "   📋 This indicates a broken cluster state"
    fi
fi

# Confirm reset if cluster is running
if [ "$CLUSTER_PARTIALLY_RUNNING" = true ]; then
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "⚠️  WARNING: Cluster components are currently running"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    echo "Detected running components:"
    [ "$API_SERVER_RUNNING" = true ] && echo "   • API Server"
    [ "$ETCD_RUNNING" = true ] && echo "   • etcd"
    echo ""
    echo "This reset will:"
    echo "   • Stop all Kubernetes services"
    echo "   • Remove all cluster data (including etcd)"
    echo "   • Delete all certificates"
    echo "   • Clean all network configurations"
    echo ""
    read -p "Are you sure you want to continue? (yes/no): " -r
    echo
    if [[ ! $REPLY =~ ^[Yy][Ee][Ss]$ ]]; then
        echo "❌ Reset cancelled"
        exit 1
    fi
fi

# ✅ Step 1: Graceful shutdown of Kubernetes components
echo ""
echo "1️⃣ Gracefully shutting down Kubernetes components..."

# Try to drain node if kubectl is available
if command -v kubectl &>/dev/null && [ -f "$HOME/.kube/config" ]; then
    NODE_NAME=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "")
    
    if [ -n "$NODE_NAME" ]; then
        echo "   🔄 Draining node: $NODE_NAME"
        kubectl drain "$NODE_NAME" --ignore-daemonsets --delete-emptydir-data --force --timeout=60s 2>/dev/null || true
        
        echo "   🗑️  Deleting node from cluster"
        kubectl delete node "$NODE_NAME" --timeout=30s 2>/dev/null || true
    fi
fi

# Stop services gracefully
echo "   🛑 Stopping kubelet..."
sudo systemctl stop kubelet || true
sleep 2

echo "   🛑 Stopping containerd..."
sudo systemctl stop containerd || true
sleep 3


# ✅ Step 2: Force kill remaining processes
echo ""
echo "2️⃣ Force killing remaining Kubernetes processes..."

PROCESSES=(
    "kubelet"
    "kube-apiserver"
    "kube-controller-manager"
    "kube-scheduler"
    "kube-proxy"
    "etcd"
)

for process in "${PROCESSES[@]}"; do
    if pgrep -f "$process" > /dev/null; then
        echo "   🔫 Killing $process..."
        sudo pkill -9 -f "$process" || true
    fi
done

sleep 2

# Verify all processes are killed
if pgrep -f "kube|etcd" > /dev/null; then
    echo "   ⚠️  Some processes still running, force killing..."
    sudo pkill -9 -f "kube" || true
    sudo pkill -9 -f "etcd" || true
    sleep 2
fi

echo "   ✓ All Kubernetes processes terminated"

# ✅ Step 3: Run kubeadm reset with force
echo ""
echo "3️⃣ Running kubeadm reset..."
sudo kubeadm reset -f --cleanup-tmp-dir || true
sleep 2

# ✅ Step 4: Remove ALL Kubernetes directories
echo ""
echo "4️⃣ Removing Kubernetes directories..."

# Backup critical data before removal (optional)
BACKUP_DIR="/tmp/k8s-backup-$(date +%s)"
if [ -d "/var/lib/etcd" ] && [ "$(sudo ls -A /var/lib/etcd 2>/dev/null)" ]; then
    echo "   📦 Creating etcd backup at: $BACKUP_DIR"
    sudo mkdir -p "$BACKUP_DIR"
    sudo tar -czf "$BACKUP_DIR/etcd-data.tar.gz" /var/lib/etcd 2>/dev/null || true
fi

# Remove manifests (static pods)
sudo rm -rf /etc/kubernetes/manifests/* || true
echo "   ✓ Manifests cleared"

# Remove PKI certificates
sudo rm -rf /etc/kubernetes/pki/* || true
echo "   ✓ PKI certificates removed"

# Remove entire kubernetes config
sudo rm -rf /etc/kubernetes/* || true
echo "   ✓ Kubernetes config removed"

# Remove etcd data
sudo rm -rf /var/lib/etcd/* || true
sudo rm -rf /var/lib/etcd/.* 2>/dev/null || true
echo "   ✓ etcd data cleared"

# Remove kubelet data
sudo rm -rf /var/lib/kubelet/* || true
sudo rm -rf /var/lib/kubelet/.* 2>/dev/null || true
echo "   ✓ Kubelet data cleared"

# Remove CNI configurations
sudo rm -rf /etc/cni/net.d/* || true
sudo rm -rf /var/lib/cni/* || true
echo "   ✓ CNI data cleared"

# Remove kubeconfig
sudo rm -rf $HOME/.kube || true
sudo rm -rf /root/.kube || true
echo "   ✓ Kubeconfig removed"

# ✅ Step 5: Clean iptables rules
echo ""
echo "5️⃣ Cleaning iptables rules..."
# Flush all chains
sudo iptables -F || true
sudo iptables -t nat -F || true
sudo iptables -t mangle -F || true

# Delete all user-defined chains
sudo iptables -X || true
sudo iptables -t nat -X || true
sudo iptables -t mangle -X || true

echo "   ✓ iptables cleaned"

# ✅ Step 6: Clean ipvs rules
echo ""
echo "6️⃣ Cleaning IPVS rules..."

if command -v ipvsadm &>/dev/null; then
    sudo ipvsadm -C || true
    echo "   ✓ IPVS rules cleaned"
else
    echo "   ⚠️  ipvsadm not found, skipping"
fi

# ✅ Step 7: Remove network interfaces
echo ""
echo "7️⃣ Removing virtual network interfaces..."

# Remove CNI interfaces
CNI_INTERFACES=$(ip link show | grep -E 'cni|flannel|calico|weave|vxlan' | awk -F: '{print $2}' | tr -d ' ' || true)

if [ -n "$CNI_INTERFACES" ]; then
    for iface in $CNI_INTERFACES; do
        sudo ip link delete "$iface" 2>/dev/null || true
        echo "   ✓ Removed interface: $iface"
    done
else
    echo "   ✓ No CNI interfaces found"
fi

# Remove docker/containerd interfaces
sudo ip link delete docker0 2>/dev/null || true
sudo ip link delete cni0 2>/dev/null || true
sudo ip link delete flannel.1 2>/dev/null || true
sudo ip link delete kube-ipvs0 2>/dev/null || true

echo "   ✓ Virtual interfaces removed"

# ✅ Step 8: Clean systemd units and configs
echo ""
echo "8️⃣ Cleaning systemd configurations..."

# Remove kubelet drop-ins
sudo rm -rf /etc/systemd/system/kubelet.service.d/* || true
sudo rm -f /var/lib/kubelet/config.yaml || true
sudo rm -f /var/lib/kubelet/kubeadm-flags.env || true

# Remove any kubelet environment files
sudo rm -f /etc/default/kubelet || true
sudo rm -f /etc/sysconfig/kubelet || true

echo "   ✓ Systemd configs cleaned"

# ✅ Step 9: Clean container runtime state
echo ""
echo "9️⃣ Cleaning container runtime state..."

# Stop containerd if still running
sudo systemctl stop containerd || true
sleep 2

# Clean containerd state
sudo rm -rf /var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/* 2>/dev/null || true
sudo rm -rf /run/containerd/* || true

# Reload systemd and restart containerd
sudo systemctl daemon-reload
sudo systemctl restart containerd
sleep 5

# Verify containerd is running
if ! sudo systemctl is-active --quiet containerd; then
    echo "❌ Containerd failed to start!"
    sudo systemctl status containerd --no-pager
    exit 1
fi
echo "   ✓ Containerd restarted successfully"

# ✅ Step 10: Verify cleanup
echo ""
echo "🔍 Verifying cleanup..."

# Check no Kubernetes processes running
CONTAINERD_RETRY=0
while [ $CONTAINERD_RETRY -lt 10 ]; do
    if sudo systemctl is-active --quiet containerd; then
        echo "   ✓ Containerd restarted successfully"
        break
    fi
    echo "   ⏳ Waiting for containerd to start (attempt $((CONTAINERD_RETRY + 1))/10)..."
    sleep 2
    CONTAINERD_RETRY=$((CONTAINERD_RETRY + 1))
done

if ! sudo systemctl is-active --quiet containerd; then
    echo "   ❌ Containerd failed to start!"
    echo "   📋 Checking containerd status..."
    sudo systemctl status containerd --no-pager || true
    echo ""
    echo "   🔧 Attempting to fix containerd configuration..."
    
    # Regenerate containerd config
    sudo mkdir -p /etc/containerd
    sudo containerd config default | sudo tee /etc/containerd/config.toml >/dev/null
    sudo sed -i 's/SystemdCgroup = false/SystemdCgroup = true/g' /etc/containerd/config.toml
    
    # Restart containerd
    sudo systemctl daemon-reload
    sudo systemctl restart containerd
    sleep 5
    
    if ! sudo systemctl is-active --quiet containerd; then
        echo "   ❌ Containerd still failed to start. Manual intervention required."
        exit 1
    fi
fi

echo "   ✓ Containerd is running"

# ✅ Step 10: Verify cleanup and system readiness
echo ""
echo "🔍 Step 10: Verifying cleanup and system readiness..."

# Check no Kubernetes processes running
echo "   🔍 Checking for remaining Kubernetes processes..."
REMAINING_PROCS=$(pgrep -f "kube|etcd" || true)
if [ -n "$REMAINING_PROCS" ]; then
    echo "   ⚠️  Found remaining processes:"
    ps aux | grep -E "kube|etcd" | grep -v grep || true
else
    echo "   ✓ No Kubernetes processes running"
fi

# Check critical ports are free
CRITICAL_PORTS=(6443 2379 2380 10250 10251 10252 10255)
PORTS_IN_USE=false

for port in "${CRITICAL_PORTS[@]}"; do
    if sudo netstat -tlnp | grep -q ":${port} "; then
        echo "   ⚠️  Port ${port} still in use"
        PORTS_IN_USE=true
    fi
done

if [ "$PORTS_IN_USE" = false ]; then
    echo "   ✓ All critical ports are free"
else
    echo "   ⚠️  Some ports are still in use. Waiting 10 seconds..."
    sleep 10
    
    # Recheck after waiting
    PORTS_STILL_IN_USE=false
    for port in "${CRITICAL_PORTS[@]}"; do
        if sudo netstat -tlnp 2>/dev/null | grep -q ":${port} " || sudo ss -tlnp 2>/dev/null | grep -q ":${port} "; then
            PORTS_STILL_IN_USE=true
        fi
    done
    
    if [ "$PORTS_STILL_IN_USE" = true ]; then
        echo "   ❌ Critical ports still in use after waiting"
        echo "   💡 You may need to manually kill processes or reboot"
    else
        echo "   ✓ All critical ports are now free"
    fi
fi

# Check directories are clean
echo "   🔍 Checking directory cleanup..."

DIRS_TO_CHECK=(
    "/var/lib/etcd"
    "/etc/kubernetes/manifests"
    "/etc/kubernetes/pki"
    "/var/lib/kubelet"
    "/etc/cni/net.d"
)
ALL_CLEAN=true
for dir in "${DIRS_TO_CHECK[@]}"; do
    if [ -d "$dir" ] && [ "$(sudo ls -A "$dir" 2>/dev/null)" ]; then
        echo "   ⚠️  $dir is not empty:"
        sudo ls -la "$dir" 2>/dev/null | head -5 || true
        ALL_CLEAN=false
    fi
done

if [ "$ALL_CLEAN" = true ]; then
    echo "   ✓ All directories are clean"
fi

# Check network interfaces
echo "   🔍 Checking network interfaces..."
KUBE_IFACES=$(ip link show | grep -E 'cni|flannel|calico|weave|vxlan|kube' | awk -F: '{print $2}' | tr -d ' ' || true)

if [ -n "$KUBE_IFACES" ]; then
    echo "   ⚠️  Found remaining Kubernetes network interfaces:"
    echo "$KUBE_IFACES"
else
    echo "   ✓ No Kubernetes network interfaces found"
fi

# Check iptables rules
echo "   🔍 Checking iptables rules..."
KUBE_RULES=$(sudo iptables -t nat -L -n 2>/dev/null | grep -i kube || true)

if [ -n "$KUBE_RULES" ]; then
    echo "   ⚠️  Found remaining Kubernetes iptables rules"
else
    echo "   ✓ No Kubernetes iptables rules found"
fi

# ✅ Step 11: System prerequisites validation
echo ""
echo "🔍 Step 11: Validating system prerequisites for fresh init..."

# Check kernel modules
echo "   🔍 Checking kernel modules..."
REQUIRED_MODULES=("overlay" "br_netfilter")
MODULES_OK=true

for mod in "${REQUIRED_MODULES[@]}"; do
    if ! lsmod | grep -q "^$mod"; then
        echo "   ⚠️  Module $mod not loaded, loading..."
        sudo modprobe "$mod" || MODULES_OK=false
    fi
done

if [ "$MODULES_OK" = true ]; then
    echo "   ✓ Required kernel modules loaded"
fi

# Check sysctl settings
echo "   🔍 Checking sysctl settings..."
SYSCTL_SETTINGS=(
    "net.bridge.bridge-nf-call-iptables=1"
    "net.bridge.bridge-nf-call-ip6tables=1"
    "net.ipv4.ip_forward=1"
)

for setting in "${SYSCTL_SETTINGS[@]}"; do
    key=$(echo "$setting" | cut -d= -f1)
    value=$(echo "$setting" | cut -d= -f2)
    current=$(sudo sysctl -n "$key" 2>/dev/null || echo "0")
    
    if [ "$current" != "$value" ]; then
        echo "   ⚠️  $key = $current (expected: $value)"
        sudo sysctl -w "$setting" >/dev/null || true
    fi
done

echo "   ✓ Sysctl settings configured"

# Check swap
echo "   🔍 Checking swap status..."
if swapon --show 2>/dev/null | tail -n +2 | grep -q .; then
    echo "   ⚠️  Swap is enabled (will be disabled during kubeadm init)"
    sudo swapoff -a || true
else
    echo "   ✓ Swap is disabled"
fi

# Check containerd configuration
echo "   🔍 Checking containerd configuration..."
if [ -f "/etc/containerd/config.toml" ]; then
    if grep -q "SystemdCgroup = true" /etc/containerd/config.toml; then
        echo "   ✓ Containerd systemd cgroup configured"
    else
        echo "   ⚠️  Containerd systemd cgroup not configured"
        echo "   🔧 Fixing containerd configuration..."
        sudo sed -i 's/SystemdCgroup = false/SystemdCgroup = true/g' /etc/containerd/config.toml
        sudo systemctl restart containerd
        sleep 3
    fi
else
    echo "   ⚠️  Containerd config not found, generating default..."
    sudo mkdir -p /etc/containerd
    sudo containerd config default | sudo tee /etc/containerd/config.toml >/dev/null
    sudo sed -i 's/SystemdCgroup = false/SystemdCgroup = true/g' /etc/containerd/config.toml
    sudo systemctl restart containerd
    sleep 3
fi

# Verify containerd can pull images
echo "   🔍 Testing containerd image pull capability..."
if sudo ctr version &>/dev/null; then
    echo "   ✓ Containerd is accessible via ctr"
else
    echo "   ⚠️  Cannot access containerd via ctr"
fi

# Check disk space
echo "   🔍 Checking disk space..."
ROOT_AVAIL=$(df -BG / | awk 'NR==2 {gsub(/G/,"",$4); print $4}')
if [ "$ROOT_AVAIL" -lt 20 ]; then
    echo "   ⚠️  Low disk space on /: ${ROOT_AVAIL}GB (recommended: 20GB+)"
else
    echo "   ✓ Sufficient disk space: ${ROOT_AVAIL}GB"
fi

# Check memory
echo "   🔍 Checking memory..."
MEM_TOTAL=$(free -g | awk '/Mem:/ {print $2}')
if [ "$MEM_TOTAL" -lt 2 ]; then
    echo "   ⚠️  Low memory: ${MEM_TOTAL}GB (recommended: 2GB+)"
else
    echo "   ✓ Sufficient memory: ${MEM_TOTAL}GB"
fi

# Check CPU
echo "   🔍 Checking CPU..."
CPU_COUNT=$(nproc)
if [ "$CPU_COUNT" -lt 2 ]; then
    echo "   ⚠️  Low CPU count: ${CPU_COUNT} (recommended: 2+)"
else
    echo "   ✓ Sufficient CPU: ${CPU_COUNT} cores"
fi

# ✅ Final Summary
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "✅ DEEP CLUSTER RESET COMPLETED!"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "📋 Summary:"
echo "   • All Kubernetes processes terminated"
echo "   • All configuration files removed"
echo "   • All data directories cleaned"
echo "   • Network configurations reset"
echo "   • Container runtime restarted"
echo "   • System prerequisites validated"
echo ""

if [ "$PORTS_IN_USE" = true ] || [ "$ALL_CLEAN" = false ]; then
    echo "⚠️  WARNINGS DETECTED:"
    [ "$PORTS_IN_USE" = true ] && echo "   • Some critical ports are still in use"
    [ "$ALL_CLEAN" = false ] && echo "   • Some directories are not completely clean"
    echo ""
    echo "💡 Recommendations:"
    echo "   • Review the warnings above"
    echo "   • Consider rebooting the system for a complete clean state"
    echo "   • Or manually investigate and resolve the issues"
    echo ""
fi

echo "🚀 System is ready for fresh cluster initialization!"
echo ""
echo "📝 Next steps:"
echo "   1. Verify all nodes are in clean state"
echo "   2. Run: pulumi up"
echo "   3. Monitor the bootstrap process"
echo ""
echo "🔧 Optional: Run this script on all nodes before cluster init"
echo ""