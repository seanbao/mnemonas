import { describe, it, expect, vi, beforeEach } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
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
    it('passes abort signals to login page bootstrap probes', async () => {
      renderLogin()

      await waitFor(() => {
        expect(mockGetSetupStatus.mock.calls[0]?.[0]).toEqual({
          signal: expect.any(AbortSignal),
        })
        expect(mockGetHealth.mock.calls[0]?.[0]).toEqual({
          signal: expect.any(AbortSignal),
        })
      })
    })

    it('aborts login page bootstrap probes on unmount', async () => {
      mockGetSetupStatus.mockReturnValue(new Promise(() => {}))
      mockGetHealth.mockReturnValue(new Promise(() => {}))

      const { unmount } = renderLogin()

      await waitFor(() => {
        expect(mockGetSetupStatus).toHaveBeenCalled()
        expect(mockGetHealth).toHaveBeenCalled()
      })
      const setupSignal = mockGetSetupStatus.mock.calls[0]?.[0]?.signal
      const healthSignal = mockGetHealth.mock.calls[0]?.[0]?.signal
      expect(setupSignal).toBeInstanceOf(AbortSignal)
      expect(healthSignal).toBeInstanceOf(AbortSignal)
      expect(setupSignal?.aborted).toBe(false)
      expect(healthSignal?.aborted).toBe(false)

      unmount()

      expect(setupSignal?.aborted).toBe(true)
      expect(healthSignal?.aborted).toBe(true)
    })

    it('renders login form', async () => {
      renderLogin()
      await waitForSetupStatusLoad()
      
      // Multiple MnemoNAS text elements exist (header, footer, etc.)
      expect(screen.getAllByText(/MnemoNAS/i).length).toBeGreaterThan(0)
      expect(screen.getByText('自托管私有云存储')).toBeInTheDocument()
      expect(screen.getByText('登录后管理文件、版本与分享')).toBeInTheDocument()
      expect(screen.getByLabelText(/用户名/i, { selector: 'input' })).toBeInTheDocument()
      expect(screen.getByLabelText(/密码/i, { selector: 'input' })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: /登录/i })).toBeInTheDocument()
      expect(screen.getByText(/支持 Linux 主机、容器和局域网部署/i)).toBeInTheDocument()
      const discouragedDeploymentCopy = new RegExp(['闲置', 'Ubuntu', '笔记本'].join(' '), 'i')
      expect(screen.queryByText(discouragedDeploymentCopy)).not.toBeInTheDocument()
      expect(screen.getByText(/开源自托管文件存储/i)).toBeInTheDocument()
    })

    it('renders generic login guidance after setup is already completed', async () => {
      renderLogin()

      expect(await screen.findByText(/使用已配置的管理员或用户账号登录/i)).toBeInTheDocument()
      expect(screen.getByText(/初始密码只会写入服务器端文件/i)).toBeInTheDocument()
    })

    it('separates ordinary-user help from local administrator recovery', async () => {
      renderLogin()

      const recoveryHelp = await screen.findByRole('note', { name: '账号恢复帮助' })
      expect(within(recoveryHelp).getByText(/普通用户：/).closest('p')).toHaveTextContent(
        '忘记密码时，请联系设备管理员。'
      )
      expect(within(recoveryHelp).getByText(/设备管理员：/).closest('p')).toHaveTextContent(
        '需先在 NAS 主机停止 MnemoNAS 服务，再按说明运行本地离线恢复命令。'
      )
      expect(recoveryHelp).toHaveTextContent('登录页不提供远程或匿名恢复入口。')

      const recoveryLink = within(recoveryHelp).getByRole('link', { name: /查看管理员恢复说明/ })
      expect(recoveryLink).toHaveAttribute(
        'href',
        'https://github.com/seanbao/mnemonas/blob/main/docs/security.md#%E7%AE%A1%E7%90%86%E5%91%98%E7%A6%BB%E7%BA%BF%E6%81%A2%E5%A4%8D'
      )
      expect(recoveryLink).toHaveAttribute('target', '_blank')
      expect(recoveryLink).toHaveAttribute('rel', expect.stringContaining('noopener'))
      expect(recoveryLink).toHaveAttribute('rel', expect.stringContaining('noreferrer'))
    })

    it('keeps the responsive login panel classes for narrow screens', async () => {
      renderLogin()
      await waitForSetupStatusLoad()

      const loginPanel = screen.getByRole('main', { name: '登录' })
      expect(loginPanel).toHaveClass(
        'w-full',
        'px-5',
        'py-8',
        'sm:p-8',
        'lg:w-[56%]'
      )
      expect(loginPanel.firstElementChild).toHaveClass('w-full', 'max-w-md')
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

    it('shows deployment safety hints when setup status reports unsafe public-access prerequisites', async () => {
      mockGetSetupStatus.mockResolvedValueOnce({
        success: true,
        is_first_run: true,
        auth_enabled: false,
        share_enabled: true,
        webdav_enabled: true,
        webdav_auth_type: 'none',
        allow_unsafe_no_auth: true,
      })

      renderLogin()

      expect(await screen.findByRole('status', { name: '部署安全提示' })).toBeInTheDocument()
      expect(screen.getByText(/认证当前关闭；公网访问前应先启用认证/i)).toBeInTheDocument()
      expect(screen.getByText(/分享功能当前启用；认证关闭时不应把服务暴露到公网/i)).toBeInTheDocument()
      expect(screen.getByText(/WebDAV 当前允许匿名访问；公网访问前应改为用户认证或关闭 WebDAV/i)).toBeInTheDocument()
      expect(screen.getByText(/无认证暴露例外当前开启；公网访问前应关闭该例外/i)).toBeInTheDocument()
    })

    it('does not show deployment safety hints when setup status is safe for public-access prerequisites', async () => {
      renderLogin()

      await waitForSetupStatusLoad()

      expect(screen.queryByRole('status', { name: '部署安全提示' })).not.toBeInTheDocument()
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

    it('does not redirect an already authenticated user back to the login page', async () => {
      mockLocationState.current = { from: '/login?expired=1' }
      const { useIsAuthenticated } = await import('@/stores/auth')
      vi.mocked(useIsAuthenticated).mockReturnValue(true)

      renderLogin()

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith('/', { replace: true })
      })
      expect(mockNavigate).not.toHaveBeenCalledWith('/login?expired=1', expect.anything())
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

    it('does not copy entered credentials into administrator recovery help or its link', async () => {
      const user = userEvent.setup()
      renderLogin()

      const privateUsername = 'private-user-938'
      const privatePassword = 'private-pass-938'
      await user.type(screen.getByLabelText(/用户名/i, { selector: 'input' }), privateUsername)
      await user.type(screen.getByLabelText(/密码/i, { selector: 'input' }), privatePassword)

      const recoveryHelp = screen.getByRole('note', { name: '账号恢复帮助' })
      const recoveryLink = within(recoveryHelp).getByRole('link', { name: /查看管理员恢复说明/ })
      expect(recoveryHelp).not.toHaveTextContent(privateUsername)
      expect(recoveryHelp).not.toHaveTextContent(privatePassword)
      expect(recoveryLink.getAttribute('href')).not.toContain(privateUsername)
      expect(recoveryLink.getAttribute('href')).not.toContain(privatePassword)
      const recoveryURL = new URL(recoveryLink.getAttribute('href') ?? '')
      expect(recoveryURL.username).toBe('')
      expect(recoveryURL.password).toBe('')
      expect(recoveryURL.search).toBe('')
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

    it('rejects a password containing only whitespace', async () => {
      const user = userEvent.setup()
      renderLogin()

      await user.type(screen.getByLabelText(/用户名/i, { selector: 'input' }), 'admin')
      fireEvent.change(screen.getByLabelText(/密码/i, { selector: 'input' }), { target: { value: '        ' } })
      await user.click(screen.getByRole('button', { name: /登录/i }))

      expect(screen.getByRole('alert')).toHaveTextContent('请输入用户名和密码')
      expect(mockLogin).not.toHaveBeenCalled()
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

    it('falls back to home after successful login when the redirect target is a public share route', async () => {
      mockLocationState.current = { from: '/s/share-1?download=1' }
      mockLogin.mockResolvedValue({ warning: false, message: undefined })
      const user = userEvent.setup()
      renderLogin()

      await user.type(screen.getByLabelText(/用户名/i, { selector: 'input' }), 'admin')
      await user.type(screen.getByLabelText(/密码/i, { selector: 'input' }), 'password')
      await user.click(screen.getByRole('button', { name: /登录/i }))

      expect(mockNavigate).toHaveBeenCalledWith('/', { replace: true })
      expect(mockNavigate).not.toHaveBeenCalledWith('/s/share-1?download=1', expect.anything())
    })

    it('shows a warning toast when login succeeds with backend warning metadata', async () => {
      mockLogin.mockResolvedValue({ warning: true, message: 'login audit write failed' })
      const user = userEvent.setup()
      renderLogin()

      await user.type(screen.getByLabelText(/用户名/i, { selector: 'input' }), 'admin')
      await user.type(screen.getByLabelText(/密码/i, { selector: 'input' }), 'password')
      await user.click(screen.getByRole('button', { name: /登录/i }))

      expect(mockNavigate).toHaveBeenCalledWith('/', { replace: true })
      expect(mockAddToast).toHaveBeenCalledWith(expect.objectContaining({ title: '登录成功，但操作记录写入失败', color: 'warning' }))
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
