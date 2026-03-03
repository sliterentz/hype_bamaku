# Konfigurasi

Semua konfigurasi diambil dari Pulumi config. Struktur kunci mengikuti pola `section:key`.

## Core

- `env`: `dev|staging|prod`.

## Hyper-V

- `hyperv:switchName`: nama vSwitch (External).
- `hyperv:externalAdapterName`: nama adapter host (contoh `Wi-Fi` / `Ethernet`) untuk membuat vSwitch jika belum ada.
- `hyperv:vmPath`: folder VM Hyper-V.
- `hyperv:baseVhdxPath`: path VHDX base Ubuntu.
- `hyperv:isoOutputDir`: folder output ISO cloud-init.
- `hyperv:secureBoot`: default `true` (template `MicrosoftUEFICertificateAuthority`).

## Network

- `network:cidr`: CIDR LAN, contoh `192.168.0.0/24`.
- `network:gateway`: gateway LAN.
- `network:dnsServers`: array DNS.
- `network:interfaceName`: default `eth0`.
- `network:metallbAddressPool`: range IP untuk MetalLB, contoh `192.168.0.200-192.168.0.210`.

## Node

- `nodes:controlPlaneCount`: default `1` (dev/staging), minimal `3` untuk `prod`.
- `nodes:workerCount`: default `2`.
- `nodes:vcpu`: default `4`.
- `nodes:memoryMb`: default `8192`.
- `nodes:diskGb`: default `20`.
- `nodes:controlPlaneIps`: list IP static.
- `nodes:workerIps`: list IP static.

## Kubernetes

- `kubernetes:version`: default `1.30.0`.
- `kubernetes:podCidr`: default `10.244.0.0/16`.
- `kubernetes:serviceCidr`: default `10.96.0.0/12`.
- `kubernetes:apiVip`: (opsional) VIP/LB untuk endpoint API; wajib di `prod`.

## Ingress/TLS

- `ingress:baseDomain`: domain utama (opsional). Jika diisi, Grafana akan dibuatkan Ingress `grafana.<baseDomain>`.
- `ingress:tlsMode`: `selfsigned|letsencrypt`.
- `ingress:letsEncryptEmail`: wajib jika `letsencrypt`.

## Observability

- `observability:prometheusGrafana`: default `true`.
- `observability:logging`: `loki|elk`.

## Backup

- `backup:enableVelero`: default `false`.
- `backup:veleroProvider`: `minio|aws`.
- `backup:veleroBucket`: default `velero`.
- `backup:veleroS3Url`: wajib jika provider `minio`.

## Contoh file config

Gunakan contoh di README. Untuk memudahkan multi-stack, buat stack berbeda:
- `pulumi stack init dev`
- `pulumi stack init staging`
- `pulumi stack init prod`
