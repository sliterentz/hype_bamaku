# hype_bamaku

Pulumi-based Infrastructure as Code (IaC) untuk mereplikasi arsitektur Kubernetes bare-metal di atas Hyper-V (Windows 11/Windows Server) untuk homelab.

Fokus:
- Provisioning VM Hyper-V (kubeadm) dengan spesifikasi default `4 vCPU`, `8GB RAM`, `20GB disk`.
- Cluster Kubernetes dengan `2 worker node` (default), multi-environment (`dev/staging/prod`).
- Add-on bare-metal: `MetalLB` + `Ingress NGINX` + `cert-manager`.
- Observability: `Prometheus + Grafana` (kube-prometheus-stack) + logging terpusat `Loki` (opsional `ECK/ELK operator`).
- Production-grade baseline: CNI dengan NetworkPolicy (Calico), storage (Longhorn), TLS termination, dan opsi backup (Velero).

Dokumentasi detail:
- [architecture.md](file:///d:/Core/hype_bamaku/docs/architecture.md)
- [configuration.md](file:///d:/Core/hype_bamaku/docs/configuration.md)
- [operations.md](file:///d:/Core/hype_bamaku/docs/operations.md)
- [pulumi-env.md](file:///d:/Core/hype_bamaku/docs/pulumi-env.md)

## Prasyarat

- Windows host dengan Hyper-V enabled.
- PowerShell 5+.
- Pulumi CLI.
- Node.js 18+.
- pnpm 9+ (atau install via `npm i -g pnpm`).
- Hyper-V Virtual Switch type `External` (bisa dibuat otomatis jika `hyperv:externalAdapterName` diisi).
- Base image Ubuntu Server 24.04 LTS dalam format `VHDX` (cloud image) yang kompatibel Generation 2.

## Quick start (dev)

1) Install dependencies:

```bash
pnpm install
```

2) Buat stack Pulumi:

```bash
pulumi stack init dev
```

3) Set konfigurasi minimal (contoh):

```bash
pulumi config set env dev
pulumi config set hyperv:switchName "ExternalSwitch"
pulumi config set hyperv:externalAdapterName "Wi-Fi"
pulumi config set hyperv:vmPath "C:\\HyperV\\VMs"
pulumi config set hyperv:baseVhdxPath "C:\\Images\\ubuntu-24.04-base.vhdx"

pulumi config set network:cidr "192.168.0.0/24"
pulumi config set network:gateway "192.168.0.1"
pulumi config set --path network:dnsServers[0] "1.1.1.1"
pulumi config set --path network:dnsServers[1] "8.8.8.8"
pulumi config set network:metallbAddressPool "192.168.0.200-192.168.0.210"

pulumi config set ssh:username "ubuntu"
pulumi config set ssh:publicKey "ssh-ed25519 AAAA..."
pulumi config set ssh:privateKeyPath "C:\\Users\\you\\.ssh\\id_ed25519"

pulumi config set --path nodes:controlPlaneIps[0] "192.168.0.11"
pulumi config set --path nodes:workerIps[0] "192.168.0.12"
pulumi config set --path nodes:workerIps[1] "192.168.0.13"
```

Catatan: jika Anda mengedit file stack `Pulumi.<stack>.yaml` manual, kunci config harus mengikuti format `<namespace>:<name>` (satu `:`). Contoh yang benar: `network:gateway`, bukan `hype-bamaku:network:gateway`.

4) Preview & apply:

```bash
pulumi preview
pulumi up
```

Output `kubeconfig` akan tersedia sebagai output stack (secret).

## Catatan penting

- Untuk `prod`, konfigurasi default mengharuskan `3 control-plane` demi HA stacked etcd.
- Static IP dilakukan via cloud-init (`network-config`). Jika interface name bukan `eth0`, set `network:interfaceName` sesuai hasil `ip a` di VM.
- Logging mode default adalah `loki`. Jika ingin ELK, gunakan mode `elk` (menginstall operator ECK; deployment Elasticsearch/Kibana dibahas di docs).
