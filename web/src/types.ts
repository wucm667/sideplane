export type NodeState = 'fresh' | 'stale' | 'offline'

export interface RuntimeStatus {
  name: string
  type?: string
  version?: string
  state?: string
  provider?: string
  model?: string
  configHash?: string
  lastError?: string
}

export interface RuntimeConfigSnapshot {
  runtimeName: string
  runtimeType: string
  configPath?: string
  source?: string
  profile?: string
  provider?: string
  model?: string
  configHash?: string
  warnings?: string[]
}

export interface ProviderModelConfig {
  provider?: string
  model?: string
}

export interface ConfigDiffEntry {
  field: string
  actual?: string
  desired?: string
  change: string
}

export interface EffectiveConfigResponse {
  nodeId: string
  runtimeType?: string
  profile?: string
  effective: ProviderModelConfig
  desiredHash?: string
  actual?: RuntimeConfigSnapshot
  diff: ConfigDiffEntry[]
}

export interface DeepProbeResult {
  runtimes?: RuntimeStatus[]
  configSnapshots?: RuntimeConfigSnapshot[]
}

export interface DesiredConfig {
  global?: ProviderModelConfig
  nodeOverrides?: Record<string, ProviderModelConfig>
  runtimeProfileOverrides?: Record<string, ProviderModelConfig>
  nodeRuntimeProfileOverrides?: Record<string, ProviderModelConfig>
}

export interface EffectiveConfigPreviewRequest {
  nodeId: string
  runtimeType?: string
  profile?: string
  desired: ProviderModelConfig
}

export interface CreateEnrollmentTokenResponse {
  token: string
  expiresAt: string
}

export interface ConfigApplyStep {
  name: string
  status: string
  detail?: string
}

export interface ConfigApplyResult {
  planId: string
  dryRun: boolean
  backupPath?: string
  tempPath?: string
  steps: ConfigApplyStep[]
}

export interface NodeStatus {
  nodeId: string
  hostname?: string
  state: NodeState
  sidecarVersion?: string
  lastHeartbeatAt: string
  runtimes?: RuntimeStatus[]
  configHash?: string
  lastError?: string
  drift?: boolean
}

export type JobType = 'deep_probe' | 'config_apply'

export type JobStatus = 'pending' | 'claimed' | 'completed' | 'failed'

export interface Job {
  id: string
  nodeId: string
  type: JobType
  status: JobStatus
  payloadJson?: string
  resultJson?: string
  error?: string
  createdAt: string
  claimedAt?: string
  claimExpiresAt?: string
  finishedAt?: string
}

export interface AuditEvent {
  id: string
  actor: string
  action: string
  targetNode?: string
  detail?: string
  createdAt: string
}

export type AuditAction = 'enrollment.token.create' | 'node.enroll' | 'node.delete' | 'job.create' | 'job.complete' | 'job.fail' | 'config.apply' | 'config.desired.update'

export interface AuditFilters {
  nodeId: string
  action: AuditAction | ''
}

export interface ListAuditEventsResponse {
  events: AuditEvent[]
}
