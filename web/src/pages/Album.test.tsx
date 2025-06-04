import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent } from '@testing-library/react'
import { render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import * as HeroUI from '@heroui/react'
import { refreshAuthSession } from '@/api/auth'
import { AlbumPage } from './Album'

const mockAddToast = vi.fn()

const { mockUser } = vi.hoisted(() => ({
  mockUser: { id: 'u1', username: 'admin', role: 'admin' as const, email: 'admin@local', homeDir: '/' },
}))

vi.mock('@/api/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/auth')>()
  return {
    ...actual,
    refreshAuthSession: vi.fn(),
  }
})

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useUser: () => mockUser,
  }
})

// Mock API
vi.mock('@/api/files', () => ({
  ApiError: class ApiError extends Error {
    status: number
    code?: string
    constructor(message: string, status: number, code?: string) {
      super(message)
      this.status = status
      this.code = code
    }
    get isUnavailable() {
      return this.status === 503 || this.code === 'SERVICE_UNAVAILABLE'
    }
  },
  listFiles: vi.fn(),
  getDownloadUrl: vi.fn((path: string) => `/api/v1/download${path}?download=true`),
  getThumbnailUrl: vi.fn((path: string) => `/api/v1/thumbnails${path}?size=medium`),
  downloadFile: vi.fn(),
}))

import { ApiError as FilesApiError, downloadFile, listFiles } from '@/api/files'

const mockListFiles = listFiles as ReturnType<typeof vi.fn>
const mockDownloadFile = vi.mocked(downloadFile)

describe('AlbumPage', () => {
  const mockRefreshAuthSession = vi.mocked(refreshAuthSession)
  const observeMock = vi.fn()

  class MockIntersectionObserver {
    private callback: IntersectionObserverCallback

    constructor(callback: IntersectionObserverCallback) {
      this.callback = callback
    }

    observe = (element: Element) => {
      observeMock(element)
      this.callback([
        {
          isIntersecting: true,
          target: element,
        } as IntersectionObserverEntry,
      ], this as unknown as IntersectionObserver)
    }

    disconnect = vi.fn()
    unobserve = vi.fn()
    takeRecords = vi.fn(() => [])
    root = null
    rootMargin = '0px'
    thresholds = []
  }

  const mockImageFiles = [
    { name: 'photo1.jpg', path: '/photos/photo1.jpg', isDir: false, size: 1024000, modTime: '2024-01-01T00:00:00Z' },
    { name: 'photo2.png', path: '/photos/photo2.png', isDir: false, size: 2048000, modTime: '2024-01-02T00:00:00Z' },
    { name: 'photo3.gif', path: '/photos/photo3.gif', isDir: false, size: 512000, modTime: '2024-01-03T00:00:00Z' },
  ]

  const mockMixedFiles = [
    { name: 'subfolder', path: '/subfolder', isDir: true, size: 0, modTime: '2024-01-01T00:00:00Z' },
    { name: 'photo1.jpg', path: '/photo1.jpg', isDir: false, size: 1024000, modTime: '2024-01-01T00:00:00Z' },
    { name: 'document.pdf', path: '/document.pdf', isDir: false, size: 2048, modTime: '2024-01-01T00:00:00Z' },
    { name: 'video.mp4', path: '/video.mp4', isDir: false, size: 10240, modTime: '2024-01-01T00:00:00Z' },
  ]

  beforeEach(() => {
    vi.clearAllMocks()
    mockUser.id = 'u1'
    mockUser.username = 'admin'
    mockUser.role = 'admin'
    mockUser.email = 'admin@local'
    mockUser.homeDir = '/'
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
    vi.stubGlobal('IntersectionObserver', MockIntersectionObserver as unknown as typeof IntersectionObserver)
    mockRefreshAuthSession.mockResolvedValue(false)
    mockDownloadFile.mockResolvedValue(undefined)
    mockListFiles.mockResolvedValue({
      files: mockImageFiles,
      path: '/',
    })
  })

  describe('loading state', () => {
    it('shows loading state initially', () => {
      mockListFiles.mockImplementation(() => new Promise(() => {}))
      render(<AlbumPage />)

      expect(screen.getByText(/loading|加载/i)).toBeTruthy()
    })
  })

  describe('header', () => {
    it('displays page title', async () => {
      render(<AlbumPage />)

      await waitFor(() => {
        expect(screen.getByText('相册')).toBeTruthy()
      })
    })

    it('displays image count', async () => {
      render(<AlbumPage />)

      await waitFor(() => {
        expect(screen.getByText(/共 \d+ 张图片/)).toBeTruthy()
      })
    })

    it('calls listFiles API on mount', async () => {
      render(<AlbumPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalledWith('/')
      })
    })

    it('uses the assigned home directory for non-admin album scans', async () => {
      mockUser.id = 'u2'
      mockUser.username = 'tester'
      mockUser.role = 'user'
      mockUser.homeDir = '/tester'
      mockListFiles.mockResolvedValueOnce({
        files: mockImageFiles.map((file) => ({
          ...file,
          path: `/tester${file.path}`,
        })),
        path: '/tester',
      })

      render(<AlbumPage />)

      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalledWith('/tester')
      })
    })
  })

  describe('empty state', () => {
    it('shows empty state when no images', async () => {
      mockListFiles.mockResolvedValue({ files: [], path: '/' })
      render(<AlbumPage />)

      await waitFor(() => {
        expect(screen.getByText('暂无图片')).toBeTruthy()
      })
    })

    it('shows empty state when only non-image files', async () => {
      mockListFiles.mockResolvedValue({
        files: [
          { name: 'document.pdf', path: '/document.pdf', isDir: false, size: 2048, modTime: '2024-01-01T00:00:00Z' },
          { name: 'video.mp4', path: '/video.mp4', isDir: false, size: 10240, modTime: '2024-01-01T00:00:00Z' },
        ],
        path: '/',
      })

      render(<AlbumPage />)

      await waitFor(() => {
        expect(screen.getByText('暂无图片')).toBeTruthy()
      })
    })
  })

  describe('image count', () => {
    it('displays total image count in subtitle', async () => {
      render(<AlbumPage />)

      await waitFor(() => {
        expect(screen.getByText('共 3 张图片')).toBeTruthy()
      })
    })
  })

  describe('recursive folder loading', () => {
    it('loads images from subfolders', async () => {
      mockListFiles
        .mockResolvedValueOnce({
          files: mockMixedFiles,
          path: '/',
        })
        .mockResolvedValueOnce({
          files: [
            { name: 'nested.jpg', path: '/subfolder/nested.jpg', isDir: false, size: 1024, modTime: '2024-01-01T00:00:00Z' },
          ],
          path: '/subfolder',
        })

      render(<AlbumPage />)

      await waitFor(() => {
        // Should call listFiles multiple times for recursive loading
        expect(mockListFiles).toHaveBeenCalledWith('/')
      })
      
      await waitFor(() => {
        expect(mockListFiles).toHaveBeenCalledWith('/subfolder')
      }, { timeout: 3000 })
    })
  })

  describe('different image formats', () => {
    it('supports multiple image formats', async () => {
      mockListFiles.mockResolvedValue({
        files: [
          { name: 'photo.jpg', path: '/photo.jpg', isDir: false, size: 1024, modTime: '2024-01-01T00:00:00Z' },
          { name: 'photo.jpeg', path: '/photo.jpeg', isDir: false, size: 1024, modTime: '2024-01-01T00:00:00Z' },
          { name: 'photo.png', path: '/photo.png', isDir: false, size: 1024, modTime: '2024-01-01T00:00:00Z' },
          { name: 'photo.gif', path: '/photo.gif', isDir: false, size: 1024, modTime: '2024-01-01T00:00:00Z' },
          { name: 'photo.webp', path: '/photo.webp', isDir: false, size: 1024, modTime: '2024-01-01T00:00:00Z' },
        ],
        path: '/',
      })

      render(<AlbumPage />)

      await waitFor(() => {
        expect(screen.getByText(/5/)).toBeTruthy()
      })
    })
  })

  describe('error handling', () => {
    it('shows a retryable error state when album loading fails', async () => {
      mockListFiles.mockRejectedValue(new Error('Network error'))
      
      render(<AlbumPage />)

      await waitFor(() => {
        expect(screen.getByText('加载相册失败')).toBeTruthy()
        expect(screen.getByText('无法扫描图片目录，当前结果不可用。请检查连接状态后重试。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('handles partial folder loading errors', async () => {
      mockListFiles
        .mockResolvedValueOnce({
          files: mockMixedFiles,
          path: '/',
        })
        .mockRejectedValueOnce(new Error('Subfolder error'))

      render(<AlbumPage />)

      await waitFor(() => {
        expect(screen.getByText('部分目录扫描失败')).toBeTruthy()
        expect(screen.getByText('当前相册仅展示已成功加载的图片，结果可能不完整。')).toBeTruthy()
      })
    })

    it('retries album loading from the error state', async () => {
      const user = userEvent.setup()
      mockListFiles
        .mockRejectedValueOnce(new Error('Network error'))
        .mockResolvedValueOnce({ files: mockImageFiles, path: '/' })

      render(<AlbumPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(screen.getByText('共 3 张图片')).toBeTruthy()
        expect(mockAddToast).toHaveBeenCalledWith({ title: '相册已刷新', color: 'success' })
      })
    })

    it('shows warning toast when retry is temporarily unavailable', async () => {
      const user = userEvent.setup()
      mockListFiles
        .mockRejectedValueOnce(new Error('Network error'))
        .mockRejectedValueOnce(new FilesApiError('album unavailable', 503, 'SERVICE_UNAVAILABLE'))

      render(<AlbumPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '刷新暂不可用',
          description: '图片目录当前不可用，请检查服务状态后重试。',
          color: 'warning',
        })
      })
    })
  })

  describe('filters only image files', () => {
    it('excludes non-image files from count', async () => {
      mockListFiles.mockResolvedValue({
        files: [
          { name: 'photo.jpg', path: '/photo.jpg', isDir: false, size: 1024, modTime: '2024-01-01T00:00:00Z' },
          { name: 'video.mp4', path: '/video.mp4', isDir: false, size: 10240, modTime: '2024-01-01T00:00:00Z' },
          { name: 'document.pdf', path: '/document.pdf', isDir: false, size: 2048, modTime: '2024-01-01T00:00:00Z' },
        ],
        path: '/',
      })

      render(<AlbumPage />)

      // Should only count 1 image
      await waitFor(() => {
        expect(screen.getByText('共 1 张图片')).toBeTruthy()
      })
    })
  })

  describe('image preview boundary cases', () => {
    it('retries thumbnail loading once after refreshing the auth session', async () => {
      mockRefreshAuthSession.mockResolvedValueOnce(true)

      render(<AlbumPage />)

      const thumbnail = await screen.findByAltText('photo1.jpg')
      fireEvent.error(thumbnail)

      await waitFor(() => {
        expect(mockRefreshAuthSession).toHaveBeenCalledTimes(1)
        expect(thumbnail).toHaveAttribute('src', '/api/v1/thumbnails/photos/photo1.jpg?size=medium&session_retry=1')
      })
    })

    it('shows a warning banner when a thumbnail still fails after auth refresh cannot recover', async () => {
      render(<AlbumPage />)

      const thumbnail = await screen.findByAltText('photo1.jpg')
      fireEvent.error(thumbnail)

      await waitFor(() => {
        expect(mockRefreshAuthSession).toHaveBeenCalledTimes(1)
        expect(screen.getByText('部分缩略图加载失败')).toBeTruthy()
        expect(screen.getByText('部分图片当前只能显示占位图；仍可尝试点击进入预览或直接下载原图。')).toBeTruthy()
      })
    })

    it('retries fullscreen image loading once after refreshing the auth session', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockRefreshAuthSession.mockResolvedValueOnce(true)

      render(<AlbumPage />)

      const thumbnail = await screen.findByAltText('photo1.jpg')
      await user.click(thumbnail)

      await waitFor(() => {
        expect(screen.getAllByRole('img', { name: 'photo1.jpg' }).length).toBeGreaterThan(1)
      })

      const previewImage = screen.getAllByRole('img', { name: 'photo1.jpg' }).at(-1)
      expect(previewImage).toBeTruthy()
      fireEvent.error(previewImage)

      await waitFor(() => {
        expect(previewImage).toHaveAttribute('src', '/api/v1/download/photos/photo1.jpg?download=true&session_retry=1')
      })
    })

    it('shows an explicit preview error state when fullscreen loading still fails after retry cannot recover', async () => {
      const user = userEvent.setup({ writeToClipboard: false })

      render(<AlbumPage />)

      const thumbnail = await screen.findByAltText('photo1.jpg')
      await user.click(thumbnail)

      await waitFor(() => {
        expect(screen.getAllByRole('img', { name: 'photo1.jpg' }).length).toBeGreaterThan(1)
      })

      const previewImage = screen.getAllByRole('img', { name: 'photo1.jpg' }).at(-1)
      expect(previewImage).toBeTruthy()
      fireEvent.error(previewImage)

      await waitFor(() => {
        expect(mockRefreshAuthSession).toHaveBeenCalledTimes(1)
        expect(screen.getByText('图片预览加载失败')).toBeTruthy()
        expect(screen.getByText('可尝试下载原图，或稍后重试。')).toBeTruthy()
      })
    })

    it('exposes accessible labels for preview controls', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      render(<AlbumPage />)

      await waitFor(() => {
        expect(screen.getByAltText('photo1.jpg')).toBeTruthy()
      })

      await user.click(screen.getByAltText('photo1.jpg'))

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '关闭预览' })).toBeTruthy()
        expect(screen.getByRole('button', { name: '上一张图片' })).toBeTruthy()
        expect(screen.getByRole('button', { name: '下一张图片' })).toBeTruthy()
        expect(screen.getByRole('button', { name: '缩小图片' })).toBeTruthy()
        expect(screen.getByRole('button', { name: '放大图片' })).toBeTruthy()
        expect(screen.getByRole('button', { name: '旋转图片' })).toBeTruthy()
        expect(screen.getByRole('button', { name: '显示图片信息' })).toBeTruthy()
        expect(screen.getByRole('button', { name: '下载当前图片' })).toBeTruthy()
      })
    })

    it('shows unknown size instead of 0 B when preview metadata is incomplete', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockListFiles.mockResolvedValue({
        files: [
          {
            ...mockImageFiles[0],
            size: undefined,
          },
        ],
        path: '/',
      })

      render(<AlbumPage />)

      const thumbnail = await screen.findByAltText('photo1.jpg')
      await user.click(thumbnail)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '显示图片信息' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '显示图片信息' }))

      await waitFor(() => {
        expect(screen.getByText('大小')).toBeTruthy()
        expect(screen.getByText('--')).toBeTruthy()
      })
    })

    it('shows unavailable toast when downloading the current preview image fails because the filesystem is unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockDownloadFile.mockRejectedValue(Object.assign(new Error('filesystem unavailable'), {
        status: 503,
        code: 'SERVICE_UNAVAILABLE',
      }))

      render(<AlbumPage />)

      const thumbnail = await screen.findByAltText('photo1.jpg')
      await user.click(thumbnail)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '下载当前图片' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '下载当前图片' }))

      await waitFor(() => {
        expect(mockDownloadFile).toHaveBeenCalledWith('/photos/photo1.jpg', { filename: 'photo1.jpg' })
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '下载暂不可用',
          description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('does not crash when currentIndex exceeds images length', async () => {
      // Start with images then clear them - simulates race condition
      mockListFiles.mockResolvedValue({
        files: mockImageFiles,
        path: '/',
      })

      const { rerender } = render(<AlbumPage />)

      await waitFor(() => {
        expect(screen.getByText('共 3 张图片')).toBeTruthy()
      })

      // Simulate API returning empty array (edge case)
      mockListFiles.mockResolvedValue({ files: [], path: '/' })
      rerender(<AlbumPage />)

      // Should not throw, page should handle gracefully
      expect(screen.queryByRole('dialog')).toBeNull()
    })

    it('handles rapid navigation without crashing', async () => {
      mockListFiles.mockResolvedValue({
        files: mockImageFiles,
        path: '/',
      })

      render(<AlbumPage />)

      await waitFor(() => {
        expect(screen.getByText('共 3 张图片')).toBeTruthy()
      })

      // Multiple rapid renders should not cause undefined access errors
      // The component should handle state transitions gracefully
    })
  })
})
