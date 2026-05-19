# 🚀 K8s-HA-Cluster: Otomasi Klaster Kubernetes High-Availability di Hyper-V dengan Pulumi & GitOps

![GitHub Workflow Status](https://img.shields.io/badge/Build%20Status-Passing-brightgreen)
![Kubernetes Version](https://img.shields.io/badge/Kubernetes-v1.35.0-blue)
![IaC Tool](https://img.shields.io/badge/IaC-Pulumi%20%28Go%29-orange)
![Platform](https://img.shields.io/badge/Platform-Hyper--V-informational)

Proyek ini menyediakan solusi *Infrastructure as Code (IaC)* yang komprehensif untuk membangun klaster Kubernetes *High Availability* (HA) dengan dua node control plane (menggunakan stacked etcd) di lingkungan virtualisasi Hyper-V. Dengan memanfaatkan Pulumi dan prinsip-prinsip GitOps, Anda dapat dengan mudah melakukan *provisioning*, konfigurasi, dan pengelolaan klaster Kubernetes yang tangguh dan siap produksi.

**Tujuan Utama:**
- Menyediakan klaster Kubernetes HA yang otomatis dan dapat direplikasi.
- Memastikan ketersediaan tinggi untuk komponen control plane.
- Mengintegrasikan praktik GitOps untuk manajemen aplikasi dan konfigurasi klaster.
- Memfasilitasi pengembangan dan pengujian di lingkungan virtualisasi lokal (Hyper-V).

## 📚 Daftar Isi
- [🚀 K8s-HA-Cluster: Otomasi Klaster Kubernetes High-Availability di Hyper-V dengan Pulumi & GitOps](#--k8s-ha-cluster-otomasi-klaster-kubernetes-high-availability-di-hyper-v-dengan-pulumi--gitops)
- [📚 Daftar Isi](#-daftar-isi)
- [🏗️ Arsitektur Klaster](#️-arsitektur-klaster)
  - [Komponen Utama](#komponen-utama)
  - [Diagram Topologi Klaster](#diagram-topologi-klaster)
- [⚙️ Prasyarat](#️-prasyarat)
  - [Spesifikasi Sumber Daya Minimal](#spesifikasi-sumber-daya-minimal)
  - [Perangkat Lunak & Tools](#perangkat-lunak--tools)
- [🚀 Panduan Instalasi](#-panduan-instalasi)
  - [1. Persiapan Lingkungan](#1-persiapan-lingkungan)
  - [2. Inisialisasi Proyek Pulumi](#2-inisialisasi-proyek-pulumi)
  - [3. Eksekusi Provisioning](#3-eksekusi-provisioning)
  - [4. Verifikasi Klaster](#4-verifikasi-klaster)
- [🧰 Panduan Operasional Harian](#-panduan-operasional-harian)
  - [Memantau Kesehatan Klaster](#memantau-kesehatan-klaster)
  - [Menambah Node Control Plane](#menambah-node-control-plane)
  - [Menambah/Menghapus Node Pekerja (Worker Node)](#menambahmenghapus-node-pekerja-worker-node)
  - [Upgrade Versi Kubernetes](#upgrade-versi-kubernetes)
  - [Skenario Pemulihan Bencana](#skenario-pemulihan-bencana)
- [🔒 Catatan Keamanan](#-catatan-keamanan)
  - [Manajemen Secrets](#manajemen-secrets)
  - [Praktik Terbaik Keamanan Klaster](#praktik-terbaik-keamanan-klaster)
- [Troubleshooting Umum](#troubleshooting-umum)
  - [`pulumi up` Gagal karena Lock File Pulumi](#pulumi-up-gagal-karena-lock-file-pulumi)
  - [`kubeadm join` Gagal (Self-Target API Server)](#kubeadm-join-gagal-self-target-api-server)
  - [Sertifikat PKI Hilang/Tidak Valid](#sertifikat-pki-hilangtidak-valid)
- [🤝 Kontribusi](#-kontribusi)
- [📞 Kontak](#-kontak)

## 🏗️ Arsitektur Klaster

Klaster ini dirancang untuk ketersediaan tinggi dengan dua node control plane yang menjalankan etcd dalam mode *stacked*. Seluruh infrastruktur di-provision di atas Hyper-V dan dikelola menggunakan Pulumi.

### Komponen Utama
-   **IaC Engine**: [Pulumi (Go)](https://www.pulumi.com/) digunakan untuk mendefinisikan, melakukan *provisioning*, dan mengelola seluruh infrastruktur klaster secara deklaratif.
-   **Hybrid Provisioning**: VM Hyper-V di-provision menggunakan Terraform melalui interop Pulumi (`local.Command` atau Pulumi Terraform Bridge), karena dukungan native Pulumi untuk Hyper-V masih berkembang.
-   **HA Network**: [kube-vip](https://kube-vip.io/) (sebagai Static Pod dalam mode ARP) digunakan untuk menyediakan *Virtual IP (VIP)* yang mengambang, memastikan *Control Plane Load Balancing* dan ketersediaan API Server.
-   **K8s Bootstrapping**: [kubeadm](https://kubernetes.io/docs/setup/production/reference/kubeadm/) dieksekusi secara remote melalui SSH Command untuk menginisialisasi klaster dan menambahkan node control plane.
-   **GitOps Alignment**: Klaster ini disiapkan untuk integrasi GitOps dengan bootstrap [Argo CD](https://argoproj.github.io/cd/) dan *ApplicationSet root*, mengikuti konsep manajemen aplikasi terpusat.

### Diagram Topologi Klaster

```mermaid
graph TD
    subgraph Hyper-V Host
        subgraph Control Plane Node 1 (k8s-ha-cp-1)
            kubelet1[Kubelet]
            kube-apiserver1[kube-apiserver]
            kube-scheduler1[kube-scheduler]
            kube-controller-manager1[kube-controller-manager]
            etcd1[etcd]
            kube-vip-pod1[kube-vip Pod]
        end

        subgraph Control Plane Node 2 (k8s-ha-cp-2)
            kubelet2[Kubelet]
            kube-apiserver2[kube-apiserver]
            kube-scheduler2[kube-scheduler]
            kube-controller-manager2[kube-controller-manager]
            etcd2[etcd]
            kube-vip-pod2[kube-vip Pod]
        end

        subgraph Worker Node (Optional)
            kubeletW[Kubelet]
            kube-proxyW[Kube-Proxy]
            containerdW[Containerd]
        end

        VIP_LB(Virtual IP Load Balancer)
    end

    User[Pengguna/kubectl] --> VIP_LB
    VIP_LB --> kube-apiserver1
    VIP_LB --> kube-apiserver2

    kube-apiserver1 <--> etcd1
    kube-apiserver1 <--> etcd2
    kube-apiserver2 <--> etcd1
    kube-apiserver2 <--> etcd2

    kube-apiserver1 <--> kubelet1
    kube-apiserver1 <--> kubelet2
    kube-apiserver2 <--> kubelet1
    kube-apiserver2 <--> kubelet2

    kube-apiserver1 <--> kube-scheduler1
    kube-apiserver1 <--> kube-controller-manager1
    kube-apiserver2 <--> kube-scheduler2
    kube-apiserver2 <--> kube-controller-manager2

    kube-vip-pod1 -- manages --> VIP_LB
    kube-vip-pod2 -- manages --> VIP_LB

    kube-apiserver1 <--> kubeletW
    kube-apiserver2 <--> kubeletW
```
*Diagram ini menunjukkan topologi klaster HA dengan dua node control plane dan satu VIP yang dikelola oleh kube-vip. Node worker dapat ditambahkan sesuai kebutuhan.*

## ⚙️ Prasyarat

Sebelum memulai, pastikan lingkungan Anda memenuhi persyaratan berikut:

### Spesifikasi Sumber Daya Minimal

| Komponen           | CPU (vCore) | RAM (GiB) | Disk (GiB) | Catatan                                          |
| :----------------- | :---------- | :-------- | :--------- | :----------------------------------------------- |
| **Control Plane**  | 2           | 4         | 40         | Per node. Direkomendasikan 2 node.               |
| **Worker Node**    | 2           | 2         | 20         | Per node. Dapat disesuaikan.                     |
| **Hyper-V Host**   | 8+          | 16+       | 200+       | Tergantung jumlah dan spesifikasi VM.            |

### Perangkat Lunak & Tools

-   **Sistem Operasi Host**: Windows 10/11 Pro/Enterprise atau Windows Server dengan fitur Hyper-V aktif.
-   **Go**: Versi 1.20+ ([instalasi](https://go.dev/doc/install)).
-   **Pulumi CLI**: Versi terbaru ([instalasi](https://www.pulumi.com/docs/get-started/install/)).
-   **Terraform CLI**: Versi 1.0+ ([instalasi](https://developer.hashicorp.com/terraform/downloads)). Diperlukan untuk provisioning VM Hyper-V.
-   **SSH Client**: `ssh` dan `scp` harus tersedia di PATH sistem Anda.
-   **`sudo` (di VM)**: User SSH yang digunakan harus memiliki akses `sudo` tanpa password (NOPASSWD) di VM target.
-   **Konektivitas Jaringan**: Pastikan host Hyper-V dan VM memiliki konektivitas jaringan yang memadai, termasuk akses internet untuk mengunduh paket.

## 🚀 Panduan Instalasi

Ikuti langkah-langkah ini untuk melakukan *provisioning* klaster Kubernetes HA Anda.

### 1. Persiapan Lingkungan

1.  **Kloning Repositori:**
    ```bash
    git clone https://github.com/your-repo/k8s-ha-cluster.git
    cd k8s-ha-cluster
    ```

2.  **Konfigurasi Variabel Lingkungan (`.env`):**
    Salin file contoh `.env.example` dan sesuaikan nilainya.
    ```bash
    cp .env.example .env
    # Edit .env dengan editor favorit Anda
    ```
    Pastikan Anda mengonfigurasi `K8sVIP`, `K8sDOMAIN`, `SSHPrivateKey`, `SSHUser`, dan parameter lainnya sesuai lingkungan Anda.

3.  **Instal Dependensi Go:**
    ```bash
    go mod tidy
    ```

### 2. Inisialisasi Proyek Pulumi

1.  **Login ke Pulumi Backend:**
    Jika Anda menggunakan Pulumi Cloud, pastikan Anda sudah login:
    ```bash
    pulumi login
    ```
    Jika menggunakan backend lokal atau S3/Azure Blob, konfigurasikan sesuai dokumentasi Pulumi.

2.  **Pilih atau Buat Stack Pulumi:**
    ```bash
    pulumi stack select dev --create
    ```

3.  **Konfigurasi Secrets:**
    Untuk informasi sensitif seperti SSH Private Key, gunakan Pulumi Secrets:
    ```bash
    pulumi config set --secret sshPrivateKey < ~/.ssh/id_rsa
    # Atau jika key ada di file lain:
    # pulumi config set --secret sshPrivateKey --file path/to/your/private_key
    ```
    Gunakan juga untuk secrets lain seperti token GitOps jika diperlukan.

### 3. Eksekusi Provisioning

Jalankan perintah `pulumi up` untuk memulai *provisioning* infrastruktur dan klaster Kubernetes.

```bash
pulumi up
```
Pulumi akan menampilkan rencana perubahan. Tinjau dengan seksama, lalu konfirmasi dengan memilih `yes`. Proses ini akan:
-   Membuat VM Hyper-V.
-   Menginstal dependensi OS di VM.
-   Menginisialisasi node control plane pertama dengan `kubeadm`.
-   Menyiapkan `kube-vip` untuk HA API Server.
-   Melakukan *pre-copy* sertifikat PKI ke node control plane kedua.
-   Menambahkan node control plane kedua ke klaster.
-   Melakukan bootstrap Argo CD (jika dikonfigurasi).

### 4. Verifikasi Klaster

Setelah `pulumi up` selesai, verifikasi status klaster Anda:

1.  **Akses Kubeconfig:**
    Kubeconfig akan secara otomatis disalin ke `$HOME/.kube/config` di node control plane pertama. Anda dapat mengambilnya dari output Pulumi atau langsung dari VM.

2.  **Periksa Status Node:**
    ```bash
    kubectl get nodes -o wide
    ```
    Pastikan kedua node control plane berstatus `Ready`.

3.  **Periksa Pod Sistem:**
    ```bash
    kubectl get pods -n kube-system -o wide
    ```
    Pastikan semua pod sistem (termasuk `kube-apiserver`, `kube-controller-manager`, `kube-scheduler`, `etcd`, `kube-vip`, dan CNI) berjalan dengan baik.

4.  **Periksa Kesehatan etcd:**
    Dari salah satu node control plane:
    ```bash
    sudo ETCDCTL_API=3 etcdctl \
      --endpoints=https://127.0.0.1:2379 \
      --cacert=/etc/kubernetes/pki/etcd/ca.crt \
      --cert=/etc/kubernetes/pki/etcd/server.crt \
      --key=/etc/kubernetes/pki/etcd/server.key \
      endpoint status -w table
    ```
    Pastikan semua member etcd sehat dan sinkron.

## 🧰 Panduan Operasional Harian

Bagian ini menyediakan panduan umum untuk mengelola klaster Kubernetes Anda.

### Memantau Kesehatan Klaster
-   Gunakan `kubectl get nodes`, `kubectl get pods --all-namespaces`, dan `kubectl describe` untuk pemeriksaan dasar.
-   Integrasikan dengan solusi monitoring seperti Prometheus & Grafana untuk metrik yang lebih mendalam.
-   Periksa log komponen klaster (`sudo journalctl -u kubelet`, `sudo crictl logs <container_id>`).

### Menambah Node Control Plane
Prosedur ini mengikuti pola pemindahan artefak yang sama dengan `kube-vip.yaml` untuk memastikan konsistensi dan integritas.

1.  **Siapkan node control-plane baru:**
    -   Pastikan OS prereq terpenuhi (containerd aktif, swap off, time sync memadai).
    -   Pastikan konektivitas ke VIP/API server tersedia.

2.  **Stage `kube-vip.yaml` (belum aktif):**
    -   File `kube-vip.yaml` akan ditempatkan di `/etc/kubernetes/kube-vip/kube-vip.yaml` pada node baru.
    -   **Penting**: Jangan meletakkan `kube-vip.yaml` di `/etc/kubernetes/manifests/` sebelum `kubeadm join` selesai untuk mencegah *takeover* VIP yang prematur.

3.  **Pre-copy sertifikat wajib ke node baru:**
    -   Sertifikat yang wajib ada di `/etc/kubernetes/pki/` adalah: `ca.crt`, `ca.key`, `etcd/ca.crt`, `etcd/ca.key`, `front-proxy-ca.crt`, `front-proxy-ca.key`, `sa.pub`, dan `sa.key`.
    -   Mekanisme: Sertifikat dikemas sebagai `tar.gz` di node0, dihitung checksum SHA-256, ditransfer, diverifikasi checksum-nya di node tujuan, lalu diekstrak ke `/etc/kubernetes/pki/`.
    -   Izin akses diatur ketat: `600` untuk file kunci privat (`*.key`) dan `644` untuk sertifikat publik (`*.crt`) serta `sa.pub`.

4.  **Validasi readiness sebelum join:**
    -   Jalankan validasi artefak di node baru:
        ```bash
        sudo bash scripts/validate-join-artifacts.sh
        ```
    -   Pastikan seluruh sertifikat wajib terdeteksi, format valid, dan permission sesuai.

5.  **Jalankan `kubeadm join --control-plane`:**
    -   Gunakan *join command* yang dihasilkan oleh node0 (termasuk `--certificate-key`).
    -   Setelah join sukses, baru aktifkan kube-vip dengan memindahkan manifest ke `/etc/kubernetes/manifests/`.

6.  **Catatan penting: Endpoint domain vs VIP (anti self-target):**
    -   Jika `K8sDOMAIN` mengarah ke domain (mis. `dev.homelab.com`), pastikan DNS A record domain tersebut menunjuk ke VIP (mis. `192.168.1.100`), bukan ke salah satu IP node control-plane.
    -   Mekanisme join di otomasi ini akan:
        -   Menghapus mapping lama domain di `/etc/hosts` (jika ada),
        -   Menulis ulang mapping domain → VIP,
        -   Memaksa `kubeadm join` menggunakan VIP untuk menghindari *drift* DNS/hosts yang dapat menyebabkan node menarget dirinya sendiri (`connect: connection refused`).

7.  **Uji end-to-end di staging:**
    -   Lakukan join control-plane di lingkungan staging terlebih dahulu untuk memastikan prosedur pre-copy ini tidak memicu *failure* di fase join dan semua komponen (etcd, apiserver, kube-vip) stabil.

### Menambah/Menghapus Node Pekerja (Worker Node)
-   **Menambah**: Siapkan VM baru, pastikan prasyarat terpenuhi, lalu gunakan `kubeadm token create` di control plane dan `kubeadm join` di worker node.
-   **Menghapus**: `kubectl drain <node-name>`, `kubectl delete node <node-name>`, lalu `kubeadm reset` di worker node.

### Upgrade Versi Kubernetes
-   Ikuti panduan resmi `kubeadm upgrade` dari dokumentasi Kubernetes. Selalu lakukan di lingkungan staging terlebih dahulu.
-   Upgrade biasanya melibatkan: `kubeadm upgrade plan`, `kubeadm upgrade apply`, `kubectl drain`, `apt/yum upgrade kubelet/kubectl/kubeadm`, `kubectl uncordon`.

### Skenario Pemulihan Bencana
-   **Backup etcd**: Lakukan backup etcd secara berkala dari salah satu node control plane.
-   **Pemulihan node control plane**: Jika satu node control plane gagal, klaster harus tetap beroperasi. Untuk memulihkan, hapus node yang gagal dari etcd, *provision* ulang VM, lalu lakukan prosedur "Menambah Node Control Plane".

## 🔒 Catatan Keamanan

Keamanan adalah prioritas utama dalam setiap deployment Kubernetes.

### Manajemen Secrets
-   **Jangan pernah *hardcode* secrets** (seperti SSH key, token API, kredensial database) langsung di kode repositori.
-   Gunakan mekanisme `pulumi config set --secret` atau [Pulumi ESC (Environments, Secrets, and Configuration)](https://www.pulumi.com/docs/concepts/secrets/) untuk mengelola secrets dengan aman. Secrets ini akan dienkripsi dan hanya dapat diakses oleh Pulumi CLI yang terautentikasi.

### Praktik Terbaik Keamanan Klaster
-   **RBAC (Role-Based Access Control)**: Konfigurasi RBAC yang ketat untuk membatasi akses pengguna dan aplikasi ke sumber daya klaster.
-   **Network Policies**: Terapkan Network Policies untuk mengontrol lalu lintas jaringan antar pod dan ke/dari klaster.
-   **Image Security**: Gunakan *container image* dari sumber terpercaya, lakukan *scanning* kerentanan, dan pastikan *image* selalu diperbarui.
-   **Host Security**: Jaga keamanan OS host VM (patching, firewall, hardening).
-   **Audit Logging**: Aktifkan dan pantau audit log Kubernetes untuk mendeteksi aktivitas mencurigakan.
-   **Sertifikat**: Pastikan sertifikat klaster diperbarui secara berkala dan disimpan dengan aman.

## Troubleshooting Umum

Bagian ini berisi solusi untuk masalah umum yang mungkin Anda temui.

### `pulumi up` Gagal karena Lock File Pulumi
**Masalah**: Anda mungkin melihat error seperti `Lock C:\Users\xyz\.pulumi\credentials.json: Access is denied.`
**Penyebab**: Ini biasanya terjadi di Windows ketika ada proses Pulumi lain yang masih berjalan atau file kredensial Pulumi terkunci oleh proses lain atau izin file yang salah.
**Solusi**:
1.  Pastikan tidak ada proses `pulumi` lain yang berjalan di Task Manager.
2.  Periksa izin file `C:\Users\xyz\.pulumi\credentials.json` dan pastikan user Anda memiliki izin `Full Control`.
3.  Coba hapus file lock secara manual jika ada (misalnya `credentials.json.lock`).
4.  Sebagai alternatif, Anda bisa mengatur variabel lingkungan `PULUMI_HOME` ke direktori lain yang writable.

### `kubeadm join` Gagal (Self-Target API Server)
**Masalah**: Node control plane baru gagal bergabung dengan klaster, dengan log kubelet menunjukkan `connection refused` saat mencoba mendaftar ke API server, dan IP yang dituju adalah IP node itu sendiri.
**Penyebab**: Ini terjadi ketika `K8sDOMAIN` (endpoint API server) di node join resolve ke IP lokal node tersebut, bukan ke VIP klaster. Ini bisa disebabkan oleh konfigurasi DNS yang salah atau entri `/etc/hosts` yang tidak tepat.
**Solusi**:
-   Otomasi ini telah diperkuat untuk mengatasi masalah ini dengan:
    -   Memastikan `/etc/hosts` di node join secara *authoritative* memetakan `K8sDOMAIN` ke `K8sVIP`.
    -   Memaksa `kubeadm join` untuk selalu menarget `K8sVIP` secara langsung, bukan `K8sDOMAIN` yang mungkin salah resolve.
-   Jika masalah masih terjadi, periksa konfigurasi DNS Anda dan pastikan `K8sDOMAIN` benar-benar menunjuk ke `K8sVIP`.

### Sertifikat PKI Hilang/Tidak Valid
**Masalah**: Selama proses join, ada peringatan atau kegagalan terkait sertifikat PKI yang hilang, kosong, atau tidak valid.
**Penyebab**: Proses *pre-copy* sertifikat dari master ke node join mungkin gagal, atau izin file tidak diatur dengan benar.
**Solusi**:
-   Otomasi ini sekarang memiliki validasi *fail-fast* untuk semua sertifikat wajib (`ca.crt`, `ca.key`, `etcd/ca.crt`, dll.) dan memastikan izin file (`600` untuk key, `644` untuk cert) diatur dengan benar.
-   Jika error ini muncul, periksa log Pulumi untuk detail lebih lanjut tentang kegagalan transfer atau validasi sertifikat.

## 🤝 Kontribusi

Kami menyambut kontribusi dari komunitas! Jika Anda ingin berkontribusi pada proyek ini, silakan ikuti langkah-langkah berikut:

1.  *Fork* repositori ini.
2.  Buat *branch* baru untuk fitur atau perbaikan Anda (`git checkout -b feature/nama-fitur`).
3.  Lakukan perubahan Anda dan pastikan semua tes lulus.
4.  *Commit* perubahan Anda dengan pesan yang jelas dan deskriptif (ikuti [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)).
5.  *Push* *branch* Anda ke *fork* Anda.
6.  Buka *Pull Request* (PR) ke repositori utama.

## 📞 Kontak

Untuk pertanyaan, saran, atau dukungan lebih lanjut, silakan hubungi:
-   [Nama Anda/Tim Anda]
-   [Email Anda/Tim Anda]
-   [Link ke Issue Tracker/Diskusi GitHub]