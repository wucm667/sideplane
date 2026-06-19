import { useMemo, useState } from 'react'
import { compactHash, formatDate, formatRelativeTime, hasActiveDeepProbe, runtimeKey, runtimeLabel, stateBadgeClasses } from '../helpers.ts'
import type { Job, NodeStatus } from '../types.ts'

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
  refreshing: boolean
  selector: string
  stats: { healthy: number; stale: number; offline: number; drift: number }
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
  refreshing,
  selector,
  stats,
  onOpenNode,
  onRefresh,
  onSelectorChange,
}: FleetOverviewProps) {
  const [sort, setSort] = useState<{ key: SortKey; direction: SortDirection }>({ key: 'state', direction: 'asc' })
  const [searchQuery, setSearchQuery] = useState('')
  const filteredNodes = useMemo(() => filterNodes(nodes, searchQuery), [nodes, searchQuery])
  const sortedNodes = useMemo(() => sortNodes(filteredNodes, sort.key, sort.direction), [filteredNodes, sort])

  const toggleSort = (key: SortKey) => {
    setSort((current) => ({
      key,
      direction: current.key === key && current.direction === 'asc' ? 'desc' : 'asc',
    }))
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

      <div className="overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-sm">
        <div className="hidden grid-cols-[2fr_1fr_1.4fr_1fr_1fr_2.5rem] gap-4 border-b border-[var(--sp-border)] px-5 py-3 text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)] lg:grid">
          <SortHeader active={sort.key === 'node'} direction={sort.direction} label="Node" onClick={() => toggleSort('node')} />
          <SortHeader active={sort.key === 'state'} direction={sort.direction} label="State" onClick={() => toggleSort('state')} />
          <div>Runtimes</div>
          <div>Config</div>
          <SortHeader active={sort.key === 'heartbeat'} direction={sort.direction} label="Heartbeat" onClick={() => toggleSort('heartbeat')} />
          <div />
        </div>

        {loading && <TableMessage message="Loading nodes…" />}
        {!loading && nodes.length === 0 && <TableMessage message="No nodes registered yet." />}
        {!loading && nodes.length > 0 && sortedNodes.length === 0 && <TableMessage message="No nodes match the current filter." />}
        {!loading && sortedNodes.map((node) => (
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
  )
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
