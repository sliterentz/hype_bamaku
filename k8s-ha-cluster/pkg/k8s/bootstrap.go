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

                echo "Sudo (non-interactive)"
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

                echo "Container runtime"
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
                if [ "${DISK_GB}" -lt 20 ]; then echo "❌ Disk free < 5GiB"; exit 1; fi

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
			LOCKFILE="/etc/kubernetes/manifests/.pulumi-manifests.lock"
			sudo -n touch "$LOCKFILE"
			TMP_MANIFEST="/tmp/kube-vip.yaml.$$"
			
			if command -v flock >/dev/null 2>&1; then
				sudo -n flock -x "$LOCKFILE" bash -c '
					set -Eeuo pipefail
					TMP_MANIFEST="/tmp/kube-vip.yaml.$$"
					ctr run --rm --net-host "ghcr.io/kube-vip/kube-vip:'"$KVVERSION"'" kube-vip \
						/kube-vip manifest pod \
						--interface "'"$IFACE"'" \
						--address "'"$VIP"'" \
						--controlplane \
						--services \
						--arp \
						--leaderElection > "$TMP_MANIFEST"
					if [ ! -s "$TMP_MANIFEST" ]; then
						echo "❌ Generated manifest is empty" >&2
						exit 1
					fi
					install -m 0644 "$TMP_MANIFEST" /etc/kubernetes/manifests/kube-vip.yaml.tmp
					mv -f /etc/kubernetes/manifests/kube-vip.yaml.tmp /etc/kubernetes/manifests/kube-vip.yaml
					chown root:root /etc/kubernetes/manifests/kube-vip.yaml
					rm -f "$TMP_MANIFEST"
					sync || true
				'
			else
				sudo -n ctr run --rm --net-host "ghcr.io/kube-vip/kube-vip:$KVVERSION" kube-vip \
					/kube-vip manifest pod \
					--interface "$IFACE" \
					--address "$VIP" \
					--controlplane \
					--services \
					--arp \
					--leaderElection > "$TMP_MANIFEST"
				if [ ! -s "$TMP_MANIFEST" ]; then
					echo "❌ Generated manifest is empty"
					exit 1
				fi
				sudo -n install -m 0644 "$TMP_MANIFEST" /etc/kubernetes/manifests/kube-vip.yaml.tmp
				sudo -n mv -f /etc/kubernetes/manifests/kube-vip.yaml.tmp /etc/kubernetes/manifests/kube-vip.yaml
				sudo -n chown root:root /etc/kubernetes/manifests/kube-vip.yaml
				rm -f "$TMP_MANIFEST"
				sudo -n sync || true
			fi

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
				set -Eeuo pipefail

				echo "🔄 Updating kubeadm-config ConfigMap..." >&2
				{ kubectl -n kube-system get configmap kubeadm-config -o yaml | \
					sed "s|controlPlaneEndpoint:.*|controlPlaneEndpoint: %s:6443|g" | \
					kubectl apply -f -; } 1>&2

				echo "📖 Reading kube-vip manifest from primary node..." >&2

				if [ -f /tmp/kube-vip-manifest.yaml ]; then
					sudo cat /tmp/kube-vip-manifest.yaml
					exit 0
				fi

				echo "⚠️  kube-vip manifest not found at /tmp/kube-vip-manifest.yaml, trying fallback..." >&2
				if [ ! -f /etc/kubernetes/manifests/kube-vip.yaml ]; then
					echo "❌ kube-vip manifest not found on primary node!" >&2
					exit 1
				fi
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
				set -Eeuo pipefail
                
                echo "📦 Preparing kube-vip manifest on node %s..."
                
                # ✅ PERBAIKAN CRITICAL: Buat directory structure SEBELUM copy
                echo "📁 Creating Kubernetes directory structure..."
                sudo mkdir -p /etc/kubernetes/manifests
				sudo mkdir -p /etc/kubernetes/kube-vip
                sudo mkdir -p /etc/kubernetes/pki
                sudo mkdir -p /tmp/kube-vip-setup
                
                # ✅ Set proper permissions
                sudo chmod 755 /etc/kubernetes
                sudo chmod 755 /etc/kubernetes/manifests
                sudo chmod 755 /etc/kubernetes/kube-vip
                sudo chown -R root:root /etc/kubernetes
                
                # ✅ Verify directory creation
                if [ ! -d /etc/kubernetes/manifests ]; then
                    echo "❌ Failed to create /etc/kubernetes/manifests directory!"
                    exit 1
                fi
                
                echo "✅ Directory structure created:"
                sudo ls -la /etc/kubernetes/
                
                # ✅ Write manifest content menggunakan heredoc
                cat <<'MANIFEST_EOF' | sudo tee /tmp/kube-vip-setup/kube-vip.yaml > /dev/null
%s
MANIFEST_EOF

                # ✅ Verify copied file exists
                if [ ! -f /tmp/kube-vip-setup/kube-vip.yaml ]; then
                    echo "❌ Manifest file not found after write!"
                    exit 1
                fi
                
                # ✅ Verify file is not empty
                FILE_SIZE=$(sudo wc -c < /tmp/kube-vip-setup/kube-vip.yaml)
                if [ "$FILE_SIZE" -eq 0 ]; then
                    echo "❌ Manifest file is empty!"
                    exit 1
                fi
                
                echo "✅ Manifest file created (size: ${FILE_SIZE} bytes)"
                
                # ✅ Validate YAML syntax
                echo "🔍 Validating YAML syntax..."
                if command -v yamllint >/dev/null 2>&1; then
                    yamllint /tmp/kube-vip-setup/kube-vip.yaml || echo "⚠️  YAML validation warning (non-critical)"
                fi
                
                # ✅ Verify manifest contains required fields
                if ! grep -q "kind: Pod" /tmp/kube-vip-setup/kube-vip.yaml; then
                    echo "❌ Manifest missing 'kind: Pod' field!"
                    cat /tmp/kube-vip-setup/kube-vip.yaml
                    exit 1
                fi
                
                if ! grep -q "name: kube-vip" /tmp/kube-vip-setup/kube-vip.yaml; then
                    echo "❌ Manifest missing 'name: kube-vip' field!"
                    exit 1
                fi                

                echo "✅ Manifest validation passed"

                # ✅ Show manifest preview
                echo "🔍 Manifest preview (first 15 lines):"
                head -n 15 /tmp/kube-vip-setup/kube-vip.yaml
                
				echo "📦 Staging manifest to /etc/kubernetes/kube-vip/kube-vip.yaml (activate after join)..."
				LOCKFILE="/etc/kubernetes/kube-vip/.pulumi-kubevip.lock"
				sudo touch "$LOCKFILE"
				if command -v flock >/dev/null 2>&1; then
					sudo flock -x "$LOCKFILE" bash -c 'set -Eeuo pipefail; install -m 0644 /tmp/kube-vip-setup/kube-vip.yaml /etc/kubernetes/kube-vip/kube-vip.yaml.tmp; mv -f /etc/kubernetes/kube-vip/kube-vip.yaml.tmp /etc/kubernetes/kube-vip/kube-vip.yaml; chown root:root /etc/kubernetes/kube-vip/kube-vip.yaml; sync || true'
				else
					sudo install -m 0644 /tmp/kube-vip-setup/kube-vip.yaml /etc/kubernetes/kube-vip/kube-vip.yaml.tmp
					sudo mv -f /etc/kubernetes/kube-vip/kube-vip.yaml.tmp /etc/kubernetes/kube-vip/kube-vip.yaml
					sudo chown root:root /etc/kubernetes/kube-vip/kube-vip.yaml
					sudo sync || true
				fi
				
				if [ -f /etc/kubernetes/manifests/kube-vip.yaml ]; then
					echo "⚠️  Found active kube-vip manifest, moving to staging to avoid VIP takeover during join..."
					sudo mv -f /etc/kubernetes/manifests/kube-vip.yaml /etc/kubernetes/kube-vip/kube-vip.yaml || true
				fi

				# ✅ Verify staging
				echo "🔍 Verifying staged manifest..."
				if [ ! -f /etc/kubernetes/kube-vip/kube-vip.yaml ]; then
					echo "❌ Manifest staging verification failed!"
					sudo ls -la /etc/kubernetes/kube-vip/ || true
					exit 1
				fi
				
				echo "✅ Manifest staged successfully:"
				sudo ls -lh /etc/kubernetes/kube-vip/kube-vip.yaml
                
                # ✅ Cleanup temp directory
                sudo rm -rf /tmp/kube-vip-setup
                
                # ✅ IMPORTANT: Prevent kubelet from starting prematurely
                echo "🛑 Disabling kubelet service (will be enabled during join)..."
                sudo systemctl stop kubelet 2>/dev/null || true
                sudo systemctl disable kubelet 2>/dev/null || true

                echo "✅ kube-vip manifest preparation complete on node %s"
				echo "✅ kube-vip manifest staged (will be activated post-join)"
            `,
				nodes[i].Name,          // Display node name (first occurrence)
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
	var previousJoin pulumi.Resource
	for i := 1; i < len(nodes); i++ {
		conn1 := createConnection(nodes[i])

		nodeName := fmt.Sprintf("kubeadm-join-cp-%d", i)

		// Ambil dependency dari copy manifest command yang sesuai
		copyVipDep := joinCommands[i-1]

		// Step 3: Baca join command dari primary node (node 0)
		readJoinCmdName := fmt.Sprintf("read-join-cmd-%d", i)
		readJoinDeps := []pulumi.Resource{joinCmd, copyVipDep}
		if previousJoin != nil {
			readJoinDeps = append(readJoinDeps, previousJoin)
		}
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
		}, pulumi.DependsOn(readJoinDeps))

		if err != nil {
			return nil, fmt.Errorf("gagal read join command: %w", err)
		}

		// ✅ CRITICAL: Enhanced etcd health check dengan sync verification
		etcdHealthCheckName := fmt.Sprintf("etcd-health-check-%d", i)
		etcdHealthCheck, err := remote.NewCommand(ctx, etcdHealthCheckName, &remote.CommandArgs{
			Connection: conn0,
			Create: pulumi.Sprintf(`
                set -Eeuo pipefail
                
                echo "🔍 Enhanced etcd cluster health check before joining node %d..."
                
                # ✅ PERBAIKAN 1: Verify etcd cluster is stable
                MAX_ETCD_WAIT=120  # Increased timeout
                ETCD_COUNT=0
                
                while [ $ETCD_COUNT -lt $MAX_ETCD_WAIT ]; do
                    # Check etcd member list
                    if MEMBER_LIST=$(sudo ETCDCTL_API=3 etcdctl \
                        --endpoints=https://127.0.0.1:2379 \
                        --cacert=/etc/kubernetes/pki/etcd/ca.crt \
                        --cert=/etc/kubernetes/pki/etcd/server.crt \
                        --key=/etc/kubernetes/pki/etcd/server.key \
                        member list 2>/dev/null); then
                        
                        echo "📋 Current etcd members:"
                        echo "$MEMBER_LIST"
                        
                        # Check etcd endpoint health
                        if HEALTH_OUTPUT=$(sudo ETCDCTL_API=3 etcdctl \
                            --endpoints=https://127.0.0.1:2379 \
                            --cacert=/etc/kubernetes/pki/etcd/ca.crt \
                            --cert=/etc/kubernetes/pki/etcd/server.crt \
                            --key=/etc/kubernetes/pki/etcd/server.key \
                            endpoint health 2>&1); then
                            
                            echo "✅ etcd endpoint health check passed"
                            echo "$HEALTH_OUTPUT"
                            
                            # ✅ PERBAIKAN 2: Verify etcd cluster status
                            if CLUSTER_STATUS=$(sudo ETCDCTL_API=3 etcdctl \
                                --endpoints=https://127.0.0.1:2379 \
                                --cacert=/etc/kubernetes/pki/etcd/ca.crt \
                                --cert=/etc/kubernetes/pki/etcd/server.crt \
                                --key=/etc/kubernetes/pki/etcd/server.key \
                                endpoint status -w table 2>/dev/null); then
                                
                                echo "📊 etcd cluster status:"
                                echo "$CLUSTER_STATUS"
                                
                                # ✅ PERBAIKAN 3: Check for learner members
                                LEARNER_COUNT=$(echo "$MEMBER_LIST" | grep -c "isLearner=true" || echo "0")
                                
                                if [ "$LEARNER_COUNT" -gt 0 ]; then
                                    echo "⚠️  Found $LEARNER_COUNT learner member(s), waiting for promotion..."
                                    ETCD_COUNT=$((ETCD_COUNT + 1))
                                    sleep 5
                                    continue
                                fi
                                
                                # ✅ PERBAIKAN 4: Verify all members are started
                                STARTED_COUNT=$(echo "$MEMBER_LIST" | grep -c "started" || echo "0")
                                TOTAL_COUNT=$(echo "$MEMBER_LIST" | wc -l)
                                
                                if [ "$STARTED_COUNT" -ne "$TOTAL_COUNT" ]; then
                                    echo "⚠️  Not all members started ($STARTED_COUNT/$TOTAL_COUNT)"
                                    ETCD_COUNT=$((ETCD_COUNT + 1))
                                    sleep 5
                                    continue
                                fi
                                
                                echo "✅ All etcd members are started and healthy"
                                break
                            fi
                        fi
                    fi
                    
                    ETCD_COUNT=$((ETCD_COUNT + 1))
                    echo "   Attempt $ETCD_COUNT/$MAX_ETCD_WAIT: Waiting for etcd cluster stability..."
                    sleep 5
                done
                
                if [ $ETCD_COUNT -eq $MAX_ETCD_WAIT ]; then
                    echo "❌ etcd cluster not stable after $MAX_ETCD_WAIT attempts"
                    echo "📋 Final member list:"
                    sudo ETCDCTL_API=3 etcdctl \
                        --endpoints=https://127.0.0.1:2379 \
                        --cacert=/etc/kubernetes/pki/etcd/ca.crt \
                        --cert=/etc/kubernetes/pki/etcd/server.crt \
                        --key=/etc/kubernetes/pki/etcd/server.key \
                        member list || true
                    exit 1
                fi
                
                # ✅ PERBAIKAN 5: Additional stability wait
                echo "⏳ Waiting 30s for etcd cluster full stabilization..."
                sleep 30
                
                # ✅ PERBAIKAN 6: Final verification
                echo "🔍 Final etcd verification..."
                sudo ETCDCTL_API=3 etcdctl \
                    --endpoints=https://127.0.0.1:2379 \
                    --cacert=/etc/kubernetes/pki/etcd/ca.crt \
                    --cert=/etc/kubernetes/pki/etcd/server.crt \
                    --key=/etc/kubernetes/pki/etcd/server.key \
                    endpoint status -w table
                
                echo "✅ etcd cluster ready for new member"
            `, i),
		}, pulumi.DependsOn([]pulumi.Resource{readJoinCmd}))

		if err != nil {
			return nil, fmt.Errorf("gagal check etcd health: %w", err)
		}

		// ✅ PERBAIKAN: Tambahkan explicit wait untuk kube-vip manifest
		waitVipManifest, err := remote.NewCommand(ctx, fmt.Sprintf("wait-vip-manifest-%d", i), &remote.CommandArgs{
			Connection: conn1,
			Create: pulumi.String(`
                set -Eeuo pipefail
                
                echo "⏳ Waiting for staged kube-vip manifest..."
                
                MAX_WAIT=60
                WAIT_COUNT=0
                
                while [ $WAIT_COUNT -lt $MAX_WAIT ]; do
                    # ✅ Check 1: Directory exists
                    if [ ! -d /etc/kubernetes/kube-vip ]; then
                        echo "⚠️  Directory /etc/kubernetes/kube-vip not found (attempt $((WAIT_COUNT+1))/$MAX_WAIT)"
                        WAIT_COUNT=$((WAIT_COUNT + 1))
                        sleep 2
                        continue
                    fi
                    
                    # ✅ Check 2: Staged file exists
                    if [ ! -f /etc/kubernetes/kube-vip/kube-vip.yaml ]; then
                        echo "⚠️  Staged manifest file not found (attempt $((WAIT_COUNT+1))/$MAX_WAIT)"
                        echo "📋 Directory contents:"
                        sudo ls -la /etc/kubernetes/kube-vip/ 2>/dev/null || echo "Cannot list directory"
                        WAIT_COUNT=$((WAIT_COUNT + 1))
                        sleep 2
                        continue
                    fi
                    
                    # ✅ Check 3: File is not empty
                    FILE_SIZE=$(sudo wc -c < /etc/kubernetes/kube-vip/kube-vip.yaml 2>/dev/null || echo "0")
                    if [ "$FILE_SIZE" -eq 0 ]; then
                        echo "⚠️  Manifest file is empty (attempt $((WAIT_COUNT+1))/$MAX_WAIT)"
                        WAIT_COUNT=$((WAIT_COUNT + 1))
                        sleep 2
                        continue
                    fi
                    
                    # ✅ Check 4: File is valid YAML with required fields
                    if ! sudo cat /etc/kubernetes/kube-vip/kube-vip.yaml | grep -q "kind: Pod"; then
                        echo "⚠️  Manifest missing 'kind: Pod' (attempt $((WAIT_COUNT+1))/$MAX_WAIT)"
                        WAIT_COUNT=$((WAIT_COUNT + 1))
                        sleep 2
                        continue
                    fi
                    
                    # ✅ All checks passed
                    echo "✅ kube-vip manifest verification complete"
                    echo "📋 Manifest details:"
                    echo "   Path: /etc/kubernetes/kube-vip/kube-vip.yaml"
                    echo "   Size: ${FILE_SIZE} bytes"
                    echo "   Permissions: $(sudo stat -c '%a' /etc/kubernetes/kube-vip/kube-vip.yaml)"
                    echo "   Owner: $(sudo stat -c '%U:%G' /etc/kubernetes/kube-vip/kube-vip.yaml)"
                    
                    # ✅ Show manifest preview
                    echo "📋 Manifest preview (first 10 lines):"
                    sudo head -n 10 /etc/kubernetes/kube-vip/kube-vip.yaml
                    
                    break
                done
                    
                if [ $WAIT_COUNT -eq $MAX_WAIT ]; then
                    echo "❌ kube-vip manifest verification failed after ${MAX_WAIT} attempts"
                    echo "📋 Final directory check:"
                    sudo ls -laR /etc/kubernetes/ 2>/dev/null || echo "Cannot access /etc/kubernetes/"
                    exit 1
                fi
                
                # ✅ Additional stability wait
                echo "⏳ Waiting 5s for filesystem sync..."
                sleep 5
                
                echo "✅ kube-vip manifest ready for join operation"
            `),
		}, pulumi.DependsOn([]pulumi.Resource{copyVipDep}))

		if err != nil {
			return nil, fmt.Errorf("gagal wait vip manifest node %d: %w", i, err)
		}

		// Join node ke cluster dan copy kube-vip manifest dari node pertama ke node lainnya
		joinNodeCmd, err := remote.NewCommand(ctx, nodeName, &remote.CommandArgs{
			Connection: conn1,
			Create: pulumi.Sprintf(`
				set -Eeuo pipefail

				STEP="join-node"
				trap 'rc=$?; echo "❌ ${STEP} failed (rc=$rc)"; echo "📋 routes:"; ip -4 route || true; echo "📋 neigh:"; ip neigh show || true; echo "📋 kubelet:"; sudo journalctl -u kubelet -n 80 --no-pager || true; exit $rc' ERR

				VIP="%s"
				NODE_NAME="%s"
				ENDPOINT="%s"
				IFACE="%s"

				echo "🚀 Joining node $NODE_NAME to cluster..."
				echo "   Using endpoint: $ENDPOINT:6443"
				echo "   Using interface: $IFACE"
				
				echo "🏷️  Ensuring hostname matches expected node name..."
				sudo hostnamectl set-hostname "$NODE_NAME" || true
				CURRENT_HOST_FQDN=$(hostname -f 2>/dev/null || hostname)
				echo "📋 Current hostname: $CURRENT_HOST_FQDN"
				
				echo "🧭 Verifying endpoint DNS resolution..."
				RESOLVED_IP=""
				if command -v getent >/dev/null 2>&1; then
					RESOLVED_IP=$(getent ahostsv4 "$ENDPOINT" | awk '{print $1; exit}' || true)
				elif command -v nslookup >/dev/null 2>&1; then
					RESOLVED_IP=$(nslookup "$ENDPOINT" 2>/dev/null | awk '/^Address: /{print $2; exit}' || true)
				fi
				echo "📋 $ENDPOINT resolves to: ${RESOLVED_IP:-<unknown>}"
				
				NODE_IP=""
				NODE_IP=$(ip -4 -o addr show dev "$IFACE" scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -n1 || true)
				echo "🔍 Node IP on $IFACE: ${NODE_IP:-<unknown>}"
				
				if [ -n "$NODE_IP" ] && [ "$NODE_IP" = "$VIP" ]; then
					echo "❌ Invalid configuration: node IP equals VIP ($VIP). Choose a VIP not used by any node."
					exit 1
				fi
				
				if [ -n "$RESOLVED_IP" ] && [ -n "$NODE_IP" ] && [ "$RESOLVED_IP" = "$NODE_IP" ]; then
					echo "❌ Endpoint $ENDPOINT resolves to this node's IP ($NODE_IP). This will break join (self-target)."
					echo "   Fix DNS A record or use /etc/hosts mapping to VIP ($VIP)."
					exit 1
				fi
				
				echo "🧾 Ensuring /etc/hosts maps endpoint to VIP (defensive against DNS issues)..."
				if ! grep -qE "^[[:space:]]*${VIP}[[:space:]]+${ENDPOINT}([[:space:]]+|$)" /etc/hosts; then
					echo "${VIP} ${ENDPOINT}" | sudo tee -a /etc/hosts >/dev/null
				fi
				
				echo "🧹 Ensuring kube-vip manifest is staged (not active) before join..."
				sudo mkdir -p /etc/kubernetes/kube-vip
				if [ -f /etc/kubernetes/manifests/kube-vip.yaml ]; then
					echo "⚠️  Active kube-vip manifest found, moving to staging to avoid VIP takeover during join"
					sudo mv -f /etc/kubernetes/manifests/kube-vip.yaml /etc/kubernetes/kube-vip/kube-vip.yaml || true
				fi
				if [ ! -f /etc/kubernetes/kube-vip/kube-vip.yaml ]; then
					echo "❌ Staged kube-vip manifest not found at /etc/kubernetes/kube-vip/kube-vip.yaml"
					sudo ls -la /etc/kubernetes/kube-vip/ || true
					exit 1
				fi
                
                echo "🔍 Pre-join verification for node $NODE_NAME..."
                
                # ✅ CRITICAL: Verify directory structure EXISTS
                echo "📁 Verifying Kubernetes directory structure..."
                
                REQUIRED_DIRS=(
                    "/etc/kubernetes"
                    "/etc/kubernetes/manifests"
					"/etc/kubernetes/kube-vip"
                    "/etc/kubernetes/pki"
                    "/var/lib/kubelet"
                )

                for dir in "${REQUIRED_DIRS[@]}"; do
                    if [ ! -d "$dir" ]; then
                        echo "⚠️  Creating missing directory: $dir"
                        sudo mkdir -p "$dir"
                        sudo chmod 755 "$dir"
                        sudo chown root:root "$dir"
                    fi
                    echo "✅ Directory exists: $dir"
                done

                # ✅ PERBAIKAN 0: Stop kubelet dulu untuk mencegah premature start
                echo "🛑 Stopping kubelet service..."
                sudo systemctl stop kubelet || true
                sudo systemctl disable kubelet || true

                # ✅ Clean up previous kubelet state SEBELUM join
                echo "🧹 Cleaning up previous kubelet state..."
                sudo rm -rf /var/lib/kubelet/* || true
                sudo rm -rf /etc/kubernetes/kubelet.conf || true
                sudo rm -rf /etc/kubernetes/pki/ca.crt || true
                sudo rm -rf /etc/kubernetes/bootstrap-kubelet.conf || true
                
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
					if curl -kfsS --connect-timeout 2 --max-time 4 https://$ENDPOINT:6443/healthz &>/dev/null; then
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
    
                # ✅ PERBAIKAN 2: Verifikasi staged kube-vip manifest (belum aktif)
                echo "🔍 Verifying staged kube-vip manifest..."
                if [ ! -f /etc/kubernetes/kube-vip/kube-vip.yaml ]; then
                    echo "❌ staged kube-vip manifest not found!"
                    echo "📋 Listing /etc/kubernetes/kube-vip:"
                    sudo ls -la /etc/kubernetes/kube-vip/ || echo "Directory not found"
                    exit 1
                fi
                
                echo "✅ staged kube-vip manifest found"
                
                echo "📋 Manifest preview:"
                sudo head -n 10 /etc/kubernetes/kube-vip/kube-vip.yaml

                # ✅ PERBAIKAN 3: Configure containerd SEBELUM join
                echo "🔧 Configuring containerd systemd cgroup..."
                sudo mkdir -p /etc/containerd
                sudo containerd config default | sudo tee /etc/containerd/config.toml >/dev/null
                sudo sed -i 's/SystemdCgroup = false/SystemdCgroup = true/g' /etc/containerd/config.toml
                sudo systemctl restart containerd
                sudo systemctl enable containerd

                # Verify containerd is running
                if ! sudo systemctl is-active --quiet containerd; then
                    echo "❌ containerd failed to start!"
                    sudo systemctl status containerd --no-pager
                    exit 1
                fi
                echo "✅ containerd configured and running"
                
                # Execute join dengan error handling
                
                # ✅ Disable swap on join node
                echo "🔧 Disabling swap..."
                sudo swapoff -a
                sudo sed -i '/ swap / s/^\(.*\)$/#\1/g' /etc/fstab

                # ✅ Ensure hostname mapping
                if ! grep -q "$(hostname)" /etc/hosts; then
                    echo "127.0.1.1 $(hostname)" | sudo tee -a /etc/hosts
                fi

                # ✅ CRITICAL FIX: Setup kubeconfig SEBELUM join
                echo "⚙️  Pre-configuring kubeconfig directory..."
                mkdir -p $HOME/.kube
                
                # Copy kubeconfig yang sudah di-update dari primary node
                if [ -f $HOME/.kube/config ]; then
                    echo "✅ Using pre-copied kubeconfig from primary node"
                    export KUBECONFIG=$HOME/.kube/config
                    
                    # Verify kubeconfig points to VIP/domain
                    CURRENT_SERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null || echo "")
                    echo "📋 Current kubeconfig server: $CURRENT_SERVER"
                    
                    if [[ "$CURRENT_SERVER" != *"$ENDPOINT"* ]]; then
                        echo "⚠️  Kubeconfig not using VIP endpoint, will update after join"
                    fi
                else
                    echo "⚠️  Pre-copied kubeconfig not found, will configure after join"
                fi
                
                # ✅ PERBAIKAN 4: Jalankan join command
                echo "🔗 Executing join command..."
                
                # ✅ Tambahkan flag --ignore-preflight-errors untuk bypass kubelet check
                JOIN_CMD_MODIFIED="${JOIN_CMD} --ignore-preflight-errors=DirAvailable--var-lib-etcd,FileAvailable--etc-kubernetes-kubelet.conf"
                
				# Execute join dengan retry khusus untuk error promote learner yang prematur
				MAX_JOIN_ATTEMPTS=5
				JOIN_ATTEMPT=1
				while [ $JOIN_ATTEMPT -le $MAX_JOIN_ATTEMPTS ]; do
					echo "🔁 Join attempt $JOIN_ATTEMPT/$MAX_JOIN_ATTEMPTS"
					set +e
					JOIN_OUTPUT=$(eval "$JOIN_CMD_MODIFIED" 2>&1)
					JOIN_RC=$?
					set -e
					if [ $JOIN_RC -eq 0 ]; then
						break
					fi
					
					if echo "$JOIN_OUTPUT" | grep -q "etcdserver: can only promote a learner member which is in sync with leader"; then
						echo "⚠️  etcd learner belum in-sync dengan leader; menunggu stabilisasi lalu retry..."
						sleep 45
						JOIN_ATTEMPT=$((JOIN_ATTEMPT + 1))
						continue
					fi
					
					echo "❌ Join command failed (rc=$JOIN_RC)!"
					echo "📋 kubeadm output (tail):"
					echo "$JOIN_OUTPUT" | tail -n 80
					echo "📋 Checking kubelet logs:"
					sudo journalctl -u kubelet -n 80 --no-pager || true
					exit 1
				done
				
				if [ $JOIN_RC -ne 0 ]; then
					echo "❌ Join command still failing after $MAX_JOIN_ATTEMPTS attempts"
					echo "📋 Last kubeadm output (tail):"
					echo "$JOIN_OUTPUT" | tail -n 120
					echo "📋 kubelet logs:"
					sudo journalctl -u kubelet -n 120 --no-pager || true
					exit 1
				fi

                echo "✅ Node joined successfully"

                # ✅ PERBAIKAN 4: Update kubeconfig SEBELUM enable kubelet
                echo "🔄 Updating kubeconfig to use VIP endpoint..."
                
                # ✅ Ensure .kube directory exists
                echo "⚙️  Setting up kubeconfig..."
                mkdir -p $HOME/.kube

                # Update server endpoint in kubeconfig
                if [ -f $HOME/.kube/config ]; then
                    # Backup original config
                    sudo cp $HOME/.kube/config $HOME/.kube/config.backup
                    
                    # Update server endpoint
                    sudo sed -i "s|server:.*|server: https://${ENDPOINT}:6443|g" $HOME/.kube/config
                    
                    # Verify update
                    CURRENT_SERVER=$(grep "server:" $HOME/.kube/config | head -1)
                    echo "📋 Updated kubeconfig server: $CURRENT_SERVER"
                    
                    if ! echo "$CURRENT_SERVER" | grep -q "${ENDPOINT}:6443"; then
                        echo "⚠️  Warning: kubeconfig update may have failed"
                        echo "   Restoring backup..."
                        sudo cp $HOME/.kube/config.backup $HOME/.kube/config
                    else
                        echo "✅ kubeconfig updated successfully"
                    fi
                fi
    
                # ✅ PERBAIKAN 5: Update kubeconfig SEBELUM enable service
                echo "🔄 Updating kubeconfig to use VIP endpoint..."
                
                if [ -f /etc/kubernetes/kubelet.conf ]; then
                    # Backup original kubelet.conf
                    sudo cp /etc/kubernetes/kubelet.conf /etc/kubernetes/kubelet.conf.backup
                    
                    # Update server endpoint in kubelet.conf
                    sudo sed -i "s|server:.*|server: https://${ENDPOINT}:6443|g" /etc/kubernetes/kubelet.conf
                    
                    # Verify update
                    KUBELET_SERVER=$(sudo grep "server:" /etc/kubernetes/kubelet.conf | head -1)
                    echo "📋 Updated kubelet.conf server: $KUBELET_SERVER"
                    
                    if ! echo "$KUBELET_SERVER" | grep -q "${ENDPOINT}:6443"; then
                        echo "⚠️  Warning: kubelet.conf update may have failed"
                        echo "   Restoring backup..."
                        sudo cp /etc/kubernetes/kubelet.conf.backup /etc/kubernetes/kubelet.conf
                    else
                        echo "✅ kubelet.conf updated successfully"
                    fi
                else
                    echo "⚠️  Warning: /etc/kubernetes/kubelet.conf not found"
                fi
                
                # ✅ PERBAIKAN 7: Reload systemd dan restart kubelet
                echo "🔄 Reloading systemd and restarting kubelet..."
                sudo systemctl daemon-reload
                
                # Enable kubelet service
                sudo systemctl enable kubelet
                
                # Restart kubelet dengan updated config
                sudo systemctl restart kubelet
                
                # Wait for kubelet to stabilize
                echo "⏳ Waiting for kubelet to stabilize..."
                sleep 15
                
                # ✅ PERBAIKAN 8: Verify kubelet status
                echo "🔍 Verifying kubelet status..."
                
                if ! sudo systemctl is-active --quiet kubelet; then
                    echo "❌ kubelet is not active!"
                    echo "📋 kubelet status:"
                    sudo systemctl status kubelet --no-pager || true
                    echo "📋 Recent kubelet logs:"
                    sudo journalctl -u kubelet -n 50 --no-pager || true
                    exit 1
                fi
                
                echo "✅ kubelet is active and running"
                
                # ✅ PERBAIKAN 9: Verify node registration
                echo "🔍 Verifying node registration..."
                
                MAX_NODE_WAIT=60
                NODE_COUNT=0
                
                while [ $NODE_COUNT -lt $MAX_NODE_WAIT ]; do
                    if kubectl get node $NODE_NAME &>/dev/null; then
                        echo "✅ Node registered in cluster"
                        
                        # Show node info
                        echo "📋 Node information:"
                        kubectl get node $NODE_NAME -o wide
                        
                        break
                    fi
                    
                    NODE_COUNT=$((NODE_COUNT + 1))
                    echo "   Attempt $NODE_COUNT/$MAX_NODE_WAIT: Waiting for node registration..."
                    sleep 3
                done
                
                if [ $NODE_COUNT -eq $MAX_NODE_WAIT ]; then
                    echo "❌ Node not registered after $MAX_NODE_WAIT attempts"
                    echo "📋 Cluster nodes:"
                    kubectl get nodes || true
                    exit 1
                fi

				echo "🚦 Activating kube-vip manifest (post-join)..."
				LOCKFILE="/etc/kubernetes/manifests/.pulumi-manifests.lock"
				sudo touch "$LOCKFILE"
				if command -v flock >/dev/null 2>&1; then
					sudo flock -x "$LOCKFILE" bash -c 'set -Eeuo pipefail; install -m 0644 /etc/kubernetes/kube-vip/kube-vip.yaml /etc/kubernetes/manifests/kube-vip.yaml.tmp; mv -f /etc/kubernetes/manifests/kube-vip.yaml.tmp /etc/kubernetes/manifests/kube-vip.yaml; chown root:root /etc/kubernetes/manifests/kube-vip.yaml; sync || true'
				else
					sudo install -m 0644 /etc/kubernetes/kube-vip/kube-vip.yaml /etc/kubernetes/manifests/kube-vip.yaml.tmp
					sudo mv -f /etc/kubernetes/manifests/kube-vip.yaml.tmp /etc/kubernetes/manifests/kube-vip.yaml
					sudo chown root:root /etc/kubernetes/manifests/kube-vip.yaml
					sudo sync || true
				fi
				echo "✅ kube-vip manifest activated"
                
                # ✅ PERBAIKAN 10: Verify kube-vip pod
                echo "🔍 Verifying kube-vip pod..."
                
                MAX_VIP_WAIT=60
                VIP_COUNT=0
                
                while [ $VIP_COUNT -lt $MAX_VIP_WAIT ]; do
                    # Check if kube-vip pod exists and is running
                    if kubectl get pod -n kube-system -l component=kube-vip --field-selector spec.nodeName=$NODE_NAME 2>/dev/null | grep -q Running; then
                        echo "✅ kube-vip pod is running on this node"
                        
                        # Show pod info
                        echo "📋 kube-vip pod information:"
                        kubectl get pod -n kube-system -l component=kube-vip --field-selector spec.nodeName=$NODE_NAME -o wide
                        
                        break
                    fi
                    
                    VIP_COUNT=$((VIP_COUNT + 1))
                    echo "   Attempt $VIP_COUNT/$MAX_VIP_WAIT: Waiting for kube-vip pod..."
                    sleep 3
                done
                
                if [ $VIP_COUNT -eq $MAX_VIP_WAIT ]; then
                    echo "⚠️  Warning: kube-vip pod not running after $MAX_VIP_WAIT attempts"
                    echo "📋 All kube-vip pods:"
                    kubectl get pod -n kube-system -l component=kube-vip -o wide || true
                    echo "📋 Static pod manifests:"
                    sudo ls -la /etc/kubernetes/manifests/ || true
                fi
                
                # ✅ PERBAIKAN 11: Final connectivity test
                echo "🔍 Final connectivity test..."
                
                # Test API server via VIP
                if curl -kfsS --connect-timeout 2 --max-time 4 https://${ENDPOINT}:6443/healthz &>/dev/null; then
                    echo "✅ API server accessible via VIP endpoint"
                else
                    echo "⚠️  Warning: API server not accessible via VIP endpoint"
                    echo "📋 Testing direct connection to localhost:"
                    curl -kfsS --connect-timeout 2 --max-time 4 https://127.0.0.1:6443/healthz || true
                fi
                
                # Show final cluster status
                echo "📋 Final cluster status:"
                kubectl get nodes -o wide
                kubectl get pods -n kube-system -o wide | grep -E "kube-vip|etcd|apiserver" || true
                
                echo "✅ Node $NODE_NAME successfully joined and configured!"
            `,
				cfg.K8sVIP,
				nodes[i].Name, // Display node name
				cfg.K8sDOMAIN, // Display endpoint
				cfg.K8sVIPInterface,
				readJoinCmd.Stdout, // Inject join command
				cfg.K8sDOMAIN),     // Ping test
		}, pulumi.DependsOn([]pulumi.Resource{
			etcdHealthCheck,
			readJoinCmd,
			copyVipDep,
			vipReachabilityCommands[i],
			waitVipManifest,
		}), pulumi.IgnoreChanges([]string{"create"}))
		if err != nil {
			return nil, fmt.Errorf("gagal join node %d: %w", i, err)
		}

		postJoinEtcdStabilizeName := fmt.Sprintf("post-join-etcd-stabilize-%d", i)
		postJoinEtcdStabilize, err := remote.NewCommand(ctx, postJoinEtcdStabilizeName, &remote.CommandArgs{
			Connection: conn0,
			Create: pulumi.Sprintf(`
				set -Eeuo pipefail
				
				echo "🔄 Waiting etcd to fully stabilize after joining node %s..."
				MAX_WAIT=300
				COUNT=0
				
				while [ $COUNT -lt $MAX_WAIT ]; do
					MEMBER_LIST=$(sudo ETCDCTL_API=3 etcdctl \
						--endpoints=https://127.0.0.1:2379 \
						--cacert=/etc/kubernetes/pki/etcd/ca.crt \
						--cert=/etc/kubernetes/pki/etcd/server.crt \
						--key=/etc/kubernetes/pki/etcd/server.key \
						member list 2>/dev/null || true)
					
					if [ -n "$MEMBER_LIST" ]; then
						LEARNER_COUNT=$(echo "$MEMBER_LIST" | grep -c "isLearner=true" || echo "0")
						if [ "$LEARNER_COUNT" -eq 0 ]; then
							if sudo ETCDCTL_API=3 etcdctl \
								--endpoints=https://127.0.0.1:2379 \
								--cacert=/etc/kubernetes/pki/etcd/ca.crt \
								--cert=/etc/kubernetes/pki/etcd/server.crt \
								--key=/etc/kubernetes/pki/etcd/server.key \
								endpoint health >/dev/null 2>&1; then
								echo "✅ etcd stable (no learners, endpoint healthy)"
								sudo ETCDCTL_API=3 etcdctl \
									--endpoints=https://127.0.0.1:2379 \
									--cacert=/etc/kubernetes/pki/etcd/ca.crt \
									--cert=/etc/kubernetes/pki/etcd/server.crt \
									--key=/etc/kubernetes/pki/etcd/server.key \
									endpoint status -w table || true
								break
							fi
						fi
					fi
					
					COUNT=$((COUNT + 1))
					sleep 1
				done
				
				if [ $COUNT -eq $MAX_WAIT ]; then
					echo "❌ etcd not stable after ${MAX_WAIT}s"
					echo "📋 member list:"
					echo "$MEMBER_LIST"
					exit 1
				fi
			`, nodes[i].Name),
		}, pulumi.DependsOn([]pulumi.Resource{joinNodeCmd}))
		if err != nil {
			return nil, fmt.Errorf("gagal stabilisasi etcd post-join node %d: %w", i, err)
		}

		joinCommands = append(joinCommands, joinNodeCmd, postJoinEtcdStabilize)
		previousJoin = postJoinEtcdStabilize
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
			
			echo ""
			echo "🗃️  etcd Status:"
			echo "============="
			ETCD_MEMBER_LIST=$(sudo ETCDCTL_API=3 etcdctl \
				--endpoints=https://127.0.0.1:2379 \
				--cacert=/etc/kubernetes/pki/etcd/ca.crt \
				--cert=/etc/kubernetes/pki/etcd/server.crt \
				--key=/etc/kubernetes/pki/etcd/server.key \
				member list 2>/dev/null || true)
			echo "$ETCD_MEMBER_LIST"
			if echo "$ETCD_MEMBER_LIST" | grep -q "isLearner=true"; then
				echo "❌ Found etcd learner member(s) still present"
				exit 1
			fi
			sudo ETCDCTL_API=3 etcdctl \
				--endpoints=https://127.0.0.1:2379 \
				--cacert=/etc/kubernetes/pki/etcd/ca.crt \
				--cert=/etc/kubernetes/pki/etcd/server.crt \
				--key=/etc/kubernetes/pki/etcd/server.key \
				endpoint status -w table || true
            
            # Show control plane pods
            echo ""
            echo "📦 System Pods:"
            echo "==============="
            kubectl get pods -n kube-system -o wide
            
            # Show kube-vip pods
            echo ""
			echo "🔌 kube-vip Pods:"
			echo "==================="
			kubectl get pods -n kube-system -l app=kube-vip -o wide 2>/dev/null || true
			kubectl get pods -n kube-system -l component=kube-vip -o wide 2>/dev/null || true

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
