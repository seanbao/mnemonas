import { describe, expect, it } from 'vitest'
import {
  buildUserListViewCsv,
  filterUsersByListFilter,
  getUserListFilterLabel,
  getUserListSortLabel,
  getUserListView,
  sortUsersForListView,
  userListExportFilename,
} from './userListView'

const baseUser = {
  id: 'user',
  email: '',
  role: 'user' as const,
  disabled: false,
  home_dir: '/home/user',
  created_at: '2024-01-01T00:00:00Z',
  updated_at: '2024-01-01T00:00:00Z',
  last_login_at: '2024-01-15T10:00:00Z',
  groups: [],
  quota_bytes: 1000,
  used_bytes: 100,
}

const users = [
  { ...baseUser, id: 'healthy', username: 'healthy', groups: ['family'] },
  { ...baseUser, id: 'never-login', username: 'neverlogin', groups: ['family'], last_login_at: undefined },
  { ...baseUser, id: 'near-quota', username: 'nearquota', groups: ['editors'], quota_bytes: 1000, used_bytes: 900 },
  { ...baseUser, id: 'admin', username: 'admin', role: 'admin' as const, home_dir: '/', quota_bytes: 0, used_bytes: 2048 },
]

describe('userListView', () => {
  it('labels list filters', () => {
    expect(getUserListFilterLabel('all')).toBe('全部用户')
    expect(getUserListFilterLabel('admin')).toBe('管理员')
    expect(getUserListFilterLabel('active')).toBe('活跃用户')
    expect(getUserListFilterLabel('account-attention')).toBe('账号关注')
    expect(getUserListFilterLabel('disabled-account')).toBe('停用账号')
    expect(getUserListFilterLabel('never-login')).toBe('从未登录')
    expect(getUserListFilterLabel('quota-attention')).toBe('配额关注')
    expect(getUserListFilterLabel('access-review')).toBe('复核提示')
  })

  it('labels list sorting options', () => {
    expect(getUserListSortLabel('default')).toBe('默认顺序')
    expect(getUserListSortLabel('username')).toBe('用户名')
    expect(getUserListSortLabel('role')).toBe('角色优先')
    expect(getUserListSortLabel('quota-used')).toBe('容量用量')
    expect(getUserListSortLabel('last-login')).toBe('最后登录')
  })

  it('filters users by the selected list filter', () => {
    const accountUsers = [
      ...users,
      { ...baseUser, id: 'disabled', username: 'disabled', disabled: true },
    ]

    expect(filterUsersByListFilter(users, 'all').map((user) => user.username)).toEqual([
      'healthy',
      'neverlogin',
      'nearquota',
      'admin',
    ])
    expect(filterUsersByListFilter(accountUsers, 'admin').map((user) => user.username)).toEqual([
      'admin',
    ])
    expect(filterUsersByListFilter(accountUsers, 'active').map((user) => user.username)).toEqual([
      'admin',
      'healthy',
      'nearquota',
      'neverlogin',
    ])
    expect(filterUsersByListFilter(accountUsers, 'account-attention').map((user) => user.username)).toEqual([
      'disabled',
      'neverlogin',
    ])
    expect(filterUsersByListFilter(accountUsers, 'disabled-account').map((user) => user.username)).toEqual([
      'disabled',
    ])
    expect(filterUsersByListFilter(accountUsers, 'never-login').map((user) => user.username)).toEqual([
      'neverlogin',
    ])
    expect(filterUsersByListFilter(users, 'quota-attention').map((user) => user.username)).toEqual(['nearquota'])
    expect(filterUsersByListFilter(users, 'access-review').map((user) => user.username)).toEqual([
      'nearquota',
      'neverlogin',
      'admin',
    ])
  })

  it('sorts visible users by the selected list sort', () => {
    expect(sortUsersForListView(users, 'default')).toBe(users)
    expect(sortUsersForListView(users, 'username').map((user) => user.username)).toEqual([
      'admin',
      'healthy',
      'nearquota',
      'neverlogin',
    ])
    expect(sortUsersForListView(users, 'role').map((user) => user.username)).toEqual([
      'admin',
      'healthy',
      'nearquota',
      'neverlogin',
    ])
    expect(sortUsersForListView(users, 'quota-used').map((user) => user.username)).toEqual([
      'admin',
      'nearquota',
      'healthy',
      'neverlogin',
    ])

    expect(sortUsersForListView([
      { ...baseUser, id: 'older', username: 'older', last_login_at: '2024-01-10T10:00:00Z' },
      { ...baseUser, id: 'never', username: 'never', last_login_at: undefined },
      { ...baseUser, id: 'newer', username: 'newer', last_login_at: '2024-02-10T10:00:00Z' },
    ], 'last-login').map((user) => user.username)).toEqual(['newer', 'older', 'never'])
  })

  it('builds summary text for all users', () => {
    expect(getUserListView(users, 'all', '')).toMatchObject({
      users,
      filterLabel: '全部用户',
      sortLabel: '默认顺序',
      filteredCount: 4,
      totalCount: 4,
      isSearchActive: false,
      isFilterActive: false,
      isSortActive: false,
      hasActiveControls: false,
      summaryText: '显示全部 4 个用户',
    })
  })

  it('builds summary text for active sorting', () => {
    expect(getUserListView(users, 'all', '', 'quota-used')).toMatchObject({
      users: [users[3], users[2], users[0], users[1]],
      sortLabel: '容量用量',
      filteredCount: 4,
      totalCount: 4,
      isSearchActive: false,
      isFilterActive: false,
      isSortActive: true,
      hasActiveControls: true,
      summaryText: '显示全部 4 个用户 · 排序：容量用量',
    })
  })

  it('builds a CSV export for the current list view', () => {
    const csv = buildUserListViewCsv([
      {
        ...baseUser,
        id: 'quoted-user',
        username: 'alice',
        email: 'alice,owner@example.com',
        groups: ['family', 'editors'],
        home_dir: '/family/"alice"',
        quota_bytes: 0,
        last_login_at: undefined,
      },
    ], {
      summaryText: '搜索命中 1 / 4 个用户 · 排序：用户名',
      filterLabel: '全部用户',
      sortLabel: '用户名',
      searchQuery: 'family',
    })

    expect(csv).toContain('导出范围,搜索命中 1 / 4 个用户 · 排序：用户名')
    expect(csv).toContain('搜索,family')
    expect(csv).toContain('用户ID,用户名,邮箱,角色,状态,账号关注,用户组,主目录,权限范围,权限说明,配额状态,配额使用率,配额说明,已用字节,配额字节,复核提示,最后登录,创建时间,更新时间')
    expect(csv).toContain('quoted-user,alice,"alice,owner@example.com",用户,启用,从未登录,editors; family,"/family/""alice"""')
    expect(csv).toContain('主目录 + 用户组范围,"主目录 /family/""alice""；用户组 editors, family，命中目录授权时可访问共享路径。"')
    expect(csv).toContain('未设配额,不限额,当前用户不受容量配额限制。,100,不限额,从未登录,从未登录')
  })

  it('builds a stable CSV export filename', () => {
    expect(userListExportFilename(new Date('2026-06-04T11:22:33.456Z'))).toBe('mnemonas-users-20260604T112233Z.csv')
  })

  it('builds summary text for focused filters and search', () => {
    expect(getUserListView(users, 'admin', '')).toMatchObject({
      filteredCount: 1,
      totalCount: 4,
      isSearchActive: false,
      isFilterActive: true,
      hasActiveControls: true,
      summaryText: '管理员 1 / 4 个用户',
    })

    expect(getUserListView(users, 'active', '')).toMatchObject({
      filteredCount: 4,
      totalCount: 4,
      isSearchActive: false,
      isFilterActive: true,
      hasActiveControls: true,
      summaryText: '活跃用户 4 / 4 个用户',
    })

    expect(getUserListView([
      ...users,
      { ...baseUser, id: 'disabled', username: 'disabled', disabled: true },
    ], 'disabled-account', '')).toMatchObject({
      filteredCount: 1,
      totalCount: 5,
      isSearchActive: false,
      isFilterActive: true,
      hasActiveControls: true,
      summaryText: '停用账号 1 / 5 个用户',
    })

    expect(getUserListView(users, 'never-login', '')).toMatchObject({
      filteredCount: 1,
      totalCount: 4,
      isSearchActive: false,
      isFilterActive: true,
      hasActiveControls: true,
      summaryText: '从未登录 1 / 4 个用户',
    })

    expect(getUserListView(users, 'access-review', '')).toMatchObject({
      filteredCount: 3,
      totalCount: 4,
      isSearchActive: false,
      isFilterActive: true,
      hasActiveControls: true,
      summaryText: '复核提示 3 / 4 个用户',
    })

    expect(getUserListView(users, 'access-review', 'family')).toMatchObject({
      filteredCount: 3,
      totalCount: 4,
      isSearchActive: true,
      isFilterActive: true,
      hasActiveControls: true,
      summaryText: '复核提示中搜索命中 1 / 3 个用户（全量 4 个）',
    })

    expect(getUserListView(users, 'all', 'family')).toMatchObject({
      filteredCount: 4,
      totalCount: 4,
      isSearchActive: true,
      isFilterActive: false,
      hasActiveControls: true,
      summaryText: '搜索命中 2 / 4 个用户',
    })
  })
})
