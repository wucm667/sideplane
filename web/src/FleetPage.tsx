import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { AuditEvent, ConfigDiffEntry, CreateEnrollmentTokenResponse, DeepProbeResult, EffectiveConfigResponse, Job, JobStatus, ListAuditEventsResponse, NodeState, NodeStatus, RuntimeConfigSnapshot, RuntimeStatus } from './types.ts'
import ConfigWizard from './ConfigWizard.tsx'

const NODE_REFRESH_MS = 10_000
const ACTIVE_JOB_REFRESH_MS = 2_000
const AUDIT_REFRESH_MS = 10_000
const ACTIVE_JOB_STATUSES: JobStatus[] = ['pending', 'claimed']
const OPERATOR_TOKEN_STORAGE_KEY = 'sideplane.operatorToken'
const THEME_STORAGE_KEY = 'sideplane.theme'

type View = 'fleet' | 'node' | 'activity' | 'enrollment'
type Theme = 'light' | 'dark'

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

function stateBadgeClasses(state: NodeState): string {
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

function jobBadgeClasses(status: JobStatus): string {
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

function formatDate(iso: string | undefined): string {
  if (!iso?.trim()) return '-'

  const date = new Date(iso)
  if (Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1) return '-'

  return date.toLocaleString()
}

function formatRelativeTime(iso: string | undefined): string {
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

function compactHash(hash: string | undefined): string {
  if (!hash?.trim()) return '-'
  const normalized = hash.replace(/^sha256:/, '')
  if (normalized.length <= 16) return normalized
  return `${normalized.slice(0, 12)}…`
}

function hasActiveJobs(jobs: Job[]): boolean {
  return jobs.some((job) => ACTIVE_JOB_STATUSES.includes(job.status))
}

function hasActiveDeepProbe(jobs: Job[]): boolean {
  return jobs.some((job) => job.type === 'deep_probe' && ACTIVE_JOB_STATUSES.includes(job.status))
}

function hasActiveConfigApply(jobs: Job[]): boolean {
  return jobs.some((job) => job.type === 'config_apply' && ACTIVE_JOB_STATUSES.includes(job.status))
}

function runtimeKey(runtime: RuntimeStatus, index: number): string {
  return `${runtime.name || runtime.type || 'runtime'}-${index}`
}

function runtimeLabel(runtime: RuntimeStatus): string {
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

function latestConfigSnapshots(jobs: Job[]): RuntimeConfigSnapshot[] {
  for (const job of jobs) {
    if (job.type !== 'deep_probe' || job.status !== 'completed') continue
    const snapshots = parseDeepProbeResult(job.resultJson)?.configSnapshots ?? []
    if (snapshots.length > 0) return snapshots
  }
  return []
}

function groupRows(nodes: NodeStatus[]) {
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

export default function FleetPage() {
  const [nodes, setNodes] = useState<NodeStatus[] | null>(null)
  const [jobsByNode, setJobsByNode] = useState<Record<string, Job[]>>({})
  const [jobsLoadingByNode, setJobsLoadingByNode] = useState<Record<string, boolean>>({})
  const [jobsErrorByNode, setJobsErrorByNode] = useState<Record<string, string>>({})
  const [creatingByNode, setCreatingByNode] = useState<Record<string, boolean>>({})
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [operatorToken, setOperatorToken] = useState(loadStoredOperatorToken)
  const [auditEvents, setAuditEvents] = useState<AuditEvent[]>([])
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

  const loadNodeJobs = useCallback(async (nodeId: string, showLoading = true) => {
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
      const res = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}/jobs`, { headers })
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}: ${res.statusText}`)
      }
      const data: Job[] = await res.json()
      if (!mountedRef.current) return
      setJobsByNode((current) => ({ ...current, [nodeId]: data }))
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
  }, [operatorToken])

  const loadNodes = useCallback(async () => {
    const res = await fetch('/api/nodes')
    if (!res.ok) {
      throw new Error(`HTTP ${res.status}: ${res.statusText}`)
    }
    const data: NodeStatus[] = await res.json()
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
        throw new Error(`HTTP ${res.status}: ${res.statusText}`)
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

  const loadAuditEvents = useCallback(async () => {
    try {
      const res = await fetch('/api/audit')
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}: ${res.statusText}`)
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
  }, [])

  const loadEffectiveConfig = useCallback(async (nodeId: string, runtimeType = 'hermes', profile = 'default') => {
    try {
      const params = new URLSearchParams({ nodeId, runtimeType, profile })
      const res = await fetch(`/api/config/effective?${params.toString()}`)
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}: ${res.statusText}`)
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

  return (
    <div data-sideplane-theme={theme} className="min-h-screen bg-[var(--sp-bg)] text-[var(--sp-text)]">
      <div className="flex min-h-screen flex-col md:flex-row">
        <Sidebar
          currentView={view}
          groups={groups}
          operatorToken={operatorToken}
          theme={theme}
          onOperatorTokenChange={setOperatorToken}
          onThemeToggle={() => setTheme((current) => (current === 'dark' ? 'light' : 'dark'))}
          onViewChange={(nextView) => {
            if (nextView !== 'node') setSelectedNodeId(null)
            setView(nextView)
          }}
        />

        <main className="min-w-0 flex-1 overflow-y-auto">
          {view === 'fleet' && (
            <FleetOverview
              bannerText={bannerText}
              error={error}
              fleetSubtitle={fleetSubtitle}
              jobsByNode={jobsByNode}
              loading={loading}
              nodes={safeNodes}
              refreshing={refreshing}
              stats={stats}
              onOpenNode={openNode}
              onRefresh={() => refreshFleet()}
            />
          )}
          {view === 'node' && selectedNode && (
            <NodeDetailView
              creating={Boolean(creatingByNode[selectedNode.nodeId])}
              jobs={jobsByNode[selectedNode.nodeId] ?? []}
              jobsError={jobsErrorByNode[selectedNode.nodeId]}
              jobsLoading={Boolean(jobsLoadingByNode[selectedNode.nodeId])}
              node={selectedNode}
              effective={effectiveByNode[selectedNode.nodeId]}
              effectiveError={effectiveErrorByNode[selectedNode.nodeId]}
              operatorToken={operatorToken}
              onBack={() => setView('fleet')}
              onDeepProbe={() => createDeepProbe(selectedNode.nodeId)}
              onApplied={() => {
                void loadNodeJobs(selectedNode.nodeId, false)
                void loadEffectiveConfig(selectedNode.nodeId)
              }}
            />
          )}
          {view === 'node' && !selectedNode && (
            <EmptyState title="Node not found" body="Return to Fleet and select a registered node." />
          )}
          {view === 'activity' && (
            <ActivityView
              error={auditError}
              events={auditEvents}
              loading={auditLoading}
              onRefresh={loadAuditEvents}
            />
          )}
          {view === 'enrollment' && (
            <EnrollmentView operatorToken={operatorToken} />
          )}
        </main>
      </div>
    </div>
  )
}

interface SidebarProps {
  currentView: View
  groups: Array<{ name: string; count: number }>
  operatorToken: string
  theme: Theme
  onOperatorTokenChange: (value: string) => void
  onThemeToggle: () => void
  onViewChange: (view: View) => void
}

function Sidebar({
  currentView,
  groups,
  operatorToken,
  theme,
  onOperatorTokenChange,
  onThemeToggle,
  onViewChange,
}: SidebarProps) {
  return (
    <aside className="border-b border-[var(--sp-border)] bg-[var(--sp-surface)] md:flex md:h-screen md:w-60 md:flex-none md:flex-col md:border-b-0 md:border-r">
      <div className="flex items-center gap-3 border-b border-[var(--sp-border)] px-5 py-4">
        <div className="relative h-7 w-7 rounded-lg bg-[var(--sp-accent)] shadow-sm">
          <div className="absolute inset-x-[7px] inset-y-[6px] rounded-sm border-2 border-white/90" />
          <div className="absolute bottom-[5px] left-1/2 top-[5px] w-0.5 -translate-x-1/2 bg-white/90" />
        </div>
        <div>
          <div className="text-sm font-bold tracking-tight">Sideplane</div>
          <div className="text-[11px] text-[var(--sp-faint)]">control plane</div>
        </div>
      </div>

      <nav className="grid grid-cols-3 gap-1 px-3 py-3 md:flex md:flex-col">
        <NavButton active={currentView === 'fleet'} label="Fleet" onClick={() => onViewChange('fleet')} />
        <NavButton active={currentView === 'activity'} label="Activity" onClick={() => onViewChange('activity')} />
        <NavButton active={currentView === 'enrollment'} label="Enrollment" onClick={() => onViewChange('enrollment')} />
      </nav>

      <div className="hidden px-4 pt-2 md:block">
        <div className="px-1 pb-2 text-[10px] font-semibold uppercase tracking-[0.16em] text-[var(--sp-faint)]">
          Groups
        </div>
        <div className="space-y-1">
          {groups.map((group, index) => (
            <div key={group.name} className="flex items-center justify-between rounded-md px-2 py-1.5 text-xs text-[var(--sp-muted)] hover:bg-[var(--sp-surface-2)]">
              <span className="flex min-w-0 items-center gap-2">
                <span className={`h-1.5 w-1.5 flex-none rounded-sm ${index === 0 ? 'bg-[var(--sp-accent)]' : 'bg-[var(--sp-faint)]'}`} />
                <span className="truncate">{group.name}</span>
              </span>
              <span className="font-mono text-[var(--sp-faint)]">{group.count}</span>
            </div>
          ))}
        </div>
      </div>

      <div className="mt-auto grid gap-3 border-t border-[var(--sp-border)] p-4">
        <label className="grid gap-1.5 text-xs text-[var(--sp-muted)]">
          <span className="flex items-center gap-2">
            <span className={`h-2 w-2 rounded-full ${operatorToken.trim() ? 'bg-emerald-500' : 'bg-amber-500'}`} />
            Operator session
          </span>
          <input
            type="password"
            className="h-9 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)]"
            value={operatorToken}
            autoComplete="off"
            placeholder="operator token"
            onChange={(event) => onOperatorTokenChange(event.target.value)}
          />
        </label>
        <button
          type="button"
          className="flex h-9 items-center justify-between rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 text-xs font-medium text-[var(--sp-text)] hover:border-[var(--sp-border-strong)]"
          onClick={onThemeToggle}
        >
          <span>{theme === 'dark' ? 'Dark mode' : 'Light mode'}</span>
          <span className="font-mono text-[var(--sp-faint)]">{theme === 'dark' ? 'on' : 'off'}</span>
        </button>
      </div>
    </aside>
  )
}

function NavButton({ active, label, onClick }: { active: boolean; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      className={`rounded-lg px-3 py-2 text-left text-xs font-semibold transition md:text-[13px] ${active ? 'bg-[var(--sp-surface-2)] text-[var(--sp-text)]' : 'text-[var(--sp-muted)] hover:bg-[var(--sp-surface-2)] hover:text-[var(--sp-text)]'}`}
      onClick={onClick}
    >
      {label}
    </button>
  )
}

interface FleetOverviewProps {
  bannerText: string
  error: string | null
  fleetSubtitle: string
  jobsByNode: Record<string, Job[]>
  loading: boolean
  nodes: NodeStatus[]
  refreshing: boolean
  stats: { healthy: number; stale: number; offline: number; drift: number }
  onOpenNode: (nodeId: string) => void
  onRefresh: () => void
}

function FleetOverview({
  bannerText,
  error,
  fleetSubtitle,
  jobsByNode,
  loading,
  nodes,
  refreshing,
  stats,
  onOpenNode,
  onRefresh,
}: FleetOverviewProps) {
  return (
    <div className="mx-auto max-w-6xl px-4 py-6 sm:px-6 lg:px-9 lg:py-8">
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Fleet</h1>
          <div className="mt-1 text-sm text-[var(--sp-muted)]">{fleetSubtitle}</div>
        </div>
        <button
          type="button"
          className="inline-flex h-9 w-fit items-center gap-2 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
          disabled={refreshing}
          onClick={onRefresh}
        >
          <span className={refreshing ? 'animate-spin' : ''}>↻</span>
          {refreshing ? 'Refreshing' : 'Refresh'}
        </button>
      </div>

      <div className="mb-5 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <SummaryCard label="Healthy" value={stats.healthy} dotClass="bg-emerald-500" />
        <SummaryCard label="Config drift" value={stats.drift} dotClass="bg-amber-500" />
        <SummaryCard label="Stale" value={stats.stale} dotClass="bg-amber-500" />
        <SummaryCard label="Offline" value={stats.offline} dotClass="bg-rose-500" />
      </div>

      {bannerText && (
        <div className="mb-5 rounded-xl border border-amber-500/35 bg-amber-500/10 px-4 py-3 text-sm font-medium text-[var(--sp-text)]">
          {bannerText}
        </div>
      )}

      {error && (
        <div className="mb-5 rounded-xl border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-600">
          Failed to load nodes: {error}
        </div>
      )}

      <div className="overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-sm">
        <div className="hidden grid-cols-[2fr_1fr_1.4fr_1fr_1fr_2.5rem] gap-4 border-b border-[var(--sp-border)] px-5 py-3 text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)] lg:grid">
          <div>Node</div>
          <div>State</div>
          <div>Runtimes</div>
          <div>Config</div>
          <div>Heartbeat</div>
          <div />
        </div>

        {loading && <TableMessage message="Loading nodes…" />}
        {!loading && nodes.length === 0 && <TableMessage message="No nodes registered yet." />}
        {!loading && nodes.map((node) => (
          <FleetRow
            key={node.nodeId}
            activeProbe={hasActiveDeepProbe(jobsByNode[node.nodeId] ?? [])}
            node={node}
            onOpen={() => onOpenNode(node.nodeId)}
          />
        ))}
      </div>
    </div>
  )
}

function SummaryCard({ label, value, dotClass }: { label: string; value: number; dotClass: string }) {
  return (
    <div className="rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] px-4 py-4">
      <div className="flex items-center gap-2 text-xs font-medium text-[var(--sp-muted)]">
        <span className={`h-2 w-2 rounded-full ${dotClass}`} />
        {label}
      </div>
      <div className="mt-2 font-mono text-3xl font-bold tracking-tight">{value}</div>
    </div>
  )
}

function FleetRow({ activeProbe, node, onOpen }: { activeProbe: boolean; node: NodeStatus; onOpen: () => void }) {
  const configLabel = node.lastError ? 'Error' : node.configHash ? 'Observed' : 'Unknown'
  const configColor = node.lastError ? 'text-rose-600' : node.configHash ? 'text-emerald-600' : 'text-[var(--sp-muted)]'

  return (
    <button
      type="button"
      className="grid w-full gap-3 border-b border-[var(--sp-border)] px-5 py-4 text-left last:border-b-0 hover:bg-[var(--sp-surface-2)] lg:grid-cols-[2fr_1fr_1.4fr_1fr_1fr_2.5rem] lg:items-center lg:gap-4"
      onClick={onOpen}
    >
      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate font-mono text-sm font-semibold">{node.nodeId}</span>
          {activeProbe && <span className="rounded bg-sky-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-sky-600">probe</span>}
        </div>
        <div className="mt-1 truncate font-mono text-xs text-[var(--sp-faint)]">{node.hostname || '-'}</div>
      </div>

      <div>
        <span className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-semibold ${stateBadgeClasses(node.state)}`}>
          <span className="h-1.5 w-1.5 rounded-full bg-current" />
          {node.state}
        </span>
      </div>

      <div className="flex flex-wrap gap-1.5">
        {(node.runtimes ?? []).length > 0 ? node.runtimes?.map((runtime, index) => (
          <span key={runtimeKey(runtime, index)} className="inline-flex max-w-full items-center gap-1.5 rounded-md bg-[var(--sp-surface-3)] px-2 py-1 font-mono text-[11px] text-[var(--sp-muted)]">
            <span className="h-1.5 w-1.5 flex-none rounded-full bg-[var(--sp-accent)]" />
            <span className="truncate">{runtimeLabel(runtime)}</span>
          </span>
        )) : <span className="text-xs text-[var(--sp-faint)]">-</span>}
      </div>

      <div>
        <div className="flex items-center gap-1.5">
          <span className={`text-xs font-semibold ${configColor}`}>{configLabel}</span>
          {node.drift === true && (
            <span className="rounded bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-amber-600">drift</span>
          )}
        </div>
        <div className="mt-1 font-mono text-[11px] text-[var(--sp-faint)]">{compactHash(node.configHash)}</div>
      </div>

      <div className="text-xs text-[var(--sp-muted)]" title={formatDate(node.lastHeartbeatAt)}>
        {formatRelativeTime(node.lastHeartbeatAt)}
      </div>

      <div className="hidden justify-end text-[var(--sp-faint)] lg:flex">›</div>
    </button>
  )
}

function TableMessage({ message }: { message: string }) {
  return <div className="px-5 py-10 text-center text-sm text-[var(--sp-muted)]">{message}</div>
}

function NodeDetailView({
  creating,
  jobs,
  jobsError,
  jobsLoading,
  node,
  effective,
  effectiveError,
  operatorToken,
  onBack,
  onDeepProbe,
  onApplied,
}: {
  creating: boolean
  jobs: Job[]
  jobsError?: string
  jobsLoading: boolean
  node: NodeStatus
  effective?: EffectiveConfigResponse
  effectiveError?: string
  operatorToken: string
  onBack: () => void
  onDeepProbe: () => void
  onApplied: () => void
}) {
  const activeProbe = hasActiveDeepProbe(jobs)
  const activeConfigApply = hasActiveConfigApply(jobs)
  const snapshots = latestConfigSnapshots(jobs)
  const primarySnapshot = snapshots[0]
  const [wizardOpen, setWizardOpen] = useState(false)
  const canEditConfig = Boolean(primarySnapshot?.configPath)

  return (
    <div className="mx-auto max-w-5xl px-4 py-6 sm:px-6 lg:px-9 lg:py-8">
      <button type="button" className="mb-5 rounded-lg px-2 py-1 text-sm font-medium text-[var(--sp-muted)] hover:bg-[var(--sp-surface-2)] hover:text-[var(--sp-text)]" onClick={onBack}>
        ‹ Fleet
      </button>
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <div className="flex flex-wrap items-center gap-3">
            <h1 className="text-2xl font-bold tracking-tight">{node.nodeId}</h1>
            <span className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-semibold ${stateBadgeClasses(node.state)}`}>
              <span className="h-1.5 w-1.5 rounded-full bg-current" />
              {node.state}
            </span>
            {node.drift === true && (
              <span className="inline-flex items-center rounded-full border border-amber-500/30 bg-amber-500/10 px-2.5 py-1 text-xs font-semibold text-amber-600">
                config drift
              </span>
            )}
          </div>
          <div className="mt-2 font-mono text-sm text-[var(--sp-muted)]">{node.hostname || '-'} · sidecar {node.sidecarVersion || 'dev'}</div>
        </div>
        <div className="flex flex-wrap gap-2">
          <button
            type="button"
            className="h-9 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
            disabled={creating || activeProbe}
            onClick={onDeepProbe}
          >
            {creating ? 'Creating…' : activeProbe ? 'Probe active' : 'Deep probe'}
          </button>
          <DisabledAction label="Restart" title="Restart runs as part of a live config apply (enable --allow-live-apply on the sidecar)" />
          <DisabledAction label="Rollback" title="Rollback runs automatically when a live apply fails its health check" />
          <button
            type="button"
            className="h-9 rounded-lg bg-[var(--sp-accent)] px-3 text-sm font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-55"
            disabled={!canEditConfig}
            title={canEditConfig ? 'Open the change configuration wizard' : 'Run a deep probe first to discover the config path'}
            onClick={() => setWizardOpen(true)}
          >
            Edit config
          </button>
        </div>
      </div>

      <div className="mb-5 rounded-xl border border-sky-500/25 bg-sky-500/10 px-4 py-3 text-sm text-[var(--sp-muted)]">
        Edit config opens a signed plan wizard. It defaults to a dry run; a live apply (replace + restart + rollback) requires the sidecar to run with <span className="font-mono">--allow-live-apply</span>.
      </div>

      <div className="mb-6 grid gap-px overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-border)] sm:grid-cols-2 lg:grid-cols-4">
        <MetricCard label="Heartbeat" value={formatRelativeTime(node.lastHeartbeatAt)} title={formatDate(node.lastHeartbeatAt)} />
        <MetricCard label="Config hash" value={compactHash(node.configHash || primarySnapshot?.configHash)} monospace />
        <MetricCard label="Desired hash" value={compactHash(effective?.desiredHash)} monospace muted={!effective?.desiredHash} title={effective?.desiredHash} />
        <MetricCard label="Last error" value={node.lastError || 'none'} tone={node.lastError ? 'danger' : 'normal'} />
      </div>

      <ConfigDiffPanel effective={effective} error={effectiveError} />

      <section className="mb-6">
        <div className="mb-3 text-xs font-semibold uppercase tracking-[0.14em] text-[var(--sp-faint)]">Runtimes</div>
        <div className="grid gap-3">
          {(node.runtimes ?? []).length > 0 ? node.runtimes?.map((runtime, index) => (
            <RuntimeCard key={runtimeKey(runtime, index)} runtime={runtime} snapshot={snapshotForRuntime(runtime, snapshots)} />
          )) : (
            <div className="rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] px-4 py-6 text-sm text-[var(--sp-muted)]">
              No runtimes reported yet. Run a deep probe to refresh runtime discovery.
            </div>
          )}
        </div>
      </section>

      <section className="rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)]">
        <div className="flex items-center justify-between border-b border-[var(--sp-border)] px-4 py-3">
          <div className="text-sm font-semibold">Recent jobs</div>
          {jobsLoading && <div className="text-xs text-[var(--sp-muted)]">Loading jobs…</div>}
        </div>
        {jobsError && <div className="mt-4 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-600">Failed to load jobs: {jobsError}</div>}
        <div className="divide-y divide-[var(--sp-border)]">
          {!jobsLoading && jobs.length === 0 && <div className="px-4 py-4 text-xs text-[var(--sp-muted)]">No jobs yet.</div>}
          {jobs.slice(0, 6).map((job) => (
            <div key={job.id} className="grid gap-2 px-4 py-3 text-xs sm:grid-cols-[1fr_auto_auto] sm:items-center">
              <span className="font-mono text-[var(--sp-text)]">{job.type}</span>
              <span className={`inline-flex w-fit rounded border px-2 py-0.5 font-semibold ${jobBadgeClasses(job.status)}`}>{job.status}</span>
              <span className="text-[var(--sp-faint)]" title={formatDate(job.createdAt)}>{formatRelativeTime(job.createdAt)}</span>
            </div>
          ))}
        </div>
      </section>

      {wizardOpen && (
        <ConfigWizard
          nodeId={node.nodeId}
          runtimeType={effective?.runtimeType || 'hermes'}
          profile={effective?.profile || 'default'}
          operatorToken={operatorToken}
          effective={effective}
          activeConfigApply={activeConfigApply}
          onClose={() => setWizardOpen(false)}
          onApplied={onApplied}
        />
      )}
    </div>
  )
}

function ConfigDiffPanel({ effective, error }: { effective?: EffectiveConfigResponse; error?: string }) {
  return (
    <section className="mb-6 rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)]">
      <div className="flex flex-col gap-2 border-b border-[var(--sp-border)] px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <div className="text-sm font-semibold">Desired configuration</div>
          <div className="mt-1 text-xs text-[var(--sp-muted)]">Effective provider/model and read-only actual diff</div>
        </div>
        <div className="font-mono text-xs text-[var(--sp-faint)]">{effective?.runtimeType || 'hermes'}/{effective?.profile || 'default'}</div>
      </div>
      {error && <div className="border-b border-rose-500/30 bg-rose-500/10 px-4 py-3 text-xs text-rose-600">Failed to load desired diff: {error}</div>}
      <div className="grid gap-4 px-4 py-4 sm:grid-cols-3">
        <RuntimeField label="Desired provider" value={effective?.effective.provider || '-'} />
        <RuntimeField label="Desired model" value={effective?.effective.model || '-'} />
        <RuntimeField label="Desired hash" value={compactHash(effective?.desiredHash)} title={effective?.desiredHash} />
      </div>
      <div className="border-t border-[var(--sp-border)] px-4 py-4">
        {!effective ? (
          <div className="text-sm text-[var(--sp-muted)]">Desired diff not loaded yet.</div>
        ) : effective.diff.length === 0 ? (
          <div className="text-sm text-emerald-600">Actual provider/model matches desired effective config.</div>
        ) : (
          <div className="grid gap-2">
            {effective.diff.map((entry) => <DiffRow key={`${entry.field}-${entry.change}`} entry={entry} />)}
          </div>
        )}
      </div>
    </section>
  )
}

function DiffRow({ entry }: { entry: ConfigDiffEntry }) {
  return (
    <div className="grid gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs sm:grid-cols-[1fr_1fr_1fr_auto] sm:items-center">
      <span className="font-mono font-semibold text-[var(--sp-text)]">{entry.field}</span>
      <span className="font-mono text-[var(--sp-muted)]">actual: {entry.actual || '-'}</span>
      <span className="font-mono text-[var(--sp-muted)]">desired: {entry.desired || '-'}</span>
      <span className="font-semibold text-amber-700">{entry.change}</span>
    </div>
  )
}

function DisabledAction({ label, title, primary = false }: { label: string; title?: string; primary?: boolean }) {
  return (
    <button
      type="button"
      className={`h-9 cursor-not-allowed rounded-lg px-3 text-sm font-medium opacity-55 ${primary ? 'bg-[var(--sp-accent)] text-white' : 'border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] text-[var(--sp-text)]'}`}
      disabled
      title={title || 'Available after the apply path lands'}
    >
      {label}
    </button>
  )
}

function MetricCard({ label, value, title, monospace = false, muted = false, tone = 'normal' }: { label: string; value: string; title?: string; monospace?: boolean; muted?: boolean; tone?: 'normal' | 'danger' }) {
  return (
    <div className="bg-[var(--sp-surface)] px-4 py-4">
      <div className="text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">{label}</div>
      <div
        className={`mt-2 truncate text-sm font-medium ${monospace ? 'font-mono' : ''} ${muted ? 'text-[var(--sp-muted)]' : ''} ${tone === 'danger' ? 'text-rose-600' : ''}`}
        title={title || value}
      >
        {value}
      </div>
    </div>
  )
}

function RuntimeCard({ runtime, snapshot }: { runtime: RuntimeStatus; snapshot?: RuntimeConfigSnapshot }) {
  const warnings = [...(snapshot?.warnings ?? [])]
  if (runtime.lastError) warnings.unshift(runtime.lastError)

  return (
    <div className="overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)]">
      <div className="flex flex-col gap-3 border-b border-[var(--sp-border)] px-4 py-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-sm font-semibold">{runtime.name || runtime.type || 'runtime'}</span>
          {runtime.type && <span className="text-xs text-[var(--sp-faint)]">{runtime.type}</span>}
          {runtime.state && <span className={`inline-flex rounded border px-2 py-0.5 text-[11px] font-semibold ${runtime.state === 'error' ? 'border-rose-500/30 bg-rose-500/10 text-rose-600' : 'border-[var(--sp-border)] bg-[var(--sp-surface-2)] text-[var(--sp-muted)]'}`}>{runtime.state}</span>}
        </div>
        <span className="font-mono text-xs text-[var(--sp-faint)]">{runtime.version || '-'}</span>
      </div>
      <div className="grid gap-4 px-4 py-4 sm:grid-cols-3">
        <RuntimeField label="Provider" value={snapshot?.provider || runtime.provider || '-'} />
        <RuntimeField label="Model" value={snapshot?.model || runtime.model || '-'} />
        <RuntimeField label="Config hash" value={compactHash(snapshot?.configHash || runtime.configHash)} title={snapshot?.configHash || runtime.configHash} />
      </div>
      {snapshot && (
        <div className="border-t border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-4 py-4">
          <div className="grid gap-3 text-xs sm:grid-cols-2">
            <RuntimeField label="Snapshot source" value={snapshot.configPath || snapshot.source || '-'} />
            <RuntimeField label="Profile" value={snapshot.profile || '-'} />
          </div>
        </div>
      )}
      {warnings.length > 0 && (
        <div className="border-t border-amber-500/30 bg-amber-500/10 px-4 py-3 text-xs text-amber-700">
          {warnings.join('; ')}
        </div>
      )}
    </div>
  )
}

function RuntimeField({ label, value, title }: { label: string; value: string; title?: string }) {
  return (
    <div className="min-w-0">
      <div className="text-[11px] text-[var(--sp-faint)]">{label}</div>
      <div className="mt-1 truncate font-mono text-sm text-[var(--sp-text)]" title={title || value}>{value}</div>
    </div>
  )
}

function snapshotForRuntime(runtime: RuntimeStatus, snapshots: RuntimeConfigSnapshot[]): RuntimeConfigSnapshot | undefined {
  return snapshots.find((snapshot) => {
    if (runtime.type && snapshot.runtimeType === runtime.type) return true
    return runtime.name && snapshot.runtimeName === runtime.name
  })
}

function ActivityView({ error, events, loading, onRefresh }: { error: string | null; events: AuditEvent[]; loading: boolean; onRefresh: () => void }) {
  return (
    <div className="mx-auto max-w-5xl px-4 py-6 sm:px-6 lg:px-9 lg:py-8">
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Activity</h1>
          <div className="mt-1 text-sm text-[var(--sp-muted)]">Recent audit events across the fleet</div>
        </div>
        <button
          type="button"
          className="h-9 w-fit rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)]"
          onClick={onRefresh}
        >
          Refresh
        </button>
      </div>

      {error && (
        <div className="mb-5 rounded-xl border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-600">
          Failed to load activity: {error}
        </div>
      )}

      <div className="overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-sm">
        <div className="hidden grid-cols-[1fr_1fr_1.4fr_1.4fr] gap-4 border-b border-[var(--sp-border)] px-5 py-3 text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)] sm:grid">
          <div>Time</div>
          <div>Actor</div>
          <div>Action</div>
          <div>Target</div>
        </div>
        {loading && <TableMessage message="Loading audit events…" />}
        {!loading && events.length === 0 && <TableMessage message="No audit events yet." />}
        {!loading && events.map((event) => (
          <div key={event.id} className="grid gap-2 border-b border-[var(--sp-border)] px-5 py-4 text-sm last:border-b-0 sm:grid-cols-[1fr_1fr_1.4fr_1.4fr] sm:items-center sm:gap-4">
            <div className="text-xs text-[var(--sp-faint)]" title={formatDate(event.createdAt)}>{formatRelativeTime(event.createdAt)}</div>
            <div className="font-mono text-xs text-[var(--sp-muted)]">{event.actor}</div>
            <div className="font-mono text-xs font-semibold text-[var(--sp-text)]">{event.action}</div>
            <div className="min-w-0">
              <div className="truncate font-mono text-xs text-[var(--sp-muted)]">{event.targetNode || '-'}</div>
              {event.detail && <div className="mt-1 truncate text-xs text-[var(--sp-faint)]">{event.detail}</div>}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

function EnrollmentView({ operatorToken }: { operatorToken: string }) {
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [created, setCreated] = useState<CreateEnrollmentTokenResponse | null>(null)
  const [copied, setCopied] = useState<'token' | 'command' | null>(null)
  const serverURL = window.location.origin
  const enrollCommand = created
    ? `sideplane-sidecar enroll --server ${serverURL} --token ${created.token}`
    : `sideplane-sidecar enroll --server ${serverURL} --token <token>`
  const tokenReady = operatorToken.trim().length > 0

  const createToken = async () => {
    if (!tokenReady || creating) return
    setCreating(true)
    setError(null)
    setCopied(null)
    try {
      const res = await fetch('/api/enrollment-tokens', {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${operatorToken.trim()}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({}),
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        throw new Error(`HTTP ${res.status}: ${res.statusText}`)
      }
      const data: CreateEnrollmentTokenResponse = await res.json()
      setCreated(data)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      setCreating(false)
    }
  }

  const copyText = async (kind: 'token' | 'command', value: string) => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(kind)
      window.setTimeout(() => setCopied((current) => (current === kind ? null : current)), 1800)
    } catch {
      setError('Clipboard copy failed')
    }
  }

  return (
    <div className="mx-auto max-w-5xl px-4 py-6 sm:px-6 lg:px-9 lg:py-8">
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Enrollment</h1>
          <div className="mt-1 text-sm text-[var(--sp-muted)]">Issue one-time sidecar enrollment tokens</div>
        </div>
        <button
          type="button"
          className="h-9 w-fit rounded-lg bg-[var(--sp-accent)] px-3 text-sm font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-55"
          disabled={!tokenReady || creating}
          onClick={createToken}
        >
          {creating ? 'Creating...' : 'Create token'}
        </button>
      </div>

      {!tokenReady && (
        <div className="mb-5 rounded-xl border border-amber-500/35 bg-amber-500/10 px-4 py-3 text-sm text-amber-700">
          Set an operator token in the sidebar before creating enrollment tokens.
        </div>
      )}

      {error && (
        <div className="mb-5 rounded-xl border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-600">
          {error}
        </div>
      )}

      <section className="overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-sm">
        <div className="border-b border-[var(--sp-border)] px-4 py-3">
          <div className="text-sm font-semibold">Enrollment token</div>
          <div className="mt-1 text-xs text-[var(--sp-muted)]">Tokens are one-time values and are shown only once.</div>
        </div>

        <div className="grid gap-4 px-4 py-4">
          <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-700">
            Copy the token now. Sideplane cannot show this token again after you leave this view or create another token.
          </div>

          <div className="grid gap-1.5">
            <div className="text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">Token</div>
            <div className="flex flex-col gap-2 sm:flex-row">
              <input
                readOnly
                className="h-10 min-w-0 flex-1 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none"
                value={created?.token ?? ''}
                placeholder="Create a token to reveal it once"
              />
              <button
                type="button"
                className="h-10 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-55"
                disabled={!created}
                onClick={() => created && copyText('token', created.token)}
              >
                {copied === 'token' ? 'Copied' : 'Copy'}
              </button>
            </div>
          </div>

          <div className="grid gap-1.5">
            <div className="text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">Expires</div>
            <div className="font-mono text-sm text-[var(--sp-muted)]">{formatDate(created?.expiresAt)}</div>
          </div>

          <div className="grid gap-1.5">
            <div className="text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">Sidecar command</div>
            <div className="flex flex-col gap-2 sm:flex-row">
              <input
                readOnly
                className="h-10 min-w-0 flex-1 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none"
                value={enrollCommand}
              />
              <button
                type="button"
                className="h-10 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-55"
                disabled={!created}
                onClick={() => copyText('command', enrollCommand)}
              >
                {copied === 'command' ? 'Copied' : 'Copy'}
              </button>
            </div>
          </div>
        </div>
      </section>
    </div>
  )
}

function EmptyState({ title, body }: { title: string; body: string }) {
  return (
    <div className="mx-auto max-w-3xl px-4 py-12 text-center sm:px-6 lg:px-9">
      <h1 className="text-xl font-semibold">{title}</h1>
      <p className="mt-2 text-sm text-[var(--sp-muted)]">{body}</p>
    </div>
  )
}
