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
  [Parameter(Mandatory=$true)][bool]$SecureBoot,
  [Parameter(Mandatory=$false)][bool]$AdoptExisting = $false
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
  
  # Check for Hyper-V Administrators group membership (Diagnostic only)
  try {
    $currentUser = [System.Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object System.Security.Principal.WindowsPrincipal($currentUser)
    $isAdmin = $principal.IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)
    
    if (-not $isAdmin) {
        Write-Warning "Running as non-Administrator. This may cause permission issues with New-VM."
    }
  } catch {
    # Ignore errors during check
  }

  try {
    New-VM -Name $Name -Generation 2 -Path $VmPath -MemoryStartupBytes ($MemoryMb * 1MB) -SwitchName $SwitchName -VHDPath $DifferencingDiskPath -ErrorAction Stop | Out-Null
  } catch {
    if ($_.Exception.Message -match "authorization policy") {
        Write-Error "PERMISSION DENIED: The user '$env:USERNAME' does not have permission to create VMs on '$env:COMPUTERNAME'."
        Write-Error "POSSIBLE FIXES:"
        Write-Error "1. Run PowerShell/Pulumi as Administrator."
        Write-Error "2. Add user '$env:USERNAME' to the local 'Hyper-V Administrators' group."
        Write-Error "   (Run: Add-LocalGroupMember -Group 'Hyper-V Administrators' -Member '$env:USERNAME')"
        Write-Error "   NOTE: You must LOG OFF and LOG IN again for group membership to take effect."
        Write-Error "3. Check 'azman.msc' -> Hyper-V -> Role Assignments."
    }
    throw $_
  }
} else {
  if ($AdoptExisting) {
    Write-Output "VM '$Name' exists. Adopting..."
    
    if ($vm.State -eq 'Running') {
        # Check if ISO is different
        $dvd = Get-VMDvdDrive -VMName $Name -ErrorAction SilentlyContinue
        if ($dvd.Path -ne $CloudInitIsoPath) {
             Write-Warning "ISO mismatch on running VM '$Name'. Expected: '$CloudInitIsoPath', Found: '$($dvd.Path)'. Updating ISO..."
             Set-VMDvdDrive -VMName $Name -Path $CloudInitIsoPath | Out-Null
        } else {
             Write-Output "ISO matches on VM '$Name'."
        }
        
        # Return OK to signal success without restart
        Write-Output "ok:$Name"
        return
    } else {
        Write-Output "VM '$Name' exists but is not running. Proceeding with configuration update..."
    }
  }

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
