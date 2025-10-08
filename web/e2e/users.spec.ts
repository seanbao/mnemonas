import { test, expect } from '@playwright/test'
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
    quota_bytes: 0,
    used_bytes: 0,
  },
] as const

test.describe('用户管理页面', () => {
  test('用户卡片应直接显示主目录边界', async ({ page }) => {
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

    await ensureAuthenticatedAt(page, '/users')

    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.getByRole('heading', { name: '用户管理' })).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('heading', { name: '用户列表' })).toBeVisible()
    await expect(page.getByRole('button', { name: '配额关注', exact: true })).toBeVisible()
    await expect(page.getByText('1 个用户接近或超过上限')).toBeVisible()
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
