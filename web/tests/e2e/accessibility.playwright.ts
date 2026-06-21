import AxeBuilder from '@axe-core/playwright'
import { expect, test, type Page } from '@playwright/test'
import { installFixtureApi } from './fixtureApi'

// Impact levels that fail the smoke. Moderate/minor findings are surfaced in the
// report attachment but do not fail the run, matching the wave's "serious or
// critical only" gate.
const FAILING_IMPACTS = new Set(['serious', 'critical'])

// Documented allowlist. color-contrast is driven entirely by the shared design
// tokens in src/index.css (e.g. --sp-faint); tuning the palette is a separate
// visual pass and out of scope for this structural-accessibility smoke, so the
// rule is excluded here rather than silently ignored.
const DISABLED_RULES = ['color-contrast']

const viewports = [
  { name: 'desktop', width: 1280, height: 900 },
  { name: 'mobile', width: 390, height: 844 },
]

for (const viewport of viewports) {
  test(`main web views have no serious accessibility violations on ${viewport.name}`, async ({ page }) => {
    await page.setViewportSize({ width: viewport.width, height: viewport.height })
    await installFixtureApi(page)

    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Fleet', exact: true })).toBeVisible()
    await expect(page.getByRole('button', { name: /node-a/ })).toBeVisible()
    await assertNoSeriousViolations(page, 'Fleet')

    await page.getByRole('button', { name: /node-a/ }).click()
    await expect(page.getByRole('heading', { name: 'node-a', exact: true })).toBeVisible()
    await expect(page.getByText('Desired configuration')).toBeVisible()
    await assertNoSeriousViolations(page, 'Node detail')

    // Config wizard dialog: exercise the modal surface too.
    await page.getByRole('button', { name: 'Edit config' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()
    await assertNoSeriousViolations(page, 'Config wizard dialog')
    await page.getByRole('dialog').getByRole('button', { name: 'Close' }).last().click()

    await page.getByRole('button', { name: 'Rollouts' }).click()
    await expect(page.getByRole('heading', { name: 'Rollouts', exact: true })).toBeVisible()
    await assertNoSeriousViolations(page, 'Rollouts')

    await page.getByRole('button', { name: 'Activity' }).click()
    await expect(page.getByRole('heading', { name: 'Activity', exact: true })).toBeVisible()
    await assertNoSeriousViolations(page, 'Activity')

    await page.getByRole('button', { name: 'Enrollment' }).click()
    await expect(page.getByRole('heading', { name: 'Enrollment', exact: true })).toBeVisible()
    await assertNoSeriousViolations(page, 'Enrollment')
  })
}

async function assertNoSeriousViolations(page: Page, label: string) {
  const results = await new AxeBuilder({ page })
    .withTags(['wcag2a', 'wcag2aa'])
    .disableRules(DISABLED_RULES)
    .analyze()

  const serious = results.violations.filter((violation) => FAILING_IMPACTS.has(violation.impact ?? ''))
  const summary = serious
    .map((violation) => `${violation.id} (${violation.impact}, ${violation.nodes.length} node(s)): ${violation.help}`)
    .join('\n')

  expect(serious, `Serious/critical accessibility violations on ${label}:\n${summary}`).toEqual([])
}
