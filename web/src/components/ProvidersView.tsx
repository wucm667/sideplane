import { useState, type FormEvent, type ReactNode } from 'react'
import { useT } from '../i18n.ts'
import type { ProviderDefinition } from '../types.ts'
import { TableMessage } from './FleetOverview.tsx'

interface ProvidersViewProps {
  providers: ProviderDefinition[]
  loading: boolean
  error: string | null
  saving: boolean
  onRefresh: () => void
  onUpsert: (provider: ProviderDefinition, apiKey?: string) => boolean | void | Promise<boolean | void>
  onDelete: (name: string) => boolean | void | Promise<boolean | void>
}

export interface ProviderFormValues {
  name: string
  baseURL: string
  models: string
  apiKeyEnv: string
}

export function providerFormValues(provider: ProviderDefinition | null): ProviderFormValues {
  return {
    name: provider?.name ?? '',
    baseURL: provider?.baseURL ?? '',
    models: provider?.models?.join('\n') ?? '',
    apiKeyEnv: provider?.apiKeyEnv ?? '',
  }
}

export function providerFromFormValues(values: ProviderFormValues): ProviderDefinition {
  const models = values.models
    .split(/[\n,]+/)
    .map((model) => model.trim())
    .filter(Boolean)

  return {
    name: values.name.trim(),
    baseURL: emptyToUndefined(values.baseURL),
    models: models.length > 0 ? models : undefined,
    apiKeyEnv: emptyToUndefined(values.apiKeyEnv),
  }
}

export function ProvidersView({
  providers,
  loading,
  error,
  saving,
  onRefresh,
  onUpsert,
  onDelete,
}: ProvidersViewProps) {
  const { t } = useT()
  const [editingProvider, setEditingProvider] = useState<ProviderDefinition | null>(null)
  const [formOpen, setFormOpen] = useState(false)
  const [confirmingDelete, setConfirmingDelete] = useState<string | null>(null)

  const openAddForm = () => {
    setEditingProvider(null)
    setFormOpen(true)
  }

  const openEditForm = (provider: ProviderDefinition) => {
    setEditingProvider(provider)
    setFormOpen(true)
  }

  const closeForm = () => {
    setEditingProvider(null)
    setFormOpen(false)
  }

  const confirmDelete = async (name: string) => {
    const result = await onDelete(name)
    if (result !== false) {
      setConfirmingDelete(null)
    }
  }

  return (
    <div className="mx-auto max-w-5xl px-4 py-6 sm:px-6 lg:px-9 lg:py-8">
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">{t('providers.title')}</h1>
          <div className="mt-1 text-sm text-[var(--sp-muted)]">{t('providers.subtitle')}</div>
        </div>
        <div className="flex flex-wrap gap-2">
          <button
            type="button"
            className="h-9 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-60"
            disabled={loading}
            onClick={onRefresh}
          >
            {loading ? t('providers.loading') : t('providers.refresh')}
          </button>
          <button
            type="button"
            className="h-9 rounded-lg bg-[var(--sp-accent)] px-3 text-sm font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-55"
            disabled={saving}
            onClick={openAddForm}
          >
            {t('providers.add')}
          </button>
        </div>
      </div>

      {error && (
        <div role="alert" className="mb-5 rounded-xl border border-rose-500/30 bg-rose-500/10 px-4 py-3 text-sm text-rose-600">
          {t('providers.error', { error })}
        </div>
      )}

      {formOpen && (
        <ProviderForm
          key={editingProvider?.name ?? 'new-provider'}
          provider={editingProvider}
          saving={saving}
          onCancel={closeForm}
          onSubmit={async (provider, apiKey) => {
            const result = apiKey === undefined ? await onUpsert(provider) : await onUpsert(provider, apiKey)
            if (result !== false) {
              closeForm()
            }
            return result
          }}
        />
      )}

      <section aria-label={t('providers.title')} className="overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-sm">
        <div className="hidden grid-cols-[1fr_1.4fr_1.4fr_1fr_auto] gap-4 border-b border-[var(--sp-border)] px-5 py-3 text-[11px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)] md:grid">
          <div>{t('providers.name')}</div>
          <div>{t('providers.baseURL')}</div>
          <div>{t('providers.models')}</div>
          <div>{t('providers.apiKeyEnv')}</div>
          <div />
        </div>
        {loading && <TableMessage message={t('providers.loading')} />}
        {!loading && providers.length === 0 && <TableMessage message={t('providers.empty')} />}
        {!loading && providers.map((provider) => (
          <div key={provider.name} className="grid gap-3 border-b border-[var(--sp-border)] px-5 py-4 text-sm last:border-b-0 md:grid-cols-[1fr_1.4fr_1.4fr_1fr_auto] md:items-center md:gap-4">
            <ProviderCell label={t('providers.name')}>
              <span className="font-mono font-semibold text-[var(--sp-text)]">{provider.name}</span>
            </ProviderCell>
            <ProviderCell label={t('providers.baseURL')}>
              <span className="break-all font-mono text-xs text-[var(--sp-muted)]">{provider.baseURL || '-'}</span>
            </ProviderCell>
            <ProviderCell label={t('providers.models')}>
              <span className="break-words font-mono text-xs text-[var(--sp-muted)]">{provider.models?.join(', ') || '-'}</span>
            </ProviderCell>
            <ProviderCell label={t('providers.apiKeyEnv')}>
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <span className="break-all font-mono text-xs text-[var(--sp-muted)]">{provider.apiKeyEnv || '-'}</span>
                {provider.apiKeyManaged ? (
                  <span className="rounded-full border border-emerald-500/30 bg-emerald-500/10 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-emerald-700">
                    {t('providers.apiKeyManagedBadge')}
                  </span>
                ) : provider.apiKeyEnv ? (
                  <span className="text-xs text-[var(--sp-faint)]">{t('providers.apiKeyEnvOnly')}</span>
                ) : null}
              </div>
            </ProviderCell>
            <div className="flex flex-wrap items-center gap-2 md:justify-end">
              <button
                type="button"
                className="h-8 rounded-lg border border-[var(--sp-border-strong)] px-3 text-xs font-medium hover:bg-[var(--sp-surface-2)] disabled:cursor-not-allowed disabled:opacity-55"
                disabled={saving}
                onClick={() => openEditForm(provider)}
              >
                {t('providers.edit')}
              </button>
              <button
                type="button"
                className="h-8 rounded-lg border border-rose-500/35 px-3 text-xs font-medium text-rose-600 hover:bg-rose-500/10 disabled:cursor-not-allowed disabled:opacity-55"
                disabled={saving}
                onClick={() => setConfirmingDelete(provider.name)}
              >
                {t('providers.delete')}
              </button>
              {confirmingDelete === provider.name && (
                <div role="dialog" aria-label={t('providers.confirmDelete', { name: provider.name })} className="basis-full rounded-lg border border-rose-500/30 bg-rose-500/10 p-3 text-xs text-rose-700 md:min-w-64">
                  <div>{t('providers.confirmDelete', { name: provider.name })}</div>
                  <div className="mt-3 flex gap-2">
                    <button
                      type="button"
                      className="h-8 rounded-lg bg-rose-600 px-3 font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-55"
                      disabled={saving}
                      onClick={() => confirmDelete(provider.name)}
                    >
                      {t('providers.delete')}
                    </button>
                    <button
                      type="button"
                      className="h-8 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 font-medium text-[var(--sp-muted)] hover:bg-[var(--sp-surface-2)]"
                      onClick={() => setConfirmingDelete(null)}
                    >
                      {t('providers.cancel')}
                    </button>
                  </div>
                </div>
              )}
            </div>
          </div>
        ))}
      </section>
    </div>
  )
}

export function ProviderForm({
  provider,
  saving,
  onCancel,
  onSubmit,
}: {
  provider: ProviderDefinition | null
  saving: boolean
  onCancel: () => void
  onSubmit: (provider: ProviderDefinition, apiKey?: string) => boolean | void | Promise<boolean | void>
}) {
  const { t } = useT()
  const initialValues = providerFormValues(provider)
  const [name, setName] = useState(initialValues.name)
  const [baseURL, setBaseURL] = useState(initialValues.baseURL)
  const [models, setModels] = useState(initialValues.models)
  const [apiKeyEnv, setAPIKeyEnv] = useState(initialValues.apiKeyEnv)
  const [apiKey, setAPIKey] = useState('')

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const values = { name, baseURL, models, apiKeyEnv }
    const nextProvider = providerFromFormValues(values)
    if (!nextProvider.name) return
    const result = apiKey.trim() === '' ? await onSubmit(nextProvider) : await onSubmit(nextProvider, apiKey)
    if (result !== false) {
      setAPIKey('')
    }
  }

  return (
    <form aria-label={provider ? t('providers.edit') : t('providers.add')} className="mb-5 rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] p-4 shadow-sm" onSubmit={submit}>
      <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="text-sm font-semibold">{provider ? t('providers.edit') : t('providers.add')}</div>
        <div className="flex gap-2">
          <button
            type="submit"
            className="h-9 rounded-lg bg-[var(--sp-accent)] px-3 text-sm font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-55"
            disabled={saving || name.trim() === ''}
          >
            {saving ? t('common.saving') : t('providers.save')}
          </button>
          <button
            type="button"
            className="h-9 rounded-lg border border-[var(--sp-border-strong)] bg-[var(--sp-surface)] px-3 text-sm font-medium text-[var(--sp-muted)] hover:bg-[var(--sp-surface-2)]"
            onClick={onCancel}
          >
            {t('providers.cancel')}
          </button>
        </div>
      </div>

      <div className="grid gap-3 md:grid-cols-2">
        <ProviderField label={t('providers.name')}>
          <input
            required
            className={inputClassName}
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </ProviderField>
        <ProviderField label={t('providers.baseURL')}>
          <input
            className={inputClassName}
            value={baseURL}
            onChange={(event) => setBaseURL(event.target.value)}
          />
        </ProviderField>
        <ProviderField label={t('providers.models')}>
          <textarea
            className={`${inputClassName} min-h-24 resize-y py-2 leading-5`}
            placeholder={'gpt-5.2\ngpt-4o'}
            rows={4}
            value={models}
            onChange={(event) => setModels(event.target.value)}
          />
          <p className="mt-1.5 text-xs leading-5 text-[var(--sp-faint)]">{t('providers.modelsHint')}</p>
        </ProviderField>
        <ProviderField label={t('providers.apiKeyEnv')}>
          <input
            className={inputClassName}
            value={apiKeyEnv}
            onChange={(event) => setAPIKeyEnv(event.target.value)}
          />
          <p className="mt-1.5 text-xs leading-5 text-[var(--sp-faint)]">{t('providers.apiKeyEnvHint')}</p>
        </ProviderField>
        <ProviderField label={t('providers.apiKey')}>
          <input
            autoComplete="off"
            className={inputClassName}
            type="password"
            value={apiKey}
            onChange={(event) => setAPIKey(event.target.value)}
          />
          <p className="mt-1.5 text-xs leading-5 text-[var(--sp-faint)]">
            {provider?.apiKeyManaged ? t('providers.apiKeyKeepHint') : t('providers.apiKeyHint')}
          </p>
        </ProviderField>
      </div>
    </form>
  )
}

function ProviderField({ children, label }: { children: ReactNode; label: string }) {
  return (
    <label className="grid gap-1.5 text-xs font-medium text-[var(--sp-muted)]">
      <span>{label}</span>
      {children}
    </label>
  )
}

function ProviderCell({ children, label }: { children: ReactNode; label: string }) {
  return (
    <div className="min-w-0">
      <div className="mb-1 text-[10px] font-semibold uppercase tracking-[0.12em] text-[var(--sp-faint)] md:hidden">{label}</div>
      {children}
    </div>
  )
}

function emptyToUndefined(value: string): string | undefined {
  const trimmed = value.trim()
  return trimmed === '' ? undefined : trimmed
}

const inputClassName = 'h-9 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)]'
