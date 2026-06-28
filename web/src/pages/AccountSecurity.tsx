import { useEffect, useRef } from 'react'
import { Button, Card, CardBody, CardHeader } from '@heroui/react'
import { KeyRound, LockKeyhole, ShieldCheck } from 'lucide-react'
import { useLocation, useNavigate } from 'react-router-dom'
import { PasswordChangeForm } from '@/components/auth/PasswordChangeForm'
import { PageHeader } from '@/components/ui/PageHeader'
import { useAuthStore, useUser } from '@/stores/auth'

const roleLabels = {
  admin: '管理员',
  user: '普通用户',
  guest: '访客',
} as const

function getAccountSecurityReturnPath(state: unknown): string {
  const returnTo = (
    typeof state === 'object'
    && state !== null
    && 'returnTo' in state
  )
    ? (state as { returnTo?: unknown }).returnTo
    : undefined
  if (typeof returnTo !== 'string' || !returnTo.startsWith('/') || returnTo.startsWith('//')) {
    return '/'
  }

  const pathname = returnTo.split(/[?#]/, 1)[0]
  const normalizedPathname = pathname.replace(/\/+$/, '').toLowerCase() || '/'
  if (
    normalizedPathname === '/login'
    || normalizedPathname === '/account/security'
    || normalizedPathname === '/s'
    || normalizedPathname.startsWith('/s/')
  ) {
    return '/'
  }
  return returnTo
}

export function AccountSecurityPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const user = useUser()
  const authEnabled = useAuthStore((state) => state.authEnabled)
  const pageRef = useRef<HTMLElement | null>(null)
  const returnPath = getAccountSecurityReturnPath(location.state)

  useEffect(() => {
    pageRef.current?.focus()
  }, [])

  if (!authEnabled) {
    return (
      <section
        ref={pageRef}
        tabIndex={-1}
        aria-labelledby="account-security-title"
        className="h-full overflow-auto outline-none custom-scrollbar"
      >
        <div className="mx-auto max-w-3xl p-4 sm:p-6 lg:p-7">
          <PageHeader title="账户安全" titleId="account-security-title" subtitle="管理当前账户的登录凭据" className="mb-8" />
          <Card className="card-mnemonas">
            <CardBody className="items-center py-14 text-center">
              <LockKeyhole className="h-10 w-10 text-default-400" aria-hidden="true" />
              <h2 className="mt-4 text-lg font-semibold text-foreground">密码登录未启用</h2>
              <p className="mt-2 max-w-md text-sm leading-6 text-default-500">
                当前设备未启用 Web 登录认证，因此没有可修改的当前账户密码。
              </p>
              <Button className="mt-6 rounded-lg" color="primary" onPress={() => navigate('/')}>
                返回首页
              </Button>
            </CardBody>
          </Card>
        </div>
      </section>
    )
  }

  if (!user) {
    return (
      <section
        ref={pageRef}
        tabIndex={-1}
        aria-labelledby="account-security-title"
        className="h-full overflow-auto outline-none custom-scrollbar"
      >
        <div className="mx-auto max-w-3xl p-4 sm:p-6 lg:p-7">
          <PageHeader title="账户安全" titleId="account-security-title" subtitle="管理当前账户的登录凭据" className="mb-8" />
          <Card className="card-mnemonas">
            <CardBody className="items-center py-14 text-center">
              <LockKeyhole className="h-10 w-10 text-default-400" aria-hidden="true" />
              <h2 className="mt-4 text-lg font-semibold text-foreground">登录会话不可用</h2>
              <p className="mt-2 max-w-md text-sm leading-6 text-default-500">
                当前登录身份无法确认，密码表单未加载。请重新登录后再修改密码。
              </p>
              <Button className="mt-6 rounded-lg" color="primary" onPress={() => navigate('/login', { replace: true })}>
                重新登录
              </Button>
            </CardBody>
          </Card>
        </div>
      </section>
    )
  }

  return (
    <section
      ref={pageRef}
      tabIndex={-1}
      aria-labelledby="account-security-title"
      className="h-full overflow-auto outline-none custom-scrollbar"
    >
      <div className="mx-auto max-w-3xl p-4 sm:p-6 lg:p-7">
        <PageHeader
          title="账户安全"
          titleId="account-security-title"
          subtitle="修改当前账户的登录密码"
          className="mb-8"
        />

        <Card className="card-mnemonas border border-divider">
          <CardHeader className="flex items-start gap-4 px-5 pb-2 pt-5 sm:px-6 sm:pt-6">
            <span className="flex h-11 w-11 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
              <ShieldCheck size={21} aria-hidden="true" />
            </span>
            <div className="min-w-0 flex-1">
              <h2 className="text-base font-semibold text-foreground">修改登录密码</h2>
              <p className="mt-1 break-anywhere text-sm text-default-500">
                当前账户：{user?.username ?? '未知账户'} · {user ? roleLabels[user.role] : '未知角色'}
              </p>
            </div>
          </CardHeader>
          <CardBody className="px-5 pb-6 pt-4 sm:px-6">
            <div className="mb-6 flex items-start gap-3 rounded-lg border border-warning/25 bg-warning/10 px-4 py-3 text-sm leading-6 text-default-700">
              <KeyRound className="mt-0.5 h-5 w-5 shrink-0 text-warning" aria-hidden="true" />
              <p>
                修改成功后，此账户在所有设备上的登录都会退出。未保存的编辑内容不会保留，请在继续前完成相关操作。
              </p>
            </div>

            <PasswordChangeForm
              key={user.id}
              accountId={user.id}
              onCancel={() => navigate(returnPath, { replace: true })}
              actionsClassName="grid grid-cols-1 gap-2 pt-1 sm:grid-cols-2"
            />
          </CardBody>
        </Card>
      </div>
    </section>
  )
}
