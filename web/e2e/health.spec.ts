import { readFile } from 'node:fs/promises'
import { test, expect } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'
import { waitForRouteSettled } from './helpers/route-ready'

test.describe('设备状态页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/system-health')
  })

  test('应显示设备状态页面', async ({ page }) => {
    await expect(page).toHaveURL(/\/system-health/)
    await expect(page.getByRole('heading', { name: '设备状态' })).toBeVisible()
  })

  test('刷新按钮应可见', async ({ page }) => {
    await expect(page.locator('main').getByRole('button', { name: /刷新/ })).toBeVisible()
  })

  test('诊断包下载按钮应可见', async ({ page }) => {
    await expect(page.locator('main').getByRole('button', { name: '下载诊断包' })).toBeVisible()
  })

  test('应能从设备状态页下载诊断包', async ({ page }, testInfo) => {
    const downloadPromise = page.waitForEvent('download')

    await page.locator('main').getByRole('button', { name: '下载诊断包' }).click()

    const download = await downloadPromise
    expect(download.suggestedFilename()).toMatch(/^mnemonas-diagnostics-\d{8}-\d{6}\.json$/)

    const diagnosticsPath = testInfo.outputPath(download.suggestedFilename())
    await download.saveAs(diagnosticsPath)

    const payload = JSON.parse(await readFile(diagnosticsPath, 'utf8')) as Record<string, unknown>
    expect(payload.schema_version).toBe(1)
    expect(typeof payload.export_time).toBe('string')
    expect(payload.system).toBeTruthy()
    expect(payload.filesystem).toBeTruthy()
  })

  test('应显示存储文件系统提示', async ({ page }) => {
    await expect(page.getByText(/原生数据校验支持|建议使用 ZFS\/Btrfs|文件系统未知|临时文件系统|网络或 FUSE 存储/)).toBeVisible()
  })

  test('应在设备状态页显示磁盘健康和通知设置', async ({ page }) => {
    await expect(page.getByLabel('磁盘健康设置')).toBeAttached()
    await expect(page.getByLabel('通知设置')).toBeAttached()
  })

  test('通知设置保存时只提交 alerts 领域', async ({ page }) => {
    let submittedBody: Record<string, unknown> | undefined
    await page.route('**/api/v1/settings/', async (route) => {
      const method = route.request().method()
      if (method === 'GET') {
        const response = await route.fetch()
        const body = await response.json() as { data: Record<string, unknown> }
        await route.fulfill({
          status: response.status(),
          contentType: 'application/json',
          json: {
            ...body,
            data: {
              ...body.data,
              alerts: {
                enabled: false,
                check_interval: '1h',
                threshold_pct: 90,
                critical_pct: 95,
                min_free_bytes: 10737418240,
                cooldown_period: '4h',
                webhook_url: '',
                webhook_url_configured: false,
                webhook_method: 'POST',
                webhook_headers: [],
                webhook_headers_configured: false,
                telegram_enabled: false,
                telegram_bot_token_configured: false,
                telegram_chat_id: '',
                wecom_enabled: false,
                wecom_webhook_url: '',
                wecom_webhook_url_configured: false,
                dingtalk_enabled: false,
                dingtalk_webhook_url: '',
                dingtalk_webhook_url_configured: false,
                email_enabled: false,
                smtp_host: '',
                smtp_port: 587,
                smtp_username: '',
                smtp_password_configured: false,
                smtp_from: '',
                smtp_to: [],
              },
            },
          },
        })
        return
      }
      if (method === 'PUT') {
        submittedBody = JSON.parse(route.request().postData() || '{}') as Record<string, unknown>
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ success: true, data: null, message: 'settings updated' }),
        })
        return
      }
      await route.continue()
    })

    await page.reload()
    await waitForRouteSettled(page, '/system-health')
    const notificationSettings = page.getByLabel('通知设置')
    const enabledSwitch = notificationSettings.getByLabel('启用提醒').getByRole('switch')
    await enabledSwitch.scrollIntoViewIfNeeded()
    await expect(enabledSwitch).not.toBeChecked()
    await enabledSwitch.click()
    await notificationSettings.getByRole('button', { name: '保存通知设置' }).click()
    await expect(page.getByText('通知设置已保存')).toBeVisible()

    expect(submittedBody).toBeDefined()
    expect(Object.keys(submittedBody ?? {})).toEqual(['alerts'])
    expect(submittedBody).toMatchObject({ alerts: { enabled: true } })
  })
})
