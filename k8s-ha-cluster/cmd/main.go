package main

import (
	"fmt"

	"k8s-ha-cluster/pkg/config"
	"k8s-ha-cluster/pkg/gitops"
	"k8s-ha-cluster/pkg/hyperv"
	"k8s-ha-cluster/pkg/k8s"
	"k8s-ha-cluster/pkg/network"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		// 1. Load Configuration (Env vars / Pulumi.<stack>.yaml)
		cfg, err := config.LoadConfig(ctx)
		if err != nil {
			return fmt.Errorf("gagal memuat konfigurasi: %w", err)
		}

		// 2. Provision Hyper-V VMs (Terraform Interop)
		nodes, err := hyperv.ProvisionNodes(ctx, cfg)
		if err != nil {
			return fmt.Errorf("gagal provision node Hyper-V: %w", err)
		}

		// 3. Setup Virtual IP untuk HA Control Plane (kube-vip)
		vip, err := network.SetupVIP(ctx, cfg, nodes)
		if err != nil {
			return fmt.Errorf("gagal konfigurasi VIP: %w", err)
		}

		// 4. Bootstrap Kubernetes Cluster (kubeadm)
		cluster, err := k8s.Bootstrap(ctx, cfg, nodes, vip)
		if err != nil {
			return fmt.Errorf("gagal bootstrap k8s: %w", err)
		}

		// 5. GitOps Bootstrap (Argo CD)
		err = gitops.BootstrapArgoCD(ctx, cfg, cluster)
		if err != nil {
			return fmt.Errorf("gagal setup GitOps: %w", err)
		}

		// Outputs
		ctx.Export("controlPlaneVIP", pulumi.String(vip.IPAddress))

		return nil
	})
}
