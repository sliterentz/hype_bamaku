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

	// 1. Kubeadm Init pada Node Pertama
	initCmd, err := remote.NewCommand(ctx, "kubeadm-init", &remote.CommandArgs{
		Connection: &remote.ConnectionArgs{
			Host: nodes[0].IPAddress,
			User: pulumi.String("ubuntu"),
		},
		Create: pulumi.Sprintf("sudo kubeadm init --control-plane-endpoint %s:6443 --upload-certs --pod-network-cidr=%s && cat /etc/kubernetes/admin.conf", vip.IPAddress, cfg.K8sPodCIDR),
	})
	if err != nil {
		return nil, err
	}

	// Catatan: Parsing token dan cert-hash harus diambil dari output initCmd untuk command join.
	// Di sini kami gunakan placeholder asumsi bahwa script di node-2 dapat mengambil token
	// yang digenerate oleh initCmd secara out-of-band (atau via SSM/Vault/Pulumi Output).

	// 2. Kubeadm Join pada Node Lainnya (Stacked Control Plane)
	for i := 1; i < len(nodes); i++ {
		_, err = remote.NewCommand(ctx, fmt.Sprintf("kubeadm-join-%d", i), &remote.CommandArgs{
			Connection: &remote.ConnectionArgs{
				Host: nodes[i].IPAddress,
				User: pulumi.String("ubuntu"),
			},
			// Pada praktiknya, ganti string di bawah dengan token dinamis dari initCmd
			Create: pulumi.Sprintf("sudo bash /tmp/join.sh"),
		}, pulumi.DependsOn([]pulumi.Resource{initCmd}))
		if err != nil {
			return nil, err
		}
	}

	return &Cluster{
		KubeConfig: initCmd.Stdout, // Output KUBECONFIG dari node 1
	}, nil
}
