import { expect, type Page } from '@playwright/test'

type RouteSettledOptions = {
  waitForNetworkIdle?: boolean
}

const routeLoadingText: Record<string, Array<{ text: string | RegExp; timeout: number }>> = {
  '/activity': [{ text: '加载最近操作...', timeout: 15_000 }],
  '/album': [{ text: '正在加载...', timeout: 15_000 }],
  '/favorites': [{ text: '加载收藏列表...', timeout: 15_000 }],
  '/files': [{ text: '加载记忆中...', timeout: 15_000 }],
  '/settings': [{ text: '加载设置...', timeout: 15_000 }],
  '/trash': [{ text: '加载回收站...', timeout: 15_000 }],
  '/users': [{ text: '加载用户列表...', timeout: 20_000 }],
}

export async function waitForAnimationFrames(page: Page, frameCount = 2): Promise<void> {
  await page.evaluate((frames) => new Promise<void>((resolve) => {
    let remainingFrames = frames
    const tick = () => {
      remainingFrames -= 1
      if (remainingFrames <= 0) {
        resolve()
        return
      }
      window.requestAnimationFrame(tick)
    }
    window.requestAnimationFrame(tick)
  }), frameCount)
}

async function waitForOptionalLoadingTextHidden(
  page: Page,
  text: string | RegExp,
  timeout: number,
): Promise<void> {
  const loading = page.getByText(text)
  const appeared = await loading.first().waitFor({ state: 'visible', timeout: 250 })
    .then(() => true)
    .catch(() => false)

  if (!appeared) {
    return
  }

  await loading.first().waitFor({ state: 'hidden', timeout })
}

export async function waitForRouteSettled(
  page: Page,
  route: string,
  options: RouteSettledOptions = {},
): Promise<void> {
  const routePath = route.split(/[?#]/, 1)[0]

  await expect(page.locator('body')).toBeVisible()
  await waitForOptionalLoadingTextHidden(page, '加载中...', 10_000)

  for (const loading of routeLoadingText[routePath] ?? []) {
    await waitForOptionalLoadingTextHidden(page, loading.text, loading.timeout)
  }

  if (options.waitForNetworkIdle) {
    await page.waitForLoadState('networkidle', { timeout: 5_000 }).catch(() => {})
  }

  await waitForAnimationFrames(page)
}
