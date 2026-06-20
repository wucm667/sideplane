import { useMemo, useState } from 'react'
import { apiErrorMessage, apiURL, compactHash, fleetOverviewMetrics, formatDate, formatRelativeTime, hasActiveDeepProbe, runtimeKey, runtimeLabel, stateBadgeClasses } from '../helpers.ts'
import type { FleetOverviewMetrics } from '../helpers.ts'
import type { BulkJobResponse, BulkNodeLabelsResponse, Job, NodeStatus, Rollout } from '../types.ts'

type SortKey = 'node' | 'state' | 'heartbeat'
type SortDirection = 'asc' | 'desc'

const STATE_SORT_ORDER = {
  offline: 0,
  stale: 1,
  fresh: 2,
}

interface FleetOverviewProps {
  bannerText: string
  error: string | null
  fleetSubtitle: string
  jobsByNode: Record<string, Job[]>
  loading: boolean
  nodes: NodeStatus[]
  operatorToken: string
  refreshing: boolean
  rollouts: Rollout[]
  selector: string
  onOpenNode: (nodeId: string) => void
  onRefresh: () => void
  onSelectorChange: (selector: string) => void
}

export function FleetOverview({
  bannerText,
  error,
  fleetSubtitle,
  jobsByNode,
  loading,
  nodes,
  operatorToken,
  refreshing,
  rollouts,
  selector,
  onOpenNode,
  onRefresh,
  onSelectorChange,
}: FleetOverviewProps) {
  const [sort, setSort] = useState<{ key: SortKey; direction: SortDirection }>({ key: 'state', direction: 'asc' })
  const [searchQuery, setSearchQuery] = useState('')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [labelInput, setLabelInput] = useState('')
  const [bulkBusy, setBulkBusy] = useState(false)
  const [bulkMessage, setBulkMessage] = useState<string | null>(null)
  const [bulkError, setBulkError] = useState<string | null>(null)
  const filteredNodes = useMemo(() => filterNodes(nodes, searchQuery), [nodes, searchQuery])
  const sortedNodes = useMemo(() => sortNodes(filteredNodes, sort.key, sort.direction), [filteredNodes, sort])
  const metrics = useMemo(() => fleetOverviewMetrics(nodes, jobsByNode, rollouts), [jobsByNode, nodes, rollouts])

  const selectedIds = useMemo(() => sortedNodes.map((node) => node.nodeId).filter((id) => selected.has(id)), [sortedNodes, selected])
  const allVisibleSelected = sortedNodes.length > 0 && selectedIds.length === sortedNodes.length

  const toggleSort = (key: SortKey) => {
    setSort((current) => ({
      key,
      direction: current.key === key && current.direction === 'asc' ? 'desc' : 'asc',
    }))
  }

  const toggleNodeSelected = (nodeId: string) => {
    setSelected((current) => {
      const next = new Set(current)
      if (next.has(nodeId)) {
        next.delete(nodeId)
      } else {
        next.add(nodeId)
      }
      return next
    })
  }

  const toggleSelectAllVisible = () => {
    setSelected((current) => {
      if (sortedNodes.length > 0 && sortedNodes.every((node) => current.has(node.nodeId))) {
        const next = new Set(current)
        sortedNodes.forEach((node) => next.delete(node.nodeId))
        return next
      }
      const next = new Set(current)
      sortedNodes.forEach((node) => next.add(node.nodeId))
      return next
    })
  }

  const authHeaders = (): HeadersInit => {
    const headers: HeadersInit = { 'Content-Type': 'application/json' }
    const token = operatorToken.trim()
    if (token) {
      headers.Authorization = `Bearer ${token}`
    }
    return headers
  }

  const bulkProbe = async () => {
    if (selectedIds.length === 0 || bulkBusy) return
    setBulkBusy(true)
    setBulkError(null)
    setBulkMessage(null)
    try {
      const res = await fetch(apiURL('/api/jobs/bulk'), {
        method: 'POST',
        headers: authHeaders(),
        body: JSON.stringify({ nodeIds: selectedIds, type: 'deep_probe' }),
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        if (res.status === 403) throw new Error('Operator token is read-only')
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as BulkJobResponse
      setBulkMessage(`Probed ${data.created} of ${selectedIds.length} node(s).`)
      setSelected(new Set())
      onRefresh()
    } catch (e) {
      setBulkError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      setBulkBusy(false)
    }
  }

  const bulkLabel = async () => {
    if (selectedIds.length === 0 || bulkBusy) return
    const labels = parseLabelAssignments(labelInput)
    if (typeof labels === 'string') {
      setBulkError(labels)
      return
    }
    setBulkBusy(true)
    setBulkError(null)
    setBulkMessage(null)
    try {
      const res = await fetch(apiURL('/api/nodes/labels'), {
        method: 'PUT',
        headers: authHeaders(),
        body: JSON.stringify({ nodeIds: selectedIds, labels }),
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        if (res.status === 403) throw new Error('Operator token is read-only')
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as BulkNodeLabelsResponse
      setBulkMessage(`Applied ${Object.keys(labels).length} label(s) to ${data.updated} node(s).`)
      setLabelInput('')
      setSelected(new Set())
      onRefresh()
    } catch (e) {
      setBulkError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      setBulkBusy(false)
    }
  }

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

      <FleetMetricsPanel metrics={metrics} />

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

      <div className="mb-3 grid gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <input
          className="h-9 w-full rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)] sm:max-w-sm"
          value={searchQuery}
          placeholder="Filter nodes..."
          onChange={(event) => setSearchQuery(event.target.value)}
        />
        <input
          className="h-9 w-full rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)] sm:max-w-sm"
          value={selector}
          placeholder="Selector role=canary,zone=lab"
          onChange={(event) => onSelectorChange(event.target.value)}
        />
      </div>

      {selectedIds.length > 0 && (
        <div className="mb-3 flex flex-col gap-2 rounded-xl border border-[var(--sp-accent)]/40 bg-[var(--sp-surface-2)] px-4 py-3 sm:flex-row sm:items-center sm:gap-3">
          <span className="text-sm font-medium">{selectedIds.length} selected</span>
          <button
            type="button"
            className="h-9 rounded-lg bg-[var(--sp-accent)] px-3 text-sm font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-55"
            disabled={bulkBusy}
            onClick={bulkProbe}
          >
            Probe selected
          </button>
          <input
            className="h-9 min-w-0 flex-1 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)] sm:max-w-xs"
            value={labelInput}
            placeholder="role=canary,zone=lab"
            onChange={(event) => setLabelInput(event.target.value)}
          />
          <button
            type="button"
            className="h-9 rounded-lg border border-[var(--sp-border-strong)] px-3 text-sm font-medium hover:bg-[var(--sp-surface)] disabled:cursor-not-allowed disabled:opacity-55"
            disabled={bulkBusy || labelInput.trim() === ''}
            onClick={bulkLabel}
          >
            Label selected
          </button>
          <button
            type="button"
            className="h-9 rounded-lg px-2 text-sm text-[var(--sp-muted)] hover:text-[var(--sp-text)]"
            onClick={() => setSelected(new Set())}
          >
            Clear
          </button>
        </div>
      )}
      {bulkMessage && <div className="mb-3 rounded-lg border border-emerald-500/25 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-600">{bulkMessage}</div>}
      {bulkError && <div className="mb-3 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-sm text-rose-600">{bulkError}</div>}

      <div className="overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-sm">
        <div className="hidden border-b border-[var(--sp-border)] px-5 py-3 lg:flex lg:items-center lg:gap-4">
          <label className="flex w-6 items-center justify-center">
            <input type="checkbox" aria-label="select all nodes" checked={allVisibleSelected} onChange={toggleSelectAllVisible} />
          </label>
          <div className="grid flex-1 grid-cols-[2fr_1fr_1.4fr_1fr_1fr_2.5rem] gap-4 text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">
            <SortHeader active={sort.key === 'node'} direction={sort.direction} label="Node" onClick={() => toggleSort('node')} />
            <SortHeader active={sort.key === 'state'} direction={sort.direction} label="State" onClick={() => toggleSort('state')} />
            <div>Runtimes</div>
            <div>Config</div>
            <SortHeader active={sort.key === 'heartbeat'} direction={sort.direction} label="Heartbeat" onClick={() => toggleSort('heartbeat')} />
            <div />
          </div>
        </div>

        {loading && <TableMessage message="Loading nodes…" />}
        {!loading && nodes.length === 0 && <TableMessage message="No nodes registered yet." />}
        {!loading && nodes.length > 0 && sortedNodes.length === 0 && <TableMessage message="No nodes match the current filter." />}
        {!loading && sortedNodes.map((node) => (
          <FleetRow
            key={node.nodeId}
            activeProbe={hasActiveDeepProbe(jobsByNode[node.nodeId] ?? [])}
            node={node}
            selected={selected.has(node.nodeId)}
            onToggleSelect={() => toggleNodeSelected(node.nodeId)}
            onOpen={() => onOpenNode(node.nodeId)}
          />
        ))}
      </div>
    </div>
  )
}

function filterNodes(nodes: NodeStatus[], query: string): NodeStatus[] {
  const normalized = query.trim().toLowerCase()
  if (!normalized) return nodes
  return nodes.filter((node) => nodeSearchText(node).includes(normalized))
}

function nodeSearchText(node: NodeStatus): string {
  const runtimeText = (node.runtimes ?? [])
    .flatMap((runtime) => [
      runtime.name,
      runtime.type,
      runtime.state,
      runtime.provider,
      runtime.model,
    ])
    .filter(Boolean)
    .join(' ')
  return [
    node.nodeId,
    node.hostname,
    node.state,
    node.maintenance ? 'maintenance' : '',
    labelsText(node.labels),
    runtimeText,
  ].filter(Boolean).join(' ').toLowerCase()
}

function sortNodes(nodes: NodeStatus[], key: SortKey, direction: SortDirection): NodeStatus[] {
  const multiplier = direction === 'asc' ? 1 : -1
  return [...nodes].sort((a, b) => {
    const primary = compareNodeSortValue(a, b, key)
    if (primary !== 0) return primary * multiplier
    return a.nodeId.localeCompare(b.nodeId)
  })
}

function compareNodeSortValue(a: NodeStatus, b: NodeStatus, key: SortKey): number {
  if (key === 'node') return a.nodeId.localeCompare(b.nodeId)
  if (key === 'state') return STATE_SORT_ORDER[a.state] - STATE_SORT_ORDER[b.state]
  return heartbeatTime(a) - heartbeatTime(b)
}

function heartbeatTime(node: NodeStatus): number {
  const value = Date.parse(node.lastHeartbeatAt)
  return Number.isNaN(value) ? 0 : value
}

function SortHeader({ active, direction, label, onClick }: { active: boolean; direction: SortDirection; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      className="flex items-center gap-1 text-left uppercase text-[var(--sp-faint)] hover:text-[var(--sp-text)]"
      onClick={onClick}
    >
      <span>{label}</span>
      {active && <span className="font-mono text-[10px]">{direction === 'asc' ? '▲' : '▼'}</span>}
    </button>
  )
}

function FleetMetricsPanel({ metrics }: { metrics: FleetOverviewMetrics }) {
  const rolloutDetail = metrics.activeRollouts > 0
    ? `${metrics.runningRollouts} running · ${metrics.pausedRollouts} paused`
    : 'none active'

  return (
    <section className="mb-5 overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-sm">
      <div className="grid divide-y divide-[var(--sp-border)] sm:grid-cols-2 sm:divide-x sm:divide-y-0 xl:grid-cols-4">
        <MetricCell
          accentClass="bg-emerald-500"
          detail={`${metrics.freshNodes} fresh · ${metrics.staleNodes} stale · ${metrics.offlineNodes} offline · ${metrics.maintenanceNodes} maint`}
          label="Fleet nodes"
          value={metrics.totalNodes}
        />
        <MetricCell
          accentClass={metrics.driftedNodes > 0 || metrics.outdatedSidecars > 0 ? 'bg-amber-500' : 'bg-emerald-500'}
          detail={`${metrics.outdatedSidecars} sidecars outdated · ${metrics.runtimeCount} runtimes`}
          label="Config drift"
          value={metrics.driftedNodes}
        />
        <MetricCell
          accentClass={metrics.activeJobs > 0 ? 'bg-sky-500' : 'bg-[var(--sp-faint)]'}
          detail="pending or claimed jobs"
          label="Active jobs"
          value={metrics.activeJobs}
        />
        <MetricCell
          accentClass={metrics.activeRollouts > 0 ? 'bg-sky-500' : 'bg-[var(--sp-faint)]'}
          detail={rolloutDetail}
          label="Rollout activity"
          value={metrics.activeRollouts}
        />
      </div>
    </section>
  )
}

function MetricCell({
  accentClass,
  detail,
  label,
  value,
}: {
  accentClass: string
  detail: string
  label: string
  value: number
}) {
  return (
    <div className="min-h-[92px] px-4 py-4">
      <div className="flex items-center gap-2 text-xs font-medium text-[var(--sp-muted)]">
        <span className={`h-2 w-2 rounded-full ${accentClass}`} />
        <span>{label}</span>
      </div>
      <div className="mt-2 font-mono text-3xl font-bold tracking-tight">{value}</div>
      <div className="mt-1 truncate text-xs text-[var(--sp-faint)]" title={detail}>{detail}</div>
    </div>
  )
}

function FleetRow({ activeProbe, node, selected, onToggleSelect, onOpen }: { activeProbe: boolean; node: NodeStatus; selected: boolean; onToggleSelect: () => void; onOpen: () => void }) {
  const configLabel = node.lastError ? 'Error' : node.configHash ? 'Observed' : 'Unknown'
  const configColor = node.lastError ? 'text-rose-600' : node.configHash ? 'text-emerald-600' : 'text-[var(--sp-muted)]'

  return (
    <div className="flex items-start border-b border-[var(--sp-border)] last:border-b-0 hover:bg-[var(--sp-surface-2)]">
      <label className="flex items-center self-stretch px-5 py-4 lg:px-5" onClick={(event) => event.stopPropagation()}>
        <input
          type="checkbox"
          aria-label={`select ${node.nodeId}`}
          checked={selected}
          onChange={onToggleSelect}
        />
      </label>
      <button
        type="button"
        className="grid min-w-0 flex-1 gap-3 py-4 pr-5 text-left lg:grid-cols-[2fr_1fr_1.4fr_1fr_1fr_2.5rem] lg:items-center lg:gap-4"
        onClick={onOpen}
      >
      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate font-mono text-sm font-semibold">{node.nodeId}</span>
          {activeProbe && <span className="rounded bg-sky-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-sky-600">probe</span>}
          {node.maintenance && <span className="rounded bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-amber-700">maint</span>}
          {node.sidecarOutdated && <span className="rounded bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-amber-600" title="sidecar version differs from expected">outdated</span>}
        </div>
        <div className="mt-1 truncate font-mono text-xs text-[var(--sp-faint)]">{node.hostname || '-'}</div>
        {Object.keys(node.labels ?? {}).length > 0 && (
          <div className="mt-2 flex flex-wrap gap-1">
            {labelPairs(node.labels).slice(0, 3).map(([key, value]) => (
              <span key={key} className="rounded border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-1.5 py-0.5 font-mono text-[10px] text-[var(--sp-muted)]">
                {key}={value}
              </span>
            ))}
          </div>
        )}
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
    </div>
  )
}

// parseLabelAssignments parses "key=value,key2=value2" into a label record, or
// returns an error string when an entry is malformed.
function parseLabelAssignments(raw: string): Record<string, string> | string {
  const labels: Record<string, string> = {}
  for (const part of raw.split(',')) {
    const trimmed = part.trim()
    if (trimmed === '') continue
    const index = trimmed.indexOf('=')
    if (index <= 0) {
      return `invalid label "${trimmed}", want key=value`
    }
    labels[trimmed.slice(0, index).trim()] = trimmed.slice(index + 1).trim()
  }
  if (Object.keys(labels).length === 0) {
    return 'provide at least one key=value label'
  }
  return labels
}

function labelPairs(labels: NodeStatus['labels']): [string, string][] {
  return Object.entries(labels ?? {}).sort(([a], [b]) => a.localeCompare(b))
}

function labelsText(labels: NodeStatus['labels']): string {
  return labelPairs(labels).map(([key, value]) => `${key} ${value}`).join(' ')
}

export function TableMessage({ message }: { message: string }) {
  return <div className="px-5 py-10 text-center text-sm text-[var(--sp-muted)]">{message}</div>
}
