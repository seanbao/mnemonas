import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { HealthPage } from './Health'
import * as HeroUI from '@heroui/react'

const mockAddToast = vi.fn()

const { mockUser } = vi.hoisted(() => ({
  mockUser: { id: 'u1', username: 'admin', role: 'admin' as const, email: 'admin@local', homeDir: '/' },
}))

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
  getDiagnostics: vi.fn(),
  getStorageStats: vi.fn(),
}))

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useUser: () => mockUser,
  }
})

import { ApiError, getDiagnostics, getStorageStats } from '@/api/files'

const mockGetDiagnostics = getDiagnostics as ReturnType<typeof vi.fn>
const mockGetStorageStats = getStorageStats as ReturnType<typeof vi.fn>

describe('HealthPage', () => {
  const mockDiagnostics = {
    system: {
      filesystemInitialized: true,
      dataplaneConnected: true,
      thumbnailServiceReady: true,
      maintenanceHistoryReady: true,
      backupManagerReady: true,
      activityLogReady: true,
      favoritesStoreReady: true,
    },
    version: {
      name: 'MnemoNAS',
      version: '0.3.0',
      go: 'go1.25.9',
      buildTime: '2026-04-29T00:00:00Z',
    },
    uptimeSecs: 86400,
    memory: {
      allocMb: 50,
      totalAllocMb: 100,
      sysMb: 150,
      numGc: 10,
    },
    goroutines: 25,
    filesystem: {
      trashItems: 5,
      trashSize: 52428800,
    },
    dataplane: {
      healthy: true,
      version: '0.3.0',
      uptimeSec: 86000,
    },
  }

  const mockStats = {
    totalObjects: 1234,
    totalSize: 5368709120,
    dedupRatio: 0.35,
    storageStatsAvailable: true,
    diskStatsAvailable: true,
    diskTotal: 21474836480,
    diskAvailable: 16106127360,
    diskUsed: 5368709120,
    diskUsageRatio: 0.25,
    diskFilesystemType: 'zfs',
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
    mockGetDiagnostics.mockResolvedValue(mockDiagnostics)
    mockGetStorageStats.mockResolvedValue(mockStats)
  })

  describe('loading state', () => {
    it('shows refresh button with loading state', () => {
      mockGetDiagnostics.mockImplementation(() => new Promise(() => {}))
      mockGetStorageStats.mockImplementation(() => new Promise(() => {}))
      render(<HealthPage />)

      expect(screen.getByText('刷新')).toBeTruthy()
    })
  })

  describe('header', () => {
    it('displays page title', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('系统健康')).toBeTruthy()
      })
    })

    it('displays subtitle', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('监控系统状态和性能指标')).toBeTruthy()
      })
    })

    it('renders refresh button', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('刷新')).toBeTruthy()
      })
    })

    it('calls refetch on refresh button click', async () => {
      const user = userEvent.setup()
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('刷新')).toBeTruthy()
      })

      await user.click(screen.getByText('刷新'))

      await waitFor(() => {
        expect(mockGetDiagnostics.mock.calls.length).toBeGreaterThanOrEqual(2)
        expect(mockGetStorageStats.mock.calls.length).toBeGreaterThanOrEqual(2)
      })
    })

    it('shows success toast when refresh succeeds', async () => {
    const user = userEvent.setup()
    render(<HealthPage />)

    await waitFor(() => {
      expect(screen.getByText('刷新')).toBeTruthy()
    })

    await user.click(screen.getByText('刷新'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({ title: '健康数据已刷新', color: 'success' })
    })
    })

    it('shows warning toast when refresh is temporarily unavailable', async () => {
    const user = userEvent.setup()
    mockGetDiagnostics.mockResolvedValueOnce(mockDiagnostics)
    mockGetStorageStats.mockResolvedValueOnce(mockStats)
    mockGetDiagnostics.mockResolvedValueOnce(mockDiagnostics)
    mockGetStorageStats.mockRejectedValueOnce(new ApiError('health unavailable', 503, 'SERVICE_UNAVAILABLE'))

    render(<HealthPage />)

    await waitFor(() => {
      expect(screen.getByText('刷新')).toBeTruthy()
    })

    await user.click(screen.getByText('刷新'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '刷新暂不可用',
        description: '诊断或存储统计服务当前不可用，请检查系统状态或稍后重试。',
        color: 'warning',
      })
    })
    })

    it('shows danger toast when refresh fails with a generic error', async () => {
    const user = userEvent.setup()
    mockGetDiagnostics.mockResolvedValueOnce(mockDiagnostics)
    mockGetStorageStats.mockResolvedValueOnce(mockStats)
    mockGetDiagnostics.mockRejectedValueOnce(new Error('refresh failed'))
    mockGetStorageStats.mockResolvedValueOnce(mockStats)

    render(<HealthPage />)

    await waitFor(() => {
      expect(screen.getByText('刷新')).toBeTruthy()
    })

    await user.click(screen.getByText('刷新'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '刷新失败',
        description: 'refresh failed',
        color: 'danger',
      })
    })
    })

    it('refetches health queries when the auth scope changes', async () => {
      mockGetDiagnostics
        .mockResolvedValueOnce(mockDiagnostics)
        .mockResolvedValueOnce({
          ...mockDiagnostics,
          uptimeSecs: 172800,
        })
      mockGetStorageStats
        .mockResolvedValueOnce(mockStats)
        .mockResolvedValueOnce({
          ...mockStats,
          totalObjects: 2048,
        })

      const { rerender } = render(<HealthPage />)

      await waitFor(() => {
        expect(mockGetDiagnostics).toHaveBeenCalledTimes(1)
        expect(mockGetStorageStats).toHaveBeenCalledTimes(1)
      })

      mockUser.id = 'u2'
      mockUser.username = 'other-admin'
      mockUser.email = 'other@local'

      rerender(<HealthPage />)

      await waitFor(() => {
        expect(mockGetDiagnostics).toHaveBeenCalledTimes(2)
        expect(mockGetStorageStats).toHaveBeenCalledTimes(2)
      })
    })
  })

  describe('system status', () => {
    it('displays system status section', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('系统状态')).toBeTruthy()
      })
    })

    it('displays filesystem status', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('文件系统')).toBeTruthy()
      })
    })

    it('displays dataplane status', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('数据面')).toBeTruthy()
      })
    })

    it('displays thumbnail service status', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('缩略图服务')).toBeTruthy()
      })
    })

    it('displays maintenance history status', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('维护历史')).toBeTruthy()
      })
    })

    it('displays backup manager status when diagnostics provide it', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('备份管理')).toBeTruthy()
      })
    })

    it('displays activity log status', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('活动日志')).toBeTruthy()
      })
    })

    it('displays favorites store status when diagnostics provide it', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('收藏存储')).toBeTruthy()
      })
    })

    it('displays version info', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText(/MnemoNAS/)).toBeTruthy()
        expect(screen.getByText(/Go 1\.25\.9/)).toBeTruthy()
        expect(screen.getByText('构建 2026-04-29T00:00:00Z')).toBeTruthy()
      })
    })

    it('formats hour-level uptime and omits unknown build time', async () => {
      mockGetDiagnostics.mockResolvedValue({
        ...mockDiagnostics,
        uptimeSecs: 7200,
        version: {
          name: 'MnemoNAS',
          version: '0.3.0',
          go: '1.25.9',
          buildTime: 'unknown',
        },
      })

      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getAllByText('2小时 0分钟').length).toBeGreaterThan(0)
        expect(screen.getByText(/Go 1\.25\.9/)).toBeTruthy()
        expect(screen.queryByText(/构建/)).toBeNull()
      })
    })

    it('displays storage alert runtime status when diagnostics provide it', async () => {
      mockGetDiagnostics.mockResolvedValue({
        ...mockDiagnostics,
        alerts: {
          enabled: true,
          runtimeAvailable: true,
          webhookConfigured: true,
          lastLevel: 'warning',
          lastCheckedAt: '2026-04-29T10:30:00Z',
          lastUsedPct: 87.5,
          lastFreeBytes: 9 * 1024 * 1024 * 1024,
        },
      })

      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('存储告警处于提醒级别')).toBeTruthy()
        expect(screen.getByText(/使用率 87\.5%/)).toBeTruthy()
      })
    })

    it('nudges admins when storage alerts are disabled', async () => {
      mockGetDiagnostics.mockResolvedValue({
        ...mockDiagnostics,
        alerts: {
          enabled: false,
          runtimeAvailable: true,
          webhookConfigured: false,
        },
      })

      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('存储告警未启用')).toBeTruthy()
      })
    })

    it('warns when alert monitoring runtime is unavailable', async () => {
      mockGetDiagnostics.mockResolvedValue({
        ...mockDiagnostics,
        alerts: {
          enabled: true,
          runtimeAvailable: false,
          webhookConfigured: true,
        },
      })

      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('存储告警运行态不可用')).toBeTruthy()
      })
    })

    it('surfaces critical storage alert status', async () => {
      mockGetDiagnostics.mockResolvedValue({
        ...mockDiagnostics,
        alerts: {
          enabled: true,
          runtimeAvailable: true,
          webhookConfigured: false,
          lastLevel: 'critical',
          lastCheckedAt: '2026-04-29T10:30:00Z',
          lastUsedPct: 95.5,
          lastFreeBytes: 1024,
        },
      })

      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('存储告警处于严重级别')).toBeTruthy()
        expect(screen.getByText(/使用率 95\.5%/)).toBeTruthy()
        expect(screen.getByText(/剩余 1 KB/)).toBeTruthy()
      })
    })

    it('shows enabled alert status when webhook is not configured yet', async () => {
      mockGetDiagnostics.mockResolvedValue({
        ...mockDiagnostics,
        alerts: {
          enabled: true,
          runtimeAvailable: true,
          webhookConfigured: false,
        },
      })

      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('存储告警已启用，未配置 Webhook')).toBeTruthy()
        expect(screen.getByText(/等待首次检查/)).toBeTruthy()
      })
    })
  })

  describe('stats cards', () => {
    it('displays uptime card', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('运行时间')).toBeTruthy()
        expect(screen.getByText(/1天/)).toBeTruthy()
      })
    })

    it('displays memory usage card', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('内存使用')).toBeTruthy()
        expect(screen.getAllByText(/50 MB/).length).toBeGreaterThan(0)
      })
    })

    it('displays storage objects card', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('存储对象')).toBeTruthy()
        expect(screen.getAllByText('1234').length).toBeGreaterThan(0)
      })
    })

    it('displays disk usage card', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getAllByText('磁盘使用').length).toBeGreaterThan(0)
        expect(screen.getAllByText('25.0%').length).toBeGreaterThan(0)
      })
    })
  })

  describe('storage details', () => {
    it('displays storage details section', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('存储详情')).toBeTruthy()
      })
    })

    it('displays object count', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('对象数量')).toBeTruthy()
      })
    })

    it('displays native filesystem integrity status', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('原生数据校验支持')).toBeTruthy()
        expect(screen.getByText('ZFS')).toBeTruthy()
      })
    })

    it('warns when storage filesystem lacks native data checksums', async () => {
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        diskFilesystemType: 'ext4',
        diskNativeDataChecksumSupport: false,
      })
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('建议使用 ZFS/Btrfs')).toBeTruthy()
        expect(screen.getByText('EXT4')).toBeTruthy()
      })
    })

    it('warns when the disk filesystem is unknown', async () => {
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        diskFilesystemType: 'unknown',
        diskNativeDataChecksumSupport: false,
      })
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('文件系统未知')).toBeTruthy()
      })
    })

    it('warns strongly when storage is backed by tmpfs', async () => {
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        diskFilesystemType: 'tmpfs',
        diskNativeDataChecksumSupport: false,
      })
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('临时文件系统')).toBeTruthy()
      })
    })

    it('warns when storage is backed by network or FUSE filesystems', async () => {
      mockGetStorageStats.mockResolvedValue({
        ...mockStats,
        diskFilesystemType: 'nfs4',
        diskNativeDataChecksumSupport: false,
      })
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('网络或 FUSE 存储')).toBeTruthy()
      })
    })

    it('treats explicit unavailable storage stats as unavailable even when numeric fields are present', async () => {
      mockGetStorageStats.mockResolvedValue({
        totalObjects: 999,
        totalSize: 1024,
        dedupRatio: 0.42,
        storageStatsAvailable: false,
      })
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('统计不可用')).toBeTruthy()
        expect(screen.queryByText('999')).toBeNull()
        expect(screen.queryByText('42.0%')).toBeNull()
      })
    })
  })

  describe('trash info', () => {
    it('displays trash section', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('回收站')).toBeTruthy()
      })
    })

    it('displays trash file count', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('待删除文件')).toBeTruthy()
        expect(screen.getByText('5')).toBeTruthy()
      })
    })
  })

  describe('memory section', () => {
    it('displays memory section', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('内存与性能')).toBeTruthy()
      })
    })

    it('displays GC count', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('GC 次数')).toBeTruthy()
        expect(screen.getByText('10')).toBeTruthy()
      })
    })

    it('displays goroutines count', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('Goroutines')).toBeTruthy()
        expect(screen.getByText('25')).toBeTruthy()
      })
    })
  })

  describe('degraded status', () => {
    it('handles disconnected dataplane', async () => {
      mockGetDiagnostics.mockResolvedValue({
        ...mockDiagnostics,
        system: { ...mockDiagnostics.system, dataplaneConnected: false },
      })
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('数据面')).toBeTruthy()
      })
    })
  })

  describe('error handling', () => {
    it('shows a partial-data warning when diagnostics or stats return service unavailable', async () => {
      mockGetDiagnostics.mockRejectedValue(new ApiError('diagnostics unavailable', 503, 'SERVICE_UNAVAILABLE'))
      mockGetStorageStats.mockResolvedValue(mockStats)
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('系统健康')).toBeTruthy()
        expect(screen.getByText('部分健康数据加载失败')).toBeTruthy()
        expect(screen.getByText('当前页面展示的是可用数据，部分指标可能不是最新状态。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      expect(screen.getByText('存储对象')).toBeTruthy()
    })

    it('shows retryable error state when health queries fail', async () => {
      mockGetDiagnostics.mockRejectedValue(new Error('Network error'))
      mockGetStorageStats.mockRejectedValue(new Error('Network error'))
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('加载系统健康信息失败')).toBeTruthy()
        expect(screen.getByText('Network error')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })
    })

    it('handles missing optional data', async () => {
      mockGetDiagnostics.mockResolvedValue({
        system: { filesystemInitialized: true },
        dataplane: {},
      })
      mockGetStorageStats.mockResolvedValue({})
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('系统健康')).toBeTruthy()
        expect(screen.getAllByText('--').length).toBeGreaterThan(0)
        expect(screen.getByText('未知')).toBeTruthy()
      })
    })

    it('renders zero uptime values instead of treating them as unknown', async () => {
      mockGetDiagnostics.mockResolvedValue({
        ...mockDiagnostics,
        uptimeSecs: 0,
        dataplane: {
          ...mockDiagnostics.dataplane,
          uptimeSec: 0,
        },
      })
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getAllByText('0分钟').length).toBeGreaterThan(0)
      })
    })
  })
})
