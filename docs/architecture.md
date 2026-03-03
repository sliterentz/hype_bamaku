# Arsitektur

## Target

Arsitektur ini mereplikasi pola bare-metal Kubernetes di homelab (Hyper-V) seperti pada referensi:
- Hyper-V sebagai bare-metal layer (Windows host) + Ubuntu VM untuk node Kubernetes.
- Static IP per node.
- kubeadm-based cluster.
- MetalLB untuk `LoadBalancer` di bare-metal.
- Ingress NGINX untuk routing berbasis domain.
- Storage untuk workload stateful (Longhorn).

## Komponen

### Lapisan Virtualisasi (Hyper-V)

- VM Generation 2.
- External vSwitch agar VM berada pada subnet LAN yang sama.
- Disk menggunakan differencing VHDX untuk hemat ruang (parent `baseVhdxPath`).

### Lapisan Kubernetes

- kubeadm untuk bootstrap control-plane dan worker.
- container runtime: containerd.
- CNI: Calico (NetworkPolicy support).

### Networking (Bare-metal)

- MetalLB (L2 mode) mengalokasikan IP dari pool LAN untuk service type `LoadBalancer`.
- Ingress NGINX expose traffic HTTP/HTTPS.
- cert-manager untuk sertifikat TLS:
  - `selfsigned` untuk dev.
  - `letsencrypt` untuk prod (butuh DNS yang mengarah ke IP Ingress).

### Storage

- Longhorn sebagai distributed block storage di cluster.
- Default replica count disesuaikan environment (prod lebih tinggi).

### Observability

- Monitoring terpusat: kube-prometheus-stack (Prometheus + Grafana + Alertmanager).
- Logging terpusat:
  - Default: Loki + Promtail.
  - Opsional: Elastic ECK operator (sebagai fondasi ELK).

## High Availability (Production)

Konsep HA yang diterapkan:
- `prod` mensyaratkan minimal 3 control-plane untuk HA stacked etcd.
- Endpoint API disarankan menggunakan VIP/LB (`kubernetes:apiVip`) agar node/komponen tidak bergantung pada satu IP control-plane.

Catatan: implementasi VIP/LB bisa memakai:
- perangkat eksternal (router/LB), atau
- VM khusus HAProxy+Keepalived (disarankan untuk homelab skala kecil). Prosedur operasional dibahas pada [operations.md](file:///d:/Core/hype_bamaku/docs/operations.md).
