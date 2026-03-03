import * as pulumi from "@pulumi/pulumi";
import * as k8s from "@pulumi/kubernetes";
import { loadConfig } from "./config";
import { ensureHypervSwitch } from "./hyperv/ensureSwitch";
import { createCloudInitIso } from "./hyperv/cloudInitIso";
import { ensureVm } from "./hyperv/ensureVm";
import { VmSpec } from "./hyperv/types";
import { bootstrapKubeadmCluster, buildNodeHandles } from "./k8s/bootstrap";
import { installAddons } from "./k8s/addons";
import { resolveNodeHostname } from "./utils/hostnames";

const cfg = loadConfig();

const sw = ensureHypervSwitch("hyperv-switch", {
  name: cfg.hyperv.switchName,
  externalAdapterName: cfg.hyperv.externalAdapterName
});

const stack = pulumi.getStack();
const artifactsRoot = `${cfg.hyperv.isoOutputDir}/${stack}`;

const cpIps = cfg.nodes.controlPlaneIps.slice(0, cfg.nodes.controlPlaneCount);
const workerIps = cfg.nodes.workerIps.slice(0, cfg.nodes.workerCount);

const vmResources: pulumi.Resource[] = [];

function makeVm(name: string, hostname: string, ip: string) {
  const isoPath = `${artifactsRoot}/isos/${name}.iso`;
  const diskPath = `${cfg.hyperv.vmPath}\\Disks\\${stack}\\${name}.vhdx`;
  const ipCidr = withPrefix(ip, cfg.network.cidr);

  const iso = createCloudInitIso(`${name}-cloudinit`, {
    isoPath,
    instanceId: name,
    hostname: hostname,
    username: cfg.ssh.username,
    sshPublicKey: cfg.ssh.publicKey,
    networkInterfaceName: cfg.network.interfaceName,
    ipCidr,
    gateway: cfg.network.gateway,
    dnsServers: cfg.network.dnsServers
  });

  // Since cloud-init only runs once, we must replace the VM if the ISO content changes.
  // We can use the 'triggers' output from the iso resource as a proxy for the content/instanceId.
  const instanceIdTrigger = iso.triggers.apply(t => JSON.stringify(t));

  const spec: VmSpec = {
    name,
    switchName: cfg.hyperv.switchName,
    vmPath: cfg.hyperv.vmPath,
    baseVhdxPath: cfg.hyperv.baseVhdxPath,
    differencingDiskPath: diskPath,
    cloudInitIsoPath: isoPath,
    vcpu: cfg.nodes.vcpu,
    memoryMb: cfg.nodes.memoryMb,
    diskGb: cfg.nodes.diskGb,
    secureBoot: cfg.hyperv.secureBoot,
    // Pass the trigger string as the instanceId so ensureVm replaces the VM when ISO changes
    instanceId: instanceIdTrigger as any
  };

  const vm = ensureVm(`${name}-vm`, spec, [sw, iso]);
  vmResources.push(vm);
}

cpIps.forEach((ip, i) => {
  const name = `${stack}-cp-${i + 1}`;
  const hostname = resolveNodeHostname('cp', i, stack, cfg.nodes.controlPlaneHostnames);
  makeVm(name, hostname, ip);
});
workerIps.forEach((ip, i) => {
  const name = `${stack}-w-${i + 1}`;
  const hostname = resolveNodeHostname('worker', i, stack, cfg.nodes.workerHostnames);
  makeVm(name, hostname, ip);
});

const controlPlanes = buildNodeHandles(
  `${stack}-cp`,
  cpIps,
  vmResources,
  cfg.ssh.username,
  cfg.ssh.privateKeyPath
);
const workers = buildNodeHandles(
  `${stack}-w`,
  workerIps,
  vmResources,
  cfg.ssh.username,
  cfg.ssh.privateKeyPath
);

const bootstrap = bootstrapKubeadmCluster("kubeadm", cfg, { controlPlanes, workers });

const k8sProvider = new k8s.Provider("k8s", {
  kubeconfig: bootstrap.kubeconfig
});

installAddons("addons", cfg, k8sProvider);

export const controlPlaneIps = cpIps;
export const workerNodeIps = workerIps;
export const kubeconfig = bootstrap.kubeconfig;
export const apiEndpoint = cfg.kubernetes.apiVip ?? cpIps[0];

function withPrefix(ip: string, cidr: string) {
  const parts = cidr.split("/");
  if (parts.length !== 2) throw new Error(`network:cidr tidak valid: ${cidr}`);
  const prefix = parts[1];
  return `${ip}/${prefix}`;
}
