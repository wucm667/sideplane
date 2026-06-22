import { useCallback, useEffect, useState } from 'react'
import { apiErrorMessage, apiURL, formatDate, sideplaneServerURL } from '../helpers.ts'
import { useT } from '../i18n.ts'
import type { AlertEventType, AlertWebhook, AlertWebhookKind, CreateAlertWebhookResponse, CreateEnrollmentTokenResponse, CreateOperatorTokenResponse, ListAlertWebhooksResponse, ListOperatorTokensResponse, OperatorToken, OperatorTokenScope, RevokeOperatorTokenResponse, ServerSettings } from '../types.ts'

const ALERT_EVENT_TYPES: AlertEventType[] = ['node.offline', 'node.drift', 'rollout.paused', 'rollout.failed']

export function EnrollmentView({ operatorToken }: { operatorToken: string }) {
  const { t } = useT()
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [created, setCreated] = useState<CreateEnrollmentTokenResponse | null>(null)
  const [copied, setCopied] = useState<'token' | 'command' | 'operatorToken' | null>(null)
  const [operatorTokens, setOperatorTokens] = useState<OperatorToken[]>([])
  const [operatorTokensLoading, setOperatorTokensLoading] = useState(false)
  const [operatorTokensError, setOperatorTokensError] = useState<string | null>(null)
  const [operatorTokenName, setOperatorTokenName] = useState('')
  const [operatorTokenScope, setOperatorTokenScope] = useState<OperatorTokenScope>('admin')
  const [creatingOperatorToken, setCreatingOperatorToken] = useState(false)
  const [createdOperatorToken, setCreatedOperatorToken] = useState<CreateOperatorTokenResponse | null>(null)
  const [revokingTokenId, setRevokingTokenId] = useState<string | null>(null)
  const serverURL = sideplaneServerURL()
  const enrollCommand = created
    ? `sideplane-sidecar enroll --server ${serverURL} --token ${created.token}`
    : `sideplane-sidecar enroll --server ${serverURL} --token <token>`
  const tokenReady = operatorToken.trim().length > 0

  const loadOperatorTokens = useCallback(async () => {
    const token = operatorToken.trim()
    if (!token) {
      setOperatorTokens([])
      setOperatorTokensLoading(false)
      setOperatorTokensError(null)
      return
    }
    setOperatorTokensLoading(true)
    setOperatorTokensError(null)
    try {
      const res = await fetch(apiURL('/api/operator-tokens'), {
        headers: { Authorization: `Bearer ${token}` },
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error(t('common.operatorTokenRequiredInvalid'))
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as ListOperatorTokensResponse
      setOperatorTokens(data.tokens ?? [])
    } catch (e) {
      setOperatorTokensError(e instanceof Error ? e.message : t('common.unknownError'))
    } finally {
      setOperatorTokensLoading(false)
    }
  }, [operatorToken, t])

  useEffect(() => {
    void loadOperatorTokens()
  }, [loadOperatorTokens])

  const createToken = async () => {
    if (!tokenReady || creating) return
    setCreating(true)
    setError(null)
    setCopied(null)
    try {
      const res = await fetch(apiURL('/api/enrollment-tokens'), {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${operatorToken.trim()}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({}),
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error(t('common.operatorTokenRequiredInvalid'))
        throw new Error(await apiErrorMessage(res))
      }
      const data: CreateEnrollmentTokenResponse = await res.json()
      setCreated(data)
    } catch (e) {
      setError(e instanceof Error ? e.message : t('common.unknownError'))
    } finally {
      setCreating(false)
    }
  }

  const copyText = async (kind: 'token' | 'command', value: string) => {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(kind)
      window.setTimeout(() => setCopied((current) => (current === kind ? null : current)), 1800)
    } catch {
      setError(t('enrollment.errorClipboard'))
    }
  }

  const copyOperatorToken = async () => {
    if (!createdOperatorToken) return
    try {
      await navigator.clipboard.writeText(createdOperatorToken.token)
      setCopied('operatorToken')
      window.setTimeout(() => setCopied((current) => (current === 'operatorToken' ? null : current)), 1800)
    } catch {
      setOperatorTokensError(t('enrollment.errorClipboard'))
    }
  }

  const createOperatorToken = async () => {
    const token = operatorToken.trim()
    const name = operatorTokenName.trim()
    if (!token || !name || creatingOperatorToken) return
    setCreatingOperatorToken(true)
    setOperatorTokensError(null)
    setCopied(null)
    try {
      const res = await fetch(apiURL('/api/operator-tokens'), {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${token}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ name, scope: operatorTokenScope }),
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error(t('common.operatorTokenRequiredInvalid'))
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as CreateOperatorTokenResponse
      setCreatedOperatorToken(data)
      setOperatorTokenName('')
      setOperatorTokenScope('admin')
      setOperatorTokens((current) => [data.operatorToken, ...current.filter((item) => item.id !== data.operatorToken.id)])
    } catch (e) {
      setOperatorTokensError(e instanceof Error ? e.message : t('common.unknownError'))
    } finally {
      setCreatingOperatorToken(false)
    }
  }

  const revokeOperatorToken = async (id: string) => {
    const token = operatorToken.trim()
    if (!token || revokingTokenId) return
    setRevokingTokenId(id)
    setOperatorTokensError(null)
    try {
      const res = await fetch(apiURL(`/api/operator-tokens/${encodeURIComponent(id)}`), {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${token}` },
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error(t('common.operatorTokenRequiredInvalid'))
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as RevokeOperatorTokenResponse
      setOperatorTokens((current) => current.map((item) => (item.id === data.operatorToken.id ? data.operatorToken : item)))
    } catch (e) {
      setOperatorTokensError(e instanceof Error ? e.message : t('common.unknownError'))
    } finally {
      setRevokingTokenId(null)
    }
  }

  return (
    <div className="mx-auto max-w-5xl px-4 py-6 sm:px-6 lg:px-9 lg:py-8">
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t('enrollment.title')}</h1>
          <div className="mt-1 text-sm text-[var(--sp-muted)]">{t('enrollment.subtitle')}</div>
        </div>
        <button
          type="button"
          className="h-9 w-fit rounded-lg bg-[var(--sp-accent)] px-3 text-sm font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-55"
          disabled={!tokenReady || creating}
          onClick={createToken}
        >
          {creating ? t('common.creating') : t('enrollment.createToken')}
        </button>
      </div>

      {!tokenReady && (
        <div className="mb-5 rounded-xl border border-amber-500/35 bg-amber-500/10 px-4 py-3 text-sm text-amber-700">
          {t('enrollment.operatorSessionRequired')}
        </div>
      )}

      {error && (
        <div role="alert" className="mb-5 rounded-xl border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-600">
          {error}
        </div>
      )}

      <section aria-labelledby="enrollment-token-heading" className="overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-sm">
        <div className="border-b border-[var(--sp-border)] px-4 py-3">
          <div id="enrollment-token-heading" className="text-sm font-semibold">{t('enrollment.tokenHeading')}</div>
          <div className="mt-1 text-xs text-[var(--sp-muted)]">{t('enrollment.tokenShownOnce')}</div>
        </div>

        <div className="grid gap-4 px-4 py-4">
          <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-700">
            {t('enrollment.tokenCopyWarning')}
          </div>

          <div className="grid gap-1.5">
            <div className="text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">{t('enrollment.token')}</div>
            <div className="flex flex-col gap-2 sm:flex-row">
              <input
                readOnly
                aria-label={t('enrollment.tokenLabel')}
                className="h-10 min-w-0 flex-1 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none"
                value={created?.token ?? ''}
                placeholder={t('enrollment.tokenPlaceholder')}
              />
              <button
                type="button"
                className="h-10 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-55"
                disabled={!created}
                onClick={() => created && copyText('token', created.token)}
              >
                {copied === 'token' ? t('common.copied') : t('common.copy')}
              </button>
            </div>
          </div>

          <div className="grid gap-1.5">
            <div className="text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">{t('enrollment.expires')}</div>
            <div className="font-mono text-sm text-[var(--sp-muted)]">{formatDate(created?.expiresAt)}</div>
          </div>

          <div className="grid gap-1.5">
            <div className="text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">{t('enrollment.sidecarCommand')}</div>
            <div className="flex flex-col gap-2 sm:flex-row">
              <input
                readOnly
                aria-label={t('enrollment.sidecarCommandLabel')}
                className="h-10 min-w-0 flex-1 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none"
                value={enrollCommand}
              />
              <button
                type="button"
                className="h-10 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-55"
                disabled={!created}
                onClick={() => copyText('command', enrollCommand)}
              >
                {copied === 'command' ? t('common.copied') : t('common.copy')}
              </button>
            </div>
          </div>
        </div>
      </section>

      <section className="mt-6 overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-sm">
        <div className="flex flex-col gap-3 border-b border-[var(--sp-border)] px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <div className="text-sm font-semibold">{t('enrollment.operatorToken')}</div>
            <div className="mt-1 text-xs text-[var(--sp-muted)]">{t('enrollment.operatorTokensDescription')}</div>
          </div>
          <button
            type="button"
            className="h-8 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-xs font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-55"
            disabled={!tokenReady || operatorTokensLoading}
            onClick={() => loadOperatorTokens()}
          >
            {operatorTokensLoading ? t('common.loading') : t('common.refresh')}
          </button>
        </div>

        <div className="grid gap-4 px-4 py-4">
          {createdOperatorToken && (
            <div role="status" className="grid gap-3 rounded-lg border border-emerald-500/25 bg-emerald-500/10 px-3 py-3">
              <div className="text-xs font-semibold text-emerald-700">{t('enrollment.createdTokenCopy')}</div>
              <div className="flex flex-col gap-2 sm:flex-row">
                <input
                  readOnly
                  aria-label={t('enrollment.operatorTokenNew')}
                  className="h-10 min-w-0 flex-1 rounded-lg border border-emerald-500/25 bg-[var(--sp-surface)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none"
                  value={createdOperatorToken.token}
                />
                <button
                  type="button"
                  className="h-10 rounded-lg border border-emerald-500/35 bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)]"
                  onClick={copyOperatorToken}
                >
                  {copied === 'operatorToken' ? t('common.copied') : t('common.copy')}
                </button>
              </div>
            </div>
          )}

          {operatorTokensError && (
            <div role="alert" className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-sm text-rose-600">
              {operatorTokensError}
            </div>
          )}

          <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto_auto]">
            <input
              className="h-10 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 text-sm text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)]"
              value={operatorTokenName}
              aria-label={t('enrollment.operatorName')}
              placeholder={t('enrollment.tokenNamePlaceholder')}
              onChange={(event) => setOperatorTokenName(event.target.value)}
            />
            <select
              className="h-10 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 text-sm text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)]"
              value={operatorTokenScope}
              aria-label={t('enrollment.scopeLabel')}
              onChange={(event) => setOperatorTokenScope(event.target.value as OperatorTokenScope)}
            >
              <option value="admin">{t('enrollment.scopeAdmin')}</option>
              <option value="readonly">{t('enrollment.scopeReadonly')}</option>
            </select>
            <button
              type="button"
              className="h-10 rounded-lg bg-[var(--sp-accent)] px-3 text-sm font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-55"
              disabled={!tokenReady || !operatorTokenName.trim() || creatingOperatorToken}
              onClick={createOperatorToken}
            >
              {creatingOperatorToken ? t('common.creating') : t('enrollment.createOperator')}
            </button>
          </div>

          <div className="overflow-x-auto rounded-lg border border-[var(--sp-border)]">
            <table className="min-w-full divide-y divide-[var(--sp-border)] text-left text-sm">
              <thead className="bg-[var(--sp-surface-2)] text-[11px] uppercase tracking-[0.12em] text-[var(--sp-faint)]">
                <tr>
                  <th className="px-3 py-2 font-semibold">{t('enrollment.name')}</th>
                  <th className="px-3 py-2 font-semibold">{t('enrollment.table.id')}</th>
                  <th className="px-3 py-2 font-semibold">{t('enrollment.scope')}</th>
                  <th className="px-3 py-2 font-semibold">{t('enrollment.table.lastUsed')}</th>
                  <th className="px-3 py-2 font-semibold">{t('enrollment.state')}</th>
                  <th className="px-3 py-2 text-right font-semibold">{t('enrollment.table.action')}</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[var(--sp-border)]">
                {operatorTokens.length === 0 && (
                  <tr>
                    <td colSpan={6} className="px-3 py-5 text-center text-sm text-[var(--sp-muted)]">
                      {operatorTokensLoading ? t('enrollment.operatorTokensLoading') : t('enrollment.noNamedTokens')}
                    </td>
                  </tr>
                )}
                {operatorTokens.map((token) => {
                  const revoked = Boolean(token.revokedAt)
                  return (
                    <tr key={token.id}>
                      <td className="px-3 py-2 font-medium text-[var(--sp-text)]">{token.name}</td>
                      <td className="px-3 py-2 font-mono text-xs text-[var(--sp-muted)]">{token.id}</td>
                      <td className="px-3 py-2">
                        <span className={`rounded border px-2 py-0.5 text-xs ${token.scope === 'readonly' ? 'border-[var(--sp-border)] bg-[var(--sp-surface-2)] text-[var(--sp-muted)]' : 'border-amber-500/30 bg-amber-500/10 text-amber-700'}`}>
                          {token.scope || t('enrollment.scopeAdmin')}
                        </span>
                      </td>
                      <td className="px-3 py-2 font-mono text-xs text-[var(--sp-muted)]">{formatDate(token.lastUsedAt)}</td>
                      <td className="px-3 py-2">
                        <span className={`rounded border px-2 py-0.5 text-xs ${revoked ? 'border-rose-500/30 bg-rose-500/10 text-rose-600' : 'border-emerald-500/25 bg-emerald-500/10 text-emerald-600'}`}>
                          {revoked ? t('enrollment.tokenStateRevoked') : t('enrollment.tokenStateActive')}
                        </span>
                      </td>
                      <td className="px-3 py-2 text-right">
                        <button
                          type="button"
                          className="h-8 rounded-lg border border-[var(--sp-border-strong)] px-3 text-xs font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-55"
                          disabled={!tokenReady || revoked || revokingTokenId === token.id}
                          onClick={() => revokeOperatorToken(token.id)}
                        >
                          {revokingTokenId === token.id ? t('common.revoking') : t('common.revoke')}
                        </button>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </div>
      </section>

      <ServerSettingsSection operatorToken={operatorToken} />

      <AlertWebhooksSection operatorToken={operatorToken} />
    </div>
  )
}

function ServerSettingsSection({ operatorToken }: { operatorToken: string }) {
  const { t } = useT()
  const [expectedVersion, setExpectedVersion] = useState('')
  const [expectedHermesVersion, setExpectedHermesVersion] = useState('')
  const [expectedOpenClawVersion, setExpectedOpenClawVersion] = useState('')
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const tokenReady = operatorToken.trim().length > 0

  const authHeaders = useCallback((): HeadersInit => {
    const headers: HeadersInit = { 'Content-Type': 'application/json' }
    const token = operatorToken.trim()
    if (token) headers.Authorization = `Bearer ${token}`
    return headers
  }, [operatorToken])

  const loadSettings = useCallback(async () => {
    try {
      const res = await fetch(apiURL('/api/settings'), { headers: authHeaders() })
      if (!res.ok) throw new Error(await apiErrorMessage(res))
      const data = (await res.json()) as ServerSettings
      setExpectedVersion(data.expectedSidecarVersion ?? '')
      setExpectedHermesVersion(data.expectedRuntimeVersions?.hermes ?? '')
      setExpectedOpenClawVersion(data.expectedRuntimeVersions?.openclaw ?? '')
    } catch (e) {
      setError(e instanceof Error ? e.message : t('common.unknownError'))
    }
  }, [authHeaders, t])

  useEffect(() => {
    void loadSettings()
  }, [loadSettings])

  const save = async () => {
    if (!tokenReady || saving) return
    setSaving(true)
    setError(null)
    setMessage(null)
    try {
      const res = await fetch(apiURL('/api/settings'), {
        method: 'PUT',
        headers: authHeaders(),
        body: JSON.stringify({
          expectedSidecarVersion: expectedVersion.trim(),
          expectedRuntimeVersions: {
            hermes: expectedHermesVersion.trim(),
            openclaw: expectedOpenClawVersion.trim(),
          },
        }),
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error(t('common.operatorTokenRequiredInvalid'))
        if (res.status === 403) throw new Error(t('common.operatorTokenReadOnly'))
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as ServerSettings
      setExpectedVersion(data.expectedSidecarVersion ?? '')
      setExpectedHermesVersion(data.expectedRuntimeVersions?.hermes ?? '')
      setExpectedOpenClawVersion(data.expectedRuntimeVersions?.openclaw ?? '')
      setMessage(t('common.saved'))
    } catch (e) {
      setError(e instanceof Error ? e.message : t('common.unknownError'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] p-5 shadow-sm">
      <h2 className="text-sm font-semibold">{t('enrollment.server.title')}</h2>
      <p className="mt-1 text-xs text-[var(--sp-muted)]">{t('enrollment.server.description')}</p>
      {!tokenReady && <div className="mt-3 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm text-amber-700">{t('enrollment.server.requiresToken')}</div>}
      {error && <div role="alert" className="mt-3 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-sm text-rose-600">{error}</div>}
      {message && <div role="status" className="mt-3 rounded-lg border border-emerald-500/25 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-700">{message}</div>}
      <div className="mt-4 grid gap-2 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1fr)_auto]">
        <input
          className="h-10 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 text-sm text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)]"
          value={expectedVersion}
          aria-label={t('enrollment.server.expectedSidecar')}
          placeholder={t('enrollment.server.expectedSidecarPlaceholder')}
          onChange={(event) => setExpectedVersion(event.target.value)}
        />
        <input
          className="h-10 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 text-sm text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)]"
          value={expectedHermesVersion}
          aria-label={t('enrollment.server.expectedHermes')}
          placeholder={t('enrollment.server.expectedHermesPlaceholder')}
          onChange={(event) => setExpectedHermesVersion(event.target.value)}
        />
        <input
          className="h-10 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 text-sm text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)]"
          value={expectedOpenClawVersion}
          aria-label={t('enrollment.server.expectedOpenClaw')}
          placeholder={t('enrollment.server.expectedOpenClawPlaceholder')}
          onChange={(event) => setExpectedOpenClawVersion(event.target.value)}
        />
        <button
          type="button"
          className="h-10 rounded-lg bg-[var(--sp-accent)] px-3 text-sm font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-55"
          disabled={!tokenReady || saving}
          onClick={save}
        >
          {saving ? t('common.saving') : t('common.save')}
        </button>
      </div>
    </section>
  )
}

function AlertWebhooksSection({ operatorToken }: { operatorToken: string }) {
  const { t } = useT()
  const [webhooks, setWebhooks] = useState<AlertWebhook[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [url, setUrl] = useState('')
  const [kind, setKind] = useState<AlertWebhookKind>('generic')
  const [events, setEvents] = useState<Set<AlertEventType>>(new Set())
  const [sign, setSign] = useState(false)
  const [creating, setCreating] = useState(false)
  const [createdSecret, setCreatedSecret] = useState<string | null>(null)
  const [deletingId, setDeletingId] = useState<string | null>(null)
  const tokenReady = operatorToken.trim().length > 0

  const authHeaders = useCallback((): HeadersInit => {
    const headers: HeadersInit = { 'Content-Type': 'application/json' }
    const token = operatorToken.trim()
    if (token) headers.Authorization = `Bearer ${token}`
    return headers
  }, [operatorToken])

  const loadWebhooks = useCallback(async () => {
    if (!tokenReady) {
      setWebhooks([])
      return
    }
    setLoading(true)
    setError(null)
    try {
      const res = await fetch(apiURL('/api/webhooks'), { headers: authHeaders() })
      if (!res.ok) throw new Error(await apiErrorMessage(res))
      const data = (await res.json()) as ListAlertWebhooksResponse
      setWebhooks(data.webhooks ?? [])
    } catch (e) {
      setError(e instanceof Error ? e.message : t('common.unknownError'))
    } finally {
      setLoading(false)
    }
  }, [authHeaders, tokenReady, t])

  useEffect(() => {
    void loadWebhooks()
  }, [loadWebhooks])

  const toggleEvent = (event: AlertEventType) => {
    setEvents((current) => {
      const next = new Set(current)
      if (next.has(event)) next.delete(event)
      else next.add(event)
      return next
    })
  }

  const createWebhook = async () => {
    if (!tokenReady || creating) return
    if (url.trim() === '' || events.size === 0) {
      setError(t('enrollment.alerts.urlEventRequired'))
      return
    }
    setCreating(true)
    setError(null)
    setCreatedSecret(null)
    try {
      const res = await fetch(apiURL('/api/webhooks'), {
        method: 'POST',
        headers: authHeaders(),
        body: JSON.stringify({ url: url.trim(), kind, events: Array.from(events), sign: kind === 'generic' ? sign : false }),
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error(t('common.operatorTokenRequiredInvalid'))
        if (res.status === 403) throw new Error(t('common.operatorTokenReadOnly'))
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as CreateAlertWebhookResponse
      setCreatedSecret(data.secret ?? null)
      setUrl('')
      setKind('generic')
      setEvents(new Set())
      setSign(false)
      setWebhooks((current) => [data.webhook, ...current.filter((item) => item.id !== data.webhook.id)])
    } catch (e) {
      setError(e instanceof Error ? e.message : t('common.unknownError'))
    } finally {
      setCreating(false)
    }
  }

  const deleteWebhook = async (id: string) => {
    if (!tokenReady || deletingId) return
    setDeletingId(id)
    setError(null)
    try {
      const res = await fetch(apiURL(`/api/webhooks/${encodeURIComponent(id)}`), { method: 'DELETE', headers: authHeaders() })
      if (!res.ok && res.status !== 204) throw new Error(await apiErrorMessage(res))
      setWebhooks((current) => current.filter((item) => item.id !== id))
    } catch (e) {
      setError(e instanceof Error ? e.message : t('common.unknownError'))
    } finally {
      setDeletingId(null)
    }
  }

  return (
    <section className="rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] p-5 shadow-sm">
      <h2 className="text-sm font-semibold">{t('enrollment.alerts.title')}</h2>
      <p className="mt-1 text-xs text-[var(--sp-muted)]">{t('enrollment.alerts.description')}</p>

      {!tokenReady && <div className="mt-3 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm text-amber-700">{t('enrollment.alerts.manageRequiresToken')}</div>}
      {error && <div role="alert" className="mt-3 rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-sm text-rose-600">{error}</div>}
      {createdSecret && (
        <div role="status" className="mt-3 rounded-lg border border-emerald-500/25 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-700">
          {t('enrollment.alerts.signingSecret')} <span className="font-mono break-all">{createdSecret}</span>
        </div>
      )}

      <div className="mt-4 grid gap-2">
        <input
          className="h-10 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 text-sm text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)]"
          value={url}
          aria-label={t('enrollment.alerts.urlLabel')}
          placeholder="https://hooks.example.com/sideplane"
          onChange={(event) => setUrl(event.target.value)}
        />
        <select
          className="h-10 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 text-sm text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)]"
          value={kind}
          aria-label={t('enrollment.alerts.kindLabel')}
          onChange={(event) => {
            const next = event.target.value as AlertWebhookKind
            setKind(next)
            if (next === 'slack') setSign(false)
          }}
        >
          <option value="generic">{t('enrollment.alerts.kindGeneric')}</option>
          <option value="slack">{t('enrollment.alerts.kindSlack')}</option>
        </select>
        <div className="flex flex-wrap gap-3">
          {ALERT_EVENT_TYPES.map((event) => (
            <label key={event} className="flex items-center gap-1.5 text-xs text-[var(--sp-muted)]">
              <input type="checkbox" checked={events.has(event)} onChange={() => toggleEvent(event)} />
              {event}
            </label>
          ))}
        </div>
        <div className="flex flex-wrap items-center gap-3">
          <label className="flex items-center gap-1.5 text-xs text-[var(--sp-muted)]">
            <input type="checkbox" checked={kind === 'generic' && sign} disabled={kind !== 'generic'} onChange={(event) => setSign(event.target.checked)} />
            {t('enrollment.alerts.sign')}
          </label>
          <button
            type="button"
            className="h-9 rounded-lg bg-[var(--sp-accent)] px-3 text-sm font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-55"
            disabled={!tokenReady || creating || url.trim() === '' || events.size === 0}
            onClick={createWebhook}
          >
            {creating ? t('common.creating') : t('enrollment.alerts.create')}
          </button>
        </div>
      </div>

      <div className="mt-4 overflow-x-auto rounded-lg border border-[var(--sp-border)]">
        <table className="min-w-full divide-y divide-[var(--sp-border)] text-left text-sm">
          <thead className="bg-[var(--sp-surface-2)] text-[11px] uppercase tracking-[0.12em] text-[var(--sp-faint)]">
            <tr>
              <th className="px-3 py-2 font-semibold">{t('enrollment.alerts.url')}</th>
              <th className="px-3 py-2 font-semibold">{t('enrollment.alerts.kind')}</th>
              <th className="px-3 py-2 font-semibold">{t('enrollment.alerts.events')}</th>
              <th className="px-3 py-2 font-semibold">{t('enrollment.alerts.signed')}</th>
              <th className="px-3 py-2 text-right font-semibold">{t('enrollment.table.action')}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[var(--sp-border)]">
            {webhooks.length === 0 && (
              <tr>
                <td colSpan={5} className="px-3 py-5 text-center text-sm text-[var(--sp-muted)]">
                  {loading ? t('enrollment.alerts.loading') : t('enrollment.alerts.empty')}
                </td>
              </tr>
            )}
            {webhooks.map((webhook) => (
              <tr key={webhook.id}>
                <td className="px-3 py-2 font-mono text-xs text-[var(--sp-text)] break-all">{webhook.url}</td>
                <td className="px-3 py-2 text-xs">{webhook.kind ?? t('enrollment.alerts.kindGeneric')}</td>
                <td className="px-3 py-2 font-mono text-[11px] text-[var(--sp-muted)]">{(webhook.events ?? []).join(', ')}</td>
                <td className="px-3 py-2 text-xs">{webhook.hasSecret ? t('common.yes') : t('common.no')}</td>
                <td className="px-3 py-2 text-right">
                  <button
                    type="button"
                    className="h-8 rounded-lg border border-[var(--sp-border-strong)] px-3 text-xs font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-55"
                    disabled={!tokenReady || deletingId === webhook.id}
                    onClick={() => deleteWebhook(webhook.id)}
                  >
                    {deletingId === webhook.id ? t('common.deleting') : t('common.delete')}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}
