import { useState, useEffect } from 'react'
import { useNavigate, useLocation } from 'react-router-dom'
import { 
  Card, 
  CardBody,
  Button,
  Input,
  addToast,
} from '@heroui/react'
import { 
  Lock,
  User,
  LogIn,
  HardDrive,
} from 'lucide-react'
import { useAuthStore, useIsAuthenticated } from '@/stores/auth'

export function LoginPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const isAuthenticated = useIsAuthenticated()
  const { login, error, isLoading, clearError, initialize } = useAuthStore()
  
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')

  // Initialize auth state on mount
  useEffect(() => {
    initialize()
  }, [initialize])

  // Redirect if already authenticated
  useEffect(() => {
    if (isAuthenticated) {
      const from = (location.state as { from?: string })?.from || '/'
      navigate(from, { replace: true })
    }
  }, [isAuthenticated, navigate, location])

  // Show error toast
  useEffect(() => {
    if (error) {
      addToast({ title: error, color: 'danger' })
      clearError()
    }
  }, [error, clearError])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    
    if (!username.trim() || !password.trim()) {
      addToast({ title: '请输入用户名和密码', color: 'warning' })
      return
    }

    const success = await login(username, password)
    if (success) {
      addToast({ title: '登录成功', color: 'success' })
      const from = (location.state as { from?: string })?.from || '/'
      navigate(from, { replace: true })
    }
  }

  return (
    <div className="min-h-screen bg-bg-primary flex items-center justify-center p-4">
      {/* Background decoration */}
      <div className="absolute inset-0 overflow-hidden pointer-events-none">
        <div className="absolute top-1/4 left-1/4 w-96 h-96 bg-accent-primary/10 rounded-full blur-3xl" />
        <div className="absolute bottom-1/4 right-1/4 w-80 h-80 bg-accent-dark/10 rounded-full blur-3xl" />
      </div>

      <Card className="w-full max-w-md bg-bg-card border border-divider shadow-xl relative">
        <CardBody className="p-8">
          {/* Logo and title */}
          <div className="text-center mb-8">
            <div className="w-16 h-16 mx-auto rounded-2xl bg-gradient-to-br from-accent-primary to-accent-dark flex items-center justify-center shadow-lg mb-4">
              <HardDrive size={32} className="text-white" />
            </div>
            <h1 className="text-2xl font-bold text-text-primary">MnemoNAS</h1>
            <p className="text-sm text-text-muted mt-1">登录以访问您的文件</p>
          </div>

          {/* Login form */}
          <form onSubmit={handleSubmit} className="space-y-6">
            <Input
              label="用户名"
              placeholder="请输入用户名"
              value={username}
              onValueChange={setUsername}
              isDisabled={isLoading}
              autoComplete="username"
              startContent={<User size={16} className="text-text-muted" />}
              classNames={{ 
                inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary",
                label: "text-text-secondary",
              }}
            />
            
            <Input
              label="密码"
              type="password"
              placeholder="请输入密码"
              value={password}
              onValueChange={setPassword}
              isDisabled={isLoading}
              autoComplete="current-password"
              startContent={<Lock size={16} className="text-text-muted" />}
              classNames={{ 
                inputWrapper: "bg-bg-secondary border-divider group-data-[focus=true]:border-accent-primary",
                label: "text-text-secondary",
              }}
            />

            <Button
              type="submit"
              className="w-full bg-gradient-to-br from-accent-primary to-accent-dark text-white shadow-[0_4px_12px_rgba(167,139,250,0.4)]"
              size="lg"
              isLoading={isLoading}
              startContent={!isLoading && <LogIn size={18} />}
            >
              {isLoading ? '登录中...' : '登录'}
            </Button>
          </form>

          {/* Hints */}
          <div className="mt-8 pt-6 border-t border-divider">
            <div className="text-xs text-text-muted text-center space-y-1">
              <p>首次运行时，默认管理员账号为 <code className="text-accent-primary">admin</code></p>
              <p>初始密码请查看服务器启动日志</p>
            </div>
          </div>
        </CardBody>
      </Card>
    </div>
  )
}
