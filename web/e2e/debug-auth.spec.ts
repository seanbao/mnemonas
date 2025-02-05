import { test, expect } from '@playwright/test'
import { skipIfAuthRequired } from './helpers/auth-check'

test('debug auth state', async ({ page }) => {
  await page.goto('/files')
  await page.waitForLoadState('networkidle')
  
  console.log('=== Before skipIfAuthRequired ===')
  console.log('URL:', page.url())
  const token1 = await page.evaluate(() => localStorage.getItem('mnemonas_token'))
  console.log('Token:', token1 ? 'exists' : 'null')
  
  await skipIfAuthRequired(page)
  
  console.log('=== After skipIfAuthRequired ===')
  console.log('URL:', page.url())
  const token2 = await page.evaluate(() => localStorage.getItem('mnemonas_token'))
  console.log('Token:', token2 ? 'exists' : 'null')
})
