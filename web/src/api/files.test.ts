import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  listFiles,
  getVersions,
  deleteFile,
  getStorageStats,
  getHealth,
  createDirectory,
  moveFile,
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
} from './files'

// Type declaration for global (Node.js environment in Vitest)
declare const global: typeof globalThis & { fetch: typeof fetch }

// Mock fetch globally
const mockFetch = vi.fn()
global.fetch = mockFetch

describe('API: files', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.removeItem('mnemonas_token')
  })

  afterEach(() => {
    vi.restoreAllMocks()
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
      expect(mockFetch).toHaveBeenCalledWith('/api/v1/files/', {
        headers: {},
      })
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
      expect(mockFetch).toHaveBeenCalledWith('/api/v1/files/documents', {
        headers: {},
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
  })

  describe('deleteFile', () => {
    it('deletes a file', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
      })

      await expect(deleteFile('/test.txt')).resolves.toBeUndefined()
      expect(mockFetch).toHaveBeenCalledWith('/api/v1/files/test.txt', {
        method: 'DELETE',
        headers: {},
      })
    })

    it('throws ApiError on failure', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: false,
        status: 403,
        statusText: 'Forbidden',
      })

      await expect(deleteFile('/protected.txt')).rejects.toThrow(ApiError)
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
  })

  describe('getHealth', () => {
    it('fetches health status', async () => {
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
      expect(result.status).toBe('healthy')
      expect(result.storage.writable).toBe(true)
    })
  })

  describe('createDirectory', () => {
    it('creates a directory', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
      })

      await expect(createDirectory('/new-folder')).resolves.toBeUndefined()
      expect(mockFetch).toHaveBeenCalledWith('/api/v1/directories/new-folder', {
        method: 'POST',
        headers: {},
      })
    })
  })

  describe('moveFile', () => {
    it('moves/renames a file', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
      })

      await expect(moveFile('/old.txt', '/new.txt')).resolves.toBeUndefined()
      expect(mockFetch).toHaveBeenCalledWith('/api/v1/files-move', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ from: '/old.txt', to: '/new.txt' }),
      })
    })
  })

  describe('restoreVersion', () => {
    it('restores a file to specific version', async () => {
      mockFetch.mockResolvedValueOnce({
        ok: true,
      })

      await expect(restoreVersion('/test.txt', 'abc123')).resolves.toBeUndefined()
      expect(mockFetch).toHaveBeenCalledWith(
        '/api/v1/versions/abc123/restore?path=%2Ftest.txt',
        { method: 'POST', headers: {} }
      )
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
    })

    describe('restoreFromTrash', () => {
      it('restores item from trash', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
        })

        await expect(restoreFromTrash('item1')).resolves.toBeUndefined()
        expect(mockFetch).toHaveBeenCalledWith('/api/v1/trash/item1/restore', {
          method: 'POST',
          headers: {},
        })
      })

      it('restores to custom path', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
        })

        await restoreFromTrash('item1', '/new-location/file.txt')
        expect(mockFetch).toHaveBeenCalledWith(
          '/api/v1/trash/item1/restore?path=%2Fnew-location%2Ffile.txt',
          { method: 'POST', headers: {} }
        )
      })
    })

    describe('deleteFromTrash', () => {
      it('permanently deletes item', async () => {
        mockFetch.mockResolvedValueOnce({
          ok: true,
        })

        await expect(deleteFromTrash('item1')).resolves.toBeUndefined()
        expect(mockFetch).toHaveBeenCalledWith('/api/v1/trash/item1', {
          method: 'DELETE',
          headers: {},
        })
      })
    })

    describe('emptyTrash', () => {
      it('empties trash and returns count', async () => {
        const mockResponse = {
          success: true,
          data: { deleted_count: 5 },
          timestamp: '2024-01-01',
        }

        mockFetch.mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve(mockResponse),
        })

        const result = await emptyTrash()
        expect(result).toBe(5)
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

      it('adds auth query when token exists', () => {
        localStorage.setItem('mnemonas_token', 'test-token')
        expect(getDownloadUrl('/docs/file.pdf')).toBe('/api/v1/download/docs/file.pdf?auth=test-token')
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

        expect(mockFetch).toHaveBeenCalledWith('/api/v1/download/docs/report.txt?download=true', expect.objectContaining({
          headers: { Authorization: 'Bearer test-token' },
        }))
        expect(clickSpy).toHaveBeenCalled()
        expect(createObjectURLSpy).toHaveBeenCalledWith(blob)
        expect(revokeObjectURLSpy).toHaveBeenCalledWith('blob:test')
      })

      it('uses fallback filename when header is missing', async () => {
        const blob = new Blob(['file-content'])
        let createdLink: HTMLAnchorElement | null = null
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
    })

    describe('getThumbnailUrl', () => {
      it('generates thumbnail URL with default size', () => {
        expect(getThumbnailUrl('/photo.jpg')).toBe('/api/v1/thumbnails/photo.jpg?size=medium')
      })

      it('respects size parameter', () => {
        expect(getThumbnailUrl('/photo.jpg', 'small')).toBe('/api/v1/thumbnails/photo.jpg?size=small')
        expect(getThumbnailUrl('/photo.jpg', 'large')).toBe('/api/v1/thumbnails/photo.jpg?size=large')
      })

      it('adds auth query when token exists', () => {
        localStorage.setItem('mnemonas_token', 'test-token')
        expect(getThumbnailUrl('/photo.jpg')).toBe('/api/v1/thumbnails/photo.jpg?size=medium&auth=test-token')
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
      expect(result.system.filesystemInitialized).toBe(true)
      expect(result.memory.allocMb).toBe(50)
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
      expect(result.uptimeSecs).toBe(0)
      expect(result.goroutines).toBe(0)
      expect(result.filesystem).toBeUndefined()
      expect(result.storage).toBeUndefined()
      expect(result.dataplane).toBeUndefined()
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
  })

  describe('runScrub', () => {
    it('runs full scrub without specific hashes', async () => {
      const mockResponse = {
        success: true,
        data: {
          id: 'scrub-456',
          status: 'running',
          message: 'Scrub started',
        },
        timestamp: '2024-01-01',
      }

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      })

      const result = await runScrub()
      expect(result.has_result).toBe(true)
      expect(result.status).toBe('running')
    })

    it('runs scrub for specific hashes', async () => {
      const mockResponse = {
        success: true,
        data: {
          id: 'scrub-789',
          status: 'running',
        },
        timestamp: '2024-01-01',
      }

      mockFetch.mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve(mockResponse),
      })

      const hashes = ['hash1', 'hash2']
      await runScrub(hashes)
      
      expect(mockFetch).toHaveBeenCalledWith('/api/v1/maintenance/scrub', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ hashes }),
      })
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
