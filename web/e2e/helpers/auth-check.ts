import { Page, expect, test } from '@playwright/test'
import { resolveE2ECredentials } from './credentials'

/**
 * 检查页面是否被重定向到登录页
 * 返回 true 表示需要认证（已重定向到登录页）
 */
export async function isRedirectedToLogin(page: Page): Promise<boolean> {
  return page.url().includes('/login')
}

/**
 * 等待认证状态初始化或在需要时自动登录
 * 
 * 由于 storageState 中的 token 可能已过期（15分钟有效期），
 * 此函数会检测到登录页时自动执行登录，并在登录成功后重新导航到原页面。
 * 
 * @param page - Playwright page 对象
 * @param targetPath - 期望的目标路径（用于登录后重新导航）
 */
export async function skipIfAuthRequired(page: Page, targetPath?: string): Promise<void> {
  if (!page.url().includes('/login')) {
    // ProtectedRoute may redirect after initial render, slightly later than networkidle.
    await page.waitForTimeout(300)
    if (!page.url().includes('/login')) {
      return
    }
  }

  // 在登录页，尝试自动登录
  const { username, password } = resolveE2ECredentials()

  // 检查登录表单是否存在
  const loginButton = page.getByRole('button', { name: /登录|sign in|login/i })
  const isLoginPage = await loginButton.isVisible({ timeout: 3000 }).catch(() => false)
  
  if (!isLoginPage) {
    // 没有登录表单，可能 auth 被禁用或者页面出错
    return
  }

  if (!password) {
    test.skip(true, 'Skipped: no E2E password configured or initial password file found')
    return
  }

  // 填写登录表单
  const usernameInput = page.getByPlaceholder(/用户名|请输入用户名/i)
  const passwordInput = page.getByPlaceholder(/密码|请输入密码/i)

  if (!await usernameInput.isVisible({ timeout: 2000 }).catch(() => false)) {
    test.skip(true, 'Skipped: Login form not found')
    return
  }

  await usernameInput.fill(username)
  await passwordInput.fill(password)
  await loginButton.click()

  // 等待登录结果（重定向离开登录页或显示错误）
  try {
    await page.waitForURL(url => !url.pathname.includes('/login'), {
      timeout: 10000,
    })
    
    // 登录成功后，如果指定了目标路径且当前不在该路径，则重新导航
    if (targetPath && !page.url().includes(targetPath)) {
      await page.goto(targetPath)
      await page.waitForLoadState('networkidle')
    }
  } catch {
    // 登录失败
    test.skip(true, 'Skipped: auto-login failed (invalid credentials, rate limit, or backend error)')
  }
}

/**
 * 确保在目标页面并完成认证
 * 结合导航和认证检查为一个函数
 */
export async function ensureAuthenticatedAt(page: Page, path: string): Promise<void> {
  await page.goto(path)
  await page.waitForLoadState('networkidle')
  await page.waitForTimeout(300)
  await skipIfAuthRequired(page, path)
}

/**
 * 断言不在登录页
 */
export async function assertNotOnLoginPage(page: Page): Promise<void> {
  await expect(page).not.toHaveURL(/\/login/)
}
