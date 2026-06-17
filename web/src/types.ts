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
  redactedValues?: Record<string, string>
}

export interface DeepProbeResult {
  runtimes?: RuntimeStatus[]
  configSnapshots?: RuntimeConfigSnapshot[]
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
}

export type JobType = 'deep_probe'

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
