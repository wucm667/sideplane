import { useEffect, useMemo, useState, type FormEvent, type ReactNode } from 'react'
import { apiURL, formatDate, formatRelativeTime, rolloutBadgeClasses } from '../helpers.ts'
import type { CreateRolloutRequest, ListRolloutTemplatesResponse, NodeStatus, Rollout, RolloutAction, RolloutBatch, RolloutNodeProgress, RolloutTemplate } from '../types.ts'
import { TableMessage } from './FleetOverview.tsx'

interface RolloutsViewProps {
  actioningId: string | null
  creating: boolean
  error: string | null
  loading: boolean
  nodes: NodeStatus[]
  operatorToken: string
  rollouts: Rollout[]
  onAction: (rolloutId: string, action: RolloutAction) => Promise<Rollout | null>
  onCreate: (request: CreateRolloutRequest) => Promise<Rollout | null>
  onOpenNode: (nodeId: string) => void
  onRefresh: () => void
}

export function RolloutsView({
  actioningId,
  creating,
  error,
  loading,
  nodes,
  operatorToken,
  rollouts,
  onAction,
  onCreate,
  onOpenNode,
  onRefresh,
}: RolloutsViewProps) {
  const tokenReady = operatorToken.trim().length > 0
  const [selectedRolloutId, setSelectedRolloutId] = useState<string | null>(null)
  const selectedRollout = useMemo(() => {
    if (selectedRolloutId) {
      return rollouts.find((rollout) => rollout.id === selectedRolloutId) ?? rollouts[0] ?? null
    }
    return rollouts[0] ?? null
  }, [rollouts, selectedRolloutId])

  useEffect(() => {
    if (selectedRollout?.id && selectedRollout.id !== selectedRolloutId) {
      setSelectedRolloutId(selectedRollout.id)
    }
  }, [selectedRollout, selectedRolloutId])

  return (
    <div className="mx-auto max-w-6xl px-4 py-6 sm:px-6 lg:px-9 lg:py-8">
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Rollouts</h1>
          <div className="mt-1 text-sm text-[var(--sp-muted)]">{rollouts.length} staged changes · {activeRolloutCount(rollouts)} active</div>
        </div>
        <button
          type="button"
          className="h-9 w-fit rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
          disabled={loading}
          onClick={onRefresh}
        >
          {loading ? 'Refreshing' : 'Refresh'}
        </button>
      </div>

      {error && (
        <div className="mb-5 rounded-xl border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-600">
          {error}
        </div>
      )}

      <RolloutCreateForm
        creating={creating}
        tokenReady={tokenReady}
        operatorToken={operatorToken}
        onCreate={async (request) => {
          const rollout = await onCreate(request)
          if (rollout) {
            setSelectedRolloutId(rollout.id)
          }
          return rollout
        }}
      />

      <div className="mt-6 grid gap-6 xl:grid-cols-[minmax(0,0.95fr)_minmax(0,1.35fr)]">
        <section className="overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-sm">
          <div className="grid grid-cols-[1fr_auto_auto] gap-3 border-b border-[var(--sp-border)] px-4 py-3 text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">
            <div>Rollout</div>
            <div>State</div>
            <div>Updated</div>
          </div>
          {loading && <TableMessage message="Loading rollouts…" />}
          {!loading && rollouts.length === 0 && <TableMessage message={tokenReady ? 'No rollouts yet.' : 'Operator token required.'} />}
          {!loading && rollouts.map((rollout) => (
            <button
              key={rollout.id}
              type="button"
              className={`grid w-full grid-cols-[1fr_auto_auto] gap-3 border-b border-[var(--sp-border)] px-4 py-3 text-left text-xs last:border-b-0 hover:bg-[var(--sp-surface-2)] ${selectedRollout?.id === rollout.id ? 'bg-[var(--sp-surface-2)]' : ''}`}
              onClick={() => setSelectedRolloutId(rollout.id)}
            >
              <span className="min-w-0">
                <span className="block truncate font-mono font-semibold text-[var(--sp-text)]">{rollout.id}</span>
                <span className="mt-1 block truncate text-[var(--sp-faint)]">{rolloutTargetLabel(rollout)} · {rolloutRuntimeLabel(rollout)}</span>
              </span>
              <RolloutStateBadge rollout={rollout} />
              <span className="text-[var(--sp-faint)]" title={formatDate(rollout.updatedAt)}>{formatRelativeTime(rollout.updatedAt)}</span>
            </button>
          ))}
        </section>

        <section className="min-w-0">
          {selectedRollout ? (
            <RolloutDetail
              actioningId={actioningId}
              nodes={nodes}
              rollout={selectedRollout}
              tokenReady={tokenReady}
              onAction={async (action) => {
                const updated = await onAction(selectedRollout.id, action)
                if (updated) {
                  setSelectedRolloutId(updated.id)
                }
              }}
              onOpenNode={onOpenNode}
            />
          ) : (
            <div className="rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] px-4 py-10 text-center text-sm text-[var(--sp-muted)]">
              Select a rollout to inspect progress.
            </div>
          )}
        </section>
      </div>
    </div>
  )
}

function RolloutCreateForm({
  creating,
  tokenReady,
  operatorToken,
  onCreate,
}: {
  creating: boolean
  tokenReady: boolean
  operatorToken: string
  onCreate: (request: CreateRolloutRequest) => Promise<Rollout | null>
}) {
  const [selector, setSelector] = useState('')
  const [nodeIds, setNodeIds] = useState('')
  const [provider, setProvider] = useState('')
  const [model, setModel] = useState('')
  const [runtimeType, setRuntimeType] = useState('hermes')
  const [profile, setProfile] = useState('default')
  const [batchSize, setBatchSize] = useState(1)
  const [startAt, setStartAt] = useState('')
  const [live, setLive] = useState(false)
  const [confirmedLive, setConfirmedLive] = useState(false)
  const [autoRollback, setAutoRollback] = useState(false)
  const [allowOverlap, setAllowOverlap] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [templates, setTemplates] = useState<RolloutTemplate[]>([])
  const [selectedTemplate, setSelectedTemplate] = useState('')
  const [templateName, setTemplateName] = useState('')
  const [templateMessage, setTemplateMessage] = useState<string | null>(null)

  const authHeaders = (): HeadersInit => {
    const headers: HeadersInit = { 'Content-Type': 'application/json' }
    const token = operatorToken.trim()
    if (token) headers.Authorization = `Bearer ${token}`
    return headers
  }

  const loadTemplates = async () => {
    if (operatorToken.trim() === '') {
      setTemplates([])
      return
    }
    try {
      const res = await fetch(apiURL('/api/rollout-templates'), { headers: authHeaders() })
      if (!res.ok) return
      const data = (await res.json()) as ListRolloutTemplatesResponse
      setTemplates(data.templates ?? [])
    } catch {
      // best-effort; templates are optional
    }
  }

  useEffect(() => {
    void loadTemplates()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [operatorToken])

  const buildSpec = (): CreateRolloutRequest['spec'] | string => {
    const parsedSelector = parseSelector(selector)
    if (typeof parsedSelector === 'string') return parsedSelector
    const nodes = parseNodeIds(nodeIds)
    if (Object.keys(parsedSelector).length === 0 && nodes.length === 0) return 'selector or node required'
    if (Object.keys(parsedSelector).length > 0 && nodes.length > 0) return 'selector and nodes conflict'
    if (!provider.trim() || !model.trim()) return 'provider and model required'
    if (batchSize <= 0) return 'batch size must be positive'
    const parsedStartAt = parseStartAtInput(startAt)
    if (parsedStartAt.error) return parsedStartAt.error
    return {
      selector: Object.keys(parsedSelector).length > 0 ? parsedSelector : undefined,
      nodeIds: nodes.length > 0 ? nodes : undefined,
      runtimeType: runtimeType.trim() || 'hermes',
      profile: profile.trim(),
      target: { provider: provider.trim(), model: model.trim() },
      batchSize,
      startAt: parsedStartAt.value,
      live,
      autoRollbackOnFailure: live ? autoRollback : undefined,
      allowOverlap: allowOverlap || undefined,
    }
  }

  const saveAsTemplate = async () => {
    if (!tokenReady) return
    const name = templateName.trim()
    if (name === '') {
      setFormError('template name required')
      return
    }
    const spec = buildSpec()
    if (typeof spec === 'string') {
      setFormError(spec)
      return
    }
    setFormError(null)
    setTemplateMessage(null)
    try {
      const res = await fetch(apiURL('/api/rollout-templates'), {
        method: 'POST',
        headers: authHeaders(),
        body: JSON.stringify({ name, spec }),
      })
      if (!res.ok) {
        if (res.status === 403) throw new Error('Operator token is read-only')
        throw new Error('save template failed')
      }
      setTemplateName('')
      setTemplateMessage('Template saved')
      await loadTemplates()
    } catch (e) {
      setFormError(e instanceof Error ? e.message : 'Unknown error')
    }
  }

  const createFromTemplate = async () => {
    if (!tokenReady || selectedTemplate === '') return
    setFormError(null)
    setTemplateMessage(null)
    await onCreate({ templateId: selectedTemplate, spec: { runtimeType: 'hermes', target: { provider: '', model: '' }, live: false } })
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const parsedSelector = parseSelector(selector)
    if (typeof parsedSelector === 'string') {
      setFormError(parsedSelector)
      return
    }
    const nodes = parseNodeIds(nodeIds)
    if (Object.keys(parsedSelector).length === 0 && nodes.length === 0) {
      setFormError('selector or node required')
      return
    }
    if (Object.keys(parsedSelector).length > 0 && nodes.length > 0) {
      setFormError('selector and nodes conflict')
      return
    }
    if (!provider.trim() || !model.trim()) {
      setFormError('provider and model required')
      return
    }
    if (batchSize <= 0) {
      setFormError('batch size must be positive')
      return
    }
    const parsedStartAt = parseStartAtInput(startAt)
    if (parsedStartAt.error) {
      setFormError(parsedStartAt.error)
      return
    }
    if (live && !confirmedLive) {
      setFormError('confirm live rollout')
      return
    }

    setFormError(null)
    const rollout = await onCreate({
      spec: {
        selector: Object.keys(parsedSelector).length > 0 ? parsedSelector : undefined,
        nodeIds: nodes.length > 0 ? nodes : undefined,
        runtimeType: runtimeType.trim() || 'hermes',
        profile: profile.trim(),
        target: { provider: provider.trim(), model: model.trim() },
        batchSize,
        startAt: parsedStartAt.value,
        live,
        autoRollbackOnFailure: live ? autoRollback : undefined,
        allowOverlap: allowOverlap || undefined,
      },
    })
    if (rollout) {
      setConfirmedLive(false)
      setAutoRollback(false)
      setAllowOverlap(false)
    }
  }

  return (
    <form className="rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] p-4 shadow-sm" onSubmit={submit}>
      <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="text-sm font-semibold">New rollout</div>
        <button
          type="submit"
          className="h-9 rounded-lg bg-[var(--sp-accent)] px-3 text-sm font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-55"
          disabled={!tokenReady || creating}
          title={!tokenReady ? 'operator token required' : live && !confirmedLive ? 'confirm live rollout' : 'create rollout'}
        >
          {creating ? 'Creating…' : 'Create rollout'}
        </button>
      </div>
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        <Field label="Selector">
          <input className={inputClassName} value={selector} placeholder="role=canary,zone=lab" onChange={(event) => setSelector(event.target.value)} />
        </Field>
        <Field label="Nodes">
          <input className={inputClassName} value={nodeIds} placeholder="node-a,node-b" onChange={(event) => setNodeIds(event.target.value)} />
        </Field>
        <Field label="Provider">
          <input className={inputClassName} value={provider} placeholder="openai" onChange={(event) => setProvider(event.target.value)} />
        </Field>
        <Field label="Model">
          <input className={inputClassName} value={model} placeholder="gpt-4o" onChange={(event) => setModel(event.target.value)} />
        </Field>
        <Field label="Runtime">
          <select className={inputClassName} value={runtimeType} onChange={(event) => setRuntimeType(event.target.value)}>
            <option value="hermes">hermes</option>
            <option value="openclaw">openclaw</option>
          </select>
        </Field>
        <Field label="Profile">
          <input className={inputClassName} value={profile} placeholder="default" onChange={(event) => setProfile(event.target.value)} />
        </Field>
        <Field label="Batch">
          <input className={inputClassName} type="number" min={1} value={batchSize} onChange={(event) => setBatchSize(Number(event.target.value))} />
        </Field>
        <Field label="Start">
          <input className={inputClassName} type="datetime-local" value={startAt} onChange={(event) => setStartAt(event.target.value)} />
        </Field>
        <div className="grid gap-2">
          <label className="flex h-9 items-center gap-2 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 text-xs text-[var(--sp-muted)]">
            <input type="checkbox" checked={live} onChange={(event) => setLive(event.target.checked)} />
            live
          </label>
          {live && (
            <label className="flex h-9 items-center gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 text-xs text-amber-700">
              <input type="checkbox" checked={confirmedLive} onChange={(event) => setConfirmedLive(event.target.checked)} />
              confirm
            </label>
          )}
          {live && (
            <label className="flex h-9 items-center gap-2 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 text-xs text-[var(--sp-muted)]">
              <input type="checkbox" checked={autoRollback} onChange={(event) => setAutoRollback(event.target.checked)} />
              auto-rollback
            </label>
          )}
          <label className="flex h-9 items-center gap-2 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 text-xs text-[var(--sp-muted)]">
            <input type="checkbox" checked={allowOverlap} onChange={(event) => setAllowOverlap(event.target.checked)} />
            allow overlap
          </label>
        </div>
      </div>
      <div className="mt-4 flex flex-wrap items-center gap-2 border-t border-[var(--sp-border)] pt-3">
        <select
          className={inputClassName + ' max-w-xs'}
          value={selectedTemplate}
          aria-label="rollout template"
          onChange={(event) => setSelectedTemplate(event.target.value)}
        >
          <option value="">Use a template…</option>
          {templates.map((template) => (
            <option key={template.id} value={template.id}>{template.name}</option>
          ))}
        </select>
        <button
          type="button"
          className="h-9 rounded-lg border border-[var(--sp-border-strong)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-55"
          disabled={!tokenReady || creating || selectedTemplate === ''}
          onClick={createFromTemplate}
        >
          Create from template
        </button>
        <input
          className={inputClassName + ' max-w-[12rem]'}
          value={templateName}
          placeholder="save as template name"
          onChange={(event) => setTemplateName(event.target.value)}
        />
        <button
          type="button"
          className="h-9 rounded-lg border border-[var(--sp-border-strong)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-55"
          disabled={!tokenReady || templateName.trim() === ''}
          onClick={saveAsTemplate}
        >
          Save as template
        </button>
      </div>
      {templateMessage && <div className="mt-2 text-xs text-emerald-600">{templateMessage}</div>}
      {formError && <div className="mt-3 text-xs text-rose-600">{formError}</div>}
    </form>
  )
}

function RolloutDetail({
  actioningId,
  nodes,
  rollout,
  tokenReady,
  onAction,
  onOpenNode,
}: {
  actioningId: string | null
  nodes: NodeStatus[]
  rollout: Rollout
  tokenReady: boolean
  onAction: (action: RolloutAction) => void
  onOpenNode: (nodeId: string) => void
}) {
  const terminal = isTerminalRollout(rollout)
  const actioning = actioningId === rollout.id
  const nodeMap = useMemo(() => new Map(nodes.map((node) => [node.nodeId, node])), [nodes])

  return (
    <div className="overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-sm">
      <div className="border-b border-[var(--sp-border)] px-4 py-4">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="truncate font-mono text-sm font-semibold">{rollout.id}</h2>
              <RolloutStateBadge rollout={rollout} />
              <span className="rounded border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-2 py-0.5 text-[11px] font-semibold text-[var(--sp-muted)]">
                {rollout.spec.live ? 'live' : 'dry-run'}
              </span>
              {rollout.spec.autoRollbackOnFailure && (
                <span className="rounded border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-[11px] font-semibold text-amber-700">
                  auto-rollback
                </span>
              )}
              {rollout.spec.allowOverlap && (
                <span className="rounded border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-2 py-0.5 text-[11px] font-semibold text-[var(--sp-muted)]">
                  overlap allowed
                </span>
              )}
            </div>
            <div className="mt-2 text-xs text-[var(--sp-muted)]">{rolloutTargetLabel(rollout)} · {rolloutRuntimeLabel(rollout)} · batch {rollout.spec.batchSize || 1}</div>
            {rollout.pauseReason && <div className="mt-2 text-xs text-amber-700">{rollout.pauseReason}</div>}
            {(rollout.failingNodeIds ?? []).length > 0 && (
              <div className="mt-1 font-mono text-[11px] text-rose-600">{rollout.failingNodeIds?.join(',')}</div>
            )}
          </div>
          <div className="flex flex-wrap gap-2">
            <ActionButton
              disabled={!tokenReady || actioning || terminal || rollout.state === 'paused' || rollout.state === 'scheduled'}
              label="Pause"
              reason={!tokenReady ? 'operator token required' : terminal ? 'terminal rollout' : rollout.state === 'paused' ? 'already paused' : rollout.state === 'scheduled' ? 'abort scheduled rollouts before start' : 'pause rollout'}
              onClick={() => onAction('pause')}
            />
            <ActionButton
              disabled={!tokenReady || actioning || rollout.state !== 'paused'}
              label="Resume"
              reason={!tokenReady ? 'operator token required' : rollout.state !== 'paused' ? 'rollout is not paused' : 'resume rollout'}
              onClick={() => onAction('resume')}
            />
            <ActionButton
              danger
              disabled={!tokenReady || actioning || terminal}
              label="Abort"
              reason={!tokenReady ? 'operator token required' : terminal ? 'terminal rollout' : 'abort rollout'}
              onClick={() => onAction('abort')}
            />
          </div>
        </div>
      </div>

      <div className="grid gap-px bg-[var(--sp-border)] sm:grid-cols-4">
        <Metric label="Created" value={formatRelativeTime(rollout.createdAt)} title={formatDate(rollout.createdAt)} />
        <Metric label="Start" value={rollout.spec.startAt ? formatDate(rollout.spec.startAt) : 'immediate'} />
        <Metric label="Updated" value={formatRelativeTime(rollout.updatedAt)} title={formatDate(rollout.updatedAt)} />
        <Metric label="Batches" value={batchProgress(rollout)} />
      </div>

      <div className="px-4 py-4">
        <div className="mb-3 text-xs font-semibold uppercase tracking-[0.14em] text-[var(--sp-faint)]">Batches</div>
        <div className="grid gap-3">
          {rollout.batches.map((batch) => (
            <BatchPanel key={batch.index} batch={batch} nodeMap={nodeMap} onOpenNode={onOpenNode} />
          ))}
        </div>
      </div>
    </div>
  )
}

function BatchPanel({ batch, nodeMap, onOpenNode }: { batch: RolloutBatch; nodeMap: Map<string, NodeStatus>; onOpenNode: (nodeId: string) => void }) {
  return (
    <div className="overflow-hidden rounded-lg border border-[var(--sp-border)]">
      <div className="flex items-center justify-between border-b border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 py-2">
        <div className="font-mono text-xs font-semibold">batch {batch.index}</div>
        <span className="text-xs text-[var(--sp-muted)]">{batch.state}</span>
      </div>
      <div className="divide-y divide-[var(--sp-border)]">
        {batch.nodeIds.map((nodeId) => (
          <NodeProgressRow key={nodeId} nodeId={nodeId} node={nodeMap.get(nodeId)} progress={batch.nodes[nodeId]} onOpenNode={onOpenNode} />
        ))}
      </div>
    </div>
  )
}

function NodeProgressRow({ nodeId, node, progress, onOpenNode }: { nodeId: string; node?: NodeStatus; progress?: RolloutNodeProgress; onOpenNode: (nodeId: string) => void }) {
  const state = progress?.state ?? 'pending'
  return (
    <div className="grid gap-2 px-3 py-3 text-xs sm:grid-cols-[1fr_auto_1fr_auto] sm:items-center">
      <button type="button" className="min-w-0 truncate text-left font-mono font-semibold text-[var(--sp-text)] hover:underline" onClick={() => onOpenNode(nodeId)}>
        {nodeId}
      </button>
      <span className={`inline-flex w-fit rounded border px-2 py-0.5 font-semibold ${nodeProgressBadgeClasses(state)}`}>{state}</span>
      <div className="min-w-0">
        {progress?.jobId ? (
          <button type="button" className="max-w-full truncate font-mono text-[var(--sp-muted)] hover:underline" onClick={() => onOpenNode(nodeId)}>
            {progress.jobId}
          </button>
        ) : (
          <span className="font-mono text-[var(--sp-faint)]">-</span>
        )}
        {progress?.lastError && <div className="mt-1 truncate text-rose-600" title={progress.lastError}>{progress.lastError}</div>}
        {progress?.rollbackJobId ? (
          <button
            type="button"
            className="mt-1 flex max-w-full items-center gap-1 truncate font-mono text-amber-700 hover:underline"
            title={`rolled back via ${progress.rollbackJobId}`}
            onClick={() => onOpenNode(nodeId)}
          >
            <span className="text-[10px] font-semibold uppercase tracking-wide">rollback</span>
            {progress.rollbackJobId}
          </button>
        ) : (
          progress?.rolledBack && <div className="mt-1 text-[10px] font-semibold uppercase tracking-wide text-amber-700">rollback attempted</div>
        )}
      </div>
      <div className="flex justify-start sm:justify-end">
        {node?.drift ? (
          <span className="rounded bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-semibold text-amber-600">drift</span>
        ) : (
          <span className="font-mono text-[var(--sp-faint)]">-</span>
        )}
      </div>
    </div>
  )
}

function RolloutStateBadge({ rollout }: { rollout: Rollout }) {
  return (
    <span className={`inline-flex w-fit items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-semibold ${rolloutBadgeClasses(rollout.state)}`}>
      <span className="h-1.5 w-1.5 rounded-full bg-current" />
      {rollout.state}
    </span>
  )
}

function ActionButton({ danger = false, disabled, label, reason, onClick }: { danger?: boolean; disabled: boolean; label: string; reason: string; onClick: () => void }) {
  return (
    <button
      type="button"
      className={`h-8 rounded-lg border px-3 text-xs font-medium disabled:cursor-not-allowed disabled:opacity-55 ${danger ? 'border-rose-500/40 bg-rose-500/10 text-rose-600 hover:bg-rose-500/15' : 'border-[var(--sp-border-strong)] bg-[var(--sp-surface)] text-[var(--sp-text)] hover:bg-[var(--sp-surface-2)]'}`}
      disabled={disabled}
      title={reason}
      onClick={onClick}
    >
      {label}
    </button>
  )
}

function Metric({ label, value, title }: { label: string; value: string; title?: string }) {
  return (
    <div className="bg-[var(--sp-surface)] px-4 py-4">
      <div className="text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">{label}</div>
      <div className="mt-2 truncate text-sm font-medium text-[var(--sp-text)]" title={title || value}>{value}</div>
    </div>
  )
}

function Field({ children, label }: { children: ReactNode; label: string }) {
  return (
    <label className="grid gap-1.5 text-xs text-[var(--sp-muted)]">
      <span>{label}</span>
      {children}
    </label>
  )
}

const inputClassName = 'h-9 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)]'

function parseSelector(value: string): Record<string, string> | string {
  const trimmed = value.trim()
  if (!trimmed) return {}
  const labels: Record<string, string> = {}
  for (const part of trimmed.split(',')) {
    const entry = part.trim()
    if (!entry) return 'selector entry is empty'
    const index = entry.indexOf('=')
    if (index <= 0) return `invalid selector ${entry}`
    const key = entry.slice(0, index).trim()
    if (labels[key] !== undefined) return `duplicate selector ${key}`
    labels[key] = entry.slice(index + 1).trim()
  }
  return labels
}

function parseNodeIds(value: string): string[] {
  const seen = new Set<string>()
  const result: string[] = []
  for (const item of value.split(/[,\s]+/)) {
    const nodeId = item.trim()
    if (!nodeId || seen.has(nodeId)) continue
    seen.add(nodeId)
    result.push(nodeId)
  }
  return result
}

function parseStartAtInput(value: string): { value?: string; error?: string } {
  const trimmed = value.trim()
  if (!trimmed) return {}
  const date = new Date(trimmed)
  if (Number.isNaN(date.getTime())) return { error: 'start time is invalid' }
  return { value: date.toISOString() }
}

function rolloutTargetLabel(rollout: Rollout): string {
  const provider = rollout.spec.target.provider || '-'
  const model = rollout.spec.target.model || '-'
  return `${provider}/${model}`
}

function rolloutRuntimeLabel(rollout: Rollout): string {
  const runtime = rollout.spec.runtimeType || '-'
  return rollout.spec.profile ? `${runtime}/${rollout.spec.profile}` : runtime
}

function activeRolloutCount(rollouts: Rollout[]): number {
  return rollouts.filter((rollout) => !isTerminalRollout(rollout) && rollout.state !== 'paused').length
}

function isTerminalRollout(rollout: Rollout): boolean {
  return rollout.state === 'completed' || rollout.state === 'aborted' || rollout.state === 'failed'
}

function batchProgress(rollout: Rollout): string {
  if (rollout.batches.length === 0) return '0/0'
  const completed = rollout.batches.filter((batch) => batch.state === 'completed').length
  return `${completed}/${rollout.batches.length}`
}

function nodeProgressBadgeClasses(state: RolloutNodeProgress['state']): string {
  switch (state) {
    case 'dispatched':
      return 'border-sky-500/30 bg-sky-500/10 text-sky-600'
    case 'succeeded':
      return 'border-emerald-500/25 bg-emerald-500/10 text-emerald-600'
    case 'failed':
    case 'timed_out':
    case 'offline':
      return 'border-rose-500/30 bg-rose-500/10 text-rose-600'
    default:
      return 'border-[var(--sp-border)] bg-[var(--sp-surface-2)] text-[var(--sp-muted)]'
  }
}
