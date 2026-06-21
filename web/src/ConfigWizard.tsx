import { useCallback, useEffect, useRef, useState } from 'react'
import { apiErrorMessage, apiURL, formatDate } from './helpers.ts'
import type { ConfigApplyResult, ConfigApplyStep, DesiredConfig, DesiredConfigHistoryEntry, EffectiveConfigPreviewRequest, EffectiveConfigResponse, Job, ListDesiredConfigHistoryResponse, RevertDesiredConfigResponse } from './types.ts'

const WIZARD_STEPS = ['Edit', 'Review', 'Apply', 'Done'] as const
type WizardStep = (typeof WIZARD_STEPS)[number]

// Canonical pipeline order so the Apply checklist renders steps that have not
// been reported yet as pending.
const PIPELINE_STEPS: Array<{ name: string; label: string }> = [
  { name: 'plan_received', label: 'Plan received' },
  { name: 'signature_verified', label: 'Plan signature verified' },
  { name: 'backup_created', label: 'Local backup created' },
  { name: 'temp_written', label: 'Temp config written' },
  { name: 'validated', label: 'Config validated' },
  { name: 'replaced', label: 'Config replaced' },
  { name: 'restarted', label: 'Runtime restarted' },
  { name: 'health_checked', label: 'Health checked' },
]

const APPLY_POLL_MS = 1_500
const APPLY_POLL_LIMIT = 40

interface ConfigWizardProps {
  nodeId: string
  runtimeType: string
  profile: string
  operatorToken: string
  effective?: EffectiveConfigResponse
  activeConfigApply: boolean
  onClose: () => void
  onApplied: () => void
}

export default function ConfigWizard({
  nodeId,
  runtimeType,
  profile,
  operatorToken,
  effective,
  activeConfigApply,
  onClose,
  onApplied,
}: ConfigWizardProps) {
  const [step, setStep] = useState<WizardStep>('Edit')
  const [provider, setProvider] = useState(effective?.effective.provider ?? '')
  const [model, setModel] = useState(effective?.effective.model ?? '')
  const [dryRun, setDryRun] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [review, setReview] = useState<EffectiveConfigResponse | undefined>(effective)
  const [applyResult, setApplyResult] = useState<ConfigApplyResult | null>(null)
  const [applyStatus, setApplyStatus] = useState<Job['status'] | null>(null)
  const [history, setHistory] = useState<DesiredConfigHistoryEntry[]>([])
  const [historyLoading, setHistoryLoading] = useState(false)
  const [historyError, setHistoryError] = useState<string | null>(null)
  const [revertingHistoryId, setRevertingHistoryId] = useState<string | null>(null)
  const mountedRef = useRef(true)
  const panelRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  // Keep keyboard focus inside the dialog and restore it to the opener on close.
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null
    panelRef.current?.focus()
    return () => opener?.focus?.()
  }, [])

  const onDialogKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Escape') {
      event.preventDefault()
      onClose()
      return
    }
    if (event.key !== 'Tab') return
    trapTabKey(event, panelRef.current)
  }

  const authedFetch = useCallback(
    (url: string, init?: RequestInit) => {
      const headers = new Headers(init?.headers)
      headers.set('Content-Type', 'application/json')
      const token = operatorToken.trim()
      if (token) headers.set('Authorization', `Bearer ${token}`)
      return fetch(apiURL(url), { ...init, headers })
    },
    [operatorToken],
  )

  const failMessage = async (res: Response): Promise<string> => {
    if (res.status === 401) return 'Operator token required or invalid'
    return apiErrorMessage(res)
  }

  const loadHistory = useCallback(async () => {
    if (!operatorToken.trim()) {
      setHistory([])
      setHistoryError(null)
      setHistoryLoading(false)
      return
    }
    setHistoryLoading(true)
    setHistoryError(null)
    try {
      const res = await authedFetch('/api/config/desired/history?limit=8', { method: 'GET' })
      if (!res.ok) throw new Error(await failMessage(res))
      const data = (await res.json()) as ListDesiredConfigHistoryResponse
      if (!mountedRef.current) return
      setHistory(data.history ?? [])
    } catch (e) {
      if (mountedRef.current) setHistoryError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      if (mountedRef.current) setHistoryLoading(false)
    }
  }, [authedFetch, operatorToken])

  useEffect(() => {
    void loadHistory()
  }, [loadHistory])

  const applyDesiredToFields = useCallback((desired: DesiredConfig) => {
    const nodeRuntimeOverride = desired.nodeRuntimeProfileOverrides?.[nodeRuntimeProfileKey(nodeId, runtimeType, profile)]
    const runtimeOverride = desired.runtimeProfileOverrides?.[runtimeProfileKey(runtimeType, profile)]
    const nodeOverride = desired.nodeOverrides?.[nodeId]
    const selected = nodeRuntimeOverride ?? runtimeOverride ?? nodeOverride ?? desired.global
    setProvider(selected?.provider ?? '')
    setModel(selected?.model ?? '')
  }, [nodeId, profile, runtimeType])

  const revertHistory = useCallback(async (entry: DesiredConfigHistoryEntry) => {
    if (!operatorToken.trim() || revertingHistoryId) return
    if (!window.confirm(`Revert desired config to ${entry.id}?`)) return
    setRevertingHistoryId(entry.id)
    setHistoryError(null)
    try {
      const res = await authedFetch('/api/config/desired/revert', {
        method: 'POST',
        body: JSON.stringify({ historyId: entry.id }),
      })
      if (!res.ok) throw new Error(await failMessage(res))
      const data = (await res.json()) as RevertDesiredConfigResponse
      if (!mountedRef.current) return
      setHistory((current) => [data.history, ...current.filter((item) => item.id !== data.history.id)])
      applyDesiredToFields(data.desired)
      onApplied()
    } catch (e) {
      if (mountedRef.current) setHistoryError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      if (mountedRef.current) setRevertingHistoryId(null)
    }
  }, [applyDesiredToFields, authedFetch, onApplied, operatorToken, revertingHistoryId])

  const goReview = useCallback(async () => {
    if (!provider.trim() || !model.trim()) {
      setError('Provider and model are required')
      return
    }
    setBusy(true)
    setError(null)
    try {
      const previewReq: EffectiveConfigPreviewRequest = {
        nodeId,
        runtimeType,
        profile,
        desired: { provider: provider.trim(), model: model.trim() },
      }
      const effRes = await authedFetch('/api/config/effective/preview', { method: 'POST', body: JSON.stringify(previewReq) })
      if (!effRes.ok) throw new Error(await failMessage(effRes))
      const effData: EffectiveConfigResponse = await effRes.json()
      if (!mountedRef.current) return
      setReview(effData)
      setStep('Review')
    } catch (e) {
      if (mountedRef.current) setError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      if (mountedRef.current) setBusy(false)
    }
  }, [authedFetch, model, nodeId, profile, provider, runtimeType])

  const pollApply = useCallback(
    async (jobId: string) => {
      for (let attempt = 0; attempt < APPLY_POLL_LIMIT; attempt++) {
        if (!mountedRef.current) return
        await new Promise((resolve) => setTimeout(resolve, APPLY_POLL_MS))
        if (!mountedRef.current) return
        const res = await authedFetch(`/api/nodes/${encodeURIComponent(nodeId)}/jobs`, { method: 'GET' })
        if (!res.ok) continue
        const jobs: Job[] = await res.json()
        const job = jobs.find((item) => item.id === jobId)
        if (!job) continue
        if (job.resultJson?.trim()) {
          try {
            setApplyResult(JSON.parse(job.resultJson) as ConfigApplyResult)
          } catch {
            // Keep the last good result; the status still advances below.
          }
        }
        setApplyStatus(job.status)
        if (job.status === 'completed' || job.status === 'failed') {
          if (job.status === 'failed' && job.error) setError(job.error)
          onApplied()
          return
        }
      }
      if (mountedRef.current) setError('Timed out waiting for the apply job to finish')
    },
    [authedFetch, nodeId, onApplied],
  )

  const startApply = useCallback(async () => {
    if (activeConfigApply) {
      setError('A config apply job is already pending or running for this node.')
      return
    }
    setBusy(true)
    setError(null)
    setApplyResult(null)
    setApplyStatus(null)
    setStep('Apply')
    try {
      const current: DesiredConfig = await fetch(apiURL('/api/config/desired')).then((res) => {
        if (!res.ok) return failMessage(res).then((message) => { throw new Error(message) })
        return res.json()
      })
      const next: DesiredConfig = {
        global: current.global,
        nodeOverrides: current.nodeOverrides,
        runtimeProfileOverrides: current.runtimeProfileOverrides,
        nodeRuntimeProfileOverrides: {
          ...(current.nodeRuntimeProfileOverrides ?? {}),
          [nodeRuntimeProfileKey(nodeId, runtimeType, profile)]: { provider: provider.trim(), model: model.trim() },
        },
      }
      const putRes = await authedFetch('/api/config/desired', { method: 'PUT', body: JSON.stringify(next) })
      if (!putRes.ok) throw new Error(await failMessage(putRes))

      const res = await authedFetch(`/api/nodes/${encodeURIComponent(nodeId)}/config-apply`, {
        method: 'POST',
        body: JSON.stringify({ runtimeType, profile, dryRun }),
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        if (res.status === 409) throw new Error('A config apply job is already pending or running for this node.')
        throw new Error(await failMessage(res))
      }
      const job: Job = await res.json()
      await pollApply(job.id)
    } catch (e) {
      if (mountedRef.current) setError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      if (mountedRef.current) setBusy(false)
    }
  }, [activeConfigApply, authedFetch, dryRun, model, nodeId, pollApply, profile, provider, runtimeType])

  const terminal = applyStatus === 'completed' || applyStatus === 'failed'
  const rollback = rollbackStep(applyResult)
  const terminalCopy = terminal ? applyTerminalMessage(applyStatus, applyResult) : ''

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-black/40" role="dialog" aria-modal="true" aria-labelledby="config-wizard-title" onKeyDown={onDialogKeyDown}>
      <button type="button" aria-label="Close" tabIndex={-1} className="flex-1 cursor-default" onClick={onClose} />
      <div ref={panelRef} tabIndex={-1} className="flex h-full w-full max-w-xl flex-col overflow-y-auto border-l border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-xl outline-none">
        <div className="flex items-start justify-between border-b border-[var(--sp-border)] px-6 py-4">
          <div>
            <div id="config-wizard-title" className="text-lg font-bold tracking-tight">Change configuration</div>
            <div className="mt-0.5 font-mono text-xs text-[var(--sp-muted)]">{nodeId} · {runtimeType}/{profile}</div>
          </div>
          <button type="button" className="rounded-lg border border-[var(--sp-border)] px-2.5 py-1 text-sm text-[var(--sp-muted)] hover:bg-[var(--sp-surface-2)]" onClick={onClose}>
            ✕
          </button>
        </div>

        <StepIndicator current={step} />

        <div className="flex-1 px-6 py-5">
          {error && (
            <div role="alert" className="mb-4 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-sm text-rose-600">{error}</div>
          )}

          {step === 'Edit' && (
            <div className="grid gap-4">
              <Field label="Provider">
                <input
                  className="h-10 w-full rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 font-mono text-sm outline-none focus:border-[var(--sp-accent)]"
                  value={provider}
                  placeholder="anthropic"
                  onChange={(event) => setProvider(event.target.value)}
                />
              </Field>
              <Field label="Model">
                <input
                  className="h-10 w-full rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 font-mono text-sm outline-none focus:border-[var(--sp-accent)]"
                  value={model}
                  placeholder="claude-3.7-sonnet"
                  onChange={(event) => setModel(event.target.value)}
                />
              </Field>
              <ApplyModeToggle dryRun={dryRun} onChange={setDryRun} />
              <DesiredHistoryPanel
                entries={history}
                error={historyError}
                loading={historyLoading}
                revertingId={revertingHistoryId}
                tokenReady={Boolean(operatorToken.trim())}
                onRefresh={loadHistory}
                onRevert={revertHistory}
              />
            </div>
          )}

          {step === 'Review' && (
            <div className="grid gap-4">
              <div className="text-sm text-[var(--sp-muted)]">
                Preview only. Desired state is saved when you run apply, then the server signs the plan for the sidecar.
              </div>
              <div className="grid grid-cols-2 gap-3">
                <Readout label="Desired provider" value={review?.effective.provider || '-'} />
                <Readout label="Desired model" value={review?.effective.model || '-'} />
              </div>
              <div className="rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 py-3">
                <div className="mb-2 text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">Diff vs actual</div>
                {!review || review.diff.length === 0 ? (
                  <div className="text-sm text-emerald-600">No change: actual already matches desired.</div>
                ) : (
                  <div className="grid gap-2">
                    {review.diff.map((entry) => (
                      <div key={`${entry.field}-${entry.change}`} className="grid grid-cols-[1fr_1fr_1fr] gap-2 text-xs">
                        <span className="font-mono font-semibold">{entry.field}</span>
                        <span className="font-mono text-[var(--sp-muted)]">{entry.actual || '-'}</span>
                        <span className="font-mono text-amber-600">→ {entry.desired || '-'}</span>
                      </div>
                    ))}
                  </div>
                )}
              </div>
              <div className={`rounded-lg border px-3 py-2 text-xs ${dryRun ? 'border-sky-500/30 bg-sky-500/10 text-sky-700' : 'border-amber-500/35 bg-amber-500/10 text-amber-700'}`}>
                {dryRun
                  ? 'Dry run: validate the plan without replacing the live config or restarting the runtime.'
                  : 'Live apply: replaces the config and restarts the runtime. Requires the sidecar to run with --allow-live-apply; otherwise it fails safely before any change.'}
              </div>
            </div>
          )}

          {(step === 'Apply' || step === 'Done') && (
            <div className="grid gap-4">
              <div className="text-sm text-[var(--sp-muted)]">
                Plan → diff → sign → sidecar → backup → validate → replace → restart → health check
              </div>
              <div className="grid gap-1.5">
                {PIPELINE_STEPS.map((entry) => (
                  <PipelineRow key={entry.name} label={entry.label} status={stepStatus(applyResult, entry.name)} />
                ))}
                {rollback && <PipelineRow label="Rollback" status={rollback.status} />}
              </div>
              {terminal && (
                <div className={`rounded-lg border px-3 py-2 text-sm ${applyStatus === 'completed' ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700' : 'border-rose-500/30 bg-rose-500/10 text-rose-600'}`}>
                  {terminalCopy}
                </div>
              )}
            </div>
          )}
        </div>

        <div className="flex items-center justify-between border-t border-[var(--sp-border)] px-6 py-4">
          <button type="button" className="rounded-lg px-3 py-2 text-sm text-[var(--sp-muted)] hover:bg-[var(--sp-surface-2)]" onClick={onClose}>
            Close
          </button>
          <div className="flex gap-2">
            {step === 'Edit' && (
              <PrimaryButton disabled={busy} label={busy ? 'Loading…' : 'Preview'} onClick={goReview} />
            )}
            {step === 'Review' && (
              <>
                <SecondaryButton disabled={busy} label="Back" onClick={() => setStep('Edit')} />
                <PrimaryButton disabled={busy || activeConfigApply} label={activeConfigApply ? 'Apply in progress' : dryRun ? 'Save and run dry-run' : 'Save and run live apply'} onClick={startApply} />
              </>
            )}
            {step === 'Apply' && terminal && (
              <PrimaryButton disabled={busy} label="Done" onClick={() => setStep('Done')} />
            )}
            {step === 'Done' && <PrimaryButton disabled={false} label="Close" onClick={onClose} />}
          </div>
        </div>
      </div>
    </div>
  )
}

// trapTabKey keeps Tab/Shift+Tab focus cycling inside the given container so a
// modal dialog does not leak focus to the page behind it.
function trapTabKey(event: React.KeyboardEvent, container: HTMLElement | null) {
  if (!container) return
  const focusable = container.querySelectorAll<HTMLElement>(
    'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
  )
  if (focusable.length === 0) return
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  const active = document.activeElement
  if (event.shiftKey) {
    if (active === first || !container.contains(active)) {
      event.preventDefault()
      last.focus()
    }
  } else if (active === last) {
    event.preventDefault()
    first.focus()
  }
}

function nodeRuntimeProfileKey(nodeId: string, runtimeType: string, profile: string): string {
  const trimmedNodeId = nodeId.trim()
  const target = runtimeProfileKey(runtimeType, profile)
  if (!trimmedNodeId) return target
  if (!target) return trimmedNodeId
  return `${trimmedNodeId}/${target}`
}

function runtimeProfileKey(runtimeType: string, profile: string): string {
  const trimmedRuntimeType = runtimeType.trim()
  const trimmedProfile = profile.trim()
  if (!trimmedRuntimeType) return trimmedProfile
  if (!trimmedProfile) return trimmedRuntimeType
  return `${trimmedRuntimeType}/${trimmedProfile}`
}

function desiredHistoryLabel(desired: DesiredConfig): string {
  const provider = desired.global?.provider?.trim()
  const model = desired.global?.model?.trim()
  if (provider && model) return `${provider}/${model}`
  if (provider) return provider
  if (model) return model
  return 'no global default'
}

function stepStatus(result: ConfigApplyResult | null, name: string): string {
  const step = result?.steps.find((entry) => entry.name === name)
  return step?.status ?? 'pending'
}

function rollbackStep(result: ConfigApplyResult | null): ConfigApplyStep | undefined {
  return result?.steps.find((entry) => entry.name === 'rolled_back')
}

function rollbackOutcome(result: ConfigApplyResult | null): 'completed' | 'failed' | 'not_recorded' | 'unknown' {
  if (!result) return 'unknown'
  const step = rollbackStep(result)
  if (!step) return 'not_recorded'
  if (step.status === 'completed') return 'completed'
  if (step.status === 'failed') return 'failed'
  return 'unknown'
}

function applyTerminalMessage(status: Job['status'] | null, result: ConfigApplyResult | null): string {
  if (status === 'completed') {
    return result?.dryRun ? 'Dry run completed. No live change was made.' : 'Live apply completed.'
  }
  if (status !== 'failed') return ''
  switch (rollbackOutcome(result)) {
    case 'completed':
      return 'Apply failed. Rollback completed and the previous config was restored.'
    case 'failed':
      return 'Apply failed. Rollback failed; inspect the job result before retrying.'
    case 'not_recorded':
      return 'Apply failed before rollback was recorded.'
    case 'unknown':
      return 'Apply failed. Rollback status is unknown because no result was returned.'
  }
}

function StepIndicator({ current }: { current: WizardStep }) {
  const currentIndex = WIZARD_STEPS.indexOf(current)
  return (
    <div className="flex gap-1 border-b border-[var(--sp-border)] px-6 py-3">
      {WIZARD_STEPS.map((label, index) => (
        <div key={label} className="flex items-center gap-2">
          <span className={`flex h-6 w-6 items-center justify-center rounded-full text-xs font-semibold ${index <= currentIndex ? 'bg-[var(--sp-accent)] text-white' : 'bg-[var(--sp-surface-2)] text-[var(--sp-faint)]'}`}>
            {index + 1}
          </span>
          <span className={`text-xs font-medium ${index <= currentIndex ? 'text-[var(--sp-text)]' : 'text-[var(--sp-faint)]'}`}>{label}</span>
          {index < WIZARD_STEPS.length - 1 && <span className="mx-1 text-[var(--sp-faint)]">›</span>}
        </div>
      ))}
    </div>
  )
}

function PipelineRow({ label, status }: { label: string; status: string }) {
  const { icon, tone } = pipelineVisual(status)
  return (
    <div className="flex items-center gap-3 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 py-2 text-sm">
      <span className={`flex h-5 w-5 flex-none items-center justify-center rounded-full text-[11px] font-bold ${tone}`}>{icon}</span>
      <span className="text-[var(--sp-text)]">{label}</span>
      <span className="ml-auto text-xs text-[var(--sp-faint)]">{status}</span>
    </div>
  )
}

function pipelineVisual(status: string): { icon: string; tone: string } {
  switch (status) {
    case 'completed':
      return { icon: '✓', tone: 'bg-emerald-500/15 text-emerald-600' }
    case 'failed':
      return { icon: '✕', tone: 'bg-rose-500/15 text-rose-600' }
    case 'skipped':
      return { icon: '–', tone: 'bg-[var(--sp-surface-3)] text-[var(--sp-faint)]' }
    default:
      return { icon: '○', tone: 'bg-[var(--sp-surface-3)] text-[var(--sp-faint)]' }
  }
}

function DesiredHistoryPanel({
  entries,
  error,
  loading,
  revertingId,
  tokenReady,
  onRefresh,
  onRevert,
}: {
  entries: DesiredConfigHistoryEntry[]
  error: string | null
  loading: boolean
  revertingId: string | null
  tokenReady: boolean
  onRefresh: () => void
  onRevert: (entry: DesiredConfigHistoryEntry) => void
}) {
  return (
    <section className="rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)]">
      <div className="flex items-center justify-between border-b border-[var(--sp-border)] px-3 py-2">
        <div>
          <div className="text-sm font-semibold">Desired config history</div>
          <div className="text-[11px] text-[var(--sp-faint)]">Past saved desired states</div>
        </div>
        <button
          type="button"
          className="h-8 rounded-lg border border-[var(--sp-border-strong)] px-2.5 text-xs font-medium hover:bg-[var(--sp-surface)] disabled:cursor-not-allowed disabled:opacity-55"
          disabled={!tokenReady || loading}
          onClick={onRefresh}
        >
          {loading ? 'Loading...' : 'Refresh'}
        </button>
      </div>
      {error && (
        <div className="border-b border-rose-500/20 bg-rose-500/10 px-3 py-2 text-xs text-rose-600">
          {error}
        </div>
      )}
      <div className="grid divide-y divide-[var(--sp-border)]">
        {entries.length === 0 && (
          <div className="px-3 py-4 text-sm text-[var(--sp-muted)]">
            {loading ? 'Loading history...' : tokenReady ? 'No desired config history yet.' : 'Set an operator token to load history.'}
          </div>
        )}
        {entries.map((entry) => (
          <div key={entry.id} className="grid gap-2 px-3 py-3">
            <div className="flex flex-col gap-1 sm:flex-row sm:items-center sm:justify-between">
              <div className="min-w-0">
                <div className="truncate font-mono text-xs text-[var(--sp-text)]">{entry.id}</div>
                <div className="mt-1 text-xs text-[var(--sp-muted)]">{desiredHistoryLabel(entry.config)} · {formatDate(entry.updatedAt)}</div>
              </div>
              <button
                type="button"
                className="h-8 rounded-lg border border-amber-500/35 bg-amber-500/10 px-2.5 text-xs font-medium text-amber-700 hover:bg-amber-500/15 disabled:cursor-not-allowed disabled:opacity-55"
                disabled={!tokenReady || Boolean(revertingId)}
                onClick={() => onRevert(entry)}
              >
                {revertingId === entry.id ? 'Reverting...' : 'Revert'}
              </button>
            </div>
            <div className="font-mono text-[11px] text-[var(--sp-faint)]">{entry.desiredHash || '-'}</div>
          </div>
        ))}
      </div>
    </section>
  )
}

function ApplyModeToggle({ dryRun, onChange }: { dryRun: boolean; onChange: (value: boolean) => void }) {
  return (
    <div className="grid gap-2">
      <div className="text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">Apply mode</div>
      <div className="flex gap-2">
        <ModeOption active={dryRun} label="Dry run" hint="safe, default" onClick={() => onChange(true)} />
        <ModeOption active={!dryRun} label="Live apply" hint="needs operator flag" onClick={() => onChange(false)} />
      </div>
    </div>
  )
}

function ModeOption({ active, label, hint, onClick }: { active: boolean; label: string; hint: string; onClick: () => void }) {
  return (
    <button
      type="button"
      className={`flex-1 rounded-lg border px-3 py-2 text-left text-sm ${active ? 'border-[var(--sp-accent)] bg-[var(--sp-accent)]/10' : 'border-[var(--sp-border)] bg-[var(--sp-surface-2)] hover:border-[var(--sp-border-strong)]'}`}
      onClick={onClick}
    >
      <div className="font-medium">{label}</div>
      <div className="text-[11px] text-[var(--sp-faint)]">{hint}</div>
    </button>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="grid gap-1.5">
      <span className="text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">{label}</span>
      {children}
    </label>
  )
}

function Readout({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 py-2">
      <div className="text-[11px] text-[var(--sp-faint)]">{label}</div>
      <div className="mt-1 truncate font-mono text-sm">{value}</div>
    </div>
  )
}

function PrimaryButton({ disabled, label, onClick }: { disabled: boolean; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      className="h-9 rounded-lg bg-[var(--sp-accent)] px-4 text-sm font-semibold text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-60"
      disabled={disabled}
      onClick={onClick}
    >
      {label}
    </button>
  )
}

function SecondaryButton({ disabled, label, onClick }: { disabled: boolean; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      className="h-9 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-4 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
      disabled={disabled}
      onClick={onClick}
    >
      {label}
    </button>
  )
}
