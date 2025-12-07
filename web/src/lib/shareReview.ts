import { formatExpiration, type Share, type ShareRiskLevel, type ShareRiskReason } from '@/api/share'

export type ShareReviewSummary = {
  totalCount: number
  enabledCount: number
  disabledCount: number
  reviewCount: number
  highRiskCount: number
  passwordlessCount: number
  broadCount: number
  expiringSoonCount: number
  staleCount: number
}

export type ShareReviewReportOptions = {
  pathFilter?: string
}

const shareRiskReasonMessages: Record<string, string> = {
  root_folder: '根目录分享会公开整个文件空间。',
  broad_folder: '顶层文件夹分享可能覆盖较多内容。',
  no_password: '未设置密码，持有链接的人可直接访问。',
  no_expiration: '未设置过期时间，链接会长期有效。',
  expiring_soon: '分享即将到期，请确认是否需要延长或关闭。',
  unlimited_access: '未设置访问次数上限。',
  unused_enabled: '该分享长期未被访问但仍处于启用状态。',
  stale_enabled: '该分享最近访问时间较久，请确认是否仍需保留。',
}

function shareHasRiskCode(share: Share, code: string): boolean {
  return share.enabled && (share.risk?.reasons?.some(reason => !reason.resolved && reason.code === code) ?? false)
}

function isReviewShare(share: Share): boolean {
  return share.enabled && !!share.risk && share.risk.level !== 'none'
}

function getUnresolvedRiskReasons(share: Share): ShareRiskReason[] {
  return share.risk?.reasons?.filter(reason => !reason.resolved) ?? []
}

export function summarizeShareReview(shares: Share[]): ShareReviewSummary {
  return shares.reduce<ShareReviewSummary>((summary, share) => {
    summary.totalCount += 1
    if (share.enabled) {
      summary.enabledCount += 1
    } else {
      summary.disabledCount += 1
    }
    if (isReviewShare(share)) {
      summary.reviewCount += 1
    }
    if (share.enabled && share.risk?.level === 'high') {
      summary.highRiskCount += 1
    }
    if (shareHasRiskCode(share, 'no_password')) {
      summary.passwordlessCount += 1
    }
    if (shareHasRiskCode(share, 'root_folder') || shareHasRiskCode(share, 'broad_folder')) {
      summary.broadCount += 1
    }
    if (shareHasRiskCode(share, 'expiring_soon')) {
      summary.expiringSoonCount += 1
    }
    if (shareHasRiskCode(share, 'unused_enabled') || shareHasRiskCode(share, 'stale_enabled')) {
      summary.staleCount += 1
    }
    return summary
  }, {
    totalCount: 0,
    enabledCount: 0,
    disabledCount: 0,
    reviewCount: 0,
    highRiskCount: 0,
    passwordlessCount: 0,
    broadCount: 0,
    expiringSoonCount: 0,
    staleCount: 0,
  })
}

function getRiskPriority(share: Share): number {
  if (!share.enabled) {
    return 4
  }
  switch (share.risk?.level) {
  case 'high':
    return 0
  case 'medium':
    return 1
  case 'low':
    return 2
  default:
    return 3
  }
}

function compareSharesByReviewPriority(left: Share, right: Share): number {
  const priorityDiff = getRiskPriority(left) - getRiskPriority(right)
  if (priorityDiff !== 0) {
    return priorityDiff
  }
  return left.path.localeCompare(right.path)
}

function formatShareType(type: Share['type']): string {
  return type === 'folder' ? '文件夹' : '文件'
}

function formatRiskLevel(level: ShareRiskLevel | undefined): string {
  switch (level) {
  case 'high':
    return '高风险'
  case 'medium':
    return '中风险'
  case 'low':
    return '低风险'
  default:
    return '无'
  }
}

function formatAccessLimit(share: Share): string {
  const maxAccess = share.max_access && share.max_access > 0 ? `${share.max_access}` : '不限'
  return `${share.access_count} / ${maxAccess}`
}

function formatRiskReason(reason: ShareRiskReason): string {
  const message = reason.message.trim()
  return message || shareRiskReasonMessages[reason.code] || formatRiskLevel(reason.level)
}

function formatRiskReasons(share: Share): string {
  const reasons = getUnresolvedRiskReasons(share).map(formatRiskReason)
  return reasons.length > 0 ? reasons.join('；') : '无'
}

function getShareReviewAction(share: Share): string {
  if (!share.enabled) {
    return '确认是否仍需保留；不再使用时可删除。'
  }
  if (share.risk?.level === 'high') {
    return '停用或补齐密码、有效期和访问次数限制。'
  }
  if (shareHasRiskCode(share, 'expiring_soon')) {
    return '确认延期或关闭。'
  }
  if (shareHasRiskCode(share, 'unused_enabled') || shareHasRiskCode(share, 'stale_enabled')) {
    return '确认是否仍需保留；不再使用时停用。'
  }
  if (isReviewShare(share)) {
    return '复核分享范围和访问限制。'
  }
  return '无需处理。'
}

export function getShareReviewReportShares<T extends Share>(shares: T[]): T[] {
  return [...shares].sort(compareSharesByReviewPriority)
}

export function formatShareReviewReport(
  shares: Share[],
  summary = summarizeShareReview(shares),
  options: ShareReviewReportOptions = {},
): string {
  const headerRows = [
    ['分享总数', `${summary.totalCount} 个`],
    ['启用分享', `${summary.enabledCount} 个`],
    ['停用分享', `${summary.disabledCount} 个`],
    ['需复核', `${summary.reviewCount} 个`],
    ['需处理', `${summary.highRiskCount} 个`],
    ['无密码', `${summary.passwordlessCount} 个`],
    ['覆盖较大', `${summary.broadCount} 个`],
    ['即将到期', `${summary.expiringSoonCount} 个`],
    ['长期未访问', `${summary.staleCount} 个`],
  ]
  if (options.pathFilter) {
    headerRows.push(['路径筛选', options.pathFilter])
  }

  const shareRows = getShareReviewReportShares(shares).map((share) => [
    share.path,
    formatShareType(share.type),
    share.enabled ? '启用' : '停用',
    formatRiskLevel(share.risk?.level),
    share.has_password ? '密码保护' : '无密码',
    formatAccessLimit(share),
    formatExpiration(share.expires_at),
    formatRiskReasons(share),
    getShareReviewAction(share),
  ].join(' | '))

  return [
    '分享复核摘要',
    ...headerRows.map(([label, value]) => `${label}：${value}`),
    '',
    '分享明细',
    '路径 | 类型 | 状态 | 风险等级 | 访问限制 | 访问次数 | 过期时间 | 风险原因 | 建议处理',
    ...shareRows,
  ].join('\n')
}
