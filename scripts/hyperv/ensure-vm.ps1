param(
  [Parameter(Mandatory=$true)][string]$Name,
  [Parameter(Mandatory=$true)][string]$SwitchName,
  [Parameter(Mandatory=$true)][string]$VmPath,
  [Parameter(Mandatory=$true)][string]$BaseVhdxPath,
  [Parameter(Mandatory=$true)][string]$DifferencingDiskPath,
  [Parameter(Mandatory=$true)][string]$CloudInitIsoPath,
  [Parameter(Mandatory=$true)][int]$CpuCount,
  [Parameter(Mandatory=$true)][int]$MemoryMb,
  [Parameter(Mandatory=$true)][int]$DiskGb,
  [Parameter(Mandatory=$true)][bool]$SecureBoot
)

$ErrorActionPreference = 'Stop'

function Ensure-Dir([string]$Path) {
  if (-not (Test-Path -LiteralPath $Path)) {
    New-Item -ItemType Directory -Path $Path | Out-Null
  }
}

$vm = Get-VM -Name $Name -ErrorAction SilentlyContinue

Ensure-Dir (Split-Path -Parent $DifferencingDiskPath)

if (-not (Test-Path -LiteralPath $BaseVhdxPath)) {
  throw "BaseVhdxPath tidak ditemukan: $BaseVhdxPath"
}

if (-not (Test-Path -LiteralPath $CloudInitIsoPath)) {
  throw "CloudInitIsoPath tidak ditemukan: $CloudInitIsoPath"
}

if (-not (Test-Path -LiteralPath $DifferencingDiskPath)) {
  # Retry loop to handle concurrent access to Base VHDX (ResourceBusy/0x80070020)
  $maxRetries = 10
  for ($i = 0; $i -lt $maxRetries; $i++) {
    try {
      New-VHD -Path $DifferencingDiskPath -ParentPath $BaseVhdxPath -Differencing -ErrorAction Stop | Out-Null
      break # Success
    } catch {
      # Check if error is ResourceBusy (0x80070020) or generic IO lock
      if ($_.Exception.InnerException.HResult -eq -2147024864 -or $_.ToString() -match "used by another process") {
        if ($i -eq $maxRetries - 1) {
          throw "Gagal membuat differencing disk setelah $maxRetries percobaan karena file terkunci: $_"
        }
        $delay = 3 + (Get-Random -Minimum 1 -Maximum 4)
        Write-Warning "New-VHD locked (Attempt $($i+1)/$maxRetries). Retrying in $delay seconds..."
        Start-Sleep -Seconds $delay
      } else {
        throw $_ # Re-throw other errors
      }
    }
  }
}

$sizeBytes = [int64]$DiskGb * 1024 * 1024 * 1024
try {
  Resize-VHD -Path $DifferencingDiskPath -SizeBytes $sizeBytes | Out-Null
} catch {
  # ignore if cannot resize (depends on base image)
}

if ($null -eq $vm) {
  Ensure-Dir $VmPath
  New-VM -Name $Name -Generation 2 -Path $VmPath -MemoryStartupBytes ($MemoryMb * 1MB) -SwitchName $SwitchName -VHDPath $DifferencingDiskPath | Out-Null
} else {
  $vm | Stop-VM -TurnOff -Force -ErrorAction SilentlyContinue | Out-Null
}

Set-VMProcessor -VMName $Name -Count $CpuCount | Out-Null
Set-VMMemory -VMName $Name -DynamicMemoryEnabled $false -StartupBytes ($MemoryMb * 1MB) | Out-Null

if ($SecureBoot) {
  Set-VMFirmware -VMName $Name -EnableSecureBoot On -SecureBootTemplate "MicrosoftUEFICertificateAuthority" | Out-Null
} else {
  Set-VMFirmware -VMName $Name -EnableSecureBoot Off | Out-Null
}

$dvd = Get-VMDvdDrive -VMName $Name -ErrorAction SilentlyContinue
if ($null -eq $dvd) {
  Add-VMDvdDrive -VMName $Name -Path $CloudInitIsoPath | Out-Null
} else {
  Set-VMDvdDrive -VMName $Name -Path $CloudInitIsoPath | Out-Null
}

Start-VM -Name $Name | Out-Null

Write-Output "ok:$Name"
