import { expect, test, type Page } from '@playwright/test'
import { installFixtureApi } from './fixtureApi'

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
    await expect(page.getByRole('button', { name: /node-a maint/ })).toBeVisible()
    await page.waitForFunction(() => {
      return ((window as unknown as { __sideplaneEventSources?: unknown[] }).__sideplaneEventSources ?? []).length >= 1
    })

    await page.getByRole('button', { name: /node-a/ }).click()
    await expectView(page, 'node-a')
    await expect(page.getByText('maintenance', { exact: true })).toBeVisible()
    await expect(page.getByText('degraded', { exact: true })).toBeVisible()
    await page.getByRole('button', { name: 'Exit maintenance' }).click()
    await expect(page.getByRole('button', { name: 'Enter maintenance' })).toBeVisible()
    await expect(page.getByText('Desired configuration')).toBeVisible()
    await page.getByRole('button', { name: 'Edit config' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()
    await expect(page.getByText('Change configuration')).toBeVisible()
    await expect(page.getByText('Desired config history')).toBeVisible()
    await page.getByRole('dialog').getByRole('button', { name: 'Close' }).last().click()

    await page.getByRole('button', { name: 'Rollouts' }).click()
    await expectView(page, 'Rollouts')
    await expect(page.getByText('New rollout')).toBeVisible()
    await expect(page.getByLabel('Start')).toBeVisible()
    await expect(page.getByRole('heading', { name: 'rollout-a' })).toBeVisible()
    // New-rollout form template controls.
    await expect(page.getByRole('button', { name: 'Save as template' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Create from template' })).toBeVisible()

    await page.getByRole('button', { name: 'Activity' }).click()
    await expectView(page, 'Activity')
    await expect(page.getByText('created fixture rollout')).toBeVisible()
    await expect(page.getByText('operator (fixture operator)')).toBeVisible()

    await page.getByRole('button', { name: 'Enrollment' }).click()
    await expectView(page, 'Enrollment')
    await expect(page.getByText('Enrollment token', { exact: true })).toBeVisible()
    await expect(page.getByText('Operator tokens', { exact: true })).toBeVisible()
    // Token scope column, webhooks, and server settings management surfaces.
    await expect(page.getByRole('columnheader', { name: 'Scope' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Alert webhooks' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Server settings' })).toBeVisible()
    await expect(page.getByText('https://hooks.example.com/fixture')).toBeVisible()

    // Command palette opens with Ctrl/Cmd-K and is keyboard dismissable.
    await page.keyboard.press('Control+k')
    const paletteInput = page.getByPlaceholder(/Search nodes/)
    await expect(paletteInput).toBeVisible()
    await paletteInput.fill('node-a')
    await expect(page.getByRole('button', { name: /Open node node-a/ })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(paletteInput).toHaveCount(0)

    await page.waitForFunction(() => {
      return ((window as unknown as { __sideplaneEventSources?: unknown[] }).__sideplaneEventSources ?? []).length >= 2
    }, undefined, { timeout: 7000 })
    await expect(page.getByText('Operator tokens', { exact: true })).toBeVisible()
    await expect(page.getByText('live', { exact: true })).toBeVisible()
  })
}

async function expectView(page: Page, heading: string) {
  await expect(page.getByRole('heading', { name: heading, exact: true })).toBeVisible()
  await expect(page.locator('main')).toBeVisible()

  const text = (await page.locator('body').innerText()).trim()
  expect(text.length).toBeGreaterThan(80)
}
