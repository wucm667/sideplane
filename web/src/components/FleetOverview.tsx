import { compactHash, formatDate, formatRelativeTime, hasActiveDeepProbe, runtimeKey, runtimeLabel, stateBadgeClasses } from '../helpers.ts'
import type { Job, NodeStatus } from '../types.ts'

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

export function FleetOverview({
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

export function TableMessage({ message }: { message: string }) {
  return <div className="px-5 py-10 text-center text-sm text-[var(--sp-muted)]">{message}</div>
}
