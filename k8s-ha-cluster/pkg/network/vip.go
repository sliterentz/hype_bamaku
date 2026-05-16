package network

import (
	"fmt"

	"github.com/pulumi/pulumi-command/sdk/go/command/remote"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"k8s-ha-cluster/pkg/config"
	"k8s-ha-cluster/pkg/hyperv"
)

type VIP struct {
	IPAddress string
}

// SetupVIP melakukan konfigurasi kube-vip untuk Control Plane HA
// (menggunakan ARP mode static pod)
func SetupVIP(ctx *pulumi.Context, cfg *config.Config, nodes []*hyperv.Node) (*VIP, []*remote.Command, error) {
	var vipCommands []*remote.Command

	for i, node := range nodes {
		// ✅ Deploy Kube-VIP static pod manifest ke setiap node control-plane
		vipCmd, err := remote.NewCommand(ctx, fmt.Sprintf("setup-kube-vip-%d", i), &remote.CommandArgs{
			Connection: &remote.ConnectionArgs{
				Host:       node.IPAddress,
				User:       pulumi.String(cfg.SSHUser),
				PrivateKey: cfg.SSHPrivateKey,
			},
			Create: pulumi.Sprintf(`
                set -Eeuo pipefail
                
                echo "🔧 Setting up kube-vip on %s..."
                
                # 1. Create manifests directory
                echo "📁 Creating manifests directory..."
                sudo mkdir -p /etc/kubernetes/manifests
                
                # 2. Pull kube-vip image
                echo "📦 Pulling kube-vip image..."
                MAX_RETRIES=3
                RETRY_COUNT=0
                
                while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
                    if sudo ctr image pull ghcr.io/kube-vip/kube-vip:v0.6.4; then
                        echo "✅ Image pulled successfully"
                        break
                    fi
                    RETRY_COUNT=$((RETRY_COUNT + 1))
                    echo "⚠️  Pull attempt $RETRY_COUNT/$MAX_RETRIES failed, retrying..."
                    sleep 5
                done
                
                if [ $RETRY_COUNT -eq $MAX_RETRIES ]; then
                    echo "❌ Failed to pull kube-vip image after $MAX_RETRIES attempts"
                    exit 1
                fi
                
                # 3. Generate kube-vip manifest
                echo "📝 Generating kube-vip manifest..."
                sudo ctr run --rm --net-host ghcr.io/kube-vip/kube-vip:v0.6.4 vip /kube-vip manifest pod \
                    --interface %s \
                    --address %s \
                    --controlplane \
                    --services \
                    --arp \
                    --leaderElection | sudo tee /etc/kubernetes/manifests/kube-vip.yaml
                
                # 4. Verify manifest was created
                if [ ! -f /etc/kubernetes/manifests/kube-vip.yaml ]; then
                    echo "❌ Failed to create kube-vip manifest!"
                    exit 1
                fi
                
                echo "✅ kube-vip manifest created successfully"
                
                # 5. Show manifest preview
                echo "📋 Manifest preview:"
                sudo head -n 20 /etc/kubernetes/manifests/kube-vip.yaml
                
                # 6. Set proper permissions
                sudo chmod 644 /etc/kubernetes/manifests/kube-vip.yaml
                
                # 7. Verify manifest syntax
                echo "🔍 Verifying manifest syntax..."
                if ! sudo cat /etc/kubernetes/manifests/kube-vip.yaml | grep -q "kind: Pod"; then
                    echo "❌ Invalid manifest format!"
                    sudo cat /etc/kubernetes/manifests/kube-vip.yaml
                    exit 1
                fi
                
                echo "✅ kube-vip setup complete on %s"
            `,
				node.Name,
				cfg.K8sVIPInterface,
				cfg.K8sVIP,
				node.Name),
		}, pulumi.DependsOn([]pulumi.Resource{}))

		if err != nil {
			return nil, nil, fmt.Errorf("failed to setup kube-vip on node %s: %w", node.Name, err)
		}

		vipCommands = append(vipCommands, vipCmd)
	}

	return &VIP{IPAddress: cfg.K8sVIP}, vipCommands, nil
}
