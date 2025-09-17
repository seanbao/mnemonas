import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  readDownloadJsonErrorDetails,
  readRangedDownloadJsonErrorDetails,
  triggerBrowserDownload,
} from './downloadResponse'

describe('downloadResponse', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  describe('readDownloadJsonErrorDetails', () => {
    it('parses JSON download errors without consuming the original response body', async () => {
      const response = new Response(JSON.stringify({
        code: 'PAYLOAD_TOO_LARGE',
        message: 'archive content is too large',
      }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })

      await expect(readDownloadJsonErrorDetails(response, '下载失败')).resolves.toEqual({
        message: 'archive content is too large',
        code: 'PAYLOAD_TOO_LARGE',
      })
      await expect(response.blob()).resolves.toBeInstanceOf(Blob)
    })

    it('parses problem+json detail messages', async () => {
      const response = new Response(JSON.stringify({
        title: 'Service unavailable',
        detail: 'preview storage unavailable',
        status: 503,
      }), {
        status: 503,
        headers: { 'Content-Type': 'application/problem+json' },
      })

      await expect(readDownloadJsonErrorDetails(response, '下载失败')).resolves.toEqual({
        message: 'preview storage unavailable',
      })
    })

    it('ignores problem-like fields in ordinary JSON payloads', async () => {
      const response = new Response(JSON.stringify({
        title: 'Draft note',
        detail: 'This is a user document.',
      }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })

      await expect(readDownloadJsonErrorDetails(response, '下载失败')).resolves.toBeUndefined()
    })

    it('skips JSON responses with a download content-disposition option', async () => {
      const response = new Response(JSON.stringify({
        code: 'PAYLOAD_TOO_LARGE',
        message: 'archive content is too large',
      }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })

      await expect(readDownloadJsonErrorDetails(response, '下载失败', {
        contentDisposition: 'attachment; filename="data.json"',
      })).resolves.toBeUndefined()
    })

    it('skips JSON responses with an attachment content-disposition response header', async () => {
      const response = new Response(JSON.stringify({
        code: 'PAYLOAD_TOO_LARGE',
        message: 'archive content is too large',
      }), {
        status: 200,
        headers: {
          'Content-Type': 'application/json',
          'Content-Disposition': 'attachment; filename="data.json"',
        },
      })

      await expect(readDownloadJsonErrorDetails(response, '下载失败')).resolves.toBeUndefined()
    })

    it('skips inline JSON responses when content-disposition has a filename', async () => {
      const response = new Response(JSON.stringify({
        code: 'PAYLOAD_TOO_LARGE',
        message: 'archive content is too large',
      }), {
        status: 200,
        headers: {
          'Content-Type': 'application/json',
          'Content-Disposition': 'inline; filename="data.json"',
        },
      })

      await expect(readDownloadJsonErrorDetails(response, '下载失败')).resolves.toBeUndefined()
    })

    it('skips JSON responses when content-disposition filename has whitespace around the equals sign', async () => {
      const response = new Response(JSON.stringify({
        code: 'PAYLOAD_TOO_LARGE',
        message: 'archive content is too large',
      }), {
        status: 200,
        headers: {
          'Content-Type': 'application/json',
          'Content-Disposition': 'inline; filename = "data.json"',
        },
      })

      await expect(readDownloadJsonErrorDetails(response, '下载失败')).resolves.toBeUndefined()
    })

    it('parses inline JSON errors when content-disposition has no filename', async () => {
      const response = new Response(JSON.stringify({
        success: false,
        error: {
          code: 'SERVICE_UNAVAILABLE',
          message: 'preview storage unavailable',
        },
      }), {
        status: 503,
        headers: {
          'Content-Type': 'application/json',
          'Content-Disposition': 'inline',
        },
      })

      await expect(readDownloadJsonErrorDetails(response, '预览失败')).resolves.toEqual({
        message: 'preview storage unavailable',
        code: 'SERVICE_UNAVAILABLE',
      })
    })

    it('skips unreadable JSON without throwing', async () => {
      const response = {
        headers: new Headers({ 'Content-Type': 'application/json' }),
        clone: () => ({
          json: vi.fn(() => Promise.reject(new SyntaxError('bad json'))),
        }),
      } as unknown as Response

      await expect(readDownloadJsonErrorDetails(response, '下载失败')).resolves.toBeUndefined()
    })
  })

  describe('readRangedDownloadJsonErrorDetails', () => {
    function createStructuredRangeResponse(cancel = vi.fn(() => Promise.resolve())) {
      return {
        ok: false,
        status: 503,
        headers: new Headers({ 'Content-Type': 'application/json' }),
        clone: () => ({
          json: vi.fn(() => Promise.resolve({
            success: false,
            error: {
              code: 'SERVICE_UNAVAILABLE',
              message: 'preview storage unavailable',
            },
          })),
        }),
        body: { cancel },
      } as unknown as Response
    }

    it('returns structured JSON error details from a one-byte range probe and cancels the unread body', async () => {
      const cancel = vi.fn(() => Promise.resolve())
      const response = createStructuredRangeResponse(cancel)
      const fetchResponse = vi.fn(() => Promise.resolve(response))

      await expect(readRangedDownloadJsonErrorDetails('/api/v1/download/video.mp4', '无法加载视频', fetchResponse))
        .resolves.toEqual({
          code: 'SERVICE_UNAVAILABLE',
          message: 'preview storage unavailable',
        })

      expect(fetchResponse).toHaveBeenCalledWith('/api/v1/download/video.mp4', {
        headers: {
          Range: 'bytes=0-0',
          'X-Mnemonas-Download-Probe': 'json-error',
        },
      })
      expect(cancel).toHaveBeenCalledTimes(1)
    })

    it('ignores successful structured JSON range probes because they may be user content', async () => {
      const cancel = vi.fn(() => Promise.resolve())
      const response = {
        ok: true,
        status: 206,
        headers: new Headers({ 'Content-Type': 'application/json' }),
        clone: () => ({
          json: vi.fn(() => Promise.resolve({
            success: false,
            error: {
              code: 'USER_DOCUMENT',
              message: 'this is file content',
            },
          })),
        }),
        body: { cancel },
      } as unknown as Response
      const fetchResponse = vi.fn(() => Promise.resolve(response))

      await expect(readRangedDownloadJsonErrorDetails('/api/v1/download/video.mp4', '无法加载视频', fetchResponse))
        .resolves.toBeUndefined()
      expect(cancel).toHaveBeenCalledTimes(1)
    })

    it('returns undefined when the range probe fails', async () => {
      const fetchResponse = vi.fn(() => Promise.reject(new Error('network unavailable')))

      await expect(readRangedDownloadJsonErrorDetails('/api/v1/download/video.mp4', '无法加载视频', fetchResponse))
        .resolves.toBeUndefined()
    })
  })

  describe('triggerBrowserDownload', () => {
    it('creates a temporary object URL and clicks a sanitized download link', () => {
      const blob = new Blob(['content'], { type: 'text/plain' })
      const createObjectURLSpy = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:download')
      const revokeObjectURLSpy = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

      triggerBrowserDownload(blob, 'folder/secret.txt')

      const clickedLink = clickSpy.mock.contexts.at(-1) as HTMLAnchorElement
      expect(clickedLink.download).toBe('folder_secret.txt')
      expect(clickedLink.href).toBe('blob:download')
      expect(createObjectURLSpy).toHaveBeenCalledWith(blob)
      expect(revokeObjectURLSpy).toHaveBeenCalledWith('blob:download')
      expect(document.body.contains(clickedLink)).toBe(false)
    })

    it('uses a safe fallback filename when sanitization fails', () => {
      const blob = new Blob(['content'], { type: 'text/plain' })
      vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:fallback')
      vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

      triggerBrowserDownload(blob, '')

      const clickedLink = clickSpy.mock.contexts.at(-1) as HTMLAnchorElement
      expect(clickedLink.download).toBe('download')
    })

    it('cleans up the temporary link and object URL when clicking fails', () => {
      const blob = new Blob(['content'], { type: 'text/plain' })
      vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:failed-click')
      const revokeObjectURLSpy = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {
        throw new Error('download blocked')
      })

      expect(() => triggerBrowserDownload(blob, 'report.txt')).toThrow('download blocked')
      const clickedLink = clickSpy.mock.contexts.at(-1) as HTMLAnchorElement | undefined
      expect(clickedLink).toBeDefined()
      expect(document.body.contains(clickedLink as HTMLAnchorElement)).toBe(false)
      expect(revokeObjectURLSpy).toHaveBeenCalledWith('blob:failed-click')
    })

    it('does not fail cleanup when the clicked link has already been removed', () => {
      const blob = new Blob(['content'], { type: 'text/plain' })
      vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:removed-link')
      const revokeObjectURLSpy = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function click(this: HTMLAnchorElement) {
        this.remove()
      })

      expect(() => triggerBrowserDownload(blob, 'report.txt')).not.toThrow()
      const clickedLink = clickSpy.mock.contexts.at(-1) as HTMLAnchorElement | undefined
      expect(clickedLink).toBeDefined()
      expect(document.body.contains(clickedLink as HTMLAnchorElement)).toBe(false)
      expect(revokeObjectURLSpy).toHaveBeenCalledWith('blob:removed-link')
    })
  })
})
