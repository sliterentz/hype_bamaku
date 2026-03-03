import * as pulumi from "@pulumi/pulumi";
import * as k8s from "@pulumi/kubernetes";
import * as helm from "@pulumi/kubernetes/helm/v3";
import { AppConfig } from "../../config";

export function installAddons(name: string, cfg: AppConfig, provider: k8s.Provider) {
  const namespaces = {
    tigera: new k8s.core.v1.Namespace(
      `${name}-ns-tigera`,
      { metadata: { name: "tigera-operator" } },
      { provider }
    ),
    metallb: new k8s.core.v1.Namespace(
      `${name}-ns-metallb`,
      { metadata: { name: "metallb-system" } },
      { provider }
    ),
    ingress: new k8s.core.v1.Namespace(
      `${name}-ns-ingress`,
      { metadata: { name: "ingress-nginx" } },
      { provider }
    ),
    certManager: new k8s.core.v1.Namespace(
      `${name}-ns-cert-manager`,
      { metadata: { name: "cert-manager" } },
      { provider }
    ),
    longhorn: new k8s.core.v1.Namespace(
      `${name}-ns-longhorn`,
      { metadata: { name: "longhorn-system" } },
      { provider }
    ),
    monitoring: new k8s.core.v1.Namespace(
      `${name}-ns-monitoring`,
      { metadata: { name: "monitoring" } },
      { provider }
    ),
    logging: new k8s.core.v1.Namespace(
      `${name}-ns-logging`,
      { metadata: { name: "logging" } },
      { provider }
    ),
    apps: new k8s.core.v1.Namespace(
      `${name}-ns-apps`,
      { metadata: { name: "apps" } },
      { provider }
    )
  };

  const calico = new helm.Chart(
    `${name}-calico`,
    {
      namespace: "tigera-operator",
      chart: "tigera-operator",
      fetchOpts: { repo: "https://docs.tigera.io/calico/charts" },
      values: {
        installation: {
          kubernetesProvider: "k8s",
          calicoNetwork: {
            ipPools: [
              {
                cidr: cfg.kubernetes.podCidr,
                encapsulation: "VXLANCrossSubnet",
                natOutgoing: "Enabled",
                nodeSelector: "all()"
              }
            ]
          }
        }
      }
    },
    { provider, dependsOn: [namespaces.tigera] }
  );

  new helm.Chart(
    `${name}-metrics-server`,
    {
      namespace: "kube-system",
      chart: "metrics-server",
      fetchOpts: { repo: "https://kubernetes-sigs.github.io/metrics-server/" },
      values: {
        args: ["--kubelet-insecure-tls"]
      }
    },
    { provider, dependsOn: [calico] }
  );

  const metallb = new helm.Chart(
    `${name}-metallb`,
    {
      namespace: namespaces.metallb.metadata.name,
      chart: "metallb",
      fetchOpts: { repo: "https://metallb.github.io/metallb" }
    },
    { provider, dependsOn: [namespaces.metallb] }
  );

  new k8s.apiextensions.CustomResource(
    `${name}-metallb-pool`,
    {
      apiVersion: "metallb.io/v1beta1",
      kind: "IPAddressPool",
      metadata: { name: "lan-pool", namespace: "metallb-system" },
      spec: {
        addresses: [cfg.network.metallbAddressPool]
      }
    },
    { provider, dependsOn: [metallb] }
  );

  new k8s.apiextensions.CustomResource(
    `${name}-metallb-l2adv`,
    {
      apiVersion: "metallb.io/v1beta1",
      kind: "L2Advertisement",
      metadata: { name: "lan-l2", namespace: "metallb-system" },
      spec: { ipAddressPools: ["lan-pool"] }
    },
    { provider, dependsOn: [metallb] }
  );

  const ingress = new helm.Chart(
    `${name}-ingress-nginx`,
    {
      namespace: namespaces.ingress.metadata.name,
      chart: "ingress-nginx",
      fetchOpts: { repo: "https://kubernetes.github.io/ingress-nginx" },
      values: {
        controller: {
          service: {
            type: "LoadBalancer"
          },
          admissionWebhooks: { enabled: true },
          metrics: { enabled: true }
        }
      }
    },
    { provider, dependsOn: [namespaces.ingress, metallb] }
  );

  const certManager = new helm.Chart(
    `${name}-cert-manager`,
    {
      namespace: namespaces.certManager.metadata.name,
      chart: "cert-manager",
      fetchOpts: { repo: "https://charts.jetstack.io" },
      values: {
        installCRDs: true
      }
    },
    { provider, dependsOn: [namespaces.certManager] }
  );

  if (cfg.ingress.tlsMode === "selfsigned") {
    new k8s.apiextensions.CustomResource(
      `${name}-issuer-selfsigned`,
      {
        apiVersion: "cert-manager.io/v1",
        kind: "ClusterIssuer",
        metadata: { name: "selfsigned" },
        spec: { selfSigned: {} }
      },
      { provider, dependsOn: [certManager] }
    );
  }

  if (cfg.ingress.tlsMode === "letsencrypt") {
    new k8s.apiextensions.CustomResource(
      `${name}-issuer-letsencrypt`,
      {
        apiVersion: "cert-manager.io/v1",
        kind: "ClusterIssuer",
        metadata: { name: "letsencrypt-http01" },
        spec: {
          acme: {
            email: cfg.ingress.letsEncryptEmail,
            server: "https://acme-v02.api.letsencrypt.org/directory",
            privateKeySecretRef: { name: "letsencrypt-http01" },
            solvers: [
              {
                http01: {
                  ingress: { class: "nginx" }
                }
              }
            ]
          }
        }
      },
      { provider, dependsOn: [certManager, ingress] }
    );
  }

  const longhorn = new helm.Chart(
    `${name}-longhorn`,
    {
      namespace: namespaces.longhorn.metadata.name,
      chart: "longhorn",
      fetchOpts: { repo: "https://charts.longhorn.io" },
      values: {
        defaultSettings: {
          defaultReplicaCount: cfg.env === "prod" ? 3 : 2
        }
      }
    },
    { provider, dependsOn: [namespaces.longhorn] }
  );

  if (cfg.observability.prometheusGrafana) {
    const grafanaHosts = cfg.ingress.baseDomain ? [`grafana.${cfg.ingress.baseDomain}`] : [];

    const issuer = cfg.ingress.tlsMode === "letsencrypt" ? "letsencrypt-http01" : "selfsigned";
    const grafanaTls = cfg.ingress.baseDomain
      ? [
          {
            secretName: "grafana-tls",
            hosts: [`grafana.${cfg.ingress.baseDomain}`]
          }
        ]
      : [];

    new helm.Chart(
      `${name}-kube-prom-stack`,
      {
        namespace: namespaces.monitoring.metadata.name,
        chart: "kube-prometheus-stack",
        fetchOpts: { repo: "https://prometheus-community.github.io/helm-charts" },
        values: {
          grafana: {
            ingress: {
              enabled: cfg.ingress.baseDomain ? true : false,
              ingressClassName: "nginx",
              annotations: {
                "cert-manager.io/cluster-issuer": issuer
              },
              hosts: grafanaHosts,
              path: "/",
              tls: grafanaTls
            }
          }
        }
      },
      { provider, dependsOn: [namespaces.monitoring, ingress, certManager] }
    );
  }

  if (cfg.observability.logging === "loki") {
    new helm.Chart(
      `${name}-loki`,
      {
        namespace: namespaces.logging.metadata.name,
        chart: "loki",
        fetchOpts: { repo: "https://grafana.github.io/helm-charts" },
        values: {
          loki: {
            commonConfig: { replication_factor: cfg.env === "prod" ? 3 : 1 },
            storage: {
              type: "filesystem"
            }
          },
          singleBinary: {
            replicas: cfg.env === "prod" ? 3 : 1
          }
        }
      },
      { provider, dependsOn: [namespaces.logging] }
    );

    new helm.Chart(
      `${name}-promtail`,
      {
        namespace: namespaces.logging.metadata.name,
        chart: "promtail",
        fetchOpts: { repo: "https://grafana.github.io/helm-charts" },
        values: {
          config: {
            clients: [
              {
                url: "http://loki:3100/loki/api/v1/push"
              }
            ]
          }
        }
      },
      { provider, dependsOn: [namespaces.logging] }
    );
  }

  if (cfg.observability.logging === "elk") {
    new helm.Chart(
      `${name}-eck-operator`,
      {
        namespace: namespaces.logging.metadata.name,
        chart: "eck-operator",
        fetchOpts: { repo: "https://helm.elastic.co" }
      },
      { provider, dependsOn: [namespaces.logging] }
    );
  }

  new k8s.networking.v1.NetworkPolicy(
    `${name}-apps-deny-all`,
    {
      metadata: { name: "default-deny", namespace: "apps" },
      spec: {
        podSelector: {},
        policyTypes: ["Ingress", "Egress"]
      }
    },
    { provider, dependsOn: [namespaces.apps] }
  );

  new k8s.networking.v1.NetworkPolicy(
    `${name}-apps-allow-dns`,
    {
      metadata: { name: "allow-dns", namespace: "apps" },
      spec: {
        podSelector: {},
        policyTypes: ["Egress"],
        egress: [
          {
            to: [
              {
                namespaceSelector: {
                  matchLabels: { "kubernetes.io/metadata.name": "kube-system" }
                }
              }
            ],
            ports: [
              { protocol: "UDP", port: 53 },
              { protocol: "TCP", port: 53 }
            ]
          }
        ]
      }
    },
    { provider, dependsOn: [namespaces.apps] }
  );

  if (cfg.backup.enableVelero) {
    const veleroNs = new k8s.core.v1.Namespace(
      `${name}-ns-velero`,
      { metadata: { name: "velero" } },
      { provider }
    );
    new helm.Chart(
      `${name}-velero`,
      {
        namespace: "velero",
        chart: "velero",
        fetchOpts: { repo: "https://vmware-tanzu.github.io/helm-charts" },
        values: {
          configuration: {
            provider: cfg.backup.veleroProvider === "aws" ? "aws" : "aws",
            backupStorageLocation: [
              {
                name: "default",
                provider: "aws",
                bucket: cfg.backup.veleroBucket,
                config: cfg.backup.veleroProvider === "minio" ? { s3Url: cfg.backup.veleroS3Url, s3ForcePathStyle: "true" } : {}
              }
            ]
          },
          initContainers: [
            {
              name: "velero-plugin-for-aws",
              image: "velero/velero-plugin-for-aws:v1.10.0",
              volumeMounts: [{ mountPath: "/target", name: "plugins" }]
            }
          ]
        }
      },
      { provider, dependsOn: [veleroNs] }
    );
  }

  return { namespaces, calico, metallb, ingress, certManager, longhorn };
}
