export type Phase = "Pending" | "Running" | "Failed" | "Succeeded" | "Stopped" | "";

export interface Meta {
  project: string;
  name: string;
  uid: string;
  generation: number;
  createdAt: string;
  updatedAt: string;
  labels?: Record<string, string>;
}

export interface Status {
  phase: Phase;
  message?: string;
  nodeId?: string;
  updatedAt: string;
}

export interface Project {
  meta: Meta;
  displayName?: string;
  description?: string;
}

export interface PortMapping {
  host: number;
  container: number;
  protocol: string;
}

export interface Container {
  meta: Meta;
  image: string;
  command?: string[];
  args?: string[];
  env?: Record<string, string>;
  ports?: PortMapping[];
  restartPolicy: string;
  resources?: { cpuMillicores?: number; memoryMB?: number };
  nodeId?: string;
  status: Status & { containerdId?: string; startedAt?: string; ipAddress?: string; image?: string };
}

export interface Bucket {
  meta: Meta;
  region?: string;
  versioning?: boolean;
  websiteAccess?: boolean;
  garageId?: string;
  sizeBytes?: number;
  status: Status;
}

export interface AccessKey {
  meta: Meta;
  bucketName?: string;
  accessKeyId: string;
  secretAccessKey?: string;
  permissions: string;
}

export interface FunctionTrigger {
  http?: { path: string; methods?: string[] };
  cron?: { schedule: string };
}

export interface FunctionRecord {
  meta: Meta;
  runtime: "wasm" | "firecracker";
  handler?: string;
  sourceRef: string;
  triggers: FunctionTrigger[];
  env?: Record<string, string>;
  memoryMB?: number;
  timeoutMs?: number;
  status: Status;
}

export interface Node {
  meta: Meta;
  address: string;
  apiAddress: string;
  raftAddress: string;
  gossipAddress: string;
  roles: string[];
  capacityGB: number;
  zone?: string;
  version: string;
  status: Status;
  lastSeenAt: string;
}

export interface ClusterConfig {
  initialized: boolean;
  multiNode: boolean;
  replicationFactor: number;
  createdAt?: string;
  updatedAt?: string;
}

export interface SetupStatus {
  initialized: boolean;
  multiNode: boolean;
  replicationFactor: number;
}
