import { describe, expect, it } from 'vitest'
import type { Share } from '@/api/share'
import {
  formatShareReviewReport,
  getShareReviewReportShares,
  summarizeShareReview,
} from './shareReview'

function createShare(overrides: Partial<Share>): Share {
  return {
    id: 'share-id',
    path: '/docs/report.pdf',
    type: 'file',
    created_by: 'admin',
    created_at: '2026-06-01T00:00:00Z',
    has_password: true,
    permission: 'read',
    enabled: true,
    access_count: 0,
    max_access: 10,
    url: '/s/share-id',
    ...overrides,
  }
}

describe('shareReview', () => {
  it('summarizes enabled share review risks without counting disabled stale shares as active risk', () => {
    const shares = [
      createShare({
        id: 'root',
        path: '/',
        type: 'folder',
        has_password: false,
        max_access: 0,
        risk: {
          level: 'high',
          reasons: [
            { code: 'root_folder', level: 'high', message: '根目录分享' },
            { code: 'no_password', level: 'high', message: '未设置密码' },
          ],
        },
      }),
      createShare({
        id: 'expiring',
        path: '/docs/plan.pdf',
        expires_at: '2099-01-01T00:00:00Z',
        risk: {
          level: 'medium',
          reasons: [
            { code: 'expiring_soon', level: 'medium', message: '即将到期' },
            { code: 'unused_enabled', level: 'low', message: '长期未访问' },
          ],
        },
      }),
      createShare({
        id: 'disabled',
        path: '/archive/old.zip',
        enabled: false,
        has_password: false,
        risk: {
          level: 'high',
          reasons: [
            { code: 'no_password', level: 'high', message: '未设置密码' },
            { code: 'stale_enabled', level: 'low', message: '长期未访问' },
          ],
        },
      }),
    ]

    expect(summarizeShareReview(shares)).toEqual({
      totalCount: 3,
      enabledCount: 2,
      disabledCount: 1,
      reviewCount: 2,
      highRiskCount: 1,
      passwordlessCount: 1,
      broadCount: 1,
      expiringSoonCount: 1,
      staleCount: 1,
    })
  })

  it('orders report rows by review priority before path', () => {
    const normal = createShare({ id: 'normal', path: '/z-normal.txt', risk: { level: 'none' } })
    const high = createShare({ id: 'high', path: '/a-high.txt', risk: { level: 'high' } })
    const disabled = createShare({ id: 'disabled', path: '/b-disabled.txt', enabled: false })
    const medium = createShare({ id: 'medium', path: '/c-medium.txt', risk: { level: 'medium' } })
    const low = createShare({ id: 'low', path: '/d-low.txt', risk: { level: 'low' } })

    expect(getShareReviewReportShares([normal, high, disabled, medium, low]).map(share => share.id)).toEqual([
      'high',
      'medium',
      'low',
      'normal',
      'disabled',
    ])
  })

  it('formats a copyable share review report for administrator review', () => {
    const report = formatShareReviewReport([
      createShare({
        id: 'normal',
        path: '/docs/readme.md',
        access_count: 2,
        max_access: 10,
        risk: { level: 'none' },
      }),
      createShare({
        id: 'root',
        path: '/',
        type: 'folder',
        has_password: false,
        access_count: 5,
        max_access: 0,
        expires_at: null,
        risk: {
          level: 'high',
          reasons: [
            { code: 'root_folder', level: 'high', message: '' },
            { code: 'no_password', level: 'high', message: '未设置密码' },
          ],
        },
      }),
      createShare({
        id: 'disabled',
        path: '/archive/old.zip',
        enabled: false,
        risk: { level: 'none' },
      }),
    ], undefined, { pathFilter: '/docs' })

    expect(report).toContain('分享复核摘要')
    expect(report).toContain('分享总数：3 个')
    expect(report).toContain('启用分享：2 个')
    expect(report).toContain('停用分享：1 个')
    expect(report).toContain('需复核：1 个')
    expect(report).toContain('需处理：1 个')
    expect(report).toContain('路径筛选：/docs')
    expect(report).toContain('路径 | 类型 | 状态 | 风险等级 | 访问限制 | 访问次数 | 过期时间 | 风险原因 | 建议处理')
    expect(report).toContain('/ | 文件夹 | 启用 | 高风险 | 无密码 | 5 / 不限 | 永不过期 | 根目录分享会公开整个文件空间。；未设置密码 | 停用或补齐密码、有效期和访问次数限制。')
    expect(report).toContain('/docs/readme.md | 文件 | 启用 | 无 | 密码保护 | 2 / 10 | 永不过期 | 无 | 无需处理。')
    expect(report).toContain('/archive/old.zip | 文件 | 停用 | 无 | 密码保护 | 0 / 10 | 永不过期 | 无 | 确认是否仍需保留；不再使用时可删除。')
    expect(report).toMatch(/\/ \| 文件夹[\s\S]*\/docs\/readme\.md[\s\S]*\/archive\/old\.zip/)
  })
})
