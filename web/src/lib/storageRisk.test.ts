import { describe, expect, it } from 'vitest'
import type { DirectoryQuotaSummary } from './directoryQuota'
import type { DiskSpaceStatus, FilesystemIntegrityStatus } from './storageStats'
import { buildStorageRiskItems, getStorageRiskLevel, getStorageRiskNextStepSummary, getStorageRiskSummary } from './storageRisk'

const normalDiskSpace: DiskSpaceStatus = {
  level: 'normal',
  title: '存储空间充足',
  description: '可用空间处于安全范围。',
  label: '空间充足',
}

const supportedFilesystem: FilesystemIntegrityStatus = {
  level: 'supported',
  title: '原生数据校验支持',
  description: 'ZFS 具备底层校验与 scrub 能力，仍需保留独立备份。',
  label: '已支持',
}

function quotaSummary(overrides: Partial<DirectoryQuotaSummary> = {}): DirectoryQuotaSummary {
  return {
    totalCount: 1,
    normalCount: 1,
    warningCount: 0,
    exceededCount: 0,
    missingCount: 0,
    usedBytes: 512,
    quotaBytes: 1024,
    usageRatio: 0.5,
    ...overrides,
  }
}

describe('storageRisk', () => {
  it('does not create risk items for healthy storage with configured quota boundaries', () => {
    const items = buildStorageRiskItems(normalDiskSpace, supportedFilesystem, true, quotaSummary())

    expect(items).toEqual([])
    expect(getStorageRiskLevel(items)).toBe('normal')
    expect(getStorageRiskSummary('normal')).toEqual({
      tone: 'normal',
      title: '状态正常',
      description: '容量、底层校验和目录配额未发现需处理项；仍应保留独立备份。',
    })
    expect(getStorageRiskNextStepSummary(items)).toBe('继续保留外部备份，并定期运行完整性检查和恢复演练。')
  })

  it('surfaces warning risks for unknown disk capacity, limited filesystem, and missing quota policy', () => {
    const items = buildStorageRiskItems(
      {
        level: 'unknown',
        title: '磁盘容量未知',
        description: '无法判断底层可用容量。',
        label: '容量未知',
      },
      {
        level: 'limited',
        title: '建议使用 ZFS/Btrfs',
        description: '当前未检测到底层数据校验。',
        label: '有限',
      },
      true,
      quotaSummary({
        totalCount: 0,
        normalCount: 0,
        usedBytes: 0,
        quotaBytes: 0,
        usageRatio: undefined,
      }),
    )

    expect(items.map((item) => item.key)).toEqual([
      'disk-space-unknown',
      'filesystem-limited',
      'directory-quota-missing-policy',
    ])
    expect(getStorageRiskLevel(items)).toBe('warning')
    expect(getStorageRiskSummary('warning').tone).toBe('warning')
    expect(getStorageRiskSummary('warning').title).toBe('需要复核')
    expect(getStorageRiskNextStepSummary(items)).toBe('复核增长趋势，并确认容量提醒已经启用；保留独立备份，并定期运行完整性检查 等 3 步')
  })

  it('prioritizes critical risks from capacity, volatile storage, and exceeded quotas', () => {
    const items = buildStorageRiskItems(
      {
        level: 'critical',
        title: '存储空间严重不足',
        description: '可用空间低于安全阈值。',
        label: '严重不足',
      },
      {
        level: 'volatile',
        title: '临时文件系统',
        description: '当前存储可能随重启丢失。',
        label: '临时',
      },
      true,
      quotaSummary({
        totalCount: 3,
        normalCount: 0,
        warningCount: 1,
        exceededCount: 1,
        missingCount: 1,
      }),
    )

    expect(items.map((item) => [item.key, item.level])).toEqual([
      ['disk-space-critical', 'critical'],
      ['filesystem-volatile', 'critical'],
      ['directory-quota-exceeded', 'critical'],
      ['directory-quota-missing', 'warning'],
      ['directory-quota-warning', 'warning'],
    ])
    expect(getStorageRiskLevel(items)).toBe('critical')
    expect(getStorageRiskSummary('critical').tone).toBe('critical')
    expect(getStorageRiskSummary('critical').title).toBe('需立即处理')
    expect(getStorageRiskNextStepSummary(items)).toBe('先清理回收站和临时数据，再安排扩容或迁移；迁移到持久化磁盘后再作为 NAS 存储使用 等 5 步')
  })

  it('warns when directory quota statistics are unavailable even if quota configuration exists', () => {
    const items = buildStorageRiskItems(normalDiskSpace, supportedFilesystem, false, quotaSummary())

    expect(items).toEqual([
      {
        key: 'directory-quota-unavailable',
        level: 'warning',
        title: '目录配额统计不可用',
        description: '无法判断目录配额是否已接近或超过上限。',
        action: '刷新存储统计，或检查后端存储状态。',
      },
    ])
    expect(getStorageRiskLevel(items)).toBe('warning')
  })
})
