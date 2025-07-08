import { Buffer } from 'node:buffer'
import { test, expect } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'
import { uploadFileThroughPicker } from './helpers/files'

const TEST_RGB_PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAIAAACQkWg2AAABz0lEQVR4nA3LobKFIBRA0fcpNxKJRiLRaDzJMRAYE8khEBzTSY7BZCKRSKb9ac/V199PMIIVBsEJXhiFSRBhEaKQhCzsggqX8PebMTN2ZphxM35mnJlmZGaZiTNpJs/sMzpzzV8ImIANDAEX8IExMAUksARiIAVyYA9o4ApfWDErdmVYcSt+ZVyZVmRlWYkraSWv7Cu6cq1fSJiETQwJl/CJMTElJLEkYiIlcmJPaOJKX9gwG3Zj2HAbfmPcmDZkY9mIG2kjb+wbunFtXyiYgi0MBVfwhbEwFaSwFGIhFXJhL2jhKl84MAf2YDhwB/5gPJgO5GA5iAfpIB/sB3pwHV9QjGKVQXGKV0ZlUkRZlKgkJSu7osqlXzgxJ/ZkOHEn/mQ8mU7kZDmJJ+kkn+wnenKdX7gxN/ZmuHE3/ma8mW7kZrmJN+km3+w3enPdX3gwD/ZheHAP/mF8mB7kYXmID+khP+wP+nA9X6iYiq0MFVfxlbEyVaSyVGIlVXJlr2jlql9omIZtDA3X8I2xMTWksTRiIzVyY29o42pf6JiO7Qwd1/GdsTN1pLN0Yid1cmfvaOfqX3gxL/ZleHEv/mV8mV7kZXmJL+klv+wv+nK9/AMmIKkQzM4+/wAAAABJRU5ErkJggg==',
  'base64',
)

/**
 * Album page E2E tests.
 * Authentication state is injected by auth.setup.ts through storageState.
 * Login setup failures fail by default; protected-page tests skip only when
 * auth skipping is explicitly enabled for reused environments.
 */

test.describe('相册页面', () => {
  test.beforeEach(async ({ page }) => {
    await ensureAuthenticatedAt(page, '/album')
  })

  test('应显示相册页面', async ({ page }) => {
    await expect(page).not.toHaveURL(/\/login/)
    await expect(page.getByRole('heading', { name: '相册', exact: true })).toBeVisible({ timeout: 5000 })
  })

  test('应显示相册内容或空状态', async ({ page }) => {
    await expect(page.getByText(/共 \d+ 张图片/)).toBeVisible({ timeout: 5000 })
  })

  test('有图片时应可打开预览，空相册时应保持空状态提示', async ({ page }) => {
    await expect(page.getByText(/共 \d+ 张图片/)).toBeVisible({ timeout: 5000 })

    const emptyStateHeading = page.getByRole('heading', { name: '暂无图片', exact: true })
    const thumbnails = page.locator('main img[alt]')

    if (await thumbnails.first().isVisible({ timeout: 1000 }).catch(() => false)) {
      await thumbnails.first().click({ force: true })
      await expect(page.getByRole('button', { name: '关闭预览' })).toBeVisible({ timeout: 5000 })
      return
    }

    await expect(emptyStateHeading).toBeVisible({ timeout: 5000 })
  })
})

test.describe('相册图片预览', () => {
  test('上传图片后应显示相册项并打开预览', async ({ page }, testInfo) => {
    const imageName = `album-e2e-${testInfo.project.name}-${testInfo.workerIndex}-${Date.now()}.png`
      .replace(/[^a-z0-9.-]/gi, '-')

    await ensureAuthenticatedAt(page, '/files')
    await uploadFileThroughPicker(page, imageName, 'image/png', TEST_RGB_PNG)

    await page.goto('/album', { waitUntil: 'domcontentloaded' })
    await expect(page.getByRole('heading', { name: '相册', exact: true })).toBeVisible({ timeout: 10_000 })
    await expect(page.getByText(/共 \d+ 张图片/)).toBeVisible({ timeout: 10_000 })

    const thumbnail = page.locator('main').getByRole('img', { name: imageName }).first()
    await expect(thumbnail).toBeVisible({ timeout: 15_000 })
    await expect.poll(async () => thumbnail.evaluate((element) => (
      element instanceof HTMLImageElement ? element.naturalWidth : 0
    )), { timeout: 10_000 }).toBeGreaterThan(0)
    await page.locator('main').getByText(imageName, { exact: true }).click()

    const preview = page.getByRole('dialog')
    await expect(preview.getByRole('button', { name: '关闭预览' })).toBeVisible({ timeout: 10_000 })
    await expect(preview.getByText(imageName, { exact: true })).toBeVisible()
    const previewImage = preview.getByRole('img', { name: imageName })
    await expect(previewImage).toBeVisible()
    await expect.poll(async () => previewImage.evaluate((element) => (
      element instanceof HTMLImageElement ? element.naturalWidth : 0
    )), { timeout: 10_000 }).toBeGreaterThan(0)
  })
})

test.describe('相册页面响应式', () => {
  test('移动端布局', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await ensureAuthenticatedAt(page, '/album')

    await expect(page.getByRole('heading', { name: '相册', exact: true })).toBeVisible({ timeout: 5000 })
  })
})
