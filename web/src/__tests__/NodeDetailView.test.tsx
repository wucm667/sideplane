import { describe, expect, it } from 'vitest'
import { effectiveConfigDiffEntries } from '../components/NodeDetailView.tsx'
import type { ConfigDiffEntry } from '../types.ts'

describe('effectiveConfigDiffEntries', () => {
  it('treats missing or null diff values as empty', () => {
    expect(effectiveConfigDiffEntries(undefined)).toEqual([])
    expect(effectiveConfigDiffEntries(null)).toEqual([])
    expect(effectiveConfigDiffEntries({})).toEqual([])
    expect(effectiveConfigDiffEntries({ diff: null })).toEqual([])
  })

  it('returns existing diff entries', () => {
    const diff: ConfigDiffEntry[] = [
      {
        field: 'model',
        actual: 'gpt-4o',
        desired: 'gpt-4o-mini',
        change: 'update',
      },
    ]

    expect(effectiveConfigDiffEntries({ diff })).toBe(diff)
  })
})
