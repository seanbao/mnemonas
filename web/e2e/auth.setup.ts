import { test as setup } from '@playwright/test'
import { resolveE2ECredentials } from './helpers/credentials'

const STORAGE_STATE_PATH = './e2e/.auth/user.json'

/**
 * Playwright 认证设置
 * 在所有测试运行前执行登录，保存认证状态供后续测试使用
 * 
 * 使用环境变量配置测试账号：
 * - E2E_USERNAME: 测试用户名（默认 admin）
 * - E2E_PASSWORD: 测试密码（默认 changeme）
 * 
 * 认证处理策略：
 * 1. 先尝试直接访问 /files，如果不被重定向到 /login，说明认证已禁用
 * 2. 如果被重定向到 /login，尝试使用测试账号登录
 * 3. 如果登录失败（后端未运行或账号错误），保存空状态让测试继续运行
 */
setup('authenticate', async ({ page }) => {
  const credentials = resolveE2ECredentials()
  const { username, password, passwordSource } = credentials

  // 先尝试直接访问受保护页面
  await page.goto('/files', { waitUntil: 'domcontentloaded' })
  await page.locator('body').waitFor({ state: 'visible' })
  await page.waitForTimeout(500)

  // 检查是否被重定向到登录页
  if (!page.url().includes('/login')) {
    console.log('Authentication disabled or already logged in, skipping login')
    await page.context().storageState({ path: STORAGE_STATE_PATH })
    return
  }

  if (!password) {
    console.log('No E2E password configured or discoverable, saving empty auth state')
    await page.context().storageState({ path: STORAGE_STATE_PATH })
    return
  }

  console.log(`Authentication required, attempting login with ${passwordSource} password...`)

  // 检查登录表单是否存在
  const loginButton = page.getByRole('button', { name: /登录|sign in|login/i })
  const isLoginPage = await loginButton.isVisible({ timeout: 3000 }).catch(() => false)
  
  if (!isLoginPage) {
    console.log('No login form found, saving empty auth state')
    await page.context().storageState({ path: STORAGE_STATE_PATH })
    return
  }

  // 填写登录表单
  const usernameInput = page.getByPlaceholder(/用户名|请输入用户名/i)
  const passwordInput = page.getByPlaceholder(/密码|请输入密码/i)

  await usernameInput.fill(username)
  await passwordInput.fill(password)

  // 点击登录按钮
  await loginButton.click()

  // 等待登录结果（重定向或错误提示）
  try {
    await page.waitForURL(url => !url.pathname.includes('/login'), {
      timeout: 10000,
    })
    console.log(`Authenticated as ${username}`)
  } catch {
    // 登录失败（可能是后端未运行或账号错误）
    const errorToast = page.locator('[class*="toast"], [class*="alert"], [role="alert"]')
    const hasError = await errorToast.isVisible({ timeout: 1000 }).catch(() => false)
    
    if (hasError) {
      console.log('Login failed (invalid credentials, rate limit, or backend error), tests will run in unauthenticated mode')
    } else {
      console.log('Login timeout (backend may not be running), tests will run in unauthenticated mode')
    }
  }

  // 保存认证状态（无论成功与否）
  await page.context().storageState({ path: STORAGE_STATE_PATH })
})
