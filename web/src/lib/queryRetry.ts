function getErrorStatus(error: unknown): number | undefined {
  if (!error || typeof error !== 'object') {
    return undefined
  }

  const candidate = error as { status?: unknown }
  return typeof candidate.status === 'number' ? candidate.status : undefined
}

export function shouldRetryQuery(failureCount: number, error: unknown): boolean {
  const status = getErrorStatus(error)

  // authFetch already performs token refresh + request replay once on 401.
  // Retrying the whole query again only amplifies auth failures into request storms.
  if (status === 401 || status === 403) {
    return false
  }

  return failureCount < 3
}