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
import { getHealth } from '@/api/files'

function getPostLoginRedirectPath(state: unknown): string {
  const from = (
    typeof state === 'object' &&
    state !== null &&
    'from' in state
  )
    ? (state as { from?: unknown }).from
    : undefined

  if (typeof from !== 'string' || !from.startsWith('/') || from.startsWith('//')) {
    return '/'
  }

  return from
}

const loginWarningTitle = '登录成功，但操作记录写入失败'

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
  const [appVersion, setAppVersion] = useState<string | null>(null)
  const usernameInputId = 'login-username'
  const passwordInputId = 'login-password'
  const displayError = formError ?? error

  // Redirect if already authenticated
  useEffect(() => {
    if (isAuthenticated) {
      const from = getPostLoginRedirectPath(location.state)
      navigate(from, { replace: true })
    }
  }, [isAuthenticated, navigate, location])

  useEffect(() => {
    let cancelled = false
    const controller = new AbortController()

    void getSetupStatus({ signal: controller.signal })
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
      controller.abort()
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    const controller = new AbortController()

    void getHealth({ signal: controller.signal })
      .then((health) => {
        if (!cancelled) {
          setAppVersion(health.version || null)
        }
      })
      .catch(() => {
        if (!cancelled) {
          setAppVersion(null)
        }
      })

    return () => {
      cancelled = true
      controller.abort()
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
        ? { title: loginWarningTitle, color: 'warning' }
        : { title: '登录成功', color: 'success' })
      const from = getPostLoginRedirectPath(location.state)
      navigate(from, { replace: true })
    } catch {
      // Error is exposed by auth store and rendered via displayError.
    }
  }

  return (
    <div className="flex min-h-dvh bg-background">
      {/* Left side - Branding */}
      <div className="login-brand-panel relative hidden overflow-hidden border-r border-white/10 lg:flex lg:w-[44%]">
        <div className="relative z-10 flex w-full flex-col justify-between p-12 text-white">
          <div>
            <div className="mb-8 flex h-14 w-14 items-center justify-center rounded-lg bg-white/10 ring-1 ring-white/15">
              <HardDrive className="h-7 w-7" />
            </div>
            <h1 className="text-4xl font-semibold">MnemoNAS</h1>
            <p className="mt-3 max-w-sm text-base leading-7 text-white/70">面向家庭和小团队的自托管私有云存储。</p>
          </div>

          <div className="max-w-md divide-y divide-white/10 border-y border-white/10 text-white/70">
            <div className="flex items-center gap-3 py-4">
              <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-white/10">
                <Shield className="h-4 w-4" />
              </div>
              <span>原生文件 + CAS 版本历史，校验关键数据</span>
            </div>
            <div className="flex items-center gap-3 py-4">
              <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-white/10">
                <Clock className="h-4 w-4" />
              </div>
              <span>版本与回收站保留关键恢复入口</span>
            </div>
            <div className="flex items-center gap-3 py-4">
              <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-white/10">
                <LogIn className="h-4 w-4" />
              </div>
              <span>支持 Linux 主机、容器和局域网部署</span>
            </div>
          </div>

          <div className="text-sm text-white/60">
            {appVersion ? `MnemoNAS ${appVersion}` : 'MnemoNAS'} · 自托管文件管理
          </div>
        </div>
      </div>

      {/* Right side - Login form */}
      <div className="flex w-full items-center justify-center px-5 py-8 sm:p-8 lg:w-[56%]">
        <div className="w-full max-w-md">
          {/* Mobile logo */}
          <div className="mb-8 text-center lg:hidden">
            <div className="gradient-mnemonas mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-lg">
              <HardDrive className="h-8 w-8 text-white" />
            </div>
            <h1 className="text-2xl font-bold">MnemoNAS</h1>
            <p className="text-default-500">自托管私有云存储</p>
          </div>

          <Card className="card-mnemonas">
            <CardBody className="p-6 sm:p-8">
              <div className="mb-8 text-center">
                <h2 className="text-2xl font-bold">欢迎回来</h2>
                <p className="text-default-500 mt-2">登录后管理文件、版本与分享</p>
              </div>

              {displayError && (
                <div role="alert" className="rounded-lg border border-danger/20 bg-danger/5 px-4 py-3 text-sm text-danger">
                  {displayError}
                </div>
              )}

              <form onSubmit={handleSubmit} noValidate className="space-y-6">
                <div>
                  <label htmlFor={usernameInputId} className="text-sm font-medium text-default-600 mb-1.5 block">用户名</label>
                  <Input
                    id={usernameInputId}
                    aria-label="用户名"
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
                    aria-label="密码"
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
                  忘记密码？请在服务器上按照文档重置管理员密码。
                </div>

                <Button
                  type="submit"
                  className="btn-primary w-full rounded-lg"
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
                <p className="mb-2 text-sm font-medium">登录帮助</p>
                <div className="text-default-500 space-y-1 text-xs">
                  {isFirstRun === true ? (
                    <>
                      <p>首次运行默认管理员账号为 <span className="font-mono text-accent-primary">admin</span></p>
                      <p>初始密码位于服务器上的 initial-password.txt，浏览器不会显示明文密码。</p>
                    </>
                  ) : isFirstRun === false ? (
                    <>
                      <p>使用已配置的管理员或用户账号登录。</p>
                      <p>初始密码只会写入服务器端文件，不会在浏览器里显示。</p>
                    </>
                  ) : (
                    <>
                      <p>使用管理员或已有账号登录。</p>
                      <p>首次启动凭据只写入服务器端 initial-password.txt。</p>
                    </>
                  )}
                </div>
              </div>
            </CardBody>
          </Card>

          <p className="text-default-500 mt-6 text-center text-sm">
            MnemoNAS · 开源自托管文件存储
          </p>
        </div>
      </div>
    </div>
  )
}
