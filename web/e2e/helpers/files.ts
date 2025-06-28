import { expect, type Page } from '@playwright/test'
import { Buffer } from 'node:buffer'

function cssString(value: string): string {
  return JSON.stringify(value)
}

export function fileRowByName(page: Page, fileName: string) {
  return page
    .locator(`button[role="checkbox"][aria-label=${cssString(`选择 ${fileName}`)}]`)
    .locator('xpath=ancestor::div[contains(@class, "grid") and contains(@class, "cursor-pointer")][1]')
}

export async function createFolderThroughUi(page: Page, folderName: string) {
  await page.getByRole('button', { name: /新建空间|新建文件夹/i }).click()
  await page.getByPlaceholder('请输入文件夹名称').fill(folderName)
  await page.getByRole('button', { name: '创建' }).click()
  await expect(fileRowByName(page, folderName)).toBeVisible({ timeout: 10_000 })
}

export async function openFolderThroughUi(page: Page, folderName: string) {
  const row = fileRowByName(page, folderName)
  await expect(row).toBeVisible({ timeout: 10_000 })
  const nameText = row.getByText(folderName, { exact: true })
  // Dispatch on the exact row text to avoid coordinate drift in transformed virtual rows.
  await nameText.dispatchEvent('dblclick', { bubbles: true, cancelable: true })
}

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
}
