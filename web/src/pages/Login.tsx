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

    try {
      await login(username, password)
      addToast({ title: '登录成功', color: 'success' })
      const from = (location.state as { from?: string })?.from || '/'
      navigate(from, { replace: true })
    } catch {
      // Error is handled by auth store and shown via error useEffect
    }
  }

  return (
    <div className="min-h-screen relative flex items-center justify-center p-4 bg-background overflow-hidden app-shell">
      {/* Background decoration */}
      <div className="absolute inset-0 z-0 overflow-hidden pointer-events-none">
        <div className="absolute -top-[20%] -left-[10%] w-[50%] h-[50%] rounded-full bg-primary/5 blur-[120px]" />
        <div className="absolute -bottom-[20%] -right-[10%] w-[50%] h-[50%] rounded-full bg-secondary/5 blur-[120px]" />
      </div>

      <Card className="w-full max-w-md card-meridian backdrop-blur-xl border border-divider/60 shadow-2xl relative z-10">
        <CardBody className="p-8">
          {/* Logo and title */}
          <div className="text-center mb-10">
            <div className="w-16 h-16 mx-auto rounded-2xl bg-gradient-to-br from-primary to-secondary flex items-center justify-center shadow-lg mb-6 transform transition-transform hover:scale-105 duration-500 logo-glow">
              <HardDrive size={32} className="text-white" />
            </div>
            <h1 className="text-2xl font-bold text-gradient-meridian">MnemoNAS</h1>
            <p className="text-default-500 text-sm mt-2">您的私有云存储空间</p>
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
              variant="bordered"
              radius="lg"
              startContent={<User size={18} className="text-default-400 shrink-0" />}
              classNames={{
                base: "gap-1",
                inputWrapper: "input-shell bg-content2/80 border-divider/70 hover:bg-content2 transition-colors gap-2 px-3",
                innerWrapper: "gap-2",
                input: "pl-0",
                label: "text-default-500",
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
              variant="bordered"
              radius="lg"
              startContent={<Lock size={18} className="text-default-400 shrink-0" />}
              classNames={{
                base: "gap-1",
                inputWrapper: "input-shell bg-content2/80 border-divider/70 hover:bg-content2 transition-colors gap-2 px-3",
                innerWrapper: "gap-2",
                input: "pl-0",
                label: "text-default-500",
              }}
            />

            <Button
              type="submit"
              color="primary"
              className="w-full font-medium shadow-lg shadow-primary/20"
              size="lg"
              radius="lg"
              isLoading={isLoading}
              startContent={!isLoading && <LogIn size={18} />}
            >
              {isLoading ? '登录中...' : '登录'}
            </Button>
          </form>

          {/* Hints */}
          <div className="mt-8 pt-6 border-t border-divider/50">
            <div className="bg-default-100/50 rounded-lg p-3 text-xs text-default-500 text-center space-y-1">
              <p>首次运行时默认管理员账号为 <span className="font-mono text-primary">admin</span></p>
              <p>初始密码请查看服务器启动日志</p>
            </div>
          </div>
        </CardBody>
      </Card>
    </div>
  )
}
