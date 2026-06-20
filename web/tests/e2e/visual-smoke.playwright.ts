import { expect, test, type Page, type Route } from '@playwright/test'

const now = '2026-06-20T00:00:00Z'
const operatorToken = 'test-operator-token'

const nodes = [
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
      },
    ],
    configHash: 'sha256:actual-node-a',
    drift: true,
    labels: { role: 'canary', zone: 'lab' },
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
      },
    ],
    configHash: 'sha256:actual-node-b',
    drift: false,
    labels: { role: 'batch' },
  },
]

const nodeAJobs = [
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
        },
      ],
    }),
  },
]

const effectiveConfig = {
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

const rollout = {
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

const viewports = [
  { name: 'desktop', width: 1280, height: 900 },
  { name: 'mobile', width: 390, height: 844 },
]

for (const viewport of viewports) {
  test(`main web views render with mocked API on ${viewport.name}`, async ({ page }) => {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await installFixtureApi(page)

    await page.goto('/')
    await expectView(page, 'Fleet')
    await expect(page.getByText('Fleet nodes')).toBeVisible()
    await expect(page.getByRole('button', { name: /node-a/ })).toBeVisible()

    await page.getByRole('button', { name: /node-a/ }).click()
    await expectView(page, 'node-a')
    await expect(page.getByText('Desired configuration')).toBeVisible()
    await page.getByRole('button', { name: 'Edit config' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()
    await expect(page.getByText('Change configuration')).toBeVisible()
    await expect(page.getByText('Desired config history')).toBeVisible()
    await page.getByRole('dialog').getByRole('button', { name: 'Close' }).last().click()

    await page.getByRole('button', { name: 'Rollouts' }).click()
    await expectView(page, 'Rollouts')
    await expect(page.getByText('New rollout')).toBeVisible()
    await expect(page.getByRole('heading', { name: 'rollout-a' })).toBeVisible()

    await page.getByRole('button', { name: 'Activity' }).click()
    await expectView(page, 'Activity')
    await expect(page.getByText('created fixture rollout')).toBeVisible()

    await page.getByRole('button', { name: 'Enrollment' }).click()
    await expectView(page, 'Enrollment')
    await expect(page.getByText('Enrollment token', { exact: true })).toBeVisible()
    await expect(page.getByText('Operator tokens', { exact: true })).toBeVisible()
  })
}

async function expectView(page: Page, heading: string) {
  await expect(page.getByRole('heading', { name: heading, exact: true })).toBeVisible()
  await expect(page.locator('main')).toBeVisible()

  const text = (await page.locator('body').innerText()).trim()
  expect(text.length).toBeGreaterThan(80)
}

async function installFixtureApi(page: Page) {
  await page.addInitScript((token) => {
    window.localStorage.setItem('sideplane.operatorToken', token)
    Object.defineProperty(window, 'EventSource', {
      configurable: true,
      value: undefined,
    })
  }, operatorToken)

  await page.route('**/api/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname
    const method = request.method()

    if (method === 'GET' && path === '/api/nodes') {
      return json(route, { nodes, total: nodes.length, limit: nodes.length, offset: 0 })
    }
    if (method === 'GET' && path === '/api/audit') {
      return json(route, {
        events: [
          {
            id: 'audit-a',
            actor: 'operator',
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

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body),
  })
}
