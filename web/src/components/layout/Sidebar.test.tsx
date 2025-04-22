import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { Sidebar } from './Sidebar'
import * as HeroUI from '@heroui/react'

const useIsAdminMock = vi.fn(() => true)
const mockGetStorageStats = vi.fn()
const mockAddToast = vi.fn()

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
  getStorageStats: (...args: unknown[]) => mockGetStorageStats(...args),
}))

import { ApiError } from '@/api/files'

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useIsAdmin: () => useIsAdminMock(),
  }
})

describe('Sidebar', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
    useIsAdminMock.mockReturnValue(true)
    mockGetStorageStats.mockResolvedValue({
      totalSize: 1024,
      dedupRatio: 0.25,
    })
  })

  describe('rendering', () => {
    it('renders logo', () => {
      render(<Sidebar />)
      expect(screen.getByText('MnemoNAS')).toBeTruthy()
      expect(screen.getByText('Memory Palace')).toBeTruthy()
    })

    it('renders navigation sections', () => {
      render(<Sidebar />)
      expect(screen.getByText('浏览')).toBeTruthy()
      expect(screen.getByText('管理')).toBeTruthy()
      expect(screen.getByText('系统')).toBeTruthy()
    })
  })

  describe('navigation items', () => {
    it('renders files link', () => {
      render(<Sidebar />)
      expect(screen.getByText('文件')).toBeTruthy()
    })

    it('renders album link', () => {
      render(<Sidebar />)
      expect(screen.getByText('相册')).toBeTruthy()
    })

    it('renders search link', () => {
      render(<Sidebar />)
      expect(screen.getByText('搜索')).toBeTruthy()
    })

    it('renders versions link with badge', () => {
      render(<Sidebar />)
      expect(screen.getByText('时光回溯')).toBeTruthy()
      expect(screen.getByText('核心')).toBeTruthy()
    })

    it('renders trash link', () => {
      render(<Sidebar />)
      expect(screen.getByText('回收站')).toBeTruthy()
    })

    it('renders storage link', () => {
      render(<Sidebar />)
      expect(screen.getByText('存储')).toBeTruthy()
    })

    it('renders maintenance link', () => {
      render(<Sidebar />)
      expect(screen.getByText('守护')).toBeTruthy()
    })

    it('renders health link', () => {
      render(<Sidebar />)
      expect(screen.getByText('健康')).toBeTruthy()
    })

    it('renders settings link', () => {
      render(<Sidebar />)
      expect(screen.getByText('设置')).toBeTruthy()
    })
  })

  describe('navigation links', () => {
    it('has correct href for files', () => {
      render(<Sidebar />)
      const link = screen.getByText('文件').closest('a')
      expect(link).toHaveAttribute('href', '/files')
    })

    it('has correct href for album', () => {
      render(<Sidebar />)
      const link = screen.getByText('相册').closest('a')
      expect(link).toHaveAttribute('href', '/album')
    })

    it('has correct href for search', () => {
      render(<Sidebar />)
      const link = screen.getByText('搜索').closest('a')
      expect(link).toHaveAttribute('href', '/search')
    })

    it('has correct href for versions', () => {
      render(<Sidebar />)
      const link = screen.getByText('时光回溯').closest('a')
      expect(link).toHaveAttribute('href', '/versions')
    })

    it('has correct href for trash', () => {
      render(<Sidebar />)
      const link = screen.getByText('回收站').closest('a')
      expect(link).toHaveAttribute('href', '/trash')
    })

    it('has correct href for storage', () => {
      render(<Sidebar />)
      const link = screen.getByText('存储').closest('a')
      expect(link).toHaveAttribute('href', '/storage')
    })

    it('has correct href for maintenance', () => {
      render(<Sidebar />)
      const link = screen.getByText('守护').closest('a')
      expect(link).toHaveAttribute('href', '/maintenance')
    })

    it('has correct href for health', () => {
      render(<Sidebar />)
      const link = screen.getByText('健康').closest('a')
      expect(link).toHaveAttribute('href', '/system-health')
    })

    it('has correct href for settings', () => {
      render(<Sidebar />)
      const link = screen.getByText('设置').closest('a')
      expect(link).toHaveAttribute('href', '/settings')
    })
  })

  describe('storage status', () => {
    it('renders storage usage section', () => {
      render(<Sidebar />)
      expect(screen.getByText('存储空间')).toBeTruthy()
    })

    it('renders progress bar', () => {
      render(<Sidebar />)
      const storageSection = screen.getByText('存储空间').closest('div')?.parentElement
      const progressBar = storageSection?.querySelector('div.bg-accent-primary')
      expect(progressBar).toBeTruthy()
    })

    it('shows a retryable error state when storage stats fail to load', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetStorageStats
        .mockRejectedValueOnce(new Error('stats unavailable'))
        .mockResolvedValueOnce({
          totalSize: 2048,
          dedupRatio: 0.1,
        })

      render(<Sidebar />)

      expect(await screen.findByText('统计加载失败')).toBeTruthy()
      expect(screen.getByText('stats unavailable')).toBeTruthy()

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      expect(await screen.findByText('2 KB')).toBeTruthy()
      expect(mockAddToast).toHaveBeenCalledWith({ title: '存储统计已刷新', color: 'success' })
    })

    it('shows warning toast when reloading storage stats is temporarily unavailable', async () => {
      const user = userEvent.setup({ writeToClipboard: false })
      mockGetStorageStats
        .mockRejectedValueOnce(new Error('stats unavailable'))
        .mockRejectedValueOnce(new ApiError('stats unavailable', 503, 'SERVICE_UNAVAILABLE'))

      render(<Sidebar />)

      expect(await screen.findByText('统计加载失败')).toBeTruthy()

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      expect(mockAddToast).toHaveBeenCalledWith({
        title: '统计暂不可用',
        description: '存储统计服务当前不可用。',
        color: 'warning',
      })
    })

    it('shows an unavailable storage state when stats service returns 503', async () => {
      mockGetStorageStats.mockRejectedValueOnce(new ApiError('stats unavailable', 503, 'SERVICE_UNAVAILABLE'))

      render(<Sidebar />)

      expect(await screen.findByText('统计暂不可用')).toBeTruthy()
      expect(screen.getByText('存储统计服务当前不可用。')).toBeTruthy()
    })

    it('shows unknown storage values when stats payload is incomplete', async () => {
      mockGetStorageStats.mockResolvedValueOnce({})

      render(<Sidebar />)

      expect(await screen.findByText('统计不可用')).toBeTruthy()
      expect(screen.getByText('--')).toBeTruthy()
      expect(screen.queryByText(/去重率/)).toBeFalsy()
    })
  })

  describe('collapsed mode', () => {
    it('hides text when collapsed', () => {
      render(<Sidebar collapsed={true} />)
      // Text like "Memory Palace" should not be visible
      expect(screen.queryByText('Memory Palace')).toBeFalsy()
    })

    it('hides section titles when collapsed', () => {
      render(<Sidebar collapsed={true} />)
      expect(screen.queryByText('浏览')).toBeFalsy()
      expect(screen.queryByText('管理')).toBeFalsy()
      expect(screen.queryByText('系统')).toBeFalsy()
    })

    it('hides storage status when collapsed', () => {
      render(<Sidebar collapsed={true} />)
      expect(screen.queryByText('存储空间')).toBeFalsy()
    })
  })

  it('hides admin-only navigation for non-admin users', () => {
    useIsAdminMock.mockReturnValue(false)
    render(<Sidebar />)

    expect(screen.queryByText('守护')).toBeFalsy()
    expect(screen.queryByText('用户')).toBeFalsy()
    expect(screen.queryByText('健康')).toBeFalsy()
    expect(screen.queryByText('设置')).toBeFalsy()
    expect(screen.getByText('活动')).toBeTruthy()
  })
})
