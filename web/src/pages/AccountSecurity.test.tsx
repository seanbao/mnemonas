import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { AuthError, PASSWORD_CHANGE_UNCONFIRMED_MESSAGE } from '@/api/auth'
import { AccountSecurityPage } from './AccountSecurity'

const { addToastMock, changePasswordMock, initializeAuthMock, navigateMock, authState, locationState } = vi.hoisted(() => ({
  addToastMock: vi.fn(),
  changePasswordMock: vi.fn(),
  initializeAuthMock: vi.fn(),
  navigateMock: vi.fn(),
  authState: {
    authEnabled: true,
    user: {
      id: 'user-1',
      username: 'family-member',
      role: 'user' as 'admin' | 'user' | 'guest',
      email: 'member@local',
      homeDir: '/family-member',
      mustChangePassword: false,
    } as {
      id: string
      username: string
      role: 'admin' | 'user' | 'guest'
      email: string
      homeDir: string
      mustChangePassword: boolean
    } | null,
  },
  locationState: { current: null as unknown },
}))

vi.mock('@/api/auth', async () => {
  const actual = await vi.importActual<typeof import('@/api/auth')>('@/api/auth')
  return {
    ...actual,
    changePassword: (...args: unknown[]) => changePasswordMock(...args),
  }
})

vi.mock('@heroui/react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@heroui/react')>()
  return {
    ...actual,
    addToast: (...args: unknown[]) => addToastMock(...args),
  }
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: (selector: (state: { authEnabled: boolean; initialize: typeof initializeAuthMock }) => unknown) => selector({
    authEnabled: authState.authEnabled,
    initialize: initializeAuthMock,
  }),
  useUser: () => authState.user,
}))

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom')
  return {
    ...actual,
    useNavigate: () => navigateMock,
    useLocation: () => ({ state: locationState.current }),
  }
})

function fillPasswordForm(newPassword = 'replacement-password') {
  fireEvent.change(screen.getByLabelText('当前密码'), { target: { value: 'current-password' } })
  fireEvent.change(screen.getByLabelText('新密码'), { target: { value: newPassword } })
  fireEvent.change(screen.getByLabelText('确认新密码'), { target: { value: newPassword } })
}

describe('AccountSecurityPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    authState.authEnabled = true
    authState.user = {
      id: 'user-1',
      username: 'family-member',
      role: 'user',
      email: 'member@local',
      homeDir: '/family-member',
      mustChangePassword: false,
    }
    locationState.current = null
    changePasswordMock.mockResolvedValue({ warning: false, message: 'password changed successfully' })
    initializeAuthMock.mockResolvedValue(undefined)
  })

  it.each([
    ['普通用户', 'user' as const],
    ['管理员', 'admin' as const],
  ])('allows a %s to manage the current account password', (_label, role) => {
    if (!authState.user) {
      throw new Error('test user is required')
    }
    authState.user.role = role

    render(<AccountSecurityPage />)

    expect(screen.getByRole('heading', { name: '账户安全' })).toBeInTheDocument()
    expect(screen.getByText(new RegExp(`当前账户：family-member · ${role === 'admin' ? '管理员' : '普通用户'}`))).toBeInTheDocument()
    expect(screen.getByText(/此账户在所有设备上的登录都会退出/)).toBeInTheDocument()
    expect(screen.getByLabelText('当前密码')).toHaveAttribute('autocomplete', 'current-password')
    expect(screen.getByLabelText('新密码')).toHaveAttribute('autocomplete', 'new-password')
  })

  it('changes the password through the shared self-service endpoint and returns to login', async () => {
    render(<AccountSecurityPage />)
    fillPasswordForm()

    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))

    await waitFor(() => {
      expect(changePasswordMock).toHaveBeenCalledWith({
        old_password: 'current-password',
        new_password: 'replacement-password',
      }, { expectedUserId: 'user-1', signal: expect.any(AbortSignal) })
    })
    expect(addToastMock).toHaveBeenCalledWith({ title: '密码已修改，请重新登录', color: 'success' })
    expect(navigateMock).toHaveBeenCalledWith('/login', { replace: true })
  })

  it('ends the local flow with sign-in guidance when the password result cannot be confirmed', async () => {
    changePasswordMock.mockRejectedValueOnce(new AuthError(
      PASSWORD_CHANGE_UNCONFIRMED_MESSAGE,
      0,
      'PASSWORD_CHANGE_UNCONFIRMED',
    ))
    render(<AccountSecurityPage />)
    fillPasswordForm()

    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))

    await waitFor(() => {
      expect(addToastMock).toHaveBeenCalledWith({
        title: '密码修改结果无法确认',
        description: PASSWORD_CHANGE_UNCONFIRMED_MESSAGE,
        color: 'warning',
      })
    })
    expect(navigateMock).toHaveBeenCalledWith('/login', { replace: true })
  })

  it('prevents duplicate submissions while the password request is pending', async () => {
    changePasswordMock.mockImplementationOnce(() => new Promise(() => undefined))
    render(<AccountSecurityPage />)
    fillPasswordForm()

    const submit = screen.getByRole('button', { name: '修改密码并重新登录' })
    fireEvent.click(submit)
    fireEvent.click(submit)

    await waitFor(() => expect(changePasswordMock).toHaveBeenCalledTimes(1))
    expect(screen.getByRole('button', { name: '取消' })).toBeDisabled()
  })

  it('aborts an unfinished password request when leaving the page', async () => {
    let signal: AbortSignal | undefined
    changePasswordMock.mockImplementationOnce((_request, options?: { signal?: AbortSignal }) => {
      signal = options?.signal
      return new Promise(() => undefined)
    })
    const view = render(<AccountSecurityPage />)
    fillPasswordForm()
    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))
    await waitFor(() => expect(signal).toBeInstanceOf(AbortSignal))

    view.unmount()

    expect(signal?.aborted).toBe(true)
  })

  it('supports explicit password visibility controls', () => {
    render(<AccountSecurityPage />)
    const currentPassword = screen.getByLabelText('当前密码')

    expect(currentPassword).toHaveAttribute('type', 'password')
    fireEvent.click(screen.getByRole('button', { name: '显示当前密码' }))
    expect(currentPassword).toHaveAttribute('type', 'text')
    expect(screen.getByRole('button', { name: '隐藏当前密码' })).toHaveAttribute('aria-pressed', 'true')
  })

  it('returns home without submitting when the user cancels', () => {
    render(<AccountSecurityPage />)
    fillPasswordForm()

    fireEvent.click(screen.getByRole('button', { name: '取消' }))

    expect(changePasswordMock).not.toHaveBeenCalled()
    expect(navigateMock).toHaveBeenCalledWith('/', { replace: true })
  })

  it('returns to account settings when opened from the settings shortcut', () => {
    locationState.current = { returnTo: '/settings?tab=general' }
    render(<AccountSecurityPage />)

    fireEvent.click(screen.getByRole('button', { name: '取消' }))

    expect(changePasswordMock).not.toHaveBeenCalled()
    expect(navigateMock).toHaveBeenCalledWith('/settings?tab=general', { replace: true })
  })

  it('returns to the safe in-app page that opened account security', () => {
    locationState.current = { returnTo: '/files?path=%2Ffamily#recent' }
    render(<AccountSecurityPage />)

    fireEvent.click(screen.getByRole('button', { name: '取消' }))

    expect(navigateMock).toHaveBeenCalledWith('/files?path=%2Ffamily#recent', { replace: true })
  })

  it('moves focus to the account-security region without opening a password field', async () => {
    render(<AccountSecurityPage />)

    const page = screen.getByRole('heading', { name: '账户安全' }).closest('section')
    await waitFor(() => expect(page).toHaveFocus())
    expect(screen.getByLabelText('当前密码')).not.toHaveFocus()
  })

  it('clears sensitive fields and aborts the old request when the account changes', async () => {
    let requestSignal: AbortSignal | undefined
    changePasswordMock.mockImplementationOnce((_request, options?: { signal?: AbortSignal }) => {
      requestSignal = options?.signal
      return new Promise(() => undefined)
    })
    const view = render(<AccountSecurityPage />)
    fillPasswordForm()
    fireEvent.click(screen.getByRole('button', { name: '显示当前密码' }))
    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))
    expect(requestSignal?.aborted).toBe(false)

    if (!authState.user) {
      throw new Error('test user is required')
    }
    authState.user.id = 'user-2'
    authState.user.username = 'second-user'
    view.rerender(<AccountSecurityPage />)

    expect(requestSignal?.aborted).toBe(true)
    expect(screen.getByLabelText('当前密码')).toHaveValue('')
    expect(screen.getByLabelText('当前密码')).toHaveAttribute('type', 'password')
    expect(screen.getByLabelText('新密码')).toHaveValue('')
    expect(screen.getByLabelText('确认新密码')).toHaveValue('')
    expect(screen.getByText(/当前账户：second-user/)).toBeInTheDocument()
  })

  it('does not expose a password form when authentication is disabled', () => {
    authState.authEnabled = false

    render(<AccountSecurityPage />)

    expect(screen.getByRole('heading', { name: '密码登录未启用' })).toBeInTheDocument()
    expect(screen.queryByLabelText('当前密码')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '返回首页' }))
    expect(navigateMock).toHaveBeenCalledWith('/')
  })

  it('requires a new sign-in when the current account cannot be confirmed', () => {
    authState.user = null

    render(<AccountSecurityPage />)

    expect(screen.getByRole('heading', { name: '登录会话不可用' })).toBeInTheDocument()
    expect(screen.queryByLabelText('当前密码')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '重新登录' }))
    expect(navigateMock).toHaveBeenCalledWith('/login', { replace: true })
  })

  it.each([
    '//external.example/path',
    '/login?next=%2Ffiles',
    '/account/security#again',
    '/ACCOUNT/SECURITY/?again=1',
    '/s/public-link',
  ])('returns home instead of accepting the unsafe source path %s', (returnTo) => {
    locationState.current = { returnTo }
    render(<AccountSecurityPage />)

    fireEvent.click(screen.getByRole('button', { name: '取消' }))

    expect(navigateMock).toHaveBeenCalledWith('/', { replace: true })
  })
})
