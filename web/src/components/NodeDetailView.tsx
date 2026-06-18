import { useState } from 'react'
import ConfigWizard from '../ConfigWizard.tsx'
import { compactHash, formatDate, formatRelativeTime, hasActiveConfigApply, hasActiveDeepProbe, jobBadgeClasses, latestConfigSnapshots, runtimeKey, snapshotForRuntime, stateBadgeClasses } from '../helpers.ts'
import type { ConfigDiffEntry, EffectiveConfigResponse, Job, NodeStatus, RuntimeConfigSnapshot, RuntimeStatus } from '../types.ts'

export function NodeDetailView({
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
