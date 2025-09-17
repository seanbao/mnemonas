import { describe, it, expect, beforeEach, vi, type Mock } from 'vitest'
import {
  listFavorites,
  addFavorite,
  removeFavorite,
  checkFavorite,
  checkFavorites,
  updateFavoriteNote,
  toggleFavorite,
  FavoritesError,
} from './favorites'

// Mock authFetch
vi.mock('./auth', () => ({
  authFetch: vi.fn(),
}))

import { authFetch } from './auth'

const mockAuthFetch = authFetch as Mock
const invalidResponseMessage = '服务器返回了无效的数据'

describe('Favorites API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  describe('listFavorites', () => {
    it('returns favorites array on success', async () => {
      const mockFavorites = [
        { path: '/file1.txt', user_id: 'user1', created_at: '2024-01-01' },
        { path: '/file2.txt', user_id: 'user1', created_at: '2024-01-02' },
      ]

      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ success: true, data: { favorites: mockFavorites, count: 2 } }),
      })

      const result = await listFavorites()

      expect(result).toEqual(mockFavorites)
      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites', {})
    })

    it('forwards abort signal when listing favorites', async () => {
      const controller = new AbortController()
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ success: true, data: { favorites: [], count: 0 } }),
      })

      await listFavorites({ signal: controller.signal })

      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites', {
        signal: controller.signal,
      })
    })

    it('returns empty array when no favorites', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ success: true, data: { favorites: [], count: 0 } }),
      })

      const result = await listFavorites()

      expect(result).toEqual([])
    })

    it('throws FavoritesError on failure', async () => {
      mockAuthFetch.mockResolvedValue({
        ok: false,
        status: 500,
        json: () => Promise.resolve({ success: false, error: { message: '服务器错误' } }),
      })

      await expect(listFavorites()).rejects.toThrow(FavoritesError)
    })

  it('preserves machine-readable feature error codes', async () => {
    mockAuthFetch.mockResolvedValueOnce({
      ok: false,
      status: 503,
      json: () => Promise.resolve({ success: false, error: { code: 'FAVORITES_FEATURE_DISABLED', message: 'favorites feature disabled' } }),
    })

    await expect(listFavorites()).rejects.toMatchObject({
      status: 503,
      code: 'FAVORITES_FEATURE_DISABLED',
      isFeatureDisabled: true,
    })
  })

    it('surfaces problem-json details from favorites failures', async () => {
      const body = {
        title: 'Service unavailable',
        detail: 'favorites storage unavailable',
        status: 503,
      }

      mockAuthFetch.mockResolvedValueOnce({
        ok: false,
        status: 503,
        headers: new Headers({ 'Content-Type': 'application/problem+json' }),
        clone: () => ({ json: () => Promise.resolve(body) }),
        json: () => Promise.resolve(body),
      })

      await expect(listFavorites()).rejects.toMatchObject({
        message: 'favorites storage unavailable',
        status: 503,
      })
    })

    it('reads legacy string error responses', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: false,
        status: 500,
        json: () => Promise.resolve({ success: false, error: '旧格式错误' }),
      })

      await expect(listFavorites()).rejects.toMatchObject({
        message: '旧格式错误',
      })
    })

    it('rejects malformed successful favorite list responses', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data: { favorites: [{ path: '/file1.txt' }], count: 1 } }),
      })

      await expect(listFavorites()).rejects.toMatchObject({
        message: invalidResponseMessage,
        status: 200,
      })
    })

    it.each([
      ['unsafe', '/docs/./report.pdf'],
      ['relative', 'docs/report.pdf'],
      ['trailing-slash', '/docs/report.pdf/'],
    ])('rejects favorite list responses with %s paths', async (_label, path) => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            favorites: [{ path, user_id: 'user1', created_at: '2024-01-01' }],
            count: 1,
          },
        }),
      })

      await expect(listFavorites()).rejects.toMatchObject({
        message: invalidResponseMessage,
        status: 200,
      })
    })

    it.each([
      ['null data', null],
      ['missing count', { favorites: [] }],
      ['non-number count', { favorites: [], count: '0' }],
      ['negative count', { favorites: [], count: -1 }],
      ['fractional count', { favorites: [], count: 1.5 }],
      ['unsafe count', { favorites: [], count: 9007199254740992 }],
      [
        'count smaller than returned favorites',
        {
          favorites: [{ path: '/file1.txt', user_id: 'user1', created_at: '2024-01-01' }],
          count: 0,
        },
      ],
    ])('rejects favorite list responses with %s', async (_name, data) => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data }),
      })

      await expect(listFavorites()).rejects.toMatchObject({
        message: invalidResponseMessage,
        status: 200,
      })
    })

    it('rejects unreadable successful favorite list responses', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
      })

      await expect(listFavorites()).rejects.toMatchObject({
        message: invalidResponseMessage,
        status: 200,
      })
    })

    it('uses default message when error parsing fails', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: false,
        status: 500,
        json: () => Promise.reject(new Error('parse error')),
      })

      await expect(listFavorites()).rejects.toMatchObject({
        message: '获取收藏列表失败',
      })
    })

    it('rejects false-success favorite list responses', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: false, data: { favorites: [], count: 0 } }),
      })

      await expect(listFavorites()).rejects.toMatchObject({
        message: invalidResponseMessage,
        status: 200,
      })
    })
  })

  describe('addFavorite', () => {
    it('adds favorite successfully', async () => {
      const mockFavorite = {
        path: '/file.txt',
        user_id: 'user1',
        created_at: '2024-01-01',
      }

      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ success: true, data: mockFavorite }),
      })

      const result = await addFavorite('/file.txt')

      expect(result).toEqual(mockFavorite)
      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: '/file.txt', note: '' }),
      })
    })

    it('adds favorite with note', async () => {
      const mockFavorite = {
        path: '/file.txt',
        user_id: 'user1',
        created_at: '2024-01-01',
        note: '重要文件',
      }

      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ success: true, data: mockFavorite }),
      })

      await expect(addFavorite('/file.txt', '重要文件')).resolves.toEqual(mockFavorite)

      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: '/file.txt', note: '重要文件' }),
      })
    })

    it('normalizes path before adding', async () => {
      const mockFavorite = {
        path: '/file.txt',
        user_id: 'user1',
        created_at: '2024-01-01',
      }

      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ success: true, data: mockFavorite }),
      })

      await expect(addFavorite('file.txt')).resolves.toEqual(mockFavorite)

      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: '/file.txt', note: '' }),
      })
    })

    it('forwards abort signal when adding a favorite', async () => {
      const controller = new AbortController()
      const mockFavorite = {
        path: '/file.txt',
        user_id: 'user1',
        created_at: '2024-01-01',
      }

      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ success: true, data: mockFavorite }),
      })

      await expect(addFavorite('/file.txt', '', { signal: controller.signal })).resolves.toEqual(mockFavorite)

      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites', {
        signal: controller.signal,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: '/file.txt', note: '' }),
      })
    })

    it('throws error with conflict message on 409', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: false,
        status: 409,
        json: () => Promise.resolve({}),
      })

      await expect(addFavorite('/file.txt')).rejects.toMatchObject({
        message: '已经收藏过了',
        status: 409,
      })
    })

    it('throws FavoritesError on other errors', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: false,
        status: 500,
        json: () => Promise.resolve({ success: false, error: { message: '服务器错误' } }),
      })

      await expect(addFavorite('/file.txt')).rejects.toThrow(FavoritesError)
    })

    it('rejects malformed successful add favorite responses', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data: { path: '/file.txt' } }),
      })

      await expect(addFavorite('/file.txt')).rejects.toMatchObject({
        message: invalidResponseMessage,
        status: 200,
      })
    })

    it.each([
      ['unsafe', '/docs/./report.pdf'],
      ['relative', 'docs/report.pdf'],
      ['trailing-slash', '/docs/report.pdf/'],
    ])('rejects successful add favorite responses with %s paths', async (_label, path) => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            path,
            user_id: 'user1',
            created_at: '2024-01-01',
          },
        }),
      })

      await expect(addFavorite('/docs/report.pdf')).rejects.toMatchObject({
        message: invalidResponseMessage,
        status: 200,
      })
    })
  })

  describe('removeFavorite', () => {
    it('removes favorite successfully', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data: null, message: 'favorite removed successfully' }),
      })

      await expect(removeFavorite('/file.txt')).resolves.toEqual({ message: 'favorite removed successfully' })

      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites/file.txt', {
        method: 'DELETE',
      })
    })

    it('encodes path correctly', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data: null, message: 'favorite removed successfully' }),
      })

      await expect(removeFavorite('/documents/my file.txt')).resolves.toEqual({ message: 'favorite removed successfully' })

      expect(mockAuthFetch).toHaveBeenCalledWith(
        '/api/v1/favorites/documents/my%20file.txt',
        { method: 'DELETE' }
      )
    })

    it('forwards abort signal when removing a favorite', async () => {
      const controller = new AbortController()
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data: null, message: 'favorite removed successfully' }),
      })

      await expect(removeFavorite('/file.txt', { signal: controller.signal })).resolves.toEqual({ message: 'favorite removed successfully' })

      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites/file.txt', {
        signal: controller.signal,
        method: 'DELETE',
      })
    })

    it('throws FavoritesError on failure', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: false,
        status: 404,
        json: () => Promise.resolve({ success: false, error: { message: '收藏不存在' } }),
      })

      await expect(removeFavorite('/file.txt')).rejects.toThrow(FavoritesError)
    })

    it('uses legacy message field for remove failures', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: false,
        status: 500,
        json: () => Promise.resolve({ success: false, message: '删除收藏失败' }),
      })

      await expect(removeFavorite('/file.txt')).rejects.toMatchObject({
        message: '删除收藏失败',
      })
    })

    it('rejects malformed successful remove responses', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true }),
      })

      await expect(removeFavorite('/file.txt')).rejects.toMatchObject({
        message: invalidResponseMessage,
        status: 200,
      })
    })

    it('rejects false-success remove responses', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: false, data: null }),
      })

      await expect(removeFavorite('/file.txt')).rejects.toMatchObject({
        message: invalidResponseMessage,
        status: 200,
      })
    })
  })

  describe('checkFavorite', () => {
    it('returns true when favorited', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ success: true, data: { is_favorite: true } }),
      })

      const result = await checkFavorite('/file.txt')

      expect(result).toBe(true)
      expect(mockAuthFetch).toHaveBeenCalledWith(
        '/api/v1/favorites/check?path=%2Ffile.txt'
      )
    })

    it('returns false when not favorited', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ success: true, data: { is_favorite: false } }),
      })

      const result = await checkFavorite('/file.txt')

      expect(result).toBe(false)
    })

    it('forwards abort signal when checking a single path', async () => {
      const controller = new AbortController()
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ success: true, data: { is_favorite: true } }),
      })

      await expect(checkFavorite('/file.txt', { signal: controller.signal })).resolves.toBe(true)

      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites/check?path=%2Ffile.txt', {
        signal: controller.signal,
      })
    })

    it('throws FavoritesError on error', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: false,
        status: 500,
        json: () => Promise.resolve({ success: false, error: { message: '收藏服务不可用' } }),
      })

      await expect(checkFavorite('/file.txt')).rejects.toMatchObject({
        message: '收藏服务不可用',
        status: 500,
      })
    })

    it('rejects malformed successful favorite status responses', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data: {} }),
      })

      await expect(checkFavorite('/file.txt')).rejects.toMatchObject({
        message: invalidResponseMessage,
        status: 200,
      })
    })

    it('rejects false-success favorite status responses', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: false, data: { is_favorite: true } }),
      })

      await expect(checkFavorite('/file.txt')).rejects.toMatchObject({
        message: invalidResponseMessage,
        status: 200,
      })
    })
  })

  describe('checkFavorites', () => {
    it('checks multiple paths at once', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        json: () =>
          Promise.resolve({
            success: true,
            data: {
              favorites: {
                '/file1.txt': true,
                '/file2.txt': false,
              },
            },
          }),
      })

      const result = await checkFavorites(['/file1.txt', '/file2.txt'])

      expect(result).toEqual({
        '/file1.txt': true,
        '/file2.txt': false,
      })
      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites/check-batch', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ paths: ['/file1.txt', '/file2.txt'] }),
      })
    })

    it('forwards abort signal when checking multiple paths', async () => {
      const controller = new AbortController()
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        json: () =>
          Promise.resolve({
            success: true,
            data: {
              favorites: {
                '/file1.txt': true,
              },
            },
          }),
      })

      await expect(checkFavorites(['/file1.txt'], { signal: controller.signal })).resolves.toEqual({
        '/file1.txt': true,
      })

      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites/check-batch', {
        signal: controller.signal,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ paths: ['/file1.txt'] }),
      })
    })

    it('throws FavoritesError on non-404 errors', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: false,
        status: 500,
        json: () => Promise.resolve({ success: false, error: { message: '收藏状态查询失败' } }),
      })

      await expect(checkFavorites(['/file1.txt', '/file2.txt'])).rejects.toMatchObject({
        message: '收藏状态查询失败',
        status: 500,
      })
    })

    it('rejects malformed successful batch favorite responses', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data: {} }),
      })

      await expect(checkFavorites(['/file1.txt', '/file2.txt'])).rejects.toMatchObject({
        message: invalidResponseMessage,
        status: 200,
      })
    })

    it('rejects successful batch favorite responses with non-boolean values', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data: { favorites: { '/file1.txt': 'yes' } } }),
      })

      await expect(checkFavorites(['/file1.txt'])).rejects.toMatchObject({
        message: invalidResponseMessage,
        status: 200,
      })
    })

    it('falls back to all false when batch check is unsupported', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: false,
        status: 404,
        json: () => Promise.resolve({ success: false, error: { message: 'not found' } }),
      })

      const result = await checkFavorites(['/file1.txt', '/file2.txt'])

      expect(result).toEqual({
        '/file1.txt': false,
        '/file2.txt': false,
      })
      mockAuthFetch.mockClear()

      await expect(checkFavorites(['/second.txt', '/third.txt'])).resolves.toEqual({
        '/second.txt': false,
        '/third.txt': false,
      })
      expect(mockAuthFetch).not.toHaveBeenCalled()
    })
  })

  describe('updateFavoriteNote', () => {
    it('updates note successfully', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data: null, message: 'favorite note updated successfully' }),
      })

      await expect(updateFavoriteNote('/file.txt', '新备注')).resolves.toEqual({ message: 'favorite note updated successfully' })

      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites/file.txt', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ note: '新备注' }),
      })
    })

    it('forwards abort signal when updating a favorite note', async () => {
      const controller = new AbortController()
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data: null, message: 'favorite note updated successfully' }),
      })

      await expect(updateFavoriteNote('/file.txt', '新备注', { signal: controller.signal })).resolves.toEqual({ message: 'favorite note updated successfully' })

      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites/file.txt', {
        signal: controller.signal,
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ note: '新备注' }),
      })
    })

    it('throws FavoritesError on failure', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: false,
        status: 404,
        json: () => Promise.resolve({ success: false, error: { message: '收藏不存在' } }),
      })

      await expect(updateFavoriteNote('/file.txt', 'note')).rejects.toThrow(
        FavoritesError
      )
    })

    it('rejects malformed successful update note responses', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true }),
      })

      await expect(updateFavoriteNote('/file.txt', 'note')).rejects.toMatchObject({
        message: invalidResponseMessage,
        status: 200,
      })
    })

    it('rejects false-success update note responses', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: false, data: null }),
      })

      await expect(updateFavoriteNote('/file.txt', 'note')).rejects.toMatchObject({
        message: invalidResponseMessage,
        status: 200,
      })
    })
  })

  describe('toggleFavorite', () => {
    it('removes favorite when currently favorited', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data: null }),
      })

      const result = await toggleFavorite('/file.txt', true)

      expect(result).toBe(false)
      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites/file.txt', {
        method: 'DELETE',
      })
    })

    it('adds favorite when not currently favorited', async () => {
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: { path: '/file.txt', user_id: 'user1', created_at: '2024-01-01' },
        }),
      })

      const result = await toggleFavorite('/file.txt', false)

      expect(result).toBe(true)
      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: '/file.txt', note: '' }),
      })
    })

    it('forwards abort signal through add toggle operations', async () => {
      const controller = new AbortController()
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: { path: '/file.txt', user_id: 'user1', created_at: '2024-01-01' },
        }),
      })

      await expect(toggleFavorite('/file.txt', false, { signal: controller.signal })).resolves.toBe(true)

      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites', {
        signal: controller.signal,
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: '/file.txt', note: '' }),
      })
    })

    it('forwards abort signal through remove toggle operations', async () => {
      const controller = new AbortController()
      mockAuthFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data: null }),
      })

      await expect(toggleFavorite('/file.txt', true, { signal: controller.signal })).resolves.toBe(false)

      expect(mockAuthFetch).toHaveBeenCalledWith('/api/v1/favorites/file.txt', {
        signal: controller.signal,
        method: 'DELETE',
      })
    })
  })

  describe('FavoritesError', () => {
    it('has correct name', () => {
      const error = new FavoritesError('test', 404)
      expect(error.name).toBe('FavoritesError')
    })

    it('isNotFound returns true for 404', () => {
      const error = new FavoritesError('not found', 404)
      expect(error.isNotFound).toBe(true)
    })

    it('isNotFound returns false for other status', () => {
      const error = new FavoritesError('error', 500)
      expect(error.isNotFound).toBe(false)
    })

    it('isConflict returns true for 409', () => {
      const error = new FavoritesError('conflict', 409)
      expect(error.isConflict).toBe(true)
    })

    it('isConflict returns false for other status', () => {
      const error = new FavoritesError('error', 500)
      expect(error.isConflict).toBe(false)
    })
  })
})
