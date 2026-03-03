import * as pulumi from "@pulumi/pulumi";
import * as command from "@pulumi/command";
import * as path from "path";
import { VmSpec } from "./types";
import { findRepoFile } from "../utils/paths";

export function ensureVm(name: string, spec: VmSpec, dependsOn?: pulumi.Input<pulumi.Resource>[]) {
  const secureBoot = spec.secureBoot ? "$true" : "$false";
  const scriptPath = findRepoFile("scripts", "hyperv", "ensure-vm.ps1");
  const scriptDir = path.dirname(scriptPath);
  
  // Convert relative cloudInitIsoPath to absolute if needed
  let isoPath = spec.cloudInitIsoPath;
  if (!path.isAbsolute(isoPath)) {
    isoPath = path.resolve(process.cwd(), isoPath);
  }

  const deleteScriptPath = findRepoFile("scripts", "hyperv", "delete-vm.ps1");

  const cmd = new command.local.Command(
    name,
    {
      interpreter: ["powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"],
      create: pulumi.interpolate`
        $ErrorActionPreference = 'Stop'
        $script = '${scriptPath}'
        if (-not (Test-Path -LiteralPath $script)) { throw "Script tidak ditemukan: $script" }
        & $script -Name '${spec.name}' -SwitchName '${spec.switchName}' -VmPath '${spec.vmPath}' -BaseVhdxPath '${spec.baseVhdxPath}' -DifferencingDiskPath '${spec.differencingDiskPath}' -CloudInitIsoPath '${isoPath}' -CpuCount ${spec.vcpu} -MemoryMb ${spec.memoryMb} -DiskGb ${spec.diskGb} -SecureBoot ${secureBoot}
      `,
      delete: pulumi.interpolate`
        $ErrorActionPreference = 'Stop'
        $script = '${deleteScriptPath}'
        if (-not (Test-Path -LiteralPath $script)) { throw "Script tidak ditemukan: $script" }
        & $script -Name '${spec.name}' -DifferencingDiskPath '${spec.differencingDiskPath}'
      `,
      dir: scriptDir,
      triggers: [
        spec.name,
        spec.switchName,
        spec.vmPath,
        spec.baseVhdxPath,
        spec.differencingDiskPath,
        spec.cloudInitIsoPath,
        `${spec.vcpu}`,
        `${spec.memoryMb}`,
        `${spec.diskGb}`,
        `${spec.secureBoot}`,
        spec.instanceId ?? ""
      ]
    },
    { dependsOn }
  );

  return cmd;
}
