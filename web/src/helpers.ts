import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { AuditEvent, AuditFilters, ConfigApplyResult, DeepProbeResult, EffectiveConfigResponse, Job, JobStatus, ListAuditEventsResponse, ListNodesResponse, NodeState, NodeStatus, RestartRequest, RollbackRequest, RuntimeConfigSnapshot, RuntimeStatus } from './types.ts'

const NODE_REFRESH_MS = 10_000
const ACTIVE_JOB_REFRESH_MS = 2_000
const AUDIT_REFRESH_MS = 10_000
const DEFAULT_AUDIT_LIMIT = 100
const DEFAULT_JOB_LIMIT = 50
const JOB_LIMIT_STEP = 50
const ACTIVE_JOB_STATUSES: JobStatus[] = ['pending', 'claimed']
const OPERATOR_TOKEN_STORAGE_KEY = 'sideplane.operatorToken'
const THEME_STORAGE_KEY = 'sideplane.theme'

export type View = 'fleet' | 'node' | 'activity' | 'enrollment'
export type Theme = 'light' | 'dark'

export interface RollbackCandidate {
  ref: string
  sourceJobId: string
  planId: string
  createdAt?: string
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

export function runtimeLabel(runtime: RuntimeStatus): string {
  if (runtime.provider && runtime.model) return `${runtime.provider}/${runtime.model}`
  if (runtime.model) return runtime.model
  return runtime.name || runtime.type || 'runtime'
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

export function snapshotForRuntime(runtime: RuntimeStatus, snapshots: RuntimeConfigSnapshot[]): RuntimeConfigSnapshot | undefined {
  return snapshots.find((snapshot) => {
    if (runtime.type && snapshot.runtimeType === runtime.type) return true
    return runtime.name && snapshot.runtimeName === runtime.name
  })
}

export function normalizeNodeListResponse(payload: NodeStatus[] | ListNodesResponse): NodeStatus[] {
  return Array.isArray(payload) ? payload : payload.nodes
}

export function useFleetPageController() {
  const [nodes, setNodes] = useState<NodeStatus[] | null>(null)
  const [jobsByNode, setJobsByNode] = useState<Record<string, Job[]>>({})
  const [jobsLoadingByNode, setJobsLoadingByNode] = useState<Record<string, boolean>>({})
  const [jobsErrorByNode, setJobsErrorByNode] = useState<Record<string, string>>({})
  const [jobStatusByNode, setJobStatusByNode] = useState<Record<string, JobStatus | ''>>({})
  const [jobLimitByNode, setJobLimitByNode] = useState<Record<string, number>>({})
  const [creatingByNode, setCreatingByNode] = useState<Record<string, boolean>>({})
  const [restartingByNode, setRestartingByNode] = useState<Record<string, boolean>>({})
  const [rollingBackByNode, setRollingBackByNode] = useState<Record<string, boolean>>({})
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [operatorToken, setOperatorToken] = useState(loadStoredOperatorToken)
  const [auditEvents, setAuditEvents] = useState<AuditEvent[]>([])
  const [auditFilters, setAuditFilters] = useState<AuditFilters>({ nodeId: '', action: '' })
  const [auditLimit, setAuditLimit] = useState(DEFAULT_AUDIT_LIMIT)
  const [auditLoading, setAuditLoading] = useState(true)
  const [auditError, setAuditError] = useState<string | null>(null)
  const [effectiveByNode, setEffectiveByNode] = useState<Record<string, EffectiveConfigResponse>>({})
  const [effectiveErrorByNode, setEffectiveErrorByNode] = useState<Record<string, string>>({})
  const [theme, setTheme] = useState<Theme>(loadStoredTheme)
  const [view, setView] = useState<View>('fleet')
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
      const res = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}/jobs?${params.toString()}`, { headers })
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
    const res = await fetch('/api/nodes')
    if (!res.ok) {
      throw new Error(await apiErrorMessage(res))
    }
    const payload = (await res.json()) as NodeStatus[] | ListNodesResponse
    const data = normalizeNodeListResponse(payload)
    if (!mountedRef.current) return null
    setNodes(data)
    setError(null)
    return data
  }, [])

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

  const createDeepProbe = useCallback(async (nodeId: string) => {
    if (!mountedRef.current) return
    setCreatingByNode((current) => ({ ...current, [nodeId]: true }))
    try {
      const headers: HeadersInit = { 'Content-Type': 'application/json' }
      const token = operatorToken.trim()
      if (token) {
        headers.Authorization = `Bearer ${token}`
      }

      const res = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}/jobs`, {
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

      const res = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}/restart`, {
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

      const res = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}/rollback`, {
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
      const res = await fetch(query ? `/api/audit?${query}` : '/api/audit')
      if (!res.ok) {
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
  }, [auditFilters, auditLimit])

  const loadEffectiveConfig = useCallback(async (nodeId: string, runtimeType = 'hermes', profile = 'default') => {
    try {
      const params = new URLSearchParams({ nodeId, runtimeType, profile })
      const res = await fetch(`/api/config/effective?${params.toString()}`)
      if (!res.ok) {
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
  }, [])

  useEffect(() => {
    refreshFleet(false)
  }, [refreshFleet])

  useEffect(() => {
    loadAuditEvents()
  }, [loadAuditEvents])

  useEffect(() => {
    const interval = window.setInterval(() => {
      refreshFleet(false)
    }, NODE_REFRESH_MS)
    return () => window.clearInterval(interval)
  }, [refreshFleet])

  useEffect(() => {
    const interval = window.setInterval(() => {
      loadAuditEvents()
    }, AUDIT_REFRESH_MS)
    return () => window.clearInterval(interval)
  }, [loadAuditEvents])

  useEffect(() => {
    const nodeIdsWithActiveJobs = Object.entries(jobsByNode)
      .filter(([, jobs]) => hasActiveJobs(jobs))
      .map(([nodeId]) => nodeId)

    if (nodeIdsWithActiveJobs.length === 0) return

    const interval = window.setInterval(() => {
      void Promise.all(nodeIdsWithActiveJobs.map((nodeId) => loadNodeJobs(nodeId, false)))
    }, ACTIVE_JOB_REFRESH_MS)

    return () => window.clearInterval(interval)
  }, [jobsByNode, loadNodeJobs])

  useEffect(() => {
    if (view === 'node' && selectedNodeId) {
      loadEffectiveConfig(selectedNodeId)
    }
  }, [loadEffectiveConfig, selectedNodeId, view])

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

  const refreshSelectedNodeAfterApply = () => {
    if (!selectedNodeId) return
    void loadNodeJobs(selectedNodeId, false)
    void loadEffectiveConfig(selectedNodeId)
  }

  return {
    auditError,
    auditEvents,
    auditFilters,
    auditLimit,
    auditLoading,
    bannerText,
    changeView,
    createDeepProbe,
    createRollback,
    createRestart,
    creatingByNode,
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
    loading,
    loadMoreNodeJobs,
    nodes: safeNodes,
    operatorToken,
    openNode,
    refreshFleet,
    refreshSelectedNodeAfterApply,
    refreshing,
    rollingBackByNode,
    restartingByNode,
    selectedNode,
    setOperatorToken,
    setAuditFilters,
    setAuditLimit,
    setNodeJobStatusFilter,
    stats,
    theme,
    toggleTheme,
    view,
    loadAuditEvents,
  }
}
