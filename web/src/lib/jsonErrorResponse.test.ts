import { describe, expect, it, vi } from 'vitest'
import {
  extractStructuredJsonErrorDetails,
  isJsonContentType,
  readStructuredJsonErrorDetails,
} from './jsonErrorResponse'

describe('jsonErrorResponse', () => {
  describe('isJsonContentType', () => {
    it.each([
      ['application/json', true],
      ['application/json; charset=utf-8', true],
      ['application/problem+json', true],
      ['text/json', false],
      ['application/zip', false],
      [null, false],
    ])('classifies %s as %s', (contentType, expected) => {
      expect(isJsonContentType(contentType)).toBe(expected)
    })
  })

  describe('extractStructuredJsonErrorDetails', () => {
    it('reads wrapped API errors', () => {
      expect(extractStructuredJsonErrorDetails({
        success: false,
        error: { code: 'PAYLOAD_TOO_LARGE', message: 'archive content is too large' },
      }, '下载失败')).toEqual({
        message: 'archive content is too large',
        code: 'PAYLOAD_TOO_LARGE',
      })
    })

    it('reads top-level API errors', () => {
      expect(extractStructuredJsonErrorDetails({
        code: 'ARCHIVE_TOO_LARGE',
        message: 'archive content is too large',
      }, '下载失败')).toEqual({
        message: 'archive content is too large',
        code: 'ARCHIVE_TOO_LARGE',
      })
    })

    it('localizes known codes before falling back to the server message', () => {
      expect(extractStructuredJsonErrorDetails({
        success: false,
        error: { code: 'SHARE_DISABLED', message: 'share disabled' },
      }, '下载失败', {
        localizeCode: (code) => code === 'SHARE_DISABLED' ? '分享已停用' : undefined,
      })).toEqual({
        message: '分享已停用',
        code: 'SHARE_DISABLED',
      })
    })

    it('ignores false-success bodies without error details', () => {
      expect(extractStructuredJsonErrorDetails({ success: false }, '下载失败')).toBeUndefined()
    })

    it('ignores code-only false-success bodies when the code is not localized', () => {
      expect(extractStructuredJsonErrorDetails({ success: false, code: 'DRAFT' }, '下载失败')).toBeUndefined()
    })

    it('uses localized messages for code-only false-success bodies', () => {
      expect(extractStructuredJsonErrorDetails({
        success: false,
        code: 'SHARE_DISABLED',
      }, '下载失败', {
        localizeCode: (code) => code === 'SHARE_DISABLED' ? '分享已停用' : undefined,
      })).toEqual({
        message: '分享已停用',
        code: 'SHARE_DISABLED',
      })
    })

    it('ignores ordinary JSON payloads', () => {
      expect(extractStructuredJsonErrorDetails({ data: { ok: true } }, '下载失败')).toBeUndefined()
    })
  })

  describe('readStructuredJsonErrorDetails', () => {
    it('parses JSON API errors without consuming the original response body', async () => {
      const response = new Response(JSON.stringify({
        code: 'PAYLOAD_TOO_LARGE',
        message: 'archive content is too large',
      }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })

      await expect(readStructuredJsonErrorDetails(response, '下载失败')).resolves.toEqual({
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

      await expect(readStructuredJsonErrorDetails(response, '下载失败')).resolves.toEqual({
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

      await expect(readStructuredJsonErrorDetails(response, '下载失败')).resolves.toBeUndefined()
    })

    it('skips unreadable JSON without throwing', async () => {
      const response = {
        headers: new Headers({ 'Content-Type': 'application/json' }),
        clone: () => ({
          json: vi.fn(() => Promise.reject(new SyntaxError('bad json'))),
        }),
      } as unknown as Response

      await expect(readStructuredJsonErrorDetails(response, '下载失败')).resolves.toBeUndefined()
    })
  })
})
