import { test, expect, type Page } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'

const coreRouteGroups = [
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

const brokenVisibleTextPatterns = [
  /\bundefined\b/i,
  /\bNaN\b/,
  /\bInvalid Date\b/i,
  /\[object Object\]/,
]

type RuntimeIssue = {
  kind: 'console' | 'pageerror' | 'requestfailed' | 'server-response'
  route: string
  message: string
}

function startRuntimeDiagnostics(page: Page) {
  const issues: RuntimeIssue[] = []
  let currentRoute = '<startup>'

  page.on('console', (message) => {
    if (message.type() !== 'error') {
      return
    }

    issues.push({
      kind: 'console',
      route: currentRoute,
      message: message.text(),
    })
  })

  page.on('pageerror', (error) => {
    issues.push({
      kind: 'pageerror',
      route: currentRoute,
      message: error.message,
    })
  })

  page.on('requestfailed', (request) => {
    issues.push({
      kind: 'requestfailed',
      route: currentRoute,
      message: `${request.method()} ${request.url()} failed: ${request.failure()?.errorText ?? 'unknown failure'}`,
    })
  })

  page.on('response', (response) => {
    if (response.status() < 500) {
      return
    }

    issues.push({
      kind: 'server-response',
      route: currentRoute,
      message: `${response.status()} ${response.request().method()} ${response.url()}`,
    })
  })

  return {
    setRoute(route: string) {
      currentRoute = route
    },
    expectClean() {
      expect(
        issues.map((issue) => `[${issue.kind}] ${issue.route}: ${issue.message}`),
        'core routes should not emit console errors, page errors, failed requests, or 5xx responses',
      ).toEqual([])
    },
  }
}

async function expectNoBrokenVisibleText(page: Page, route: string) {
  const bodyText = await page.locator('body').innerText({ timeout: 10_000 })

  for (const pattern of brokenVisibleTextPatterns) {
    expect(bodyText, `${route} should not expose broken placeholder text matching ${pattern}`).not.toMatch(pattern)
  }
}

test.describe('运行时诊断', () => {
  for (const group of coreRouteGroups) {
    test(`${group.name}不应产生运行时错误、失败请求或破碎可见文本`, async ({ page }, testInfo) => {
      testInfo.setTimeout(60_000)

      const diagnostics = startRuntimeDiagnostics(page)

      for (const route of group.routes) {
        diagnostics.setRoute(route)
        await ensureAuthenticatedAt(page, route)
        await expect(page.locator('body')).toBeVisible()
        await page.getByText('加载中...').waitFor({ state: 'hidden', timeout: 10_000 }).catch(() => {})
        await page.waitForTimeout(500)
        await expectNoBrokenVisibleText(page, route)
      }

      diagnostics.expectClean()
    })
  }
})
