import { describe, it, expect, beforeEach, vi } from 'vitest'
import { accessShareWithPassword, copyShareUrl, createShare, deleteShare, downloadShare, formatDuration, formatExpiration, formatShareUrl, getPublicShare, getPublicShareItems, getShare, listShares, getShareDownloadUrl, getShareFileDownloadUrl, ShareError, updateShare, type Share } from './share'

const mockCopyTextToClipboard = vi.fn()

vi.mock('@/lib/utils', async () => {
  const actual = await vi.importActual('@/lib/utils')
  return {
    ...actual,
    copyTextToClipboard: (...args: unknown[]) => mockCopyTextToClipboard(...args),
  }
})

function createValidShare(overrides: Partial<Share> = {}): Share {
  return {
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
    ...overrides,
  }
}

describe('Share API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    global.fetch = vi.fn()
    mockCopyTextToClipboard.mockResolvedValue(undefined)
  })

  describe('URL helpers', () => {
    it('encodes shared folder download path segments', () => {
      const url = getShareFileDownloadUrl('abc123', '/folder/my file.txt')
      expect(url).toBe('/api/v1/public/shares/abc123/download/folder/my%20file.txt')
    })

    it('keeps absolute http and https share URLs', () => {
      expect(formatShareUrl('https://nas.example.com/s/share-1', 'https://local.example'))
        .toBe('https://nas.example.com/s/share-1')
      expect(formatShareUrl('http://nas.example.com/s/share-1', 'https://local.example'))
        .toBe('http://nas.example.com/s/share-1')
    })

    it('resolves relative share URLs against the current origin', () => {
      expect(formatShareUrl('/s/share-1', 'https://local.example')).toBe('https://local.example/s/share-1')
      expect(formatShareUrl('s/share-1', 'https://local.example')).toBe('https://local.example/s/share-1')
    })

    it('does not treat non-http schemes as trusted absolute share URLs', () => {
      expect(formatShareUrl('httpx://evil.example/s/share-1', 'https://local.example'))
        .toBe('https://local.example/httpx://evil.example/s/share-1')
      expect(formatShareUrl('javascript:alert(1)', 'https://local.example'))
        .toBe('https://local.example/javascript:alert(1)')
    })
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

      expect(global.fetch).toHaveBeenCalledWith('/api/v1/public/shares/share-1/download', { credentials: 'same-origin' })
      expect(createObjectURLSpy).toHaveBeenCalledWith(blob)
      expect(clickSpy).toHaveBeenCalled()
      expect(revokeObjectURLSpy).toHaveBeenCalledWith('blob:share')
    })

    it('downloads nested shared files and falls back to the file path name', async () => {
      const blob = new Blob(['nested-share-content'], { type: 'text/plain' })
      vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:nested-share')
      vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        headers: new Headers(),
        blob: () => Promise.resolve(blob),
      })

      await downloadShare('share-1', { filePath: '/folder/report.txt' })

      expect(global.fetch).toHaveBeenCalledWith('/api/v1/public/shares/share-1/download/folder/report.txt', { credentials: 'same-origin' })
      const clickedLink = clickSpy.mock.contexts.at(-1) as HTMLAnchorElement
      expect(clickedLink.download).toBe('report.txt')
    })

    it('uses decoded UTF-8 filenames from content disposition', async () => {
      const blob = new Blob(['share-content'], { type: 'text/plain' })
      vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:utf8-share')
      vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        headers: new Headers({ 'Content-Disposition': "attachment; filename*=UTF-8''report%20final.txt" }),
        blob: () => Promise.resolve(blob),
      })

      await downloadShare('share-1')

      const clickedLink = clickSpy.mock.contexts.at(-1) as HTMLAnchorElement
      expect(clickedLink.download).toBe('report final.txt')
    })

    it('sanitizes filenames from content disposition before triggering download', async () => {
      const blob = new Blob(['share-content'], { type: 'text/plain' })
      vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:safe-share')
      vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        headers: new Headers({ 'Content-Disposition': 'attachment; filename="folder/secret.txt"' }),
        blob: () => Promise.resolve(blob),
      })

      await downloadShare('share-1')

      const clickedLink = clickSpy.mock.contexts.at(-1) as HTMLAnchorElement
      expect(clickedLink.download).toBe('folder_secret.txt')
    })

    it('falls back to the raw UTF-8 filename token when decoding fails', async () => {
      const blob = new Blob(['share-content'], { type: 'text/plain' })
      vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:bad-utf8-share')
      vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        headers: new Headers({ 'Content-Disposition': "attachment; filename*=UTF-8''%E0%A4%A" }),
        blob: () => Promise.resolve(blob),
      })

      await downloadShare('share-1')

      const clickedLink = clickSpy.mock.contexts.at(-1) as HTMLAnchorElement
      expect(clickedLink.download).toBe('%E0%A4%A')
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

    it.each([
      [410, '分享已过期、已禁用或访问次数已达上限'],
      [429, '尝试次数过多，请稍后再试'],
    ])('uses fallback download error text for status %s when the body is unreadable', async (status, message) => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status,
        json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
      })

      await expect(downloadShare('share-1')).rejects.toMatchObject({
        message,
        status,
      })
    })
  })

  it('preserves path separators for nested files', () => {
    const url = getShareFileDownloadUrl('abc123', 'folder/sub/file.txt')
    expect(url).toBe('/api/v1/public/shares/abc123/download/folder/sub/file.txt')
  })

  it('builds shared file download URL without password query', () => {
    const url = getShareFileDownloadUrl('abc123', '/folder/file.txt')
    expect(url).toBe('/api/v1/public/shares/abc123/download/folder/file.txt')
  })

  it('builds shared root download URL without password query', () => {
    expect(getShareDownloadUrl('abc123')).toBe('/api/v1/public/shares/abc123/download')
  })

  describe('getPublicShareItems', () => {
    it('requests items with path only', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ path: 'docs', items: [] }),
      })

      await getPublicShareItems('share-1', { path: 'docs' })

      expect(global.fetch).toHaveBeenCalledWith('/api/v1/public/shares/share-1/items?path=docs', { credentials: 'same-origin' })
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

    it.each([
      [410, '分享已过期、已禁用或访问次数已达上限'],
      [401, '密码错误'],
    ])('uses fallback folder listing error text for status %s when the body is unreadable', async (status, message) => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status,
        json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
      })

      await expect(getPublicShareItems('share-1')).rejects.toMatchObject({
        message,
        status,
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

    it('rejects malformed successful folder item entries', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ path: 'docs', items: [{ name: 'bad-entry' }] }),
      })

      await expect(getPublicShareItems('share-1')).rejects.toMatchObject({
        message: '分享文件夹响应无效',
        status: 200,
      })
    })

    it.each([
      ['non-object item', null],
      ['invalid is_dir', { name: 'file.txt', path: '/file.txt', is_dir: 'false', size: 1, mod_time: '2026-03-13T00:00:00Z' }],
      ['invalid size', { name: 'file.txt', path: '/file.txt', is_dir: false, size: '1', mod_time: '2026-03-13T00:00:00Z' }],
      ['invalid mod_time', { name: 'file.txt', path: '/file.txt', is_dir: false, size: 1, mod_time: 123 }],
    ])('rejects folder item responses with %s', async (_label, item) => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ path: 'docs', items: [item] }),
      })

      await expect(getPublicShareItems('share-1')).rejects.toMatchObject({
        message: '分享文件夹响应无效',
        status: 200,
      })
    })
  })

  describe('getPublicShare', () => {
    it('requests the dedicated public share API route', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ id: 'share-1', type: 'file', has_password: false, permission: 'read' }),
      })

      await getPublicShare('share-1')

      expect(global.fetch).toHaveBeenCalledWith('/api/v1/public/shares/share-1', { credentials: 'same-origin' })
    })

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

    it('preserves machine-readable codes for disabled public shares', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 503,
        json: () => Promise.resolve({ success: false, error: { code: 'SHARE_FEATURE_DISABLED', message: 'share feature disabled' } }),
      })

      await expect(getPublicShare('missing')).rejects.toMatchObject({
        message: 'share feature disabled',
        status: 503,
        code: 'SHARE_FEATURE_DISABLED',
        isFeatureDisabled: true,
      })
    })

    it('uses the expired fallback when public share error bodies are unreadable', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 410,
        json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
      })

      await expect(getPublicShare('expired')).rejects.toMatchObject({
        message: '分享已过期、已禁用或访问次数已达上限',
        status: 410,
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

    it('rejects malformed successful public share payloads', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ id: 'share-1', type: 'file' }),
      })

      await expect(getPublicShare('share-1')).rejects.toMatchObject({
        message: '分享信息无效',
        status: 200,
      })
    })

    it.each([
      ['description', { description: 42 }],
      ['file_name', { file_name: 42 }],
      ['file_size', { file_size: '42' }],
      ['folder_items', { folder_items: '2' }],
    ])('rejects public share payloads with invalid optional %s', async (_label, overrides) => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          id: 'share-1',
          type: 'folder',
          has_password: false,
          permission: 'read',
          ...overrides,
        }),
      })

      await expect(getPublicShare('share-1')).rejects.toMatchObject({
        message: '分享信息无效',
        status: 200,
      })
    })
  })

  describe('authenticated share APIs', () => {
    it('requests all shares when requested', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: [],
        }),
      })

      await listShares(true)

      expect(global.fetch).toHaveBeenCalledWith('/api/v1/shares?all=true', expect.anything())
    })

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

    it('unwraps share detail responses', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: createValidShare({ id: 'share-detail' }),
        }),
      })

      await expect(getShare('share-detail')).resolves.toMatchObject({
        id: 'share-detail',
      })
    })

    it('throws ShareError when loading share details fails', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 404,
        json: () => Promise.resolve({ success: false, error: { message: 'share missing' } }),
      })

      await expect(getShare('missing')).rejects.toMatchObject({
        message: 'share missing',
        status: 404,
      })
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
      expect(result.warning).toBe(false)
      expect(result.message).toBeUndefined()
    })

    it('updates shares with a JSON body', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: createValidShare({ enabled: false }),
        }),
      })

      await expect(updateShare('share-1', { enabled: false })).resolves.toMatchObject({
        enabled: false,
      })

      expect(global.fetch).toHaveBeenCalledWith('/api/v1/shares/share-1', expect.objectContaining({
        method: 'PUT',
        body: JSON.stringify({ enabled: false }),
      }))
    })

    it('returns warning details for successful create share responses with warnings', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 201,
        headers: { get: (name: string) => name === 'Warning' ? '199 MnemoNAS "activity log persistence failed"' : null },
        json: () => Promise.resolve({
          success: true,
          data: { id: 'share-2', path: '/docs/b.txt', type: 'file', created_by: 'u1', created_at: '2026-03-13T00:00:00Z', has_password: false, permission: 'read', enabled: true, access_count: 0, url: '/s/share-2' },
          message: 'share created with audit warning',
        }),
      })

      await expect(createShare({ path: '/docs/b.txt' })).resolves.toMatchObject({
        id: 'share-2',
        warning: true,
        message: 'share created with audit warning',
      })
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

    it('reads top-level share error messages', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 500,
        json: () => Promise.resolve({
          success: false,
          message: 'top-level failure',
        }),
      })

      await expect(listShares()).rejects.toMatchObject({
        message: 'top-level failure',
        status: 500,
      })
    })

    it('uses fallback share error messages when error bodies are unreadable', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 500,
        json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
      })

      await expect(listShares()).rejects.toMatchObject({
        message: '获取分享列表失败',
        status: 500,
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

    it.each([
      ['non-object share', null],
      ['invalid expires_at', createValidShare({ expires_at: 123 as unknown as string })],
      ['invalid max_access', createValidShare({ max_access: '5' as unknown as number })],
      ['invalid description', createValidShare({ description: 42 as unknown as string })],
    ])('rejects share list responses with %s', async (_label, share) => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data: [share] }),
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

    it('rejects unreadable successful share responses', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
      })

      await expect(listShares()).rejects.toMatchObject({
        message: '获取分享列表响应无效',
        status: 200,
      })
    })

    it('rejects malformed successful update share responses', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true, data: { id: 'share-1' } }),
      })

      await expect(updateShare('share-1', { enabled: false })).rejects.toMatchObject({
        message: '更新分享响应无效',
        status: 200,
      })
    })

    it('throws ShareError when update share fails', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 403,
        json: () => Promise.resolve({ success: false, error: { message: 'cannot update share' } }),
      })

      await expect(updateShare('share-1', { enabled: false })).rejects.toMatchObject({
        message: 'cannot update share',
        status: 403,
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

    it('returns warning details for successful delete share responses with warnings', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: { get: (name: string) => name === 'Warning' ? '199 MnemoNAS "audit log persistence incomplete"' : null },
        json: () => Promise.resolve({
          success: true,
          data: null,
          message: 'share deleted with audit warning',
        }),
      })

      await expect(deleteShare('share-1')).resolves.toEqual({
        warning: true,
        message: 'share deleted with audit warning',
      })
    })

    it('rejects malformed successful delete share responses', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true }),
      })

      await expect(deleteShare('share-1')).rejects.toMatchObject({
        message: '删除分享响应无效',
        status: 200,
      })
    })

    it('rejects false-success delete share responses', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: false, data: null }),
      })

      await expect(deleteShare('share-1')).rejects.toMatchObject({
        message: '删除分享响应无效',
        status: 200,
      })
    })

    it('throws ShareError when deleting a share fails', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 404,
        json: () => Promise.resolve({ success: false, error: { message: 'share already deleted' } }),
      })

      await expect(deleteShare('share-1')).rejects.toMatchObject({
        message: 'share already deleted',
        status: 404,
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

    it('copies absolute share URLs without rewriting them', async () => {
      await copyShareUrl(createValidShare({ url: 'https://cdn.example.com/s/share-1' }))

      expect(mockCopyTextToClipboard).toHaveBeenCalledWith('https://cdn.example.com/s/share-1')
    })
  })

  describe('accessShareWithPassword', () => {
    it('uses same-origin credentials so browser stores access cookie', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ id: 'share-1', type: 'file', has_password: true, permission: 'read' }),
      })

      await accessShareWithPassword('share-1', 'secret')

      expect(global.fetch).toHaveBeenCalledWith('/api/v1/public/shares/share-1/access', expect.objectContaining({
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

    it('uses the expired fallback when password access error bodies are unreadable', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: false,
        status: 410,
        json: () => Promise.reject(new SyntaxError('Unexpected token < in JSON')),
      })

      await expect(accessShareWithPassword('share-1', 'secret')).rejects.toMatchObject({
        message: '分享已过期、已禁用或访问次数已达上限',
        status: 410,
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

    it('rejects malformed successful password access payloads', async () => {
      ;(global.fetch as ReturnType<typeof vi.fn>).mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ id: 'share-1', type: 'file' }),
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

    it('classifies public share error states', () => {
      expect(new ShareError('missing', 404).isNotFound).toBe(true)
      expect(new ShareError('disabled', 403, 'SHARE_DISABLED').isDisabled).toBe(true)
      expect(new ShareError('limit', 403, 'SHARE_ACCESS_LIMIT_REACHED').isAccessLimitReached).toBe(true)
      expect(new ShareError('expired', 410, 'SHARE_EXPIRED').isExpired).toBe(true)
      expect(new ShareError('gone', 410).isExpired).toBe(true)
      expect(new ShareError('password', 401).isUnauthorized).toBe(true)
      expect(new ShareError('slow down', 429).isRateLimited).toBe(true)
      expect(new ShareError('unavailable', 503).isUnavailable).toBe(true)
      expect(new ShareError('feature disabled', 503, 'SHARE_FEATURE_DISABLED').isUnavailable).toBe(false)
    })
  })

  describe('format helpers', () => {
    it('formats expiration windows from the current time', () => {
      vi.useFakeTimers()
      try {
        vi.setSystemTime(new Date('2026-05-04T00:00:00Z'))

        expect(formatExpiration()).toBe('永不过期')
        expect(formatExpiration('2026-05-06T02:00:00Z')).toBe('2 天后过期')
        expect(formatExpiration('2026-05-04T03:00:00Z')).toBe('3 小时后过期')
        expect(formatExpiration('2026-05-04T00:30:00Z')).toBe('即将过期')
        expect(formatExpiration('2026-05-03T23:59:00Z')).toBe('已过期')
      } finally {
        vi.useRealTimers()
      }
    })

    it('formats duration shortcuts and preserves unknown values', () => {
      expect(formatDuration('7d')).toBe('7 天')
      expect(formatDuration('12h')).toBe('12 小时')
      expect(formatDuration('30m')).toBe('30 分钟')
      expect(formatDuration('d')).toBe('d')
      expect(formatDuration('h')).toBe('h')
      expect(formatDuration('custom')).toBe('custom')
      expect(formatDuration('forever')).toBe('forever')
    })
  })
})
