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

type AccessibilityIssue = {
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

async function collectAccessibilityIssues(page: Page, route: string): Promise<AccessibilityIssue[]> {
  return page.evaluate((currentRoute) => {
    const issues: AccessibilityIssue[] = []
    const focusableSelector = [
      'a[href]',
      'button',
      'input',
      'select',
      'textarea',
      '[role="button"]',
      '[role="link"]',
      '[role="menuitem"]',
      '[role="checkbox"]',
      '[role="switch"]',
      '[role="tab"]',
      '[role="textbox"]',
      '[role="combobox"]',
      '[tabindex]:not([tabindex="-1"])',
    ].join(',')

    const describeTarget = (element: Element) => {
      const tagName = element.tagName.toLowerCase()
      const role = element.getAttribute('role')
      const id = element.getAttribute('id')
      const ariaLabel = element.getAttribute('aria-label')
      const text = (element.textContent ?? '').replace(/\s+/g, ' ').trim()
      const parts = [tagName]

      if (role) parts.push(`[role="${role}"]`)
      if (id) parts.push(`#${id}`)
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

    const isHiddenFromUsers = (element: Element) => {
      return !!element.closest('[hidden], [inert], [aria-hidden="true"]')
    }

    const getReferencedText = (element: Element, attribute: string) => {
      const ids = element.getAttribute(attribute)?.split(/\s+/).filter(Boolean) ?? []
      return ids
        .map((id) => document.getElementById(id)?.textContent?.trim() ?? '')
        .filter(Boolean)
        .join(' ')
    }

    const getAccessibleName = (element: Element) => {
      const ariaLabel = element.getAttribute('aria-label')?.trim()
      if (ariaLabel) return ariaLabel

      const labelledByText = getReferencedText(element, 'aria-labelledby')
      if (labelledByText) return labelledByText

      const title = element.getAttribute('title')?.trim()
      if (title) return title

      if (element instanceof HTMLInputElement) {
        const labelText = element.labels ? Array.from(element.labels).map((label) => label.textContent?.trim() ?? '').join(' ') : ''
        if (labelText.trim()) return labelText.trim()
        if (element.placeholder.trim()) return element.placeholder.trim()
      }

      if (element instanceof HTMLTextAreaElement || element instanceof HTMLSelectElement) {
        const labelText = element.labels ? Array.from(element.labels).map((label) => label.textContent?.trim() ?? '').join(' ') : ''
        if (labelText.trim()) return labelText.trim()
      }

      return (element.textContent ?? '').replace(/\s+/g, ' ').trim()
    }

    const isKeyboardFocusable = (element: Element) => {
      if (!(element instanceof HTMLElement)) {
        return false
      }

      if (element.hasAttribute('disabled') || element.getAttribute('aria-disabled') === 'true') {
        return false
      }

      return element.tabIndex >= 0 && isElementVisible(element)
    }

    const hasFocusableDescendant = (element: Element) => {
      return Array.from(element.querySelectorAll(focusableSelector)).some((child) => isKeyboardFocusable(child))
    }

    const interactiveElements = Array.from(document.querySelectorAll(focusableSelector))
    for (const element of interactiveElements) {
      if (!isElementVisible(element) || isHiddenFromUsers(element)) {
        continue
      }

      if (!getAccessibleName(element)) {
        issues.push({
          route: currentRoute,
          rule: 'interactive-name',
          target: describeTarget(element),
          message: 'visible interactive element has no accessible name',
        })
      }
    }

    const ids = new Map<string, Element[]>()
    for (const element of Array.from(document.querySelectorAll('[id]'))) {
      const id = element.id
      if (!id) continue
      ids.set(id, [...(ids.get(id) ?? []), element])
    }
    for (const [id, elements] of ids) {
      if (elements.length > 1) {
        issues.push({
          route: currentRoute,
          rule: 'duplicate-id',
          target: `#${id}`,
          message: `id is used ${elements.length} times`,
        })
      }
    }

    for (const element of Array.from(document.querySelectorAll('[aria-labelledby], [aria-describedby]'))) {
      for (const attribute of ['aria-labelledby', 'aria-describedby']) {
        const ids = element.getAttribute(attribute)?.split(/\s+/).filter(Boolean) ?? []
        for (const id of ids) {
          if (!document.getElementById(id)) {
            issues.push({
              route: currentRoute,
              rule: 'aria-reference',
              target: describeTarget(element),
              message: `${attribute} references missing id "${id}"`,
            })
          }
        }
      }
    }

    for (const element of Array.from(document.querySelectorAll('[aria-hidden="true"]'))) {
      if (hasFocusableDescendant(element)) {
        issues.push({
          route: currentRoute,
          rule: 'aria-hidden-focus',
          target: describeTarget(element),
          message: 'aria-hidden subtree contains a visible focusable element',
        })
      }
    }

    for (const image of Array.from(document.images)) {
      if (!isElementVisible(image) || isHiddenFromUsers(image)) {
        continue
      }

      const isDecorative = image.getAttribute('role') === 'presentation' || image.getAttribute('role') === 'none'
      if (!isDecorative && !image.hasAttribute('alt')) {
        issues.push({
          route: currentRoute,
          rule: 'image-alt',
          target: describeTarget(image),
          message: 'visible image is missing alt text',
        })
      }
    }

    return issues
  }, route)
}

test.describe('可访问性语义扫描', () => {
  for (const group of routeGroups) {
    test(`${group.name}应满足基础语义规则`, async ({ page }, testInfo) => {
      testInfo.setTimeout(60_000)

      const issues: AccessibilityIssue[] = []

      for (const route of group.routes) {
        await prepareRoute(page, route)
        issues.push(...await collectAccessibilityIssues(page, route))
      }

      expect(
        issues.map((issue) => `[${issue.rule}] ${issue.route} ${issue.target}: ${issue.message}`),
      ).toEqual([])
    })
  }
})
