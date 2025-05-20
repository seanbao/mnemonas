import fs from 'node:fs'
import path from 'node:path'
import { test, expect } from '@playwright/test'

const E2E_ROOT = process.env.MNEMONAS_E2E_ROOT || '/tmp/mnemonas-playwright'
const PUBLIC_SHARE_ID_FILE = path.join(E2E_ROOT, 'backend', 'public-share-id.txt')
const PROTECTED_SHARE_ID_FILE = path.join(E2E_ROOT, 'backend', 'protected-share-id.txt')
const PROTECTED_SHARE_PASSWORD_FILE = path.join(E2E_ROOT, 'backend', 'protected-share-password.txt')
const DISABLED_SHARE_ID_FILE = path.join(E2E_ROOT, 'backend', 'disabled-share-id.txt')
const FOLDER_SHARE_ID_FILE = path.join(E2E_ROOT, 'backend', 'folder-share-id.txt')

function readFixtureValue(filePath: string): string | null {
  if (!fs.existsSync(filePath)) {
    return null
  }

  const value = fs.readFileSync(filePath, 'utf8').trim()
  return value || null
}

test.use({
  storageState: { cookies: [], origins: [] },
})

test.describe('公开分享页面', () => {
  test('应显示公开分享文件信息', async ({ page }) => {
    const shareId = readFixtureValue(PUBLIC_SHARE_ID_FILE)
    test.skip(!shareId, 'Skipped: no seeded public share fixture')

    await page.goto(`/s/${shareId}`, { waitUntil: 'domcontentloaded' })

    await expect(page.getByText('e2e-share-fixture.txt')).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('button', { name: '下载文件' })).toBeVisible({ timeout: 5000 })
  })

  test('密码保护分享应先显示密码表单并在验证后显示文件信息', async ({ page }) => {
    const shareId = readFixtureValue(PROTECTED_SHARE_ID_FILE)
    const sharePassword = readFixtureValue(PROTECTED_SHARE_PASSWORD_FILE)
    test.skip(!shareId || !sharePassword, 'Skipped: no seeded protected public share fixture')

    await page.goto(`/s/${shareId}`, { waitUntil: 'domcontentloaded' })

    await expect(page.getByText('此分享需要密码')).toBeVisible({ timeout: 5000 })
    await page.getByPlaceholder('请输入密码').fill(sharePassword)
    await page.getByRole('button', { name: '验证密码' }).click()

    await expect(page.getByText('e2e-protected-share-fixture.txt')).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('button', { name: '下载文件' })).toBeVisible({ timeout: 5000 })
  })

  test('已禁用分享应显示失效状态', async ({ page }) => {
    const shareId = readFixtureValue(DISABLED_SHARE_ID_FILE)
    test.skip(!shareId, 'Skipped: no seeded disabled public share fixture')

    await page.goto(`/s/${shareId}`, { waitUntil: 'domcontentloaded' })

    await expect(page.getByText('分享已失效')).toBeVisible({ timeout: 5000 })
  })

  test('公开文件夹分享应支持浏览子目录', async ({ page }) => {
    const shareId = readFixtureValue(FOLDER_SHARE_ID_FILE)
    test.skip(!shareId, 'Skipped: no seeded public folder share fixture')

    await page.goto(`/s/${shareId}`, { waitUntil: 'domcontentloaded' })

    await expect(page.getByText('root-note.txt')).toBeVisible({ timeout: 5000 })
    await expect(page.getByText('subdir')).toBeVisible({ timeout: 5000 })
    await expect(page.getByText('根目录')).toBeVisible({ timeout: 5000 })

    await page.getByRole('button', { name: 'subdir' }).click()

    await expect(page.getByText('/subdir')).toBeVisible({ timeout: 5000 })
    await expect(page.getByText('nested-note.txt')).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('button', { name: '返回上级' })).toBeVisible({ timeout: 5000 })

    await page.getByRole('button', { name: '返回上级' }).click()
    await expect(page.getByText('root-note.txt')).toBeVisible({ timeout: 5000 })
  })
})