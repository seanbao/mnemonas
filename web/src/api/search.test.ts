import { beforeEach, describe, expect, it, vi, type Mock } from 'vitest'
import { SearchError, searchFiles } from './search'

vi.mock('./auth', () => ({
  authFetch: vi.fn(),
}))

import { authFetch } from './auth'

const mockAuthFetch = authFetch as Mock

describe('Search API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('unwraps wrapped search responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          query: 'report',
          count: 1,
          results: [{ name: 'report.pdf', path: '/docs/report.pdf', isDir: false, size: 100, modTime: '2026-03-14T00:00:00Z' }],
        },
      }),
    })

    const result = await searchFiles('report')

    expect(result.count).toBe(1)
    expect(result.results[0].name).toBe('report.pdf')
  })

  it('trims the search query before sending the request', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          query: 'report',
          count: 0,
          results: [],
        },
      }),
    })

    await searchFiles('  report  ')

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/search?q=report')
  })

  it('rejects blank search queries without calling the API', async () => {
    await expect(searchFiles('   ')).rejects.toThrow('Search query is required')
    expect(mockAuthFetch).not.toHaveBeenCalled()
  })

  it('rejects invalid limits without calling the API', async () => {
    await expect(searchFiles('report', 0)).rejects.toThrow('Search limit must be between 1 and 100')
    await expect(searchFiles('report', 101)).rejects.toThrow('Search limit must be between 1 and 100')
    await expect(searchFiles('report', 1.5)).rejects.toThrow('Search limit must be between 1 and 100')
    expect(mockAuthFetch).not.toHaveBeenCalled()
  })

  it('preserves unavailable search metadata on failure', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      statusText: 'Service Unavailable',
      json: () => Promise.resolve({ success: false, error: { message: 'search unavailable' } }),
    })

    try {
      await searchFiles('report')
      throw new Error('Expected searchFiles to throw')
    } catch (error) {
      expect(error).toBeInstanceOf(SearchError)
      expect((error as SearchError).message).toBe('search unavailable')
      expect((error as SearchError).status).toBe(503)
      expect((error as SearchError).statusText).toBe('Service Unavailable')
      expect((error as SearchError).isUnavailable).toBe(true)
    }
  })

  it('preserves backend error codes on failure', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      statusText: 'Service Unavailable',
      json: () => Promise.resolve({ success: false, error: { code: 'SERVICE_UNAVAILABLE', message: 'search unavailable' } }),
    })

    try {
      await searchFiles('report')
      throw new Error('Expected searchFiles to throw')
    } catch (error) {
      expect(error).toBeInstanceOf(SearchError)
      expect((error as SearchError).code).toBe('SERVICE_UNAVAILABLE')
      expect((error as SearchError).isUnavailable).toBe(true)
    }
  })

  it('falls back to a generic error when the error body is invalid', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
    })

    await expect(searchFiles('report')).rejects.toThrow('Search failed')
  })

  it('rejects malformed successful wrapped search responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          results: [],
        },
      }),
    })

    await expect(searchFiles('report')).rejects.toThrow('服务器返回了无效的数据')
  })

  it('rejects wrapped search responses when success is false', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: false,
        data: {
          query: 'report',
          count: 0,
          results: [],
        },
      }),
    })

    await expect(searchFiles('report')).rejects.toThrow('服务器返回了无效的数据')
  })

  it('still accepts legacy raw search responses when valid', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        query: 'report',
        count: 1,
        results: [{ name: 'report.pdf', path: '/docs/report.pdf', is_dir: false, size: 100, mod_time: '2026-03-14T00:00:00Z' }],
      }),
    })

    const result = await searchFiles('report')

    expect(result.count).toBe(1)
    expect(result.results[0]).toMatchObject({
      name: 'report.pdf',
      path: '/docs/report.pdf',
      isDir: false,
      modTime: '2026-03-14T00:00:00Z',
    })
  })
})