import { test, expect } from '@playwright/test'
import {
  DISABLED_SHARE_ID_FILE,
  FOLDER_SHARE_ID_FILE,
  PROTECTED_SHARE_ID_FILE,
  PROTECTED_SHARE_PASSWORD_FILE,
  PUBLIC_SHARE_ID_FILE,
  requirePublicShareFixture,
} from './helpers/public-share-fixtures'

test.use({
  storageState: { cookies: [], origins: [] },
})

test.describe('公开分享页面', () => {
  test('缺少分享 ID 时应显示公开分享错误页', async ({ page }) => {
    await page.goto('/s', { waitUntil: 'domcontentloaded' })

    await expect(page).toHaveURL(/\/s$/)
    await expect(page.getByText('无法访问分享')).toBeVisible({ timeout: 5000 })
    await expect(page.getByText('无效的分享链接')).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('button', { name: '重新加载' })).toBeVisible({ timeout: 5000 })
  })

  test('应显示公开分享文件信息', async ({ page }) => {
    const shareId = requirePublicShareFixture(PUBLIC_SHARE_ID_FILE)

    await page.goto(`/s/${shareId}`, { waitUntil: 'domcontentloaded' })

    await expect(page.getByText('e2e-share-fixture.txt')).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('button', { name: '下载文件' })).toBeVisible({ timeout: 5000 })
  })

  test('密码保护分享应先显示密码表单并在验证后显示文件信息', async ({ page }) => {
    const shareId = requirePublicShareFixture(PROTECTED_SHARE_ID_FILE)
    const sharePassword = requirePublicShareFixture(PROTECTED_SHARE_PASSWORD_FILE)

    await page.goto(`/s/${shareId}`, { waitUntil: 'domcontentloaded' })

    await expect(page.getByText('此分享需要密码')).toBeVisible({ timeout: 5000 })
    await page.getByLabel('访问密码', { exact: true }).fill(sharePassword)
    await page.getByRole('button', { name: '验证密码' }).click()

    await expect(page.getByText('e2e-protected-share-fixture.txt')).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('button', { name: '下载文件' })).toBeVisible({ timeout: 5000 })
  })

  test('密码保护分享验证后应允许下载文件', async ({ page }) => {
    const shareId = requirePublicShareFixture(PROTECTED_SHARE_ID_FILE)
    const sharePassword = requirePublicShareFixture(PROTECTED_SHARE_PASSWORD_FILE)

    await page.goto(`/s/${shareId}`, { waitUntil: 'domcontentloaded' })

    await page.getByLabel('访问密码', { exact: true }).fill(sharePassword)
    await page.getByRole('button', { name: '验证密码' }).click()

    await expect(page.getByText('e2e-protected-share-fixture.txt')).toBeVisible({ timeout: 5000 })

    const responsePromise = page.waitForResponse((response) => {
      return response.request().method() === 'GET' && response.url().includes(`/api/v1/public/shares/${shareId}/download`)
    })

    await page.getByRole('button', { name: '下载文件' }).click()

    const response = await responsePromise
    expect(response.status()).toBe(200)
    await expect(page.getByLabel('访问密码', { exact: true })).toHaveCount(0)
  })

  test('已禁用分享应显示失效状态', async ({ page }) => {
    const shareId = requirePublicShareFixture(DISABLED_SHARE_ID_FILE)

    await page.goto(`/s/${shareId}`, { waitUntil: 'domcontentloaded' })

    await expect(page.getByText('分享已停用')).toBeVisible({ timeout: 5000 })
  })

  test('公开文件夹分享应支持浏览子目录', async ({ page }) => {
    const shareId = requirePublicShareFixture(FOLDER_SHARE_ID_FILE)

    const rootItemsResponse = page.waitForResponse((response) => (
      response.request().method() === 'GET'
      && response.url().includes(`/api/v1/public/shares/${shareId}/items`)
    ))

    await page.goto(`/s/${shareId}`, { waitUntil: 'domcontentloaded' })
    expect((await rootItemsResponse).status()).toBe(200)

    await expect(page.getByRole('button', { name: /root-note\.txt/ })).toBeVisible({ timeout: 10000 })
    await expect(page.getByRole('button', { name: /subdir\s+文件夹/ })).toBeVisible({ timeout: 10000 })
    await expect(page.getByText('根目录')).toBeVisible({ timeout: 10000 })

    const subdirItemsResponse = page.waitForResponse((response) => (
      response.request().method() === 'GET'
      && response.url().includes(`/api/v1/public/shares/${shareId}/items`)
      && response.url().includes('path=subdir')
    ))
    await page.getByRole('button', { name: /subdir\s+文件夹/ }).click()
    expect((await subdirItemsResponse).status()).toBe(200)

    await expect(page.getByText('/subdir')).toBeVisible({ timeout: 10000 })
    await expect(page.getByRole('button', { name: /nested-note\.txt/ })).toBeVisible({ timeout: 10000 })
    await expect(page.getByRole('button', { name: '返回上级' })).toBeVisible({ timeout: 10000 })

    const backToRootResponse = page.waitForResponse((response) => (
      response.request().method() === 'GET'
      && response.url().includes(`/api/v1/public/shares/${shareId}/items`)
      && !response.url().includes('path=')
    ))
    await page.getByRole('button', { name: '返回上级' }).click()
    expect((await backToRootResponse).status()).toBe(200)
    await expect(page.getByRole('button', { name: /root-note\.txt/ })).toBeVisible({ timeout: 10000 })
  })
})
