import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import {
  Link,
  Outlet,
  RouterProvider,
  createMemoryRouter,
  useLocation,
  useNavigate,
} from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as settingsApi from '@/api/settings'
import { UserAccessView } from '@/components/users/UserAccessView'
import { useSettingsDraftStore } from '@/stores/settingsDraft'
import { UnsavedChangesGuard } from './UnsavedChangesGuard'

const confirmMock = vi.fn(() => false)

vi.mock('@heroui/react', async () => {
  const actual = await vi.importActual<typeof import('@heroui/react')>('@heroui/react')
  return {
    ...actual,
    addToast: vi.fn(),
  }
})

vi.mock('@/stores/auth', () => ({
  useUser: () => ({
    id: 'admin-1',
    username: 'admin',
    role: 'admin',
    homeDir: '/',
    email: '',
    mustChangePassword: false,
  }),
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

function NavigationRoot() {
  const location = useLocation()
  const navigate = useNavigate()
  return (
    <>
      <UnsavedChangesGuard />
      <nav>
        <Link to="/storage">转到空间</Link>
        <button type="button" onClick={() => navigate('/settings')}>程序导航到设置</button>
      </nav>
      <output aria-label="当前路由">{`${location.pathname}${location.search}`}</output>
      <Outlet />
    </>
  )
}

function SettingsDraftHarness() {
  const [draft, setDraft] = useState('')
  const setHasPendingChanges = useSettingsDraftStore((state) => state.setHasPendingChanges)
  const navigate = useNavigate()

  return (
    <>
      <label>
        设置草稿
        <input
          aria-label="设置草稿"
          value={draft}
          onChange={(event) => {
            setDraft(event.target.value)
            setHasPendingChanges(event.target.value !== '')
          }}
        />
      </label>
      <button type="button" onClick={() => navigate('/settings?tab=shares')}>
        切换到分享分类
      </button>
      <button type="button" onClick={() => navigate('/storage')}>
        离开设置页面
      </button>
    </>
  )
}

function createTestRouter(
  initialEntries = ['/users?view=access'],
  initialIndex = initialEntries.length - 1,
) {
  return createMemoryRouter([
    {
      path: '/',
      element: <NavigationRoot />,
      children: [
        { path: 'users', element: <UserAccessView /> },
        { path: 'dashboard', element: <div>首页目标</div> },
        { path: 'storage', element: <div>空间目标</div> },
        { path: 'settings', element: <SettingsDraftHarness /> },
      ],
    },
  ], { initialEntries, initialIndex })
}

function renderRouter(
  initialEntries?: string[],
  initialIndex?: number,
) {
  const router = createTestRouter(initialEntries, initialIndex)
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
  return {
    router,
    ...render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    ),
  }
}

async function makeDirectoryDraftDirty() {
  fireEvent.change(await screen.findByRole('textbox', { name: '目录配额' }), {
    target: { value: '/draft 2 GB' },
  })
  await waitFor(() => expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(true))
}

describe('UnsavedChangesGuard with UserAccessView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    Object.defineProperty(window, 'confirm', { configurable: true, value: confirmMock })
    confirmMock.mockReturnValue(false)
    useSettingsDraftStore.setState({ hasPendingChanges: false })
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

  it('blocks Link navigation and proceeds to the original target after confirmation', async () => {
    const user = userEvent.setup()
    renderRouter()
    await makeDirectoryDraftDirty()

    await user.click(screen.getByRole('link', { name: '转到空间' }))
    await waitFor(() => expect(confirmMock).toHaveBeenCalledTimes(1))
    expect(screen.getByLabelText('当前路由')).toHaveTextContent('/users?view=access')

    confirmMock.mockReturnValueOnce(true)
    await user.click(screen.getByRole('link', { name: '转到空间' }))
    await screen.findByText('空间目标')
    expect(screen.getByLabelText('当前路由')).toHaveTextContent('/storage')
  })

  it('blocks programmatic navigate calls while the draft remains dirty', async () => {
    const user = userEvent.setup()
    renderRouter()
    await makeDirectoryDraftDirty()

    await user.click(screen.getByRole('button', { name: '程序导航到设置' }))

    await waitFor(() => expect(confirmMock).toHaveBeenCalledTimes(1))
    expect(screen.getByLabelText('当前路由')).toHaveTextContent('/users?view=access')
    expect(screen.queryByRole('textbox', { name: '设置草稿' })).toBeNull()
  })

  it('allows settings query navigation without discarding the mounted draft', async () => {
    const user = userEvent.setup()
    renderRouter(['/settings?tab=general'])

    await user.type(screen.getByRole('textbox', { name: '设置草稿' }), '保留当前修改')
    await waitFor(() => expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(true))

    await user.click(screen.getByRole('button', { name: '切换到分享分类' }))

    await waitFor(() => {
      expect(screen.getByLabelText('当前路由')).toHaveTextContent('/settings?tab=shares')
    })
    expect(confirmMock).not.toHaveBeenCalled()
    expect(screen.getByRole('textbox', { name: '设置草稿' })).toHaveValue('保留当前修改')
    expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(true)
  })

  it('still confirms before a dirty settings draft leaves its pathname', async () => {
    const user = userEvent.setup()
    renderRouter(['/settings?tab=general'])

    await user.type(screen.getByRole('textbox', { name: '设置草稿' }), '尚未保存')
    await user.click(screen.getByRole('button', { name: '离开设置页面' }))

    await waitFor(() => expect(confirmMock).toHaveBeenCalledTimes(1))
    expect(screen.getByLabelText('当前路由')).toHaveTextContent('/settings?tab=general')
    expect(screen.getByRole('textbox', { name: '设置草稿' })).toHaveValue('尚未保存')
    expect(screen.queryByText('空间目标')).toBeNull()
  })

  it('replays a confirmed POP back navigation to its exact target', async () => {
    confirmMock.mockReturnValue(true)
    const { router } = renderRouter(
      ['/dashboard?source=before', '/users?view=access', '/storage?source=after'],
      1,
    )
    await makeDirectoryDraftDirty()

    await act(async () => {
      await router.navigate(-1)
    })

    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(screen.getByLabelText('当前路由')).toHaveTextContent('/dashboard?source=before')
    expect(screen.getByText('首页目标')).toBeTruthy()
  })

  it('preserves the history index after a canceled POP and reaches the forward target', async () => {
    const { router } = renderRouter(
      ['/dashboard?source=before', '/users?view=access', '/storage?source=after'],
      1,
    )
    await makeDirectoryDraftDirty()

    await act(async () => {
      await router.navigate(-1)
    })
    expect(screen.getByLabelText('当前路由')).toHaveTextContent('/users?view=access')

    confirmMock.mockReturnValueOnce(true)
    await act(async () => {
      await router.navigate(1)
    })

    expect(confirmMock).toHaveBeenCalledTimes(2)
    expect(screen.getByLabelText('当前路由')).toHaveTextContent('/storage?source=after')
    expect(screen.getByText('空间目标')).toBeTruthy()
  })

  it('prevents full-page unload only while the directory draft is dirty', async () => {
    const user = userEvent.setup()
    let beforeUnloadListener: EventListenerOrEventListenerObject | null = null
    const originalAddEventListener = window.addEventListener.bind(window)
    const addEventListenerSpy = vi.spyOn(window, 'addEventListener').mockImplementation((
      type: string,
      listener: EventListenerOrEventListenerObject,
      options?: boolean | AddEventListenerOptions,
    ) => {
      if (type === 'beforeunload') {
        beforeUnloadListener = listener
        return
      }
      originalAddEventListener(type, listener, options)
    })
    renderRouter()
    await makeDirectoryDraftDirty()

    const blockedEvent = new Event('beforeunload', { cancelable: true })
    if (typeof beforeUnloadListener === 'function') {
      beforeUnloadListener(blockedEvent)
    } else {
      beforeUnloadListener?.handleEvent(blockedEvent)
    }
    expect(blockedEvent.defaultPrevented).toBe(true)

    await user.click(screen.getByRole('button', { name: '重置草稿' }))
    const allowedEvent = new Event('beforeunload', { cancelable: true })
    if (typeof beforeUnloadListener === 'function') {
      beforeUnloadListener(allowedEvent)
    } else {
      beforeUnloadListener?.handleEvent(allowedEvent)
    }
    expect(allowedEvent.defaultPrevented).toBe(false)
    addEventListenerSpy.mockRestore()
  })
})
