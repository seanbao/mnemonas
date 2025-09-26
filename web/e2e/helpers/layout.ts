import { expect, type Page } from '@playwright/test'

export async function expectNoPageHorizontalOverflow(page: Page) {
  const overflow = await page.evaluate(() => {
    const root = document.documentElement
    const body = document.body
    return Math.max(root.scrollWidth, body.scrollWidth) - root.clientWidth
  })

  expect(overflow).toBeLessThanOrEqual(2)
}
