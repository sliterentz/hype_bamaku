
function Remove-FileWithRetry {
    param(
        [Parameter(Mandatory=$true)]
        [string]$Path,
        
        [int]$MaxRetries = 5,
        [int]$BaseDelaySeconds = 1
    )

    if (-not (Test-Path -LiteralPath $Path)) { return }

    $retryCount = 0
    
    while ($retryCount -lt $MaxRetries) {
        try {
            # Try to rename first as a lock check
            # This is often faster/more reliable than waiting for Remove-Item to timeout
            $tempName = $Path + ".deleting." + [Guid]::NewGuid().ToString()
            Rename-Item -LiteralPath $Path -NewName $tempName -ErrorAction Stop
            
            # If rename succeeds, the file is likely not locked by an external process
            # strictly speaking, a race condition is still possible, but this reduces the window
            Remove-Item -LiteralPath $tempName -Force -ErrorAction Stop
            return
        } catch {
            $retryCount++
            if ($retryCount -ge $MaxRetries) {
                Write-Error "Failed to remove file '$Path' after $MaxRetries attempts. The file might be locked by another process (e.g., Hyper-V VM)."
                throw $_
            }
            
            $delay = $BaseDelaySeconds * [Math]::Pow(2, $retryCount - 1)
            Write-Warning "File '$Path' is locked. Retrying removal in $delay seconds (Attempt $retryCount/$MaxRetries)..."
            Start-Sleep -Seconds $delay
        }
    }
}
