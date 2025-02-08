import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { HealthPage } from './Health'

// Mock API
vi.mock('@/api/files', () => ({
  getDiagnostics: vi.fn(),
  getStorageStats: vi.fn(),
}))

import { getDiagnostics, getStorageStats } from '@/api/files'

const mockGetDiagnostics = getDiagnostics as ReturnType<typeof vi.fn>
const mockGetStorageStats = getStorageStats as ReturnType<typeof vi.fn>

describe('HealthPage', () => {
  const mockDiagnostics = {
    system: {
      filesystemInitialized: true,
      dataplaneConnected: true,
      thumbnailServiceReady: true,
    },
    version: {
      name: 'MnemoNAS',
      version: '0.3.0',
      go: '1.22',
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
  }

  beforeEach(() => {
    vi.clearAllMocks()
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

    it('displays version info', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText(/MnemoNAS/)).toBeTruthy()
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

    it('displays dedup ratio card', async () => {
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('去重率')).toBeTruthy()
        // Multiple elements may display 35.0%
        expect(screen.getAllByText('35.0%').length).toBeGreaterThan(0)
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
      })
      mockGetStorageStats.mockResolvedValue({})
      render(<HealthPage />)

      await waitFor(() => {
        expect(screen.getByText('系统健康')).toBeTruthy()
      })
    })
  })
})
