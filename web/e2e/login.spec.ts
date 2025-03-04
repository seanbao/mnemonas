import { test, expect } from '@playwright/test'

test.describe('登录页面', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/login')
  })

  test('应显示登录表单', async ({ page }) => {
    await expect(page.getByRole('heading', { name: /欢迎|登录|MnemoNAS/i })).toBeVisible()
    await expect(page.getByLabel(/用户名/i)).toBeVisible()
    await expect(page.getByLabel(/密码/i)).toBeVisible()
    await expect(page.getByRole('button', { name: /登录/i })).toBeVisible()
  })

  test('应显示产品品牌信息', async ({ page }) => {
    await expect(page.getByText('MnemoNAS')).toBeVisible()
  })

  test('空表单提交应显示错误或阻止提交', async ({ page }) => {
    await page.getByRole('button', { name: /登录/i }).click()
    // 应停留在登录页
    await expect(page).toHaveURL(/\/login/)
  })

  test('错误密码应显示错误提示', async ({ page }) => {
    await page.getByLabel(/用户名/i).fill('admin')
    await page.getByLabel(/密码/i).fill('wrongpassword')
    await page.getByRole('button', { name: /登录/i }).click()
    
    // 等待错误提示出现
    await expect(page.getByText(/错误|失败|invalid/i)).toBeVisible({ timeout: 5000 })
  })

  // 跳过需要真实凭据的测试 - 在 CI 环境中密码是随机生成的
  test.skip('正确凭据应登录成功', async ({ page }) => {
    // 需要从 <storage_root>/.mnemonas/initial-password.txt 获取密码
    // 此测试在手动测试时使用
    await page.getByLabel(/用户名/i).fill('admin')
    await page.getByLabel(/密码/i).fill('admin')
    await page.getByRole('button', { name: /登录/i }).click()
    
    // 成功登录应跳转到主页或仪表板
    await expect(page).not.toHaveURL(/\/login/, { timeout: 5000 })
  })

  test('视觉回归 - 登录页截图', async ({ page }) => {
    await expect(page).toHaveScreenshot('login-page.png', {
      maxDiffPixelRatio: 0.05,
    })
  })
})

test.describe('登录页响应式', () => {
  test('移动端布局', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 })
    await page.goto('/login')
    
    await expect(page.getByRole('button', { name: /登录/i })).toBeVisible()
    await expect(page).toHaveScreenshot('login-mobile.png', {
      maxDiffPixelRatio: 0.05,
    })
  })
})
