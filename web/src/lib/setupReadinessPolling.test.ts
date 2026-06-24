import { afterEach, describe, expect, it, vi } from 'vitest'
import type { SetupReadiness } from '@/api/setup'
import {
  getSetupReadinessRefetchInterval,
  SETUP_READINESS_DEFERRED_MAX_REFETCH_INTERVAL_MS,
  SETUP_READINESS_DEFERRED_MIN_REFETCH_INTERVAL_MS,
  SETUP_READINESS_PENDING_REFETCH_INTERVAL_MS,
} from './setupReadinessPolling'

function readiness(overrides: Partial<SetupReadiness> = {}): SetupReadiness {
  return {
    lifecycle: 'pending',
    overall_status: 'action_required',
    prompt: true,
    generated_at: '2026-07-13T00:00:00Z',
    can_complete: false,
    can_defer: false,
    required: { completed: 0, total: 1 },
    recommended: { completed: 0, total: 0 },
    checks: [],
    summary: {
      auth_enabled: true,
      active_admin_count: 1,
      password_change_required_admin_count: 0,
      initial_password_file: 'missing',
      enabled_backup_job_count: 0,
      security_status: 'pass',
      security_blocking_check_ids: [],
    },
    ...overrides,
  }
}

describe('getSetupReadinessRefetchInterval', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('polls a deferred lifecycle at a bounded interval and schedules the expiry boundary', () => {
    vi.useFakeTimers()
    vi.setSystemTime('2026-07-13T00:00:00Z')
    const deferred = readiness({
      lifecycle: 'deferred',
      prompt: false,
      deferred_until: '2026-07-13T00:02:00Z',
    })

    expect(getSetupReadinessRefetchInterval(deferred)).toBe(SETUP_READINESS_DEFERRED_MAX_REFETCH_INTERVAL_MS)

    vi.advanceTimersByTime(90_000)
    expect(getSetupReadinessRefetchInterval(deferred)).toBe(30_000)

    vi.advanceTimersByTime(29_500)
    expect(getSetupReadinessRefetchInterval(deferred)).toBe(SETUP_READINESS_DEFERRED_MIN_REFETCH_INTERVAL_MS)

    vi.advanceTimersByTime(500)
    expect(getSetupReadinessRefetchInterval(deferred)).toBe(SETUP_READINESS_PENDING_REFETCH_INTERVAL_MS)
  })

  it('keeps pending checks fresh and never polls completed setup', () => {
    expect(getSetupReadinessRefetchInterval(readiness())).toBe(SETUP_READINESS_PENDING_REFETCH_INTERVAL_MS)
    expect(getSetupReadinessRefetchInterval(readiness({
      lifecycle: 'completed',
      prompt: false,
      completed_at: '2026-07-13T00:00:00Z',
    }))).toBe(false)
  })
})
