import { useState, useCallback } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { Button, Avatar, Dropdown, DropdownTrigger, DropdownMenu, DropdownItem, addToast } from '@heroui/react'
import { Search, Bell, Menu, RefreshCw } from 'lucide-react'
import { useAuthStore, useIsAdmin, useUser } from '@/stores/auth'
import { useQueryClient } from '@tanstack/react-query'
import { ThemeToggle } from '@/components/ThemeToggle'
import { openUrlInNewTab } from '@/lib/utils'

interface HeaderProps {
  onMenuClick?: () => void
}

const HELP_DOCS_URL = 'https://github.com/seanbao/mnemonas/tree/main/docs'
const logoutWarningTitle = '已退出登录，但操作记录写入失败'
const logoutFailureDescription = '退出登录失败，请稍后重试。'
const refreshFailureDescription = '数据刷新失败，请稍后重试。'

export function Header({ onMenuClick }: HeaderProps) {
  const navigate = useNavigate()
  const location = useLocation()
  const user = useUser()
  const isAdmin = useIsAdmin()
  const logout = useAuthStore((state) => state.logout)
  const queryClient = useQueryClient()
  const [searchQuery, setSearchQuery] = useState('')
  const [isRefreshing, setIsRefreshing] = useState(false)

  const handleLogout = async () => {
    try {
      const result = await logout()
      queryClient.clear()
      addToast(result.warning
        ? { title: logoutWarningTitle, color: 'warning' }
        : { title: '已退出登录', color: 'success' })
      navigate('/login', { replace: true })
    } catch {
      addToast({
        title: '退出登录失败',
        description: logoutFailureDescription,
        color: 'danger',
      })
    }
  }

  const handleSettings = () => {
    navigate('/settings')
  }

  const handleHelp = useCallback(() => {
    if (!openUrlInNewTab(HELP_DOCS_URL)) {
      addToast({ title: '浏览器拦截了新标签页，请允许弹窗后重试', color: 'warning' })
    }
  }, [])

  const handleAlertsSettings = useCallback(() => {
    if (!isAdmin) {
      addToast({ title: '系统提醒设置仅管理员可用', color: 'warning' })
      return
    }

    navigate('/settings?tab=advanced')
  }, [isAdmin, navigate])

  const handleRefresh = async () => {
    setIsRefreshing(true)
    try {
      await queryClient.refetchQueries({ type: 'active' }, { throwOnError: true })
      addToast({ title: '数据已刷新', color: 'success' })
    } catch {
      addToast({
        title: '刷新失败',
        description: refreshFailureDescription,
        color: 'danger',
      })
    } finally {
      setIsRefreshing(false)
    }
  }

  const handleSearch = useCallback((e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' && searchQuery.trim()) {
      navigate(`/search?q=${encodeURIComponent(searchQuery.trim())}`, { replace: location.pathname === '/search' })
    }
  }, [location.pathname, navigate, searchQuery])

  const handleSearchClick = useCallback(() => {
    if (searchQuery.trim()) {
      navigate(`/search?q=${encodeURIComponent(searchQuery.trim())}`, { replace: location.pathname === '/search' })
    } else {
      navigate('/search', { replace: location.pathname === '/search' })
    }
  }, [location.pathname, navigate, searchQuery])

  const displayName = user?.username || '访客'
  const displayEmail = user?.email || 'guest@local'
  const avatarName = displayName.slice(0, 2).toUpperCase()

  return (
    <header className="header-surface sticky top-0 z-30 flex h-16 shrink-0 items-center justify-between border-b border-divider px-3 sm:px-4 lg:px-8">
      {/* Left section */}
      <div className="flex min-w-0 items-center gap-3">
        <button
          onClick={onMenuClick}
          className="rounded-lg p-2 hover:bg-content2 lg:hidden"
          aria-label="打开导航菜单"
        >
          <Menu size={20} className="text-default-600" />
        </button>
        <div className="min-w-0 sm:hidden">
          <h1 className="truncate text-base font-semibold">MnemoNAS</h1>
        </div>
        <div className="hidden sm:block">
          <h1 className="text-lg font-semibold">私有云存储</h1>
          <p className="text-muted-foreground text-xs">文件、版本与分享，日常管理</p>
        </div>
      </div>

      {/* Right section: Search & Actions */}
      <div className="flex shrink-0 items-center gap-1.5 sm:gap-2">
        <div 
          className="input-shell hidden w-[240px] cursor-pointer items-center gap-2 rounded-lg px-3 py-2 transition-all duration-200 focus-within:border-accent-primary focus-within:ring-2 focus-within:ring-accent-primary/15 md:flex lg:w-[280px]"
          onClick={handleSearchClick}
        >
          <Search size={16} className="text-default-500 shrink-0" />
          <input 
            type="text" 
            placeholder="搜索文件"
            className="flex-1 bg-transparent border-none outline-none text-[13px] text-foreground placeholder:text-default-500"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            onKeyDown={handleSearch}
            onClick={(e) => e.stopPropagation()}
          />
        </div>

        <Button
          isIconOnly
          variant="light"
          size="sm"
          onPress={handleRefresh}
          isLoading={isRefreshing}
          aria-label="刷新数据"
          className="rounded-lg"
        >
          <RefreshCw size={18} className={isRefreshing ? 'animate-spin' : ''} />
        </Button>

        <Button isIconOnly variant="light" size="sm" aria-label="提醒设置" onPress={handleAlertsSettings} className="hidden rounded-lg sm:inline-flex">
          <Bell size={18} />
        </Button>

        <ThemeToggle />

        <Dropdown placement="bottom-end">
          <DropdownTrigger>
            <button
              className="h-9 w-9 overflow-hidden rounded-lg border border-divider bg-content1 p-0.5 transition-colors hover:border-accent-primary/50"
              aria-label="打开用户菜单"
            >
              <Avatar
                name={avatarName}
                className="h-full w-full rounded-md bg-primary/10 text-primary"
              />
            </button>
          </DropdownTrigger>
          <DropdownMenu 
            aria-label="User menu" 
            className="w-56 rounded-lg border border-divider bg-content1 shadow-xl"
            itemClasses={{
              base: "data-[hover=true]:bg-content2 data-[hover=true]:text-foreground text-default-600",
            }}
            onAction={(key) => {
              if (key === 'logout') handleLogout()
              if (key === 'settings') handleSettings()
            }}
          >
            <DropdownItem key="profile" className="h-14 gap-2" textValue="Profile">
              <div className="flex items-center gap-3">
                <Avatar
                  name={avatarName}
                  className="h-10 w-10 bg-primary/10 text-primary"
                />
                <div>
                  <p className="font-semibold text-foreground">{displayName}</p>
                  <p className="text-xs text-default-500">{displayEmail}</p>
                </div>
              </div>
            </DropdownItem>
            {isAdmin ? <DropdownItem key="settings">设置</DropdownItem> : null}
            <DropdownItem key="help" onPress={handleHelp}>帮助文档</DropdownItem>
            <DropdownItem key="logout" className="text-rose data-[hover=true]:text-rose data-[hover=true]:bg-rose/10">
              退出登录
            </DropdownItem>
          </DropdownMenu>
        </Dropdown>
      </div>
    </header>
  )
}
