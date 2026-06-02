#!/bin/bash
set -e

# Trap untuk error handling
trap 'echo "❌ Error at line $LINENO. Exit code: $?"; exit 1' ERR

CP_IP_START=$1
CP_IP_END=$2
NODE_NAME_CP1=$3
NODE_NAME_CP2=$4

echo "🔐 Generating etcd Certificates for HA Cluster"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Master Control Plane Node: ${NODE_NAME_CP1} (${CP_IP_START})"
echo "Second Control Plane Node: ${NODE_NAME_CP2} (${CP_IP_END})"
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
CERT_DIR="/tmp/etcd-certs"
echo ""
echo "📁 Go to certificate directory: ${CERT_DIR}"
# sudo rm -rf ${CERT_DIR}
# sudo mkdir -p ${CERT_DIR}
cd ${CERT_DIR}

# ✅ Generate CA certificate
echo ""
echo "🔑 Step 1: Generating Certificate Authority (CA)..."
cat << EOF | sudo tee /tmp/etcd-certs/ca-config.yaml > /dev/null 
apiVersion: kubeadm.k8s.io/v1beta4
kind: ClusterConfiguration
etcd:
  local:
    serverCertSANs:
    - "${CP_IP_START}"
    - "${CP_IP_END}"
    - "${NODE_NAME_CP1}"
    - "${NODE_NAME_CP2}"
    - "localhost"
    - "127.0.0.1"
    peerCertSANs:
    - "${CP_IP_START}"
    - "${CP_IP_END}"
    - "${NODE_NAME_CP1}"
    - "${NODE_NAME_CP2}"
    - "localhost"
    - "127.0.0.1"
EOF

echo "📋 CA Configuration:"
cat ca-config.yaml

# ✅ Generate CA certificate
echo ""
echo "🔐 Generating etcd CA certificate..."
sudo kubeadm init phase certs etcd-ca

if [ ! -f "/etc/kubernetes/pki/etcd/ca.crt" ] || [ ! -f "/etc/kubernetes/pki/etcd/ca.key" ]; then
    echo "❌ Failed to generate CA certificate"
    exit 1
fi

echo "✅ CA certificate generated successfully"
echo "   CA Cert: /etc/kubernetes/pki/etcd/ca.crt"
echo "   CA Key:  /etc/kubernetes/pki/etcd/ca.key"

# ✅ Copy CA to working directory
# sudo cp -r /etc/kubernetes/pki ${CERT_DIR}/
# sudo chown -R $(id -u):$(id -g) ${CERT_DIR}/pki

# ✅ Generate certificates for each node
HOSTS=(${CP_IP_START} ${CP_IP_END})
NAMES=(${NODE_NAME_CP1} ${NODE_NAME_CP2})

for i in "${!HOSTS[@]}"; do
    HOST=${HOSTS[$i]}
    NAME=${NAMES[$i]}
    
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "🔑 Step 2.$((i+1)): Generate certificates for ${NAME} (${HOST})"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    
    # Create node-specific directory
    NODE_DIR="${CERT_DIR}/${NAMES}"

    # ✅ PERBAIKAN: Verifikasi kubeadm-config.yaml exists
    if [ ! -f "${NODE_DIR}/kubeadm-config.yaml" ]; then
        echo "❌ kubeadm-config.yaml not found at ${NODE_DIR}/kubeadm-config.yaml"
        echo "📋 Available files in ${NODE_DIR}:"
        ls -la ${NODE_DIR}/ || true
        exit 1
    fi

    echo "📋 Node configuration:"
    cat ${NODE_DIR}/kubeadm-config.yaml
    
    # ✅ PERBAIKAN: Clean up existing certificates untuk node ini
    echo ""
    echo "🧹 Cleaning up existing certificates for ${NAME}..."
    sudo rm -rf /etc/kubernetes/pki/etcd/server.* || true
    sudo rm -rf /etc/kubernetes/pki/etcd/peer.* || true
    sudo rm -rf /etc/kubernetes/pki/etcd/healthcheck-client.* || true

    # Generate certificates for this node
    echo ""
    echo "🔐 Generating server certificate for ${NAME}..."
    sudo kubeadm init phase certs etcd-server --config=${NODE_DIR}/kubeadm-config.yaml
    
    echo "🔐 Generating peer certificate for ${NAME}..."
    sudo kubeadm init phase certs etcd-peer --config=${NODE_DIR}/kubeadm-config.yaml
    
    echo "🔐 Generating healthcheck-client certificate for ${NAME}..."
    sudo kubeadm init phase certs etcd-healthcheck-client --config=${NODE_DIR}/kubeadm-config.yaml
    
    echo "🔐 Generating apiserver-etcd-client certificate..."
    sudo kubeadm init phase certs apiserver-etcd-client --config=${NODE_DIR}/kubeadm-config.yaml
    
    # Copy certificates to node directory
    echo ""
    echo "📦 Organizing certificates for ${NAME}..."
    sudo cp -R /etc/kubernetes/pki ${NODE_DIR}/
    
    # Clean up unnecessary files
    # sudo find ${NODE_DIR}/pki -not -name "*etcd*" -type f -delete
    
    # Set proper permissions
    sudo chown -R $(id -u):$(id -g) ${NODE_DIR}
    
    echo "✅ Certificates generated for ${NAME}"
    echo ""
    echo "📋 Certificate structure for ${NAME}:"
    tree -L 3 ${NODE_DIR}/pki || find ${NODE_DIR}/pki -type f
    
    # Verify certificates
    echo ""
    echo "🔍 Verifying certificates for ${NAME}..."
    
    # Check server certificate
    if [ -f "${NODE_DIR}/pki/etcd/server.crt" ]; then
        echo "✅ Server certificate exists"
        openssl x509 -in ${NODE_DIR}/pki/etcd/server.crt -text -noout | grep -E "Subject:|DNS:|IP Address:" || true
    else
        echo "❌ Server certificate missing"
        exit 1
    fi

    # Check peer certificate
    if [ -f "${NODE_DIR}/pki/etcd/peer.crt" ]; then
        echo ""
        echo "✅ Peer certificate exists"
        echo "📋 Peer certificate details:"
        openssl x509 -in ${NODE_DIR}/pki/etcd/peer.crt -text -noout | grep -E "Subject:|Issuer:|DNS:|IP Address:" || true
        
        # Verify certificate is valid
        if ! openssl x509 -in ${NODE_DIR}/pki/etcd/peer.crt -noout -checkend 0; then
            echo "❌ Peer certificate is expired or invalid"
            exit 1
        fi
        echo "✅ Peer certificate is valid"
    else
        echo "❌ Peer certificate missing at ${NODE_DIR}/pki/etcd/peer.crt"
        echo "📋 Directory contents:"
        ls -la ${NODE_DIR}/pki/etcd/ || true
        exit 1
    fi
    
    # Check healthcheck-client certificate
    if [ -f "${NODE_DIR}/pki/etcd/healthcheck-client.crt" ]; then
        echo ""
        echo "✅ Healthcheck-client certificate exists"
        echo "📋 Healthcheck-client certificate details:"
        openssl x509 -in ${NODE_DIR}/pki/etcd/healthcheck-client.crt -text -noout | grep -E "Subject:|Issuer:" || true
        
        # Verify certificate is valid
        if ! openssl x509 -in ${NODE_DIR}/pki/etcd/healthcheck-client.crt -noout -checkend 0; then
            echo "❌ Healthcheck-client certificate is expired or invalid"
            exit 1
        fi
        echo "✅ Healthcheck-client certificate is valid"
    else
        echo "❌ Healthcheck-client certificate missing at ${NODE_DIR}/pki/etcd/healthcheck-client.crt"
        exit 1
    fi
    
    # Check apiserver-etcd-client certificate
    if [ -f "${NODE_DIR}/pki/apiserver-etcd-client.crt" ]; then
        echo ""
        echo "✅ Apiserver-etcd-client certificate exists"
        echo "📋 Apiserver-etcd-client certificate details:"
        openssl x509 -in ${NODE_DIR}/pki/apiserver-etcd-client.crt -text -noout | grep -E "Subject:|Issuer:" || true
        
        # Verify certificate is valid
        if ! openssl x509 -in ${NODE_DIR}/pki/apiserver-etcd-client.crt -noout -checkend 0; then
            echo "❌ Apiserver-etcd-client certificate is expired or invalid"
            exit 1
        fi
        echo "✅ Apiserver-etcd-client certificate is valid"
    else
        echo "❌ Apiserver-etcd-client certificate missing at ${NODE_DIR}/pki/apiserver-etcd-client.crt"
        exit 1
    fi
    
    # Verify CA certificate
    if [ -f "${NODE_DIR}/pki/etcd/ca.crt" ]; then
        echo ""
        echo "✅ CA certificate exists"
        echo "📋 CA certificate details:"
        openssl x509 -in ${NODE_DIR}/pki/etcd/ca.crt -text -noout | grep -E "Subject:|Issuer:|Not Before|Not After" || true
        
        # Verify CA certificate is valid
        if ! openssl x509 -in ${NODE_DIR}/pki/etcd/ca.crt -noout -checkend 0; then
            echo "❌ CA certificate is expired or invalid"
            exit 1
        fi
        echo "✅ CA certificate is valid"
    else
        echo "❌ CA certificate missing at ${NODE_DIR}/pki/etcd/ca.crt"
        exit 1
    fi
    
    # Verify certificate chain
    echo ""
    echo "🔗 Verifying certificate chain..."
    
    # Verify server cert is signed by CA
    if openssl verify -CAfile ${NODE_DIR}/pki/etcd/ca.crt ${NODE_DIR}/pki/etcd/server.crt >/dev/null 2>&1; then
        echo "✅ Server certificate chain is valid"
    else
        echo "❌ Server certificate chain verification failed"
        openssl verify -CAfile ${NODE_DIR}/pki/etcd/ca.crt ${NODE_DIR}/pki/etcd/server.crt || true
        exit 1
    fi
    
    # Verify peer cert is signed by CA
    if openssl verify -CAfile ${NODE_DIR}/pki/etcd/ca.crt ${NODE_DIR}/pki/etcd/peer.crt >/dev/null 2>&1; then
        echo "✅ Peer certificate chain is valid"
    else
        echo "❌ Peer certificate chain verification failed"
        openssl verify -CAfile ${NODE_DIR}/pki/etcd/ca.crt ${NODE_DIR}/pki/etcd/peer.crt || true
        exit 1
    fi
    
    # Create tarball for easy distribution
    echo ""
    echo "📦 Creating certificate archive for ${NAME}..."

    # Create tarball with proper structure
    cd ${CERT_DIR}
    sudo tar czf ${NAME}-etcd-certs.tar.gz -C ${NAME} pki
        
    # Set proper ownership setelah membuat tarball
    sudo chown $(id -u):$(id -g) ${NAME}-etcd-certs.tar.gz

    # Verify tarball was created
    if [ ! -f "${CERT_DIR}/${NAME}-etcd-certs.tar.gz" ]; then
        echo "❌ Failed to create tarball"
        exit 1
    fi
    
    # Verify tarball contents
    echo "📋 Tarball contents:"
    tar tzf ${NAME}-etcd-certs.tar.gz | head -20
    
    # Get tarball size
    TARBALL_SIZE=$(du -h ${NAME}-etcd-certs.tar.gz | cut -f1)
    echo "✅ Archive created: ${CERT_DIR}/${NAME}-etcd-certs.tar.gz (${TARBALL_SIZE})"
    
    # Create checksum for verification
    echo ""
    echo "🔐 Creating checksum..."
    sha256sum ${NAME}-etcd-certs.tar.gz | sudo tee ${NAME}-etcd-certs.tar.gz.sha256 > /dev/null
    # ✅ Set proper ownership untuk checksum file
    sudo chown $(id -u):$(id -g) ${NAME}-etcd-certs.tar.gz.sha256

    echo "✅ Checksum created: ${NAME}-etcd-certs.tar.gz.sha256"
    sudo cat ${NAME}-etcd-certs.tar.gz.sha256
    
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "✅ Certificate generation complete for ${NAME}"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    
done

# ✅ Final Summary
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "✅ All Certificate Generation Complete!"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo "📋 Generated certificate archives:"
sudo ls -lh ${CERT_DIR}/*.tar.gz 2>/dev/null || echo "No tarballs found"
echo ""
echo "📋 Checksums:"
sudo ls -lh ${CERT_DIR}/*.sha256 2>/dev/null || echo "No checksums found"
echo ""
echo "📁 Certificate directory: ${CERT_DIR}"
echo ""
echo "📊 Certificate summary:"
for name in "${NAMES[@]}"; do
    if [ -d "${CERT_DIR}/${name}/pki" ]; then
        echo ""
        echo "  Node: ${name}"
        echo "  ├── Certificates: $(find ${CERT_DIR}/${name}/pki -name "*.crt" | wc -l)"
        echo "  ├── Keys: $(find ${CERT_DIR}/${name}/pki -name "*.key" | wc -l)"
        if [ -f "${CERT_DIR}/${name}-etcd-certs.tar.gz" ]; then
            SIZE=$(du -h ${CERT_DIR}/${name}-etcd-certs.tar.gz | cut -f1)
            echo "  └── Archive: ${name}-etcd-certs.tar.gz (${SIZE})"
        fi
    fi
done
echo ""
echo "📝 Next steps:"
echo "   1. Verify checksums on target nodes:"
echo "      sha256sum -c <node>-etcd-certs.tar.gz.sha256"
echo ""
echo "   2. Distribute certificates to each node:"
echo "      scp ${CERT_DIR}/<node>-etcd-certs.tar.gz user@<node-ip>:/tmp/"
echo ""
echo "   3. Extract certificates on each node:"
echo "      sudo tar xzf /tmp/<node>-etcd-certs.tar.gz -C /etc/kubernetes/"
echo ""
echo "   4. Set proper permissions on each node:"
echo "      sudo chown -R root:root /etc/kubernetes/pki"
echo "      sudo chmod 644 /etc/kubernetes/pki/etcd/*.crt"
echo "      sudo chmod 600 /etc/kubernetes/pki/etcd/*.key"
echo ""
echo "   5. Generate etcd static pod manifests:"
echo "      ./create-etcd-manifests.sh <CP_IP_START> <CP_IP_END> <NODE_NAME_CP1> <NODE_NAME_CP2>"
echo ""
echo "   6. Deploy manifests to /etc/kubernetes/manifests/ on each node"
echo ""
echo "   7. Verify etcd cluster health:"
echo "      sudo ETCDCTL_API=3 etcdctl --endpoints=https://127.0.0.1:2379 \\"
echo "        --cacert=/etc/kubernetes/pki/etcd/ca.crt \\"
echo "        --cert=/etc/kubernetes/pki/etcd/healthcheck-client.crt \\"
echo "        --key=/etc/kubernetes/pki/etcd/healthcheck-client.key \\"
echo "        member list"
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"