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
