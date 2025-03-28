import { useState, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
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

export function Header({ onMenuClick }: HeaderProps) {
  const navigate = useNavigate()
  const user = useUser()
  const isAdmin = useIsAdmin()
  const logout = useAuthStore((state) => state.logout)
  const queryClient = useQueryClient()
  const [searchQuery, setSearchQuery] = useState('')
  const [isRefreshing, setIsRefreshing] = useState(false)

  const handleLogout = async () => {
    await logout()
    addToast({ title: '已退出登录', color: 'success' })
    navigate('/login', { replace: true })
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
      await queryClient.invalidateQueries()
      addToast({ title: '数据已刷新', color: 'success' })
    } catch (error) {
      addToast({
        title: '刷新失败',
        description: error instanceof Error ? error.message : '数据刷新失败，请稍后重试。',
        color: 'danger',
      })
    } finally {
      setIsRefreshing(false)
    }
  }

  const handleSearch = useCallback((e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter' && searchQuery.trim()) {
      navigate(`/search?q=${encodeURIComponent(searchQuery.trim())}`)
    }
  }, [searchQuery, navigate])

  const handleSearchClick = useCallback(() => {
    if (searchQuery.trim()) {
      navigate(`/search?q=${encodeURIComponent(searchQuery.trim())}`)
    } else {
      navigate('/search')
    }
  }, [searchQuery, navigate])

  // Generate avatar URL based on username
  const avatarUrl = user?.username 
    ? `https://api.dicebear.com/7.x/avataaars/svg?seed=${user.username}`
    : 'https://api.dicebear.com/7.x/avataaars/svg?seed=guest'

  const displayName = user?.username || '访客'
  const displayEmail = user?.email || 'guest@local'

  return (
    <header className="h-16 border-b border-divider flex items-center justify-between px-4 lg:px-8 sticky top-0 z-40 glass">
      {/* Left section */}
      <div className="flex items-center gap-4">
        <button
          onClick={onMenuClick}
          className="p-2 rounded-lg hover:bg-content2 lg:hidden"
          aria-label="打开导航菜单"
        >
          <Menu size={20} className="text-default-600" />
        </button>
        <div className="hidden sm:block">
          <h1 className="text-lg font-semibold">私有云存储</h1>
          <p className="text-muted-foreground text-xs">数据在自己手里，体验不输云服务</p>
        </div>
      </div>

      {/* Right section: Search & Actions */}
      <div className="flex items-center gap-2">
        {/* Search - Mnemosyne Style */}
        <div 
          className="hidden sm:flex items-center gap-2 px-3 py-2 glass rounded-xl w-[240px] focus-within:border-accent-primary focus-within:ring-2 focus-within:ring-accent-primary/15 transition-all duration-200 cursor-pointer border border-transparent"
          onClick={handleSearchClick}
        >
          <Search size={16} className="text-default-500 shrink-0" />
          <input 
            type="text" 
            placeholder="搜索文件与记忆" 
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
          className="rounded-xl"
        >
          <RefreshCw size={18} className={isRefreshing ? 'animate-spin' : ''} />
        </Button>

        <Button isIconOnly variant="light" size="sm" aria-label="系统提醒设置" onPress={handleAlertsSettings} className="rounded-xl">
          <Bell size={18} />
        </Button>

        <ThemeToggle />

        <Dropdown placement="bottom-end">
          <DropdownTrigger>
            <button className="w-9 h-9 rounded-xl border border-divider glass p-0.5 hover:border-accent-primary/50 transition-colors overflow-hidden">
              <Avatar
                src={avatarUrl}
                className="w-full h-full rounded-lg"
              />
            </button>
          </DropdownTrigger>
          <DropdownMenu 
            aria-label="User menu" 
            className="w-56 bg-content1 border border-divider rounded-xl shadow-xl"
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
                  src={avatarUrl}
                  className="w-10 h-10"
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
