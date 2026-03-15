export interface VmSpec {
  name: string;
  switchName: string;
  vmPath: string;
  baseVhdxPath: string;
  differencingDiskPath: string;
  cloudInitIsoPath: string;
  vcpu: number;
  memoryMb: number;
  diskGb: number;
  secureBoot: boolean;
  adoptExisting?: boolean;
  instanceId?: string;
}
