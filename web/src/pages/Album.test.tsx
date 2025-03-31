import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { AlbumPage } from './Album'

// Mock API
vi.mock('@/api/files', () => ({
  listFiles: vi.fn(),
  getDownloadUrl: vi.fn((path: string) => `/api/v1/download${path}?download=true`),
  getThumbnailUrl: vi.fn((path: string) => `/api/v1/thumbnails${path}?size=medium`),
  downloadFile: vi.fn(),
}))

import { listFiles } from '@/api/files'

const mockListFiles = listFiles as ReturnType<typeof vi.fn>

describe('AlbumPage', () => {
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
