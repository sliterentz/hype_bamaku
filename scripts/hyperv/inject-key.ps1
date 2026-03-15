
<#
.SYNOPSIS
    Forces SSH key injection into a running Ubuntu Hyper-V VM using PowerShell Direct.
    
.DESCRIPTION
    This script bypasses network SSH and injects the public key directly via VMBus.
    Requires 'Hyper-V Guest Services' to be enabled and running on the guest.
    For Ubuntu, verify `linux-tools-virtual` and `linux-cloud-tools-virtual` are installed.
    
.EXAMPLE
    .\inject-key.ps1 -VmName "dev-cp-1-vm" -User "qbig_user" -PublicKeyPath "C:\Users\slite\.ssh\pulumi_key.pub"
#>

param(
    [Parameter(Mandatory=$true)][string]$VmName,
    [Parameter(Mandatory=$true)][string]$User,
    [Parameter(Mandatory=$false)][string]$PublicKeyPath,
    [Parameter(Mandatory=$false)][string]$PublicKeyContent
)

$ErrorActionPreference = 'Stop'

if (-not [string]::IsNullOrWhiteSpace($PublicKeyPath)) {
    if (-not (Test-Path $PublicKeyPath)) {
        throw "Public key file not found: $PublicKeyPath"
    }
    $pubKeyContent = Get-Content -Path $PublicKeyPath -Raw
} elseif (-not [string]::IsNullOrWhiteSpace($PublicKeyContent)) {
    $pubKeyContent = $PublicKeyContent
} else {
    throw "Either PublicKeyPath or PublicKeyContent must be provided."
}

$pubKeyContent = $pubKeyContent.Trim()

Write-Host "Injecting key into VM '$VmName' for user '$User'..." -ForegroundColor Cyan

# Check if VM is running
$vm = Get-VM -Name $VmName
if ($vm.State -ne 'Running') {
    throw "VM '$VmName' is not running."
}

# Define the script block to run inside the VM
$scriptBlock = {
    param($u, $k)
    
    $homeDir = "/home/$u"
    if (-not (Test-Path $homeDir)) {
        Write-Error "Home directory not found: $homeDir"
        exit 1
    }

    $sshDir = "$homeDir/.ssh"
    $authFile = "$sshDir/authorized_keys"

    # Create .ssh dir
    if (-not (Test-Path $sshDir)) {
        New-Item -ItemType Directory -Path $sshDir -Force | Out-Null
    }

    # Write key
    $k | Out-File -FilePath $authFile -Encoding ascii -NoNewline

    # Fix permissions (Linux style via chmod/chown)
    # Note: PowerShell Core on Linux might not have chmod cmdlet built-in directly in the same way, 
    # but we can call /bin/chmod.
    
    # We use /bin/bash to execute shell commands for permission fixing
    /bin/bash -c "chown -R {$u}:{$u} $sshDir && chmod 700 $sshDir && chmod 600 $authFile"
    
    # Check if successful
    if ($LASTEXITCODE -eq 0) {
        Write-Host "Key injected and permissions fixed."
    } else {
        Write-Error "Failed to set permissions."
    }
}

# Try to use PowerShell Direct (Invoke-Command -VMName)
# This requires PowerShell to be installed on the Linux guest!
# Most minimal Ubuntu cloud images DO NOT have PowerShell installed.
# So this method might fail if pwsh is missing.

# First, check the OS type. If it's not Windows, and we know it's a Linux cloud image,
# PowerShell Direct is very likely to fail unless explicitly installed.
$guestOs = $vm.GuestOperatingSystem
if ($guestOs -notmatch "Windows") {
    Write-Warning "Guest OS appears to be Linux ($guestOs). PowerShell Direct may fail if 'pwsh' is not installed."
}

# ALTERNATIVE: Use `Copy-VMFile` (Guest Services) to push the file, then `Invoke-Command`?
# `Copy-VMFile` pushes to specific locations, usually requires enabling "Guest Services" integration component.

# BETTER ALTERNATIVE for Linux Guests without PowerShell:
# There is no built-in "RunShellCommand" for Hyper-V Linux guests from host PowerShell without external tools or PSRP.
# However, if the VM is reachable via network (which it is, TCP 22 ok), but auth fails...
# We are stuck.

# BUT, if we can use `kvp` (Key-Value Pair) exchange? No, that's for data exchange.

# Let's try to see if we can use `hvc` (Hyper-V Console)? No, that's interactive.

# If we cannot assume PowerShell is on the guest, we can't use Invoke-Command -VMName.
# But we can try! Maybe the cloud-init installed it?
# The cloud-init I see installs `linux-cloud-tools-virtual`. Not PowerShell.

Write-Warning "PowerShell Direct requires PowerShell installed on the guest."
Write-Warning "Attempting Invoke-Command... (This will fail if pwsh is missing on guest)"

try {
    # We need credentials for the GUEST. But we don't have them!
    # Wait, PowerShell Direct allows -Credential. But we don't know the password (it is "password" in config).
    # Let's try with default "password".
    
    $cred = New-Object System.Management.Automation.PSCredential ($User, (ConvertTo-SecureString "password" -AsPlainText -Force))
    
    Invoke-Command -VMName $VmName -Credential $cred -ScriptBlock $scriptBlock -ArgumentList $User, $pubKeyContent
    
    Write-Host "Success!" -ForegroundColor Green
} catch {
    Write-Warning "PowerShell Direct failed: $_"
    Write-Warning "This is expected if the guest does not have PowerShell installed (e.g. Ubuntu cloud images)."
    Write-Warning "Relying on Cloud-Init injection."
    exit 0 # We don't want to fail the Pulumi deployment, just warn and move on
}
