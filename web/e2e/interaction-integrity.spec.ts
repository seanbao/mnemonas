import { test, expect, type Page } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

const routeGroups = [
  {
    name: '首页和文件页',
    routes: ['/', '/files'],
  },
  {
    name: '媒体收藏和回收站页面',
    routes: ['/album', '/favorites', '/trash'],
  },
  {
    name: '搜索页面',
    routes: ['/search'],
  },
  {
    name: '历史和活动页面',
    routes: ['/versions', '/activity'],
  },
  {
    name: '存储和健康页面',
    routes: ['/storage', '/system-health'],
  },
  {
    name: '维护和用户页面',
    routes: ['/maintenance', '/users'],
  },
  {
    name: '设置页面',
    routes: ['/settings'],
  },
]

type InteractionIssue = {
  route: string
  rule: string
  target: string
  message: string
}

async function prepareRoute(page: Page, route: string) {
  await ensureAuthenticatedAt(page, route)
  await expect(page.locator('body')).toBeVisible()
  await page.getByText('加载中...').waitFor({ state: 'hidden', timeout: 10_000 }).catch(() => {})
  await page.waitForTimeout(500)
}

async function collectHitTargetIssues(page: Page, route: string): Promise<InteractionIssue[]> {
  return page.evaluate((currentRoute) => {
    const issues: InteractionIssue[] = []
    const clickableSelector = [
      'button',
      'a[href]',
      '[role="button"]',
      '[role="link"]',
      '[role="menuitem"]',
      '[role="tab"]',
    ].join(',')

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

    const isElementVisible = (element: Element) => {
      if (!(element instanceof HTMLElement || element instanceof SVGElement)) {
        return false
      }
      const style = window.getComputedStyle(element)
      const rect = element.getBoundingClientRect()
      return style.visibility !== 'hidden'
        && style.display !== 'none'
        && rect.width > 0
        && rect.height > 0
    }

    const isInViewport = (element: Element) => {
      const rect = element.getBoundingClientRect()
      return rect.bottom > 0
        && rect.right > 0
        && rect.top < window.innerHeight
        && rect.left < window.innerWidth
    }

    const isUnavailable = (element: Element) => {
      return !!element.closest('[hidden], [inert], [aria-hidden="true"], [disabled], [aria-disabled="true"]')
    }

    const pointHitBlocker = (element: Element, x: number, y: number) => {
      const topElement = document.elementFromPoint(x, y)
      if (!topElement) {
        return '<none>'
      }
      if (topElement === element || element.contains(topElement) || topElement.contains(element)) {
        return null
      }

      if (topElement.closest(clickableSelector) === element) {
        return null
      }

      return describeTarget(topElement)
    }

    const isPointWithinClippingAncestors = (element: Element, x: number, y: number) => {
      let parent = element.parentElement

      while (parent) {
        const style = window.getComputedStyle(parent)
        const clips = [style.overflow, style.overflowX, style.overflowY]
          .some((value) => ['auto', 'scroll', 'hidden', 'clip'].includes(value))

        if (clips) {
          const rect = parent.getBoundingClientRect()
          if (x < rect.left || x > rect.right || y < rect.top || y > rect.bottom) {
            return false
          }
        }

        parent = parent.parentElement
      }

      return true
    }

    const getHitTestBlocker = (element: Element) => {
      const rect = element.getBoundingClientRect()
      const left = Math.max(0, rect.left)
      const right = Math.min(window.innerWidth - 1, rect.right)
      const top = Math.max(0, rect.top)
      const bottom = Math.min(window.innerHeight - 1, rect.bottom)

      if (right <= left || bottom <= top) {
        return '<outside viewport>'
      }

      const points = [
        [left + ((right - left) / 2), top + ((bottom - top) / 2)],
        [left + Math.min(8, (right - left) / 2), top + Math.min(8, (bottom - top) / 2)],
        [right - Math.min(8, (right - left) / 2), bottom - Math.min(8, (bottom - top) / 2)],
      ]

      const visiblePoints = points.filter(([x, y]) => isPointWithinClippingAncestors(element, x, y))
      if (visiblePoints.length === 0) {
        return null
      }

      const blockers = visiblePoints.map(([x, y]) => pointHitBlocker(element, x, y))
      if (blockers.some((blocker) => blocker === null)) {
        return null
      }

      return blockers.find(Boolean) ?? '<unknown>'
    }

    for (const element of Array.from(document.querySelectorAll(clickableSelector))) {
      if (!isElementVisible(element) || !isInViewport(element) || isUnavailable(element)) {
        continue
      }

      const blocker = getHitTestBlocker(element)
      if (blocker) {
        issues.push({
          route: currentRoute,
          rule: 'click-target-covered',
          target: describeTarget(element),
          message: `visible clickable element is not the top hit target at sampled points; top target was ${blocker}`,
        })
      }
    }

    return issues
  }, route)
}

async function collectKeyboardIssues(page: Page, route: string): Promise<InteractionIssue[]> {
  const issues: InteractionIssue[] = []
  const seenTargets = new Set<string>()

  await page.evaluate(() => {
    window.scrollTo(0, 0)
    if (document.activeElement instanceof HTMLElement) {
      document.activeElement.blur()
    }
  })

  for (let index = 0; index < 30; index += 1) {
    await page.keyboard.press('Tab')
    await page.waitForFunction(() => {
      const active = document.activeElement
      if (!active || active === document.body || active === document.documentElement) {
        return true
      }
      const rect = active.getBoundingClientRect()
      return rect.bottom > 0
        && rect.right > 0
        && rect.top < window.innerHeight
        && rect.left < window.innerWidth
    }, undefined, { timeout: 750 }).catch(() => {})

    const result = await page.evaluate((currentRoute) => {
      const active = document.activeElement
      if (!active || active === document.body || active === document.documentElement) {
        return {
          issue: null,
          target: '',
        }
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

      const target = describeTarget(active)

      if (!(active instanceof HTMLElement || active instanceof SVGElement)) {
        return {
          issue: {
            route: currentRoute,
            rule: 'keyboard-focus-invalid',
            target,
            message: 'focused element is not an HTMLElement or SVGElement',
          },
          target,
        }
      }

      const style = window.getComputedStyle(active)
      const rect = active.getBoundingClientRect()
      const visible = style.visibility !== 'hidden'
        && style.display !== 'none'
        && rect.width > 0
        && rect.height > 0
      const inViewport = rect.bottom > 0
        && rect.right > 0
        && rect.top < window.innerHeight
        && rect.left < window.innerWidth

      if (!visible || !inViewport || active.closest('[hidden], [inert], [aria-hidden="true"]')) {
        return {
          issue: {
            route: currentRoute,
            rule: 'keyboard-focus-hidden',
            target,
            message: 'Tab focus landed on an invisible, offscreen, or hidden-from-users element',
          },
          target,
        }
      }

      return {
        issue: null,
        target,
      }
    }, route)

    if (result.issue) {
      issues.push(result.issue)
    }
    if (result.target) {
      seenTargets.add(result.target)
    }
  }

  if (seenTargets.size < 2) {
    issues.push({
      route,
      rule: 'keyboard-navigation',
      target: '<document>',
      message: `Tab navigation reached only ${seenTargets.size} distinct focus targets`,
    })
  }

  return issues
}

test.describe('交互完整性扫描', () => {
  for (const group of routeGroups) {
    test(`${group.name}应保持键盘和点击目标可达`, async ({ page }, testInfo) => {
      testInfo.setTimeout(60_000)

      const issues: InteractionIssue[] = []

      for (const route of group.routes) {
        await prepareRoute(page, route)
        issues.push(...await collectHitTargetIssues(page, route))
        issues.push(...await collectKeyboardIssues(page, route))
      }

      expect(
        issues.map((issue) => `[${issue.rule}] ${issue.route} ${issue.target}: ${issue.message}`),
      ).toEqual([])
    })
  }
})
