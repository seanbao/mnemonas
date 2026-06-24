import type { SetupReadiness } from '@/api/setup'

export const SETUP_READINESS_PENDING_REFETCH_INTERVAL_MS = 30_000
export const SETUP_READINESS_DEFERRED_MAX_REFETCH_INTERVAL_MS = 60_000
export const SETUP_READINESS_DEFERRED_MIN_REFETCH_INTERVAL_MS = 1_000

export function getSetupReadinessRefetchInterval(
  readiness: SetupReadiness | undefined,
  now = Date.now(),
): number | false {
  if (!readiness || readiness.lifecycle === 'completed') {
    return false
  }
  if (readiness.lifecycle === 'pending') {
    return SETUP_READINESS_PENDING_REFETCH_INTERVAL_MS
  }

  const deferredUntil = readiness.deferred_until
    ? Date.parse(readiness.deferred_until)
    : Number.NaN
  if (!Number.isFinite(deferredUntil) || deferredUntil <= now) {
    return SETUP_READINESS_PENDING_REFETCH_INTERVAL_MS
  }

  return Math.min(
    SETUP_READINESS_DEFERRED_MAX_REFETCH_INTERVAL_MS,
    Math.max(SETUP_READINESS_DEFERRED_MIN_REFETCH_INTERVAL_MS, deferredUntil - now),
  )
}
