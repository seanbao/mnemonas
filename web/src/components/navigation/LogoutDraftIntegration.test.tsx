import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  Outlet,
  RouterProvider,
  createMemoryRouter,
  useLocation,
} from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AUTH_CLEARED_EVENT } from '@/api/auth'
import * as settingsApi from '@/api/settings'
import { ProtectedRoute } from '@/components/auth'
import { Header } from '@/components/layout/Header'
import { UserAccessView } from '@/components/users/UserAccessView'
import { useAuthStore } from '@/stores/auth'
import { useSettingsDraftStore } from '@/stores/settingsDraft'
import { UnsavedChangesGuard } from './UnsavedChangesGuard'

const confirmMock = vi.fn(() => true)
const singletonQueryClearMock = vi.fn()
const fetchMock = vi.mocked(globalThis.fetch)

vi.mock('@/api/setup', () => ({
  getSetupStatus: vi.fn(),
}))

vi.mock('@/lib/queryClient', () => ({
  queryClient: {
    clear: (...args: unknown[]) => singletonQueryClearMock(...args),
  },
}))

vi.mock('@/api/settings', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/settings')>()
  return {
    ...actual,
    getSettings: vi.fn(),
    updateSettings: vi.fn(),
    listDirectoryAccessReviewRecords: vi.fn(),
    clearDirectoryAccessReviewRecords: vi.fn(),
  }
})

vi.mock('@heroui/react', async () => {
  const actual = await vi.importActual<typeof import('@heroui/react')>('@heroui/react')
  return { ...actual, addToast: vi.fn() }
})

vi.mock('@/components/ThemeToggle', () => ({
  ThemeToggle: () => <button type="button" aria-label="切换主题" />,
}))

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

function RouterRoot() {
  const location = useLocation()
  return (
    <>
      <UnsavedChangesGuard />
      <output aria-label="当前路由">{location.pathname}</output>
      <Outlet />
    </>
  )
}

function ProtectedDirectoryRoute() {
  return (
    <ProtectedRoute>
      <Header />
      <UserAccessView />
    </ProtectedRoute>
  )
}

function renderLogoutRouter() {
  const router = createMemoryRouter([
    {
      path: '/',
      element: <RouterRoot />,
      children: [
        { path: 'users', element: <ProtectedDirectoryRoute /> },
        { path: 'login', element: <div>登录目标</div> },
      ],
    },
  ], { initialEntries: ['/users'] })
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  return {
    router,
    queryClient,
    ...render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    ),
  }
}

async function makeDraftDirtyAndStartLogout() {
  const user = userEvent.setup()
  const quotaInput = await screen.findByRole('textbox', { name: '目录配额' })
  fireEvent.change(quotaInput, { target: { value: '/draft 2 GB' } })
  await waitFor(() => expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(true))
  await user.click(screen.getByRole('button', { name: '打开用户菜单' }))
  await user.click(await screen.findByText('退出登录'))
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
  return quotaInput
}

function logoutSuccessResponse(): Response {
  return {
    ok: true,
    status: 200,
    headers: new Headers(),
    json: vi.fn().mockResolvedValue({ success: true, data: null }),
  } as unknown as Response
}

describe('logout with a dirty directory draft', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    fetchMock.mockReset()
    window.localStorage.clear()
    Object.defineProperty(window, 'confirm', { configurable: true, value: confirmMock })
    confirmMock.mockReturnValue(true)
    useSettingsDraftStore.setState({ hasPendingChanges: false })
    useAuthStore.setState({
      user: {
        id: 'admin-1',
        username: 'admin',
        role: 'admin',
        email: '',
        homeDir: '/',
        mustChangePassword: false,
      },
      isAuthenticated: true,
      isLoading: false,
      error: null,
      notice: null,
      authEnabled: true,
      shareEnabled: true,
    })
    vi.mocked(settingsApi.getSettings).mockResolvedValue({
      success: true,
      data: {
        storage: {
          root: '~/.mnemonas',
          directory_quotas: [{ path: '/team', quota_bytes: 1073741824 }],
          directory_access_rules: [{ path: '/team', read_groups: ['family'] }],
        },
      },
    } as Awaited<ReturnType<typeof settingsApi.getSettings>>)
    vi.mocked(settingsApi.listDirectoryAccessReviewRecords).mockResolvedValue({
      items: [],
      total: 0,
      limit: 5,
      offset: 0,
    })
    vi.mocked(settingsApi.clearDirectoryAccessReviewRecords).mockResolvedValue({
      success: true,
      message: '',
    })
  })

  it('keeps the protected page and draft mounted while logout is pending and after failure', async () => {
    const logoutRequest = deferred<Response>()
    fetchMock.mockReturnValueOnce(logoutRequest.promise)
    renderLogoutRouter()

    const quotaInput = await makeDraftDirtyAndStartLogout()

    expect(useAuthStore.getState().isLoading).toBe(false)
    expect(useAuthStore.getState().isAuthenticated).toBe(true)
    expect(screen.getByLabelText('当前路由')).toHaveTextContent('/users')
    expect(quotaInput).toHaveValue('/draft 2 GB')
    expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(true)

    await act(async () => {
      logoutRequest.reject(new Error('logout unavailable'))
      await logoutRequest.promise.catch(() => undefined)
    })

    await waitFor(() => expect(screen.getByText('退出登录')).toBeTruthy())
    expect(screen.getByLabelText('当前路由')).toHaveTextContent('/users')
    expect(screen.getByRole('textbox', { name: '目录配额' })).toHaveValue('/draft 2 GB')
    expect(useAuthStore.getState().isAuthenticated).toBe(true)
    expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(true)
    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('clears the draft after logout succeeds and reaches login with one confirmation', async () => {
    const logoutRequest = deferred<Response>()
    fetchMock.mockReturnValueOnce(logoutRequest.promise)
    const { queryClient } = renderLogoutRouter()
    const headerQueryClearMock = vi.spyOn(queryClient, 'clear')

    const quotaInput = await makeDraftDirtyAndStartLogout()
    expect(screen.getByLabelText('当前路由')).toHaveTextContent('/users')
    expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(true)

    const transitions: string[] = []
    const authClearedSnapshots: Array<{
      hasPendingChanges: boolean
      isAuthenticated: boolean
      headerQueryClearCount: number
    }> = []
    const authClearedListener = vi.fn(() => {
      authClearedSnapshots.push({
        hasPendingChanges: useSettingsDraftStore.getState().hasPendingChanges,
        isAuthenticated: useAuthStore.getState().isAuthenticated,
        headerQueryClearCount: headerQueryClearMock.mock.calls.length,
      })
    })
    const unsubscribeDraft = useSettingsDraftStore.subscribe((state, previous) => {
      if (previous.hasPendingChanges && !state.hasPendingChanges) transitions.push('draft-cleared')
    })
    const unsubscribeAuth = useAuthStore.subscribe((state, previous) => {
      if (previous.isAuthenticated && !state.isAuthenticated) transitions.push('auth-cleared')
    })
    window.addEventListener(AUTH_CLEARED_EVENT, authClearedListener)

    try {
      await act(async () => {
        logoutRequest.resolve(logoutSuccessResponse())
        await logoutRequest.promise
      })

      expect(await screen.findByText('登录目标')).toBeTruthy()
      expect(screen.getByLabelText('当前路由')).toHaveTextContent('/login')
      expect(quotaInput).not.toBeInTheDocument()
      expect(screen.queryByRole('textbox', { name: '目录配额' })).toBeNull()
      expect(useAuthStore.getState().isAuthenticated).toBe(false)
      expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(false)
      expect(transitions.slice(0, 2)).toEqual(['draft-cleared', 'auth-cleared'])
      expect(authClearedListener).toHaveBeenCalledTimes(1)
      expect(authClearedSnapshots).toEqual([{
        hasPendingChanges: false,
        isAuthenticated: false,
        headerQueryClearCount: 0,
      }])
      expect(singletonQueryClearMock).toHaveBeenCalledTimes(1)
      expect(headerQueryClearMock).toHaveBeenCalledTimes(1)
      expect(confirmMock).toHaveBeenCalledTimes(1)
      expect(fetchMock).toHaveBeenCalledTimes(1)
      expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/logout', {
        method: 'POST',
        credentials: 'same-origin',
      })
    } finally {
      window.removeEventListener(AUTH_CLEARED_EVENT, authClearedListener)
      unsubscribeDraft()
      unsubscribeAuth()
    }
  })
})
