import * as pulumi from "@pulumi/pulumi";
import * as command from "@pulumi/command";

export function waitForSsh(
  name: string,
  host: pulumi.Input<string>,
  username: pulumi.Input<string>,
  privateKeyPath: pulumi.Input<string>,
  dependsOn?: pulumi.Input<pulumi.Resource>[]
) {
  return new command.local.Command(
    name,
    {
      interpreter: ["powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"],
      create: pulumi.interpolate`
        $ErrorActionPreference = 'Stop'
        $hostName = '${host}'
        $user = '${username}'
        $key = '${privateKeyPath}'
        $timeoutSeconds = 600 # Optimized timeout value
        $deadline = (Get-Date).AddSeconds($timeoutSeconds)
        $retryCount = 0
        $backoff = 2 # Initial backoff in seconds
        $maxBackoff = 30 # Max backoff in seconds

        # 1. Validate Private Key Existence
        if (-not (Test-Path -LiteralPath $key)) {
          throw "Private key file not found at: $key"
        }

        # 2. Validate Private Key Permissions (Windows)
        # Check if the key is accessible only by the current user (Owner) and Administrators/System.
        # This is a basic check. SSH client itself will enforce stricter checks.
        $acl = Get-Acl -Path $key
        # We can't easily check 0400 exactly in PowerShell without complex logic, 
        # but we can rely on ssh -i to complain if permissions are too open.
        # However, we can warn if Everyone/Users has access.
        $accessRules = $acl.Access
        foreach ($rule in $accessRules) {
            if ($rule.IdentityReference -match "Everyone" -or $rule.IdentityReference -match "Users") {
                Write-Warning "Private key permissions might be too open (Users/Everyone has access). SSH might refuse to use it."
                # We don't throw here to avoid blocking if the user knows what they are doing, but it's a good diagnostic.
            }
        }

        Write-Output "Waiting for SSH on $hostName (timeout: $timeoutSeconds)..."
        
        while ((Get-Date) -lt $deadline) {
          $retryCount++
          
          # Check TCP first
          $tcp = Test-NetConnection -ComputerName $hostName -Port 22 -WarningAction SilentlyContinue
          
          if ($tcp.TcpTestSucceeded) {
            # Try Auth
            # Note: We use -o BatchMode=yes to fail fast if key auth fails (non-interactive)
            # -o StrictHostKeyChecking=no to ignore host key changes (automation)
            # -o UserKnownHostsFile=NUL to avoid polluting known_hosts or failing on mismatch
            # -o ConnectTimeout=5 to avoid hanging
            $proc = Start-Process -FilePath "ssh" -ArgumentList "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=NUL", "-o", "ConnectTimeout=5", "-o", "PreferredAuthentications=publickey", "-o", "PasswordAuthentication=no", "-i", "$key", "$user@$hostName", "echo ssh-connection-verified" -NoNewWindow -PassThru -Wait
            
            if ($proc.ExitCode -eq 0) {
              Write-Output "ssh-ready:$hostName"
              exit 0
            } else {
              Write-Output "SSH TCP ok, but auth failed (Exit Code: $($proc.ExitCode)). Retrying in $backoff seconds..."
            }
          } else {
             Write-Output "SSH TCP connection failed. Retrying in $backoff seconds..."
          }

          if ((Get-Date) -ge $deadline) {
            break
          }

          Start-Sleep -Seconds $backoff
          
          # Exponential Backoff
          $backoff = [Math]::Min($backoff * 2, $maxBackoff)
        }
        
        throw "Timeout waiting for SSH auth on $hostName after $timeoutSeconds seconds ($retryCount attempts). Last error: Auth failed or TCP unreachable."
      `,
      triggers: [host, username, privateKeyPath]
    },
    { dependsOn }
  );
}
