import { test, expect, type Locator, type Page } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'
import { backupJob, routeBackupJobs } from './helpers/backups'
import { expectNoPageHorizontalOverflow } from './helpers/layout'
import {
  completedSetupReadiness,
  createSetupReadiness,
  routeSetupReadiness,
} from './helpers/setup-readiness'

async function expectDashboardReady(page: Page) {
  const main = page.getByRole('main')
  await expect(page).not.toHaveURL(/\/login/)
  await expect(main.getByRole('heading', { name: '首页' })).toBeVisible({ timeout: 5000 })
  await expect(main.getByText('存储概览', { exact: true })).toBeVisible()
  await expect(main.getByText('最近操作', { exact: true })).toBeVisible()
}

async function expectRenderedAbove(earlier: Locator, later: Locator) {
  const [earlierBox, laterBox] = await Promise.all([
    earlier.boundingBox(),
    later.boundingBox(),
  ])

  expect(earlierBox).not.toBeNull()
  expect(laterBox).not.toBeNull()
  expect(earlierBox!.y).toBeLessThan(laterBox!.y)
}

async function expectDailyEntryOrder(page: Page) {
  const entries = page.getByRole('main').getByRole('navigation', { name: '常用入口' }).getByRole('button')
  await expect(entries).toHaveCount(4)
  await expect(entries.nth(0)).toContainText('文件')
  await expect(entries.nth(1)).toContainText('版本')
  await expect(entries.nth(2)).toContainText('空间')
  await expect(entries.nth(3)).toContainText('备份与维护')
}

test.describe('主页', () => {
  test('认证后应显示首页内容', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/')

    await expectDashboardReady(page)
  })

  test('认证后应显示导航入口', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/')
    await expectDashboardReady(page)

    await expect
      .poll(async () => {
        const mobileMenuVisible = await page.getByRole('button', { name: '打开导航菜单' }).isVisible().catch(() => false)
        const mobileNavVisible = await page.getByRole('navigation', { name: '移动端主导航' }).isVisible().catch(() => false)
        const desktopNavVisible = await page.getByRole('navigation', { name: '主导航' }).isVisible().catch(() => false)

        return mobileMenuVisible || mobileNavVisible || desktopNavVisible
      }, {
        message: 'home page should expose a visible navigation entry point',
        timeout: 10_000,
      })
      .toBe(true)
  })

  test('初始密码未更换时应仅展示服务端检测动作且不可完成或延期', async ({ page }) => {
    await routeBackupJobs(page, [
      backupJob('external-disk', '外置硬盘备份', '/restore/mnemonas', false),
    ])
    const readinessRoute = await routeSetupReadiness(page, {
      initialReadiness: createSetupReadiness({
        checkOverrides: {
          bootstrap_credential: {
            status: 'incomplete',
            title: '更换初始密码',
            message: '仍有管理员使用初始化流程生成的密码。',
            action: 'change_password',
          },
        },
        summary: {
          active_admin_count: 2,
          password_change_required_admin_count: 1,
        },
      }),
    })

    await ensureAuthenticatedAt(page, '/')
    await expectDashboardReady(page)

    const main = page.getByRole('main')
    const dailySummary = main.getByRole('region', { name: '日常空间摘要' })
    const dailyEntries = main.getByRole('navigation', { name: '常用入口' })
    const setupCard = main.getByRole('region', { name: '首次设置检查' })
    const backupAttention = main.getByText('备份需要查看')

    await expect(setupCard).toBeVisible()
    await expect(setupCard.getByText('必需 5/6')).toBeVisible()
    await expect(setupCard.getByRole('checkbox')).toHaveCount(0)
    await expect(setupCard.getByRole('button', { name: '完成设置' })).toHaveCount(0)
    await expect(setupCard.getByRole('button', { name: '稍后提醒' })).toHaveCount(0)
    await expectDailyEntryOrder(page)
    await expectRenderedAbove(dailySummary, setupCard)
    await expectRenderedAbove(dailyEntries, setupCard)
    await expectRenderedAbove(dailySummary, backupAttention)
    await expectRenderedAbove(dailyEntries, backupAttention)

    await setupCard.getByRole('button', { name: '查看检测结果' }).click()
    await expect(setupCard.getByRole('button', { name: '收起检测结果' })).toHaveAttribute('aria-expanded', 'true')
    await expect(setupCard.getByRole('heading', { name: '更换初始密码' })).toBeVisible()
    await expect(setupCard.getByText('仍有管理员使用初始化流程生成的密码。')).toBeVisible()
    await expect(setupCard.getByRole('button', { name: '修改密码' })).toBeVisible()
    await expect(setupCard.getByText('待处理').first()).toBeVisible()
    expect(readinessRoute.requests.readiness.length).toBeGreaterThan(0)
  })

  test('仅备份必需项缺失时应按精确 payload 延期七天', async ({ page }) => {
    const readinessRoute = await routeSetupReadiness(page, {
      initialReadiness: createSetupReadiness({
        checkOverrides: {
          backup_job: {
            status: 'incomplete',
            message: '尚未添加启用中的备份任务。',
          },
          backup_success: {
            status: 'incomplete',
            message: '尚无当前有效的成功备份。',
          },
          backup_schedule: {
            status: 'incomplete',
            message: '建议为备份任务启用自动计划。',
          },
          restore_verification: {
            status: 'incomplete',
            message: '建议执行一次恢复演练并保持验证结果有效。',
          },
        },
        summary: { enabled_backup_job_count: 0 },
      }),
    })

    await ensureAuthenticatedAt(page, '/')
    await expectDashboardReady(page)

    const setupCard = page.getByRole('main').getByRole('region', { name: '首次设置检查' })
    await expect(setupCard.getByText('必需 4/6')).toBeVisible()
    await expect(setupCard.getByRole('button', { name: '稍后提醒' })).toBeEnabled()
    await expect(setupCard.getByRole('button', { name: '完成设置' })).toHaveCount(0)

    await setupCard.getByRole('button', { name: '稍后提醒' }).click()
    await expect(page.getByRole('radio', { name: '7 天后提醒' })).toBeChecked()
    await page.getByRole('button', { name: '确认延期' }).click()

    await expect(page.getByRole('region', { name: '设置提醒已延期' })).toBeVisible()
    await expect(setupCard).toHaveCount(0)
    expect(readinessRoute.requests.defer).toHaveLength(1)
    expect(readinessRoute.requests.defer[0]).toMatchObject({
      method: 'POST',
      pathname: '/api/v1/setup/defer',
      body: { remind_in_days: 7 },
    })
    expect(readinessRoute.current.lifecycle).toBe('deferred')
    expect(readinessRoute.requests.readiness.length).toBeGreaterThanOrEqual(2)
  })

  test('就绪后应等待服务端确认 completed 再移除卡片', async ({ page }) => {
    let releaseAcknowledge!: () => void
    const acknowledgeGate = new Promise<void>((resolve) => {
      releaseAcknowledge = resolve
    })
    const readinessRoute = await routeSetupReadiness(page, {
      initialReadiness: createSetupReadiness(),
      onAcknowledge: async (_request, state) => {
        await acknowledgeGate
        return {
          status: 200,
          readiness: completedSetupReadiness(state.current),
          message: 'setup completed',
        }
      },
    })

    await ensureAuthenticatedAt(page, '/')
    await expectDashboardReady(page)

    const setupCard = page.getByRole('main').getByRole('region', { name: '首次设置检查' })
    await setupCard.getByRole('button', { name: '完成设置' }).click()
    await expect.poll(() => readinessRoute.requests.acknowledge.length).toBe(1)
    expect(readinessRoute.requests.acknowledge[0].body).toEqual({})
    await expect(setupCard).toBeVisible()

    releaseAcknowledge()
    await expect(setupCard).toHaveCount(0)
    await expect(page.getByRole('region', { name: '首次设置已完成' })).toHaveCount(0)
    expect(readinessRoute.current.lifecycle).toBe('completed')
  })

  test('完成请求冲突后应保留卡片并展示服务端新缺失项', async ({ page }) => {
    const degraded = createSetupReadiness({
      checkOverrides: {
        security_baseline: {
          status: 'incomplete',
          title: '处理新发现的安全阻断项',
          message: '安全自检发现了新的必须处理项。',
          action: 'review_security',
        },
        security_recommendations: {
          status: 'incomplete',
          message: '安全自检仍有建议处理的项目。',
        },
      },
      summary: {
        security_status: 'block',
        security_blocking_check_ids: ['new_security_block'],
      },
    })
    const readinessRoute = await routeSetupReadiness(page, {
      initialReadiness: createSetupReadiness(),
      onAcknowledge: (_request, state) => {
        state.setReadiness(degraded)
        return {
          status: 409,
          error: {
            code: 'SETUP_NOT_READY',
            message: 'required setup checks changed',
            details: { required_check_ids: ['security_baseline'] },
          },
        }
      },
    })

    await ensureAuthenticatedAt(page, '/')
    await expectDashboardReady(page)

    const setupCard = page.getByRole('main').getByRole('region', { name: '首次设置检查' })
    await setupCard.getByRole('button', { name: '完成设置' }).click()

    await expect(setupCard).toBeVisible()
    await expect(setupCard.getByText('必需 5/6')).toBeVisible()
    await expect(setupCard.getByRole('alert')).toBeVisible()
    await setupCard.getByRole('button', { name: '查看检测结果' }).click()
    const newMissingItem = setupCard.getByRole('listitem').filter({ hasText: '处理新发现的安全阻断项' })
    await expect(newMissingItem).toContainText('待处理')
    await expect(newMissingItem.getByRole('button', { name: '查看安全检查' })).toBeVisible()
    expect(readinessRoute.requests.acknowledge).toHaveLength(1)
    expect(readinessRoute.requests.readiness.length).toBeGreaterThanOrEqual(2)
  })
})

test.describe('首页备份风险提示', () => {
  test.beforeEach(async ({ page }) => {
    await routeBackupJobs(page, [
      backupJob('external-disk', '外置硬盘备份', '/restore/mnemonas', false),
    ])
    await ensureAuthenticatedAt(page, '/')
  })

  test('应提示恢复后缺少匹配校验的备份任务', async ({ page }) => {
    await expect(page.getByText('备份需要查看')).toBeVisible()
    await expect(page.getByText('1 项待处理')).toBeVisible()
    await expect(page.getByText('恢复待校验').first()).toBeVisible()
    await expect(page.getByRole('button', { name: '打开备份' })).toBeVisible()
  })
})

test.describe('文件浏览功能', () => {
  test('认证后文件页面应显示文件浏览器', async ({ page }) => {
    await ensureAuthenticatedAt(page, '/files')

    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.getByRole('button', { name: '根目录' })).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('button', { name: '上传文件', exact: true })).toBeVisible()
  })
})

test.describe('响应式布局', () => {
  test('桌面端日常入口应保持顺序且页面无横向溢出', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await routeSetupReadiness(page, {
      initialReadiness: createSetupReadiness({
        checkOverrides: {
          backup_job: { status: 'incomplete', message: '尚未添加启用中的独立备份任务。' },
          backup_success: { status: 'incomplete', message: '尚无当前有效的成功备份。' },
          backup_schedule: { status: 'incomplete', message: '建议为备份任务启用自动计划。' },
          restore_verification: { status: 'incomplete', message: '建议执行一次恢复演练并保持验证结果有效。' },
        },
        summary: { enabled_backup_job_count: 0 },
      }),
    })
    await ensureAuthenticatedAt(page, '/')

    await expectDashboardReady(page)
    await expectDailyEntryOrder(page)
    await expect(page.getByRole('main').getByRole('region', { name: '首次设置检查' })).toBeVisible()
    await expectNoPageHorizontalOverflow(page)
  })

  test('移动端展开设置检查后应正常渲染且无横向溢出', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await routeSetupReadiness(page, {
      initialReadiness: createSetupReadiness({
        checkOverrides: {
          bootstrap_credential: {
            status: 'incomplete',
            title: '更换初始密码',
            message: '仍有管理员使用初始化流程生成的密码，请先完成更换再继续首次设置。',
          },
        },
        summary: { password_change_required_admin_count: 1 },
      }),
    })
    await ensureAuthenticatedAt(page, '/')

    await expectDashboardReady(page)
    const dailyEntries = page.getByRole('main').getByRole('navigation', { name: '常用入口' })
    await expect(dailyEntries.getByRole('button').nth(0)).toBeInViewport({ ratio: 1 })
    await expect(dailyEntries.getByRole('button').nth(1)).toBeInViewport({ ratio: 1 })
    const setupCard = page.getByRole('main').getByRole('region', { name: '首次设置检查' })
    await setupCard.getByRole('button', { name: '查看检测结果' }).click()
    await expect(setupCard.getByRole('button', { name: '修改密码' })).toBeVisible()
    await expectNoPageHorizontalOverflow(page)
  })

  test('平板端应正常渲染', async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 1024 })
    await ensureAuthenticatedAt(page, '/')

    await expectDashboardReady(page)
    await expectNoPageHorizontalOverflow(page)
  })
})
