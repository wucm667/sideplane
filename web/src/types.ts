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

export type NodeStatus = NodeStatusWithDrift

export type AuditAction =
  | 'enrollment.token.create'
  | 'node.enroll'
  | 'node.delete'
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
