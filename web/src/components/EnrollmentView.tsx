import { useCallback, useEffect, useState } from 'react'
import { apiErrorMessage, formatDate } from '../helpers.ts'
import type { CreateEnrollmentTokenResponse, CreateOperatorTokenResponse, ListOperatorTokensResponse, OperatorToken, OperatorTokenScope, RevokeOperatorTokenResponse } from '../types.ts'

export function EnrollmentView({ operatorToken }: { operatorToken: string }) {
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
  const serverURL = window.location.origin
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
      const res = await fetch('/api/operator-tokens', {
        headers: { Authorization: `Bearer ${token}` },
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as ListOperatorTokensResponse
      setOperatorTokens(data.tokens ?? [])
    } catch (e) {
      setOperatorTokensError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      setOperatorTokensLoading(false)
    }
  }, [operatorToken])

  useEffect(() => {
    void loadOperatorTokens()
  }, [loadOperatorTokens])

  const createToken = async () => {
    if (!tokenReady || creating) return
    setCreating(true)
    setError(null)
    setCopied(null)
    try {
      const res = await fetch('/api/enrollment-tokens', {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${operatorToken.trim()}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({}),
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        throw new Error(await apiErrorMessage(res))
      }
      const data: CreateEnrollmentTokenResponse = await res.json()
      setCreated(data)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Unknown error')
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
      setError('Clipboard copy failed')
    }
  }

  const copyOperatorToken = async () => {
    if (!createdOperatorToken) return
    try {
      await navigator.clipboard.writeText(createdOperatorToken.token)
      setCopied('operatorToken')
      window.setTimeout(() => setCopied((current) => (current === 'operatorToken' ? null : current)), 1800)
    } catch {
      setOperatorTokensError('Clipboard copy failed')
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
      const res = await fetch('/api/operator-tokens', {
        method: 'POST',
        headers: {
          Authorization: `Bearer ${token}`,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ name, scope: operatorTokenScope }),
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as CreateOperatorTokenResponse
      setCreatedOperatorToken(data)
      setOperatorTokenName('')
      setOperatorTokenScope('admin')
      setOperatorTokens((current) => [data.operatorToken, ...current.filter((item) => item.id !== data.operatorToken.id)])
    } catch (e) {
      setOperatorTokensError(e instanceof Error ? e.message : 'Unknown error')
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
      const res = await fetch(`/api/operator-tokens/${encodeURIComponent(id)}`, {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${token}` },
      })
      if (!res.ok) {
        if (res.status === 401) throw new Error('Operator token required or invalid')
        throw new Error(await apiErrorMessage(res))
      }
      const data = (await res.json()) as RevokeOperatorTokenResponse
      setOperatorTokens((current) => current.map((item) => (item.id === data.operatorToken.id ? data.operatorToken : item)))
    } catch (e) {
      setOperatorTokensError(e instanceof Error ? e.message : 'Unknown error')
    } finally {
      setRevokingTokenId(null)
    }
  }

  return (
    <div className="mx-auto max-w-5xl px-4 py-6 sm:px-6 lg:px-9 lg:py-8">
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Enrollment</h1>
          <div className="mt-1 text-sm text-[var(--sp-muted)]">Issue one-time sidecar enrollment tokens</div>
        </div>
        <button
          type="button"
          className="h-9 w-fit rounded-lg bg-[var(--sp-accent)] px-3 text-sm font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-55"
          disabled={!tokenReady || creating}
          onClick={createToken}
        >
          {creating ? 'Creating...' : 'Create token'}
        </button>
      </div>

      {!tokenReady && (
        <div className="mb-5 rounded-xl border border-amber-500/35 bg-amber-500/10 px-4 py-3 text-sm text-amber-700">
          Set an operator token in the sidebar before creating enrollment tokens.
        </div>
      )}

      {error && (
        <div className="mb-5 rounded-xl border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-600">
          {error}
        </div>
      )}

      <section className="overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-sm">
        <div className="border-b border-[var(--sp-border)] px-4 py-3">
          <div className="text-sm font-semibold">Enrollment token</div>
          <div className="mt-1 text-xs text-[var(--sp-muted)]">Tokens are one-time values and are shown only once.</div>
        </div>

        <div className="grid gap-4 px-4 py-4">
          <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-700">
            Copy the token now. Sideplane cannot show this token again after you leave this view or create another token.
          </div>

          <div className="grid gap-1.5">
            <div className="text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">Token</div>
            <div className="flex flex-col gap-2 sm:flex-row">
              <input
                readOnly
                className="h-10 min-w-0 flex-1 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none"
                value={created?.token ?? ''}
                placeholder="Create a token to reveal it once"
              />
              <button
                type="button"
                className="h-10 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-55"
                disabled={!created}
                onClick={() => created && copyText('token', created.token)}
              >
                {copied === 'token' ? 'Copied' : 'Copy'}
              </button>
            </div>
          </div>

          <div className="grid gap-1.5">
            <div className="text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">Expires</div>
            <div className="font-mono text-sm text-[var(--sp-muted)]">{formatDate(created?.expiresAt)}</div>
          </div>

          <div className="grid gap-1.5">
            <div className="text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)]">Sidecar command</div>
            <div className="flex flex-col gap-2 sm:flex-row">
              <input
                readOnly
                className="h-10 min-w-0 flex-1 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none"
                value={enrollCommand}
              />
              <button
                type="button"
                className="h-10 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-55"
                disabled={!created}
                onClick={() => copyText('command', enrollCommand)}
              >
                {copied === 'command' ? 'Copied' : 'Copy'}
              </button>
            </div>
          </div>
        </div>
      </section>

      <section className="mt-6 overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-sm">
        <div className="flex flex-col gap-3 border-b border-[var(--sp-border)] px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <div className="text-sm font-semibold">Operator tokens</div>
            <div className="mt-1 text-xs text-[var(--sp-muted)]">Named API tokens can be revoked independently.</div>
          </div>
          <button
            type="button"
            className="h-8 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-xs font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-55"
            disabled={!tokenReady || operatorTokensLoading}
            onClick={() => loadOperatorTokens()}
          >
            {operatorTokensLoading ? 'Loading...' : 'Refresh'}
          </button>
        </div>

        <div className="grid gap-4 px-4 py-4">
          {createdOperatorToken && (
            <div className="grid gap-3 rounded-lg border border-emerald-500/25 bg-emerald-500/10 px-3 py-3">
              <div className="text-xs font-semibold text-emerald-700">Copy this operator token now. It will not be shown again.</div>
              <div className="flex flex-col gap-2 sm:flex-row">
                <input
                  readOnly
                  className="h-10 min-w-0 flex-1 rounded-lg border border-emerald-500/25 bg-[var(--sp-surface)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none"
                  value={createdOperatorToken.token}
                />
                <button
                  type="button"
                  className="h-10 rounded-lg border border-emerald-500/35 bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)]"
                  onClick={copyOperatorToken}
                >
                  {copied === 'operatorToken' ? 'Copied' : 'Copy'}
                </button>
              </div>
            </div>
          )}

          {operatorTokensError && (
            <div className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-sm text-rose-600">
              {operatorTokensError}
            </div>
          )}

          <div className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_auto_auto]">
            <input
              className="h-10 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 text-sm text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)]"
              value={operatorTokenName}
              placeholder="token name"
              onChange={(event) => setOperatorTokenName(event.target.value)}
            />
            <select
              className="h-10 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 text-sm text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)]"
              value={operatorTokenScope}
              aria-label="token scope"
              onChange={(event) => setOperatorTokenScope(event.target.value as OperatorTokenScope)}
            >
              <option value="admin">admin</option>
              <option value="readonly">readonly</option>
            </select>
            <button
              type="button"
              className="h-10 rounded-lg bg-[var(--sp-accent)] px-3 text-sm font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-55"
              disabled={!tokenReady || !operatorTokenName.trim() || creatingOperatorToken}
              onClick={createOperatorToken}
            >
              {creatingOperatorToken ? 'Creating...' : 'Create operator token'}
            </button>
          </div>

          <div className="overflow-x-auto rounded-lg border border-[var(--sp-border)]">
            <table className="min-w-full divide-y divide-[var(--sp-border)] text-left text-sm">
              <thead className="bg-[var(--sp-surface-2)] text-[11px] uppercase tracking-[0.12em] text-[var(--sp-faint)]">
                <tr>
                  <th className="px-3 py-2 font-semibold">Name</th>
                  <th className="px-3 py-2 font-semibold">ID</th>
                  <th className="px-3 py-2 font-semibold">Scope</th>
                  <th className="px-3 py-2 font-semibold">Last used</th>
                  <th className="px-3 py-2 font-semibold">State</th>
                  <th className="px-3 py-2 text-right font-semibold">Action</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-[var(--sp-border)]">
                {operatorTokens.length === 0 && (
                  <tr>
                    <td colSpan={6} className="px-3 py-5 text-center text-sm text-[var(--sp-muted)]">
                      {operatorTokensLoading ? 'Loading tokens...' : 'No named tokens'}
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
                          {token.scope || 'admin'}
                        </span>
                      </td>
                      <td className="px-3 py-2 font-mono text-xs text-[var(--sp-muted)]">{formatDate(token.lastUsedAt)}</td>
                      <td className="px-3 py-2">
                        <span className={`rounded border px-2 py-0.5 text-xs ${revoked ? 'border-rose-500/30 bg-rose-500/10 text-rose-600' : 'border-emerald-500/25 bg-emerald-500/10 text-emerald-600'}`}>
                          {revoked ? 'revoked' : 'active'}
                        </span>
                      </td>
                      <td className="px-3 py-2 text-right">
                        <button
                          type="button"
                          className="h-8 rounded-lg border border-[var(--sp-border-strong)] px-3 text-xs font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-55"
                          disabled={!tokenReady || revoked || revokingTokenId === token.id}
                          onClick={() => revokeOperatorToken(token.id)}
                        >
                          {revokingTokenId === token.id ? 'Revoking...' : 'Revoke'}
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
    </div>
  )
}
