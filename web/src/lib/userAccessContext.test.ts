import { describe, expect, it } from 'vitest'
import {
  formatUserAccessReviewReport,
  getUserAccessContext,
  getUserAccessReviewListItems,
  getUserAccessReviewReportUsers,
  summarizeUserAccessReview,
  userNeedsAccessReview,
} from './userAccessContext'

describe('userAccessContext', () => {
  it('describes administrator global access and unlimited quota review', () => {
    expect(getUserAccessContext({
      role: 'admin',
      disabled: false,
      home_dir: '/',
      groups: ['ops'],
      last_login_at: '2024-01-15T10:00:00Z',
      quota_bytes: 0,
      used_bytes: 2048,
    })).toEqual({
      scopeLabel: '管理员全局范围',
      scopeDescription: '可管理配置和全部文件入口；用户组 ops。',
      scopeTone: 'danger',
      reviewHints: [
        { key: 'admin-unlimited', label: '管理员不限额', tone: 'default' },
      ],
    })
  })

  it('describes user group access with sorted group names', () => {
    expect(getUserAccessContext({
      role: 'user',
      disabled: false,
      home_dir: '/home/alice',
      groups: ['editors', 'family'],
      last_login_at: '2024-01-15T10:00:00Z',
      quota_bytes: 1000,
      used_bytes: 500,
    })).toEqual({
      scopeLabel: '主目录 + 用户组范围',
      scopeDescription: '主目录 /home/alice；用户组 editors, family，命中目录授权时可访问共享路径。',
      scopeTone: 'primary',
      reviewHints: [],
    })
  })

  it('describes guest home access without groups', () => {
    expect(getUserAccessContext({
      role: 'guest',
      disabled: false,
      home_dir: '/guest/public',
      groups: [],
      last_login_at: '2024-01-15T10:00:00Z',
      quota_bytes: 1000,
      used_bytes: 100,
    })).toEqual({
      scopeLabel: '访客主目录范围',
      scopeDescription: '默认限制在 /guest/public；未加入用户组。',
      scopeTone: 'warning',
      reviewHints: [],
    })
  })

  it('combines account and quota review hints by severity source', () => {
    expect(getUserAccessContext({
      role: 'user',
      disabled: true,
      home_dir: '/home/bob',
      groups: [],
      quota_bytes: 1000,
      used_bytes: 1200,
    }).reviewHints).toEqual([
      { key: 'disabled', label: '复核停用账号', tone: 'warning' },
      { key: 'never-login', label: '从未登录', tone: 'warning' },
      { key: 'quota-exceeded', label: '配额已超限', tone: 'danger' },
    ])
  })

  it('summarizes users for access review', () => {
    const users = [
      { username: 'admin', role: 'admin' as const, disabled: false, home_dir: '/', groups: ['ops'], last_login_at: '2024-01-15T10:00:00Z', quota_bytes: 0, used_bytes: 2048 },
      { username: 'alice', role: 'user' as const, disabled: false, home_dir: '/home/alice', groups: ['family'], quota_bytes: 1000, used_bytes: 900 },
      { username: 'guest', role: 'guest' as const, disabled: true, home_dir: '/guest/public', groups: [], last_login_at: '2024-01-16T10:00:00Z', quota_bytes: 1000, used_bytes: 100 },
    ]

    expect(summarizeUserAccessReview(users)).toEqual({
      totalCount: 3,
      adminCount: 1,
      groupedCount: 2,
      homeOnlyCount: 1,
      disabledCount: 1,
      neverLoggedInCount: 1,
      reviewCount: 3,
      dangerReviewCount: 0,
      warningReviewCount: 2,
      noteReviewCount: 1,
    })
  })

  it('orders access review report users by actionable hints and username', () => {
    const users = [
      { username: 'healthy', role: 'user' as const, disabled: false, home_dir: '/home/healthy', groups: [], last_login_at: '2024-01-15T10:00:00Z', quota_bytes: 1000, used_bytes: 100 },
      { username: 'warning', role: 'user' as const, disabled: false, home_dir: '/home/warning', groups: [], quota_bytes: 1000, used_bytes: 900 },
      { username: 'z-over', role: 'user' as const, disabled: false, home_dir: '/home/z-over', groups: [], last_login_at: '2024-01-15T10:00:00Z', quota_bytes: 1000, used_bytes: 1200 },
      { username: 'a-over', role: 'user' as const, disabled: false, home_dir: '/home/a-over', groups: [], last_login_at: '2024-01-15T10:00:00Z', quota_bytes: 1000, used_bytes: 1200 },
      { username: 'admin', role: 'admin' as const, disabled: false, home_dir: '/', groups: [], last_login_at: '2024-01-15T10:00:00Z', quota_bytes: 0, used_bytes: 0 },
    ]

    expect(getUserAccessReviewReportUsers(users).map((user) => user.username)).toEqual([
      'a-over',
      'z-over',
      'warning',
      'admin',
      'healthy',
    ])
  })

  it('filters access review list items to users with review hints', () => {
    const users = [
      { username: 'healthy', role: 'user' as const, disabled: false, home_dir: '/home/healthy', groups: [], last_login_at: '2024-01-15T10:00:00Z', quota_bytes: 1000, used_bytes: 100 },
      { username: 'warning', role: 'user' as const, disabled: false, home_dir: '/home/warning', groups: [], quota_bytes: 1000, used_bytes: 900 },
      { username: 'admin', role: 'admin' as const, disabled: false, home_dir: '/', groups: [], last_login_at: '2024-01-15T10:00:00Z', quota_bytes: 0, used_bytes: 0 },
    ]

    expect(userNeedsAccessReview(users[0])).toBe(false)
    expect(userNeedsAccessReview(users[1])).toBe(true)
    expect(userNeedsAccessReview(users[2])).toBe(true)
    expect(getUserAccessReviewListItems(users).map((user) => user.username)).toEqual([
      'warning',
      'admin',
    ])
  })

  it('formats a copyable access review report', () => {
    const users = [
      { username: 'admin', email: 'admin@example.com', role: 'admin' as const, disabled: false, home_dir: '/', groups: ['ops'], last_login_at: '2024-01-15T10:00:00Z', quota_bytes: 0, used_bytes: 2048 },
      { username: 'alice', email: 'alice@example.com', role: 'user' as const, disabled: false, home_dir: '/home/alice', groups: ['family', 'editors'], quota_bytes: 1000, used_bytes: 900 },
      { username: 'guest', role: 'guest' as const, disabled: true, home_dir: '/guest/public', groups: [], last_login_at: '2024-01-16T10:00:00Z', quota_bytes: 1000, used_bytes: 1200 },
    ]

    expect(formatUserAccessReviewReport(users)).toBe([
      '用户权限复核摘要',
      '用户总数：3 个',
      '管理员：1 个',
      '含用户组：2 个',
      '仅主目录范围：1 个',
      '停用账号：1 个',
      '从未登录：1 个',
      '需复核：3 个',
      '严重复核：1 个',
      '提醒复核：1 个',
      '记录复核：1 个',
      '',
      '用户明细',
      '用户名 | 邮箱 | 角色 | 状态 | 用户组 | 主目录 | 权限范围 | 权限说明 | 复核提示 | 最后登录',
      'guest | 未设置 | 访客 | 已停用 | 未分组 | /guest/public | 访客主目录范围 | 默认限制在 /guest/public；未加入用户组。 | 复核停用账号, 配额已超限 | 2024-01-16T10:00:00Z',
      'alice | alice@example.com | 普通用户 | 启用 | editors, family | /home/alice | 主目录 + 用户组范围 | 主目录 /home/alice；用户组 editors, family，命中目录授权时可访问共享路径。 | 从未登录, 配额接近上限 | 从未登录',
      'admin | admin@example.com | 管理员 | 启用 | ops | / | 管理员全局范围 | 可管理配置和全部文件入口；用户组 ops。 | 管理员不限额 | 2024-01-15T10:00:00Z',
    ].join('\n'))
  })
})
