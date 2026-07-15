import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, act, fireEvent, waitFor } from '@testing-library/react'
import React from 'react'
import { useSettingsDraftStore } from '@/stores/settingsDraft'
import { Header } from './Header'

const refetchQueries = vi.fn().mockResolvedValue(undefined)
const clearQueries = vi.fn()
const useIsAdminMock = vi.fn(() => true)
const mockAddToast = vi.fn()
const openUrlInNewTabMock = vi.fn(() => true)
const navigateMock = vi.fn()
const logoutMock = vi.fn()
const confirmMock = vi.fn(() => true)
let locationPathname = '/'
let locationSearch = ''
let locationHash = ''
let authEnabled = true

vi.mock('@tanstack/react-query', () => ({
  useQueryClient: () => ({ refetchQueries, clear: clearQueries }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: (selector?: (state: { authEnabled: boolean; logout: typeof logoutMock }) => unknown) => {
    const state = { authEnabled, logout: logoutMock }
    return selector ? selector(state) : state
  },
  useUser: () => ({ username: 'admin', email: 'admin@local' }),
  useIsAdmin: () => useIsAdminMock(),
}))

vi.mock('react-router-dom', () => ({
  useNavigate: () => navigateMock,
  useLocation: () => ({ pathname: locationPathname, search: locationSearch, hash: locationHash }),
}))

vi.mock('@/components/ThemeToggle', () => ({
  ThemeToggle: () => <button type="button" aria-label="切换主题" />,
}))

vi.mock('@/lib/utils', async () => {
  const actual = await vi.importActual<typeof import('@/lib/utils')>('@/lib/utils')
  return {
    ...actual,
    openUrlInNewTab: (...args: unknown[]) => openUrlInNewTabMock(...args),
  }
})

vi.mock('@heroui/react', () => ({
  Button: ({ children, onPress, isLoading, 'aria-label': ariaLabel }: { children: React.ReactNode; onPress?: () => void; isLoading?: boolean; 'aria-label'?: string }) => (
    <button onClick={onPress} disabled={isLoading} aria-label={ariaLabel}>{children}</button>
  ),
  Avatar: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
  Dropdown: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownTrigger: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenu: ({
    children,
    onAction,
    'aria-label': ariaLabel,
  }: {
    children: React.ReactNode
    onAction?: (key: string) => void
    'aria-label'?: string
  }) => (
    <div role="menu" aria-label={ariaLabel}>
      {React.Children.map(children, (child) => {
        if (!React.isValidElement(child)) {
          return child
        }

        const rawKey = child.key == null ? undefined : String(child.key)
        const actionKey = rawKey?.startsWith('.$') ? rawKey.slice(2) : rawKey
        return React.cloneElement(child, { __menuActionKey: actionKey, __menuOnAction: onAction })
      })}
    </div>
  ),
  DropdownItem: ({
    children,
    onPress,
    __menuActionKey,
    __menuOnAction,
  }: {
    children: React.ReactNode
    onPress?: () => void
    __menuActionKey?: string
    __menuOnAction?: (key: string) => void
  }) => (
    <button onClick={() => {
      onPress?.()
      if (__menuActionKey) {
        __menuOnAction?.(__menuActionKey)
      }
    }}>{children}</button>
  ),
  addToast: (...args: unknown[]) => mockAddToast(...args),
}))

describe('Header', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useIsAdminMock.mockReturnValue(true)
    openUrlInNewTabMock.mockReturnValue(true)
    logoutMock.mockResolvedValue({ warning: false, message: undefined })
    locationPathname = '/'
    locationSearch = ''
    locationHash = ''
    authEnabled = true
    Object.defineProperty(window, 'confirm', { configurable: true, value: confirmMock })
    confirmMock.mockReturnValue(true)
    useSettingsDraftStore.setState({ hasPendingChanges: false })
  })

  it('refetches active queries when refreshing data', async () => {
    render(<Header />)

    const refreshButton = screen.getByLabelText('刷新数据')
    await act(async () => {
      refreshButton.click()
      await Promise.resolve()
    })

    expect(refetchQueries).toHaveBeenCalledWith({ type: 'active' }, { throwOnError: true })
    expect(mockAddToast).toHaveBeenCalledWith({ title: '数据已刷新', color: 'success' })
  })

  it('shows a danger toast when active query refetch fails', async () => {
    refetchQueries.mockRejectedValueOnce(new Error('refresh failed'))
    render(<Header />)

    const refreshButton = screen.getByLabelText('刷新数据')
    await act(async () => {
      refreshButton.click()
      await Promise.resolve()
    })

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '刷新失败',
      description: '数据刷新失败，请稍后重试。',
      color: 'danger',
    })
  })

  it('shows a fallback danger toast when active query refetch fails with an unknown value', async () => {
    refetchQueries.mockRejectedValueOnce('refresh failed')
    render(<Header />)

    const refreshButton = screen.getByLabelText('刷新数据')
    await act(async () => {
      refreshButton.click()
      await Promise.resolve()
    })

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '刷新失败',
      description: '数据刷新失败，请稍后重试。',
      color: 'danger',
    })
  })

  it('hides settings menu item for non-admin users', () => {
    useIsAdminMock.mockReturnValue(false)
    render(<Header />)

    expect(screen.queryByText('设置')).toBeNull()
    expect(screen.getByText('账户安全')).toBeTruthy()
    expect(screen.getByText('帮助文档')).toBeTruthy()
  })

  it('hides account security when password authentication is disabled', () => {
    authEnabled = false
    render(<Header />)

    expect(screen.queryByText('账户安全')).toBeNull()
  })

  it('uses a localized accessible label for the user menu', () => {
    render(<Header />)

    expect(screen.getByRole('menu', { name: '用户菜单' })).toBeTruthy()
  })

  it('navigates admins to the alerts settings entry from the bell button', () => {
    render(<Header />)

    screen.getByLabelText('提醒设置').click()

    expect(navigateMock).toHaveBeenCalledWith('/system-health#notification-settings')
  })

  it('shows a warning when non-admin users click the alerts settings button', () => {
    useIsAdminMock.mockReturnValue(false)
    render(<Header />)

    screen.getByLabelText('提醒设置').click()

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '系统提醒设置仅管理员可用',
      color: 'warning',
    })
    expect(navigateMock).not.toHaveBeenCalledWith('/system-health#notification-settings')
  })

  it('opens project docs when the help item is clicked', () => {
    render(<Header />)

    screen.getByRole('button', { name: '帮助文档' }).click()

    expect(openUrlInNewTabMock).toHaveBeenCalledWith('https://github.com/seanbao/mnemonas/tree/main/docs')
  })

  it('replaces search history when quick search is submitted from the search page', () => {
    locationPathname = '/search'
    render(<Header />)

    const searchInput = screen.getByLabelText('全局搜索文件')
    fireEvent.change(searchInput, { target: { value: 'report' } })
    fireEvent.keyDown(searchInput, { key: 'Enter' })

    expect(navigateMock).toHaveBeenCalledWith('/search?q=report', { replace: true })
  })

  it('navigates to search when clicking the empty search shell outside the search page', () => {
    render(<Header />)

    fireEvent.click(screen.getByRole('search', { name: '全局搜索' }))

    expect(navigateMock).toHaveBeenCalledWith('/search', { replace: false })
  })

  it('replaces search history when clicking the search shell with a trimmed query on the search page', () => {
    locationPathname = '/search'
    render(<Header />)

    const searchInput = screen.getByLabelText('全局搜索文件')
    fireEvent.change(searchInput, { target: { value: '  quarterly report  ' } })
    fireEvent.click(screen.getByRole('search', { name: '全局搜索' }))

    expect(navigateMock).toHaveBeenCalledWith('/search?q=quarterly%20report', { replace: true })
  })

  it('does not submit search when clicking directly inside the input', () => {
    render(<Header />)

    fireEvent.click(screen.getByLabelText('全局搜索文件'))

    expect(navigateMock).not.toHaveBeenCalled()
  })

  it('shows a warning toast when the browser blocks the docs tab', () => {
    openUrlInNewTabMock.mockReturnValue(false)
    render(<Header />)

    screen.getByRole('button', { name: '帮助文档' }).click()

    expect(mockAddToast).toHaveBeenCalledWith({
      title: '浏览器拦截了新标签页，请允许弹窗后重试',
      color: 'warning',
    })
  })

  it('shows a warning toast when logout succeeds with backend warning metadata', async () => {
    logoutMock.mockResolvedValueOnce({ warning: true, message: 'logout audit write failed' })
    render(<Header />)

    await act(async () => {
      screen.getByText('退出登录').click()
    })

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '已退出登录，但操作记录写入失败',
        color: 'warning',
      })
    })
    expect(clearQueries).toHaveBeenCalledTimes(1)
    expect(navigateMock).toHaveBeenCalledWith('/login', { replace: true })
  })

  it('blocks logout before calling the API when unsaved changes are not discarded', async () => {
    useSettingsDraftStore.setState({ hasPendingChanges: true })
    confirmMock.mockReturnValueOnce(false)
    render(<Header />)

    await act(async () => {
      screen.getByText('退出登录').click()
    })

    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(logoutMock).not.toHaveBeenCalled()
    expect(clearQueries).not.toHaveBeenCalled()
    expect(navigateMock).not.toHaveBeenCalled()
    expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(true)
  })

  it('clears confirmed unsaved changes only after logout succeeds', async () => {
    useSettingsDraftStore.setState({ hasPendingChanges: true })
    render(<Header />)

    await act(async () => {
      screen.getByText('退出登录').click()
    })

    await waitFor(() => expect(logoutMock).toHaveBeenCalledTimes(1))
    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(false)
    expect(navigateMock).toHaveBeenCalledWith('/login', { replace: true })
  })

  it('navigates to settings from the user menu', () => {
    render(<Header />)

    screen.getByRole('button', { name: '设置' }).click()

    expect(navigateMock).toHaveBeenCalledWith('/settings')
  })

  it('opens account security from the user menu', () => {
    locationPathname = '/files'
    locationSearch = '?path=%2Ffamily'
    locationHash = '#recent'
    render(<Header />)

    screen.getByRole('button', { name: '账户安全' }).click()

    expect(navigateMock).toHaveBeenCalledWith('/account/security', {
      state: { returnTo: '/files?path=%2Ffamily#recent' },
    })
  })

  it('clears cached queries when logout succeeds without warnings', async () => {
    render(<Header />)

    await act(async () => {
      screen.getByText('退出登录').click()
    })

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({ title: '已退出登录', color: 'success' })
    })
    expect(clearQueries).toHaveBeenCalledTimes(1)
    expect(navigateMock).toHaveBeenCalledWith('/login', { replace: true })
  })

  it('shows a danger toast when logout fails', async () => {
    logoutMock.mockRejectedValueOnce(new Error('logout refused'))
    render(<Header />)

    await act(async () => {
      screen.getByText('退出登录').click()
    })

    await waitFor(() => {
      expect(mockAddToast).toHaveBeenCalledWith({
        title: '退出登录失败',
        description: '退出登录失败，请稍后重试。',
        color: 'danger',
      })
    })
    expect(clearQueries).not.toHaveBeenCalled()
    expect(navigateMock).not.toHaveBeenCalledWith('/login', { replace: true })
  })

  it('keeps unsaved-change protection when a confirmed logout fails', async () => {
    useSettingsDraftStore.setState({ hasPendingChanges: true })
    logoutMock.mockRejectedValueOnce(new Error('logout refused'))
    render(<Header />)

    await act(async () => {
      screen.getByText('退出登录').click()
    })

    await waitFor(() => expect(logoutMock).toHaveBeenCalledTimes(1))
    expect(confirmMock).toHaveBeenCalledTimes(1)
    expect(useSettingsDraftStore.getState().hasPendingChanges).toBe(true)
    expect(navigateMock).not.toHaveBeenCalledWith('/login', { replace: true })
  })
})
