import type { DirectoryQuotaUsage } from '@/api/files'
import { formatBytes } from '@/lib/utils'
import { formatUsagePercent } from '@/lib/storageStats'

export type DirectoryQuotaSummary = {
  totalCount: number
  normalCount: number
  warningCount: number
  exceededCount: number
  missingCount: number
  usedBytes: number
  quotaBytes: number
  usageRatio: number | undefined
}

export type DirectoryQuotaListFilter = 'all' | 'attention'

export function getDirectoryQuotaStatusLabel(quota: DirectoryQuotaUsage): string {
  if (quota.status === 'missing') {
    return '目录未创建'
  }
  if (quota.status === 'exceeded') {
    return '已达上限'
  }
  if (quota.status === 'warning') {
    return '接近上限'
  }
  return '正常'
}

export function getDirectoryQuotaActionText(quota: DirectoryQuotaUsage): string {
  if (quota.status === 'exceeded') {
    return '清理目录内容、提高配额，或迁移部分数据。'
  }
  if (quota.status === 'missing') {
    return '创建目标目录，或删除不再使用的配额配置。'
  }
  if (quota.status === 'warning') {
    return '复核近期增长，并确认是否需要扩容或归档。'
  }
  return '保持当前配置。'
}

export function summarizeDirectoryQuotas(quotas: DirectoryQuotaUsage[]): DirectoryQuotaSummary {
  const summary = quotas.reduce<Omit<DirectoryQuotaSummary, 'usageRatio'>>((acc, quota) => {
    acc.totalCount += 1
    acc.usedBytes += quota.usedBytes
    acc.quotaBytes += quota.quotaBytes

    if (quota.status === 'exceeded') {
      acc.exceededCount += 1
    } else if (quota.status === 'warning') {
      acc.warningCount += 1
    } else if (quota.status === 'missing') {
      acc.missingCount += 1
    } else {
      acc.normalCount += 1
    }

    return acc
  }, {
    totalCount: 0,
    normalCount: 0,
    warningCount: 0,
    exceededCount: 0,
    missingCount: 0,
    usedBytes: 0,
    quotaBytes: 0,
  })

  return {
    ...summary,
    usageRatio: summary.quotaBytes > 0 ? summary.usedBytes / summary.quotaBytes : undefined,
  }
}

function getDirectoryQuotaAttentionPriority(status: DirectoryQuotaUsage['status']): number {
  if (status === 'exceeded') {
    return 0
  }
  if (status === 'missing') {
    return 1
  }
  if (status === 'warning') {
    return 2
  }
  return 3
}

function compareDirectoryQuotasByAttention(left: DirectoryQuotaUsage, right: DirectoryQuotaUsage): number {
  const priorityDiff = getDirectoryQuotaAttentionPriority(left.status) - getDirectoryQuotaAttentionPriority(right.status)
  if (priorityDiff !== 0) {
    return priorityDiff
  }
  if (right.usageRatio !== left.usageRatio) {
    return right.usageRatio - left.usageRatio
  }
  return left.path.localeCompare(right.path)
}

export function getDirectoryQuotaAttentionListItems(quotas: DirectoryQuotaUsage[]): DirectoryQuotaUsage[] {
  return quotas
    .filter((quota) => quota.status !== 'normal')
    .sort(compareDirectoryQuotasByAttention)
}

export function getDirectoryQuotaReportItems(quotas: DirectoryQuotaUsage[]): DirectoryQuotaUsage[] {
  return [...quotas].sort(compareDirectoryQuotasByAttention)
}

export function formatDirectoryQuotaSummaryReport(
  quotas: DirectoryQuotaUsage[],
  summary: DirectoryQuotaSummary,
  attentionCount: number,
): string {
  const headerRows = [
    ['配额目录', `${summary.totalCount} 个`],
    ['正常', `${summary.normalCount} 个`],
    ['接近上限', `${summary.warningCount} 个`],
    ['已达上限', `${summary.exceededCount} 个`],
    ['路径不存在', `${summary.missingCount} 个`],
    ['总用量', `${formatBytes(summary.usedBytes)} / ${formatBytes(summary.quotaBytes)} (${formatUsagePercent(summary.usageRatio)})`],
    ['需复核', `${attentionCount} 个`],
  ]
  const quotaRows = getDirectoryQuotaReportItems(quotas).map((quota) => [
    quota.path,
    getDirectoryQuotaStatusLabel(quota),
    `${formatBytes(quota.usedBytes)} / ${formatBytes(quota.quotaBytes)}`,
    formatBytes(quota.availableBytes),
    formatUsagePercent(quota.usageRatio),
    quota.exists ? '当前目录' : '路径不存在',
    getDirectoryQuotaActionText(quota),
  ].join(' | '))

  return [
    '目录配额摘要',
    ...headerRows.map(([label, value]) => `${label}: ${value}`),
    '',
    '目录明细',
    '路径 | 状态 | 用量 | 剩余 | 占比 | 存在状态 | 建议处理',
    ...quotaRows,
  ].join('\n')
}
