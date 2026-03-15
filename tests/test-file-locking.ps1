
$ErrorActionPreference = 'Stop'

$ScriptDir = Split-Path $MyInvocation.MyCommand.Path
$ProjectRoot = Split-Path $ScriptDir -Parent
$UtilsPath = Join-Path $ProjectRoot "scripts\cloudinit\FileLockingUtils.ps1"

Write-Host "Loading utils from: $UtilsPath"
if (-not (Test-Path $UtilsPath)) {
    Write-Error "Utils script not found at $UtilsPath"
    exit 1
}

. $UtilsPath

function Assert-True($condition, $message) {
    if (-not $condition) {
        Write-Error "FAIL: $message"
        exit 1
    } else {
        Write-Host "PASS: $message" -ForegroundColor Green
    }
}

# Test 1: Simple Removal
Write-Host "`nRunning Test 1: Simple Removal"
$TestFile1 = Join-Path $env:TEMP "test-simple-removal.txt"
"Test Content" | Set-Content -Path $TestFile1
Remove-FileWithRetry -Path $TestFile1
Assert-True (-not (Test-Path $TestFile1)) "File should be removed immediately"

# Test 2: Locked File Retry Success
Write-Host "`nRunning Test 2: Locked File Retry Success"
$TestFile2 = Join-Path $env:TEMP "test-locked-retry.txt"
"Test Content" | Set-Content -Path $TestFile2

# Start background job to lock file for 3 seconds
$job = Start-Job -ScriptBlock {
    param($path)
    $fs = [System.IO.File]::Open($path, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::None)
    Start-Sleep -Seconds 3
    $fs.Close()
    $fs.Dispose()
} -ArgumentList $TestFile2

Start-Sleep -Milliseconds 500 # Wait for lock

Write-Host "Attempting removal (should take ~3-4 seconds)..."
$sw = [System.Diagnostics.Stopwatch]::StartNew()
Remove-FileWithRetry -Path $TestFile2 -MaxRetries 5 -BaseDelaySeconds 1
$sw.Stop()

Assert-True (-not (Test-Path $TestFile2)) "File should be removed after retry"
Assert-True ($sw.Elapsed.TotalSeconds -ge 3) "Removal should have waited for lock release"

Receive-Job $job
Remove-Job $job

# Test 3: Locked File Failure
Write-Host "`nRunning Test 3: Locked File Failure"
$TestFile3 = Join-Path $env:TEMP "test-locked-fail.txt"
"Test Content" | Set-Content -Path $TestFile3

# Start background job to lock file for 10 seconds (longer than retries)
$job = Start-Job -ScriptBlock {
    param($path)
    $fs = [System.IO.File]::Open($path, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::None)
    Start-Sleep -Seconds 10
    $fs.Close()
    $fs.Dispose()
} -ArgumentList $TestFile3

Start-Sleep -Milliseconds 500 # Wait for lock

try {
    Write-Host "Attempting removal (should fail)..."
    Remove-FileWithRetry -Path $TestFile3 -MaxRetries 3 -BaseDelaySeconds 1
    Write-Error "FAIL: Should have thrown an error"
} catch {
    Write-Host "PASS: Correctly threw error: $_" -ForegroundColor Green
}

Stop-Job $job
Remove-Job $job
if (Test-Path $TestFile3) { Remove-Item $TestFile3 -Force }

Write-Host "`nAll tests completed."
