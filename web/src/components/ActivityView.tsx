import { formatDate } from '../helpers.ts'
import { formatRelativeTimeLabel, useT } from '../i18n.ts'
import type { AuditAction, AuditEvent, AuditFilters } from '../types.ts'
import { TableMessage } from './FleetOverview.tsx'

const AUDIT_ACTIONS: AuditAction[] = [
  'enrollment.token.create',
  'node.enroll',
  'node.delete',
  'node.labels.update',
  'job.create',
  'job.complete',
  'job.fail',
  'config.apply',
  'restart',
  'rollback',
  'rollout.create',
  'rollout.pause',
  'rollout.resume',
  'rollout.abort',
  'config.desired.update',
]

export function ActivityView({
  error,
  events,
  filters,
  limit,
  loading,
  onFiltersChange,
  onLimitChange,
  onRefresh,
}: {
  error: string | null
  events: AuditEvent[]
  filters: AuditFilters
  limit: number
  loading: boolean
  onFiltersChange: (filters: AuditFilters) => void
  onLimitChange: (limit: number) => void
  onRefresh: () => void
}) {
  const { t } = useT()
  const hasFilters = filters.nodeId.trim() !== '' || filters.action !== ''

  return (
    <div className="mx-auto max-w-5xl px-4 py-6 sm:px-6 lg:px-9 lg:py-8">
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t('activity.title')}</h1>
          <div className="mt-1 text-sm text-[var(--sp-muted)]">{t('activity.subtitle')}</div>
        </div>
        <button
          type="button"
          className="h-9 w-fit rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)]"
          onClick={onRefresh}
        >
          {t('common.refresh')}
        </button>
      </div>

      <div className="mb-5 grid gap-2 rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] p-3 sm:grid-cols-[1fr_1fr_auto_auto]">
        <input
          className="h-9 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)]"
          value={filters.nodeId}
          aria-label={t('activity.filterNode')}
          placeholder={t('activity.filterNode')}
          onChange={(event) => onFiltersChange({ ...filters, nodeId: event.target.value })}
        />
        <select
          className="h-9 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)]"
          value={filters.action}
          aria-label={t('activity.filterAction')}
          onChange={(event) => onFiltersChange({ ...filters, action: event.target.value as AuditAction | '' })}
        >
          <option value="">{t('activity.actions.all')}</option>
          {AUDIT_ACTIONS.map((action) => (
            <option key={action} value={action}>{action}</option>
          ))}
        </select>
        <select
          className="h-9 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)]"
          value={limit}
          aria-label={t('activity.limitLabel')}
          onChange={(event) => onLimitChange(Number(event.target.value))}
        >
          <option value={50}>{t('activity.events.50')}</option>
          <option value={100}>{t('activity.events.100')}</option>
          <option value={250}>{t('activity.events.250')}</option>
        </select>
        {hasFilters && (
          <button
            type="button"
            className="h-9 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)]"
            onClick={() => onFiltersChange({ nodeId: '', action: '' })}
          >
            {t('activity.clearFilters')}
          </button>
        )}
      </div>

      {error && (
        <div role="alert" className="mb-5 rounded-xl border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-600">
          {t('activity.failedLoad', { error })}
        </div>
      )}

      <div role="region" aria-label={t('activity.region')} className="overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-sm">
        <div className="hidden grid-cols-[1fr_1fr_1.4fr_1.4fr] gap-4 border-b border-[var(--sp-border)] px-5 py-3 text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)] sm:grid">
          <div>{t('activity.table.time')}</div>
          <div>{t('activity.table.actor')}</div>
          <div>{t('activity.table.action')}</div>
          <div>{t('activity.table.target')}</div>
        </div>
        {loading && <TableMessage message={t('activity.loading')} />}
        {!loading && events.length === 0 && <TableMessage message={hasFilters ? t('activity.emptyFiltered') : t('activity.empty')} />}
        {!loading && events.map((event) => (
          <div key={event.id} className="grid gap-2 border-b border-[var(--sp-border)] px-5 py-4 text-sm last:border-b-0 sm:grid-cols-[1fr_1fr_1.4fr_1.4fr] sm:items-center sm:gap-4">
            <div className="text-xs text-[var(--sp-faint)]" title={formatDate(event.createdAt)}>{formatRelativeTimeLabel(event.createdAt, t)}</div>
            <div className="font-mono text-xs text-[var(--sp-muted)]">
              {event.actor}
              {event.actorName && <span className="text-[var(--sp-faint)]"> ({event.actorName})</span>}
            </div>
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
