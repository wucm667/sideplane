import type { Theme, View } from '../helpers.ts'
import { useT, type Lang } from '../i18n.ts'

interface SidebarProps {
  currentView: View
  groups: Array<{ name: string; count: number }>
  lang: Lang
  liveConnected: boolean
  operatorToken: string
  theme: Theme
  onLangToggle: () => void
  onOperatorTokenChange: (value: string) => void
  onThemeToggle: () => void
  onViewChange: (view: View) => void
}

export function Sidebar({
  currentView,
  groups,
  lang,
  liveConnected,
  operatorToken,
  theme,
  onLangToggle,
  onOperatorTokenChange,
  onThemeToggle,
  onViewChange,
}: SidebarProps) {
  const { t } = useT()

  return (
    <aside className="border-b border-[var(--sp-border)] bg-[var(--sp-surface)] md:flex md:h-screen md:w-60 md:flex-none md:flex-col md:border-b-0 md:border-r">
      <div className="flex items-center gap-3 border-b border-[var(--sp-border)] px-5 py-4">
        <div className="relative h-7 w-7 rounded-lg bg-[var(--sp-accent)] shadow-sm">
          <div className="absolute inset-x-[7px] inset-y-[6px] rounded-sm border-2 border-white/90" />
          <div className="absolute bottom-[5px] left-1/2 top-[5px] w-0.5 -translate-x-1/2 bg-white/90" />
        </div>
        <div>
          <div className="text-sm font-bold tracking-tight">Sideplane</div>
          <div className="text-[11px] text-[var(--sp-faint)]">{t('sidebar.controlPlane')}</div>
        </div>
      </div>

      <nav aria-label={t('sidebar.nav.primary')} className="grid grid-cols-4 gap-1 px-3 py-3 md:flex md:flex-col">
        <NavButton active={currentView === 'fleet'} label={t('sidebar.nav.fleet')} onClick={() => onViewChange('fleet')} />
        <NavButton active={currentView === 'rollouts'} label={t('sidebar.nav.rollouts')} onClick={() => onViewChange('rollouts')} />
        <NavButton active={currentView === 'activity'} label={t('sidebar.nav.activity')} onClick={() => onViewChange('activity')} />
        <NavButton active={currentView === 'enrollment'} label={t('sidebar.nav.enrollment')} onClick={() => onViewChange('enrollment')} />
      </nav>

      <div className="hidden px-4 pt-2 md:block">
        <div className="px-1 pb-2 text-[10px] font-semibold uppercase tracking-[0.16em] text-[var(--sp-faint)]">
          {t('sidebar.groups')}
        </div>
        <div className="space-y-1">
          {groups.map((group, index) => (
            <div key={group.name} className="flex items-center justify-between rounded-md px-2 py-1.5 text-xs text-[var(--sp-muted)] hover:bg-[var(--sp-surface-2)]">
              <span className="flex min-w-0 items-center gap-2">
                <span className={`h-1.5 w-1.5 flex-none rounded-sm ${index === 0 ? 'bg-[var(--sp-accent)]' : 'bg-[var(--sp-faint)]'}`} />
                <span className="truncate">{groupLabel(group.name, t)}</span>
              </span>
              <span className="font-mono text-[var(--sp-faint)]">{group.count}</span>
            </div>
          ))}
        </div>
      </div>

      <div className="mt-auto grid gap-3 border-t border-[var(--sp-border)] p-4">
        <label className="grid gap-1.5 text-xs text-[var(--sp-muted)]">
          <span className="flex items-center gap-2">
            <span className={`h-2 w-2 rounded-full ${operatorToken.trim() ? 'bg-emerald-500' : 'bg-amber-500'}`} />
            {t('sidebar.operatorSession')}
            {liveConnected && (
              <span className="ml-auto rounded border border-emerald-500/25 bg-emerald-500/10 px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-normal text-emerald-600">
                {t('sidebar.live')}
              </span>
            )}
          </span>
          <input
            type="password"
            className="h-9 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 font-mono text-xs text-[var(--sp-text)] outline-none focus:border-[var(--sp-accent)]"
            value={operatorToken}
            autoComplete="off"
            placeholder={t('sidebar.operatorToken')}
            onChange={(event) => onOperatorTokenChange(event.target.value)}
          />
        </label>
        <button
          type="button"
          className="h-8 rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 text-xs font-medium text-[var(--sp-muted)] hover:border-[var(--sp-border-strong)] disabled:cursor-not-allowed disabled:opacity-55"
          disabled={!operatorToken.trim()}
          onClick={() => onOperatorTokenChange('')}
        >
          {t('sidebar.clearToken')}
        </button>
        <div className="grid grid-cols-2 gap-2">
          <button
            type="button"
            className="flex h-9 items-center justify-between rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-3 text-xs font-medium text-[var(--sp-text)] hover:border-[var(--sp-border-strong)]"
            onClick={onThemeToggle}
          >
            <span>{theme === 'dark' ? t('sidebar.darkMode') : t('sidebar.lightMode')}</span>
            <span className="font-mono text-[var(--sp-faint)]">{theme === 'dark' ? t('sidebar.on') : t('sidebar.off')}</span>
          </button>
          <button
            type="button"
            aria-label={t('sidebar.language')}
            className="flex h-9 items-center justify-center rounded-lg border border-[var(--sp-border)] bg-[var(--sp-surface-2)] px-2 font-mono text-xs font-semibold text-[var(--sp-text)] hover:border-[var(--sp-border-strong)]"
            onClick={onLangToggle}
          >
            <span className={lang === 'en' ? 'text-[var(--sp-text)]' : 'text-[var(--sp-faint)]'}>EN</span>
            <span className="px-1 text-[var(--sp-faint)]">|</span>
            <span className={lang === 'zh' ? 'text-[var(--sp-text)]' : 'text-[var(--sp-faint)]'}>中文</span>
          </button>
        </div>
        <div className="flex items-center justify-between px-1 text-[11px] text-[var(--sp-faint)]">
          <span>?</span>
          <span>{t('sidebar.shortcuts')}</span>
        </div>
      </div>
    </aside>
  )
}

function groupLabel(name: string, t: (key: string) => string): string {
  if (name === 'all nodes') return t('sidebar.allNodes')
  if (name === 'no runtime') return t('sidebar.noRuntime')
  return name
}

function NavButton({ active, label, onClick }: { active: boolean; label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      aria-current={active ? 'page' : undefined}
      className={`rounded-lg px-3 py-2 text-left text-xs font-semibold transition md:text-[13px] ${active ? 'bg-[var(--sp-surface-2)] text-[var(--sp-text)]' : 'text-[var(--sp-muted)] hover:bg-[var(--sp-surface-2)] hover:text-[var(--sp-text)]'}`}
      onClick={onClick}
    >
      {label}
    </button>
  )
}
