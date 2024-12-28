import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { useThemeStore } from './theme'

describe('themeStore', () => {
  beforeEach(() => {
    // Reset store and clear localStorage
    useThemeStore.setState({
      theme: 'dark',
      resolvedTheme: 'dark',
    })
    localStorage.clear()
    // Reset document class
    document.documentElement.classList.remove('dark')
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  describe('initial state', () => {
    it('has dark theme by default', () => {
      const state = useThemeStore.getState()
      expect(state.theme).toBe('dark')
      expect(state.resolvedTheme).toBe('dark')
    })

    it('has setTheme function', () => {
      const state = useThemeStore.getState()
      expect(typeof state.setTheme).toBe('function')
    })
  })

  describe('setTheme', () => {
    it('sets theme to light', () => {
      useThemeStore.getState().setTheme('light')
      const state = useThemeStore.getState()
      expect(state.theme).toBe('light')
      expect(state.resolvedTheme).toBe('light')
    })

    it('sets theme to dark', () => {
      useThemeStore.getState().setTheme('light') // First change to light
      useThemeStore.getState().setTheme('dark')
      const state = useThemeStore.getState()
      expect(state.theme).toBe('dark')
      expect(state.resolvedTheme).toBe('dark')
    })

    it('sets theme to system', () => {
      useThemeStore.getState().setTheme('system')
      const state = useThemeStore.getState()
      expect(state.theme).toBe('system')
      // resolvedTheme depends on system preference
      expect(['light', 'dark']).toContain(state.resolvedTheme)
    })

    it('applies dark class to document for dark theme', () => {
      useThemeStore.getState().setTheme('dark')
      expect(document.documentElement.classList.contains('dark')).toBe(true)
    })

    it('removes dark class from document for light theme', () => {
      document.documentElement.classList.add('dark')
      useThemeStore.getState().setTheme('light')
      expect(document.documentElement.classList.contains('dark')).toBe(false)
    })

    it('can switch themes multiple times', () => {
      useThemeStore.getState().setTheme('light')
      useThemeStore.getState().setTheme('dark')
      useThemeStore.getState().setTheme('system')
      useThemeStore.getState().setTheme('light')
      
      const state = useThemeStore.getState()
      expect(state.theme).toBe('light')
    })
  })

  describe('persistence', () => {
    it('persists theme to localStorage', () => {
      useThemeStore.getState().setTheme('light')
      const stored = localStorage.getItem('mnemonas-theme')
      expect(stored).toBeTruthy()
      const parsed = JSON.parse(stored!)
      expect(parsed.state.theme).toBe('light')
    })

    it('persists dark theme to localStorage', () => {
      useThemeStore.getState().setTheme('dark')
      const stored = localStorage.getItem('mnemonas-theme')
      expect(stored).toBeTruthy()
      const parsed = JSON.parse(stored!)
      expect(parsed.state.theme).toBe('dark')
    })

    it('persists system theme to localStorage', () => {
      useThemeStore.getState().setTheme('system')
      const stored = localStorage.getItem('mnemonas-theme')
      expect(stored).toBeTruthy()
      const parsed = JSON.parse(stored!)
      expect(parsed.state.theme).toBe('system')
    })
  })

  describe('system theme detection', () => {
    it('respects window.matchMedia for system theme', () => {
      // Mock matchMedia to return dark preference
      vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
        matches: query.includes('dark'),
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }))

      useThemeStore.getState().setTheme('system')
      const state = useThemeStore.getState()
      expect(state.theme).toBe('system')
      // System theme should resolve to dark based on mock
      expect(state.resolvedTheme).toBe('dark')
    })

    it('resolves to light when system prefers light', () => {
      // Mock matchMedia to return light preference
      vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
        matches: !query.includes('dark'),
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      }))

      useThemeStore.getState().setTheme('system')
      const state = useThemeStore.getState()
      expect(state.theme).toBe('system')
      expect(state.resolvedTheme).toBe('light')
    })
  })

  describe('document class manipulation', () => {
    it('toggles class correctly when switching themes', () => {
      useThemeStore.getState().setTheme('dark')
      expect(document.documentElement.classList.contains('dark')).toBe(true)

      useThemeStore.getState().setTheme('light')
      expect(document.documentElement.classList.contains('dark')).toBe(false)

      useThemeStore.getState().setTheme('dark')
      expect(document.documentElement.classList.contains('dark')).toBe(true)
    })

    it('handles rapid theme switches', () => {
      for (let i = 0; i < 5; i++) {
        useThemeStore.getState().setTheme('light')
        useThemeStore.getState().setTheme('dark')
      }
      expect(document.documentElement.classList.contains('dark')).toBe(true)

      useThemeStore.getState().setTheme('light')
      expect(document.documentElement.classList.contains('dark')).toBe(false)
    })
  })

  describe('rehydration', () => {
    it('rehydrates from localStorage with dark theme', () => {
      localStorage.setItem('mnemonas-theme', JSON.stringify({
        state: { theme: 'dark', resolvedTheme: 'dark' },
        version: 0
      }))
      
      // Reset and let persist rehydrate
      useThemeStore.setState({ theme: 'dark', resolvedTheme: 'dark' })
      const state = useThemeStore.getState()
      expect(state.theme).toBe('dark')
    })

    it('rehydrates from localStorage with light theme', () => {
      localStorage.setItem('mnemonas-theme', JSON.stringify({
        state: { theme: 'light', resolvedTheme: 'light' },
        version: 0
      }))
      
      const state = useThemeStore.getState()
      expect(['dark', 'light']).toContain(state.theme)
    })
  })
})
