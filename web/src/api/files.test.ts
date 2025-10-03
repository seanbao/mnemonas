import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  listFiles,
  getVersions,
  deleteFile,
  getStorageStats,
  getHealth,
  getAppVersion,
  createDirectory,
  moveFile,
  copyFile,
  restoreVersion,
  listTrash,
  restoreFromTrash,
  deleteFromTrash,
  emptyTrash,
  ApiError,
  buildDownloadUrl,
  downloadFile,
  getDownloadUrl,
  getThumbnailUrl,
  versionToDisplayFormat,
  getDiagnostics,
  getDiskHealth,
  getScrubResult,
  runScrub,
  listBackupJobs,
  runBackupJob,
  checkBackupRetentionJob,
  runBackupRestoreDrill,
  previewBackupRestoreJob,
  previewBatchBackupRestore,
  restoreBackupJob,
  runBatchBackupRestore,
  verifyBackupRestoreJob,
  downloadBackupRestoreReport,
  uploadFile,
  downloadDiagnosticsExport,
} from './files'

// Type declaration for global (Node.js environment in Vitest)
declare const global: typeof globalThis & { fetch: typeof fetch }

// Mock fetch globally
const mockFetch = vi.fn()
global.fetch = mockFetch

type MockXHRResult = {
  type: 'load' | 'error' | 'timeout'
  status?: number
  statusText?: string
  responseText?: string
  responseHeaders?: Record<string, string>
  progressEvents?: Array<{
    lengthComputable: boolean
    loaded: number
    total: number
  }>
}

class MockXMLHttpRequest {
  static queuedResults: MockXHRResult[] = []
  static instances: MockXMLHttpRequest[] = []

  static reset(): void {
    MockXMLHttpRequest.queuedResults = []
    MockXMLHttpRequest.instances = []
  }

  status = 0
  statusText = ''
  responseText = ''
  responseHeaders = new Map<string, string>()
  method = ''
  url = ''
  body: Document | XMLHttpRequestBodyInit | null = null
  headers = new Map<string, string>()
  withCredentials = false
  private uploadListeners = new Map<string, Array<(event: ProgressEvent) => void>>()
  private listeners = new Map<string, Array<() => void>>()

  upload = {
    addEventListener: vi.fn((type: string, listener: (event: ProgressEvent) => void) => {
      const existing = this.uploadListeners.get(type) ?? []
      existing.push(listener)
      this.uploadListeners.set(type, existing)
    }),
  }

  constructor() {
    MockXMLHttpRequest.instances.push(this)
  }

  open(method: string, url: string): void {
    this.method = method
    this.url = url
  }

  setRequestHeader(name: string, value: string): void {
    this.headers.set(name, value)
  }

  addEventListener(type: string, listener: () => void): void {
    const existing = this.listeners.get(type) ?? []
    existing.push(listener)
    this.listeners.set(type, existing)
  }

  getResponseHeader(name: string): string | null {
    return this.responseHeaders.get(name.toLowerCase()) ?? null
  }

  send(body: Document | XMLHttpRequestBodyInit | null = null): void {
    this.body = body
    const next = MockXMLHttpRequest.queuedResults.shift()
    if (!next) {
      throw new Error('No queued XHR result available')
    }

    this.status = next.status ?? 0
    this.statusText = next.statusText ?? ''
    this.responseText = next.responseText ?? ''
    this.responseHeaders = new Map(
      Object.entries(next.responseHeaders ?? {}).map(([key, value]) => [key.toLowerCase(), value])
    )
    const progressListeners = this.uploadListeners.get('progress') ?? []
    for (const event of next.progressEvents ?? []) {
      for (const listener of progressListeners) {
        listener(event as ProgressEvent)
      }
    }

    const listeners = this.listeners.get(next.type) ?? []
    for (const listener of listeners) {
      listener()
    }
  }
}

function expectFetchCall(
  index: number,
  url: string,
  options: {
    method?: string
    body?: string
    headers?: Record<string, string>
    signal?: AbortSignal
  } = {}
) {
  const call = mockFetch.mock.calls[index - 1]
  expect(call?.[0]).toBe(url)

  const requestInit = (call?.[1] ?? {}) as RequestInit & { headers?: Headers }

  if (options.method !== undefined) {
    expect(requestInit.method).toBe(options.method)
  }

  if (options.body !== undefined) {
    expect(requestInit.body).toBe(options.body)
  }

  if (options.headers !== undefined) {
    expect(requestInit.headers).toBeInstanceOf(Headers)
    const headers = requestInit.headers as Headers
    const entries = Array.from(headers.entries())
      .map(([key, value]) => [key.toLowerCase(), value] as const)
      .sort(([a], [b]) => a.localeCompare(b))
    const expectedEntries = Object.entries(options.headers)
      .map(([key, value]) => [key.toLowerCase(), value] as const)
      .sort(([a], [b]) => a.localeCompare(b))

    expect(entries).toEqual(expectedEntries)
  }

  if (options.signal !== undefined) {
    expect(requestInit.signal).toBe(options.signal)
  }
}

describe('API: files', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.removeItem('mnemonas_token')
    localStorage.removeItem('mnemonas_refresh_token')
    MockXMLHttpRequest.reset()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  describe('uploadFile', () => {
    const originalXMLHttpRequest = global.XMLHttpRequest

    beforeEach(() => {
      vi.stubGlobal('XMLHttpRequest', MockXMLHttpRequest as unknown as typeof XMLHttpRequest)
    })

    afterEach(() => {
      vi.stubGlobal('XMLHttpRequest', originalXMLHttpRequest)
    })

    it('retries once after refreshing token on 401', async () => {
      localStorage.setItem('mnemonas_token', 'access-1')
      localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

      MockXMLHttpRequest.queuedResults.push(
        { type: 'load', status: 401, statusText: 'Unauthorized' },
        { type: 'load', status: 201, statusText: 'Created' },
      )

      mockFetch
        .mockResolvedValueOnce({
          ok: true,
          status: 200,
          json: () => Promise.resolve({
            success: true,
            data: {
              access_token: 'access-2',
              refresh_token: 'refresh-2',
              expires_at: '2026-03-13T00:00:00Z',
              token_type: 'Bearer',
              user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/' },
            },
          }),
        })
        .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })

      await expect(uploadFile('/docs', new File(['content'], 'report.txt'))).resolves.toEqual({ warning: false, message: undefined })

      expect(mockFetch).toHaveBeenNthCalledWith(1, '/api/v1/auth/refresh', expect.objectContaining({
        method: 'POST',
        credentials: 'same-origin',
      }))
      expect(mockFetch).toHaveBeenNthCalledWith(2, '/api/v1/auth/download-session', expect.objectContaining({
        method: 'POST',
        credentials: 'same-origin',
      }))
      expect(MockXMLHttpRequest.instances).toHaveLength(2)
      expect(MockXMLHttpRequest.instances[0]?.headers.get('Authorization')).toBeUndefined()
      expect(MockXMLHttpRequest.instances[1]?.headers.get('Authorization')).toBeUndefined()
      expect(MockXMLHttpRequest.instances[0]?.withCredentials).toBe(true)
      expect(MockXMLHttpRequest.instances[1]?.withCredentials).toBe(true)
    })

    it('fails with unauthorized error when refresh does not recover the session', async () => {
      localStorage.setItem('mnemonas_token', 'access-1')
      localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

      MockXMLHttpRequest.queuedResults.push({ type: 'load', status: 401, statusText: 'Unauthorized' })
      mockFetch.mockResolvedValueOnce({ ok: false, status: 401, statusText: 'Unauthorized' })

      await expect(uploadFile('/docs', new File(['content'], 'report.txt'))).rejects.toMatchObject({
        message: '上传失败',
        status: 401,
      })

      expect(MockXMLHttpRequest.instances).toHaveLength(1)
      expect(localStorage.getItem('mnemonas_token')).toBeNull()
      expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    })

    it('surfaces a clear error when the backend rejects oversized uploads', async () => {
      MockXMLHttpRequest.queuedResults.push({ type: 'load', status: 413, statusText: 'Request Entity Too Large' })

      await expect(uploadFile('/docs', new File(['content'], 'report.txt'))).rejects.toMatchObject({
        message: '文件超过 10 GB 上传限制',
        status: 413,
      })
    })

    it('preserves structured unavailable upload errors from XHR responses', async () => {
      MockXMLHttpRequest.queuedResults.push({
        type: 'load',
        status: 503,
        statusText: 'Service Unavailable',
        responseText: JSON.stringify({ error: { code: 'SERVICE_UNAVAILABLE', message: 'filesystem not initialized' } }),
      })

      try {
        await uploadFile('/docs', new File(['content'], 'report.txt'))
        throw new Error('Expected uploadFile to throw')
      } catch (error) {
        expect(error).toBeInstanceOf(ApiError)
        expect((error as ApiError).message).toBe('filesystem not initialized')
        expect((error as ApiError).status).toBe(503)
        expect((error as ApiError).code).toBe('SERVICE_UNAVAILABLE')
        expect((error as ApiError).isUnavailable).toBe(true)
      }
    })

    it('preserves top-level unavailable upload error codes from XHR responses', async () => {
      MockXMLHttpRequest.queuedResults.push({
        type: 'load',
        status: 503,
        statusText: 'Service Unavailable',
        responseText: JSON.stringify({
          code: 'SERVICE_UNAVAILABLE',
          message: 'filesystem not initialized',
          timestamp: '2026-04-23T00:00:00Z',
        }),
      })

      try {
        await uploadFile('/docs', new File(['content'], 'report.txt'))
        throw new Error('Expected uploadFile to throw')
      } catch (error) {
        expect(error).toBeInstanceOf(ApiError)
        expect((error as ApiError).message).toBe('filesystem not initialized')
        expect((error as ApiError).status).toBe(503)
        expect((error as ApiError).code).toBe('SERVICE_UNAVAILABLE')
        expect((error as ApiError).isUnavailable).toBe(true)
      }
    })

    it('returns warning details for successful uploads with warning headers', async () => {
      MockXMLHttpRequest.queuedResults.push({
        type: 'load',
        status: 201,
        statusText: 'Created',
        responseText: JSON.stringify({
          success: true,
          data: {
            path: '/docs/report.txt',
          },
          message: 'file uploaded with persistence warning',
          timestamp: '2024-01-01',
        }),
        responseHeaders: {
          Warning: '199 MnemoNAS "workspace mutation persistence incomplete"',
        },
      })

      await expect(uploadFile('/docs', new File(['content'], 'report.txt'))).resolves.toEqual({
        warning: true,
        message: 'file uploaded with persistence warning',
      })
    })

    it('builds a single-slash upload URL for root uploads', async () => {
      MockXMLHttpRequest.queuedResults.push({
        type: 'load',
        status: 201,
        statusText: 'Created',
        responseText: JSON.stringify({
          success: true,
          data: {
            path: '/report.txt',
          },
        }),
      })

      await expect(uploadFile('/', new File(['content'], 'report.txt'))).resolves.toEqual({
        warning: false,
        message: undefined,
      })

      expect(MockXMLHttpRequest.instances[0]?.url).toBe('/api/v1/files/report.txt')
    })

    it('reports computable upload progress and preserves data warnings', async () => {
      const onProgress = vi.fn()
      MockXMLHttpRequest.queuedResults.push({
        type: 'load',
        status: 201,
        statusText: 'Created',
        responseText: JSON.stringify({
          success: true,
          data: {
            path: '/docs/report.txt',
            warning: true,
          },
          message: 'file uploaded with data warning',
          timestamp: '2024-01-01',
        }),
        progressEvents: [
          { lengthComputable: false, loaded: 10, total: 100 },
          { lengthComputable: true, loaded: 25, total: 100 },
          { lengthComputable: true, loaded: 100, total: 100 },
        ],
      })

      await expect(uploadFile('/docs', new File(['content'], 'report.txt'), onProgress)).resolves.toEqual({
        warning: true,
        message: 'file uploaded with data warning',
      })
      expect(onProgress).toHaveBeenNthCalledWith(1, 25)
      expect(onProgress).toHaveBeenNthCalledWith(2, 100)
    })

    it('normalizes unusual upload progress events before reporting them', async () => {
      const onProgress = vi.fn()
      MockXMLHttpRequest.queuedResults.push({
        type: 'load',
        status: 201,
        statusText: 'Created',
        responseText: JSON.stringify({
          success: true,
          data: {
            path: '/docs/report.txt',
          },
        }),
        progressEvents: [
          { lengthComputable: true, loaded: 10, total: 0 },
          { lengthComputable: true, loaded: Number.POSITIVE_INFINITY, total: 100 },
          { lengthComputable: true, loaded: 50, total: Number.NaN },
          { lengthComputable: true, loaded: -10, total: 100 },
          { lengthComputable: true, loaded: 125, total: 100 },
        ],
      })

      await expect(uploadFile('/docs', new File(['content'], 'report.txt'), onProgress)).resolves.toEqual({
        warning: false,
        message: undefined,
      })
      expect(onProgress).toHaveBeenNthCalledWith(1, 0)
      expect(onProgress).toHaveBeenNthCalledWith(2, 100)
      expect(onProgress).toHaveBeenCalledTimes(2)
    })

    it('rejects without starting XHR when the upload signal is already aborted', async () => {
      const controller = new AbortController()
      controller.abort()

      await expect(
        uploadFile('/docs', new File(['content'], 'report.txt'), undefined, { signal: controller.signal })
      ).rejects.toMatchObject({
        name: 'AbortError',
      })

      expect(MockXMLHttpRequest.instances).toHaveLength(0)
    })

    it('rejects when the upload retry fails after a successful refresh', async () => {
      localStorage.setItem('mnemonas_token', 'access-1')
      localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

      MockXMLHttpRequest.queuedResults.push(
        { type: 'load', status: 401, statusText: 'Unauthorized' },
        {
          type: 'load',
          status: 503,
          statusText: 'Service Unavailable',
          responseText: JSON.stringify({ error: { code: 'SERVICE_UNAVAILABLE', message: 'retry upload failed' } }),
        },
      )

      mockFetch
        .mockResolvedValueOnce({
          ok: true,
          status: 200,
          json: () => Promise.resolve({
            success: true,
            data: {
              access_token: 'access-2',
              refresh_token: 'refresh-2',
              expires_at: '2026-03-13T00:00:00Z',
              token_type: 'Bearer',
              user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/' },
            },
          }),
        })
        .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })

      await expect(uploadFile('/docs', new File(['content'], 'report.txt'))).rejects.toMatchObject({
        message: 'retry upload failed',
        status: 503,
        code: 'SERVICE_UNAVAILABLE',
      })
    })

    it('surfaces upload network and timeout failures', async () => {
      MockXMLHttpRequest.queuedResults.push({ type: 'error' })
      await expect(uploadFile('/docs', new File(['content'], 'report.txt'))).rejects.toThrow('网络错误，上传失败')

      MockXMLHttpRequest.queuedResults.push({ type: 'timeout' })
      await expect(uploadFile('/docs', new File(['content'], 'report.txt'))).rejects.toThrow('上传超时')
    })
  })

  describe('ApiError', () => {
    it('creates error with correct properties', () => {
      const error = new ApiError('Test error', 404, 'Not Found')
      expect(error.message).toBe('Test error')
      expect(error.status).toBe(404)
      expect(error.statusText).toBe('Not Found')
      expect(error.name).toBe('ApiError')
    })

    it('identifies not found errors', () => {
      const error = new ApiError('Not found', 404, 'Not Found')
      expect(error.isNotFound).toBe(true)
      expect(error.isUnauthorized).toBe(false)
      expect(error.isForbidden).toBe(false)
      expect(error.isServerError).toBe(false)
    })

    it('identifies unauthorized errors', () => {
      const error = new ApiError('Unauthorized', 401, 'Unauthorized')
      expect(error.isUnauthorized).toBe(true)
    })

    it('identifies forbidden errors', () => {
      const error = new ApiError('Forbidden', 403, 'Forbidden')
      expect(error.isForbidden).toBe(true)
    })

    it('identifies server errors', () => {
      const error = new ApiError('Server error', 500, 'Internal Server Error')
      expect(error.isServerError).toBe(true)
    })
  })

  describe('versionToDisplayFormat', () => {
    it('converts version info to display format', () => {
      const version = {
        version: 1,
        hash: 'abc123',
        size: 1024,
        timestamp: '2024-01-15T10:00:00Z',
      }
      const display = versionToDisplayFormat(version)
      expect(display.modTime).toBe('2024-01-15T10:00:00Z')
      expect(display.size).toBe(1024)
      expect(display.hash).toBe('abc123')
    })
  })

  describe('listFiles', () => {
    it('fetches files for a path', async () => {
      const mockResponse = {
        success: true,
        data: {
          capabilities: { read: true, concreteRead: false, write: false },
          files: [
            {
              name: 'test.txt',
              path: '/test.txt',
              isDir: false,
              size: 100,
              modTime: '2024-01-01',
              capabilities: { read: true, concreteRead: true, write: false },
            },
          ],
          path: '/',
        },
        timestamp: '2024-01-01',
      }

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      })

      const result = await listFiles('/')
      expect(result.files).toHaveLength(1)
      expect(result.path).toBe('/')
      expect(result.capabilities).toEqual({ read: true, concreteRead: false, write: false })
      expect(result.files[0]?.capabilities).toEqual({ read: true, concreteRead: true, write: false })
      expectFetchCall(1, '/api/v1/files/', { headers: {} })
    })

    it('handles path normalization', async () => {
      const mockResponse = {
        success: true,
        data: { files: [], path: '/documents' },
        timestamp: '2024-01-01',
      }

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      })

      await listFiles('documents')
      expectFetchCall(1, '/api/v1/files/documents', { headers: {} })
    })

    it('forwards abort signals to the authenticated fetch request', async () => {
      const controller = new AbortController()
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: { files: [], path: '/' },
        }),
      })

      await listFiles('/', { signal: controller.signal })

      expectFetchCall(1, '/api/v1/files/', {
        headers: {},
        signal: controller.signal,
      })
    })

    it('throws ApiError on failure', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 404,
        statusText: 'Not Found',
        json: () => Promise.resolve({ error: '目录不存在' }),
      })

      await expect(listFiles('/nonexistent')).rejects.toThrow('目录不存在')
    })

    it('preserves service-unavailable directory load error codes', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 503,
        statusText: 'Service Unavailable',
        json: () => Promise.resolve({ error: { code: 'SERVICE_UNAVAILABLE', message: 'filesystem not initialized' } }),
      })

      await expect(listFiles('/')).rejects.toMatchObject({
        message: 'filesystem not initialized',
        status: 503,
        code: 'SERVICE_UNAVAILABLE',
        isUnavailable: true,
      })
    })

    it('rejects wrapped file list responses when success is false', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ success: false, data: { files: [], path: '/' } }),
      })

      await expect(listFiles('/')).rejects.toThrow('服务器返回了无效的数据')
    })

    it('rejects malformed successful file list payloads', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: { files: null, path: '/' },
        }),
      })

      await expect(listFiles('/')).rejects.toThrow('服务器返回了无效的数据')
    })

    it.each([
      ['unsafe', '/docs/./reports'],
      ['relative', 'docs/reports'],
      ['trailing-slash', '/docs/reports/'],
    ])('rejects file list payloads with %s response paths', async (_label, path) => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            path,
            files: [],
          },
        }),
      })

      await expect(listFiles('/docs')).rejects.toThrow('服务器返回了无效的数据')
    })

    it.each([
      ['negative file size', { name: 'test.txt', path: '/test.txt', isDir: false, size: -1, modTime: '2024-01-01' }],
      ['fractional file size', { name: 'test.txt', path: '/test.txt', isDir: false, size: 1.5, modTime: '2024-01-01' }],
      ['unsafe file size', { name: 'test.txt', path: '/test.txt', isDir: false, size: 9007199254740992, modTime: '2024-01-01' }],
      ['unsafe file path', { name: 'test.txt', path: '/docs/./test.txt', isDir: false, size: 100, modTime: '2024-01-01' }],
      ['relative file path', { name: 'test.txt', path: 'docs/test.txt', isDir: false, size: 100, modTime: '2024-01-01' }],
      ['trailing-slash file path', { name: 'test.txt', path: '/docs/test.txt/', isDir: false, size: 100, modTime: '2024-01-01' }],
    ])('rejects file list payloads with %s', async (_label, file) => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: { files: [file], path: '/' },
        }),
      })

      await expect(listFiles('/')).rejects.toThrow('服务器返回了无效的数据')
    })
  })

  describe('getVersions', () => {
    it('fetches versions for a file', async () => {
      const mockResponse = {
        success: true,
        data: {
          path: '/test.txt',
          versions: [
            { version: 1, hash: 'abc123', size: 100, timestamp: '2024-01-01' },
          ],
        },
        timestamp: '2024-01-01',
      }

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      })

      const result = await getVersions('/test.txt')
      expect(result).toHaveLength(1)
      expect(result[0].hash).toBe('abc123')
    })

    it('forwards abort signals when fetching versions for a file', async () => {
      const controller = new AbortController()
      const mockResponse = {
        success: true,
        data: {
          path: '/test.txt',
          versions: [],
        },
        timestamp: '2024-01-01',
      }

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      })

      await getVersions('/test.txt', { signal: controller.signal })

      expectFetchCall(1, '/api/v1/versions/test.txt', {
        signal: controller.signal,
      })
    })

    it('preserves service-unavailable version history error codes', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 503,
        statusText: 'Service Unavailable',
        json: () => Promise.resolve({ error: { code: 'SERVICE_UNAVAILABLE', message: 'version storage unavailable' } }),
      })

      await expect(getVersions('/test.txt')).rejects.toMatchObject({
        message: 'version storage unavailable',
        status: 503,
        code: 'SERVICE_UNAVAILABLE',
        isUnavailable: true,
      })
    })

    it('rejects malformed successful version history payloads', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            path: '/test.txt',
            versions: { invalid: true },
          },
        }),
      })

      await expect(getVersions('/test.txt')).rejects.toThrow('服务器返回了无效的数据')
    })

    it.each([
      ['unsafe', '/docs/./test.txt'],
      ['relative', 'docs/test.txt'],
      ['trailing-slash', '/docs/test.txt/'],
    ])('rejects version history payloads with %s response paths', async (_label, path) => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            path,
            versions: [],
          },
        }),
      })

      await expect(getVersions('/test.txt')).rejects.toThrow('服务器返回了无效的数据')
    })

    it.each([
      ['zero version number', { version: 0, hash: 'abc123', size: 100, timestamp: '2024-01-01' }],
      ['fractional version number', { version: 1.5, hash: 'abc123', size: 100, timestamp: '2024-01-01' }],
      ['unsafe version number', { version: 9007199254740992, hash: 'abc123', size: 100, timestamp: '2024-01-01' }],
      ['negative version size', { version: 1, hash: 'abc123', size: -1, timestamp: '2024-01-01' }],
      ['fractional version size', { version: 1, hash: 'abc123', size: 1.5, timestamp: '2024-01-01' }],
      ['unsafe version size', { version: 1, hash: 'abc123', size: 9007199254740992, timestamp: '2024-01-01' }],
    ])('rejects version history payloads with %s', async (_label, version) => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            path: '/test.txt',
            versions: [version],
          },
        }),
      })

      await expect(getVersions('/test.txt')).rejects.toThrow('服务器返回了无效的数据')
    })
  })

  describe('deleteFile', () => {
    it('deletes a file', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            path: '/test.txt',
          },
          timestamp: '2024-01-01',
        }),
      })

      await expect(deleteFile('/test.txt')).resolves.toEqual({ warning: false, message: undefined })
      expectFetchCall(1, '/api/v1/files/test.txt', {
        method: 'DELETE',
        headers: {},
      })
    })

    it('returns warning details for successful delete responses with warnings', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        headers: { get: (name: string) => name === 'Warning' ? '199 MnemoNAS "workspace mutation persistence incomplete"' : null },
        json: () => Promise.resolve({
          success: true,
          data: {
            path: '/test.txt',
          },
          message: 'file deleted with persistence warning',
          timestamp: '2024-01-01',
        }),
      })

      await expect(deleteFile('/test.txt')).resolves.toEqual({
        warning: true,
        message: 'file deleted with persistence warning',
      })
    })

    it('forwards abort signal when deleting a file', async () => {
      const controller = new AbortController()
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            path: '/test.txt',
          },
          timestamp: '2024-01-01',
        }),
      })

      await expect(deleteFile('/test.txt', { signal: controller.signal })).resolves.toEqual({
        warning: false,
        message: undefined,
      })
      expectFetchCall(1, '/api/v1/files/test.txt', {
        method: 'DELETE',
        headers: {},
        signal: controller.signal,
      })
    })

    it('rejects malformed successful delete responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            deleted: true,
          },
          timestamp: '2024-01-01',
        }),
      })

      await expect(deleteFile('/test.txt')).rejects.toThrow('服务器返回了无效的数据')
    })

    it('throws ApiError on failure', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 403,
        statusText: 'Forbidden',
        json: () => Promise.resolve({ error: { message: '只允许删除主目录内的文件' } }),
      })

      await expect(deleteFile('/protected.txt')).rejects.toThrow('只允许删除主目录内的文件')
    })
  })

  describe('getStorageStats', () => {
    it('fetches storage statistics', async () => {
      const mockResponse = {
        success: true,
        data: {
          total_files: 42,
          total_files_available: true,
          storage_stats_available: true,
          disk_stats_available: true,
          directory_quota_stats_available: true,
          total_size: 1073741824,
          total_chunks: 100,
          unique_size: 536870912,
          dedup_ratio: 1.5,
          disk_total: 2147483648,
          disk_free: 1073741824,
          disk_available: 1048576000,
          disk_used: 1073741824,
          disk_usage_ratio: 0.5,
          disk_filesystem_type: 'zfs',
          disk_mount_point: '/srv/mnemonas',
          disk_mount_source: 'tank/mnemonas',
          disk_mount_options: 'rw,relatime',
          disk_native_data_checksum_support: true,
          directory_quotas: [
            {
              path: '/team',
              quota_bytes: 2147483648,
              used_bytes: 1073741824,
              available_bytes: 1073741824,
              usage_ratio: 0.5,
              exists: true,
              status: 'normal',
            },
          ],
        },
      }

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      })

      const result = await getStorageStats()
      expect(result.fileCount).toBe(42)
      expect(result.fileCountAvailable).toBe(true)
      expect(result.storageStatsAvailable).toBe(true)
      expect(result.diskStatsAvailable).toBe(true)
      expect(result.directoryQuotaStatsAvailable).toBe(true)
      expect(result.totalSize).toBe(1073741824)
      expect(result.totalObjects).toBe(100)
      expect(result.uniqueSize).toBe(536870912)
      expect(result.dedupRatio).toBe(1.5)
      expect(result.diskTotal).toBe(2147483648)
      expect(result.diskFree).toBe(1073741824)
      expect(result.diskAvailable).toBe(1048576000)
      expect(result.diskUsed).toBe(1073741824)
      expect(result.diskUsageRatio).toBe(0.5)
      expect(result.diskFilesystemType).toBe('zfs')
      expect(result.diskMountPoint).toBe('/srv/mnemonas')
      expect(result.diskMountSource).toBe('tank/mnemonas')
      expect(result.diskMountOptions).toBe('rw,relatime')
      expect(result.diskNativeDataChecksumSupport).toBe(true)
      expect(result.directoryQuotas).toEqual([
        {
          path: '/team',
          quotaBytes: 2147483648,
          usedBytes: 1073741824,
          availableBytes: 1073741824,
          usageRatio: 0.5,
          exists: true,
          status: 'normal',
        },
      ])
    })

    it('forwards abort signal when fetching storage statistics', async () => {
      const controller = new AbortController()
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {},
        }),
      })

      await getStorageStats({ signal: controller.signal })

      expectFetchCall(1, '/api/v1/stats', {
        signal: controller.signal,
      })
    })

    it('rejects invalid wrapped response for storage stats', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ total_size: 1 }),
      })

      await expect(getStorageStats()).rejects.toThrow('服务器返回了无效的数据')
    })

    it('preserves unknown storage stats fields instead of coercing zero values', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {},
        }),
      })

      const result = await getStorageStats()
      expect(result.fileCount).toBeUndefined()
      expect(result.fileCountAvailable).toBe(false)
      expect(result.storageStatsAvailable).toBe(false)
      expect(result.diskStatsAvailable).toBe(false)
      expect(result.directoryQuotaStatsAvailable).toBe(false)
      expect(result.totalSize).toBeUndefined()
      expect(result.totalObjects).toBeUndefined()
      expect(result.uniqueSize).toBeUndefined()
      expect(result.dedupRatio).toBeUndefined()
      expect(result.diskTotal).toBeUndefined()
      expect(result.diskFree).toBeUndefined()
      expect(result.diskAvailable).toBeUndefined()
      expect(result.diskUsed).toBeUndefined()
      expect(result.diskUsageRatio).toBeUndefined()
      expect(result.diskFilesystemType).toBeUndefined()
      expect(result.diskMountPoint).toBeUndefined()
      expect(result.diskMountSource).toBeUndefined()
      expect(result.diskMountOptions).toBeUndefined()
      expect(result.diskNativeDataChecksumSupport).toBeUndefined()
      expect(result.directoryQuotas).toBeUndefined()
    })

    it('preserves service-unavailable storage stats error codes', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 503,
        statusText: 'Service Unavailable',
        json: () => Promise.resolve({
          error: { code: 'SERVICE_UNAVAILABLE', message: 'storage stats unavailable' },
        }),
      })

      await expect(getStorageStats()).rejects.toMatchObject({
        message: 'storage stats unavailable',
        status: 503,
        code: 'SERVICE_UNAVAILABLE',
        isUnavailable: true,
      })
    })

    it('preserves top-level service-unavailable storage stats error codes', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 503,
        statusText: 'Service Unavailable',
        json: () => Promise.resolve({
          code: 'SERVICE_UNAVAILABLE',
          message: 'storage stats unavailable',
          timestamp: '2026-04-23T00:00:00Z',
        }),
      })

      await expect(getStorageStats()).rejects.toMatchObject({
        message: 'storage stats unavailable',
        status: 503,
        code: 'SERVICE_UNAVAILABLE',
        isUnavailable: true,
      })
    })

    it('rejects malformed successful storage stats payloads', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            total_size: 'not-a-number',
            total_chunks: null,
            dedup_ratio: 'invalid',
            directory_quotas: [{ path: '/team', status: 'bad' }],
          },
        }),
      })

      await expect(getStorageStats()).rejects.toThrow('服务器返回了无效的数据')
    })

    it.each([
      ['negative total files', { total_files: -1 }],
      ['fractional total chunks', { total_chunks: 1.5 }],
      ['unsafe total size', { total_size: 9007199254740992 }],
      ['negative unique size', { unique_size: -1 }],
      ['non-finite dedup ratio', { dedup_ratio: Number.POSITIVE_INFINITY }],
      ['negative disk total', { disk_total: -1 }],
      ['fractional disk free bytes', { disk_free: 1.5 }],
      ['unsafe disk available bytes', { disk_available: 9007199254740992 }],
      ['negative disk used bytes', { disk_used: -1 }],
      ['out-of-range disk usage ratio', { disk_usage_ratio: 1.5 }],
      ['negative directory quota bytes', { directory_quotas: [{ path: '/team', quota_bytes: -1, used_bytes: 0, available_bytes: 0, usage_ratio: 0, exists: true, status: 'normal' }] }],
      ['fractional directory used bytes', { directory_quotas: [{ path: '/team', quota_bytes: 1024, used_bytes: 1.5, available_bytes: 1024, usage_ratio: 0.5, exists: true, status: 'normal' }] }],
      ['unsafe directory available bytes', { directory_quotas: [{ path: '/team', quota_bytes: 1024, used_bytes: 0, available_bytes: 9007199254740992, usage_ratio: 0.5, exists: true, status: 'normal' }] }],
      ['negative directory usage ratio', { directory_quotas: [{ path: '/team', quota_bytes: 1024, used_bytes: 0, available_bytes: 1024, usage_ratio: -0.1, exists: true, status: 'normal' }] }],
      ['unsafe directory quota path', { directory_quotas: [{ path: '/team/./private', quota_bytes: 1024, used_bytes: 0, available_bytes: 1024, usage_ratio: 0.5, exists: true, status: 'normal' }] }],
      ['relative directory quota path', { directory_quotas: [{ path: 'team', quota_bytes: 1024, used_bytes: 0, available_bytes: 1024, usage_ratio: 0.5, exists: true, status: 'normal' }] }],
      ['trailing-slash directory quota path', { directory_quotas: [{ path: '/team/', quota_bytes: 1024, used_bytes: 0, available_bytes: 1024, usage_ratio: 0.5, exists: true, status: 'normal' }] }],
    ])('rejects storage stats payloads with %s', async (_label, data) => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data,
        }),
      })

      await expect(getStorageStats()).rejects.toThrow('服务器返回了无效的数据')
    })
  })

  describe('getHealth', () => {
    it('accepts the current backend health response shape', async () => {
      const mockResponse = {
        status: 'healthy',
        timestamp: '2024-01-15T10:00:00Z',
        uptime: '1h30m',
        uptime_secs: 5400,
        dataplane: {
          healthy: true,
          version: '0.3.0',
          uptime: 3600,
        },
      }

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      })

      const result = await getHealth()
      expect(result.status).toBe('healthy')
      expect(result.dataplane?.healthy).toBe(true)
      expect(result.timestamp).toBe('2024-01-15T10:00:00Z')
      expect(result.uptimeSecs).toBe(5400)
    })

    it('forwards abort signal when fetching health status', async () => {
      const controller = new AbortController()
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          status: 'healthy',
          uptime: '1h30m',
        }),
      })

      await getHealth({ signal: controller.signal })

      expectFetchCall(1, '/health', {
        signal: controller.signal,
      })
    })

    it('keeps optional legacy fields when present', async () => {
      const mockResponse = {
        status: 'healthy',
        version: '0.1.0',
        uptime: '1h30m',
        storage: {
          dataDir: '/data',
          writable: true,
        },
      }

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      })

      const result = await getHealth()
      expect(result.version).toBe('0.1.0')
      expect(result.storage?.writable).toBe(true)
    })

    it('rejects malformed successful health responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ status: 'healthy' }),
      })

      await expect(getHealth()).rejects.toThrow('服务器返回了无效的数据')
    })

    it('throws ApiError when the health endpoint is unavailable', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 503,
        statusText: 'Service Unavailable',
      })

      await expect(getHealth()).rejects.toMatchObject({
        message: '获取健康状态失败',
        status: 503,
      })
    })

    it('surfaces problem-json health errors', async () => {
      const body = {
        title: 'Service unavailable',
        detail: 'health dependencies unavailable',
        status: 503,
      }

      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 503,
        statusText: 'Service Unavailable',
        headers: new Headers({ 'Content-Type': 'application/problem+json' }),
        clone: () => ({ json: () => Promise.resolve(body) }),
        json: () => Promise.resolve(body),
      })

      await expect(getHealth()).rejects.toMatchObject({
        message: 'health dependencies unavailable',
        status: 503,
      })
    })

    it('rejects unreadable health JSON', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.reject(new Error('Invalid JSON')),
      })

      await expect(getHealth()).rejects.toThrow('服务器返回了无效的数据')
    })

    it.each([
      ['invalid timestamp', { status: 'healthy', uptime: '1h30m', timestamp: 123 }],
      ['invalid uptime seconds', { status: 'healthy', uptime: '1h30m', uptime_secs: '5400' }],
      ['negative uptime seconds', { status: 'healthy', uptime: '1h30m', uptime_secs: -1 }],
      ['unsafe uptime seconds', { status: 'healthy', uptime: '1h30m', uptime_secs: 9007199254740992 }],
      ['invalid storage details', { status: 'healthy', uptime: '1h30m', storage: { dataDir: 42 } }],
      ['invalid dataplane details', { status: 'healthy', uptime: '1h30m', dataplane: { uptime: 'bad' } }],
      ['fractional dataplane uptime', { status: 'healthy', uptime: '1h30m', dataplane: { uptime: 1.5 } }],
      ['negative dataplane uptime', { status: 'healthy', uptime: '1h30m', dataplane: { uptime: -1 } }],
    ])('rejects health responses with %s', async (_label, body) => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(body),
      })

      await expect(getHealth()).rejects.toThrow('服务器返回了无效的数据')
    })
  })

  describe('getAppVersion', () => {
    it('unwraps valid version responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            name: 'MnemoNAS',
            version: '0.1.0',
            go: 'go1.24.0',
            build_time: '2026-04-29T00:00:00Z',
          },
        }),
      })

      const result = await getAppVersion()
      expect(result.version).toBe('0.1.0')
      expect(result.go).toBe('go1.24.0')
      expect(result.buildTime).toBe('2026-04-29T00:00:00Z')
    })

    it('forwards abort signal when fetching app version', async () => {
      const controller = new AbortController()
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            name: 'MnemoNAS',
            version: '0.1.0',
            go: 'go1.24.0',
          },
        }),
      })

      await getAppVersion({ signal: controller.signal })

      expectFetchCall(1, '/api/v1/version', {
        signal: controller.signal,
      })
    })

    it('rejects malformed successful version responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            version: '0.1.0',
          },
        }),
      })

      await expect(getAppVersion()).rejects.toThrow('服务器返回了无效的数据')
    })
  })

  describe('createDirectory', () => {
    it('creates a directory', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            path: '/new-folder',
          },
          timestamp: '2024-01-01',
        }),
      })

      await expect(createDirectory('/new-folder')).resolves.toEqual({ warning: false, message: undefined })
      expectFetchCall(1, '/api/v1/directories/new-folder', {
        method: 'POST',
        headers: {},
      })
    })

    it('forwards abort signal when creating a directory', async () => {
      const controller = new AbortController()
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            path: '/new-folder',
          },
        }),
      })

      await expect(createDirectory('/new-folder', { signal: controller.signal })).resolves.toEqual({
        warning: false,
        message: undefined,
      })

      expectFetchCall(1, '/api/v1/directories/new-folder', {
        method: 'POST',
        headers: {},
        signal: controller.signal,
      })
    })

    it('returns warning details for successful create-directory responses with warnings', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        headers: { get: (name: string) => name === 'Warning' ? '199 MnemoNAS "workspace mutation persistence incomplete"' : null },
        json: () => Promise.resolve({
          success: true,
          data: {
            path: '/new-folder',
          },
          message: 'directory created with persistence warning',
          timestamp: '2024-01-01',
        }),
      })

      await expect(createDirectory('/new-folder')).resolves.toEqual({
        warning: true,
        message: 'directory created with persistence warning',
      })
    })

    it('rejects malformed successful create directory responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            created: true,
          },
          timestamp: '2024-01-01',
        }),
      })

      await expect(createDirectory('/new-folder')).rejects.toThrow('服务器返回了无效的数据')
    })

    it('surfaces structured backend errors', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 409,
        statusText: 'Conflict',
        json: () => Promise.resolve({ error: { message: '目录已存在' } }),
      })

      await expect(createDirectory('/new-folder')).rejects.toThrow('目录已存在')
    })

    it('rejects action wrappers that omit data', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          timestamp: '2024-01-01',
        }),
      })

      await expect(createDirectory('/new-folder')).rejects.toThrow('服务器返回了无效的数据')
    })

    it('falls back when backend error body is not an object', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 500,
        statusText: 'Internal Server Error',
        json: () => Promise.resolve(null),
      })

      await expect(createDirectory('/new-folder')).rejects.toMatchObject({
        message: '创建文件夹失败',
        status: 500,
      })
    })

    it('uses top-level messages when structured errors omit a message', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 409,
        statusText: 'Conflict',
        json: () => Promise.resolve({
          error: { code: 'DIRECTORY_EXISTS' },
          message: '目录已存在',
        }),
      })

      await expect(createDirectory('/new-folder')).rejects.toMatchObject({
        message: '目录已存在',
        status: 409,
        code: 'DIRECTORY_EXISTS',
      })
    })

    it('keeps structured error codes when no message is available', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 503,
        statusText: 'Service Unavailable',
        json: () => Promise.resolve({
          error: { code: 'SERVICE_UNAVAILABLE' },
        }),
      })

      await expect(createDirectory('/new-folder')).rejects.toMatchObject({
        message: '创建文件夹失败',
        status: 503,
        code: 'SERVICE_UNAVAILABLE',
      })
    })

    it('keeps top-level error codes when no message is available', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 503,
        statusText: 'Service Unavailable',
        json: () => Promise.resolve({ code: 'SERVICE_UNAVAILABLE' }),
      })

      await expect(createDirectory('/new-folder')).rejects.toMatchObject({
        message: '创建文件夹失败',
        status: 503,
        code: 'SERVICE_UNAVAILABLE',
      })
    })
  })

  describe('moveFile', () => {
    it('moves/renames a file', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            from: '/old.txt',
            to: '/new.txt',
          },
          timestamp: '2024-01-01',
        }),
      })

      await expect(moveFile('/old.txt', '/new.txt')).resolves.toEqual({ warning: false, message: undefined })
      expectFetchCall(1, '/api/v1/files-move', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ from: '/old.txt', to: '/new.txt' }),
      })
    })

    it('passes abort signals to move requests', async () => {
      const controller = new AbortController()
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            from: '/old.txt',
            to: '/new.txt',
          },
          timestamp: '2024-01-01',
        }),
      })

      await expect(moveFile('/old.txt', '/new.txt', { signal: controller.signal })).resolves.toEqual({
        warning: false,
        message: undefined,
      })
      expectFetchCall(1, '/api/v1/files-move', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ from: '/old.txt', to: '/new.txt' }),
        signal: controller.signal,
      })
    })

    it('returns warning details for successful move responses with warnings', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        headers: { get: (name: string) => name === 'Warning' ? '199 MnemoNAS "workspace mutation persistence incomplete"' : null },
        json: () => Promise.resolve({
          success: true,
          data: {
            from: '/old.txt',
            to: '/new.txt',
            warning: true,
          },
          message: 'resource moved with persistence warning',
          timestamp: '2024-01-01',
        }),
      })

      await expect(moveFile('/old.txt', '/new.txt')).resolves.toEqual({
        warning: true,
        message: 'resource moved with persistence warning',
      })
    })

    it('rejects malformed successful move responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            from: '/old.txt',
          },
          timestamp: '2024-01-01',
        }),
      })

      await expect(moveFile('/old.txt', '/new.txt')).rejects.toThrow('服务器返回了无效的数据')
    })

    it('surfaces structured backend errors', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 404,
        statusText: 'Not Found',
        json: () => Promise.resolve({ error: { message: '目标目录不存在' } }),
      })

      await expect(moveFile('/old.txt', '/missing/new.txt')).rejects.toThrow('目标目录不存在')
    })
  })

  describe('copyFile', () => {
    it('copies a file', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            from: '/old.txt',
            to: '/copy.txt',
          },
          timestamp: '2024-01-01',
        }),
      })

      await expect(copyFile('/old.txt', '/copy.txt')).resolves.toEqual({ warning: false, message: undefined })
      expectFetchCall(1, '/api/v1/files-copy', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ from: '/old.txt', to: '/copy.txt' }),
      })
    })

    it('passes abort signals to copy requests', async () => {
      const controller = new AbortController()
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            from: '/old.txt',
            to: '/copy.txt',
          },
          timestamp: '2024-01-01',
        }),
      })

      await expect(copyFile('/old.txt', '/copy.txt', { signal: controller.signal })).resolves.toEqual({
        warning: false,
        message: undefined,
      })
      expectFetchCall(1, '/api/v1/files-copy', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ from: '/old.txt', to: '/copy.txt' }),
        signal: controller.signal,
      })
    })

    it('returns warning details for successful copy responses with warnings', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        headers: { get: (name: string) => name === 'Warning' ? '199 MnemoNAS "workspace mutation persistence incomplete"' : null },
        json: () => Promise.resolve({
          success: true,
          data: {
            from: '/old.txt',
            to: '/copy.txt',
          },
          message: 'resource copied with persistence warning',
          timestamp: '2024-01-01',
        }),
      })

      await expect(copyFile('/old.txt', '/copy.txt')).resolves.toEqual({
        warning: true,
        message: 'resource copied with persistence warning',
      })
    })

    it('rejects malformed successful copy responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            to: '/copy.txt',
          },
          timestamp: '2024-01-01',
        }),
      })

      await expect(copyFile('/old.txt', '/copy.txt')).rejects.toThrow('服务器返回了无效的数据')
    })

    it('surfaces structured backend errors', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 409,
        statusText: 'Conflict',
        json: () => Promise.resolve({ error: { message: '目标路径已存在' } }),
      })

      await expect(copyFile('/old.txt', '/existing.txt')).rejects.toThrow('目标路径已存在')
    })
  })

  describe('restoreVersion', () => {
    it('restores a file to specific version', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            path: '/test.txt',
            restored: 'abc123',
          },
          timestamp: '2024-01-01',
        }),
      })

      await expect(restoreVersion('/test.txt', 'abc123')).resolves.toEqual({ warning: false, message: undefined })
      expectFetchCall(1, '/api/v1/versions/abc123/restore?path=%2Ftest.txt', {
        method: 'POST',
        headers: {},
      })
    })

    it('passes abort signals to restore version requests', async () => {
      const controller = new AbortController()
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            path: '/test.txt',
            restored: 'abc123',
          },
          timestamp: '2024-01-01',
        }),
      })

      await expect(restoreVersion('/test.txt', 'abc123', { signal: controller.signal })).resolves.toEqual({
        warning: false,
        message: undefined,
      })
      expectFetchCall(1, '/api/v1/versions/abc123/restore?path=%2Ftest.txt', {
        method: 'POST',
        headers: {},
        signal: controller.signal,
      })
    })

    it('encodes version hashes as path segments when restoring versions', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            path: '/test.txt',
            restored: 'hash?with space%',
          },
          timestamp: '2024-01-01',
        }),
      })

      await expect(restoreVersion('/test.txt', 'hash?with space%')).resolves.toEqual({ warning: false, message: undefined })
      expectFetchCall(1, '/api/v1/versions/hash%3Fwith%20space%25/restore?path=%2Ftest.txt', {
        method: 'POST',
        headers: {},
      })
    })

    it('returns warning details for successful restore-version responses with warnings', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        headers: { get: (name: string) => name === 'Warning' ? '199 MnemoNAS "workspace mutation persistence incomplete"' : null },
        json: () => Promise.resolve({
          success: true,
          data: {
            path: '/test.txt',
            restored: 'abc123',
          },
          message: 'version restored with persistence warning',
          timestamp: '2024-01-01',
        }),
      })

      await expect(restoreVersion('/test.txt', 'abc123')).resolves.toEqual({
        warning: true,
        message: 'version restored with persistence warning',
      })
    })

    it('rejects malformed successful restore version responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            path: '/test.txt',
            restored: true,
          },
          timestamp: '2024-01-01',
        }),
      })

      await expect(restoreVersion('/test.txt', 'abc123')).rejects.toThrow('服务器返回了无效的数据')
    })

    it('surfaces structured backend errors', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 403,
        statusText: 'Forbidden',
        json: () => Promise.resolve({ error: { message: '仅管理员可恢复历史版本' } }),
      })

      await expect(restoreVersion('/test.txt', 'abc123')).rejects.toThrow('仅管理员可恢复历史版本')
    })
  })

  describe('Trash APIs', () => {
    describe('listTrash', () => {
      it('lists trash items', async () => {
        const mockResponse = {
          success: true,
          data: {
            items: [
              {
                id: 'item1',
                originalPath: '/deleted.txt',
                deletedAt: '2024-01-01T00:00:00Z',
                name: 'deleted.txt',
                isDir: false,
                size: 100,
              },
            ],
            count: 1,
            totalSize: 100,
          },
          timestamp: '2024-01-01',
        }

        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve(mockResponse),
        })

        const result = await listTrash()
        expect(result.items).toHaveLength(1)
        expect(result.items[0].originalPath).toBe('/deleted.txt')
        expect(result.count).toBe(1)
      })

      it('forwards abort signal when listing trash items', async () => {
        const controller = new AbortController()
        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({
            success: true,
            data: {
              items: [],
              count: 0,
              totalSize: 0,
            },
          }),
        })

        await listTrash({ signal: controller.signal })

        expectFetchCall(1, '/api/v1/trash/', {
          signal: controller.signal,
        })
      })

      it('derives trash count and total size from returned items when summary fields are missing', async () => {
        const mockResponse = {
          success: true,
          data: {
            items: [
              {
                id: 'item1',
                originalPath: '/deleted.txt',
                deletedAt: '2024-01-01T00:00:00Z',
                name: 'deleted.txt',
                isDir: false,
                size: 100,
              },
              {
                id: 'item2',
                originalPath: '/deleted-2.txt',
                deletedAt: '2024-01-02T00:00:00Z',
                name: 'deleted-2.txt',
                isDir: false,
                size: 24,
              },
            ],
          },
          timestamp: '2024-01-01',
        }

        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve(mockResponse),
        })

        const result = await listTrash()
        expect(result.items).toHaveLength(2)
        expect(result.count).toBe(2)
        expect(result.totalSize).toBe(124)
      })

      it('rejects malformed successful trash list payloads', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({
            success: true,
            data: {
              items: [{
                id: 'item1',
                originalPath: '/deleted.txt',
                deletedAt: '2024-01-01T00:00:00Z',
                name: 'deleted.txt',
                isDir: false,
                size: '100',
              }],
              count: 1,
              totalSize: 100,
            },
            timestamp: '2024-01-01',
          }),
        })

        await expect(listTrash()).rejects.toThrow('服务器返回了无效的数据')
      })

      it.each([
        ['negative item size', { items: [{ id: 'item1', originalPath: '/deleted.txt', deletedAt: '2024-01-01T00:00:00Z', name: 'deleted.txt', isDir: false, size: -1 }] }],
        ['fractional item size', { items: [{ id: 'item1', originalPath: '/deleted.txt', deletedAt: '2024-01-01T00:00:00Z', name: 'deleted.txt', isDir: false, size: 1.5 }] }],
        ['unsafe item size', { items: [{ id: 'item1', originalPath: '/deleted.txt', deletedAt: '2024-01-01T00:00:00Z', name: 'deleted.txt', isDir: false, size: 9007199254740992 }] }],
        ['negative count', { items: [], count: -1 }],
        ['fractional count', { items: [], count: 1.5 }],
        ['count smaller than returned items', { items: [{ id: 'item1', originalPath: '/deleted.txt', deletedAt: '2024-01-01T00:00:00Z', name: 'deleted.txt', isDir: false, size: 1 }], count: 0 }],
        ['unsafe total size', { items: [], totalSize: 9007199254740992 }],
        ['negative retention days', { items: [], retentionDays: -1 }],
        ['fractional retention max size', { items: [], retentionMaxSize: 1.5 }],
      ])('rejects trash list payloads with %s', async (_label, data) => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({
            success: true,
            data,
            timestamp: '2024-01-01',
          }),
        })

        await expect(listTrash()).rejects.toThrow('服务器返回了无效的数据')
      })
    })

    describe('restoreFromTrash', () => {
      it('restores item from trash', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({
            success: true,
            data: {
              id: 'item1',
              restored: true,
            },
            timestamp: '2024-01-01',
          }),
        })

    await expect(restoreFromTrash('item1')).resolves.toEqual({ warning: false, message: undefined })
        expectFetchCall(1, '/api/v1/trash/item1/restore', {
          method: 'POST',
          headers: {},
        })
      })

    it('preserves warning-bearing successful restore responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        headers: new Headers({ Warning: '199 MnemoNAS "trash restore metadata incomplete"' }),
        json: () => Promise.resolve({
          success: true,
          data: {
            id: 'item1',
            restored: true,
            warning: true,
          },
          message: 'file restored with metadata warning',
          timestamp: '2024-01-01',
        }),
      })

      await expect(restoreFromTrash('item1')).resolves.toEqual({
        warning: true,
        message: 'file restored with metadata warning',
      })
    })

      it('restores to custom path', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({
            success: true,
            data: {
              id: 'item1',
              restored: true,
            },
            timestamp: '2024-01-01',
          }),
        })

        await restoreFromTrash('item1', '/new-location/file.txt')
        expectFetchCall(1, '/api/v1/trash/item1/restore?path=%2Fnew-location%2Ffile.txt', {
          method: 'POST',
          headers: {},
        })
      })

      it('encodes trash item IDs as path segments for restore and delete actions', async () => {
        const trashId = 'trash?with space%'
        const encodedTrashId = 'trash%3Fwith%20space%25'

        mockFetch
          .mockResolvedValueOnce({
            ok: true,
            json: () => Promise.resolve({
              success: true,
              data: {
                id: trashId,
                restored: true,
              },
              timestamp: '2024-01-01',
            }),
          })
          .mockResolvedValueOnce({
            ok: true,
            json: () => Promise.resolve({
              success: true,
              data: {
                id: trashId,
                deleted: true,
              },
              timestamp: '2024-01-01',
            }),
          })

        await restoreFromTrash(trashId, '/new-location/file.txt')
        await deleteFromTrash(trashId)

        expectFetchCall(1, `/api/v1/trash/${encodedTrashId}/restore?path=%2Fnew-location%2Ffile.txt`, {
          method: 'POST',
          headers: {},
        })
        expectFetchCall(2, `/api/v1/trash/${encodedTrashId}`, {
          method: 'DELETE',
          headers: {},
        })
      })

      it('rejects malformed successful restore responses', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({
            success: true,
            data: {
              id: 'item1',
            },
            timestamp: '2024-01-01',
          }),
        })

        await expect(restoreFromTrash('item1')).rejects.toThrow('服务器返回了无效的数据')
      })

      it('surfaces structured backend errors', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: false,
          status: 403,
          statusText: 'Forbidden',
          json: () => Promise.resolve({ error: { message: '仅可恢复主目录内的文件' } }),
        })

        await expect(restoreFromTrash('item1')).rejects.toThrow('仅可恢复主目录内的文件')
      })

      it('preserves unavailable error codes for restore actions', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: false,
          status: 503,
          statusText: 'Service Unavailable',
          json: () => Promise.resolve({ error: { code: 'SERVICE_UNAVAILABLE', message: 'filesystem not initialized' } }),
        })

        try {
          await restoreFromTrash('item1')
          throw new Error('Expected restoreFromTrash to throw')
        } catch (error) {
          expect(error).toBeInstanceOf(ApiError)
          expect((error as ApiError).message).toBe('filesystem not initialized')
          expect((error as ApiError).status).toBe(503)
          expect((error as ApiError).code).toBe('SERVICE_UNAVAILABLE')
          expect((error as ApiError).isUnavailable).toBe(true)
        }
      })

      it('forwards abort signals for trash restore, permanent delete, and empty operations', async () => {
        const signal = new AbortController().signal
        mockFetch
          .mockResolvedValueOnce({
            ok: true,
            json: () => Promise.resolve({
              success: true,
              data: { id: 'item1', restored: true },
              timestamp: '2024-01-01',
            }),
          })
          .mockResolvedValueOnce({
            ok: true,
            json: () => Promise.resolve({
              success: true,
              data: { id: 'item1', deleted: true },
              timestamp: '2024-01-01',
            }),
          })
          .mockResolvedValueOnce({
            ok: true,
            json: () => Promise.resolve({
              success: true,
              data: { deleted_count: 1, partial: false },
              timestamp: '2024-01-01',
            }),
          })

        await restoreFromTrash('item1', undefined, { signal })
        await deleteFromTrash('item1', { signal })
        await emptyTrash({ signal })

        expectFetchCall(1, '/api/v1/trash/item1/restore', {
          method: 'POST',
          signal,
        })
        expectFetchCall(2, '/api/v1/trash/item1', {
          method: 'DELETE',
          signal,
        })
        expectFetchCall(3, '/api/v1/trash/', {
          method: 'DELETE',
          signal,
        })
      })
    })

    describe('deleteFromTrash', () => {
      it('permanently deletes item', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({
            success: true,
            data: {
              id: 'item1',
              deleted: true,
            },
            timestamp: '2024-01-01',
          }),
        })

    await expect(deleteFromTrash('item1')).resolves.toEqual({ warning: false, message: undefined })
        expectFetchCall(1, '/api/v1/trash/item1', {
          method: 'DELETE',
          headers: {},
        })
      })

    it('preserves warning-bearing successful permanent delete responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            id: 'item1',
            deleted: true,
            warning: true,
          },
          message: 'item permanently deleted with cleanup warning',
          timestamp: '2024-01-01',
        }),
      })

      await expect(deleteFromTrash('item1')).resolves.toEqual({
        warning: true,
        message: 'item permanently deleted with cleanup warning',
      })
    })

      it('rejects malformed successful delete responses', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({
            success: true,
            data: {
              id: 'item1',
            },
            timestamp: '2024-01-01',
          }),
        })

        await expect(deleteFromTrash('item1')).rejects.toThrow('服务器返回了无效的数据')
      })

      it('surfaces structured backend errors', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: false,
          status: 409,
          statusText: 'Conflict',
          json: () => Promise.resolve({ error: { message: '回收站条目状态已变更，请刷新后重试' } }),
        })

        await expect(deleteFromTrash('item1')).rejects.toThrow('回收站条目状态已变更，请刷新后重试')
      })
    })

    describe('emptyTrash', () => {
      it('empties trash and returns count', async () => {
        const mockResponse = {
          success: true,
          data: { deleted_count: 5, partial: false },
          timestamp: '2024-01-01',
        }

        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve(mockResponse),
        })

        const result = await emptyTrash()
        expect(result).toEqual({ deletedCount: 5, partial: false, warning: false, message: undefined })
      })

      it('returns partial result when backend reports partial empty', async () => {
        const mockResponse = {
          success: true,
          data: { deleted_count: 2, partial: true },
          timestamp: '2024-01-01',
        }

        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve(mockResponse),
        })

        const result = await emptyTrash()
        expect(result).toEqual({ deletedCount: 2, partial: true, warning: false, message: undefined })
      })

      it('returns warning details for successful empty trash responses with cleanup warnings', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
          headers: { get: (name: string) => name === 'Warning' ? '199 MnemoNAS "trash delete cleanup incomplete"' : null },
          json: () => Promise.resolve({
            success: true,
            data: { deleted_count: 3, partial: false, warning: true },
            message: 'trash emptied with cleanup warning',
            timestamp: '2024-01-01',
          }),
        })

        const result = await emptyTrash()
        expect(result).toEqual({
          deletedCount: 3,
          partial: false,
          warning: true,
          message: 'trash emptied with cleanup warning',
        })
      })

      it('rejects malformed empty trash wrappers', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({
            success: false,
            timestamp: '2024-01-01',
          }),
        })

        await expect(emptyTrash()).rejects.toThrow('服务器返回了无效的数据')
      })

      it('rejects malformed successful empty trash responses', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({
            success: true,
            data: { deleted_count: '2', partial: true },
            timestamp: '2024-01-01',
          }),
        })

        await expect(emptyTrash()).rejects.toThrow('服务器返回了无效的数据')
      })

      it.each([
        ['negative deleted count', -1],
        ['fractional deleted count', 1.5],
        ['unsafe deleted count', 9007199254740992],
      ])('rejects empty trash responses with %s', async (_label, deletedCount) => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({
            success: true,
            data: { deleted_count: deletedCount, partial: false },
            timestamp: '2024-01-01',
          }),
        })

        await expect(emptyTrash()).rejects.toThrow('服务器返回了无效的数据')
      })
    })
  })

  describe('URL helpers', () => {
    describe('buildDownloadUrl', () => {
      it('returns an empty URL when path is missing', () => {
        expect(buildDownloadUrl()).toBe('')
      })

      it('adds optional version and download parameters', () => {
        expect(buildDownloadUrl('/docs/file.pdf', { version: 'abc123', download: true }))
          .toBe('/api/v1/download/docs/file.pdf?version=abc123&download=true')
      })

      it('adds optional archive parameter', () => {
        expect(buildDownloadUrl('/docs', { download: true, archive: 'zip' }))
          .toBe('/api/v1/download/docs?download=true&archive=zip')
      })
    })

    describe('getDownloadUrl', () => {
      it('generates correct download URL', () => {
        expect(getDownloadUrl('/docs/file.pdf')).toBe('/api/v1/download/docs/file.pdf')
      })

      it('handles special characters', () => {
        expect(getDownloadUrl('/文档/测试.txt')).toBe('/api/v1/download/%E6%96%87%E6%A1%A3/%E6%B5%8B%E8%AF%95.txt')
      })

      it('preserves literal percent sequences by escaping them once for URLs', () => {
        expect(getDownloadUrl('/docs/report%20%E4%B8%89.txt')).toBe('/api/v1/download/docs/report%2520%25E4%25B8%2589.txt')
      })

      it('does not add auth query when token exists', () => {
        localStorage.setItem('mnemonas_token', 'test-token')
        expect(getDownloadUrl('/docs/file.pdf')).toBe('/api/v1/download/docs/file.pdf')
      })
    })

    describe('downloadFile', () => {
      it('downloads via authenticated fetch without auth query', async () => {
        const blob = new Blob(['file-content'], { type: 'text/plain' })
        const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
        const createObjectURLSpy = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test')
        const revokeObjectURLSpy = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})

        localStorage.setItem('mnemonas_token', 'test-token')
        mockFetch.mockResolvedValueOnce({
          ok: true,
          status: 200,
          statusText: 'OK',
          headers: new Headers({ 'Content-Disposition': 'attachment; filename="report.txt"' }),
          blob: () => Promise.resolve(blob),
        })

        await downloadFile('/docs/report.txt')

        expectFetchCall(1, '/api/v1/download/docs/report.txt?download=true', {
          headers: {},
        })
        expect(clickSpy).toHaveBeenCalled()
        expect(createObjectURLSpy).toHaveBeenCalledWith(blob)
        expect(revokeObjectURLSpy).toHaveBeenCalledWith('blob:test')
      })

      it('forwards abort signal when downloading a file', async () => {
        const controller = new AbortController()
        const blob = new Blob(['file-content'], { type: 'text/plain' })
        vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
        vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test')
        vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})

        mockFetch.mockResolvedValueOnce({
          ok: true,
          status: 200,
          statusText: 'OK',
          headers: new Headers(),
          blob: () => Promise.resolve(blob),
        })

        await downloadFile('/docs/report.txt', { signal: controller.signal })

        expectFetchCall(1, '/api/v1/download/docs/report.txt?download=true', {
          signal: controller.signal,
        })
      })

      it('uses fallback filename when header is missing', async () => {
        const blob = new Blob(['file-content'])
        let createdLink: HTMLAnchorElement | undefined
        const originalCreateElement = document.createElement.bind(document)
        const createElementSpy = vi.spyOn(document, 'createElement').mockImplementation(((tagName: string) => {
          const element = originalCreateElement(tagName)
          if (tagName === 'a') {
            createdLink = element as HTMLAnchorElement
          }
          return element
        }) as typeof document.createElement)
        vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test')
        vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
        vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

        mockFetch.mockResolvedValueOnce({
          ok: true,
          status: 200,
          statusText: 'OK',
          headers: new Headers(),
          blob: () => Promise.resolve(blob),
        })

        await downloadFile('/docs/report.txt', { filename: 'custom.txt' })

        expect(createdLink?.download).toBe('custom.txt')
        createElementSpy.mockRestore()
      })

      it('sanitizes filenames from content disposition before triggering download', async () => {
        const blob = new Blob(['file-content'])
        let createdLink: HTMLAnchorElement | undefined
        const originalCreateElement = document.createElement.bind(document)
        const createElementSpy = vi.spyOn(document, 'createElement').mockImplementation(((tagName: string) => {
          const element = originalCreateElement(tagName)
          if (tagName === 'a') {
            createdLink = element as HTMLAnchorElement
          }
          return element
        }) as typeof document.createElement)
        vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test')
        vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
        vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

        mockFetch.mockResolvedValueOnce({
          ok: true,
          status: 200,
          statusText: 'OK',
          headers: new Headers({ 'Content-Disposition': 'attachment; filename="folder/secret.txt"' }),
          blob: () => Promise.resolve(blob),
        })

        await downloadFile('/docs/report.txt')

        expect(createdLink?.download).toBe('folder_secret.txt')
        createElementSpy.mockRestore()
      })

      it('passes version parameters and keeps undecodable UTF-8 filenames', async () => {
        const blob = new Blob(['file-content'])
        let createdLink: HTMLAnchorElement | undefined
        const originalCreateElement = document.createElement.bind(document)
        const createElementSpy = vi.spyOn(document, 'createElement').mockImplementation(((tagName: string) => {
          const element = originalCreateElement(tagName)
          if (tagName === 'a') {
            createdLink = element as HTMLAnchorElement
          }
          return element
        }) as typeof document.createElement)
        vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test')
        vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
        vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

        mockFetch.mockResolvedValueOnce({
          ok: true,
          status: 200,
          statusText: 'OK',
          headers: new Headers({ 'Content-Disposition': "attachment; filename*=UTF-8''%E0%A4%A" }),
          blob: () => Promise.resolve(blob),
        })

        await downloadFile('/docs/report.txt', { version: 'abc123' })

        expectFetchCall(1, '/api/v1/download/docs/report.txt?version=abc123&download=true')
        expect(createdLink?.download).toBe('%E0%A4%A')
        createElementSpy.mockRestore()
      })

      it('passes archive parameter and defaults folder filename to zip', async () => {
        const blob = new Blob(['zip-content'], { type: 'application/zip' })
        let createdLink: HTMLAnchorElement | undefined
        const originalCreateElement = document.createElement.bind(document)
        const createElementSpy = vi.spyOn(document, 'createElement').mockImplementation(((tagName: string) => {
          const element = originalCreateElement(tagName)
          if (tagName === 'a') {
            createdLink = element as HTMLAnchorElement
          }
          return element
        }) as typeof document.createElement)
        vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test')
        vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
        vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

        mockFetch.mockResolvedValueOnce({
          ok: true,
          status: 200,
          statusText: 'OK',
          headers: new Headers(),
          blob: () => Promise.resolve(blob),
        })

        await downloadFile('/docs', { archive: 'zip' })

        expectFetchCall(1, '/api/v1/download/docs?download=true&archive=zip')
        expect(createdLink?.download).toBe('docs.zip')
        createElementSpy.mockRestore()
      })

      it('does not duplicate the zip extension when archiving a zip-named folder', async () => {
        const blob = new Blob(['zip-content'], { type: 'application/zip' })
        let createdLink: HTMLAnchorElement | undefined
        const originalCreateElement = document.createElement.bind(document)
        const createElementSpy = vi.spyOn(document, 'createElement').mockImplementation(((tagName: string) => {
          const element = originalCreateElement(tagName)
          if (tagName === 'a') {
            createdLink = element as HTMLAnchorElement
          }
          return element
        }) as typeof document.createElement)
        vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test')
        vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
        vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

        mockFetch.mockResolvedValueOnce({
          ok: true,
          status: 200,
          statusText: 'OK',
          headers: new Headers(),
          blob: () => Promise.resolve(blob),
        })

        await downloadFile('/backups.zip', { archive: 'zip' })

        expectFetchCall(1, '/api/v1/download/backups.zip?download=true&archive=zip')
        expect(createdLink?.download).toBe('backups.zip')
        createElementSpy.mockRestore()
      })

      it('adds a zip extension to custom archive filenames', async () => {
        const blob = new Blob(['zip-content'], { type: 'application/zip' })
        let createdLink: HTMLAnchorElement | undefined
        const originalCreateElement = document.createElement.bind(document)
        const createElementSpy = vi.spyOn(document, 'createElement').mockImplementation(((tagName: string) => {
          const element = originalCreateElement(tagName)
          if (tagName === 'a') {
            createdLink = element as HTMLAnchorElement
          }
          return element
        }) as typeof document.createElement)
        vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test')
        vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
        vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

        mockFetch.mockResolvedValueOnce({
          ok: true,
          status: 200,
          statusText: 'OK',
          headers: new Headers(),
          blob: () => Promise.resolve(blob),
        })

        await downloadFile('/docs', { archive: 'zip', filename: 'family-photos' })

        expectFetchCall(1, '/api/v1/download/docs?download=true&archive=zip')
        expect(createdLink?.download).toBe('family-photos.zip')
        createElementSpy.mockRestore()
      })

      it('uses the archive fallback when the custom archive filename is blank', async () => {
        const blob = new Blob(['zip-content'], { type: 'application/zip' })
        let createdLink: HTMLAnchorElement | undefined
        const originalCreateElement = document.createElement.bind(document)
        const createElementSpy = vi.spyOn(document, 'createElement').mockImplementation(((tagName: string) => {
          const element = originalCreateElement(tagName)
          if (tagName === 'a') {
            createdLink = element as HTMLAnchorElement
          }
          return element
        }) as typeof document.createElement)
        vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test')
        vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
        vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

        mockFetch.mockResolvedValueOnce({
          ok: true,
          status: 200,
          statusText: 'OK',
          headers: new Headers(),
          blob: () => Promise.resolve(blob),
        })

        await downloadFile('/docs', { archive: 'zip', filename: '   ' })

        expectFetchCall(1, '/api/v1/download/docs?download=true&archive=zip')
        expect(createdLink?.download).toBe('download.zip')
        createElementSpy.mockRestore()
      })

      it('surfaces structured backend errors', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: false,
          status: 409,
          statusText: 'Conflict',
          json: () => Promise.resolve({ error: { message: '父路径不是目录' } }),
        })

        await expect(downloadFile('/docs/report.txt')).rejects.toThrow('父路径不是目录')
      })

      it('surfaces problem-json details for failed downloads', async () => {
        const json = vi.fn(() => Promise.resolve({
          title: 'Service unavailable',
          detail: 'download storage unavailable',
          status: 503,
        }))

        mockFetch.mockResolvedValueOnce({
          ok: false,
          status: 503,
          statusText: 'Service Unavailable',
          headers: new Headers({ 'Content-Type': 'application/problem+json' }),
          clone: () => ({ json }),
          json: () => Promise.resolve({
            title: 'Service unavailable',
            detail: 'download storage unavailable',
            status: 503,
          }),
        })

        await expect(downloadFile('/docs/report.txt')).rejects.toMatchObject({
          message: 'download storage unavailable',
          status: 503,
        })
        expect(json).toHaveBeenCalled()
      })

      it('treats successful structured JSON without an attachment header as a download error', async () => {
        const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
        const createObjectURLSpy = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test')
        const json = vi.fn(() => Promise.resolve({
          code: 'PAYLOAD_TOO_LARGE',
          message: 'archive content is too large',
        }))
        const blob = vi.fn(() => Promise.resolve(new Blob(['{}'], { type: 'application/json' })))

        mockFetch.mockResolvedValueOnce({
          ok: true,
          status: 200,
          statusText: 'OK',
          headers: new Headers({ 'Content-Type': 'application/json' }),
          clone: () => ({ json }),
          blob,
        })

        await expect(downloadFile('/docs', { archive: 'zip' })).rejects.toMatchObject({
          message: 'archive content is too large',
          status: 200,
          code: 'PAYLOAD_TOO_LARGE',
        })
        expect(json).toHaveBeenCalled()
        expect(blob).not.toHaveBeenCalled()
        expect(createObjectURLSpy).not.toHaveBeenCalled()
        expect(clickSpy).not.toHaveBeenCalled()
      })

      it('downloads JSON content when an attachment header is present', async () => {
        const blob = new Blob(['{"message":"keep"}'], { type: 'application/json' })
        const clone = vi.fn()
        const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
        const createObjectURLSpy = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:test')
        vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})

        mockFetch.mockResolvedValueOnce({
          ok: true,
          status: 200,
          statusText: 'OK',
          headers: new Headers({
            'Content-Type': 'application/json',
            'Content-Disposition': 'attachment; filename="data.json"',
          }),
          clone,
          blob: () => Promise.resolve(blob),
        })

        await downloadFile('/docs/data.json')

        expect(clone).not.toHaveBeenCalled()
        expect(createObjectURLSpy).toHaveBeenCalledWith(blob)
        expect(clickSpy).toHaveBeenCalled()
      })
    })

    describe('downloadDiagnosticsExport', () => {
      it('uses content-disposition filename when provided', async () => {
        const blob = new Blob(['{}'], { type: 'application/json' })
        let createdLink: HTMLAnchorElement | undefined
        const originalCreateElement = document.createElement.bind(document)
        const createElementSpy = vi.spyOn(document, 'createElement').mockImplementation(((tagName: string) => {
          const element = originalCreateElement(tagName)
          if (tagName === 'a') {
            createdLink = element as HTMLAnchorElement
          }
          return element
        }) as typeof document.createElement)
        vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:diagnostics')
        vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
        vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

        mockFetch.mockResolvedValueOnce({
          ok: true,
          status: 200,
          statusText: 'OK',
          headers: new Headers({ 'Content-Disposition': 'attachment; filename="diagnostics.json"' }),
          blob: () => Promise.resolve(blob),
        })

        await downloadDiagnosticsExport()

        expect(createdLink?.download).toBe('diagnostics.json')
        createElementSpy.mockRestore()
      })

      it('forwards abort signal when downloading diagnostics export', async () => {
        const controller = new AbortController()
        const blob = new Blob(['{}'], { type: 'application/json' })
        vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:diagnostics')
        vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
        vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

        mockFetch.mockResolvedValueOnce({
          ok: true,
          status: 200,
          statusText: 'OK',
          headers: new Headers(),
          blob: () => Promise.resolve(blob),
        })

        await downloadDiagnosticsExport({ signal: controller.signal })

        expectFetchCall(1, '/api/v1/diagnostics-export', {
          signal: controller.signal,
        })
      })

      it('surfaces structured backend errors', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: false,
          status: 403,
          statusText: 'Forbidden',
          json: () => Promise.resolve({ error: { message: 'admin access required' } }),
        })

        await expect(downloadDiagnosticsExport()).rejects.toThrow('admin access required')
      })

      it('treats successful structured JSON without an attachment header as an export error', async () => {
        const json = vi.fn(() => Promise.resolve({
          code: 'INTERNAL_ERROR',
          message: 'diagnostics export failed',
        }))
        const blob = vi.fn(() => Promise.resolve(new Blob(['{}'], { type: 'application/json' })))
        const createObjectURLSpy = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:diagnostics-error')
        const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

        mockFetch.mockResolvedValueOnce({
          ok: true,
          status: 200,
          statusText: 'OK',
          headers: new Headers({ 'Content-Type': 'application/json' }),
          clone: () => ({ json }),
          blob,
        })

        await expect(downloadDiagnosticsExport()).rejects.toMatchObject({
          message: 'diagnostics export failed',
          status: 200,
          code: 'INTERNAL_ERROR',
        })
        expect(json).toHaveBeenCalled()
        expect(blob).not.toHaveBeenCalled()
        expect(createObjectURLSpy).not.toHaveBeenCalled()
        expect(clickSpy).not.toHaveBeenCalled()
      })
    })

    describe('getThumbnailUrl', () => {
      it('returns an empty URL when path is missing', () => {
        expect(getThumbnailUrl()).toBe('')
      })

      it('generates thumbnail URL with default size', () => {
        expect(getThumbnailUrl('/photo.jpg')).toBe('/api/v1/thumbnails/photo.jpg?size=medium')
      })

      it('respects size parameter', () => {
        expect(getThumbnailUrl('/photo.jpg', 'small')).toBe('/api/v1/thumbnails/photo.jpg?size=small')
        expect(getThumbnailUrl('/photo.jpg', 'large')).toBe('/api/v1/thumbnails/photo.jpg?size=large')
      })

      it('encodes thumbnail size as a query parameter value', () => {
        const unsafeSize = 'small&download=true' as Parameters<typeof getThumbnailUrl>[1]

        expect(getThumbnailUrl('/photo.jpg', unsafeSize)).toBe('/api/v1/thumbnails/photo.jpg?size=small%26download%3Dtrue')
      })

      it('does not add auth query when token exists', () => {
        localStorage.setItem('mnemonas_token', 'test-token')
      expect(getThumbnailUrl('/photo.jpg')).toBe('/api/v1/thumbnails/photo.jpg?size=medium')
      })
    })
  })

  describe('getDiskHealth', () => {
    it('fetches disk health report', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            enabled: true,
            status: 'warning',
            checked_at: '2026-05-13T08:30:00Z',
            message: 'one or more disks need attention',
            warnings: ['data: temperature high'],
            devices: [{
              name: 'data',
              path: '/dev/disk/by-id/test',
              type: 'sat',
              expected_serial: 'SER123',
              serial: 'SER123',
              model: 'TestDisk',
              present: true,
              smart_available: true,
              smart_passed: true,
              temperature_c: 52,
              power_on_hours: 1234,
              wear_percent_used: 82,
              available_spare_percent: 93,
              available_spare_threshold_percent: 10,
              media_errors: 0,
              nvme_critical_warning: 0,
              status: 'warning',
              message: 'temperature high',
              temperature_warning_c: 50,
              temperature_critical_c: 60,
            }],
          },
        }),
      })

      const result = await getDiskHealth()

      expect(mockFetch).toHaveBeenCalledWith('/api/v1/maintenance/disk-health', expect.any(Object))
      expect(result.enabled).toBe(true)
      expect(result.status).toBe('warning')
      expect(result.checkedAt).toBe('2026-05-13T08:30:00Z')
      expect(result.devices[0].smartAvailable).toBe(true)
      expect(result.devices[0].temperatureC).toBe(52)
      expect(result.devices[0].wearPercentUsed).toBe(82)
      expect(result.devices[0].availableSparePercent).toBe(93)
      expect(result.devices[0].availableSpareThresholdPercent).toBe(10)
      expect(result.devices[0].mediaErrors).toBe(0)
      expect(result.devices[0].expectedSerial).toBe('SER123')
    })

    it('forwards abort signal when fetching disk health report', async () => {
      const controller = new AbortController()
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            enabled: false,
            status: 'unknown',
            checked_at: '2026-05-13T08:30:00Z',
            devices: [],
          },
        }),
      })

      await getDiskHealth({ signal: controller.signal })

      expect(mockFetch).toHaveBeenCalledWith(
        '/api/v1/maintenance/disk-health',
        expect.objectContaining({ signal: controller.signal }),
      )
    })

    it('rejects malformed disk health data', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            enabled: true,
            status: 'ok',
            checked_at: '2026-05-13T08:30:00Z',
            devices: [{ path: '/dev/sda', present: true, status: 'ok' }],
          },
        }),
      })

      await expect(getDiskHealth()).rejects.toThrow('服务器返回了无效的数据')
    })

    it.each([
      ['non-finite temperature', { temperature_c: Number.NaN }],
      ['negative power-on hours', { power_on_hours: -1 }],
      ['fractional power-on hours', { power_on_hours: 1.5 }],
      ['negative wear percentage', { wear_percent_used: -1 }],
      ['out-of-range available spare', { available_spare_percent: 101 }],
      ['unsafe media error count', { media_errors: 9007199254740992 }],
      ['negative critical warning bitmask', { nvme_critical_warning: -1 }],
      ['non-finite temperature warning', { temperature_warning_c: Number.POSITIVE_INFINITY }],
    ])('rejects disk health devices with %s', async (_label, deviceOverride) => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            enabled: true,
            status: 'warning',
            checked_at: '2026-05-13T08:30:00Z',
            devices: [{
              path: '/dev/disk/by-id/test',
              present: true,
              smart_available: true,
              status: 'warning',
              ...deviceOverride,
            }],
          },
        }),
      })

      await expect(getDiskHealth()).rejects.toThrow('服务器返回了无效的数据')
    })
  })

  describe('getDiagnostics', () => {
    it('forwards abort signal when fetching diagnostics info', async () => {
      const controller = new AbortController()
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            timestamp: '2024-01-15T10:00:00Z',
            uptime: '1h30m',
            version: { name: 'MnemoNAS', version: '0.1.0', go: 'go1.24.0' },
          },
        }),
      })

      await getDiagnostics({ signal: controller.signal })

      expectFetchCall(1, '/api/v1/diagnostics', {
        signal: controller.signal,
      })
    })

    it('fetches diagnostics info', async () => {
      const mockResponse = {
        success: true,
        data: {
          timestamp: '2024-01-15T10:00:00Z',
          uptime: '1h30m',
          uptime_secs: 5400,
          version: { name: 'MnemoNAS', version: '0.1.0', go: '1.21', build_time: '2026-04-29T00:00:00Z' },
          system: {
            filesystem_initialized: true,
            dataplane_connected: true,
            thumbnail_service_ready: false,
            maintenance_history_ready: true,
            activity_log_ready: true,
            favorites_store_ready: true,
            smb_runtime_ready: false,
          },
          memory: {
            alloc_mb: 50,
            total_alloc_mb: 100,
            sys_mb: 200,
            num_gc: 10,
          },
          goroutines: 25,
          filesystem: {
            trash_stats_available: true,
            trash_items: 5,
            trash_size: 1024,
            disk_stats_available: true,
            disk_total: 20480,
            disk_free: 10240,
            disk_available: 8192,
            disk_used: 10240,
            disk_usage_ratio: 0.5,
            disk_filesystem_type: 'btrfs',
            disk_mount_point: '/srv/mnemonas',
            disk_mount_source: 'tank/mnemonas',
            disk_mount_options: 'rw,relatime',
            disk_native_data_checksum_support: true,
          },
          alerts: {
            enabled: true,
            runtime_available: true,
            check_interval: '30m',
            threshold_pct: 85,
            critical_pct: 92,
            min_free_bytes: 21474836480,
            cooldown_period: '2h',
            webhook_configured: true,
            telegram_configured: true,
            wecom_configured: true,
            email_configured: true,
            webhook_method: 'POST',
            last_level: 'warning',
            last_checked_at: '2026-04-29T10:30:00Z',
            last_used_pct: 87.5,
            last_free_bytes: 9663676416,
          },
          maintenance: {
            history_ready: true,
            scrub_schedule_enabled: true,
            scrub_schedule_interval: '168h0m0s',
            scrub_retry_interval: '1h0m0s',
            scrub_max_retries: 1,
            last_scrub_status: 'completed',
            last_scrub_at: '2026-05-13T08:30:00Z',
            scrub_failure_retries: 0,
          },
          disk_health: {
            enabled: true,
            runtime_available: true,
            check_interval: '1h',
            probe_timeout: '15s',
            cooldown_period: '4h',
            temperature_warning_c: 50,
            temperature_critical_c: 60,
            media_wear_warning_percent: 80,
            media_wear_critical_percent: 100,
            device_count: 1,
            last_status: 'ok',
            last_checked_at: '2026-05-13T08:30:00Z',
            last_warning_count: 0,
            last_device_count: 1,
            last_critical_devices: 0,
            last_warning_devices: 0,
            last_unavailable_devices: 0,
          },
          smb: {
            enabled: true,
            runtime_available: false,
            implementation: 'planned_sidecar',
            listen: '127.0.0.1:1445',
            server_name: 'mnemonas',
            signing_required: true,
            encryption_required: false,
            share_count: 1,
            credentials_ready: true,
            gateway_configured: true,
            message: 'SMB is configured but unavailable.',
          },
          storage: { total_chunks: 100, total_size: 10240, unique_size: 8192, dedup_ratio: 1.25 },
          dataplane: { healthy: true, version: '0.1.0', uptime_sec: 3600 },
        },
      }

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      })

      const result = await getDiagnostics()
      expect(result.uptimeSecs).toBe(5400)
      expect(result.version.buildTime).toBe('2026-04-29T00:00:00Z')
      expect(result.system?.filesystemInitialized).toBe(true)
      expect(result.system?.maintenanceHistoryReady).toBe(true)
      expect(result.system?.activityLogReady).toBe(true)
      expect(result.system?.favoritesStoreReady).toBe(true)
      expect(result.system?.smbRuntimeReady).toBe(false)
      expect(result.memory?.allocMb).toBe(50)
      expect(result.goroutines).toBe(25)
      expect(result.filesystem?.trashStatsAvailable).toBe(true)
      expect(result.filesystem?.trashItems).toBe(5)
      expect(result.filesystem?.diskStatsAvailable).toBe(true)
      expect(result.filesystem?.diskTotal).toBe(20480)
      expect(result.filesystem?.diskAvailable).toBe(8192)
      expect(result.filesystem?.diskUsageRatio).toBe(0.5)
      expect(result.filesystem?.diskFilesystemType).toBe('btrfs')
      expect(result.filesystem?.diskMountPoint).toBe('/srv/mnemonas')
      expect(result.filesystem?.diskMountSource).toBe('tank/mnemonas')
      expect(result.filesystem?.diskMountOptions).toBe('rw,relatime')
      expect(result.filesystem?.diskNativeDataChecksumSupport).toBe(true)
      expect(result.alerts?.enabled).toBe(true)
      expect(result.alerts?.runtimeAvailable).toBe(true)
      expect(result.alerts?.webhookConfigured).toBe(true)
      expect(result.alerts?.telegramConfigured).toBe(true)
      expect(result.alerts?.wecomConfigured).toBe(true)
      expect(result.alerts?.emailConfigured).toBe(true)
      expect(result.alerts?.lastLevel).toBe('warning')
      expect(result.alerts?.lastUsedPct).toBe(87.5)
      expect(result.maintenance?.historyReady).toBe(true)
      expect(result.maintenance?.scrubScheduleEnabled).toBe(true)
      expect(result.maintenance?.scrubScheduleInterval).toBe('168h0m0s')
      expect(result.maintenance?.scrubRetryInterval).toBe('1h0m0s')
      expect(result.maintenance?.scrubMaxRetries).toBe(1)
      expect(result.maintenance?.lastScrubStatus).toBe('completed')
      expect(result.maintenance?.lastScrubAt).toBe('2026-05-13T08:30:00Z')
      expect(result.diskHealth?.enabled).toBe(true)
      expect(result.diskHealth?.lastStatus).toBe('ok')
      expect(result.diskHealth?.lastDeviceCount).toBe(1)
      expect(result.diskHealth?.mediaWearWarningPercent).toBe(80)
      expect(result.smb?.enabled).toBe(true)
      expect(result.smb?.runtimeAvailable).toBe(false)
      expect(result.smb?.implementation).toBe('planned_sidecar')
      expect(result.smb?.shareCount).toBe(1)
      expect(result.storage?.dedupRatio).toBe(1.25)
      expect(result.dataplane?.healthy).toBe(true)
    })

    it('handles missing optional fields', async () => {
      const mockResponse = {
        success: true,
        data: {
          timestamp: '2024-01-15T10:00:00Z',
          uptime: '1h30m',
          version: { name: 'MnemoNAS', version: '0.1.0', go: '1.21' },
        },
      }

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      })

      const result = await getDiagnostics()
      expect(result.uptimeSecs).toBeUndefined()
      expect(result.system).toBeUndefined()
      expect(result.memory).toBeUndefined()
      expect(result.goroutines).toBeUndefined()
      expect(result.filesystem).toBeUndefined()
      expect(result.alerts).toBeUndefined()
      expect(result.maintenance).toBeUndefined()
      expect(result.diskHealth).toBeUndefined()
      expect(result.smb).toBeUndefined()
      expect(result.storage).toBeUndefined()
      expect(result.dataplane).toBeUndefined()
    })

    it('preserves unknown nested diagnostics fields instead of coercing defaults', async () => {
      const mockResponse = {
        success: true,
        data: {
          timestamp: '2024-01-15T10:00:00Z',
          uptime: '1h30m',
          uptime_secs: 0,
          version: { name: 'MnemoNAS', version: '0.1.0', go: '1.21' },
          system: {},
          memory: {},
          filesystem: {},
          alerts: {},
          maintenance: {},
          disk_health: {},
          smb: {},
          storage: {},
          dataplane: {},
        },
      }

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      })

      const result = await getDiagnostics()
      expect(result.uptimeSecs).toBe(0)
      expect(result.system?.filesystemInitialized).toBeUndefined()
      expect(result.system?.maintenanceHistoryReady).toBeUndefined()
      expect(result.system?.activityLogReady).toBeUndefined()
      expect(result.system?.favoritesStoreReady).toBeUndefined()
      expect(result.system?.smbRuntimeReady).toBeUndefined()
      expect(result.memory?.allocMb).toBeUndefined()
      expect(result.filesystem?.trashItems).toBeUndefined()
      expect(result.alerts?.enabled).toBeUndefined()
      expect(result.alerts?.lastLevel).toBeUndefined()
      expect(result.maintenance?.historyReady).toBeUndefined()
      expect(result.maintenance?.scrubScheduleEnabled).toBeUndefined()
      expect(result.diskHealth?.enabled).toBeUndefined()
      expect(result.diskHealth?.lastStatus).toBeUndefined()
      expect(result.smb?.enabled).toBeUndefined()
      expect(result.storage?.dedupRatio).toBeUndefined()
      expect(result.dataplane?.healthy).toBeUndefined()
    })

    it('rejects invalid wrapped response for diagnostics', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({ timestamp: '2024-01-15T10:00:00Z' }),
      })

      await expect(getDiagnostics()).rejects.toThrow('服务器返回了无效的数据')
    })

    it('rejects malformed successful diagnostics payloads', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            timestamp: '2024-01-15T10:00:00Z',
            uptime: '1h30m',
            version: { name: 'MnemoNAS', version: '0.1.0' },
          },
        }),
      })

      await expect(getDiagnostics()).rejects.toThrow('服务器返回了无效的数据')
    })

    it.each([
      ['uptime counters', { uptime_secs: 'bad' }],
      ['negative uptime counters', { uptime_secs: -1 }],
      ['unsafe goroutine counters', { goroutines: 9007199254740992 }],
      ['system state', { system: { filesystem_initialized: 'yes' } }],
      ['memory stats', { memory: { alloc_mb: '50' } }],
      ['non-finite memory stats', { memory: { alloc_mb: Number.POSITIVE_INFINITY } }],
      ['filesystem stats', { filesystem: { trash_items: '5' } }],
      ['negative filesystem counters', { filesystem: { trash_items: -1 } }],
      ['unsafe filesystem bytes', { filesystem: { disk_total: 9007199254740992 } }],
      ['out-of-range filesystem ratio', { filesystem: { disk_usage_ratio: 1.5 } }],
      ['alert settings', { alerts: { enabled: 'yes' } }],
      ['out-of-range alert threshold', { alerts: { threshold_pct: 101 } }],
      ['negative alert free bytes', { alerts: { min_free_bytes: -1 } }],
      ['non-finite alert usage percent', { alerts: { last_used_pct: Number.POSITIVE_INFINITY } }],
      ['maintenance settings', { maintenance: { scrub_schedule_enabled: 'yes' } }],
      ['fractional maintenance counters', { maintenance: { scrub_max_retries: 1.5 } }],
      ['negative maintenance counters', { maintenance: { scrub_failure_retries: -1 } }],
      ['disk-health temperatures', { disk_health: { temperature_warning_c: Number.NaN } }],
      ['out-of-range disk-health wear percent', { disk_health: { media_wear_warning_percent: 101 } }],
      ['unsafe disk-health device count', { disk_health: { device_count: 9007199254740992 } }],
      ['SMB settings', { smb: { enabled: 'yes' } }],
      ['negative SMB share count', { smb: { share_count: -1 } }],
      ['storage stats', { storage: { total_chunks: '100' } }],
      ['unsafe storage bytes', { storage: { total_size: 9007199254740992 } }],
      ['non-finite storage ratio', { storage: { dedup_ratio: Number.POSITIVE_INFINITY } }],
      ['dataplane status', { dataplane: { healthy: 'yes' } }],
      ['negative dataplane uptime', { dataplane: { uptime_sec: -1 } }],
    ])('rejects diagnostics responses with invalid %s', async (_label, overrides) => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            timestamp: '2024-01-15T10:00:00Z',
            uptime: '1h30m',
            version: { name: 'MnemoNAS', version: '0.1.0', go: '1.21' },
            ...overrides,
          },
        }),
      })

      await expect(getDiagnostics()).rejects.toThrow('服务器返回了无效的数据')
    })
  })

  describe('getScrubResult', () => {
    it('fetches last scrub result', async () => {
      const mockResponse = {
        success: true,
        data: {
          has_result: true,
          id: 'scrub-123',
          status: 'completed',
          total_objects: 100,
          valid_objects: 98,
          corrupted_objects: 2,
        },
        timestamp: '2024-01-01',
      }

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      })

      const result = await getScrubResult()
      expect(result.has_result).toBe(true)
      expect(result.status).toBe('completed')
      expect(result.corrupted_objects).toBe(2)
    })

    it('forwards abort signal when fetching scrub result', async () => {
      const controller = new AbortController()
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: { has_result: false },
        }),
      })

      await getScrubResult({ signal: controller.signal })

      expectFetchCall(1, '/api/v1/maintenance/scrub', {
        signal: controller.signal,
      })
    })

    it('accepts scrub results with detailed errors', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            has_result: true,
            status: 'failed',
            errors: [
              { hash: 'hash1', error_type: 'corrupt', message: 'checksum mismatch' },
            ],
          },
          timestamp: '2024-01-01',
        }),
      })

      const result = await getScrubResult()
      expect(result.errors).toEqual([
        { hash: 'hash1', error_type: 'corrupt', message: 'checksum mismatch' },
      ])
    })

    it('rejects wrapped scrub responses when success is false', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: false,
          data: {
            has_result: true,
            status: 'completed',
          },
        }),
      })

      await expect(getScrubResult()).rejects.toThrow('服务器返回了无效的数据')
    })

    it('rejects malformed successful scrub result payloads', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: null,
        }),
      })

      await expect(getScrubResult()).rejects.toThrow('服务器返回了无效的数据')
    })

    it.each([
      ['message', { message: 123 }],
      ['id', { id: 123 }],
      ['start time', { start_time: 123 }],
      ['end time', { end_time: 123 }],
      ['status', { status: 'unknown' }],
      ['numeric counters', { total_objects: '100' }],
      ['negative counters', { total_objects: -1 }],
      ['fractional counters', { valid_objects: 1.5 }],
      ['unsafe counters', { total_size: 9007199254740992 }],
      ['non-finite counters', { duration_ms: Number.POSITIVE_INFINITY }],
      ['error message', { error_message: 404 }],
    ])('rejects scrub results with invalid %s', async (_label, overrides) => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            has_result: true,
            ...overrides,
          },
        }),
      })

      await expect(getScrubResult()).rejects.toThrow('服务器返回了无效的数据')
    })
  })

  describe('runScrub', () => {
    it('runs full scrub without specific hashes', async () => {
      const mockResponse = {
        success: true,
        data: {
          total_objects: 100,
          valid_objects: 99,
          corrupted_objects: 1,
          missing_objects: 0,
          total_size: 2048,
          duration_ms: 500,
          errors: [],
        },
        timestamp: '2024-01-01',
      }

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      })

      const result = await runScrub()
      expect(result.has_result).toBe(true)
      expect(result.status).toBe('completed')
      expect(result.corrupted_objects).toBe(1)
      expect(result.warning).toBe(false)
    })

    it('returns warning details for successful scrub responses with persistence warnings', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        headers: { get: (name: string) => name === 'Warning' ? '199 MnemoNAS "scrub result persistence incomplete"' : null },
        json: () => Promise.resolve({
          success: true,
          data: {
            total_objects: 100,
            valid_objects: 100,
            corrupted_objects: 0,
            missing_objects: 0,
            total_size: 2048,
            duration_ms: 500,
            errors: [],
            warning: true,
          },
          message: 'scrub completed with persistence warning',
          timestamp: '2024-01-01',
        }),
      })

      const result = await runScrub()
      expect(result).toMatchObject({
        has_result: true,
        status: 'completed',
        warning: true,
        message: 'scrub completed with persistence warning',
      })
    })

    it('runs scrub for specific hashes', async () => {
      const mockResponse = {
        success: true,
        data: {
          total_objects: 2,
          valid_objects: 2,
          corrupted_objects: 0,
          missing_objects: 0,
          total_size: 128,
          duration_ms: 10,
          errors: [],
        },
        timestamp: '2024-01-01',
      }

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      })

      const hashes = ['hash1', 'hash2']
      await runScrub(hashes)

      expectFetchCall(1, '/api/v1/maintenance/scrub', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ hashes }),
      })
    })

    it('rejects malformed scrub-run wrappers', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: false,
        }),
      })

      await expect(runScrub()).rejects.toThrow('服务器返回了无效的数据')
    })

    it('rejects scrub-run payloads that are not objects', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: null,
        }),
      })

      await expect(runScrub()).rejects.toThrow('服务器返回了无效的数据')
    })

    it('rejects malformed successful scrub-run responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            errors: 'invalid',
          },
        }),
      })

      await expect(runScrub()).rejects.toThrow('服务器返回了无效的数据')
    })
  })

  describe('backup jobs', () => {
    const backupJob = {
      id: 'external-disk',
      name: 'External disk',
      type: 'local',
      source: '/srv/mnemonas',
      destination: '/mnt/backup-drive/mnemonas',
      disabled: false,
      schedule_interval: '24h0m0s',
      schedule_window_start: '02:00',
      schedule_window_end: '05:00',
      next_run_at: '2026-05-10T02:03:04Z',
      stale_after: '72h0m0s',
      restore_drill_stale_after: '720h0m0s',
      max_snapshots: 7,
      max_age: '720h0m0s',
      retention_status: 'ok',
      retention_message: '本地快照自动清理已配置',
      health_status: 'ok',
      health_message: 'last successful backup completed recently',
      restore_drill_status: 'ok',
      restore_drill_message: '恢复演练仍在预期窗口内',
      last_restore_drill_reminder_at: '2026-05-08T03:00:00Z',
      restore_drill_stats: {
        total_runs: 2,
        successful_runs: 1,
        failed_runs: 1,
        success_rate: 0.5,
        consecutive_successes: 1,
        latest_success_at: '2026-05-09T03:00:01Z',
        latest_failure_at: '2026-05-08T03:00:01Z',
        last_failure_message: 'manifest missing',
        last_failure_category: 'integrity_check',
      },
      include_config: true,
      verify_after_backup: true,
      exclude: ['.mnemonas/thumbnails'],
      running: false,
      last_run: {
        id: '20260509T020304.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T02:03:04Z',
        finished_at: '2026-05-09T02:03:05Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: '/mnt/backup-drive/mnemonas',
        snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
        manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
        file_count: 12,
        total_bytes: 4096,
        config_included: true,
        trigger: 'scheduled',
        warning: false,
        warnings: [],
        pruned_snapshots: 1,
      },
      last_successful_run: {
        id: '20260509T020304.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T02:03:04Z',
        finished_at: '2026-05-09T02:03:05Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: '/mnt/backup-drive/mnemonas',
        snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
        manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
        file_count: 12,
        total_bytes: 4096,
        config_included: true,
        trigger: 'scheduled',
        warning: false,
        warnings: [],
        pruned_snapshots: 1,
      },
      last_restore_drill: {
        id: '20260509T030000.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T03:00:00Z',
        finished_at: '2026-05-09T03:00:01Z',
        duration_ms: 1000,
        snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
        manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
        restored_path: '/mnt/backup-drive/mnemonas/external-disk/restore-drills/20260509T030000.000000000Z/restored',
        artifact_kept: true,
        file_count: 12,
        verified_bytes: 4096,
      },
      restore_drill_history: [{
        id: '20260509T030000.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T03:00:00Z',
        finished_at: '2026-05-09T03:00:01Z',
        duration_ms: 1000,
        snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
        manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
        restored_path: '/mnt/backup-drive/mnemonas/external-disk/restore-drills/20260509T030000.000000000Z/restored',
        artifact_kept: true,
        file_count: 12,
        verified_bytes: 4096,
      }],
      last_restore: {
        id: '20260509T040000.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T04:00:00Z',
        finished_at: '2026-05-09T04:00:01Z',
        duration_ms: 1000,
        snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
        manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
        target_path: '/restore/mnemonas',
        config_restored: true,
        config_path: '/restore/mnemonas/.mnemonas-restore/config.toml',
        file_count: 12,
        verified_bytes: 4096,
      },
      last_restore_verify: {
        id: '20260509T040005.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T04:00:05Z',
        finished_at: '2026-05-09T04:00:06Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: '/mnt/backup-drive/mnemonas',
        target_path: '/restore/mnemonas',
        file_count: 12,
        verified_bytes: 4096,
        config_found: true,
        files_dir_found: true,
        internal_dir_found: true,
        index_found: true,
        objects_dir_found: true,
        looks_like_storage_root: true,
        warnings: [],
      },
      last_matching_restore_verify: {
        id: '20260509T040005.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T04:00:05Z',
        finished_at: '2026-05-09T04:00:06Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: '/mnt/backup-drive/mnemonas',
        snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
        manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
        target_path: '/restore/mnemonas',
        file_count: 12,
        verified_bytes: 4096,
        config_path: '/restore/mnemonas/.mnemonas-restore/config.toml',
        config_found: true,
        files_dir_found: true,
        internal_dir_found: true,
        index_found: true,
        objects_dir_found: true,
        looks_like_storage_root: true,
        warnings: [],
      },
      restore_report_findings: ['未发现阻塞项；仍需在切换前按恢复清单人工复核。'],
      restore_history: [{
        id: '20260509T040000.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T04:00:00Z',
        finished_at: '2026-05-09T04:00:01Z',
        duration_ms: 1000,
        snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
        manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
        target_path: '/restore/mnemonas',
        config_restored: true,
        config_path: '/restore/mnemonas/.mnemonas-restore/config.toml',
        file_count: 12,
        verified_bytes: 4096,
      }],
      last_retention_check: {
        id: '20260509T041000.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T04:10:00Z',
        finished_at: '2026-05-09T04:10:01Z',
        duration_ms: 1000,
        target: '/mnt/backup-drive/mnemonas',
        policy: '',
        snapshot_count: 3,
        oldest_snapshot_at: '2026-05-07T02:00:00Z',
        latest_snapshot_at: '2026-05-09T02:03:05Z',
        warning: false,
        warnings: [],
      },
    }

    it('lists configured backup jobs', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: [backupJob],
          timestamp: '2026-05-09',
        }),
      })

      const jobs = await listBackupJobs()
      expect(jobs).toHaveLength(1)
      expect(jobs[0].id).toBe('external-disk')
      expect(jobs[0].health_status).toBe('ok')
      expect(jobs[0].restore_drill_status).toBe('ok')
      expect(jobs[0].last_restore_drill_reminder_at).toBe('2026-05-08T03:00:00Z')
      expect(jobs[0].restore_drill_stats?.success_rate).toBe(0.5)
      expect(jobs[0].restore_drill_stats?.last_failure_category).toBe('integrity_check')
      expect(jobs[0].retention_status).toBe('ok')
      expect(jobs[0].schedule_interval).toBe('24h0m0s')
      expect(jobs[0].schedule_window_start).toBe('02:00')
      expect(jobs[0].schedule_window_end).toBe('05:00')
      expect(jobs[0].max_snapshots).toBe(7)
      expect(jobs[0].last_run?.file_count).toBe(12)
      expect(jobs[0].last_run?.pruned_snapshots).toBe(1)
      expect(jobs[0].restore_drill_history).toHaveLength(1)
      expect(jobs[0].last_restore?.target_path).toBe('/restore/mnemonas')
      expect(jobs[0].last_restore_verify?.looks_like_storage_root).toBe(true)
      expect(jobs[0].last_matching_restore_verify?.id).toBe('20260509T040005.000000000Z')
      expect(jobs[0].restore_report_findings).toEqual(['未发现阻塞项；仍需在切换前按恢复清单人工复核。'])
      expect(jobs[0].restore_history).toHaveLength(1)
      expect(jobs[0].last_retention_check?.snapshot_count).toBe(3)
      expectFetchCall(1, '/api/v1/maintenance/backups')
    })

    it('forwards abort signal when listing backup jobs', async () => {
      const controller = new AbortController()
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: [backupJob],
          timestamp: '2026-05-09',
        }),
      })

      await listBackupJobs({ signal: controller.signal })

      expectFetchCall(1, '/api/v1/maintenance/backups', {
        signal: controller.signal,
      })
    })

    it('forwards abort signals for maintenance write requests', async () => {
      const controller = new AbortController()
      const items = [{ job_id: 'external-disk', target_path: '/restore/a', include_config: true }]
      const scrubRun = {
        total_objects: 1,
        valid_objects: 1,
        corrupted_objects: 0,
        missing_objects: 0,
        total_size: 128,
        duration_ms: 10,
        errors: [],
      }
      const previewResult = {
        id: '20260509T035900.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T03:59:00Z',
        finished_at: '2026-05-09T03:59:01Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: '/mnt/backup-drive/mnemonas',
        target_path: '/restore/a',
        file_count: 12,
        total_bytes: 4096,
        config_available: true,
        config_included: true,
        warnings: [],
      }
      const batchPreview = {
        id: '20260509T035901.000000000Z',
        status: 'completed',
        started_at: '2026-05-09T03:59:01Z',
        finished_at: '2026-05-09T03:59:02Z',
        duration_ms: 1000,
        items: [{
          index: 0,
          job_id: 'external-disk',
          target_path: '/restore/a',
          include_config: true,
          status: 'completed',
          preview: previewResult,
        }],
        total_files: 12,
        total_bytes: 4096,
        warning: false,
        warnings: [],
      }
      const batchRestore = {
        id: '20260509T040001.000000000Z',
        status: 'completed',
        started_at: '2026-05-09T04:00:01Z',
        finished_at: '2026-05-09T04:00:02Z',
        duration_ms: 1000,
        items: [{
          index: 0,
          job_id: 'external-disk',
          target_path: '/restore/a',
          include_config: true,
          status: 'completed',
          restore: backupJob.last_restore,
          verify: backupJob.last_restore_verify,
          warnings: [],
        }],
        total_files: 12,
        verified_bytes: 4096,
        warning: false,
        warnings: [],
      }
      const response = (data: unknown) => ({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data,
          timestamp: '2026-05-09',
        }),
      })

      mockFetch.mockResolvedValueOnce(response(scrubRun))
      await runScrub(undefined, { signal: controller.signal })
      expectFetchCall(1, '/api/v1/maintenance/scrub', {
        method: 'POST',
        signal: controller.signal,
      })

      mockFetch.mockResolvedValueOnce(response(backupJob.last_run))
      await runBackupJob('external-disk', { signal: controller.signal })
      expectFetchCall(2, '/api/v1/maintenance/backups/external-disk/run', {
        method: 'POST',
        body: '{}',
        signal: controller.signal,
      })

      mockFetch.mockResolvedValueOnce(response(backupJob.last_retention_check))
      await checkBackupRetentionJob('external-disk', { signal: controller.signal })
      expectFetchCall(3, '/api/v1/maintenance/backups/external-disk/retention-check', {
        method: 'POST',
        body: '{}',
        signal: controller.signal,
      })

      mockFetch.mockResolvedValueOnce(response(backupJob.last_restore_drill))
      await runBackupRestoreDrill('external-disk', true, { signal: controller.signal })
      expectFetchCall(4, '/api/v1/maintenance/backups/external-disk/restore-drill', {
        method: 'POST',
        body: JSON.stringify({ keep_artifact: true }),
        signal: controller.signal,
      })

      mockFetch.mockResolvedValueOnce(response(previewResult))
      await previewBackupRestoreJob('external-disk', '/restore/a', true, { signal: controller.signal })
      expectFetchCall(5, '/api/v1/maintenance/backups/external-disk/restore-preview', {
        method: 'POST',
        body: JSON.stringify({ target_path: '/restore/a', include_config: true }),
        signal: controller.signal,
      })

      mockFetch.mockResolvedValueOnce(response(batchPreview))
      await previewBatchBackupRestore(items, { signal: controller.signal })
      expectFetchCall(6, '/api/v1/maintenance/backups/batch-restore-preview', {
        method: 'POST',
        body: JSON.stringify({ items }),
        signal: controller.signal,
      })

      mockFetch.mockResolvedValueOnce(response(backupJob.last_restore))
      await restoreBackupJob('external-disk', '/restore/a', true, { signal: controller.signal })
      expectFetchCall(7, '/api/v1/maintenance/backups/external-disk/restore', {
        method: 'POST',
        body: JSON.stringify({ target_path: '/restore/a', include_config: true }),
        signal: controller.signal,
      })

      mockFetch.mockResolvedValueOnce(response(batchRestore))
      await runBatchBackupRestore(items, { signal: controller.signal })
      expectFetchCall(8, '/api/v1/maintenance/backups/batch-restore', {
        method: 'POST',
        body: JSON.stringify({ items }),
        signal: controller.signal,
      })

      mockFetch.mockResolvedValueOnce(response(backupJob.last_restore_verify))
      await verifyBackupRestoreJob('external-disk', '/restore/a', { signal: controller.signal })
      expectFetchCall(9, '/api/v1/maintenance/backups/external-disk/restore-verify', {
        method: 'POST',
        body: JSON.stringify({ target_path: '/restore/a' }),
        signal: controller.signal,
      })
    })

    it('runs a backup job', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: backupJob.last_run,
          timestamp: '2026-05-09',
        }),
      })

      const result = await runBackupJob('external-disk')
      expect(result.status).toBe('completed')
      expectFetchCall(1, '/api/v1/maintenance/backups/external-disk/run', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{}',
      })
    })

    it('normalizes backup restore target paths before sending requests', async () => {
      const previewResult = {
        id: '20260509T035900.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T03:59:00Z',
        finished_at: '2026-05-09T03:59:01Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: '/mnt/backup-drive/mnemonas',
        target_path: '/restore/mnemonas',
        file_count: 12,
        total_bytes: 4096,
        config_available: true,
        config_included: true,
      }
      const batchPreview = {
        id: '20260509T035901.000000000Z',
        status: 'completed',
        started_at: '2026-05-09T03:59:01Z',
        finished_at: '2026-05-09T03:59:02Z',
        duration_ms: 1000,
        items: [{
          index: 0,
          job_id: 'external-disk',
          target_path: '/restore/a',
          include_config: true,
          status: 'completed',
          preview: {
            ...previewResult,
            target_path: '/restore/a',
          },
        }],
        total_files: 12,
        total_bytes: 4096,
      }

      mockFetch
        .mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({ success: true, data: previewResult, timestamp: '2026-05-09' }),
        })
        .mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({ success: true, data: batchPreview, timestamp: '2026-05-09' }),
        })

      await previewBackupRestoreJob('external-disk', ' /restore/mnemonas/ ', true)
      await previewBatchBackupRestore([{ job_id: ' external-disk ', target_path: '/restore/a/', include_config: true }])

      expectFetchCall(1, '/api/v1/maintenance/backups/external-disk/restore-preview', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ target_path: '/restore/mnemonas', include_config: true }),
      })
      expectFetchCall(2, '/api/v1/maintenance/backups/batch-restore-preview', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ items: [{ job_id: 'external-disk', target_path: '/restore/a', include_config: true }] }),
      })
    })

    it.each([
      ['preview relative path', () => previewBackupRestoreJob('external-disk', 'restore/mnemonas')],
      ['preview root path', () => previewBackupRestoreJob('external-disk', '/')],
      ['preview backslash path', () => previewBackupRestoreJob('external-disk', '/restore\\mnemonas')],
      ['restore dot segment path', () => restoreBackupJob('external-disk', '/restore/../mnemonas')],
      ['verify control character path', () => verifyBackupRestoreJob('external-disk', '/restore\nmnemonas')],
      ['batch preview relative path', () => previewBatchBackupRestore([{ job_id: 'external-disk', target_path: 'restore/a', include_config: true }])],
      ['batch preview backslash path', () => previewBatchBackupRestore([{ job_id: 'external-disk', target_path: '/restore\\a', include_config: true }])],
      ['batch restore root path', () => runBatchBackupRestore([{ job_id: 'external-disk', target_path: '/', include_config: true }])],
    ])('rejects invalid backup restore target paths before sending requests: %s', async (_name, call) => {
      await expect(call()).rejects.toThrow('非法路径')
      expect(mockFetch).not.toHaveBeenCalled()
    })

    it('runs a backup restore drill', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: backupJob.last_restore_drill,
          timestamp: '2026-05-09',
        }),
      })

      const result = await runBackupRestoreDrill('external-disk', true)
      expect(result.status).toBe('completed')
      expect(result.artifact_kept).toBe(true)
      expectFetchCall(1, '/api/v1/maintenance/backups/external-disk/restore-drill', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ keep_artifact: true }),
      })
    })

    it('checks backup retention policy', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: backupJob.last_retention_check,
          timestamp: '2026-05-09',
        }),
      })

      const result = await checkBackupRetentionJob('external-disk')
      expect(result.status).toBe('completed')
      expect(result.snapshot_count).toBe(3)
      expectFetchCall(1, '/api/v1/maintenance/backups/external-disk/retention-check', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{}',
      })
    })

    it('previews a local backup restore target', async () => {
      const previewResult = {
        id: '20260509T035900.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T03:59:00Z',
        finished_at: '2026-05-09T03:59:01Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: '/mnt/backup-drive/mnemonas',
        snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
        manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
        target_path: '/restore/mnemonas',
        file_count: 12,
        total_bytes: 4096,
        config_available: true,
        config_included: true,
        sample_paths: ['docs/note.txt', '.mnemonas-restore/config.toml'],
        preflight_checks: [{
          id: 'target_scope',
          status: 'passed',
          title: '目标路径隔离',
          detail: '目标目录位于受保护路径之外。',
        }],
        warnings: [],
        cutover_checklist: ['校验恢复目录'],
        rollback_checklist: ['指回原 storage.root'],
      }
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: previewResult,
          timestamp: '2026-05-09',
        }),
      })

      const result = await previewBackupRestoreJob('external-disk', '/restore/mnemonas', true)
      expect(result.status).toBe('completed')
      expect(result.sample_paths).toContain('docs/note.txt')
      expect(result.preflight_checks?.[0].id).toBe('target_scope')
      expect(result.cutover_checklist).toContain('校验恢复目录')
      expectFetchCall(1, '/api/v1/maintenance/backups/external-disk/restore-preview', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ target_path: '/restore/mnemonas', include_config: true }),
      })
    })

    it.each([
      ['fractional duration', { duration_ms: 1.5 }],
      ['negative file_count', { file_count: -1 }],
      ['unsafe total_bytes', { total_bytes: 9007199254740992 }],
      ['relative target_path', { target_path: 'restore/mnemonas' }],
      ['backslash target_path', { target_path: '/restore\\mnemonas' }],
      ['trailing-slash target_path', { target_path: '/restore/mnemonas/' }],
    ])('rejects backup restore preview responses with %s', async (_name, override) => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            id: '20260509T035900.000000000Z',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T03:59:00Z',
            finished_at: '2026-05-09T03:59:01Z',
            duration_ms: 1000,
            source: '/srv/mnemonas',
            destination: '/mnt/backup-drive/mnemonas',
            target_path: '/restore/mnemonas',
            file_count: 12,
            total_bytes: 4096,
            config_available: true,
            config_included: true,
            ...override,
          },
          timestamp: '2026-05-09',
        }),
      })

      await expect(previewBackupRestoreJob('external-disk', '/restore/mnemonas', true)).rejects.toThrow('服务器返回了无效的数据')
    })

    it('previews a batch backup restore request', async () => {
      const batchPreview = {
        id: '20260509T035901.000000000Z',
        status: 'completed',
        started_at: '2026-05-09T03:59:01Z',
        finished_at: '2026-05-09T03:59:02Z',
        duration_ms: 1000,
        items: [{
          index: 0,
          job_id: 'external-disk',
          target_path: '/restore/a',
          include_config: true,
          status: 'completed',
          preview: {
            id: '20260509T035900.000000000Z',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T03:59:00Z',
            finished_at: '2026-05-09T03:59:01Z',
            duration_ms: 1000,
            source: '/srv/mnemonas',
            destination: '/mnt/backup-drive/mnemonas',
            target_path: '/restore/a',
            file_count: 12,
            total_bytes: 4096,
            config_available: true,
            config_included: true,
          },
        }],
        total_files: 12,
        total_bytes: 4096,
        warning: false,
        warnings: [],
      }
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: batchPreview,
          timestamp: '2026-05-09',
        }),
      })

      const items = [{ job_id: 'external-disk', target_path: '/restore/a', include_config: true }]
      const result = await previewBatchBackupRestore(items)
      expect(result.items[0].preview?.file_count).toBe(12)
      expectFetchCall(1, '/api/v1/maintenance/backups/batch-restore-preview', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ items }),
      })
    })

    it.each([
      ['fractional item index', {
        itemOverride: { index: 0.5 },
        resultOverride: {},
        previewOverride: {},
      }],
      ['negative total_files', {
        itemOverride: {},
        resultOverride: { total_files: -1 },
        previewOverride: {},
      }],
      ['unsafe nested preview file_count', {
        itemOverride: {},
        resultOverride: {},
        previewOverride: { file_count: 9007199254740992 },
      }],
      ['relative item target_path', {
        itemOverride: { target_path: 'restore/a' },
        resultOverride: {},
        previewOverride: {},
      }],
      ['backslash item target_path', {
        itemOverride: { target_path: '/restore\\a' },
        resultOverride: {},
        previewOverride: {},
      }],
      ['relative nested preview target_path', {
        itemOverride: {},
        resultOverride: {},
        previewOverride: { target_path: 'restore/a' },
      }],
      ['backslash nested preview target_path', {
        itemOverride: {},
        resultOverride: {},
        previewOverride: { target_path: '/restore\\a' },
      }],
    ])('rejects batch backup restore preview responses with %s', async (_name, overrides) => {
      const preview = {
        id: '20260509T035900.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T03:59:00Z',
        finished_at: '2026-05-09T03:59:01Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: '/mnt/backup-drive/mnemonas',
        target_path: '/restore/a',
        file_count: 12,
        total_bytes: 4096,
        config_available: true,
        config_included: true,
        ...overrides.previewOverride,
      }
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            id: '20260509T035901.000000000Z',
            status: 'completed',
            started_at: '2026-05-09T03:59:01Z',
            finished_at: '2026-05-09T03:59:02Z',
            duration_ms: 1000,
            items: [{
              index: 0,
              job_id: 'external-disk',
              target_path: '/restore/a',
              include_config: true,
              status: 'completed',
              preview,
              ...overrides.itemOverride,
            }],
            total_files: 12,
            total_bytes: 4096,
            ...overrides.resultOverride,
          },
          timestamp: '2026-05-09',
        }),
      })

      const items = [{ job_id: 'external-disk', target_path: '/restore/a', include_config: true }]
      await expect(previewBatchBackupRestore(items)).rejects.toThrow('服务器返回了无效的数据')
    })

    it('restores a local backup job to a target path', async () => {
      const restoreResult = {
        id: '20260509T040000.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T04:00:00Z',
        finished_at: '2026-05-09T04:00:01Z',
        duration_ms: 1000,
        snapshot_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z',
        manifest_path: '/mnt/backup-drive/mnemonas/external-disk/snapshots/20260509T020304.000000000Z/manifest.json',
        target_path: '/restore/mnemonas',
        config_restored: true,
        config_path: '/restore/mnemonas/.mnemonas-restore/config.toml',
        file_count: 12,
        verified_bytes: 4096,
        preflight_checks: [{
          id: 'target_scope',
          status: 'passed',
          title: '目标路径隔离',
          detail: '目标目录位于受保护路径之外。',
        }],
        warnings: [],
        cutover_checklist: ['校验恢复目录'],
        rollback_checklist: ['指回原 storage.root'],
      }
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: restoreResult,
          timestamp: '2026-05-09',
        }),
      })

      const result = await restoreBackupJob('external-disk', '/restore/mnemonas', true)
      expect(result.status).toBe('completed')
      expect(result.target_path).toBe('/restore/mnemonas')
      expect(result.rollback_checklist).toContain('指回原 storage.root')
      expectFetchCall(1, '/api/v1/maintenance/backups/external-disk/restore', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ target_path: '/restore/mnemonas', include_config: true }),
      })
    })

    it.each([
      ['trailing slash target_path', { target_path: '/restore/mnemonas/' }],
      ['backslash target_path', { target_path: '/restore\\mnemonas' }],
      ['backslash config_path', { config_path: '/restore\\mnemonas\\.mnemonas-restore\\config.toml' }],
    ])('rejects backup restore responses with %s', async (_name, override) => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            id: '20260509T040000.000000000Z',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T04:00:00Z',
            finished_at: '2026-05-09T04:00:01Z',
            duration_ms: 1000,
            target_path: '/restore/mnemonas',
            config_restored: true,
            config_path: '/restore/mnemonas/.mnemonas-restore/config.toml',
            file_count: 12,
            verified_bytes: 4096,
            ...override,
          },
          timestamp: '2026-05-09',
        }),
      })

      await expect(restoreBackupJob('external-disk', '/restore/mnemonas', true)).rejects.toThrow('服务器返回了无效的数据')
    })

    it('runs a batch backup restore request', async () => {
      const batchRestore = {
        id: '20260509T040001.000000000Z',
        status: 'completed',
        started_at: '2026-05-09T04:00:01Z',
        finished_at: '2026-05-09T04:00:02Z',
        duration_ms: 1000,
        items: [{
          index: 0,
          job_id: 'external-disk',
          target_path: '/restore/a',
          include_config: true,
          status: 'completed',
          restore: {
            id: '20260509T040000.000000000Z',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T04:00:00Z',
            finished_at: '2026-05-09T04:00:01Z',
            duration_ms: 1000,
            target_path: '/restore/a',
            config_restored: true,
            file_count: 12,
            verified_bytes: 4096,
          },
          verify: {
            id: '20260509T040005.000000000Z',
            job_id: 'external-disk',
            status: 'completed',
            started_at: '2026-05-09T04:00:05Z',
            finished_at: '2026-05-09T04:00:06Z',
            duration_ms: 1000,
            source: '/srv/mnemonas',
            destination: '/mnt/backup-drive/mnemonas',
            target_path: '/restore/a',
            file_count: 12,
            verified_bytes: 4096,
            config_found: true,
            files_dir_found: true,
            internal_dir_found: true,
            index_found: true,
            objects_dir_found: true,
            looks_like_storage_root: true,
          },
          warnings: [],
        }],
        total_files: 12,
        verified_bytes: 4096,
        warning: false,
        warnings: [],
      }
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: batchRestore,
          timestamp: '2026-05-09',
        }),
      })

      const items = [{ job_id: 'external-disk', target_path: '/restore/a', include_config: true }]
      const result = await runBatchBackupRestore(items)
      expect(result.items[0].verify?.looks_like_storage_root).toBe(true)
      expectFetchCall(1, '/api/v1/maintenance/backups/batch-restore', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ items }),
      })
    })

    it.each([
      ['fractional item index', {
        itemOverride: { index: 0.5 },
        resultOverride: {},
        verifyOverride: {},
      }],
      ['unsafe total_files', {
        itemOverride: {},
        resultOverride: { total_files: 9007199254740992 },
        verifyOverride: {},
      }],
      ['negative verified_bytes', {
        itemOverride: {},
        resultOverride: { verified_bytes: -1 },
        verifyOverride: {},
      }],
      ['unsafe nested verify file_count', {
        itemOverride: {},
        resultOverride: {},
        verifyOverride: { file_count: 9007199254740992 },
      }],
      ['relative item target_path', {
        itemOverride: { target_path: 'restore/a' },
        resultOverride: {},
        verifyOverride: {},
      }],
      ['backslash item target_path', {
        itemOverride: { target_path: '/restore\\a' },
        resultOverride: {},
        verifyOverride: {},
      }],
      ['relative nested verify target_path', {
        itemOverride: {},
        resultOverride: {},
        verifyOverride: { target_path: 'restore/a' },
      }],
      ['backslash nested verify target_path', {
        itemOverride: {},
        resultOverride: {},
        verifyOverride: { target_path: '/restore\\a' },
      }],
    ])('rejects batch backup restore responses with %s', async (_name, overrides) => {
      const verify = {
        id: '20260509T040005.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T04:00:05Z',
        finished_at: '2026-05-09T04:00:06Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: '/mnt/backup-drive/mnemonas',
        target_path: '/restore/a',
        file_count: 12,
        verified_bytes: 4096,
        config_found: true,
        files_dir_found: true,
        internal_dir_found: true,
        index_found: true,
        objects_dir_found: true,
        looks_like_storage_root: true,
        ...overrides.verifyOverride,
      }
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            id: '20260509T040001.000000000Z',
            status: 'completed',
            started_at: '2026-05-09T04:00:01Z',
            finished_at: '2026-05-09T04:00:02Z',
            duration_ms: 1000,
            items: [{
              index: 0,
              job_id: 'external-disk',
              target_path: '/restore/a',
              include_config: true,
              status: 'completed',
              verify,
              warnings: [],
              ...overrides.itemOverride,
            }],
            total_files: 12,
            verified_bytes: 4096,
            ...overrides.resultOverride,
          },
          timestamp: '2026-05-09',
        }),
      })

      const items = [{ job_id: 'external-disk', target_path: '/restore/a', include_config: true }]
      await expect(runBatchBackupRestore(items)).rejects.toThrow('服务器返回了无效的数据')
    })

    it('verifies a restored backup target', async () => {
      const verifyResult = {
        id: '20260509T040005.000000000Z',
        job_id: 'external-disk',
        status: 'completed',
        started_at: '2026-05-09T04:00:05Z',
        finished_at: '2026-05-09T04:00:06Z',
        duration_ms: 1000,
        source: '/srv/mnemonas',
        destination: '/mnt/backup-drive/mnemonas',
        target_path: '/restore/mnemonas',
        file_count: 12,
        verified_bytes: 4096,
        config_path: '/restore/mnemonas/.mnemonas-restore/config.toml',
        config_found: true,
        files_dir_found: true,
        internal_dir_found: true,
        index_found: true,
        objects_dir_found: true,
        looks_like_storage_root: true,
        warnings: [],
      }
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: verifyResult,
          timestamp: '2026-05-09',
        }),
      })

      const result = await verifyBackupRestoreJob('external-disk', '/restore/mnemonas')
      expect(result.status).toBe('completed')
      expect(result.looks_like_storage_root).toBe(true)
      expectFetchCall(1, '/api/v1/maintenance/backups/external-disk/restore-verify', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ target_path: '/restore/mnemonas' }),
      })
    })

    it('downloads a backup restore summary', async () => {
      const blob = new Blob(['{}'], { type: 'application/json' })
      let createdLink: HTMLAnchorElement | undefined
      const originalCreateElement = document.createElement.bind(document)
      const createElementSpy = vi.spyOn(document, 'createElement').mockImplementation(((tagName: string) => {
        const element = originalCreateElement(tagName)
        if (tagName === 'a') {
          createdLink = element as HTMLAnchorElement
        }
        return element
      }) as typeof document.createElement)
      vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:restore-summary')
      vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
      vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        statusText: 'OK',
        headers: new Headers({ 'Content-Disposition': 'attachment; filename="restore-summary.json"' }),
        blob: () => Promise.resolve(blob),
      })

      await downloadBackupRestoreReport('external-disk')

      expect(createdLink?.download).toBe('restore-summary.json')
      expectFetchCall(1, '/api/v1/maintenance/backups/external-disk/restore-report')
      createElementSpy.mockRestore()
    })

    it('forwards abort signal when downloading a backup restore summary', async () => {
      const controller = new AbortController()
      const blob = new Blob(['{}'], { type: 'application/json' })
      vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:restore-summary')
      vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
      vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        statusText: 'OK',
        headers: new Headers(),
        blob: () => Promise.resolve(blob),
      })

      await downloadBackupRestoreReport('external-disk', { signal: controller.signal })

      expectFetchCall(1, '/api/v1/maintenance/backups/external-disk/restore-report', {
        signal: controller.signal,
      })
    })

    it('treats successful structured JSON without an attachment header as a restore summary export error', async () => {
      const json = vi.fn(() => Promise.resolve({
        success: false,
        error: {
          code: 'RESTORE_REPORT_UNAVAILABLE',
          message: 'restore report is unavailable',
        },
      }))
      const blob = vi.fn(() => Promise.resolve(new Blob(['{}'], { type: 'application/json' })))
      const createObjectURLSpy = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:restore-summary-error')
      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

      mockFetch.mockResolvedValueOnce({
        ok: true,
        status: 200,
        statusText: 'OK',
        headers: new Headers({ 'Content-Type': 'application/json' }),
        clone: () => ({ json }),
        blob,
      })

      await expect(downloadBackupRestoreReport('external-disk')).rejects.toMatchObject({
        message: 'restore report is unavailable',
        status: 200,
        code: 'RESTORE_REPORT_UNAVAILABLE',
      })
      expect(json).toHaveBeenCalled()
      expect(blob).not.toHaveBeenCalled()
      expect(createObjectURLSpy).not.toHaveBeenCalled()
      expect(clickSpy).not.toHaveBeenCalled()
    })

    it('rejects malformed backup job responses', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: [{ ...backupJob, running: 'no' }],
        }),
      })

      await expect(listBackupJobs()).rejects.toThrow('服务器返回了无效的数据')
    })

    it.each([
      ['max_snapshots', { ...backupJob, max_snapshots: 1.5 }],
      ['last_run duration', { ...backupJob, last_run: { ...backupJob.last_run, duration_ms: -1 } }],
      ['last_run file_count', { ...backupJob, last_run: { ...backupJob.last_run, file_count: 1.5 } }],
      ['last_run total_bytes', { ...backupJob, last_run: { ...backupJob.last_run, total_bytes: 9007199254740992 } }],
      ['last_run pruned_snapshots', { ...backupJob, last_run: { ...backupJob.last_run, pruned_snapshots: -1 } }],
      ['restore drill stats total_runs', { ...backupJob, restore_drill_stats: { ...backupJob.restore_drill_stats, total_runs: 1.5 } }],
      ['restore drill stats success_rate', { ...backupJob, restore_drill_stats: { ...backupJob.restore_drill_stats, success_rate: 1.1 } }],
      ['restore drill verified_bytes', { ...backupJob, last_restore_drill: { ...backupJob.last_restore_drill, verified_bytes: 9007199254740992 } }],
      ['restore file_count', { ...backupJob, last_restore: { ...backupJob.last_restore, file_count: -1 } }],
      ['restore verify verified_bytes', { ...backupJob, last_restore_verify: { ...backupJob.last_restore_verify, verified_bytes: Number.NaN } }],
      ['retention snapshot_count', { ...backupJob, last_retention_check: { ...backupJob.last_retention_check, snapshot_count: 9007199254740992 } }],
    ])('rejects backup job responses with invalid %s', async (_name, job) => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: [job],
          timestamp: '2026-05-09',
        }),
      })

      await expect(listBackupJobs()).rejects.toThrow('服务器返回了无效的数据')
    })
  })

  describe('handleResponse error handling', () => {
    it('handles JSON error response with error field', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 400,
        statusText: 'Bad Request',
        json: () => Promise.resolve({ error: '参数错误' }),
      })

      await expect(listFiles('/test')).rejects.toThrow('参数错误')
    })

    it('handles JSON error response with structured error field', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 400,
        statusText: 'Bad Request',
        json: () => Promise.resolve({ error: { message: '结构化参数错误', code: 'INVALID_ARGUMENT' } }),
      })

      await expect(listFiles('/test')).rejects.toThrow('结构化参数错误')
    })

    it('handles JSON error response with message field', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 400,
        statusText: 'Bad Request',
        json: () => Promise.resolve({ message: '请求无效' }),
      })

      await expect(listFiles('/test')).rejects.toThrow('请求无效')
    })

    it('handles non-JSON error response', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 500,
        statusText: 'Internal Server Error',
        json: () => Promise.reject(new Error('Invalid JSON')),
      })

      await expect(listFiles('/test')).rejects.toThrow('获取文件列表失败: Internal Server Error')
    })

    it('handles invalid JSON in success response', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.reject(new Error('Invalid JSON')),
      })

      await expect(listFiles('/test')).rejects.toThrow('服务器返回了无效的数据')
    })
  })
})
