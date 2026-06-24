import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { LANG_STORAGE_KEY, loadStoredLang, type Lang } from './i18n.ts'
import type { AuditEvent, AuditFilters, ConfigApplyResult, CreateRolloutRequest, DeepProbeResult, EffectiveConfigResponse, Job, JobStatus, ListAuditEventsResponse, ListNodesResponse, ListRollbackBackupsResponse, ListRolloutsResponse, NodeLabels, NodeLabelsResponse, NodeMaintenanceResponse, NodeState, NodeStatus, ProviderCatalogResponse, ProviderDefinition, RestartRequest, RollbackBackupInventoryItem, RollbackRequest, Rollout, RolloutAction, RolloutActionResponse, RolloutState, RuntimeConfigSnapshot, RuntimeStatus, UpsertProviderRequest } from './types.ts'

const NODE_REFRESH_MS = 10_000
const ACTIVE_JOB_REFRESH_MS = 2_000
const AUDIT_REFRESH_MS = 10_000
const ROLLOUT_REFRESH_MS = 5_000
const LIVE_RECONNECT_MS = 5_000
const DEFAULT_AUDIT_LIMIT = 100
const DEFAULT_JOB_LIMIT = 50
const JOB_LIMIT_STEP = 50
const ACTIVE_JOB_STATUSES: JobStatus[] = ['pending', 'claimed']
const OPERATOR_TOKEN_STORAGE_KEY = 'sideplane.operatorToken'
const THEME_STORAGE_KEY = 'sideplane.theme'

declare global {
  interface Window {
    __SIDEPLANE_BASE__?: string
  }
}

export type View = 'fleet' | 'node' | 'rollouts' | 'activity' | 'enrollment' | 'providers'
export type Theme = 'light' | 'dark'

export function sideplaneBasePath(): string {
  if (typeof window === 'undefined') return ''
  const raw = typeof window.__SIDEPLANE_BASE__ === 'string' ? window.__SIDEPLANE_BASE__.trim() : ''
  if (!raw || raw === '/') return ''
  const withLeadingSlash = raw.startsWith('/') ? raw : `/${raw}`
  return withLeadingSlash.replace(/\/+$/, '')
}

export function apiURL(path: string): string {
  const normalizedPath = path.startsWith('/') ? path : `/${path}`
  return `${sideplaneBasePath()}${normalizedPath}`
}

export function sideplaneServerURL(): string {
  if (typeof window === 'undefined') return ''
  return `${window.location.origin}${sideplaneBasePath()}`
}

// fuzzyMatch reports whether every character of query appears in order within
// text (case-insensitive subsequence match). An empty query always matches.
export function fuzzyMatch(query: string, text: string): boolean {
  const q = query.trim().toLowerCase()
  if (q === '') return true
  const t = text.toLowerCase()
  let qi = 0
  for (let ti = 0; ti < t.length && qi < q.length; ti++) {
    if (t[ti] === q[qi]) qi++
  }
  return qi === q.length
}

// filterFuzzy returns items whose searchable text fuzzily matches query,
// preserving input order.
export function filterFuzzy<T>(items: T[], query: string, toText: (item: T) => string): T[] {
  const q = query.trim()
  if (q === '') return items
  return items.filter((item) => fuzzyMatch(q, toText(item)))
}

export interface RollbackCandidate {
  ref: string
  sourceJobId: string
  planId: string
  createdAt?: string
}

export interface FleetOverviewMetrics {
  totalNodes: number
  freshNodes: number
  staleNodes: number
  offlineNodes: number
  maintenanceNodes: number
  driftedNodes: number
  outdatedSidecars: number
  outdatedRuntimes: number
  runtimeCount: number
  activeJobs: number
  activeRollouts: number
  runningRollouts: number
  pausedRollouts: number
}

interface EventTicketResponse {
  ticket: string
  expiresAt: string
}

interface ServerEventPayload {
  jobId?: string
  nodeId?: string
  rolloutId?: string
  state?: string
  status?: string
}

function loadStoredOperatorToken(): string {
  try {
    return window.localStorage.getItem(OPERATOR_TOKEN_STORAGE_KEY) ?? ''
  } catch {
    return ''
  }
}

function loadStoredTheme(): Theme {
  try {
    return window.localStorage.getItem(THEME_STORAGE_KEY) === 'dark' ? 'dark' : 'light'
  } catch {
    return 'light'
  }
}

export function stateBadgeClasses(state: NodeState): string {
  switch (state) {
    case 'fresh':
      return 'border-emerald-500/25 bg-emerald-500/10 text-emerald-600'
    case 'stale':
      return 'border-amber-500/30 bg-amber-500/10 text-amber-600'
    case 'offline':
      return 'border-rose-500/30 bg-rose-500/10 text-rose-600'
    default:
      return 'border-[var(--sp-border)] bg-[var(--sp-surface-2)] text-[var(--sp-muted)]'
  }
}

export function jobBadgeClasses(status: JobStatus): string {
  switch (status) {
    case 'pending':
      return 'border-[var(--sp-border)] bg-[var(--sp-surface-2)] text-[var(--sp-muted)]'
    case 'claimed':
      return 'border-sky-500/30 bg-sky-500/10 text-sky-600'
    case 'completed':
      return 'border-emerald-500/25 bg-emerald-500/10 text-emerald-600'
    case 'failed':
      return 'border-rose-500/30 bg-rose-500/10 text-rose-600'
    default:
      return 'border-[var(--sp-border)] bg-[var(--sp-surface-2)] text-[var(--sp-muted)]'
  }
}

export function rolloutBadgeClasses(state: RolloutState): string {
  switch (state) {
    case 'pending':
      return 'border-[var(--sp-border)] bg-[var(--sp-surface-2)] text-[var(--sp-muted)]'
    case 'scheduled':
      return 'border-violet-500/30 bg-violet-500/10 text-violet-600'
    case 'running':
      return 'border-sky-500/30 bg-sky-500/10 text-sky-600'
    case 'paused':
      return 'border-amber-500/30 bg-amber-500/10 text-amber-600'
    case 'completed':
      return 'border-emerald-500/25 bg-emerald-500/10 text-emerald-600'
    case 'aborted':
    case 'failed':
      return 'border-rose-500/30 bg-rose-500/10 text-rose-600'
    default:
      return 'border-[var(--sp-border)] bg-[var(--sp-surface-2)] text-[var(--sp-muted)]'
  }
}

export function formatDate(iso: string | undefined): string {
  if (!iso?.trim()) return '-'

  const date = new Date(iso)
  if (Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1) return '-'

  return date.toLocaleString()
}

export function formatRelativeTime(iso: string | undefined): string {
  if (!iso?.trim()) return '-'

  const date = new Date(iso)
  if (Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1) return '-'

  const seconds = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000))
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

export function compactHash(hash: string | undefined): string {
  if (!hash?.trim()) return '-'
  const normalized = hash.replace(/^sha256:/, '')
  if (normalized.length <= 16) return normalized
  return `${normalized.slice(0, 12)}…`
}

export async function apiErrorMessage(res: Response): Promise<string> {
  const fallback = `HTTP ${res.status}: ${res.statusText}`
  try {
    const contentType = res.headers.get('Content-Type') ?? ''
    if (contentType.includes('application/json')) {
      const data: unknown = await res.json()
      if (data && typeof data === 'object' && 'message' in data) {
        const message = String((data as { message?: unknown }).message ?? '').trim()
        if (message) return message
      }
      return fallback
    }
    const text = (await res.text()).trim()
    return text || fallback
  } catch {
    return fallback
  }
}

function hasActiveJobs(jobs: Job[]): boolean {
  return jobs.some((job) => ACTIVE_JOB_STATUSES.includes(job.status))
}

function isActiveJob(job: Job): boolean {
  return ACTIVE_JOB_STATUSES.includes(job.status)
}

function mergeActiveJobs(previous: Job[], next: Job[]): Job[] {
  const seen = new Set(next.map((job) => job.id))
  const active = previous.filter((job) => isActiveJob(job) && !seen.has(job.id))
  return [...active, ...next]
}

function parseServerEventPayload(event: Event): ServerEventPayload {
  if (!(event instanceof MessageEvent) || typeof event.data !== 'string') return {}

  try {
    const parsed = JSON.parse(event.data) as unknown
    if (!parsed || typeof parsed !== 'object') return {}
    const record = parsed as Record<string, unknown>
    const payload: ServerEventPayload = {}
    for (const key of ['jobId', 'nodeId', 'rolloutId', 'state', 'status'] as const) {
      if (typeof record[key] === 'string') {
        payload[key] = record[key]
      }
    }
    return payload
  } catch {
    return {}
  }
}

export function hasActiveDeepProbe(jobs: Job[]): boolean {
  return jobs.some((job) => job.type === 'deep_probe' && ACTIVE_JOB_STATUSES.includes(job.status))
}

export function hasActiveConfigApply(jobs: Job[]): boolean {
  return jobs.some((job) => job.type === 'config_apply' && ACTIVE_JOB_STATUSES.includes(job.status))
}

export function hasActiveRestart(jobs: Job[]): boolean {
  return jobs.some((job) => job.type === 'restart' && ACTIVE_JOB_STATUSES.includes(job.status))
}

export function hasActiveRollback(jobs: Job[]): boolean {
  return jobs.some((job) => job.type === 'rollback' && ACTIVE_JOB_STATUSES.includes(job.status))
}

export function runtimeKey(runtime: RuntimeStatus, index: number): string {
  return `${runtime.name || runtime.type || 'runtime'}-${index}`
}

// runtimeModelLabel returns the provider/model in use for a runtime, falling
// back to the runtime name or type when no model is known. It never includes
// the version or deployment mode — those are shown as separate fields.
export function runtimeModelLabel(runtime: RuntimeStatus): string {
  return runtime.provider && runtime.model
    ? `${runtime.provider}/${runtime.model}`
    : runtime.model || runtime.name || runtime.type || 'runtime'
}

// runtimeDeploymentLabel returns how the runtime is deployed/managed
// (container / systemd / local), or an empty string when unknown.
export function runtimeDeploymentLabel(runtime: RuntimeStatus): string {
  return runtime.deploymentMode?.trim() ?? ''
}

// runtimeDeploymentDisplay maps Sideplane deployment modes to compact operator
// enum tokens for the fleet table.
export function runtimeDeploymentDisplay(runtime: RuntimeStatus): string {
  const deployment = runtimeDeploymentLabel(runtime)
  if (!deployment) return ''

  switch (deployment.toLowerCase()) {
    case 'container':
      return 'DOCKER'
    case 'systemd':
      return 'SYSTEM'
    case 'local':
      return 'LOCAL'
    default:
      return deployment.toUpperCase()
  }
}

// runtimeVersionLabel returns the runtime software version, or an empty string
// when unknown.
export function runtimeVersionLabel(runtime: RuntimeStatus): string {
  return runtime.version?.trim() ?? ''
}

function parseDeepProbeResult(resultJson: string | undefined): DeepProbeResult | null {
  if (!resultJson?.trim()) return null

  try {
    const parsed = JSON.parse(resultJson) as DeepProbeResult
    if (!parsed || typeof parsed !== 'object') return null
    return parsed
  } catch {
    return null
  }
}

export function latestConfigSnapshots(jobs: Job[]): RuntimeConfigSnapshot[] {
  for (const job of jobs) {
    if (job.type !== 'deep_probe' || job.status !== 'completed') continue
    const snapshots = parseDeepProbeResult(job.resultJson)?.configSnapshots ?? []
    if (snapshots.length > 0) return snapshots
  }
  return []
}

export function latestRollbackBackup(jobs: Job[]): RollbackCandidate | null {
  for (const job of jobs) {
    if (job.type !== 'config_apply') continue
    if (job.status !== 'completed' && job.status !== 'failed') continue
    const result = parseConfigApplyResult(job.resultJson)
    if (!result?.backupPath || !result.planId) continue
    return {
      ref: result.backup?.ref || `config_apply:${job.id}:${result.planId}`,
      sourceJobId: result.backup?.sourceJobId || job.id,
      planId: result.planId,
      createdAt: result.backup?.createdAt || job.finishedAt || job.createdAt,
    }
  }
  return null
}

function parseConfigApplyResult(resultJson: string | undefined): ConfigApplyResult | null {
  if (!resultJson?.trim()) return null
  try {
    const parsed = JSON.parse(resultJson) as ConfigApplyResult
    if (!parsed || typeof parsed !== 'object') return null
    return parsed
  } catch {
    return null
  }
}

export function groupRows(nodes: NodeStatus[]) {
  const groups = new Map<string, number>()
  groups.set('all nodes', nodes.length)
  for (const node of nodes) {
    if (!node.runtimes || node.runtimes.length === 0) {
      groups.set('no runtime', (groups.get('no runtime') ?? 0) + 1)
      continue
    }
    for (const runtime of node.runtimes) {
      const key = runtime.type || runtime.name || 'runtime'
      groups.set(key, (groups.get(key) ?? 0) + 1)
    }
  }
  return Array.from(groups.entries()).map(([name, count]) => ({ name, count }))
}

export function fleetOverviewMetrics(nodes: NodeStatus[], jobsByNode: Record<string, Job[]>, rollouts: Rollout[]): FleetOverviewMetrics {
  return {
    totalNodes: nodes.length,
    freshNodes: nodes.filter((node) => node.state === 'fresh').length,
    staleNodes: nodes.filter((node) => node.state === 'stale').length,
    offlineNodes: nodes.filter((node) => node.state === 'offline').length,
    maintenanceNodes: nodes.filter((node) => node.maintenance).length,
    driftedNodes: nodes.filter((node) => node.drift).length,
    outdatedSidecars: nodes.filter((node) => node.sidecarOutdated).length,
    outdatedRuntimes: nodes.reduce((total, node) => total + (node.runtimes ?? []).filter((runtime) => runtime.outdated).length, 0),
    runtimeCount: nodes.reduce((total, node) => total + (node.runtimes?.length ?? 0), 0),
    activeJobs: Object.values(jobsByNode).reduce((total, jobs) => total + jobs.filter(isActiveJob).length, 0),
    activeRollouts: rollouts.filter(isActiveRollout).length,
    runningRollouts: rollouts.filter((rollout) => rollout.state === 'running').length,
    pausedRollouts: rollouts.filter((rollout) => rollout.state === 'paused').length,
  }
}

function isActiveRollout(rollout: Rollout): boolean {
  return rollout.state === 'pending' || rollout.state === 'scheduled' || rollout.state === 'running' || rollout.state === 'paused'
}

export function snapshotForRuntime(runtime: RuntimeStatus, snapshots: RuntimeConfigSnapshot[]): RuntimeConfigSnapshot | undefined {
  return snapshots.find((snapshot) => {
    if (runtime.type && snapshot.runtimeType === runtime.type) return true
    return runtime.name && snapshot.runtimeName === runtime.name
  })
}

export function mergeFleetNodes(previous: NodeStatus[] | null, incoming: NodeStatus[]): NodeStatus[] {
  if (!previous?.length) return incoming

  const previousByNodeId = new Map(previous.map((node) => [node.nodeId, node]))
  return incoming.map((incomingNode) => {
    const previousNode = previousByNodeId.get(incomingNode.nodeId)
    if (!previousNode) return incomingNode
    return mergeFleetNode(previousNode, incomingNode)
  })
}

function mergeFleetNode(previous: NodeStatus, incoming: NodeStatus): NodeStatus {
  let merged = incoming

  const hostname = stickyString(incoming.hostname, previous.hostname)
  if (hostname !== incoming.hostname) {
    merged = { ...merged, hostname }
  }

  const sidecarVersion = stickyString(incoming.sidecarVersion, previous.sidecarVersion)
  if (sidecarVersion !== incoming.sidecarVersion) {
    merged = { ...merged, sidecarVersion }
  }

  const runtimes = mergeRuntimeStatuses(previous.runtimes, incoming.runtimes)
  if (runtimes !== incoming.runtimes) {
    merged = { ...merged, runtimes }
  }

  return nodeStatusesEqual(merged, previous) ? previous : merged
}

function mergeRuntimeStatuses(previous: RuntimeStatus[] | undefined, incoming: RuntimeStatus[] | undefined): RuntimeStatus[] | undefined {
  if (!previous?.length || !incoming) return incoming

  const previousByName = new Map<string, RuntimeStatus>()
  const previousByType = new Map<string, RuntimeStatus>()
  for (const runtime of previous) {
    const name = runtime.name.trim()
    if (name && !previousByName.has(name)) previousByName.set(name, runtime)
    const type = runtime.type?.trim()
    if (type && !previousByType.has(type)) previousByType.set(type, runtime)
  }

  const merged = incoming.map((runtime) => {
    const previousRuntime = findPreviousRuntime(runtime, previousByName, previousByType)
    if (!previousRuntime) return runtime
    return mergeRuntimeStatus(previousRuntime, runtime)
  })

  return runtimeArraysEqual(merged, previous) ? previous : merged
}

function findPreviousRuntime(runtime: RuntimeStatus, previousByName: Map<string, RuntimeStatus>, previousByType: Map<string, RuntimeStatus>): RuntimeStatus | undefined {
  const name = runtime.name.trim()
  if (name) {
    const match = previousByName.get(name)
    if (match) return match
  }

  const type = runtime.type?.trim()
  return type ? previousByType.get(type) : undefined
}

function mergeRuntimeStatus(previous: RuntimeStatus, incoming: RuntimeStatus): RuntimeStatus {
  let merged = incoming

  const version = stickyString(incoming.version, previous.version)
  if (version !== incoming.version) {
    merged = { ...merged, version }
  }

  const deploymentMode = stickyString(incoming.deploymentMode, previous.deploymentMode)
  if (deploymentMode !== incoming.deploymentMode) {
    merged = { ...merged, deploymentMode }
  }

  const provider = stickyString(incoming.provider, previous.provider)
  if (provider !== incoming.provider) {
    merged = { ...merged, provider }
  }

  const model = stickyString(incoming.model, previous.model)
  if (model !== incoming.model) {
    merged = { ...merged, model }
  }

  const configHash = stickyString(incoming.configHash, previous.configHash)
  if (configHash !== incoming.configHash) {
    merged = { ...merged, configHash }
  }

  return runtimeStatusesEqual(merged, previous) ? previous : merged
}

function stickyString<T extends string | undefined>(incoming: T, previous: T): T {
  return isBlank(incoming) && !isBlank(previous) ? previous : incoming
}

function isBlank(value: string | undefined): boolean {
  return !value?.trim()
}

function nodeStatusesEqual(a: NodeStatus, b: NodeStatus): boolean {
  return a.nodeId === b.nodeId
    && a.hostname === b.hostname
    && a.state === b.state
    && a.sidecarVersion === b.sidecarVersion
    && a.lastHeartbeatAt === b.lastHeartbeatAt
    && a.configHash === b.configHash
    && a.drift === b.drift
    && a.maintenance === b.maintenance
    && a.lastError === b.lastError
    && a.sidecarOutdated === b.sidecarOutdated
    && valuesEqual(a.labels, b.labels)
    && runtimeArraysEqual(a.runtimes, b.runtimes)
}

function runtimeArraysEqual(a: RuntimeStatus[] | undefined, b: RuntimeStatus[] | undefined): boolean {
  if (a === b) return true
  if (!a || !b || a.length !== b.length) return false
  return a.every((runtime, index) => runtimeStatusesEqual(runtime, b[index]))
}

function runtimeStatusesEqual(a: RuntimeStatus, b: RuntimeStatus): boolean {
  return a.name === b.name
    && a.type === b.type
    && a.version === b.version
    && a.deploymentMode === b.deploymentMode
    && a.state === b.state
    && a.provider === b.provider
    && a.model === b.model
    && a.configHash === b.configHash
    && a.lastError === b.lastError
    && a.outdated === b.outdated
    && valuesEqual(a.health, b.health)
    && valuesEqual(a.warnings, b.warnings)
}

function valuesEqual(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false
    return a.every((value, index) => valuesEqual(value, b[index]))
  }
  if (!a || !b || typeof a !== 'object' || typeof b !== 'object') return false

  const aRecord = a as Record<string, unknown>
  const bRecord = b as Record<string, unknown>
  const keys = new Set([...Object.keys(aRecord), ...Object.keys(bRecord)])
  for (const key of keys) {
    if (!valuesEqual(aRecord[key], bRecord[key])) return false
  }
  return true
}

export function normalizeNodeListResponse(payload: NodeStatus[] | ListNodesResponse): NodeStatus[] {
  return Array.isArray(payload) ? payload : payload.nodes
}

export function useFleetPageController() {
  const [nodes, setNodes] = useState<NodeStatus[] | null>(null)
  const [jobsByNode, setJobsByNode] = useState<Record<string, Job[]>>({})
  const [backupsByNode, setBackupsByNode] = useState<Record<string, RollbackBackupInventoryItem[]>>({})
  const [backupsLoadingByNode, setBackupsLoadingByNode] = useState<Record<string, boolean>>({})
  const [backupsErrorByNode, setBackupsErrorByNode] = useState<Record<string, string>>({})
  const [jobsLoadingByNode, setJobsLoadingByNode] = useState<Record<string, boolean>>({})
  const [jobsErrorByNode, setJobsErrorByNode] = useState<Record<string, string>>({})
  const [jobStatusByNode, setJobStatusByNode] = useState<Record<string, JobStatus | ''>>({})
  const [jobLimitByNode, setJobLimitByNode] = useState<Record<string, number>>({})
  const [creatingByNode, setCreatingByNode] = useState<Record<string, boolean>>({})
  const [restartingByNode, setRestartingByNode] = useState<Record<string, boolean>>({})
  const [rollingBackByNode, setRollingBackByNode] = useState<Record<string, boolean>>({})
  const [savingLabelsByNode, setSavingLabelsByNode] = useState<Record<string, boolean>>({})
  const [savingMaintenanceByNode, setSavingMaintenanceByNode] = useState<Record<string, boolean>>({})
  const [labelErrorByNode, setLabelErrorByNode] = useState<Record<string, string>>({})
  const [maintenanceErrorByNode, setMaintenanceErrorByNode] = useState<Record<string, string>>({})
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [operatorToken, setOperatorToken] = useState(loadStoredOperatorToken)
  const [auditEvents, setAuditEvents] = useState<AuditEvent[]>([])
  const [auditFilters, setAuditFilters] = useState<AuditFilters>({ nodeId: '', action: '' })
  const [auditLimit, setAuditLimit] = useState(DEFAULT_AUDIT_LIMIT)
  const [auditLoading, setAuditLoading] = useState(true)
  const [auditError, setAuditError] = useState<string | null>(null)
  const [rollouts, setRollouts] = useState<Rollout[]>([])
  const [rolloutsLoading, setRolloutsLoading] = useState(true)
  const [rolloutsError, setRolloutsError] = useState<string | null>(null)
  const [creatingRollout, setCreatingRollout] = useState(false)
  const [rolloutActioningId, setRolloutActioningId] = useState<string | null>(null)
  const [providers, setProviders] = useState<ProviderDefinition[]>([])
  const [providersLoading, setProvidersLoading] = useState(false)
  const [providersError, setProvidersError] = useState<string | null>(null)
  const [savingProvider, setSavingProvider] = useState(false)
  const [effectiveByNode, setEffectiveByNode] = useState<Record<string, EffectiveConfigResponse>>({})
  const [effectiveErrorByNode, setEffectiveErrorByNode] = useState<Record<string, string>>({})
  const [liveConnected, setLiveConnected] = useState(false)
  const [theme, setTheme] = useState<Theme>(loadStoredTheme)
  const [lang, setLang] = useState<Lang>(loadStoredLang)
  const [view, setView] = useState<View>('fleet')
  const [selector, setSelector] = useState('')
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null)
  const mountedRef = useRef(false)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  useEffect(() => {
    try {
      const token = operatorToken.trim()
      if (token) {
        window.localStorage.setItem(OPERATOR_TOKEN_STORAGE_KEY, token)
      } else {
        window.localStorage.removeItem(OPERATOR_TOKEN_STORAGE_KEY)
      }
    } catch {
      // Local storage is a convenience only; the API request still works with in-memory state.
    }
  }, [operatorToken])

  useEffect(() => {
    try {
      window.localStorage.setItem(THEME_STORAGE_KEY, theme)
    } catch {
      // Theme persistence is optional.
    }
  }, [theme])

  useEffect(() => {
    try {
      window.localStorage.setItem(LANG_STORAGE_KEY, lang)
    } catch {
      // Language persistence is optional.
    }
    try {
      document.documentElement.lang = lang
    } catch {
      // Document metadata is best-effort in non-browser test environments.
    }
  }, [lang])

  const loadNodeJobs = useCallback(async (nodeId: string, showLoading = true, options?: { status?: JobStatus | ''; limit?: number }) => {
    if (!mountedRef.current) return
    if (showLoading) {
      setJobsLoadingByNode((current) => ({ ...current, [nodeId]: true }))
    }
    try {
      const headers: HeadersInit = {}
      const token = operatorToken.trim()
      if (token) {
        headers.Authorization = `Bearer ${token}`
      }
      const status = options?.status ?? jobStatusByNode[nodeId] ?? ''
      const limit = options?.limit ?? jobLimitByNode[nodeId] ?? DEFAULT_JOB_LIMIT
      const params = new URLSearchParams({ limit: String(limit) })
      if (status) {
        params.set('status', status)
      }
      const res = await fetch(apiURL(`/api/nodes/${encodeURIComponent(nodeId)}/jobs?${params.toString()}`), { headers })
      if (!res.ok) {
        throw new Error(await apiErrorMessage(res))
      }
      const data: Job[] = await res.json()
      if (!mountedRef.current) return
      setJobsByNode((current) => ({
        ...current,
        [nodeId]: status ? mergeActiveJobs(current[nodeId] ?? [], data) : data,
      }))
      setJobsErrorByNode((current) => {
        const next = { ...current }
        delete next[nodeId]
        return next
      })
    } catch (e) {
      if (!mountedRef.current) return
      setJobsErrorByNode((current) => ({
        ...current,
        [nodeId]: e instanceof Error ? e.message : 'Unknown error',
      }))
    } finally {
      if (mountedRef.current && showLoading) {
        setJobsLoadingByNode((current) => ({ ...current, [nodeId]: false }))
      }
    }
  }, [jobLimitByNode, jobStatusByNode, operatorToken])

  const setNodeJobStatusFilter = useCallback((nodeId: string, status: JobStatus | '') => {
    const limit = DEFAULT_JOB_LIMIT
    setJobStatusByNode((current) => ({ ...current, [nodeId]: status }))
    setJobLimitByNode((current) => ({ ...current, [nodeId]: limit }))
    void loadNodeJobs(nodeId, true, { status, limit })
  }, [loadNodeJobs])

  const loadMoreNodeJobs = useCallback((nodeId: string) => {
    const status = jobStatusByNode[nodeId] ?? ''
    const limit = (jobLimitByNode[nodeId] ?? DEFAULT_JOB_LIMIT) + JOB_LIMIT_STEP
    setJobLimitByNode((current) => ({ ...current, [nodeId]: limit }))
    void loadNodeJobs(nodeId, true, { status, limit })
  }, [jobLimitByNode, jobStatusByNode, loadNodeJobs])

  const loadNodes = useCallback(async () => {
    const params = new URLSearchParams()
    const selectorValue = selector.trim()
    if (selectorValue) {
      params.set('selector', selectorValue)
    }
    const query = params.toString()
    const path = query ? `/api/nodes?${query}` : '/api/nodes'
    const headers: HeadersInit = {}
    const token = operatorToken.trim()
    if (token) {
      headers.Authorization = `Bearer ${token}`
    }
    const res = await fetch(apiURL(path), { headers })
    if (!res.ok) {
      if (res.status === 401) throw new Error('Operator token required or invalid')
      throw new Error(await apiErrorMessage(res))
    }
    const payload = (await res.json()) as NodeStatus[] | ListNodesResponse
    const data = normalizeNodeListResponse(payload)
    if (!mountedRef.current) return null
    setNodes((current) => mergeFleetNodes(current, data))
    setError(null)
    return data
  }, [operatorToken, selector])

  const refreshFleet = useCallback(async (showRefreshing = true) => {
    if (!mountedRef.current) return
    if (showRefreshing) {
      setRefreshing(true)
    }
    try {
      const data = await loadNodes()
      if (data) {
        await Promise.all(data.map((node) => loadNodeJobs(node.nodeId, showRefreshing)))
      }
    } catch (e) {
      if (mountedRef.current) {
        setError(e instanceof Error ? e.message : 'Unknown error')
      }
    } finally {
      if (mountedRef.current) {
        setLoading(false)
        if (showRefreshing) {
          setRefreshing(false)
        }
      }
    }
  }, [loadNodeJobs, loadNodes])

  const refreshNodeList = useCallback(async () => {
    try {
      await loadNodes()
    } catch (e) {
      if (mountedRef.current) {
        setError(e instanceof Error ? e.message : 'Unknown error')
      }
    } finally {
      if (mountedRef.current) {
        setLoading(false)
      }
    }
  }, [loadNodes])

  const createDeepProbe = useCallback(async (nodeId: string) => {
    if (!mountedRef.current) return
    setCreatingByNode((current) => ({ ...current, [nodeId]: true }))
    try {
      const headers: HeadersInit = { 'Content-Type': 'application/json' }
      const token = operatorToken.trim()
      if (token) {
        headers.Authorization = `Bearer ${token}`
      }

      const res = await fetch(apiURL(`/api/nodes/${encodeURIComponent(nodeId)}/jobs`), {
        method: 'POST',
        headers,
        body: JSON.stringify({ type: 'deep_probe' }),
      })
      if (!res.ok) {
        if (res.status === 401) {
          throw new Error('Operator token required or invalid')
        }
        throw new Error(await apiErrorMessage(res))
      }
      const job: Job = await res.json()
      if (!mountedRef.current) return
      setJobsByNode((current) => ({
        ...current,
        [nodeId]: [job, ...(current[nodeId] ?? []).filter((item) => item.id !== job.id)],
      }))
      setJobsErrorByNode((current) => {
        const next = { ...current }
        delete next[nodeId]
        return next
      })
      await loadNodeJobs(nodeId, false)
    } catch (e) {
      if (!mountedRef.current) return
      setJobsErrorByNode((current) => ({
        ...current,
        [nodeId]: e instanceof Error ? e.message : 'Unknown error',
      }))
    } finally {
      if (mountedRef.current) {
        setCreatingByNode((current) => ({ ...current, [nodeId]: false }))
      }
    }
  }, [loadNodeJobs, operatorToken])

  const createRestart = useCallback(async (nodeId: string, request: RestartRequest) => {
    if (!mountedRef.current) return
    setRestartingByNode((current) => ({ ...current, [nodeId]: true }))
    try {
      const headers: HeadersInit = { 'Content-Type': 'application/json' }
      const token = operatorToken.trim()
      if (token) {
        headers.Authorization = `Bearer ${token}`
      }

      const res = await fetch(apiURL(`/api/nodes/${encodeURIComponent(nodeId)}/restart`), {
        method: 'POST',
        headers,
        body: JSON.stringify(request),
      })
      if (!res.ok) {
        if (res.status === 401) {
          throw new Error('Operator token required or invalid')
        }
        throw new Error(await apiErrorMessage(res))
      }
      const job: Job = await res.json()
      if (!mountedRef.current) return
      setJobsByNode((current) => ({
        ...current,
        [nodeId]: [job, ...(current[nodeId] ?? []).filter((item) => item.id !== job.id)],
      }))
      setJobsErrorByNode((current) => {
        const next = { ...current }
        delete next[nodeId]
        return next
      })
      await loadNodeJobs(nodeId, false)
    } catch (e) {
      if (!mountedRef.current) return
      setJobsErrorByNode((current) => ({
        ...current,
        [nodeId]: e instanceof Error ? e.message : 'Unknown error',
      }))
    } finally {
      if (mountedRef.current) {
        setRestartingByNode((current) => ({ ...current, [nodeId]: false }))
      }
    }
  }, [loadNodeJobs, operatorToken])

  const createRollback = useCallback(async (nodeId: string, request: RollbackRequest) => {
    if (!mountedRef.current) return
    setRollingBackByNode((current) => ({ ...current, [nodeId]: true }))
    try {
      const headers: HeadersInit = { 'Content-Type': 'application/json' }
      const token = operatorToken.trim()
      if (token) {
        headers.Authorization = `Bearer ${token}`
      }

      const res = await fetch(apiURL(`/api/nodes/${encodeURIComponent(nodeId)}/rollback`), {
        method: 'POST',
        headers,
        body: JSON.stringify(request),
      })
      if (!res.ok) {
        if (res.status === 401) {
          throw new Error('Operator token required or invalid')
        }
        throw new Error(await apiErrorMessage(res))
      }
      const job: Job = await res.json()
      if (!mountedRef.current) return
      setJobsByNode((current) => ({
        ...current,
        [nodeId]: [job, ...(current[nodeId] ?? []).filter((item) => item.id !== job.id)],
      }))
      setJobsErrorByNode((current) => {
        const next = { ...current }
        delete next[nodeId]
        return next
      })
      await loadNodeJobs(nodeId, false)
    } catch (e) {
      if (!mountedRef.current) return
      setJobsErrorByNode((current) => ({
        ...current,
        [nodeId]: e instanceof Error ? e.message : 'Unknown error',
      }))
    } finally {
      if (mountedRef.current) {
        setRollingBackByNode((current) => ({ ...current, [nodeId]: false }))
      }
    }
  }, [loadNodeJobs, operatorToken])

  const loadNodeBackups = useCallback(async (nodeId: string, showLoading = true) => {
    if (!mountedRef.current) return
    const token = operatorToken.trim()
    if (!token) {
      setBackupsByNode((current) => ({ ...current, [nodeId]: [] }))
      return
    }
    if (showLoading) {
      setBackupsLoadingByNode((current) => ({ ...current, [nodeId]: true }))
    }
    try {
      const res = await fetch(apiURL(`/api/nodes/${encodeURIComponent(nodeId)}/backups`), {
        headers: { Authorization: `Bearer ${token}` },
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as ListRollbackBackupsResponse
      if (!mountedRef.current) return
      setBackupsByNode((current) => ({ ...current, [nodeId]: data.backups ?? [] }))
      setBackupsErrorByNode((current) => {
        const next = { ...current }
        delete next[nodeId]
        return next
      })
    } catch (e) {
      if (!mountedRef.current) return
      setBackupsErrorByNode((current) => ({
        ...current,
        [nodeId]: e instanceof Error ? e.message : 'Unknown error',
      }))
    } finally {
      if (mountedRef.current && showLoading) {
        setBackupsLoadingByNode((current) => ({ ...current, [nodeId]: false }))
      }
    }
  }, [operatorToken])

  const saveNodeLabels = useCallback(async (nodeId: string, labels: NodeLabels) => {
    if (!mountedRef.current) return
    setSavingLabelsByNode((current) => ({ ...current, [nodeId]: true }))
    try {
      const headers: HeadersInit = { 'Content-Type': 'application/json' }
      const token = operatorToken.trim()
      if (token) {
        headers.Authorization = `Bearer ${token}`
      }

      const res = await fetch(apiURL(`/api/nodes/${encodeURIComponent(nodeId)}/labels`), {
        method: 'PUT',
        headers,
        body: JSON.stringify({ labels }),
      })
      if (!res.ok) {
        if (res.status === 401) {
          throw new Error('Operator token required or invalid')
        }
        throw new Error(await apiErrorMessage(res))
      }
      const response = (await res.json()) as NodeLabelsResponse
      if (!mountedRef.current) return
      setNodes((current) => current?.map((node) => (
        node.nodeId === nodeId ? { ...node, labels: response.labels ?? {} } : node
      )) ?? current)
      setLabelErrorByNode((current) => {
        const next = { ...current }
        delete next[nodeId]
        return next
      })
    } catch (e) {
      if (!mountedRef.current) return
      setLabelErrorByNode((current) => ({
        ...current,
        [nodeId]: e instanceof Error ? e.message : 'Unknown error',
      }))
    } finally {
      if (mountedRef.current) {
        setSavingLabelsByNode((current) => ({ ...current, [nodeId]: false }))
      }
    }
  }, [operatorToken])

  const setNodeMaintenance = useCallback(async (nodeId: string, maintenance: boolean) => {
    if (!mountedRef.current) return
    setSavingMaintenanceByNode((current) => ({ ...current, [nodeId]: true }))
    try {
      const headers: HeadersInit = { 'Content-Type': 'application/json' }
      const token = operatorToken.trim()
      if (token) {
        headers.Authorization = `Bearer ${token}`
      }

      const res = await fetch(apiURL(`/api/nodes/${encodeURIComponent(nodeId)}/maintenance`), {
        method: 'PUT',
        headers,
        body: JSON.stringify({ maintenance }),
      })
      if (!res.ok) {
        if (res.status === 401) {
          throw new Error('Operator token required or invalid')
        }
        if (res.status === 403) {
          throw new Error('Operator token is read-only')
        }
        throw new Error(await apiErrorMessage(res))
      }
      const response = (await res.json()) as NodeMaintenanceResponse
      if (!mountedRef.current) return
      setNodes((current) => current?.map((node) => (
        node.nodeId === nodeId ? { ...node, maintenance: response.maintenance } : node
      )) ?? current)
      setMaintenanceErrorByNode((current) => {
        const next = { ...current }
        delete next[nodeId]
        return next
      })
    } catch (e) {
      if (!mountedRef.current) return
      setMaintenanceErrorByNode((current) => ({
        ...current,
        [nodeId]: e instanceof Error ? e.message : 'Unknown error',
      }))
    } finally {
      if (mountedRef.current) {
        setSavingMaintenanceByNode((current) => ({ ...current, [nodeId]: false }))
      }
    }
  }, [operatorToken])

  const loadAuditEvents = useCallback(async () => {
    try {
      const params = new URLSearchParams({ limit: String(auditLimit) })
      const nodeId = auditFilters.nodeId.trim()
      if (nodeId) {
        params.set('nodeId', nodeId)
      }
      if (auditFilters.action) {
        params.set('action', auditFilters.action)
      }
      const query = params.toString()
      const headers: HeadersInit = {}
      const token = operatorToken.trim()
      if (token) {
        headers.Authorization = `Bearer ${token}`
      }
      const res = await fetch(apiURL(query ? `/api/audit?${query}` : '/api/audit'), { headers })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        throw new Error(await apiErrorMessage(res))
      }
      const data: ListAuditEventsResponse = await res.json()
      if (!mountedRef.current) return
      setAuditEvents(data.events ?? [])
      setAuditError(null)
    } catch (e) {
      if (!mountedRef.current) return
      setAuditError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      if (mountedRef.current) {
        setAuditLoading(false)
      }
    }
  }, [auditFilters, auditLimit, operatorToken])

  const loadRollouts = useCallback(async (showLoading = true) => {
    if (!mountedRef.current) return
    const token = operatorToken.trim()
    if (!token) {
      setRollouts([])
      setRolloutsError('Operator token required')
      setRolloutsLoading(false)
      return
    }
    if (showLoading) {
      setRolloutsLoading(true)
    }
    try {
      const res = await fetch(apiURL('/api/rollouts'), {
        headers: { Authorization: `Bearer ${token}` },
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as ListRolloutsResponse
      if (!mountedRef.current) return
      setRollouts(data.rollouts ?? [])
      setRolloutsError(null)
    } catch (e) {
      if (!mountedRef.current) return
      setRolloutsError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      if (mountedRef.current) {
        setRolloutsLoading(false)
      }
    }
  }, [operatorToken])

  const loadProviders = useCallback(async () => {
    if (!mountedRef.current) return
    const token = operatorToken.trim()
    if (!token) {
      setProviders([])
      setProvidersError('Operator token required')
      setProvidersLoading(false)
      return
    }
    setProvidersLoading(true)
    try {
      const res = await fetch(apiURL('/api/config/providers'), {
        headers: { Authorization: `Bearer ${token}` },
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        if (res.status === 403) throw new Error('Operator token is read-only')
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as ProviderCatalogResponse
      if (!mountedRef.current) return
      setProviders(data.global ?? [])
      setProvidersError(null)
    } catch (e) {
      if (!mountedRef.current) return
      setProvidersError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      if (mountedRef.current) {
        setProvidersLoading(false)
      }
    }
  }, [operatorToken])

  const upsertProvider = useCallback(async (provider: ProviderDefinition, apiKey?: string): Promise<boolean> => {
    if (!mountedRef.current) return false
    const token = operatorToken.trim()
    if (!token) {
      setProvidersError('Operator token required')
      return false
    }
    setSavingProvider(true)
    try {
      const trimmedAPIKey = apiKey?.trim()
      const request: UpsertProviderRequest = trimmedAPIKey ? { provider, apiKey: trimmedAPIKey } : { provider }
      const res = await fetch(apiURL('/api/config/providers'), {
        method: 'PUT',
        headers: {
          Authorization: `Bearer ${token}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(request),
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        if (res.status === 403) throw new Error('Operator token is read-only')
        throw new Error(await apiErrorMessage(res))
      }
      if (!mountedRef.current) return false
      setProvidersError(null)
      await loadProviders()
      return true
    } catch (e) {
      if (!mountedRef.current) return false
      setProvidersError(e instanceof Error ? e.message : 'Unknown error')
      return false
    } finally {
      if (mountedRef.current) {
        setSavingProvider(false)
      }
    }
  }, [loadProviders, operatorToken])

  const deleteProvider = useCallback(async (name: string): Promise<boolean> => {
    if (!mountedRef.current) return false
    const token = operatorToken.trim()
    if (!token) {
      setProvidersError('Operator token required')
      return false
    }
    setSavingProvider(true)
    try {
      const res = await fetch(apiURL(`/api/config/providers?name=${encodeURIComponent(name)}`), {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${token}` },
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        if (res.status === 403) throw new Error('Operator token is read-only')
        throw new Error(await apiErrorMessage(res))
      }
      if (!mountedRef.current) return false
      setProvidersError(null)
      await loadProviders()
      return true
    } catch (e) {
      if (!mountedRef.current) return false
      setProvidersError(e instanceof Error ? e.message : 'Unknown error')
      return false
    } finally {
      if (mountedRef.current) {
        setSavingProvider(false)
      }
    }
  }, [loadProviders, operatorToken])

  const createRollout = useCallback(async (request: CreateRolloutRequest): Promise<Rollout | null> => {
    if (!mountedRef.current) return null
    const token = operatorToken.trim()
    if (!token) {
      setRolloutsError('Operator token required')
      return null
    }
    setCreatingRollout(true)
    try {
      const res = await fetch(apiURL('/api/rollouts'), {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${token}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(request),
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as { rollout: Rollout }
      if (!mountedRef.current) return null
      setRollouts((current) => [data.rollout, ...current.filter((rollout) => rollout.id !== data.rollout.id)])
      setRolloutsError(null)
      return data.rollout
    } catch (e) {
      if (!mountedRef.current) return null
      setRolloutsError(e instanceof Error ? e.message : 'Unknown error')
      return null
    } finally {
      if (mountedRef.current) {
        setCreatingRollout(false)
      }
    }
  }, [operatorToken])

  const performRolloutAction = useCallback(async (rolloutId: string, action: RolloutAction): Promise<Rollout | null> => {
    if (!mountedRef.current) return null
    const token = operatorToken.trim()
    if (!token) {
      setRolloutsError('Operator token required')
      return null
    }
    setRolloutActioningId(rolloutId)
    try {
      const res = await fetch(apiURL(`/api/rollouts/${encodeURIComponent(rolloutId)}/actions`), {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${token}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ action }),
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as RolloutActionResponse
      if (!mountedRef.current) return null
      setRollouts((current) => current.map((rollout) => (rollout.id === data.rollout.id ? data.rollout : rollout)))
      setRolloutsError(null)
      return data.rollout
    } catch (e) {
      if (!mountedRef.current) return null
      setRolloutsError(e instanceof Error ? e.message : 'Unknown error')
      return null
    } finally {
      if (mountedRef.current) {
        setRolloutActioningId(null)
      }
    }
  }, [operatorToken])

  const loadEffectiveConfig = useCallback(async (nodeId: string, runtimeType = 'hermes', profile = 'default') => {
    try {
      const params = new URLSearchParams({ nodeId, runtimeType, profile })
      const headers: HeadersInit = {}
      const token = operatorToken.trim()
      if (token) {
        headers.Authorization = `Bearer ${token}`
      }
      const res = await fetch(apiURL(`/api/config/effective?${params.toString()}`), { headers })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        throw new Error(await apiErrorMessage(res))
      }
      const data: EffectiveConfigResponse = await res.json()
      if (!mountedRef.current) return
      setEffectiveByNode((current) => ({ ...current, [nodeId]: data }))
      setEffectiveErrorByNode((current) => {
        const next = { ...current }
        delete next[nodeId]
        return next
      })
    } catch (e) {
      if (!mountedRef.current) return
      setEffectiveErrorByNode((current) => ({
        ...current,
        [nodeId]: e instanceof Error ? e.message : 'Unknown error',
      }))
    }
  }, [operatorToken])

  useEffect(() => {
    refreshFleet(false)
  }, [refreshFleet])

  useEffect(() => {
    loadAuditEvents()
  }, [loadAuditEvents])

  useEffect(() => {
    loadRollouts(false)
  }, [loadRollouts])

  useEffect(() => {
    const token = operatorToken.trim()
    if (!token || typeof window.EventSource === 'undefined') {
      setLiveConnected(false)
      return
    }

    let closed = false
    let source: EventSource | null = null
    let ticketRequest: AbortController | null = null
    let reconnectTimer: number | undefined

    const scheduleReconnect = () => {
      if (closed || reconnectTimer !== undefined) return
      reconnectTimer = window.setTimeout(() => {
        reconnectTimer = undefined
        void connect()
      }, LIVE_RECONNECT_MS)
    }

    const connect = async () => {
      if (closed) return
      ticketRequest?.abort()
      ticketRequest = new AbortController()

      try {
        const res = await fetch(apiURL('/api/events/tickets'), {
          method: 'POST',
          headers: { Authorization: `Bearer ${token}` },
          signal: ticketRequest.signal,
        })
        if (!res.ok) {
          throw new Error(await apiErrorMessage(res))
        }
        const ticket = (await res.json()) as EventTicketResponse
        if (closed || !ticket.ticket) return

        source?.close()
        source = new EventSource(apiURL(`/api/events?ticket=${encodeURIComponent(ticket.ticket)}`))
        source.onopen = () => {
          if (closed) return
          setLiveConnected(true)
        }
        source.onerror = () => {
          if (closed) return
          setLiveConnected(false)
          source?.close()
          source = null
          scheduleReconnect()
        }
        source.addEventListener('node', () => {
          void refreshNodeList()
        })
        source.addEventListener('job', (event) => {
          const payload = parseServerEventPayload(event)
          if (!payload.nodeId) return
          void loadNodeJobs(payload.nodeId, false)
        })
        source.addEventListener('rollout', () => {
          void loadRollouts(false)
        })
      } catch (e) {
        if (closed || e instanceof DOMException && e.name === 'AbortError') return
        setLiveConnected(false)
        scheduleReconnect()
      } finally {
        ticketRequest = null
      }
    }

    void connect()

    return () => {
      closed = true
      ticketRequest?.abort()
      source?.close()
      if (reconnectTimer !== undefined) {
        window.clearTimeout(reconnectTimer)
      }
      if (mountedRef.current) {
        setLiveConnected(false)
      }
    }
  }, [loadNodeJobs, loadRollouts, operatorToken, refreshNodeList])

  useEffect(() => {
    if (liveConnected) return
    const interval = window.setInterval(() => {
      refreshFleet(false)
    }, NODE_REFRESH_MS)
    return () => window.clearInterval(interval)
  }, [liveConnected, refreshFleet])

  useEffect(() => {
    const interval = window.setInterval(() => {
      loadAuditEvents()
    }, AUDIT_REFRESH_MS)
    return () => window.clearInterval(interval)
  }, [loadAuditEvents])

  useEffect(() => {
    if (liveConnected) return
    const interval = window.setInterval(() => {
      void loadRollouts(false)
    }, ROLLOUT_REFRESH_MS)
    return () => window.clearInterval(interval)
  }, [liveConnected, loadRollouts])

  useEffect(() => {
    if (liveConnected) return
    const nodeIdsWithActiveJobs = Object.entries(jobsByNode)
      .filter(([, jobs]) => hasActiveJobs(jobs))
      .map(([nodeId]) => nodeId)

    if (nodeIdsWithActiveJobs.length === 0) return

    const interval = window.setInterval(() => {
      void Promise.all(nodeIdsWithActiveJobs.map((nodeId) => loadNodeJobs(nodeId, false)))
    }, ACTIVE_JOB_REFRESH_MS)

    return () => window.clearInterval(interval)
  }, [jobsByNode, liveConnected, loadNodeJobs])

  useEffect(() => {
    if (view === 'node' && selectedNodeId) {
      loadEffectiveConfig(selectedNodeId)
      void loadNodeBackups(selectedNodeId)
    }
  }, [loadEffectiveConfig, loadNodeBackups, selectedNodeId, view])

  useEffect(() => {
    if (view === 'providers') {
      void loadProviders()
    }
  }, [loadProviders, view])

  const safeNodes = nodes ?? []
  const stats = useMemo(() => {
    const healthy = safeNodes.filter((node) => node.state === 'fresh').length
    const stale = safeNodes.filter((node) => node.state === 'stale').length
    const offline = safeNodes.filter((node) => node.state === 'offline').length
    const drift = safeNodes.filter((node) => node.drift).length
    return { healthy, stale, offline, drift }
  }, [safeNodes])
  const groups = useMemo(() => groupRows(safeNodes), [safeNodes])
  const selectedNode = safeNodes.find((node) => node.nodeId === selectedNodeId) ?? null
  const fleetSubtitle = `${safeNodes.length} nodes · ${groups.length} groups · ${stats.healthy} healthy`
  const bannerText = [
    stats.drift > 0 ? `${stats.drift} node${stats.drift === 1 ? '' : 's'} with config drift` : '',
    stats.stale > 0 ? `${stats.stale} stale` : '',
    stats.offline > 0 ? `${stats.offline} offline` : '',
  ].filter(Boolean).join(' · ')

  const openNode = (nodeId: string) => {
    setSelectedNodeId(nodeId)
    setView('node')
  }

  const changeView = (nextView: View) => {
    if (nextView !== 'node') setSelectedNodeId(null)
    setView(nextView)
  }

  const toggleTheme = () => setTheme((current) => (current === 'dark' ? 'light' : 'dark'))
  const toggleLang = () => setLang((current) => (current === 'en' ? 'zh' : 'en'))

  const refreshSelectedNodeAfterApply = () => {
    if (!selectedNodeId) return
    void loadNodeJobs(selectedNodeId, false)
    void loadEffectiveConfig(selectedNodeId)
    void loadNodeBackups(selectedNodeId, false)
  }

  return {
    auditError,
    auditEvents,
    auditFilters,
    auditLimit,
    auditLoading,
    backupsByNode,
    backupsErrorByNode,
    backupsLoadingByNode,
    bannerText,
    changeView,
    createRollout,
    createDeepProbe,
    createRollback,
    createRestart,
    creatingRollout,
    creatingByNode,
    deleteProvider,
    effectiveByNode,
    effectiveErrorByNode,
    error,
    fleetSubtitle,
    groups,
    jobsByNode,
    jobsErrorByNode,
    jobLimitByNode,
    jobsLoadingByNode,
    jobStatusByNode,
    liveConnected,
    loading,
    loadMoreNodeJobs,
    loadProviders,
    loadRollouts,
    maintenanceErrorByNode,
    nodes: safeNodes,
    operatorToken,
    openNode,
    providers,
    providersError,
    providersLoading,
    refreshFleet,
    refreshSelectedNodeAfterApply,
    refreshing,
    rollingBackByNode,
    rolloutActioningId,
    rollouts,
    rolloutsError,
    rolloutsLoading,
    savingProvider,
    savingLabelsByNode,
    savingMaintenanceByNode,
    restartingByNode,
    selectedNode,
    selector,
    setOperatorToken,
    setAuditFilters,
    setAuditLimit,
    setNodeJobStatusFilter,
    setSelector,
    labelErrorByNode,
    lang,
    saveNodeLabels,
    setNodeMaintenance,
    stats,
    setLang,
    theme,
    toggleLang,
    toggleTheme,
    view,
    loadAuditEvents,
    performRolloutAction,
    upsertProvider,
  }
}
