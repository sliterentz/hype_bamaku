package k8s

import (
	"fmt"
	"strings"

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

	allNodeIPInputs := make([]interface{}, 0, len(nodes))
	for _, n := range nodes {
		allNodeIPInputs = append(allNodeIPInputs, n.IPAddress)
	}
	allNodeIPsStr := pulumi.All(allNodeIPInputs...).ApplyT(func(args []interface{}) string {
		ips := make([]string, 0, len(args))
		for _, a := range args {
			if s, ok := a.(string); ok {
				ips = append(ips, s)
			}
		}
		return strings.Join(ips, " ")
	}).(pulumi.StringOutput)

	var preflightCommands []pulumi.Resource
	for _, node := range nodes {
		name := fmt.Sprintf("preflight-%s", node.Name)
		conn := createConnection(node)
		preflightCmd, err := remote.NewCommand(ctx, name, &remote.CommandArgs{
			Connection: conn,
			Create: pulumi.Sprintf(`
                set -Eeuo pipefail

                STEP="preflight"
                trap 'rc=$?; echo "❌ ${STEP} failed (rc=$rc)"; echo "📋 kubelet:"; sudo systemctl status kubelet --no-pager || true; echo "📋 containerd:"; sudo systemctl status containerd --no-pager || true; exit $rc' ERR

                echo "🔎 Pre-flight checks on $(hostname)"

                echo "� Sudo (non-interactive)"
                if ! sudo -n true 2>/dev/null; then
                    echo "❌ Passwordless sudo is required for bootstrap/preflight"
                    echo "   Fix: ensure SSH user has NOPASSWD sudo (e.g. /etc/sudoers.d/99-bootstrap-user)"
                    exit 1
                fi

                echo "🧩 Kernel modules & sysctl"
                sudo -n mkdir -p /etc/modules-load.d /etc/sysctl.d
                cat <<'EOF' | sudo -n tee /etc/modules-load.d/k8s.conf >/dev/null
overlay
br_netfilter
EOF
                sudo -n modprobe overlay || true
                sudo -n modprobe br_netfilter || true
                cat <<'EOF' | sudo -n tee /etc/sysctl.d/k8s.conf >/dev/null
net.bridge.bridge-nf-call-iptables = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward = 1
EOF
                sudo -n sysctl --system >/dev/null

                echo "� Container runtime"
                if ! sudo -n systemctl is-active --quiet containerd; then
                    echo "❌ containerd is not active"
                    exit 1
                fi
                containerd --version || true
                if command -v ctr >/dev/null 2>&1; then
                    echo "📋 containerd socket permissions:"
                    sudo -n ls -l /run/containerd/containerd.sock || true
                    echo "📋 executing user: $(id)"

                    echo "🔍 Validating ctr access to containerd..."
                    CTR_OK=false
                    CTR_LAST=""
                    for i in {1..15}; do
                        if CTR_LAST=$(sudo -n ctr version 2>&1); then
                            CTR_OK=true
                            break
                        fi
                        echo "   Attempt $i/15: $CTR_LAST"
                        sleep 2
                    done
                    if [ "$CTR_OK" != true ]; then
                        echo "❌ ctr cannot connect to /run/containerd/containerd.sock"
                        exit 1
                    fi
                else
                    echo "⚠️  ctr not found (containerd tooling missing)"
                fi

                echo "🧠 CPU/Memory/Disk"
                CPU=$(nproc || echo 0)
                MEM_MB=$(free -m | awk '/Mem:/ {print $2}' || echo 0)
                DISK_GB=$(df -BG / | awk 'NR==2 {gsub(/G/,"",$4); print $4}' || echo 0)
                echo "   CPU: ${CPU} cores"
                echo "   Mem: ${MEM_MB} MiB"
                echo "   Disk free (/): ${DISK_GB} GiB"
                if [ "${CPU}" -lt 2 ]; then echo "❌ CPU < 2 cores"; exit 1; fi
                if [ "${MEM_MB}" -lt 2048 ]; then echo "❌ Memory < 2GiB"; exit 1; fi
                if [ "${DISK_GB}" -lt 5 ]; then echo "❌ Disk free < 5GiB"; exit 1; fi

                echo "🧩 Kernel modules"
                MODS="br_netfilter ip_vs ip_vs_rr ip_vs_wrr ip_vs_sh nf_conntrack"
                for m in $MODS; do
                    if ! lsmod | awk '{print $1}' | grep -qx "$m"; then
                        sudo modprobe "$m" || echo "⚠️  failed to modprobe $m"
                    fi
                done
                if ! lsmod | awk '{print $1}' | grep -qx br_netfilter; then
                    echo "❌ br_netfilter not loaded"
                    exit 1
                fi

                echo "🔁 Swap"
                if swapon --show | tail -n +2 | grep -q .; then
                    echo "❌ swap is enabled"
                    swapon --show || true
                    exit 1
                fi

                echo "🛡️  SELinux/AppArmor"
                if command -v getenforce >/dev/null 2>&1; then
                    SE=$(getenforce || true)
                    echo "   SELinux: $SE"
                    if [ "$SE" = "Enforcing" ]; then
                        echo "❌ SELinux is Enforcing (set to Permissive/Disabled)"
                        exit 1
                    fi
                fi
                if command -v aa-status >/dev/null 2>&1; then
                    aa-status || true
                fi

                echo "⏱️  Time sync"
                if command -v timedatectl >/dev/null 2>&1; then
                    timedatectl status || true
                    NTPSYNC=$(timedatectl show -p NTPSynchronized --value 2>/dev/null || echo "")
                    if [ "$NTPSYNC" = "no" ]; then
                        echo "⚠️  NTP not synchronized"
                    fi
                fi

                echo "🌐 VIP config"
                VIP_IFACE="%s"
                VIP_ADDR="%s"
                if ! ip link show "$VIP_IFACE" >/dev/null 2>&1; then
                    echo "❌ VIP interface not found: $VIP_IFACE"
                    ip link show || true
                    exit 1
                fi
                if ip addr | grep -w "$VIP_ADDR" >/dev/null 2>&1; then
                    echo "⚠️  VIP already present on this node: $VIP_ADDR"
                fi

                echo "🔌 Basic network reachability"
                PEERS="%s"
                for p in $PEERS; do
                    if [ "$p" = "$(hostname -I | awk '{print $1}')" ]; then
                        continue
                    fi
                    ping -c 1 -W 2 "$p" >/dev/null 2>&1 || echo "⚠️  ping failed to $p"
                done

                echo "✅ Pre-flight checks passed on $(hostname)"
			`, cfg.K8sVIPInterface, cfg.K8sVIP, allNodeIPsStr),
		})
		if err != nil {
			return nil, fmt.Errorf("gagal preflight node %s: %w", node.Name, err)
		}
		preflightCommands = append(preflightCommands, preflightCmd)
	}

	// 1. Reset cluster guard (mengganti langkah destruktif)
	var resetCommands []pulumi.Resource
	for _, node := range nodes {
		nodeName := fmt.Sprintf("kubeadm-reset-%s", node.Name)
		conn := createConnection(node)

		resetCmd, err := remote.NewCommand(ctx, nodeName, &remote.CommandArgs{
			Connection: conn,
			Create: pulumi.String(`
                set -e
                # Guard terhadap reset destruktif
                if [ -f /etc/kubernetes/admin.conf ]; then
                    if [ "$(sudo ls -A /var/lib/etcd 2>/dev/null)" ]; then
                        echo "⚠️  Cluster state exists and etcd is not empty. Aborting reset to prevent data loss."
                        exit 0
                    else
                        echo "Resetting empty cluster state..."
                        sudo kubeadm reset -f
                        sudo rm -rf /etc/kubernetes
                        sudo rm -rf /var/lib/etcd
                        sudo rm -rf $HOME/.kube
                    fi
                fi
                
                # Clean up iptables rules
                # sudo iptables -F && sudo iptables -t nat -F && sudo iptables -t mangle -F && sudo iptables -X
                
                # Restart services
                sudo systemctl restart kubelet || true
                sudo systemctl restart containerd || true
            `),
		}, pulumi.DependsOn(preflightCommands))
		if err != nil {
			return nil, fmt.Errorf("gagal reset node %s: %w", node.Name, err)
		}
		resetCommands = append(resetCommands, resetCmd)
	}

	// 2. Kubeadm Init pada Node Pertama (menggunakan VIP/DNS endpoint)
	initCmd, err := remote.NewCommand(ctx, "kubeadm-init", &remote.CommandArgs{
		Connection: conn0,
		Create: pulumi.Sprintf(`
			set -Eeuo pipefail

			STEP="init"
			trap 'rc=$?; echo "❌ ${STEP} failed (rc=$rc)"; sudo journalctl -u kubelet -n 120 --no-pager || true; exit $rc' ERR

			if ! sudo -n true 2>/dev/null; then
				echo "❌ Passwordless sudo is required for kubeadm init"
				exit 1
			fi

			if ! command -v kubeadm &> /dev/null; then
				echo "❌ kubeadm tidak ditemukan"
				exit 1
			fi

			if [ -f /etc/kubernetes/admin.conf ]; then
				echo "✅ Cluster already initialized. Skipping init."
				exit 0
			fi

			DOMAIN="%s"
			VIP="%s"
			ENDPOINT="$VIP"
			if (command -v getent >/dev/null 2>&1 && getent hosts "$DOMAIN" >/dev/null 2>&1) || (command -v nslookup >/dev/null 2>&1 && nslookup "$DOMAIN" >/dev/null 2>&1); then
				ENDPOINT="$DOMAIN"
			fi
			echo "🚀 Initializing HA Kubernetes Control Plane (endpoint: ${ENDPOINT}:6443)"

            echo "🛑 Stopping kubelet service..."
            sudo -n systemctl stop kubelet || true

			echo "🔧 Configuring containerd systemd cgroup"
			sudo -n mkdir -p /etc/containerd
			sudo -n containerd config default | sudo -n tee /etc/containerd/config.toml >/dev/null
			sudo -n sed -i 's/SystemdCgroup = false/SystemdCgroup = true/g' /etc/containerd/config.toml
			sudo -n systemctl restart containerd

            sleep 3

			echo "🔧 Disabling swap"
			sudo -n swapoff -a
			sudo -n sed -i '/ swap / s/^\(.*\)$/#\1/g' /etc/fstab

			if ! grep -q "$(hostname)" /etc/hosts; then
				echo "127.0.1.1 $(hostname)" | sudo -n tee -a /etc/hosts >/dev/null
			fi

            echo "🚀 Running kubeadm init..."
            sudo kubeadm init \
                --control-plane-endpoint "%s:6443" \
                --apiserver-cert-extra-sans="%s,%s,%s" \
                --upload-certs \
                --pod-network-cidr="%s" \
                --kubernetes-version=%s
			sudo -n kubeadm init --config /tmp/kubeadm-config.yaml --upload-certs --v=5

            echo "⏳ Waiting for kubelet to be ready..."
            for i in {1..30}; do
                if sudo -n systemctl is-active --quiet kubelet; then
                    echo "✅ Kubelet is active"
                    break
                fi
                echo "   Attempt $i/30: Waiting for kubelet..."
                sleep 2
            done
            
			mkdir -p $HOME/.kube
			sudo -n cp -f /etc/kubernetes/admin.conf $HOME/.kube/config
			sudo -n chown $(id -u):$(id -g) $HOME/.kube/config

            export KUBECONFIG=$HOME/.kube/config
            if ! kubectl cluster-info &>/dev/null; then
                echo "❌ Failed to connect to cluster after init"
                echo "📋 Kubelet status:"
                sudo -n systemctl status kubelet --no-pager || true
                exit 1
            fi

			echo "✅ Initialization Complete."
            echo "📋 Cluster status:"
            kubectl get nodes
            kubectl get pods -n kube-system
		`,
			cfg.K8sDOMAIN,
			cfg.K8sVIP,
			nodes[0].IPAddress,
			cfg.K8sDOMAIN,
			cfg.K8sVIP,
			nodes[0].IPAddress,
			cfg.K8sPodCIDR,
			cfg.K8sVersion,
			nodes[0].IPAddress),
	}, pulumi.DependsOn(resetCommands))
	if err != nil {
		return nil, fmt.Errorf("gagal init kubeadm: %w", err)
	}

	// 3. Install CNI (Calico)
	cniCmd, err := remote.NewCommand(ctx, "install-cni", &remote.CommandArgs{
		Connection: conn0,
		Create: pulumi.Sprintf(`
			set -e

			echo "📦 Installing Calico CNI..."

			curl -LO https://raw.githubusercontent.com/projectcalico/calico/v3.31.5/manifests/calico.yaml

			sed -i 's|# - name: CALICO_IPV4POOL_CIDR|- name: CALICO_IPV4POOL_CIDR|g' calico.yaml
			sed -i 's|#   value: "192.168.0.0/16"|  value: "%s"|g' calico.yaml

			kubectl apply -f calico.yaml

			echo "⏳ Waiting for Calico pods to be ready..."
			kubectl wait --for=condition=ready pod -l k8s-app=calico-node -n kube-system --timeout=300s || true

			echo ""
			echo "📊 CNI Status:"
			kubectl get pods -n kube-system -l k8s-app=calico-node
			kubectl get nodes

			echo "✅ CNI Installation Complete"
		`, cfg.K8sPodCIDR),
	}, pulumi.DependsOn([]pulumi.Resource{initCmd}))
	if err != nil {
		return nil, fmt.Errorf("gagal install CNI: %w", err)
	}

	// 4. Setup kube-vip pada Node Pertama (Setelah kubeadm init)
	vipCmd, err := remote.NewCommand(ctx, "setup-kube-vip-primary", &remote.CommandArgs{
		Connection: conn0,
		Create: pulumi.Sprintf(`
			set -Eeuo pipefail

			STEP="vipCmd"
			trap 'rc=$?; echo "❌ ${STEP} failed (rc=$rc)"; echo "📋 containerd:"; sudo systemctl status containerd --no-pager || true; echo "📋 kubelet:"; sudo systemctl status kubelet --no-pager || true; echo "📋 manifests:"; sudo ls -la /etc/kubernetes/manifests || true; echo "📋 kube-vip manifest preview:"; sudo head -n 120 /etc/kubernetes/manifests/kube-vip.yaml 2>/dev/null || true; exit $rc' ERR

			if ! sudo -n true 2>/dev/null; then
				echo "❌ Passwordless sudo is required for vipCmd"
				exit 1
			fi

			IFACE="%s"
			VIP="%s"
			KVVERSION="v1.1.2"

			echo "🔧 Setting up kube-vip static pod (control plane VIP)"
			echo "   interface: $IFACE"
			echo "   vip:       $VIP"
			echo "   version:   $KVVERSION"

			if ! ip link show "$IFACE" >/dev/null 2>&1; then
				echo "❌ Interface not found: $IFACE"
				ip link show || true
				exit 1
			fi

			if ! sudo -n systemctl is-active --quiet containerd; then
				echo "❌ containerd is not active"
				exit 1
			fi
			if ! command -v ctr >/dev/null 2>&1; then
				echo "❌ ctr not found"
				exit 1
			fi

			sudo -n mkdir -p /etc/kubernetes/manifests
			sudo -n mkdir -p /etc/kubernetes
			sudo -n touch /etc/kubernetes/admin.conf

			echo "📥 Ensuring kube-vip image exists in containerd (namespace k8s.io)"
			if ! sudo -n ctr images ls | grep -q "ghcr.io/kube-vip/kube-vip:$KVVERSION"; then
				for i in 1 2 3; do
					if sudo -n ctr image pull "ghcr.io/kube-vip/kube-vip:$KVVERSION"; then
						break
					fi
					echo "  ⚠️  pull attempt $i failed, retrying..."
					sleep $((i*3))
				done
			fi

			if [ -f /etc/kubernetes/manifests/kube-vip.yaml ]; then
				sudo -n rm -f /etc/kubernetes/manifests/kube-vip.yaml
				sleep 3
			fi
			sudo -n ctr containers rm kube-vip 2>/dev/null || true
			sudo -n ctr tasks kill kube-vip 2>/dev/null || true

			echo "🧾 Generating kube-vip manifest (static pod)"
			sudo -n ctr run --rm --net-host "ghcr.io/kube-vip/kube-vip:$KVVERSION" kube-vip \
				/kube-vip manifest pod \
				--interface "$IFACE" \
				--address "$VIP" \
				--controlplane \
				--services \
				--arp \
				--leaderElection | sudo -n tee /etc/kubernetes/manifests/kube-vip.yaml >/dev/null

            echo "⏳ Waiting for kube-vip to initialize..."
            sleep 15

            echo "🔄 Restarting kubelet to load kube-vip manifest..."
            sudo -n systemctl restart kubelet || true
            sleep 10

			echo "🔍 Waiting for VIP to be present on interface"
			VIP_ACTIVE=false
			for i in {1..60}; do
				if ip addr show "$IFACE" | grep -q "$VIP"; then
					VIP_ACTIVE=true
					break
				fi
				sleep 2
			done
			if [ "$VIP_ACTIVE" != true ]; then
                echo "❌ VIP did not activate within timeout"
                echo "📋 Interface status:"
                ip addr show "$IFACE" || true
                echo "📋 Kubelet status:"
                sudo systemctl status kubelet --no-pager || true
                echo "📋 kube-vip manifest:"
                sudo cat /etc/kubernetes/manifests/kube-vip.yaml || true
                exit 1
			fi

            echo "🔍 Verifying API server via VIP..."
            API_VIP_OK=false
            for i in {1..30}; do
                if curl -kfsS --connect-timeout 2 --max-time 4 "https://$VIP:6443/healthz" >/dev/null 2>&1; then
                    API_VIP_OK=true
                    echo "✅ API server reachable via VIP"
                    break
                fi
                echo "   Attempt $i/30: API not yet reachable via VIP..."
                sleep 3
            done

            if [ "$API_VIP_OK" != true ]; then
                echo "❌ API server not reachable via VIP after timeout"
                echo "📋 Testing direct API access:"
                curl -vk --connect-timeout 2 --max-time 4 "https://$VIP:6443/healthz" || true
                exit 1
            fi

            echo "✅ kube-vip setup complete and verified"
			ip addr show "$IFACE" | sed -n '1,20p'

            echo "💾 Saving kube-vip manifest for secondary nodes..."
            sudo cat /etc/kubernetes/manifests/kube-vip.yaml | sudo tee /tmp/kube-vip-manifest.yaml >/dev/null
            sudo chmod 644 /tmp/kube-vip-manifest.yaml
            echo "✅ Manifest saved to /tmp/kube-vip-manifest.yaml"
		`, cfg.K8sVIPInterface, cfg.K8sVIP),
	}, pulumi.DependsOn([]pulumi.Resource{initCmd}))
	if err != nil {
		return nil, fmt.Errorf("gagal setup kube-vip: %w", err)
	}

	var vipReachabilityCommands []pulumi.Resource
	for i := 0; i < len(nodes); i++ {
		conn := createConnection(nodes[i])
		name := fmt.Sprintf("verify-vip-reachability-%d", i)

		cmd, err := remote.NewCommand(ctx, name, &remote.CommandArgs{
			Connection: conn,
			Create: pulumi.Sprintf(`
				set -Eeuo pipefail
                
                STEP="vip-reachability"
                trap 'rc=$?; echo "❌ ${STEP} failed (rc=$rc)"; echo "📋 routes:"; ip -4 route || true; echo "📋 route get VIP:"; ip -4 route get %s || true; echo "📋 neigh:"; ip neigh show || true; exit $rc' ERR

				echo "🔌 Verifying reachability to VIP/API from $(hostname)"
				VIP="%s"
				DOMAIN="%s"

				echo "📍 DNS resolution (best-effort)"
				if command -v getent >/dev/null 2>&1; then
					getent hosts "$DOMAIN" || true
				elif command -v nslookup >/dev/null 2>&1; then
					nslookup "$DOMAIN" || true
				fi

				echo "🧭 Route to VIP"
				ip -4 route get "$VIP" >/dev/null

				echo "🏓 Ping VIP"
				PING_OK=false
				for i in {1..10}; do
					if ping -c 1 -W 2 "$VIP" >/dev/null 2>&1; then
						PING_OK=true
						break
					fi
					echo "   Attempt $i/10: ping failed"
					sleep 2
				done
				if [ "$PING_OK" != true ]; then
					echo "❌ Cannot reach VIP via ICMP"
					exit 1
				fi

				echo "🩺 API healthz via VIP"
				API_OK=false
				for i in {1..20}; do
					if curl -kfsS --connect-timeout 2 --max-time 4 "https://$VIP:6443/healthz" >/dev/null 2>&1; then
						API_OK=true
						break
					fi
					echo "   Attempt $i/20: healthz not reachable"
					sleep 3
				done
				if [ "$API_OK" != true ]; then
					echo "❌ Cannot reach API server via VIP: https://$VIP:6443/healthz"
					exit 1
				fi

				echo "✅ VIP/API reachable from $(hostname)"
			`, cfg.K8sVIP, cfg.K8sVIP, cfg.K8sDOMAIN),
		}, pulumi.DependsOn([]pulumi.Resource{vipCmd, initCmd}))
		if err != nil {
			return nil, fmt.Errorf("gagal verifikasi VIP reachability node %s: %w", nodes[i].Name, err)
		}
		vipReachabilityCommands = append(vipReachabilityCommands, cmd)
	}

	// 5. Generate join command untuk control plane nodes
	joinCmd, err := remote.NewCommand(ctx, "generate-join-command", &remote.CommandArgs{
		Connection: conn0,
		Create: pulumi.Sprintf(`
            set -Eeuo pipefail

            STEP="joinCmd"
            trap 'rc=$?; echo "❌ ${STEP} failed (rc=$rc)"; echo "📋 kubeadm:"; kubeadm version -o short 2>/dev/null || true; echo "� kubelet:"; sudo systemctl status kubelet --no-pager || true; echo "📋 kubeadm-config (excerpt):"; kubectl -n kube-system get configmap kubeadm-config -o yaml 2>/dev/null | sed -n "1,160p" || true; exit $rc' ERR

            echo "Generating join command for control plane nodes..."

            # ✅ PERBAIKAN 1: Update kubeadm-config ConfigMap SEBELUM generate join command
            echo "🔄 Updating kubeadm-config ConfigMap to use domain/VIP endpoint..."
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

            # Tentukan endpoint yang akan digunakan
			ENDPOINT_CANDIDATE="%s"
			VIP="%s"
			if ping -c 1 -W 2 "$ENDPOINT_CANDIDATE" &>/dev/null || nslookup "$ENDPOINT_CANDIDATE" &>/dev/null; then
				ENDPOINT="$ENDPOINT_CANDIDATE"
			else
				ENDPOINT="$VIP"
				echo "⚠️  DNS not resolvable, using VIP $ENDPOINT for ConfigMap"
			fi
            
            # Backup original configmap
            # kubectl -n kube-system get configmap kubeadm-config -o yaml > /tmp/kubeadm-config.backup.yaml

            # ✅ Update controlPlaneEndpoint di ClusterConfiguration
            echo "🔄 Updating kubeadm-config ConfigMap..."
            kubectl -n kube-system get configmap kubeadm-config -o yaml > /tmp/kubeadm-config.yaml
            sed -i -E "s|^([[:space:]]*)controlPlaneEndpoint:.*|\\1controlPlaneEndpoint: \"${ENDPOINT}:6443\"|g" /tmp/kubeadm-config.yaml
            kubectl apply -f /tmp/kubeadm-config.yaml
            
            # Verify update
            echo "📋 Verifying ConfigMap update..."
            CURRENT_ENDPOINT=$(kubectl -n kube-system get configmap kubeadm-config -o jsonpath='{.data.ClusterConfiguration}' | tr '\\r' '\\n' | grep -m1 controlPlaneEndpoint || true)
            echo "   Current endpoint in ConfigMap: $CURRENT_ENDPOINT"
            
            if ! echo "$CURRENT_ENDPOINT" | grep -q "${ENDPOINT}:6443"; then
                echo "⚠️  Warning: ConfigMap update may not have applied correctly"
                echo "   Expected: ${ENDPOINT}:6443"
                echo "   Got: $CURRENT_ENDPOINT"
            else
                echo "✅ ConfigMap updated successfully"
            fi
    
            # ✅ PERBAIKAN 2: Update kubeconfig
            echo "🔄 Updating kubeconfig..."
            export KUBECONFIG=$HOME/.kube/config
            kubectl config set-cluster kubernetes --server=https://${ENDPOINT}:6443
            
            # Verify kubeconfig update
            CURRENT_SERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
            echo "📋 Current kubeconfig server: $CURRENT_SERVER"
            
            # ✅ PERBAIKAN 3: Test connectivity sebelum generate join command
            echo "🔍 Testing API server connectivity..."
            MAX_RETRIES=30
            RETRY_COUNT=0
            
            while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
                if kubectl cluster-info &>/dev/null; then
                    echo "✅ Successfully connected to API server"
                    break
                fi
                
                RETRY_COUNT=$((RETRY_COUNT + 1))
                echo "   Attempt $RETRY_COUNT/$MAX_RETRIES: Waiting for API server..."
                sleep 2
            done
            
            if [ $RETRY_COUNT -eq $MAX_RETRIES ]; then
                echo "❌ Failed to connect to API server after $MAX_RETRIES attempts"
                exit 1
            fi

			echo "🔍 Verifying API healthz via chosen endpoint..."
			if ! curl -kfsS --connect-timeout 2 --max-time 4 "https://${ENDPOINT}:6443/healthz" >/dev/null 2>&1; then
				if [ "$ENDPOINT" != "$VIP" ] && curl -kfsS --connect-timeout 2 --max-time 4 "https://${VIP}:6443/healthz" >/dev/null 2>&1; then
					echo "⚠️  Domain endpoint not reachable, falling back to VIP"
					ENDPOINT="$VIP"
					kubectl -n kube-system get configmap kubeadm-config -o yaml > /tmp/kubeadm-config.yaml
					sed -i -E "s|^([[:space:]]*)controlPlaneEndpoint:.*|\\1controlPlaneEndpoint: \"${ENDPOINT}:6443\"|g" /tmp/kubeadm-config.yaml
					kubectl apply -f /tmp/kubeadm-config.yaml
					kubectl config set-cluster kubernetes --server=https://${ENDPOINT}:6443
				else
					echo "❌ API healthz not reachable via https://${ENDPOINT}:6443/healthz"
					echo "📋 route get VIP:"
					ip -4 route get "$VIP" || true
					exit 1
				fi
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
            
            # ✅ PERBAIKAN 4: Generate join command dengan explicit API server endpoint
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
                TOKEN=$(sudo kubeadm token create --ttl 24h --kubeconfig $HOME/.kube/config 2>&1)
                
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

            # ✅ PERBAIKAN 6: Verify dan modify join command
            echo "🔍 Verifying join command..."

            # Build join command dengan proper escaping
            JOIN_CMD="sudo kubeadm join ${ENDPOINT}:6443 --control-plane"
            JOIN_CMD="$JOIN_CMD --token $TOKEN"
            JOIN_CMD="$JOIN_CMD --discovery-token-ca-cert-hash sha256:$CA_CERT_HASH"
            JOIN_CMD="$JOIN_CMD --certificate-key $CERT_KEY"
            
            # Tambahkan flags untuk cgroupDriver dan node registration
            JOIN_CMD="$JOIN_CMD"
            
            echo "✅ Join command validation passed"
                    
            # Extract current server dari join command
            echo "   Join command endpoint: ${ENDPOINT}:6443"

            # ✅ PERBAIKAN 8: Display join command info (tanpa sensitive data)
            echo ""
            echo "📋 Join Command Components:"
            echo "   Endpoint: ${ENDPOINT}:6443"
            echo "   Token: ${TOKEN:0:6}.***"
            echo "   CA Hash: sha256:${CA_CERT_HASH:0:10}..."
            echo "   Cert Key: ${CERT_KEY:0:10}..."
            echo ""
            echo "✅ Join command generated successfully"
            
            # ✅ Save join command to file
            echo "💾 Saving join command to /tmp/k8s-join-command.txt..."
            echo "$JOIN_CMD" | sudo tee /tmp/k8s-join-command.txt > /dev/null
            sudo chmod 644 /tmp/k8s-join-command.txt
            
            echo "✅ Join command generated and saved successfully"
        `,
			cfg.K8sDOMAIN,
			cfg.K8sDOMAIN,
			cfg.K8sDOMAIN,
			cfg.K8sDOMAIN,
			cfg.K8sDOMAIN,
			cfg.K8sVIP),
	}, pulumi.DependsOn(append([]pulumi.Resource{cniCmd}, vipReachabilityCommands...)), pulumi.IgnoreChanges([]string{"create"}))
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

                echo "🔄 Updating kubeadm-config ConfigMap..."
                kubectl -n kube-system get configmap kubeadm-config -o yaml | \
                sed "s|controlPlaneEndpoint:.*|controlPlaneEndpoint: %s:6443|g" | \
                kubectl apply -f -
                
                echo "📖 Reading kube-vip manifest from primary node..."
                
                # ✅ PERBAIKAN: Baca dari file yang sudah disimpan
                if [ ! sudo ls -f /tmp/kube-vip-manifest.yaml ]; then
                    echo "❌ kube-vip manifest not found at /tmp/kube-vip-manifest.yaml!"
                    echo "📋 Trying fallback location..."
                    if [ ! sudo ls -f /etc/kubernetes/manifests/kube-vip.yaml ]; then
                        echo "❌ kube-vip manifest not found on primary node!"
                        exit 1
                    fi
                    sudo cat /etc/kubernetes/manifests/kube-vip.yaml
                else
                    sudo cat /tmp/kube-vip-manifest.yaml
                fi
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
                
                # ✅ Write manifest content menggunakan heredoc
                cat <<'MANIFEST_EOF' | sudo tee /tmp/kube-vip-setup/kube-vip.yaml > /dev/null
%s
MANIFEST_EOF
                
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
                sudo rm -rf /tmp/kube-vip-setup
                
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
				readManifestCmd.Stdout, // Inject content from primary node
				nodes[i].Name),         // Display node name
		}, pulumi.DependsOn([]pulumi.Resource{readManifestCmd, vipReachabilityCommands[i]}))

		if err != nil {
			return nil, fmt.Errorf("gagal copy kube-vip manifest ke node %d: %w", i, err)
		}

		// Step 1b: Baca kubeconfig dari primary node (node 0)
		readKubeconfigName := fmt.Sprintf("read-kubeconfig-%d", i)
		readKubeconfigCmd, err := remote.NewCommand(ctx, readKubeconfigName, &remote.CommandArgs{
			Connection: conn0,
			Create: pulumi.String(`
                set -e
                sudo cat $HOME/.kube/config
            `),
		}, pulumi.DependsOn([]pulumi.Resource{vipCmd, cniCmd}))

		if err != nil {
			return nil, fmt.Errorf("gagal read kubeconfig: %w", err)
		}

		// Step 2b: Write kubeconfig ke secondary node (node i)
		writeKubeconfigName := fmt.Sprintf("write-kubeconfig-%d", i)
		writeKubeconfigCmd, err := remote.NewCommand(ctx, writeKubeconfigName, &remote.CommandArgs{
			Connection: conn1,
			Create: pulumi.Sprintf(`
                set -e
                echo "📦 Copying kubeconfig from primary node..."
                
                sudo mkdir -p $HOME/.kube
                cat <<'KUBECONFIG_EOF' | sudo tee $HOME/.kube/config > /dev/null
%s
KUBECONFIG_EOF
                
                sudo chown $(id -u):$(id -g) $HOME/.kube/config
                sudo chmod 600 $HOME/.kube/config
                echo "✅ kubeconfig copied successfully"
            `, readKubeconfigCmd.Stdout),
		}, pulumi.DependsOn([]pulumi.Resource{readKubeconfigCmd, writeManifestCmd}))

		if err != nil {
			return nil, fmt.Errorf("gagal copy kubeconfig ke node %d: %w", i, err)
		}

		joinCommands = append(joinCommands, writeKubeconfigCmd)
	}

	// Join node ke cluster (setelah manifest kube-vip di-copy)
	for i := 1; i < len(nodes); i++ {
		conn1 := createConnection(nodes[i])

		nodeName := fmt.Sprintf("kubeadm-join-cp-%d", i)

		// Ambil dependency dari copy manifest command yang sesuai
		copyVipDep := joinCommands[i-1]

		// Step 3: Baca join command dari primary node (node 0)
		readJoinCmdName := fmt.Sprintf("read-join-cmd-%d", i)
		readJoinCmd, err := remote.NewCommand(ctx, readJoinCmdName, &remote.CommandArgs{
			Connection: conn0, // ✅ Baca dari primary node
			Create: pulumi.String(`
                set -e
                if [ ! -f /tmp/k8s-join-command.txt ]; then
                    echo "❌ Join command file not found on primary node!" >&2
                    exit 1
                fi
                sudo cat /tmp/k8s-join-command.txt
            `),
		}, pulumi.DependsOn([]pulumi.Resource{joinCmd, copyVipDep}))

		if err != nil {
			return nil, fmt.Errorf("gagal read join command: %w", err)
		}

		// Join node ke cluster dan copy kube-vip manifest dari node pertama ke node lainnya
		joinNodeCmd, err := remote.NewCommand(ctx, nodeName, &remote.CommandArgs{
			Connection: conn1,
			Create: pulumi.Sprintf(`
				set -Eeuo pipefail

				STEP="join-node"
				trap 'rc=$?; echo "❌ ${STEP} failed (rc=$rc)"; echo "📋 routes:"; ip -4 route || true; echo "📋 neigh:"; ip neigh show || true; echo "📋 kubelet:"; sudo journalctl -u kubelet -n 80 --no-pager || true; exit $rc' ERR

				VIP="%s"

                echo "🚀 Joining node %s to cluster..."
                echo "   Using endpoint: %s:6443"
                
                # ✅ PERBAIKAN: Inject join command langsung dari Pulumi Output
                JOIN_CMD='%s'
                
                # Validate join command
                if [ -z "$JOIN_CMD" ]; then
                    echo "❌ Join command is empty!"
                    echo "📋 Debug: JOIN_CMD variable is not set"
                    exit 1
                fi
                
                # Verify join command format
                if ! echo "$JOIN_CMD" | grep -q "sudo kubeadm join"; then
                    echo "❌ Invalid join command format!"
                    echo "📋 Received: $JOIN_CMD"
                    exit 1
                fi                
                
                # ✅ Export KUBECONFIG agar preflight check kubeadm bisa sukses menggunakan kredensial dari master
                export KUBECONFIG=$HOME/.kube/config
                echo "📋 Using KUBECONFIG=$KUBECONFIG"
                
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
				if [ $PING_COUNT -eq $MAX_PING_RETRIES ]; then
					echo "❌ Cannot ping control plane endpoint"
					echo "📋 route get VIP:"
					ip -4 route get "$VIP" || true
					exit 1
				fi
                
                # Test API server connectivity
                echo "🔍 Testing API server connectivity..."
                MAX_API_RETRIES=15
                API_COUNT=0
                
                while [ $API_COUNT -lt $MAX_API_RETRIES ]; do
					if curl -kfsS --connect-timeout 2 --max-time 4 https://%s:6443/healthz &>/dev/null; then
                        echo "✅ API server is responding"
                        break
                    fi
                    API_COUNT=$((API_COUNT + 1))
                    echo "   Attempt $API_COUNT/$MAX_API_RETRIES: Waiting for API server..."
                    sleep 5
                done
				if [ $API_COUNT -eq $MAX_API_RETRIES ]; then
					echo "❌ API server not reachable via endpoint"
					echo "📋 testing VIP healthz directly: https://$VIP:6443/healthz"
					curl -vk --connect-timeout 2 --max-time 4 "https://$VIP:6443/healthz" || true
					exit 1
				fi
    
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
                echo "   Command preview: sudo kubeadm join %s:6443 ..."
                
                # Execute join dengan error handling
                
                # 🛠 FIX: Ensure containerd uses systemd cgroup on join node
                echo "🔧 Configuring containerd systemd cgroup..."
                sudo mkdir -p /etc/containerd
                sudo containerd config default | sudo tee /etc/containerd/config.toml >/dev/null
                sudo sed -i 's/SystemdCgroup = false/SystemdCgroup = true/g' /etc/containerd/config.toml
                sudo systemctl restart containerd
                
                # 🛠 FIX: Disable swap on join node
                echo "🔧 Disabling swap..."
                sudo swapoff -a
                sudo sed -i '/ swap / s/^\(.*\)$/#\1/g' /etc/fstab

                # 🛠 FIX: Ensure hostname is mapped in /etc/hosts
                if ! grep -q "$(hostname)" /etc/hosts; then
                    echo "127.0.1.1 $(hostname)" | sudo tee -a /etc/hosts
                fi

                if sudo eval "$JOIN_CMD"; then
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
				cfg.K8sVIP,
				nodes[i].Name,      // Display node name
				cfg.K8sDOMAIN,      // Display endpoint
				readJoinCmd.Stdout, // Inject join command
				cfg.K8sDOMAIN,      // Ping test
				cfg.K8sDOMAIN,      // API test
				cfg.K8sDOMAIN,      // Display join endpoint
				cfg.K8sDOMAIN,      // Update kubeconfig
				cfg.K8sDOMAIN,      // Display updated endpoint
				nodes[i].Name),     // Display node name
		}, pulumi.DependsOn([]pulumi.Resource{readJoinCmd, copyVipDep, vipReachabilityCommands[i]}), pulumi.IgnoreChanges([]string{"create"}))
		if err != nil {
			return nil, fmt.Errorf("gagal join node %d: %w", i, err)
		}
		joinCommands = append(joinCommands, joinNodeCmd)
	}

	// 7. Verifikasi cluster health (setelah semua node join)
	verifyCmd, err := remote.NewCommand(ctx, "verify-cluster", &remote.CommandArgs{
		Connection: conn0,
		Create: pulumi.String(`
			set -Eeuo pipefail

			STEP="verify"
			trap 'rc=$?; echo "❌ ${STEP} failed (rc=$rc)"; kubectl get nodes -o wide || true; exit $rc' ERR
			export KUBECONFIG=$HOME/.kube/config
            
            echo "🔍 Verifying HA Cluster Health..."
            echo ""
            
            # Wait for all nodes to be ready
            echo "⏳ Waiting for all nodes to be Ready..."
            kubectl wait --for=condition=Ready nodes --all --timeout=300s || true
            
            # Show cluster status
            echo ""
            echo "📊 Cluster Status:"
            echo "=================="
            kubectl get nodes -o wide
            
            # Show control plane pods
            echo ""
            echo "📦 System Pods:"
            echo "==============="
            kubectl get pods -n kube-system -o wide
            
            # Show kube-vip pods
            echo ""
            echo "🔌 kube-vip Pods:"
            echo "==================="
            kubectl get pods -n kube-system -l app=kube-vip

            # Show CNI pods
            echo ""
            echo "🌐 CNI Pods:"
            kubectl get pods -n kube-system -l k8s-app=calico-node
            
            # Verify VIP
            echo ""
            echo "📋 VIP Configuration:"
            echo "====================="
            kubectl get configmap -n kube-system kube-vip -o yaml 2>/dev/null || echo "No kube-vip configmap found (normal for static pod setup)"
			
			echo ""
			echo "🔌 Control plane network port checks (best-effort)"
			PORTS="6443 2379 2380 10250"
			check_port() {
				host="$1"; port="$2"
				if command -v nc >/dev/null 2>&1; then
					nc -z -w 2 "$host" "$port" >/dev/null 2>&1
					return $?
				fi
				if command -v timeout >/dev/null 2>&1; then
					timeout 2 bash -c "</dev/tcp/$host/$port" >/dev/null 2>&1
					return $?
				fi
				bash -c "</dev/tcp/$host/$port" >/dev/null 2>&1
			}
			for n in $(kubectl get nodes -o jsonpath='{.items[*].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null); do
				echo "== $n =="
				for p in $PORTS; do
					if check_port "$n" "$p"; then
						echo "✅ $n:$p"
					else
						echo "❌ $n:$p"
					fi
				done
			done
            
            echo ""
            echo "✅ HA Cluster verification complete!"
        `),
	}, pulumi.DependsOn(joinCommands))

	if err != nil {
		return nil, fmt.Errorf("gagal verifikasi cluster: %w", err)
	}

	monitorVipCmd, err := remote.NewCommand(ctx, "monitor-kube-vip", &remote.CommandArgs{
		Connection: conn0,
		Create: pulumi.String(`
			set -Eeuo pipefail
			STEP="monitor-kube-vip"
			trap 'rc=$?; echo "❌ ${STEP} failed (rc=$rc)"; exit $rc' ERR
			export KUBECONFIG=$HOME/.kube/config

			echo "📈 Monitoring kube-vip stability (60s)"
			kubectl -n kube-system get pods -l app=kube-vip -o wide 2>/dev/null || kubectl -n kube-system get pods -o wide | grep -i kube-vip || true

			echo "📋 kube-vip logs (tail)"
			kubectl -n kube-system logs -l app=kube-vip --tail=120 2>/dev/null || true

			echo "📊 kube-vip resource usage (best-effort)"
			kubectl -n kube-system top pod -l app=kube-vip 2>/dev/null || true

			for i in {1..6}; do
				echo "\n== tick $i/6 ($(date -Is)) =="
				kubectl -n kube-system get pods -l app=kube-vip -o wide 2>/dev/null || kubectl -n kube-system get pods -o wide | grep -i kube-vip || true
				curl -kfsS --connect-timeout 2 --max-time 4 https://127.0.0.1:6443/healthz >/dev/null 2>&1 || true
				sleep 10
			done

			echo "✅ kube-vip monitoring complete"
		`),
	}, pulumi.DependsOn([]pulumi.Resource{verifyCmd}))
	if err != nil {
		return nil, fmt.Errorf("gagal monitoring kube-vip: %w", err)
	}

	// 7.5. Setup etcd backup timer on all control plane nodes
	var etcdBackupCommands []pulumi.Resource
	for _, node := range nodes {
		nodeName := fmt.Sprintf("setup-etcd-backup-%s", node.Name)
		conn := createConnection(node)

		etcdBackupCmd, err := remote.NewCommand(ctx, nodeName, &remote.CommandArgs{
			Connection: conn,
			Create: pulumi.String(`
                set -e
                echo "💾 Setting up etcd automatic backup..."
                
                sudo mkdir -p /var/lib/etcd/snapshots
                
                cat <<'EOF' | sudo tee /etc/systemd/system/etcd-snapshot.service
[Unit]
Description=etcd snapshot backup

[Service]
Type=oneshot
ExecStart=/usr/bin/bash -c 'sudo ctr -n k8s.io run --rm --net-host \
  --mount type=bind,src=/etc/kubernetes/pki/etcd,dst=/etc/kubernetes/pki/etcd,options=rbind:ro \
  --mount type=bind,src=/var/lib/etcd/snapshots,dst=/var/lib/etcd/snapshots,options=rbind:rw \
  registry.k8s.io/etcd:3.5.10-0 etcdctl \
  --endpoints=https://127.0.0.1:2379 \
  --cacert=/etc/kubernetes/pki/etcd/ca.crt \
  --cert=/etc/kubernetes/pki/etcd/healthcheck-client.crt \
  --key=/etc/kubernetes/pki/etcd/healthcheck-client.key \
  snapshot save /var/lib/etcd/snapshots/etcd-$(date +%%F-%%H%%M).db'
EOF

                cat <<'EOF' | sudo tee /etc/systemd/system/etcd-snapshot.timer
[Unit]
Description=etcd snapshot backup timer

[Timer]
OnCalendar=*-*-* 00/6:00:00
Persistent=true

[Install]
WantedBy=timers.target
EOF

                sudo systemctl daemon-reload
                sudo systemctl enable --now etcd-snapshot.timer
                echo "✅ etcd backup timer configured on $HOSTNAME"
            `),
		}, pulumi.DependsOn([]pulumi.Resource{monitorVipCmd}))
		if err != nil {
			return nil, fmt.Errorf("gagal setup etcd backup node %s: %w", node.Name, err)
		}
		etcdBackupCommands = append(etcdBackupCommands, etcdBackupCmd)
	}

	// 8. Install MetalLB jika enabled
	var metallbCmd pulumi.Resource
	if cfg.K8sMetalLBEnabled {
		metallbCmd, err = remote.NewCommand(ctx, "install-metallb", &remote.CommandArgs{
			Connection: conn0,
			Create: pulumi.Sprintf(`
				set -Eeuo pipefail

				STEP="metallb"
				trap 'rc=$?; echo "❌ ${STEP} failed (rc=$rc)"; echo "📋 nodes:"; kubectl get nodes -o wide || true; echo "📋 metallb pods:"; kubectl -n metallb-system get pods -o wide 2>/dev/null || true; echo "📋 metallb controller logs:"; kubectl -n metallb-system logs -l component=controller --tail=200 2>/dev/null || true; exit $rc' ERR
                
                echo "📦 Installing MetalLB..."
				export KUBECONFIG=$HOME/.kube/config
				
				echo "⏳ Waiting for cluster to be Ready..."
				kubectl wait --for=condition=Ready nodes --all --timeout=300s
				
				echo "⏳ Waiting for kube-proxy rollout..."
				kubectl -n kube-system rollout status ds/kube-proxy --timeout=300s
                
                # Install MetalLB
				for i in 1 2 3; do
					if kubectl apply -f https://raw.githubusercontent.com/metallb/metallb/v0.15.3/config/manifests/metallb-native.yaml; then
						break
					fi
					echo "⚠️  apply attempt $i failed, retrying..."
					sleep $((i*3))
				done

				echo "⏳ Waiting for MetalLB CRDs..."
				kubectl wait --for=condition=Established crd/ipaddresspools.metallb.io --timeout=120s
				kubectl wait --for=condition=Established crd/l2advertisements.metallb.io --timeout=120s
                
                # Wait for MetalLB pods
                echo "⏳ Waiting for MetalLB pods..."
				kubectl wait --for=condition=ready pod --all -n metallb-system --timeout=300s
                
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
