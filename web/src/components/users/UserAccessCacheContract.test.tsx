import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  Link,
  Outlet,
  RouterProvider,
  createMemoryRouter,
} from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import * as settingsApi from '@/api/settings'
import { useSettingsDraftStore } from '@/stores/settingsDraft'
import { UserAccessView } from './UserAccessView'

vi.mock('@heroui/react', async () => {
  const actual = await vi.importActual<typeof import('@heroui/react')>('@heroui/react')
  return { ...actual, addToast: vi.fn() }
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

function settingsResponse(
  root = '~/.mnemonas',
  quotaBytes = 1073741824,
) {
  return {
    success: true,
    data: {
      storage: {
        root,
        directory_quotas: [{ path: '/team', quota_bytes: quotaBytes }],
        directory_access_rules: [{ path: '/team', read_groups: ['family'] }],
      },
    },
  } as Awaited<ReturnType<typeof settingsApi.getSettings>>
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

function SettingsQueryConsumer() {
  const { data } = useQuery({
    queryKey: ['settings', 'admin-1'],
    queryFn: ({ signal }) => settingsApi.getSettings({ signal }),
    staleTime: Infinity,
  })
  return <output aria-label="设置缓存根目录">{data?.data.storage.root ?? '加载中'}</output>
}

function CacheContractRoot() {
  return (
    <>
      <nav>
        <Link to="/directory">目录策略</Link>
        <Link to="/settings-consumer">设置消费者</Link>
      </nav>
      <Outlet />
    </>
  )
}

function renderCacheRouter(
  queryClient: QueryClient,
  initialEntry: '/directory' | '/settings-consumer',
) {
  const router = createMemoryRouter([
    {
      path: '/',
      element: <CacheContractRoot />,
      children: [
        { path: 'directory', element: <UserAccessView /> },
        { path: 'settings-consumer', element: <SettingsQueryConsumer /> },
      ],
    },
  ], { initialEntries: [initialEntry] })
  return {
    router,
    ...render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    ),
  }
}

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  })
}

describe('directory policy cache contract', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useSettingsDraftStore.setState({ hasPendingChanges: false })
    vi.mocked(settingsApi.getSettings).mockResolvedValue(settingsResponse())
    vi.mocked(settingsApi.updateSettings).mockResolvedValue({ success: true, message: '' })
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

  it('keeps directory snapshots separate from the standard settings response cache', async () => {
    const user = userEvent.setup()
    const queryClient = createQueryClient()
    queryClient.setQueryData(['settings', 'admin-1'], settingsResponse('/cached-settings'))
    renderCacheRouter(queryClient, '/directory')

    expect(await screen.findByRole('textbox', { name: '目录配额' })).toHaveValue('/team 1 GB')
    expect(queryClient.getQueryData(['directory-policies', 'admin-1'])).toMatchObject({
      response: { success: true },
      generation: expect.any(Number),
    })
    expect(queryClient.getQueryData(['settings', 'admin-1'])).toMatchObject({
      success: true,
      data: { storage: { root: '/cached-settings' } },
    })
    expect(queryClient.getQueryData(['settings', 'admin-1'])).not.toHaveProperty('response')

    await user.click(screen.getByRole('link', { name: '设置消费者' }))
    expect(await screen.findByLabelText('设置缓存根目录')).toHaveTextContent('/cached-settings')
  })

  it('updates and invalidates a standard settings cache without changing its shape after save', async () => {
    const user = userEvent.setup()
    const queryClient = createQueryClient()
    queryClient.setQueryData(['settings', 'admin-1'], settingsResponse('/cached-settings'))
    renderCacheRouter(queryClient, '/directory')

    fireEvent.change(await screen.findByRole('textbox', { name: '目录配额' }), {
      target: { value: '/team 2 GB' },
    })
    await user.click(screen.getByRole('button', { name: '保存目录策略' }))

    await waitFor(() => {
      expect(queryClient.getQueryData(['settings', 'admin-1'])).toMatchObject({
        success: true,
        data: {
          storage: {
            root: '/cached-settings',
            directory_quotas: [{ path: '/team', quota_bytes: 2147483648 }],
          },
        },
      })
      expect(queryClient.getQueryState(['settings', 'admin-1'])?.isInvalidated).toBe(true)
    })
    expect(queryClient.getQueryData(['settings', 'admin-1'])).not.toHaveProperty('response')
  })

  it('shows a loading state instead of an empty draft when entering from a settings consumer', async () => {
    const user = userEvent.setup()
    const directoryResponse = deferred<Awaited<ReturnType<typeof settingsApi.getSettings>>>()
    vi.mocked(settingsApi.getSettings).mockReturnValueOnce(directoryResponse.promise)
    const queryClient = createQueryClient()
    queryClient.setQueryData(['settings', 'admin-1'], settingsResponse('/cached-settings'))
    renderCacheRouter(queryClient, '/settings-consumer')

    expect(await screen.findByLabelText('设置缓存根目录')).toHaveTextContent('/cached-settings')
    await user.click(screen.getByRole('link', { name: '目录策略' }))
    expect(screen.getByRole('status', { name: '加载目录访问策略' })).toBeTruthy()
    expect(screen.queryByRole('textbox', { name: '目录配额' })).toBeNull()

    await act(async () => {
      directoryResponse.resolve(settingsResponse('/fresh-settings', 2147483648))
      await directoryResponse.promise
    })
    expect(await screen.findByRole('textbox', { name: '目录配额' })).toHaveValue('/team 2 GB')
  })
})
