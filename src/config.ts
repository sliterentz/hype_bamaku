import * as pulumi from "@pulumi/pulumi";
import { z } from "zod";
import { validateUniqueHostnames } from "./utils/hostnames";

const ConfigSchema = z.object({
  env: z.enum(["dev", "staging", "prod"]).default("dev"),

  hyperv: z.object({
    switchName: z.string().min(1),
    externalAdapterName: z.string().min(1).optional(),
    vmPath: z.string().min(1).default("C:\\HyperV\\VMs"),
    baseVhdxPath: z.string().min(1),
    isoOutputDir: z.string().min(1).default(".pulumi-artifacts"),
    secureBoot: z.boolean().default(true),
    adoptExisting: z.boolean().default(false)
  }),

  network: z.object({
    cidr: z.string().min(1),
    gateway: z.string().min(1),
    dnsServers: z.array(z.string().min(1)).default(["1.1.1.1", "8.8.8.8"]),
    interfaceName: z.string().min(1).default("eth0"),
    metallbAddressPool: z.string().min(1)
  }),

  ssh: z.object({
    username: z.string().min(1).default("ubuntu"),
    publicKey: z.string().min(1),
    privateKeyPath: z.string().min(1)
  }),

  nodes: z.object({
    controlPlaneCount: z.number().int().min(1).default(1),
    workerCount: z.number().int().min(1).default(1),
    vcpu: z.number().int().min(1).default(4),
    memoryMb: z.number().int().min(1024).default(8192),
    diskGb: z.number().int().min(10).default(20),
    controlPlaneIps: z.array(z.string().min(1)).min(1),
    workerIps: z.array(z.string().min(1)).min(1),
    controlPlaneHostnames: z.array(z.string().min(1)).optional(),
    workerHostnames: z.array(z.string().min(1)).optional()
  }),

  kubernetes: z.object({
    version: z.string().min(1).default("1.30.0"),
    podCidr: z.string().min(1).default("10.244.0.0/16"),
    serviceCidr: z.string().min(1).default("10.96.0.0/12"),
    apiVip: z.string().min(1).optional(),
    clusterDomain: z.string().min(1).default("cluster.local")
  }),

  ingress: z.object({
    baseDomain: z.string().min(1).optional(),
    tlsMode: z.enum(["selfsigned", "letsencrypt"]).default("selfsigned"),
    letsEncryptEmail: z.string().min(3).optional()
  }),

  observability: z.object({
    logging: z.enum(["loki", "elk"]).default("loki"),
    prometheusGrafana: z.boolean().default(true)
  }),

  backup: z.object({
    enableVelero: z.boolean().default(false),
    veleroProvider: z.enum(["aws", "minio"]).default("minio"),
    veleroBucket: z.string().min(1).default("velero"),
    veleroS3Url: z.string().min(1).optional()
  })
});

export type AppConfig = z.infer<typeof ConfigSchema>;

export function loadConfig(): AppConfig {
  const cfg = new pulumi.Config();
  const hypervCfg = new pulumi.Config("hyperv");
  const networkCfg = new pulumi.Config("network");
  const sshCfg = new pulumi.Config("ssh");
  const nodesCfg = new pulumi.Config("nodes");
  const kubernetesCfg = new pulumi.Config("kubernetes");
  const ingressCfg = new pulumi.Config("ingress");
  const observabilityCfg = new pulumi.Config("observability");
  const backupCfg = new pulumi.Config("backup");

  const raw = {
    env: cfg.get("env") ?? stackNameToEnv(pulumi.getStack()),
    hyperv: {
      switchName: hypervCfg.require("switchName"),
      externalAdapterName: hypervCfg.get("externalAdapterName") ?? undefined,
      vmPath: hypervCfg.get("vmPath") ?? undefined,
      baseVhdxPath: hypervCfg.require("baseVhdxPath"),
      isoOutputDir: hypervCfg.get("isoOutputDir") ?? undefined,
      secureBoot: hypervCfg.getBoolean("secureBoot") ?? undefined,
      adoptExisting: hypervCfg.getBoolean("adoptExisting") ?? undefined
    },
    network: {
      cidr: networkCfg.require("cidr"),
      gateway: networkCfg.require("gateway"),
      dnsServers: networkCfg.getObject<string[]>("dnsServers") ?? undefined,
      interfaceName: networkCfg.get("interfaceName") ?? undefined,
      metallbAddressPool: networkCfg.require("metallbAddressPool")
    },
    ssh: {
      username: sshCfg.get("username") ?? undefined,
      publicKey: sshCfg.require("publicKey"),
      privateKeyPath: sshCfg.require("privateKeyPath")
    },
    nodes: {
      controlPlaneCount: nodesCfg.getNumber("controlPlaneCount") ?? undefined,
      workerCount: nodesCfg.getNumber("workerCount") ?? undefined,
      vcpu: nodesCfg.getNumber("vcpu") ?? undefined,
      memoryMb: nodesCfg.getNumber("memoryMb") ?? undefined,
      diskGb: nodesCfg.getNumber("diskGb") ?? undefined,
      controlPlaneIps: nodesCfg.requireObject<string[]>("controlPlaneIps"),
      workerIps: nodesCfg.requireObject<string[]>("workerIps"),
      controlPlaneHostnames: nodesCfg.getObject<string[]>("controlPlaneHostnames") ?? undefined,
      workerHostnames: nodesCfg.getObject<string[]>("workerHostnames") ?? undefined
    },
    kubernetes: {
      version: kubernetesCfg.get("version") ?? undefined,
      podCidr: kubernetesCfg.get("podCidr") ?? undefined,
      serviceCidr: kubernetesCfg.get("serviceCidr") ?? undefined,
      apiVip: kubernetesCfg.get("apiVip") ?? undefined,
      clusterDomain: kubernetesCfg.get("clusterDomain") ?? undefined
    },
    ingress: {
      baseDomain: ingressCfg.get("baseDomain") ?? undefined,
      tlsMode: (ingressCfg.get("tlsMode") as "selfsigned" | "letsencrypt" | null) ?? undefined,
      letsEncryptEmail: ingressCfg.get("letsEncryptEmail") ?? undefined
    },
    observability: {
      logging: (observabilityCfg.get("logging") as "loki" | "elk" | null) ?? undefined,
      prometheusGrafana: observabilityCfg.getBoolean("prometheusGrafana") ?? undefined
    },
    backup: {
      enableVelero: backupCfg.getBoolean("enableVelero") ?? undefined,
      veleroProvider: (backupCfg.get("veleroProvider") as "aws" | "minio" | null) ?? undefined,
      veleroBucket: backupCfg.get("veleroBucket") ?? undefined,
      veleroS3Url: backupCfg.get("veleroS3Url") ?? undefined
    }
  };

  const parsed = ConfigSchema.parse(raw);

  if (parsed.env === "prod" && parsed.nodes.controlPlaneCount < 3) {
    throw new Error("Untuk env=prod, nodes:controlPlaneCount minimal 3 untuk HA control-plane/etcd.");
  }

  if (parsed.nodes.controlPlaneIps.length < parsed.nodes.controlPlaneCount) {
    throw new Error("nodes:controlPlaneIps harus menyediakan IP sebanyak controlPlaneCount.");
  }

  if (parsed.nodes.workerIps.length < parsed.nodes.workerCount) {
    throw new Error("nodes:workerIps harus menyediakan IP sebanyak workerCount.");
  }

  if (parsed.ingress.tlsMode === "letsencrypt" && !parsed.ingress.letsEncryptEmail) {
    throw new Error("ingress:letsEncryptEmail wajib saat tlsMode=letsencrypt.");
  }

  if (parsed.env === "prod" && !parsed.kubernetes.apiVip) {
    throw new Error("kubernetes:apiVip wajib untuk HA API endpoint pada env=prod.");
  }

  // Validate Hostnames Uniqueness
  const allHostnames: string[] = [];
  if (parsed.nodes.controlPlaneHostnames) {
    allHostnames.push(...parsed.nodes.controlPlaneHostnames);
  }
  if (parsed.nodes.workerHostnames) {
    allHostnames.push(...parsed.nodes.workerHostnames);
  }
  if (allHostnames.length > 0) {
    validateUniqueHostnames(allHostnames);
  }

  // Ensure hostname lists match counts if provided
  if (parsed.nodes.controlPlaneHostnames && parsed.nodes.controlPlaneHostnames.length !== parsed.nodes.controlPlaneCount) {
    throw new Error(`nodes:controlPlaneHostnames length (${parsed.nodes.controlPlaneHostnames.length}) must match nodes:controlPlaneCount (${parsed.nodes.controlPlaneCount}).`);
  }
  if (parsed.nodes.workerHostnames && parsed.nodes.workerHostnames.length !== parsed.nodes.workerCount) {
    throw new Error(`nodes:workerHostnames length (${parsed.nodes.workerHostnames.length}) must match nodes:workerCount (${parsed.nodes.workerCount}).`);
  }

  return parsed;
}

function stackNameToEnv(stack: string): "dev" | "staging" | "prod" {
  if (stack.includes("prod")) return "prod";
  if (stack.includes("stag")) return "staging";
  return "dev";
}
