import { describe, expect, it } from 'vitest'
import {
  createUserQuotaTrendPoint,
  formatUserQuotaSummaryReport,
  getUserQuotaAggregateStatus,
  getQuotaStatus,
  getQuotaUsagePercent,
  getUserQuotaAttentionListItems,
  getUserQuotaTrendSignature,
  mergeUserQuotaTrendHistory,
  normalizeUserQuotaTrendHistory,
  quotaBytesToFormValue,
  quotaFormValueToBytes,
  summarizeUserQuotaTrendHistory,
  summarizeUserQuotas,
  userNeedsQuotaAttention,
} from './userQuota'

describe('userQuota', () => {
  it('converts quota bytes to compact form values', () => {
    expect(quotaBytesToFormValue(0)).toEqual({ value: '0', unit: 'GB' })
    expect(quotaBytesToFormValue(-1)).toEqual({ value: '0', unit: 'GB' })
    expect(quotaBytesToFormValue(1024 ** 4)).toEqual({ value: '1', unit: 'TB' })
    expect(quotaBytesToFormValue(2 * 1024 ** 3)).toEqual({ value: '2', unit: 'GB' })
    expect(quotaBytesToFormValue(1536)).toEqual({ value: '1536', unit: 'B' })
  })

  it('converts quota form values to safe byte counts', () => {
    expect(quotaFormValueToBytes('', 'GB')).toBe(0)
    expect(quotaFormValueToBytes(' 2 ', 'GB')).toBe(2 * 1024 ** 3)
    expect(quotaFormValueToBytes('1.5', 'GB')).toBe(Math.round(1.5 * 1024 ** 3))
    expect(quotaFormValueToBytes('-1', 'GB')).toBeNull()
    expect(quotaFormValueToBytes('not-a-number', 'GB')).toBeNull()
    expect(quotaFormValueToBytes(String(Number.MAX_SAFE_INTEGER), 'TB')).toBeNull()
  })

  it('formats unlimited, normal, warning, and exceeded quota status', () => {
    expect(getQuotaStatus({ quota_bytes: 0, used_bytes: 999 })).toEqual({
      status: 'unlimited',
      label: '未设配额',
      detail: '当前用户不受容量配额限制。',
      tone: 'default',
      percent: null,
    })
    expect(getQuotaStatus({ quota_bytes: 1000, used_bytes: 500 })).toEqual({
      status: 'normal',
      label: '配额正常',
      detail: '剩余 500 B。',
      tone: 'success',
      percent: 50,
    })
    expect(getQuotaStatus({ quota_bytes: 1000, used_bytes: 900 })).toEqual({
      status: 'warning',
      label: '接近上限',
      detail: '剩余 100 B。',
      tone: 'warning',
      percent: 90,
    })
    expect(getQuotaStatus({ quota_bytes: 1000, used_bytes: 1250 })).toEqual({
      status: 'exceeded',
      label: '已超限',
      detail: '已超出 250 B。',
      tone: 'danger',
      percent: 125,
    })
  })

  it('identifies users that need quota attention', () => {
    expect(getQuotaUsagePercent({ quota_bytes: 0, used_bytes: 500 })).toBeNull()
    expect(getQuotaUsagePercent({ quota_bytes: 1000, used_bytes: 899 })).toBe(90)
    expect(userNeedsQuotaAttention({ quota_bytes: 0, used_bytes: 999 })).toBe(false)
    expect(userNeedsQuotaAttention({ quota_bytes: 1000, used_bytes: 500 })).toBe(false)
    expect(userNeedsQuotaAttention({ quota_bytes: 1000, used_bytes: 900 })).toBe(true)
    expect(userNeedsQuotaAttention({ quota_bytes: 1000, used_bytes: 1250 })).toBe(true)
  })

  it('orders quota attention users by severity, usage, and username', () => {
    const users = [
      { username: 'healthy', quota_bytes: 1000, used_bytes: 100 },
      { username: 'warning-low', quota_bytes: 1000, used_bytes: 900 },
      { username: 'warning-high', quota_bytes: 1000, used_bytes: 980 },
      { username: 'z-over', quota_bytes: 1000, used_bytes: 1200 },
      { username: 'a-over', quota_bytes: 1000, used_bytes: 1200 },
      { username: 'unlimited', quota_bytes: 0, used_bytes: 9999 },
    ]

    expect(getUserQuotaAttentionListItems(users).map((user) => user.username)).toEqual([
      'a-over',
      'z-over',
      'warning-high',
      'warning-low',
    ])
  })

  it('summarizes user quota state for administrator review', () => {
    const users = [
      { username: 'admin', role: 'admin' as const, disabled: false, quota_bytes: 1000, used_bytes: 100 },
      { username: 'alice', role: 'user' as const, disabled: false, quota_bytes: 1000, used_bytes: 900 },
      { username: 'bob', role: 'guest' as const, disabled: true, quota_bytes: 1000, used_bytes: 1200 },
      { username: 'media', role: 'user' as const, disabled: false, quota_bytes: 0, used_bytes: 2048 },
    ]

    expect(summarizeUserQuotas(users)).toEqual({
      totalCount: 4,
      activeCount: 3,
      disabledCount: 1,
      limitedCount: 3,
      unlimitedCount: 1,
      normalCount: 1,
      warningCount: 1,
      exceededCount: 1,
      attentionCount: 2,
      usedBytes: 4248,
      limitedUsedBytes: 2200,
      quotaBytes: 3000,
    })
  })

  it('summarizes aggregate quota headroom for limited users', () => {
    expect(getUserQuotaAggregateStatus({
      totalCount: 2,
      activeCount: 2,
      disabledCount: 0,
      limitedCount: 0,
      unlimitedCount: 2,
      normalCount: 0,
      warningCount: 0,
      exceededCount: 0,
      attentionCount: 0,
      usedBytes: 4096,
      limitedUsedBytes: 0,
      quotaBytes: 0,
    })).toEqual({
      label: '未设置总配额',
      detail: '当前没有已设配额用户；用户总用量 4 KB。',
      tone: 'default',
      percent: null,
      usedBytes: 0,
      quotaBytes: 0,
    })

    expect(getUserQuotaAggregateStatus({
      totalCount: 3,
      activeCount: 3,
      disabledCount: 0,
      limitedCount: 3,
      unlimitedCount: 0,
      normalCount: 1,
      warningCount: 2,
      exceededCount: 0,
      attentionCount: 2,
      usedBytes: 2760,
      limitedUsedBytes: 2760,
      quotaBytes: 3000,
    })).toEqual({
      label: '总体接近上限',
      detail: '受限用户合计剩余 240 B。',
      tone: 'warning',
      percent: 92,
      usedBytes: 2760,
      quotaBytes: 3000,
    })

    expect(getUserQuotaAggregateStatus({
      totalCount: 2,
      activeCount: 2,
      disabledCount: 0,
      limitedCount: 2,
      unlimitedCount: 0,
      normalCount: 0,
      warningCount: 0,
      exceededCount: 2,
      attentionCount: 2,
      usedBytes: 3400,
      limitedUsedBytes: 3400,
      quotaBytes: 3000,
    })).toEqual({
      label: '总体已超限',
      detail: '受限用户合计超出 400 B。',
      tone: 'danger',
      percent: 113,
      usedBytes: 3400,
      quotaBytes: 3000,
    })
  })

  it('formats a copyable user quota summary report', () => {
    const users = [
      { username: 'admin', email: 'admin@example.com', role: 'admin' as const, disabled: false, groups: ['admins'], home_dir: '/home/admin', quota_bytes: 1000, used_bytes: 100 },
      { username: 'alice', email: 'alice@example.com', role: 'user' as const, disabled: false, groups: ['family', 'editors'], home_dir: '/home/alice', last_login_at: '2024-01-15T10:00:00Z', quota_bytes: 1000, used_bytes: 900 },
      { username: 'bob', role: 'guest' as const, disabled: true, groups: [], home_dir: '/home/guest', quota_bytes: 1000, used_bytes: 1200 },
      { username: 'media', role: 'user' as const, disabled: false, quota_bytes: 0, used_bytes: 2048 },
    ]

    expect(formatUserQuotaSummaryReport(users)).toBe([
      '用户配额摘要',
      '用户总数：4 个',
      '活跃用户：3 个',
      '停用用户：1 个',
      '已设配额：3 个',
      '未设配额：1 个',
      '配额正常：1 个',
      '接近上限：1 个',
      '已超限：1 个',
      '需复核：2 个',
      '总用量：4.15 KB / 2.93 KB',
      '受限用户用量：2.15 KB / 2.93 KB',
      '',
      '用户明细',
      '用户名 | 邮箱 | 角色 | 状态 | 用户组 | 主目录 | 最后登录 | 配额状态 | 用量 | 剩余/超出 | 占比 | 建议处理',
      'bob | 未设置 | 访客 | 已停用 | 未分组 | /home/guest | 从未登录 | 已超限 | 1.17 KB / 1000 B | 超出 200 B | 120% | 清理用户主目录、提高配额，或迁移部分数据。',
      'alice | alice@example.com | 普通用户 | 启用 | family, editors | /home/alice | 2024-01-15T10:00:00Z | 接近上限 | 900 B / 1000 B | 剩余 100 B | 90% | 复核近期增长，必要时扩容或归档。',
      'admin | admin@example.com | 管理员 | 启用 | admins | /home/admin | 从未登录 | 配额正常 | 100 B / 1000 B | 剩余 900 B | 10% | 保持当前配额。',
      'media | 未设置 | 普通用户 | 启用 | 未分组 | 未设置 | 从未登录 | 未设配额 | 2 KB / 不限额 | 不限额 | 不限额 | 如需限制长期占用，可设置用户配额。',
    ].join('\n'))
  })

  it('creates stable user quota trend points from quota summaries', () => {
    const users = [
      { username: 'alice', role: 'user' as const, disabled: false, quota_bytes: 1000, used_bytes: 900 },
      { username: 'bob', role: 'guest' as const, disabled: true, quota_bytes: 1000, used_bytes: 1200 },
      { username: 'media', role: 'user' as const, disabled: false, quota_bytes: 0, used_bytes: 2048 },
    ]

    expect(createUserQuotaTrendPoint(users, '2024-01-02T00:00:00Z')).toEqual({
      capturedAt: '2024-01-02T00:00:00Z',
      totalCount: 3,
      activeCount: 2,
      limitedCount: 2,
      warningCount: 1,
      exceededCount: 1,
      attentionCount: 2,
      usedBytes: 4148,
      limitedUsedBytes: 2100,
      quotaBytes: 2000,
    })
  })

  it('derives quota trend signatures independent of response order', () => {
    const left = [
      { id: 'b', username: 'bob', disabled: false, quota_bytes: 1000, used_bytes: 200 },
      { id: 'a', username: 'alice', disabled: true, quota_bytes: 0, used_bytes: 100 },
    ]
    const right = [...left].reverse()

    expect(getUserQuotaTrendSignature(left)).toBe(getUserQuotaTrendSignature(right))
    expect(getUserQuotaTrendSignature([{ ...left[0], used_bytes: 201 }, left[1]])).not.toBe(getUserQuotaTrendSignature(left))
  })

  it('normalizes and merges quota trend history defensively', () => {
    const oldPoint = createUserQuotaTrendPoint([
      { username: 'alice', role: 'user' as const, disabled: false, quota_bytes: 1000, used_bytes: 100 },
    ], '2024-01-01T00:00:00Z')
    const newPoint = createUserQuotaTrendPoint([
      { username: 'alice', role: 'user' as const, disabled: false, quota_bytes: 1000, used_bytes: 500 },
    ], '2024-01-02T00:00:00Z')

    expect(normalizeUserQuotaTrendHistory([
      oldPoint,
      { capturedAt: 'invalid', limitedUsedBytes: 1 },
      newPoint,
    ])).toEqual([newPoint, oldPoint])
    expect(mergeUserQuotaTrendHistory([oldPoint], newPoint, 2)).toEqual([newPoint, oldPoint])
    expect(mergeUserQuotaTrendHistory([newPoint], { ...newPoint, capturedAt: '2024-01-03T00:00:00Z' }, 2)).toEqual([newPoint])
  })

  it('summarizes quota trend deltas for the administrator view', () => {
    const oldPoint = createUserQuotaTrendPoint([
      { username: 'alice', role: 'user' as const, disabled: false, quota_bytes: 1000, used_bytes: 100 },
    ], '2024-01-01T00:00:00Z')
    const newPoint = createUserQuotaTrendPoint([
      { username: 'alice', role: 'user' as const, disabled: false, quota_bytes: 1000, used_bytes: 950 },
    ], '2024-01-02T00:00:00Z')

    expect(summarizeUserQuotaTrendHistory([oldPoint])).toMatchObject({
      sampleCount: 1,
      label: '已记录首个快照',
      detail: '当前受限用量 100 B。',
      tone: 'success',
    })
    expect(summarizeUserQuotaTrendHistory([oldPoint, newPoint])).toMatchObject({
      latest: newPoint,
      previous: oldPoint,
      sampleCount: 2,
      limitedUsedDeltaBytes: 850,
      attentionDelta: 1,
      peakLimitedUsedBytes: 950,
      peakAttentionCount: 1,
      label: '受限用量增加',
      detail: '较上一快照 +850 B；复核用户 +1 个。',
      tone: 'warning',
    })
  })
})
