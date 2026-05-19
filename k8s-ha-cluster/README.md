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

## 🧰 Runbook: Menambah Control Plane Node

Prosedur ini mengikuti pola pemindahan artefak yang sama dengan `kube-vip.yaml`: transfer terkontrol, verifikasi integritas, penyesuaian izin akses, lalu validasi pra-kondisi sebelum `kubeadm join`.

1. **Siapkan node control-plane baru**
   - Pastikan OS prereq terpenuhi (containerd aktif, swap off, time sync memadai).
   - Pastikan konektivitas ke VIP/API server tersedia.

2. **Stage `kube-vip.yaml` (belum aktif)**
   - Pastikan file berada di `/etc/kubernetes/kube-vip/kube-vip.yaml` pada node baru.
   - Jangan meletakkan `kube-vip.yaml` di `/etc/kubernetes/manifests/` sebelum join selesai untuk mencegah takeover VIP.

3. **Pre-copy sertifikat wajib ke node baru**
   - Sertifikat yang wajib ada di `/etc/kubernetes/pki/`:
     - `ca.crt`, `ca.key`
     - `etcd/ca.crt`, `etcd/ca.key`
     - `front-proxy-ca.crt`, `front-proxy-ca.key`
     - `sa.pub`, `sa.key`
   - Mekanisme: dikemas sebagai tar.gz di node0, dihitung checksum SHA-256, ditransfer, diverifikasi checksum-nya di node tujuan, lalu diekstrak ke `/etc/kubernetes/pki/`.
   - Izin akses:
     - `600` untuk file kunci privat (`*.key`)
     - `644` untuk sertifikat publik (`*.crt`) dan `sa.pub`

4. **Validasi readiness sebelum join**
   - Jalankan validasi artefak di node baru:
     ```bash
     sudo bash scripts/validate-join-artifacts.sh
     ```
   - Pastikan seluruh sertifikat wajib terdeteksi, format valid, dan permission sesuai.

5. **Jalankan `kubeadm join --control-plane`**
   - Gunakan join command yang dihasilkan oleh node0 (termasuk `--certificate-key`).
   - Setelah join sukses, baru aktifkan kube-vip dengan memindahkan manifest ke `/etc/kubernetes/manifests/`.

6. **Catatan penting: Endpoint domain vs VIP (anti self-target)**
   - Jika `K8sDOMAIN` mengarah ke domain (mis. `dev.homelab.com`), pastikan DNS A record domain tersebut menunjuk ke VIP (mis. `192.168.1.100`), bukan ke salah satu IP node control-plane.
   - Mekanisme join di automation ini akan:
     - Menghapus mapping lama domain di `/etc/hosts` (jika ada),
     - Menulis ulang mapping domain → VIP,
     - Memaksa `kubeadm join` menggunakan VIP untuk menghindari drift DNS/hosts yang dapat menyebabkan node menarget dirinya sendiri (`connect: connection refused`).

7. **Uji end-to-end di staging**
   - Lakukan join control-plane di environment staging terlebih dahulu untuk memastikan prosedur pre-copy ini tidak memicu failure di fase join dan semua komponen (etcd, apiserver, kube-vip) stabil.

## 🔐 Manajemen Secrets
Pada proyek ini, kredensial (seperti SSH key atau Token GitOps) disarankan **tidak di-hardcode**. 
Gunakan mekanisme `pulumi config set --secret` atau **Pulumi ESC (Environments, Secrets, and Configuration)**, yang kemudian diakses dari object `ctx *pulumi.Context`.

## 🌐 Terraform Interop untuk Hyper-V
Saat ini, proyek memodelkan `pkg/hyperv/provision.go` dengan eksekusi Command wrapper untuk mengeksekusi Terraform (`terraform apply`). 
Apabila Anda menggunakan [Pulumi Terraform Bridge](https://www.pulumi.com/docs/guides/adopting/from-terraform/), maka Anda dapat mengganti wrapper tersebut dengan SDK Go dari module Terraform tersebut (`pulumi package add terraform-module <source>`).
