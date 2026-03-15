# Test Script for waitForSsh Logic
# Usage: .\scripts\tests\test-wait-logic.ps1

$ErrorActionPreference = 'Stop'

# Create a dummy key file
$keyFile = ".\dummy_key"
New-Item -ItemType File -Path $keyFile -Force | Out-Null

# Mock Test-NetConnection
function Test-NetConnection {
    param([string]$ComputerName, [int]$Port, [string]$WarningAction)
    Write-Host "Mocking TCP check for $ComputerName : $Port -> OK"
    return [PSCustomObject]@{ TcpTestSucceeded = $true }
}

# Mock ssh command (initially fails, then succeeds after retries)
$global:sshAttempts = 0
function ssh {
    $global:sshAttempts++
    Write-Host "Mocking SSH attempt #$global:sshAttempts"
    if ($global:sshAttempts -lt 3) {
        Write-Host "Simulating Auth Failure"
        $global:LASTEXITCODE = 1
    } else {
        Write-Host "Simulating Auth Success"
        $global:LASTEXITCODE = 0
    }
}

# The logic from waitForSsh.ts (adapted slightly for test environment)
$hostName = "mock-host"
$user = "mock-user"
$key = $keyFile
$timeoutSeconds = 10 # Short timeout for test
$deadline = (Get-Date).AddSeconds($timeoutSeconds)
$retryCount = 0
$backoff = 1 # Fast backoff for test
$maxBackoff = 2

Write-Host "Starting Test..."

# 1. Validate Private Key Existence
if (-not (Test-Path -LiteralPath $key)) {
  throw "Private key file not found at: $key"
}

# 2. Validate Private Key Permissions (skipped for mock test as we don't control CI/CD permissions fully here)

Write-Host "Waiting for SSH on $hostName (timeout: ${timeoutSeconds}s)..."

while ((Get-Date) -lt $deadline) {
  $retryCount++
  
  # Check TCP first (mocked)
  $tcp = Test-NetConnection -ComputerName $hostName -Port 22 -WarningAction SilentlyContinue
  
  if ($tcp.TcpTestSucceeded) {
    # Try Auth (mocked via function 'ssh')
    # Note: Using Start-Process to invoke our mocked function is tricky because Start-Process spawns a new process.
    # So we simulate the logic directly:
    
    ssh # Call our mock function directly
    if ($LASTEXITCODE -eq 0) {
      Write-Host "ssh-ready:$hostName"
      Remove-Item $keyFile -Force
      Write-Host "TEST PASSED: SSH Auth succeeded after retries."
      exit 0
    } else {
      Write-Host "SSH TCP ok, but auth failed (Exit Code: $LASTEXITCODE). Retrying in $backoff seconds..."
    }
  } else {
     Write-Host "SSH TCP connection failed. Retrying in $backoff seconds..."
  }

  if ((Get-Date) -ge $deadline) {
    break
  }

  Start-Sleep -Seconds $backoff
  
  # Exponential Backoff
  $backoff = [Math]::Min($backoff * 2, $maxBackoff)
}

Remove-Item $keyFile -Force
throw "TEST FAILED: Timeout waiting for SSH auth on $hostName"
