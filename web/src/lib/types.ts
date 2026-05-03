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

export interface BuildSpec {
  language?: string;
  buildImage?: string;
  commands?: string[];
  output?: string;
  entrypoint?: string[];
  template?: string;
}

export interface GitSourceSpec {
  url: string;
  ref?: string;
  subPath?: string;
  authSecret?: string;
  webhookToken?: string;
  webhookSecret?: string;
  build: BuildSpec;
}

export interface FunctionSource {
  type?: "upload" | "git" | "";
  git?: GitSourceSpec;
}

export interface SecretMount {
  secret: string;
  envName?: string;
  mountPath?: string;
}

export interface FunctionRecord {
  meta: Meta;
  runtime: "wasm" | "firecracker";
  handler?: string;
  sourceRef?: string;
  source?: FunctionSource;
  triggers: FunctionTrigger[];
  env?: Record<string, string>;
  secretMounts?: SecretMount[];
  memoryMB?: number;
  timeoutMs?: number;
  latestBuild?: string;
  status: Status;
}

export interface BuildRecord {
  meta: Meta;
  functionRef: string;
  commitSha?: string;
  trigger?: string;
  status: Phase;
  message?: string;
  startedAt?: string;
  finishedAt?: string;
  logsRef?: string;
  artifactRef?: string;
}

export interface SecretRecord {
  meta: Meta;
  description?: string;
  keyId: string;
  version: number;
}

export interface WebhookAction {
  url: string;
  method?: string;
  headers?: Record<string, string>;
  secret?: string;
}

export interface InvokeAction {
  project?: string;
  function: string;
  path?: string;
}

export interface ContainerEventAction {
  project?: string;
  container: string;
  action: "start" | "stop" | "restart";
}

export interface EventMatch {
  types?: string[];
  subject?: string;
  filter?: Record<string, string>;
}

export interface EventActionSpec {
  webhook?: WebhookAction;
  invoke?: InvokeAction;
  container?: ContainerEventAction;
}

export interface EventRule {
  meta: Meta;
  description?: string;
  match: EventMatch;
  action: EventActionSpec;
  enabled: boolean;
  lastFiredAt?: string;
  fireCount?: number;
}

export interface EventRecord {
  uid: string;
  type: string;
  project?: string;
  subject?: string;
  at: string;
  data?: Record<string, string>;
}

export interface WebhookDelivery {
  meta: Meta;
  rule: string;
  url: string;
  status?: number;
  error?: string;
  attempt: number;
  nextAttempt?: string;
  done: boolean;
  startedAt: string;
  finishedAt?: string;
  eventUid?: string;
  eventType?: string;
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
