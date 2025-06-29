import { readFile } from 'node:fs/promises'
import { test, expect } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

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
})
