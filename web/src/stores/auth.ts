import { create } from 'zustand'
import type { AuthActionResult, User } from '@/api/auth'
import { 
  AUTH_CLEARED_EVENT,
  login as apiLogin, 
  logout as apiLogout, 
  getCurrentUser,
  getStoredUser,
} from '@/api/auth'
import { acknowledgeSetup, getSetupStatus } from '@/api/setup'
import { queryClient } from '@/lib/queryClient'

let authStateEpoch = 0
let initializeRunId = 0
let initializeAbortController: AbortController | null = null
let postLoginSetupAbortController: AbortController | null = null

function bumpAuthStateEpoch(): number {
  authStateEpoch += 1
  return authStateEpoch
}

function cancelPendingInitialize(): void {
  initializeAbortController?.abort()
  initializeAbortController = null
}

function cancelPendingPostLoginSetup(): void {
  postLoginSetupAbortController?.abort()
  postLoginSetupAbortController = null
}

function getAuthStoreActionErrorMessage(error: unknown, fallback: string): string {
  if (error instanceof Error && error.name === 'AuthError' && error.message) {
    return error.message
  }

  return fallback
}

interface AuthState {
  user: User | null
  isAuthenticated: boolean
  isLoading: boolean
  error: string | null
  
  // Whether auth is enabled on the server (default: true for security)
  authEnabled: boolean
  shareEnabled: boolean | null
  
  // Actions
  initialize: () => Promise<void>
  login: (username: string, password: string) => Promise<AuthActionResult>
  logout: () => Promise<AuthActionResult>
  clearError: () => void
  setAuthEnabled: (enabled: boolean) => void
  setShareEnabled: (enabled: boolean | null) => void
}

export const useAuthStore = create<AuthState>((set) => ({
  user: null,
  isAuthenticated: false,
  isLoading: true,
  error: null,
  // Default to true for security - auth is required unless explicitly disabled by server
  authEnabled: true,
  shareEnabled: null,
  
  initialize: async () => {
    cancelPendingInitialize()
    const runId = ++initializeRunId
    const startEpoch = authStateEpoch
    const controller = new AbortController()
    initializeAbortController = controller
    const isCurrent = () => runId === initializeRunId
      && authStateEpoch === startEpoch
      && initializeAbortController === controller
      && !controller.signal.aborted

    set({ isLoading: true, error: null, authEnabled: true, shareEnabled: null })

    try {
      // First, check if auth is enabled on the server
      try {
        const setupStatus = await getSetupStatus({ signal: controller.signal })
        if (!isCurrent()) {
          return
        }
        set({ shareEnabled: setupStatus.share_enabled ?? null })

        if (!setupStatus.auth_enabled) {
          // Auth is disabled on server, skip login requirement
          set({
            authEnabled: false,
            shareEnabled: setupStatus.share_enabled ?? null,
            isAuthenticated: true, // Treat as authenticated when auth is disabled
            isLoading: false,
            user: { id: 'guest', username: 'guest', role: 'admin' as const, email: '', homeDir: '/' }
          })
          return
        }
      } catch {
        if (!isCurrent()) {
          return
        }
        // If we can't get setup status, assume auth is enabled for security
      }

      if (!isCurrent()) {
        return
      }

      const storedUser = getStoredUser()

      // Try to get current user. The browser sends the HttpOnly session cookie.
      try {
        const user = await getCurrentUser({ signal: controller.signal })
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
        if (storedUser) {
          set({ user: storedUser, isAuthenticated: true, isLoading: false, error: null })
          return
        }

        set({ user: null, isAuthenticated: false, isLoading: false })
      }
    } finally {
      if (initializeAbortController === controller) {
        initializeAbortController = null
      }
    }
  },
  
  login: async (username: string, password: string) => {
    const loginEpoch = bumpAuthStateEpoch()
    cancelPendingInitialize()
    cancelPendingPostLoginSetup()
    set({ isLoading: true, error: null })
    
    try {
      const result = await apiLogin(username, password)
      const user = result.user

      set({ user, isAuthenticated: true, isLoading: false, error: null })

      if (user.role === 'admin') {
        const controller = new AbortController()
        postLoginSetupAbortController = controller
        const isCurrent = () => authStateEpoch === loginEpoch
          && postLoginSetupAbortController === controller
          && !controller.signal.aborted
          && useAuthStore.getState().user?.id === user.id

        void (async () => {
          try {
            const setupStatus = await getSetupStatus({ signal: controller.signal })
            if (!isCurrent()) {
              return
            }
            useAuthStore.getState().setShareEnabled(setupStatus.share_enabled ?? null)
            if (setupStatus.is_first_run) {
              await acknowledgeSetup({ signal: controller.signal })
            }
          } catch {
            // Ignore setup acknowledgement failures to avoid blocking login.
          } finally {
            if (postLoginSetupAbortController === controller) {
              postLoginSetupAbortController = null
            }
          }
        })()
      }

      return {
        warning: result.warning,
        message: result.message,
      }
    } catch (err) {
      const message = getAuthStoreActionErrorMessage(err, '登录失败')
      set({ isLoading: false, error: message })
      throw err
    }
  },
  
  logout: async () => {
    bumpAuthStateEpoch()
    cancelPendingInitialize()
    cancelPendingPostLoginSetup()
    set({ isLoading: true, error: null })

    try {
      const result = await apiLogout()
      set({ user: null, isAuthenticated: false, isLoading: false, error: null, shareEnabled: null })
      return result
    } catch (err) {
      const message = getAuthStoreActionErrorMessage(err, '退出登录失败')
      set({ isLoading: false, error: message })
      throw err
    }
  },
  
  clearError: () => set({ error: null }),
  
  setAuthEnabled: (enabled: boolean) => set({ authEnabled: enabled }),
  setShareEnabled: (enabled: boolean | null) => set({ shareEnabled: enabled }),
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

export function useShareEnabled() {
  return useAuthStore((state) => state.shareEnabled)
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
      cancelPendingInitialize()
      cancelPendingPostLoginSetup()
      const state = useAuthStore.getState()
      if (!state.isAuthenticated && !state.user && !state.isLoading) {
        return
      }

      const detail = event instanceof CustomEvent ? event.detail : undefined
      queryClient.clear()

      useAuthStore.setState({
        user: null,
        isAuthenticated: false,
        isLoading: false,
        shareEnabled: null,
        error: detail?.message ?? state.error ?? '登录已过期，请重新登录',
      })
    })

    markerWindow[AUTH_CLEARED_LISTENER_KEY] = true
  }
}
