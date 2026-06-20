import { useEffect, useState, type ReactNode } from 'react'
import ConfigWizard from '../ConfigWizard.tsx'
import { apiErrorMessage, apiURL, compactHash, formatDate, formatRelativeTime, hasActiveConfigApply, hasActiveDeepProbe, hasActiveRestart, hasActiveRollback, jobBadgeClasses, latestConfigSnapshots, runtimeKey, snapshotForRuntime, stateBadgeClasses } from '../helpers.ts'
import type { ConfigApplyResult, ConfigDiffEntry, DeepProbeResult, EffectiveConfigResponse, Job, JobStatus, NodeLabels, NodeStatus, RestartJobResult, RestartRequest, RollbackBackupInventoryItem, RollbackJobResult, RollbackRequest, RuntimeConfigSnapshot, RuntimeHealth, RuntimeStatus } from '../types.ts'

export function NodeDetailView({
  creating,
  rollingBack,
  restarting,
  backups,
  backupsError,
  backupsLoading,
  jobs,
  jobsError,
  jobLimit,
  jobsLoading,
  jobStatusFilter,
  node,
  effective,
  effectiveError,
  labelError,
  labelsSaving,
  maintenanceError,
  maintenanceSaving,
  operatorToken,
  onBack,
  onDeepProbe,
  onRollback,
  onRestart,
  onJobStatusFilterChange,
  onLoadMoreJobs,
  onMaintenanceChange,
  onSaveLabels,
  onApplied,
}: {
  creating: boolean
  rollingBack: boolean
  restarting: boolean
  backups: RollbackBackupInventoryItem[]
  backupsError?: string
  backupsLoading: boolean
  jobs: Job[]
  jobsError?: string
  jobLimit: number
  jobsLoading: boolean
  jobStatusFilter: JobStatus | ''
  node: NodeStatus
  effective?: EffectiveConfigResponse
  effectiveError?: string
  labelError?: string
  labelsSaving: boolean
  maintenanceError?: string
  maintenanceSaving: boolean
  operatorToken: string
  onBack: () => void
  onDeepProbe: () => void
  onRollback: (request: RollbackRequest) => void
  onRestart: (request: RestartRequest) => void
  onJobStatusFilterChange: (status: JobStatus | '') => void
  onLoadMoreJobs: () => void
  onMaintenanceChange: (maintenance: boolean) => void
  onSaveLabels: (labels: NodeLabels) => void
  onApplied: () => void
}) {
  const activeProbe = hasActiveDeepProbe(jobs)
  const activeConfigApply = hasActiveConfigApply(jobs)
  const activeRestart = hasActiveRestart(jobs)
  const activeRollback = hasActiveRollback(jobs)
  const snapshots = latestConfigSnapshots(jobs)
  const primarySnapshot = snapshots[0]
  const [selectedBackupRef, setSelectedBackupRef] = useState('')
  const rollbackBackup = backups.find((backup) => backup.ref === selectedBackupRef) ?? backups[0]
  const [wizardOpen, setWizardOpen] = useState(false)
  const [removing, setRemoving] = useState(false)
  const [removeError, setRemoveError] = useState<string | null>(null)
  const [selectedJobId, setSelectedJobId] = useState<string | null>(null)
  const tokenReady = operatorToken.trim().length > 0
  const canDeepProbe = tokenReady && !creating && !activeProbe
  const canRestart = tokenReady && !restarting && !activeRestart
  const canRollback = tokenReady && !rollingBack && !activeRollback && Boolean(rollbackBackup)
  const canEditConfig = tokenReady && Boolean(primarySnapshot?.configPath)
  const canRemoveNode = tokenReady
  const canToggleMaintenance = tokenReady && !maintenanceSaving
  const restartRuntimeType = knownRestartRuntime(effective?.runtimeType || primarySnapshot?.runtimeType || 'hermes')
  const restartProfile = effective?.profile || primarySnapshot?.profile || 'default'

  useEffect(() => {
    if (backups.length === 0) {
      setSelectedBackupRef('')
      return
    }
    if (!backups.some((backup) => backup.ref === selectedBackupRef)) {
      setSelectedBackupRef(backups[0].ref)
    }
  }, [backups, selectedBackupRef])

  const removeNode = async () => {
    if (!canRemoveNode || removing) return
    const confirmed = window.confirm(`Remove node ${node.nodeId} from Sideplane? This clears its fleet record, jobs, credentials, and node-scoped audit history.`)
    if (!confirmed) return

    setRemoving(true)
    setRemoveError(null)
    let removed = false
    try {
      const res = await fetch(apiURL(`/api/nodes/${encodeURIComponent(node.nodeId)}`), {
        method: 'DELETE',
        headers: {
          Authorization: `Bearer ${operatorToken.trim()}`,
        },
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        throw new Error(await apiErrorMessage(res))
      }
      removed = true
      onBack()
    } catch (e) {
      setRemoveError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      if (!removed) {
        setRemoving(false)
      }
    }
  }

  const createRestart = (live: boolean) => {
    if (!canRestart) return
    if (live) {
      const confirmed = window.confirm(`Create a live restart job for ${node.nodeId}? The sidecar must be running with live apply enabled.`)
      if (!confirmed) return
    }
    const request: RestartRequest = {
      profile: restartProfile,
      live,
    }
    if (restartRuntimeType) {
      request.runtimeType = restartRuntimeType
    }
    onRestart(request)
  }

  const createRollback = (live: boolean) => {
    if (!rollbackBackup || !canRollback) return
    if (live) {
      const confirmed = window.confirm(`Create a live rollback job for ${node.nodeId} using backup ${rollbackBackup.ref}? The sidecar must be running with live apply enabled.`)
      if (!confirmed) return
    }
    const request: RollbackRequest = {
      backupRef: rollbackBackup.ref,
      profile: rollbackBackup.profile || restartProfile,
      live,
    }
    const runtimeType = rollbackBackup.runtimeType || restartRuntimeType
    if (runtimeType) {
      request.runtimeType = runtimeType as 'hermes' | 'openclaw'
    }
    onRollback(request)
  }

  return (
    <div className="mx-auto max-w-5xl px-4 py-6 sm:px-6 lg:px-9 lg:py-8">
      <button type="button" className="mb-5 rounded-lg px-2 py-1 text-sm font-medium text-[var(--sp-muted)] hover:bg-[var(--sp-surface-2)] hover:text-[var(--sp-text)]" onClick={onBack}>
        ‹ Fleet
      </button>
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <div className="flex flex-wrap items-center gap-3">
            <h1 className="text-2xl font-bold tracking-tight">{node.nodeId}</h1>
            <CopyButton value={node.nodeId} label="Copy node ID" />
            <span className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-semibold ${stateBadgeClasses(node.state)}`}>
              <span className="h-1.5 w-1.5 rounded-full bg-current" />
              {node.state}
            </span>
            {node.drift === true && (
              <span className="inline-flex items-center rounded-full border border-amber-500/30 bg-amber-500/10 px-2.5 py-1 text-xs font-semibold text-amber-600">
                config drift
              </span>
            )}
            {node.maintenance && (
              <span className="inline-flex items-center rounded-full border border-amber-500/30 bg-amber-500/10 px-2.5 py-1 text-xs font-semibold text-amber-700">
                maintenance
              </span>
            )}
            {node.sidecarOutdated && (
              <span className="inline-flex items-center rounded-full border border-amber-500/30 bg-amber-500/10 px-2.5 py-1 text-xs font-semibold text-amber-600" title="sidecar version differs from expected">
                sidecar outdated
              </span>
            )}
          </div>
          <div className="mt-2 font-mono text-sm text-[var(--sp-muted)]">{node.hostname || '-'} · sidecar {node.sidecarVersion || 'dev'}</div>
          <LabelEditor
            error={labelError}
            labels={node.labels ?? {}}
            saving={labelsSaving}
            tokenReady={tokenReady}
            onSave={onSaveLabels}
          />
        </div>
        <div className="flex flex-wrap gap-2">
          <button
            type="button"
            className={node.maintenance ? 'h-9 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 text-sm font-medium text-amber-700 hover:bg-amber-500/15 disabled:cursor-not-allowed disabled:opacity-55' : 'h-9 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-60'}
            disabled={!canToggleMaintenance}
            title={!tokenReady ? 'Set an operator token before changing maintenance mode' : node.maintenance ? 'Exit maintenance mode for this node' : 'Enter maintenance mode for this node'}
            onClick={() => onMaintenanceChange(!Boolean(node.maintenance))}
          >
            {maintenanceSaving ? 'Saving...' : node.maintenance ? 'Exit maintenance' : 'Enter maintenance'}
          </button>
          <button
            type="button"
            className="h-9 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
            disabled={!canDeepProbe}
            title={!tokenReady ? 'Set an operator token before creating jobs' : activeProbe ? 'A deep probe is already pending or running' : 'Create a deep probe job'}
            onClick={onDeepProbe}
          >
            {creating ? 'Creating…' : activeProbe ? 'Probe active' : 'Deep probe'}
          </button>
          <button
            type="button"
            className="h-9 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
            disabled={!canRestart}
            title={!tokenReady ? 'Set an operator token before creating restart jobs' : activeRestart ? 'A restart job is already pending or running' : 'Create a dry-run restart job'}
            onClick={() => createRestart(false)}
          >
            {restarting ? 'Creating...' : activeRestart ? 'Restart active' : 'Restart'}
          </button>
          <button
            type="button"
            className="h-9 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 text-sm font-medium text-amber-700 hover:bg-amber-500/15 disabled:cursor-not-allowed disabled:opacity-55"
            disabled={!canRestart}
            title={!tokenReady ? 'Set an operator token before live restart' : activeRestart ? 'A restart job is already pending or running' : 'Confirm and create a live restart job'}
            onClick={() => createRestart(true)}
          >
            Live restart
          </button>
          {backups.length > 0 && (
            <div className="flex flex-wrap gap-2">
              <select
                className="h-9 max-w-80 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface)] px-2 font-mono text-xs outline-none disabled:cursor-not-allowed disabled:opacity-60"
                value={rollbackBackup?.ref ?? ''}
                disabled={!tokenReady || rollingBack || activeRollback || backupsLoading}
                onChange={(event) => setSelectedBackupRef(event.target.value)}
              >
                {backups.map((backup) => (
                  <option key={backup.ref} value={backup.ref}>
                    {backup.ref}
                  </option>
                ))}
              </select>
              <button
                type="button"
                className="h-9 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
                disabled={!canRollback}
                title={!tokenReady ? 'Set an operator token before creating rollback jobs' : activeRollback ? 'A rollback job is already pending or running' : `Create a dry-run rollback job for ${rollbackBackup.ref}`}
                onClick={() => createRollback(false)}
              >
                {rollingBack ? 'Creating...' : activeRollback ? 'Rollback active' : 'Rollback'}
              </button>
              <button
                type="button"
                className="h-9 rounded-lg border border-rose-500/40 bg-rose-500/10 px-3 text-sm font-medium text-rose-600 hover:bg-rose-500/15 disabled:cursor-not-allowed disabled:opacity-55"
                disabled={!canRollback}
                title={!tokenReady ? 'Set an operator token before live rollback' : activeRollback ? 'A rollback job is already pending or running' : `Confirm and create a live rollback job for ${rollbackBackup.ref}`}
                onClick={() => createRollback(true)}
              >
                Live rollback
              </button>
            </div>
          )}
          <button
            type="button"
            className="h-9 rounded-lg bg-[var(--sp-accent)] px-3 text-sm font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-55"
            disabled={!canEditConfig}
            title={!tokenReady ? 'Set an operator token before changing config' : canEditConfig ? 'Open the change configuration wizard' : 'Run a deep probe first to discover the config path'}
            onClick={() => setWizardOpen(true)}
          >
            Edit config
          </button>
          <button
            type="button"
            className="h-9 rounded-lg border border-rose-500/40 bg-rose-500/10 px-3 text-sm font-medium text-rose-600 hover:bg-rose-500/15 disabled:cursor-not-allowed disabled:opacity-55"
            disabled={!canRemoveNode || removing}
            title={canRemoveNode ? 'Remove this node from the fleet inventory' : 'Set an operator token before removing a node'}
            onClick={removeNode}
          >
            {removing ? 'Removing…' : 'Remove node'}
          </button>
        </div>
      </div>

      {removeError && (
        <div className="mb-5 rounded-xl border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-600">
          Failed to remove node: {removeError}
        </div>
      )}

      {maintenanceError && (
        <div className="mb-5 rounded-xl border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-600">
          Failed to update maintenance: {maintenanceError}
        </div>
      )}

      {backupsError && (
        <div className="mb-5 rounded-xl border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-600">
          Failed to load backups: {backupsError}
        </div>
      )}

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
        <div className="flex flex-col gap-3 border-b border-[var(--sp-border)] px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="text-sm font-semibold">Recent jobs</div>
          <div className="flex flex-wrap items-center gap-2">
            <select
              className="h-8 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-2 text-xs outline-none"
              value={jobStatusFilter}
              onChange={(event) => onJobStatusFilterChange(event.target.value as JobStatus | '')}
            >
              <option value="">All statuses</option>
              <option value="pending">Pending</option>
              <option value="claimed">Claimed</option>
              <option value="completed">Completed</option>
              <option value="failed">Failed</option>
            </select>
            <button
              type="button"
              className="h-8 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-2.5 text-xs font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-55"
              disabled={jobsLoading}
              onClick={onLoadMoreJobs}
            >
              Load more
            </button>
            <span className="font-mono text-[11px] text-[var(--sp-faint)]">{jobLimit}</span>
            {jobsLoading && <div className="text-xs text-[var(--sp-muted)]">Loading jobs...</div>}
          </div>
        </div>
        {jobsError && <div className="mt-4 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-600">Failed to load jobs: {jobsError}</div>}
        <div className="divide-y divide-[var(--sp-border)]">
          {!jobsLoading && jobs.length === 0 && <div className="px-4 py-4 text-xs text-[var(--sp-muted)]">No jobs yet.</div>}
          {jobs.map((job) => (
            <div key={job.id}>
              <div
                role="button"
                tabIndex={0}
                className="grid w-full gap-2 px-4 py-3 text-left text-xs hover:bg-[var(--sp-surface-2)] sm:grid-cols-[1fr_auto_auto_auto] sm:items-center"
                aria-expanded={selectedJobId === job.id}
                onClick={() => setSelectedJobId((current) => (current === job.id ? null : job.id))}
                onKeyDown={(event) => {
                  if (event.key !== 'Enter' && event.key !== ' ') return
                  event.preventDefault()
                  setSelectedJobId((current) => (current === job.id ? null : job.id))
                }}
              >
                <span className="flex min-w-0 items-center gap-2 font-mono text-[var(--sp-text)]">
                  <span className="inline-flex h-5 w-5 flex-none items-center justify-center rounded border border-[var(--sp-border)] text-[11px] text-[var(--sp-muted)]">
                    {selectedJobId === job.id ? '-' : '+'}
                  </span>
                  <span className="min-w-0">
                    <span className="block truncate">{job.type}</span>
                    <span className="block truncate text-[10px] text-[var(--sp-faint)]">{job.id}</span>
                  </span>
                </span>
                <span onClick={(event) => event.stopPropagation()}>
                  <CopyButton value={job.id} label="Copy job ID" />
                </span>
                <span className={`inline-flex w-fit rounded border px-2 py-0.5 font-semibold ${jobBadgeClasses(job.status)}`}>{job.status}</span>
                <span className="text-[var(--sp-faint)]" title={formatDate(job.createdAt)}>{formatRelativeTime(job.createdAt)}</span>
              </div>
              {selectedJobId === job.id && <JobDetail job={job} />}
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

function LabelEditor({
  error,
  labels,
  saving,
  tokenReady,
  onSave,
}: {
  error?: string
  labels: NodeLabels
  saving: boolean
  tokenReady: boolean
  onSave: (labels: NodeLabels) => void
}) {
  const [draft, setDraft] = useState(formatLabelDraft(labels))
  const [draftError, setDraftError] = useState<string | null>(null)

  useEffect(() => {
    setDraft(formatLabelDraft(labels))
  }, [labels])

  const save = () => {
    const parsed = parseLabelDraft(draft)
    if (typeof parsed === 'string') {
      setDraftError(parsed)
      return
    }
    setDraftError(null)
    onSave(parsed)
  }

  const pairs = labelPairs(labels)
  return (
    <div className="mt-3 max-w-2xl rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface)] p-3">
      <div className="mb-2 flex flex-wrap gap-1.5">
        {pairs.length > 0 ? pairs.map(([key, value]) => (
          <span key={key} className="rounded border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-2 py-1 font-mono text-[11px] text-[var(--sp-muted)]">
            {key}={value}
          </span>
        )) : <span className="text-xs text-[var(--sp-faint)]">No labels</span>}
      </div>
      <div className="flex flex-col gap-2 sm:flex-row">
        <input
          className="h-9 min-w-0 flex-1 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)] disabled:cursor-not-allowed disabled:opacity-60"
          value={draft}
          disabled={!tokenReady || saving}
          placeholder="role=canary,zone=lab"
          onChange={(event) => setDraft(event.target.value)}
        />
        <button
          type="button"
          className="h-9 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
          disabled={!tokenReady || saving}
          title={tokenReady ? 'Save labels' : 'Set an operator token before editing labels'}
          onClick={save}
        >
          {saving ? 'Saving...' : 'Save labels'}
        </button>
      </div>
      {(draftError || error) && (
        <div className="mt-2 text-xs text-rose-600">{draftError || error}</div>
      )}
    </div>
  )
}

function formatLabelDraft(labels: NodeLabels): string {
  return labelPairs(labels).map(([key, value]) => `${key}=${value}`).join(',')
}

function parseLabelDraft(value: string): NodeLabels | string {
  const trimmed = value.trim()
  if (!trimmed) return {}
  const labels: NodeLabels = {}
  for (const part of trimmed.split(',')) {
    const entry = part.trim()
    if (!entry) return 'label entry is empty'
    const index = entry.indexOf('=')
    if (index <= 0) return `invalid label ${entry}`
    const key = entry.slice(0, index).trim()
    const labelValue = entry.slice(index + 1).trim()
    if (!key) return 'label key is required'
    if (labels[key] !== undefined) return `duplicate label ${key}`
    labels[key] = labelValue
  }
  return labels
}

function labelPairs(labels: NodeLabels): [string, string][] {
  return Object.entries(labels).sort(([a], [b]) => a.localeCompare(b))
}

function JobDetail({ job }: { job: Job }) {
  if (job.status === 'pending' || job.status === 'claimed') {
    return <JobDetailShell>Waiting for sidecar...</JobDetailShell>
  }
  if (job.type === 'deep_probe') {
    return <DeepProbeJobDetail job={job} />
  }
  if (job.type === 'config_apply') {
    return <ConfigApplyJobDetail job={job} />
  }
  if (job.type === 'restart') {
    return <RestartJobDetail job={job} />
  }
  if (job.type === 'rollback') {
    return <RollbackJobDetail job={job} />
  }
  if (job.status === 'failed') {
    return <JobDetailShell tone="danger">{job.error || 'Job failed without an error message.'}</JobDetailShell>
  }
  return <JobDetailShell>No structured result available.</JobDetailShell>
}

function DeepProbeJobDetail({ job }: { job: Job }) {
  const result = parseJobResult<DeepProbeResult>(job)
  const snapshots = result?.configSnapshots ?? []
  if (snapshots.length === 0) {
    return <JobDetailShell>No config snapshots reported.</JobDetailShell>
  }

  return (
    <JobDetailShell>
      <div className="grid gap-2">
        {snapshots.map((snapshot, index) => (
          <div key={`${snapshot.runtimeName || snapshot.runtimeType}-${snapshot.profile || index}`} className="grid gap-2 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface)] px-3 py-2 sm:grid-cols-[1fr_1fr_1fr_1fr]">
            <RuntimeField label="Runtime" value={snapshot.runtimeName || snapshot.runtimeType || '-'} />
            <RuntimeField label="Provider" value={snapshot.provider || '-'} />
            <RuntimeField label="Model" value={snapshot.model || '-'} />
            <RuntimeField label="Hash" value={compactHash(snapshot.configHash)} title={snapshot.configHash} />
          </div>
        ))}
      </div>
    </JobDetailShell>
  )
}

function ConfigApplyJobDetail({ job }: { job: Job }) {
  const result = parseJobResult<ConfigApplyResult>(job)
  const steps = result?.steps ?? []
  if (steps.length === 0) {
    return <JobDetailShell>No config apply steps reported.</JobDetailShell>
  }

  return (
    <JobDetailShell>
      {result?.planId && (
        <div className="mb-2 flex items-center gap-2 font-mono text-[11px] text-[var(--sp-faint)]">
          <span>{result.planId}</span>
          <CopyButton value={result.planId} label="Copy plan ID" />
        </div>
      )}
      <div className="mb-2 font-mono text-[11px] text-[var(--sp-faint)]">{result?.dryRun ? 'dry-run' : 'live'} apply</div>
      <div className="grid gap-2">
        {steps.map((step, index) => (
          <div key={`${step.name}-${index}`} className="grid gap-2 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface)] px-3 py-2 sm:grid-cols-[1fr_auto_2fr] sm:items-center">
            <span className="font-mono text-xs font-semibold text-[var(--sp-text)]">{step.name}</span>
            <span className={`inline-flex w-fit rounded border px-2 py-0.5 text-[11px] font-semibold ${step.status === 'failed' ? 'border-rose-500/30 bg-rose-500/10 text-rose-600' : 'border-[var(--sp-border)] bg-[var(--sp-surface-2)] text-[var(--sp-muted)]'}`}>{step.status}</span>
            <span className="min-w-0 truncate text-xs text-[var(--sp-muted)]" title={step.detail || ''}>{step.detail || '-'}</span>
          </div>
        ))}
      </div>
    </JobDetailShell>
  )
}

function RestartJobDetail({ job }: { job: Job }) {
  const result = parseJobResult<RestartJobResult>(job)
  const steps = result?.steps ?? []
  if (steps.length === 0) {
    return <JobDetailShell tone={job.status === 'failed' ? 'danger' : 'normal'}>{job.error || 'No restart steps reported.'}</JobDetailShell>
  }

  return (
    <JobDetailShell tone={job.status === 'failed' ? 'danger' : 'normal'}>
      <div className="mb-2 grid gap-2 font-mono text-[11px] text-[var(--sp-faint)] sm:grid-cols-2">
        <span>controller: {result?.controller || '-'}</span>
        <span>health: {result?.healthStatus || '-'}</span>
      </div>
      <div className="grid gap-2">
        {steps.map((step, index) => (
          <div key={`${step.name}-${index}`} className="grid gap-2 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface)] px-3 py-2 sm:grid-cols-[1fr_auto_2fr] sm:items-center">
            <span className="font-mono text-xs font-semibold text-[var(--sp-text)]">{step.name}</span>
            <span className={`inline-flex w-fit rounded border px-2 py-0.5 text-[11px] font-semibold ${step.status === 'failed' ? 'border-rose-500/30 bg-rose-500/10 text-rose-600' : 'border-[var(--sp-border)] bg-[var(--sp-surface-2)] text-[var(--sp-muted)]'}`}>{step.status}</span>
            <span className="min-w-0 truncate text-xs text-[var(--sp-muted)]" title={step.detail || ''}>{step.detail || '-'}</span>
          </div>
        ))}
      </div>
    </JobDetailShell>
  )
}

function RollbackJobDetail({ job }: { job: Job }) {
  const result = parseJobResult<RollbackJobResult>(job)
  const steps = result?.steps ?? []
  if (steps.length === 0) {
    return <JobDetailShell tone={job.status === 'failed' ? 'danger' : 'normal'}>{job.error || 'No rollback steps reported.'}</JobDetailShell>
  }

  return (
    <JobDetailShell tone={job.status === 'failed' ? 'danger' : 'normal'}>
      <div className="mb-2 grid gap-2 font-mono text-[11px] text-[var(--sp-faint)] sm:grid-cols-2">
        <span>backup: {result?.backupRef || '-'}</span>
        <span>health: {result?.healthStatus || '-'}</span>
      </div>
      <div className="grid gap-2">
        {steps.map((step, index) => (
          <div key={`${step.name}-${index}`} className="grid gap-2 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface)] px-3 py-2 sm:grid-cols-[1fr_auto_2fr] sm:items-center">
            <span className="font-mono text-xs font-semibold text-[var(--sp-text)]">{step.name}</span>
            <span className={`inline-flex w-fit rounded border px-2 py-0.5 text-[11px] font-semibold ${step.status === 'failed' ? 'border-rose-500/30 bg-rose-500/10 text-rose-600' : 'border-[var(--sp-border)] bg-[var(--sp-surface-2)] text-[var(--sp-muted)]'}`}>{step.status}</span>
            <span className="min-w-0 truncate text-xs text-[var(--sp-muted)]" title={step.detail || ''}>{step.detail || '-'}</span>
          </div>
        ))}
      </div>
    </JobDetailShell>
  )
}

function knownRestartRuntime(value: string | undefined): RestartRequest['runtimeType'] | undefined {
  return value === 'hermes' || value === 'openclaw' ? value : undefined
}

function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1400)
    } catch {
      setCopied(false)
    }
  }

  return (
    <button
      type="button"
      className="h-6 rounded border border-[var(--sp-border)] px-2 text-[10px] font-semibold text-[var(--sp-muted)] hover:bg-[var(--sp-surface-2)]"
      title={label}
      onClick={copy}
    >
      {copied ? 'Copied' : 'Copy'}
    </button>
  )
}

function JobDetailShell({ children, tone = 'normal' }: { children: ReactNode; tone?: 'normal' | 'danger' }) {
  return (
    <div className={`border-t border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-4 py-3 text-xs ${tone === 'danger' ? 'text-rose-600' : 'text-[var(--sp-muted)]'}`}>
      {children}
    </div>
  )
}

function parseJobResult<T>(job: Job): T | null {
  if (!job.resultJson?.trim()) return null
  try {
    return JSON.parse(job.resultJson) as T
  } catch {
    return null
  }
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
  const warnings = [...(runtime.warnings ?? []), ...(snapshot?.warnings ?? [])]
  if (runtime.lastError) warnings.unshift(runtime.lastError)
  const health = snapshot?.health ?? runtime.health

  return (
    <div className="overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)]">
      <div className="flex flex-col gap-3 border-b border-[var(--sp-border)] px-4 py-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-sm font-semibold">{runtime.name || runtime.type || 'runtime'}</span>
          {runtime.type && <span className="text-xs text-[var(--sp-faint)]">{runtime.type}</span>}
          {runtime.state && <span className={`inline-flex rounded border px-2 py-0.5 text-[11px] font-semibold ${runtime.state === 'error' ? 'border-rose-500/30 bg-rose-500/10 text-rose-600' : 'border-[var(--sp-border)] bg-[var(--sp-surface-2)] text-[var(--sp-muted)]'}`}>{runtime.state}</span>}
          <RuntimeHealthBadge health={health} />
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

function RuntimeHealthBadge({ health }: { health?: RuntimeHealth }) {
  if (!health?.state) return null
  const classes = health.state === 'healthy'
    ? 'border-emerald-500/25 bg-emerald-500/10 text-emerald-600'
    : health.state === 'degraded'
      ? 'border-rose-500/30 bg-rose-500/10 text-rose-600'
      : 'border-[var(--sp-border)] bg-[var(--sp-surface-2)] text-[var(--sp-muted)]'
  return (
    <span className={`inline-flex rounded border px-2 py-0.5 text-[11px] font-semibold ${classes}`} title={health.reason || health.state}>
      {health.state}
    </span>
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
