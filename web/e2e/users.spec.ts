import { test, expect, type Page } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

const usersFixture = [
  {
    id: 'e2e-admin',
    username: 'admin',
    email: 'admin@example.com',
    role: 'admin',
    groups: ['operators'],
    disabled: false,
    home_dir: '/',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    last_login_at: '2026-01-02T00:00:00Z',
    must_change_password: false,
    quota_bytes: 0,
    used_bytes: 1048576,
  },
  {
    id: 'e2e-alice',
    username: 'alice',
    email: 'alice@example.com',
    role: 'user',
    groups: ['family'],
    disabled: false,
    home_dir: '/users/alice',
    created_at: '2026-01-03T00:00:00Z',
    updated_at: '2026-01-03T00:00:00Z',
    must_change_password: false,
    quota_bytes: 1073741824,
    used_bytes: 1006632960,
  },
  {
    id: 'e2e-reviewer',
    username: 'reviewer',
    email: 'reviewer@example.com',
    role: 'guest',
    groups: ['review'],
    disabled: true,
    home_dir: '/shared/review',
    created_at: '2026-01-04T00:00:00Z',
    updated_at: '2026-01-04T00:00:00Z',
    must_change_password: false,
    quota_bytes: 0,
    used_bytes: 0,
  },
] as const

async function routeUserList(page: Page) {
  await page.route('**/api/v1/admin/users/', async (route) => {
    if (route.request().method() !== 'GET') {
      await route.continue()
      return
    }

    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: {
          users: usersFixture,
          total: usersFixture.length,
        },
      }),
    })
  })
}

test.describe('用户管理页面', () => {
  test('用户卡片应直接显示主目录边界', async ({ page }) => {
    await routeUserList(page)

    await ensureAuthenticatedAt(page, '/users')

    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.getByRole('heading', { name: '用户管理' })).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('heading', { name: '用户列表' })).toBeVisible()
    await expect(page.getByRole('button', { name: '配额关注', exact: true })).toBeVisible()
    await expect(page.getByText('1 个用户接近或超过上限')).toBeVisible()
    const quotaTrend = page.getByRole('region', { name: '用户配额趋势' })
    await expect(quotaTrend).toBeVisible()
    await expect(quotaTrend.getByText('已记录首个快照')).toBeVisible()
    await expect(quotaTrend.getByText('960 MB / 1 GB')).toBeVisible()
    const quotaPermissionReview = page.getByRole('region', { name: '用户配额权限复核' })
    await expect(quotaPermissionReview).toBeVisible()
    await expect(quotaPermissionReview.getByText('配额与权限联合复核')).toBeVisible()
    await expect(quotaPermissionReview.getByText('需要联合复核')).toBeVisible()
    await expect(quotaPermissionReview.getByText('2 个用户需要结合配额、主目录和授权范围复核。')).toBeVisible()
    await expect(quotaPermissionReview.getByText('范围：主目录 + 用户组范围')).toBeVisible()
    await expect(quotaPermissionReview.getByText('配额接近上限且具备共享或全局访问范围')).toBeVisible()
    await expect(quotaPermissionReview.getByText('管理员未设置容量上限')).toBeVisible()
    await expect(page.getByText('alice', { exact: true })).toBeVisible()
    await expect(page.getByText('/users/alice', { exact: true })).toBeVisible()
    await expect(page.getByText('接近上限', { exact: true })).toBeVisible()
    await expect(page.getByLabel('alice 配额使用率')).toBeVisible()
    await expect(page.getByText('reviewer', { exact: true })).toBeVisible()
    await expect(page.getByText('/shared/review', { exact: true })).toBeVisible()

    await page.getByRole('button', { name: '查看配额关注用户' }).click()
    await expect(page.getByText('alice', { exact: true })).toBeVisible()
    await expect(page.getByText('admin', { exact: true })).toBeHidden()
    await expect(page.getByText('reviewer', { exact: true })).toBeHidden()

    await page.getByRole('button', { name: '查看全部用户' }).click()
    await expect(page.getByText('reviewer', { exact: true })).toBeVisible()
  })

  test('移动端用户列表不应被底部导航裁剪', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await routeUserList(page)

    await ensureAuthenticatedAt(page, '/users')

    await expect(page.getByRole('heading', { name: '用户管理' })).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('navigation', { name: '移动端主导航' })).toBeVisible()
    await expect.poll(
      async () => page.locator('main').evaluate((main) => main.scrollHeight > main.clientHeight),
      { message: '用户列表移动端内容应由主内容区滚动呈现' }
    ).toBe(true)

    const reviewerAction = page.getByRole('button', { name: 'reviewer 用户操作' })
    await reviewerAction.scrollIntoViewIfNeeded()
    await expect(reviewerAction).toBeVisible()
    await expect(page.getByText('/shared/review', { exact: true })).toBeVisible()

    const mobileNavBox = await page.getByRole('navigation', { name: '移动端主导航' }).boundingBox()
    const actionBox = await reviewerAction.boundingBox()

    expect(mobileNavBox).not.toBeNull()
    expect(actionBox).not.toBeNull()
    expect(actionBox!.y + actionBox!.height).toBeLessThanOrEqual(mobileNavBox!.y - 4)
  })

  test('创建用户时应可一次设置主目录和配额', async ({ page }) => {
    let submittedBody: unknown
    await page.route('**/api/v1/admin/users/', async (route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            success: true,
            data: {
              users: usersFixture,
              total: usersFixture.length,
            },
          }),
        })
        return
      }

      if (route.request().method() === 'POST') {
        submittedBody = JSON.parse(route.request().postData() || '{}')
        await route.fulfill({
          status: 201,
          contentType: 'application/json',
          body: JSON.stringify({
            success: true,
            data: {
              user: {
                id: 'e2e-newuser',
                username: 'newuser',
                email: 'newuser@example.com',
                role: 'user',
                groups: [],
                disabled: false,
                home_dir: '/team/newuser',
                created_at: '2026-01-05T00:00:00Z',
                updated_at: '2026-01-05T00:00:00Z',
                must_change_password: false,
                quota_bytes: 2147483648,
                used_bytes: 0,
              },
            },
          }),
        })
        return
      }

      await route.continue()
    })

    await ensureAuthenticatedAt(page, '/users')
    await page.getByRole('button', { name: '添加用户' }).click()

    await page.getByLabel('用户名').fill('newuser')
    await page.getByLabel('密码').fill('password123')
    await page.getByLabel('邮箱').fill('newuser@example.com')
    await page.getByLabel('主目录').fill('/team/newuser')
    await page.getByLabel('容量配额').fill('2')

    await page.getByRole('button', { name: '创建' }).click()
    await expect(page.getByText('用户创建成功')).toBeVisible({ timeout: 5000 })

    expect(submittedBody).toMatchObject({
      username: 'newuser',
      password: 'password123',
      email: 'newuser@example.com',
      role: 'user',
      groups: [],
      home_dir: '/team/newuser',
      quota_bytes: 2147483648,
    })
  })
})

test.describe('目录与访问页面', () => {
  test('目录权限矩阵复核记录应写入近期历史', async ({ page }) => {
    let persistedReview: Record<string, unknown> | null = null

    await page.route('**/api/v1/settings/access-report', async (route) => {
      expect(route.request().method()).toBe('POST')
      expect(JSON.parse(route.request().postData() || '{}')).toMatchObject({ path: '/team/readme.txt' })
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: {
            path: '/team/readme.txt',
            summary: {
              users: 2,
              read_allowed: 1,
              read_denied: 1,
              write_allowed: 1,
              write_denied: 1,
              related_shares: 1,
              active_related_shares: 1,
              password_protected_shares: 1,
            },
            users: [
              {
                username: 'alice',
                user_id: 'u1',
                role: 'user',
                groups: ['family'],
                home_dir: '/users/alice',
                path: '/team/readme.txt',
                read: {
                  mode: 'read',
                  allowed: true,
                  source: 'directory_access_rule',
                  matched_rule: {
                    path: '/team',
                    read_groups: ['family'],
                  },
                },
                write: {
                  mode: 'write',
                  allowed: true,
                  source: 'directory_access_rule',
                  matched_rule: {
                    path: '/team',
                    write_groups: ['family'],
                  },
                },
              },
              {
                username: 'bob',
                user_id: 'u2',
                role: 'user',
                groups: [],
                home_dir: '/users/bob',
                path: '/team/readme.txt',
                read: {
                  mode: 'read',
                  allowed: false,
                  source: 'home_dir',
                },
                write: {
                  mode: 'write',
                  allowed: false,
                  source: 'home_dir',
                },
              },
            ],
            rule_effects: [
              {
                path: '/team',
                index: 0,
                read_allowed: 1,
                read_denied: 0,
                write_allowed: 1,
                write_denied: 0,
                user_samples: ['alice'],
              },
            ],
            shares: [
              {
                id: 'share-1',
                path: '/team',
                type: 'folder',
                created_by: 'u1',
                relation: 'covers_path',
                enabled: true,
                active: true,
                has_password: true,
                access_count: 0,
                max_access: 0,
                url: '/s/share-1',
              },
            ],
          },
        }),
      })
    })
    await page.route('**/api/v1/settings/access-reviews*', async (route) => {
      const method = route.request().method()
      if (method === 'GET') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            success: true,
            data: {
              items: persistedReview ? [persistedReview] : [],
              total: persistedReview ? 1 : 0,
              limit: 5,
              offset: 0,
            },
          }),
        })
        return
      }
      if (method === 'POST') {
        const request = JSON.parse(route.request().postData() || '{}')
        persistedReview = {
          id: 'review-1',
          reviewed_at: '2026-07-18T00:00:00Z',
          reviewer: 'admin',
          ...request,
        }
        await route.fulfill({
          status: 201,
          contentType: 'application/json',
          body: JSON.stringify({ success: true, data: persistedReview }),
        })
        return
      }
      if (method === 'DELETE') {
        persistedReview = null
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ success: true, data: null, message: 'cleared' }),
        })
        return
      }
      await route.continue()
    })

    await ensureAuthenticatedAt(page, '/users?view=access')
    await expect(page.getByRole('tab', { name: '目录与访问' })).toHaveAttribute('aria-selected', 'true')
    await expect(page.getByLabel('目录与访问管理')).toBeVisible({ timeout: 5000 })

    await page.getByLabel('检查路径').fill('/team/readme.txt')
    await page.getByRole('button', { name: '用户矩阵' }).click()

    const matrix = page.getByLabel('目录权限用户矩阵')
    await expect(matrix).toBeVisible({ timeout: 5000 })
    await expect(matrix.getByText('/team/readme.txt')).toBeVisible()
    await expect(matrix.getByText('用户 2')).toBeVisible()
    const ruleEffects = matrix.getByLabel('用户矩阵规则生效明细')
    await expect(ruleEffects.getByText('规则 1 · /team')).toBeVisible()
    await expect(ruleEffects.getByText('读允许 1')).toBeVisible()
    await expect(ruleEffects.getByText('写允许 1')).toBeVisible()

    await page.evaluate(() => {
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText: async () => undefined },
      })
    })
    await page.getByRole('button', { name: '复制复核记录' }).click()

    const history = page.getByLabel('目录权限近期复核历史')
    await expect(history.getByText('/team/readme.txt')).toBeVisible({ timeout: 5000 })
    await expect(history.getByText('用户矩阵')).toBeVisible()
    await expect(history.getByText('用户 2')).toBeVisible()
    await expect(history.getByText('可读 1')).toBeVisible()
    await expect(history.getByText('可写 1')).toBeVisible()

    const storedHistory = await page.evaluate(() => {
      const storageKey = Object.keys(window.localStorage).find((key) => (
        key.startsWith('mnemonas_directory_access_review_history:')
      ))
      return storageKey ? JSON.parse(window.localStorage.getItem(storageKey) || '[]') : []
    })
    expect(storedHistory).toEqual([
      expect.objectContaining({
        title: '用户矩阵',
        path: '/team/readme.txt',
        preview: false,
        users: 2,
        readAllowed: 1,
        writeAllowed: 1,
        relatedShares: 1,
      }),
    ])

    await history.getByRole('button', { name: '清空近期记录' }).click()
    await expect(history.getByText('暂无近期目录权限复核记录。')).toBeVisible({ timeout: 5000 })
    expect(persistedReview).toBeNull()
  })
})
