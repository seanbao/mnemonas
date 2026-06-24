import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { AuthError } from '@/api/auth'
import { PasswordChangeGate } from './PasswordChangeGate'

const changePasswordMock = vi.fn()
const logoutMock = vi.fn()
const navigateMock = vi.fn()
const businessPageRenderMock = vi.fn()
const addToastMock = vi.fn()
const authState = {
  user: {
    id: 'admin-1',
    username: 'admin',
    role: 'admin' as 'admin' | 'user',
    email: '',
    homeDir: '/',
    mustChangePassword: true,
  },
}

vi.mock('@/api/auth', async () => {
  const actual = await vi.importActual<typeof import('@/api/auth')>('@/api/auth')
  return {
    ...actual,
    changePassword: (...args: unknown[]) => changePasswordMock(...args),
    logout: (...args: unknown[]) => logoutMock(...args),
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
  useUser: () => authState.user,
}))

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom')
  return {
    ...actual,
    useNavigate: () => navigateMock,
  }
})

function BusinessPage() {
  businessPageRenderMock()
  return <div>normal business page</div>
}

function renderGate() {
  return render(
    <PasswordChangeGate>
      <BusinessPage />
    </PasswordChangeGate>,
  )
}

function fillPasswordForm(newPassword: string, confirmation = newPassword) {
  fireEvent.change(screen.getByLabelText('当前密码'), { target: { value: 'initial-password' } })
  fireEvent.change(screen.getByLabelText('新密码'), { target: { value: newPassword } })
  fireEvent.change(screen.getByLabelText('确认新密码'), { target: { value: confirmation } })
}

describe('PasswordChangeGate', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    authState.user = {
      id: 'admin-1',
      username: 'admin',
      role: 'admin',
      email: '',
      homeDir: '/',
      mustChangePassword: true,
    }
    changePasswordMock.mockResolvedValue({ warning: false, message: 'password changed successfully' })
    logoutMock.mockResolvedValue({ warning: false, message: undefined })
  })

  it('renders normal application content when no password change is required', () => {
    authState.user.mustChangePassword = false

    renderGate()

    expect(screen.getByText('normal business page')).toBeInTheDocument()
    expect(businessPageRenderMock).toHaveBeenCalledTimes(1)
    expect(screen.queryByText('必须修改密码')).not.toBeInTheDocument()
  })

  it.each([
    ['管理员', 'admin' as const],
    ['普通用户', 'user' as const],
  ])('blocks business content for %s accounts', (_label, role) => {
    authState.user.role = role

    renderGate()
    fireEvent.keyDown(window, { key: 'Escape' })

    expect(screen.getByRole('heading', { name: '必须修改密码' })).toBeInTheDocument()
    expect(screen.getByText(/完成修改前，文件和管理功能不可访问/)).toBeInTheDocument()
    expect(screen.queryByText('normal business page')).not.toBeInTheDocument()
    expect(businessPageRenderMock).not.toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: /关闭|取消/ })).not.toBeInTheDocument()
  })

  it('submits passwords that satisfy the UTF-8 byte limits', async () => {
    renderGate()
    const multiBytePassword = '密码密码密码'
    fillPasswordForm(multiBytePassword)

    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))

    await waitFor(() => {
      expect(changePasswordMock).toHaveBeenCalledWith({
        old_password: 'initial-password',
        new_password: multiBytePassword,
      }, { signal: expect.any(AbortSignal) })
    })
    expect(navigateMock).toHaveBeenCalledWith('/login', { replace: true })
  })

  it('describes password persistence warnings as authentication-state durability warnings', async () => {
    changePasswordMock.mockResolvedValueOnce({
      warning: true,
      message: 'password changed with persistence warning',
    })
    renderGate()
    fillPasswordForm('replacement-password')

    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))

    await waitFor(() => {
      expect(addToastMock).toHaveBeenCalledWith({
        title: '密码已修改，但认证状态持久化未完全确认',
        description: '请使用新密码重新登录验证，并检查设备存储状态或服务日志。',
        color: 'warning',
      })
    })
    expect(navigateMock).toHaveBeenCalledWith('/login', { replace: true })
  })

  it.each([
    ['少于 8 个 UTF-8 字节', 'short'],
    ['超过 72 个 UTF-8 字节', '密'.repeat(25)],
  ])('rejects a new password with %s', async (_label, password) => {
    renderGate()
    fillPasswordForm(password)

    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('新密码长度必须为 8 至 72 个 UTF-8 字节。')
    expect(changePasswordMock).not.toHaveBeenCalled()
  })

  it('rejects a new password containing only whitespace', async () => {
    renderGate()
    fillPasswordForm('        ')

    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('请输入新密码。')
    expect(changePasswordMock).not.toHaveBeenCalled()
  })

  it.each([
    ['当前密码', () => undefined, '请输入当前密码。'],
    ['新密码', () => {
      fireEvent.change(screen.getByLabelText('当前密码'), { target: { value: 'initial-password' } })
    }, '请输入新密码。'],
    ['确认新密码', () => {
      fireEvent.change(screen.getByLabelText('当前密码'), { target: { value: 'initial-password' } })
      fireEvent.change(screen.getByLabelText('新密码'), { target: { value: 'replacement-password' } })
    }, '请再次输入新密码。'],
  ])('requires the %s field', async (_label, fillForm, expectedMessage) => {
    renderGate()
    fillForm()

    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(expectedMessage)
    expect(changePasswordMock).not.toHaveBeenCalled()
  })

  it('requires the confirmation to match the new password', async () => {
    renderGate()
    fillPasswordForm('replacement-password', 'different-password')

    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('两次输入的新密码不一致。')
    expect(changePasswordMock).not.toHaveBeenCalled()
  })

  it('clears a validation error after the user edits a password field', async () => {
    renderGate()

    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('请输入当前密码。')

    fireEvent.change(screen.getByLabelText('当前密码'), { target: { value: 'initial-password' } })

    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('rejects a new password that matches the current password', async () => {
    renderGate()
    fillPasswordForm('initial-password')

    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('新密码不能与当前密码相同。')
    expect(changePasswordMock).not.toHaveBeenCalled()
  })

  it('shows a localized error when the current password is incorrect', async () => {
    changePasswordMock.mockRejectedValueOnce(new AuthError(
      'current password is incorrect',
      401,
      'INVALID_PASSWORD',
    ))
    renderGate()
    fillPasswordForm('replacement-password')

    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('当前密码不正确。')
    expect(screen.queryByText('normal business page')).not.toBeInTheDocument()
    expect(navigateMock).not.toHaveBeenCalled()
  })

  it('shows a localized error when the server rejects an unchanged password', async () => {
    changePasswordMock.mockRejectedValueOnce(new AuthError(
      'new password must differ from current password',
      400,
      'PASSWORD_UNCHANGED',
    ))
    renderGate()
    fillPasswordForm('replacement-password')

    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('新密码不能与当前密码相同。')
    expect(navigateMock).not.toHaveBeenCalled()
  })

  it('keeps the safe client message for a malformed password-change response', async () => {
    changePasswordMock.mockRejectedValueOnce(new AuthError(
      '修改密码响应无效',
      502,
      'INVALID_RESPONSE',
    ))
    renderGate()
    fillPasswordForm('replacement-password')

    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('修改密码响应无效')
    expect(navigateMock).not.toHaveBeenCalled()
  })

  it.each([
    ['PASSWORD_TOO_SHORT', '新密码长度必须为 8 至 72 个 UTF-8 字节。'],
    ['USER_DISABLED', '当前账户已被禁用，请退出后联系管理员。'],
    ['TOKEN_EXPIRED', '登录会话已失效，请退出后重新登录。'],
  ])('maps the %s server error to a safe localized message', async (code, expectedMessage) => {
    changePasswordMock.mockRejectedValueOnce(new AuthError('server rejected password change', 400, code))
    renderGate()
    fillPasswordForm('replacement-password')

    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(expectedMessage)
    expect(navigateMock).not.toHaveBeenCalled()
  })

  it('does not expose unexpected password-change failures', async () => {
    changePasswordMock.mockRejectedValueOnce(new Error('upstream secret detail'))
    renderGate()
    fillPasswordForm('replacement-password')

    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('密码修改失败，请检查网络后重试。')
    expect(screen.queryByText('upstream secret detail')).not.toBeInTheDocument()
  })

  it('allows the user to log out without mounting business content', async () => {
    renderGate()

    fireEvent.click(screen.getByRole('button', { name: '退出登录' }))

    await waitFor(() => {
      expect(logoutMock).toHaveBeenCalledTimes(1)
    })
    expect(navigateMock).toHaveBeenCalledWith('/login', { replace: true })
    expect(businessPageRenderMock).not.toHaveBeenCalled()
  })

  it('describes logout persistence warnings as authentication-state durability warnings', async () => {
    logoutMock.mockResolvedValueOnce({
      warning: true,
      message: 'logged out with persistence warning',
    })
    renderGate()

    fireEvent.click(screen.getByRole('button', { name: '退出登录' }))

    await waitFor(() => {
      expect(addToastMock).toHaveBeenCalledWith({
        title: '已退出登录，但认证状态持久化未完全确认',
        description: '请检查设备存储状态或服务日志，确认会话撤销已持久化。',
        color: 'warning',
      })
    })
    expect(navigateMock).toHaveBeenCalledWith('/login', { replace: true })
  })

  it('keeps the gate mounted when logout fails', async () => {
    logoutMock.mockRejectedValueOnce(new Error('network unavailable'))
    renderGate()

    fireEvent.click(screen.getByRole('button', { name: '退出登录' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('退出登录失败，请稍后重试。')
    expect(screen.getByRole('heading', { name: '必须修改密码' })).toBeInTheDocument()
    expect(businessPageRenderMock).not.toHaveBeenCalled()
    expect(navigateMock).not.toHaveBeenCalled()
  })

  it('moves focus to the gate and announces the blocked account', async () => {
    renderGate()

    const heading = screen.getByRole('heading', { name: '必须修改密码' })
    const gate = heading.closest('main')
    await waitFor(() => expect(gate).toHaveFocus())
    expect(screen.getByRole('status')).toHaveTextContent('账户 admin 必须修改密码')
  })

  it('clears sensitive fields and aborts the old request when the gated account changes', async () => {
    let requestSignal: AbortSignal | undefined
    changePasswordMock.mockImplementationOnce((_request, options?: { signal?: AbortSignal }) => {
      requestSignal = options?.signal
      return new Promise(() => undefined)
    })
    const view = renderGate()
    fillPasswordForm('replacement-password')
    fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))
    expect(requestSignal?.aborted).toBe(false)

    authState.user = {
      ...authState.user,
      id: 'admin-2',
      username: 'second-admin',
    }
    view.rerender(
      <PasswordChangeGate>
        <BusinessPage />
      </PasswordChangeGate>,
    )

    expect(requestSignal?.aborted).toBe(true)
    expect(screen.getByLabelText('当前密码')).toHaveValue('')
    expect(screen.getByLabelText('新密码')).toHaveValue('')
    expect(screen.getByLabelText('确认新密码')).toHaveValue('')
    expect(screen.getByRole('status')).toHaveTextContent('账户 second-admin 必须修改密码')
  })

  it('does not restore prior password fields when the gate deactivates and activates again', () => {
    const view = renderGate()
    fillPasswordForm('replacement-password')

    authState.user = { ...authState.user, mustChangePassword: false }
    view.rerender(
      <PasswordChangeGate>
        <BusinessPage />
      </PasswordChangeGate>,
    )
    expect(screen.getByText('normal business page')).toBeInTheDocument()

    authState.user = { ...authState.user, mustChangePassword: true }
    view.rerender(
      <PasswordChangeGate>
        <BusinessPage />
      </PasswordChangeGate>,
    )
    expect(screen.getByLabelText('当前密码')).toHaveValue('')
    expect(screen.getByLabelText('新密码')).toHaveValue('')
    expect(screen.getByLabelText('确认新密码')).toHaveValue('')
  })
})
