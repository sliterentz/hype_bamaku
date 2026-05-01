package k8s

import (
	"fmt"

	"github.com/pulumi/pulumi-command/sdk/go/command/remote"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"k8s-ha-cluster/pkg/config"
	"k8s-ha-cluster/pkg/hyperv"
)

type Cluster struct {
	KubeConfig pulumi.StringOutput
	VIP        string
}

func Bootstrap(ctx *pulumi.Context, cfg *config.Config, nodes []*hyperv.Node) (*Cluster, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("❌ tidak ada node yang tersedia untuk bootstrap")
	}

	// ✅ VALIDASI: SSH Config
	if cfg.SSHPrivateKey == nil {
		return nil, fmt.Errorf("❌ SSH private key tidak dikonfigurasi. Set dengan:\n" +
			"   pulumi config set --secret sshPrivateKey < ~/.ssh/id_rsa\n" +
			"   atau set SSH_PRIVATE_KEY di .env")
	}
	if cfg.SSHUser == "" {
		return nil, fmt.Errorf("❌ SSH user tidak dikonfigurasi")
	}

	// ✅ VALIDASI: Semua node harus valid
	for i, node := range nodes {
		if node == nil {
			return nil, fmt.Errorf("❌ node[%d] adalah nil", i)
		}
		if node.Name == "" {
			return nil, fmt.Errorf("❌ node[%d] tidak memiliki nama", i)
		}

		// Log untuk debugging
		ctx.Log.Info(fmt.Sprintf("✅ Node %d validated: %s @ %v", i, node.Name, node.IPAddress), nil)
	}

	// ✅ VALIDASI: VIP Configuration
	if cfg.K8sVIP == "" {
		return nil, fmt.Errorf("❌ K8sVIP tidak dikonfigurasi")
	}
	if cfg.K8sVIPInterface == "" {
		return nil, fmt.Errorf("❌ K8sVIPInterface tidak dikonfigurasi")
	}

	// Helper function dengan error handling
	createConnection := func(node *hyperv.Node) *remote.ConnectionArgs {

		return &remote.ConnectionArgs{
			Host:           node.IPAddress,
			User:           pulumi.String(cfg.SSHUser),
			PrivateKey:     cfg.SSHPrivateKey,
			Port:           pulumi.Float64(22),
			DialErrorLimit: pulumi.Int(10),
		}
	}

	// ✅ Buat connection untuk node pertama
	conn0 := createConnection(nodes[0])

	ctx.Log.Info(fmt.Sprintf("🚀 Starting bootstrap on primary node: %s", nodes[0].Name), nil)

	// 1. Reset cluster jika sudah ada (cleanup)
	var resetCommands []pulumi.Resource
	for _, node := range nodes {
		nodeName := fmt.Sprintf("kubeadm-reset-%s", node.Name)

		resetCmd, err := remote.NewCommand(ctx, nodeName, &remote.CommandArgs{
			Connection: conn0,
			Create: pulumi.String(`
                set -e
                # Reset kubeadm jika sudah pernah di-init
                if [ -f /etc/kubernetes/admin.conf ]; then
                    echo "Resetting existing cluster..."
                    sudo kubeadm reset -f
                    sudo rm -rf /etc/kubernetes
                    sudo rm -rf /var/lib/etcd
                    sudo rm -rf $HOME/.kube
                fi
                
                # Clean up iptables rules
                sudo iptables -F && sudo iptables -t nat -F && sudo iptables -t mangle -F && sudo iptables -X
                
                # Restart kubelet
                sudo systemctl restart kubelet || true

                # Restart containerd
                sudo systemctl restart containerd || true
            `),
		})
		if err != nil {
			return nil, fmt.Errorf("gagal reset node %s: %w", node.Name, err)
		}
		resetCommands = append(resetCommands, resetCmd)
	}

	// 2. Kubeadm Init pada Node Pertama
	initCmd, err := remote.NewCommand(ctx, "kubeadm-init", &remote.CommandArgs{
		Connection: conn0,
		Create: pulumi.Sprintf(`
            set -e

            # Pastikan kubeadm sudah terinstall
            if ! command -v kubeadm &> /dev/null; then
                echo "❌ kubeadm tidak ditemukan"
                exit 1
            fi
            
            echo "🚀 Initializing HA Kubernetes Control Plane..."
            echo "   Primary Node IP: %s"
            echo "   VIP (will be active after kube-vip): %s"
            
            # Init cluster dengan parameter yang sama seperti init.sh
            sudo kubeadm init \
                --control-plane-endpoint "%s:6443" \
                --apiserver-cert-extra-sans="%s,%s,%s" \
                --upload-certs \
                --pod-network-cidr="%s" \
                --kubernetes-version=%s
            
            # Setup kubeconfig untuk user
            mkdir -p $HOME/.kube
            sudo cp -f /etc/kubernetes/admin.conf $HOME/.kube/config
            sudo chown $(id -u):$(id -g) $HOME/.kube/config
			
            echo "✅ Initialization Complete."
            echo "📋 Cluster initialized with endpoint: %s:6443"
            
            # Output kubeconfig
            cat $HOME/.kube/config
        `,
			nodes[0].IPAddress,  // Display primary node IP
			cfg.K8sVIP,          // VIP untuk display
			nodes[0].IPAddress,  // control-plane-endpoint
			cfg.K8sDOMAIN,       // cert extra SANs - domain
			cfg.K8sVIP,          // cert extra SANs - VIP
			nodes[0].IPAddress,  // cert extra SANs - first node IP
			cfg.K8sPodCIDR,      // pod network CIDR
			cfg.K8sVersion,      // kubernetes version
			nodes[0].IPAddress), // Display endpoint
	}, pulumi.DependsOn(resetCommands))
	if err != nil {
		return nil, fmt.Errorf("gagal init kubeadm: %w", err)
	}

	// 3. Setup kube-vip pada Node Pertama (SETELAH kubeadm init)
	vipCmd, err := remote.NewCommand(ctx, "setup-kube-vip-primary", &remote.CommandArgs{
		Connection: conn0,
		Create: pulumi.Sprintf(`
            set -e
            
            echo "🔧 Setting up kube-vip on primary control plane..."
            
            # ✅ Check if image already exists before pulling
            echo "🔍 Checking kube-vip image..."
            if sudo ctr -n k8s.io images ls | grep -q "ghcr.io/kube-vip/kube-vip:v1.1.2"; then
                echo "  ✅ kube-vip image already exists, skipping pull"
            else
                echo "  📥 Pulling kube-vip image..."
                sudo ctr -n k8s.io image pull ghcr.io/kube-vip/kube-vip:v1.1.2
                echo "  ✅ Image pulled successfully"
            fi
            
            # ✅ Remove existing manifest if present
            if [ -f /etc/kubernetes/manifests/kube-vip.yaml ]; then
                echo "  🗑️  Removing existing kube-vip manifest..."
                sudo rm -f /etc/kubernetes/manifests/kube-vip.yaml
                sleep 5  # Wait for pod to be removed
            fi
            
            sudo ctr run --rm --net-host ghcr.io/kube-vip/kube-vip:v1.1.2 vip \
                /kube-vip manifest pod \
                --interface %s \
                --address %s \
                --controlplane \
                --services \
                --arp \
                --leaderElection | sudo tee /etc/kubernetes/manifests/kube-vip.yaml
            
            # Wait for kube-vip to start
            echo "⏳ Waiting for kube-vip to initialize..."
            sleep 15
            
            # Verify VIP is active
            echo "🔍 Verifying VIP activation..."
            for i in {1..30}; do
                if ip addr show %s | grep -q %s; then
                    echo "✅ VIP %s is now active!"
                    break
                fi
                echo "   Attempt $i/30: VIP not yet active, waiting..."
                sleep 2
            done
            
            if [ "$VIP_ACTIVE" = false ]; then
                echo "⚠️  Warning: VIP did not activate within timeout"
                echo "   Continuing anyway - VIP may activate shortly"
            fi
            
            # Verify kube-vip pod
            echo "🔍 Checking kube-vip pod status..."
            kubectl get pods -n kube-system | grep kube-vip || echo "⚠️  kube-vip pod still starting..."
            
            # ✅ PERBAIKAN: Update kubeconfig dengan validasi
            echo "🔄 Updating kubeconfig to use domain endpoint..."
            
            # Backup kubeconfig
            cp $HOME/.kube/config $HOME/.kube/config.backup
            
            # Update server endpoint dengan error handling
            if kubectl config set-cluster kubernetes --server=https://%s:6443 2>/dev/null; then
                echo "✅ Kubeconfig updated successfully"
            else
                echo "⚠️  Failed to update kubeconfig, restoring backup"
                cp $HOME/.kube/config.backup $HOME/.kube/config
            fi
            
            # Verify kubeconfig update
            echo "📋 Current API Server endpoint:"
            CURRENT_SERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null || echo "unknown")
            echo "   $CURRENT_SERVER"
            
            # Test connectivity to new endpoint
            echo "🔍 Testing connectivity to API server via domain..."
            if kubectl cluster-info 2>/dev/null | head -n 1; then
                echo "✅ Successfully connected to API server via domain"
            else
                echo "⚠️  Cannot connect via domain, trying VIP..."
                kubectl config set-cluster kubernetes --server=https://%s:6443
                if kubectl cluster-info 2>/dev/null | head -n 1; then
                    echo "✅ Connected via VIP fallback"
                else
                    echo "⚠️  Connection issues detected, but continuing..."
                fi
            fi
            
            echo "✅ kube-vip setup complete!"
            echo "📋 API Server now accessible via: %s:6443"
        `,
			cfg.K8sVIPInterface, // interface
			cfg.K8sVIP,          // VIP address
			cfg.K8sVIPInterface, // interface untuk verify
			cfg.K8sVIP,          // VIP untuk verify
			cfg.K8sVIP,          // VIP untuk display
			cfg.K8sDOMAIN,       // ✅ Update ConfigMap dengan domain
			cfg.K8sVIP,          // ✅ VIP sebagai fallback
			cfg.K8sDOMAIN),      // Display domain
	}, pulumi.DependsOn([]pulumi.Resource{initCmd}))
	if err != nil {
		return nil, fmt.Errorf("gagal setup kube-vip: %w", err)
	}

	// 4. Install CNI (Calico)
	cniCmd, err := remote.NewCommand(ctx, "install-cni", &remote.CommandArgs{
		Connection: conn0,
		Create: pulumi.Sprintf(`
            set -e
            
            echo "📦 Installing Calico CNI..."
            
            # Download Calico manifest
            curl -LO https://raw.githubusercontent.com/projectcalico/calico/v3.31.5/manifests/calico.yaml
            
            # Modify pod CIDR if needed
            sed -i 's|# - name: CALICO_IPV4POOL_CIDR|- name: CALICO_IPV4POOL_CIDR|g' calico.yaml
            sed -i 's|#   value: "192.168.0.0/16"|  value: "%s"|g' calico.yaml
            
            # Apply Calico
            kubectl apply -f calico.yaml
            
            # Wait for Calico pods
            echo "⏳ Waiting for Calico pods to be ready..."
            kubectl wait --for=condition=ready pod -l k8s-app=calico-node -n kube-system --timeout=300s || true
            
            # Show status
            echo ""
            echo "📊 CNI Status:"
            kubectl get pods -n kube-system -l k8s-app=calico-node
            kubectl get nodes
            
            echo "✅ CNI Installation Complete"
        `, cfg.K8sPodCIDR),
	}, pulumi.DependsOn([]pulumi.Resource{vipCmd}))
	if err != nil {
		return nil, fmt.Errorf("gagal install CNI: %w", err)
	}

	// 5. Generate join command untuk control plane nodes
	joinCmd, err := remote.NewCommand(ctx, "generate-join-command", &remote.CommandArgs{
		Connection: conn0,
		Create: pulumi.Sprintf(`
            set -e

            echo "🔑 Generating join command for control plane nodes..."

            # ✅ PERBAIKAN 1: Update kubeadm-config ConfigMap SEBELUM generate join command
            echo "🔄 Updating kubeadm-config ConfigMap to use domain endpoint..."
            
            # Backup original configmap
            kubectl -n kube-system get configmap kubeadm-config -o yaml > /tmp/kubeadm-config.backup.yaml

            # ✅ Update controlPlaneEndpoint di ClusterConfiguration
            echo "🔄 Updating kubeadm-config ConfigMap..."
            kubectl -n kube-system get configmap kubeadm-config -o yaml | \
            sed "s|controlPlaneEndpoint:.*|controlPlaneEndpoint: %s:6443|g" | \
            kubectl apply -f -
            
            # Verify update
            echo "📋 Verifying ConfigMap update..."
            CURRENT_ENDPOINT=$(kubectl -n kube-system get configmap kubeadm-config -o yaml | grep controlPlaneEndpoint | awk '{print $2}')
            echo "   Current endpoint in ConfigMap: $CURRENT_ENDPOINT"
            
            if [ "$CURRENT_ENDPOINT" != "%s:6443" ]; then
                echo "⚠️  Warning: ConfigMap update may not have applied correctly"
                echo "   Expected: %s:6443"
                echo "   Got: $CURRENT_ENDPOINT"
            else
                echo "✅ ConfigMap updated successfully"
            fi
    
            # ✅ PERBAIKAN 2: Update kubeconfig untuk menggunakan domain endpoint
            echo "🔄 Updating kubeconfig to use domain endpoint..."
            kubectl config set-cluster kubernetes --server=https://%s:6443
            
            # Verify kubeconfig update
            CURRENT_SERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
            echo "📋 Current kubeconfig server: $CURRENT_SERVER"
            
            # ✅ PERBAIKAN 3: Test connectivity sebelum generate join command
            echo "🔍 Testing API server connectivity via domain..."
            MAX_RETRIES=30
            RETRY_COUNT=0
            
            while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
                if kubectl cluster-info &>/dev/null; then
                    echo "✅ Successfully connected to API server via %s"
                    break
                fi
                
                RETRY_COUNT=$((RETRY_COUNT + 1))
                echo "   Attempt $RETRY_COUNT/$MAX_RETRIES: Waiting for API server..."
                sleep 2
                
                # Fallback ke VIP jika domain tidak bisa diakses
                if [ $RETRY_COUNT -eq 15 ]; then
                    echo "⚠️  Cannot connect via domain, trying VIP fallback..."
                    kubectl config set-cluster kubernetes --server=https://%s:6443
                fi
            done
            
            if [ $RETRY_COUNT -eq $MAX_RETRIES ]; then
                echo "❌ Failed to connect to API server after $MAX_RETRIES attempts"
                exit 1
            fi
            
            # ✅ PERBAIKAN 3: Generate certificate key dengan proper error handling
            echo "🔐 Generating certificate key..."
            
            CERT_KEY=""
            CERT_RETRY=0
            MAX_CERT_RETRY=3
            
            while [ $CERT_RETRY -lt $MAX_CERT_RETRY ] && [ -z "$CERT_KEY" ]; do
                # Upload certs dan ambil key
                CERT_OUTPUT=$(sudo kubeadm init phase upload-certs --upload-certs 2>&1)
                
                # Extract certificate key (baris terakhir dari output)
                CERT_KEY=$(echo "$CERT_OUTPUT" | grep -v "upload-certs" | grep -v "Using certificate" | tail -1 | tr -d '[:space:]')
                
                # Validate certificate key format (harus 64 karakter hex)
                if [ -n "$CERT_KEY" ] && [ ${#CERT_KEY} -eq 64 ]; then
                    echo "✅ Certificate key generated: ${CERT_KEY:0:10}..."
                    break
                else
                    CERT_KEY=""
                    CERT_RETRY=$((CERT_RETRY + 1))
                    echo "⚠️  Attempt $CERT_RETRY/$MAX_CERT_RETRY: Invalid certificate key, retrying..."
                    sleep 3
                fi
            done
    
            if [ -z "$CERT_KEY" ]; then
                echo "❌ Failed to generate valid certificate key after $MAX_CERT_RETRY attempts"
                exit 1
            fi

            # ✅ PERBAIKAN 4: Generate certificate key dengan retry
            echo "🔐 Generating certificate key..."
            CERT_KEY=""
            CERT_RETRY=0
            MAX_CERT_RETRY=3
            
            while [ $CERT_RETRY -lt $MAX_CERT_RETRY ] && [ -z "$CERT_KEY" ]; do
                CERT_KEY=$(sudo kubeadm init phase upload-certs --upload-certs 2>/dev/null | tail -1)
                
                if [ -z "$CERT_KEY" ]; then
                    CERT_RETRY=$((CERT_RETRY + 1))
                    echo "⚠️  Attempt $CERT_RETRY/$MAX_CERT_RETRY: Failed to generate certificate key, retrying..."
                    sleep 3
                fi
            done
    
            if [ -z "$CERT_KEY" ]; then
                echo "❌ Failed to generate certificate key after $MAX_CERT_RETRY attempts"
                exit 1
            fi
            
            echo "✅ Certificate key generated: ${CERT_KEY:0:10}..."
            
            # ✅ PERBAIKAN 5: Generate join command dengan explicit API server endpoint
            echo "📝 Creating join token with domain endpoint..."
            
            # Set KUBECONFIG explicitly
            export KUBECONFIG=$HOME/.kube/config
            
            # Generate join command dengan retry
            JOIN_CMD=""
            JOIN_RETRY=0
            MAX_JOIN_RETRY=3
            
            while [ $JOIN_RETRY -lt $MAX_JOIN_RETRY ] && [ -z "$JOIN_CMD" ]; do
                # Generate join command dengan explicit server endpoint
                JOIN_CMD=$(sudo kubeadm token create \
                    --print-join-command \
                    --certificate-key $CERT_KEY \
                    --kubeconfig $HOME/.kube/config 2>&1)
                
                # Check if command contains error
                if echo "$JOIN_CMD" | grep -q "error\|Error\|failed\|Failed"; then
                    echo "⚠️  Attempt $((JOIN_RETRY + 1))/$MAX_JOIN_RETRY: Join command generation failed"
                    echo "   Error: $JOIN_CMD"
                    JOIN_CMD=""
                    JOIN_RETRY=$((JOIN_RETRY + 1))
                    sleep 3
                else
                    break
                fi
            done
            
            if [ -z "$JOIN_CMD" ]; then
                echo "❌ Failed to generate join command after $MAX_JOIN_RETRY attempts"
                echo "📋 Debugging information:"
                echo "   API Server: $(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')"
                echo "   Cluster info:"
                kubectl cluster-info || true
                exit 1
            fi
            
            # ✅ PERBAIKAN 4: Generate join token
            echo "🎫 Generating join token..."
            
            TOKEN=""
            TOKEN_RETRY=0
            MAX_TOKEN_RETRY=3
            
            while [ $TOKEN_RETRY -lt $MAX_TOKEN_RETRY ] && [ -z "$TOKEN" ]; do
                TOKEN=$(sudo kubeadm token create --ttl 24h 2>&1)
                
                # Validate token format (should be like: abcdef.0123456789abcdef)
                if echo "$TOKEN" | grep -qE '^[a-z0-9]{6}\.[a-z0-9]{16}$'; then
                    echo "✅ Token generated: ${TOKEN:0:10}..."
                    break
                else
                    TOKEN=""
                    TOKEN_RETRY=$((TOKEN_RETRY + 1))
                    echo "⚠️  Attempt $TOKEN_RETRY/$MAX_TOKEN_RETRY: Invalid token format, retrying..."
                    sleep 2
                fi
            done
            
            if [ -z "$TOKEN" ]; then
                echo "❌ Failed to generate valid token after $MAX_TOKEN_RETRY attempts"
                exit 1
            fi
                    
            # ✅ PERBAIKAN 5: Get CA cert hash
            echo "🔒 Getting CA certificate hash..."
            
            CA_CERT_HASH=$(openssl x509 -pubkey -in /etc/kubernetes/pki/ca.crt | \
                openssl rsa -pubin -outform der 2>/dev/null | \
                openssl dgst -sha256 -hex | sed 's/^.* //')
            
            if [ -z "$CA_CERT_HASH" ]; then
                echo "❌ Failed to generate CA cert hash"
                exit 1
            fi
            
            echo "✅ CA cert hash: sha256:${CA_CERT_HASH:0:10}..."

            # ✅ PERBAIKAN 6: Verify dan modify join command untuk menggunakan domain endpoint
            echo "🔍 Verifying join command..."
            
            # Build join command dengan proper escaping
            JOIN_CMD="kubeadm join %s:6443"
            JOIN_CMD="$JOIN_CMD --token $TOKEN"
            JOIN_CMD="$JOIN_CMD --discovery-token-ca-cert-hash sha256:$CA_CERT_HASH"
            JOIN_CMD="$JOIN_CMD --certificate-key $CERT_KEY"
            
            # Extract current server dari join command
            CURRENT_JOIN_SERVER=$(echo "$JOIN_CMD" | grep -oP 'https://[^:]+:6443' | head -1)
            echo "   Join command server: $CURRENT_JOIN_SERVER"
            
            # Replace dengan domain endpoint jika berbeda
            if [ "$CURRENT_JOIN_SERVER" != "https://%s:6443" ]; then
                echo "🔄 Replacing join command endpoint with domain..."
                JOIN_CMD=$(echo "$JOIN_CMD" | sed "s|https://[^:]*:6443|https://%s:6443|g")
                echo "   Updated to: https://%s:6443"
            fi
            
            echo "✅ Join command generated successfully"
            echo "📋 Join command preview:"
            echo "   $(echo "$JOIN_CMD" | head -c 100)..."
            
            # Output join command

            echo "$JOIN_CMD"            
        `,
			cfg.K8sDOMAIN,  // Update ConfigMap
			cfg.K8sDOMAIN,  // Verify ConfigMap
			cfg.K8sDOMAIN,  // Display expected
			cfg.K8sDOMAIN,  // Update kubeconfig
			cfg.K8sDOMAIN,  // Display success message
			cfg.K8sVIP,     // VIP fallback
			cfg.K8sDOMAIN,  // Verify join command server
			cfg.K8sDOMAIN,  // Replace join command server
			cfg.K8sDOMAIN), // Display updated server
	}, pulumi.DependsOn([]pulumi.Resource{cniCmd}))
	if err != nil {
		return nil, fmt.Errorf("gagal generate join command: %w", err)
	}

	// 6. Join node lainnya sebagai control plane
	var joinCommands []pulumi.Resource

	// ✅ STEP BARU: Copy kube-vip manifest dari node pertama ke node lainnya
	for i := 1; i < len(nodes); i++ {
		conn1 := createConnection(nodes[i])

		// Step 1: Baca manifest dari primary node (node 0)
		readManifestName := fmt.Sprintf("read-kube-vip-manifest-%d", i)
		readManifestCmd, err := remote.NewCommand(ctx, readManifestName, &remote.CommandArgs{
			Connection: conn0, // ✅ Baca dari primary node
			Create: pulumi.String(`
                set -e
                echo "📖 Reading kube-vip manifest from primary node..."
                
                # Verify manifest exists
                if [ ! sudo ls -f /etc/kubernetes/manifests/kube-vip.yaml ]; then
                    echo "❌ kube-vip manifest not found on primary node!"
                    exit 1
                fi
                
                # Output manifest content (akan di-capture oleh Pulumi)
                sudo cat /etc/kubernetes/manifests/kube-vip.yaml
            `),
		}, pulumi.DependsOn([]pulumi.Resource{vipCmd, cniCmd}))

		if err != nil {
			return nil, fmt.Errorf("gagal read kube-vip manifest: %w", err)
		}

		// ✅ SOLUSI 1: Gunakan remote.NewCommand dengan scp built-in
		// Lebih reliable daripada manual scp karena menggunakan Pulumi's SSH connection
		// Step 2: Write manifest ke secondary node (node i)
		writeManifestName := fmt.Sprintf("write-kube-vip-manifest-%d", i)
		writeManifestCmd, err := remote.NewCommand(ctx, writeManifestName, &remote.CommandArgs{
			Connection: conn1, // ✅ Write ke secondary node
			Create: pulumi.Sprintf(`
                set -e
                
                echo "📦 Copying kube-vip manifest from primary node..."
                
                # ✅ Create temporary directory
                sudo mkdir -p /tmp/kube-vip-setup
                
                # ✅ Copy manifest dari primary node menggunakan SSH
                # Write manifest content menggunakan heredoc
                cat <<'MANIFEST_EOF' | sudo tee /etc/kubernetes/manifests/kube-vip.yaml > /dev/null
    %s
    MANIFEST_EOF
                
                if [ "$SUCCESS" = false ]; then
                    echo "❌ Failed to copy manifest after $MAX_RETRIES attempts"
                    echo "📋 Debugging info:"
                    echo "   Primary node: $PRIMARY_NODE_IP"
                    echo "   SSH test:"
                    ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 %s@$PRIMARY_NODE_IP "echo 'SSH OK'" || echo "SSH Failed"
                    exit 1
                fi
                
                # ✅ Verify copied file
                if [ ! -f /tmp/kube-vip-setup/kube-vip.yaml ]; then
                    echo "❌ Manifest file not found after copy!"
                    exit 1
                fi
                
                # ✅ Verify file is not empty
                if [ ! -s /tmp/kube-vip-setup/kube-vip.yaml ]; then
                    echo "❌ Manifest file is empty!"
                    exit 1
                fi
                
                echo "🔍 Manifest preview:"
                head -n 5 /tmp/kube-vip-setup/kube-vip.yaml
                
                # ✅ Create manifests directory if not exists
                sudo mkdir -p /etc/kubernetes/manifests
                
                # ✅ Move manifest to final location
                sudo cp /tmp/kube-vip-setup/kube-vip.yaml /etc/kubernetes/manifests/kube-vip.yaml
                
                # ✅ Set proper permissions
                sudo chmod 644 /etc/kubernetes/manifests/kube-vip.yaml
                sudo chown root:root /etc/kubernetes/manifests/kube-vip.yaml
                
                # ✅ Cleanup temp directory
                rm -rf /tmp/kube-vip-setup
                
                # ✅ Verify final installation
                echo "🔍 Verifying manifest installation..."
                if [ -f /etc/kubernetes/manifests/kube-vip.yaml ]; then
                    sudo ls -lh /etc/kubernetes/manifests/kube-vip.yaml
                    echo "✅ kube-vip manifest installed successfully on node %s"
                else
                    echo "❌ Manifest installation verification failed!"
                    exit 1
                fi
                
                # ✅ Wait for kubelet to detect the manifest
                echo "⏳ Waiting for kubelet to detect manifest..."
                sleep 10
            `,
				nodes[1].IPAddress, // PRIMARY_NODE_IP
				cfg.SSHUser,        // SSH user untuk copy
				cfg.SSHUser,        // SSH user untuk test
				nodes[i].Name),     // Display node name
		}, pulumi.DependsOn([]pulumi.Resource{readManifestCmd}))

		if err != nil {
			return nil, fmt.Errorf("gagal copy kube-vip manifest ke node %d: %w", i, err)
		}

		joinCommands = append(joinCommands, writeManifestCmd)
	}

	// Join node ke cluster (setelah manifest kube-vip di-copy)
	for i := 1; i < len(nodes); i++ {
		conn1 := createConnection(nodes[i])

		nodeName := fmt.Sprintf("kubeadm-join-cp-%d", i)

		// Ambil dependency dari copy manifest command yang sesuai
		copyVipDep := joinCommands[i-1]

		// Join node ke cluster dan copy kube-vip manifest dari node pertama ke node lainnya
		joinNodeCmd, err := remote.NewCommand(ctx, nodeName, &remote.CommandArgs{
			Connection: conn1,
			Create: pulumi.Sprintf(`
                set -e

                echo "🚀 Joining node %s to cluster..."
                echo "   Using endpoint: %s:6443"
                
                # ✅ PERBAIKAN 1: Test connectivity SEBELUM join
                echo "🔍 Testing connectivity to control plane..."
                MAX_PING_RETRIES=10
                PING_COUNT=0
                
                while [ $PING_COUNT -lt $MAX_PING_RETRIES ]; do
                    if ping -c 2 %s &>/dev/null; then
                        echo "✅ Can ping control plane endpoint"
                        break
                    fi
                    PING_COUNT=$((PING_COUNT + 1))
                    echo "   Attempt $PING_COUNT/$MAX_PING_RETRIES: Waiting for endpoint..."
                    sleep 3
                done
                
                # Test API server connectivity
                echo "🔍 Testing API server connectivity..."
                MAX_API_RETRIES=15
                API_COUNT=0
                
                while [ $API_COUNT -lt $MAX_API_RETRIES ]; do
                    if curl -k https://%s:6443/healthz &>/dev/null; then
                        echo "✅ API server is responding"
                        break
                    fi
                    API_COUNT=$((API_COUNT + 1))
                    echo "   Attempt $API_COUNT/$MAX_API_RETRIES: Waiting for API server..."
                    sleep 5
                done
    
                # ✅ PERBAIKAN 2: Verifikasi kube-vip manifest
                echo "🔍 Verifying kube-vip manifest..."
                if [ ! -f /etc/kubernetes/manifests/kube-vip.yaml ]; then
                    echo "❌ kube-vip manifest not found!"
                    echo "📋 Listing /etc/kubernetes/manifests:"
                    sudo ls -la /etc/kubernetes/manifests/ || echo "Directory not found"
                    exit 1
                fi
                
                echo "✅ kube-vip manifest found"
                echo "📋 Manifest preview:"
                sudo head -n 10 /etc/kubernetes/manifests/kube-vip.yaml
    
                # ✅ PERBAIKAN 3: Jalankan join command
                echo "📝 Executing join command..."
                echo "   Command preview: kubeadm join %s:6443 ..."
                
                # Execute join dengan error handling
                if sudo %s; then
                    echo "✅ Join command executed successfully"
                else
                    echo "❌ Join command failed!"
                    echo "📋 Checking kubelet logs:"
                    sudo journalctl -u kubelet -n 50 --no-pager || true
                    exit 1
                fi
                
                # ✅ PERBAIKAN 4: Setup kubeconfig SEBELUM kubectl commands
                echo "⚙️  Setting up kubeconfig..."
                
                # Create .kube directory
                mkdir -p $HOME/.kube
                
                # Wait for admin.conf to be created
                echo "⏳ Waiting for admin.conf..."
                MAX_WAIT=30
                WAIT_COUNT=0
                
                while [ $WAIT_COUNT -lt $MAX_WAIT ]; do
                    if [ -f /etc/kubernetes/admin.conf ]; then
                        echo "✅ admin.conf found"
                        break
                    fi
                    WAIT_COUNT=$((WAIT_COUNT + 1))
                    echo "   Attempt $WAIT_COUNT/$MAX_WAIT: Waiting for admin.conf..."
                    sleep 2
                done
                
                if [ ! -f /etc/kubernetes/admin.conf ]; then
                    echo "❌ admin.conf not found after join!"
                    exit 1
                fi
                
                # Copy kubeconfig
                sudo cp -f /etc/kubernetes/admin.conf $HOME/.kube/config
                sudo chown $(id -u):$(id -g) $HOME/.kube/config
                
                # ✅ Set KUBECONFIG environment variable explicitly
                export KUBECONFIG=$HOME/.kube/config
                
                # Verify kubeconfig is valid
                echo "🔍 Verifying kubeconfig..."
                if kubectl config view &>/dev/null; then
                    echo "✅ Kubeconfig is valid"
                else
                    echo "❌ Kubeconfig is invalid!"
                    cat $HOME/.kube/config
                    exit 1
                fi
    
                # ✅ PERBAIKAN 5: Update kubeconfig untuk menggunakan domain/VIP
                echo "🔄 Updating kubeconfig to use domain endpoint..."
                
                # Show current server
                CURRENT_SERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
                echo "   Current server: $CURRENT_SERVER"
                
                # Update to domain endpoint
                if kubectl config set-cluster kubernetes --server=https://%s:6443; then
                    echo "✅ Kubeconfig updated to: https://%s:6443"
                else
                    echo "⚠️  Failed to update kubeconfig, but continuing..."
                fi
                
                # Verify update
                NEW_SERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
                echo "   New server: $NEW_SERVER"
    
                # ✅ PERBAIKAN 6: Tunggu kube-vip pod aktif
                echo "⏳ Waiting for kube-vip pod to start on this node..."
                sleep 15
                
                # Wait for kubelet to be ready
                echo "⏳ Waiting for kubelet to be ready..."
                MAX_KUBELET_WAIT=30
                KUBELET_COUNT=0
                
                while [ $KUBELET_COUNT -lt $MAX_KUBELET_WAIT ]; do
                    if systemctl is-active --quiet kubelet; then
                        echo "✅ Kubelet is active"
                        break
                    fi
                    KUBELET_COUNT=$((KUBELET_COUNT + 1))
                    echo "   Attempt $KUBELET_COUNT/$MAX_KUBELET_WAIT: Waiting for kubelet..."
                    sleep 2
                done
                
                # ✅ PERBAIKAN 7: Verify cluster connectivity
                echo "🔍 Verifying cluster connectivity..."
                MAX_CLUSTER_RETRIES=30
                CLUSTER_COUNT=0
                
                while [ $CLUSTER_COUNT -lt $MAX_CLUSTER_RETRIES ]; do
                    if kubectl cluster-info &>/dev/null; then
                        echo "✅ Can access cluster via domain endpoint!"
                        break
                    fi
                    CLUSTER_COUNT=$((CLUSTER_COUNT + 1))
                    echo "   Attempt $CLUSTER_COUNT/$MAX_CLUSTER_RETRIES: Waiting for cluster access..."
                    sleep 2
                done
                
                if [ $CLUSTER_COUNT -eq $MAX_CLUSTER_RETRIES ]; then
                    echo "⚠️  Warning: Cannot access cluster via domain, trying direct connection..."
                    
                    # Try to get nodes without domain
                    if kubectl get nodes &>/dev/null; then
                        echo "✅ Can access cluster (fallback mode)"
                    else
                        echo "❌ Cannot access cluster!"
                        echo "📋 Debugging info:"
                        echo "   KUBECONFIG: $KUBECONFIG"
                        echo "   Config content:"
                        cat $HOME/.kube/config
                        echo ""
                        echo "   Kubelet status:"
                        sudo systemctl status kubelet --no-pager || true
                        exit 1
                    fi
                fi
    
                # ✅ PERBAIKAN 8: Verify node joined successfully
                echo "🔍 Verifying node status..."
                
                # Get node name
                NODE_NAME=$(hostname)
                echo "   Node name: $NODE_NAME"
                
                # Check if node appears in cluster
                if kubectl get nodes | grep -q "$NODE_NAME"; then
                    echo "✅ Node $NODE_NAME is registered in cluster"
                    
                    # Show node details
                    kubectl get node "$NODE_NAME" -o wide || true
                else
                    echo "⚠️  Warning: Node not yet visible in cluster"
                    echo "   This may be normal - node registration can take a moment"
                fi
                
                # Show all nodes
                echo ""
                echo "📊 Current cluster nodes:"
                kubectl get nodes -o wide || echo "Cannot retrieve nodes list"
    
                echo ""
                echo "✅ Node %s joined successfully"
                echo "📋 Summary:"
                echo "   - Kubeconfig: $KUBECONFIG"
                echo "   - API Server: $(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')"
                echo "   - Node Status: $(kubectl get node "$NODE_NAME" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo 'Pending')"
            `,
				nodes[i].Name,  // Display node name
				cfg.K8sDOMAIN,  // Display endpoint
				cfg.K8sDOMAIN,  // Ping test
				cfg.K8sDOMAIN,  // API test
				cfg.K8sDOMAIN,  // Display join endpoint
				joinCmd.Stdout, // Join command
				cfg.K8sDOMAIN,  // Update kubeconfig
				cfg.K8sDOMAIN,  // Display updated endpoint
				nodes[i].Name), // Display node name
		}, pulumi.DependsOn([]pulumi.Resource{copyVipDep, joinCmd}))
		if err != nil {
			return nil, fmt.Errorf("gagal join node %d: %w", i, err)
		}
		joinCommands = append(joinCommands, joinNodeCmd)
	}

	// 7. Verifikasi cluster health (setelah semua node join)
	verifyCmd, err := remote.NewCommand(ctx, "verify-cluster", &remote.CommandArgs{
		Connection: conn0,
		Create: pulumi.String(`
            set -e
            
            echo "🔍 Verifying cluster health..."
            
            # Wait for all nodes to be ready
            echo "⏳ Waiting for all nodes to be Ready..."
            kubectl wait --for=condition=Ready nodes --all --timeout=300s || true
            
            # Show cluster status
            echo ""
            echo "📊 Cluster Status:"
            echo "=================="
            kubectl get nodes -o wide
            
            echo ""
            echo "📦 System Pods:"
            echo "==============="
            kubectl get pods -n kube-system -o wide
            
            echo ""
            echo "🔧 kube-vip Status:"
            echo "==================="
            kubectl get pods -n kube-system -l app=kube-vip
            
            echo ""
            echo "🌐 VIP Configuration:"
            echo "====================="
            kubectl get configmap -n kube-system kube-vip -o yaml 2>/dev/null || echo "No kube-vip configmap found (normal for static pod setup)"
            
            echo ""
            echo "✅ Cluster verification complete!"
        `),
	}, pulumi.DependsOn(joinCommands))
	if err != nil {
		return nil, fmt.Errorf("gagal verifikasi cluster: %w", err)
	}

	// 8. Install MetalLB jika enabled
	var metallbCmd pulumi.Resource
	if cfg.K8sMetalLBEnabled {
		metallbCmd, err = remote.NewCommand(ctx, "install-metallb", &remote.CommandArgs{
			Connection: conn0,
			Create: pulumi.Sprintf(`
                set -e
                
                echo "📦 Installing MetalLB..."
                
                # Install MetalLB
                kubectl apply -f https://raw.githubusercontent.com/metallb/metallb/v0.15.3/config/manifests/metallb-native.yaml
                
                # Wait for MetalLB pods
                echo "⏳ Waiting for MetalLB pods..."
                kubectl wait --for=condition=ready pod -l app=metallb -n metallb-system --timeout=300s || true
                
                # Create IPAddressPool
                cat <<EOF | kubectl apply -f -
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: %s
  namespace: metallb-system
spec:
  addresses:
  - %s-%s
EOF
                
                # Create L2Advertisement
                cat <<EOF | kubectl apply -f -
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: %s-l2
  namespace: metallb-system
spec:
  ipAddressPools:
  - %s
EOF
                
                echo "✅ MetalLB installed and configured"
                
                # Show MetalLB status
                echo ""
                echo "📊 MetalLB Status:"
                kubectl get pods -n metallb-system
                kubectl get ipaddresspool -n metallb-system
                kubectl get l2advertisement -n metallb-system
            `,
				cfg.K8sMetalLBAddressPoolName,
				cfg.K8sMetalLBIPRangeStart,
				cfg.K8sMetalLBIPRangeEnd,
				cfg.K8sMetalLBAddressPoolName,
				cfg.K8sMetalLBAddressPoolName),
		}, pulumi.DependsOn([]pulumi.Resource{verifyCmd}))
		if err != nil {
			return nil, fmt.Errorf("gagal install MetalLB: %w", err)
		}
	}

	// 9. Export kubeconfig dan informasi cluster
	var finalDeps []pulumi.Resource
	if metallbCmd != nil {
		finalDeps = []pulumi.Resource{metallbCmd}
	} else {
		finalDeps = []pulumi.Resource{verifyCmd}
	}

	exportCmd, err := remote.NewCommand(ctx, "export-cluster-info", &remote.CommandArgs{
		Connection: conn0,
		Create: pulumi.String(`
            set -e
            
            echo ""
            echo "📋 Cluster Information:"
            echo "======================="
            echo "🏷️  Cluster Name: $(kubectl config current-context)"
            echo "🌐 API Server: $(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')"
            echo "📦 Kubernetes Version: $(kubectl version --short 2>/dev/null | grep Server || kubectl version --output=json | jq -r .serverVersion.gitVersion)"
            echo ""
            
            # Output kubeconfig untuk Pulumi
            cat $HOME/.kube/config
        `),
	}, pulumi.DependsOn(finalDeps))
	if err != nil {
		return nil, fmt.Errorf("gagal export cluster info: %w", err)
	}

	return &Cluster{
		KubeConfig: exportCmd.Stdout, // Output KUBECONFIG dari node 1
		VIP:        cfg.K8sVIP,
	}, nil
}
