import { beforeEach, describe, expect, it } from 'vitest'
import { loadHistory, mergeHistory } from './userAccessHistory'

const storageKey = 'mnemonas:test:directory-access-history'
const validEntry = {
  id: 'review-1',
  recordedAt: '2026-07-18T08:00:00Z',
  reviewer: 'admin',
  title: '用户矩阵',
  path: '/team',
  preview: false,
  users: 2,
  readAllowed: 1,
  writeAllowed: 1,
  relatedShares: 0,
  reportText: '目录权限复核记录',
}

describe('directory access history storage', () => {
  beforeEach(() => {
    window.localStorage.clear()
  })

  it('keeps only entries with non-empty strings and optional non-empty reviewers', () => {
    const withoutReviewer: Partial<typeof validEntry> = { ...validEntry, id: 'review-2' }
    delete withoutReviewer.reviewer
    window.localStorage.setItem(storageKey, JSON.stringify([
      validEntry,
      withoutReviewer,
      { ...validEntry, id: '' },
      { ...validEntry, recordedAt: 'not-a-date' },
      { ...validEntry, reviewer: '  ' },
      { ...validEntry, title: '' },
      { ...validEntry, path: '' },
      { ...validEntry, reportText: '' },
    ]))

    expect(loadHistory(storageKey)).toEqual([validEntry, withoutReviewer])
  })

  it.each([
    ['negative users', { users: -1 }],
    ['fractional read count', { readAllowed: 0.5 }],
    ['unsafe write count', { writeAllowed: Number.MAX_SAFE_INTEGER + 1 }],
    ['negative share count', { relatedShares: -1 }],
  ])('rejects entries with %s', (_label, override) => {
    window.localStorage.setItem(storageKey, JSON.stringify([{ ...validEntry, ...override }]))
    expect(loadHistory(storageKey)).toEqual([])
  })

  it('returns an empty history for malformed JSON without throwing', () => {
    window.localStorage.setItem(storageKey, '{invalid')
    expect(() => loadHistory(storageKey)).not.toThrow()
    expect(loadHistory(storageKey)).toEqual([])
  })

  it('keeps repeated reviews of the same path in event order and limits the result to five', () => {
    const repeatedReviews = Array.from({ length: 6 }, (_, index) => ({
      ...validEntry,
      id: `review-${index + 1}`,
      recordedAt: `2026-07-18T0${index}:00:00Z`,
    }))

    expect(mergeHistory(repeatedReviews, []).map((entry) => entry.id)).toEqual([
      'review-1',
      'review-2',
      'review-3',
      'review-4',
      'review-5',
    ])
  })

  it('deduplicates server and browser copies only when they have the same event ID', () => {
    const server = { ...validEntry, reviewer: 'server-admin' }
    const browserCopy = { ...validEntry, reviewer: 'browser-admin' }
    const anotherReview = {
      ...validEntry,
      id: 'review-2',
      recordedAt: '2026-07-18T09:00:00Z',
    }

    expect(mergeHistory([server], [browserCopy, anotherReview])).toEqual([
      server,
      anotherReview,
    ])
  })
})
