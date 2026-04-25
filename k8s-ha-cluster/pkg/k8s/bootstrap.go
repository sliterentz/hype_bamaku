package k8s

import (
	"fmt"

	"github.com/pulumi/pulumi-command/sdk/go/command/remote"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"k8s-ha-cluster/pkg/config"
	"k8s-ha-cluster/pkg/hyperv"
	"k8s-ha-cluster/pkg/network"
)

type Cluster struct {
	KubeConfig pulumi.StringOutput
}

func Bootstrap(ctx *pulumi.Context, cfg *config.Config, nodes []*hyperv.Node, vip *network.VIP) (*Cluster, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("tidak ada node yang tersedia untuk bootstrap")
	}

	// Helper function untuk membuat connection args
    createConnection := func(node *hyperv.Node) *remote.ConnectionArgs {
        return &remote.ConnectionArgs{
            Host:       node.IPAddress,
            User:       pulumi.String(cfg.SSHUser),
            PrivateKey: cfg.SSHPrivateKey,
            Port:       pulumi.Float64(22),
            DialErrorLimit: pulumi.Int(10), // Retry limit
        }
    }

	    // 1. Reset cluster jika sudah ada (cleanup)
    for _, node := range nodes {
        nodeName := fmt.Sprintf("kubeadm-reset-%s", node.Name)
        _, err := remote.NewCommand(ctx, nodeName, &remote.CommandArgs{
            Connection: createConnection(node),
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
            `),
        })
        if err != nil {
            return nil, fmt.Errorf("gagal reset node %s: %w", node.Name, err)
        }
    }

	// 2. Kubeadm Init pada Node Pertama (sesuai dengan init.sh)
    initCmd, err := remote.NewCommand(ctx, "kubeadm-init", &remote.CommandArgs{
        Connection: createConnection(nodes[0]),
        Create: pulumi.Sprintf(`
            set -e
            # Pastikan kubeadm sudah terinstall
            if ! command -v kubeadm &> /dev/null; then
                echo "kubeadm tidak ditemukan"
                exit 1
            fi
            
            echo "Initializing HA Kubernetes Control Plane at VIP: %s"
            
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
			
			echo "Initialization Complete."
            
            # Output kubeconfig
            cat $HOME/.kube/config
        `, 
        cfg.K8sVIP,                    // VIP untuk display
        cfg.K8sVIP,                    // control-plane-endpoint
        cfg.K8sDOMAIN,                 // cert extra SANs - domain
        cfg.K8sVIP,                    // cert extra SANs - VIP
        nodes[0].IPAddress,            // cert extra SANs - first node IP
        cfg.K8sPodCIDR,                // pod network CIDR
        cfg.K8sVersion),               // kubernetes version
    }, pulumi.DependsOn([]pulumi.Resource{/* reset commands */}))
    if err != nil {
        return nil, fmt.Errorf("gagal init kubeadm: %w", err)
    }

    // 3. Generate join command untuk control plane nodes
    joinCmd, err := remote.NewCommand(ctx, "generate-join-command", &remote.CommandArgs{
        Connection: createConnection(nodes[0]),
        Create: pulumi.String(`
            set -e
            # Generate certificate key
            CERT_KEY=$(sudo kubeadm init phase upload-certs --upload-certs 2>/dev/null | tail -1)
            
            # Generate join command dengan certificate key
            sudo kubeadm token create --print-join-command --certificate-key $CERT_KEY
        `),
    }, pulumi.DependsOn([]pulumi.Resource{initCmd}))
    if err != nil {
        return nil, fmt.Errorf("gagal generate join command: %w", err)
    }

    // 4. Join node lainnya sebagai control plane
    for i := 1; i < len(nodes); i++ {
        nodeName := fmt.Sprintf("kubeadm-join-cp-%d", i)
        _, err = remote.NewCommand(ctx, nodeName, &remote.CommandArgs{
            Connection: createConnection(nodes[i]),
            Create: pulumi.Sprintf(`
                set -e
                echo "Joining node %s to cluster..."

                # Jalankan join command
                sudo %s --control-plane
                
                # Setup kubeconfig
                mkdir -p $HOME/.kube
                sudo cp -f /etc/kubernetes/admin.conf $HOME/.kube/config
                sudo chown $(id -u):$(id -g) $HOME/.kube/config

				echo "Node joined successfully."
            `, nodes[i].Name, joinCmd.Stdout),
        }, pulumi.DependsOn([]pulumi.Resource{joinCmd}))
        if err != nil {
            return nil, fmt.Errorf("gagal join node %d: %w", i, err)
        }
    }

	return &Cluster{
		KubeConfig: initCmd.Stdout, // Output KUBECONFIG dari node 1
	}, nil
}
