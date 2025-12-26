import type { User } from '@/api/users'
import { formatBytes } from '@/lib/utils'

export const quotaUnits = [
  { key: 'B', label: 'B', multiplier: 1 },
  { key: 'MB', label: 'MB', multiplier: 1024 ** 2 },
  { key: 'GB', label: 'GB', multiplier: 1024 ** 3 },
  { key: 'TB', label: 'TB', multiplier: 1024 ** 4 },
] as const

export type QuotaUnit = typeof quotaUnits[number]['key']
export type QuotaStatus = 'unlimited' | 'normal' | 'warning' | 'exceeded'
export type QuotaTone = 'default' | 'success' | 'warning' | 'danger'

export type UserQuotaStatus = {
  status: QuotaStatus
  label: string
  detail: string
  tone: QuotaTone
  percent: number | null
}

export type UserQuotaSummary = {
  totalCount: number
  activeCount: number
  disabledCount: number
  limitedCount: number
  unlimitedCount: number
  normalCount: number
  warningCount: number
  exceededCount: number
  attentionCount: number
  usedBytes: number
  limitedUsedBytes: number
  quotaBytes: number
}

export type UserQuotaAggregateStatus = {
  label: string
  detail: string
  tone: QuotaTone
  percent: number | null
  usedBytes: number
  quotaBytes: number
}

type UserQuotaReportUser = Pick<User, 'username' | 'role' | 'disabled' | 'quota_bytes' | 'used_bytes'>
  & Partial<Pick<User, 'email' | 'groups' | 'home_dir' | 'last_login_at'>>
type UserQuotaAttentionUser = Pick<User, 'username' | 'quota_bytes' | 'used_bytes'>

export function quotaBytesToFormValue(bytes: number): { value: string; unit: QuotaUnit } {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return { value: '0', unit: 'GB' }
  }

  for (const unit of [...quotaUnits].reverse()) {
    const value = bytes / unit.multiplier
    if (value >= 1 && Number.isInteger(value)) {
      return { value: String(value), unit: unit.key }
    }
  }

  return { value: String(bytes), unit: 'B' }
}

export function quotaFormValueToBytes(value: string, unitKey: QuotaUnit): number | null {
  const normalized = value.trim()
  if (!normalized) {
    return 0
  }

  const numericValue = Number(normalized)
  const unit = quotaUnits.find((candidate) => candidate.key === unitKey)
  if (!unit || !Number.isFinite(numericValue) || numericValue < 0) {
    return null
  }

  const bytes = Math.round(numericValue * unit.multiplier)
  return Number.isSafeInteger(bytes) ? bytes : null
}

export function getQuotaUsagePercent(user: Pick<User, 'quota_bytes' | 'used_bytes'>): number | null {
  if (user.quota_bytes <= 0) {
    return null
  }

  return Math.round((user.used_bytes / user.quota_bytes) * 100)
}

export function getQuotaStatus(user: Pick<User, 'quota_bytes' | 'used_bytes'>): UserQuotaStatus {
  const percent = getQuotaUsagePercent(user)

  if (percent === null) {
    return {
      status: 'unlimited',
      label: '未设配额',
      detail: '当前用户不受容量配额限制。',
      tone: 'default',
      percent,
    }
  }

  if (user.used_bytes > user.quota_bytes) {
    return {
      status: 'exceeded',
      label: '已超限',
      detail: `已超出 ${formatBytes(user.used_bytes - user.quota_bytes)}。`,
      tone: 'danger',
      percent,
    }
  }

  if (percent >= 90) {
    return {
      status: 'warning',
      label: '接近上限',
      detail: `剩余 ${formatBytes(Math.max(0, user.quota_bytes - user.used_bytes))}。`,
      tone: 'warning',
      percent,
    }
  }

  return {
    status: 'normal',
    label: '配额正常',
    detail: `剩余 ${formatBytes(user.quota_bytes - user.used_bytes)}。`,
    tone: 'success',
    percent,
  }
}

export function userNeedsQuotaAttention(user: Pick<User, 'quota_bytes' | 'used_bytes'>): boolean {
  const status = getQuotaStatus(user).status
  return status === 'warning' || status === 'exceeded'
}

function getUserQuotaPriority(status: QuotaStatus): number {
  if (status === 'exceeded') {
    return 0
  }
  if (status === 'warning') {
    return 1
  }
  if (status === 'normal') {
    return 2
  }
  return 3
}

function compareUsersByQuotaPriority<T extends UserQuotaAttentionUser>(left: T, right: T): number {
  const leftQuota = getQuotaStatus(left)
  const rightQuota = getQuotaStatus(right)
  const priorityDiff = getUserQuotaPriority(leftQuota.status) - getUserQuotaPriority(rightQuota.status)
  if (priorityDiff !== 0) {
    return priorityDiff
  }

  const leftPercent = leftQuota.percent ?? -1
  const rightPercent = rightQuota.percent ?? -1
  if (rightPercent !== leftPercent) {
    return rightPercent - leftPercent
  }

  return left.username.localeCompare(right.username)
}

export function getUserQuotaAttentionListItems<T extends UserQuotaAttentionUser>(users: T[]): T[] {
  return users
    .filter(userNeedsQuotaAttention)
    .sort(compareUsersByQuotaPriority)
}

export function getUserQuotaReportUsers<T extends UserQuotaReportUser>(users: T[]): T[] {
  return [...users].sort(compareUsersByQuotaPriority)
}

export function summarizeUserQuotas(users: UserQuotaReportUser[]): UserQuotaSummary {
  return users.reduce<UserQuotaSummary>((summary, user) => {
    const status = getQuotaStatus(user).status
    summary.totalCount += 1
    summary.usedBytes += user.used_bytes

    if (user.disabled) {
      summary.disabledCount += 1
    } else {
      summary.activeCount += 1
    }

    if (user.quota_bytes > 0) {
      summary.limitedCount += 1
      summary.limitedUsedBytes += user.used_bytes
      summary.quotaBytes += user.quota_bytes
    } else {
      summary.unlimitedCount += 1
    }

    if (status === 'normal') {
      summary.normalCount += 1
    } else if (status === 'warning') {
      summary.warningCount += 1
      summary.attentionCount += 1
    } else if (status === 'exceeded') {
      summary.exceededCount += 1
      summary.attentionCount += 1
    }

    return summary
  }, {
    totalCount: 0,
    activeCount: 0,
    disabledCount: 0,
    limitedCount: 0,
    unlimitedCount: 0,
    normalCount: 0,
    warningCount: 0,
    exceededCount: 0,
    attentionCount: 0,
    usedBytes: 0,
    limitedUsedBytes: 0,
    quotaBytes: 0,
  })
}

export function getUserQuotaAggregateStatus(summary: UserQuotaSummary): UserQuotaAggregateStatus {
  if (summary.quotaBytes <= 0) {
    return {
      label: '未设置总配额',
      detail: `当前没有已设配额用户；用户总用量 ${formatBytes(summary.usedBytes)}。`,
      tone: 'default',
      percent: null,
      usedBytes: summary.limitedUsedBytes,
      quotaBytes: summary.quotaBytes,
    }
  }

  const percent = Math.round((summary.limitedUsedBytes / summary.quotaBytes) * 100)
  if (summary.limitedUsedBytes > summary.quotaBytes) {
    return {
      label: '总体已超限',
      detail: `受限用户合计超出 ${formatBytes(summary.limitedUsedBytes - summary.quotaBytes)}。`,
      tone: 'danger',
      percent,
      usedBytes: summary.limitedUsedBytes,
      quotaBytes: summary.quotaBytes,
    }
  }

  const remainingBytes = summary.quotaBytes - summary.limitedUsedBytes
  if (percent >= 90) {
    return {
      label: '总体接近上限',
      detail: `受限用户合计剩余 ${formatBytes(remainingBytes)}。`,
      tone: 'warning',
      percent,
      usedBytes: summary.limitedUsedBytes,
      quotaBytes: summary.quotaBytes,
    }
  }

  return {
    label: '总体配额正常',
    detail: `受限用户合计剩余 ${formatBytes(remainingBytes)}。`,
    tone: 'success',
    percent,
    usedBytes: summary.limitedUsedBytes,
    quotaBytes: summary.quotaBytes,
  }
}

function formatUserRole(role: User['role']): string {
  if (role === 'admin') {
    return '管理员'
  }
  if (role === 'guest') {
    return '访客'
  }
  return '普通用户'
}

function getQuotaActionText(status: QuotaStatus): string {
  if (status === 'exceeded') {
    return '清理用户主目录、提高配额，或迁移部分数据。'
  }
  if (status === 'warning') {
    return '复核近期增长，必要时扩容或归档。'
  }
  if (status === 'unlimited') {
    return '如需限制长期占用，可设置用户配额。'
  }
  return '保持当前配额。'
}

function formatQuotaBalanceText(user: Pick<User, 'quota_bytes' | 'used_bytes'>): string {
  if (user.quota_bytes <= 0) {
    return '不限额'
  }
  if (user.used_bytes > user.quota_bytes) {
    return `超出 ${formatBytes(user.used_bytes - user.quota_bytes)}`
  }
  return `剩余 ${formatBytes(user.quota_bytes - user.used_bytes)}`
}

function formatReportEmail(email: string | undefined): string {
  return email && email.trim() ? email : '未设置'
}

function formatReportGroups(groups: string[] | undefined): string {
  return groups && groups.length > 0 ? groups.join(', ') : '未分组'
}

function formatReportHomeDir(homeDir: string | undefined): string {
  return homeDir && homeDir.trim() ? homeDir : '未设置'
}

function formatReportLastLogin(lastLoginAt: string | undefined): string {
  return lastLoginAt && lastLoginAt.trim() ? lastLoginAt : '从未登录'
}

export function formatUserQuotaSummaryReport(
  users: UserQuotaReportUser[],
  summary = summarizeUserQuotas(users),
): string {
  const headerRows = [
    ['用户总数', `${summary.totalCount} 个`],
    ['活跃用户', `${summary.activeCount} 个`],
    ['停用用户', `${summary.disabledCount} 个`],
    ['已设配额', `${summary.limitedCount} 个`],
    ['未设配额', `${summary.unlimitedCount} 个`],
    ['配额正常', `${summary.normalCount} 个`],
    ['接近上限', `${summary.warningCount} 个`],
    ['已超限', `${summary.exceededCount} 个`],
    ['需复核', `${summary.attentionCount} 个`],
    ['总用量', `${formatBytes(summary.usedBytes)} / ${summary.quotaBytes > 0 ? formatBytes(summary.quotaBytes) : '未设总配额'}`],
    ['受限用户用量', `${formatBytes(summary.limitedUsedBytes)} / ${summary.quotaBytes > 0 ? formatBytes(summary.quotaBytes) : '未设总配额'}`],
  ]
  const userRows = getUserQuotaReportUsers(users).map((user) => {
    const quota = getQuotaStatus(user)
    const quotaLimit = user.quota_bytes > 0 ? formatBytes(user.quota_bytes) : '不限额'
    const percent = quota.percent === null ? '不限额' : `${quota.percent}%`
    return [
      user.username,
      formatReportEmail(user.email),
      formatUserRole(user.role),
      user.disabled ? '已停用' : '启用',
      formatReportGroups(user.groups),
      formatReportHomeDir(user.home_dir),
      formatReportLastLogin(user.last_login_at),
      quota.label,
      `${formatBytes(user.used_bytes)} / ${quotaLimit}`,
      formatQuotaBalanceText(user),
      percent,
      getQuotaActionText(quota.status),
    ].join(' | ')
  })

  return [
    '用户配额摘要',
    ...headerRows.map(([label, value]) => `${label}：${value}`),
    '',
    '用户明细',
    '用户名 | 邮箱 | 角色 | 状态 | 用户组 | 主目录 | 最后登录 | 配额状态 | 用量 | 剩余/超出 | 占比 | 建议处理',
    ...userRows,
  ].join('\n')
}
