import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { BrowserRouter } from 'react-router-dom'
import { LoginPage } from './Login'

const { mockAddToast } = vi.hoisted(() => ({
  mockAddToast: vi.fn(),
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

// Mock react-router-dom navigate
const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useLocation: () => ({ state: null, pathname: '/login' }),
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

    const { useAuthStore } = await import('@/stores/auth')
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

  describe('rendering', () => {
    it('renders login form', () => {
      renderLogin()
      
      // Multiple MnemoNAS text elements exist (header, footer, etc.)
      expect(screen.getAllByText(/MnemoNAS/i).length).toBeGreaterThan(0)
      expect(screen.getByText('请登录以继续访问系统')).toBeInTheDocument()
      expect(screen.getByLabelText(/用户名/i, { selector: 'input' })).toBeInTheDocument()
      expect(screen.getByLabelText(/密码/i, { selector: 'input' })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: /登录/i })).toBeInTheDocument()
    })

    it('renders help text for first-time users', () => {
      renderLogin()
      
      expect(screen.getByText(/首次运行时默认管理员账号为/i)).toBeInTheDocument()
      expect(screen.getByText(/初始密码请查看服务器启动日志，浏览器界面不显示初始密码/i)).toBeInTheDocument()
      expect(screen.getByText(/当前版本未提供浏览器内密码重置入口/i)).toBeInTheDocument()
    })

    it('does not initialize auth directly on mount', () => {
      renderLogin()
      expect(mockLogin).not.toHaveBeenCalled()
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

      expect(screen.getByRole('alert')).toHaveTextContent('用户名或密码错误')
      expect(mockClearError).toHaveBeenCalled()
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

    it('navigates to home on successful login', async () => {
      mockLogin.mockResolvedValue(true)
      const user = userEvent.setup()
      renderLogin()
      
      await user.type(screen.getByLabelText(/用户名/i, { selector: 'input' }), 'admin')
      await user.type(screen.getByLabelText(/密码/i, { selector: 'input' }), 'password')
      await user.click(screen.getByRole('button', { name: /登录/i }))
      
      expect(mockNavigate).toHaveBeenCalledWith('/', { replace: true })
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '登录成功', color: 'success' }))
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
      
      expect(screen.getByLabelText(/用户名/i, { selector: 'input' })).toBeDisabled()
      expect(screen.getByLabelText(/密码/i, { selector: 'input' })).toBeDisabled()
    })
  })
})
