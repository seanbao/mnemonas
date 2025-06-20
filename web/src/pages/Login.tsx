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
import { getSetupStatus } from '@/api/setup'

export function LoginPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const isAuthenticated = useIsAuthenticated()
  const { login, error, isLoading, clearError } = useAuthStore()
  
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [isFirstRun, setIsFirstRun] = useState<boolean | null>(null)
  const usernameInputId = 'login-username'
  const passwordInputId = 'login-password'
  const displayError = formError ?? error

  // Redirect if already authenticated
  useEffect(() => {
    if (isAuthenticated) {
      const from = (location.state as { from?: string })?.from || '/'
      navigate(from, { replace: true })
    }
  }, [isAuthenticated, navigate, location])

  useEffect(() => {
    let cancelled = false

    void getSetupStatus()
      .then((status) => {
        if (!cancelled) {
          setIsFirstRun(status.is_first_run)
        }
      })
      .catch(() => {
        if (!cancelled) {
          setIsFirstRun(null)
        }
      })

    return () => {
      cancelled = true
    }
  }, [])

  const handleUsernameChange = (value: string) => {
    setUsername(value)
    if (formError) {
      setFormError(null)
    }
    if (error) {
      clearError()
    }
  }

  const handlePasswordChange = (value: string) => {
    setPassword(value)
    if (formError) {
      setFormError(null)
    }
    if (error) {
      clearError()
    }
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    const trimmedUsername = username.trim()
    
    if (!trimmedUsername || !password.trim()) {
      setFormError('请输入用户名和密码')
      return
    }

    try {
      setFormError(null)
      const result = await login(trimmedUsername, password)
      clearError()
      addToast(result.warning
        ? { title: result.message ?? '登录成功，但活动日志写入失败', color: 'warning' }
        : { title: '登录成功', color: 'success' })
      const from = (location.state as { from?: string })?.from || '/'
      navigate(from, { replace: true })
    } catch {
      // Error is exposed by auth store and rendered via displayError.
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

              {displayError && (
                <div role="alert" className="rounded-xl border border-danger/20 bg-danger/5 px-4 py-3 text-sm text-danger">
                  {displayError}
                </div>
              )}

              <form onSubmit={handleSubmit} noValidate className="space-y-6">
                <div>
                  <label htmlFor={usernameInputId} className="text-sm font-medium text-default-600 mb-1.5 block">用户名</label>
                  <Input
                    id={usernameInputId}
                    placeholder="请输入用户名"
                    value={username}
                    onValueChange={handleUsernameChange}
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
                  <label htmlFor={passwordInputId} className="text-sm font-medium text-default-600 mb-1.5 block">密码</label>
                  <Input
                    id={passwordInputId}
                    type={showPassword ? 'text' : 'password'}
                    placeholder="请输入密码"
                    value={password}
                    onValueChange={handlePasswordChange}
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
                        aria-label={showPassword ? '隐藏密码' : '显示密码'}
                        aria-pressed={showPassword}
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

                <div className="rounded-lg bg-content2/40 px-3 py-2 text-xs text-default-500">
                  当前版本未提供浏览器内密码重置入口。管理员密码重置需通过服务端配置或运维流程完成。
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
                <p className="mb-2 text-sm font-medium">登录说明</p>
                <div className="text-default-500 space-y-1 text-xs">
                  {isFirstRun === true ? (
                    <>
                      <p>首次运行时默认管理员账号为 <span className="font-mono text-accent-primary">admin</span></p>
                      <p>初始密码请查看服务器启动日志，浏览器界面不显示初始密码</p>
                    </>
                  ) : isFirstRun === false ? (
                    <>
                      <p>使用已配置的管理员或用户账户登录。</p>
                      <p>首次启动时生成的初始密码不会在浏览器界面再次显示。</p>
                    </>
                  ) : (
                    <>
                      <p>使用管理员或已有账户登录。</p>
                      <p>首次启动凭据仅记录在服务端日志中，浏览器界面不显示初始密码。</p>
                    </>
                  )}
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
