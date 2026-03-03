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
        $deadline = (Get-Date).AddMinutes(15)
        
        Write-Output "Waiting for SSH on $hostName..."
        
        while ((Get-Date) -lt $deadline) {
          # Check TCP first
          $tcp = Test-NetConnection -ComputerName $hostName -Port 22 -WarningAction SilentlyContinue
          if ($tcp.TcpTestSucceeded) {
            # Try Auth
            # Note: We use -o BatchMode=yes to fail fast if key auth fails, and StrictHostKeyChecking=no to ignore host key changes
            $proc = Start-Process -FilePath "ssh" -ArgumentList "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=NUL", "-i", "$key", "$user@$hostName", "echo ok" -NoNewWindow -PassThru -Wait
            if ($proc.ExitCode -eq 0) {
              Write-Output "ssh-ready:$hostName"
              exit 0
            } else {
              Write-Output "SSH TCP ok, but auth failed. Retrying..."
            }
          }
          Start-Sleep -Seconds 10
        }
        throw "Timeout waiting for SSH auth on $hostName"
      `,
      triggers: [host, username, privateKeyPath]
    },
    { dependsOn }
  );
}
