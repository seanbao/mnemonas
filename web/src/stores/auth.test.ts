import { beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import {
  useAuthError,
  useAuthLoading,
  useAuthStore,
  useCanWrite,
  useIsAdmin,
  useIsAuthenticated,
  useIsGuest,
  useUser,
} from './auth'

const loginMock = vi.fn()
const logoutMock = vi.fn()
const getCurrentUserMock = vi.fn()
const getStoredUserMock = vi.fn()
const invalidateAuthSessionRequestsMock = vi.fn()
const recordAuthCrossTabScopeTransitionMock = vi.fn()
const acknowledgeSetupMock = vi.fn()
const getSetupStatusMock = vi.fn()
const clearQueryClientMock = vi.fn()

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

vi.mock('@/api/auth', () => ({
  AUTH_CLEARED_EVENT: 'mnemonas:auth-cleared',
  AUTH_CROSS_TAB_CHANNEL_NAME: 'mnemonas:auth-session',
  AUTH_CROSS_TAB_SOURCE_ID: 'current-tab',
  AUTH_CROSS_TAB_SYNC_KEY: 'mnemonas:auth-cross-tab-sync',
  AUTH_SESSION_UPDATED_EVENT: 'mnemonas:auth-session-updated',
  PASSWORD_CHANGE_UNCONFIRMED_MESSAGE: '密码修改结果无法确认。请先尝试使用新密码登录；若无法登录，再尝试原密码。',
  login: (...args: unknown[]) => loginMock(...args),
  logout: (...args: unknown[]) => logoutMock(...args),
  getCurrentUser: (...args: unknown[]) => getCurrentUserMock(...args),
  getStoredUser: (...args: unknown[]) => getStoredUserMock(...args),
  invalidateAuthSessionRequests: (...args: unknown[]) => invalidateAuthSessionRequestsMock(...args),
  recordAuthCrossTabScopeTransition: (...args: unknown[]) => recordAuthCrossTabScopeTransitionMock(...args),
}))

vi.mock('@/api/setup', () => ({
  acknowledgeSetup: (...args: unknown[]) => acknowledgeSetupMock(...args),
  getSetupStatus: (...args: unknown[]) => getSetupStatusMock(...args),
}))

vi.mock('@/lib/queryClient', () => ({
  queryClient: {
    clear: (...args: unknown[]) => clearQueryClientMock(...args),
  },
}))

describe('authStore', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    useAuthStore.setState({
      user: null,
      isAuthenticated: false,
      isLoading: true,
      error: null,
      notice: null,
      authEnabled: true,
      shareEnabled: null,
    })

    logoutMock.mockResolvedValue({ warning: false, message: undefined })
    getCurrentUserMock.mockResolvedValue(null)
    getStoredUserMock.mockReturnValue(null)
    acknowledgeSetupMock.mockResolvedValue({ success: true, message: 'ok' })
    getSetupStatusMock.mockResolvedValue({
      success: true,
      is_first_run: false,
      auth_enabled: true,
      share_enabled: true,
      webdav_enabled: true,
      webdav_auth_type: 'basic',
    })
  })

  afterEach(async () => {
    await useAuthStore.getState().logout()
  })

  it('completes admin login without waiting for setup status sync', async () => {
    let resolveSetup: ((value: {
      success: boolean
      is_first_run: boolean
      auth_enabled: boolean
      share_enabled?: boolean
      webdav_enabled: boolean
      webdav_auth_type: string
    }) => void) | null = null

    loginMock.mockResolvedValue({
      user: {
        id: 'admin-1',
        username: 'admin',
        role: 'admin',
        email: '',
        homeDir: '/',
      },
      warning: false,
      message: undefined,
    })
    getSetupStatusMock.mockImplementation(() => new Promise((resolve) => {
      resolveSetup = resolve
    }))

    await expect(useAuthStore.getState().login('admin', 'password')).resolves.toMatchObject({ warning: false })

    expect(useAuthStore.getState().isAuthenticated).toBe(true)
    expect(useAuthStore.getState().user?.username).toBe('admin')
    expect(acknowledgeSetupMock).not.toHaveBeenCalled()

    resolveSetup?.({
      success: true,
      is_first_run: true,
      auth_enabled: true,
      share_enabled: false,
      webdav_enabled: true,
      webdav_auth_type: 'basic',
    })
    await Promise.resolve()
    await Promise.resolve()

    expect(useAuthStore.getState().shareEnabled).toBe(false)
    expect(acknowledgeSetupMock).not.toHaveBeenCalled()
  })

  it('cancels stale admin setup sync after logout', async () => {
    let resolveSetup: ((value: {
      success: boolean
      is_first_run: boolean
      auth_enabled: boolean
      share_enabled?: boolean
      webdav_enabled: boolean
      webdav_auth_type: string
    }) => void) | null = null
    let setupSignal: AbortSignal | undefined

    loginMock.mockResolvedValue({
      user: {
        id: 'admin-1',
        username: 'admin',
        role: 'admin',
        email: '',
        homeDir: '/',
      },
      warning: false,
      message: undefined,
    })
    getSetupStatusMock.mockImplementation((options?: { signal?: AbortSignal }) => {
      setupSignal = options?.signal
      return new Promise((resolve) => {
        resolveSetup = resolve
      })
    })

    await expect(useAuthStore.getState().login('admin', 'password')).resolves.toMatchObject({ warning: false })

    expect(setupSignal).toBeInstanceOf(AbortSignal)
    expect(setupSignal?.aborted).toBe(false)

    await expect(useAuthStore.getState().logout()).resolves.toMatchObject({ warning: false })

    expect(setupSignal?.aborted).toBe(true)

    resolveSetup?.({
      success: true,
      is_first_run: true,
      auth_enabled: true,
      share_enabled: false,
      webdav_enabled: true,
      webdav_auth_type: 'basic',
    })
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()

    expect(useAuthStore.getState().shareEnabled).toBeNull()
    expect(acknowledgeSetupMock).not.toHaveBeenCalled()
  })

  it('preserves authenticated state when logout fails', async () => {
    useAuthStore.setState({
      user: {
        id: 'admin-1',
        username: 'admin',
        role: 'admin',
        email: '',
        homeDir: '/',
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
    })
    logoutMock.mockRejectedValueOnce(new Error('退出登录失败'))

    await expect(useAuthStore.getState().logout()).rejects.toThrow('退出登录失败')

    expect(useAuthStore.getState().isAuthenticated).toBe(true)
    expect(useAuthStore.getState().user?.username).toBe('admin')
    expect(useAuthStore.getState().isLoading).toBe(false)
    expect(useAuthStore.getState().error).toBe('退出登录失败')
  })

  it('does not acknowledge setup for non-admin login', async () => {
    loginMock.mockResolvedValue({
      user: {
        id: 'user-1',
        username: 'user',
        role: 'user',
        email: '',
        homeDir: '/',
      },
      warning: false,
      message: undefined,
    })

    await expect(useAuthStore.getState().login('user', 'password')).resolves.toMatchObject({ warning: false })

    expect(useAuthStore.getState().isAuthenticated).toBe(true)
    expect(getSetupStatusMock).not.toHaveBeenCalled()
    expect(acknowledgeSetupMock).not.toHaveBeenCalled()
  })

  it('does not start admin setup synchronization while a password change is required', async () => {
    loginMock.mockResolvedValue({
      user: {
        id: 'admin-1',
        username: 'admin',
        role: 'admin',
        email: '',
        homeDir: '/',
        mustChangePassword: true,
      },
      warning: false,
      message: undefined,
    })

    await expect(useAuthStore.getState().login('admin', 'initial-password')).resolves.toMatchObject({ warning: false })

    expect(useAuthStore.getState().user?.mustChangePassword).toBe(true)
    expect(useAuthStore.getState().isAuthenticated).toBe(true)
    expect(getSetupStatusMock).not.toHaveBeenCalled()
  })

  it('replaces the active user when a refresh requires a password change', () => {
    useAuthStore.setState({
      user: {
        id: 'user-1',
        username: 'user',
        role: 'user',
        email: '',
        homeDir: '/',
        mustChangePassword: false,
      },
      isAuthenticated: true,
      isLoading: false,
      error: 'stale error',
      authEnabled: true,
    })

    window.dispatchEvent(new CustomEvent('mnemonas:auth-session-updated', {
      detail: {
        user: {
          id: 'user-1',
          username: 'user',
          role: 'user',
          email: '',
          homeDir: '/',
          mustChangePassword: true,
        },
      },
    }))

    const state = useAuthStore.getState()
    expect(state.user?.mustChangePassword).toBe(true)
    expect(state.isAuthenticated).toBe(true)
    expect(state.isLoading).toBe(false)
    expect(state.error).toBeNull()
    expect(clearQueryClientMock).toHaveBeenCalledTimes(1)
  })

  it('resets auth state when auth is cleared externally', () => {
    useAuthStore.setState({
      user: {
        id: 'admin-1',
        username: 'admin',
        role: 'admin',
        email: '',
        homeDir: '/',
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
    })

    window.dispatchEvent(new Event('mnemonas:auth-cleared'))

    const state = useAuthStore.getState()
    expect(clearQueryClientMock).toHaveBeenCalledTimes(1)
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
    expect(state.isLoading).toBe(false)
    expect(state.error).toBe('登录已过期，请重新登录')
  })

  it('preserves explicit auth-cleared messages from the auth layer', () => {
    useAuthStore.setState({
      user: {
        id: 'user-1',
        username: 'user',
        role: 'user',
        email: '',
        homeDir: '/users/user',
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
    })

    window.dispatchEvent(new CustomEvent('mnemonas:auth-cleared', {
      detail: {
        reason: 'disabled',
        message: '账户已被禁用，请联系管理员',
      },
    }))

    const state = useAuthStore.getState()
    expect(clearQueryClientMock).toHaveBeenCalledTimes(1)
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
    expect(state.isLoading).toBe(false)
    expect(state.error).toBe('账户已被禁用，请联系管理员')
  })

  it.each([
    ['logging out', 'logout', null],
    ['changing the password', 'password_changed', { title: '密码已修改，请重新登录', color: 'success' }],
  ] as const)('clears authentication without an expiry error after %s', (_label, reason, notice) => {
    useAuthStore.setState({
      user: {
        id: 'user-1',
        username: 'user',
        role: 'user',
        email: '',
        homeDir: '/',
        mustChangePassword: true,
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
    })

    window.dispatchEvent(new CustomEvent('mnemonas:auth-cleared', {
      detail: { reason },
    }))

    const state = useAuthStore.getState()
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
    expect(state.error).toBeNull()
    expect(state.notice).toEqual(notice)
    expect(clearQueryClientMock).toHaveBeenCalledTimes(1)
  })

  it('clears cached queries when another tab clears an already-hidden session', () => {
    useAuthStore.setState({
      user: null,
      isAuthenticated: false,
      isLoading: false,
      error: null,
      authEnabled: true,
      shareEnabled: null,
    })

    window.dispatchEvent(new StorageEvent('storage', {
      key: 'mnemonas:auth-cross-tab-sync',
      newValue: JSON.stringify({
        version: 1,
        type: 'cleared',
        reason: 'logout',
        nonce: 'other-tab-logout',
      }),
    }))

    const state = useAuthStore.getState()
    expect(clearQueryClientMock).toHaveBeenCalledTimes(1)
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
    expect(state.error).toBeNull()
  })

  it('shows deterministic sign-in guidance when another tab cannot confirm a password change', () => {
    useAuthStore.setState({
      user: {
        id: 'user-1',
        username: 'user',
        role: 'user',
        email: '',
        homeDir: '/users/user',
        mustChangePassword: false,
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
      shareEnabled: true,
    })

    window.dispatchEvent(new StorageEvent('storage', {
      key: 'mnemonas:auth-cross-tab-sync',
      newValue: JSON.stringify({
        version: 1,
        type: 'cleared',
        reason: 'password_change_unconfirmed',
        nonce: 'other-tab-password-change-unconfirmed',
        user_id: 'user-1',
      }),
    }))

    const state = useAuthStore.getState()
    expect(clearQueryClientMock).toHaveBeenCalledTimes(1)
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
    expect(state.error).toBeNull()
    expect(state.notice).toEqual({
      title: '密码修改结果无法确认',
      description: '密码修改结果无法确认。请先尝试使用新密码登录；若无法登录，再尝试原密码。',
      color: 'warning',
    })
  })

  it('shows the persistence warning after another tab changes the password', () => {
    useAuthStore.setState({
      user: {
        id: 'user-1',
        username: 'user',
        role: 'user',
        email: '',
        homeDir: '/users/user',
        mustChangePassword: false,
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      notice: null,
      authEnabled: true,
      shareEnabled: true,
    })

    window.dispatchEvent(new StorageEvent('storage', {
      key: 'mnemonas:auth-cross-tab-sync',
      newValue: JSON.stringify({
        version: 1,
        type: 'cleared',
        reason: 'password_change_warning',
        nonce: 'other-tab-password-change-warning',
        user_id: 'user-1',
      }),
    }))

    const state = useAuthStore.getState()
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
    expect(state.error).toBeNull()
    expect(state.notice).toEqual({
      title: '密码已修改，请重新登录',
      description: '设备未确认所有登录的注销状态已保存。请使用新密码重新登录，并检查其他设备是否已退出。',
      color: 'warning',
    })
  })

  it('isolates cached queries and validates another tab session against the server', async () => {
    useAuthStore.setState({
      user: {
        id: 'user-1',
        username: 'first-user',
        role: 'user',
        email: '',
        homeDir: '/users/first-user',
        mustChangePassword: false,
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
      shareEnabled: true,
    })
    getCurrentUserMock.mockResolvedValueOnce({
      id: 'admin-2',
      username: 'second-admin',
      role: 'admin',
      email: '',
      homeDir: '/',
      mustChangePassword: false,
    })
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'forged-admin',
      username: 'forged-admin',
      role: 'admin',
      homeDir: '/',
      mustChangePassword: false,
    }))

    window.dispatchEvent(new StorageEvent('storage', {
      key: 'mnemonas:auth-cross-tab-sync',
      newValue: JSON.stringify({
        version: 1,
        type: 'session_updated',
        nonce: 'other-tab-login',
        user_id: 'admin-2',
      }),
    }))

    expect(recordAuthCrossTabScopeTransitionMock).toHaveBeenCalledWith('admin-2')
    expect(clearQueryClientMock).toHaveBeenCalledTimes(1)
    expect(useAuthStore.getState()).toMatchObject({
      user: null,
      isAuthenticated: false,
      isLoading: true,
    })
    await vi.waitFor(() => {
      expect(useAuthStore.getState()).toMatchObject({
        user: expect.objectContaining({ id: 'admin-2' }),
        isAuthenticated: true,
        isLoading: false,
        error: null,
      })
    })
    expect(getCurrentUserMock).toHaveBeenCalledWith({ signal: expect.any(AbortSignal) })
  })

  it('validates another tab session through BroadcastChannel when browser storage is blocked', async () => {
    useAuthStore.setState({
      user: {
        id: 'user-1',
        username: 'first-user',
        role: 'user',
        email: '',
        homeDir: '/users/first-user',
        mustChangePassword: false,
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
      shareEnabled: true,
    })
    getCurrentUserMock.mockResolvedValueOnce({
      id: 'admin-2',
      username: 'second-admin',
      role: 'admin',
      email: '',
      homeDir: '/',
      mustChangePassword: false,
    })
    const storageError = new DOMException('localStorage is blocked', 'SecurityError')
    const getItemSpy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw storageError
    })
    const setItemSpy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw storageError
    })
    const removeItemSpy = vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw storageError
    })
    const channel = new window.BroadcastChannel('mnemonas:auth-session')

    try {
      channel.postMessage({
        version: 1,
        type: 'session_updated',
        nonce: 'broadcast-login-storage-blocked',
        source_id: 'other-tab',
        user_id: 'admin-2',
      })

      await vi.waitFor(() => {
        expect(useAuthStore.getState()).toMatchObject({
          user: expect.objectContaining({ id: 'admin-2' }),
          isAuthenticated: true,
          isLoading: false,
          error: null,
        })
      })
      expect(clearQueryClientMock).toHaveBeenCalledTimes(1)
      expect(getCurrentUserMock).toHaveBeenCalledWith({ signal: expect.any(AbortSignal) })
    } finally {
      channel.close()
      getItemSpy.mockRestore()
      setItemSpy.mockRestore()
      removeItemSpy.mockRestore()
    }
  })

  it('deduplicates the same cross-tab signal delivered by storage and BroadcastChannel', async () => {
    const validation = createDeferred<{
      id: string
      username: string
      role: 'admin'
      email: string
      homeDir: string
      mustChangePassword: boolean
    }>()
    getCurrentUserMock.mockReturnValueOnce(validation.promise)
    const signal = {
      version: 1,
      type: 'session_updated',
      nonce: 'duplicate-transport-signal',
      source_id: 'other-tab',
    }
    const channel = new window.BroadcastChannel('mnemonas:auth-session')

    window.dispatchEvent(new StorageEvent('storage', {
      key: 'mnemonas:auth-cross-tab-sync',
      newValue: JSON.stringify(signal),
    }))
    channel.postMessage(signal)

    await vi.waitFor(() => expect(getCurrentUserMock).toHaveBeenCalledTimes(1))
    validation.resolve({
      id: 'admin-2',
      username: 'second-admin',
      role: 'admin',
      email: '',
      homeDir: '/',
      mustChangePassword: false,
    })
    await vi.waitFor(() => expect(useAuthStore.getState().user?.id).toBe('admin-2'))
    channel.close()
  })

  it('clears a visible session from another tab through BroadcastChannel', async () => {
    useAuthStore.setState({
      user: {
        id: 'user-1',
        username: 'first-user',
        role: 'user',
        email: '',
        homeDir: '/users/first-user',
        mustChangePassword: false,
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
      shareEnabled: true,
    })
    const channel = new window.BroadcastChannel('mnemonas:auth-session')

    channel.postMessage({
      version: 1,
      type: 'cleared',
      reason: 'logout',
      nonce: 'broadcast-logout',
      source_id: 'other-tab',
      user_id: 'user-1',
    })

    await vi.waitFor(() => {
      expect(useAuthStore.getState()).toMatchObject({
        user: null,
        isAuthenticated: false,
        isLoading: false,
        error: null,
      })
    })
    expect(clearQueryClientMock).toHaveBeenCalledTimes(1)
    channel.close()
  })

  it('does not apply a delayed clear for an older account to the current account', () => {
    const currentUser = {
      id: 'user-2',
      username: 'second-user',
      role: 'user' as const,
      email: '',
      homeDir: '/users/second-user',
      mustChangePassword: false,
    }
    useAuthStore.setState({
      user: currentUser,
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
      shareEnabled: true,
    })
    getStoredUserMock.mockReturnValue(currentUser)

    window.dispatchEvent(new StorageEvent('storage', {
      key: 'mnemonas:auth-cross-tab-sync',
      newValue: JSON.stringify({
        version: 1,
        type: 'cleared',
        reason: 'password_changed',
        nonce: 'delayed-password-clear',
        source_id: 'other-tab',
        user_id: 'user-1',
      }),
    }))

    expect(recordAuthCrossTabScopeTransitionMock).toHaveBeenCalledWith(null)
    expect(clearQueryClientMock).not.toHaveBeenCalled()
    expect(getCurrentUserMock).not.toHaveBeenCalled()
    expect(useAuthStore.getState()).toMatchObject({
      user: currentUser,
      isAuthenticated: true,
      isLoading: false,
      error: null,
    })
  })

  it('validates a delayed clear when store and storage identify different accounts', async () => {
    const staleUser = {
      id: 'user-1',
      username: 'first-user',
      role: 'user' as const,
      email: '',
      homeDir: '/users/first-user',
      mustChangePassword: false,
    }
    const currentUser = {
      id: 'user-2',
      username: 'second-user',
      role: 'user' as const,
      email: '',
      homeDir: '/users/second-user',
      mustChangePassword: false,
    }
    useAuthStore.setState({
      user: staleUser,
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
      shareEnabled: true,
    })
    getStoredUserMock.mockReturnValue(currentUser)
    getCurrentUserMock.mockResolvedValueOnce(currentUser)

    window.dispatchEvent(new StorageEvent('storage', {
      key: 'mnemonas:auth-cross-tab-sync',
      newValue: JSON.stringify({
        version: 1,
        type: 'cleared',
        reason: 'password_changed',
        nonce: 'delayed-clear-during-session-update',
        source_id: 'other-tab',
        user_id: 'user-1',
      }),
    }))

    expect(recordAuthCrossTabScopeTransitionMock).toHaveBeenCalledWith(null)
    await vi.waitFor(() => expect(getCurrentUserMock).toHaveBeenCalledWith({ signal: expect.any(AbortSignal) }))
    await vi.waitFor(() => {
      expect(useAuthStore.getState()).toMatchObject({
        user: currentUser,
        isAuthenticated: true,
        isLoading: false,
        error: null,
      })
    })
  })

  it('validates an accountless legacy clear without overwriting a known account', async () => {
    const currentUser = {
      id: 'user-2',
      username: 'second-user',
      role: 'user' as const,
      email: '',
      homeDir: '/users/second-user',
      mustChangePassword: false,
    }
    useAuthStore.setState({
      user: currentUser,
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
      shareEnabled: true,
    })
    getStoredUserMock.mockReturnValue(currentUser)
    getCurrentUserMock.mockResolvedValueOnce(currentUser)

    window.dispatchEvent(new StorageEvent('storage', {
      key: 'mnemonas:auth-cross-tab-sync',
      newValue: JSON.stringify({
        version: 1,
        type: 'cleared',
        reason: 'logout',
        nonce: 'legacy-accountless-clear',
        source_id: 'other-tab',
      }),
    }))

    expect(recordAuthCrossTabScopeTransitionMock).toHaveBeenCalledWith(null)
    await vi.waitFor(() => expect(getCurrentUserMock).toHaveBeenCalledWith({ signal: expect.any(AbortSignal) }))
    await vi.waitFor(() => {
      expect(useAuthStore.getState()).toMatchObject({
        user: currentUser,
        isAuthenticated: true,
        isLoading: false,
        error: null,
      })
    })
  })

  it('fails closed when another tab session signal cannot be verified', async () => {
    useAuthStore.setState({
      user: {
        id: 'user-1',
        username: 'first-user',
        role: 'user',
        email: '',
        homeDir: '/users/first-user',
        mustChangePassword: false,
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
    })
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'forged-admin',
      username: 'forged-admin',
      role: 'admin',
      homeDir: '/',
      mustChangePassword: false,
    }))
    getCurrentUserMock.mockRejectedValueOnce(new Error('network down'))

    window.dispatchEvent(new StorageEvent('storage', {
      key: 'mnemonas:auth-cross-tab-sync',
      newValue: JSON.stringify({
        version: 1,
        type: 'session_updated',
        nonce: 'forged-session-update',
      }),
    }))

    await vi.waitFor(() => {
      expect(useAuthStore.getState()).toMatchObject({
        user: null,
        isAuthenticated: false,
        isLoading: false,
        error: '无法验证登录会话，请检查网络后重试',
      })
    })
  })

  it('does not adopt a session validation result after a later clear signal', async () => {
    const validation = createDeferred<{
      id: string
      username: string
      role: 'admin'
      email: string
      homeDir: string
      mustChangePassword: boolean
    }>()
    let validationSignal: AbortSignal | undefined
    getCurrentUserMock.mockImplementationOnce((options?: { signal?: AbortSignal }) => {
      validationSignal = options?.signal
      return validation.promise
    })

    window.dispatchEvent(new StorageEvent('storage', {
      key: 'mnemonas:auth-cross-tab-sync',
      newValue: JSON.stringify({ version: 1, type: 'session_updated', nonce: 'login-first' }),
    }))
    window.dispatchEvent(new StorageEvent('storage', {
      key: 'mnemonas:auth-cross-tab-sync',
      newValue: JSON.stringify({ version: 1, type: 'cleared', reason: 'logout', nonce: 'logout-later' }),
    }))
    expect(validationSignal?.aborted).toBe(true)

    validation.resolve({
      id: 'stale-admin',
      username: 'stale-admin',
      role: 'admin',
      email: '',
      homeDir: '/',
      mustChangePassword: false,
    })
    await Promise.resolve()
    await Promise.resolve()

    expect(useAuthStore.getState()).toMatchObject({
      user: null,
      isAuthenticated: false,
      isLoading: false,
      error: null,
    })
  })

  it('clears cached queries when the same user security scope changes', () => {
    useAuthStore.setState({
      user: {
        id: 'user-1',
        username: 'user',
        role: 'admin',
        email: '',
        homeDir: '/',
        mustChangePassword: false,
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
    })

    window.dispatchEvent(new CustomEvent('mnemonas:auth-session-updated', {
      detail: {
        user: {
          id: 'user-1',
          username: 'user',
          role: 'user',
          email: '',
          homeDir: '/users/user',
          mustChangePassword: false,
        },
      },
    }))

    expect(clearQueryClientMock).toHaveBeenCalledTimes(1)
    expect(useAuthStore.getState().user).toMatchObject({
      role: 'user',
      homeDir: '/users/user',
    })
  })

  it('redacts secrets from explicit auth-cleared messages', () => {
    useAuthStore.setState({
      user: {
        id: 'user-1',
        username: 'user',
        role: 'user',
        email: '',
        homeDir: '/users/user',
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
    })

    window.dispatchEvent(new CustomEvent('mnemonas:auth-cleared', {
      detail: {
        reason: 'expired',
        message: '登录已过期 token=session-secret Authorization: Bearer bearer-secret',
      },
    }))

    const state = useAuthStore.getState()
    expect(clearQueryClientMock).toHaveBeenCalledTimes(1)
    expect(state.error).toBe('登录已过期 token=<redacted> Authorization: Bearer <redacted>')
    expect(state.error).not.toContain('session-secret')
    expect(state.error).not.toContain('bearer-secret')
  })

  it('ignores blank auth-cleared messages from the auth layer', () => {
    useAuthStore.setState({
      user: {
        id: 'user-1',
        username: 'user',
        role: 'user',
        email: '',
        homeDir: '/users/user',
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
    })

    window.dispatchEvent(new CustomEvent('mnemonas:auth-cleared', {
      detail: {
        reason: 'expired',
        message: '   ',
      },
    }))

    const state = useAuthStore.getState()
    expect(clearQueryClientMock).toHaveBeenCalledTimes(1)
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
    expect(state.isLoading).toBe(false)
    expect(state.error).toBe('登录已过期，请重新登录')
  })

  it('fails closed when current user validation is temporarily unavailable', async () => {
    getCurrentUserMock.mockRejectedValue(new Error('network down'))

    await expect(useAuthStore.getState().initialize()).resolves.toBeUndefined()

    const state = useAuthStore.getState()
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
    expect(state.isLoading).toBe(false)
    expect(state.error).toBe('无法验证登录会话，请检查网络后重试')
    expect(clearQueryClientMock).toHaveBeenCalled()
  })

  it('tracks share availability from setup status', async () => {
    getSetupStatusMock.mockResolvedValue({
      success: true,
      is_first_run: false,
      auth_enabled: true,
      share_enabled: false,
      webdav_enabled: true,
      webdav_auth_type: 'basic',
    })

    await expect(useAuthStore.getState().initialize()).resolves.toBeUndefined()

    expect(useAuthStore.getState().shareEnabled).toBe(false)
  })

  it('clears stale share availability when setup status cannot be loaded', async () => {
    useAuthStore.setState({ shareEnabled: false })
    getSetupStatusMock.mockRejectedValue(new Error('setup unavailable'))

    await expect(useAuthStore.getState().initialize()).resolves.toBeUndefined()

    const state = useAuthStore.getState()
    expect(state.authEnabled).toBe(true)
    expect(state.shareEnabled).toBeNull()
    expect(state.isAuthenticated).toBe(false)
    expect(state.isLoading).toBe(false)
  })

  it('restores secure authEnabled state when initialize runs after guest mode and auth is enabled again', async () => {
    useAuthStore.setState({
      user: {
        id: 'guest',
        username: 'guest',
        role: 'admin',
        email: '',
        homeDir: '/',
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: false,
    })

    getSetupStatusMock.mockResolvedValue({
      success: true,
      is_first_run: false,
      auth_enabled: true,
      webdav_enabled: true,
      webdav_auth_type: 'basic',
    })

    await expect(useAuthStore.getState().initialize()).resolves.toBeUndefined()

    const state = useAuthStore.getState()
    expect(state.authEnabled).toBe(true)
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
    expect(state.isLoading).toBe(false)
  })

  it('falls back to secure authEnabled=true when setup status refresh fails after guest mode', async () => {
    useAuthStore.setState({
      user: {
        id: 'guest',
        username: 'guest',
        role: 'admin',
        email: '',
        homeDir: '/',
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: false,
    })

    getSetupStatusMock.mockRejectedValue(new Error('setup unavailable'))

    await expect(useAuthStore.getState().initialize()).resolves.toBeUndefined()

    const state = useAuthStore.getState()
    expect(state.authEnabled).toBe(true)
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
    expect(state.isLoading).toBe(false)
  })

  it('does not let a stale initialize result overwrite a successful login', async () => {
    let resolveSetupStatus: ((value: {
      success: boolean
      is_first_run: boolean
      auth_enabled: boolean
      webdav_enabled: boolean
      webdav_auth_type: string
    }) => void) | null = null
    let resolveCurrentUser: ((value: null) => void) | null = null

    getSetupStatusMock.mockImplementation(() => new Promise((resolve) => {
      resolveSetupStatus = resolve
    }))
    getCurrentUserMock.mockImplementation(() => new Promise((resolve) => {
      resolveCurrentUser = resolve
    }))
    loginMock.mockResolvedValue({
      user: {
        id: 'user-1',
        username: 'user',
        role: 'user',
        email: '',
        homeDir: '/',
      },
      warning: false,
      message: undefined,
    })

    const initializePromise = useAuthStore.getState().initialize()

    await expect(useAuthStore.getState().login('user', 'password')).resolves.toMatchObject({ warning: false })

    resolveSetupStatus?.({
      success: true,
      is_first_run: false,
      auth_enabled: true,
      webdav_enabled: true,
      webdav_auth_type: 'basic',
    })
    await Promise.resolve()

    resolveCurrentUser?.(null)
    await initializePromise

    const state = useAuthStore.getState()
    expect(state.user?.username).toBe('user')
    expect(state.isAuthenticated).toBe(true)
    expect(state.isLoading).toBe(false)
  })

  it('passes abort signals to initialize checks and aborts a pending initialize before login', async () => {
    let setupSignal: AbortSignal | undefined

    getSetupStatusMock.mockImplementation((options?: { signal?: AbortSignal }) => {
      setupSignal = options?.signal
      return new Promise((_resolve, reject) => {
        options?.signal?.addEventListener('abort', () => {
          reject(new DOMException('initialize aborted', 'AbortError'))
        }, { once: true })
      })
    })
    loginMock.mockResolvedValue({
      user: {
        id: 'user-1',
        username: 'user',
        role: 'user',
        email: '',
        homeDir: '/',
      },
      warning: false,
      message: undefined,
    })

    const initializePromise = useAuthStore.getState().initialize()

    expect(setupSignal).toBeInstanceOf(AbortSignal)
    expect(setupSignal?.aborted).toBe(false)

    await expect(useAuthStore.getState().login('user', 'password')).resolves.toMatchObject({ warning: false })

    expect(setupSignal?.aborted).toBe(true)
    await expect(initializePromise).resolves.toBeUndefined()
    expect(getCurrentUserMock).not.toHaveBeenCalled()
  })

  it('initializes guest access when server auth is disabled', async () => {
    getSetupStatusMock.mockResolvedValue({
      success: true,
      is_first_run: false,
      auth_enabled: false,
      share_enabled: true,
      webdav_enabled: true,
      webdav_auth_type: 'none',
    })

    await expect(useAuthStore.getState().initialize()).resolves.toBeUndefined()

    const state = useAuthStore.getState()
    expect(state.authEnabled).toBe(false)
    expect(state.shareEnabled).toBe(true)
    expect(state.isAuthenticated).toBe(true)
    expect(state.isLoading).toBe(false)
    expect(state.user).toEqual({
      id: 'guest',
      username: 'guest',
      role: 'admin',
      email: '',
      homeDir: '/',
      mustChangePassword: false,
    })
  })

  it('initializes an authenticated session when the cookie session validates', async () => {
    const user = {
      id: 'admin-1',
      username: 'admin',
      role: 'admin' as const,
      email: '',
      homeDir: '/',
    }
    getCurrentUserMock.mockResolvedValue(user)

    await expect(useAuthStore.getState().initialize()).resolves.toBeUndefined()

    expect(getSetupStatusMock.mock.calls[0]?.[0]).toEqual({
      signal: expect.any(AbortSignal),
    })
    expect(getCurrentUserMock.mock.calls[0]?.[0]).toEqual({
      signal: expect.any(AbortSignal),
    })

    const state = useAuthStore.getState()
    expect(state.user).toEqual(user)
    expect(state.isAuthenticated).toBe(true)
    expect(state.isLoading).toBe(false)
  })

  it('can skip session validation for public entry routes without cached auth state', async () => {
    await expect(useAuthStore.getState().initialize({ validateSession: false })).resolves.toBeUndefined()

    expect(getSetupStatusMock).toHaveBeenCalledWith({
      signal: expect.any(AbortSignal),
    })
    expect(getCurrentUserMock).not.toHaveBeenCalled()

    const state = useAuthStore.getState()
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
    expect(state.isLoading).toBe(false)
    expect(state.shareEnabled).toBe(true)
  })

  it('skips session validation for public entry routes even with stale cached auth state', async () => {
    const user = {
      id: 'admin-1',
      username: 'admin',
      role: 'admin' as const,
      email: '',
      homeDir: '/',
    }
    useAuthStore.setState({ user, isAuthenticated: true, isLoading: false })
    getCurrentUserMock.mockResolvedValue(user)

    await expect(useAuthStore.getState().initialize({ validateSession: false })).resolves.toBeUndefined()

    expect(getCurrentUserMock).not.toHaveBeenCalled()

    const state = useAuthStore.getState()
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
    expect(state.isLoading).toBe(false)
  })

  it('clears auth state when the cookie session is invalid', async () => {
    useAuthStore.setState({
      user: {
        id: 'cached-1',
        username: 'cached-admin',
        role: 'admin',
        email: '',
        homeDir: '/',
        mustChangePassword: false,
      },
      isAuthenticated: true,
      isLoading: false,
    })
    getCurrentUserMock.mockResolvedValue(null)

    await expect(useAuthStore.getState().initialize()).resolves.toBeUndefined()

    const state = useAuthStore.getState()
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
    expect(state.isLoading).toBe(false)
  })

  it('clears auth state when current user validation fails without a cached session', async () => {
    getCurrentUserMock.mockRejectedValue(new Error('network down'))

    await expect(useAuthStore.getState().initialize()).resolves.toBeUndefined()

    const state = useAuthStore.getState()
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
    expect(state.isLoading).toBe(false)
    expect(state.error).toBe('无法验证登录会话，请检查网络后重试')
  })

  it('stores localized login errors and rethrows the original failure', async () => {
    loginMock.mockRejectedValueOnce('bad credentials')

    await expect(useAuthStore.getState().login('admin', 'wrong')).rejects.toBe('bad credentials')

    const state = useAuthStore.getState()
    expect(state.isLoading).toBe(false)
    expect(state.error).toBe('登录失败')
  })

  it('preserves unconfirmed password guidance when the first recovery login fails', async () => {
    const notice = {
      title: '密码修改结果无法确认',
      description: '密码修改结果无法确认。请先尝试使用新密码登录；若无法登录，再尝试原密码。',
      color: 'warning' as const,
    }
    useAuthStore.setState({ notice })
    const authError = Object.assign(new Error('用户名或密码错误'), { name: 'AuthError' })
    loginMock.mockRejectedValueOnce(authError)

    await expect(useAuthStore.getState().login('admin', 'new-password')).rejects.toThrow('用户名或密码错误')

    const state = useAuthStore.getState()
    expect(state.error).toBe('用户名或密码错误')
    expect(state.notice).toEqual(notice)
  })

  it('does not expose arbitrary Error messages from failed login attempts', async () => {
    loginMock.mockRejectedValueOnce(new Error('database connection refused'))

    await expect(useAuthStore.getState().login('admin', 'wrong')).rejects.toThrow('database connection refused')

    const state = useAuthStore.getState()
    expect(state.isLoading).toBe(false)
    expect(state.error).toBe('登录失败')
  })

  it('preserves explicit AuthError login messages from the auth API', async () => {
    const authError = Object.assign(new Error('用户名或密码错误'), { name: 'AuthError' })
    loginMock.mockRejectedValueOnce(authError)

    await expect(useAuthStore.getState().login('admin', 'wrong')).rejects.toThrow('用户名或密码错误')

    const state = useAuthStore.getState()
    expect(state.isLoading).toBe(false)
    expect(state.error).toBe('用户名或密码错误')
  })

  it('redacts secrets from explicit AuthError login messages', async () => {
    const authError = Object.assign(
      new Error('用户名或密码错误 token=login-secret --password repo:pass/with/slash'),
      { name: 'AuthError' }
    )
    loginMock.mockRejectedValueOnce(authError)

    await expect(useAuthStore.getState().login('admin', 'wrong')).rejects.toThrow('login-secret')

    const state = useAuthStore.getState()
    expect(state.isLoading).toBe(false)
    expect(state.error).toBe('用户名或密码错误 token=<redacted> --password <redacted>')
    expect(state.error).not.toContain('login-secret')
    expect(state.error).not.toContain('repo:pass/with/slash')
  })

  it('ignores blank AuthError login messages from the auth API', async () => {
    const authError = Object.assign(new Error('   '), { name: 'AuthError' })
    loginMock.mockRejectedValueOnce(authError)

    await expect(useAuthStore.getState().login('admin', 'wrong')).rejects.toThrow('   ')

    const state = useAuthStore.getState()
    expect(state.isLoading).toBe(false)
    expect(state.error).toBe('登录失败')
  })

  it('does not expose arbitrary Error messages from failed logout attempts', async () => {
    useAuthStore.setState({
      user: {
        id: 'admin-1',
        username: 'admin',
        role: 'admin',
        email: '',
        homeDir: '/',
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
    })
    logoutMock.mockRejectedValueOnce(new Error('socket hang up'))

    await expect(useAuthStore.getState().logout()).rejects.toThrow('socket hang up')

    expect(useAuthStore.getState().isAuthenticated).toBe(true)
    expect(useAuthStore.getState().isLoading).toBe(false)
    expect(useAuthStore.getState().error).toBe('退出登录失败')
  })

  it('ignores blank AuthError logout messages from the auth API', async () => {
    useAuthStore.setState({
      user: {
        id: 'admin-1',
        username: 'admin',
        role: 'admin',
        email: '',
        homeDir: '/',
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      authEnabled: true,
    })
    const authError = Object.assign(new Error('   '), { name: 'AuthError' })
    logoutMock.mockRejectedValueOnce(authError)

    await expect(useAuthStore.getState().logout()).rejects.toThrow('   ')

    expect(useAuthStore.getState().isAuthenticated).toBe(true)
    expect(useAuthStore.getState().isLoading).toBe(false)
    expect(useAuthStore.getState().error).toBe('退出登录失败')
  })

  it('clears errors and updates authEnabled through lightweight actions', () => {
    useAuthStore.setState({ error: 'boom', authEnabled: true })

    useAuthStore.getState().clearError()
    useAuthStore.getState().setAuthEnabled(false)

    const state = useAuthStore.getState()
    expect(state.error).toBeNull()
    expect(state.authEnabled).toBe(false)
  })

  it('clears cached queries without adding an error when the store is already anonymous and idle', () => {
    useAuthStore.setState({
      user: null,
      isAuthenticated: false,
      isLoading: false,
      error: null,
      authEnabled: true,
    })

    window.dispatchEvent(new Event('mnemonas:auth-cleared'))

    expect(clearQueryClientMock).toHaveBeenCalledTimes(1)
    expect(useAuthStore.getState().error).toBeNull()
  })

  it('uses the existing auth error when an auth-cleared event has no explicit message', () => {
    useAuthStore.setState({
      user: null,
      isAuthenticated: false,
      isLoading: true,
      error: '已有认证错误',
      authEnabled: true,
    })

    window.dispatchEvent(new Event('mnemonas:auth-cleared'))

    expect(clearQueryClientMock).toHaveBeenCalledTimes(1)
    expect(useAuthStore.getState().error).toBe('已有认证错误')
  })

  it('exposes selector hooks for auth state and permissions', () => {
    const readHook = <T,>(hook: () => T): T => {
      const { result, unmount } = renderHook(hook)
      const value = result.current
      unmount()
      return value
    }

    useAuthStore.setState({
      user: {
        id: 'guest-1',
        username: 'guest',
        role: 'guest',
        email: '',
        homeDir: '/',
      },
      isAuthenticated: true,
      isLoading: false,
      error: 'read only',
      authEnabled: true,
    })

    expect(readHook(() => useUser())?.username).toBe('guest')
    expect(readHook(() => useIsAuthenticated())).toBe(true)
    expect(readHook(() => useIsAdmin())).toBe(false)
    expect(readHook(() => useIsGuest())).toBe(true)
    expect(readHook(() => useCanWrite())).toBe(false)
    expect(readHook(() => useAuthLoading())).toBe(false)
    expect(readHook(() => useAuthError())).toBe('read only')

    useAuthStore.setState({
      user: null,
      authEnabled: false,
    })
    expect(readHook(() => useCanWrite())).toBe(true)

    useAuthStore.setState({
      authEnabled: true,
      user: {
        id: 'user-1',
        username: 'user',
        role: 'user',
        email: '',
        homeDir: '/',
      },
    })
    expect(readHook(() => useCanWrite())).toBe(true)
  })
})
