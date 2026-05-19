package main

import (
	"fmt"

	"k8s-ha-cluster/pkg/config"
	"k8s-ha-cluster/pkg/gitops"
	"k8s-ha-cluster/pkg/hyperv"
	"k8s-ha-cluster/pkg/k8s"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		// 1. Load Configuration (Env vars / Pulumi.<stack>.yaml)
		cfg, err := config.LoadConfig(ctx)
		if err != nil {
			return fmt.Errorf("gagal memuat konfigurasi: %w", err)
		}

		// ✅ VALIDASI: Pastikan config valid sebelum lanjut
		if err := cfg.Validate(); err != nil {
			return fmt.Errorf("konfigurasi tidak valid: %w", err)
		}

		// 2. Provision Hyper-V VMs (Terraform Interop)
		nodes, err := hyperv.ProvisionNodes(ctx, cfg)
		if err != nil {
			return fmt.Errorf("gagal provision node Hyper-V: %w", err)
		}

		// ✅ VALIDASI: Pastikan nodes tidak kosong
		if len(nodes) == 0 {
			return fmt.Errorf("tidak ada node yang berhasil di-provision")
		}

		// ✅ WAIT: Tunggu semua nodes ready sebelum bootstrap
		ctx.Log.Info("⏳ Waiting for all nodes to be ready...", nil)

		// 3. Bootstrap Kubernetes Cluster untuk HA Control Plane (termasuk VIP setup)
		cluster, err := k8s.Bootstrap(ctx, cfg, nodes)
		if err != nil {
			return fmt.Errorf("gagal bootstrap k8s: %w", err)
		}

		preflight := cluster.KubeConfig.ApplyT(func(kubeconfig string) (string, error) {
			return k8s.PreflightKubeConfig(kubeconfig)
		}).(pulumi.StringOutput)

		// 5. GitOps Bootstrap (Argo CD)
		err = gitops.BootstrapArgoCD(ctx, cfg, cluster, preflight)
		if err != nil {
			return fmt.Errorf("gagal setup GitOps: %w", err)
		}

		// Outputs
		ctx.Export("controlPlaneVIP", pulumi.String(cfg.K8sVIP))
		ctx.Export("kubeconfig", cluster.KubeConfig)
		ctx.Export("k8sPreflight", preflight)

		// ✅ Export node IPs untuk debugging
		for i, node := range nodes {
			ctx.Export(fmt.Sprintf("node%d_ip", i), node.IPAddress)
		}

		return nil
	})
}
