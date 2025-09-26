import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, render, screen, waitFor, within } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import * as HeroUI from '@heroui/react'

const useIsAdminMock = vi.fn(() => true)
const mockAddToast = vi.fn()

const { mockNavigate, mockUser } = vi.hoisted(() => ({
  mockNavigate: vi.fn(),
  mockUser: { id: 'u1', username: 'admin', role: 'admin' as const, email: 'admin@local', homeDir: '/' },
}))

vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

import { StoragePage } from './Storage'

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
  getStorageStats: vi.fn(),
}))

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useUser: () => mockUser,
    useIsAdmin: () => useIsAdminMock(),
  }
})

import { ApiError, getStorageStats } from '@/api/files'

const mockGetStorageStats = getStorageStats as ReturnType<typeof vi.fn>

const originalClipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard')

function expectCalledWithAbortSignal(mockFn: ReturnType<typeof vi.fn>) {
  const call = mockFn.mock.calls.find(([options]) => {
    return (options as { signal?: AbortSignal } | undefined)?.signal instanceof AbortSignal
  })
  expect(call).toBeTruthy()
  expect(Object.keys((call?.[0] ?? {}) as Record<string, unknown>).sort()).toEqual(['signal'])
}

describe('StoragePage', () => {
  const mockStats = {
    totalObjects: 1234,
    totalSize: 5368709120, // 5 GB
    dedupRatio: 0.35,
    storageStatsAvailable: true,
    diskStatsAvailable: true,
    directoryQuotaStatsAvailable: true,
    directoryQuotas: [],
    diskTotal: 21474836480, // 20 GB
    diskAvailable: 16106127360, // 15 GB
    diskUsed: 5368709120, // 5 GB
    diskUsageRatio: 0.25,
    diskFilesystemType: 'zfs',
    diskMountPoint: '/srv/mnemonas',
    diskMountSource: 'tank/mnemonas',
    diskMountOptions: 'rw,relatime',
    diskNativeDataChecksumSupport: true,
  }

  beforeEach(() => {
    vi.clearAllMocks()
    mockUser.id = 'u1'
    mockUser.username = 'admin'
    mockUser.role = 'admin'
    mockUser.email = 'admin@local'
    mockUser.homeDir = '/'
    vi.spyOn(HeroUI, 'addToast').mockImplementation(((...args: unknown[]) => mockAddToast(...args)) as typeof HeroUI.addToast)
    useIsAdminMock.mockReturnValue(true)
    mockNavigate.mockClear()
    mockGetStorageStats.mockResolvedValue(mockStats)
  })

  afterEach(() => {
    vi.useRealTimers()
    if (originalClipboardDescriptor) {
      Object.defineProperty(navigator, 'clipboard', originalClipboardDescriptor)
    } else {
      Reflect.deleteProperty(navigator, 'clipboard')
    }
  })

  describe('loading state', () => {
    it('shows loading skeleton initially', () => {
      mockGetStorageStats.mockImplementation(() => new Promise(() => {}))
      render(<StoragePage />)

      expect(screen.getByRole('status', { name: '加载空间与存储' })).toBeInTheDocument()
      expect(screen.queryByRole('heading', { name: '空间与存储' })).not.toBeInTheDocument()
    })
  })

  describe('header', () => {
    it('passes abort signals to the storage stats query', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expectCalledWithAbortSignal(mockGetStorageStats)
      })
    })

    it('displays page title', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('空间与存储')).toBeTruthy()
      })
    })

    it('displays subtitle', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('文件占用、版本对象和目录配额')).toBeTruthy()
      })
    })

    it('renders refresh button', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('刷新')).toBeTruthy()
      })
    })

    it('calls refetch on refresh button click', async () => {
      const user = userEvent.setup()
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('刷新')).toBeTruthy()
      })

      await user.click(screen.getByText('刷新'))

      // First call on mount, second on refresh
      await waitFor(() => {
        expect(mockGetStorageStats.mock.calls.length).toBeGreaterThanOrEqual(1)
      })
    })

    it('auto-refreshes storage stats every 30 seconds', async () => {
      vi.useFakeTimers()

      const flushUi = async () => {
        await act(async () => {
          await Promise.resolve()
          await Promise.resolve()
        })
        act(() => {
          vi.advanceTimersByTime(0)
        })
      }

      render(<StoragePage />)

      await flushUi()
      expect(mockGetStorageStats).toHaveBeenCalledTimes(1)

      await act(async () => {
        await vi.advanceTimersByTimeAsync(30000)
      })

      await flushUi()
      expect(mockGetStorageStats).toHaveBeenCalledTimes(2)
    })

    it('refetches storage stats when the auth scope changes', async () => {
    mockGetStorageStats
      .mockResolvedValueOnce(mockStats)
      .mockResolvedValueOnce({
        ...mockStats,
        totalObjects: 2048,
      })

    const { rerender } = render(<StoragePage />)

    await waitFor(() => {
      expect(mockGetStorageStats).toHaveBeenCalledTimes(1)
    })

    mockUser.id = 'u2'
    mockUser.username = 'other-admin'
    mockUser.email = 'other@local'
    mockUser.role = 'admin'
    mockUser.homeDir = '/'

    rerender(<StoragePage />)

    await waitFor(() => {
      expect(mockGetStorageStats).toHaveBeenCalledTimes(2)
    })
    })
  })

  describe('storage overview', () => {
    it('displays storage usage section', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('存储空间使用情况')).toBeTruthy()
      })
    })

    it('displays formatted storage size', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText(/5.*GB.*已使用/)).toBeTruthy()
      })
    })

    it('shows progress bar', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        const progressBar = screen.getByRole('progressbar', { name: '存储使用率' })
        expect(progressBar).toBeTruthy()
        expect(progressBar).toHaveAttribute('aria-valuenow', '25')
        expect(progressBar).toHaveAttribute('aria-valuetext', '25.0% 已用')
      })
    })

    it('displays the storage mount point and source', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('挂载点')).toBeTruthy()
        expect(screen.getByText('/srv/mnemonas')).toBeTruthy()
        expect(screen.getByText('存储源')).toBeTruthy()
        expect(screen.getByText('tank/mnemonas')).toBeTruthy()
      })
    })

    it('warns when disk space is below the safe operating margin', async () => {
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        diskAvailable: 512 * 1024 * 1024,
        diskUsed: 20937965568,
        diskUsageRatio: 0.975,
      })

      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByText('存储空间严重不足').length).toBeGreaterThanOrEqual(1)
        expect(screen.getAllByText(/尽快清理回收站/).length).toBeGreaterThanOrEqual(1)
      })
    })

    it('shows a warning-level disk space alert before capacity becomes critical', async () => {
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        diskAvailable: 5 * 1024 * 1024 * 1024,
        diskUsed: 15 * 1024 * 1024 * 1024,
        diskUsageRatio: 0.91,
      })

      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByText('存储空间偏紧').length).toBeGreaterThanOrEqual(1)
        expect(screen.getAllByText(/建议开启提醒/).length).toBeGreaterThanOrEqual(1)
      })
    })

    it('summarizes storage risks for administrator review', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        const summary = within(screen.getByLabelText('存储健康摘要'))
        expect(summary.getByText('需要复核')).toBeTruthy()
        expect(summary.getByText('未配置目录配额')).toBeTruthy()
        expect(summary.getAllByText(/为家庭共享、媒体库或团队目录配置明确上限/).length).toBeGreaterThanOrEqual(2)
        expect(summary.getByText('建议处理：为家庭共享、媒体库或团队目录配置明确上限')).toBeTruthy()
        expect(summary.getByText('容量：空间充足')).toBeTruthy()
        expect(summary.getByText('校验：已支持')).toBeTruthy()
      })
    })

    it('surfaces critical storage risk when capacity and quota boundaries fail', async () => {
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        diskAvailable: 512 * 1024 * 1024,
        diskUsed: 20937965568,
        diskUsageRatio: 0.975,
        diskFilesystemType: 'ext4',
        diskNativeDataChecksumSupport: false,
        directoryQuotaStatsAvailable: true,
        directoryQuotas: [
          {
            path: '/media',
            quotaBytes: 1073741824,
            usedBytes: 1610612736,
            availableBytes: 0,
            usageRatio: 1.5,
            exists: true,
            status: 'exceeded',
          },
          {
            path: '/archive',
            quotaBytes: 1073741824,
            usedBytes: 1046898278,
            availableBytes: 1678546,
            usageRatio: 0.975,
            exists: true,
            status: 'warning',
          },
        ],
      })

      render(<StoragePage />)

      await waitFor(() => {
        const summary = within(screen.getByLabelText('存储健康摘要'))
        expect(summary.getByText('需立即处理')).toBeTruthy()
        expect(summary.getByText('存储空间严重不足')).toBeTruthy()
        expect(summary.getByText('建议使用 ZFS/Btrfs')).toBeTruthy()
        expect(summary.getByText('1 个目录已达配额上限')).toBeTruthy()
        expect(summary.getByText('1 个目录接近配额上限')).toBeTruthy()
        expect(summary.getByText('建议处理：先清理回收站和临时数据，再安排扩容或迁移；保留独立备份，并定期运行完整性检查 等 4 步')).toBeTruthy()
        expect(summary.getByText('配额异常：2 个')).toBeTruthy()
      })
    })
  })

  describe('stats cards', () => {
    it('displays total objects count', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('对象总数')).toBeTruthy()
        expect(screen.getByText('1,234')).toBeTruthy()
      })
    })

    it('displays disk capacity and available space', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('磁盘容量')).toBeTruthy()
        expect(screen.getByText('可用空间')).toBeTruthy()
        expect(screen.getByText('磁盘占用')).toBeTruthy()
        expect(screen.getByText('25.0%')).toBeTruthy()
      })
    })

    it('displays filesystem type', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('文件系统')).toBeTruthy()
        expect(screen.getByText('ZFS')).toBeTruthy()
      })
    })

    it('displays storage backing integrity and mount options for admins', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('数据校验能力')).toBeTruthy()
        expect(screen.getByText('原生数据校验支持')).toBeTruthy()
        expect(screen.getByText('挂载选项')).toBeTruthy()
        expect(screen.getByText('rw,relatime')).toBeTruthy()
      })
    })

    it('copies the storage backing summary for admins', async () => {
      const user = userEvent.setup()
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })

      render(<StoragePage />)

      const copyButton = await screen.findByRole('button', { name: '复制存储摘要' })
      await user.click(copyButton)

      await waitFor(() => {
        expect(writeText).toHaveBeenCalledTimes(1)
      })
      const summary = writeText.mock.calls[0]?.[0] as string
      expect(summary).toContain('文件系统: ZFS')
      expect(summary).toContain('挂载点: /srv/mnemonas')
      expect(summary).toContain('存储源: tank/mnemonas')
      expect(summary).toContain('挂载选项: rw,relatime')
      expect(summary).toContain('数据校验能力: 原生数据校验支持')
      expect(mockAddToast).toHaveBeenCalledWith({ title: '存储摘要已复制', color: 'success' })
    })

    it('shows a warning when storage backing summary cannot be copied', async () => {
      const user = userEvent.setup()
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: undefined,
      })

      render(<StoragePage />)

      const copyButton = await screen.findByRole('button', { name: '复制存储摘要' })
      await user.click(copyButton)

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '无法复制存储摘要',
          description: '当前浏览器不支持剪贴板写入。',
          color: 'warning',
        })
      })
    })

    it('uses a stable message when clipboard write fails', async () => {
      const user = userEvent.setup()
      const writeText = vi.fn().mockRejectedValue(new Error('Document is not focused'))
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })

      render(<StoragePage />)

      const copyButton = await screen.findByRole('button', { name: '复制存储摘要' })
      await user.click(copyButton)

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '无法复制存储摘要',
          description: '请检查浏览器剪贴板权限。',
          color: 'danger',
        })
      })
      expect(mockAddToast).not.toHaveBeenCalledWith(expect.objectContaining({
        description: 'Document is not focused',
      }))
    })

    it('hides storage backing diagnostics from non-admin users', async () => {
      useIsAdminMock.mockReturnValue(false)

      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('空间与存储')).toBeTruthy()
      })
      expect(screen.queryByText('数据校验能力')).toBeNull()
      expect(screen.queryByText('挂载选项')).toBeNull()
      expect(screen.queryByText('存储健康摘要')).toBeNull()
      expect(screen.queryByRole('button', { name: '复制存储摘要' })).toBeNull()
    })

    it('warns when the storage backing lacks native data checksums', async () => {
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        diskFilesystemType: 'ext4',
        diskNativeDataChecksumSupport: false,
      })

      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByText('建议使用 ZFS/Btrfs').length).toBeGreaterThanOrEqual(1)
        expect(screen.getAllByText(/当前未检测到底层数据校验/).length).toBeGreaterThanOrEqual(1)
      })
    })

    it('displays CAS storage size', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('CAS 大小')).toBeTruthy()
      })
    })

    it('displays dedup ratio', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('去重率')).toBeTruthy()
        expect(screen.getByText('35.0%')).toBeTruthy()
      })
    })

    it('displays saved space', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('节省空间')).toBeTruthy()
      })
    })
  })

  describe('directory quotas', () => {
    it('shows an empty directory quota state for admins when no quotas are configured', async () => {
      const user = userEvent.setup()
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('目录配额')).toBeTruthy()
        expect(screen.getAllByText('未配置目录配额').length).toBeGreaterThanOrEqual(1)
        expect(screen.getByText(/避免单个目录持续占满存储空间/)).toBeTruthy()
        const quotaExample = screen.getByText((content, element) => (
          element?.tagName.toLowerCase() === 'code' && content.includes('/team 2 GB')
        ))
        expect(quotaExample.textContent).toContain('/media 512 MB')
      })

      await user.click(screen.getByRole('button', { name: '配置目录配额' }))

      expect(mockNavigate).toHaveBeenCalledWith('/settings?tab=retention')
    })

    it('renders directory quota usage summaries', async () => {
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        directoryQuotaStatsAvailable: true,
        directoryQuotas: [
          {
            path: '/team',
            quotaBytes: 2147483648,
            usedBytes: 1073741824,
            availableBytes: 1073741824,
            usageRatio: 0.5,
            exists: true,
            status: 'normal',
          },
          {
            path: '/archive',
            quotaBytes: 1073741824,
            usedBytes: 1046898278,
            availableBytes: 1678546,
            usageRatio: 0.975,
            exists: true,
            status: 'warning',
          },
          {
            path: '/missing',
            quotaBytes: 536870912,
            usedBytes: 0,
            availableBytes: 536870912,
            usageRatio: 0,
            exists: false,
            status: 'missing',
          },
        ],
      })

      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('/team')).toBeTruthy()
        expect(screen.getAllByText('/archive').length).toBeGreaterThanOrEqual(1)
        expect(screen.getAllByText('/missing').length).toBeGreaterThanOrEqual(1)
        const teamQuotaProgress = screen.getByRole('progressbar', { name: '/team 目录配额使用率' })
        expect(teamQuotaProgress).toHaveAttribute('aria-valuenow', '50')
        expect(teamQuotaProgress).toHaveAttribute('aria-valuetext', '50.0% 已用，剩余 1 GB，正常')
        const quotaSummary = within(screen.getByLabelText('目录配额汇总'))
        expect(quotaSummary.getByText('配额目录')).toBeTruthy()
        expect(quotaSummary.getByText('3 个')).toBeTruthy()
        expect(quotaSummary.getByText('正常 1 个')).toBeTruthy()
        expect(quotaSummary.getByText('总用量')).toBeTruthy()
        expect(quotaSummary.getByText('1.97 GB')).toBeTruthy()
        expect(quotaSummary.getByText('/ 3.5 GB · 56.4%')).toBeTruthy()
        expect(quotaSummary.getByText('建议复核增长较快目录')).toBeTruthy()
        expect(quotaSummary.getByText('接近上限 1 个 · 已超限 0 个 · 路径不存在 1 个')).toBeTruthy()
        const quotaAttention = within(screen.getByLabelText('目录配额关注清单'))
        expect(quotaAttention.getByText('目录配额关注清单')).toBeTruthy()
        expect(quotaAttention.getByText('显示 2 / 2 个需复核')).toBeTruthy()
        expect(quotaAttention.getByText('复核近期增长，并确认是否需要扩容或归档。')).toBeTruthy()
        expect(quotaAttention.getByText('创建目标目录，或删除不再使用的配额配置。')).toBeTruthy()
        expect(screen.getByText('正常')).toBeTruthy()
        expect(screen.getAllByText('接近上限').length).toBeGreaterThanOrEqual(1)
        expect(screen.getAllByText('目录未创建').length).toBeGreaterThanOrEqual(1)
        expect(screen.getByText('50.0%')).toBeTruthy()
        expect(screen.getAllByText('97.5%').length).toBeGreaterThanOrEqual(1)
        expect(screen.getByText('路径不存在')).toBeTruthy()
      })
    })

    it('copies a directory quota summary for administrator review', async () => {
      const user = userEvent.setup()
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        directoryQuotaStatsAvailable: true,
        directoryQuotas: [
          {
            path: '/team',
            quotaBytes: 2147483648,
            usedBytes: 1073741824,
            availableBytes: 1073741824,
            usageRatio: 0.5,
            exists: true,
            status: 'normal',
          },
          {
            path: '/archive',
            quotaBytes: 1073741824,
            usedBytes: 1046898278,
            availableBytes: 1678546,
            usageRatio: 0.975,
            exists: true,
            status: 'warning',
          },
          {
            path: '/missing',
            quotaBytes: 536870912,
            usedBytes: 0,
            availableBytes: 536870912,
            usageRatio: 0,
            exists: false,
            status: 'missing',
          },
        ],
      })

      render(<StoragePage />)

      const copyButton = await screen.findByRole('button', { name: '复制配额摘要' })
      await user.click(copyButton)

      await waitFor(() => {
        expect(writeText).toHaveBeenCalledTimes(1)
      })
      const report = writeText.mock.calls[0]?.[0] as string
      expect(report).toContain('目录配额摘要')
      expect(report).toContain('配额目录：3 个')
      expect(report).toContain('需复核：2 个')
      expect(report).toContain('路径 | 状态 | 用量 | 剩余 | 占比 | 存在状态 | 建议处理')
      expect(report).toContain('/archive | 接近上限 | 998.4 MB / 1 GB | 1.6 MB | 97.5%')
      expect(report).toContain('/missing | 目录未创建 | 0 B / 512 MB | 512 MB | 0.0%')
      expect(report).toContain('创建目标目录，或删除不再使用的配额配置。')
      expect(mockAddToast).toHaveBeenCalledWith({ title: '目录配额摘要已复制', color: 'success' })
    })

    it('filters directory quota cards to attention items', async () => {
      const user = userEvent.setup()
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        directoryQuotaStatsAvailable: true,
        directoryQuotas: [
          {
            path: '/team',
            quotaBytes: 2147483648,
            usedBytes: 1073741824,
            availableBytes: 1073741824,
            usageRatio: 0.5,
            exists: true,
            status: 'normal',
          },
          {
            path: '/archive',
            quotaBytes: 1073741824,
            usedBytes: 1046898278,
            availableBytes: 1678546,
            usageRatio: 0.975,
            exists: true,
            status: 'warning',
          },
          {
            path: '/missing',
            quotaBytes: 536870912,
            usedBytes: 0,
            availableBytes: 536870912,
            usageRatio: 0,
            exists: false,
            status: 'missing',
          },
        ],
      })

      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('/team')).toBeTruthy()
        expect(screen.getByRole('group', { name: '目录配额筛选' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '配额关注' }))

      await waitFor(() => {
        expect(screen.queryByText('/team')).toBeNull()
        expect(screen.getAllByText('/archive').length).toBeGreaterThanOrEqual(1)
        expect(screen.getAllByText('/missing').length).toBeGreaterThanOrEqual(1)
      })

      await user.click(screen.getByRole('button', { name: '全部目录' }))

      await waitFor(() => {
        expect(screen.getByText('/team')).toBeTruthy()
      })
    })

    it('shows an empty state when the attention filter has no matching directory quota', async () => {
      const user = userEvent.setup()
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        directoryQuotaStatsAvailable: true,
        directoryQuotas: [
          {
            path: '/team',
            quotaBytes: 2147483648,
            usedBytes: 1073741824,
            availableBytes: 1073741824,
            usageRatio: 0.5,
            exists: true,
            status: 'normal',
          },
          {
            path: '/media',
            quotaBytes: 4294967296,
            usedBytes: 1073741824,
            availableBytes: 3221225472,
            usageRatio: 0.25,
            exists: true,
            status: 'normal',
          },
        ],
      })

      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('/team')).toBeTruthy()
        expect(screen.getByText('/media')).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '配额关注' }))

      await waitFor(() => {
        expect(screen.getByText('暂无配额关注目录')).toBeTruthy()
        expect(screen.getByText('所有已配置目录配额当前都处于正常范围。')).toBeTruthy()
        expect(screen.queryByText('/team')).toBeNull()
        expect(screen.queryByText('/media')).toBeNull()
      })
    })

    it('prioritizes exceeded directory quotas in the attention list', async () => {
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        directoryQuotaStatsAvailable: true,
        directoryQuotas: [
          {
            path: '/archive',
            quotaBytes: 1073741824,
            usedBytes: 1046898278,
            availableBytes: 1678546,
            usageRatio: 0.975,
            exists: true,
            status: 'warning',
          },
          {
            path: '/media',
            quotaBytes: 1073741824,
            usedBytes: 1610612736,
            availableBytes: 0,
            usageRatio: 1.5,
            exists: true,
            status: 'exceeded',
          },
          {
            path: '/missing',
            quotaBytes: 536870912,
            usedBytes: 0,
            availableBytes: 536870912,
            usageRatio: 0,
            exists: false,
            status: 'missing',
          },
        ],
      })

      render(<StoragePage />)

      await waitFor(() => {
        const quotaAttention = screen.getByLabelText('目录配额关注清单')
        expect(quotaAttention.textContent).toMatch(/已达上限[\s\S]*\/media[\s\S]*目录未创建[\s\S]*\/missing[\s\S]*接近上限[\s\S]*\/archive/)
        expect(within(quotaAttention).getByText('清理目录内容、提高配额，或迁移部分数据。')).toBeTruthy()
      })
    })

    it('shows how many attention items are currently visible when the list is truncated', async () => {
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        directoryQuotaStatsAvailable: true,
        directoryQuotas: [
          {
            path: '/media',
            quotaBytes: 1073741824,
            usedBytes: 1610612736,
            availableBytes: 0,
            usageRatio: 1.5,
            exists: true,
            status: 'exceeded',
          },
          {
            path: '/photos',
            quotaBytes: 1073741824,
            usedBytes: 1577058304,
            availableBytes: 0,
            usageRatio: 1.47,
            exists: true,
            status: 'exceeded',
          },
          {
            path: '/missing-a',
            quotaBytes: 536870912,
            usedBytes: 0,
            availableBytes: 536870912,
            usageRatio: 0,
            exists: false,
            status: 'missing',
          },
          {
            path: '/missing-b',
            quotaBytes: 536870912,
            usedBytes: 0,
            availableBytes: 536870912,
            usageRatio: 0,
            exists: false,
            status: 'missing',
          },
          {
            path: '/archive',
            quotaBytes: 1073741824,
            usedBytes: 1046898278,
            availableBytes: 1678546,
            usageRatio: 0.975,
            exists: true,
            status: 'warning',
          },
          {
            path: '/team',
            quotaBytes: 1073741824,
            usedBytes: 1009317315,
            availableBytes: 64466309,
            usageRatio: 0.94,
            exists: true,
            status: 'warning',
          },
        ],
      })

      render(<StoragePage />)

      await waitFor(() => {
        const quotaAttention = screen.getByLabelText('目录配额关注清单')
        expect(within(quotaAttention).getByText('显示 5 / 6 个需复核')).toBeTruthy()
        expect(within(quotaAttention).queryByText('/team')).toBeNull()
      })
    })

    it('shows quota stats unavailable state when collection fails', async () => {
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        directoryQuotaStatsAvailable: false,
      })

      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('目录配额统计暂不可用，请稍后刷新或检查存储状态。')).toBeTruthy()
      })
    })

    it('hides directory quota summaries from non-admin users', async () => {
      useIsAdminMock.mockReturnValue(false)

      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.queryByText('目录配额')).toBeNull()
      })
    })
  })

  describe('maintenance cards', () => {
    it('displays scrub card', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('完整性检查')).toBeTruthy()
        expect(screen.getByText('确认已存数据仍可正确读取')).toBeTruthy()
      })
    })

    it('displays GC card', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('清理历史对象')).toBeTruthy()
        expect(screen.getByText('清理不再被引用的版本数据')).toBeTruthy()
      })
    })

    it('renders scrub maintenance button', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByText('打开维护工具').length).toBeGreaterThan(0)
      })
    })

    it('renders GC maintenance button', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByText('打开维护工具').length).toBeGreaterThan(1)
      })
    })

    it('shows scrub execution context', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByText('在备份与维护中执行').length).toBeGreaterThan(0)
      })
    })

    it('shows GC execution context', async () => {
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByText(/支持/).length).toBeGreaterThan(1)
      })
    })

    it('disables maintenance actions for non-admin users', async () => {
      useIsAdminMock.mockReturnValue(false)
      render(<StoragePage />)

      await waitFor(() => {
        const buttons = screen.getAllByRole('button', { name: '仅管理员可用' })
        expect(buttons.length).toBe(2)
        buttons.forEach((button) => expect(button).toBeDisabled())
      })
    })

    it('navigates to maintenance from both admin maintenance actions', async () => {
      const user = userEvent.setup()
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByRole('button', { name: '打开维护工具' })).toHaveLength(2)
      })

      const buttons = screen.getAllByRole('button', { name: '打开维护工具' })
      await user.click(buttons[0])
      await user.click(buttons[1])

      expect(mockNavigate).toHaveBeenNthCalledWith(1, '/maintenance')
      expect(mockNavigate).toHaveBeenNthCalledWith(2, '/maintenance')
    })
  })

  describe('error handling', () => {
    it('shows an unavailable state when storage stats return service unavailable', async () => {
      mockGetStorageStats.mockRejectedValue(new ApiError('storage stats unavailable', 503, 'SERVICE_UNAVAILABLE'))
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('存储统计暂不可用')).toBeTruthy()
        expect(screen.getByText('存储统计服务当前不可用，请检查设备状态或稍后重试。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('shows retryable error state on stats fetch failure', async () => {
      mockGetStorageStats.mockRejectedValue(new Error('Network error'))
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('加载存储统计失败')).toBeTruthy()
        expect(screen.getByText('存储统计加载失败，请检查网络或稍后重试。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('shows success toast when storage reload succeeds', async () => {
      const user = userEvent.setup()
      mockGetStorageStats
        .mockRejectedValueOnce(new Error('Network error'))
        .mockResolvedValueOnce(mockStats)
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '存储统计已刷新', color: 'success' })
      })
    })

    it('shows warning toast when storage reload is temporarily unavailable', async () => {
      const user = userEvent.setup()
      mockGetStorageStats
        .mockRejectedValueOnce(new Error('Network error'))
        .mockRejectedValueOnce(new ApiError('storage stats unavailable', 503, 'SERVICE_UNAVAILABLE'))
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '存储统计暂不可用',
          description: '存储统计服务当前不可用，请检查设备状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('shows danger toast when storage reload fails with a generic error', async () => {
      const user = userEvent.setup()
      mockGetStorageStats
        .mockRejectedValueOnce(new Error('Network error'))
        .mockRejectedValueOnce(new Error('still offline'))
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '重新加载' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '加载存储统计失败',
          description: '操作未完成，请稍后重试。',
          color: 'danger',
        })
      })
    })

    it('handles empty stats', async () => {
      mockGetStorageStats.mockResolvedValue({})
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getByText('空间与存储')).toBeTruthy()
        expect(screen.getAllByText('统计不可用').length).toBeGreaterThan(0)
        expect(screen.getAllByText('--').length).toBeGreaterThan(0)
      })
    })

    it('treats explicit unavailable storage stats as unavailable even when numeric fields are present', async () => {
      mockGetStorageStats.mockResolvedValue({
        totalObjects: 999,
        totalSize: 1024,
        dedupRatio: 0.42,
        storageStatsAvailable: false,
      })
      render(<StoragePage />)

      await waitFor(() => {
        expect(screen.getAllByText('统计不可用').length).toBeGreaterThan(0)
        expect(screen.queryByText('999')).toBeNull()
        expect(screen.queryByText('42.0%')).toBeNull()
      })
    })
  })
})
