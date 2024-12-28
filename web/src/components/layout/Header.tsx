import { useState, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { Button, Avatar, Dropdown, DropdownTrigger, DropdownMenu, DropdownItem, addToast } from '@heroui/react'
import { Search, Bell, Menu, ChevronRight } from 'lucide-react'
import { useAuthStore, useUser } from '@/stores/auth'

interface HeaderProps {
  onMenuClick?: () => void
}

export function Header({ onMenuClick }: HeaderProps) {
  const navigate = useNavigate()
  const user = useUser()
  const logout = useAuthStore((state) => state.logout)
  const [searchQuery, setSearchQuery] = useState('')

  const handleLogout = async () => {
    await logout()
    addToast({ title: '已退出登录', color: 'success' })
    navigate('/login', { replace: true })
  }

  const handleSettings = () => {
    navigate('/settings')
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
    <header className="h-[60px] bg-bg-primary/80 backdrop-blur-md border-b border-divider flex items-center justify-between px-7 sticky top-0 z-40">
      {/* Left section: Breadcrumb */}
      <div className="flex items-center gap-4">
        <Button
          isIconOnly
          variant="light"
          className="lg:hidden text-text-secondary"
          onPress={onMenuClick}
        >
          <Menu size={20} />
        </Button>
        
        <div className="hidden md:flex items-center gap-2 text-sm">
          <span className="text-text-secondary hover:text-accent-light cursor-pointer transition-colors">记忆宫殿</span>
          <ChevronRight size={14} className="text-text-muted" />
          <span className="text-text-secondary hover:text-accent-light cursor-pointer transition-colors">照片</span>
          <ChevronRight size={14} className="text-text-muted" />
          <span className="text-text-primary font-medium">2025</span>
        </div>
      </div>

      {/* Right section: Search & Actions */}
      <div className="flex items-center gap-3">
        {/* Search - Mnemosyne Style */}
        <div 
          className="hidden sm:flex items-center gap-2.5 px-3.5 py-2 bg-bg-card border border-divider rounded-[10px] w-[260px] focus-within:border-accent-primary focus-within:ring-1 focus-within:ring-accent-primary/20 transition-all duration-200 cursor-pointer"
          onClick={handleSearchClick}
        >
          <Search size={16} className="text-text-muted" />
          <input 
            type="text" 
            placeholder="搜索记忆..." 
            className="flex-1 bg-transparent border-none outline-none text-[13px] text-text-primary placeholder:text-text-muted"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            onKeyDown={handleSearch}
            onClick={(e) => e.stopPropagation()}
          />
          <span className="text-[10px] px-1.5 py-0.5 bg-bg-hover rounded text-text-muted font-mono">⌘K</span>
        </div>

        <div className="h-6 w-[1px] bg-divider mx-1" />

        <Button isIconOnly variant="light" className="w-[38px] h-[38px] min-w-[38px] rounded-[10px] border border-divider bg-bg-card text-text-secondary hover:bg-bg-hover hover:text-text-primary hover:border-divider/50">
          <Bell size={18} />
        </Button>

        <Dropdown placement="bottom-end">
          <DropdownTrigger>
            <button className="w-[38px] h-[38px] rounded-[10px] border border-divider bg-bg-card p-0.5 hover:border-accent-primary/50 transition-colors overflow-hidden">
              <Avatar
                src={avatarUrl}
                className="w-full h-full rounded-lg"
              />
            </button>
          </DropdownTrigger>
          <DropdownMenu 
            aria-label="User menu" 
            className="w-56 bg-bg-card border border-divider rounded-xl shadow-xl"
            itemClasses={{
              base: "data-[hover=true]:bg-bg-hover data-[hover=true]:text-text-primary text-text-secondary",
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
                  <p className="font-semibold text-text-primary">{displayName}</p>
                  <p className="text-xs text-text-muted">{displayEmail}</p>
                </div>
              </div>
            </DropdownItem>
            <DropdownItem key="settings">设置</DropdownItem>
            <DropdownItem key="help">帮助文档</DropdownItem>
            <DropdownItem key="logout" className="text-rose data-[hover=true]:text-rose data-[hover=true]:bg-rose/10">
              退出登录
            </DropdownItem>
          </DropdownMenu>
        </Dropdown>
      </div>
    </header>
  )
}
