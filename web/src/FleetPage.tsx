import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { Job, JobStatus, NodeState, NodeStatus, RuntimeStatus } from './types.ts'

const NODE_REFRESH_MS = 10_000
const ACTIVE_JOB_REFRESH_MS = 2_000
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

function runtimeKey(runtime: RuntimeStatus, index: number): string {
  return `${runtime.name || runtime.type || 'runtime'}-${index}`
}

function runtimeLabel(runtime: RuntimeStatus): string {
  if (runtime.provider && runtime.model) return `${runtime.provider}/${runtime.model}`
  if (runtime.model) return runtime.model
  return runtime.name || runtime.type || 'runtime'
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
      const res = await fetch(`/api/nodes/${encodeURIComponent(nodeId)}/jobs`)
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
  }, [])

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

  useEffect(() => {
    refreshFleet(false)
  }, [refreshFleet])

  useEffect(() => {
    const interval = window.setInterval(() => {
      refreshFleet(false)
    }, NODE_REFRESH_MS)
    return () => window.clearInterval(interval)
  }, [refreshFleet])

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

  const safeNodes = nodes ?? []
  const stats = useMemo(() => {
    const healthy = safeNodes.filter((node) => node.state === 'fresh').length
    const stale = safeNodes.filter((node) => node.state === 'stale').length
    const offline = safeNodes.filter((node) => node.state === 'offline').length
    const drift = 0
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
            <NodePlaceholder
              creating={Boolean(creatingByNode[selectedNode.nodeId])}
              jobs={jobsByNode[selectedNode.nodeId] ?? []}
              jobsError={jobsErrorByNode[selectedNode.nodeId]}
              jobsLoading={Boolean(jobsLoadingByNode[selectedNode.nodeId])}
              node={selectedNode}
              onBack={() => setView('fleet')}
              onDeepProbe={() => createDeepProbe(selectedNode.nodeId)}
            />
          )}
          {view === 'node' && !selectedNode && (
            <EmptyState title="Node not found" body="Return to Fleet and select a registered node." />
          )}
          {view === 'activity' && (
            <PlaceholderPage
              title="Activity"
              body="Audit-backed activity lands in Phase 3. Fleet collection continues to refresh in the background."
            />
          )}
          {view === 'enrollment' && (
            <PlaceholderPage
              title="Enrollment"
              body="Create a one-time enrollment token with the CLI, then run the sidecar on a node. UI token issuance lands after the audit/config work."
            />
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
        <div className={`text-xs font-semibold ${configColor}`}>{configLabel}</div>
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

function NodePlaceholder({
  creating,
  jobs,
  jobsError,
  jobsLoading,
  node,
  onBack,
  onDeepProbe,
}: {
  creating: boolean
  jobs: Job[]
  jobsError?: string
  jobsLoading: boolean
  node: NodeStatus
  onBack: () => void
  onDeepProbe: () => void
}) {
  const activeProbe = hasActiveDeepProbe(jobs)
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
          </div>
          <div className="mt-2 font-mono text-sm text-[var(--sp-muted)]">{node.hostname || '-'} · sidecar {node.sidecarVersion || 'dev'}</div>
        </div>
        <button
          type="button"
          className="h-9 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
          disabled={creating || activeProbe}
          onClick={onDeepProbe}
        >
          {creating ? 'Creating…' : activeProbe ? 'Probe active' : 'Deep probe'}
        </button>
      </div>

      <div className="rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] p-5">
        <div className="text-sm font-semibold">Node detail placeholder</div>
        <div className="mt-2 text-sm text-[var(--sp-muted)]">
          The full node detail view lands in the next task. Deep probe remains available here so existing read-only operations continue working.
        </div>
        {jobsError && <div className="mt-4 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-600">Failed to load jobs: {jobsError}</div>}
        <div className="mt-5 divide-y divide-[var(--sp-border)] rounded-lg border border-[var(--sp-border)]">
          {jobsLoading && <div className="px-3 py-3 text-xs text-[var(--sp-muted)]">Loading jobs…</div>}
          {!jobsLoading && jobs.length === 0 && <div className="px-3 py-3 text-xs text-[var(--sp-muted)]">No jobs yet.</div>}
          {jobs.slice(0, 4).map((job) => (
            <div key={job.id} className="grid gap-2 px-3 py-3 text-xs sm:grid-cols-[1fr_auto_auto] sm:items-center">
              <span className="font-mono text-[var(--sp-text)]">{job.type}</span>
              <span className={`inline-flex w-fit rounded border px-2 py-0.5 font-semibold ${jobBadgeClasses(job.status)}`}>{job.status}</span>
              <span className="text-[var(--sp-faint)]">{formatRelativeTime(job.createdAt)}</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

function PlaceholderPage({ title, body }: { title: string; body: string }) {
  return (
    <div className="mx-auto max-w-4xl px-4 py-6 sm:px-6 lg:px-9 lg:py-8">
      <h1 className="text-2xl font-bold tracking-tight">{title}</h1>
      <div className="mt-5 rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] p-6 text-sm text-[var(--sp-muted)]">
        {body}
      </div>
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
