import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@/test/utils'
import userEvent from '@testing-library/user-event'
import { ThemeToggle } from './ThemeToggle'
import { useThemeStore } from '@/stores/theme'

describe('ThemeToggle', () => {
  beforeEach(() => {
    useThemeStore.setState({ theme: 'system', resolvedTheme: 'light' })
    localStorage.clear()
    document.documentElement.classList.remove('dark')
  })

  it('uses a localized accessible label for the current theme', () => {
    render(<ThemeToggle />)

    expect(screen.getByRole('button', { name: '切换主题，当前为跟随系统' })).toBeTruthy()
  })

  it('updates the accessible label when cycling themes', async () => {
    const user = userEvent.setup()
    render(<ThemeToggle />)

    await user.click(screen.getByRole('button', { name: '切换主题，当前为跟随系统' }))

    expect(screen.getByRole('button', { name: '切换主题，当前为浅色' })).toBeTruthy()
  })
})
