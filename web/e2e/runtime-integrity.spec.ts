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

type RuntimeIssue = {
  route: string
  rule: string
  message: string
}

const ignoredConsoleErrorPatterns = [
  /Failed to load resource: the server responded with a status of 40[134]/i,
  /Failed to load resource: the server responded with a status of 410 \(Gone\).*\/api\/v1\/public\/shares\//i,
  /favicon\.ico/i,
]

function shouldIgnoreConsoleError(text: string, url = '') {
  return ignoredConsoleErrorPatterns.some((pattern) => pattern.test(`${text} ${url}`))
}

function shouldIgnoreRequestFailure(resourceType: string, failureText: string) {
  if (resourceType !== 'fetch' && resourceType !== 'xhr') {
    return false
  }

  return /Load request cancell?ed|ERR_ABORTED|NS_BINDING_ABORTED/i.test(failureText)
}

async function prepareRoute(page: Page, route: string) {
  await ensureAuthenticatedAt(page, route)
  await waitForRouteSettled(page, route, { waitForNetworkIdle: true })
}

function startRuntimeIssueCollection(page: Page, issues: RuntimeIssue[]) {
  let currentRoute = '<startup>'

  page.on('pageerror', (error) => {
    issues.push({
      route: currentRoute,
      rule: 'pageerror',
      message: error.stack || error.message,
    })
  })

  page.on('console', (message) => {
    if (message.type() !== 'error') {
      return
    }

    const text = message.text()
    const location = message.location()
    if (shouldIgnoreConsoleError(text, location.url)) {
      return
    }
    const locationLabel = location.url ? ` (${location.url})` : ''

    issues.push({
      route: currentRoute,
      rule: 'console-error',
      message: `${text}${locationLabel}`,
    })
  })

  page.on('requestfailed', (request) => {
    const resourceType = request.resourceType()
    if (resourceType === 'websocket' || resourceType === 'eventsource') {
      return
    }
    const failureText = request.failure()?.errorText ?? 'unknown error'
    if (shouldIgnoreRequestFailure(resourceType, failureText)) {
      return
    }

    issues.push({
      route: currentRoute,
      rule: 'request-failed',
      message: `${request.method()} ${request.url()} failed: ${failureText}`,
    })
  })

  page.on('response', (response) => {
    const request = response.request()
    const resourceType = request.resourceType()
    const status = response.status()
    const url = response.url()
    const criticalAssetFailed = ['document', 'script', 'stylesheet'].includes(resourceType) && status >= 400
    const serverError = status >= 500

    if (!criticalAssetFailed && !serverError) {
      return
    }

    issues.push({
      route: currentRoute,
      rule: 'bad-response',
      message: `${request.method()} ${url} returned ${status}`,
    })
  })

  return {
    setRoute(route: string) {
      currentRoute = route
    },
  }
}

async function collectVisibleRuntimeErrors(page: Page, route: string): Promise<RuntimeIssue[]> {
  return page.evaluate((currentRoute) => {
    const issueTexts = [
      'Application error',
      'Cannot read properties',
      'ChunkLoadError',
      'Minified React error',
      'ReferenceError',
      'TypeError',
      'Unhandled Runtime Error',
      'Uncaught',
      'is not a function',
    ]

    const candidates = [
      'vite-error-overlay',
      '[data-nextjs-dialog-overlay]',
      '[role="alert"]',
      'pre',
    ].join(',')

    const issues: RuntimeIssue[] = []
    for (const element of Array.from(document.querySelectorAll(candidates))) {
      if (!(element instanceof HTMLElement)) {
        continue
      }
      const style = window.getComputedStyle(element)
      const rect = element.getBoundingClientRect()
      const visible = style.display !== 'none'
        && style.visibility !== 'hidden'
        && Number.parseFloat(style.opacity || '1') > 0.01
        && rect.width > 0
        && rect.height > 0

      if (!visible) {
        continue
      }

      const text = (element.textContent ?? '').replace(/\s+/g, ' ').trim()
      const matched = issueTexts.find((issueText) => text.includes(issueText))
      if (!matched) {
        continue
      }

      issues.push({
        route: currentRoute,
        rule: 'visible-runtime-error',
        message: `${matched}: ${text.slice(0, 240)}`,
      })
    }

    return issues
  }, route)
}

test.describe('前端运行时完整性扫描', () => {
  test('核心页面不应出现浏览器运行时错误或关键资源加载失败', async ({ page }, testInfo) => {
    testInfo.setTimeout(120_000)

    const issues: RuntimeIssue[] = []
    const diagnostics = startRuntimeIssueCollection(page, issues)

    for (const route of routes) {
      diagnostics.setRoute(route)
      await prepareRoute(page, route)
      issues.push(...await collectVisibleRuntimeErrors(page, route))
    }

    expect(
      issues.map((issue) => `[${issue.rule}] ${issue.route}: ${issue.message}`),
    ).toEqual([])
  })
})

test.describe('公开入口运行时完整性扫描', () => {
  test.use({
    storageState: { cookies: [], origins: [] },
  })

  test('登录页和公开分享页不应出现浏览器运行时错误或关键资源加载失败', async ({ page }, testInfo) => {
    testInfo.setTimeout(60_000)

    const issues: RuntimeIssue[] = []
    const diagnostics = startRuntimeIssueCollection(page, issues)

    for (const route of publicEntryRoutes()) {
      diagnostics.setRoute(route)
      await page.goto(route, { waitUntil: 'domcontentloaded' })
      await waitForRouteSettled(page, route, { waitForNetworkIdle: true })
      issues.push(...await collectVisibleRuntimeErrors(page, route))
    }

    expect(
      issues.map((issue) => `[${issue.rule}] ${issue.route}: ${issue.message}`),
    ).toEqual([])
  })
})
