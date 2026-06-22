import { describe, expect, it } from 'vitest'
import { applyTerminalMessage, stepStatus } from '../ConfigWizard.tsx'
import type { TFunction } from '../i18n.ts'
import type { ConfigApplyResult } from '../types.ts'

const policyError = 'live config apply is disabled by sidecar policy (--allow-live-apply off)'

const t: TFunction = (key) => ({
  'wizard.result.dryRunCompleted': 'dry-run completed',
  'wizard.result.failedNoRollback': 'generic no rollback',
  'wizard.result.failedRollbackCompleted': 'rollback completed',
  'wizard.result.failedRollbackFailed': 'rollback failed',
  'wizard.result.failedRollbackUnknown': 'rollback unknown',
  'wizard.result.liveCompleted': 'live completed',
}[key] ?? key)

describe('config apply pipeline display helpers', () => {
  it('keeps unreached policy-rejected steps pending and surfaces the policy error', () => {
    const result: ConfigApplyResult = {
      planId: 'plan-policy',
      dryRun: false,
      steps: [
        { name: 'plan_received', status: 'completed' },
        { name: 'signature_verified', status: 'completed' },
      ],
    }

    expect(stepStatus(result, 'plan_received', policyError)).toBe('completed')
    expect(stepStatus(result, 'validated', policyError)).toBe('pending')
    expect(stepStatus(result, 'replaced', policyError)).toBe('pending')
    expect(applyTerminalMessage('failed', result, policyError, t)).toBe(policyError)
  })

  it('does not render a legacy policy rejection detail as validation failure', () => {
    const result: ConfigApplyResult = {
      planId: 'plan-policy',
      dryRun: false,
      steps: [
        { name: 'plan_received', status: 'completed' },
        { name: 'signature_verified', status: 'completed' },
        { name: 'validated', status: 'failed', detail: policyError },
      ],
    }

    expect(stepStatus(result, 'validated')).toBe('pending')
    expect(applyTerminalMessage('failed', result, null, t)).toBe(policyError)
  })
})
