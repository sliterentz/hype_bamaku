param(
  [Parameter(Mandatory=$true)][string]$Name,
  [Parameter(Mandatory=$true)][string]$DifferencingDiskPath
)

$ErrorActionPreference = 'Stop'

Write-Host "Stopping VM $Name..."
Get-VM -Name $Name -ErrorAction SilentlyContinue | Stop-VM -TurnOff -Force -ErrorAction SilentlyContinue

Write-Host "Removing VM $Name..."
Get-VM -Name $Name -ErrorAction SilentlyContinue | Remove-VM -Force -ErrorAction SilentlyContinue

if (Test-Path -LiteralPath $DifferencingDiskPath) {
    Write-Host "Removing disk $DifferencingDiskPath..."
    Remove-Item -LiteralPath $DifferencingDiskPath -Force -ErrorAction SilentlyContinue
}

Write-Output "deleted:$Name"
