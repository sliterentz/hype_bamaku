package gitops

import (
	"github.com/pulumi/pulumi-command/sdk/go/command/local"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"

	"k8s-ha-cluster/pkg/config"
	"k8s-ha-cluster/pkg/k8s"
)

func BootstrapArgoCD(ctx *pulumi.Context, cfg *config.Config, cluster *k8s.Cluster, preflight pulumi.StringInput) error {
	// Menjalankan instalasi Argo CD secara deklaratif
	// (Menggunakan kubeconfig dari bootstrap phase)
	_, err := local.NewCommand(ctx, "install-argocd", &local.CommandArgs{
		Create: pulumi.Sprintf(`
			echo "%s" > /dev/null
			# Menyimpan kubeconfig sementara
			echo "%s" > /tmp/kubeconfig-%s
			export KUBECONFIG=/tmp/kubeconfig-%s
			
			kubectl create namespace argocd --dry-run=client -o yaml | kubectl apply -f -
			kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
			
			# Bootstrap ApplicationSet untuk struktur repo GitOps
			cat <<EOF | kubectl apply -f -
			apiVersion: argoproj.io/v1alpha1
			kind: Application
			metadata:
			  name: %s-root
			  namespace: argocd
			spec:
			  project: default
			  source:
			    repoURL: '%s'
			    targetRevision: '%s'
			    path: bootstrap
			  destination:
			    server: 'https://kubernetes.default.svc'
			    namespace: argocd
			  syncPolicy:
			    automated:
			      prune: true
			      selfHeal: true
			EOF
		`, preflight, cluster.KubeConfig, cfg.PulumiStack, cfg.PulumiStack, cfg.ArgoCDProjectPrefix, cfg.GitOpsRepoURL, cfg.GitOpsBranch),
	})

	return err
}
