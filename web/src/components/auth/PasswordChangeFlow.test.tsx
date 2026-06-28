import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { clearTokens, storeTokens, type User } from '@/api/auth'
import { useAuthStore } from '@/stores/auth'
import { ProtectedRoute } from './ProtectedRoute'
import { PasswordChangeForm } from './PasswordChangeForm'

const testUser: User = {
  id: 'user-1',
  username: 'family-user',
  role: 'user',
  homeDir: '/family-user',
  mustChangePassword: false,
}

function LoginOutcomeProbe() {
  const notice = useAuthStore((state) => state.notice)
  return (
    <main>
      <h1>登录</h1>
      {notice && (
        <div role="status">
          <p>{notice.title}</p>
          {notice.description && <p>{notice.description}</p>}
        </div>
      )}
    </main>
  )
}

function renderPasswordChangeFlow() {
  return render(
    <MemoryRouter initialEntries={['/account/security']}>
      <Routes>
        <Route path="/login" element={<LoginOutcomeProbe />} />
        <Route
          path="/account/security"
          element={(
            <ProtectedRoute>
              <PasswordChangeForm accountId={testUser.id} />
            </ProtectedRoute>
          )}
        />
      </Routes>
    </MemoryRouter>,
  )
}

function submitPasswordChange() {
  fireEvent.change(screen.getByLabelText('当前密码'), { target: { value: 'current-password' } })
  fireEvent.change(screen.getByLabelText('新密码'), { target: { value: 'replacement-password' } })
  fireEvent.change(screen.getByLabelText('确认新密码'), { target: { value: 'replacement-password' } })
  fireEvent.click(screen.getByRole('button', { name: '修改密码并重新登录' }))
}

describe('password change protected-route outcome', () => {
  beforeEach(() => {
    clearTokens({ reason: 'logout' })
    localStorage.clear()
    storeTokens('', '', testUser)
    useAuthStore.setState({
      user: testUser,
      isAuthenticated: true,
      isLoading: false,
      error: null,
      notice: null,
      authEnabled: true,
      shareEnabled: true,
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    clearTokens({ reason: 'logout' })
    localStorage.clear()
  })

  it('keeps the persistence warning after auth clearing unmounts the password form', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ Warning: '199 auth persistence warning' }),
      json: () => Promise.resolve({
        success: true,
        data: { warning: true },
        message: 'password changed with persistence warning',
      }),
    } as Response))
    renderPasswordChangeFlow()

    submitPasswordChange()

    await waitFor(() => expect(screen.getByRole('heading', { name: '登录' })).toBeInTheDocument())
    const notice = screen.getByRole('status')
    expect(notice).toHaveTextContent('密码已修改，请重新登录')
    expect(notice).toHaveTextContent('请使用新密码重新登录，并检查其他设备是否已退出')
  })

  it('keeps recovery guidance after an unconfirmed request clears authentication', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('network unavailable')))
    renderPasswordChangeFlow()

    submitPasswordChange()

    await waitFor(() => expect(screen.getByRole('heading', { name: '登录' })).toBeInTheDocument())
    const notice = screen.getByRole('status')
    expect(notice).toHaveTextContent('密码修改结果无法确认')
    expect(notice).toHaveTextContent('请先尝试使用新密码登录；若无法登录，再尝试原密码')
  })
})
