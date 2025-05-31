import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { BrowserRouter } from 'react-router-dom'
import { LoginPage } from './Login'

const { mockAddToast, mockGetHealth, mockGetSetupStatus, mockLocationState } = vi.hoisted(() => ({
  mockAddToast: vi.fn(),
  mockGetHealth: vi.fn(),
  mockGetSetupStatus: vi.fn(),
  mockLocationState: { current: null as unknown },
}))

// Mock the auth store
const mockLogin = vi.fn()
const mockClearError = vi.fn()

vi.mock('@/stores/auth', () => ({
  useAuthStore: vi.fn(() => ({
    login: mockLogin,
    error: null,
    isLoading: false,
    clearError: mockClearError,
  })),
  useIsAuthenticated: vi.fn(() => false),
}))

vi.mock('@/api/setup', () => ({
  getSetupStatus: (...args: unknown[]) => mockGetSetupStatus(...args),
}))

vi.mock('@/api/files', () => ({
  getHealth: (...args: unknown[]) => mockGetHealth(...args),
}))

// Mock react-router-dom navigate
const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useLocation: () => ({ state: mockLocationState.current, pathname: '/login' }),
  }
})

// Mock HeroUI addToast
vi.mock('@heroui/react', async () => {
  const actual = await vi.importActual('@heroui/react')
  return {
    ...actual,
    addToast: mockAddToast,
  }
})

describe('LoginPage', () => {
  beforeEach(async () => {
    vi.clearAllMocks()
    mockLocationState.current = null
    mockGetSetupStatus.mockResolvedValue({
      success: true,
      is_first_run: false,
      auth_enabled: true,
      webdav_enabled: true,
      webdav_auth_type: 'basic',
    })
    mockGetHealth.mockResolvedValue({
      status: 'healthy',
      uptime: '1m',
      version: 'test-version',
    })

    const { useAuthStore, useIsAuthenticated } = await import('@/stores/auth')
    vi.mocked(useIsAuthenticated).mockReturnValue(false)
    vi.mocked(useAuthStore).mockReturnValue({
      login: mockLogin,
      error: null,
      isLoading: false,
      clearError: mockClearError,
    })
  })

  const renderLogin = () => {
    return render(
      <BrowserRouter>
        <LoginPage />
      </BrowserRouter>
    )
  }

  const waitForSetupStatusLoad = async () => {
    await waitFor(() => {
      expect(mockGetSetupStatus).toHaveBeenCalled()
    })
  }

  describe('rendering', () => {
    it('renders login form', async () => {
      renderLogin()
      await waitForSetupStatusLoad()
      
      // Multiple MnemoNAS text elements exist (header, footer, etc.)
      expect(screen.getAllByText(/MnemoNAS/i).length).toBeGreaterThan(0)
      expect(screen.getByText('登录后管理文件、版本与分享')).toBeInTheDocument()
      expect(screen.getByLabelText(/用户名/i, { selector: 'input' })).toBeInTheDocument()
      expect(screen.getByLabelText(/密码/i, { selector: 'input' })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: /登录/i })).toBeInTheDocument()
      expect(screen.getByText(/开源自托管文件存储/i)).toBeInTheDocument()
    })

    it('renders generic login guidance after setup is already completed', async () => {
      renderLogin()

      expect(await screen.findByText(/使用已配置的管理员或用户账号登录/i)).toBeInTheDocument()
      expect(screen.getByText(/初始密码只会写入服务器端文件/i)).toBeInTheDocument()
      expect(screen.getByText(/忘记密码？请在服务器上按照文档重置管理员密码/i)).toBeInTheDocument()
    })

    it('renders the backend version in the brand panel when available', async () => {
      renderLogin()

      expect(await screen.findByText(/MnemoNAS test-version · 自托管文件管理/i)).toBeInTheDocument()
    })

    it('hides the concrete version when health cannot be loaded', async () => {
      mockGetHealth.mockRejectedValueOnce(new Error('health unavailable'))

      renderLogin()

      expect(await screen.findByText(/MnemoNAS · 自托管文件管理/i)).toBeInTheDocument()
    })

    it('renders first-run guidance when setup status reports first run', async () => {
      mockGetSetupStatus.mockResolvedValueOnce({
        success: true,
        is_first_run: true,
        auth_enabled: true,
        webdav_enabled: true,
        webdav_auth_type: 'basic',
      })

      renderLogin()

      expect(await screen.findByText(/首次运行默认管理员账号为/i)).toBeInTheDocument()
      expect(screen.getByText(/初始密码位于服务器上的 initial-password.txt/i)).toBeInTheDocument()
    })

    it('falls back to neutral guidance when setup status cannot be loaded', async () => {
      mockGetSetupStatus.mockRejectedValueOnce(new Error('setup status unavailable'))

      renderLogin()

      expect(await screen.findByText(/使用管理员或已有账号登录/i)).toBeInTheDocument()
      expect(screen.getByText(/首次启动凭据只写入服务器端 initial-password.txt/i)).toBeInTheDocument()
    })

    it('does not initialize auth directly on mount', async () => {
      renderLogin()
      await waitForSetupStatusLoad()
      expect(mockLogin).not.toHaveBeenCalled()
    })

    it('redirects immediately when the user is already authenticated', async () => {
      const { useIsAuthenticated } = await import('@/stores/auth')
      vi.mocked(useIsAuthenticated).mockReturnValue(true)

      renderLogin()

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith('/', { replace: true })
      })
    })

    it('does not redirect an already authenticated user to an external location state target', async () => {
      mockLocationState.current = { from: '//evil.example/login' }
      const { useIsAuthenticated } = await import('@/stores/auth')
      vi.mocked(useIsAuthenticated).mockReturnValue(true)

      renderLogin()

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith('/', { replace: true })
      })
      expect(mockNavigate).not.toHaveBeenCalledWith('//evil.example/login', expect.anything())
    })

    it('shows a persistent inline error when auth store reports a login error', async () => {
      const { useAuthStore } = await import('@/stores/auth')
      vi.mocked(useAuthStore).mockReturnValue({
        login: mockLogin,
        error: '用户名或密码错误',
        isLoading: false,
        clearError: mockClearError,
      })

      renderLogin()
      await waitForSetupStatusLoad()

      expect(screen.getByRole('alert')).toHaveTextContent('用户名或密码错误')
      expect(mockAddToast).not.toHaveBeenCalled()
    })
  })

  describe('form interaction', () => {
    it('allows typing in username field', async () => {
      const user = userEvent.setup()
      renderLogin()
      
      const usernameInput = screen.getByLabelText(/用户名/i, { selector: 'input' })
      await user.type(usernameInput, 'testuser')
      
      expect(usernameInput).toHaveValue('testuser')
    })

    it('allows typing in password field', async () => {
      const user = userEvent.setup()
      renderLogin()
      
      const passwordInput = screen.getByLabelText(/密码/i, { selector: 'input' })
      await user.type(passwordInput, 'testpass')
      
      expect(passwordInput).toHaveValue('testpass')
    })

    it('toggles password visibility with an accessible control', async () => {
      const user = userEvent.setup()
      renderLogin()

      const passwordInput = screen.getByLabelText(/密码/i, { selector: 'input' })
      expect(passwordInput).toHaveAttribute('type', 'password')

      await user.click(screen.getByRole('button', { name: '显示密码' }))
      expect(passwordInput).toHaveAttribute('type', 'text')

      await user.click(screen.getByRole('button', { name: '隐藏密码' }))
      expect(passwordInput).toHaveAttribute('type', 'password')
    })

    it('calls login on form submit', async () => {
      mockLogin.mockResolvedValue(true)
      const user = userEvent.setup()
      renderLogin()
      
      await user.type(screen.getByLabelText(/用户名/i, { selector: 'input' }), 'admin')
      await user.type(screen.getByLabelText(/密码/i, { selector: 'input' }), 'password')
      await user.click(screen.getByRole('button', { name: /登录/i }))
      
      expect(mockLogin).toHaveBeenCalledWith('admin', 'password')
    })

    it('trims username before submitting login', async () => {
      mockLogin.mockResolvedValue(true)
      const user = userEvent.setup()
      renderLogin()

      await user.type(screen.getByLabelText(/用户名/i, { selector: 'input' }), '  admin  ')
      await user.type(screen.getByLabelText(/密码/i, { selector: 'input' }), 'password')
      await user.click(screen.getByRole('button', { name: /登录/i }))

      expect(mockLogin).toHaveBeenCalledWith('admin', 'password')
    })

    it('shows inline validation feedback for empty credentials', async () => {
      const user = userEvent.setup()
      renderLogin()

      await user.click(screen.getByRole('button', { name: /登录/i }))

      expect(screen.getByRole('alert')).toHaveTextContent('请输入用户名和密码')
      expect(mockAddToast).not.toHaveBeenCalled()
    })

    it('clears inline validation error after user edits input', async () => {
      const user = userEvent.setup()
      renderLogin()

      await user.click(screen.getByRole('button', { name: /登录/i }))
      expect(screen.getByRole('alert')).toHaveTextContent('请输入用户名和密码')

      await user.type(screen.getByLabelText(/用户名/i, { selector: 'input' }), 'admin')

      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    })

    it('clears inline validation error after user edits the password input', async () => {
      const user = userEvent.setup()
      renderLogin()

      await user.click(screen.getByRole('button', { name: /登录/i }))
      expect(screen.getByRole('alert')).toHaveTextContent('请输入用户名和密码')

      await user.type(screen.getByLabelText(/密码/i, { selector: 'input' }), 'password')

      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    })

    it('clears auth store errors when credentials are edited', async () => {
      const { useAuthStore } = await import('@/stores/auth')
      vi.mocked(useAuthStore).mockReturnValue({
        login: mockLogin,
        error: '用户名或密码错误',
        isLoading: false,
        clearError: mockClearError,
      })
      const user = userEvent.setup()
      renderLogin()

      await user.type(screen.getByLabelText(/用户名/i, { selector: 'input' }), 'a')
      await user.type(screen.getByLabelText(/密码/i, { selector: 'input' }), 'b')

      expect(mockClearError).toHaveBeenCalledTimes(2)
    })

    it('navigates to home on successful login', async () => {
      mockLogin.mockResolvedValue({ warning: false, message: undefined })
      const user = userEvent.setup()
      renderLogin()
      
      await user.type(screen.getByLabelText(/用户名/i, { selector: 'input' }), 'admin')
      await user.type(screen.getByLabelText(/密码/i, { selector: 'input' }), 'password')
      await user.click(screen.getByRole('button', { name: /登录/i }))
      
      expect(mockNavigate).toHaveBeenCalledWith('/', { replace: true })
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '登录成功', color: 'success' }))
    })

    it('navigates to the protected route after successful login when the redirect target is local', async () => {
      mockLocationState.current = { from: '/files/report?view=grid#preview' }
      mockLogin.mockResolvedValue({ warning: false, message: undefined })
      const user = userEvent.setup()
      renderLogin()

      await user.type(screen.getByLabelText(/用户名/i, { selector: 'input' }), 'admin')
      await user.type(screen.getByLabelText(/密码/i, { selector: 'input' }), 'password')
      await user.click(screen.getByRole('button', { name: /登录/i }))

      expect(mockNavigate).toHaveBeenCalledWith('/files/report?view=grid#preview', { replace: true })
    })

    it('falls back to home after successful login when the redirect target is external', async () => {
      mockLocationState.current = { from: 'https://evil.example/login' }
      mockLogin.mockResolvedValue({ warning: false, message: undefined })
      const user = userEvent.setup()
      renderLogin()

      await user.type(screen.getByLabelText(/用户名/i, { selector: 'input' }), 'admin')
      await user.type(screen.getByLabelText(/密码/i, { selector: 'input' }), 'password')
      await user.click(screen.getByRole('button', { name: /登录/i }))

      expect(mockNavigate).toHaveBeenCalledWith('/', { replace: true })
      expect(mockNavigate).not.toHaveBeenCalledWith('https://evil.example/login', expect.anything())
    })

    it('shows a warning toast when login succeeds with backend warning metadata', async () => {
      mockLogin.mockResolvedValue({ warning: true, message: undefined })
      const user = userEvent.setup()
      renderLogin()

      await user.type(screen.getByLabelText(/用户名/i, { selector: 'input' }), 'admin')
      await user.type(screen.getByLabelText(/密码/i, { selector: 'input' }), 'password')
      await user.click(screen.getByRole('button', { name: /登录/i }))

      expect(mockNavigate).toHaveBeenCalledWith('/', { replace: true })
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '登录成功，但活动日志写入失败', color: 'warning' }))
    })
  })

  describe('loading state', () => {
    it('disables form during login', async () => {
      // Re-mock with loading state
      const { useAuthStore } = await import('@/stores/auth')
      vi.mocked(useAuthStore).mockReturnValue({
        login: mockLogin,
        error: null,
        isLoading: true,
        clearError: mockClearError,
      })
      
      renderLogin()
      await waitForSetupStatusLoad()
      
      expect(screen.getByLabelText(/用户名/i, { selector: 'input' })).toBeDisabled()
      expect(screen.getByLabelText(/密码/i, { selector: 'input' })).toBeDisabled()
    })
  })
})
