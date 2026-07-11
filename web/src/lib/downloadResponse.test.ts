import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  MAX_RETAINED_DOWNLOAD_FRAMES,
  readDownloadJsonErrorDetails,
  readRangedDownloadJsonErrorDetails,
  reserveBrowserDownloadNavigation,
  triggerBrowserDownload,
  triggerBrowserDownloadUrl,
} from './downloadResponse'

describe('downloadResponse', () => {
  afterEach(() => {
    window.dispatchEvent(new Event('pagehide'))
    document.querySelectorAll('[data-mnemonas-download-frame]').forEach((frame) => frame.remove())
    vi.useRealTimers()
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

  describe('triggerBrowserDownloadUrl', () => {
    function captureNativeNavigation() {
      const navigationSpy = vi.fn()
      let navigation: { href: string; target: string; rel: string; referrerPolicy: string } | undefined
      vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function click(this: HTMLAnchorElement) {
        navigation = {
          href: this.href,
          target: this.target,
          rel: this.rel,
          referrerPolicy: this.referrerPolicy,
        }
        navigationSpy()
      })
      return { navigationSpy, getNavigation: () => navigation }
    }

    it('retains a hidden same-origin target frame until the page lifecycle ends', () => {
      const createObjectURLSpy = vi.spyOn(URL, 'createObjectURL')
      createObjectURLSpy.mockClear()
      const appendSpy = vi.spyOn(document.body, 'appendChild')
      const removeSpy = vi.spyOn(HTMLIFrameElement.prototype, 'remove')
      const { navigationSpy, getNavigation } = captureNativeNavigation()

      triggerBrowserDownloadUrl('/api/v1/download/report.txt?ticket=opaque-ticket', 'folder/report.txt')

      const frame = appendSpy.mock.calls.map(([node]) => node).find((node) => node instanceof HTMLIFrameElement) as HTMLIFrameElement | undefined
      const link = appendSpy.mock.calls.map(([node]) => node).find((node) => node instanceof HTMLAnchorElement) as HTMLAnchorElement | undefined
      expect(frame?.getAttribute('src')).toBeNull()
      expect(frame?.src).toBe('')
      expect(frame?.title).toBe('下载 folder_report.txt')
      expect(frame?.hidden).toBe(true)
      expect(frame?.getAttribute('sandbox')).toBe('allow-downloads')
      expect(frame?.referrerPolicy).toBe('no-referrer')
      expect(getNavigation()).toEqual({
        href: `${window.location.origin}/api/v1/download/report.txt?ticket=opaque-ticket`,
        target: frame?.name,
        rel: '',
        referrerPolicy: 'no-referrer',
      })
      expect(createObjectURLSpy).not.toHaveBeenCalled()
      expect(appendSpy).toHaveBeenCalledTimes(2)
      expect(navigationSpy).toHaveBeenCalledTimes(1)
      expect(link?.getAttribute('href')).toBeNull()
      expect(document.body.contains(link as HTMLAnchorElement)).toBe(false)
      expect(document.body.contains(frame as HTMLIFrameElement)).toBe(true)

      expect(removeSpy).not.toHaveBeenCalled()
      window.dispatchEvent(new Event('pagehide'))
      expect(removeSpy).toHaveBeenCalledTimes(1)
      expect(document.body.contains(frame as HTMLIFrameElement)).toBe(false)
    })

    it('registers one lifecycle cleanup for multiple native download frames', () => {
      const appendSpy = vi.spyOn(document.body, 'appendChild').mockImplementation((node) => node)
      const addEventListenerSpy = vi.spyOn(window, 'addEventListener')
      const removeSpy = vi.spyOn(HTMLIFrameElement.prototype, 'remove').mockImplementation(() => {})
      captureNativeNavigation()

      triggerBrowserDownloadUrl('/api/v1/download/first.txt', 'first.txt')
      triggerBrowserDownloadUrl('/api/v1/download/second.txt', 'second.txt')

      const frames = appendSpy.mock.calls.map(([node]) => node).filter((node) => node instanceof HTMLIFrameElement)
      expect(frames).toHaveLength(2)
      expect((frames[0] as HTMLIFrameElement).name).toMatch(/^mnemonas-download-[0-9a-f]{32}$/)
      expect((frames[1] as HTMLIFrameElement).name).not.toBe((frames[0] as HTMLIFrameElement).name)
      expect(addEventListenerSpy.mock.calls.filter(([type]) => type === 'pagehide')).toHaveLength(1)
      expect(removeSpy).not.toHaveBeenCalled()

      window.dispatchEvent(new Event('pagehide'))
      expect(removeSpy).toHaveBeenCalledTimes(2)
    })

    it('releases a retained frame when a non-download response finishes loading', () => {
      const appendSpy = vi.spyOn(document.body, 'appendChild').mockImplementation((node) => node)
      const removeSpy = vi.spyOn(HTMLIFrameElement.prototype, 'remove').mockImplementation(() => {})
      captureNativeNavigation()

      triggerBrowserDownloadUrl('/api/v1/download/error.txt', 'error.txt')

      const frame = appendSpy.mock.calls[0]?.[0] as HTMLIFrameElement | undefined
      expect(frame).toBeDefined()
      frame?.dispatchEvent(new Event('load'))
      expect(removeSpy).toHaveBeenCalledTimes(1)

      window.dispatchEvent(new Event('pagehide'))
      expect(removeSpy).toHaveBeenCalledTimes(1)
    })

    it('bounds retained native download frames without cancelling older submissions', () => {
      const appendSpy = vi.spyOn(document.body, 'appendChild').mockImplementation((node) => node)
      captureNativeNavigation()

      for (let index = 0; index < MAX_RETAINED_DOWNLOAD_FRAMES; index += 1) {
        triggerBrowserDownloadUrl(`/api/v1/download/file-${index}.txt`, `file-${index}.txt`)
      }

      const frameCount = () => appendSpy.mock.calls
        .map(([node]) => node)
        .filter((node) => node instanceof HTMLIFrameElement).length
      expect(frameCount()).toBe(MAX_RETAINED_DOWNLOAD_FRAMES)
      expect(() => triggerBrowserDownloadUrl('/api/v1/download/overflow.txt', 'overflow.txt'))
        .toThrow('当前页面已提交过多下载，请刷新页面后重试')
      expect(frameCount()).toBe(MAX_RETAINED_DOWNLOAD_FRAMES)
    })

    it('reserves frame capacity before asynchronous download preparation', () => {
      const reservations = Array.from(
        { length: MAX_RETAINED_DOWNLOAD_FRAMES },
        () => reserveBrowserDownloadNavigation(),
      )
      try {
        expect(() => reserveBrowserDownloadNavigation())
          .toThrow('当前页面已提交过多下载，请刷新页面后重试')
      } finally {
        reservations.forEach((reservation) => reservation.release())
      }
    })

    it('uses a safe frame label when URL download filename sanitization fails', () => {
      const appendSpy = vi.spyOn(document.body, 'appendChild').mockImplementation((node) => node)
      captureNativeNavigation()
      triggerBrowserDownloadUrl('/api/v1/download/report.txt', '')

      const frame = appendSpy.mock.calls.map(([node]) => node).find((node) => node instanceof HTMLIFrameElement) as HTMLIFrameElement | undefined
      expect(frame?.title).toBe('下载 download')
    })

    it.each([
      ['cross-origin URL', 'https://evil.example/report.txt'],
      ['scheme-relative cross-origin URL', '//evil.example/report.txt'],
      ['javascript URL', 'javascript:alert(1)'],
      ['data URL', 'data:text/plain,secret'],
      ['malformed URL', 'http://['],
      ['same-origin URL with credentials', `${window.location.protocol}//user:pass@${window.location.host}/report.txt`],
    ])('rejects a %s', (_label, url) => {
      const appendSpy = vi.spyOn(document.body, 'appendChild').mockImplementation((node) => node)
      expect(() => triggerBrowserDownloadUrl(url, 'report.txt')).toThrow('不安全的下载地址')
      expect(appendSpy).not.toHaveBeenCalled()
    })

    it('does not leave a frame when attaching the native navigation fails', () => {
      const appendSpy = vi.spyOn(document.body, 'appendChild').mockImplementation(() => {
        throw new Error('navigation blocked')
      })

      expect(() => triggerBrowserDownloadUrl('/api/v1/download/report.txt', 'report.txt')).toThrow('navigation blocked')
      expect(appendSpy).toHaveBeenCalledTimes(1)
      expect(document.querySelector('[data-mnemonas-download-frame]')).toBeNull()
    })

    it('releases the target frame when the native navigation click fails', () => {
      vi.spyOn(document.body, 'appendChild').mockImplementation((node) => node)
      vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {
        throw new Error('download blocked')
      })
      const removeSpy = vi.spyOn(HTMLIFrameElement.prototype, 'remove').mockImplementation(() => {})

      expect(() => triggerBrowserDownloadUrl('/api/v1/download/report.txt', 'report.txt')).toThrow('download blocked')
      expect(removeSpy).toHaveBeenCalledTimes(1)

      window.dispatchEvent(new Event('pagehide'))
      expect(removeSpy).toHaveBeenCalledTimes(1)
    })
  })
})
