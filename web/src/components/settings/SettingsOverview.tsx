import {
  ArrowRight,
  BellRing,
  Cable,
  FolderLock,
  History,
  Share2,
  ShieldCheck,
  type LucideIcon,
} from 'lucide-react'

export type SettingsDestination = 'general' | 'retention' | 'webdav' | 'device-care' | 'shares' | 'users-access'

interface SettingsOverviewProps {
  trashEnabled: boolean
  webdavEnabled: boolean
  webdavAuthType: string
  shareEnabled: boolean
  alertsEnabled: boolean
  diskHealthEnabled: boolean
  scrubScheduleEnabled: boolean
  onNavigate: (destination: SettingsDestination) => void
}

type StatusTone = 'success' | 'warning' | 'neutral'

interface SettingsTask {
  destination: SettingsDestination
  title: string
  description: string
  status: string
  tone: StatusTone
  icon: LucideIcon
}

const statusToneClass: Record<StatusTone, string> = {
  success: 'border-success/25 bg-success/10 text-success',
  warning: 'border-warning/25 bg-warning/10 text-warning',
  neutral: 'border-divider bg-content2 text-default-600',
}

function formatWebDAVStatus(enabled: boolean, authType: string): string {
  if (!enabled) {
    return '未启用'
  }

  if (authType === 'users') {
    return '用户账号认证'
  }
  if (authType === 'basic') {
    return '独立凭据'
  }
  if (authType === 'none') {
    return '匿名访问'
  }
  return '已启用'
}

function getWebDAVTone(enabled: boolean, authType: string): StatusTone {
  if (!enabled) {
    return 'neutral'
  }
  return authType === 'none' ? 'warning' : 'success'
}

function formatProtectionStatus(trashEnabled: boolean): string {
  return trashEnabled ? '回收站已开启' : '删除将直接生效'
}

function formatDeviceCareStatus(alertsEnabled: boolean, diskHealthEnabled: boolean, scrubScheduleEnabled: boolean): string {
  const enabledCount = [alertsEnabled, diskHealthEnabled, scrubScheduleEnabled].filter(Boolean).length
  if (enabledCount === 0) {
    return '尚未启用主动照看'
  }
  if (enabledCount === 3) {
    return '三项主动照看已启用'
  }
  return `${enabledCount} / 3 项已启用`
}

function SettingsTaskCard({ task, onNavigate }: { task: SettingsTask; onNavigate: (destination: SettingsDestination) => void }) {
  const Icon = task.icon

  return (
    <button
      type="button"
      className="group flex min-h-44 w-full flex-col rounded-lg border border-divider bg-content1 p-5 text-left shadow-[var(--shadow-soft)] transition-[border-color,box-shadow,transform] duration-200 hover:-translate-y-0.5 hover:border-primary/30 hover:shadow-[var(--shadow-medium)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/35"
      onClick={() => onNavigate(task.destination)}
      aria-label={`${task.title}：${task.description}`}
    >
      <div className="flex w-full items-start justify-between gap-4">
        <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
          <Icon size={20} aria-hidden="true" />
        </span>
        <span className={`rounded-full border px-2.5 py-1 text-xs font-medium ${statusToneClass[task.tone]}`}>
          {task.status}
        </span>
      </div>
      <span className="mt-5 text-base font-semibold text-foreground">{task.title}</span>
      <span className="mt-1.5 flex-1 text-sm leading-6 text-default-500">{task.description}</span>
      <span className="mt-4 inline-flex items-center gap-1.5 text-sm font-medium text-primary">
        打开
        <ArrowRight size={15} className="transition-transform group-hover:translate-x-0.5" aria-hidden="true" />
      </span>
    </button>
  )
}

export function SettingsOverview({
  trashEnabled,
  webdavEnabled,
  webdavAuthType,
  shareEnabled,
  alertsEnabled,
  diskHealthEnabled,
  scrubScheduleEnabled,
  onNavigate,
}: SettingsOverviewProps) {
  const tasks: SettingsTask[] = [
    {
      destination: 'general',
      title: '账户与远程访问',
      description: '检查登录会话、HTTPS 和公网访问边界。',
      status: '安全与连接',
      tone: 'neutral',
      icon: ShieldCheck,
    },
    {
      destination: 'retention',
      title: '数据保护',
      description: '管理回收站、版本保留和自动版本化。',
      status: formatProtectionStatus(trashEnabled),
      tone: trashEnabled ? 'success' : 'warning',
      icon: History,
    },
    {
      destination: 'users-access',
      title: '目录与访问',
      description: '管理目录配额、访问规则和有效权限复核。',
      status: '用户管理',
      tone: 'neutral',
      icon: FolderLock,
    },
    {
      destination: 'shares',
      title: '分享与协作',
      description: '设置分享默认策略，并复核已经创建的访问链接。',
      status: shareEnabled ? '分享已启用' : '分享未启用',
      tone: shareEnabled ? 'success' : 'neutral',
      icon: Share2,
    },
    {
      destination: 'webdav',
      title: '设备挂载',
      description: '查看 WebDAV 挂载地址，并选择适合的认证方式。',
      status: formatWebDAVStatus(webdavEnabled, webdavAuthType),
      tone: getWebDAVTone(webdavEnabled, webdavAuthType),
      icon: Cable,
    },
    {
      destination: 'device-care',
      title: '设备状态与通知',
      description: '查看磁盘健康、数据校验和异常通知。',
      status: formatDeviceCareStatus(alertsEnabled, diskHealthEnabled, scrubScheduleEnabled),
      tone: alertsEnabled || diskHealthEnabled || scrubScheduleEnabled ? 'success' : 'neutral',
      icon: BellRing,
    },
  ]

  return (
    <section aria-labelledby="settings-overview-title" className="space-y-6">
      <div className="rounded-lg border border-divider bg-content1 px-5 py-5 shadow-[var(--shadow-soft)] sm:px-6">
        <p className="text-xs font-semibold uppercase tracking-[0.16em] text-primary">设置概览</p>
        <div className="mt-2 max-w-2xl">
          <h2 id="settings-overview-title" className="text-xl font-semibold text-foreground">
            按使用目标调整设备
          </h2>
          <p className="mt-2 text-sm leading-6 text-default-500">
            设置按使用目标分组；周期校验等设备任务在对应页面独立保存。低频技术参数保留在相关分类中，避免日常操作被运维选项打断。
          </p>
        </div>
      </div>

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {tasks.map((task) => (
          <SettingsTaskCard key={task.destination} task={task} onNavigate={onNavigate} />
        ))}
      </div>
    </section>
  )
}
