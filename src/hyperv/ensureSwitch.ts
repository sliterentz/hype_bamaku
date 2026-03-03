import * as pulumi from "@pulumi/pulumi";
import * as command from "@pulumi/command";

export interface EnsureSwitchArgs {
  name: string;
  externalAdapterName?: string;
}

export function ensureHypervSwitch(name: string, args: EnsureSwitchArgs) {
  return new command.local.Command(
    name,
    {
      interpreter: ["powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"],
      create: pulumi.interpolate`
        $ErrorActionPreference = 'Stop'
        $switchName = '${args.name}'
        $existing = Get-VMSwitch -Name $switchName -ErrorAction SilentlyContinue
        if ($null -ne $existing) {
          Write-Output "exists:$($existing.Name)"
          exit 0
        }
        if ('${args.externalAdapterName ?? ""}' -eq '') {
          throw 'hyperv:externalAdapterName wajib jika vSwitch belum ada.'
        }
        $adapterName = '${args.externalAdapterName ?? ""}'
        New-VMSwitch -Name $switchName -NetAdapterName $adapterName -AllowManagementOS $true | Out-Null
        Write-Output "created:$switchName"
      `
    },
    { protect: true }
  );
}
