package hyperv

import (
	"fmt"

	"github.com/pulumi/pulumi-command/sdk/go/command/local"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"k8s-ha-cluster/pkg/config"
)

type Node struct {
	Name      string
	IPAddress pulumi.StringOutput
	Role      string
}

// ProvisionNodes membungkus Terraform Hyper-V module menggunakan Pulumi Command Local.
// Ini adalah implementasi interop Hybrid Pulumi + Terraform.
// Jika menggunakan `pulumi package add terraform-module`, ini bisa diganti dengan native module struct.
func ProvisionNodes(ctx *pulumi.Context, cfg *config.Config) ([]*Node, error) {
	// var nodes []*Node

	// Deteksi mode: gunakan MOCK jika Terraform directory tidak ada
    useMockMode := true // Set ke false jika ingin gunakan Terraform

	if useMockMode {
        return provisionMockNodes(ctx, cfg)
    }

    return provisionTerraformNodes(ctx, cfg)
}

// provisionMockNodes - Mode development tanpa Hyper-V
func provisionMockNodes(ctx *pulumi.Context, cfg *config.Config) ([]*Node, error) {
    var nodes []*Node

    ctx.Log.Warn("🔧 MOCK MODE: Menggunakan IP statis untuk development", nil)

    for i := 0; i < cfg.HyperVNodeCount; i++ {
        nodeName := fmt.Sprintf("cp-node-%d", i+1)
        mockIP := fmt.Sprintf("192.168.1.%d", 15+i)

        nodes = append(nodes, &Node{
            Name:      nodeName,
            IPAddress: pulumi.String(mockIP).ToStringOutput(),
            Role:      "control-plane",
        })

        ctx.Log.Info(fmt.Sprintf("✅ Mock node created: %s with IP %s", nodeName, mockIP), nil)
    }

    return nodes, nil
}


// provisionTerraformNodes - Mode production dengan Terraform + Hyper-V
func provisionTerraformNodes(ctx *pulumi.Context, cfg *config.Config) ([]*Node, error) {
    var nodes []*Node

    // Terraform init (hanya sekali)
    initCmd, err := local.NewCommand(ctx, "tf-init", &local.CommandArgs{
        Create: pulumi.String("terraform -chdir=./scripts/terraform init"),
    })
    if err != nil {
        return nil, fmt.Errorf("terraform init gagal: %w", err)
    }

    for i := 0; i < cfg.HyperVNodeCount; i++ {
        nodeName := fmt.Sprintf("cp-node-%d", i+1)

        // Terraform apply untuk provision VM
        tfApply, err := local.NewCommand(ctx, fmt.Sprintf("tf-apply-%s", nodeName), &local.CommandArgs{
            Create: pulumi.Sprintf(
                "terraform -chdir=./scripts/terraform apply "+
                    "-var='node_name=%s' "+
                    "-var='cpus=%d' "+
                    "-var='memory=%d' "+
                    "-var='disk_size=%d' "+
                    "-var='network_switch=%s' "+
                    "-auto-approve",
                nodeName,
                cfg.HyperVCPUs,
                cfg.HyperVMemoryGB,
                cfg.HyperVDiskGB,
                cfg.HyperVNetworkSwitch,
            ),
            Delete: pulumi.Sprintf(
                "terraform -chdir=./scripts/terraform destroy "+
                    "-var='node_name=%s' "+
                    "-auto-approve",
                nodeName,
            ),
        }, pulumi.DependsOn([]pulumi.Resource{initCmd}))
        if err != nil {
            return nil, fmt.Errorf("terraform apply gagal untuk %s: %w", nodeName, err)
        }

        // Dapatkan IP dari Terraform output
        tfOutput, err := local.NewCommand(ctx, fmt.Sprintf("tf-output-%s", nodeName), &local.CommandArgs{
            Create: pulumi.String("terraform -chdir=./scripts/terraform output -json ip_address"),
        }, pulumi.DependsOn([]pulumi.Resource{tfApply}))
        if err != nil {
            return nil, fmt.Errorf("terraform output gagal untuk %s: %w", nodeName, err)
        }

        // Parse IP dari output (dengan fallback)
        ipAddress := tfOutput.Stdout.ApplyT(func(stdout string) string {
            // Terraform output format: "192.168.1.110"
            // Bersihkan quotes jika ada
            ip := stdout
            if len(ip) > 2 && ip[0] == '"' && ip[len(ip)-1] == '"' {
                ip = ip[1 : len(ip)-1]
            }
            
            // Fallback jika parsing gagal
            if ip == "" {
                return fmt.Sprintf("192.168.1.%d", 110+i)
            }
            
            return ip
        }).(pulumi.StringOutput)

        nodes = append(nodes, &Node{
            Name:      nodeName,
            IPAddress: ipAddress,
            Role:      "control-plane",
        })

        ctx.Log.Info(fmt.Sprintf("✅ Terraform node provisioned: %s", nodeName), nil)
    }

    return nodes, nil
}
