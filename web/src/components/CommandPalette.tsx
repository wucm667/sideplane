import { useEffect, useMemo, useRef, useState } from 'react'
import { filterFuzzy } from '../helpers.ts'

export interface CommandItem {
  id: string
  label: string
  hint?: string
  keywords: string
  run: () => void
}

export function CommandPalette({ open, commands, onClose }: { open: boolean; commands: CommandItem[]; onClose: () => void }) {
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)

  const results = useMemo(() => filterFuzzy(commands, query, (command) => `${command.label} ${command.keywords}`).slice(0, 50), [commands, query])

  useEffect(() => {
    if (open) {
      setQuery('')
      setActiveIndex(0)
      // Focus after the dialog mounts.
      window.setTimeout(() => inputRef.current?.focus(), 0)
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
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 px-4 pt-[12vh]"
      role="dialog"
      aria-modal="true"
      aria-label="Command palette"
      onClick={onClose}
    >
      <div
        className="w-full max-w-xl overflow-hidden rounded-xl border border-[var(--sp-border)] bg-[var(--sp-surface)] shadow-2xl"
        onClick={(event) => event.stopPropagation()}
        onKeyDown={onKeyDown}
      >
        <input
          ref={inputRef}
          className="h-12 w-full border-b border-[var(--sp-border)] bg-transparent px-4 text-sm text-[var(--sp-text)] outline-none"
          value={query}
          placeholder="Search nodes, views, actions…"
          aria-label="command palette search"
          onChange={(event) => setQuery(event.target.value)}
        />
        <ul className="max-h-80 overflow-y-auto py-1">
          {results.length === 0 && (
            <li className="px-4 py-3 text-sm text-[var(--sp-muted)]">No matches</li>
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
