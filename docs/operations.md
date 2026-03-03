# Operasional (Deployment & Maintenance)

## Pengaturan Environment (Pulumi)

Untuk menjalankan perintah Pulumi (`preview`, `up`, `destroy`) secara konsisten, gunakan script helper `scripts/setup/pulumi-env.ps1`. Script ini mengatur:
- `PULUMI_HOME`: Lokasi data lokal Pulumi (default: `.pulumi-home`).
- `PULUMI_BACKEND_URL`: Lokasi penyimpanan state (default: `.pulumi-state`).
- `PULUMI_CONFIG_PASSPHRASE`: Kunci enkripsi untuk data sensitif di stack.
- Loading otomatis file `.env.<stack>` (contoh: `.env.dev`).

### Contoh Penggunaan

```powershell
# Inisialisasi environment untuk stack 'dev'
.\scripts\setup\pulumi-env.ps1 -Backend local -Stack dev

# Jalankan perintah Pulumi
pulumi preview
```

### Penanganan Masalah Passphrase (`incorrect passphrase`)

Jika muncul error `incorrect passphrase`, hal ini disebabkan oleh ketidakcocokan antara passphrase di environment dengan yang tersimpan di file state (`.pulumi-state`).

**Langkah Perbaikan:**
1. **Pastikan Passphrase Benar**: Periksa file `.env.<stack>` dan pastikan nilai `PULUMI_CONFIG_PASSPHRASE` sesuai dengan yang digunakan saat inisialisasi awal.
2. **Reset Passphrase (Jika Lupa)**:
   Jika passphrase hilang, Anda harus melakukan inisialisasi ulang stack atau mengubah passphrase jika masih memiliki akses ke state lama:
   ```powershell
   # Mengubah passphrase (membutuhkan passphrase lama)
   pulumi stack change-secrets-provider passphrase
   ```
3. **Sinkronisasi Manual**:
   Jika menggunakan backend lokal, pastikan variabel environment diatur sebelum menjalankan perintah Pulumi. Gunakan `pulumi-env.ps1` untuk memastikan konsistensi.

## Menyiapkan base image Ubuntu (VHDX)

Pulumi mengasumsikan `hyperv:baseVhdxPath` menunjuk ke VHDX yang bisa boot (Ubuntu 24.04 LTS). Cara menyiapkannya bervariasi; pola yang umum:
- Ambil Ubuntu cloud image.
- Konversi ke VHDX (misalnya dengan `qemu-img`), lalu pastikan bisa boot di VM Gen2.

Catatan: cloud-init ISO akan dibuat per node dan di-attach sebagai DVD (volume `CIDATA`).

## High Availability (prod)

### Control-plane HA

- Set `nodes:controlPlaneCount = 3`.
- Set `kubernetes:apiVip` ke alamat VIP/LB yang stabil.

### API endpoint via VIP/LB

Opsional yang disarankan:
- Gunakan 2 VM LB (HAProxy+Keepalived) untuk VIP `kubernetes:apiVip`.
- Backend HAProxy menunjuk ke `controlPlaneIps[*]:6443`.

Jika belum memakai LB/VIP, gunakan IP control-plane pertama sebagai endpoint. Ini tidak HA untuk API server.

## Network Policies

Cluster menggunakan Calico sehingga NetworkPolicy aktif.

Baseline yang dibuat otomatis:
- Namespace `apps`: `default-deny` (Ingress+Egress) dan allow DNS.

Praktik produksi:
- Tempatkan workload aplikasi di `apps`.
- Buat policy allow hanya untuk traffic yang dibutuhkan (ingress dari ingress-nginx, egress ke backend/db, dsb).

## Storage & Persistent Volumes

Longhorn dipasang untuk persistent volume.

Rekomendasi:
- Untuk `prod`, pastikan minimal 3 node yang dapat menjalankan replica Longhorn (atau turunkan replica count sesuai jumlah node yang tersedia).
- Monitor disk usage node (Longhorn akan menolak scheduling jika disk pressure).

## TLS termination

- Ingress NGINX melakukan termination TLS.
- cert-manager membuat sertifikat.

Mode:
- `selfsigned`: cocok dev/staging.
- `letsencrypt`: butuh DNS `A record` ke IP Ingress LoadBalancer.

## Backup strategy

### Etcd snapshot (disarankan)

Untuk DR control-plane, snapshot etcd sangat penting.

Opsi:
- systemd timer di setiap control-plane untuk `etcdctl snapshot save`.
- Simpan snapshot ke lokasi eksternal (NAS/S3).

### Backup PV

Opsi yang umum:
- Longhorn Backup Target (S3/NFS) untuk volume.
- Velero (opsional, tergantung tipe storage dan plugin).

Jika `backup:enableVelero=true`, chart Velero akan terpasang. Anda tetap perlu menyediakan kredensial S3/MinIO dan memastikan endpoint dapat diakses.

## Disaster recovery procedures (ringkas)

### Kehilangan 1 worker

- Drain node, hapus dari cluster, reprovision VM, join ulang worker.
- Longhorn akan rebuild replica jika masih ada quorum.

### Kehilangan 1 control-plane (prod)

- Dengan 3 control-plane, etcd masih quorum (2/3). Reprovision node dan join ulang control-plane.

### Kehilangan seluruh control-plane

- Restore etcd dari snapshot ke cluster baru (kubeadm restore workflow).
- Re-apply add-ons (Pulumi dapat menjalankan ulang) dan restore volume dari Longhorn backup target.
