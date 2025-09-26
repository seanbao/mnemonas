import { create } from 'zustand'
import { persist } from 'zustand/middleware'

const THEMES = ['light', 'dark', 'system'] as const
type Theme = typeof THEMES[number]

interface ThemeState {
  theme: Theme
  setTheme: (theme: Theme) => void
  resolvedTheme: 'light' | 'dark'
}

const getSystemTheme = (): 'light' | 'dark' => {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return 'light'
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

const isTheme = (value: unknown): value is Theme => (
  typeof value === 'string' && THEMES.includes(value as Theme)
)

const resolveTheme = (theme: Theme): 'light' | 'dark' => (
  theme === 'system' ? getSystemTheme() : theme
)

const readPersistedTheme = (state: unknown): Theme | undefined => {
  if (!state || typeof state !== 'object') {
    return undefined
  }

  const theme = (state as { theme?: unknown }).theme
  return isTheme(theme) ? theme : undefined
}

const readStoredTheme = (storageValue: unknown): Theme | undefined => {
  if (!storageValue || typeof storageValue !== 'object') {
    return undefined
  }

  return readPersistedTheme((storageValue as { state?: unknown }).state)
}

// Apply theme to document - handles both adding and removing dark class
const applyTheme = (resolved: 'light' | 'dark') => {
  if (typeof document === 'undefined') return

  if (resolved === 'dark') {
    document.documentElement.classList.add('dark')
  } else {
    document.documentElement.classList.remove('dark')
  }
}

// Initialize theme before React hydration to prevent flash
const initializeTheme = () => {
  if (typeof window === 'undefined') return

  try {
    const stored = localStorage.getItem('mnemonas-theme')
    if (stored) {
      const parsed = JSON.parse(stored)
      const theme = readStoredTheme(parsed)
      if (theme) {
        applyTheme(resolveTheme(theme))
        return
      }
    }
  } catch {
    // Ignore parse errors
  }

  // Default to system theme
  applyTheme(getSystemTheme())
}

// Run immediately on module load
initializeTheme()

export const useThemeStore = create<ThemeState>()(
  persist(
    (set) => ({
      theme: 'system',
      resolvedTheme: getSystemTheme(),
      setTheme: (theme) => {
        const resolved = resolveTheme(theme)
        set({ theme, resolvedTheme: resolved })
        applyTheme(resolved)
      },
    }),
    {
      name: 'mnemonas-theme',
      merge: (persistedState, currentState) => {
        const theme = readPersistedTheme(persistedState) ?? currentState.theme
        return {
          ...currentState,
          theme,
          resolvedTheme: resolveTheme(theme),
        }
      },
      onRehydrateStorage: () => (state) => {
        if (state) {
          const theme = isTheme(state.theme) ? state.theme : 'system'
          const resolved = resolveTheme(theme)
          state.theme = theme
          // Update resolvedTheme in state
          state.resolvedTheme = resolved
          applyTheme(resolved)
        }
      },
    }
  )
)

// Listen for system theme changes when theme is set to 'system'
if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
  const systemThemeQuery = window.matchMedia('(prefers-color-scheme: dark)')
  const handleSystemThemeChange = () => {
    const state = useThemeStore.getState()
    if (state.theme === 'system') {
      const resolved = getSystemTheme()
      useThemeStore.setState({ resolvedTheme: resolved })
      applyTheme(resolved)
    }
  }

  if (typeof systemThemeQuery.addEventListener === 'function') {
    systemThemeQuery.addEventListener('change', handleSystemThemeChange)
  } else if (typeof systemThemeQuery.addListener === 'function') {
    systemThemeQuery.addListener(handleSystemThemeChange)
  }
}
