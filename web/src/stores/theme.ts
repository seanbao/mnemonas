import { create } from 'zustand'
import { persist } from 'zustand/middleware'

type Theme = 'light' | 'dark' | 'system'

interface ThemeState {
  theme: Theme
  setTheme: (theme: Theme) => void
  resolvedTheme: 'light' | 'dark'
}

const getSystemTheme = (): 'light' | 'dark' => {
  if (typeof window === 'undefined') return 'light'
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
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
      const theme = parsed?.state?.theme as Theme | undefined
      if (theme) {
        const resolved = theme === 'system' ? getSystemTheme() : theme
        applyTheme(resolved)
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
        const resolved = theme === 'system' ? getSystemTheme() : theme
        set({ theme, resolvedTheme: resolved })
        applyTheme(resolved)
      },
    }),
    {
      name: 'mnemonas-theme',
      onRehydrateStorage: () => (state) => {
        if (state) {
          const resolved = state.theme === 'system' ? getSystemTheme() : state.theme
          // Update resolvedTheme in state
          state.resolvedTheme = resolved
          applyTheme(resolved)
        }
      },
    }
  )
)

// Listen for system theme changes when theme is set to 'system'
if (typeof window !== 'undefined') {
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    const state = useThemeStore.getState()
    if (state.theme === 'system') {
      const resolved = getSystemTheme()
      useThemeStore.setState({ resolvedTheme: resolved })
      applyTheme(resolved)
    }
  })
}
