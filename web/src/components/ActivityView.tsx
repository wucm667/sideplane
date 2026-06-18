import { formatDate, formatRelativeTime } from '../helpers.ts'
import type { AuditEvent } from '../types.ts'
import { TableMessage } from './FleetOverview.tsx'

export function ActivityView({ error, events, loading, onRefresh }: { error: string | null; events: AuditEvent[]; loading: boolean; onRefresh: () => void }) {
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
