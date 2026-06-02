#!/bin/bash
set -e

CP_IP_START="192.168.1.8"
CP_IP_END="192.168.1.9"
NODE_NAME_CP1="k8s-ha-cp-1"
NODE_NAME_CP2="k8s-ha-cp-2"
VIP_DOMAIN="rifcloud.fantastickim.dev"
VIP="192.168.1.12"
POD_CIDR="10.244.0.0/16"
POD_SERVICE_CIDR="10.96.0.0/12"
K8S_VERSION="v1.35.0"

echo "🚀 Initializing HA Kubernetes Control Plane"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "VIP Domain:        $VIP_DOMAIN"
echo "VIP Address:       $VIP"
echo "Pod CIDR:          $POD_CIDR"
echo "Master Node:       $NODE_NAME_CP1 ($CP_IP_START)"
echo "Secondary Node:    $NODE_NAME_CP2 ($CP_IP_END)"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# ✅ Validasi parameter
if [ -z "$CP_IP_START" ] || [ -z "$CP_IP_END" ] || [ -z "$NODE_NAME_CP1" ] || [ -z "$NODE_NAME_CP2" ]; then
    echo "❌ Missing required parameters!"
    echo "Usage: $0 <CP_IP_START> <CP_IP_END> <NODE_NAME_CP1> <NODE_NAME_CP2> <VIP_DOMAIN> <VIP> <POD_CIDR> <POD_SERVICE_CIDR>"
    exit 1
fi

echo ""
echo "📁 Creating temporary directories..."
sudo mkdir -p /tmp/etcd-certs/${NODE_NAME_CP1} /tmp/etcd-certs/${NODE_NAME_CP2}

HOSTS=(${CP_IP_START} ${CP_IP_END})
NAMES=(${NODE_NAME_CP1} ${NODE_NAME_CP2})

for i in "${!HOSTS[@]}"; do
    HOST=${HOSTS[$i]}
    NAME=${NAMES[$i]}

    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "📝 Configuring node: ${NAME} (${HOST})"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

    # ✅ Configure kubelet as service manager for etcd
    echo ""
    echo "🔧 Configuring kubelet as service manager for etcd..."
    
    # Create kubelet systemd drop-in directory
    sudo mkdir -p /etc/systemd/system/kubelet.service.d
    
    # Create kubelet configuration for etcd
    cat << 'EOF_KUBELET' | sudo tee /etc/systemd/system/kubelet.service.d/20-etcd-service-manager.conf > /dev/null
[Service]
ExecStart=
# Replace "systemd" with the cgroup driver of your container runtime. The default value in the kubelet is "cgroupfs".
# Replace the value of "--container-runtime-endpoint" for a different container runtime if needed.
ExecStart=/usr/bin/kubelet --address=127.0.0.1 --pod-manifest-path=/etc/kubernetes/manifests --cgroup-driver=systemd --container-runtime-endpoint=unix:///var/run/containerd/containerd.sock
Restart=always
EOF_KUBELET

    echo "✅ Kubelet service manager configuration created"

    # ✅ Configure kubelet extra args for etcd
    echo ""
    echo "🔧 Creating kubelet configuration file..."
    
    sudo mkdir -p /var/lib/kubelet
    
    cat << EOF_KUBELET_CONFIG | sudo tee /var/lib/kubelet/config.yaml > /dev/null
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
# kubelet specific options here
authentication:
  anonymous:
    enabled: false
  webhook:
    enabled: false
authorization:
  mode: AlwaysAllow
cgroupDriver: systemd
clusterDomain: cluster.local
containerRuntimeEndpoint: unix:///var/run/containerd/containerd.sock
cpuManagerReconcilePeriod: 0s
evictionPressureTransitionPeriod: 0s
fileCheckFrequency: 0s
healthzBindAddress: 127.0.0.1
healthzPort: 10248
httpCheckFrequency: 0s
imageMinimumGCAge: 0s
logging:
  flushFrequency: 0
  options:
    json:
      infoBufferSize: "0"
  verbosity: 0
memorySwap: {}
nodeStatusReportFrequency: 0s
nodeStatusUpdateFrequency: 0s
rotateCertificates: true
runtimeRequestTimeout: 0s
shutdownGracePeriod: 0s
shutdownGracePeriodCriticalPods: 0s
staticPodPath: /etc/kubernetes/manifests
streamingConnectionIdleTimeout: 0s
syncFrequency: 0s
volumeStatsAggPeriod: 0s
EOF_KUBELET_CONFIG

    echo "✅ Kubelet configuration file created"

    # ✅ TAMBAHAN: Reload systemd and restart kubelet
    echo ""
    echo "🔄 Reloading systemd daemon and restarting kubelet..."
    sudo systemctl daemon-reload
    sudo systemctl enable kubelet
    sudo systemctl restart kubelet
    
    # Wait for kubelet to be ready
    echo "⏳ Waiting for kubelet to be ready..."
    for attempt in {1..30}; do
        if sudo systemctl is-active --quiet kubelet; then
            echo "✅ Kubelet is active and running"
            break
        fi
        if [ $attempt -eq 30 ]; then
            echo "❌ Kubelet failed to start within timeout"
            sudo systemctl status kubelet --no-pager || true
            exit 1
        fi
        echo "   Attempt $attempt/30: Waiting for kubelet..."
        sleep 2
    done

    # ✅ TAMBAHAN: Create etcd data directory with proper permissions
    echo ""
    echo "📁 Creating etcd data directory..."
    sudo mkdir -p /var/lib/etcd
    sudo chmod 700 /var/lib/etcd
    echo "✅ etcd data directory created with proper permissions"

    echo ""
    echo "📝 Generating kubeadm config for ${NAME} (${HOST})..."
    
    cat << EOFCONFIG | sudo tee /tmp/etcd-certs/${NAME}/kubeadm-config.yaml > /dev/null  
---
apiVersion: kubeadm.k8s.io/v1beta4
kind: InitConfiguration
localAPIEndpoint:
  advertiseAddress: ${HOST}
  bindPort: 6443
nodeRegistration:
  name: ${NAME}
  criSocket: unix:///var/run/containerd/containerd.sock
  imagePullPolicy: IfNotPresent
  taints: null
---
apiVersion: kubeadm.k8s.io/v1beta4
kind: ClusterConfiguration
kubernetesVersion: K8S_VERSION_PLACEHOLDER
controlPlaneEndpoint: "CP_IP_START_PLACEHOLDER:6443"
certificatesDir: /etc/kubernetes/pki
imageRepository: registry.k8s.io
networking:
  podSubnet: POD_CIDR_PLACEHOLDER
  serviceSubnet: POD_SERVICE_CIDR_PLACEHOLDER
apiServer:
  certSANs:
  - VIP_DOMAIN_PLACEHOLDER
  - VIP_PLACEHOLDER
  - CP_IP_START_PLACEHOLDER
  - NODE_NAME_PLACEHOLDER
  extraArgs:
  - name: advertise-address
    value: CP_IP_START_PLACEHOLDER
controllerManager:
  extraArgs:
  - name: bind-address
    value: "0.0.0.0"
scheduler:
  extraArgs:
  - name: bind-address
    value: "0.0.0.0"
etcd:
  local:
    dataDir: /var/lib/etcd
    serverCertSANs:
    - ${NAME}
    peerCertSANs:
    - ${NAME}
    extraArgs:
    - name: name
      value: ${NAME}
    - name: listen-peer-urls
      value: https://${HOST}:2380
    - name: listen-client-urls
      value: https://${HOST}:2379,https://127.0.0.1:2379
    - name: advertise-client-urls
      value: https://${HOST}:2379
    - name: initial-advertise-peer-urls
      value: https://${HOST}:2380
    - name: initial-cluster
      value: NODE_NAME_PLACEHOLDER=https://CP_IP_START_PLACEHOLDER:2380,NODE_NAME_CP2_PLACEHOLDER=https://CP_IP_END_PLACEHOLDER:2380
    - name: initial-cluster-state
      value: new
EOFCONFIG

    # ✅ PERBAIKAN: Verifikasi file tergenerate
    if [ ! -f "/tmp/etcd-certs/${NAME}/kubeadm-config.yaml" ]; then
        echo "❌ Failed to create kubeadm-config.yaml for ${HOST}"
        exit 1
    fi

    echo "✅ Template file created ($(wc -l < /tmp/etcd-certs/${NAME}/kubeadm-config.yaml) lines)"

    # ✅ PERBAIKAN: Replace placeholders dengan nama file yang benar
    echo "🔄 Replacing placeholders..."
    sudo sed -i "s|CP_IP_START_PLACEHOLDER|${CP_IP_START}|g" /tmp/etcd-certs/${NAME}/kubeadm-config.yaml
    sudo sed -i "s|CP_IP_END_PLACEHOLDER|${CP_IP_END}|g" /tmp/etcd-certs/${NAME}/kubeadm-config.yaml
    sudo sed -i "s|NODE_NAME_PLACEHOLDER|${NODE_NAME_CP1}|g" /tmp/etcd-certs/${NAME}/kubeadm-config.yaml
    sudo sed -i "s|NODE_NAME_CP2_PLACEHOLDER|${NODE_NAME_CP2}|g" /tmp/etcd-certs/${NAME}/kubeadm-config.yaml
    sudo sed -i "s|VIP_DOMAIN_PLACEHOLDER|${VIP_DOMAIN}|g" /tmp/etcd-certs/${NAME}/kubeadm-config.yaml
    sudo sed -i "s|VIP_PLACEHOLDER|${VIP}|g" /tmp/etcd-certs/${NAME}/kubeadm-config.yaml
    sudo sed -i "s|POD_CIDR_PLACEHOLDER|${POD_CIDR}|g" /tmp/etcd-certs/${NAME}/kubeadm-config.yaml
    sudo sed -i "s|POD_SERVICE_CIDR_PLACEHOLDER|${POD_SERVICE_CIDR}|g" /tmp/etcd-certs/${NAME}/kubeadm-config.yaml
    sudo sed -i "s|K8S_VERSION_PLACEHOLDER|${K8S_VERSION}|g" /tmp/etcd-certs/${NAME}/kubeadm-config.yaml
    
    # ✅ Verifikasi hasil replacement
    if grep -q "PLACEHOLDER" /tmp/etcd-certs/${NAME}/kubeadm-config.yaml; then
        echo "⚠️  Warning: Some placeholders were not replaced!"
        echo "Remaining placeholders:"
        grep "PLACEHOLDER" /tmp/etcd-certs/${NAME}/kubeadm-config.yaml || true
    fi
    
    echo ""
    echo "📋 Generated kubeadm configuration for ${NAME}:"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    cat /tmp/etcd-certs/${NAME}/kubeadm-config.yaml
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

    # ✅ Validate configuration
    echo ""
    echo "🔍 Validating kubeadm configuration..."
    if ! sudo kubeadm config validate --config /tmp/etcd-certs/${NAME}/kubeadm-config.yaml; then
        echo "❌ Configuration validation failed!"
        exit 1
    fi
    echo "✅ Configuration validation passed for ${NAME}"

    # ✅ Pre-flight checks dengan handling untuk port conflicts
    echo ""
    echo "🔍 Running pre-flight checks..."
    
    # Check if port 10250 is already in use
    if sudo netstat -tlnp | grep -q ":10250"; then
        echo "⚠️  Port 10250 is already in use (kubelet running)"
        echo "🔄 Stopping kubelet temporarily for pre-flight checks..."
        
        # Stop kubelet temporarily
        sudo systemctl stop kubelet || true
        sleep 3
        
        # Verify port is now free
        if sudo netstat -tlnp | grep -q ":10250"; then
            echo "❌ Port 10250 still in use after stopping kubelet"
            echo "📋 Process using port 10250:"
            sudo netstat -tlnp | grep ":10250" || true
            sudo lsof -i :10250 || true
            
            # Try to kill the process
            echo "🔄 Attempting to force kill process on port 10250..."
            PORT_PID=$(sudo lsof -t -i:10250 || true)
            if [ -n "$PORT_PID" ]; then
                sudo kill -9 $PORT_PID || true
                sleep 2
            fi
            
            # Final check
            if sudo netstat -tlnp | grep -q ":10250"; then
                echo "❌ Unable to free port 10250"
                exit 1
            fi
        fi
        
        echo "✅ Port 10250 is now free"
    fi
    
    # Check other critical ports
    CRITICAL_PORTS=(6443 2379 2380 10251 10252 10255)
    PORT_CONFLICTS=false
    
    for port in "${CRITICAL_PORTS[@]}"; do
        if sudo netstat -tlnp | grep -q ":${port}"; then
            echo "⚠️  Port ${port} is already in use"
            PORT_CONFLICTS=true
        fi
    done
    
    if [ "$PORT_CONFLICTS" = true ]; then
        echo ""
        echo "📋 Current port usage:"
        sudo netstat -tlnp | grep -E ":(6443|2379|2380|10250|10251|10252|10255)" || true
        echo ""
        echo "⚠️  Some ports are in use. Attempting cleanup..."
        
        # Stop all Kubernetes services
        echo "🛑 Stopping Kubernetes services..."
        sudo systemctl stop kubelet || true
        sudo systemctl stop containerd || true
        sleep 3
        
        # Kill any remaining processes
        echo "🔄 Cleaning up remaining processes..."
        sudo pkill -9 kubelet || true
        sudo pkill -9 kube-apiserver || true
        sudo pkill -9 kube-controller-manager || true
        sudo pkill -9 kube-scheduler || true
        sudo pkill -9 etcd || true
        sleep 2
        
        # Restart containerd
        echo "🔄 Restarting containerd..."
        sudo systemctl start containerd
        sleep 3
        
        # Verify ports are now free
        echo "🔍 Verifying ports are free..."
        STILL_BLOCKED=false
        for port in "${CRITICAL_PORTS[@]}"; do
            if sudo netstat -tlnp | grep -q ":${port}"; then
                echo "❌ Port ${port} is still in use"
                STILL_BLOCKED=true
            fi
        done
        
        if [ "$STILL_BLOCKED" = true ]; then
            echo "❌ Unable to free all required ports"
            echo "📋 Remaining port conflicts:"
            sudo netstat -tlnp | grep -E ":(6443|2379|2380|10250|10251|10252|10255)" || true
            exit 1
        fi
        
        echo "✅ All critical ports are now free"
    fi
    
    # Run pre-flight checks with proper error handling
    echo ""
    echo "🔍 Running kubeadm pre-flight checks..."
    
    PREFLIGHT_OUTPUT=$(sudo kubeadm init phase preflight --config /tmp/etcd-certs/${NAME}/kubeadm-config.yaml 2>&1) || PREFLIGHT_FAILED=true
    
    if [ "$PREFLIGHT_FAILED" = true ]; then
        echo "⚠️  Pre-flight checks encountered issues:"
        echo "$PREFLIGHT_OUTPUT"
        
        # Check for specific errors and handle them
        if echo "$PREFLIGHT_OUTPUT" | grep -q "Port 10250 is in use"; then
            echo ""
            echo "🔄 Handling port 10250 conflict..."
            
            # More aggressive cleanup
            sudo systemctl stop kubelet || true
            sudo pkill -9 kubelet || true
            sleep 3
            
            # Remove kubelet state
            sudo rm -rf /var/lib/kubelet/* || true
            
            # Retry pre-flight
            echo "🔄 Retrying pre-flight checks..."
            if ! sudo kubeadm init phase preflight --config /tmp/etcd-certs/${NAME}/kubeadm-config.yaml; then
                echo "❌ Pre-flight checks failed after cleanup"
                exit 1
            fi
        elif echo "$PREFLIGHT_OUTPUT" | grep -q "FileAvailable--etc-kubernetes-manifests-etcd.yaml"; then
            echo ""
            echo "🔄 Handling existing etcd manifest..."
            
            # Backup and remove existing manifest
            if [ -f /etc/kubernetes/manifests/etcd.yaml ]; then
                sudo mv /etc/kubernetes/manifests/etcd.yaml /tmp/etcd.yaml.backup.$(date +%s) || true
            fi
            
            # Retry pre-flight
            echo "🔄 Retrying pre-flight checks..."
            if ! sudo kubeadm init phase preflight --config /tmp/etcd-certs/${NAME}/kubeadm-config.yaml; then
                echo "❌ Pre-flight checks failed after cleanup"
                exit 1
            fi
        elif echo "$PREFLIGHT_OUTPUT" | grep -q "DirAvailable--var-lib-etcd"; then
            echo ""
            echo "🔄 Handling existing etcd data directory..."
            
            # Backup and clean etcd data
            if [ -d /var/lib/etcd ] && [ "$(sudo ls -A /var/lib/etcd)" ]; then
                echo "⚠️  etcd data directory is not empty"
                echo "📋 Creating backup..."
                sudo tar -czf /tmp/etcd-backup-$(date +%s).tar.gz /var/lib/etcd || true
                
                echo "🗑️  Cleaning etcd data directory..."
                sudo rm -rf /var/lib/etcd/* || true
            fi
            
            # Retry pre-flight
            echo "🔄 Retrying pre-flight checks..."
            if ! sudo kubeadm init phase preflight --config /tmp/etcd-certs/${NAME}/kubeadm-config.yaml; then
                echo "❌ Pre-flight checks failed after cleanup"
                exit 1
            fi
        else
            echo "❌ Pre-flight checks failed with unhandled error"
            exit 1
        fi
    fi
    
    echo "✅ Pre-flight checks passed for ${NAME}"
    
    # Restart kubelet if it was stopped
    echo ""
    echo "🔄 Ensuring kubelet is running..."
    sudo systemctl start kubelet || true
    sleep 2
    
    # Verify kubelet status
    if sudo systemctl is-active --quiet kubelet; then
        echo "✅ Kubelet is running"
    else
        echo "⚠️  Kubelet is not running (will be started during init)"
    fi
    
    echo ""
done

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "✅ All configurations generated and validated successfully!"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "📋 Summary:"
echo "   - Node 1: ${NODE_NAME_CP1} (${CP_IP_START})"
echo "   - Node 2: ${NODE_NAME_CP2} (${CP_IP_END})"
echo "   - VIP: ${VIP_DOMAIN} (${VIP})"
echo "   - Pod CIDR: ${POD_CIDR}"
echo "   - Service CIDR: ${POD_SERVICE_CIDR}"
echo ""
echo "🎯 Next steps:"
echo "   1. Run certificate generation script"
echo "   2. Deploy etcd cluster"
echo "   3. Initialize Kubernetes control plane"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"