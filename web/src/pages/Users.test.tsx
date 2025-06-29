import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import { UsersPage } from './Users'
import * as usersApi from '@/api/users'
import * as authApi from '@/api/auth'
import { UsersError } from '@/api/users'

const mockAddToast = vi.fn()

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

vi.mock('@/api/auth', () => ({
  getStoredUser: vi.fn(),
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

describe('UsersPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(authApi.getStoredUser).mockReturnValue({
      id: 'user-1',
      username: 'admin',
      role: 'admin',
      homeDir: '/',
      email: '',
    })
    vi.mocked(usersApi.listUsers).mockResolvedValue({
      success: true,
      users: mockUsers,
      total: mockUsers.length,
    })
  })

  describe('rendering', () => {
    it('renders page header', async () => {
      renderUsersPage()
      expect(screen.getByText('用户管理')).toBeInTheDocument()
      expect(screen.getByText('管理系统用户、权限和配额')).toBeInTheDocument()
    })

    it('refetches the user list when the current session changes', async () => {
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

    vi.mocked(authApi.getStoredUser).mockReturnValue({
      id: 'user-2',
      username: 'other-admin',
      role: 'admin',
      homeDir: '/',
      email: 'other@example.com',
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
        expect(screen.getByText('管理员')).toBeInTheDocument()
        expect(screen.getByText('活跃用户')).toBeInTheDocument()
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
      })
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
  })

  describe('stats', () => {
    it('shows correct total users count', async () => {
      renderUsersPage()
      await waitFor(() => {
        expect(screen.getByText('3')).toBeInTheDocument()
      })
    })

    it('shows correct admin count', async () => {
      renderUsersPage()
      await waitFor(() => {
        const statCards = screen.getAllByText('1')
        expect(statCards.length).toBeGreaterThan(0)
      })
    })

    it('shows correct active users count', async () => {
      renderUsersPage()
      await waitFor(() => {
        expect(screen.getByText('2')).toBeInTheDocument()
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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('编辑用户'))!)

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('编辑用户'))!)

      await waitFor(() => {
        expect(screen.getByRole('heading', { name: '编辑用户' })).toBeInTheDocument()
      })

      fireEvent.change(screen.getByLabelText('主目录'), { target: { value: '/' } })

      expect(screen.getByRole('button', { name: '保存' })).toBeDisabled()
      expect(usersApi.updateUser).not.toHaveBeenCalled()
    })

    it('rejects invalid group names before updating a user', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('编辑用户'))!)

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))

      await waitFor(() => {
        expect(screen.getByText('删除用户')).toBeInTheDocument()
      })
    })

    it('closes the delete confirmation modal when cancellation is allowed', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('删除用户'))!)

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('删除用户'))!)
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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))

      await waitFor(() => {
        expect(screen.getByText('禁用用户')).toBeInTheDocument()
      })

      await user.click(screen.getByText('禁用用户'))

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

      await user.click(screen.getByRole('button', { name: 'guest 用户操作' }))

      await waitFor(() => {
        expect(screen.getByText('启用用户')).toBeInTheDocument()
      })

      await user.click(screen.getByText('启用用户'))

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))

      await waitFor(() => {
        expect(screen.getByText('让现有登录失效')).toBeInTheDocument()
      })

      await user.click(screen.getByText('让现有登录失效'))

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getByText('让现有登录失效'))

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getByText('让现有登录失效'))

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('删除用户'))!)

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('删除用户'))!)

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('删除用户'))!)

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('删除用户'))!)
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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))

      await waitFor(() => {
        expect(screen.getByText('重置密码')).toBeInTheDocument()
      })
    })

    it('closes and clears the reset password modal when cancellation is allowed', async () => {
      const user = userEvent.setup()
      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('重置密码'))!)

      await waitFor(() => {
        expect(screen.getByLabelText('新密码')).toBeInTheDocument()
      })

      await user.type(screen.getByLabelText('新密码'), 'password123')
      await user.click(screen.getByRole('button', { name: '取消' }))

      await waitFor(() => {
        expect(screen.queryByLabelText('新密码')).not.toBeInTheDocument()
      })

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('重置密码'))!)

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('重置密码'))!)

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

    it('keeps the reset password modal open when a pending reset later fails', async () => {
      const user = userEvent.setup()
      const pendingReset = createDeferred<{ success: boolean }>()
      vi.mocked(usersApi.resetUserPassword).mockImplementationOnce(() => pendingReset.promise)

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('testuser')).toBeInTheDocument()
      })

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('重置密码'))!)

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('重置密码'))!)

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('重置密码'))!)
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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('编辑用户'))!)
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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('删除用户'))!)
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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getAllByRole('menuitem').find((item) => item.textContent?.includes('重置密码'))!)
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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getByText('禁用用户'))

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))
      await user.click(screen.getByText('让现有登录失效'))

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
      expect(screen.getByText('加载用户列表...')).toBeInTheDocument()
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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))

      await waitFor(() => {
        expect(screen.getByText('禁用用户')).toBeInTheDocument()
      })

      await user.click(screen.getByText('禁用用户'))

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))

      await waitFor(() => {
        expect(screen.getByText('让现有登录失效')).toBeInTheDocument()
      })

      await user.click(screen.getByText('让现有登录失效'))

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
      vi.mocked(authApi.getStoredUser).mockReturnValue({
        id: 'another-admin',
        username: 'operator',
        role: 'admin',
        homeDir: '/',
        email: '',
      })
      vi.mocked(usersApi.toggleUserStatus).mockRejectedValueOnce(new UsersError('cannot disable self', 400, 'SELF_DISABLE'))

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('admin')).toBeInTheDocument()
      })

      await user.click(screen.getByRole('button', { name: 'admin 用户操作' }))

      await waitFor(() => {
        expect(screen.getByText('禁用用户')).toBeInTheDocument()
      })

      await user.click(screen.getByText('禁用用户'))

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
      vi.mocked(authApi.getStoredUser).mockReturnValue({
        id: 'another-admin',
        username: 'operator',
        role: 'admin',
        homeDir: '/',
        email: '',
      })
      vi.mocked(usersApi.toggleUserStatus).mockRejectedValueOnce(new UsersError('last admin', 400, 'LAST_ADMIN'))

      renderUsersPage()

      await waitFor(() => {
        expect(screen.getByText('admin')).toBeInTheDocument()
      })

      await user.click(screen.getByRole('button', { name: 'admin 用户操作' }))

      await waitFor(() => {
        expect(screen.getByText('禁用用户')).toBeInTheDocument()
      })

      await user.click(screen.getByText('禁用用户'))

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))

      await waitFor(() => {
        expect(screen.getByText('禁用用户')).toBeInTheDocument()
      })

      await user.click(screen.getByText('禁用用户'))

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

      await user.click(screen.getByRole('button', { name: 'testuser 用户操作' }))

      await waitFor(() => {
        expect(screen.getByText('禁用用户')).toBeInTheDocument()
      })

      await user.click(screen.getByText('禁用用户'))

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
