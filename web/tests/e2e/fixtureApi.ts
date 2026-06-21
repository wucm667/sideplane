import type { Page, Route } from '@playwright/test'

// Shared mocked-API fixture for the Playwright smoke and accessibility checks.
// Everything here is in-memory; no real server, machine, or network is touched.

export const now = '2026-06-20T00:00:00Z'
export const operatorToken = 'test-operator-token'

export const nodes = [
  {
    nodeId: 'node-a',
    hostname: 'alpha-fixture',
    state: 'fresh',
    sidecarVersion: 'test',
    lastHeartbeatAt: now,
    runtimes: [
      {
        name: 'Hermes Agent',
        type: 'hermes',
        state: 'running',
        provider: 'openai',
        model: 'gpt-4o',
        configHash: 'sha256:actual-node-a',
        health: { state: 'degraded', reason: 'fixture degraded runtime' },
      },
    ],
    configHash: 'sha256:actual-node-a',
    drift: true,
    labels: { role: 'canary', zone: 'lab' },
    maintenance: true,
  },
  {
    nodeId: 'node-b',
    hostname: 'beta-fixture',
    state: 'stale',
    sidecarVersion: 'test',
    lastHeartbeatAt: now,
    runtimes: [
      {
        name: 'OpenClaw',
        type: 'openclaw',
        state: 'running',
        provider: 'anthropic',
        model: 'claude-3-5-sonnet',
        configHash: 'sha256:actual-node-b',
        health: { state: 'healthy' },
      },
    ],
    configHash: 'sha256:actual-node-b',
    drift: false,
    labels: { role: 'batch' },
  },
]

export const nodeAJobs = [
  {
    id: 'job-deep-probe-a',
    nodeId: 'node-a',
    type: 'deep_probe',
    status: 'completed',
    createdAt: now,
    finishedAt: now,
    resultJson: JSON.stringify({
      runtimes: nodes[0].runtimes,
      configSnapshots: [
        {
          runtimeName: 'Hermes Agent',
          runtimeType: 'hermes',
          profile: 'default',
          source: 'fixture',
          configPath: 'fixture://hermes/default',
          provider: 'openai',
          model: 'gpt-4o',
          configHash: 'sha256:actual-node-a',
          health: { state: 'degraded', reason: 'fixture degraded runtime' },
        },
      ],
    }),
  },
]

export const effectiveConfig = {
  nodeId: 'node-a',
  runtimeType: 'hermes',
  profile: 'default',
  effective: { provider: 'openai', model: 'gpt-4o' },
  desiredHash: 'sha256:desired-node-a',
  actual: {
    runtimeName: 'Hermes Agent',
    runtimeType: 'hermes',
    profile: 'default',
    source: 'fixture',
    configPath: 'fixture://hermes/default',
    provider: 'openai',
    model: 'gpt-4o-mini',
    configHash: 'sha256:actual-node-a',
  },
  diff: [
    {
      field: 'model',
      actual: 'gpt-4o-mini',
      desired: 'gpt-4o',
      change: 'update',
    },
  ],
}

export const rollout = {
  id: 'rollout-a',
  spec: {
    selector: { role: 'canary' },
    runtimeType: 'hermes',
    profile: 'default',
    target: { provider: 'openai', model: 'gpt-4o' },
    batchSize: 1,
    live: false,
  },
  state: 'running',
  batches: [
    {
      index: 0,
      nodeIds: ['node-a'],
      state: 'running',
      nodes: {
        'node-a': {
          nodeId: 'node-a',
          jobId: 'job-deep-probe-a',
          state: 'dispatched',
          startedAt: now,
        },
      },
    },
  ],
  createdAt: now,
  updatedAt: now,
}

export async function installFixtureApi(page: Page) {
  await page.addInitScript((token) => {
    window.localStorage.setItem('sideplane.operatorToken', token)
    window.__sideplaneEventSources = []
    let openedSources = 0
    class FixtureEventSource extends EventTarget {
      url: string
      onopen: ((event: Event) => void) | null = null
      onerror: ((event: Event) => void) | null = null
      closed = false

      constructor(url: string) {
        super()
        this.url = url
        window.__sideplaneEventSources.push(this)
        window.setTimeout(() => {
          if (this.closed) return
          this.onopen?.(new Event('open'))
          if (openedSources === 0) {
            openedSources += 1
            window.setTimeout(() => {
              if (!this.closed) {
                this.onerror?.(new Event('error'))
              }
            }, 0)
            return
          }
          openedSources += 1
        }, 0)
      }

      close() {
        this.closed = true
      }
    }
    Object.defineProperty(window, 'EventSource', {
      configurable: true,
      value: FixtureEventSource,
    })
  }, operatorToken)

  let ticketSequence = 0
  await page.route('**/api/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname
    const method = request.method()

    if (method === 'POST' && path === '/api/events/tickets') {
      ticketSequence += 1
      return json(route, { ticket: `fixture-ticket-${ticketSequence}`, expiresAt: now })
    }
    if (method === 'GET' && path === '/api/nodes') {
      return json(route, { nodes, total: nodes.length, limit: nodes.length, offset: 0 })
    }
    if (method === 'GET' && path === '/api/audit') {
      return json(route, {
        events: [
          {
            id: 'audit-a',
            actor: 'operator',
            actorName: 'fixture operator',
            action: 'rollout.create',
            targetNode: 'node-a',
            detail: 'created fixture rollout',
            createdAt: now,
          },
        ],
      })
    }
    if (method === 'GET' && path === '/api/rollouts') {
      return json(route, { rollouts: [rollout], total: 1, limit: 50, offset: 0 })
    }
    if (method === 'GET' && path === '/api/operator-tokens') {
      return json(route, {
        tokens: [
          {
            id: 'operator-token-a',
            name: 'fixture operator',
            scope: 'admin',
            createdAt: now,
          },
        ],
      })
    }
    if (method === 'GET' && path === '/api/webhooks') {
      return json(route, {
        webhooks: [
          {
            id: 'webhook-a',
            url: 'https://hooks.example.com/fixture',
            events: ['rollout.paused', 'rollout.failed'],
            hasSecret: true,
            disabled: false,
            createdAt: now,
          },
        ],
      })
    }
    if (method === 'GET' && path === '/api/settings') {
      return json(route, { expectedSidecarVersion: 'v1.0.0' })
    }
    if (method === 'GET' && path === '/api/rollout-templates') {
      return json(route, {
        templates: [
          {
            id: 'template-a',
            name: 'fixture canary',
            spec: {
              selector: { role: 'canary' },
              runtimeType: 'hermes',
              target: { provider: 'openai', model: 'gpt-4o' },
              batchSize: 1,
              live: false,
            },
            createdAt: now,
          },
        ],
      })
    }
    if (method === 'GET' && path === '/api/config/effective') {
      return json(route, effectiveConfig)
    }
    if (method === 'GET' && path === '/api/config/desired/history') {
      return json(route, {
        history: [
          {
            id: 'desired-history-a',
            config: {
              global: { provider: 'openai', model: 'gpt-4o' },
            },
            desiredHash: 'sha256:desired-node-a',
            updatedAt: now,
            actor: 'operator',
          },
        ],
        total: 1,
        limit: 8,
        offset: 0,
      })
    }

    const maintenanceMatch = path.match(/^\/api\/nodes\/([^/]+)\/maintenance$/)
    if (method === 'PUT' && maintenanceMatch) {
      const nodeId = decodeURIComponent(maintenanceMatch[1])
      const payload = request.postDataJSON() as { maintenance?: boolean }
      return json(route, { nodeId, maintenance: Boolean(payload.maintenance) })
    }

    const jobMatch = path.match(/^\/api\/nodes\/([^/]+)\/jobs$/)
    if (method === 'GET' && jobMatch) {
      const nodeId = decodeURIComponent(jobMatch[1])
      return json(route, nodeId === 'node-a' ? nodeAJobs : [])
    }

    const backupsMatch = path.match(/^\/api\/nodes\/([^/]+)\/backups$/)
    if (method === 'GET' && backupsMatch) {
      return json(route, {
        backups: [
          {
            ref: 'backup://fixture/node-a/plan-a',
            sourceJobId: 'job-config-apply-a',
            runtimeType: 'hermes',
            profile: 'default',
            configHash: 'sha256:actual-node-a',
            createdAt: now,
          },
        ],
        total: 1,
        limit: 50,
      })
    }

    return json(route, { message: `Unhandled ${method} ${path}` }, 404)
  })
}

export async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body),
  })
}
