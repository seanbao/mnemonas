function getErrorStatus(error: unknown): number | undefined {
  if (!error || typeof error !== 'object') {
    return undefined
  }

  const candidate = error as { status?: unknown }
  return typeof candidate.status === 'number' ? candidate.status : undefined
}

function isRetryableStatus(status: number): boolean {
  if (status >= 500) {
    return true
  }

  return status === 408 || status === 425 || status === 429
}

export function shouldRetryQuery(failureCount: number, error: unknown): boolean {
  const status = getErrorStatus(error)

  // Known HTTP statuses should only retry when the failure is plausibly transient.
  // This keeps 401/403/404/409/410-style terminal states from being amplified into request storms.
  if (status !== undefined) {
    return failureCount < 3 && isRetryableStatus(status)
  }

  return failureCount < 3
}