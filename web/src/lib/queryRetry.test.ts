import { describe, expect, it } from 'vitest'
import { shouldRetryQuery } from './queryRetry'

describe('shouldRetryQuery', () => {
  it('does not retry unauthorized errors', () => {
    expect(shouldRetryQuery(0, { status: 401 })).toBe(false)
  })

  it('does not retry forbidden errors', () => {
    expect(shouldRetryQuery(0, { status: 403 })).toBe(false)
  })

  it('does not retry permanent client errors', () => {
    expect(shouldRetryQuery(0, { status: 400 })).toBe(false)
    expect(shouldRetryQuery(0, { status: 404 })).toBe(false)
    expect(shouldRetryQuery(0, { status: 409 })).toBe(false)
    expect(shouldRetryQuery(0, { status: 410 })).toBe(false)
  })

  it('retries transient client throttling and timeout errors', () => {
    expect(shouldRetryQuery(0, { status: 408 })).toBe(true)
    expect(shouldRetryQuery(1, { status: 425 })).toBe(true)
    expect(shouldRetryQuery(2, { status: 429 })).toBe(true)
    expect(shouldRetryQuery(3, { status: 429 })).toBe(false)
  })

  it('retries transient errors up to the existing limit', () => {
    expect(shouldRetryQuery(0, new Error('Network error'))).toBe(true)
    expect(shouldRetryQuery(1, { status: 500 })).toBe(true)
    expect(shouldRetryQuery(2, { status: 502 })).toBe(true)
    expect(shouldRetryQuery(3, { status: 500 })).toBe(false)
  })

  it('retries unknown error shapes until the retry limit is reached', () => {
    expect(shouldRetryQuery(0, 'boom')).toBe(true)
    expect(shouldRetryQuery(3, 'boom')).toBe(false)
  })
})