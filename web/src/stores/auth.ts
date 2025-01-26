import { create } from 'zustand'
import type { User } from '@/api/auth'
import { 
  login as apiLogin, 
  logout as apiLogout, 
  getCurrentUser,
  getStoredUser,
  getStoredToken,
} from '@/api/auth'
import { acknowledgeSetup, getSetupStatus } from '@/api/setup'

interface AuthState {
  user: User | null
  isAuthenticated: boolean
  isLoading: boolean
  error: string | null
  
  // Whether auth is enabled on the server (default: true for security)
  authEnabled: boolean
  
  // Actions
  initialize: () => Promise<void>
  login: (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
  clearError: () => void
  setAuthEnabled: (enabled: boolean) => void
}

export const useAuthStore = create<AuthState>((set) => ({
  user: null,
  isAuthenticated: false,
  isLoading: true,
  error: null,
  // Default to true for security - auth is required unless explicitly disabled by server
  authEnabled: true,
  
  initialize: async () => {
    set({ isLoading: true, error: null })
    
    // First, check if auth is enabled on the server
    try {
      const setupStatus = await getSetupStatus()
      if (!setupStatus.auth_enabled) {
        // Auth is disabled on server, skip login requirement
        set({ 
          authEnabled: false, 
          isAuthenticated: true, // Treat as authenticated when auth is disabled
          isLoading: false,
          user: { id: 'guest', username: 'guest', role: 'admin' as const, email: '', homeDir: '/' }
        })
        return
      }
    } catch {
      // If we can't get setup status, assume auth is enabled for security
    }
    
    // Check if there's a stored token
    const token = getStoredToken()
    if (!token) {
      set({ isLoading: false, isAuthenticated: false, user: null })
      return
    }
    
    // Try to get current user (validates token)
    try {
      const user = await getCurrentUser()
      if (user) {
        set({ user, isAuthenticated: true, isLoading: false })
      } else {
        // Token invalid
        set({ user: null, isAuthenticated: false, isLoading: false })
      }
    } catch {
      // Use stored user as fallback
      const storedUser = getStoredUser()
      if (storedUser) {
        set({ user: storedUser, isAuthenticated: true, isLoading: false })
      } else {
        set({ user: null, isAuthenticated: false, isLoading: false })
      }
    }
  },
  
  login: async (username: string, password: string) => {
    set({ isLoading: true, error: null })
    
    try {
      const user = await apiLogin(username, password)

      set({ user, isAuthenticated: true, isLoading: false, error: null })

      if (user.role === 'admin') {
        void (async () => {
          try {
            const setupStatus = await getSetupStatus()
            if (setupStatus.is_first_run) {
              await acknowledgeSetup()
            }
          } catch {
            // Ignore setup acknowledgement failures to avoid blocking login.
          }
        })()
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : '登录失败'
      set({ isLoading: false, error: message })
      throw err
    }
  },
  
  logout: async () => {
    set({ isLoading: true })
    
    try {
      await apiLogout()
    } finally {
      set({ user: null, isAuthenticated: false, isLoading: false, error: null })
    }
  },
  
  clearError: () => set({ error: null }),
  
  setAuthEnabled: (enabled: boolean) => set({ authEnabled: enabled }),
}))

// Selector hooks for common use cases
export function useUser() {
  return useAuthStore((state) => state.user)
}

export function useIsAuthenticated() {
  return useAuthStore((state) => state.isAuthenticated)
}

export function useIsAdmin() {
  return useAuthStore((state) => state.user?.role === 'admin')
}

export function useAuthLoading() {
  return useAuthStore((state) => state.isLoading)
}

export function useAuthError() {
  return useAuthStore((state) => state.error)
}
