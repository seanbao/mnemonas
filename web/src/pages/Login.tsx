import { useState, useEffect } from 'react'
import { useNavigate, useLocation } from 'react-router-dom'
import { 
  Card, 
  CardBody,
  Button,
  Input,
  Divider,
  addToast,
} from '@heroui/react'
import { 
  Lock,
  User,
  LogIn,
  HardDrive,
  Shield,
  Clock,
  Eye,
  EyeOff,
} from 'lucide-react'
import { useAuthStore, useIsAuthenticated } from '@/stores/auth'

export function LoginPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const isAuthenticated = useIsAuthenticated()
  const { login, error, isLoading, clearError, initialize } = useAuthStore()
  
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [rememberMe, setRememberMe] = useState(false)

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
    <div className="flex min-h-screen">
      {/* Left side - Branding */}
      <div className="gradient-meridian-hero relative hidden overflow-hidden lg:flex lg:w-1/2">
        {/* Subtle gradient overlay */}
        <div className="absolute inset-0 bg-gradient-to-br from-white/5 via-transparent to-black/10" />

        {/* Animated decorative shapes */}
        <div className="absolute top-20 left-20 h-64 w-64 animate-pulse rounded-full bg-white/10 blur-3xl" />
        <div
          className="absolute right-20 bottom-20 h-96 w-96 animate-pulse rounded-full bg-white/5 blur-3xl"
          style={{ animationDelay: '1s' }}
        />
        <div className="absolute top-1/2 left-1/3 h-48 w-48 rounded-full bg-purple-400/10 blur-2xl" />

        {/* Content */}
        <div className="relative z-10 flex w-full flex-col items-center justify-center p-12 text-white">
          <div className="mb-8">
            <div className="mb-6 flex h-20 w-20 items-center justify-center rounded-2xl bg-white/20 backdrop-blur">
              <HardDrive className="h-10 w-10" />
            </div>
          </div>

          <h1 className="mb-4 text-4xl font-bold">MnemoNAS</h1>
          <p className="mb-8 text-xl text-white/80">您的私有云存储空间</p>

          <div className="max-w-md space-y-4 text-white/70">
            <div className="flex items-center gap-3">
              <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-white/10">
                <Shield className="h-4 w-4" />
              </div>
              <span>CAS 内容寻址存储，数据完整性保障</span>
            </div>
            <div className="flex items-center gap-3">
              <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-white/10">
                <Clock className="h-4 w-4" />
              </div>
              <span>时光回溯，任意时间点数据恢复</span>
            </div>
            <div className="flex items-center gap-3">
              <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-white/10">
                <LogIn className="h-4 w-4" />
              </div>
              <span>数据在自己手里，体验不输云服务</span>
            </div>
          </div>

          <div className="absolute bottom-8 text-sm text-white/70">
            MnemoNAS v0.1.0 · 记忆宫殿，永不遗忘
          </div>
        </div>
      </div>

      {/* Right side - Login form */}
      <div className="bg-white dark:bg-zinc-900 flex w-full items-center justify-center p-8 lg:w-1/2">
        <div className="w-full max-w-md">
          {/* Mobile logo */}
          <div className="mb-8 text-center lg:hidden">
            <div className="gradient-meridian mx-auto mb-4 flex h-16 w-16 items-center justify-center rounded-2xl">
              <HardDrive className="h-8 w-8 text-white" />
            </div>
            <h1 className="text-2xl font-bold">MnemoNAS</h1>
            <p className="text-default-500">您的私有云存储空间</p>
          </div>

          <Card className="card-meridian">
            <CardBody className="p-8">
              <div className="mb-8 text-center">
                <h2 className="text-2xl font-bold">欢迎回来</h2>
                <p className="text-default-500 mt-2">请登录以继续访问系统</p>
              </div>

              <form onSubmit={handleSubmit} className="space-y-6">
                <div>
                  <label className="text-sm font-medium text-default-600 mb-1.5 block">用户名</label>
                  <Input
                    placeholder="请输入用户名"
                    value={username}
                    onValueChange={setUsername}
                    isDisabled={isLoading}
                    autoComplete="username"
                    variant="bordered"
                    size="lg"
                    isRequired
                    startContent={<User size={18} className="text-default-400 shrink-0" />}
                    classNames={{
                      inputWrapper: "border-divider hover:border-accent-primary/50",
                    }}
                  />
                </div>
                
                <div>
                  <label className="text-sm font-medium text-default-600 mb-1.5 block">密码</label>
                  <Input
                    type={showPassword ? 'text' : 'password'}
                    placeholder="请输入密码"
                    value={password}
                    onValueChange={setPassword}
                    isDisabled={isLoading}
                    autoComplete="current-password"
                    variant="bordered"
                    size="lg"
                    isRequired
                    startContent={<Lock size={18} className="text-default-400 shrink-0" />}
                    endContent={
                      <button
                        type="button"
                        onClick={() => setShowPassword(!showPassword)}
                        className="focus:outline-none"
                      >
                        {showPassword ? (
                          <EyeOff className="text-default-400 h-4 w-4" />
                        ) : (
                          <Eye className="text-default-400 h-4 w-4" />
                        )}
                      </button>
                    }
                    classNames={{
                      inputWrapper: "border-divider hover:border-accent-primary/50",
                    }}
                  />
                </div>

                <div className="flex items-center justify-between">
                  <label className="flex items-center gap-2 cursor-pointer group">
                    <input
                      type="checkbox"
                      className="w-4 h-4 rounded-lg border-divider text-accent-primary focus:ring-accent-primary/20 transition-colors cursor-pointer"
                      checked={rememberMe}
                      onChange={(e) => setRememberMe(e.target.checked)}
                    />
                    <span className="text-sm text-default-600 group-hover:text-foreground transition-colors">记住登录状态</span>
                  </label>
                  <Button variant="light" size="sm" className="text-accent-primary rounded-xl" isDisabled>
                    忘记密码？
                  </Button>
                </div>

                <Button
                  type="submit"
                  className="w-full btn-primary rounded-xl"
                  size="lg"
                  isLoading={isLoading}
                  startContent={!isLoading && <LogIn className="h-4 w-4" />}
                >
                  登录
                </Button>
              </form>

              <Divider className="my-6" />

              {/* Hints */}
              <div className="bg-content2/50 rounded-lg p-4">
                <p className="mb-2 text-sm font-medium">默认账户</p>
                <div className="text-default-500 space-y-1 text-xs">
                  <p>首次运行时默认管理员账号为 <span className="font-mono text-accent-primary">admin</span></p>
                  <p>初始密码请查看服务器启动日志</p>
                </div>
              </div>
            </CardBody>
          </Card>

          <p className="text-default-500 mt-6 text-center text-sm">
            © 2026 MnemoNAS. All rights reserved.
          </p>
        </div>
      </div>
    </div>
  )
}
