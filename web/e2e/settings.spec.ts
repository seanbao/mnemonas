import { test, expect, type Page } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'
import { routeBackupJobs } from './helpers/backups'
import { waitForRouteSettled } from './helpers/route-ready'

async function selectSettingsCategory(page: Page, value: string, tabName: RegExp) {
  const mobileSelector = page.locator('select#settings-mobile-category')
  if (await mobileSelector.isVisible().catch(() => false)) {
    await mobileSelector.selectOption(value)
    return
  }
  await page.getByRole('tab', { name: tabName }).click()
}

/**
 * Settings page E2E tests.
 * Authentication state is injected by auth.setup.ts through storageState.
 * Login setup failures fail by default; protected-page tests skip only when
 * auth skipping is explicitly enabled for reused environments.
 */

test.describe('设置页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/settings')
  })

  test('应显示设置页面', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.getByRole('heading', { name: /设置/i })).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('heading', { name: '按使用目标调整设备' })).toBeVisible()
    await expect(page.getByRole('button', { name: /账户与远程访问/ })).toBeVisible()
  })

  test('应显示设置页面标题', async ({ page }) => {
    const title = page.getByRole('heading', { name: /设置/i })
    await expect(title).toBeVisible({ timeout: 5000 })
  })

  test('应显示任务导向的设置入口', async ({ page }) => {
    const tasks = [
      /账户与远程访问/i,
      /数据保护与权限/i,
      /设备挂载/i,
      /设备状态与通知/i,
      /分享与协作/i,
    ]

    for (const taskPattern of tasks) {
      await expect(page.getByRole('button', { name: taskPattern })).toBeVisible({ timeout: 5000 })
    }
  })

  test('移动端应完整显示所有设置任务入口', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await ensureAuthenticatedAt(page, '/settings')

    const taskNames = ['账户与远程访问', '数据保护与权限', '设备挂载', '设备状态与通知', '分享与协作']
    for (const taskName of taskNames) {
      const task = page.getByRole('button', { name: new RegExp(taskName) })
      await task.scrollIntoViewIfNeeded()
      await expect(task).toBeVisible({ timeout: 5000 })
      const box = await task.boundingBox()
      expect(box, `${taskName} task should have a layout box`).not.toBeNull()
      expect(Math.round((box?.x ?? 0) + (box?.width ?? 0))).toBeLessThanOrEqual(390)
      expect(Math.round(box?.x ?? 0)).toBeGreaterThanOrEqual(0)
    }
  })

  test('进入分类后应显示保存和重置按钮', async ({ page }) => {
    await page.getByRole('button', { name: /账户与远程访问/ }).click()
    const saveBtn = page.getByRole('button', { name: /保存|保存设置/i })
    const resetBtn = page.getByRole('button', { name: /重置/i })

    await expect(saveBtn).toBeVisible({ timeout: 5000 })
    await expect(resetBtn).toBeVisible({ timeout: 5000 })
  })
})

test.describe('设置选项卡切换', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/settings?tab=general')
    await waitForRouteSettled(page, '/settings')
  })

  test('点击 WebDAV 选项卡应显示 WebDAV 设置', async ({ page }) => {
    await selectSettingsCategory(page, 'webdav', /设备挂载/i)

    const webdavSwitch = page.getByRole('switch', { name: /启用 WebDAV/i })
    await expect(webdavSwitch).toBeVisible({ timeout: 5000 })
    await expect(page.getByText('日常或生产挂载优先使用 MnemoNAS 用户账号；Basic Auth 仅用于旧客户端或专用服务凭据')).toBeVisible()

    await page.getByLabel('WebDAV 认证方式').selectOption('users')
    await expect(page.getByLabel('WebDAV 用户账号认证说明')).toBeVisible()
    await expect(page.getByText('WebDAV 登录会复用已启用用户账号，并继续受用户状态和目录权限限制。')).toBeVisible()
    await expect(page.locator('input[aria-label="WebDAV Basic Auth 用户名"]')).toHaveCount(0)
    await expect(page.locator('input[aria-label="WebDAV Basic Auth 密码"]')).toHaveCount(0)
  })

  test('点击版本保留选项卡应显示版本设置', async ({ page }) => {
    await selectSettingsCategory(page, 'retention', /数据保护/i)

    const maxVersions = page.getByText(/最大版本数/i)
    await expect(maxVersions).toBeVisible({ timeout: 5000 })
    await expect(page.getByText('路径直接填写 MnemoNAS 逻辑路径')).toBeVisible()
  })

  test('目录权限矩阵复核记录应写入近期历史', async ({ page }) => {
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

    await selectSettingsCategory(page, 'retention', /数据保护/i)
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
  })

  test('点击分享选项卡应显示分享策略覆盖摘要', async ({ page }) => {
    await selectSettingsCategory(page, 'shares', /分享与协作/i)

    const coverage = page.getByLabel('分享策略覆盖摘要')
    await expect(coverage.getByText('分享策略覆盖摘要')).toBeVisible({ timeout: 5000 })
    await expect(coverage.getByText('策略关注项')).toBeVisible()
    await expect(page.getByText('分享策略路径填写 MnemoNAS 逻辑路径')).toBeVisible()
  })
})

test.describe('设置表单交互', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/settings?tab=general')
  })

  test('点击保存设置应提交成功并显示提示', async ({ page }) => {
    await waitForRouteSettled(page, '/settings')

    const saveButton = page.getByRole('button', { name: /保存|保存设置/i })
    await expect(saveButton).toBeVisible({ timeout: 5000 })

    const saveResponsePromise = page.waitForResponse((response) => (
      response.request().method() === 'PUT'
      && response.url().includes('/api/v1/settings')
    ))

    await saveButton.click()

    const saveResponse = await saveResponsePromise
    expect(saveResponse.status()).toBe(200)
    const submittedBody = saveResponse.request().postDataJSON() as Record<string, unknown>
    expect(submittedBody).not.toHaveProperty('dataplane')
    expect(submittedBody).not.toHaveProperty('cdc')
    expect(submittedBody).not.toHaveProperty('disk_health')
    expect(submittedBody).not.toHaveProperty('alerts')
    expect(submittedBody).not.toHaveProperty('favorites')
    await expect(page.getByText('设置已保存')).toBeVisible({ timeout: 5000 })
  })

  test('WebDAV Basic Auth 可切回自动生成密码', async ({ page }) => {
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
        body: JSON.stringify({ success: true, data: null, message: 'settings updated' }),
      })
    })

    await waitForRouteSettled(page, '/settings')
    await selectSettingsCategory(page, 'webdav', /设备挂载/i)

    const webdavSwitch = page.getByRole('switch', { name: /启用 WebDAV/i })
    if (!(await webdavSwitch.isChecked())) {
      await webdavSwitch.click()
    }
    await page.getByLabel('WebDAV 认证方式').selectOption('basic')

    const generatedPassword = page.getByLabel('保存时使用自动生成密码')
    await expect(generatedPassword).toBeEnabled({ timeout: 5000 })
    await generatedPassword.check()
    await expect(generatedPassword).toBeChecked()

    await page.getByRole('button', { name: /保存|保存设置/i }).click()
    await expect(page.getByText('设置已保存')).toBeVisible({ timeout: 5000 })

    expect(submittedBody).toMatchObject({
      webdav: {
        enabled: true,
        auth_type: 'basic',
        password: '',
      },
    })
  })

  test('服务器地址输入框应可编辑', async ({ page }) => {
    await waitForRouteSettled(page, '/settings')
    await page.getByText('专业网络参数').click()

    const hostInput = page.getByLabel('服务器监听地址')
    await expect(hostInput).toBeVisible({ timeout: 5000 })
    await hostInput.clear()
    await hostInput.fill('127.0.0.1')
    await expect(hostInput).toHaveValue('127.0.0.1')
  })

  test('端口输入框应可编辑', async ({ page }) => {
    await waitForRouteSettled(page, '/settings')
    await page.getByText('专业网络参数').click()

    const portInput = page.getByLabel('服务器端口')
    await expect(portInput).toBeVisible({ timeout: 5000 })
    await portInput.clear()
    await portInput.fill('9080')
    await expect(portInput).toHaveValue('9080')
  })
})

test.describe('公网访问向导', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/settings?tab=general')
    await waitForRouteSettled(page, '/settings')
  })

  test('填写域名后应更新命令并应用推荐配置到表单', async ({ page }) => {
    await expect(page.getByRole('heading', { name: '公网访问向导' })).toBeVisible({ timeout: 5000 })

    await page.getByLabel('公网域名').fill('nas.example.com')
    await expect(page.getByText('sudo mnemonas-public-setup --proxy caddy nas.example.com admin@example.com')).toBeVisible()
    await expect(page.getByText('sudo mnemonas-doctor --public-domain nas.example.com')).toBeVisible()

    await page.getByLabel('反向代理').selectOption('nginx')
    await expect(page.getByText('sudo mnemonas-public-setup --proxy nginx nas.example.com admin@example.com')).toBeVisible()

    await page.getByRole('button', { name: '应用推荐到表单' }).click()

    await expect(page.getByLabel('服务器监听地址')).toHaveValue('127.0.0.1')
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

  test('应用推荐后保存应提交公网分享和会话安全默认值', async ({ page }) => {
    let submittedBody: unknown
    await page.route('**/api/v1/settings/', async (route) => {
      const method = route.request().method()
      if (method === 'GET') {
        const response = await route.fetch()
        const body = await response.json() as {
          data: {
            auth?: Record<string, unknown>
            share?: Record<string, unknown>
          }
        }
        await route.fulfill({
          status: response.status(),
          contentType: 'application/json',
          json: {
            ...body,
            data: {
              ...body.data,
              auth: {
                ...body.data.auth,
                access_token_ttl: '2h0m0s',
                refresh_token_ttl: '1080h0m0s',
              },
              share: {
                ...body.data.share,
                enabled: true,
                base_url: '',
                default_expires_in: '0',
                default_max_access: 0,
              },
            },
          },
        })
        return
      }
      if (method !== 'PUT') {
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

    await page.reload()
    await waitForRouteSettled(page, '/settings')
    await page.getByLabel('公网域名').fill('nas.example.com')
    await page.getByRole('button', { name: '应用推荐到表单' }).click()
    await page.getByRole('button', { name: /保存|保存设置/i }).click()

    await expect(page.getByText('设置已保存，部分变更需要重启后生效')).toBeVisible({ timeout: 5000 })
    expect(submittedBody).toMatchObject({
      server: {
        host: '127.0.0.1',
        trusted_proxy_hops: 1,
      },
      auth: {
        access_token_ttl: '1h',
        refresh_token_ttl: '720h',
      },
      share: {
        enabled: true,
        base_url: 'https://nas.example.com',
        default_expires_in: '168h',
        default_max_access: 20,
      },
    })
    await page.unrouteAll({ behavior: 'ignoreErrors' })
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
    await ensureAuthenticatedAt(page, '/settings?tab=general')
    await waitForRouteSettled(page, '/settings')
    await expect(page.getByText('Web 服务监听范围偏宽')).toBeVisible({ timeout: 5000 })

    await page.getByRole('button', { name: '改为本机监听' }).click()

    await expect(page.getByLabel('服务器监听地址')).toHaveValue('127.0.0.1')
  })

  test('公开分享边界异常不应显示代理修复按钮', async ({ page }) => {
    await page.route('**/api/v1/settings/security-check', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: {
            status: 'block',
            generated_at: '2026-05-08T00:00:00Z',
            checks: [
              {
                id: 'public_share_boundary',
                status: 'block',
                title: '公开分享浏览器边界异常',
                message: 'backend raw public share boundary block detail',
                details: {
                  share_enabled: true,
                  password_cookie_secure: true,
                  password_cookie_same_site: 'Strict',
                  metadata_vary_cookie: false,
                },
              },
            ],
            request: { scheme: 'https' },
            config: { auth_enabled: true, trusted_proxy_hops: 1 },
          },
        }),
      })
    })

    await ensureAuthenticatedAt(page, '/settings?tab=general')
    await waitForRouteSettled(page, '/settings')
    await expect(page.getByText('公开分享浏览器边界异常')).toBeVisible({ timeout: 5000 })
    await expect(page.getByText('公开分享访问 cookie、失败限速或缓存边界未满足公网安全要求。')).toBeVisible()
    await expect(page.getByText('backend raw public share boundary block detail')).toHaveCount(0)
    await expect(page.getByRole('button', { name: '应用代理推荐' })).toHaveCount(0)
  })

  test('路径和本地备份风险应提供可执行提示', async ({ page }) => {
    await page.route('**/api/v1/settings/security-check', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          success: true,
          data: {
            status: 'block',
            generated_at: '2026-05-08T00:00:00Z',
            checks: [
              {
                id: 'config_file_access',
                status: 'block',
                title: '配置文件路径包含符号链接',
                message: 'backend raw config file detail',
                details: {
                  path: '/srv/mnemonas/config/config.toml',
                  path_kind: 'symlink_component',
                  symlink_component: '/srv/mnemonas/config',
                },
              },
              {
                id: 'secrets_file_access',
                status: 'block',
                title: '自动 WebDAV 凭据路径包含符号链接',
                message: 'backend raw secrets file detail',
                details: {
                  path: '/srv/mnemonas/data/secrets.json',
                  path_kind: 'symlink_component',
                  symlink_component: '/srv/mnemonas/data',
                  generated_webdav_password_required: true,
                },
              },
              {
                id: 'backup_local_destinations',
                status: 'block',
                title: '本地备份目标位于主存储内',
                message: 'backend raw backup destination detail',
                details: {
                  job_id: 'external-disk',
                  destination: '/srv/mnemonas/data/backups',
                  source: '/srv/mnemonas/data',
                  storage_root: '/srv/mnemonas/data',
                  destination_kind: 'inside_storage_root',
                },
              },
            ],
            request: { scheme: 'https' },
            config: { auth_enabled: true, webdav_enabled: true, webdav_auth_type: 'basic' },
          },
        }),
      })
    })
    await routeBackupJobs(page)

    await ensureAuthenticatedAt(page, '/settings?tab=general')
    await waitForRouteSettled(page, '/settings')
    await expect(page.getByText('配置文件路径包含符号链接')).toBeVisible({ timeout: 5000 })
    await expect(page.getByText('自动 WebDAV 凭据路径包含符号链接')).toBeVisible()
    await expect(page.getByText('本地备份目标位于主存储内')).toBeVisible()
    await expect(page.getByText('backend raw config file detail')).toHaveCount(0)
    await expect(page.getByText('backend raw secrets file detail')).toHaveCount(0)
    await expect(page.getByText('backend raw backup destination detail')).toHaveCount(0)

    await page.getByRole('button', { name: '查看配置路径' }).click()
    await expect(page.getByText('需要检查配置文件路径')).toBeVisible({ timeout: 5000 })
    await expect(page.getByText(/\/srv\/mnemonas\/config\/config\.toml 是普通文件/)).toBeVisible()

    await page.getByRole('button', { name: '查看凭据路径' }).click()
    await expect(page.getByText('需要检查自动 WebDAV 凭据')).toBeVisible({ timeout: 5000 })
    await expect(page.getByText(/\/srv\/mnemonas\/data\/secrets\.json 是普通文件/)).toBeVisible()

    await page.getByRole('button', { name: '查看备份目标' }).click()
    await expect(page.getByText('需要检查本地备份目标')).toBeVisible({ timeout: 5000 })
    await expect(page).toHaveURL(/\/maintenance\?backupJob=external-disk/)
    const focusedBackupJob = page.getByRole('group', { name: '外置硬盘备份 备份任务，安全自检定位' })
    await expect(focusedBackupJob).toBeVisible({ timeout: 5000 })
    await expect(focusedBackupJob.getByText('安全自检定位')).toBeVisible()
  })
})

test.describe('设置页面响应式', () => {
  test('移动端布局', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/settings')

    const title = page.getByRole('heading', { name: /设置/i })
    await expect(title).toBeVisible({ timeout: 5000 })
  })

  test('移动端公网访问向导主要操作可用且不产生横向溢出', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await ensureAuthenticatedAt(page, '/settings?tab=general')
    await waitForRouteSettled(page, '/settings')

    const wizardHeading = page.getByRole('heading', { name: '公网访问向导' })
    await wizardHeading.scrollIntoViewIfNeeded()
    await expect(wizardHeading).toBeVisible({ timeout: 5000 })

    await page.getByLabel('公网域名').fill('nas.example.com')
    await expect(page.getByText('sudo mnemonas-public-setup --proxy caddy nas.example.com admin@example.com')).toBeVisible()
    await expect(page.getByText('sudo mnemonas-doctor --public-domain nas.example.com')).toBeVisible()

    await page.getByRole('button', { name: '应用推荐到表单' }).click()
    await expect(page.getByLabel('受信代理层数')).toHaveValue('1')

    const horizontalOverflow = await page.evaluate(() => {
      const pageWidth = Math.max(document.documentElement.scrollWidth, document.body.scrollWidth)
      return pageWidth - window.innerWidth
    })
    expect(horizontalOverflow).toBeLessThanOrEqual(2)
  })

  test('移动端公网访问向导可修复公开分享默认策略', async ({ page }) => {
    let submittedBody: unknown
    await page.route('**/api/v1/settings/', async (route) => {
      const method = route.request().method()
      if (method === 'GET') {
        const response = await route.fetch()
        const body = await response.json() as {
          data: {
            share?: Record<string, unknown>
          }
        }
        await route.fulfill({
          status: response.status(),
          contentType: 'application/json',
          json: {
            ...body,
            data: {
              ...body.data,
              share: {
                ...body.data.share,
                enabled: true,
                base_url: '',
                default_expires_in: '0',
                default_max_access: 0,
              },
            },
          },
        })
        return
      }
      if (method !== 'PUT') {
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

    await page.setViewportSize({ width: 390, height: 844 })
    await ensureAuthenticatedAt(page, '/settings?tab=general')
    await waitForRouteSettled(page, '/settings')

    const wizardHeading = page.getByRole('heading', { name: '公网访问向导' })
    await wizardHeading.scrollIntoViewIfNeeded()
    await expect(wizardHeading).toBeVisible({ timeout: 5000 })
    await expect(page.getByText('填写公网域名后设置')).toBeVisible()
    await expect(page.getByRole('button', { name: '应用推荐到表单' })).toBeDisabled()

    await page.getByLabel('公网域名').fill('nas.example.com')
    await expect(page.getByText('https://nas.example.com')).toBeVisible()
    await page.getByRole('button', { name: '应用推荐到表单' }).click()
    await selectSettingsCategory(page, 'shares', /分享与协作/i)
    await expect(page.getByLabel('新分享默认有效期')).toHaveValue('168h')
    await expect(page.getByLabel('新分享默认下载次数')).toHaveValue('20')

    await page.getByRole('button', { name: /保存|保存设置/i }).click()
    await expect(page.getByText('设置已保存，部分变更需要重启后生效')).toBeVisible({ timeout: 5000 })
    expect(submittedBody).toMatchObject({
      share: {
        enabled: true,
        base_url: 'https://nas.example.com',
        default_expires_in: '168h',
        default_max_access: 20,
      },
    })

    const horizontalOverflow = await page.evaluate(() => {
      const pageWidth = Math.max(document.documentElement.scrollWidth, document.body.scrollWidth)
      return pageWidth - window.innerWidth
    })
    expect(horizontalOverflow).toBeLessThanOrEqual(2)
  })

  test('平板端布局', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await ensureAuthenticatedAt(page, '/settings')

    const title = page.getByRole('heading', { name: /设置/i })
    await expect(title).toBeVisible({ timeout: 5000 })
  })
})
