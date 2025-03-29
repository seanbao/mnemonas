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
  downloadFile,
  getDownloadUrl,
  getThumbnailUrl,
  versionToDisplayFormat,
  getDiagnostics,
  getScrubResult,
  runScrub,
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
}

class MockXMLHttpRequest {
  static queuedResults: MockXHRResult[] = []
  static instances: MockXMLHttpRequest[] = []

  static reset(): void {
    MockXMLHttpRequest.queuedResults = []
    MockXMLHttpRequest.instances = []
  }

  upload = {
    addEventListener: vi.fn(),
  }

  status = 0
  statusText = ''
  responseText = ''
  method = ''
  url = ''
  body: Document | XMLHttpRequestBodyInit | null = null
  headers = new Map<string, string>()
  private listeners = new Map<string, Array<() => void>>()

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

  send(body: Document | XMLHttpRequestBodyInit | null = null): void {
    this.body = body
    const next = MockXMLHttpRequest.queuedResults.shift()
    if (!next) {
      throw new Error('No queued XHR result available')
    }

    this.status = next.status ?? 0
    this.statusText = next.statusText ?? ''
    this.responseText = next.responseText ?? ''
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

      await expect(uploadFile('/docs', new File(['content'], 'report.txt'))).resolves.toBeUndefined()

      expect(mockFetch).toHaveBeenNthCalledWith(1, '/api/v1/auth/refresh', expect.objectContaining({ method: 'POST' }))
      expect(mockFetch).toHaveBeenNthCalledWith(2, '/api/v1/auth/download-session', expect.objectContaining({ method: 'POST' }))
      expect(MockXMLHttpRequest.instances).toHaveLength(2)
      expect(MockXMLHttpRequest.instances[0]?.headers.get('Authorization')).toBe('Bearer access-1')
      expect(MockXMLHttpRequest.instances[1]?.headers.get('Authorization')).toBe('Bearer access-2')
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
          files: [
            { name: 'test.txt', path: '/test.txt', isDir: false, size: 100, modTime: '2024-01-01' },
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

      await expect(deleteFile('/test.txt')).resolves.toBeUndefined()
      expectFetchCall(1, '/api/v1/files/test.txt', {
        method: 'DELETE',
        headers: {},
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
          total_size: 1073741824,
          total_chunks: 100,
          unique_size: 536870912,
          dedup_ratio: 1.5,
        },
      }

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      })

      const result = await getStorageStats()
      expect(result.totalSize).toBe(1073741824)
      expect(result.totalObjects).toBe(100)
      expect(result.uniqueSize).toBe(536870912)
      expect(result.dedupRatio).toBe(1.5)
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
      expect(result.totalSize).toBeUndefined()
      expect(result.totalObjects).toBeUndefined()
      expect(result.uniqueSize).toBeUndefined()
      expect(result.dedupRatio).toBeUndefined()
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

    it('rejects malformed successful storage stats payloads', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            total_size: 'not-a-number',
            total_chunks: null,
            dedup_ratio: 'invalid',
          },
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
          },
        }),
      })

      const result = await getAppVersion()
      expect(result.version).toBe('0.1.0')
      expect(result.go).toBe('go1.24.0')
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

      await expect(createDirectory('/new-folder')).resolves.toBeUndefined()
      expectFetchCall(1, '/api/v1/directories/new-folder', {
        method: 'POST',
        headers: {},
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

      await expect(moveFile('/old.txt', '/new.txt')).resolves.toBeUndefined()
      expectFetchCall(1, '/api/v1/files-move', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ from: '/old.txt', to: '/new.txt' }),
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

      await expect(copyFile('/old.txt', '/copy.txt')).resolves.toBeUndefined()
      expectFetchCall(1, '/api/v1/files-copy', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ from: '/old.txt', to: '/copy.txt' }),
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

      await expect(restoreVersion('/test.txt', 'abc123')).resolves.toBeUndefined()
      expectFetchCall(1, '/api/v1/versions/abc123/restore?path=%2Ftest.txt', {
        method: 'POST',
        headers: {},
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

        await expect(restoreFromTrash('item1')).resolves.toBeUndefined()
        expectFetchCall(1, '/api/v1/trash/item1/restore', {
          method: 'POST',
          headers: {},
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

        await expect(deleteFromTrash('item1')).resolves.toBeUndefined()
        expectFetchCall(1, '/api/v1/trash/item1', {
          method: 'DELETE',
          headers: {},
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
        expect(result).toEqual({ deletedCount: 5, partial: false })
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
        expect(result).toEqual({ deletedCount: 2, partial: true })
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
    })
  })

  describe('URL helpers', () => {
    describe('getDownloadUrl', () => {
      it('generates correct download URL', () => {
        expect(getDownloadUrl('/docs/file.pdf')).toBe('/api/v1/download/docs/file.pdf')
      })

      it('handles special characters', () => {
        expect(getDownloadUrl('/文档/测试.txt')).toBe('/api/v1/download/%E6%96%87%E6%A1%A3/%E6%B5%8B%E8%AF%95.txt')
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
          headers: { Authorization: 'Bearer test-token' },
        })
        expect(clickSpy).toHaveBeenCalled()
        expect(createObjectURLSpy).toHaveBeenCalledWith(blob)
        expect(revokeObjectURLSpy).toHaveBeenCalledWith('blob:test')
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

      it('surfaces structured backend errors', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: false,
          status: 409,
          statusText: 'Conflict',
          json: () => Promise.resolve({ error: { message: '父路径不是目录' } }),
        })

        await expect(downloadFile('/docs/report.txt')).rejects.toThrow('父路径不是目录')
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

      it('surfaces structured backend errors', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: false,
          status: 403,
          statusText: 'Forbidden',
          json: () => Promise.resolve({ error: { message: 'admin access required' } }),
        })

        await expect(downloadDiagnosticsExport()).rejects.toThrow('admin access required')
      })
    })

    describe('getThumbnailUrl', () => {
      it('generates thumbnail URL with default size', () => {
        expect(getThumbnailUrl('/photo.jpg')).toBe('/api/v1/thumbnails/photo.jpg?size=medium')
      })

      it('respects size parameter', () => {
        expect(getThumbnailUrl('/photo.jpg', 'small')).toBe('/api/v1/thumbnails/photo.jpg?size=small')
        expect(getThumbnailUrl('/photo.jpg', 'large')).toBe('/api/v1/thumbnails/photo.jpg?size=large')
      })

      it('does not add auth query when token exists', () => {
        localStorage.setItem('mnemonas_token', 'test-token')
      expect(getThumbnailUrl('/photo.jpg')).toBe('/api/v1/thumbnails/photo.jpg?size=medium')
      })
    })
  })

  describe('getDiagnostics', () => {
    it('fetches diagnostics info', async () => {
      const mockResponse = {
        success: true,
        data: {
          timestamp: '2024-01-15T10:00:00Z',
          uptime: '1h30m',
          uptime_secs: 5400,
          version: { name: 'MnemoNAS', version: '0.1.0', go: '1.21' },
          system: {
            filesystem_initialized: true,
            dataplane_connected: true,
            thumbnail_service_ready: false,
            maintenance_history_ready: true,
            activity_log_ready: true,
            favorites_store_ready: true,
          },
          memory: {
            alloc_mb: 50,
            total_alloc_mb: 100,
            sys_mb: 200,
            num_gc: 10,
          },
          goroutines: 25,
          filesystem: { trash_items: 5, trash_size: 1024 },
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
      expect(result.system?.filesystemInitialized).toBe(true)
      expect(result.system?.maintenanceHistoryReady).toBe(true)
      expect(result.system?.activityLogReady).toBe(true)
        expect(result.system?.favoritesStoreReady).toBe(true)
      expect(result.memory?.allocMb).toBe(50)
      expect(result.goroutines).toBe(25)
      expect(result.filesystem?.trashItems).toBe(5)
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
      expect(result.memory?.allocMb).toBeUndefined()
      expect(result.filesystem?.trashItems).toBeUndefined()
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
