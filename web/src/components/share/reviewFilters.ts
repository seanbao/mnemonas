export type ShareReviewFilter = 'all' | 'review' | 'expiring' | 'passwordless' | 'broad' | 'stale'

const SHARE_REVIEW_FILTERS = new Set<ShareReviewFilter>(['all', 'review', 'expiring', 'passwordless', 'broad', 'stale'])

export function normalizeShareReviewFilter(value: string | null | undefined): ShareReviewFilter {
  return value && SHARE_REVIEW_FILTERS.has(value as ShareReviewFilter)
    ? value as ShareReviewFilter
    : 'all'
}
