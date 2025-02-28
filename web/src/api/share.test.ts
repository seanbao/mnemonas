import { describe, it, expect, beforeEach, vi } from 'vitest'
import { accessShareWithPassword, getPublicShareItems, getShareDownloadUrl, getShareFileDownloadUrl } from './share'

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

  it('builds shared file download URL without password query', () => {
    const url = getShareFileDownloadUrl('abc123', '/folder/file.txt')
    expect(url).toBe('/s/abc123/download/folder/file.txt')
  })

  it('builds shared root download URL without password query', () => {
    expect(getShareDownloadUrl('abc123')).toBe('/s/abc123/download')
  })
  })

  describe('getPublicShareItems', () => {
    it('requests items with path only', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ path: 'docs', items: [] }),
      })

      await getPublicShareItems('share-1', { path: 'docs' })

      expect(global.fetch).toHaveBeenCalledWith('/s/share-1/items?path=docs')
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

  describe('accessShareWithPassword', () => {
    it('uses same-origin credentials so browser stores access cookie', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ id: 'share-1', type: 'file', has_password: true, permission: 'read' }),
      })

      await accessShareWithPassword('share-1', 'secret')

      expect(global.fetch).toHaveBeenCalledWith('/s/share-1', expect.objectContaining({
        method: 'POST',
        credentials: 'same-origin',
      }))
    })
  })
})
