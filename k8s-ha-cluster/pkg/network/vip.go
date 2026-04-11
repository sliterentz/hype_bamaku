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
func SetupVIP(ctx *pulumi.Context, cfg *config.Config, nodes []*hyperv.Node) (*VIP, error) {
	for i, node := range nodes {
		// Deploy Kube-VIP static pod manifest ke setiap node control-plane
		_, err := remote.NewCommand(ctx, fmt.Sprintf("setup-kube-vip-%d", i), &remote.CommandArgs{
			Connection: &remote.ConnectionArgs{
				Host: 			node.IPAddress,
				User: 			pulumi.String("ubuntu"),
				PrivateKey:     pulumi.String(cfg.SSHPrivateKey), // Dari config atau ESC
			},
			Create: pulumi.Sprintf(`
				sudo mkdir -p /etc/kubernetes/manifests &&
				sudo ctr image pull ghcr.io/kube-vip/kube-vip:v0.6.4 &&
				sudo ctr run --rm --net-host ghcr.io/kube-vip/kube-vip:v0.6.4 vip /kube-vip manifest pod \
					--interface %s \
					--address %s \
					--controlplane \
					--services \
					--arp \
					--leaderElection | sudo tee /etc/kubernetes/manifests/kube-vip.yaml`, 
				cfg.K8sVIPInterface,
				cfg.K8sVIP),
		}, pulumi.DependsOn([]pulumi.Resource{}))

        if err != nil {
            return nil, fmt.Errorf("failed to setup kube-vip on node %s: %w", node.Name, err)
        }
	}

	return &VIP{IPAddress: cfg.K8sVIP}, nil
}
