import { describe, it, expect, beforeEach, vi } from 'vitest'
import { getPublicShareItems, getShareFileDownloadUrl } from './share'

describe('Share API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    global.fetch = vi.fn()
  })

  describe('URL helpers', () => {
  it('encodes shared folder download path segments', () => {
    const url = getShareFileDownloadUrl('abc123', '/folder/my file.txt')
    expect(url).toBe('/s/abc123/download/folder/my%20file.txt')
  })

  it('preserves path separators for nested files', () => {
    const url = getShareFileDownloadUrl('abc123', 'folder/sub/file.txt')
    expect(url).toBe('/s/abc123/download/folder/sub/file.txt')
  })

  it('adds encoded password query', () => {
    const url = getShareFileDownloadUrl('abc123', '/folder/file.txt', 'p@ss word')
    expect(url).toBe('/s/abc123/download/folder/file.txt?password=p%40ss%20word')
  })
  })

  describe('getPublicShareItems', () => {
    it('requests items with path and password', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ path: 'docs', items: [] }),
      })

      await getPublicShareItems('share-1', { path: 'docs', password: 'secret' })

      expect(global.fetch).toHaveBeenCalledWith('/s/share-1/items?path=docs&password=secret')
    })

    it('throws ShareError on failure', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 404,
        json: () => Promise.resolve({ error: '分享不存在' }),
      })

      await expect(getPublicShareItems('missing')).rejects.toMatchObject({
        message: '分享不存在',
        status: 404,
      })
    })
  })
})
