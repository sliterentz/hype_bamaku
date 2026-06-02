
#!/bin/bash
set -Eeuo pipefail

# Trap untuk error handling
trap 'echo "❌ Error at line $LINENO. Exit code: $?"; exit 1' ERR

CP_IP_START=$1
CP_IP_END=$2
NODE_NAME_CP1=$3
NODE_NAME_CP2=$4

echo "📝 Creating etcd Static Pod Manifests"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Node 1: ${NODE_NAME_CP1} (${CP_IP_START})"
echo "Node 2: ${NODE_NAME_CP2} (${CP_IP_END})"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# ✅ Validasi parameter
if [ -z "$CP_IP_START" ] || [ -z "$CP_IP_END" ] || [ -z "$NODE_NAME_CP1" ] || [ -z "$NODE_NAME_CP2" ]; then
    echo "❌ Missing required parameters!"
    echo "Usage: $0 <CP_IP_START> <CP_IP_END> <NODE_NAME_CP1> <NODE_NAME_CP2>"
    exit 1
fi

# ✅ Validasi kubeadm tersedia
if ! command -v kubeadm &> /dev/null; then
    echo "❌ kubeadm not found. Please install kubeadm first."
    exit 1
fi

# ✅ Setup working directory
MANIFEST_DIR="/tmp/etcd-manifests"
CERT_DIR="/tmp/etcd-certs"

echo ""
echo "📁 Setting up manifest directory: ${MANIFEST_DIR}"
sudo rm -rf ${MANIFEST_DIR}
sudo mkdir -p ${MANIFEST_DIR}

# ✅ Validasi certificates sudah di-generate
if [ ! -d "${CERT_DIR}" ]; then
    echo "❌ Certificate directory not found: ${CERT_DIR}"
    echo "   Please run generate-etcd-certs.sh first"
    exit 1
fi

# ✅ etcd version dan image
ETCD_VERSION="3.5.17-0"
ETCD_IMAGE="registry.k8s.io/etcd:${ETCD_VERSION}"

echo ""
echo "🐳 Using etcd image: ${ETCD_IMAGE}"

# ✅ Initial cluster configuration
INITIAL_CLUSTER="${NODE_NAME_CP1}=https://${CP_IP_START}:2380,${NODE_NAME_CP2}=https://${CP_IP_END}:2380"

echo ""
echo "🔗 Initial cluster: ${INITIAL_CLUSTER}"

# ✅ Generate manifests untuk setiap node
HOSTS=(${CP_IP_START} ${CP_IP_END})
NAMES=(${NODE_NAME_CP1} ${NODE_NAME_CP2})

for i in "${!HOSTS[@]}"; do
    HOST=${HOSTS[$i]}
    NAME=${NAMES[$i]}
    
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "📝 Step $((i+1)): Creating manifest for ${NAME} (${HOST})"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    
    # Create node-specific directory
    NODE_MANIFEST_DIR="${MANIFEST_DIR}/${HOST}"
    sudo mkdir -p ${NODE_MANIFEST_DIR}
    
    # ✅ Create kubeadm configuration for manifest generation
    cat << EOF > ${NODE_MANIFEST_DIR}/kubeadmcfg.yaml
apiVersion: kubeadm.k8s.io/v1beta4
kind: ClusterConfiguration
etcd:
  local:
    imageRepository: registry.k8s.io
    imageTag: ${ETCD_VERSION}
    dataDir: /var/lib/etcd
    serverCertSANs:
    - "${HOST}"
    - "${NAME}"
    - "localhost"
    - "127.0.0.1"
    peerCertSANs:
    - "${HOST}"
    - "${NAME}"
    - "localhost"
    - "127.0.0.1"
    extraArgs:
      name: ${NAME}
      listen-peer-urls: https://${HOST}:2380
      listen-client-urls: https://${HOST}:2379,https://127.0.0.1:2379
      advertise-client-urls: https://${HOST}:2379
      initial-advertise-peer-urls: https://${HOST}:2380
      initial-cluster: ${INITIAL_CLUSTER}
      initial-cluster-state: new
      initial-cluster-token: etcd-cluster-1
      # Performance tuning
      heartbeat-interval: "100"
      election-timeout: "1000"
      snapshot-count: "10000"
      # Data integrity
      quota-backend-bytes: "8589934592"
      auto-compaction-mode: periodic
      auto-compaction-retention: "1"
      # Logging
      log-level: info
      # Metrics
      metrics: extensive
      listen-metrics-urls: http://0.0.0.0:2381
EOF

    echo "📋 Kubeadm configuration for ${NAME}:"
    cat ${NODE_MANIFEST_DIR}/kubeadmcfg.yaml
    
    # ✅ Generate static pod manifest
    echo ""
    echo "🔨 Generating static pod manifest for ${NAME}..."
    
    # Backup existing certificates if any
    if [ -d "/etc/kubernetes/pki/etcd" ]; then
        echo "📦 Backing up existing etcd certificates..."
        sudo cp -r /etc/kubernetes/pki/etcd /tmp/etcd-pki-backup-$(date +%s) || true
    fi
    
    # Copy certificates for this node
    echo "📦 Copying certificates for ${NAME}..."
    sudo mkdir -p /etc/kubernetes/pki
    
    if [ -d "${CERT_DIR}/${HOST}/pki" ]; then
        sudo cp -r ${CERT_DIR}/${HOST}/pki /etc/kubernetes/
    else
        echo "❌ Certificates not found for ${HOST}"
        echo "   Expected: ${CERT_DIR}/${HOST}/pki"
        exit 1
    fi
    
    # Generate manifest using kubeadm
    echo "🔨 Running kubeadm to generate manifest..."
    sudo kubeadm init phase etcd local --config=${NODE_MANIFEST_DIR}/kubeadmcfg.yaml
    
    # Copy generated manifest
    if [ -f "/etc/kubernetes/manifests/etcd.yaml" ]; then
        sudo cp /etc/kubernetes/manifests/etcd.yaml ${NODE_MANIFEST_DIR}/
        sudo chown $(id -u):$(id -g) ${NODE_MANIFEST_DIR}/etcd.yaml
        
        # Remove from manifests directory (we'll deploy later)
        sudo rm /etc/kubernetes/manifests/etcd.yaml
        
        echo "✅ Manifest generated: ${NODE_MANIFEST_DIR}/etcd.yaml"
    else
        echo "❌ Failed to generate manifest for ${NAME}"
        exit 1
    fi
    
    # ✅ Verify manifest content
    echo ""
    echo "🔍 Verifying manifest for ${NAME}..."
    
    # Check required fields
    if ! grep -q "name: ${NAME}" ${NODE_MANIFEST_DIR}/etcd.yaml; then
        echo "❌ Manifest validation failed: name not found"
        exit 1
    fi
    
    if ! grep -q "initial-cluster: ${INITIAL_CLUSTER}" ${NODE_MANIFEST_DIR}/etcd.yaml; then
        echo "❌ Manifest validation failed: initial-cluster not correct"
        exit 1
    fi
    
    if ! grep -q "listen-client-urls: https://${HOST}:2379" ${NODE_MANIFEST_DIR}/etcd.yaml; then
        echo "❌ Manifest validation failed: listen-client-urls not correct"
        exit 1
    fi
    
    echo "✅ Manifest validation passed for ${NAME}"
    
    # ✅ Display manifest summary
    echo ""
    echo "📋 Manifest summary for ${NAME}:"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    grep -E "name:|image:|listen-client-urls:|listen-peer-urls:|initial-cluster:" ${NODE_MANIFEST_DIR}/etcd.yaml | head -20
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    
    # ✅ Create deployment package
    echo ""
    echo "📦 Creating deployment package for ${NAME}..."
    
    # Create deployment directory structure
    DEPLOY_DIR="${NODE_MANIFEST_DIR}/deploy"
    mkdir -p ${DEPLOY_DIR}/manifests
    mkdir -p ${DEPLOY_DIR}/pki
    
    # Copy manifest
    cp ${NODE_MANIFEST_DIR}/etcd.yaml ${DEPLOY_DIR}/manifests/
    
    # Copy certificates
    cp -r ${CERT_DIR}/${HOST}/pki/* ${DEPLOY_DIR}/pki/
    
    # Create deployment script
    cat << 'EOF_DEPLOY' > ${DEPLOY_DIR}/deploy.sh
#!/bin/bash
set -Eeuo pipefail

echo "🚀 Deploying etcd static pod..."

# Backup existing configuration
if [ -d "/etc/kubernetes/pki/etcd" ]; then
    echo "📦 Backing up existing etcd configuration..."
    sudo cp -r /etc/kubernetes/pki/etcd /tmp/etcd-backup-$(date +%s)
fi

# Create directories
echo "📁 Creating directories..."
sudo mkdir -p /etc/kubernetes/manifests
sudo mkdir -p /etc/kubernetes/pki
sudo mkdir -p /var/lib/etcd
sudo chmod 700 /var/lib/etcd

# Deploy certificates
echo "🔐 Deploying certificates..."
sudo cp -r pki/* /etc/kubernetes/pki/

# Set proper permissions
sudo chown -R root:root /etc/kubernetes/pki
sudo chmod 600 /etc/kubernetes/pki/etcd/*.key

# Deploy manifest
echo "📝 Deploying manifest..."
sudo cp manifests/etcd.yaml /etc/kubernetes/manifests/

# Wait for pod to start
echo "⏳ Waiting for etcd pod to start..."
sleep 10

# Check if pod is running
if sudo crictl pods | grep -q etcd; then
    echo "✅ etcd pod is running"
    sudo crictl pods | grep etcd
else
    echo "⚠️  etcd pod not found yet, checking logs..."
    sudo journalctl -u kubelet -n 50 --no-pager || true
fi

echo ""
echo "✅ Deployment complete!"
echo ""
echo "📋 Verify with:"
echo "   sudo crictl pods | grep etcd"
echo "   sudo crictl logs <pod-id>"
echo "   ETCDCTL_API=3 etcdctl --endpoints=https://127.0.0.1:2379 \\"
echo "     --cacert=/etc/kubernetes/pki/etcd/ca.crt \\"
echo "     --cert=/etc/kubernetes/pki/etcd/server.crt \\"
echo "     --key=/etc/kubernetes/pki/etcd/server.key \\"
echo "     member list"
EOF_DEPLOY

    chmod +x ${DEPLOY_DIR}/deploy.sh
    
    # Create verification script
    cat << 'EOF_VERIFY' > ${DEPLOY_DIR}/verify.sh
#!/bin/bash
set -e

echo "🔍 Verifying etcd cluster health..."
echo ""

# Check if etcd pod is running
echo "📋 Checking etcd pod status..."
if sudo crictl pods | grep -q etcd; then
    echo "✅ etcd pod is running"
    sudo crictl pods | grep etcd
else
    echo "❌ etcd pod is not running"
    exit 1
fi

# Get pod ID
POD_ID=$(sudo crictl pods | grep etcd | awk '{print $1}' | head -1)
echo ""
echo "📋 Pod ID: ${POD_ID}"

# Check etcd health
echo ""
echo "🩺 Checking etcd health..."
if sudo ETCDCTL_API=3 etcdctl \
    --endpoints=https://127.0.0.1:2379 \
    --cacert=/etc/kubernetes/pki/etcd/ca.crt \
    --cert=/etc/kubernetes/pki/etcd/healthcheck-client.crt \
    --key=/etc/kubernetes/pki/etcd/healthcheck-client.key \
    endpoint health; then
    echo "✅ etcd is healthy"
else
    echo "❌ etcd health check failed"
    exit 1
fi

# List cluster members
echo ""
echo "👥 Cluster members:"
sudo ETCDCTL_API=3 etcdctl \
    --endpoints=https://127.0.0.1:2379 \
    --cacert=/etc/kubernetes/pki/etcd/ca.crt \
    --cert=/etc/kubernetes/pki/etcd/healthcheck-client.crt \
    --key=/etc/kubernetes/pki/etcd/healthcheck-client.key \
    member list -w table

# Check cluster status
echo ""
echo "📊 Cluster status:"
sudo ETCDCTL_API=3 etcdctl \
    --endpoints=https://127.0.0.1:2379 \
    --cacert=/etc/kubernetes/pki/etcd/ca.crt \
    --cert=/etc/kubernetes/pki/etcd/healthcheck-client.crt \
    --key=/etc/kubernetes/pki/etcd/healthcheck-client.key \
    endpoint status -w table

echo ""
echo "✅ Verification complete!"
EOF_VERIFY

    chmod +x ${DEPLOY_DIR}/verify.sh
    
    # Create README
    cat << EOF_README > ${DEPLOY_DIR}/README.md
# etcd Deployment Package for ${NAME}

## Contents
- \`manifests/etcd.yaml\`: Static pod manifest
- \`pki/\`: etcd certificates and keys
- \`deploy.sh\`: Deployment script
- \`verify.sh\`: Verification script

## Deployment Steps

### 1. Transfer package to node
\`\`\`bash
# On local machine
scp -r deploy/ user@${HOST}:/tmp/etcd-deploy/
\`\`\`

### 2. Deploy on node
\`\`\`bash
# On ${NAME} (${HOST})
cd /tmp/etcd-deploy
sudo ./deploy.sh
\`\`\`

### 3. Verify deployment
\`\`\`bash
# Wait a few seconds for pod to start
sleep 15

# Run verification
sudo ./verify.sh
\`\`\`

## Manual Verification Commands

### Check pod status
\`\`\`bash
sudo crictl pods | grep etcd
sudo crictl ps | grep etcd
\`\`\`

### Check pod logs
\`\`\`bash
POD_ID=\$(sudo crictl pods | grep etcd | awk '{print \$1}')
sudo crictl logs \$POD_ID
\`\`\`

### Check etcd health
\`\`\`bash
sudo ETCDCTL_API=3 etcdctl \\
  --endpoints=https://127.0.0.1:2379 \\
  --cacert=/etc/kubernetes/pki/etcd/ca.crt \\
  --cert=/etc/kubernetes/pki/etcd/healthcheck-client.crt \\
  --key=/etc/kubernetes/pki/etcd/healthcheck-client.key \\
  endpoint health
\`\`\`

### List cluster members
\`\`\`bash
sudo ETCDCTL_API=3 etcdctl \\
  --endpoints=https://127.0.0.1:2379 \\
  --cacert=/etc/kubernetes/pki/etcd/ca.crt \\
  --cert=/etc/kubernetes/pki/etcd/healthcheck-client.crt \\
  --key=/etc/kubernetes/pki/etcd/healthcheck-client.key \\
  member list -w table
\`\`\`

## Troubleshooting

### Pod not starting
\`\`\`bash
# Check kubelet logs
sudo journalctl -u kubelet -n 100 --no-pager

# Check manifest syntax
sudo cat /etc/kubernetes/manifests/etcd.yaml

# Check certificate permissions
sudo ls -la /etc/kubernetes/pki/etcd/
\`\`\`

### Connection refused
\`\`\`bash
# Check if etcd is listening
sudo netstat -tlnp | grep 2379
sudo netstat -tlnp | grep 2380

# Check firewall
sudo iptables -L -n | grep 2379
sudo iptables -L -n | grep 2380
\`\`\`

### Certificate errors
\`\`\`bash
# Verify certificate validity
openssl x509 -in /etc/kubernetes/pki/etcd/server.crt -text -noout

# Check certificate SANs
openssl x509 -in /etc/kubernetes/pki/etcd/server.crt -text -noout | grep -A1 "Subject Alternative Name"
\`\`\`

## Node Information
- **Node Name**: ${NAME}
- **IP Address**: ${HOST}
- **Client URL**: https://${HOST}:2379
- **Peer URL**: https://${HOST}:2380
- **Metrics URL**: http://${HOST}:2381

## Initial Cluster
${INITIAL_CLUSTER}
EOF_README

    echo "✅ Deployment package created: ${DEPLOY_DIR}"
    
    # Create tarball for easy transfer
    echo ""
    echo "📦 Creating deployment tarball..."
    tar czf ${NODE_MANIFEST_DIR}/${NAME}-etcd-deploy.tar.gz -C ${NODE_MANIFEST_DIR} deploy
    echo "✅ Tarball created: ${NODE_MANIFEST_DIR}/${NAME}-etcd-deploy.tar.gz"
    
    # Display package contents
    echo ""
    echo "📋 Package contents:"
    tree ${DEPLOY_DIR} || find ${DEPLOY_DIR} -type f
    
done

# ✅ Create master deployment script
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "📝 Creating master deployment script..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

cat << 'EOF_MASTER' > ${MANIFEST_DIR}/deploy-all.sh
#!/bin/bash
set -Eeuo pipefail

# Configuration
CP_IP_START="__CP_IP_START__"
CP_IP_END="__CP_IP_END__"
NODE_NAME_CP1="__NODE_NAME_CP1__"
NODE_NAME_CP2="__NODE_NAME_CP2__"
SSH_USER="${SSH_USER:-ubuntu}"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/id_rsa}"

echo "🚀 Deploying etcd cluster to all nodes"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Node 1: ${NODE_NAME_CP1} (${CP_IP_START})"
echo "Node 2: ${NODE_NAME_CP2} (${CP_IP_END})"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

HOSTS=(${CP_IP_START} ${CP_IP_END})
NAMES=(${NODE_NAME_CP1} ${NODE_NAME_CP2})

# Deploy to each node
for i in "${!HOSTS[@]}"; do
    HOST=${HOSTS[$i]}
    NAME=${NAMES[$i]}
    
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "📤 Deploying to ${NAME} (${HOST})"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    
    # Check SSH connectivity
    echo "🔌 Testing SSH connection..."
    if ! ssh -i ${SSH_KEY} -o ConnectTimeout=5 -o StrictHostKeyChecking=no ${SSH_USER}@${HOST} "echo 'SSH OK'"; then
        echo "❌ Cannot connect to ${HOST}"
        exit 1
    fi
    
    # Transfer deployment package
    echo "📦 Transferring deployment package..."
    ssh -i ${SSH_KEY} ${SSH_USER}@${HOST} "sudo rm -rf /tmp/etcd-deploy"
    scp -i ${SSH_KEY} -r ${HOST}/deploy ${SSH_USER}@${HOST}:/tmp/etcd-deploy
    
    # Run deployment
    echo "🚀 Running deployment on ${NAME}..."
    ssh -i ${SSH_KEY} ${SSH_USER}@${HOST} "cd /tmp/etcd-deploy && sudo ./deploy.sh"
    
    echo "✅ Deployment complete on ${NAME}"
    
    # Wait before deploying to next node
    if [ $i -lt $((${#HOSTS[@]} - 1)) ]; then
        echo ""
        echo "⏳ Waiting 15 seconds before deploying to next node..."
        sleep 15
    fi
done

# Verify cluster
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "🔍 Verifying cluster health..."
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# Wait for cluster to stabilize
echo "⏳ Waiting 30 seconds for cluster to stabilize..."
sleep 30

# Verify on first node
echo ""
echo "📋 Checking cluster status from ${NODE_NAME_CP1}..."
ssh -i ${SSH_KEY} ${SSH_USER}@${CP_IP_START} "cd /tmp/etcd-deploy && sudo ./verify.sh"

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "✅ etcd Cluster Deployment Complete!"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "📋 Next steps:"
echo "   1. Verify cluster health on all nodes"
echo "   2. Configure Kubernetes to use external etcd"
echo "   3. Initialize Kubernetes control plane"
EOF_MASTER


# Replace placeholders
sed -i "s/__CP_IP_START__/${CP_IP_START}/g" ${MANIFEST_DIR}/deploy-all.sh
sed -i "s/__CP_IP_END__/${CP_IP_END}/g" ${MANIFEST_DIR}/deploy-all.sh
sed -i "s/__NODE_NAME_CP1__/${NODE_NAME_CP1}/g" ${MANIFEST_DIR}/deploy-all.sh
sed -i "s/__NODE_NAME_CP2__/${NODE_NAME_CP2}/g" ${MANIFEST_DIR}/deploy-all.sh

chmod +x ${MANIFEST_DIR}/deploy-all.sh

echo "✅ Master deployment script created: ${MANIFEST_DIR}/deploy-all.sh"

# ✅ Create cluster verification script
echo ""
echo "📝 Creating cluster verification script..."

cat << 'EOF_CLUSTER_VERIFY' > ${MANIFEST_DIR}/verify-cluster.sh
#!/bin/bash
set -e

CP_IP_START="__CP_IP_START__"
CP_IP_END="__CP_IP_END__"
NODE_NAME_CP1="__NODE_NAME_CP1__"
NODE_NAME_CP2="__NODE_NAME_CP2__"
SSH_USER="${SSH_USER:-ubuntu}"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/id_rsa}"

echo "🔍 Verifying etcd Cluster Health"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

HOSTS=(${CP_IP_START} ${CP_IP_END})
NAMES=(${NODE_NAME_CP1} ${NODE_NAME_CP2})

ALL_HEALTHY=true

for i in "${!HOSTS[@]}"; do
    HOST=${HOSTS[$i]}
    NAME=${NAMES[$i]}
    
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "📋 Checking ${NAME} (${HOST})"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    
    # Check pod status
    echo "🔍 Pod status:"
    if ssh -i ${SSH_KEY} ${SSH_USER}@${HOST} "sudo crictl pods | grep etcd" 2>/dev/null; then
        echo "✅ Pod is running"
    else
        echo "❌ Pod is not running"
        ALL_HEALTHY=false
        continue
    fi
    
    # Check etcd health
    echo ""
    echo "🩺 Health check:"
    if ssh -i ${SSH_KEY} ${SSH_USER}@${HOST} "
        sudo ETCDCTL_API=3 etcdctl \
          --endpoints=https://127.0.0.1:2379 \
          --cacert=/etc/kubernetes/pki/etcd/ca.crt \
          --cert=/etc/kubernetes/pki/etcd/healthcheck-client.crt \
          --key=/etc/kubernetes/pki/etcd/healthcheck-client.key \
          endpoint health
    " 2>/dev/null; then
        echo "✅ etcd is healthy"
    else
        echo "❌ etcd health check failed"
        ALL_HEALTHY=false
    fi
done

# Check cluster-wide status from first node
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "📊 Cluster-wide Status (from ${NODE_NAME_CP1})"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

echo ""
echo "👥 Cluster Members:"
ssh -i ${SSH_KEY} ${SSH_USER}@${CP_IP_START} "
    sudo ETCDCTL_API=3 etcdctl \
      --endpoints=https://127.0.0.1:2379 \
      --cacert=/etc/kubernetes/pki/etcd/ca.crt \
      --cert=/etc/kubernetes/pki/etcd/healthcheck-client.crt \
      --key=/etc/kubernetes/pki/etcd/healthcheck-client.key \
      member list -w table
"

echo ""
echo "📈 Endpoint Status:"
ssh -i ${SSH_KEY} ${SSH_USER}@${CP_IP_START} "
    sudo ETCDCTL_API=3 etcdctl \
      --endpoints=https://${CP_IP_START}:2379,https://${CP_IP_END}:2379 \
      --cacert=/etc/kubernetes/pki/etcd/ca.crt \
      --cert=/etc/kubernetes/pki/etcd/healthcheck-client.crt \
      --key=/etc/kubernetes/pki/etcd/healthcheck-client.key \
      endpoint status -w table
"

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
if [ "$ALL_HEALTHY" = true ]; then
    echo "✅ All nodes are healthy!"
    exit 0
else
    echo "❌ Some nodes have issues. Please check logs."
    exit 1
fi
EOF_CLUSTER_VERIFY

# Replace placeholders
sed -i "s/__CP_IP_START__/${CP_IP_START}/g" ${MANIFEST_DIR}/verify-cluster.sh
sed -i "s/__CP_IP_END__/${CP_IP_END}/g" ${MANIFEST_DIR}/verify-cluster.sh
sed -i "s/__NODE_NAME_CP1__/${NODE_NAME_CP1}/g" ${MANIFEST_DIR}/verify-cluster.sh
sed -i "s/__NODE_NAME_CP2__/${NODE_NAME_CP2}/g" ${MANIFEST_DIR}/verify-cluster.sh

chmod +x ${MANIFEST_DIR}/verify-cluster.sh

echo "✅ Cluster verification script created: ${MANIFEST_DIR}/verify-cluster.sh"

# ✅ Create cleanup script
echo ""
echo "📝 Creating cleanup script..."

cat << 'EOF_CLEANUP' > ${MANIFEST_DIR}/cleanup-all.sh
#!/bin/bash
set -e

CP_IP_START="__CP_IP_START__"
CP_IP_END="__CP_IP_END__"
NODE_NAME_CP1="__NODE_NAME_CP1__"
NODE_NAME_CP2="__NODE_NAME_CP2__"
SSH_USER="${SSH_USER:-ubuntu}"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/id_rsa}"

echo "🧹 Cleaning up etcd cluster from all nodes"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "⚠️  WARNING: This will remove all etcd data!"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

read -p "Are you sure? (yes/no): " CONFIRM
if [ "$CONFIRM" != "yes" ]; then
    echo "❌ Cleanup cancelled"
    exit 0
fi

HOSTS=(${CP_IP_START} ${CP_IP_END})
NAMES=(${NODE_NAME_CP1} ${NODE_NAME_CP2})

for i in "${!HOSTS[@]}"; do
    HOST=${HOSTS[$i]}
    NAME=${NAMES[$i]}
    
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "🧹 Cleaning ${NAME} (${HOST})"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    
    ssh -i ${SSH_KEY} ${SSH_USER}@${HOST} "
        set -e
        
        echo '🛑 Stopping etcd pod...'
        sudo rm -f /etc/kubernetes/manifests/etcd.yaml || true
        
        echo '⏳ Waiting for pod to stop...'
        sleep 10
        
        echo '🗑️  Removing etcd data...'
        sudo rm -rf /var/lib/etcd
        
        echo '🗑️  Removing certificates...'
        sudo rm -rf /etc/kubernetes/pki/etcd
        
        echo '🗑️  Removing deployment files...'
        sudo rm -rf /tmp/etcd-deploy
        
        echo '✅ Cleanup complete on ${NAME}'
    "
done

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "✅ Cleanup complete on all nodes!"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
EOF_CLEANUP

# Replace placeholders
sed -i "s/__CP_IP_START__/${CP_IP_START}/g" ${MANIFEST_DIR}/cleanup-all.sh
sed -i "s/__CP_IP_END__/${CP_IP_END}/g" ${MANIFEST_DIR}/cleanup-all.sh
sed -i "s/__NODE_NAME_CP1__/${NODE_NAME_CP1}/g" ${MANIFEST_DIR}/cleanup-all.sh
sed -i "s/__NODE_NAME_CP2__/${NODE_NAME_CP2}/g" ${MANIFEST_DIR}/cleanup-all.sh

chmod +x ${MANIFEST_DIR}/cleanup-all.sh

echo "✅ Cleanup script created: ${MANIFEST_DIR}/cleanup-all.sh"

# ✅ Create master README
echo ""
echo "📝 Creating master README..."

cat << EOF_MASTER_README > ${MANIFEST_DIR}/README.md
# etcd HA Cluster Deployment

This directory contains all necessary files to deploy a 2-node etcd cluster for Kubernetes HA.

## Cluster Configuration

- **Node 1**: ${NODE_NAME_CP1} (${CP_IP_START})
- **Node 2**: ${NODE_NAME_CP2} (${CP_IP_END})
- **etcd Version**: ${ETCD_VERSION}
- **Initial Cluster**: ${INITIAL_CLUSTER}
