import type { User } from '@/api/users'
import { getUserAccessContext, getUserAccessReviewListItems } from '@/lib/userAccessContext'
import { getUserAccountAttentionListItems } from '@/lib/userAccountAttention'
import { getQuotaStatus, getUserQuotaAttentionListItems } from '@/lib/userQuota'
import { filterUsersBySearchQuery } from '@/lib/userSearch'

export type UserListFilter =
  | 'all'
  | 'admin'
  | 'active'
  | 'quota-attention'
  | 'account-attention'
  | 'disabled-account'
  | 'never-login'
  | 'access-review'
export type UserListSort = 'default' | 'username' | 'role' | 'quota-used' | 'last-login'

export type UserListView<T extends User> = {
  users: T[]
  filterLabel: string
  sortLabel: string
  filteredCount: number
  totalCount: number
  isSearchActive: boolean
  isFilterActive: boolean
  isSortActive: boolean
  hasActiveControls: boolean
  summaryText: string
}

export type UserListExportContext = {
  summaryText: string
  filterLabel: string
  sortLabel: string
  searchQuery?: string
}

const usernameCollator = new Intl.Collator('zh-Hans-CN', { numeric: true, sensitivity: 'base' })
const rolePriority: Record<User['role'], number> = {
  admin: 0,
  user: 1,
  guest: 2,
}
const roleLabels: Record<User['role'], string> = {
  admin: '管理员',
  user: '用户',
  guest: '访客',
}
const userListExportHeaders = [
  '用户ID',
  '用户名',
  '邮箱',
  '角色',
  '状态',
  '账号关注',
  '用户组',
  '主目录',
  '权限范围',
  '权限说明',
  '配额状态',
  '配额使用率',
  '配额说明',
  '已用字节',
  '配额字节',
  '复核提示',
  '最后登录',
  '创建时间',
  '更新时间',
]

export function getUserListFilterLabel(filter: UserListFilter): string {
  if (filter === 'admin') {
    return '管理员'
  }
  if (filter === 'active') {
    return '活跃用户'
  }
  if (filter === 'account-attention') {
    return '账号关注'
  }
  if (filter === 'disabled-account') {
    return '停用账号'
  }
  if (filter === 'never-login') {
    return '从未登录'
  }
  if (filter === 'quota-attention') {
    return '配额关注'
  }
  if (filter === 'access-review') {
    return '复核提示'
  }
  return '全部用户'
}

export function getUserListSortLabel(sort: UserListSort): string {
  if (sort === 'username') {
    return '用户名'
  }
  if (sort === 'role') {
    return '角色优先'
  }
  if (sort === 'quota-used') {
    return '容量用量'
  }
  if (sort === 'last-login') {
    return '最后登录'
  }
  return '默认顺序'
}

export function filterUsersByListFilter<T extends User>(users: T[], filter: UserListFilter): T[] {
  if (filter === 'admin') {
    return [...users].filter((user) => user.role === 'admin').sort(compareUsersByUsername)
  }
  if (filter === 'active') {
    return [...users].filter((user) => !user.disabled).sort(compareUsersByUsername)
  }
  if (filter === 'quota-attention') {
    return getUserQuotaAttentionListItems(users)
  }
  if (filter === 'account-attention') {
    return getUserAccountAttentionListItems(users)
  }
  if (filter === 'disabled-account') {
    return [...users].filter((user) => user.disabled).sort(compareUsersByUsername)
  }
  if (filter === 'never-login') {
    return [...users].filter((user) => !user.last_login_at).sort(compareUsersByUsername)
  }
  if (filter === 'access-review') {
    return getUserAccessReviewListItems(users)
  }
  return users
}

function compareUsersByUsername(left: User, right: User): number {
  return usernameCollator.compare(left.username, right.username)
}

function getLastLoginTimestamp(user: User): number | null {
  if (!user.last_login_at) {
    return null
  }

  const timestamp = Date.parse(user.last_login_at)
  return Number.isNaN(timestamp) ? null : timestamp
}

export function sortUsersForListView<T extends User>(users: T[], sort: UserListSort): T[] {
  if (sort === 'default') {
    return users
  }

  return [...users].sort((left, right) => {
    if (sort === 'username') {
      return compareUsersByUsername(left, right)
    }

    if (sort === 'role') {
      return rolePriority[left.role] - rolePriority[right.role] || compareUsersByUsername(left, right)
    }

    if (sort === 'quota-used') {
      return right.used_bytes - left.used_bytes || compareUsersByUsername(left, right)
    }

    const leftLogin = getLastLoginTimestamp(left)
    const rightLogin = getLastLoginTimestamp(right)
    if (leftLogin === null && rightLogin === null) {
      return compareUsersByUsername(left, right)
    }
    if (leftLogin === null) {
      return 1
    }
    if (rightLogin === null) {
      return -1
    }
    return rightLogin - leftLogin || compareUsersByUsername(left, right)
  })
}

function escapeCsvValue(value: string | number): string {
  const text = String(value)
  return /[",\n\r]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text
}

function formatCsvRow(values: Array<string | number>): string {
  return values.map(escapeCsvValue).join(',')
}

function formatAccountAttention(user: Pick<User, 'disabled' | 'last_login_at'>): string {
  const issues: string[] = []
  if (user.disabled) {
    issues.push('停用账号')
  }
  if (!user.last_login_at) {
    issues.push('从未登录')
  }
  return issues.length > 0 ? issues.join('; ') : '无'
}

function formatReviewHints(user: User): string {
  const context = getUserAccessContext(user)
  return context.reviewHints.length > 0 ? context.reviewHints.map((hint) => hint.label).join('; ') : '无'
}

export function buildUserListViewCsv(users: User[], context: UserListExportContext): string {
  const metadataRows = [
    formatCsvRow(['导出范围', context.summaryText]),
    formatCsvRow(['筛选', context.filterLabel]),
    formatCsvRow(['排序', context.sortLabel]),
  ]
  const searchQuery = context.searchQuery?.trim()
  if (searchQuery) {
    metadataRows.push(formatCsvRow(['搜索', searchQuery]))
  }

  const userRows = users.map((user) => {
    const accessContext = getUserAccessContext(user)
    const quota = getQuotaStatus(user)

    return formatCsvRow([
      user.id,
      user.username,
      user.email ?? '',
      roleLabels[user.role],
      user.disabled ? '已禁用' : '启用',
      formatAccountAttention(user),
      [...(user.groups ?? [])].sort().join('; '),
      user.home_dir,
      accessContext.scopeLabel,
      accessContext.scopeDescription,
      quota.label,
      quota.percent === null ? '不限额' : `${quota.percent}%`,
      quota.detail,
      user.used_bytes,
      user.quota_bytes > 0 ? user.quota_bytes : '不限额',
      formatReviewHints(user),
      user.last_login_at ?? '从未登录',
      user.created_at,
      user.updated_at,
    ])
  })

  return [
    ...metadataRows,
    '',
    formatCsvRow(userListExportHeaders),
    ...userRows,
  ].join('\n')
}

export function userListExportFilename(now = new Date()): string {
  const timestamp = now.toISOString().replaceAll(/[-:]/g, '').replace(/\.\d{3}Z$/, 'Z')
  return `mnemonas-users-${timestamp}.csv`
}

function getUserListViewSummaryText(
  filterLabel: string,
  totalCount: number,
  filteredCount: number,
  visibleCount: number,
  isSearchActive: boolean,
): string {
  if (totalCount === 0) {
    return '暂无用户'
  }

  if (!isSearchActive) {
    return filterLabel === '全部用户'
      ? `显示全部 ${totalCount} 个用户`
      : `${filterLabel} ${visibleCount} / ${totalCount} 个用户`
  }

  if (filterLabel === '全部用户') {
    return `搜索命中 ${visibleCount} / ${totalCount} 个用户`
  }

  return `${filterLabel}中搜索命中 ${visibleCount} / ${filteredCount} 个用户（全量 ${totalCount} 个）`
}

export function getUserListView<T extends User>(
  users: T[],
  filter: UserListFilter,
  searchQuery: string,
  sort: UserListSort = 'default',
): UserListView<T> {
  const filteredUsers = filterUsersByListFilter(users, filter)
  const visibleUsers = sortUsersForListView(filterUsersBySearchQuery(filteredUsers, searchQuery), sort)
  const filterLabel = getUserListFilterLabel(filter)
  const sortLabel = getUserListSortLabel(sort)
  const isSearchActive = searchQuery.trim().length > 0
  const isFilterActive = filter !== 'all'
  const isSortActive = sort !== 'default'
  const summaryText = getUserListViewSummaryText(
    filterLabel,
    users.length,
    filteredUsers.length,
    visibleUsers.length,
    isSearchActive,
  )

  return {
    users: visibleUsers,
    filterLabel,
    sortLabel,
    filteredCount: filteredUsers.length,
    totalCount: users.length,
    isSearchActive,
    isFilterActive,
    isSortActive,
    hasActiveControls: isSearchActive || isFilterActive || isSortActive,
    summaryText: isSortActive && users.length > 0 ? `${summaryText} · 排序：${sortLabel}` : summaryText,
  }
}
