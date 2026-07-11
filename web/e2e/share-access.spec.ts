import { test as base, expect } from '@playwright/test'
import { readFile } from 'node:fs/promises'
import {
  DISABLED_SHARE_ID_FILE,
  FOLDER_SHARE_ID_FILE,
  PROTECTED_SHARE_PASSWORD_FILE,
  PUBLIC_SHARE_ID_FILE,
  requirePublicShareFixture,
} from './helpers/public-share-fixtures'
import { isAuthSkipAllowed } from './helpers/auth-check'

const AUTH_STORAGE_STATE = './e2e/.auth/user.json'

interface ProtectedShareFixture {
  id: string
  password: string
}

const test = base.extend<{ protectedShare: ProtectedShareFixture }>({
  protectedShare: async ({ playwright }, provide, testInfo) => {
    const baseURL = testInfo.project.use.baseURL
    if (typeof baseURL !== 'string' || baseURL.length === 0) {
      throw new Error('The protected-share fixture requires a Playwright baseURL.')
    }

    const adminRequest = await playwright.request.newContext({ baseURL, storageState: AUTH_STORAGE_STATE })
    let shareID = ''
    let failed = false
    let failure: unknown
    const rememberFailure = (error: unknown): void => {
      if (!failed) {
        failed = true
        failure = error
      }
    }

    try {
      const password = requirePublicShareFixture(PROTECTED_SHARE_PASSWORD_FILE)
      const createResponse = await adminRequest.post('/api/v1/shares', {
        data: {
          path: '/e2e-protected-share-fixture.txt',
          type: 'file',
          permission: 'read',
          password,
          expires_in: '1h',
          description: 'playwright isolated protected public share fixture',
        },
      })
      if (!createResponse.ok()) {
        testInfo.skip(
          isAuthSkipAllowed() && (createResponse.status() === 401 || createResponse.status() === 403),
          'Skipped: authenticated isolated share fixtures are unavailable.',
        )
        throw new Error(`Failed to create an isolated protected share fixture: HTTP ${createResponse.status()}`)
      }
      const createPayload: unknown = await createResponse.json()
      if (
        !createPayload
        || typeof createPayload !== 'object'
        || !('data' in createPayload)
        || !createPayload.data
        || typeof createPayload.data !== 'object'
        || !('id' in createPayload.data)
        || typeof createPayload.data.id !== 'string'
        || createPayload.data.id.length === 0
      ) {
        throw new Error('The isolated protected share fixture returned an invalid response.')
      }
      shareID = createPayload.data.id
      await provide({ id: shareID, password })
    } catch (error) {
      rememberFailure(error)
    }

    if (shareID) {
      try {
        const deleteResponse = await adminRequest.delete(`/api/v1/shares/${encodeURIComponent(shareID)}`)
        if (!deleteResponse.ok()) {
          throw new Error(`Failed to delete an isolated protected share fixture: HTTP ${deleteResponse.status()}`)
        }
      } catch (error) {
        rememberFailure(error)
      }
    }
    try {
      await adminRequest.dispose()
    } catch (error) {
      rememberFailure(error)
    }
    if (failed) {
      throw failure
    }
  },
})

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
    await expect(page.getByRole('button', { name: '下载文件 e2e-share-fixture.txt' })).toBeVisible({ timeout: 5000 })
  })

  test('密码保护分享应先显示密码表单并在验证后显示文件信息', async ({ page, protectedShare }) => {
    const shareId = protectedShare.id
    const sharePassword = protectedShare.password

    await page.goto(`/s/${shareId}`, { waitUntil: 'domcontentloaded' })

    await expect(page.getByText('此分享需要密码')).toBeVisible({ timeout: 5000 })
    await page.getByLabel('访问密码', { exact: true }).fill(sharePassword)
    await page.getByRole('button', { name: '验证密码' }).click()

    await expect(page.getByText('e2e-protected-share-fixture.txt')).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('button', { name: '下载文件 e2e-protected-share-fixture.txt' })).toBeVisible({ timeout: 5000 })
  })

  test('密码保护分享验证后应允许下载文件', async ({ page, protectedShare }) => {
    const shareId = protectedShare.id
    const sharePassword = protectedShare.password

    await page.goto(`/s/${shareId}`, { waitUntil: 'domcontentloaded' })

    await page.getByLabel('访问密码', { exact: true }).fill(sharePassword)
    await page.getByRole('button', { name: '验证密码' }).click()

    await expect(page.getByText('e2e-protected-share-fixture.txt')).toBeVisible({ timeout: 5000 })

    const ticketResponsePromise = page.waitForResponse((response) => {
      return response.request().method() === 'POST' && response.url().includes(`/api/v1/public/shares/${shareId}/download-ticket`)
    })
    const responsePromise = page.waitForResponse((response) => {
      return response.request().method() === 'GET' && response.url().includes(`/api/v1/public/shares/${shareId}/download`)
    })
    const downloadPromise = page.waitForEvent('download')

    await page.getByRole('button', { name: '下载文件 e2e-protected-share-fixture.txt' }).click()

    const ticketResponse = await ticketResponsePromise
    const response = await responsePromise
    const download = await downloadPromise
    expect(ticketResponse.status()).toBe(200)
    expect(response.status()).toBe(200)
    expect(download.suggestedFilename()).toBe('e2e-protected-share-fixture.txt')
    const downloadPath = await download.path()
    expect(downloadPath).not.toBeNull()
    expect(await readFile(downloadPath!, 'utf8')).toBe('fixture for protected public share e2e')
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

    await expect(page.getByText('root-note.txt', { exact: true })).toBeVisible({ timeout: 10000 })
    await expect(page.getByRole('button', { name: '打开文件夹 subdir' })).toBeVisible({ timeout: 10000 })
    await expect(page.getByText('根目录')).toBeVisible({ timeout: 10000 })

    const subdirItemsResponse = page.waitForResponse((response) => (
      response.request().method() === 'GET'
      && response.url().includes(`/api/v1/public/shares/${shareId}/items`)
      && response.url().includes('path=subdir')
    ))
    await page.getByRole('button', { name: '打开文件夹 subdir' }).click()
    expect((await subdirItemsResponse).status()).toBe(200)

    await expect(page.getByText('/subdir')).toBeVisible({ timeout: 10000 })
    await expect(page.getByText('nested-note.txt', { exact: true })).toBeVisible({ timeout: 10000 })
    await expect(page.getByRole('button', { name: '返回上级' })).toBeVisible({ timeout: 10000 })

    const backToRootResponse = page.waitForResponse((response) => (
      response.request().method() === 'GET'
      && response.url().includes(`/api/v1/public/shares/${shareId}/items`)
      && !response.url().includes('path=')
    ))
    await page.getByRole('button', { name: '返回上级' }).click()
    expect((await backToRootResponse).status()).toBe(200)
    await expect(page.getByText('root-note.txt', { exact: true })).toBeVisible({ timeout: 10000 })
  })
})
