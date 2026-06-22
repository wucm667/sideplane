import type { DesiredConfig, NodeStatusWithDrift, ProviderModelConfig, RuntimeConfigSnapshot as GeneratedRuntimeConfigSnapshot, RuntimeStatus as GeneratedRuntimeStatus } from './generated/api.ts'

export type {
  APIError,
  AuditEvent,
  ConfigApplyResult,
  ConfigApplyStep,
  ConfigDiffEntry,
  CreateEnrollmentTokenResponse,
  DeepProbeResult,
  DesiredConfig,
  EffectiveConfigPreviewRequest,
  EffectiveConfigResponse,
  Job,
  JobStatus,
  JobType,
  ListAuditEventsResponse,
  ListNodesResponse,
  NodeState,
  ProviderModelConfig,
  RollbackBackup,
  RollbackJobPayload,
  RollbackJobResult,
  RollbackRequest,
  RestartJobResult,
  RestartRequest,
} from './generated/api.ts'

export type NodeLabels = Record<string, string>
export type RuntimeHealthState = 'healthy' | 'degraded' | 'unknown'
export interface RuntimeHealth {
  state: RuntimeHealthState
  reason?: string
}
export type RuntimeStatus = GeneratedRuntimeStatus & { health?: RuntimeHealth; version?: string; outdated?: boolean }
export type RuntimeConfigSnapshot = GeneratedRuntimeConfigSnapshot & { health?: RuntimeHealth }
export type NodeStatus = Omit<NodeStatusWithDrift, 'runtimes'> & { labels?: NodeLabels; maintenance?: boolean; sidecarOutdated?: boolean; runtimes?: RuntimeStatus[] }

export interface ServerSettings {
  expectedSidecarVersion: string
  expectedRuntimeVersions: Record<string, string>
}

export interface NodeLabelsRequest {
  labels: NodeLabels
}

export interface NodeLabelsResponse {
  nodeId: string
  labels: NodeLabels
}

export interface NodeMaintenanceResponse {
  nodeId: string
  maintenance: boolean
}

export interface RollbackBackupInventoryItem {
  ref: string
  sourceJobId: string
  runtimeType?: string
  profile?: string
  configHash?: string
  createdAt?: string
}

export interface ListRollbackBackupsResponse {
  backups: RollbackBackupInventoryItem[]
  total: number
  limit: number
}

export type RolloutState = 'pending' | 'scheduled' | 'running' | 'paused' | 'completed' | 'aborted' | 'failed'
export type RolloutBatchState = 'pending' | 'running' | 'completed' | 'paused' | 'failed'
export type RolloutNodeState = 'pending' | 'dispatched' | 'succeeded' | 'failed' | 'timed_out' | 'offline'
export type RolloutAction = 'pause' | 'resume' | 'abort'

export interface RolloutSpec {
  selector?: Record<string, string>
  nodeIds?: string[]
  runtimeType: string
  profile?: string
  target: ProviderModelConfig
  batchSize?: number
  startAt?: string
  live: boolean
  autoRollbackOnFailure?: boolean
  allowOverlap?: boolean
  healthTimeout?: number
}

export interface RolloutNodeProgress {
  nodeId: string
  jobId?: string
  state: RolloutNodeState
  lastError?: string
  startedAt?: string
  finishedAt?: string
  rollbackJobId?: string
  rolledBack?: boolean
}

export interface RolloutBatch {
  index: number
  nodeIds: string[]
  state: RolloutBatchState
  nodes: Record<string, RolloutNodeProgress>
}

export interface Rollout {
  id: string
  spec: RolloutSpec
  state: RolloutState
  batches: RolloutBatch[]
  pauseReason?: string
  failingNodeIds?: string[]
  createdAt: string
  updatedAt: string
  finishedAt?: string
}

export interface CreateRolloutRequest {
  spec: RolloutSpec
  templateId?: string
}

export interface CreateRolloutResponse {
  rollout: Rollout
}

export interface RolloutTemplate {
  id: string
  name: string
  spec: RolloutSpec
  createdAt: string
}

export interface CreateRolloutTemplateResponse {
  template: RolloutTemplate
}

export interface ListRolloutTemplatesResponse {
  templates: RolloutTemplate[]
}

export interface ListRolloutsResponse {
  rollouts: Rollout[]
  total: number
  limit: number
  offset: number
}

export interface GetRolloutResponse {
  rollout: Rollout
}

export interface RolloutActionRequest {
  action: RolloutAction
}

export interface RolloutActionResponse {
  rollout: Rollout
}

export interface BulkJobResult {
  nodeId: string
  jobId?: string
  error?: string
}

export interface BulkJobResponse {
  jobs: BulkJobResult[]
  created: number
}

export interface BulkNodeLabelsResponse {
  nodeIds: string[]
  updated: number
}

export type AlertEventType = 'node.offline' | 'node.drift' | 'rollout.paused' | 'rollout.failed'
export type AlertWebhookKind = 'generic' | 'slack'

export interface AlertWebhook {
  id: string
  kind: AlertWebhookKind
  url: string
  events: AlertEventType[]
  hasSecret: boolean
  disabled: boolean
  createdAt: string
}

export interface CreateAlertWebhookResponse {
  webhook: AlertWebhook
  secret?: string
}

export interface ListAlertWebhooksResponse {
  webhooks: AlertWebhook[]
}

export type OperatorTokenScope = 'admin' | 'readonly'

export interface OperatorToken {
  id: string
  name: string
  scope: OperatorTokenScope
  createdAt: string
  lastUsedAt?: string
  revokedAt?: string
}

export interface CreateOperatorTokenRequest {
  name: string
  scope?: OperatorTokenScope
}

export interface CreateOperatorTokenResponse {
  operatorToken: OperatorToken
  token: string
}

export interface ListOperatorTokensResponse {
  tokens: OperatorToken[]
}

export interface RevokeOperatorTokenResponse {
  operatorToken: OperatorToken
}

export interface DesiredConfigHistoryEntry {
  id: string
  config: DesiredConfig
  desiredHash?: string
  updatedAt: string
  actor: string
}

export interface ListDesiredConfigHistoryResponse {
  history: DesiredConfigHistoryEntry[]
  total: number
  limit: number
  offset: number
}

export interface RevertDesiredConfigRequest {
  historyId: string
}

export interface RevertDesiredConfigResponse {
  desired: DesiredConfig
  history: DesiredConfigHistoryEntry
}

export type AuditAction =
  | 'enrollment.token.create'
  | 'operator.token.create'
  | 'operator.token.list'
  | 'operator.token.revoke'
  | 'node.enroll'
  | 'node.delete'
  | 'node.labels.update'
  | 'job.create'
  | 'job.complete'
  | 'job.fail'
  | 'config.apply'
  | 'restart'
  | 'rollback'
  | 'rollout.create'
  | 'rollout.pause'
  | 'rollout.resume'
  | 'rollout.abort'
  | 'config.desired.update'
  | 'config.desired.revert'

export interface AuditFilters {
  nodeId: string
  action: AuditAction | ''
}
