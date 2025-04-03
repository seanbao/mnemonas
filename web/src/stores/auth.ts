import { create } from 'zustand'
import type { User } from '@/api/auth'
import { 
  AUTH_CLEARED_EVENT,
  login as apiLogin, 
  logout as apiLogout, 
  getCurrentUser,
  getStoredToken,
  getStoredUser,
} from '@/api/auth'
import { acknowledgeSetup, getSetupStatus } from '@/api/setup'

let authStateEpoch = 0
let initializeRunId = 0

function bumpAuthStateEpoch(): number {
  authStateEpoch += 1
  return authStateEpoch
}

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
    const runId = ++initializeRunId
    const startEpoch = authStateEpoch
    const isCurrent = () => runId === initializeRunId && authStateEpoch === startEpoch

    set({ isLoading: true, error: null, authEnabled: true })
    
    // First, check if auth is enabled on the server
    try {
      const setupStatus = await getSetupStatus()
      if (!isCurrent()) {
        return
      }

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
      if (isCurrent()) {
        set({ isLoading: false, isAuthenticated: false, user: null })
      }
      return
    }

    const storedUser = getStoredUser()
    
    // Try to get current user (validates token)
    try {
      const user = await getCurrentUser()
      if (!isCurrent()) {
        return
      }

      if (user) {
        set({ user, isAuthenticated: true, isLoading: false })
      } else {
        // Token invalid
        set({ user: null, isAuthenticated: false, isLoading: false })
      }
    } catch {
      if (!isCurrent()) {
        return
      }

      // Terminal auth failures clear local state inside the auth API and return null above.
      // Preserve the cached session only when validation is temporarily unavailable.
      if (storedUser && getStoredToken()) {
        set({ user: storedUser, isAuthenticated: true, isLoading: false, error: null })
        return
      }

      set({ user: null, isAuthenticated: false, isLoading: false })
    }
  },
  
  login: async (username: string, password: string) => {
    bumpAuthStateEpoch()
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
    bumpAuthStateEpoch()
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

export function useIsGuest() {
  return useAuthStore((state) => state.user?.role === 'guest')
}

export function useCanWrite() {
  return useAuthStore((state) => !state.authEnabled || state.user?.role === 'admin' || state.user?.role === 'user')
}

export function useAuthLoading() {
  return useAuthStore((state) => state.isLoading)
}

export function useAuthError() {
  return useAuthStore((state) => state.error)
}

const AUTH_CLEARED_LISTENER_KEY = '__mnemonasAuthClearedListenerRegistered__'

if (typeof window !== 'undefined') {
  const markerWindow = window as Window & { [AUTH_CLEARED_LISTENER_KEY]?: boolean }

  if (!markerWindow[AUTH_CLEARED_LISTENER_KEY]) {
    window.addEventListener(AUTH_CLEARED_EVENT, (event) => {
      bumpAuthStateEpoch()
      const state = useAuthStore.getState()
      if (!state.isAuthenticated && !state.user && !state.isLoading) {
        return
      }

      const detail = event instanceof CustomEvent ? event.detail : undefined

      useAuthStore.setState({
        user: null,
        isAuthenticated: false,
        isLoading: false,
        error: detail?.message ?? state.error ?? '登录已过期，请重新登录',
      })
    })

    markerWindow[AUTH_CLEARED_LISTENER_KEY] = true
  }
}
