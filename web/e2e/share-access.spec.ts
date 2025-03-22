import fs from 'node:fs'
import path from 'node:path'
import { test, expect } from '@playwright/test'

const E2E_ROOT = process.env.MNEMONAS_E2E_ROOT || '/tmp/mnemonas-playwright'
const PUBLIC_SHARE_ID_FILE = path.join(E2E_ROOT, 'backend', 'public-share-id.txt')

function readSeededPublicShareId(): string | null {
  if (!fs.existsSync(PUBLIC_SHARE_ID_FILE)) {
    return null
  }

  const shareId = fs.readFileSync(PUBLIC_SHARE_ID_FILE, 'utf8').trim()
  return shareId || null
}

test.use({
  storageState: { cookies: [], origins: [] },
})

test.describe('公开分享页面', () => {
  test('应显示公开分享文件信息', async ({ page }) => {
    const shareId = readSeededPublicShareId()
    test.skip(!shareId, 'Skipped: no seeded public share fixture')

    await page.goto(`/s/${shareId}`, { waitUntil: 'domcontentloaded' })

    await expect(page.getByText('e2e-share-fixture.txt')).toBeVisible({ timeout: 5000 })
    await expect(page.getByRole('button', { name: '下载文件' })).toBeVisible({ timeout: 5000 })
  })
})