import { test, expect, type Page } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'
import { publicEntryRoutes } from './helpers/public-share-fixtures'
import { waitForRouteSettled } from './helpers/route-ready'

const routes = [
  '/',
  '/files',
  '/album',
  '/favorites',
  '/trash',
  '/search',
  '/versions',
  '/activity',
  '/storage',
  '/system-health',
  '/maintenance',
  '/users',
  '/settings',
  '/nonexistent-page-xyz123',
]

type LayoutIssue = {
  route: string
  rule: string
  target: string
  message: string
}

async function prepareRoute(page: Page, route: string) {
  await ensureAuthenticatedAt(page, route)
  await waitForRouteSettled(page, route)
}

async function collectLayoutIssues(page: Page, route: string): Promise<LayoutIssue[]> {
  return page.evaluate((currentRoute) => {
    const issues: LayoutIssue[] = []
    const viewportWidth = document.documentElement.clientWidth
    const viewportHeight = document.documentElement.clientHeight
    const documentWidth = Math.max(
      document.documentElement.scrollWidth,
      document.body?.scrollWidth ?? 0,
    )

    if (documentWidth > viewportWidth + 2) {
      issues.push({
        route: currentRoute,
        rule: 'horizontal-overflow',
        target: '<document>',
        message: `document scrollWidth ${documentWidth}px exceeds viewport width ${viewportWidth}px`,
      })
    }

    const describeTarget = (element: Element) => {
      const tagName = element.tagName.toLowerCase()
      const role = element.getAttribute('role')
      const ariaLabel = element.getAttribute('aria-label')
      const text = (element.textContent ?? '').replace(/\s+/g, ' ').trim()
      const parts = [tagName]

      if (role) parts.push(`[role="${role}"]`)
      if (ariaLabel) parts.push(`[aria-label="${ariaLabel}"]`)
      if (text) parts.push(`"${text.slice(0, 60)}"`)

      return parts.join('')
    }

    const isVisible = (element: Element) => {
      if (!(element instanceof HTMLElement || element instanceof SVGElement)) {
        return false
      }
      const rect = element.getBoundingClientRect()
      const style = window.getComputedStyle(element)
      return style.display !== 'none'
        && style.visibility !== 'hidden'
        && Number.parseFloat(style.opacity || '1') > 0.01
        && rect.width > 0
        && rect.height > 0
        && rect.bottom > 0
        && rect.right > 0
        && rect.top < viewportHeight
        && rect.left < viewportWidth
    }

    const isInsideHorizontalScrollClip = (element: Element) => {
      let parent = element.parentElement

      while (parent) {
        const style = window.getComputedStyle(parent)
        const clipsHorizontally = ['auto', 'scroll', 'hidden', 'clip'].includes(style.overflowX)
          || ['auto', 'scroll', 'hidden', 'clip'].includes(style.overflow)

        if (clipsHorizontally && parent.scrollWidth > parent.clientWidth + 1) {
          return true
        }

        parent = parent.parentElement
      }

      return false
    }

    for (const element of Array.from(document.body.querySelectorAll('*'))) {
      if (!isVisible(element)) {
        continue
      }

      const rect = element.getBoundingClientRect()
      const overflowLeft = rect.left < -2
      const overflowRight = rect.right > viewportWidth + 2

      if (!overflowLeft && !overflowRight) {
        continue
      }

      if (isInsideHorizontalScrollClip(element)) {
        continue
      }

      const style = window.getComputedStyle(element)
      if (style.position === 'fixed' && element.closest('[aria-hidden="true"], [hidden], [inert]')) {
        continue
      }

      issues.push({
        route: currentRoute,
        rule: 'visible-element-outside-viewport',
        target: describeTarget(element),
        message: `visible element bounds ${Math.round(rect.left)}..${Math.round(rect.right)} exceed viewport width ${viewportWidth}`,
      })
    }

    return issues
  }, route)
}

test.describe('响应式布局完整性扫描', () => {
  test('核心页面不应出现横向溢出或可见元素越界', async ({ page }, testInfo) => {
    testInfo.setTimeout(90_000)

    const issues: LayoutIssue[] = []
    for (const route of routes) {
      await prepareRoute(page, route)
      issues.push(...await collectLayoutIssues(page, route))
    }

    expect(
      issues.map((issue) => `[${issue.rule}] ${issue.route} ${issue.target}: ${issue.message}`),
    ).toEqual([])
  })
})

test.describe('公开入口响应式布局完整性扫描', () => {
  test.use({
    storageState: { cookies: [], origins: [] },
  })

  test('登录页和公开分享页不应出现横向溢出或可见元素越界', async ({ page }, testInfo) => {
    testInfo.setTimeout(60_000)

    const issues: LayoutIssue[] = []
    for (const route of publicEntryRoutes()) {
      await page.goto(route, { waitUntil: 'domcontentloaded' })
      await waitForRouteSettled(page, route, { waitForNetworkIdle: true })
      issues.push(...await collectLayoutIssues(page, route))
    }

    expect(
      issues.map((issue) => `[${issue.rule}] ${issue.route} ${issue.target}: ${issue.message}`),
    ).toEqual([])
  })
})
