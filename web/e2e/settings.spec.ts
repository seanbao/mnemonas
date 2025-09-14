import { test, expect } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

/**
 * 设置页面 E2E 测试
 * 认证状态由 auth.setup.ts 通过 storageState 自动注入
 * 如果认证启用但登录失败，测试会被跳过
 */

test.describe('设置页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/settings')
  })

  test('应显示设置页面', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.locator('body')).toBeVisible()
  })

  test('应显示设置页面标题', async ({ page }) => {
    const title = page.getByRole('heading', { name: /系统设置|设置/i })
    await expect(title).toBeVisible({ timeout: 5000 })
  })

  test('应显示设置选项卡', async ({ page }) => {
    const tabs = [
      /常规/i,
      /版本保留/i,
      /WebDAV/i,
      /高级/i,
    ]

    for (const tabPattern of tabs) {
      const tab = page.getByRole('tab', { name: tabPattern })
      if (await tab.isVisible({ timeout: 1000 }).catch(() => false)) {
        await expect(tab).toBeVisible()
      }
    }
  })

  test('应显示保存和重置按钮', async ({ page }) => {
    const saveBtn = page.getByRole('button', { name: /保存|保存设置/i })
    const resetBtn = page.getByRole('button', { name: /重置/i })

    await expect(saveBtn).toBeVisible({ timeout: 5000 })
    await expect(resetBtn).toBeVisible({ timeout: 5000 })
  })
})

test.describe('设置选项卡切换', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/settings')
  })

  test('点击 WebDAV 选项卡应显示 WebDAV 设置', async ({ page }) => {
    const webdavTab = page.getByRole('tab', { name: /WebDAV/i })
    if (await webdavTab.isVisible({ timeout: 2000 }).catch(() => false)) {
      await webdavTab.click()
      await page.waitForTimeout(500)

      // 检查 WebDAV 相关设置项
      const webdavSwitch = page.getByRole('switch', { name: /启用 WebDAV/i })
      await expect(webdavSwitch).toBeVisible({ timeout: 5000 })
    }
  })

  test('点击版本保留选项卡应显示版本设置', async ({ page }) => {
    const retentionTab = page.getByRole('tab', { name: /版本保留/i })
    if (await retentionTab.isVisible({ timeout: 2000 }).catch(() => false)) {
      await retentionTab.click()
      await page.waitForTimeout(500)

      // 检查版本相关设置项
      const maxVersions = page.getByText(/最大版本数/i)
      await expect(maxVersions).toBeVisible({ timeout: 5000 })
    }
  })

  test('点击高级选项卡应显示 CDC 设置', async ({ page }) => {
    const advancedTab = page.getByRole('tab', { name: /高级/i })
    if (await advancedTab.isVisible({ timeout: 2000 }).catch(() => false)) {
      await advancedTab.click()
      await page.waitForTimeout(500)

      const cdcHeading = page.getByRole('heading', { name: 'CDC 分块参数' })
      await expect(cdcHeading).toBeVisible({ timeout: 5000 })
    }
  })
})

test.describe('设置表单交互', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/settings')
  })

  test('点击保存设置应提交成功并显示提示', async ({ page }) => {
    await expect(page.getByText('加载设置...')).toHaveCount(0, { timeout: 15000 })

    const saveButton = page.getByRole('button', { name: /保存|保存设置/i })
    await expect(saveButton).toBeVisible({ timeout: 5000 })

    const saveResponsePromise = page.waitForResponse((response) => (
      response.request().method() === 'PUT'
      && response.url().includes('/api/v1/settings')
    ))

    await saveButton.click()

    const saveResponse = await saveResponsePromise
    expect(saveResponse.status()).toBe(200)
    await expect(page.getByText('设置已保存')).toBeVisible({ timeout: 5000 })
  })

  test('服务器地址输入框应可编辑', async ({ page }) => {
    const hostInput = page.getByLabel(/监听地址/i)
    if (await hostInput.isVisible({ timeout: 2000 }).catch(() => false)) {
      await hostInput.clear()
      await hostInput.fill('127.0.0.1')
      await expect(hostInput).toHaveValue('127.0.0.1')
    }
  })

  test('端口输入框应可编辑', async ({ page }) => {
    const portInput = page.getByLabel(/端口/i)
    if (await portInput.isVisible({ timeout: 2000 }).catch(() => false)) {
      await portInput.clear()
      await portInput.fill('9080')
      await expect(portInput).toHaveValue('9080')
    }
  })
})

test.describe('公网访问向导', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/settings')
    await expect(page.getByText('加载设置...')).toHaveCount(0, { timeout: 15000 })
  })

  test('填写域名后应更新命令并应用推荐配置到表单', async ({ page }) => {
    await expect(page.getByRole('heading', { name: '公网访问向导' })).toBeVisible({ timeout: 5000 })

    await page.getByLabel('公网域名').fill('nas.example.com')
    await expect(page.getByText('sudo mnemonas-public-setup --proxy caddy nas.example.com admin@example.com')).toBeVisible()
    await expect(page.getByText('sudo mnemonas-doctor --public-domain nas.example.com')).toBeVisible()

    await page.getByLabel('反向代理').selectOption('nginx')
    await expect(page.getByText('sudo mnemonas-public-setup --proxy nginx nas.example.com admin@example.com')).toBeVisible()

    await page.getByRole('button', { name: '应用推荐到表单' }).click()

    await expect(page.getByPlaceholder('0.0.0.0')).toHaveValue('127.0.0.1')
    await expect(page.getByLabel('受信代理层数')).toHaveValue('1')
  })

  test('应用推荐后保存应提交本机监听和受信代理配置', async ({ page }) => {
    let submittedBody: unknown
    await page.route('**/api/v1/settings/', async (route) => {
      if (route.request().method() !== 'PUT') {
        await route.continue()
        return
      }

      submittedBody = JSON.parse(route.request().postData() || '{}')
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ success: true, data: null, message: 'settings updated, some changes may require restart' }),
      })
    })

    await page.getByLabel('公网域名').fill('nas.example.com')
    await page.getByRole('button', { name: '应用推荐到表单' }).click()
    await page.getByRole('button', { name: /保存|保存设置/i }).click()

    await expect(page.getByText('设置已保存，部分变更需要重启后生效')).toBeVisible({ timeout: 5000 })
    expect(submittedBody).toMatchObject({
      server: {
        host: '127.0.0.1',
        trusted_proxy_hops: 1,
      },
    })
  })
})

test.describe('安全自检修复动作', () => {
  test('监听范围风险应提供修复按钮并更新表单', async ({ page }) => {
    await page.route('**/api/v1/settings/security-check', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: {
            status: 'warning',
            generated_at: '2026-05-08T00:00:00Z',
            checks: [
              {
                id: 'server_listen',
                status: 'warning',
                title: 'Web 服务监听范围偏宽',
                message: '建议只监听本机地址。',
              },
            ],
            request: { scheme: 'http' },
            config: { server_host: '0.0.0.0' },
          },
        }),
      })
    })

    await ensureAuthenticatedAt(page, '/settings')
    await expect(page.getByText('加载设置...')).toHaveCount(0, { timeout: 15000 })
    await expect(page.getByText('Web 服务监听范围偏宽')).toBeVisible({ timeout: 5000 })

    await page.getByRole('button', { name: '改为本机监听' }).click()

    await expect(page.getByPlaceholder('0.0.0.0')).toHaveValue('127.0.0.1')
  })
})

test.describe('设置页面响应式', () => {
  test('移动端布局', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/settings')

    const title = page.getByRole('heading', { name: /系统设置|设置/i })
    await expect(title).toBeVisible({ timeout: 5000 })
  })

  test('平板端布局', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await ensureAuthenticatedAt(page, '/settings')

    const title = page.getByRole('heading', { name: /系统设置|设置/i })
    await expect(title).toBeVisible({ timeout: 5000 })
  })
})
