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

  it('sends a non-default search limit when provided', async () => {
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

    await searchFiles('report', 25)

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/search?q=report&limit=25')
  })

  it('forwards abort signals to the authenticated fetch request', async () => {
    const controller = new AbortController()
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

    await searchFiles('report', { signal: controller.signal })

    expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/search?q=report', {
      signal: controller.signal,
    })
  })

  it('rejects blank search queries without calling the API', async () => {
    await expect(searchFiles('   ')).rejects.toThrow('请输入搜索关键词')
    expect(mockAuthFetch).not.toHaveBeenCalled()
  })

  it('rejects invalid limits without calling the API', async () => {
    await expect(searchFiles('report', 0)).rejects.toThrow('搜索结果数量必须在 1 到 100 之间')
    await expect(searchFiles('report', 101)).rejects.toThrow('搜索结果数量必须在 1 到 100 之间')
    await expect(searchFiles('report', 1.5)).rejects.toThrow('搜索结果数量必须在 1 到 100 之间')
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

  it('preserves top-level backend error codes on failure', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      statusText: 'Service Unavailable',
      json: () => Promise.resolve({ code: 'SERVICE_UNAVAILABLE', message: 'search unavailable', timestamp: '2026-04-23T00:00:00Z' }),
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

  it('ignores blank legacy error messages and codes on failure', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 500,
      statusText: 'Internal Server Error',
      json: () => Promise.resolve({ success: false, error: { code: '   ', message: '   ' }, message: '  ', code: '   ' }),
    })

    await expect(searchFiles('report')).rejects.toMatchObject({
      message: '搜索失败',
      code: undefined,
    })
  })

  it('surfaces problem-json search errors', async () => {
    const body = {
      title: 'Service unavailable',
      detail: 'search index unavailable',
      status: 503,
    }

    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      statusText: 'Service Unavailable',
      headers: new Headers({ 'Content-Type': 'application/problem+json' }),
      clone: () => ({ json: () => Promise.resolve(body) }),
      json: () => Promise.resolve(body),
    })

    await expect(searchFiles('report')).rejects.toMatchObject({
      message: 'search index unavailable',
      status: 503,
      isUnavailable: true,
    })
  })

  it('falls back to a generic error when the error body is invalid', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
    })

    await expect(searchFiles('report')).rejects.toThrow('搜索失败')
  })

  it('rejects invalid JSON in successful search responses', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
    })

    await expect(searchFiles('report')).rejects.toThrow('服务器返回了无效的数据')
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

  it('rejects successful search responses with malformed result items', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          query: 'report',
          count: 1,
          results: [{ name: 'report.pdf', path: '/docs/report.pdf', size: 100, modTime: '2026-03-14T00:00:00Z' }],
        },
      }),
    })

    await expect(searchFiles('report')).rejects.toThrow('服务器返回了无效的数据')
  })

  it.each([
    ['unsafe', '/docs/./report.pdf'],
    ['relative', 'docs/report.pdf'],
    ['trailing-slash', '/docs/report.pdf/'],
  ])('rejects successful search responses with %s result paths', async (_label, path) => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          query: 'report',
          count: 1,
          results: [{ name: 'report.pdf', path, isDir: false, size: 100, modTime: '2026-03-14T00:00:00Z' }],
        },
      }),
    })

    await expect(searchFiles('report')).rejects.toThrow('服务器返回了无效的数据')
  })

  it.each([
    ['negative count', { count: -1, results: [] }],
    ['fractional count', { count: 1.5, results: [] }],
    ['unsafe count', { count: 9007199254740992, results: [] }],
    ['count smaller than results', { count: 0, results: [{ name: 'report.pdf', path: '/docs/report.pdf', isDir: false, size: 100, modTime: '2026-03-14T00:00:00Z' }] }],
    ['negative result size', { count: 1, results: [{ name: 'report.pdf', path: '/docs/report.pdf', isDir: false, size: -1, modTime: '2026-03-14T00:00:00Z' }] }],
    ['fractional result size', { count: 1, results: [{ name: 'report.pdf', path: '/docs/report.pdf', isDir: false, size: 1.5, modTime: '2026-03-14T00:00:00Z' }] }],
    ['unsafe result size', { count: 1, results: [{ name: 'report.pdf', path: '/docs/report.pdf', isDir: false, size: 9007199254740992, modTime: '2026-03-14T00:00:00Z' }] }],
  ])('rejects successful search responses with %s', async (_label, overrides) => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: true,
      json: () => Promise.resolve({
        success: true,
        data: {
          query: 'report',
          ...overrides,
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
