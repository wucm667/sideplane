import { useEffect, useMemo, useRef, useState } from 'react'
import { filterFuzzy } from '../helpers.ts'
import { useT } from '../i18n.ts'

export interface CommandItem {
  id: string
  label: string
  hint?: string
  keywords: string
  run: () => void
}

export function CommandPalette({ open, commands, onClose }: { open: boolean; commands: CommandItem[]; onClose: () => void }) {
  const { t } = useT()
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const panelRef = useRef<HTMLDivElement>(null)
  const openerRef = useRef<HTMLElement | null>(null)

  const results = useMemo(() => filterFuzzy(commands, query, (command) => `${command.label} ${command.keywords}`).slice(0, 50), [commands, query])

  useEffect(() => {
    if (open) {
      openerRef.current = document.activeElement as HTMLElement | null
      setQuery('')
      setActiveIndex(0)
      // Focus after the dialog mounts.
      window.setTimeout(() => inputRef.current?.focus(), 0)
    } else {
      // Restore focus to whatever opened the palette.
      openerRef.current?.focus?.()
    }
  }, [open])

  useEffect(() => {
    setActiveIndex(0)
  }, [query])

  if (!open) return null

  const runActive = () => {
    const command = results[activeIndex]
    if (command) {
      command.run()
      onClose()
    }
  }

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'Escape') {
      event.preventDefault()
      onClose()
    } else if (event.key === 'ArrowDown') {
      event.preventDefault()
      setActiveIndex((index) => (results.length === 0 ? 0 : (index + 1) % results.length))
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      setActiveIndex((index) => (results.length === 0 ? 0 : (index - 1 + results.length) % results.length))
    } else if (event.key === 'Enter') {
      event.preventDefault()
      runActive()
    } else if (event.key === 'Tab') {
      // Keep focus inside the palette instead of leaking to the page behind it.
      const container = panelRef.current
      if (!container) return
      const focusable = container.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])',
      )
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      const active = document.activeElement
      if (event.shiftKey && (active === first || !container.contains(active))) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && active === last) {
        event.preventDefault()
        first.focus()
      }
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 px-4 pt-[12vh]"
      role="dialog"
      aria-modal="true"
      aria-label={t('command.palette')}
      onClick={onClose}
    >
      <div
        ref={panelRef}
        className="w-full max-w-xl overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-2xl"
        onClick={(event) => event.stopPropagation()}
        onKeyDown={onKeyDown}
      >
        <input
          ref={inputRef}
          className="h-12 w-full border-b border-[var(--sp-border)] bg-transparent px-4 text-sm text-[var(--sp-text)] outline-none"
          value={query}
          placeholder={t('command.searchPlaceholder')}
          aria-label={t('command.search')}
          onChange={(event) => setQuery(event.target.value)}
        />
        <ul className="max-h-80 overflow-y-auto py-1">
          {results.length === 0 && (
            <li className="px-4 py-3 text-sm text-[var(--sp-muted)]">{t('command.noMatches')}</li>
          )}
          {results.map((command, index) => (
            <li key={command.id}>
              <button
                type="button"
                className={`flex w-full items-center justify-between gap-3 px-4 py-2 text-left text-sm ${index === activeIndex ? 'bg-[var(--sp-surface-2)]' : ''}`}
                onMouseEnter={() => setActiveIndex(index)}
                onClick={() => {
                  command.run()
                  onClose()
                }}
              >
                <span className="truncate font-medium text-[var(--sp-text)]">{command.label}</span>
                {command.hint && <span className="shrink-0 truncate font-mono text-xs text-[var(--sp-faint)]">{command.hint}</span>}
              </button>
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}
