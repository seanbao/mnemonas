import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { BrowserRouter } from 'react-router-dom'
import { LoginPage } from './Login'

// Mock the auth store
const mockLogin = vi.fn()
const mockClearError = vi.fn()
const mockInitialize = vi.fn()

vi.mock('@/stores/auth', () => ({
  useAuthStore: vi.fn(() => ({
    login: mockLogin,
    error: null,
    isLoading: false,
    clearError: mockClearError,
    initialize: mockInitialize,
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
    addToast: vi.fn(),
  }
})

describe('LoginPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
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
      expect(screen.getByLabelText(/用户名/i)).toBeInTheDocument()
      expect(screen.getByLabelText(/密码/i)).toBeInTheDocument()
      expect(screen.getByRole('button', { name: /登录/i })).toBeInTheDocument()
    })

    it('renders help text for first-time users', () => {
      renderLogin()
      
      expect(screen.getByText(/首次运行时默认管理员账号为/i)).toBeInTheDocument()
      expect(screen.getByText(/初始密码请查看服务器启动日志/i)).toBeInTheDocument()
    })

    it('initializes auth on mount', () => {
      renderLogin()
      expect(mockInitialize).toHaveBeenCalled()
    })
  })

  describe('form interaction', () => {
    it('allows typing in username field', async () => {
      const user = userEvent.setup()
      renderLogin()
      
      const usernameInput = screen.getByLabelText(/用户名/i)
      await user.type(usernameInput, 'testuser')
      
      expect(usernameInput).toHaveValue('testuser')
    })

    it('allows typing in password field', async () => {
      const user = userEvent.setup()
      renderLogin()
      
      const passwordInput = screen.getByLabelText(/密码/i)
      await user.type(passwordInput, 'testpass')
      
      expect(passwordInput).toHaveValue('testpass')
    })

    it('calls login on form submit', async () => {
      mockLogin.mockResolvedValue(true)
      const user = userEvent.setup()
      renderLogin()
      
      await user.type(screen.getByLabelText(/用户名/i), 'admin')
      await user.type(screen.getByLabelText(/密码/i), 'password')
      await user.click(screen.getByRole('button', { name: /登录/i }))
      
      expect(mockLogin).toHaveBeenCalledWith('admin', 'password')
    })

    it('navigates to home on successful login', async () => {
      mockLogin.mockResolvedValue(true)
      const user = userEvent.setup()
      renderLogin()
      
      await user.type(screen.getByLabelText(/用户名/i), 'admin')
      await user.type(screen.getByLabelText(/密码/i), 'password')
      await user.click(screen.getByRole('button', { name: /登录/i }))
      
      expect(mockNavigate).toHaveBeenCalledWith('/', { replace: true })
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
        initialize: mockInitialize,
      })
      
      renderLogin()
      
      expect(screen.getByLabelText(/用户名/i)).toBeDisabled()
      expect(screen.getByLabelText(/密码/i)).toBeDisabled()
    })
  })
})
