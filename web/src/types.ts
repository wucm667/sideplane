import type { NodeStatusWithDrift } from './generated/api.ts'

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
  RuntimeConfigSnapshot,
  RuntimeStatus,
} from './generated/api.ts'

export type NodeLabels = Record<string, string>
export type NodeStatus = NodeStatusWithDrift & { labels?: NodeLabels }

export interface NodeLabelsRequest {
  labels: NodeLabels
}

export interface NodeLabelsResponse {
  nodeId: string
  labels: NodeLabels
}

export type AuditAction =
  | 'enrollment.token.create'
  | 'node.enroll'
  | 'node.delete'
  | 'node.labels.update'
  | 'job.create'
  | 'job.complete'
  | 'job.fail'
  | 'config.apply'
  | 'restart'
  | 'rollback'
  | 'config.desired.update'

export interface AuditFilters {
  nodeId: string
  action: AuditAction | ''
}
