import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  compactHash,
  formatRelativeTime,
  groupRows,
  jobBadgeClasses,
  latestConfigSnapshots,
  snapshotForRuntime,
  stateBadgeClasses,
} from '../helpers.ts'
import type { Job, JobStatus, NodeState, NodeStatus, RuntimeConfigSnapshot, RuntimeStatus } from '../types.ts'

describe('formatRelativeTime', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-19T12:00:00Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('formats empty and invalid values as dashes', () => {
    expect(formatRelativeTime(undefined)).toBe('-')
    expect(formatRelativeTime('')).toBe('-')
    expect(formatRelativeTime('not-a-date')).toBe('-')
  })

  it('formats seconds minutes hours and days', () => {
    expect(formatRelativeTime('2026-06-19T11:59:45Z')).toBe('15s ago')
    expect(formatRelativeTime('2026-06-19T11:56:00Z')).toBe('4m ago')
    expect(formatRelativeTime('2026-06-19T09:00:00Z')).toBe('3h ago')
    expect(formatRelativeTime('2026-06-17T12:00:00Z')).toBe('2d ago')
  })

  it('does not report negative ages for future timestamps', () => {
    expect(formatRelativeTime('2026-06-19T12:00:30Z')).toBe('0s ago')
  })
})

describe('compactHash', () => {
  it('formats empty and short hashes', () => {
    expect(compactHash(undefined)).toBe('-')
    expect(compactHash('')).toBe('-')
    expect(compactHash('abc123')).toBe('abc123')
  })

  it('removes sha256 prefix and compacts long hashes', () => {
    expect(compactHash('sha256:1234567890abcdef1234')).toBe('1234567890ab…')
  })
})

describe('latestConfigSnapshots', () => {
  it('returns snapshots from the first completed deep probe with config snapshots', () => {
    const snapshots: RuntimeConfigSnapshot[] = [
      {
        runtimeName: 'hermes',
        runtimeType: 'hermes',
        source: 'file',
        provider: 'openai',
        model: 'gpt-4o',
        configHash: 'sha256:abc',
      },
    ]
    const jobs: Job[] = [
      job({ id: 'job-ignored', type: 'deep_probe', status: 'completed', resultJson: '{"configSnapshots":[]}' }),
      job({ id: 'job-latest', type: 'deep_probe', status: 'completed', resultJson: JSON.stringify({ configSnapshots: snapshots }) }),
      job({ id: 'job-pending', type: 'deep_probe', status: 'pending', resultJson: JSON.stringify({ configSnapshots: snapshots }) }),
    ]

    expect(latestConfigSnapshots(jobs)).toEqual(snapshots)
  })

  it('tolerates malformed deep probe result JSON', () => {
    expect(latestConfigSnapshots([job({ id: 'job-bad', type: 'deep_probe', status: 'completed', resultJson: '{' })])).toEqual([])
  })
})

describe('badge class helpers', () => {
  it('returns state-specific classes for every known node state', () => {
    expect(stateBadgeClasses('fresh')).toContain('emerald')
    expect(stateBadgeClasses('stale')).toContain('amber')
    expect(stateBadgeClasses('offline')).toContain('rose')
    expect(stateBadgeClasses('unknown' as NodeState)).toContain('var(--sp-muted)')
  })

  it('returns job-specific classes for every known job state', () => {
    expect(jobBadgeClasses('pending')).toContain('var(--sp-muted)')
    expect(jobBadgeClasses('claimed')).toContain('sky')
    expect(jobBadgeClasses('completed')).toContain('emerald')
    expect(jobBadgeClasses('failed')).toContain('rose')
    expect(jobBadgeClasses('unknown' as JobStatus)).toContain('var(--sp-muted)')
  })
})

describe('fleet helper summaries', () => {
  it('groups nodes by runtime type including nodes without runtimes', () => {
    const groups = groupRows([
      node({ nodeId: 'node-a', runtimes: [runtime({ type: 'hermes' })] }),
      node({ nodeId: 'node-b', runtimes: [runtime({ type: 'hermes' }), runtime({ type: 'openclaw' })] }),
      node({ nodeId: 'node-c', runtimes: [] }),
    ])

    expect(groups).toEqual([
      { name: 'all nodes', count: 3 },
      { name: 'hermes', count: 2 },
      { name: 'openclaw', count: 1 },
      { name: 'no runtime', count: 1 },
    ])
  })

  it('matches snapshots to runtimes by type or name', () => {
    const snapshots: RuntimeConfigSnapshot[] = [
      { runtimeName: 'custom-runtime', runtimeType: 'custom', source: 'file' },
      { runtimeName: 'hermes', runtimeType: 'hermes', source: 'file' },
    ]

    expect(snapshotForRuntime(runtime({ type: 'hermes', name: 'Hermes Agent' }), snapshots)?.runtimeType).toBe('hermes')
    expect(snapshotForRuntime(runtime({ name: 'custom-runtime' }), snapshots)?.runtimeName).toBe('custom-runtime')
  })
})

function job(overrides: Partial<Job>): Job {
  return {
    id: 'job',
    nodeId: 'node-a',
    type: 'deep_probe',
    status: 'pending',
    createdAt: '2026-06-19T12:00:00Z',
    ...overrides,
  }
}

function node(overrides: Partial<NodeStatus>): NodeStatus {
  return {
    nodeId: 'node',
    hostname: 'node',
    state: 'fresh',
    sidecarVersion: 'test',
    lastHeartbeatAt: '2026-06-19T12:00:00Z',
    configHash: '',
    drift: false,
    runtimes: [],
    ...overrides,
  }
}

function runtime(overrides: Partial<RuntimeStatus>): RuntimeStatus {
  return {
    name: 'runtime',
    type: 'hermes',
    state: 'present',
    provider: '',
    model: '',
    configHash: '',
    ...overrides,
  }
}
