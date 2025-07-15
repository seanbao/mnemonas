import { describe, expect, it } from 'vitest'
import { queryClient } from './queryClient'

describe('queryClient', () => {
  it('uses stable query defaults for application data', () => {
    const defaults = queryClient.getDefaultOptions()

    expect(defaults.queries?.staleTime).toBe(60_000)
    expect(typeof defaults.queries?.retry).toBe('function')
    expect(defaults.mutations?.retry).toBe(false)
  })

  it('caps exponential retry delay at 30 seconds', () => {
    const retryDelay = queryClient.getDefaultOptions().queries?.retryDelay
    if (typeof retryDelay !== 'function') {
      throw new Error('expected retryDelay to be configured')
    }

    expect(retryDelay(0, new Error('network'))).toBe(1000)
    expect(retryDelay(2, new Error('network'))).toBe(4000)
    expect(retryDelay(10, new Error('network'))).toBe(30_000)
  })
})
