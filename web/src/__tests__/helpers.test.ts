import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  apiURL,
  compactHash,
  filterFuzzy,
  fleetOverviewMetrics,
  formatRelativeTime,
  fuzzyMatch,
  groupRows,
  jobBadgeClasses,
  latestConfigSnapshots,
  normalizeNodeListResponse,
  rolloutBadgeClasses,
  runtimeDeploymentDisplay,
  runtimeDeploymentLabel,
  runtimeModelLabel,
  runtimeVersionLabel,
  sideplaneBasePath,
  sideplaneServerURL,
  snapshotForRuntime,
  stateBadgeClasses,
} from '../helpers.ts'
import type { Job, JobStatus, NodeState, NodeStatus, Rollout, RuntimeConfigSnapshot, RuntimeStatus } from '../types.ts'

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

describe('base path URL helpers', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('keeps API URLs unchanged without an injected base path', () => {
    expect(sideplaneBasePath()).toBe('')
    expect(apiURL('/api/nodes')).toBe('/api/nodes')
    expect(apiURL('api/events')).toBe('/api/events')
  })

  it('prefixes API URLs with the normalized injected base path', () => {
    vi.stubGlobal('window', {
      __SIDEPLANE_BASE__: '/sideplane/',
      location: { origin: 'https://ops.example' },
    } as unknown as Window)

    expect(sideplaneBasePath()).toBe('/sideplane')
    expect(apiURL('/api/nodes?selector=role%3Dcanary')).toBe('/sideplane/api/nodes?selector=role%3Dcanary')
    expect(sideplaneServerURL()).toBe('https://ops.example/sideplane')
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

describe('runtime field helpers', () => {
  it('formats provider/model and falls back without version or deployment', () => {
    expect(runtimeModelLabel({ name: 'hermes', provider: 'openai', model: 'gpt-5', version: 'v1', deploymentMode: 'container' })).toBe('openai/gpt-5')
    expect(runtimeModelLabel({ name: 'hermes', model: 'gpt-5' })).toBe('gpt-5')
    expect(runtimeModelLabel({ name: 'hermes' })).toBe('hermes')
    expect(runtimeModelLabel({ name: '', type: 'openclaw' })).toBe('openclaw')
    expect(runtimeModelLabel({ name: '' })).toBe('runtime')
  })

  it('returns deployment mode or empty when unknown', () => {
    expect(runtimeDeploymentLabel({ name: 'hermes', deploymentMode: 'systemd' })).toBe('systemd')
    expect(runtimeDeploymentLabel({ name: 'hermes', deploymentMode: 'container' })).toBe('container')
    expect(runtimeDeploymentLabel({ name: 'hermes' })).toBe('')
  })

  it('maps deployment modes to fleet table enum tokens', () => {
    expect(runtimeDeploymentDisplay({ name: 'hermes', deploymentMode: 'container' })).toBe('DOCKER')
    expect(runtimeDeploymentDisplay({ name: 'hermes', deploymentMode: 'systemd' })).toBe('SYSTEM')
    expect(runtimeDeploymentDisplay({ name: 'hermes', deploymentMode: 'local' })).toBe('LOCAL')
    expect(runtimeDeploymentDisplay({ name: 'hermes', deploymentMode: 'supervisor' } as unknown as RuntimeStatus)).toBe('SUPERVISOR')
    expect(runtimeDeploymentDisplay({ name: 'hermes' })).toBe('')
  })

  it('returns version or empty when unknown', () => {
    expect(runtimeVersionLabel({ name: 'hermes', version: ' v2026.5.1 ' })).toBe('v2026.5.1')
    expect(runtimeVersionLabel({ name: 'hermes' })).toBe('')
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

  it('returns rollout-specific classes for active and terminal states', () => {
    expect(rolloutBadgeClasses('scheduled')).toContain('violet')
    expect(rolloutBadgeClasses('running')).toContain('sky')
    expect(rolloutBadgeClasses('paused')).toContain('amber')
    expect(rolloutBadgeClasses('completed')).toContain('emerald')
    expect(rolloutBadgeClasses('failed')).toContain('rose')
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

  it('normalizes paginated and legacy node list responses', () => {
    const nodes = [node({ nodeId: 'node-a' })]

    expect(normalizeNodeListResponse(nodes)).toEqual(nodes)
    expect(normalizeNodeListResponse({ nodes, total: 1, limit: 100, offset: 0 })).toEqual(nodes)
  })

  it('aggregates fleet overview metrics from loaded client data', () => {
    const nodes = [
      node({ nodeId: 'node-a', state: 'fresh', drift: true, runtimes: [runtime({ type: 'hermes' })] }),
      node({ nodeId: 'node-b', state: 'stale', maintenance: true, runtimes: [runtime({ type: 'openclaw', outdated: true }), runtime({ type: 'hermes' })] }),
      node({ nodeId: 'node-c', state: 'offline' }),
    ]

    expect(fleetOverviewMetrics(nodes, {
      'node-a': [
        job({ id: 'job-pending', status: 'pending' }),
        job({ id: 'job-done', status: 'completed' }),
      ],
      'node-b': [job({ id: 'job-claimed', status: 'claimed' })],
    }, [
      rollout('scheduled'),
      rollout('running'),
      rollout('paused'),
      rollout('completed'),
    ])).toEqual({
      totalNodes: 3,
      freshNodes: 1,
      staleNodes: 1,
      offlineNodes: 1,
      maintenanceNodes: 1,
      driftedNodes: 1,
      outdatedSidecars: 0,
      outdatedRuntimes: 1,
      runtimeCount: 3,
      activeJobs: 2,
      activeRollouts: 3,
      runningRollouts: 1,
      pausedRollouts: 1,
    })
  })
})

describe('fuzzyMatch', () => {
  it('matches an in-order subsequence case-insensitively', () => {
    expect(fuzzyMatch('nb', 'node-b')).toBe(true)
    expect(fuzzyMatch('NDB', 'node-b')).toBe(true)
    expect(fuzzyMatch('canary', 'role=canary')).toBe(true)
  })

  it('rejects characters out of order or missing', () => {
    expect(fuzzyMatch('bn', 'node-b')).toBe(false)
    expect(fuzzyMatch('xyz', 'node-b')).toBe(false)
  })

  it('treats an empty query as a match', () => {
    expect(fuzzyMatch('', 'anything')).toBe(true)
    expect(fuzzyMatch('   ', 'anything')).toBe(true)
  })

  it('filterFuzzy keeps matching items in order', () => {
    const items = [
      { id: 'a', text: 'node-a host-a' },
      { id: 'b', text: 'node-b host-b' },
      { id: 'c', text: 'rollouts view' },
    ]
    expect(filterFuzzy(items, 'nodeb', (item) => item.text).map((item) => item.id)).toEqual(['b'])
    expect(filterFuzzy(items, '', (item) => item.text)).toEqual(items)
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

function rollout(state: Rollout['state']): Rollout {
  return {
    id: `rollout-${state}`,
    state,
    spec: {
      runtimeType: 'hermes',
      profile: 'default',
      target: { provider: 'openai', model: 'gpt-4o' },
      live: false,
    },
    batches: [],
    createdAt: '2026-06-19T12:00:00Z',
    updatedAt: '2026-06-19T12:00:00Z',
  }
}
