import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import { UsersPage } from './Users'
import * as usersApi from '@/api/users'
import * as authStore from '@/stores/auth'
import { UsersError } from '@/api/users'
import { useSettingsDraftStore } from '@/stores/settingsDraft'

const mockAddToast = vi.fn()
const mockTriggerBrowserDownload = vi.fn()
const originalClipboardDescriptor = Object.getOwnPropertyDescriptor(navigator, 'clipboard')

vi.mock('@heroui/react', async () => {
  const actual = await vi.importActual<typeof import('@heroui/react')>('@heroui/react')
  return {
    ...actual,
    addToast: (...args: unknown[]) => mockAddToast(...args),
  }
})

// Mock the users API
vi.mock('@/api/users', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/users')>()
  return {
    ...actual,
    listUsers: vi.fn(),
    createUser: vi.fn(),
    deleteUser: vi.fn(),
    resetUserPassword: vi.fn(),
    revokeUserSessions: vi.fn(),
    toggleUserStatus: vi.fn(),
    updateUser: vi.fn(),
  }
})

vi.mock('@/stores/auth', () => ({
  useUser: vi.fn(),
}))

vi.mock('@/lib/downloadResponse', () => ({
  triggerBrowserDownload: (...args: unknown[]) => mockTriggerBrowserDownload(...args),
}))

vi.mock('@/components/users/UserAccessView', () => ({
  UserAccessView: () => <div aria-label="目录与访问管理">目录策略视图</div>,
}))

const mockUsers = [
  {
    id: 'user-1',
    username: 'admin',
    email: 'admin@example.com',
    role: 'admin' as const,
    disabled: false,
    home_dir: '/home/admin',
    created_at: '2024-01-01T00:00:00Z',
    updated_at: '2024-01-01T00:00:00Z',
    last_login_at: '2024-01-15T10:00:00Z',
    must_change_password: false,
    password_changed_at: '2024-01-02T08:30:00Z',
    quota_bytes: 10737418240,
    used_bytes: 1073741824,
  },
  {
    id: 'user-2',
    username: 'testuser',
    email: 'test@example.com',
    role: 'user' as const,
    disabled: false,
    home_dir: '/home/testuser',
    created_at: '2024-01-05T00:00:00Z',
    updated_at: '2024-01-05T00:00:00Z',
    must_change_password: true,
    quota_bytes: 5368709120,
    used_bytes: 536870912,
  },
  {
    id: 'user-3',
    username: 'guest',
    email: '',
    role: 'guest' as const,
    disabled: true,
    home_dir: '/home/guest',
    created_at: '2024-01-10T00:00:00Z',
    updated_at: '2024-01-10T00:00:00Z',
    must_change_password: false,
    quota_bytes: 0,
    used_bytes: 0,
  },
]

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  })
}

function renderUsersPage(queryClient = createTestQueryClient()) {
  return render(
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <UsersPage />
      </BrowserRouter>
    </QueryClientProvider>
  )
}

function expectAbortSignal(signal: AbortSignal | undefined): asserts signal is AbortSignal {
  expect(signal).toBeDefined()
  expect(typeof signal?.aborted).toBe('boolean')
}

function getVisibleUsernamesByCardOrder(): string[] {
  return screen.getAllByRole('button', { name: / 用户操作$/ })
    .map((button) => button.getAttribute('aria-label')?.replace(/ 用户操作$/, '') ?? '')
}

async function openUserActionMenu(user: ReturnType<typeof userEvent.setup>, username: string) {
  await user.click(screen.getByRole('button', { name: `${username} 用户操作` }))
}

async function clickUserActionMenuItem(
  user: ReturnType<typeof userEvent.setup>,
  username: string,
  action: string,
) {
  await openUserActionMenu(user, username)
  await user.click(screen.getByRole('menuitem', { name: action }))
}

describe('UsersPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    window.localStorage.clear()
    window.history.replaceState({}, '', '/users')
    useSettingsDraftStore.setState({ hasPendingChanges: false })
    vi.mocked(authStore.useUser).mockReturnValue({
      id: 'user-1',
      username: 'admin',
      role: 'admin',
      homeDir: '/',
      email: '',
      mustChangePassword: false,
    })
    vi.mocked(usersApi.listUsers).mockResolvedValue({
      success: true,
      users: mockUsers,
      total: mockUsers.length,
    })
  })

  afterEach(() => {
    if (originalClipboardDescriptor) {
      Object.defineProperty(navigator, 'clipboard', originalClipboardDescriptor)
    } else {
      Reflect.deleteProperty(navigator, 'clipboard')
    }
    window.localStorage.clear()
  })

  describe('rendering', () => {
    it('renders page header', async () => {
      renderUsersPage()
      expect(screen.getByText('用户管理')).toBeInTheDocument()
      expect(screen.getByText('管理系统用户、权限和配额')).toBeInTheDocument()
    })

    it('defaults invalid views to accounts and marks the account tab as selected', () => {
      window.history.replaceState({}, '', '/users?view=unknown&source=storage')
      renderUsersPage()

      expect(screen.getByRole('tab', { name: '用户账号' })).toHaveAttribute('aria-selected', 'true')
      expect(screen.queryByLabelText('目录与访问管理')).not.toBeInTheDocument()
      expect(screen.getByRole('button', { name: /添加用户/i })).toBeInTheDocument()
    })

    it('opens the directory access view from the query parameter', () => {
      window.history.replaceState({}, '', '/users?view=access')
      renderUsersPage()

      expect(screen.getByRole('tab', { name: '目录与访问' })).toHaveAttribute('aria-selected', 'true')
      expect(screen.getByLabelText('目录与访问管理')).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: /添加用户/i })).not.toBeInTheDocument()
      expect(usersApi.listUsers).not.toHaveBeenCalled()
    })

    it('pushes view changes while preserving unrelated query parameters', async () => {
      const user = userEvent.setup()
      const pushState = vi.spyOn(window.history, 'pushState')
      window.history.replaceState({}, '', '/users?filter=active&source=storage')
      renderUsersPage()

      await user.click(screen.getByRole('tab', { name: '目录与访问' }))
      expect(window.location.search).toContain('filter=active')
      expect(window.location.search).toContain('source=storage')
      expect(window.location.search).toContain('view=access')
      expect(pushState).toHaveBeenCalled()

      await user.click(screen.getByRole('tab', { name: '用户账号' }))
      expect(window.location.search).toContain('filter=active')
      expect(window.location.search).toContain('source=storage')
      expect(window.location.search).not.toContain('view=')
    })

    it('shows password-change requirements and the last confirmed password-change time', async () => {
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('必须修改密码')).toBeInTheDocument()
        expect(screen.getByText(/密码修改于/)).toBeInTheDocument()
        expect(screen.getAllByText('密码修改时间尚无记录').length).toBeGreaterThan(0)
      })
      expect(screen.getByText(/管理员重置不能代替此步骤/)).toBeInTheDocument()
    })

    it('refetches the user list when the current session security scope changes', async () => {
    vi.mocked(usersApi.listUsers)
      .mockResolvedValueOnce({
        success: true,
        users: mockUsers,
        total: mockUsers.length,
      })
      .mockResolvedValueOnce({
        success: true,
        users: [mockUsers[0]],
        total: 1,
      })

    const queryClient = createTestQueryClient()
    const { rerender } = renderUsersPage(queryClient)

    await waitFor(() => {
      expect(vi.mocked(usersApi.listUsers)).toHaveBeenCalledTimes(1)
    })

    vi.mocked(authStore.useUser).mockReturnValue({
      id: 'user-1',
      username: 'admin',
      role: 'user',
      homeDir: '/restricted/admin',
      email: 'admin@example.com',
      mustChangePassword: false,
    })

    rerender(
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <UsersPage />
        </BrowserRouter>
      </QueryClientProvider>
    )

    await waitFor(() => {
      expect(vi.mocked(usersApi.listUsers)).toHaveBeenCalledTimes(2)
    })
    })

    it('renders add user button', async () => {
      renderUsersPage()
      expect(screen.getByRole('button', { name: /添加用户/i })).toBeInTheDocument()
    })

    it('renders refresh button', async () => {
      renderUsersPage()
      expect(screen.getByRole('button', { name: /刷新/i })).toBeInTheDocument()
    })

    it('renders stats cards', async () => {
      renderUsersPage()
      await waitFor(() => {
        expect(screen.getByText('总用户数')).toBeInTheDocument()
        expect(screen.getByRole('button', { name: '查看管理员' })).toBeInTheDocument()
        expect(screen.getByRole('button', { name: '查看活跃用户' })).toBeInTheDocument()
      })
    })
  })

  describe('user list', () => {
    it('displays users after loading', async () => {
      renderUsersPage()
      await waitFor(() => {
        expect(screen.getByText('admin')).toBeInTheDocument()
        expect(screen.getByText('testuser')).toBeInTheDocument()
        expect(screen.getByText('guest')).toBeInTheDocument()
        expect(screen.getByText('显示全部 3 个用户')).toBeInTheDocument()
      })
    })

    it('focuses the readiness remediation on enabled administrators that must change passwords', async () => {
      window.history.replaceState({}, '', '/users?filter=password-change-required')
      const focusedUsers = [
        mockUsers[0],
        {
          ...mockUsers[0],
          id: 'pending-admin',
          username: 'pending-admin',
          must_change_password: true,
          password_changed_at: undefined,
        },
        {
          ...mockUsers[0],
          id: 'disabled-pending-admin',
          username: 'disabled-pending-admin',
          disabled: true,
          must_change_password: true,
        },
        mockUsers[1],
      ]
      vi.mocked(usersApi.listUsers).mockResolvedValueOnce({
        success: true,
        users: focusedUsers,
        total: focusedUsers.length,
      })

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByRole('region', { name: '待改密管理员处理说明' })).toBeInTheDocument()
        expect(screen.getByText('待改密管理员 1 个')).toBeInTheDocument()
        expect(getVisibleUsernamesByCardOrder()).toEqual(['pending-admin'])
      })
      expect(screen.getByText(/账号本人需使用当前临时密码登录/)).toBeInTheDocument()
      expect(screen.getByText(/不能代替账号本人完成改密/)).toBeInTheDocument()
    })

    it('keeps the selected filter in the URL while preserving unrelated parameters', async () => {
      const user = userEvent.setup()
      window.history.replaceState({}, '', '/users?source=readiness')
      renderUsersPage()
      await screen.findByText('admin')

      await user.click(screen.getByRole('button', { name: '管理员' }))

      expect(new URLSearchParams(window.location.search).get('filter')).toBe('admin')
      expect(new URLSearchParams(window.location.search).get('source')).toBe('readiness')
      await user.click(screen.getByRole('button', { name: '全部用户' }))
      expect(new URLSearchParams(window.location.search).has('filter')).toBe(false)
      expect(new URLSearchParams(window.location.search).get('source')).toBe('readiness')
    })

    it('updates the active filter when browser navigation changes the URL', async () => {
      renderUsersPage()
      await screen.findByText('admin')

      act(() => {
        window.history.pushState({}, '', '/users?filter=disabled-account')
        window.dispatchEvent(new PopStateEvent('popstate'))
      })

      await waitFor(() => {
        expect(screen.getByText('停用账号 1 / 3 个用户')).toBeInTheDocument()
        expect(getVisibleUsernamesByCardOrder()).toEqual(['guest'])
      })
    })

    it('searches users by username, email, group, and home directory', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            ...mockUsers[0],
            username: 'admin',
            email: 'admin@example.com',
            groups: ['ops'],
            home_dir: '/home/admin',
          },
          {
            ...mockUsers[1],
            username: 'alice',
            email: 'alice@example.com',
            groups: ['family', 'editors'],
            home_dir: '/family/alice',
          },
          {
            ...mockUsers[2],
            username: 'media',
            email: '',
            groups: ['guests'],
            home_dir: '/shares/media',
          },
        ],
        total: 3,
      })

      renderUsersPage()

      const searchInput = await screen.findByRole('textbox', { name: '搜索用户' })
      await user.type(searchInput, 'editors')

      expect(screen.getByText('alice')).toBeInTheDocument()
      expect(screen.getByText('搜索命中 1 / 3 个用户')).toBeInTheDocument()
      expect(screen.queryByText('admin')).not.toBeInTheDocument()
      expect(screen.queryByText('media')).not.toBeInTheDocument()

      fireEvent.change(searchInput, { target: { value: '/shares' } })
      expect(screen.getByText('media')).toBeInTheDocument()
      expect(screen.queryByText('alice')).not.toBeInTheDocument()

      fireEvent.change(searchInput, { target: { value: 'ADMIN@EXAMPLE' } })
      expect(screen.getByText('admin')).toBeInTheDocument()
      expect(screen.queryByText('media')).not.toBeInTheDocument()
    })

    it('combines user search with review hint filtering', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            ...mockUsers[0],
            id: 'user-healthy',
            username: 'healthy',
            role: 'user',
            disabled: false,
            groups: ['family'],
            last_login_at: '2024-01-15T10:00:00Z',
            quota_bytes: 1000,
            used_bytes: 100,
          },
          {
            ...mockUsers[1],
            id: 'user-warning',
            username: 'warning',
            role: 'user',
            disabled: false,
            groups: ['family'],
            last_login_at: undefined,
            quota_bytes: 1000,
            used_bytes: 900,
          },
          {
            ...mockUsers[2],
            id: 'user-admin-review',
            username: 'adminreview',
            role: 'admin',
            disabled: false,
            groups: ['ops'],
            last_login_at: '2024-01-15T10:00:00Z',
            quota_bytes: 0,
            used_bytes: 0,
          },
        ],
        total: 3,
      })

      renderUsersPage()

      await user.click(await screen.findByRole('button', { name: '复核提示' }))
      await user.type(screen.getByRole('textbox', { name: '搜索用户' }), 'family')

      expect(screen.getByText('warning')).toBeInTheDocument()
      expect(screen.getByText('复核提示中搜索命中 1 / 2 个用户（全量 3 个）')).toBeInTheDocument()
      expect(screen.queryByText('healthy')).not.toBeInTheDocument()
      expect(screen.queryByText('adminreview')).not.toBeInTheDocument()

      await user.click(screen.getByRole('button', { name: '清除筛选' }))

      expect(screen.getByText('显示全部 3 个用户')).toBeInTheDocument()
      expect(screen.getByText('healthy')).toBeInTheDocument()
      expect(screen.getByText('adminreview')).toBeInTheDocument()
    })

    it('filters account attention by disabled and never-login reasons', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await screen.findByText('admin')

      await user.click(screen.getByRole('button', { name: '停用账号' }))
      expect(screen.getByText('停用账号 1 / 3 个用户')).toBeInTheDocument()
      expect(getVisibleUsernamesByCardOrder()).toEqual(['guest'])
      expect(screen.queryByText('testuser')).not.toBeInTheDocument()

      await user.click(screen.getByRole('button', { name: '从未登录' }))
      expect(screen.getByText('从未登录 2 / 3 个用户')).toBeInTheDocument()
      expect(getVisibleUsernamesByCardOrder()).toEqual(['guest', 'testuser'])
      expect(screen.queryByText('admin')).not.toBeInTheDocument()
    })

    it('focuses user list filters from the stats cards', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            ...mockUsers[0],
            id: 'user-healthy',
            username: 'healthy',
            role: 'user',
            disabled: false,
            last_login_at: '2024-01-15T10:00:00Z',
            quota_bytes: 1000,
            used_bytes: 100,
          },
          {
            ...mockUsers[1],
            id: 'user-never',
            username: 'neverlogin',
            role: 'user',
            disabled: false,
            last_login_at: undefined,
            quota_bytes: 1000,
            used_bytes: 100,
          },
          {
            ...mockUsers[2],
            id: 'user-near-quota',
            username: 'nearquota',
            role: 'user',
            disabled: false,
            last_login_at: '2024-01-16T10:00:00Z',
            quota_bytes: 1000,
            used_bytes: 900,
          },
          {
            ...mockUsers[2],
            id: 'user-admin-review',
            username: 'adminreview',
            role: 'admin',
            disabled: false,
            last_login_at: '2024-01-17T10:00:00Z',
            quota_bytes: 0,
            used_bytes: 0,
          },
          {
            ...mockUsers[2],
            id: 'user-disabled',
            username: 'disabled',
            role: 'guest',
            disabled: true,
            last_login_at: '2024-01-18T10:00:00Z',
            quota_bytes: 1000,
            used_bytes: 100,
          },
        ],
        total: 5,
      })

      renderUsersPage()

      const searchInput = await screen.findByRole('textbox', { name: '搜索用户' })
      await user.type(searchInput, 'healthy')
      expect(screen.getByText('搜索命中 1 / 5 个用户')).toBeInTheDocument()

      await user.click(screen.getByRole('button', { name: '查看管理员' }))
      expect((searchInput as HTMLInputElement).value).toBe('')
      expect(screen.getByText('管理员 1 / 5 个用户')).toBeInTheDocument()
      expect(getVisibleUsernamesByCardOrder()).toEqual(['adminreview'])

      await user.click(screen.getByRole('button', { name: '查看活跃用户' }))
      expect(screen.getByText('活跃用户 4 / 5 个用户')).toBeInTheDocument()
      expect(getVisibleUsernamesByCardOrder()).toEqual(['adminreview', 'healthy', 'nearquota', 'neverlogin'])
      expect(screen.queryByText('disabled')).not.toBeInTheDocument()

      await user.click(screen.getByRole('button', { name: '查看账号关注用户' }))
      expect(screen.getByText('账号关注 2 / 5 个用户')).toBeInTheDocument()
      expect(getVisibleUsernamesByCardOrder()).toEqual(['disabled', 'neverlogin'])

      await user.click(screen.getByRole('button', { name: '查看配额关注用户' }))
      expect(screen.getByText('配额关注 1 / 5 个用户')).toBeInTheDocument()
      expect(getVisibleUsernamesByCardOrder()).toEqual(['nearquota'])

      await user.click(screen.getByRole('button', { name: '查看复核提示用户' }))
      expect(screen.getByText('复核提示 4 / 5 个用户')).toBeInTheDocument()
      expect(getVisibleUsernamesByCardOrder()).toEqual(['disabled', 'nearquota', 'neverlogin', 'adminreview'])

      await user.click(screen.getByRole('button', { name: '查看全部用户' }))
      expect(screen.getByText('显示全部 5 个用户')).toBeInTheDocument()
      expect(getVisibleUsernamesByCardOrder()).toEqual(['healthy', 'neverlogin', 'nearquota', 'adminreview', 'disabled'])
    })

    it('sorts users from the list toolbar and clears the selected sort', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            ...mockUsers[0],
            id: 'user-beta',
            username: 'beta',
            role: 'user',
            quota_bytes: 1000,
            used_bytes: 50,
            last_login_at: '2024-01-15T10:00:00Z',
          },
          {
            ...mockUsers[1],
            id: 'user-alpha',
            username: 'alpha',
            role: 'user',
            quota_bytes: 1000,
            used_bytes: 900,
            last_login_at: '2024-02-15T10:00:00Z',
          },
          {
            ...mockUsers[2],
            id: 'user-gamma',
            username: 'gamma',
            role: 'guest',
            disabled: false,
            quota_bytes: 1000,
            used_bytes: 300,
            last_login_at: undefined,
          },
        ],
        total: 3,
      })

      renderUsersPage()

      await screen.findByRole('button', { name: '排序：默认顺序' })
      await screen.findByText('beta')
      expect(getVisibleUsernamesByCardOrder()).toEqual(['beta', 'alpha', 'gamma'])

      await user.click(screen.getByRole('button', { name: '排序：默认顺序' }))
      await user.click(await screen.findByText('按容量用量'))

      expect(screen.getByRole('button', { name: '排序：容量用量' })).toBeInTheDocument()
      expect(screen.getByText('显示全部 3 个用户 · 排序：容量用量')).toBeInTheDocument()
      expect(getVisibleUsernamesByCardOrder()).toEqual(['alpha', 'gamma', 'beta'])

      await user.click(screen.getByRole('button', { name: '清除筛选' }))

      expect(screen.getByRole('button', { name: '排序：默认顺序' })).toBeInTheDocument()
      expect(screen.getByText('显示全部 3 个用户')).toBeInTheDocument()
      expect(getVisibleUsernamesByCardOrder()).toEqual(['beta', 'alpha', 'gamma'])
    })

    it('exports the current visible user list as CSV', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            ...mockUsers[0],
            id: 'user-alice',
            username: 'alice',
            email: 'alice@example.com',
            role: 'user',
            groups: ['family', 'editors'],
            home_dir: '/family/alice',
            quota_bytes: 1000,
            used_bytes: 900,
          },
          {
            ...mockUsers[1],
            id: 'user-media',
            username: 'media',
            email: '',
            role: 'guest',
            groups: ['media'],
            home_dir: '/shares/media',
            quota_bytes: 0,
            used_bytes: 100,
          },
        ],
        total: 2,
      })

      renderUsersPage()

      await user.type(await screen.findByRole('textbox', { name: '搜索用户' }), 'family')
      await user.click(screen.getByRole('button', { name: '导出当前清单' }))

      await waitFor(() => {
        expect(mockTriggerBrowserDownload).toHaveBeenCalledTimes(1)
        expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({
          title: '用户清单已导出',
          description: '已导出当前视图 1 个用户。',
          color: 'success',
        }))
      })

      const [blob, filename] = mockTriggerBrowserDownload.mock.calls[0] as [Blob, string]
      expect(filename).toMatch(/^mnemonas-users-.+\.csv$/)
      const csv = await blob.text()
      expect(csv.startsWith('\uFEFF导出范围,搜索命中 1 / 2 个用户')).toBe(true)
      expect(csv).toContain('搜索,family')
      expect(csv).toContain('用户ID,用户名,邮箱,角色,状态,账号关注,用户组,主目录,权限范围,权限说明,配额状态,配额使用率,配额说明')
      expect(csv).toContain('user-alice,alice,alice@example.com,用户,启用,无,editors; family,/family/alice,主目录 + 用户组范围')
      expect(csv).toContain('接近上限,90%,剩余 100 B。,900,1000,配额接近上限')
      expect(csv).not.toContain('user-media')
    })

    it('shows an empty state when user search has no matches', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await user.type(await screen.findByRole('textbox', { name: '搜索用户' }), 'missing-user')

      expect(screen.getByText('没有匹配的用户')).toBeInTheDocument()
      expect(screen.getByText('请调整搜索关键词，或切换用户列表筛选条件。')).toBeInTheDocument()
      expect(screen.queryByText('admin')).not.toBeInTheDocument()

      await user.click(screen.getByRole('button', { name: '清除空状态用户筛选' }))

      expect(screen.getByText('显示全部 3 个用户')).toBeInTheDocument()
      expect(screen.getByText('admin')).toBeInTheDocument()
    })

    it('shows current user badge', async () => {
      renderUsersPage()
      await waitFor(() => {
        expect(screen.getByText('当前用户')).toBeInTheDocument()
      })
    })

    it('shows disabled badge for disabled users', async () => {
      renderUsersPage()
      await waitFor(() => {
        expect(screen.getByText('已禁用')).toBeInTheDocument()
      })
    })

    it('shows role badges', async () => {
      renderUsersPage()
      await waitFor(() => {
        // Use getAllByText since there might be multiple occurrences
        const adminBadges = screen.getAllByText('管理员')
        expect(adminBadges.length).toBeGreaterThan(0)
      })
    })

    it('shows each user home directory on the user card', async () => {
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('/home/admin')).toBeInTheDocument()
        expect(screen.getByText('/home/testuser')).toBeInTheDocument()
      })
    })

    it('shows permission scope context and account review hints on user cards', async () => {
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            ...mockUsers[1],
            id: 'user-alice',
            username: 'alice',
            groups: ['family', 'editors'],
            home_dir: '/home/alice',
            last_login_at: undefined,
            quota_bytes: 1000,
            used_bytes: 1200,
          },
          {
            ...mockUsers[2],
            id: 'user-visitor',
            username: 'visitor',
            role: 'guest',
            disabled: true,
            groups: [],
            home_dir: '/guest/public',
            last_login_at: '2024-01-18T10:00:00Z',
          },
        ],
        total: 2,
      })

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('alice')).toBeInTheDocument()
        expect(screen.getByText('visitor')).toBeInTheDocument()
      })

      expect(screen.getByText('主目录 + 用户组范围')).toBeInTheDocument()
      expect(screen.getByText('主目录 /home/alice；用户组 editors, family，命中目录授权时可访问共享路径。')).toBeInTheDocument()
      expect(screen.getByText('访客主目录范围')).toBeInTheDocument()
      expect(screen.getByText('默认限制在 /guest/public；未加入用户组。')).toBeInTheDocument()

      const aliceHints = screen.getByLabelText('alice 复核提示')
      expect(within(aliceHints).getByText('从未登录')).toBeInTheDocument()
      expect(within(aliceHints).getByText('配额已超限')).toBeInTheDocument()
      expect(within(screen.getByLabelText('visitor 复核提示')).getByText('复核停用账号')).toBeInTheDocument()
    })

    it('renders unknown roles with their backend label and omits missing optional fields', async () => {
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            id: 'user-4',
            username: 'auditor',
            email: '',
            role: 'manager' as unknown as 'user',
            disabled: false,
            home_dir: '/home/auditor',
            created_at: '2024-01-20T00:00:00Z',
            updated_at: '2024-01-20T00:00:00Z',
            quota_bytes: 0,
            used_bytes: 0,
          },
        ],
        total: 1,
      })

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('auditor')).toBeInTheDocument()
        expect(screen.getByText('manager')).toBeInTheDocument()
      })
      expect(screen.queryByText(/最后登录/)).not.toBeInTheDocument()
      expect(screen.getByText('已用 0 B')).toBeInTheDocument()
    })

    it('shows quota usage state on user cards', async () => {
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            ...mockUsers[1],
            id: 'user-near-quota',
            username: 'nearquota',
            quota_bytes: 1000,
            used_bytes: 950,
          },
          {
            ...mockUsers[2],
            id: 'user-over-quota',
            username: 'overquota',
            quota_bytes: 1000,
            used_bytes: 1200,
          },
          {
            ...mockUsers[0],
            id: 'user-unlimited',
            username: 'unlimited',
            quota_bytes: 0,
            used_bytes: 2048,
          },
        ],
        total: 3,
      })

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('nearquota')).toBeInTheDocument()
        expect(screen.getByText('overquota')).toBeInTheDocument()
      })

      expect(screen.getByText('接近上限')).toBeInTheDocument()
      expect(screen.getByText('已超限')).toBeInTheDocument()
      expect(screen.getAllByText('未设配额').length).toBeGreaterThanOrEqual(1)
      expect(screen.getByRole('progressbar', { name: 'nearquota 配额使用率' })).toHaveAttribute('aria-valuetext', '95% 已用，剩余 50 B。')
      expect(screen.getByRole('progressbar', { name: 'overquota 配额使用率' })).toHaveAttribute('aria-valuetext', '120% 已用，已超出 200 B。')
      expect(screen.getByRole('progressbar', { name: 'unlimited 未设置用户容量限制' })).toHaveAttribute('aria-valuetext', '不限额，已用 2 KB')
      expect(screen.getByText('95%')).toBeInTheDocument()
      expect(screen.getByText('120%')).toBeInTheDocument()
    })

    it('copies a user quota summary for administrator review', async () => {
      const user = userEvent.setup()
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            ...mockUsers[0],
            username: 'admin',
            role: 'admin',
            disabled: false,
            groups: ['admins'],
            quota_bytes: 1000,
            used_bytes: 100,
          },
          {
            ...mockUsers[1],
            username: 'alice',
            role: 'user',
            disabled: false,
            groups: ['family', 'editors'],
            home_dir: '/home/alice',
            quota_bytes: 1000,
            used_bytes: 900,
          },
          {
            ...mockUsers[2],
            username: 'guest',
            role: 'guest',
            disabled: true,
            groups: [],
            quota_bytes: 1000,
            used_bytes: 1200,
          },
        ],
        total: 3,
      })

      renderUsersPage()

      const copyButton = await screen.findByRole('button', { name: '复制配额摘要' })
      await user.click(copyButton)

      await waitFor(() => {
        expect(writeText).toHaveBeenCalledTimes(1)
      })
      const report = writeText.mock.calls[0]?.[0] as string
      expect(report).toContain('用户配额摘要')
      expect(report).toContain('用户总数：3 个')
      expect(report).toContain('需复核：2 个')
      expect(report).toContain('用户名 | 邮箱 | 角色 | 状态 | 用户组 | 主目录 | 最后登录 | 配额状态 | 用量 | 剩余/超出 | 占比 | 建议处理')
      expect(report).toContain('admin | admin@example.com | 管理员 | 启用 | admins | /home/admin | 2024-01-15T10:00:00Z | 配额正常 | 100 B / 1000 B | 剩余 900 B')
      expect(report).toContain('alice | test@example.com | 普通用户 | 启用 | family, editors | /home/alice | 从未登录 | 接近上限 | 900 B / 1000 B | 剩余 100 B')
      expect(report).toContain('guest | 未设置 | 访客 | 已停用 | 未分组 | /home/guest | 从未登录 | 已超限 | 1.17 KB / 1000 B | 超出 200 B')
      expect(report).toMatch(/guest[\s\S]*alice[\s\S]*admin/)
      expect(mockAddToast).toHaveBeenCalledWith({ title: '用户配额摘要已复制', color: 'success' })
    })

    it('copies a user account attention summary for administrator review', async () => {
      const user = userEvent.setup()
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            ...mockUsers[0],
            username: 'owner',
            role: 'admin',
            disabled: false,
            groups: ['admins'],
            home_dir: '/',
            last_login_at: '2024-01-15T10:00:00Z',
          },
          {
            ...mockUsers[1],
            username: 'alice',
            role: 'user',
            disabled: false,
            groups: ['family'],
            home_dir: '/home/alice',
            last_login_at: undefined,
          },
          {
            ...mockUsers[2],
            username: 'guest',
            role: 'guest',
            disabled: true,
            groups: [],
            home_dir: '/guest/public',
            last_login_at: undefined,
          },
        ],
        total: 3,
      })

      renderUsersPage()

      const copyButton = await screen.findByRole('button', { name: '复制账号摘要' })
      await user.click(copyButton)

      await waitFor(() => {
        expect(writeText).toHaveBeenCalledTimes(1)
      })
      const report = writeText.mock.calls[0]?.[0] as string
      expect(report).toContain('用户账号复核摘要')
      expect(report).toContain('用户总数：3 个')
      expect(report).toContain('需复核：2 个')
      expect(report).toContain('停用账号：1 个')
      expect(report).toContain('从未登录：2 个')
      expect(report).toContain('用户名 | 邮箱 | 角色 | 状态 | 用户组 | 主目录 | 最后登录 | 账号关注 | 建议处理')
      expect(report).toContain('guest | 未设置 | 访客 | 已停用 | 未分组 | /guest/public | 从未登录 | 停用账号, 从未登录')
      expect(report).toContain('alice | test@example.com | 普通用户 | 启用 | family | /home/alice | 从未登录 | 从未登录')
      expect(report).toContain('owner | admin@example.com | 管理员 | 启用 | admins | / | 2024-01-15T10:00:00Z | 无')
      expect(report).toMatch(/guest[\s\S]*alice[\s\S]*owner/)
      expect(mockAddToast).toHaveBeenCalledWith({ title: '用户账号摘要已复制', color: 'success' })
    })

    it('copies a user access review summary for administrator review', async () => {
      const user = userEvent.setup()
      const writeText = vi.fn().mockResolvedValue(undefined)
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText },
      })
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            ...mockUsers[0],
            username: 'admin',
            role: 'admin',
            disabled: false,
            groups: ['ops'],
            home_dir: '/',
            quota_bytes: 0,
            used_bytes: 2048,
          },
          {
            ...mockUsers[1],
            username: 'alice',
            role: 'user',
            disabled: false,
            groups: ['family', 'editors'],
            home_dir: '/home/alice',
            last_login_at: undefined,
            quota_bytes: 1000,
            used_bytes: 900,
          },
          {
            ...mockUsers[2],
            username: 'guest',
            role: 'guest',
            disabled: true,
            groups: [],
            home_dir: '/guest/public',
            last_login_at: '2024-01-16T10:00:00Z',
            quota_bytes: 1000,
            used_bytes: 1200,
          },
        ],
        total: 3,
      })

      renderUsersPage()

      const copyButton = await screen.findByRole('button', { name: '复制权限摘要' })
      await user.click(copyButton)

      await waitFor(() => {
        expect(writeText).toHaveBeenCalledTimes(1)
      })
      const report = writeText.mock.calls[0]?.[0] as string
      expect(report).toContain('用户权限复核摘要')
      expect(report).toContain('用户总数：3 个')
      expect(report).toContain('管理员：1 个')
      expect(report).toContain('需复核：3 个')
      expect(report).toContain('用户名 | 邮箱 | 角色 | 状态 | 用户组 | 主目录 | 权限范围 | 权限说明 | 复核提示 | 最后登录')
      expect(report).toContain('guest | 未设置 | 访客 | 已停用 | 未分组 | /guest/public | 访客主目录范围')
      expect(report).toContain('alice | test@example.com | 普通用户 | 启用 | editors, family | /home/alice | 主目录 + 用户组范围')
      expect(report).toContain('admin | admin@example.com | 管理员 | 启用 | ops | / | 管理员全局范围')
      expect(report).toMatch(/guest[\s\S]*alice[\s\S]*admin/)
      expect(mockAddToast).toHaveBeenCalledWith({ title: '用户权限摘要已复制', color: 'success' })
    })

    it('filters the list to quota attention users', async () => {
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            ...mockUsers[0],
            id: 'user-healthy',
            username: 'healthy',
            quota_bytes: 1000,
            used_bytes: 100,
          },
          {
            ...mockUsers[1],
            id: 'user-near-quota',
            username: 'nearquota',
            quota_bytes: 1000,
            used_bytes: 900,
          },
          {
            ...mockUsers[2],
            id: 'user-over-quota',
            username: 'overquota',
            quota_bytes: 1000,
            used_bytes: 1200,
          },
        ],
        total: 3,
      })

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('healthy')).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: '配额关注' }))

      expect(screen.queryByText('healthy')).not.toBeInTheDocument()
      expect(screen.getByText('nearquota')).toBeInTheDocument()
      expect(screen.getByText('overquota')).toBeInTheDocument()
      expect(document.body.textContent).toMatch(/overquota[\s\S]*nearquota/)

      await userEvent.click(screen.getByRole('button', { name: '全部用户' }))

      expect(screen.getByText('healthy')).toBeInTheDocument()
    })

    it('shows an empty state when quota attention filter has no matches', async () => {
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            ...mockUsers[0],
            id: 'user-healthy',
            username: 'healthy',
            quota_bytes: 1000,
            used_bytes: 100,
          },
        ],
        total: 1,
      })

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('healthy')).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: '配额关注' }))

      expect(screen.getByText('暂无配额关注用户')).toBeInTheDocument()
      expect(screen.getByText('所有已设置配额的用户当前都低于关注阈值。')).toBeInTheDocument()
      expect(screen.queryByText('healthy')).not.toBeInTheDocument()
    })

    it('filters the list to account attention users', async () => {
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            ...mockUsers[0],
            id: 'user-healthy',
            username: 'healthy',
            disabled: false,
            last_login_at: '2024-01-15T10:00:00Z',
          },
          {
            ...mockUsers[1],
            id: 'user-never-login',
            username: 'neverlogin',
            disabled: false,
            last_login_at: undefined,
          },
          {
            ...mockUsers[2],
            id: 'user-disabled',
            username: 'disabled',
            disabled: true,
            last_login_at: '2024-01-16T10:00:00Z',
          },
        ],
        total: 3,
      })

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('healthy')).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: '账号关注' }))

      expect(screen.queryByText('healthy')).not.toBeInTheDocument()
      expect(screen.getByText('disabled')).toBeInTheDocument()
      expect(screen.getByText('neverlogin')).toBeInTheDocument()
      expect(document.body.textContent).toMatch(/disabled[\s\S]*neverlogin/)

      await userEvent.click(screen.getByRole('button', { name: '全部用户' }))

      expect(screen.getByText('healthy')).toBeInTheDocument()
    })

    it('filters the list to users with review hints', async () => {
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            ...mockUsers[0],
            id: 'user-healthy',
            username: 'healthy',
            role: 'user',
            disabled: false,
            last_login_at: '2024-01-15T10:00:00Z',
            quota_bytes: 1000,
            used_bytes: 100,
          },
          {
            ...mockUsers[0],
            id: 'user-admin',
            username: 'adminreview',
            role: 'admin',
            disabled: false,
            last_login_at: '2024-01-15T10:00:00Z',
            quota_bytes: 0,
            used_bytes: 2048,
          },
          {
            ...mockUsers[1],
            id: 'user-near-quota',
            username: 'nearquota',
            disabled: false,
            last_login_at: undefined,
            quota_bytes: 1000,
            used_bytes: 900,
          },
          {
            ...mockUsers[2],
            id: 'user-over-quota',
            username: 'overquota',
            disabled: false,
            last_login_at: '2024-01-16T10:00:00Z',
            quota_bytes: 1000,
            used_bytes: 1200,
          },
        ],
        total: 4,
      })

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('healthy')).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: '复核提示' }))

      expect(screen.queryByText('healthy')).not.toBeInTheDocument()
      expect(screen.getByText('overquota')).toBeInTheDocument()
      expect(screen.getByText('nearquota')).toBeInTheDocument()
      expect(screen.getByText('adminreview')).toBeInTheDocument()
      expect(document.body.textContent).toMatch(/overquota[\s\S]*nearquota[\s\S]*adminreview/)
    })

    it('shows an empty state when review hint filter has no matches', async () => {
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            ...mockUsers[0],
            id: 'user-healthy',
            username: 'healthy',
            role: 'user',
            disabled: false,
            last_login_at: '2024-01-15T10:00:00Z',
            quota_bytes: 1000,
            used_bytes: 100,
          },
        ],
        total: 1,
      })

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('healthy')).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: '复核提示' }))

      expect(screen.getByText('暂无复核提示用户')).toBeInTheDocument()
      expect(screen.getByText('所有用户当前暂无账号、权限或配额复核提示。')).toBeInTheDocument()
      expect(screen.queryByText('healthy')).not.toBeInTheDocument()
    })

    it('shows an empty state when account attention filter has no matches', async () => {
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          {
            ...mockUsers[0],
            id: 'user-healthy',
            username: 'healthy',
            disabled: false,
            last_login_at: '2024-01-15T10:00:00Z',
          },
        ],
        total: 1,
      })

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('healthy')).toBeInTheDocument()
      })

      await userEvent.click(screen.getByRole('button', { name: '账号关注' }))

      expect(screen.getByText('暂无账号关注用户')).toBeInTheDocument()
      expect(screen.getByText('所有用户当前均为启用且已有登录记录。')).toBeInTheDocument()
      expect(screen.queryByText('healthy')).not.toBeInTheDocument()
    })
  })

  describe('stats', () => {
    it('shows correct total users count', async () => {
      renderUsersPage()
      await waitFor(() => {
        const totalUsersCard = screen.getByRole('group', { name: '总用户数，3' })
        expect(within(totalUsersCard).getByRole('button', { name: '查看全部用户' })).toBeInTheDocument()
        expect(within(totalUsersCard).getByText('3')).toBeInTheDocument()
      })
    })

    it('shows correct admin count', async () => {
      renderUsersPage()
      await waitFor(() => {
        const adminCard = screen.getByRole('group', { name: '管理员，1' })
        expect(within(adminCard).getByRole('button', { name: '查看管理员' })).toBeInTheDocument()
        expect(within(adminCard).getByText('1')).toBeInTheDocument()
      })
    })

    it('shows correct active users count', async () => {
      renderUsersPage()
      await waitFor(() => {
        const activeUsersCard = screen.getByRole('group', { name: '活跃用户，2' })
        expect(within(activeUsersCard).getByRole('button', { name: '查看活跃用户' })).toBeInTheDocument()
        expect(within(activeUsersCard).getByText('2')).toBeInTheDocument()
      })
    })

    it('shows account attention count', async () => {
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          { ...mockUsers[0], disabled: false, last_login_at: '2024-01-15T10:00:00Z' },
          { ...mockUsers[1], disabled: false, last_login_at: undefined },
          { ...mockUsers[2], disabled: true, last_login_at: '2024-01-16T10:00:00Z' },
        ],
        total: 3,
      })

      renderUsersPage()

      await waitFor(() => {
        const accountAttentionCard = screen.getByRole('group', { name: '账号关注，2，停用 1 个 · 从未登录 1 个' })
        expect(within(accountAttentionCard).getByText('账号关注')).toBeInTheDocument()
        expect(within(accountAttentionCard).getByText('2')).toBeInTheDocument()
        expect(within(accountAttentionCard).getByText('停用 1 个 · 从未登录 1 个')).toBeInTheDocument()
      })
    })

    it('shows quota attention count', async () => {
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          { ...mockUsers[0], quota_bytes: 1000, used_bytes: 100 },
          { ...mockUsers[1], quota_bytes: 1000, used_bytes: 900 },
          { ...mockUsers[2], quota_bytes: 1000, used_bytes: 1100 },
        ],
        total: 3,
      })

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getAllByText('配额关注').length).toBeGreaterThan(0)
        expect(screen.getByText('2 个用户接近或超过上限')).toBeInTheDocument()
      })
    })

    it('shows aggregate quota usage for limited users', async () => {
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          { ...mockUsers[0], quota_bytes: 1024, used_bytes: 1024 },
          { ...mockUsers[1], quota_bytes: 1024, used_bytes: 1024 },
          { ...mockUsers[2], quota_bytes: 0, used_bytes: 2048 },
        ],
        total: 3,
      })

      renderUsersPage()

      const overview = await screen.findByLabelText('用户配额总览')
      expect(within(overview).getByText('用户配额总览')).toBeInTheDocument()
      expect(within(overview).getByText('总体接近上限')).toBeInTheDocument()
      expect(within(overview).getByText('受限用户合计剩余 0 B。')).toBeInTheDocument()
      expect(within(overview).getByText('2 KB / 2 KB')).toBeInTheDocument()
      expect(within(overview).getAllByText('2 个').length).toBeGreaterThanOrEqual(2)
      expect(within(overview).getByText('1 个')).toBeInTheDocument()
      expect(within(overview).getByRole('progressbar', { name: '用户总配额使用率' })).toHaveAttribute(
        'aria-valuetext',
        '100% 已用，受限用户合计剩余 0 B。',
      )
    })

    it('shows recent quota trend snapshots for the current browser', async () => {
      window.localStorage.setItem('mnemonas:user-quota-trend:user-1', JSON.stringify([
        {
          capturedAt: '2024-01-01T00:00:00Z',
          totalCount: 3,
          activeCount: 2,
          limitedCount: 2,
          warningCount: 0,
          exceededCount: 0,
          attentionCount: 0,
          usedBytes: 512,
          limitedUsedBytes: 512,
          quotaBytes: 2048,
        },
      ]))
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          { ...mockUsers[0], quota_bytes: 1024, used_bytes: 1024 },
          { ...mockUsers[1], quota_bytes: 1024, used_bytes: 512 },
          { ...mockUsers[2], quota_bytes: 0, used_bytes: 2048 },
        ],
        total: 3,
      })

      renderUsersPage()

      const trend = await screen.findByLabelText('用户配额趋势')
      await waitFor(() => {
        expect(within(trend).getByText('受限用量增加')).toBeInTheDocument()
        expect(within(trend).getByText('较上一快照 +1 KB；复核用户 +1 个。')).toBeInTheDocument()
        expect(within(trend).getByText('2 次')).toBeInTheDocument()
        expect(within(trend).getByText('1.5 KB / 2 KB')).toBeInTheDocument()
        expect(within(trend).getByText('+1 KB')).toBeInTheDocument()
      })
      expect(JSON.parse(window.localStorage.getItem('mnemonas:user-quota-trend:user-1') ?? '[]')).toHaveLength(2)
    })

    it('prefers server-side quota trend history when available', async () => {
      window.localStorage.setItem('mnemonas:user-quota-trend:user-1', JSON.stringify([
        {
          capturedAt: '2024-01-01T00:00:00Z',
          totalCount: 3,
          activeCount: 2,
          limitedCount: 1,
          warningCount: 0,
          exceededCount: 0,
          attentionCount: 0,
          usedBytes: 1024,
          limitedUsedBytes: 1024,
          quotaBytes: 4096,
        },
      ]))
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: mockUsers,
        total: mockUsers.length,
        quota_history_available: true,
        quota_history: [
          {
            capturedAt: '2024-01-03T00:00:00Z',
            totalCount: 3,
            activeCount: 2,
            limitedCount: 2,
            warningCount: 0,
            exceededCount: 0,
            attentionCount: 0,
            usedBytes: 6144,
            limitedUsedBytes: 4096,
            quotaBytes: 8192,
          },
          {
            capturedAt: '2024-01-02T00:00:00Z',
            totalCount: 3,
            activeCount: 2,
            limitedCount: 2,
            warningCount: 0,
            exceededCount: 0,
            attentionCount: 0,
            usedBytes: 4096,
            limitedUsedBytes: 2048,
            quotaBytes: 8192,
          },
          ...Array.from({ length: 8 }, (_, index) => ({
            capturedAt: `2023-12-${String(24 - index).padStart(2, '0')}T00:00:00Z`,
            totalCount: 3,
            activeCount: 2,
            limitedCount: 2,
            warningCount: 0,
            exceededCount: 0,
            attentionCount: 0,
            usedBytes: 3072,
            limitedUsedBytes: 1024,
            quotaBytes: 8192,
          })),
        ],
      })

      renderUsersPage()

      const trend = await screen.findByLabelText('用户配额趋势')
      expect(within(trend).getByText('服务端历史')).toBeInTheDocument()
      expect(within(trend).getByText('受限用量增加')).toBeInTheDocument()
      expect(within(trend).getByText('4 KB / 8 KB')).toBeInTheDocument()
      expect(within(trend).getByText('+2 KB')).toBeInTheDocument()
      expect(within(trend).getByText('10 次')).toBeInTheDocument()
      expect(JSON.parse(window.localStorage.getItem('mnemonas:user-quota-trend:user-1') ?? '[]')).toHaveLength(1)
    })

    it('shows quota and permission context in one review view', async () => {
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          { ...mockUsers[0], quota_bytes: 0, used_bytes: 2048 },
          { ...mockUsers[1], username: 'alice', groups: ['family'], quota_bytes: 1000, used_bytes: 950 },
          { ...mockUsers[2], username: 'archived', disabled: true, quota_bytes: 1000, used_bytes: 512 },
        ],
        total: 3,
      })

      const user = userEvent.setup()
      renderUsersPage()

      const review = await screen.findByLabelText('用户配额权限复核')
      expect(within(review).getByText('配额与权限联合复核')).toBeInTheDocument()
      expect(within(review).getByText('需要联合复核')).toBeInTheDocument()
      expect(review).toHaveTextContent('3 个用户需要结合配额、主目录和授权范围复核。')
      expect(review).toHaveTextContent(/配额关注\s*1 个/)
      expect(review).toHaveTextContent(/共享\/全局范围\s*1 个/)
      expect(review).toHaveTextContent(/不限额特权\s*1 个/)
      expect(review).toHaveTextContent(/停用占用\s*1 个/)
      expect(within(review).getByText('用户 alice')).toBeInTheDocument()
      expect(within(review).getByText('范围：主目录 + 用户组范围')).toBeInTheDocument()
      expect(within(review).getByText('配额接近上限且具备共享或全局访问范围')).toBeInTheDocument()
      expect(within(review).getByText('复核近期增长是否来自共享路径，再决定扩容或归档。')).toBeInTheDocument()
      expect(within(review).getByText('用户 admin')).toBeInTheDocument()
      expect(within(review).getByText('范围：管理员全局范围')).toBeInTheDocument()
      expect(within(review).getByText('管理员未设置容量上限')).toBeInTheDocument()
      expect(within(review).getByText('用户 archived')).toBeInTheDocument()
      expect(within(review).getByText('停用账号仍占用容量')).toBeInTheDocument()

      await user.click(within(review).getByRole('button', { name: '查看配额关注' }))
      expect(screen.getByText('配额关注 1 / 3 个用户')).toBeInTheDocument()
    })

    it('shows review hint count breakdown', async () => {
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [
          { ...mockUsers[0], role: 'user', disabled: false, last_login_at: '2024-01-15T10:00:00Z', quota_bytes: 1000, used_bytes: 100 },
          { ...mockUsers[1], role: 'user', disabled: false, last_login_at: undefined, quota_bytes: 1000, used_bytes: 900 },
          { ...mockUsers[2], role: 'guest', disabled: true, last_login_at: '2024-01-16T10:00:00Z', quota_bytes: 1000, used_bytes: 1200 },
          { ...mockUsers[0], id: 'user-admin-review', username: 'adminreview', role: 'admin', disabled: false, last_login_at: '2024-01-17T10:00:00Z', quota_bytes: 0, used_bytes: 0 },
        ],
        total: 4,
      })

      renderUsersPage()

      await waitFor(() => {
        const reviewCard = screen.getByRole('group', { name: '复核提示，3，严重 1 个 · 提醒 1 个 · 记录 1 个' })
        expect(within(reviewCard).getByText('复核提示')).toBeInTheDocument()
        expect(within(reviewCard).getByText('3')).toBeInTheDocument()
        expect(within(reviewCard).getByText('严重 1 个 · 提醒 1 个 · 记录 1 个')).toBeInTheDocument()
      })
    })

    it('derives total users from the list when the summary field is missing', async () => {
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: mockUsers,
        total: undefined as unknown as number,
      })

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('3')).toBeInTheDocument()
      })
    })
  })

  describe('create user modal', () => {
    it('opens create modal on button click', async () => {
      const user = userEvent.setup()
      renderUsersPage()
      
      await user.click(screen.getByRole('button', { name: /添加用户/i }))
      
      await waitFor(() => {
        // Use getAllByText for labels that may appear multiple times
        expect(screen.getAllByText('用户名').length).toBeGreaterThan(0)
        expect(screen.getAllByText('密码').length).toBeGreaterThan(0)
        expect(screen.getAllByText(/邮箱/).length).toBeGreaterThan(0)
      })
    })

    it('closes and resets the create modal when cancellation is allowed', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await user.click(screen.getByRole('button', { name: /添加用户/i }))

      const usernameInput = await screen.findByLabelText(/用户名/i)
      const passwordInput = screen.getByLabelText(/密码/i)
      const emailInput = screen.getByLabelText(/邮箱/i)

      fireEvent.change(usernameInput, { target: { value: 'draftuser' } })
      fireEvent.change(passwordInput, { target: { value: 'password123' } })
      fireEvent.change(emailInput, { target: { value: 'draft@example.com' } })
      await user.click(screen.getByRole('button', { name: '取消' }))

      await waitFor(() => {
        expect(screen.queryByLabelText(/用户名/i)).not.toBeInTheDocument()
      })

      await user.click(screen.getByRole('button', { name: /添加用户/i }))

      expect(await screen.findByLabelText(/用户名/i)).toHaveValue('')
      expect(screen.getByLabelText(/密码/i)).toHaveValue('')
      expect(screen.getByLabelText(/邮箱/i)).toHaveValue('')
    })

    it('submits create form', async () => {
      vi.mocked(usersApi.createUser).mockResolvedValue({
        success: true,
        user: {
          id: 'new-user',
          username: 'newuser',
          email: 'new@example.com',
          role: 'user',
          disabled: false,
          home_dir: '/home/newuser',
          created_at: '2024-01-20T00:00:00Z',
          updated_at: '2024-01-20T00:00:00Z',
          quota_bytes: 0,
          used_bytes: 0,
        },
      })

      const user = userEvent.setup()
      renderUsersPage()
      
      await user.click(screen.getByRole('button', { name: /添加用户/i }))
      
      const usernameInput = await screen.findByLabelText(/用户名/i)
      const passwordInput = screen.getByLabelText(/密码/i)
      const emailInput = screen.getByLabelText(/邮箱/i)

      fireEvent.change(usernameInput, { target: { value: 'newuser' } })
      fireEvent.change(passwordInput, { target: { value: 'password123' } })
      fireEvent.change(emailInput, { target: { value: 'new@example.com' } })
      
      await user.click(screen.getByRole('button', { name: '创建' }))
      
      await waitFor(() => {
        expect(usersApi.createUser).toHaveBeenCalled()
        // Verify the first argument of the call contains expected data
        const callArgs = vi.mocked(usersApi.createUser).mock.calls[0][0]
        expect(callArgs.username).toBe('newuser')
        expect(callArgs.password).toBe('password123')
        expect(callArgs.email).toBe('new@example.com')
      })
    })

    it('submits create form with home directory and quota', async () => {
      vi.mocked(usersApi.createUser).mockResolvedValue({
        success: true,
        user: {
          id: 'new-user',
          username: 'newuser',
          email: 'new@example.com',
          role: 'user',
          disabled: false,
          home_dir: '/team/newuser',
          created_at: '2024-01-20T00:00:00Z',
          updated_at: '2024-01-20T00:00:00Z',
          quota_bytes: 2147483648,
          used_bytes: 0,
        },
      })

      const user = userEvent.setup()
      renderUsersPage()

      await user.click(screen.getByRole('button', { name: /添加用户/i }))

      fireEvent.change(await screen.findByLabelText(/用户名/i), { target: { value: 'newuser' } })
      fireEvent.change(screen.getByLabelText(/密码/i), { target: { value: 'password123' } })
      fireEvent.change(screen.getByLabelText('邮箱'), { target: { value: 'new@example.com' } })
      fireEvent.change(screen.getByLabelText('主目录'), { target: { value: ' /team/newuser ' } })
      fireEvent.change(screen.getByLabelText('容量配额'), { target: { value: '2' } })

      await user.click(screen.getByRole('button', { name: '创建' }))

      await waitFor(() => {
        expect(usersApi.createUser).toHaveBeenCalledWith({
          username: 'newuser',
          password: 'password123',
          email: 'new@example.com',
          role: 'user',
          groups: [],
          home_dir: '/team/newuser',
          quota_bytes: 2147483648,
        }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
      })
    })

    it('disables create button for non-admin root home directory', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await user.click(screen.getByRole('button', { name: /添加用户/i }))

      fireEvent.change(await screen.findByLabelText(/用户名/i), { target: { value: 'rootuser' } })
      fireEvent.change(screen.getByLabelText(/密码/i), { target: { value: 'password123' } })
      fireEvent.change(screen.getByLabelText('主目录'), { target: { value: '/' } })

      expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
      expect(usersApi.createUser).not.toHaveBeenCalled()
    })

    it('disables create button for malformed home directory path segments', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await user.click(screen.getByRole('button', { name: /添加用户/i }))

      fireEvent.change(await screen.findByLabelText(/用户名/i), { target: { value: 'dotuser' } })
      fireEvent.change(screen.getByLabelText(/密码/i), { target: { value: 'password123' } })
      fireEvent.change(screen.getByLabelText('主目录'), { target: { value: '/team/./dotuser' } })

      expect(screen.getByText('主目录不能包含空字符、. 或 .. 路径段。')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
      expect(usersApi.createUser).not.toHaveBeenCalled()
    })

    it('disables create button when form is incomplete', async () => {
      const user = userEvent.setup()
      renderUsersPage()
      
      await user.click(screen.getByRole('button', { name: /添加用户/i }))
      
      await waitFor(() => {
        const createButton = screen.getByRole('button', { name: '创建' })
        expect(createButton).toBeDisabled()
      })
    })

    it('keeps the create modal open while a pending create request is in flight', async () => {
      const user = userEvent.setup()
      const pendingCreate = createDeferred<{ success: boolean }>()
      vi.mocked(usersApi.createUser).mockImplementationOnce(() => pendingCreate.promise)

      renderUsersPage()

      await user.click(screen.getByRole('button', { name: /添加用户/i }))
      fireEvent.change(screen.getByLabelText(/用户名/i), { target: { value: 'alice' } })
      fireEvent.change(screen.getByLabelText(/密码/i), { target: { value: 'password123' } })
      fireEvent.change(screen.getByLabelText(/邮箱/i), { target: { value: 'alice@example.com' } })
      await user.click(screen.getByRole('button', { name: '创建' }))

      await waitFor(() => {
        expect(vi.mocked(usersApi.createUser).mock.calls[0]?.[0]).toMatchObject({
          username: 'alice',
          password: 'password123',
          email: 'alice@example.com',
        })
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      expect(screen.getByRole('heading', { name: '添加用户' })).toBeInTheDocument()
      expect(screen.getByLabelText(/用户名/i)).toHaveValue('alice')
      expect(screen.getByLabelText(/邮箱/i)).toHaveValue('alice@example.com')

      await act(async () => {
        pendingCreate.resolve({ success: true })
      })

      await waitFor(() => {
        expect(screen.queryByLabelText(/用户名/i)).not.toBeInTheDocument()
      })
    })

    it('keeps the create form open when a pending create request later fails', async () => {
      const user = userEvent.setup()
      const pendingCreate = createDeferred<{ success: boolean }>()
      vi.mocked(usersApi.createUser).mockImplementationOnce(() => pendingCreate.promise)

      renderUsersPage()

      await user.click(screen.getByRole('button', { name: /添加用户/i }))
      fireEvent.change(screen.getByLabelText(/用户名/i), { target: { value: 'alice' } })
      fireEvent.change(screen.getByLabelText(/密码/i), { target: { value: 'password123' } })
      fireEvent.change(screen.getByLabelText(/邮箱/i), { target: { value: 'alice@example.com' } })
      await user.click(screen.getByRole('button', { name: '创建' }))

      await waitFor(() => {
        expect(vi.mocked(usersApi.createUser).mock.calls[0]?.[0]).toMatchObject({
          username: 'alice',
          password: 'password123',
          email: 'alice@example.com',
        })
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      expect(screen.getByRole('button', { name: /创建/ })).toBeInTheDocument()
      expect(screen.getByLabelText(/用户名/i)).toHaveValue('alice')
      expect(screen.getByLabelText(/邮箱/i)).toHaveValue('alice@example.com')

      await act(async () => {
        pendingCreate.reject(new Error('create failed'))
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '创建失败',
          description: '操作未完成，请稍后重试。',
          color: 'danger',
        })
      })

      expect(screen.getByRole('button', { name: /创建/ })).toBeInTheDocument()
      expect(screen.getByLabelText(/用户名/i)).toHaveValue('alice')
    })

    it('keeps newer create form edits when an older create request resolves', async () => {
      const user = userEvent.setup()
      const pendingCreate = createDeferred<{ success: boolean }>()
      vi.mocked(usersApi.createUser).mockImplementationOnce(() => pendingCreate.promise)

      renderUsersPage()

      await user.click(screen.getByRole('button', { name: /添加用户/i }))
      fireEvent.change(screen.getByLabelText(/用户名/i), { target: { value: 'alice' } })
      fireEvent.change(screen.getByLabelText(/密码/i), { target: { value: 'password123' } })
      fireEvent.change(screen.getByLabelText(/邮箱/i), { target: { value: 'alice@example.com' } })
      await user.click(screen.getByRole('button', { name: '创建' }))

      await waitFor(() => {
        expect(vi.mocked(usersApi.createUser).mock.calls[0]?.[0]).toMatchObject({
          username: 'alice',
          password: 'password123',
          email: 'alice@example.com',
        })
      })

      fireEvent.change(screen.getByLabelText(/用户名/i), { target: { value: 'bob' } })
      fireEvent.change(screen.getByLabelText(/邮箱/i), { target: { value: 'bob@example.com' } })

      await act(async () => {
        pendingCreate.resolve({ success: true })
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '用户创建成功', color: 'success' })
      })

      expect(screen.getByRole('heading', { name: '添加用户' })).toBeInTheDocument()
      expect(screen.getByLabelText(/用户名/i)).toHaveValue('bob')
      expect(screen.getByLabelText(/邮箱/i)).toHaveValue('bob@example.com')
    })

    it('shows a specific warning when the username already exists', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.createUser).mockRejectedValueOnce(new UsersError('user already exists', 409, 'USER_EXISTS'))

      renderUsersPage()

      await user.click(screen.getByRole('button', { name: /添加用户/i }))
      fireEvent.change(screen.getByLabelText(/用户名/i), { target: { value: 'admin' } })
      fireEvent.change(screen.getByLabelText(/密码/i), { target: { value: 'password123' } })
      await user.click(screen.getByRole('button', { name: '创建' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '用户名已存在',
          description: '该用户名已被占用，请使用其他用户名。',
          color: 'warning',
        })
      })

      expect(screen.getByRole('button', { name: /创建/ })).toBeInTheDocument()
    })

    it('shows a localized warning when create succeeds with a persistence warning', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.createUser).mockResolvedValueOnce({
        success: true,
        warning: true,
        message: 'user created with persistence warning',
        user: {
          id: 'new-user',
          username: 'newuser',
          email: 'new@example.com',
          role: 'user',
          disabled: false,
          home_dir: '/home/newuser',
          created_at: '2024-01-20T00:00:00Z',
          updated_at: '2024-01-20T00:00:00Z',
          quota_bytes: 0,
          used_bytes: 0,
        },
      })

      renderUsersPage()

      await user.click(screen.getByRole('button', { name: /添加用户/i }))
      fireEvent.change(screen.getByLabelText(/用户名/i), { target: { value: 'newuser' } })
      fireEvent.change(screen.getByLabelText(/密码/i), { target: { value: 'password123' } })
      await user.click(screen.getByRole('button', { name: '创建' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '用户已创建，但保存存在提醒',
          description: '操作已提交，但用户配置保存存在提醒，请检查设备状态。',
          color: 'warning',
        })
      })
    })
  })

  describe('edit user modal', () => {
    it('submits edited metadata, groups, home directory, and quota', async () => {
      vi.mocked(usersApi.updateUser).mockResolvedValueOnce({
        success: true,
        warning: false,
        message: 'user updated successfully',
        user: {
          ...mockUsers[1],
          email: 'editor@example.com',
          groups: ['editors', 'family'],
          home_dir: '/team/editors',
          quota_bytes: 2147483648,
        },
      })
      const user = userEvent.setup()
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '编辑用户')

      await waitFor(() => {
        expect(screen.getByRole('heading', { name: '编辑用户' })).toBeInTheDocument()
      })

      fireEvent.change(screen.getByLabelText('邮箱'), { target: { value: ' editor@example.com ' } })
      fireEvent.change(screen.getByLabelText('用户组'), { target: { value: 'Family editors family' } })
      fireEvent.change(screen.getByLabelText('主目录'), { target: { value: ' /team/editors ' } })
      fireEvent.change(screen.getByLabelText('容量配额'), { target: { value: '2' } })
      await user.click(screen.getByRole('button', { name: '保存' }))

      await waitFor(() => {
        expect(usersApi.updateUser).toHaveBeenCalledWith('user-2', {
          email: 'editor@example.com',
          role: 'user',
          groups: ['editors', 'family'],
          home_dir: '/team/editors',
          quota_bytes: 2147483648,
        }, expect.objectContaining({ signal: expect.any(AbortSignal) }))
        expect(mockAddToast).toHaveBeenCalledWith({ title: '用户已更新', color: 'success' })
      })
    })

    it('disables update button for non-admin root home directory', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '编辑用户')

      await waitFor(() => {
        expect(screen.getByRole('heading', { name: '编辑用户' })).toBeInTheDocument()
      })

      fireEvent.change(screen.getByLabelText('主目录'), { target: { value: '/' } })

      expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
      expect(usersApi.updateUser).not.toHaveBeenCalled()
    })

    it('disables update button for malformed home directory path segments', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '编辑用户')

      await waitFor(() => {
        expect(screen.getByRole('heading', { name: '编辑用户' })).toBeInTheDocument()
      })

      fireEvent.change(screen.getByLabelText('主目录'), { target: { value: '/team/./editors' } })

      expect(screen.getByText('主目录不能包含空字符、. 或 .. 路径段。')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
      expect(usersApi.updateUser).not.toHaveBeenCalled()
    })

    it('rejects invalid group names before updating a user', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '编辑用户')

      await waitFor(() => {
        expect(screen.getByRole('heading', { name: '编辑用户' })).toBeInTheDocument()
      })

      fireEvent.change(screen.getByLabelText('用户组'), { target: { value: 'family/team' } })
      await user.click(screen.getByRole('button', { name: '保存' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '用户组无效',
          description: '用户组只能包含字母、数字、点、短横线和下划线。',
          color: 'warning',
        })
      })
      expect(usersApi.updateUser).not.toHaveBeenCalled()
    })
  })

  describe('delete user', () => {
    it('shows delete confirmation modal', async () => {
      const user = userEvent.setup()
      renderUsersPage()
      
      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await openUserActionMenu(user, 'testuser')

      await waitFor(() => {
        expect(screen.getByRole('menuitem', { name: '删除用户' })).toBeInTheDocument()
      })
    })

    it('closes the delete confirmation modal when cancellation is allowed', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '删除用户')

      await waitFor(() => {
        expect(screen.getByRole('heading', { name: '确认删除' })).toBeInTheDocument()
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      await waitFor(() => {
        expect(screen.queryByRole('heading', { name: '确认删除' })).not.toBeInTheDocument()
      })
    })

    it('deletes a user after confirmation', async () => {
      vi.mocked(usersApi.deleteUser).mockResolvedValue({ success: true })
      const user = userEvent.setup()

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '删除用户')
      await user.click(screen.getByRole('button', { name: '删除' }))

      await waitFor(() => {
        expect(vi.mocked(usersApi.deleteUser).mock.calls[0]?.[0]).toBe('user-2')
        expect(mockAddToast).toHaveBeenCalledWith({ title: '用户已删除', color: 'success' })
      })
    })

    it('calls toggle status API when disabling a user', async () => {
      vi.mocked(usersApi.toggleUserStatus).mockResolvedValue({ success: true })
      const user = userEvent.setup()
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '禁用用户')

      await waitFor(() => {
        expect(usersApi.toggleUserStatus).toHaveBeenCalledWith('user-2', true, expect.objectContaining({ signal: expect.any(AbortSignal) }))
      })
    })

    it('enables a disabled user and shows success feedback', async () => {
      vi.mocked(usersApi.toggleUserStatus).mockResolvedValue({ success: true })
      const user = userEvent.setup()
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('guest')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'guest', '启用用户')

      await waitFor(() => {
        expect(usersApi.toggleUserStatus).toHaveBeenCalledWith('user-3', false, expect.objectContaining({ signal: expect.any(AbortSignal) }))
        expect(mockAddToast).toHaveBeenCalledWith({ title: '用户已启用', color: 'success' })
      })
    })

    it('revokes another user sessions from the action menu', async () => {
      vi.mocked(usersApi.revokeUserSessions).mockResolvedValue({ success: true })
      const user = userEvent.setup()
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '让现有登录失效')

      await waitFor(() => {
        expect(usersApi.revokeUserSessions).toHaveBeenCalledWith('user-2', expect.objectContaining({ signal: expect.any(AbortSignal) }))
        expect(mockAddToast).toHaveBeenCalledWith({ title: '现有登录已失效', color: 'success' })
      })
    })

    it('removes a stale user when session revoke hits not found', async () => {
      vi.mocked(usersApi.revokeUserSessions).mockRejectedValueOnce(new UsersError('user not found', 404, 'USER_NOT_FOUND'))
      const user = userEvent.setup()
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
        expect(screen.getByText('guest')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '让现有登录失效')

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '用户已不存在，已同步更新', color: 'warning' })
      })

      expect(screen.queryByText('testuser')).not.toBeInTheDocument()
      expect(screen.getByText('guest')).toBeInTheDocument()
    })

    it('shows a localized warning when session revoke succeeds with a persistence warning', async () => {
      vi.mocked(usersApi.revokeUserSessions).mockResolvedValueOnce({
        success: true,
        warning: true,
        message: 'user sessions revoked with persistence warning',
      })
      const user = userEvent.setup()
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '让现有登录失效')

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '登录已失效，但保存存在提醒',
          description: '操作已提交，但用户配置保存存在提醒，请检查设备状态。',
          color: 'warning',
        })
      })
    })

    it('exposes accessible user action menu labels', async () => {
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByRole('button', { name: 'admin 用户操作' })).toBeInTheDocument()
        expect(screen.getByRole('button', { name: 'testuser 用户操作' })).toBeInTheDocument()
      })
    })

    it('keeps the delete modal open when a pending delete request later fails', async () => {
      const user = userEvent.setup()
      const pendingDelete = createDeferred<{ success: boolean }>()
      vi.mocked(usersApi.deleteUser).mockImplementationOnce(() => pendingDelete.promise)

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '删除用户')

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '删除' })).toBeInTheDocument()
      })

      await user.click(screen.getByRole('button', { name: '删除' }))

      await waitFor(() => {
        expect(vi.mocked(usersApi.deleteUser).mock.calls[0]?.[0]).toBe('user-2')
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      expect(screen.getByRole('heading', { name: '确认删除' })).toBeInTheDocument()
      expect(screen.getAllByText('testuser').length).toBeGreaterThan(0)

      await act(async () => {
        pendingDelete.reject(new Error('delete failed'))
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '删除失败',
          description: '操作未完成，请稍后重试。',
          color: 'danger',
        })
      })

      expect(screen.getByRole('heading', { name: '确认删除' })).toBeInTheDocument()
    })

    it('keeps the delete modal open while a pending delete is in flight', async () => {
      const user = userEvent.setup()
      const pendingDelete = createDeferred<{ success: boolean }>()
      vi.mocked(usersApi.deleteUser).mockImplementationOnce(() => pendingDelete.promise)

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
        expect(screen.getByText('guest')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '删除用户')

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '删除' })).toBeInTheDocument()
      })

      await user.click(screen.getByRole('button', { name: '删除' }))

      await waitFor(() => {
        expect(vi.mocked(usersApi.deleteUser).mock.calls[0]?.[0]).toBe('user-2')
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      await waitFor(() => {
        expect(screen.getByRole('heading', { name: '确认删除' })).toBeInTheDocument()
        expect(screen.getAllByText('testuser').length).toBeGreaterThan(0)
      })

      await act(async () => {
        pendingDelete.resolve({ success: true })
      })

      await waitFor(() => {
        expect(screen.queryByText('确认删除')).not.toBeInTheDocument()
      })
    })

    it('closes the delete modal and removes a stale user when delete hits not found', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.deleteUser).mockRejectedValueOnce(new UsersError('user not found', 404, 'USER_NOT_FOUND'))

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
        expect(screen.getByText('guest')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '删除用户')

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '删除' })).toBeInTheDocument()
      })

      await user.click(screen.getByRole('button', { name: '删除' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '用户已不存在，已同步更新', color: 'warning' })
      })

      await waitFor(() => {
        expect(screen.queryByRole('heading', { name: '确认删除' })).not.toBeInTheDocument()
        expect(screen.queryByText('testuser')).not.toBeInTheDocument()
        expect(screen.getByText('guest')).toBeInTheDocument()
      })
    })

    it('shows a localized warning when delete succeeds with a persistence warning', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.deleteUser).mockResolvedValueOnce({
        success: true,
        warning: true,
        message: 'user deleted with persistence warning',
      })

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '删除用户')
      await user.click(screen.getByRole('button', { name: '删除' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '用户已删除，但保存存在提醒',
          description: '操作已提交，但用户配置保存存在提醒，请检查设备状态。',
          color: 'warning',
        })
      })
    })
  })

  describe('reset password', () => {
    it('shows reset password modal', async () => {
      const user = userEvent.setup()
      renderUsersPage()
      
      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await openUserActionMenu(user, 'testuser')

      await waitFor(() => {
        expect(screen.getByRole('menuitem', { name: '重置密码' })).toBeInTheDocument()
      })
    })

    it('closes and clears the reset password modal when cancellation is allowed', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '重置密码')

      await waitFor(() => {
        expect(screen.getByLabelText('新密码')).toBeInTheDocument()
        expect(screen.getByText(/本操作不能代替账号本人完成改密/)).toBeInTheDocument()
      })

      await user.type(screen.getByLabelText('新密码'), 'password123')
      await user.click(screen.getByRole('button', { name: '取消' }))

      await waitFor(() => {
        expect(screen.queryByLabelText('新密码')).not.toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '重置密码')

      await waitFor(() => {
        expect(screen.getByLabelText('新密码')).toHaveValue('')
      })
    })

    it('resets a password successfully', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.resetUserPassword).mockResolvedValueOnce({ success: true })

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '重置密码')

      await user.type(screen.getByLabelText('新密码'), 'password123')
      await user.click(screen.getByRole('button', { name: '确认重置' }))

      await waitFor(() => {
        expect(usersApi.resetUserPassword).toHaveBeenCalledWith(
          'user-2',
          { new_password: 'password123' },
          expect.objectContaining({ signal: expect.any(AbortSignal) })
        )
        expect(mockAddToast).toHaveBeenCalledWith({ title: '密码已重置', color: 'success' })
      })
    })

    it('rejects a reset password containing only whitespace', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '重置密码')
      fireEvent.change(screen.getByLabelText('新密码'), { target: { value: '        ' } })

      expect(screen.getByRole('button', { name: '确认重置' })).toBeDisabled()
      expect(usersApi.resetUserPassword).not.toHaveBeenCalled()
    })

    it('accepts a multibyte reset password within the UTF-8 byte limits', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '重置密码')
      fireEvent.change(screen.getByLabelText('新密码'), { target: { value: '密码密码密码' } })

      expect(screen.getByRole('button', { name: '确认重置' })).toBeEnabled()
    })

    it('keeps the reset password modal open when a pending reset later fails', async () => {
      const user = userEvent.setup()
      const pendingReset = createDeferred<{ success: boolean }>()
      vi.mocked(usersApi.resetUserPassword).mockImplementationOnce(() => pendingReset.promise)

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '重置密码')

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '确认重置' })).toBeInTheDocument()
      })

      await user.type(screen.getByLabelText('新密码'), 'password123')
      await user.click(screen.getByRole('button', { name: '确认重置' }))

      await waitFor(() => {
        expect(vi.mocked(usersApi.resetUserPassword).mock.calls[0]).toEqual([
          'user-2',
          { new_password: 'password123' },
          expect.objectContaining({ signal: expect.any(AbortSignal) }),
        ])
      })

      await user.click(screen.getByRole('button', { name: '取消' }))

      expect(screen.getByRole('button', { name: /确认重置/ })).toBeInTheDocument()
      expect(screen.getByLabelText('新密码')).toHaveValue('password123')

      await act(async () => {
        pendingReset.reject(new Error('reset failed'))
      })

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '重置失败',
          description: '操作未完成，请稍后重试。',
          color: 'danger',
        })
      })

      expect(screen.getByRole('button', { name: /确认重置/ })).toBeInTheDocument()
      expect(screen.getByLabelText('新密码')).toHaveValue('password123')
    })

    it('closes the reset modal and removes a stale user when reset hits not found', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.resetUserPassword).mockRejectedValueOnce(new UsersError('user not found', 404, 'USER_NOT_FOUND'))

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
        expect(screen.getByText('guest')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '重置密码')

      await waitFor(() => {
        expect(screen.getByRole('button', { name: '确认重置' })).toBeInTheDocument()
      })

      await user.type(screen.getByLabelText('新密码'), 'password123')
      await user.click(screen.getByRole('button', { name: '确认重置' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '用户已不存在，已同步更新', color: 'warning' })
      })

      await waitFor(() => {
        expect(screen.queryByRole('button', { name: '确认重置' })).not.toBeInTheDocument()
        expect(screen.queryByText('testuser')).not.toBeInTheDocument()
        expect(screen.getByText('guest')).toBeInTheDocument()
      })
    })

    it('shows a localized warning when reset succeeds with a persistence warning', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.resetUserPassword).mockResolvedValueOnce({
        success: true,
        warning: true,
        message: 'password reset with persistence warning',
      })

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '重置密码')
      await user.type(screen.getByLabelText('新密码'), 'password123')
      await user.click(screen.getByRole('button', { name: '确认重置' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '密码已重置，但保存存在提醒',
          description: '操作已提交，但用户配置保存存在提醒，请检查设备状态。',
          color: 'warning',
        })
      })
    })
  })

  describe('request cancellation', () => {
    it('aborts a pending create request when the page unmounts', async () => {
      const user = userEvent.setup()
      const pendingCreate = createDeferred<Awaited<ReturnType<typeof usersApi.createUser>>>()
      let signal: AbortSignal | undefined
      vi.mocked(usersApi.createUser).mockImplementationOnce((_request, options) => {
        signal = options?.signal
        return pendingCreate.promise
      })

      const { unmount } = renderUsersPage()

      await user.click(screen.getByRole('button', { name: /添加用户/i }))
      fireEvent.change(screen.getByLabelText(/用户名/i), { target: { value: 'alice' } })
      fireEvent.change(screen.getByLabelText(/密码/i), { target: { value: 'password123' } })
      await user.click(screen.getByRole('button', { name: '创建' }))

      await waitFor(() => {
        expectAbortSignal(signal)
      })
      expect(signal.aborted).toBe(false)

      unmount()

      expect(signal.aborted).toBe(true)
    })

    it('aborts a pending update request when the page unmounts', async () => {
      const user = userEvent.setup()
      const pendingUpdate = createDeferred<Awaited<ReturnType<typeof usersApi.updateUser>>>()
      let signal: AbortSignal | undefined
      vi.mocked(usersApi.updateUser).mockImplementationOnce((_userId, _request, options) => {
        signal = options?.signal
        return pendingUpdate.promise
      })

      const { unmount } = renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '编辑用户')
      await user.click(screen.getByRole('button', { name: '保存' }))

      await waitFor(() => {
        expectAbortSignal(signal)
      })
      expect(signal.aborted).toBe(false)

      unmount()

      expect(signal.aborted).toBe(true)
    })

    it('aborts a pending delete request when the page unmounts', async () => {
      const user = userEvent.setup()
      const pendingDelete = createDeferred<Awaited<ReturnType<typeof usersApi.deleteUser>>>()
      let signal: AbortSignal | undefined
      vi.mocked(usersApi.deleteUser).mockImplementationOnce((_userId, options) => {
        signal = options?.signal
        return pendingDelete.promise
      })

      const { unmount } = renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '删除用户')
      await user.click(screen.getByRole('button', { name: '删除' }))

      await waitFor(() => {
        expectAbortSignal(signal)
      })
      expect(signal.aborted).toBe(false)

      unmount()

      expect(signal.aborted).toBe(true)
    })

    it('aborts a pending password reset request when the page unmounts', async () => {
      const user = userEvent.setup()
      const pendingReset = createDeferred<Awaited<ReturnType<typeof usersApi.resetUserPassword>>>()
      let signal: AbortSignal | undefined
      vi.mocked(usersApi.resetUserPassword).mockImplementationOnce((_userId, _request, options) => {
        signal = options?.signal
        return pendingReset.promise
      })

      const { unmount } = renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '重置密码')
      await user.type(screen.getByLabelText('新密码'), 'password123')
      await user.click(screen.getByRole('button', { name: '确认重置' }))

      await waitFor(() => {
        expectAbortSignal(signal)
      })
      expect(signal.aborted).toBe(false)

      unmount()

      expect(signal.aborted).toBe(true)
    })

    it('aborts a pending status toggle request when the page unmounts', async () => {
      const user = userEvent.setup()
      const pendingToggle = createDeferred<Awaited<ReturnType<typeof usersApi.toggleUserStatus>>>()
      let signal: AbortSignal | undefined
      vi.mocked(usersApi.toggleUserStatus).mockImplementationOnce((_userId, _disabled, options) => {
        signal = options?.signal
        return pendingToggle.promise
      })

      const { unmount } = renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '禁用用户')

      await waitFor(() => {
        expectAbortSignal(signal)
      })
      expect(signal.aborted).toBe(false)

      unmount()

      expect(signal.aborted).toBe(true)
    })

    it('aborts a pending session revoke request when the page unmounts', async () => {
      const user = userEvent.setup()
      const pendingRevoke = createDeferred<Awaited<ReturnType<typeof usersApi.revokeUserSessions>>>()
      let signal: AbortSignal | undefined
      vi.mocked(usersApi.revokeUserSessions).mockImplementationOnce((_userId, options) => {
        signal = options?.signal
        return pendingRevoke.promise
      })

      const { unmount } = renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '让现有登录失效')

      await waitFor(() => {
        expectAbortSignal(signal)
      })
      expect(signal.aborted).toBe(false)

      unmount()

      expect(signal.aborted).toBe(true)
    })
  })

  describe('loading state', () => {
    it('shows loading state initially', () => {
      vi.mocked(usersApi.listUsers).mockImplementation(() => new Promise(() => {}))
      renderUsersPage()
      expect(screen.getByText('加载用户列表…')).toBeInTheDocument()
    })
  })

  describe('empty state', () => {
    it('shows empty state when no users', async () => {
      vi.mocked(usersApi.listUsers).mockResolvedValue({
        success: true,
        users: [],
        total: 0,
      })

      renderUsersPage()
      
      await waitFor(() => {
        expect(screen.getByText('暂无用户')).toBeInTheDocument()
      })
    })

    it('shows retryable error state when loading users fails', async () => {
      vi.mocked(usersApi.listUsers).mockRejectedValue(new Error('Network error'))

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('加载用户列表失败')).toBeInTheDocument()
        expect(screen.getByText('用户列表加载失败，请检查网络或稍后重试。')).toBeInTheDocument()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
      })
    })

    it('shows unavailable state when users configuration is unavailable', async () => {
      vi.mocked(usersApi.listUsers).mockRejectedValue(new UsersError('configuration not available', 503))

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('用户管理暂不可用')).toBeInTheDocument()
        expect(screen.getByText('用户配置当前不可用，请检查系统配置状态或稍后重试。')).toBeInTheDocument()
        expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
      })
    })

    it('shows success toast when reloading users from an error state succeeds', async () => {
    const user = userEvent.setup()
    vi.mocked(usersApi.listUsers)
      .mockRejectedValueOnce(new Error('Network error'))
      .mockResolvedValueOnce({
        users: mockUsers,
        total: mockUsers.length,
      })

    renderUsersPage()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '重新加载' }))

    await waitFor(() => {
      expect(screen.getByText('用户列表')).toBeInTheDocument()
      expect(mockAddToast).toHaveBeenCalledWith({ title: '用户列表已刷新', color: 'success' })
    })
    })

    it('shows warning toast when reloading users becomes unavailable', async () => {
    const user = userEvent.setup()
    vi.mocked(usersApi.listUsers)
      .mockRejectedValueOnce(new Error('Network error'))
      .mockRejectedValueOnce(new UsersError('configuration not available', 503))

    renderUsersPage()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '重新加载' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '用户管理暂不可用',
        description: '用户配置当前不可用，请检查系统配置状态或稍后重试。',
        color: 'warning',
      })
    })
    })

    it('shows generic failure toast when reloading users fails with an Error object', async () => {
    const user = userEvent.setup()
    vi.mocked(usersApi.listUsers)
      .mockRejectedValueOnce(new Error('Network error'))
      .mockRejectedValueOnce(new Error('backend timeout'))

    renderUsersPage()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '重新加载' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '刷新失败',
        description: '操作未完成，请稍后重试。',
        color: 'danger',
      })
    })
    })

    it('shows generic failure toast when reloading users fails without an Error object', async () => {
    const user = userEvent.setup()
    vi.mocked(usersApi.listUsers)
      .mockRejectedValueOnce(new Error('Network error'))
      .mockRejectedValueOnce('still broken')

    renderUsersPage()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: '重新加载' }))

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '刷新失败',
        description: '操作未完成，请稍后重试。',
        color: 'danger',
      })
    })
    })
  })

  describe('validation feedback', () => {
    it('disables create when the password contains only whitespace', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await user.click(screen.getByRole('button', { name: /添加用户/i }))

      await waitFor(() => {
        expect(screen.getByLabelText(/用户名/i)).toBeInTheDocument()
      })

      fireEvent.change(screen.getByLabelText(/用户名/i), { target: { value: 'newuser' } })
      fireEvent.change(screen.getByLabelText(/密码/i), { target: { value: '        ' } })

      expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
      expect(usersApi.createUser).not.toHaveBeenCalled()
    })

    it('accepts a multibyte create password within the UTF-8 byte limits', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await user.click(screen.getByRole('button', { name: /添加用户/i }))

      await waitFor(() => {
        expect(screen.getByLabelText(/用户名/i)).toBeInTheDocument()
      })

      fireEvent.change(screen.getByLabelText(/用户名/i), { target: { value: 'newuser' } })
      fireEvent.change(screen.getByLabelText(/密码/i), { target: { value: '密码密码密码' } })

      expect(screen.getByRole('button', { name: '创建' })).toBeEnabled()
    })

    it('shows warning when trying to create a user with a short password', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await user.click(screen.getByRole('button', { name: /添加用户/i }))

      await waitFor(() => {
        expect(screen.getByLabelText(/用户名/i)).toBeInTheDocument()
      })

      fireEvent.change(screen.getByLabelText(/用户名/i), { target: { value: 'newuser' } })
      fireEvent.change(screen.getByLabelText(/密码/i), { target: { value: 'short' } })

      expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
      expect(usersApi.createUser).not.toHaveBeenCalled()
    })

    it('disables create when password exceeds bcrypt byte limit', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await user.click(screen.getByRole('button', { name: /添加用户/i }))

      await waitFor(() => {
        expect(screen.getByLabelText(/用户名/i)).toBeInTheDocument()
      })

      fireEvent.change(screen.getByLabelText(/用户名/i), { target: { value: 'newuser' } })
      fireEvent.change(screen.getByLabelText(/密码/i), { target: { value: 'a'.repeat(73) } })

      expect(screen.getByRole('button', { name: '创建' })).toBeDisabled()
      expect(usersApi.createUser).not.toHaveBeenCalled()
    })

    it('shows unavailable toast when creating a user is temporarily unavailable', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.createUser).mockRejectedValue(new UsersError('configuration not available', 503))

      renderUsersPage()

      await user.click(screen.getByRole('button', { name: /添加用户/i }))

      await waitFor(() => {
        expect(screen.getByLabelText(/用户名/i)).toBeInTheDocument()
      })

      fireEvent.change(screen.getByLabelText(/用户名/i), { target: { value: 'newuser' } })
      fireEvent.change(screen.getByLabelText(/密码/i), { target: { value: 'password123' } })
      await user.click(screen.getByRole('button', { name: '创建' }))

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '创建用户暂不可用',
          description: '用户配置当前不可用，请检查系统配置状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('shows unavailable toast when toggling user status is temporarily unavailable', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.toggleUserStatus).mockRejectedValue(new UsersError('configuration not available', 503))

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '禁用用户')

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '状态更新暂不可用',
          description: '用户配置当前不可用，请检查系统配置状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('shows unavailable toast when revoking sessions is temporarily unavailable', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.revokeUserSessions).mockRejectedValue(new UsersError('configuration not available', 503))

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '让现有登录失效')

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '吊销登录暂不可用',
          description: '用户配置当前不可用，请检查系统配置状态或稍后重试。',
          color: 'warning',
        })
      })
    })

    it('shows a specific warning for SELF_DISABLE responses', async () => {
      const user = userEvent.setup()
      vi.mocked(authStore.useUser).mockReturnValue({
        id: 'another-admin',
        username: 'operator',
        role: 'admin',
        homeDir: '/',
        email: '',
        mustChangePassword: false,
      })
      vi.mocked(usersApi.toggleUserStatus).mockRejectedValueOnce(new UsersError('cannot disable self', 400, 'SELF_DISABLE'))

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('admin')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'admin', '禁用用户')

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '不能禁用当前用户',
          description: '当前登录用户不能禁用自身账号。',
          color: 'warning',
        })
      })
    })

    it('shows a specific warning for LAST_ADMIN responses', async () => {
      const user = userEvent.setup()
      vi.mocked(authStore.useUser).mockReturnValue({
        id: 'another-admin',
        username: 'operator',
        role: 'admin',
        homeDir: '/',
        email: '',
        mustChangePassword: false,
      })
      vi.mocked(usersApi.toggleUserStatus).mockRejectedValueOnce(new UsersError('last admin', 400, 'LAST_ADMIN'))

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('admin')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'admin', '禁用用户')

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '不能禁用最后一个管理员',
          description: '系统至少需要保留一个启用中的管理员账号。',
          color: 'warning',
        })
      })
    })

    it('removes a stale user when status update hits not found', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.toggleUserStatus).mockRejectedValueOnce(new UsersError('user not found', 404, 'USER_NOT_FOUND'))

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
        expect(screen.getByText('guest')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '禁用用户')

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({ title: '用户已不存在，已同步更新', color: 'warning' })
      })

      expect(screen.queryByText('testuser')).not.toBeInTheDocument()
      expect(screen.getByText('guest')).toBeInTheDocument()
    })

    it('shows a localized warning when status update succeeds with a persistence warning', async () => {
      const user = userEvent.setup()
      vi.mocked(usersApi.toggleUserStatus).mockResolvedValueOnce({
        success: true,
        warning: true,
        message: 'user status updated with persistence warning',
      })

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await clickUserActionMenuItem(user, 'testuser', '禁用用户')

      await waitFor(() => {
        expect(mockAddToast).toHaveBeenCalledWith({
          title: '用户已禁用，但保存存在提醒',
          description: '操作已提交，但用户配置保存存在提醒，请检查设备状态。',
          color: 'warning',
        })
      })
    })
  })
})
