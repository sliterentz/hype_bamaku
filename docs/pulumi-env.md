# Pulumi CLI Environment Variables

Dokumen ini merangkum environment variables yang umum dipakai untuk menjalankan `pulumi preview` / `pulumi up` secara konsisten (lokal dan CI).

Referensi resmi:
- Pulumi CLI env vars: https://www.pulumi.com/docs/iac/cli/environment-variables/
- Semua flag CLI bisa di-set via env vars `PULUMI_OPTION_*`: https://www.pulumi.com/blog/controlling-the-cli-through-environment-variables/

## Rekomendasi (Lokal)

Pilih salah satu backend:

### A) Pulumi Cloud backend

- `PULUMI_ACCESS_TOKEN`: set hanya jika ingin non-interactive login.
- `PULUMI_HOME`: arahkan ke folder writable untuk menghindari isu permission/lock.

### B) Local backend (file)

- `PULUMI_BACKEND_URL`: set ke `file:///...` agar state tersimpan di folder proyek (atau lokasi yang Anda tentukan).
- `PULUMI_CONFIG_PASSPHRASE_FILE`: lebih aman daripada menyimpan passphrase langsung di env var.
- `PULUMI_HOME`: arahkan ke folder writable.

## Rekomendasi (CI/CD)

- `PULUMI_CI=true`.
- `PULUMI_ACCESS_TOKEN` (jika pakai Pulumi Cloud backend).
- `PULUMI_BACKEND_URL` + `PULUMI_CONFIG_PASSPHRASE_FILE` (jika pakai local/diy backend).
- `PULUMI_OPTION_NON_INTERACTIVE=true`.
- `PULUMI_OPTION_YES=true`.
- `PULUMI_OPTION_SKIP_PREVIEW=true` (opsional; beberapa tim tetap menjalankan preview untuk audit).
- `PULUMI_STACK` (opsional) agar pipeline tidak tergantung `pulumi stack select`.

## Helper script (Windows PowerShell)

Repo ini menyediakan helper untuk set env vars pada session PowerShell saat ini:

```powershell
. .\scripts\setup\pulumi-env.ps1 -Backend local -Stack dev -PassphraseFile "$env:USERPROFILE\.pulumi-passphrase"
```

Atau CI mode:

```powershell
. .\scripts\setup\pulumi-env.ps1 -Backend cloud -Stack dev -CI -AccessToken $env:PULUMI_ACCESS_TOKEN
```

Catatan:
- Script di-dot-source (`. <path>`) supaya env vars menempel di session Anda.
- Jangan commit file passphrase / token ke repo.
