import { expect, test, type Page } from '@playwright/test'
import { ensureAuthenticatedAt } from './helpers/auth-check'
import { waitForRouteSettled } from './helpers/route-ready'

test.use({
  colorScheme: 'light',
})

async function stabilizeConsumerShell(page: Page): Promise<void> {
  await page.emulateMedia({ colorScheme: 'light', reducedMotion: 'reduce' })

  await page.route('**/api/v1/stats', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: {
          total_files: 12,
          total_files_available: true,
          storage_stats_available: true,
          disk_stats_available: true,
          directory_quota_stats_available: true,
          total_size: 3_221_225_472,
          total_chunks: 1_024,
          unique_size: 2_147_483_648,
          dedup_ratio: 1.5,
          disk_total: 1_099_511_627_776,
          disk_free: 687_194_767_360,
          disk_available: 687_194_767_360,
          disk_used: 412_316_860_416,
          disk_usage_ratio: 0.375,
          disk_filesystem_type: 'ext4',
          disk_mount_point: '/srv/mnemonas',
          disk_mount_source: '/dev/visual-test',
          directory_quotas: [],
        },
      }),
    })
  })

  await page.route('**/api/v1/version', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        data: {
          name: 'MnemoNAS',
          version: 'visual-test',
          go: 'go1.24.0',
          build_time: '2026-01-01T00:00:00Z',
        },
      }),
    })
  })

  await page.addInitScript(() => {
    window.localStorage.setItem('mnemonas-theme', JSON.stringify({
      state: { theme: 'light', resolvedTheme: 'light' },
      version: 0,
    }))
  })
}

test.describe('消费级设置概览视觉回归', () => {
  test.beforeEach(async ({ page }) => {
    await stabilizeConsumerShell(page)
    await ensureAuthenticatedAt(page, '/settings')
    await waitForRouteSettled(page, '/settings')
    await expect(page.getByRole('heading', { name: '按使用目标调整设备' })).toBeVisible()
    await expect(page.getByRole('button', { name: /设备状态与通知/ })).toBeAttached()
    await page.evaluate(async () => {
      await document.fonts.ready
    })
    await page.addStyleTag({
      content: `
        *, *::before, *::after {
          animation-duration: 0s !important;
          animation-delay: 0s !important;
          transition-duration: 0s !important;
          transition-delay: 0s !important;
          caret-color: transparent !important;
        }
      `,
    })
  })

  test('默认概览在桌面端和移动端保持稳定', async ({ page }) => {
    await expect(page).toHaveScreenshot('consumer-settings-overview.png', {
      animations: 'disabled',
      caret: 'hide',
      mask: [page.getByRole('button', { name: '打开用户菜单' })],
      maskColor: '#e5e7eb',
      maxDiffPixelRatio: 0.005,
    })
  })
})
