import type { User } from '@/api/users'
import { getQuotaStatus } from '@/lib/userQuota'

export type UserAccessContextTone = 'default' | 'primary' | 'warning' | 'danger'

export type UserAccessReviewHint = {
  key: string
  label: string
  tone: 'default' | 'warning' | 'danger'
}

export type UserAccessContext = {
  scopeLabel: string
  scopeDescription: string
  scopeTone: UserAccessContextTone
  reviewHints: UserAccessReviewHint[]
}

type UserAccessContextUser = Pick<User, 'role' | 'disabled' | 'home_dir' | 'groups' | 'last_login_at' | 'quota_bytes' | 'used_bytes'>
type UserAccessReportUser = Pick<User, 'username' | 'role' | 'disabled' | 'home_dir' | 'groups' | 'last_login_at' | 'quota_bytes' | 'used_bytes'>
  & Partial<Pick<User, 'email'>>

export type UserAccessReviewSummary = {
  totalCount: number
  adminCount: number
  groupedCount: number
  homeOnlyCount: number
  disabledCount: number
  neverLoggedInCount: number
  reviewCount: number
  dangerReviewCount: number
  warningReviewCount: number
  noteReviewCount: number
}

function getSortedGroups(groups: string[] | undefined): string[] {
  return [...(groups ?? [])]
    .map((group) => group.trim())
    .filter(Boolean)
    .sort((left, right) => left.localeCompare(right))
}

function formatGroupSummary(groups: string[]): string {
  if (groups.length === 0) {
    return '未加入用户组'
  }
  return `用户组 ${groups.join(', ')}`
}

function getAccessScope(user: UserAccessContextUser, groups: string[]): Pick<UserAccessContext, 'scopeLabel' | 'scopeDescription' | 'scopeTone'> {
  if (user.role === 'admin') {
    return {
      scopeLabel: '管理员全局范围',
      scopeDescription: `可管理配置和全部文件入口；${formatGroupSummary(groups)}。`,
      scopeTone: 'danger',
    }
  }

  if (groups.length > 0) {
    return {
      scopeLabel: user.role === 'guest' ? '访客组授权范围' : '主目录 + 用户组范围',
      scopeDescription: `主目录 ${user.home_dir}；${formatGroupSummary(groups)}，命中目录授权时可访问共享路径。`,
      scopeTone: user.role === 'guest' ? 'warning' : 'primary',
    }
  }

  return {
    scopeLabel: user.role === 'guest' ? '访客主目录范围' : '主目录范围',
    scopeDescription: `默认限制在 ${user.home_dir}；未加入用户组。`,
    scopeTone: user.role === 'guest' ? 'warning' : 'default',
  }
}

export function getUserAccessContext(user: UserAccessContextUser): UserAccessContext {
  const groups = getSortedGroups(user.groups)
  const quota = getQuotaStatus(user)
  const reviewHints: UserAccessReviewHint[] = []

  if (user.disabled) {
    reviewHints.push({ key: 'disabled', label: '复核停用账号', tone: 'warning' })
  }
  if (!user.last_login_at) {
    reviewHints.push({ key: 'never-login', label: '从未登录', tone: 'warning' })
  }
  if (quota.status === 'exceeded') {
    reviewHints.push({ key: 'quota-exceeded', label: '配额已超限', tone: 'danger' })
  } else if (quota.status === 'warning') {
    reviewHints.push({ key: 'quota-warning', label: '配额接近上限', tone: 'warning' })
  }
  if (user.role === 'admin' && quota.status === 'unlimited') {
    reviewHints.push({ key: 'admin-unlimited', label: '管理员不限额', tone: 'default' })
  }

  return {
    ...getAccessScope(user, groups),
    reviewHints,
  }
}

function getReviewPriority(user: UserAccessReportUser): number {
  const context = getUserAccessContext(user)
  if (context.reviewHints.some((hint) => hint.tone === 'danger')) {
    return 0
  }
  if (context.reviewHints.some((hint) => hint.tone === 'warning')) {
    return 1
  }
  if (context.reviewHints.length > 0) {
    return 2
  }
  return 3
}

function compareUsersByAccessReview(left: UserAccessReportUser, right: UserAccessReportUser): number {
  const priorityDiff = getReviewPriority(left) - getReviewPriority(right)
  if (priorityDiff !== 0) {
    return priorityDiff
  }

  return left.username.localeCompare(right.username)
}

export function userNeedsAccessReview(user: UserAccessReportUser): boolean {
  return getUserAccessContext(user).reviewHints.length > 0
}

export function getUserAccessReviewListItems<T extends UserAccessReportUser>(users: T[]): T[] {
  return users
    .filter(userNeedsAccessReview)
    .sort(compareUsersByAccessReview)
}

export function getUserAccessReviewReportUsers<T extends UserAccessReportUser>(users: T[]): T[] {
  return [...users].sort(compareUsersByAccessReview)
}

export function summarizeUserAccessReview(users: UserAccessReportUser[]): UserAccessReviewSummary {
  return users.reduce<UserAccessReviewSummary>((summary, user) => {
    const groups = getSortedGroups(user.groups)
    const context = getUserAccessContext(user)
    const hasDangerHint = context.reviewHints.some((hint) => hint.tone === 'danger')
    const hasWarningHint = context.reviewHints.some((hint) => hint.tone === 'warning')

    summary.totalCount += 1
    if (user.role === 'admin') {
      summary.adminCount += 1
    }
    if (groups.length > 0) {
      summary.groupedCount += 1
    } else {
      summary.homeOnlyCount += 1
    }
    if (user.disabled) {
      summary.disabledCount += 1
    }
    if (!user.last_login_at) {
      summary.neverLoggedInCount += 1
    }
    if (context.reviewHints.length > 0) {
      summary.reviewCount += 1
      if (hasDangerHint) {
        summary.dangerReviewCount += 1
      } else if (hasWarningHint) {
        summary.warningReviewCount += 1
      } else {
        summary.noteReviewCount += 1
      }
    }

    return summary
  }, {
    totalCount: 0,
    adminCount: 0,
    groupedCount: 0,
    homeOnlyCount: 0,
    disabledCount: 0,
    neverLoggedInCount: 0,
    reviewCount: 0,
    dangerReviewCount: 0,
    warningReviewCount: 0,
    noteReviewCount: 0,
  })
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

function formatReportEmail(email: string | undefined): string {
  return email && email.trim() ? email : '未设置'
}

function formatReportGroups(groups: string[] | undefined): string {
  const sortedGroups = getSortedGroups(groups)
  return sortedGroups.length > 0 ? sortedGroups.join(', ') : '未分组'
}

function formatReportLastLogin(lastLoginAt: string | undefined): string {
  return lastLoginAt && lastLoginAt.trim() ? lastLoginAt : '从未登录'
}

function formatReviewHints(context: UserAccessContext): string {
  return context.reviewHints.length > 0
    ? context.reviewHints.map((hint) => hint.label).join(', ')
    : '无'
}

export function formatUserAccessReviewReport(
  users: UserAccessReportUser[],
  summary = summarizeUserAccessReview(users),
): string {
  const headerRows = [
    ['用户总数', `${summary.totalCount} 个`],
    ['管理员', `${summary.adminCount} 个`],
    ['含用户组', `${summary.groupedCount} 个`],
    ['仅主目录范围', `${summary.homeOnlyCount} 个`],
    ['停用账号', `${summary.disabledCount} 个`],
    ['从未登录', `${summary.neverLoggedInCount} 个`],
    ['需复核', `${summary.reviewCount} 个`],
    ['严重复核', `${summary.dangerReviewCount} 个`],
    ['提醒复核', `${summary.warningReviewCount} 个`],
    ['记录复核', `${summary.noteReviewCount} 个`],
  ]
  const userRows = getUserAccessReviewReportUsers(users).map((user) => {
    const context = getUserAccessContext(user)
    return [
      user.username,
      formatReportEmail(user.email),
      formatUserRole(user.role),
      user.disabled ? '已停用' : '启用',
      formatReportGroups(user.groups),
      user.home_dir,
      context.scopeLabel,
      context.scopeDescription,
      formatReviewHints(context),
      formatReportLastLogin(user.last_login_at),
    ].join(' | ')
  })

  return [
    '用户权限复核摘要',
    ...headerRows.map(([label, value]) => `${label}：${value}`),
    '',
    '用户明细',
    '用户名 | 邮箱 | 角色 | 状态 | 用户组 | 主目录 | 权限范围 | 权限说明 | 复核提示 | 最后登录',
    ...userRows,
  ].join('\n')
}
