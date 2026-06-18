import { useState } from 'react'
import { formatDate } from '../helpers.ts'
import type { CreateEnrollmentTokenResponse } from '../types.ts'

export function EnrollmentView({ operatorToken }: { operatorToken: string }) {
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [created, setCreated] = useState<CreateEnrollmentTokenResponse | null>(null)
  const [copied, setCopied] = useState<'token' | 'command' | null>(null)
  const serverURL = window.location.origin
  const enrollCommand = created
    ? `sideplane-sidecar enroll --server ${serverURL} --token ${created.token}`
    : `sideplane-sidecar enroll --server ${serverURL} --token <token>`
  const tokenReady = operatorToken.trim().length > 0

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
        throw new Error(`HTTP ${res.status}: ${res.statusText}`)
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
    </div>
  )
}
