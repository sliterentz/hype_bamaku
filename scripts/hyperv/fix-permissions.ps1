
<#
.SYNOPSIS
    Diagnoses and fixes common Hyper-V permission issues causing "You do not have the required permission" errors.
    
.DESCRIPTION
    This script performs the following checks and fixes:
    1. verifies if the current user is a member of the 'Hyper-V Administrators' local group.
    2. Adds the user to the group if missing (requires logoff/restart).
    3. Verifies and repairs Access Control Lists (ACLs) on critical Hyper-V directories.
    4. Checks if the current PowerShell session is elevated (Administrator).
    
.EXAMPLE
    .\fix-permissions.ps1
#>

$ErrorActionPreference = 'Stop'
$CurrentUser = [System.Security.Principal.WindowsIdentity]::GetCurrent()
$Principal = New-Object System.Security.Principal.WindowsPrincipal($CurrentUser)

# 1. Check Elevation
if (-not $Principal.IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Warning "This script must be run as Administrator to apply fixes."
    Write-Warning "Please restart PowerShell as Administrator."
    exit 1
}

Write-Host "Running as Administrator: YES" -ForegroundColor Green

# 2. Check Hyper-V Administrators Group Membership
$GroupName = "Hyper-V Administrators"
$Group = Get-LocalGroup -Name $GroupName -ErrorAction SilentlyContinue

if ($null -eq $Group) {
    Write-Error "Group '$GroupName' not found. Is Hyper-V feature installed?"
    exit 1
}

$IsMember = $false
$Members = Get-LocalGroupMember -Group $GroupName
foreach ($member in $Members) {
    if ($member.Name -eq "$env:USERDOMAIN\$env:USERNAME" -or $member.Name -eq "$env:COMPUTERNAME\$env:USERNAME") {
        $IsMember = $true
        break
    }
}

if ($IsMember) {
    Write-Host "User '$env:USERNAME' is a member of '$GroupName': YES" -ForegroundColor Green
} else {
    Write-Warning "User '$env:USERNAME' is NOT a member of '$GroupName'."
    try {
        Add-LocalGroupMember -Group $GroupName -Member $env:USERNAME
        Write-Host "Successfully added '$env:USERNAME' to '$GroupName'." -ForegroundColor Green
        Write-Warning "YOU MUST LOG OFF AND LOG BACK IN for this change to take effect."
    } catch {
        Write-Error "Failed to add user to group: $_"
    }
}

# 3. Fix File System Permissions (ACLs)
# Ensure VMMS (Virtual Machine Management Service) has access to these paths
$PathsToFix = @(
    "C:\HyperV\VMs",
    "C:\ProgramData\Microsoft\Windows\Virtual Hard Disks",
    "$PSScriptRoot\..\..\src\.pulumi-artifacts" 
)

$VmmsAccount = "NT VIRTUAL MACHINE\Virtual Machines"
$HyperVAdminGroup = "Hyper-V Administrators"

foreach ($Path in $PathsToFix) {
    if (-not (Test-Path -LiteralPath $Path)) {
        New-Item -ItemType Directory -Path $Path -Force | Out-Null
    }

    $ResolvedPath = (Resolve-Path -LiteralPath $Path).Path
    Write-Host "Checking permissions for: $ResolvedPath"

    try {
        $Acl = Get-Acl -Path $ResolvedPath
        
        # Rule for Hyper-V Administrators (Full Control)
        $ArHyperV = New-Object System.Security.AccessControl.FileSystemAccessRule(
            $HyperVAdminGroup,
            "FullControl",
            "ContainerInherit,ObjectInherit",
            "None",
            "Allow"
        )
        
        # Rule for VMMS (Modify/Read/Write)
        # "NT VIRTUAL MACHINE\Virtual Machines" is a special group used by Hyper-V
        $ArVmms = New-Object System.Security.AccessControl.FileSystemAccessRule(
            $VmmsAccount,
            "Modify", 
            "ContainerInherit,ObjectInherit",
            "None",
            "Allow"
        )

        $Acl.SetAccessRule($ArHyperV)
        $Acl.AddAccessRule($ArVmms)
        
        Set-Acl -Path $ResolvedPath -AclObject $Acl
        Write-Host "  -> Permissions fixed." -ForegroundColor Green
    } catch {
        Write-Warning "  -> Failed to set permissions: $_"
    }
}

# 4. Check AzMan Store (Authorization Manager)
# This is a legacy store but still used by Hyper-V. Sometimes it gets corrupted or misconfigured.
$AzManXml = "$env:ProgramData\Microsoft\Windows\Hyper-V\authorization.xml"
if (Test-Path $AzManXml) {
    Write-Host "AzMan authorization store found at: $AzManXml"
    Write-Host "If permission errors persist, consider resetting the Authorization Store or checking 'azman.msc'."
} else {
    Write-Warning "AzMan authorization store NOT found at default location."
}

Write-Host "`nDiagnostics Complete."
Write-Host "If you were added to 'Hyper-V Administrators', please LOG OFF and LOG IN again."
