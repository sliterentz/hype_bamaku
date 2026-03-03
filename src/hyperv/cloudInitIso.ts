import * as pulumi from "@pulumi/pulumi";
import * as command from "@pulumi/command";
import * as crypto from "crypto";
import * as path from "path";
import { findRepoFile } from "../utils/paths";

export interface CloudInitIsoArgs {
  isoPath: string;
  instanceId: string;
  hostname: string;
  username: string;
  sshPublicKey: string;
  networkInterfaceName: string;
  ipCidr: string;
  gateway: string;
  dnsServers: string[];
}

export function createCloudInitIso(name: string, args: CloudInitIsoArgs) {
  const scriptPath = findRepoFile("scripts", "cloudinit", "new-cloudinit-iso.ps1");
  const scriptDir = path.dirname(scriptPath);
  const userData = `#cloud-config\n` +
    `users:\n` +
    `  - default\n` +
    `  - name: ${args.username}\n` +
    `    sudo: ALL=(ALL) NOPASSWD:ALL\n` +
    `    groups: users, sudo\n` +
    `    shell: /bin/bash\n` +
    `    ssh_authorized_keys:\n` +
    `      - ${args.sshPublicKey.trim()}\n` +
    `ssh_pwauth: true\n` +
    `chpasswd:\n` +
    `  list: |\n` +
    `    ${args.username}:password\n` +
    `    ubuntu:password\n` +
    `  expire: false\n` +
    `package_update: true\n` +
    `packages:\n` +
    `  - openssh-server\n` +
    `  - linux-cloud-tools-virtual\n` +
    `  - linux-tools-virtual\n` +
    `runcmd:\n` +
    `  - [ systemctl, enable, --now, ssh ]\n`;

  const cloudInitHash = crypto
    .createHash("sha256")
    .update(userData)
    .update("\n")
    .update(args.hostname)
    .update("\n")
    .update(args.networkInterfaceName)
    .update("\n")
    .update(args.ipCidr)
    .update("\n")
    .update(args.gateway)
    .update("\n")
    .update(args.dnsServers.join(","))
    .update("\n")
    .update(args.sshPublicKey.trim())
    .digest("hex")
    .slice(0, 12);

  const metaData = `instance-id: ${args.instanceId}-${cloudInitHash}\nlocal-hostname: ${args.hostname}\n`;

  const dns = args.dnsServers.map((d) => `        - ${d}`).join("\n");
  const networkConfig = `version: 2\n` +
    `ethernets:\n` +
    `  id0:\n` +
    `    match:\n` +
    `      name: ${args.networkInterfaceName}\n` +
    `    dhcp4: false\n` +
    `    addresses:\n` +
    `      - ${args.ipCidr}\n` +
    `    routes:\n` +
    `      - to: default\n` +
    `        via: ${args.gateway}\n` +
    `    nameservers:\n` +
    `      addresses:\n` +
    `${dns}\n`;

  const userDataB64 = Buffer.from(userData, "utf8").toString("base64");
  const metaDataB64 = Buffer.from(metaData, "utf8").toString("base64");
  const networkConfigB64 = Buffer.from(networkConfig, "utf8").toString("base64");
  
  // Convert relative isoPath to absolute if needed
  let isoPath = args.isoPath;
  if (!path.isAbsolute(isoPath)) {
    isoPath = path.resolve(process.cwd(), isoPath);
  }

  return new command.local.Command(name, {
    interpreter: ["powershell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command"],
    create: pulumi.interpolate`
      $ErrorActionPreference = 'Stop'
      $script = '${scriptPath}'
      if (-not (Test-Path -LiteralPath $script)) { throw "Script tidak ditemukan: $script" }
      & $script -IsoPath '${isoPath}' -UserDataBase64 '${userDataB64}' -MetaDataBase64 '${metaDataB64}' -NetworkConfigBase64 '${networkConfigB64}'
    `,
    dir: scriptDir,
    triggers: [
      args.isoPath,
      args.instanceId,
      args.hostname,
      args.username,
      args.sshPublicKey,
      args.networkInterfaceName,
      args.ipCidr,
      args.gateway,
      args.dnsServers.join(",")
    ]
  });
}
