import { useMemo, useState } from 'react'
import { apiErrorMessage, apiURL, compactHash, fleetOverviewMetrics, formatDate, hasActiveDeepProbe, runtimeDeploymentLabel, runtimeKey, runtimeModelLabel, runtimeVersionLabel, stateBadgeClasses } from '../helpers.ts'
import type { FleetOverviewMetrics } from '../helpers.ts'
import { formatRelativeTimeLabel, useT, type TFunction } from '../i18n.ts'
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
  const { t } = useT()
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
        if (res.status === 401) throw new Error(t('common.operatorTokenRequiredInvalid'))
        if (res.status === 403) throw new Error(t('common.operatorTokenReadOnly'))
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as BulkJobResponse
      setBulkMessage(t('fleet.bulk.probed', { created: data.created, total: selectedIds.length }))
      setSelected(new Set())
      onRefresh()
    } catch (e) {
      setBulkError(e instanceof Error ? e.message : t('common.unknownError'))
    } finally {
      setBulkBusy(false)
    }
  }

  const bulkLabel = async () => {
    if (selectedIds.length === 0 || bulkBusy) return
    const labels = parseLabelAssignments(labelInput, t)
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
        if (res.status === 401) throw new Error(t('common.operatorTokenRequiredInvalid'))
        if (res.status === 403) throw new Error(t('common.operatorTokenReadOnly'))
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as BulkNodeLabelsResponse
      setBulkMessage(t('fleet.bulk.appliedLabels', { labelCount: Object.keys(labels).length, updated: data.updated }))
      setLabelInput('')
      setSelected(new Set())
      onRefresh()
    } catch (e) {
      setBulkError(e instanceof Error ? e.message : t('common.unknownError'))
    } finally {
      setBulkBusy(false)
    }
  }

  return (
    <div className="mx-auto max-w-6xl px-4 py-6 sm:px-6 lg:px-9 lg:py-8">
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t('fleet.title')}</h1>
          <div className="mt-1 text-sm text-[var(--sp-muted)]">{fleetSubtitle}</div>
        </div>
        <button
          type="button"
          className="inline-flex h-9 w-fit items-center gap-2 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
          disabled={refreshing}
          onClick={onRefresh}
        >
          <span className={refreshing ? 'animate-spin' : ''}>↻</span>
          {refreshing ? t('common.refreshing') : t('common.refresh')}
        </button>
      </div>

      <FleetMetricsPanel metrics={metrics} />

      {bannerText && (
        <div role="status" className="mb-5 rounded-xl border border-amber-500/35 bg-amber-500/10 px-4 py-3 text-sm font-medium text-[var(--sp-text)]">
          {bannerText}
        </div>
      )}

      {error && (
        <div role="alert" className="mb-5 rounded-xl border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-600">
          {t('fleet.errorLoadNodes', { error })}
        </div>
      )}

      <div className="mb-3 grid gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <input
          className="h-9 w-full rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)] sm:max-w-sm"
          value={searchQuery}
          aria-label={t('fleet.filterNodes')}
          placeholder={t('fleet.filterPlaceholder')}
          onChange={(event) => setSearchQuery(event.target.value)}
        />
        <input
          className="h-9 w-full rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)] sm:max-w-sm"
          value={selector}
          aria-label={t('fleet.nodeSelector')}
          placeholder={t('fleet.selectorPlaceholder')}
          onChange={(event) => onSelectorChange(event.target.value)}
        />
      </div>

      {selectedIds.length > 0 && (
        <div className="mb-3 flex flex-col gap-2 rounded-xl border border-[var(--sp-accent)]/40 bg-[var(--sp-surface-2)] px-4 py-3 sm:flex-row sm:items-center sm:gap-3">
          <span className="text-sm font-medium">{t('fleet.bulk.selected', { count: selectedIds.length })}</span>
          <button
            type="button"
            className="h-9 rounded-lg bg-[var(--sp-accent)] px-3 text-sm font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-55"
            disabled={bulkBusy}
            onClick={bulkProbe}
          >
            {t('fleet.bulk.probeSelected')}
          </button>
          <input
            className="h-9 min-w-0 flex-1 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)] sm:max-w-xs"
            value={labelInput}
            aria-label={t('fleet.labelsSelected')}
            placeholder={t('fleet.labelsPlaceholder')}
            onChange={(event) => setLabelInput(event.target.value)}
          />
          <button
            type="button"
            className="h-9 rounded-lg border border-[var(--sp-border-strong)] px-3 text-sm font-medium hover:bg-[var(--sp-surface)] disabled:cursor-not-allowed disabled:opacity-55"
            disabled={bulkBusy || labelInput.trim() === ''}
            onClick={bulkLabel}
          >
            {t('fleet.labelSelected')}
          </button>
          <button
            type="button"
            className="h-9 rounded-lg px-2 text-sm text-[var(--sp-muted)] hover:text-[var(--sp-text)]"
            onClick={() => setSelected(new Set())}
          >
            {t('common.clear')}
          </button>
        </div>
      )}
      {bulkMessage && <div role="status" className="mb-3 rounded-lg border border-emerald-500/25 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-600">{bulkMessage}</div>}
      {bulkError && <div role="alert" className="mb-3 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-sm text-rose-600">{bulkError}</div>}

      <div role="region" aria-label={t('fleet.nodesRegion')} className="overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-sm">
        <div className="hidden border-b border-[var(--sp-border)] px-5 py-3 lg:flex lg:items-center lg:gap-4">
          <label className="flex w-6 items-center justify-center">
            <input type="checkbox" aria-label={t('fleet.selectAll')} checked={allVisibleSelected} onChange={toggleSelectAllVisible} />
          </label>
          <div className="grid flex-1 grid-cols-[2fr_1fr_1.4fr_1.2fr_1fr_1fr_2.5rem] gap-4 text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">
            <SortHeader active={sort.key === 'node'} direction={sort.direction} label={t('fleet.table.node')} onClick={() => toggleSort('node')} />
            <SortHeader active={sort.key === 'state'} direction={sort.direction} label={t('fleet.table.state')} onClick={() => toggleSort('state')} />
            <div>{t('fleet.table.runtimes')}</div>
            <div>{t('fleet.table.model')}</div>
            <div>{t('fleet.table.config')}</div>
            <SortHeader active={sort.key === 'heartbeat'} direction={sort.direction} label={t('fleet.table.heartbeat')} onClick={() => toggleSort('heartbeat')} />
            <div />
          </div>
        </div>

        {loading && <TableMessage message={t('fleet.table.loading')} />}
        {!loading && nodes.length === 0 && <TableMessage message={t('fleet.table.noNodes')} />}
        {!loading && nodes.length > 0 && sortedNodes.length === 0 && <TableMessage message={t('fleet.table.noMatch')} />}
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
      runtime.deploymentMode,
      runtime.provider,
      runtime.model,
      runtime.version,
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
  const { t } = useT()
  const rolloutDetail = metrics.activeRollouts > 0
    ? t('fleet.metrics.rolloutDetail', { running: metrics.runningRollouts, paused: metrics.pausedRollouts })
    : t('fleet.metrics.rolloutNone')

  return (
    <section className="mb-5 overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-sm">
      <div className="grid divide-y divide-[var(--sp-border)] sm:grid-cols-2 sm:divide-x sm:divide-y-0 xl:grid-cols-4">
        <MetricCell
          accentClass="bg-emerald-500"
          detail={t('fleet.metrics.nodesDetail', { fresh: metrics.freshNodes, stale: metrics.staleNodes, offline: metrics.offlineNodes, maintenance: metrics.maintenanceNodes })}
          label={t('fleet.metrics.nodes')}
          value={metrics.totalNodes}
        />
        <MetricCell
          accentClass={metrics.driftedNodes > 0 || metrics.outdatedSidecars > 0 || metrics.outdatedRuntimes > 0 ? 'bg-amber-500' : 'bg-emerald-500'}
          detail={t('fleet.metrics.configDriftDetail', { sidecars: metrics.outdatedSidecars, runtimeOutdated: metrics.outdatedRuntimes, runtimes: metrics.runtimeCount })}
          label={t('fleet.metrics.configDrift')}
          value={metrics.driftedNodes}
        />
        <MetricCell
          accentClass={metrics.activeJobs > 0 ? 'bg-sky-500' : 'bg-[var(--sp-faint)]'}
          detail={t('fleet.metrics.activeJobsDetail')}
          label={t('fleet.metrics.activeJobs')}
          value={metrics.activeJobs}
        />
        <MetricCell
          accentClass={metrics.activeRollouts > 0 ? 'bg-sky-500' : 'bg-[var(--sp-faint)]'}
          detail={rolloutDetail}
          label={t('fleet.metrics.rolloutActivity')}
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
  const { t } = useT()
  const configLabel = node.lastError ? t('fleet.config.error') : node.configHash ? t('fleet.config.observed') : t('fleet.config.unknown')
  const configColor = node.lastError ? 'text-rose-600' : node.configHash ? 'text-emerald-600' : 'text-[var(--sp-muted)]'
  const runtimes = node.runtimes ?? []

  return (
    <div className="flex items-start border-b border-[var(--sp-border)] last:border-b-0 hover:bg-[var(--sp-surface-2)]">
      <label className="flex items-center self-stretch px-5 py-4 lg:px-5" onClick={(event) => event.stopPropagation()}>
        <input
          type="checkbox"
          aria-label={t('fleet.selectNode', { nodeId: node.nodeId })}
          checked={selected}
          onChange={onToggleSelect}
        />
      </label>
      <button
        type="button"
        className="grid min-w-0 flex-1 gap-3 py-4 pr-5 text-left lg:grid-cols-[2fr_1fr_1.4fr_1.2fr_1fr_1fr_2.5rem] lg:items-center lg:gap-4"
        onClick={onOpen}
      >
      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate font-mono text-sm font-semibold">{node.nodeId}</span>
          {activeProbe && <span className="rounded bg-sky-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-sky-600">{t('fleet.row.probe')}</span>}
          {node.maintenance && <span className="rounded bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-amber-700">{t('fleet.row.maint')}</span>}
          {node.sidecarOutdated && <span className="rounded bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-amber-600" title={t('fleet.sidecarOutdatedTitle')}>{t('fleet.row.outdated')}</span>}
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

      <div className="flex flex-wrap gap-1.5 lg:flex-col lg:items-start">
        {runtimes.length > 0 ? runtimes.map((runtime, index) => {
          const deployment = runtimeDeploymentLabel(runtime)
          const version = runtimeVersionLabel(runtime)
          return (
            <span key={runtimeKey(runtime, index)} className="inline-flex max-w-full items-center gap-1.5 rounded-md bg-[var(--sp-surface-3)] px-2 py-1 font-mono text-[11px] text-[var(--sp-muted)]">
              <span className="h-1.5 w-1.5 flex-none rounded-full bg-[var(--sp-accent)]" />
              {deployment && <span className="flex-none rounded bg-[var(--sp-surface-2)] px-1 py-0.5 text-[9px] font-semibold uppercase tracking-wide text-[var(--sp-faint)]" title={t('fleet.runtime.deployment')}>{deployment}</span>}
              {version && <span className="flex-none text-[var(--sp-faint)]" title={t('fleet.runtime.version')}>{version}</span>}
              {runtime.outdated && <span className="rounded bg-amber-500/10 px-1 py-0.5 text-[9px] font-semibold text-amber-600" title={t('fleet.runtimeOutdatedTitle')}>{t('fleet.row.outdated')}</span>}
            </span>
          )
        }) : <span className="text-xs text-[var(--sp-faint)]">-</span>}
      </div>

      <div className="flex flex-wrap gap-1.5 lg:flex-col lg:items-start">
        {runtimes.length > 0 ? runtimes.map((runtime, index) => (
          <span key={runtimeKey(runtime, index)} className="inline-flex max-w-full rounded-md bg-[var(--sp-surface-2)] px-2 py-1 font-mono text-[11px] text-[var(--sp-muted)]" title={t('fleet.runtime.model')}>
            <span className="truncate">{runtimeModelLabel(runtime)}</span>
          </span>
        )) : <span className="text-xs text-[var(--sp-faint)]">-</span>}
      </div>

      <div>
        <div className="flex items-center gap-1.5">
          <span className={`text-xs font-semibold ${configColor}`}>{configLabel}</span>
          {node.drift === true && (
            <span className="rounded bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-amber-600">{t('fleet.config.drift')}</span>
          )}
        </div>
        <div className="mt-1 font-mono text-[11px] text-[var(--sp-faint)]">{compactHash(node.configHash)}</div>
      </div>

      <div className="text-xs text-[var(--sp-muted)]" title={formatDate(node.lastHeartbeatAt)}>
        {formatRelativeTimeLabel(node.lastHeartbeatAt, t)}
      </div>

        <div className="hidden justify-end text-[var(--sp-faint)] lg:flex">›</div>
      </button>
    </div>
  )
}

// parseLabelAssignments parses "key=value,key2=value2" into a label record, or
// returns an error string when an entry is malformed.
function parseLabelAssignments(raw: string, t: TFunction): Record<string, string> | string {
  const labels: Record<string, string> = {}
  for (const part of raw.split(',')) {
    const trimmed = part.trim()
    if (trimmed === '') continue
    const index = trimmed.indexOf('=')
    if (index <= 0) {
      return t('fleet.bulk.invalidLabel', { label: trimmed })
    }
    labels[trimmed.slice(0, index).trim()] = trimmed.slice(index + 1).trim()
  }
  if (Object.keys(labels).length === 0) {
    return t('fleet.bulk.provideLabel')
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
