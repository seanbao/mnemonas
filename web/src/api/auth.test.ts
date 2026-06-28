import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  AUTH_CLEARED_EVENT,
  AUTH_CROSS_TAB_CHANNEL_NAME,
  AUTH_CROSS_TAB_SYNC_KEY,
  AUTH_SESSION_UPDATED_EVENT,
  PASSWORD_CHANGE_UNCONFIRMED_MESSAGE,
  AuthError,
  authFetch,
  changePassword,
  ensureDownloadSession,
  getAuthHeaders,
  getCurrentUser,
  getStoredUser,
  invalidateAuthSessionRequests,
  login,
  logout,
  recordAuthCrossTabScopeTransition,
  refreshAuthSession,
  storeTokens,
} from './auth'

const fetchMock = vi.fn()
const originalNavigatorLocksDescriptor = Object.getOwnPropertyDescriptor(navigator, 'locks')
const grantedRefreshLock = { name: 'mnemonas:auth-refresh', mode: 'exclusive' } as Lock

function setNavigatorLocks(locks: LockManager | undefined): void {
  Object.defineProperty(navigator, 'locks', {
    configurable: true,
    value: locks,
  })
}

function restoreNavigatorLocks(): void {
  if (originalNavigatorLocksDescriptor) {
    Object.defineProperty(navigator, 'locks', originalNavigatorLocksDescriptor)
    return
  }
  Reflect.deleteProperty(navigator, 'locks')
}

function blockLocalStorageAccess() {
  const error = new DOMException('cross-tab storage is blocked', 'SecurityError')
  const descriptor = Object.getOwnPropertyDescriptor(window, 'localStorage')
  Object.defineProperty(window, 'localStorage', {
    configurable: true,
    get: () => {
      throw error
    },
  })

  return {
    error,
    restore: () => {
      if (descriptor) {
        Object.defineProperty(window, 'localStorage', descriptor)
        return
      }
      Reflect.deleteProperty(window, 'localStorage')
    },
  }
}

function blockStoredUserWrites() {
  const storage = window.localStorage
  const descriptor = Object.getOwnPropertyDescriptor(window, 'localStorage')
  const proxy = {
    get length() {
      return storage.length
    },
    clear: () => storage.clear(),
    getItem: (key: string) => storage.getItem(key),
    key: (index: number) => storage.key(index),
    removeItem: (key: string) => storage.removeItem(key),
    setItem: (key: string, value: string) => {
      if (key === 'mnemonas_user') {
        throw new DOMException('stored user is read only', 'QuotaExceededError')
      }
      storage.setItem(key, value)
    },
  } satisfies Storage
  Object.defineProperty(window, 'localStorage', {
    configurable: true,
    get: () => proxy,
  })

  return {
    storage,
    restore: () => {
      if (descriptor) {
        Object.defineProperty(window, 'localStorage', descriptor)
        return
      }
      Reflect.deleteProperty(window, 'localStorage')
    },
  }
}

function createQueuedLockManager(onRequest?: () => void): LockManager {
  type TestLockCallback = (lock: Lock | null) => unknown

  let held = false
  const queue: Array<() => void> = []

  const start = (callback: TestLockCallback): Promise<unknown> => {
    held = true
    return Promise.resolve(callback(grantedRefreshLock)).finally(() => {
      held = false
      queue.shift()?.()
    })
  }

  const request = (
    _name: string,
    optionsOrCallback: LockOptions | TestLockCallback,
    maybeCallback?: TestLockCallback,
  ): Promise<unknown> => {
    onRequest?.()
    const options = typeof optionsOrCallback === 'function' ? {} : optionsOrCallback
    const callback = typeof optionsOrCallback === 'function' ? optionsOrCallback : maybeCallback
    if (!callback) {
      return Promise.reject(new TypeError('lock callback is required'))
    }
    if (options.signal?.aborted) {
      return Promise.reject(new DOMException('The operation was aborted', 'AbortError'))
    }
    if (options.ifAvailable && held) {
      return Promise.resolve(callback(null))
    }
    if (!held) {
      return start(callback)
    }

    return new Promise((resolve, reject) => {
      const begin = () => {
        options.signal?.removeEventListener('abort', abort)
        void start(callback).then(resolve, reject)
      }
      const abort = () => {
        const index = queue.indexOf(begin)
        if (index >= 0) {
          queue.splice(index, 1)
        }
        reject(new DOMException('The operation was aborted', 'AbortError'))
      }
      options.signal?.addEventListener('abort', abort, { once: true })
      queue.push(begin)
    })
  }

  return {
    query: async () => ({ held: [], pending: [] }),
    request: request as LockManager['request'],
  }
}

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

function setPasswordChangeTestUser(id = 'u1'): void {
  localStorage.setItem('mnemonas_session', '1')
  localStorage.setItem('mnemonas_user', JSON.stringify({
    id,
    username: id,
    role: 'user',
    homeDir: `/${id}`,
    mustChangePassword: false,
  }))
}

describe('auth API', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    fetchMock.mockReset()
    localStorage.clear()
    setNavigatorLocks(undefined)
    global.fetch = fetchMock as typeof fetch
  })

  afterEach(() => {
    restoreNavigatorLocks()
  })

  it('treats download session as ready when no auth state is stored', async () => {
    await expect(ensureDownloadSession()).resolves.toEqual({ ok: true })
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('forwards abort signal when syncing an existing download session', async () => {
    const controller = new AbortController()
    localStorage.setItem('mnemonas_session', '1')
    fetchMock.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true }) })

    await expect(ensureDownloadSession({ signal: controller.signal })).resolves.toEqual({ ok: true })

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/download-session', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
      signal: controller.signal,
    }))
  })

  it('identifies common auth error statuses', () => {
    expect(new AuthError('unauthorized', 401).isUnauthorized).toBe(true)
    expect(new AuthError('forbidden', 403).isForbidden).toBe(true)
  })

  it('returns empty auth state when nothing is stored', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        json: () => Promise.reject(new Error('invalid json')),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 400,
        json: () => Promise.resolve({ success: false }),
      })

    expect(getStoredUser()).toBeNull()
    expect(getAuthHeaders()).toEqual({})
    await expect(getCurrentUser()).resolves.toBeNull()
    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/v1/auth/me', expect.objectContaining({
      credentials: 'same-origin',
    }))
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(authCleared).not.toHaveBeenCalled()

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('keeps auth helpers usable when localStorage reads are unavailable', async () => {
    const storageError = new DOMException('localStorage is blocked', 'SecurityError')
    const getItemSpy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw storageError
    })
    const removeItemSpy = vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw storageError
    })

    try {
      expect(getStoredUser()).toBeNull()
      expect(getAuthHeaders()).toEqual({})
      await expect(ensureDownloadSession()).resolves.toEqual({ ok: true })
      expect(fetchMock).not.toHaveBeenCalled()
    } finally {
      getItemSpy.mockRestore()
      removeItemSpy.mockRestore()
    }
  })

  it('does not refresh missing sessions when no auth state is stored', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 401,
      statusText: 'Unauthorized',
      clone: () => ({
        json: () => Promise.resolve({
          success: false,
          error: {
            code: 'MISSING_AUTH_HEADER',
            message: 'missing authorization header',
          },
        }),
      }),
      json: () => Promise.resolve({
        success: false,
        error: {
          code: 'MISSING_AUTH_HEADER',
          message: 'missing authorization header',
        },
      }),
    })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('can refresh an expired access cookie even when local auth state is missing', async () => {
    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        clone: () => ({
          json: () => Promise.resolve({
            success: false,
            error: {
              code: 'TOKEN_EXPIRED',
              message: 'token expired',
            },
          }),
        }),
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-2',
            refresh_token: 'refresh-2',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
        }),
      })
      .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })
      .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })

    const response = await authFetch('/api/v1/files')

    expect(response.ok).toBe(true)
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/v1/auth/refresh', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
    }))
    expect(fetchMock).toHaveBeenCalledTimes(4)
    expect(getStoredUser()).toMatchObject({ username: 'admin', homeDir: '/' })
  })

  it('logs out with the HttpOnly cookie session and clears local auth state', async () => {
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
    }))
    fetchMock.mockResolvedValueOnce({
      ok: true,
      headers: { get: () => null },
      json: () => Promise.resolve({ success: true, data: null }),
    })

    await expect(logout()).resolves.toEqual({ warning: false, message: undefined })
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/logout', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
    }))
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
  })

  it('logs out when localStorage removal is unavailable', async () => {
    const storageError = new DOMException('localStorage is blocked', 'SecurityError')
    const removeItemSpy = vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw storageError
    })

    try {
      fetchMock.mockResolvedValueOnce({
        ok: true,
        headers: { get: () => null },
        json: () => Promise.resolve({ success: true, data: null }),
      })

      await expect(logout()).resolves.toEqual({ warning: false, message: undefined })
      expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/logout', expect.objectContaining({
        method: 'POST',
        credentials: 'same-origin',
      }))
    } finally {
      removeItemSpy.mockRestore()
    }
  })

  it('changes the current password and clears local authentication state', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_session', '1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      homeDir: '/',
      mustChangePassword: true,
    }))
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      headers: { get: () => null },
      json: () => Promise.resolve({
        success: true,
        data: null,
        message: 'password changed successfully',
      }),
    })

    await expect(changePassword({
      old_password: 'initial-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'u1' })).resolves.toEqual({
      warning: false,
      message: 'password changed successfully',
    })

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/password', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({
        old_password: 'initial-password',
        new_password: 'replacement-password',
        expected_user_id: 'u1',
      }),
    })
    expect(localStorage.getItem('mnemonas_session')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)
    expect(authCleared.mock.calls[0]?.[0]).toMatchObject({
      detail: { reason: 'password_changed' },
    })
    expect(JSON.parse(localStorage.getItem(AUTH_CROSS_TAB_SYNC_KEY) ?? '{}')).toMatchObject({
      version: 1,
      type: 'cleared',
      reason: 'password_changed',
      user_id: 'u1',
    })

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('preserves password-change warning metadata and forwards abort signals', async () => {
    const signal = new AbortController().signal
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    setPasswordChangeTestUser()
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      headers: { get: () => '199 auth persistence warning' },
      json: () => Promise.resolve({
        success: true,
        data: { warning: true },
        message: 'password changed with persistence warning',
      }),
    })

    await expect(changePassword({
      old_password: 'initial-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'u1', signal })).resolves.toEqual({
      warning: true,
      message: 'password changed with persistence warning',
    })

    expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/password', expect.objectContaining({ signal }))
    expect(authCleared).toHaveBeenCalledWith(expect.objectContaining({
      detail: { reason: 'password_change_warning' },
    }))
    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('surfaces structured password-change failures without clearing the session', async () => {
    setPasswordChangeTestUser()
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 401,
      json: () => Promise.resolve({
        success: false,
        error: {
          code: 'INVALID_PASSWORD',
          message: 'current password is incorrect',
        },
      }),
    })

    await expect(changePassword({
      old_password: 'incorrect-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'u1' })).rejects.toMatchObject({
      message: 'current password is incorrect',
      status: 401,
      code: 'INVALID_PASSWORD',
    })
    expect(localStorage.getItem('mnemonas_session')).toBe('1')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it.each([
    [503, 'TOKEN_STATE_UNAVAILABLE'],
    [500, 'PASSWORD_ERROR'],
  ] as const)('preserves the session for a definitive %s %s password-change failure', async (status, code) => {
    setPasswordChangeTestUser()
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status,
      json: () => Promise.resolve({
        success: false,
        error: {
          code,
          message: 'password change was rejected before completion',
        },
      }),
    })

    await expect(changePassword({
      old_password: 'current-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'u1' })).rejects.toMatchObject({ status, code })

    expect(localStorage.getItem('mnemonas_session')).toBe('1')
    expect(getStoredUser()).toMatchObject({ id: 'u1' })
  })

  it.each([
    {
      label: 'bodyless bad gateway',
      status: 502,
      json: () => Promise.reject(new SyntaxError('gateway returned HTML')),
    },
    {
      label: 'unrecognized gateway error',
      status: 504,
      json: () => Promise.resolve({
        success: false,
        error: { code: 'GATEWAY_TIMEOUT', message: 'upstream response timed out' },
      }),
    },
  ])('fails closed when password change receives $label', async ({ status, json }) => {
    setPasswordChangeTestUser()
    fetchMock.mockResolvedValueOnce({ ok: false, status, json })

    await expect(changePassword({
      old_password: 'current-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'u1' })).rejects.toMatchObject({
      message: PASSWORD_CHANGE_UNCONFIRMED_MESSAGE,
      status,
      code: 'PASSWORD_CHANGE_UNCONFIRMED',
    })

    expect(localStorage.getItem('mnemonas_session')).toBeNull()
    expect(getStoredUser()).toBeNull()
    expect(JSON.parse(localStorage.getItem(AUTH_CROSS_TAB_SYNC_KEY) ?? '{}')).toMatchObject({
      type: 'cleared',
      reason: 'password_change_unconfirmed',
      user_id: 'u1',
    })
  })

  it.each([
    [401, 'TOKEN_EXPIRED', 'expired'],
    [401, 'TOKEN_REVOKED', 'expired'],
    [401, 'USER_NOT_FOUND', 'missing'],
    [403, 'USER_DISABLED', 'disabled'],
  ] as const)('clears a terminal password-change session for %s %s', async (status, code, reason) => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    setPasswordChangeTestUser()
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status,
      json: () => Promise.resolve({
        success: false,
        error: { code, message: 'authentication is no longer valid' },
      }),
    })

    await expect(changePassword({
      old_password: 'current-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'u1' })).rejects.toMatchObject({ status, code })

    expect(localStorage.getItem('mnemonas_session')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)
    expect(authCleared.mock.calls[0]?.[0]).toMatchObject({ detail: { reason } })
    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('fails closed when a password-change request ends before receiving a response', async () => {
    setPasswordChangeTestUser()
    fetchMock.mockRejectedValueOnce(new TypeError('network unavailable'))

    await expect(changePassword({
      old_password: 'current-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'u1' })).rejects.toMatchObject({
      message: PASSWORD_CHANGE_UNCONFIRMED_MESSAGE,
      status: 0,
      code: 'PASSWORD_CHANGE_UNCONFIRMED',
    })

    expect(localStorage.getItem('mnemonas_session')).toBeNull()
    expect(JSON.parse(localStorage.getItem(AUTH_CROSS_TAB_SYNC_KEY) ?? '{}')).toMatchObject({
      type: 'cleared',
      reason: 'password_change_unconfirmed',
      user_id: 'u1',
    })
  })

  it('fails closed when the same account receives a newer auth generation during the request', async () => {
    const pending = createDeferred<Response>()
    const authCleared = vi.fn()
    setPasswordChangeTestUser()
    fetchMock.mockReturnValueOnce(pending.promise)
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)

    const request = changePassword({
      old_password: 'current-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'u1' })
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    localStorage.setItem(AUTH_CROSS_TAB_SYNC_KEY, JSON.stringify({
      version: 1,
      type: 'session_updated',
      nonce: 'same-account-new-generation',
      source_id: 'other-tab',
      user_id: 'u1',
    }))
    invalidateAuthSessionRequests()
    pending.reject(new TypeError('network unavailable'))

    await expect(request).rejects.toMatchObject({
      message: PASSWORD_CHANGE_UNCONFIRMED_MESSAGE,
      code: 'PASSWORD_CHANGE_UNCONFIRMED',
    })
    expect(authCleared).toHaveBeenCalledWith(expect.objectContaining({
      detail: expect.objectContaining({ reason: 'password_change_unconfirmed' }),
    }))
    expect(localStorage.getItem('mnemonas_session')).toBeNull()
    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('fails closed when an in-flight password-change request is aborted', async () => {
    const controller = new AbortController()
    setPasswordChangeTestUser()
    fetchMock.mockImplementationOnce((_input, init?: RequestInit) => new Promise((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => {
        reject(new DOMException('request aborted', 'AbortError'))
      }, { once: true })
    }))

    const request = changePassword({
      old_password: 'current-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'u1', signal: controller.signal })
    controller.abort()

    await expect(request).rejects.toMatchObject({
      code: 'PASSWORD_CHANGE_UNCONFIRMED',
      message: PASSWORD_CHANGE_UNCONFIRMED_MESSAGE,
    })
    expect(localStorage.getItem('mnemonas_session')).toBeNull()
  })

  it('does not clear a newer account when an older password request ends without a response', async () => {
    const pending = createDeferred<Response>()
    setPasswordChangeTestUser('old-user')
    fetchMock.mockReturnValueOnce(pending.promise)

    const request = changePassword({
      old_password: 'old-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'old-user' })
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    setPasswordChangeTestUser('new-user')
    pending.reject(new TypeError('network unavailable'))

    await expect(request).rejects.toMatchObject({ name: 'AbortError' })
    expect(getStoredUser()).toMatchObject({ id: 'new-user' })
    expect(localStorage.getItem('mnemonas_session')).toBe('1')
  })

  it('does not apply an older password response to a newer account scope', async () => {
    const pending = createDeferred<Response>()
    setPasswordChangeTestUser('old-user')
    fetchMock.mockReturnValueOnce(pending.promise)

    const request = changePassword({
      old_password: 'old-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'old-user' })
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    setPasswordChangeTestUser('new-user')
    pending.resolve({
      ok: true,
      status: 200,
      headers: new Headers(),
      json: () => Promise.resolve({ success: true, data: null }),
    } as Response)

    await expect(request).rejects.toMatchObject({ name: 'AbortError' })
    expect(getStoredUser()).toMatchObject({ id: 'new-user' })
    expect(localStorage.getItem('mnemonas_session')).toBe('1')
  })

  it('rejects a stale password form before sending credentials for another account', async () => {
    setPasswordChangeTestUser('new-user')

    await expect(changePassword({
      old_password: 'old-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'old-user' })).rejects.toMatchObject({
      status: 409,
      code: 'AUTH_SCOPE_CHANGED',
    })
    expect(fetchMock).not.toHaveBeenCalled()
    expect(getStoredUser()).toMatchObject({ id: 'new-user' })
  })

  it('does not clear another account identified by a broadcast-only scope transition', async () => {
    const pending = createDeferred<Response>()
    const authCleared = vi.fn()
    setPasswordChangeTestUser('old-user')
    fetchMock.mockReturnValueOnce(pending.promise)
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)

    const request = changePassword({
      old_password: 'old-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'old-user' })
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))

    recordAuthCrossTabScopeTransition('new-user')
    invalidateAuthSessionRequests()
    pending.reject(new TypeError('network unavailable'))

    await expect(request).rejects.toMatchObject({ name: 'AbortError' })
    expect(authCleared).not.toHaveBeenCalled()
    expect(localStorage.getItem('mnemonas_session')).toBe('1')
    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('does not let a same-account broadcast transition hide a newer storage account', async () => {
    const pending = createDeferred<Response>()
    const authCleared = vi.fn()
    setPasswordChangeTestUser('old-user')
    fetchMock.mockReturnValueOnce(pending.promise)
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)

    const request = changePassword({
      old_password: 'old-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'old-user' })
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))

    recordAuthCrossTabScopeTransition('old-user')
    setPasswordChangeTestUser('new-user')
    localStorage.setItem(AUTH_CROSS_TAB_SYNC_KEY, JSON.stringify({
      version: 1,
      type: 'session_updated',
      nonce: 'newer-storage-account',
      source_id: 'other-tab',
      user_id: 'new-user',
    }))
    pending.reject(new TypeError('network unavailable'))

    await expect(request).rejects.toMatchObject({ name: 'AbortError' })
    expect(authCleared).not.toHaveBeenCalled()
    expect(getStoredUser()).toMatchObject({ id: 'new-user' })
    expect(localStorage.getItem('mnemonas_session')).toBe('1')
    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('does not treat a cleared broadcast transition as the current account scope', async () => {
    const pending = createDeferred<Response>()
    const authCleared = vi.fn()
    setPasswordChangeTestUser('old-user')
    fetchMock.mockReturnValueOnce(pending.promise)
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)

    const request = changePassword({
      old_password: 'old-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'old-user' })
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))

    recordAuthCrossTabScopeTransition(null)
    pending.reject(new TypeError('network unavailable'))

    await expect(request).rejects.toMatchObject({ name: 'AbortError' })
    expect(authCleared).not.toHaveBeenCalled()
    expect(localStorage.getItem('mnemonas_session')).toBe('1')
    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('does not clear when a changed storage transition has no account scope', async () => {
    const pending = createDeferred<Response>()
    const authCleared = vi.fn()
    setPasswordChangeTestUser('old-user')
    fetchMock.mockReturnValueOnce(pending.promise)
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)

    const request = changePassword({
      old_password: 'old-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'old-user' })
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))

    localStorage.setItem(AUTH_CROSS_TAB_SYNC_KEY, JSON.stringify({
      version: 1,
      type: 'session_updated',
      nonce: 'legacy-scope-unknown',
      source_id: 'other-tab',
    }))
    pending.reject(new TypeError('network unavailable'))

    await expect(request).rejects.toMatchObject({ name: 'AbortError' })
    expect(authCleared).not.toHaveBeenCalled()
    expect(localStorage.getItem('mnemonas_session')).toBe('1')
    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('does not clear when the current user scope becomes unknown', async () => {
    const pending = createDeferred<Response>()
    const authCleared = vi.fn()
    setPasswordChangeTestUser('old-user')
    fetchMock.mockReturnValueOnce(pending.promise)
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)

    const request = changePassword({
      old_password: 'old-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'old-user' })
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))

    localStorage.removeItem('mnemonas_user')
    pending.reject(new TypeError('network unavailable'))

    await expect(request).rejects.toMatchObject({ name: 'AbortError' })
    expect(authCleared).not.toHaveBeenCalled()
    expect(localStorage.getItem('mnemonas_session')).toBe('1')
    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('keeps a memory-only newer account authoritative when browser storage writes fail', async () => {
    const pending = createDeferred<Response>()
    const authCleared = vi.fn()
    setPasswordChangeTestUser('old-user')
    fetchMock.mockReturnValueOnce(pending.promise)
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)

    const oldRequest = changePassword({
      old_password: 'old-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'old-user' })
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))

    const blockedStorage = blockStoredUserWrites()

    try {
      storeTokens('', '', {
        id: 'new-user',
        username: 'new-user',
        role: 'user',
        homeDir: '/new-user',
        mustChangePassword: false,
      })
      pending.reject(new TypeError('network unavailable'))

      await expect(oldRequest).rejects.toMatchObject({ name: 'AbortError' })
      expect(authCleared).not.toHaveBeenCalled()
      expect(getStoredUser()).toMatchObject({ id: 'new-user' })

      fetchMock.mockResolvedValueOnce({
        ok: false,
        status: 401,
        json: () => Promise.resolve({
          success: false,
          error: { code: 'INVALID_PASSWORD', message: 'current password is incorrect' },
        }),
      })
      await expect(changePassword({
        old_password: 'incorrect-password',
        new_password: 'another-password',
      }, { expectedUserId: 'new-user' })).rejects.toMatchObject({ code: 'INVALID_PASSWORD' })
      expect(fetchMock).toHaveBeenCalledTimes(2)
    } finally {
      blockedStorage.restore()
      window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
      storeTokens('', '', {
        id: 'cleanup-user',
        username: 'cleanup-user',
        role: 'user',
        homeDir: '/cleanup-user',
        mustChangePassword: false,
      })
      localStorage.clear()
      expect(getStoredUser()).toBeNull()
    }
  })

  it('treats a sync-only cross-tab account update as an uncertain memory-only scope', async () => {
    const pending = createDeferred<Response>()
    const authCleared = vi.fn()
    setPasswordChangeTestUser('old-user')
    const blockedStorage = blockStoredUserWrites()

    try {
      storeTokens('', '', {
        id: 'old-user',
        username: 'old-user',
        role: 'user',
        homeDir: '/old-user',
        mustChangePassword: false,
      })
      fetchMock.mockReturnValueOnce(pending.promise)
      window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
      const oldRequest = changePassword({
        old_password: 'old-password',
        new_password: 'replacement-password',
      }, { expectedUserId: 'old-user' })
      await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))

      blockedStorage.storage.setItem(AUTH_CROSS_TAB_SYNC_KEY, JSON.stringify({
        version: 1,
        type: 'session_updated',
        nonce: 'other-tab-session',
        source_id: 'other-tab',
        user_id: 'new-user',
      }))
      pending.reject(new TypeError('network unavailable'))

      await expect(oldRequest).rejects.toMatchObject({ name: 'AbortError' })
      expect(authCleared).not.toHaveBeenCalled()
      await expect(changePassword({
        old_password: 'old-password',
        new_password: 'another-password',
      }, { expectedUserId: 'old-user' })).rejects.toMatchObject({
        status: 409,
        code: 'AUTH_SCOPE_CHANGED',
      })
      expect(fetchMock).toHaveBeenCalledTimes(1)
    } finally {
      blockedStorage.restore()
      window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
      storeTokens('', '', {
        id: 'cleanup-user',
        username: 'cleanup-user',
        role: 'user',
        homeDir: '/cleanup-user',
        mustChangePassword: false,
      })
      localStorage.clear()
      expect(getStoredUser()).toBeNull()
    }
  })

  it('preserves the session when the server rejects an unchanged password', async () => {
    setPasswordChangeTestUser()
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 400,
      json: () => Promise.resolve({
        success: false,
        error: {
          code: 'PASSWORD_UNCHANGED',
          message: 'new password must differ from current password',
        },
      }),
    })

    await expect(changePassword({
      old_password: 'same-password',
      new_password: 'same-password',
    }, { expectedUserId: 'u1' })).rejects.toMatchObject({
      status: 400,
      code: 'PASSWORD_UNCHANGED',
    })
    expect(localStorage.getItem('mnemonas_session')).toBe('1')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it.each([
    ['missing data', { success: true }],
    ['false success', { success: false, data: null }],
    ['invalid warning data', { success: true, data: { warning: false } }],
    ['invalid message metadata', { success: true, data: null, message: 42 }],
  ])('rejects password-change success responses with %s and clears the invalidated session', async (_label, body) => {
    setPasswordChangeTestUser()
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      headers: { get: () => null },
      json: () => Promise.resolve(body),
    })

    await expect(changePassword({
      old_password: 'initial-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'u1' })).rejects.toMatchObject({
      message: '修改密码响应无效',
      status: 200,
    })
    expect(localStorage.getItem('mnemonas_session')).toBeNull()
    expect(JSON.parse(localStorage.getItem(AUTH_CROSS_TAB_SYNC_KEY) ?? '{}')).toMatchObject({
      version: 1,
      type: 'cleared',
      reason: 'password_change_unconfirmed',
    })
  })

  it('clears the invalidated session when a successful password change returns malformed JSON', async () => {
    setPasswordChangeTestUser()
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      headers: { get: () => null },
      json: () => Promise.reject(new SyntaxError('malformed JSON')),
    })

    await expect(changePassword({
      old_password: 'initial-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'u1' })).rejects.toMatchObject({
      message: '修改密码响应无效',
      status: 200,
    })
    expect(localStorage.getItem('mnemonas_session')).toBeNull()
    expect(JSON.parse(localStorage.getItem(AUTH_CROSS_TAB_SYNC_KEY) ?? '{}')).toMatchObject({
      type: 'cleared',
      reason: 'password_change_unconfirmed',
    })
  })

  it('reports an unconfirmed result when a successful response body is aborted', async () => {
    setPasswordChangeTestUser()
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      headers: { get: () => null },
      json: () => Promise.reject(new DOMException('response body aborted', 'AbortError')),
    })

    await expect(changePassword({
      old_password: 'initial-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'u1' })).rejects.toMatchObject({
      message: PASSWORD_CHANGE_UNCONFIRMED_MESSAGE,
      code: 'PASSWORD_CHANGE_UNCONFIRMED',
    })
    expect(localStorage.getItem('mnemonas_session')).toBeNull()
    expect(JSON.parse(localStorage.getItem(AUTH_CROSS_TAB_SYNC_KEY) ?? '{}')).toMatchObject({
      type: 'cleared',
      reason: 'password_change_unconfirmed',
    })
  })

  it('clears legacy bearer tokens without syncing a download session', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

    await expect(ensureDownloadSession()).resolves.toEqual({ ok: true })
    expect(fetchMock).not.toHaveBeenCalled()
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
  })

  it('syncs download session directly when a browser session marker is stored', async () => {
    localStorage.setItem('mnemonas_session', '1')
    fetchMock.mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true }) })

    await expect(ensureDownloadSession()).resolves.toEqual({ ok: true })
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/download-session', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
    }))
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
  })

  it('clears stale browser auth state when download session has no access cookie', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_session', '1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      homeDir: '/',
      mustChangePassword: false,
    }))
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 401,
      json: () => Promise.resolve({
        success: false,
        error: {
          code: 'MISSING_AUTH_HEADER',
          message: 'missing authorization header',
        },
      }),
    })

    await expect(ensureDownloadSession()).resolves.toMatchObject({
      ok: false,
      authCleared: true,
      status: 401,
      code: 'MISSING_AUTH_HEADER',
      message: '登录会话未建立，请重新登录',
    })
    expect(localStorage.getItem('mnemonas_session')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)
    expect(authCleared.mock.calls[0]?.[0]).toMatchObject({
      detail: {
        reason: 'expired',
        message: '登录会话未建立，请重新登录',
      },
    })

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('syncs download session after login', async () => {
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-1',
            refresh_token: 'refresh-1',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
        }),
      })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true }) })

    await expect(login('admin', 'password')).resolves.toMatchObject({
      user: { homeDir: '/', mustChangePassword: false },
      warning: false,
      message: undefined,
    })

    expect(getStoredUser()).toMatchObject({ homeDir: '/', mustChangePassword: false })
    expect(JSON.parse(localStorage.getItem(AUTH_CROSS_TAB_SYNC_KEY) ?? '{}')).toMatchObject({
      version: 1,
      type: 'session_updated',
      user_id: 'u1',
    })

    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/v1/auth/download-session', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
    }))
  })

  it('does not sync a download session when login requires a password change', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      headers: { get: () => null },
      json: () => Promise.resolve({
        success: true,
        data: {
          user: {
            id: 'u1',
            username: 'admin',
            role: 'admin',
            home_dir: '/',
            must_change_password: true,
          },
        },
      }),
    })

    await expect(login('admin', 'initial-password')).resolves.toMatchObject({
      user: { mustChangePassword: true },
      warning: false,
    })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('syncs download session after login when localStorage writes are unavailable', async () => {
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
    const peerChannel = new window.BroadcastChannel(AUTH_CROSS_TAB_CHANNEL_NAME)
    const broadcastSignal = new Promise<unknown>((resolve) => {
      peerChannel.addEventListener('message', (event) => resolve(event.data), { once: true })
    })

    try {
      fetchMock
        .mockResolvedValueOnce({
          ok: true,
          json: () => Promise.resolve({
            success: true,
            data: {
              access_token: 'access-1',
              refresh_token: 'refresh-1',
              expires_at: '2026-03-13T00:00:00Z',
              token_type: 'Bearer',
              user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
            },
          }),
        })
        .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true }) })

      await expect(login('admin', 'password')).resolves.toMatchObject({
        user: { username: 'admin', homeDir: '/' },
        warning: false,
      })

      expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/v1/auth/download-session', expect.objectContaining({
        method: 'POST',
        credentials: 'same-origin',
      }))
      await expect(broadcastSignal).resolves.toMatchObject({
        version: 1,
        type: 'session_updated',
        nonce: expect.any(String),
        source_id: expect.any(String),
        user_id: 'u1',
      })
    } finally {
      peerChannel.close()
      getItemSpy.mockRestore()
      setItemSpy.mockRestore()
      removeItemSpy.mockRestore()
    }
  })

  it('fails login cleanly when the browser did not establish the cookie session', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: { get: () => null },
        json: () => Promise.resolve({
          success: true,
          data: {
            refresh_token: 'refresh-1',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
        }),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        json: () => Promise.resolve({
          success: false,
          error: {
            code: 'MISSING_AUTH_HEADER',
            message: 'missing authorization header',
          },
        }),
      })

    await expect(login('admin', 'password')).rejects.toMatchObject({
      message: '登录会话未建立，请重新登录',
      status: 401,
      code: 'MISSING_AUTH_HEADER',
    })
    expect(localStorage.getItem('mnemonas_session')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('returns warning metadata for successful login responses with warning headers', async () => {
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: { get: (name: string) => name === 'Warning' ? '199 MnemoNAS "activity log persistence failed"' : null },
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-1',
            refresh_token: 'refresh-1',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
        }),
      })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true }) })

    await expect(login('admin', 'password')).resolves.toMatchObject({
      user: { username: 'admin' },
      warning: true,
      message: undefined,
    })
  })

  it('returns warning metadata for successful login responses with data warning flags', async () => {
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: { get: () => null },
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-1',
            refresh_token: 'refresh-1',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            warning: true,
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
          message: 'login succeeded with persistence warning',
        }),
      })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true }) })

    await expect(login('admin', 'password')).resolves.toMatchObject({
      user: { username: 'admin' },
      warning: true,
      message: 'login succeeded with persistence warning',
    })
  })

  it('surfaces structured login failures', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 403,
      json: () => Promise.resolve({
        success: false,
        error: {
          code: 'USER_DISABLED',
          message: 'account disabled',
        },
      }),
    })

    await expect(login('admin', 'password')).rejects.toMatchObject({
      message: 'account disabled',
      status: 403,
      code: 'USER_DISABLED',
    })
  })

  it('surfaces problem-json login failures', async () => {
    const body = {
      title: 'Service unavailable',
      detail: 'auth service unavailable',
      status: 503,
    }

    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 503,
      headers: new Headers({ 'Content-Type': 'application/problem+json' }),
      clone: () => ({ json: () => Promise.resolve(body) }),
      json: () => Promise.resolve(body),
    })

    await expect(login('admin', 'password')).rejects.toMatchObject({
      message: 'auth service unavailable',
      status: 503,
    })
  })

  it('uses the default login failure when the error body is unreadable', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 500,
      json: () => Promise.reject(new Error('invalid json')),
    })

    await expect(login('admin', 'password')).rejects.toMatchObject({
      message: '登录失败',
      status: 500,
    })
  })

  it('returns a warning when download session sync fails after login', async () => {
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: { get: () => null },
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-1',
            refresh_token: 'refresh-1',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
        }),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 503,
        json: () => Promise.resolve({
          success: false,
          error: { message: 'download session unavailable' },
        }),
      })

    await expect(login('admin', 'password')).resolves.toMatchObject({
      user: { username: 'admin' },
      warning: true,
      message: 'download session unavailable',
    })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).not.toBeNull()
  })

  it('uses the fallback warning when download session sync messages are blank after login', async () => {
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: { get: () => null },
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-1',
            refresh_token: 'refresh-1',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
        }),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 503,
        json: () => Promise.resolve({
          success: false,
          error: { message: '   ', code: '   ' },
        }),
      })

    await expect(login('admin', 'password')).resolves.toMatchObject({
      user: { username: 'admin' },
      warning: true,
      message: '原始预览和下载会话同步失败，请稍后重试',
    })
  })

  it('returns a warning when download session sync throws after login', async () => {
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: { get: () => null },
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-1',
            refresh_token: 'refresh-1',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
        }),
      })
      .mockRejectedValueOnce(new Error('network down'))

    await expect(login('admin', 'password')).resolves.toMatchObject({
      user: { username: 'admin' },
      warning: true,
      message: '原始预览和下载会话同步失败，请稍后重试',
    })
  })

  it('rejects malformed successful login responses instead of storing fake session state', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          access_token: 'access-1',
          refresh_token: 'refresh-1',
          expires_at: '2026-03-13T00:00:00Z',
          token_type: 'Bearer',
          user: { id: 'u1', username: 'admin', role: 'admin' },
        },
      }),
    })

    await expect(login('admin', 'password')).rejects.toMatchObject({
      message: '登录响应无效',
      status: 200,
    })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it.each([
    ['a missing password-change requirement', { id: 'u1', username: 'admin', role: 'admin', home_dir: '/' }],
    ['a non-boolean password-change requirement', { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: 'true' }],
  ])('rejects successful login responses with %s', async (_label, user) => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: { user },
      }),
    })

    await expect(login('admin', 'password')).rejects.toMatchObject({
      message: '登录响应无效',
      status: 200,
    })
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('accepts successful cookie-session login responses that omit bearer tokens', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          refresh_token: 'refresh-1',
          expires_at: '2026-03-13T00:00:00Z',
          token_type: 'Bearer',
          user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
        },
      }),
    })
    fetchMock.mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })

    await expect(login('admin', 'password')).resolves.toMatchObject({
      user: { username: 'admin', homeDir: '/' },
      warning: false,
    })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('rejects successful login responses with an invalid home directory', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          access_token: 'access-1',
          refresh_token: 'refresh-1',
          expires_at: '2026-03-13T00:00:00Z',
          token_type: 'Bearer',
          user: { id: 'u1', username: 'tester', role: 'user', home_dir: '   ', must_change_password: false },
        },
      }),
    })

    await expect(login('tester', 'password')).rejects.toMatchObject({
      message: '登录响应无效',
      status: 200,
    })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
  })

  it.each([
    ['blank id', { id: '   ', username: 'admin' }],
    ['blank username', { id: 'u1', username: '   ' }],
    ['trimmed username', { id: 'u1', username: ' admin ' }],
  ])('rejects successful login responses with a %s', async (_label, userOverride) => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          access_token: 'access-1',
          refresh_token: 'refresh-1',
          expires_at: '2026-03-13T00:00:00Z',
          token_type: 'Bearer',
          user: { role: 'admin', home_dir: '/', must_change_password: false, ...userOverride },
        },
      }),
    })

    await expect(login('admin', 'password')).rejects.toMatchObject({
      message: '登录响应无效',
      status: 200,
    })
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
  })

  it('rejects false-success login responses instead of storing fake session state', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: false,
        data: {
          access_token: 'access-1',
          refresh_token: 'refresh-1',
          expires_at: '2026-03-13T00:00:00Z',
          token_type: 'Bearer',
          user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
        },
      }),
    })

    await expect(login('admin', 'password')).rejects.toMatchObject({
      message: '登录响应无效',
      status: 200,
    })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
  })

  it('syncs download session after loading current user', async () => {
    const controller = new AbortController()
    localStorage.setItem('mnemonas_token', 'access-1')
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
        }),
      })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true }) })

    await getCurrentUser({ signal: controller.signal })

    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/v1/auth/me', expect.objectContaining({
      credentials: 'same-origin',
      signal: controller.signal,
    }))

    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/v1/auth/download-session', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
      signal: controller.signal,
    }))
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
  })

  it('restores local session state and syncs download session from cookie-only current user lookup', async () => {
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
        }),
      })
      .mockResolvedValueOnce({ ok: true, json: () => Promise.resolve({ success: true }) })

    await expect(getCurrentUser()).resolves.toMatchObject({
      username: 'admin',
      homeDir: '/',
      mustChangePassword: false,
    })

    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/v1/auth/me', expect.objectContaining({
      credentials: 'same-origin',
    }))
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/v1/auth/download-session', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
    }))
    expect(localStorage.getItem('mnemonas_session')).toBe('1')
    expect(JSON.parse(localStorage.getItem('mnemonas_user') || '{}')).toMatchObject({
      username: 'admin',
      homeDir: '/',
      mustChangePassword: false,
    })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
  })

  it('does not sync a download session when the current user must change the password', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          user: {
            id: 'u1',
            username: 'user',
            role: 'user',
            home_dir: '/users/user',
            must_change_password: true,
          },
        },
      }),
    })

    await expect(getCurrentUser()).resolves.toMatchObject({
      username: 'user',
      mustChangePassword: true,
    })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('clears local auth state when current user payload is malformed', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
      must_change_password: false,
    }))

    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          user: { id: 'u1', username: 'admin', role: 'admin' },
        },
      }),
    })

    await expect(getCurrentUser()).resolves.toBeNull()
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
  })

  it('clears local auth state when current user payload contains an invalid home directory', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
    }))

    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '../secret', must_change_password: false },
        },
      }),
    })

    await expect(getCurrentUser()).resolves.toBeNull()
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
  })

  it('clears local auth state when current user password-change requirement is invalid', async () => {
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      homeDir: '/',
      mustChangePassword: false,
    }))

    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: 1 },
        },
      }),
    })

    await expect(getCurrentUser()).resolves.toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('returns warning metadata for successful logout responses with warning headers', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    fetchMock.mockResolvedValueOnce({
      ok: true,
      headers: { get: (name: string) => name === 'Warning' ? '199 MnemoNAS "activity log persistence failed"' : null },
      json: () => Promise.resolve({ success: true, data: null }),
    })

    await expect(logout()).resolves.toMatchObject({
      warning: true,
      message: undefined,
    })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
  })

  it('returns warning metadata for successful logout responses with body warning flags', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    fetchMock.mockResolvedValueOnce({
      ok: true,
      headers: { get: () => null },
      json: () => Promise.resolve({
        success: true,
        data: null,
        warning: true,
        message: 'logged out with persistence warning',
      }),
    })

    await expect(logout()).resolves.toEqual({
      warning: true,
      message: 'logged out with persistence warning',
    })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
  })

  it('ignores blank logout success messages', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    fetchMock.mockResolvedValueOnce({
      ok: true,
      headers: { get: () => null },
      json: () => Promise.resolve({ success: true, data: null, message: '   ' }),
    })

    await expect(logout()).resolves.toEqual({ warning: false, message: undefined })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
  })

  it('returns a default logout result when the success body is unreadable', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    fetchMock.mockResolvedValueOnce({
      ok: true,
      headers: { get: () => null },
      json: () => Promise.reject(new Error('invalid json')),
    })

    await expect(logout()).resolves.toEqual({ warning: false, message: undefined })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
  })

  it('surfaces network failures during logout', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    fetchMock.mockRejectedValueOnce(new Error('network down'))

    await expect(logout()).rejects.toMatchObject({
      message: '退出登录失败',
      status: 0,
    })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
  })

  it('preserves local auth state when logout fails', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
    }))

    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 500,
      json: () => Promise.resolve({
        success: false,
        error: {
          code: 'LOGOUT_FAILED',
          message: 'logout unavailable',
        },
      }),
    })

    await expect(logout()).rejects.toMatchObject({
      message: 'logout unavailable',
      status: 500,
      code: 'LOGOUT_FAILED',
    })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(JSON.parse(localStorage.getItem('mnemonas_user') ?? '{}')).toMatchObject({ username: 'admin' })
  })

  it('falls back when logout error messages and codes are blank', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
    }))

    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 503,
      json: () => Promise.resolve({
        success: false,
        error: {
          code: '   ',
          message: '   ',
        },
      }),
    })

    await expect(logout()).rejects.toMatchObject({
      message: '退出登录失败',
      status: 503,
      code: undefined,
    })
    expect(localStorage.getItem('mnemonas_user')).not.toBeNull()
  })

  it('preserves legacy top-level auth error messages and codes', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 503,
      json: () => Promise.resolve({
        success: false,
        code: 'SERVICE_UNAVAILABLE',
        message: 'auth service unavailable',
      }),
    })

    await expect(login('admin', 'password')).rejects.toMatchObject({
      message: 'auth service unavailable',
      status: 503,
      code: 'SERVICE_UNAVAILABLE',
    })
  })

  it('ignores blank legacy top-level auth error messages and codes', async () => {
    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 503,
      json: () => Promise.resolve({
        success: false,
        code: '   ',
        message: '   ',
      }),
    })

    await expect(login('admin', 'password')).rejects.toMatchObject({
      message: '登录失败',
      status: 503,
      code: undefined,
    })
  })

  it('preserves local auth state when current user lookup is temporarily unavailable', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
    }))

    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 503,
      json: () => Promise.resolve({
        success: false,
        error: {
          code: 'SERVICE_UNAVAILABLE',
          message: 'auth unavailable',
        },
      }),
    })

    await expect(getCurrentUser()).rejects.toMatchObject({
      message: 'auth unavailable',
      status: 503,
      code: 'SERVICE_UNAVAILABLE',
    })
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).not.toBeNull()
  })

  it('retries once after refreshing token', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-2',
            refresh_token: 'refresh-2',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
        }),
      })
      .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })
      .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })

    const response = await authFetch('/api/v1/files')

    expect(response.ok).toBe(true)
    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/v1/files', expect.objectContaining({
      headers: expect.any(Headers),
      credentials: 'same-origin',
    }))
    expect((fetchMock.mock.calls[0]?.[1]?.headers as Headers).get('Authorization')).toBeNull()
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/v1/auth/refresh', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
    }))
    expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/v1/auth/download-session', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
    }))
    expect(fetchMock).toHaveBeenNthCalledWith(4, '/api/v1/files', expect.objectContaining({
      headers: expect.any(Headers),
      credentials: 'same-origin',
    }))
    expect((fetchMock.mock.calls[3]?.[1]?.headers as Headers).get('Authorization')).toBeNull()
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
  })

  it('reports refreshAuthSession failure when download session sync does not complete', async () => {
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-2',
            refresh_token: 'refresh-2',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
        }),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 503,
        json: () => Promise.resolve({
          success: false,
          error: { message: 'download session unavailable' },
        }),
      })

    await expect(refreshAuthSession()).resolves.toBe(false)
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).not.toBeNull()
  })

  it('publishes refreshed users that require a password change', async () => {
    const sessionUpdated = vi.fn()
    window.addEventListener(AUTH_SESSION_UPDATED_EVENT, sessionUpdated)
    localStorage.setItem('mnemonas_session', '1')
    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          access_token: 'access-2',
          refresh_token: 'refresh-2',
          expires_at: '2026-03-13T00:00:00Z',
          token_type: 'Bearer',
          user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: true },
        },
      }),
    })

    await expect(refreshAuthSession()).resolves.toBe(false)
    expect(sessionUpdated).toHaveBeenCalledTimes(1)
    expect(sessionUpdated.mock.calls[0]?.[0]).toMatchObject({
      detail: {
        user: {
          id: 'u1',
          mustChangePassword: true,
        },
      },
    })
    expect(fetchMock).toHaveBeenCalledTimes(1)

    window.removeEventListener(AUTH_SESSION_UPDATED_EVENT, sessionUpdated)
  })

  it('does not replay a protected request after refresh requires a password change', async () => {
    localStorage.setItem('mnemonas_session', '1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'user',
      role: 'user',
      homeDir: '/users/user',
      mustChangePassword: false,
    }))
    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-2',
            refresh_token: 'refresh-2',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: {
              id: 'u1',
              username: 'user',
              role: 'user',
              home_dir: '/users/user',
              must_change_password: true,
            },
          },
        }),
      })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/v1/auth/refresh', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
    }))
    expect(getStoredUser()).toMatchObject({ mustChangePassword: true })
  })

  it('does not retry refresh endpoint after 401', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 401,
      statusText: 'Unauthorized',
    })

    const response = await authFetch('/api/v1/auth/refresh', { method: 'POST' })

    expect(response.status).toBe(401)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('attempts cookie refresh for unauthorized requests without readable refresh tokens', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')

    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        json: () => Promise.reject(new Error('invalid json')),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 400,
        json: () => Promise.resolve({ success: false }),
      })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
  })

  it('does not refresh or replay an unauthorized request after its signal is aborted', async () => {
    localStorage.setItem('mnemonas_session', '1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      homeDir: '/',
      mustChangePassword: false,
    }))
    const controller = new AbortController()

    fetchMock.mockImplementationOnce(async () => {
      controller.abort()
      return {
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        clone: () => ({
          json: () => Promise.resolve({
            success: false,
            error: { code: 'TOKEN_EXPIRED', message: 'token expired' },
          }),
        }),
        json: () => Promise.resolve({
          success: false,
          error: { code: 'TOKEN_EXPIRED', message: 'token expired' },
        }),
      }
    })

    const response = await authFetch('/api/v1/files', { signal: controller.signal })

    expect(response.status).toBe(401)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(localStorage.getItem('mnemonas_session')).toBe('1')
    expect(getStoredUser()).toMatchObject({ username: 'admin' })
  })

  it('retries unauthorized requests even when URL parsing falls back to the raw input', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

    fetchMock
      .mockResolvedValueOnce({ ok: false, status: 401, statusText: 'Unauthorized' })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-2',
            refresh_token: 'refresh-2',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
        }),
      })
      .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })
      .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })

    const response = await authFetch('http://[')

    expect(response.ok).toBe(true)
    expect(fetchMock).toHaveBeenCalledTimes(4)
  })

  it('stops after one retry when retried request is still unauthorized', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-2',
            refresh_token: 'refresh-2',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
        }),
      })
      .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })
      .mockResolvedValueOnce({ ok: false, status: 401, statusText: 'Unauthorized' })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(fetchMock).toHaveBeenCalledTimes(4)
  })

  it('clears auth state when refresh returns a malformed success payload', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
    }))

    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-2',
            refresh_token: 'refresh-2',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin' },
          },
        }),
      })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('clears auth state when refresh returns a false-success payload', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
    }))

    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: false,
          data: {
            access_token: 'access-2',
            refresh_token: 'refresh-2',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
        }),
      })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('normalizes legacy stored user payloads', () => {
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/legacy',
      must_change_password: false,
    }))

    expect(getStoredUser()).toMatchObject({ homeDir: '/legacy', mustChangePassword: false })
  })

  it('preserves password-change requirements in stored user payloads', () => {
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      homeDir: '/',
      mustChangePassword: true,
    }))

    expect(getStoredUser()).toMatchObject({ homeDir: '/', mustChangePassword: true })
  })

  it('clears stored user payloads with invalid password-change requirements', () => {
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      homeDir: '/',
      mustChangePassword: 'true',
    }))

    expect(getStoredUser()).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
  })

  it('clears stored user payloads without password-change requirements', () => {
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      homeDir: '/',
    }))

    expect(getStoredUser()).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
  })

  it('clears stored user payloads with an invalid home directory', () => {
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '   ',
    }))

    expect(getStoredUser()).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
  })

  it.each([
    ['blank id', { id: '', username: 'admin' }],
    ['blank username', { id: 'u1', username: '   ' }],
    ['trimmed username', { id: 'u1', username: ' admin ' }],
  ])('clears stored user payloads with a %s', (_label, userOverride) => {
    localStorage.setItem('mnemonas_user', JSON.stringify({
      role: 'admin',
      home_dir: '/',
      ...userOverride,
    }))

    expect(getStoredUser()).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
  })

  it('clears corrupted stored user payloads', () => {
    localStorage.setItem('mnemonas_user', '{invalid-json')

    expect(getStoredUser()).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
  })

  it('preserves Headers instances and custom headers across retry', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

    fetchMock
      .mockResolvedValueOnce({ ok: false, status: 401, statusText: 'Unauthorized' })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-2',
            refresh_token: 'refresh-2',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
        }),
      })
      .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })
      .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })

    const customHeaders = new Headers({
      'Content-Type': 'application/json',
      'X-Trace-Id': 'trace-1',
    })

    await authFetch('/api/v1/files', {
      method: 'POST',
      headers: customHeaders,
      body: JSON.stringify({ name: 'demo' }),
    })

    const firstRequestHeaders = fetchMock.mock.calls[0]?.[1]?.headers as Headers
    const retriedRequestHeaders = fetchMock.mock.calls[3]?.[1]?.headers as Headers

    expect(firstRequestHeaders).toBeInstanceOf(Headers)
    expect(firstRequestHeaders.get('Authorization')).toBeNull()
    expect(firstRequestHeaders.get('Content-Type')).toBe('application/json')
    expect(firstRequestHeaders.get('X-Trace-Id')).toBe('trace-1')

    expect(retriedRequestHeaders).toBeInstanceOf(Headers)
    expect(retriedRequestHeaders.get('Authorization')).toBeNull()
    expect(retriedRequestHeaders.get('Content-Type')).toBe('application/json')
    expect(retriedRequestHeaders.get('X-Trace-Id')).toBe('trace-1')
  })

  it('shares a single refresh request across concurrent unauthorized calls', async () => {
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

    let fileAttempts = 0
    fetchMock.mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)

      if (url === '/api/v1/files') {
        fileAttempts += 1
        if (fileAttempts <= 2) {
          return { ok: false, status: 401, statusText: 'Unauthorized' } as Response
        }

        expect((init?.headers as Headers).get('Authorization')).toBeNull()
        expect(init?.credentials).toBe('same-origin')
        return { ok: true, status: 200, json: () => Promise.resolve({ success: true }) } as Response
      }

      if (url === '/api/v1/auth/refresh') {
        return {
          ok: true,
          status: 200,
          json: () => Promise.resolve({
            success: true,
            data: {
              access_token: 'access-2',
              refresh_token: 'refresh-2',
              expires_at: '2026-03-13T00:00:00Z',
              token_type: 'Bearer',
              user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
            },
          }),
        } as Response
      }

      if (url === '/api/v1/auth/download-session') {
        return { ok: true, status: 200, json: () => Promise.resolve({ success: true }) } as Response
      }

      throw new Error(`unexpected fetch: ${url}`)
    })

    const [first, second] = await Promise.all([
      authFetch('/api/v1/files'),
      authFetch('/api/v1/files'),
    ])

    expect(first.ok).toBe(true)
    expect(second.ok).toBe(true)
    expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/auth/refresh')).toHaveLength(1)
    expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/auth/download-session')).toHaveLength(1)
  })

  it('keeps the single-tab refresh operation when Web Locks is unavailable', async () => {
    localStorage.setItem('mnemonas_session', '1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      homeDir: '/',
      mustChangePassword: false,
    }))
    fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/api/v1/auth/refresh') {
        return {
          ok: true,
          status: 200,
          json: () => Promise.resolve({
            success: true,
            data: {
              user: {
                id: 'u1',
                username: 'admin',
                role: 'admin',
                home_dir: '/',
                must_change_password: false,
              },
            },
          }),
        } as Response
      }
      if (url === '/api/v1/auth/download-session') {
        return { ok: true, status: 200 } as Response
      }
      throw new Error(`unexpected fetch: ${url}`)
    })

    await expect(Promise.all([
      refreshAuthSession(),
      refreshAuthSession(),
    ])).resolves.toEqual([true, true])

    expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/auth/refresh')).toHaveLength(1)
    expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/auth/download-session')).toHaveLength(1)
  })

  it('serializes simulated tab refreshes and reuses the access cookie updated by the lock owner', async () => {
    const pendingRefresh = createDeferred<Response>()
    let lockRequests = 0
    setNavigatorLocks(createQueuedLockManager(() => {
      lockRequests += 1
    }))
    localStorage.setItem('mnemonas_session', '1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      homeDir: '/',
      mustChangePassword: false,
    }))
    fetchMock.mockImplementation((input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/api/v1/auth/refresh') {
        return pendingRefresh.promise
      }
      if (url === '/api/v1/auth/me') {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({
            success: true,
            data: {
              user: {
                id: 'u1',
                username: 'admin',
                role: 'admin',
                home_dir: '/',
                must_change_password: false,
              },
            },
          }),
        } as Response)
      }
      if (url === '/api/v1/auth/download-session') {
        return Promise.resolve({ ok: true, status: 200 } as Response)
      }
      throw new Error(`unexpected fetch: ${url}`)
    })

    vi.resetModules()
    const firstTabAuth = await import('./auth')
    vi.resetModules()
    const secondTabAuth = await import('./auth')

    const firstRefresh = firstTabAuth.refreshAuthSession()
    await vi.waitFor(() => {
      expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/auth/refresh')).toHaveLength(1)
    })
    const secondRefresh = secondTabAuth.refreshAuthSession()
    await vi.waitFor(() => expect(lockRequests).toBe(3))
    pendingRefresh.resolve({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          user: {
            id: 'u1',
            username: 'admin',
            role: 'admin',
            home_dir: '/',
            must_change_password: false,
          },
        },
      }),
    } as Response)

    await expect(Promise.all([firstRefresh, secondRefresh])).resolves.toEqual([true, true])
    expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/auth/refresh')).toHaveLength(1)
    expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/auth/me')).toHaveLength(1)
    expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/auth/download-session')).toHaveLength(2)
  })

  it('reuses the lock owner session when cross-tab storage is blocked', async () => {
    const pendingRefresh = createDeferred<Response>()
    let lockRequests = 0
    setNavigatorLocks(createQueuedLockManager(() => {
      lockRequests += 1
    }))
    vi.resetModules()
    const firstTabAuth = await import('./auth')
    vi.resetModules()
    const secondTabAuth = await import('./auth')
    const existingUser = {
      id: 'u1',
      username: 'admin',
      role: 'admin' as const,
      homeDir: '/',
      mustChangePassword: false,
    }
    firstTabAuth.storeTokens('', '', existingUser)
    secondTabAuth.storeTokens('', '', existingUser)

    const blockedStorage = blockLocalStorageAccess()
    expect(() => localStorage.getItem('blocked-read')).toThrow(blockedStorage.error)
    expect(() => localStorage.setItem('blocked-write', '1')).toThrow(blockedStorage.error)
    expect(() => localStorage.removeItem('blocked-remove')).toThrow(blockedStorage.error)
    let refreshAttempts = 0
    fetchMock.mockImplementation((input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/api/v1/auth/refresh') {
        refreshAttempts += 1
        if (refreshAttempts === 1) {
          return pendingRefresh.promise
        }
        return Promise.resolve({
          ok: false,
          status: 429,
          clone: () => ({
            json: () => Promise.resolve({
              success: false,
              error: { code: 'RATE_LIMITED', message: 'refresh rotation is rate limited' },
            }),
          }),
        } as Response)
      }
      if (url === '/api/v1/auth/me') {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({
            success: true,
            data: {
              user: {
                id: 'u1',
                username: 'admin',
                role: 'admin',
                home_dir: '/',
                must_change_password: false,
              },
            },
          }),
        } as Response)
      }
      if (url === '/api/v1/auth/download-session') {
        return Promise.resolve({ ok: true, status: 200 } as Response)
      }
      throw new Error(`unexpected fetch: ${url}`)
    })

    try {
      const firstRefresh = firstTabAuth.refreshAuthSession()
      await vi.waitFor(() => expect(refreshAttempts).toBe(1))
      const secondRefresh = secondTabAuth.refreshAuthSession()
      await vi.waitFor(() => expect(lockRequests).toBe(3))
      pendingRefresh.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            user: {
              id: 'u1',
              username: 'admin',
              role: 'admin',
              home_dir: '/',
              must_change_password: false,
            },
          },
        }),
      } as Response)

      await expect(Promise.all([firstRefresh, secondRefresh])).resolves.toEqual([true, true])
      expect(refreshAttempts).toBe(1)
      expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/auth/me')).toHaveLength(1)
    } finally {
      blockedStorage.restore()
    }
  })

  it.each([401, 403])(
    'retries refresh without clearing its capability when the waited session probe returns %i',
    async (probeStatus) => {
      const pendingOwnerRefresh = createDeferred<Response>()
      let lockRequests = 0
      setNavigatorLocks(createQueuedLockManager(() => {
        lockRequests += 1
      }))
      vi.resetModules()
      const firstTabAuth = await import('./auth')
      vi.resetModules()
      const secondTabAuth = await import('./auth')
      const existingUser = {
        id: 'u1',
        username: 'admin',
        role: 'admin' as const,
        homeDir: '/',
        mustChangePassword: false,
      }
      firstTabAuth.storeTokens('', '', existingUser)
      secondTabAuth.storeTokens('', '', existingUser)
      const blockedStorage = blockLocalStorageAccess()
      let refreshAttempts = 0
      fetchMock.mockImplementation((input: RequestInfo | URL) => {
        const url = String(input)
        if (url === '/api/v1/auth/refresh') {
          refreshAttempts += 1
          if (refreshAttempts === 1) {
            return pendingOwnerRefresh.promise
          }
          return Promise.resolve({
            ok: true,
            status: 200,
            json: () => Promise.resolve({
              success: true,
              data: {
                user: {
                  id: 'u1',
                  username: 'admin',
                  role: 'admin',
                  home_dir: '/',
                  must_change_password: false,
                },
              },
            }),
          } as Response)
        }
        if (url === '/api/v1/auth/me') {
          return Promise.resolve({ ok: false, status: probeStatus } as Response)
        }
        if (url === '/api/v1/auth/download-session') {
          return Promise.resolve({ ok: true, status: 200 } as Response)
        }
        throw new Error(`unexpected fetch: ${url}`)
      })

      try {
        const firstRefresh = firstTabAuth.refreshAuthSession()
        await vi.waitFor(() => expect(refreshAttempts).toBe(1))
        const secondRefresh = secondTabAuth.refreshAuthSession()
        await vi.waitFor(() => expect(lockRequests).toBe(3))
        pendingOwnerRefresh.resolve({
          ok: false,
          status: 503,
          clone: () => ({
            json: () => Promise.resolve({
              success: false,
              error: { code: 'SERVICE_UNAVAILABLE', message: 'refresh service unavailable' },
            }),
          }),
        } as Response)

        await expect(Promise.all([firstRefresh, secondRefresh])).resolves.toEqual([false, true])
        expect(refreshAttempts).toBe(2)
        expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/auth/me')).toHaveLength(1)
      } finally {
        blockedStorage.restore()
      }
    },
  )

  it('does not reuse or refresh a waited session when the user security scope changed', async () => {
    const pendingRefresh = createDeferred<Response>()
    let lockRequests = 0
    setNavigatorLocks(createQueuedLockManager(() => {
      lockRequests += 1
    }))
    vi.resetModules()
    const firstTabAuth = await import('./auth')
    vi.resetModules()
    const secondTabAuth = await import('./auth')
    const existingUser = {
      id: 'u1',
      username: 'family-user',
      role: 'user' as const,
      homeDir: '/users/family-user',
      mustChangePassword: false,
    }
    firstTabAuth.storeTokens('', '', existingUser)
    secondTabAuth.storeTokens('', '', existingUser)
    const blockedStorage = blockLocalStorageAccess()
    let refreshAttempts = 0
    fetchMock.mockImplementation((input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/api/v1/auth/refresh') {
        refreshAttempts += 1
        return pendingRefresh.promise
      }
      if (url === '/api/v1/auth/me') {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({
            success: true,
            data: {
              user: {
                id: 'u1',
                username: 'family-user',
                role: 'admin',
                home_dir: '/',
                must_change_password: false,
              },
            },
          }),
        } as Response)
      }
      if (url === '/api/v1/auth/download-session') {
        return Promise.resolve({ ok: true, status: 200 } as Response)
      }
      throw new Error(`unexpected fetch: ${url}`)
    })

    try {
      const firstRefresh = firstTabAuth.refreshAuthSession()
      await vi.waitFor(() => expect(refreshAttempts).toBe(1))
      const secondRefresh = secondTabAuth.refreshAuthSession()
      await vi.waitFor(() => expect(lockRequests).toBe(3))
      pendingRefresh.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            user: {
              id: 'u1',
              username: 'family-user',
              role: 'admin',
              home_dir: '/',
              must_change_password: false,
            },
          },
        }),
      } as Response)

      await expect(Promise.all([firstRefresh, secondRefresh])).resolves.toEqual([true, false])
      expect(refreshAttempts).toBe(1)
      expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/auth/me')).toHaveLength(1)
      expect(fetchMock.mock.calls.filter(([url]) => String(url) === '/api/v1/auth/download-session')).toHaveLength(1)
    } finally {
      blockedStorage.restore()
    }
  })

  it('preserves the browser session when Web Lock acquisition fails', async () => {
    const authCleared = vi.fn()
    const request = vi.fn().mockRejectedValue(new DOMException('locks are unavailable', 'SecurityError'))
    setNavigatorLocks({
      query: async () => ({ held: [], pending: [] }),
      request: request as LockManager['request'],
    })
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_session', '1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      homeDir: '/',
      mustChangePassword: false,
    }))

    await expect(refreshAuthSession()).resolves.toBe(false)

    expect(request).toHaveBeenCalledTimes(1)
    expect(fetchMock).not.toHaveBeenCalled()
    expect(localStorage.getItem('mnemonas_session')).toBe('1')
    expect(getStoredUser()).toMatchObject({ id: 'u1' })
    expect(authCleared).not.toHaveBeenCalled()
    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('aborts a queued Web Lock refresh after the auth generation changes', async () => {
    const authCleared = vi.fn()
    const request = vi.fn((
      _name: string,
      options: LockOptions,
      callback: (lock: Lock | null) => unknown,
    ) => {
      if (options.ifAvailable) {
        return Promise.resolve(callback(null))
      }
      return new Promise((_resolve, reject) => {
        options.signal?.addEventListener('abort', () => {
          reject(new DOMException('The operation was aborted', 'AbortError'))
        }, { once: true })
      })
    })
    setNavigatorLocks({
      query: async () => ({ held: [], pending: [] }),
      request: request as LockManager['request'],
    })
    const existingUser = {
      id: 'u1',
      username: 'admin',
      role: 'admin' as const,
      homeDir: '/',
      mustChangePassword: false,
    }
    storeTokens('', '', existingUser)
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    const blockedStorage = blockLocalStorageAccess()

    try {
      const refresh = refreshAuthSession()
      await vi.waitFor(() => expect(request).toHaveBeenCalledTimes(2))
      invalidateAuthSessionRequests()

      await expect(refresh).resolves.toBe(false)
      expect(fetchMock).not.toHaveBeenCalled()
      expect(authCleared).not.toHaveBeenCalled()
    } finally {
      blockedStorage.restore()
      window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
    }
    expect(localStorage.getItem('mnemonas_session')).toBe('1')
    expect(getStoredUser()).toMatchObject({ id: 'u1' })
  })

  it('does not restore a refreshed user after logout supersedes the request', async () => {
    const pendingRefresh = createDeferred<Response>()
    let refreshSignal: AbortSignal | undefined
    localStorage.setItem('mnemonas_session', '1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'old-user',
      username: 'old-user',
      role: 'user',
      homeDir: '/old-user',
      mustChangePassword: false,
    }))
    fetchMock.mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url === '/api/v1/auth/refresh') {
        refreshSignal = init?.signal as AbortSignal | undefined
        return pendingRefresh.promise
      }
      if (url === '/api/v1/auth/logout') {
        return Promise.resolve({
          ok: true,
          status: 200,
          headers: new Headers(),
          json: () => Promise.resolve({ success: true, data: null }),
        } as Response)
      }
      throw new Error(`unexpected fetch: ${url}`)
    })

    const refresh = refreshAuthSession()
    await vi.waitFor(() => expect(refreshSignal).toBeInstanceOf(AbortSignal))
    await expect(logout()).resolves.toMatchObject({ warning: false })
    expect(refreshSignal?.aborted).toBe(true)

    pendingRefresh.resolve({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          user: {
            id: 'old-user',
            username: 'old-user',
            role: 'user',
            home_dir: '/old-user',
            must_change_password: false,
          },
        },
      }),
    } as Response)

    await expect(refresh).resolves.toBe(false)
    expect(getStoredUser()).toBeNull()
  })

  it('does not restore a refreshed user after a password change supersedes the request', async () => {
    const pendingRefresh = createDeferred<Response>()
    let refreshSignal: AbortSignal | undefined
    localStorage.setItem('mnemonas_session', '1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'old-user',
      username: 'old-user',
      role: 'user',
      homeDir: '/old-user',
      mustChangePassword: true,
    }))
    fetchMock.mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url === '/api/v1/auth/refresh') {
        refreshSignal = init?.signal as AbortSignal | undefined
        return pendingRefresh.promise
      }
      if (url === '/api/v1/auth/password') {
        return Promise.resolve({
          ok: true,
          status: 200,
          headers: new Headers(),
          json: () => Promise.resolve({ success: true, data: null }),
        } as Response)
      }
      throw new Error(`unexpected fetch: ${url}`)
    })

    const refresh = refreshAuthSession()
    await vi.waitFor(() => expect(refreshSignal).toBeInstanceOf(AbortSignal))
    await expect(changePassword({
      old_password: 'initial-password',
      new_password: 'replacement-password',
    }, { expectedUserId: 'old-user' })).resolves.toMatchObject({ warning: false })
    expect(refreshSignal?.aborted).toBe(true)

    pendingRefresh.resolve({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          user: {
            id: 'old-user',
            username: 'old-user',
            role: 'user',
            home_dir: '/old-user',
            must_change_password: false,
          },
        },
      }),
    } as Response)

    await expect(refresh).resolves.toBe(false)
    expect(getStoredUser()).toBeNull()
  })

  it('does not let a stale refresh replace a newer login session', async () => {
    const pendingRefresh = createDeferred<Response>()
    let refreshSignal: AbortSignal | undefined
    localStorage.setItem('mnemonas_session', '1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'old-user',
      username: 'old-user',
      role: 'user',
      homeDir: '/old-user',
      mustChangePassword: false,
    }))
    fetchMock.mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url === '/api/v1/auth/refresh') {
        refreshSignal = init?.signal as AbortSignal | undefined
        return pendingRefresh.promise
      }
      if (url === '/api/v1/auth/login') {
        return Promise.resolve({
          ok: true,
          status: 200,
          headers: new Headers(),
          json: () => Promise.resolve({
            success: true,
            data: {
              user: {
                id: 'new-user',
                username: 'new-user',
                role: 'admin',
                home_dir: '/',
                must_change_password: false,
              },
            },
          }),
        } as Response)
      }
      if (url === '/api/v1/auth/download-session') {
        return Promise.resolve({ ok: true, status: 200 } as Response)
      }
      throw new Error(`unexpected fetch: ${url}`)
    })

    const refresh = refreshAuthSession()
    await vi.waitFor(() => expect(refreshSignal).toBeInstanceOf(AbortSignal))
    await expect(login('new-user', 'new-password')).resolves.toMatchObject({
      user: { id: 'new-user' },
    })
    expect(refreshSignal?.aborted).toBe(true)

    pendingRefresh.resolve({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          user: {
            id: 'old-user',
            username: 'old-user',
            role: 'user',
            home_dir: '/old-user',
            must_change_password: false,
          },
        },
      }),
    } as Response)

    await expect(refresh).resolves.toBe(false)
    expect(getStoredUser()).toMatchObject({ id: 'new-user', username: 'new-user' })
  })

  it('does not let a stale refresh download-session check clear a newer login', async () => {
    const pendingOldDownload = createDeferred<Response>()
    let oldDownloadSignal: AbortSignal | undefined
    let downloadAttempt = 0
    localStorage.setItem('mnemonas_session', '1')
    fetchMock.mockImplementation((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url === '/api/v1/auth/refresh') {
        return Promise.resolve({
          ok: true,
          status: 200,
          json: () => Promise.resolve({
            success: true,
            data: {
              user: {
                id: 'old-user',
                username: 'old-user',
                role: 'user',
                home_dir: '/old-user',
                must_change_password: false,
              },
            },
          }),
        } as Response)
      }
      if (url === '/api/v1/auth/login') {
        return Promise.resolve({
          ok: true,
          status: 200,
          headers: new Headers(),
          json: () => Promise.resolve({
            success: true,
            data: {
              user: {
                id: 'new-user',
                username: 'new-user',
                role: 'admin',
                home_dir: '/',
                must_change_password: false,
              },
            },
          }),
        } as Response)
      }
      if (url === '/api/v1/auth/download-session') {
        downloadAttempt += 1
        if (downloadAttempt === 1) {
          oldDownloadSignal = init?.signal as AbortSignal | undefined
          return pendingOldDownload.promise
        }
        return Promise.resolve({ ok: true, status: 200 } as Response)
      }
      throw new Error(`unexpected fetch: ${url}`)
    })

    const refresh = refreshAuthSession()
    await vi.waitFor(() => expect(oldDownloadSignal).toBeInstanceOf(AbortSignal))
    await expect(login('new-user', 'new-password')).resolves.toMatchObject({
      user: { id: 'new-user' },
    })
    expect(oldDownloadSignal?.aborted).toBe(true)

    pendingOldDownload.resolve({
      ok: false,
      status: 401,
      json: () => Promise.resolve({
        success: false,
        error: { code: 'NOT_AUTHENTICATED', message: 'old session missing' },
      }),
    } as Response)

    await expect(refresh).resolves.toBe(false)
    expect(getStoredUser()).toMatchObject({ id: 'new-user' })
  })

  it('does not restore a stale refresh after another tab signals a session transition', async () => {
    const pendingRefresh = createDeferred<Response>()
    let refreshSignal: AbortSignal | undefined
    localStorage.setItem('mnemonas_session', '1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'old-user',
      username: 'old-user',
      role: 'user',
      homeDir: '/old-user',
      mustChangePassword: false,
    }))
    fetchMock.mockImplementation((_input: RequestInfo | URL, init?: RequestInit) => {
      refreshSignal = init?.signal as AbortSignal | undefined
      return pendingRefresh.promise
    })

    const refresh = refreshAuthSession()
    await vi.waitFor(() => expect(refreshSignal).toBeInstanceOf(AbortSignal))
    invalidateAuthSessionRequests()
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'verified-new-user',
      username: 'verified-new-user',
      role: 'admin',
      homeDir: '/',
      mustChangePassword: false,
    }))
    expect(refreshSignal?.aborted).toBe(true)

    pendingRefresh.resolve({
      ok: true,
      status: 200,
      json: () => Promise.resolve({
        success: true,
        data: {
          user: {
            id: 'old-user',
            username: 'old-user',
            role: 'user',
            home_dir: '/old-user',
            must_change_password: false,
          },
        },
      }),
    } as Response)

    await expect(refresh).resolves.toBe(false)
    expect(getStoredUser()).toMatchObject({ id: 'verified-new-user' })
  })

  it('recovers a browser session when refresh reports a replayed token but current cookies are valid', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_session', '1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
    }))

    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        clone: () => ({
          json: () => Promise.resolve({
            success: false,
            error: {
              code: 'TOKEN_REVOKED',
              message: 'token has been revoked',
            },
          }),
        }),
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            user: { id: 'u1', username: 'admin', role: 'admin', home_dir: '/', must_change_password: false },
          },
        }),
      })
      .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })
      .mockResolvedValueOnce({ ok: true, status: 200, json: () => Promise.resolve({ success: true }) })

    const response = await authFetch('/api/v1/files')

    expect(response.ok).toBe(true)
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/v1/auth/refresh', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
    }))
    expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/v1/auth/me', expect.objectContaining({
      credentials: 'same-origin',
    }))
    expect(fetchMock).toHaveBeenNthCalledWith(4, '/api/v1/auth/download-session', expect.objectContaining({
      method: 'POST',
      credentials: 'same-origin',
    }))
    expect(fetchMock).toHaveBeenNthCalledWith(5, '/api/v1/files', expect.objectContaining({
      credentials: 'same-origin',
    }))
    expect(localStorage.getItem('mnemonas_session')).toBe('1')
    expect(getStoredUser()).toMatchObject({ username: 'admin', homeDir: '/' })
    expect(authCleared).not.toHaveBeenCalled()

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('clears browser auth state when replay recovery cannot validate the current user', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_session', '1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
    }))

    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        clone: () => ({
          json: () => Promise.resolve({
            success: false,
            error: {
              code: 'TOKEN_REVOKED',
              message: 'token has been revoked',
            },
          }),
        }),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        clone: () => ({
          json: () => Promise.resolve({
            success: false,
            error: {
              code: 'USER_NOT_FOUND',
              message: 'user not found',
            },
          }),
        }),
      })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(localStorage.getItem('mnemonas_session')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)
    expect(authCleared.mock.calls[0]?.[0]).toMatchObject({
      detail: {
        reason: 'missing',
        message: 'user not found',
      },
    })

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('keeps browser auth state when replay recovery is temporarily unavailable', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_session', '1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'admin',
      role: 'admin',
      home_dir: '/',
      must_change_password: false,
    }))

    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        clone: () => ({
          json: () => Promise.resolve({
            success: false,
            error: {
              code: 'TOKEN_REVOKED',
              message: 'token has been revoked',
            },
          }),
        }),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 503,
        statusText: 'Service Unavailable',
        clone: () => ({
          json: () => Promise.resolve({
            success: false,
            error: {
              code: 'SERVICE_UNAVAILABLE',
              message: 'service unavailable',
            },
          }),
        }),
      })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(localStorage.getItem('mnemonas_session')).toBe('1')
    expect(getStoredUser()).toMatchObject({ username: 'admin', homeDir: '/' })
    expect(authCleared).not.toHaveBeenCalled()

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('dispatches auth-cleared when refresh fails', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')

    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(authCleared).toHaveBeenCalledTimes(1)
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('clears local auth state when an authenticated request reports USER_NOT_FOUND', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'user',
      role: 'user',
      home_dir: '/users/user',
    }))

    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-2',
            refresh_token: 'refresh-2',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'user', role: 'user', home_dir: '/users/user', must_change_password: false },
          },
        }),
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true }),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        json: () => Promise.resolve({
          success: false,
          error: {
            code: 'USER_NOT_FOUND',
            message: 'user not found',
          },
        }),
      })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(fetchMock).toHaveBeenCalledTimes(4)
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)

    const event = authCleared.mock.calls[0]?.[0] as CustomEvent<{ message?: string; reason?: string }>
    expect(event.detail).toEqual({
      reason: 'missing',
      message: 'user not found',
    })

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('clears local auth state on terminal unauthorized responses without a structured body', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'user',
      role: 'user',
      home_dir: '/users/user',
    }))

    fetchMock
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({
          success: true,
          data: {
            access_token: 'access-2',
            refresh_token: 'refresh-2',
            expires_at: '2026-03-13T00:00:00Z',
            token_type: 'Bearer',
            user: { id: 'u1', username: 'user', role: 'user', home_dir: '/users/user', must_change_password: false },
          },
        }),
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ success: true }),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        json: () => Promise.reject(new Error('invalid json')),
      })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(401)
    expect(fetchMock).toHaveBeenCalledTimes(4)
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)

    const event = authCleared.mock.calls[0]?.[0] as CustomEvent<{ message?: string; reason?: string }>
    expect(event.detail).toEqual({
      reason: 'expired',
      message: '登录已过期，请重新登录',
    })

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('clears local auth state when an authenticated request reports USER_DISABLED', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'user',
      role: 'user',
      home_dir: '/users/user',
    }))

    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 403,
      json: () => Promise.resolve({
        success: false,
        error: {
          code: 'USER_DISABLED',
          message: 'user account is disabled',
        },
      }),
    })

    const response = await authFetch('/api/v1/files')

    expect(response.status).toBe(403)
    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)

    const event = authCleared.mock.calls[0]?.[0] as CustomEvent<{ message?: string; reason?: string }>
    expect(event.detail).toEqual({
      reason: 'disabled',
      message: 'user account is disabled',
    })

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('clears local auth state when current user lookup reports USER_DISABLED', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'user',
      role: 'user',
      home_dir: '/users/user',
    }))

    fetchMock.mockResolvedValueOnce({
      ok: false,
      status: 403,
      json: () => Promise.resolve({
        success: false,
        error: {
          code: 'USER_DISABLED',
          message: 'user account is disabled',
        },
      }),
    })

    await expect(getCurrentUser()).resolves.toBeNull()

    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('clears local auth state when current user lookup returns invalid JSON', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'user',
      role: 'user',
      home_dir: '/users/user',
    }))

    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.reject(new Error('invalid json')),
    })

    await expect(getCurrentUser()).resolves.toBeNull()

    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

  it('clears local auth state when current user lookup returns no user payload', async () => {
    const authCleared = vi.fn()
    window.addEventListener(AUTH_CLEARED_EVENT, authCleared)
    localStorage.setItem('mnemonas_token', 'access-1')
    localStorage.setItem('mnemonas_refresh_token', 'refresh-1')
    localStorage.setItem('mnemonas_user', JSON.stringify({
      id: 'u1',
      username: 'user',
      role: 'user',
      home_dir: '/users/user',
    }))

    fetchMock.mockResolvedValueOnce({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ success: true }),
    })

    await expect(getCurrentUser()).resolves.toBeNull()

    expect(localStorage.getItem('mnemonas_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_refresh_token')).toBeNull()
    expect(localStorage.getItem('mnemonas_user')).toBeNull()
    expect(authCleared).toHaveBeenCalledTimes(1)

    window.removeEventListener(AUTH_CLEARED_EVENT, authCleared)
  })

})
