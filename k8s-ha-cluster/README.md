# 2-Node HA Kubernetes Cluster (Pulumi + GitOps)

Proyek ini adalah implementasi *Infrastructure as Code (IaC)* untuk membangun Kubernetes Cluster *High Availability* (2 control plane nodes, stacked etcd) di atas Hyper-V.

## 🏗️ Arsitektur
- **IaC Engine**: Pulumi (Go)
- **Hybrid Provisioning**: Terraform digunakan via Pulumi (`local.Command` atau Terraform module interop) untuk provision VM Hyper-V yang saat ini belum sepenuhnya didukung native oleh provider Pulumi.
- **HA Network**: `kube-vip` (Static Pod, ARP mode) untuk *Control Plane Load Balancing* (VIP).
- **K8s Bootstrapping**: `kubeadm` dieksekusi secara remote (via SSH Command).
- **GitOps Alignment**: Setup *opinionated* yang langsung melakukan bootstrap Argo CD dan *ApplicationSet* root untuk GitOps yang tersentralisasi (mirip konsep Kubara Framework).

## 🗂️ Struktur Direktori

```text
k8s-ha-cluster/
├── Pulumi.yaml / Pulumi.dev.yaml # Konfigurasi metadata & stack
├── .env.example                  # Spesifikasi env vars
├── go.mod / go.sum               # Go dependencies
├── cmd/
│   └── main.go                   # Entry point Pulumi
├── pkg/
│   ├── config/                   # Loader environment (.env)
│   ├── hyperv/                   # Provision VM Hyper-V (via Terraform/Command)
│   ├── k8s/                      # Eksekusi kubeadm (init & join control plane)
│   ├── network/                  # Setup Virtual IP (kube-vip)
│   └── gitops/                   # Bootstrap deklaratif Argo CD
├── scripts/
│   ├── cloud-init/               # user-data.yaml untuk VM (install docker, kubeadm)
│   └── kubeadm/                  # bash script wrapper untuk k8s init & join
└── test/
    └── e2e/                      # (Placeholder) Integration test
```

## 🚀 Cara Menjalankan

1. **Persiapan `.env`**
   Salin file env dan sesuaikan spesifikasinya.
   ```bash
   cp .env.example .env
   ```

2. **Inisialisasi Stack**
   ```bash
   pulumi stack init dev
   ```

3. **Install Dependencies**
   ```bash
   go mod tidy
   ```

4. **Eksekusi Provisioning**
   ```bash
   pulumi up
   ```

## 🔐 Manajemen Secrets
Pada proyek ini, kredensial (seperti SSH key atau Token GitOps) disarankan **tidak di-hardcode**. 
Gunakan mekanisme `pulumi config set --secret` atau **Pulumi ESC (Environments, Secrets, and Configuration)**, yang kemudian diakses dari object `ctx *pulumi.Context`.

## 🌐 Terraform Interop untuk Hyper-V
Saat ini, proyek memodelkan `pkg/hyperv/provision.go` dengan eksekusi Command wrapper untuk mengeksekusi Terraform (`terraform apply`). 
Apabila Anda menggunakan [Pulumi Terraform Bridge](https://www.pulumi.com/docs/guides/adopting/from-terraform/), maka Anda dapat mengganti wrapper tersebut dengan SDK Go dari module Terraform tersebut (`pulumi package add terraform-module <source>`).
