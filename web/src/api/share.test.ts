import { describe, it, expect, beforeEach, vi } from 'vitest'
import { accessShareWithPassword, copyShareUrl, createShare, downloadShare, getPublicShare, getPublicShareItems, getShare, listShares, getShareDownloadUrl, getShareFileDownloadUrl, ShareError } from './share'

const mockCopyTextToClipboard = vi.fn()

vi.mock('@/lib/utils', async () => {
  const actual = await vi.importActual('@/lib/utils')
  return {
    ...actual,
    copyTextToClipboard: (...args: unknown[]) => mockCopyTextToClipboard(...args),
  }
})

describe('Share API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    global.fetch = vi.fn()
    mockCopyTextToClipboard.mockResolvedValue(undefined)
  })

  describe('URL helpers', () => {
  it('encodes shared folder download path segments', () => {
    const url = getShareFileDownloadUrl('abc123', '/folder/my file.txt')
    expect(url).toBe('/s/abc123/download/folder/my%20file.txt')
  })

  describe('downloadShare', () => {
    it('downloads the root shared file as a blob', async () => {
      const blob = new Blob(['share-content'], { type: 'text/plain' })
      const createObjectURLSpy = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:share')
      const revokeObjectURLSpy = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        headers: new Headers({ 'Content-Disposition': 'attachment; filename="secret.txt"' }),
        blob: () => Promise.resolve(blob),
      })

      await downloadShare('share-1')

      expect(global.fetch).toHaveBeenCalledWith('/s/share-1/download', { credentials: 'same-origin' })
      expect(createObjectURLSpy).toHaveBeenCalledWith(blob)
      expect(clickSpy).toHaveBeenCalled()
      expect(revokeObjectURLSpy).toHaveBeenCalledWith('blob:share')
    })

    it('throws a ShareError with structured details when download fails', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 401,
        json: () => Promise.resolve({ success: false, error: { code: 'PASSWORD_REQUIRED', message: '访问凭证已失效，请重新输入密码' } }),
      })

      await expect(downloadShare('share-1')).rejects.toMatchObject({
        message: '访问凭证已失效，请重新输入密码',
        status: 401,
        code: 'PASSWORD_REQUIRED',
      })
    })
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

    it('reads structured public share item errors', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 500,
        json: () => Promise.resolve({ success: false, error: { message: 'folder unavailable' } }),
      })

      await expect(getPublicShareItems('share-1')).rejects.toMatchObject({
        message: 'folder unavailable',
        status: 500,
      })
    })

    it('surfaces rate limit errors for shared folder listing', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 429,
        json: () => Promise.resolve({ success: false, error: { code: 'SHARE_PASSWORD_RATE_LIMITED', message: 'too many attempts, try later' } }),
      })

      await expect(getPublicShareItems('share-1')).rejects.toMatchObject({
        message: 'too many attempts, try later',
        status: 429,
        code: 'SHARE_PASSWORD_RATE_LIMITED',
      })
    })

    it('rejects malformed successful folder item responses', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ path: 'docs' }),
      })

      await expect(getPublicShareItems('share-1')).rejects.toMatchObject({
        message: '分享文件夹响应无效',
        status: 200,
      })
    })
  })

  describe('getPublicShare', () => {
    it('reads wrapped public share errors', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 404,
        json: () => Promise.resolve({ success: false, error: { message: 'share missing' } }),
      })

      await expect(getPublicShare('missing')).rejects.toMatchObject({
        message: 'share missing',
        status: 404,
      })
    })

    it('rejects malformed successful public share responses', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve(null),
      })

      await expect(getPublicShare('share-1')).rejects.toMatchObject({
        message: '分享信息无效',
        status: 200,
      })
    })
  })

  describe('authenticated share APIs', () => {
    it('unwraps share list responses', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: [{ id: 'share-1', path: '/docs/a.txt', type: 'file', created_by: 'u1', created_at: '2026-03-13T00:00:00Z', has_password: false, permission: 'read', enabled: true, access_count: 0, url: '/s/share-1' }],
        }),
      })

      const result = await listShares()

      expect(result).toHaveLength(1)
      expect(result[0].id).toBe('share-1')
    })

    it('unwraps create share responses', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 201,
        json: () => Promise.resolve({
          success: true,
          data: { id: 'share-2', path: '/docs/b.txt', type: 'file', created_by: 'u1', created_at: '2026-03-13T00:00:00Z', has_password: false, permission: 'read', enabled: true, access_count: 0, url: '/s/share-2' },
        }),
      })

      const result = await createShare({ path: '/docs/b.txt' })

      expect(result.id).toBe('share-2')
    })

    it('reads structured share errors', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 403,
        json: () => Promise.resolve({
          success: false,
          error: { code: 'FORBIDDEN', message: 'forbidden' },
        }),
      })

      await expect(listShares()).rejects.toMatchObject({
        message: 'forbidden',
        status: 403,
      })
    })

    it('preserves machine-readable codes for disabled share features', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 503,
        json: () => Promise.resolve({
          success: false,
          error: { code: 'SHARE_FEATURE_DISABLED', message: 'share feature disabled' },
        }),
      })

      await expect(createShare({ path: '/docs/b.txt' })).rejects.toMatchObject({
        message: 'share feature disabled',
        status: 503,
        code: 'SHARE_FEATURE_DISABLED',
        isFeatureDisabled: true,
      })
    })

    it('rejects malformed successful share list responses', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data: [{}] }),
      })

      await expect(listShares()).rejects.toMatchObject({
        message: '获取分享列表响应无效',
        status: 200,
      })
    })

    it('rejects false-success share list responses', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: false, data: [] }),
      })

      await expect(listShares()).rejects.toMatchObject({
        message: '获取分享列表响应无效',
        status: 200,
      })
    })

    it('rejects malformed successful share detail responses', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data: { id: 'share-1' } }),
      })

      await expect(getShare('share-1')).rejects.toMatchObject({
        message: '获取分享详情响应无效',
        status: 200,
      })
    })

    it('rejects malformed successful create share responses', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 201,
        json: () => Promise.resolve({ success: true, data: { id: 'share-2' } }),
      })

      await expect(createShare({ path: '/docs/b.txt' })).rejects.toMatchObject({
        message: '创建分享响应无效',
        status: 201,
      })
    })

    it('rejects false-success create share responses', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 201,
        json: () => Promise.resolve({ success: false, data: { id: 'share-2' } }),
      })

      await expect(createShare({ path: '/docs/b.txt' })).rejects.toMatchObject({
        message: '创建分享响应无效',
        status: 201,
      })
    })

    it('copies relative share URLs as absolute URLs', async () => {
      vi.stubGlobal('location', { origin: 'https://nas.example.com' })

      await copyShareUrl({
        id: 'share-1',
        path: '/docs/a.txt',
        type: 'file',
        created_by: 'u1',
        created_at: '2026-03-13T00:00:00Z',
        has_password: false,
        permission: 'read',
        enabled: true,
        access_count: 0,
        url: '/s/share-1',
      })

      expect(mockCopyTextToClipboard).toHaveBeenCalledWith('https://nas.example.com/s/share-1')
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

    it('uses wrapped password error details', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 401,
        json: () => Promise.resolve({ success: false, error: { message: 'password rejected' } }),
      })

      await expect(accessShareWithPassword('share-1', 'bad')).rejects.toMatchObject({
        message: 'password rejected',
        status: 401,
      })
    })

    it('surfaces rate limit errors for password-protected share access', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 429,
        json: () => Promise.resolve({ success: false, error: { code: 'SHARE_PASSWORD_RATE_LIMITED', message: 'too many attempts, try later' } }),
      })

      await expect(accessShareWithPassword('share-1', 'bad')).rejects.toMatchObject({
        message: 'too many attempts, try later',
        status: 429,
        code: 'SHARE_PASSWORD_RATE_LIMITED',
      })
    })

    it('rejects malformed successful password access responses', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
      })

      await expect(accessShareWithPassword('share-1', 'secret')).rejects.toMatchObject({
        message: '分享信息无效',
        status: 200,
      })
    })
  })

  describe('ShareError', () => {
    it('reports feature-disabled state from code', () => {
      const error = new ShareError('share feature disabled', 503, 'SHARE_FEATURE_DISABLED')

      expect(error.isFeatureDisabled).toBe(true)
    })
  })
})
