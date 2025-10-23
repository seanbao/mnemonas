import { describe, expect, it } from 'vitest'
import type { DirectoryQuotaUsage } from '@/api/files'
import {
  formatDirectoryQuotaSummaryReport,
  getDirectoryQuotaActionText,
  getDirectoryQuotaAttentionListItems,
  getDirectoryQuotaReportItems,
  getDirectoryQuotaStatusLabel,
  summarizeDirectoryQuotas,
} from './directoryQuota'

function directoryQuota(overrides: Partial<DirectoryQuotaUsage>): DirectoryQuotaUsage {
  return {
    path: '/team',
    quotaBytes: 1024,
    usedBytes: 512,
    availableBytes: 512,
    usageRatio: 0.5,
    exists: true,
    status: 'normal',
    ...overrides,
  }
}

describe('directoryQuota', () => {
  it('summarizes directory quota counts and usage', () => {
    const quotas = [
      directoryQuota({ path: '/team', quotaBytes: 1024, usedBytes: 512, status: 'normal' }),
      directoryQuota({ path: '/archive', quotaBytes: 2048, usedBytes: 1900, status: 'warning', usageRatio: 1900 / 2048 }),
      directoryQuota({ path: '/media', quotaBytes: 4096, usedBytes: 4608, status: 'exceeded', usageRatio: 1.125 }),
      directoryQuota({ path: '/missing', quotaBytes: 512, usedBytes: 0, status: 'missing', usageRatio: 0, exists: false }),
    ]

    expect(summarizeDirectoryQuotas(quotas)).toEqual({
      totalCount: 4,
      normalCount: 1,
      warningCount: 1,
      exceededCount: 1,
      missingCount: 1,
      usedBytes: 7020,
      quotaBytes: 7680,
      usageRatio: 7020 / 7680,
    })
  })

  it('orders attention items by operational priority, usage, and path', () => {
    const quotas = [
      directoryQuota({ path: '/normal', status: 'normal', usageRatio: 0.5 }),
      directoryQuota({ path: '/warning-low', status: 'warning', usageRatio: 0.91 }),
      directoryQuota({ path: '/warning-high', status: 'warning', usageRatio: 0.98 }),
      directoryQuota({ path: '/missing-b', status: 'missing', usageRatio: 0, exists: false }),
      directoryQuota({ path: '/missing-a', status: 'missing', usageRatio: 0, exists: false }),
      directoryQuota({ path: '/exceeded', status: 'exceeded', usageRatio: 1.2 }),
    ]

    expect(getDirectoryQuotaAttentionListItems(quotas).map((quota) => quota.path)).toEqual([
      '/exceeded',
      '/missing-a',
      '/missing-b',
      '/warning-high',
      '/warning-low',
    ])
    expect(getDirectoryQuotaReportItems(quotas).map((quota) => quota.path)).toEqual([
      '/exceeded',
      '/missing-a',
      '/missing-b',
      '/warning-high',
      '/warning-low',
      '/normal',
    ])
  })

  it('formats labels and recommended actions for each status', () => {
    const normal = directoryQuota({ status: 'normal' })
    const warning = directoryQuota({ status: 'warning' })
    const exceeded = directoryQuota({ status: 'exceeded' })
    const missing = directoryQuota({ status: 'missing' })

    expect(getDirectoryQuotaStatusLabel(normal)).toBe('正常')
    expect(getDirectoryQuotaStatusLabel(warning)).toBe('接近上限')
    expect(getDirectoryQuotaStatusLabel(exceeded)).toBe('已达上限')
    expect(getDirectoryQuotaStatusLabel(missing)).toBe('目录未创建')
    expect(getDirectoryQuotaActionText(normal)).toBe('保持当前配置。')
    expect(getDirectoryQuotaActionText(warning)).toBe('复核近期增长，并确认是否需要扩容或归档。')
    expect(getDirectoryQuotaActionText(exceeded)).toBe('清理目录内容、提高配额，或迁移部分数据。')
    expect(getDirectoryQuotaActionText(missing)).toBe('创建目标目录，或删除不再使用的配额配置。')
  })

  it('formats a copyable administrator quota report', () => {
    const quotas = [
      directoryQuota({
        path: '/archive',
        quotaBytes: 1024 ** 3,
        usedBytes: Math.round(0.95 * 1024 ** 3),
        availableBytes: Math.round(0.05 * 1024 ** 3),
        usageRatio: 0.95,
        status: 'warning',
      }),
      directoryQuota({
        path: '/missing',
        quotaBytes: 512 * 1024 ** 2,
        usedBytes: 0,
        availableBytes: 512 * 1024 ** 2,
        usageRatio: 0,
        status: 'missing',
        exists: false,
      }),
    ]
    const summary = summarizeDirectoryQuotas(quotas)

    expect(formatDirectoryQuotaSummaryReport(quotas, summary, 2)).toBe([
      '目录配额摘要',
      '配额目录：2 个',
      '正常：0 个',
      '接近上限：1 个',
      '已达上限：0 个',
      '路径不存在：1 个',
      '总用量：972.8 MB / 1.5 GB (63.3%)',
      '需复核：2 个',
      '',
      '目录明细',
      '路径 | 状态 | 用量 | 剩余 | 占比 | 存在状态 | 建议处理',
      '/missing | 目录未创建 | 0 B / 512 MB | 512 MB | 0.0% | 路径不存在 | 创建目标目录，或删除不再使用的配额配置。',
      '/archive | 接近上限 | 972.8 MB / 1 GB | 51.2 MB | 95.0% | 当前目录 | 复核近期增长，并确认是否需要扩容或归档。',
    ].join('\n'))
  })
})
