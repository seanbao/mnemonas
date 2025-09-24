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
  getDiskHealth: vi.fn(),
  getStorageStats: vi.fn(),
  downloadDiagnosticsExport: vi.fn(),
}))

vi.mock('@/stores/auth', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/auth')>()
  return {
    ...actual,
    useUser: () => mockUser,
  }
})

import { ApiError, getDiagnostics, getDiskHealth, getStorageStats, downloadDiagnosticsExport } from '@/api/files'

const mockGetDiagnostics = getDiagnostics as ReturnType<typeof vi.fn>
const mockGetDiskHealth = getDiskHealth as ReturnType<typeof vi.fn>
const mockGetStorageStats = getStorageStats as ReturnType<typeof vi.fn>
const mockDownloadDiagnosticsExport = downloadDiagnosticsExport as ReturnType<typeof vi.fn>

function expectCalledWithAbortSignal(mockFn: ReturnType<typeof vi.fn>) {
  const call = mockFn.mock.calls.find(([options]) => {
    return (options as { signal?: AbortSignal } | undefined)?.signal instanceof AbortSignal
  })
  expect(call).toBeTruthy()
  expect(Object.keys((call?.[0] ?? {}) as Record<string, unknown>).sort()).toEqual(['signal'])
}

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
    maintenance: {
      historyReady: true,
      scrubScheduleEnabled: true,
      scrubScheduleInterval: '168h0m0s',
      scrubRetryInterval: '1h0m0s',
      scrubMaxRetries: 1,
      lastScrubStatus: 'completed',
      lastScrubAt: '2026-05-13T08:30:00Z',
      scrubFailureRetries: 0,
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

  const mockDiskHealth = {
    enabled: true,
    status: 'ok',
    checkedAt: '2026-05-13T08:30:00Z',
    message: 'all configured disks are healthy',
    devices: [{
      name: 'data',
      path: '/dev/disk/by-id/test',
      model: 'TestDisk',
      present: true,
      smartAvailable: true,
      smartPassed: true,
      temperatureC: 42,
      powerOnHours: 1234,
      wearPercentUsed: 8,
      availableSparePercent: 95,
      mediaErrors: 0,
      status: 'ok',
      message: 'device is healthy',
    }],
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
    mockGetDiskHealth.mockResolvedValue(mockDiskHealth)
    mockGetStorageStats.mockResolvedValue(mockStats)
    mockDownloadDiagnosticsExport.mockResolvedValue(undefined)
  })

  describe('loading state', () => {
    it('shows refresh button with loading state', () => {
      mockGetDiagnostics.mockImplementation(() => new Promise(() => {}))
      mockGetDiskHealth.mockImplementation(() => new Promise(() => {}))
      mockGetStorageStats.mockImplementation(() => new Promise(() => {}))
      render(<HealthPage />)

      expect(screen.getByText('刷新')).toBeTruthy()
    })
  })

  describe('header', () => {
    it('passes abort signals to health queries', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expectCalledWithAbortSignal(mockGetDiagnostics)
        expectCalledWithAbortSignal(mockGetStorageStats)
        expectCalledWithAbortSignal(mockGetDiskHealth)
      })
    })

    it('displays page title', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('设备状态')).toBeTruthy()
      })
    })

    it('displays subtitle', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('磁盘、存储和后台服务是否正常')).toBeTruthy()
      })
    })

    it('renders refresh button', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('刷新')).toBeTruthy()
      })
    })

    it('downloads the diagnostics bundle from the health page', async () => {
      const user = userEvent.setup()
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '下载诊断包' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '下载诊断包' }))

      await waitFor(() => {
        expect(mockDownloadDiagnosticsExport).toHaveBeenCalledWith(expect.objectContaining({
          signal: expect.any(AbortSignal),
        }))
      })

      expect(mockAddToast).toHaveBeenCalledWith({
        title: '诊断信息导出已开始',
        color: 'success',
      })
    })

    it('shows an unavailable warning when diagnostics export is temporarily unavailable', async () => {
      const user = userEvent.setup()
      mockDownloadDiagnosticsExport.mockRejectedValue(new ApiError('diagnostics unavailable', 503, 'SERVICE_UNAVAILABLE'))
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '下载诊断包' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '下载诊断包' }))

      await waitFor(() => {
        expect(mockDownloadDiagnosticsExport).toHaveBeenCalled()
      })

      expect(mockAddToast).toHaveBeenCalledWith({
        title: '诊断包暂不可用',
        description: '诊断包服务当前不可用，请检查设备状态后重试。',
        color: 'warning',
      })
    })

    it('shows generic error feedback when diagnostics export fails', async () => {
      const user = userEvent.setup()
      mockDownloadDiagnosticsExport.mockRejectedValue(new Error('export failed'))
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '下载诊断包' })).toBeTruthy()
      })

      await user.click(screen.getByRole('button', { name: '下载诊断包' }))

      await waitFor(() => {
        expect(mockDownloadDiagnosticsExport).toHaveBeenCalled()
      })

      expect(mockAddToast).toHaveBeenCalledWith({
        title: '下载诊断包失败',
        description: '操作未完成，请稍后重试。',
        color: 'danger',
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
        expect(mockGetDiskHealth.mock.calls.length).toBeGreaterThanOrEqual(2)
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
      expect(mockAddToast).toHaveBeenCalledWith({ title: '设备状态已刷新', color: 'success' })
    })
    })

    it('shows warning toast when refresh is temporarily unavailable', async () => {
    const user = userEvent.setup()
    mockGetDiagnostics.mockResolvedValueOnce(mockDiagnostics)
    mockGetDiskHealth.mockResolvedValueOnce(mockDiskHealth)
    mockGetStorageStats.mockResolvedValueOnce(mockStats)
    mockGetDiagnostics.mockResolvedValueOnce(mockDiagnostics)
    mockGetDiskHealth.mockResolvedValueOnce(mockDiskHealth)
    mockGetStorageStats.mockRejectedValueOnce(new ApiError('health unavailable', 503, 'SERVICE_UNAVAILABLE'))

    render(<HealthPage />)

    await waitFor(() => {
      expect(screen.getByText('刷新')).toBeTruthy()
    })

    await user.click(screen.getByText('刷新'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '刷新暂不可用',
        description: '状态数据当前不可用，请检查服务状态或稍后重试。',
        color: 'warning',
      })
    })
    })

    it('shows danger toast when refresh fails with a generic error', async () => {
    const user = userEvent.setup()
    mockGetDiagnostics.mockResolvedValueOnce(mockDiagnostics)
    mockGetDiskHealth.mockResolvedValueOnce(mockDiskHealth)
    mockGetStorageStats.mockResolvedValueOnce(mockStats)
    mockGetDiagnostics.mockRejectedValueOnce(new Error('refresh failed'))
    mockGetDiskHealth.mockResolvedValueOnce(mockDiskHealth)
    mockGetStorageStats.mockResolvedValueOnce(mockStats)

    render(<HealthPage />)

    await waitFor(() => {
      expect(screen.getByText('刷新')).toBeTruthy()
    })

    await user.click(screen.getByText('刷新'))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '刷新失败',
        description: '操作未完成，请稍后重试。',
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
      mockGetDiskHealth
        .mockResolvedValueOnce(mockDiskHealth)
        .mockResolvedValueOnce({
          ...mockDiskHealth,
          checkedAt: '2026-05-13T09:30:00Z',
        })

      const { rerender } = render(<HealthPage />)

      await waitFor(() => {
        expect(mockGetDiagnostics).toHaveBeenCalledTimes(1)
        expect(mockGetDiskHealth).toHaveBeenCalledTimes(1)
        expect(mockGetStorageStats).toHaveBeenCalledTimes(1)
      })

      mockUser.id = 'u2'
      mockUser.username = 'other-admin'
      mockUser.email = 'other@local'

      rerender(<HealthPage />)

      await waitFor(() => {
        expect(mockGetDiagnostics).toHaveBeenCalledTimes(2)
        expect(mockGetDiskHealth).toHaveBeenCalledTimes(2)
        expect(mockGetStorageStats).toHaveBeenCalledTimes(2)
      })
    })
  })

  describe('system status', () => {
    it('displays system status section', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('运行状态')).toBeTruthy()
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

    it('displays scheduled scrub status', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('周期 Scrub 已启用')).toBeTruthy()
        expect(screen.getByText(/每 7 天 自动巡检/)).toBeTruthy()
      })
    })

    it('warns when scheduled scrub recently failed', async () => {
      mockGetDiagnostics.mockResolvedValue({
        ...mockDiagnostics,
        maintenance: {
          historyReady: true,
          scrubScheduleEnabled: true,
          scrubScheduleInterval: '24h0m0s',
          scrubRetryInterval: '30m0s',
          scrubMaxRetries: 2,
          lastScrubStatus: 'failed',
          lastScrubAt: '2026-05-13T08:30:00Z',
          scrubFailureRetries: 1,
        },
      })

      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('周期 Scrub 最近失败')).toBeTruthy()
        expect(screen.getByText(/当前已重试 1\/2 次/)).toBeTruthy()
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
        expect(screen.getByText('最近操作')).toBeTruthy()
      })
    })

    it('displays favorites store status when diagnostics provide it', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('收藏存储')).toBeTruthy()
      })
    })

    it('warns when SMB is configured without a runtime', async () => {
      mockGetDiagnostics.mockResolvedValue({
        ...mockDiagnostics,
        system: {
          ...mockDiagnostics.system,
          smbRuntimeReady: false,
        },
        smb: {
          enabled: true,
          runtimeAvailable: false,
          implementation: 'planned_sidecar',
          listen: '127.0.0.1:1445',
          serverName: 'mnemonas',
          signingRequired: true,
          encryptionRequired: false,
          shareCount: 2,
          credentialsReady: true,
          gatewayConfigured: true,
          message: 'SMB sidecar is not implemented.',
        },
      })

      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('SMB 运行态')).toBeTruthy()
        expect(screen.getByText('SMB 当前不可挂载')).toBeTruthy()
        expect(screen.getByText(/已配置 2 个共享/)).toBeTruthy()
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
        expect(screen.getAllByText('2 小时').length).toBeGreaterThan(0)
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
        expect(screen.getByText('可用空间偏紧')).toBeTruthy()
        expect(screen.getByText(/使用率 87\.5%/)).toBeTruthy()
        expect(screen.getByText(/通知通道：Webhook/)).toBeTruthy()
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
        expect(screen.getByText('空间提醒未启用')).toBeTruthy()
      })
    })

    it('warns when alert monitoring runtime is unavailable', async () => {
      mockGetDiagnostics.mockResolvedValue({
        ...mockDiagnostics,
        alerts: {
          enabled: true,
          runtimeAvailable: false,
          webhookConfigured: true,
          telegramConfigured: false,
          emailConfigured: true,
        },
      })

      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('空间提醒暂不可用')).toBeTruthy()
        expect(screen.getByText(/通知通道：Webhook、邮件/)).toBeTruthy()
      })
    })

    it('surfaces critical storage alert status', async () => {
      mockGetDiagnostics.mockResolvedValue({
        ...mockDiagnostics,
        alerts: {
          enabled: true,
          runtimeAvailable: true,
          webhookConfigured: false,
          telegramConfigured: false,
          emailConfigured: false,
          lastLevel: 'critical',
          lastCheckedAt: '2026-04-29T10:30:00Z',
          lastUsedPct: 95.5,
          lastFreeBytes: 1024,
        },
      })

      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('可用空间严重不足')).toBeTruthy()
        expect(screen.getByText(/使用率 95\.5%/)).toBeTruthy()
        expect(screen.getByText(/剩余 1 KB/)).toBeTruthy()
        expect(screen.getByText(/未配置外部通知通道/)).toBeTruthy()
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
        expect(screen.getByText('空间提醒已启用，未配置通知通道')).toBeTruthy()
        expect(screen.getByText(/等待首次检查/)).toBeTruthy()
      })
    })

    it('lists Telegram as a configured alert notification channel', async () => {
      mockGetDiagnostics.mockResolvedValue({
        ...mockDiagnostics,
        alerts: {
          enabled: true,
          runtimeAvailable: true,
          webhookConfigured: false,
          telegramConfigured: true,
          emailConfigured: false,
        },
      })

      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('空间提醒已启用')).toBeTruthy()
        expect(screen.getByText(/通知通道已配置：Telegram/)).toBeTruthy()
      })
    })

    it('lists WeCom as a configured alert notification channel', async () => {
      mockGetDiagnostics.mockResolvedValue({
        ...mockDiagnostics,
        alerts: {
          enabled: true,
          runtimeAvailable: true,
          webhookConfigured: false,
          telegramConfigured: false,
          wecomConfigured: true,
          emailConfigured: false,
        },
      })

      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('空间提醒已启用')).toBeTruthy()
        expect(screen.getByText(/通知通道已配置：企业微信/)).toBeTruthy()
      })
    })

    it('lists Webhook and email as configured alert notification channels', async () => {
      mockGetDiagnostics.mockResolvedValue({
        ...mockDiagnostics,
        alerts: {
          enabled: true,
          runtimeAvailable: true,
          webhookConfigured: true,
          telegramConfigured: false,
          emailConfigured: true,
        },
      })

      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('空间提醒已启用')).toBeTruthy()
        expect(screen.getByText(/通知通道已配置：Webhook、邮件/)).toBeTruthy()
      })
    })

    it('displays disk health summary and devices', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('磁盘健康')).toBeTruthy()
        expect(screen.getByText('磁盘健康正常')).toBeTruthy()
        expect(screen.getByText('TestDisk')).toBeTruthy()
        expect(screen.getByText('42 C · 磨损 8% · 备用 95%')).toBeTruthy()
      })
    })

    it('surfaces critical disk health status', async () => {
      mockGetDiskHealth.mockResolvedValue({
        ...mockDiskHealth,
        status: 'critical',
        message: 'one or more disks require immediate attention',
        devices: [{
          ...mockDiskHealth.devices[0],
          status: 'critical',
          smartPassed: false,
          message: 'SMART self-assessment failed',
        }],
      })

      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('磁盘健康严重异常')).toBeTruthy()
        expect(screen.getByText('SMART 自检未通过，请尽快备份并检查磁盘。')).toBeTruthy()
        expect(screen.getByText('严重')).toBeTruthy()
      })
      expect(screen.queryByText('SMART self-assessment failed')).toBeNull()
    })

    it('maps disk health device backend messages before rendering them', async () => {
      mockGetDiskHealth.mockResolvedValue({
        ...mockDiskHealth,
        status: 'warning',
        devices: [
          {
            ...mockDiskHealth.devices[0],
            name: 'hot-disk',
            path: '/dev/disk/by-id/hot-disk',
            status: 'warning',
            message: 'temperature 55 C reached warning threshold 50 C',
          },
          {
            ...mockDiskHealth.devices[0],
            name: 'wear-disk',
            path: '/dev/disk/by-id/wear-disk',
            status: 'critical',
            message: 'media wear used 98% reached critical threshold 95%',
          },
          {
            ...mockDiskHealth.devices[0],
            name: 'probe-disk',
            path: '/dev/disk/by-id/probe-disk',
            status: 'unavailable',
            message: 'smart probe failed: permission denied',
          },
          {
            ...mockDiskHealth.devices[0],
            name: 'unknown-disk',
            path: '/dev/disk/by-id/unknown-disk',
            status: 'warning',
            message: 'backend raw diagnostic text',
          },
        ],
      })

      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('磁盘温度 55 C 已达到提醒阈值 50 C。')).toBeTruthy()
        expect(screen.getByText('介质磨损 98% 已达到严重阈值 95%。')).toBeTruthy()
        expect(screen.getByText('SMART 检测命令执行失败，请检查 smartctl 权限和设备路径。')).toBeTruthy()
        expect(screen.getByText('磁盘健康存在提醒，请检查 SMART、温度、磨损和设备连接状态。')).toBeTruthy()
      })
      expect(screen.queryByText('temperature 55 C reached warning threshold 50 C')).toBeNull()
      expect(screen.queryByText('media wear used 98% reached critical threshold 95%')).toBeNull()
      expect(screen.queryByText('smart probe failed: permission denied')).toBeNull()
      expect(screen.queryByText('backend raw diagnostic text')).toBeNull()
    })

    it('shows disabled disk health guidance', async () => {
      mockGetDiskHealth.mockResolvedValue({
        enabled: false,
        status: 'disabled',
        checkedAt: '2026-05-13T08:30:00Z',
        devices: [],
        message: 'disk health checks are disabled',
      })

      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('磁盘健康监控未启用')).toBeTruthy()
      })
    })
  })

  describe('stats cards', () => {
    it('displays uptime card', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('运行时间')).toBeTruthy()
        expect(screen.getByText(/1 天/)).toBeTruthy()
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
        expect(screen.getByRole('progressbar', { name: '磁盘使用' })).toHaveAttribute('aria-valuetext', '25.0% 已用')
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
        expect(screen.getByRole('progressbar', { name: '存储使用' })).toHaveAttribute('aria-valuetext', '统计不可用')
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
        expect(screen.getByText('设备状态')).toBeTruthy()
        expect(screen.getByText('部分状态数据加载失败')).toBeTruthy()
        expect(screen.getByText('当前页面展示的是可用数据，部分指标可能不是最新状态。')).toBeTruthy()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeTruthy()
      })

      expect(screen.getByText('存储对象')).toBeTruthy()
    })

    it('shows retryable error state when health queries fail', async () => {
      mockGetDiagnostics.mockRejectedValue(new Error('Network error'))
      mockGetDiskHealth.mockRejectedValue(new Error('Network error'))
      mockGetStorageStats.mockRejectedValue(new Error('Network error'))
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('加载设备状态失败')).toBeTruthy()
        expect(screen.getByText('数据加载失败，请检查网络或稍后重试。')).toBeTruthy()
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
        expect(screen.getByText('设备状态')).toBeTruthy()
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
        expect(screen.getAllByText('0 秒').length).toBeGreaterThan(0)
      })
    })
  })
})
