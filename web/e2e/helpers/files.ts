import { expect, type Page } from '@playwright/test'
import { Buffer } from 'node:buffer'

export async function uploadTextFileThroughPicker(page: Page, fileName: string, content: string) {
  const uploadResponsePromise = page.waitForResponse((response) => {
    if (response.request().method() !== 'POST') {
      return false
    }

    const { pathname } = new URL(response.url())
    return pathname.startsWith('/api/v1/files/') && pathname.endsWith(`/${encodeURIComponent(fileName)}`)
  })

  const fileChooserPromise = page.waitForEvent('filechooser')
  await page.getByRole('button', { name: '上传文件', exact: true }).click()
  const fileChooser = await fileChooserPromise

  await fileChooser.setFiles({
    name: fileName,
    mimeType: 'text/plain',
    buffer: Buffer.from(content),
  })

  const uploadResponse = await uploadResponsePromise
  expect(uploadResponse.ok()).toBe(true)
  await expect(page.getByLabel(`${fileName} 操作菜单`).first()).toBeVisible({ timeout: 15_000 })
}
