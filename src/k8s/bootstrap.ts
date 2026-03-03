import * as pulumi from "@pulumi/pulumi";
import * as command from "@pulumi/command";
import * as remote from "@pulumi/command/remote";
import { AppConfig } from "../config";
import { readFileUtf8 } from "../utils/fs";
import { findRepoFile } from "../utils/paths";
import { waitForSsh } from "./waitForSsh";

export interface NodeHandle {
  name: string;
  ip: string;
  wait: command.local.Command;
}

export interface BootstrapResult {
  kubeconfig: pulumi.Output<string>;
  kubeconfigB64: pulumi.Output<string>;
  joinWorkerCommand: pulumi.Output<string>;
  joinControlPlaneCommand: pulumi.Output<string>;
}

export function bootstrapKubeadmCluster(
  name: string,
  cfg: AppConfig,
  nodes: { controlPlanes: NodeHandle[]; workers: NodeHandle[] }
): BootstrapResult {
  const privateKey = pulumi.secret(readFileUtf8(cfg.ssh.privateKeyPath));
  const username = cfg.ssh.username;
  const k8sVersion = cfg.kubernetes.version;

  const bootstrapCommonAsset = new pulumi.asset.FileAsset(findRepoFile("scripts", "k8s", "bootstrap-common.sh"));
  const controlPlaneInitAsset = new pulumi.asset.FileAsset(findRepoFile("scripts", "k8s", "control-plane-init.sh"));
  const joinNodeAsset = new pulumi.asset.FileAsset(findRepoFile("scripts", "k8s", "join-node.sh"));

  const scriptDir = "/tmp/pulumi-k8s";
  const commonScript = new remote.CopyToRemote(
    `${name}-copy-common`,
    {
      connection: {
        host: nodes.controlPlanes[0].ip,
        user: username,
        privateKey
      },
      source: bootstrapCommonAsset,
      remotePath: `${scriptDir}/bootstrap-common.sh`
    },
    { dependsOn: [nodes.controlPlanes[0].wait] }
  );

  const initScript = new remote.CopyToRemote(
    `${name}-copy-init`,
    {
      connection: {
        host: nodes.controlPlanes[0].ip,
        user: username,
        privateKey
      },
      source: controlPlaneInitAsset,
      remotePath: `${scriptDir}/control-plane-init.sh`
    },
    { dependsOn: [commonScript] }
  );

  const joinScript = new remote.CopyToRemote(
    `${name}-copy-join`,
    {
      connection: {
        host: nodes.controlPlanes[0].ip,
        user: username,
        privateKey
      },
      source: joinNodeAsset,
      remotePath: `${scriptDir}/join-node.sh`
    },
    { dependsOn: [initScript] }
  );

  const apiEndpoint = cfg.kubernetes.apiVip ?? nodes.controlPlanes[0].ip;

  const cp0Prep = new remote.Command(
    `${name}-cp0-common`,
    {
      connection: {
        host: nodes.controlPlanes[0].ip,
        user: username,
        privateKey
      },
      create: `chmod +x ${scriptDir}/*.sh && K8S_VERSION=${k8sVersion} bash ${scriptDir}/bootstrap-common.sh`
    },
    { dependsOn: [joinScript] }
  );

  const cp0Init = new remote.Command(
    `${name}-cp0-init`,
    {
      connection: {
        host: nodes.controlPlanes[0].ip,
        user: username,
        privateKey
      },
      create: `NODE_IP=${nodes.controlPlanes[0].ip} API_ENDPOINT=${apiEndpoint} POD_CIDR=${cfg.kubernetes.podCidr} SERVICE_CIDR=${cfg.kubernetes.serviceCidr} CLUSTER_DOMAIN=${cfg.kubernetes.clusterDomain} bash ${scriptDir}/control-plane-init.sh`
    },
    { dependsOn: [cp0Prep] }
  );

  const parsed = pulumi.output(cp0Init.stdout).apply((s) => {
    const obj = JSON.parse(s.trim());
    return {
      joinWorker: obj.joinWorker as string,
      joinControlPlane: obj.joinControlPlane as string,
      kubeconfigB64: obj.kubeconfigB64 as string
    };
  });

  const joinWorkerCommand = parsed.apply((p) => p.joinWorker);
  const joinControlPlaneCommand = parsed.apply((p) => p.joinControlPlane);
  const kubeconfigB64 = pulumi.secret(parsed.apply((p) => p.kubeconfigB64));
  const kubeconfig = kubeconfigB64.apply((b64) => Buffer.from(b64, "base64").toString("utf8"));

  const additionalControlPlanes = nodes.controlPlanes.slice(1);
  additionalControlPlanes.forEach((node, idx) => {
    const copyCommon = new remote.CopyToRemote(
      `${name}-cp${idx + 2}-copy-common`,
      {
        connection: { host: node.ip, user: username, privateKey },
        source: bootstrapCommonAsset,
        remotePath: `${scriptDir}/bootstrap-common.sh`
      },
      { dependsOn: [node.wait, cp0Init] }
    );
    const copyJoin = new remote.CopyToRemote(
      `${name}-cp${idx + 2}-copy-join`,
      {
        connection: { host: node.ip, user: username, privateKey },
        source: joinNodeAsset,
        remotePath: `${scriptDir}/join-node.sh`
      },
      { dependsOn: [copyCommon] }
    );

    const prep = new remote.Command(
      `${name}-cp${idx + 2}-common`,
      {
        connection: { host: node.ip, user: username, privateKey },
        create: `chmod +x ${scriptDir}/*.sh && K8S_VERSION=${k8sVersion} bash ${scriptDir}/bootstrap-common.sh`
      },
      { dependsOn: [copyJoin] }
    );

    new remote.Command(
      `${name}-cp${idx + 2}-join`,
      {
        connection: { host: node.ip, user: username, privateKey },
        create: joinControlPlaneCommand.apply((cmd) => `JOIN_COMMAND='${escapeSingleQuotes(cmd)}' bash ${scriptDir}/join-node.sh`)
      },
      { dependsOn: [prep] }
    );
  });

  nodes.workers.forEach((node, idx) => {
    const copyCommon = new remote.CopyToRemote(
      `${name}-w${idx + 1}-copy-common`,
      {
        connection: { host: node.ip, user: username, privateKey },
        source: bootstrapCommonAsset,
        remotePath: `${scriptDir}/bootstrap-common.sh`
      },
      { dependsOn: [node.wait, cp0Init] }
    );
    const copyJoin = new remote.CopyToRemote(
      `${name}-w${idx + 1}-copy-join`,
      {
        connection: { host: node.ip, user: username, privateKey },
        source: joinNodeAsset,
        remotePath: `${scriptDir}/join-node.sh`
      },
      { dependsOn: [copyCommon] }
    );
    const prep = new remote.Command(
      `${name}-w${idx + 1}-common`,
      {
        connection: { host: node.ip, user: username, privateKey },
        create: `chmod +x ${scriptDir}/*.sh && K8S_VERSION=${k8sVersion} bash ${scriptDir}/bootstrap-common.sh`
      },
      { dependsOn: [copyJoin] }
    );
    new remote.Command(
      `${name}-w${idx + 1}-join`,
      {
        connection: { host: node.ip, user: username, privateKey },
        create: joinWorkerCommand.apply((cmd) => `JOIN_COMMAND='${escapeSingleQuotes(cmd)}' bash ${scriptDir}/join-node.sh`)
      },
      { dependsOn: [prep] }
    );
  });

  return { kubeconfig, kubeconfigB64, joinWorkerCommand, joinControlPlaneCommand };
}

export function buildNodeHandles(
  name: string,
  ips: string[],
  dependsOn: pulumi.Input<pulumi.Resource>[],
  username: pulumi.Input<string>,
  privateKeyPath: pulumi.Input<string>
): NodeHandle[] {
  return ips.map((ip, i) => ({
    name: `${name}-${i + 1}`,
    ip,
    wait: waitForSsh(`${name}-${i + 1}-wait-ssh`, ip, username, privateKeyPath, dependsOn)
  }));
}

function escapeSingleQuotes(s: string): string {
  return s.replace(/'/g, "'\\''");
}
