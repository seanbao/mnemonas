import { describe, expect, it } from 'vitest'
import {
  formatUserAccountAttentionReport,
  getUserAccountAttentionListItems,
  getUserAccountAttentionReportUsers,
  summarizeUserAccountAttention,
  userNeedsAccountAttention,
} from './userAccountAttention'

describe('userAccountAttention', () => {
  it('identifies disabled and never-login users as account attention items', () => {
    expect(userNeedsAccountAttention({ disabled: false, last_login_at: '2024-01-15T10:00:00Z' })).toBe(false)
    expect(userNeedsAccountAttention({ disabled: true, last_login_at: '2024-01-15T10:00:00Z' })).toBe(true)
    expect(userNeedsAccountAttention({ disabled: false, last_login_at: undefined })).toBe(true)
    expect(userNeedsAccountAttention({ disabled: true, last_login_at: undefined })).toBe(true)
  })

  it('orders account attention users by disabled state and username', () => {
    const users = [
      { username: 'active', disabled: false, last_login_at: '2024-01-15T10:00:00Z' },
      { username: 'z-disabled', disabled: true, last_login_at: '2024-01-15T10:00:00Z' },
      { username: 'a-disabled', disabled: true, last_login_at: undefined },
      { username: 'z-never-login', disabled: false, last_login_at: undefined },
      { username: 'a-never-login', disabled: false, last_login_at: undefined },
    ]

    expect(getUserAccountAttentionListItems(users).map((user) => user.username)).toEqual([
      'a-disabled',
      'z-disabled',
      'a-never-login',
      'z-never-login',
    ])
  })

  it('orders account attention report users before healthy accounts', () => {
    const users = [
      { username: 'healthy', role: 'user' as const, disabled: false, last_login_at: '2024-01-15T10:00:00Z' },
      { username: 'never', role: 'user' as const, disabled: false, last_login_at: undefined },
      { username: 'disabled', role: 'guest' as const, disabled: true, last_login_at: '2024-01-15T10:00:00Z' },
    ]

    expect(getUserAccountAttentionReportUsers(users).map((user) => user.username)).toEqual([
      'disabled',
      'never',
      'healthy',
    ])
  })

  it('summarizes account attention counts without double-counting overlap', () => {
    const users = [
      { disabled: false, last_login_at: '2024-01-15T10:00:00Z' },
      { disabled: true, last_login_at: '2024-01-15T10:00:00Z' },
      { disabled: false, last_login_at: undefined },
      { disabled: true, last_login_at: undefined },
    ]

    expect(summarizeUserAccountAttention(users)).toEqual({
      attentionCount: 3,
      disabledCount: 2,
      neverLoggedInCount: 2,
    })
  })

  it('formats an account attention report for administrator review', () => {
    const report = formatUserAccountAttentionReport([
      {
        username: 'owner',
        email: 'owner@example.com',
        role: 'admin' as const,
        disabled: false,
        groups: ['admins'],
        home_dir: '/',
        last_login_at: '2024-01-15T10:00:00Z',
      },
      {
        username: 'guest',
        role: 'guest' as const,
        disabled: true,
        groups: [],
        home_dir: '/guest/public',
        last_login_at: undefined,
      },
      {
        username: 'alice',
        email: 'alice@example.com',
        role: 'user' as const,
        disabled: false,
        groups: ['family'],
        home_dir: '/home/alice',
        last_login_at: undefined,
      },
    ])

    expect(report).toContain('用户账号复核摘要')
    expect(report).toContain('用户总数: 3 个')
    expect(report).toContain('需复核: 2 个')
    expect(report).toContain('停用账号: 1 个')
    expect(report).toContain('从未登录: 2 个')
    expect(report).toContain('用户名 | 邮箱 | 角色 | 状态 | 用户组 | 主目录 | 最后登录 | 账号关注 | 建议处理')
    expect(report).toContain('guest | 未设置 | 访客 | 已停用 | 未分组 | /guest/public | 从未登录 | 停用账号, 从未登录 | 确认账号是否仍需保留；如不再使用，可删除或保持停用。')
    expect(report).toContain('alice | alice@example.com | 普通用户 | 启用 | family | /home/alice | 从未登录 | 从未登录 | 确认账号是否已交付；必要时重置密码或删除未使用账号。')
    expect(report).toContain('owner | owner@example.com | 管理员 | 启用 | admins | / | 2024-01-15T10:00:00Z | 无 | 无需处理。')
    expect(report).toMatch(/guest[\s\S]*alice[\s\S]*owner/)
  })
})
