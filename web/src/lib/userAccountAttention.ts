import type { User } from '@/api/users'

type UserAccountAttentionUser = Pick<User, 'username' | 'disabled' | 'last_login_at'>
type UserAccountAttentionReportUser = Pick<User, 'username' | 'role' | 'disabled' | 'last_login_at'>
  & Partial<Pick<User, 'email' | 'groups' | 'home_dir'>>

export type UserAccountAttentionSummary = {
  attentionCount: number
  disabledCount: number
  neverLoggedInCount: number
}

export function userNeedsAccountAttention(user: Pick<User, 'disabled' | 'last_login_at'>): boolean {
  return user.disabled || !user.last_login_at
}

function compareUsersByAccountAttention<T extends UserAccountAttentionUser>(left: T, right: T): number {
  const leftPriority = left.disabled ? 0 : 1
  const rightPriority = right.disabled ? 0 : 1
  if (leftPriority !== rightPriority) {
    return leftPriority - rightPriority
  }
  return left.username.localeCompare(right.username)
}

export function getUserAccountAttentionListItems<T extends UserAccountAttentionUser>(users: T[]): T[] {
  return users
    .filter(userNeedsAccountAttention)
    .sort(compareUsersByAccountAttention)
}

export function getUserAccountAttentionReportUsers<T extends UserAccountAttentionReportUser>(users: T[]): T[] {
  const attentionUsers = getUserAccountAttentionListItems(users)
  const healthyUsers = users
    .filter((user) => !userNeedsAccountAttention(user))
    .sort((left, right) => left.username.localeCompare(right.username))
  return [...attentionUsers, ...healthyUsers]
}

export function summarizeUserAccountAttention(users: Pick<User, 'disabled' | 'last_login_at'>[]): UserAccountAttentionSummary {
  return users.reduce<UserAccountAttentionSummary>((summary, user) => {
    if (user.disabled) {
      summary.disabledCount += 1
    }
    if (!user.last_login_at) {
      summary.neverLoggedInCount += 1
    }
    if (userNeedsAccountAttention(user)) {
      summary.attentionCount += 1
    }
    return summary
  }, {
    attentionCount: 0,
    disabledCount: 0,
    neverLoggedInCount: 0,
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
  return groups && groups.length > 0 ? [...groups].sort().join(', ') : '未分组'
}

function formatReportHomeDir(homeDir: string | undefined): string {
  return homeDir && homeDir.trim() ? homeDir : '未设置'
}

function formatReportLastLogin(lastLoginAt: string | undefined): string {
  return lastLoginAt && lastLoginAt.trim() ? lastLoginAt : '从未登录'
}

function formatAccountAttentionReasons(user: Pick<User, 'disabled' | 'last_login_at'>): string {
  const reasons: string[] = []
  if (user.disabled) {
    reasons.push('停用账号')
  }
  if (!user.last_login_at) {
    reasons.push('从未登录')
  }
  return reasons.length > 0 ? reasons.join(', ') : '无'
}

function getAccountAttentionAction(user: Pick<User, 'disabled' | 'last_login_at'>): string {
  if (user.disabled && !user.last_login_at) {
    return '确认账号是否仍需保留；如不再使用，可删除或保持停用。'
  }
  if (user.disabled) {
    return '确认停用原因；必要时重新启用或删除账号。'
  }
  if (!user.last_login_at) {
    return '确认账号是否已交付；必要时重置密码或删除未使用账号。'
  }
  return '无需处理。'
}

export function formatUserAccountAttentionReport(
  users: UserAccountAttentionReportUser[],
  summary = summarizeUserAccountAttention(users),
): string {
  const headerRows = [
    ['用户总数', `${users.length} 个`],
    ['需复核', `${summary.attentionCount} 个`],
    ['停用账号', `${summary.disabledCount} 个`],
    ['从未登录', `${summary.neverLoggedInCount} 个`],
  ]
  const userRows = getUserAccountAttentionReportUsers(users).map((user) => [
    user.username,
    formatReportEmail(user.email),
    formatUserRole(user.role),
    user.disabled ? '已停用' : '启用',
    formatReportGroups(user.groups),
    formatReportHomeDir(user.home_dir),
    formatReportLastLogin(user.last_login_at),
    formatAccountAttentionReasons(user),
    getAccountAttentionAction(user),
  ].join(' | '))

  return [
    '用户账号复核摘要',
    ...headerRows.map(([label, value]) => `${label}：${value}`),
    '',
    '用户明细',
    '用户名 | 邮箱 | 角色 | 状态 | 用户组 | 主目录 | 最后登录 | 账号关注 | 建议处理',
    ...userRows,
  ].join('\n')
}
