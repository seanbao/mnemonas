import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useAuthStore } from './auth'

const loginMock = vi.fn()
const logoutMock = vi.fn()
const getCurrentUserMock = vi.fn()
const getStoredUserMock = vi.fn()
const getStoredTokenMock = vi.fn()
const acknowledgeSetupMock = vi.fn()
const getSetupStatusMock = vi.fn()

vi.mock('@/api/auth', () => ({
  AUTH_CLEARED_EVENT: 'mnemonas:auth-cleared',
  login: (...args: unknown[]) => loginMock(...args),
  logout: (...args: unknown[]) => logoutMock(...args),
  getCurrentUser: (...args: unknown[]) => getCurrentUserMock(...args),
  getStoredUser: (...args: unknown[]) => getStoredUserMock(...args),
  getStoredToken: (...args: unknown[]) => getStoredTokenMock(...args),
}))

vi.mock('@/api/setup', () => ({
  acknowledgeSetup: (...args: unknown[]) => acknowledgeSetupMock(...args),
  getSetupStatus: (...args: unknown[]) => getSetupStatusMock(...args),
}))

describe('authStore', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useAuthStore.setState({
      user: null,
      isAuthenticated: false,
      isLoading: true,
      error: null,
      authEnabled: true,
    })

    logoutMock.mockResolvedValue(undefined)
    getCurrentUserMock.mockResolvedValue(null)
    getStoredUserMock.mockReturnValue(null)
    getStoredTokenMock.mockReturnValue(null)
    acknowledgeSetupMock.mockResolvedValue({ success: true, message: 'ok' })
    getSetupStatusMock.mockResolvedValue({
      success: true,
      is_first_run: false,
      auth_enabled: true,
      webdav_enabled: true,
      webdav_auth_type: 'basic',
    })
  })

  it('completes admin login without waiting for setup acknowledgement', async () => {
    let resolveSetup: ((value: {
      success: boolean
      is_first_run: boolean
      auth_enabled: boolean
      webdav_enabled: boolean
      webdav_auth_type: string
    }) => void) | null = null

    loginMock.mockResolvedValue({
      id: 'admin-1',
      username: 'admin',
      role: 'admin',
      email: '',
      homeDir: '/',
    })
    getSetupStatusMock.mockImplementation(() => new Promise((resolve) => {
      resolveSetup = resolve
    }))

    await expect(useAuthStore.getState().login('admin', 'password')).resolves.toBeUndefined()

    expect(useAuthStore.getState().isAuthenticated).toBe(true)
    expect(useAuthStore.getState().user?.username).toBe('admin')
    expect(acknowledgeSetupMock).not.toHaveBeenCalled()

    resolveSetup?.({
      success: true,
      is_first_run: true,
      auth_enabled: true,
      webdav_enabled: true,
      webdav_auth_type: 'basic',
    })
    await Promise.resolve()
    await Promise.resolve()

    expect(acknowledgeSetupMock).toHaveBeenCalledTimes(1)
  })

  it('does not acknowledge setup for non-admin login', async () => {
    loginMock.mockResolvedValue({
      id: 'user-1',
      username: 'user',
      role: 'user',
      email: '',
      homeDir: '/',
    })

    await expect(useAuthStore.getState().login('user', 'password')).resolves.toBeUndefined()

    expect(useAuthStore.getState().isAuthenticated).toBe(true)
    expect(getSetupStatusMock).not.toHaveBeenCalled()
    expect(acknowledgeSetupMock).not.toHaveBeenCalled()
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
    expect(state.user).toBeNull()
    expect(state.isAuthenticated).toBe(false)
    expect(state.isLoading).toBe(false)
    expect(state.error).toBe('登录已过期，请重新登录')
  })
})