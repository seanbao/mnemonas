import { useQuery } from '@tanstack/react-query'
import { Card, CardBody, CardHeader, Progress, Chip, Button, Divider, addToast } from '@heroui/react'
import { 
  Activity, 
  Server, 
  HardDrive, 
  Cpu, 
  Database,
  Trash2,
  RefreshCw,
  CheckCircle,
  XCircle,
  AlertCircle,
  ShieldCheck,
  Clock,
  BellRing,
  Thermometer,
  Network,
  type LucideIcon,
} from 'lucide-react'
import { ApiError, getDiagnostics, getDiskHealth, getStorageStats, type DiagnosticsInfo, type DiskHealthReport } from '@/api/files'
import { formatBytes } from '@/lib/utils'
import { areDiskStatsAvailable, areStorageStatsAvailable, clampUsagePercent, formatFilesystemType, formatUsagePercent } from '@/lib/storageStats'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'
import { useUser } from '@/stores/auth'

function StatusIndicator({ 
  status, 
  label 
}: { 
  status: boolean | 'warning' | 'unknown'
  label: string
}) {
  const getColor = () => {
    if (status === true) return 'success'
    if (status === false) return 'danger'
    if (status === 'warning') return 'warning'
    return 'default'
  }

  const getIcon = () => {
    if (status === true) return <CheckCircle size={14} />
    if (status === false) return <XCircle size={14} />
    if (status === 'warning') return <AlertCircle size={14} />
    return <AlertCircle size={14} />
  }

  return (
    <Chip
      size="sm"
      color={getColor()}
      variant="flat"
      startContent={getIcon()}
    >
      {label}
    </Chip>
  )
}

// Format uptime
function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)

  if (days > 0) {
    return `${days}天 ${hours}小时`
  }
  if (hours > 0) {
    return `${hours}小时 ${minutes}分钟`
  }
  return `${minutes}分钟`
}

function formatMetricWithUnit(value: number | undefined, unit: string): string {
  return value === undefined ? '--' : `${value} ${unit}`
}

function formatBuildTime(value: string | undefined): string | null {
  if (!value || value === 'unknown') {
    return null
  }
  return value
}

function formatGoVersion(value: string): string {
  const normalized = value.trim()
  if (normalized.toLowerCase().startsWith('go')) {
    return `Go ${normalized.slice(2)}`
  }
  return `Go ${normalized}`
}

function getFilesystemPresentation(
  fsType: string | undefined,
  nativeDataChecksumSupport: boolean | undefined,
): {
  icon: LucideIcon
  title: string
  description: string
  className: string
  iconClassName: string
} {
  const normalized = fsType?.trim().toLowerCase()
  if (nativeDataChecksumSupport === true) {
    return {
      icon: ShieldCheck,
      title: '原生数据校验支持',
      description: `${formatFilesystemType(fsType)} 具备底层校验与 scrub 能力，仍需保留独立备份。`,
      className: 'border-success/25 bg-success/5',
      iconClassName: 'text-success',
    }
  }

  if (!normalized || normalized === 'unknown') {
    return {
      icon: AlertCircle,
      title: '文件系统未知',
      description: '无法识别底层文件系统，建议在部署机上运行 mnemonas-doctor 核对磁盘布局。',
      className: 'border-default-200 bg-content2/40',
      iconClassName: 'text-default-500',
    }
  }

  if (normalized === 'tmpfs') {
    return {
      icon: AlertCircle,
      title: '临时文件系统',
      description: '当前存储看起来是 tmpfs，重启可能丢失数据。请迁移到持久磁盘。',
      className: 'border-danger/25 bg-danger/5',
      iconClassName: 'text-danger',
    }
  }

  if (['nfs', 'cifs', 'smb', 'smb2', 'fuse'].some((prefix) => normalized.startsWith(prefix))) {
    return {
      icon: AlertCircle,
      title: '网络或 FUSE 存储',
      description: '请确认一致性、断线恢复和独立备份策略。',
      className: 'border-warning/25 bg-warning/5',
      iconClassName: 'text-warning',
    }
  }

  return {
    icon: AlertCircle,
    title: '建议使用 ZFS/Btrfs',
    description: '当前未检测到底层数据校验与 scrub 能力，请依赖 MnemoNAS scrub 和独立备份。',
    className: 'border-warning/25 bg-warning/5',
    iconClassName: 'text-warning',
  }
}

function getAlertsPresentation(alerts: DiagnosticsInfo['alerts']): {
  icon: LucideIcon
  title: string
  description: string
  className: string
  iconClassName: string
} | undefined {
  if (!alerts) {
    return undefined
  }

  if (alerts.enabled !== true) {
    return {
      icon: AlertCircle,
      title: '存储告警未启用',
      description: '建议在设置中启用存储告警，避免磁盘写满后才发现问题。',
      className: 'border-warning/25 bg-warning/5',
      iconClassName: 'text-warning',
    }
  }

  if (alerts.runtimeAvailable === false) {
    return {
      icon: AlertCircle,
      title: '存储告警运行态不可用',
      description: '配置已启用，但当前进程没有挂载告警监控，请检查服务启动日志。',
      className: 'border-danger/25 bg-danger/5',
      iconClassName: 'text-danger',
    }
  }

  const lastLevel = alerts.lastLevel?.trim().toLowerCase()
  const checkedText = alerts.lastCheckedAt ? `最近检查 ${alerts.lastCheckedAt}` : '等待首次检查'
  const usageText = alerts.lastUsedPct !== undefined
    ? `，使用率 ${alerts.lastUsedPct.toFixed(1)}%`
    : ''
  const freeText = alerts.lastFreeBytes !== undefined
    ? `，剩余 ${formatBytes(alerts.lastFreeBytes)}`
    : ''

  if (lastLevel === 'critical') {
    return {
      icon: AlertCircle,
      title: '存储告警处于严重级别',
      description: `${checkedText}${usageText}${freeText}。请尽快清理或扩容。`,
      className: 'border-danger/25 bg-danger/5',
      iconClassName: 'text-danger',
    }
  }

  if (lastLevel === 'warning') {
    return {
      icon: AlertCircle,
      title: '存储告警处于提醒级别',
      description: `${checkedText}${usageText}${freeText}。建议安排清理或扩容。`,
      className: 'border-warning/25 bg-warning/5',
      iconClassName: 'text-warning',
    }
  }

  const hasNotificationChannel = alerts.webhookConfigured || alerts.telegramConfigured || alerts.emailConfigured
  const notificationText = hasNotificationChannel
    ? '通知通道已配置。'
    : '如需外部通知，请在设置中配置 Webhook、Telegram 或邮件。'

  return {
    icon: BellRing,
    title: hasNotificationChannel ? '存储告警已启用' : '存储告警已启用，未配置通知通道',
    description: `${checkedText}。${notificationText}`,
    className: 'border-success/25 bg-success/5',
    iconClassName: 'text-success',
  }
}

function formatGoDurationLabel(value: string | undefined): string {
  if (!value) {
    return '--'
  }

  const match = value.match(/^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/)
  if (!match || match[0] === '') {
    return value
  }

  const hours = Number(match[1] ?? 0)
  const minutes = Number(match[2] ?? 0)
  const seconds = Number(match[3] ?? 0)
  if (hours > 0 && minutes === 0 && seconds === 0 && hours % 24 === 0) {
    return `${hours / 24} 天`
  }

  const parts: string[] = []
  if (hours > 0) parts.push(`${hours} 小时`)
  if (minutes > 0) parts.push(`${minutes} 分钟`)
  if (seconds > 0 || parts.length === 0) parts.push(`${seconds} 秒`)
  return parts.join(' ')
}

function getMaintenancePresentation(maintenance: DiagnosticsInfo['maintenance']): {
  icon: LucideIcon
  title: string
  description: string
  className: string
  iconClassName: string
} | undefined {
  if (!maintenance) {
    return undefined
  }

  if (maintenance.historyReady === false) {
    return {
      icon: AlertCircle,
      title: '维护历史不可用',
      description: 'Scrub 和备份恢复演练记录无法读取，请检查维护目录权限和服务日志。',
      className: 'border-danger/25 bg-danger/5',
      iconClassName: 'text-danger',
    }
  }

  if (maintenance.scrubScheduleEnabled !== true) {
    return {
      icon: Clock,
      title: '周期 Scrub 未启用',
      description: '可在配置中启用自动数据巡检，定期校验 CAS 对象完整性。',
      className: 'border-warning/25 bg-warning/5',
      iconClassName: 'text-warning',
    }
  }

  const scheduleText = formatGoDurationLabel(maintenance.scrubScheduleInterval)
  const retryText = formatGoDurationLabel(maintenance.scrubRetryInterval)
  const maxRetries = maintenance.scrubMaxRetries ?? 0
  const lastText = maintenance.lastScrubAt ? `最近 Scrub ${maintenance.lastScrubAt}` : '等待首次 Scrub'

  if (maintenance.lastScrubStatus === 'failed') {
    const retries = maintenance.scrubFailureRetries ?? 0
    const retryDescription = maxRetries > 0
      ? `失败后每 ${retryText} 重试，当前已重试 ${retries}/${maxRetries} 次。`
      : '失败后不会自动重试。'
    return {
      icon: AlertCircle,
      title: '周期 Scrub 最近失败',
      description: `${lastText}。${retryDescription}`,
      className: 'border-warning/25 bg-warning/5',
      iconClassName: 'text-warning',
    }
  }

  const retryDescription = maxRetries > 0
    ? `失败后每 ${retryText} 重试，最多 ${maxRetries} 次。`
    : '失败后不自动重试。'
  return {
    icon: ShieldCheck,
    title: '周期 Scrub 已启用',
    description: `每 ${scheduleText} 自动巡检。${lastText}。${retryDescription}`,
    className: 'border-success/25 bg-success/5',
    iconClassName: 'text-success',
  }
}

function getSMBPresentation(smb: DiagnosticsInfo['smb']): {
  icon: LucideIcon
  title: string
  description: string
  className: string
  iconClassName: string
} | undefined {
  if (!smb || smb.enabled !== true) {
    return undefined
  }

  if (smb.runtimeAvailable === false) {
    const shareText = smb.shareCount !== undefined ? `已配置 ${smb.shareCount} 个共享。` : ''
    return {
      icon: Network,
      title: 'SMB 当前不可挂载',
      description: `${shareText}当前版本只保留 SMB 网关配置和权限模型，尚未内置 SMB/Samba 运行时。请继续使用 WebDAV。`,
      className: 'border-warning/25 bg-warning/5',
      iconClassName: 'text-warning',
    }
  }

  return {
    icon: Network,
    title: 'SMB 运行态已就绪',
    description: `${smb.serverName ?? 'MnemoNAS'} 正在监听 ${smb.listen ?? '--'}。`,
    className: 'border-success/25 bg-success/5',
    iconClassName: 'text-success',
  }
}

function getDiskHealthPresentation(report: DiskHealthReport | undefined, diagnostics: DiagnosticsInfo['diskHealth']): {
  icon: LucideIcon
  title: string
  description: string
  className: string
  iconClassName: string
} {
  const status = (report?.status ?? diagnostics?.lastStatus ?? '').trim().toLowerCase()
  const enabled = report?.enabled ?? diagnostics?.enabled
  const checkedAt = report?.checkedAt ?? diagnostics?.lastCheckedAt
  const deviceCount = report?.devices.length ?? diagnostics?.lastDeviceCount ?? diagnostics?.deviceCount ?? 0
  const checkedText = checkedAt ? `最近检查 ${checkedAt}` : '等待首次检查'

  if (enabled !== true) {
    return {
      icon: AlertCircle,
      title: '磁盘健康监控未启用',
      description: '配置 SMART 设备后可监控温度、SMART 状态、介质磨损和掉盘。',
      className: 'border-warning/25 bg-warning/5',
      iconClassName: 'text-warning',
    }
  }

  if (diagnostics?.runtimeAvailable === false && !report) {
    return {
      icon: AlertCircle,
      title: '磁盘健康运行态不可用',
      description: '配置已启用，但当前进程没有挂载磁盘健康监控。',
      className: 'border-danger/25 bg-danger/5',
      iconClassName: 'text-danger',
    }
  }

  if (status === 'critical') {
    return {
      icon: AlertCircle,
      title: '磁盘健康严重异常',
      description: `${checkedText}。请检查 SMART、设备连接和序列号匹配。`,
      className: 'border-danger/25 bg-danger/5',
      iconClassName: 'text-danger',
    }
  }

  if (status === 'warning' || status === 'unavailable') {
    return {
      icon: AlertCircle,
      title: status === 'unavailable' ? '磁盘健康状态不可用' : '磁盘健康需要关注',
      description: `${checkedText}。请确认 smartctl 权限、设备路径、温度和介质健康状态。`,
      className: 'border-warning/25 bg-warning/5',
      iconClassName: 'text-warning',
    }
  }

  return {
    icon: ShieldCheck,
    title: deviceCount > 0 ? '磁盘健康正常' : '磁盘健康等待设备',
    description: deviceCount > 0 ? `${checkedText}，已检查 ${deviceCount} 块磁盘。` : '已启用监控，但尚未配置磁盘设备。',
    className: deviceCount > 0 ? 'border-success/25 bg-success/5' : 'border-warning/25 bg-warning/5',
    iconClassName: deviceCount > 0 ? 'text-success' : 'text-warning',
  }
}

function diskHealthDeviceMetricSummary(device: DiskHealthReport['devices'][number]): string {
  const parts: string[] = []
  if (device.temperatureC !== undefined) {
    parts.push(`${device.temperatureC} C`)
  }
  if (device.wearPercentUsed !== undefined) {
    parts.push(`磨损 ${device.wearPercentUsed}%`)
  }
  if (device.availableSparePercent !== undefined) {
    parts.push(`备用 ${device.availableSparePercent}%`)
  }
  if (device.mediaErrors !== undefined && device.mediaErrors > 0) {
    parts.push(`介质错误 ${device.mediaErrors}`)
  }
  if (parts.length > 0) {
    return parts.join(' · ')
  }
  return device.present ? '在线' : '离线'
}

function getHealthLoadErrorPresentation(errors: Array<unknown>): { title: string; description: string } {
  if (errors.some((error) => error instanceof ApiError && error.isUnavailable)) {
    return {
      title: '系统健康信息暂不可用',
      description: '健康数据服务当前不可用，请检查系统状态或稍后重试。',
    }
  }

  const firstError = errors.find(Boolean)
  return {
    title: '加载系统健康信息失败',
    description: firstError instanceof Error ? firstError.message : '请稍后重试',
  }
}

function getHealthRefreshErrorToast(errors: Array<unknown>): { title: string; description: string; color: 'warning' | 'danger' } {
  const presentation = getHealthLoadErrorPresentation(errors)
  if (errors.some((error) => error instanceof ApiError && error.isUnavailable)) {
    return {
      title: '刷新暂不可用',
      description: presentation.description,
      color: 'warning',
    }
  }

  return {
    title: '刷新失败',
    description: presentation.description,
    color: 'danger',
  }
}

export function HealthPage() {
  const user = useUser()
  const { data: diagnostics, isLoading: diagLoading, error: diagError, refetch: refetchDiag } = useQuery({
    queryKey: ['diagnostics', user?.id ?? 'anonymous'],
    queryFn: getDiagnostics,
    refetchInterval: 30000, // Refresh every 30 seconds
  })

  const { data: stats, isLoading: statsLoading, error: statsError, refetch: refetchStats } = useQuery({
    queryKey: ['storage-stats', user?.id ?? 'anonymous'],
    queryFn: getStorageStats,
    refetchInterval: 30000,
  })

  const { data: diskHealth, isLoading: diskHealthLoading, error: diskHealthError, refetch: refetchDiskHealth } = useQuery({
    queryKey: ['disk-health', user?.id ?? 'anonymous'],
    queryFn: getDiskHealth,
    refetchInterval: 30000,
  })

  const isLoading = diagLoading || statsLoading || diskHealthLoading
  const healthErrors = [diagError, statsError, diskHealthError]
  const hasAvailableData = diagnostics !== undefined || stats !== undefined || diskHealth !== undefined
  const hasPartialError = !isLoading && healthErrors.some(Boolean) && hasAvailableData
  const loadError = diagError || statsError || diskHealthError
  const loadErrorPresentation = getHealthLoadErrorPresentation(healthErrors)
  const storageStatsAvailable = areStorageStatsAvailable(stats)
  const diskStatsAvailable = areDiskStatsAvailable(stats)
  const diskUsagePercent = diskStatsAvailable ? clampUsagePercent(stats?.diskUsageRatio) : undefined
  const diskFilesystemType = stats?.diskFilesystemType ?? diagnostics?.filesystem?.diskFilesystemType
  const diskNativeDataChecksumSupport = stats?.diskNativeDataChecksumSupport ?? diagnostics?.filesystem?.diskNativeDataChecksumSupport
  const filesystemPresentation = diskStatsAvailable
    ? getFilesystemPresentation(diskFilesystemType, diskNativeDataChecksumSupport)
    : undefined
  const FilesystemStatusIcon = filesystemPresentation?.icon
  const alertsPresentation = getAlertsPresentation(diagnostics?.alerts)
  const AlertsStatusIcon = alertsPresentation?.icon
  const maintenancePresentation = getMaintenancePresentation(diagnostics?.maintenance)
  const MaintenanceStatusIcon = maintenancePresentation?.icon
  const smbPresentation = getSMBPresentation(diagnostics?.smb)
  const SMBStatusIcon = smbPresentation?.icon
  const diskHealthPresentation = getDiskHealthPresentation(diskHealth, diagnostics?.diskHealth)
  const DiskHealthStatusIcon = diskHealthPresentation.icon
  const buildTime = formatBuildTime(diagnostics?.version?.buildTime)

  const handleRefresh = async () => {
    const [diagResult, statsResult, diskHealthResult] = await Promise.all([refetchDiag(), refetchStats(), refetchDiskHealth()])
    const refreshErrors = [diagResult.error, statsResult.error, diskHealthResult.error].filter((error): error is Error => Boolean(error))

    if (refreshErrors.length > 0) {
      addToast(getHealthRefreshErrorToast(refreshErrors))
      return
    }

    addToast({ title: '健康数据已刷新', color: 'success' })
  }

  const statsCards = [
    {
      icon: Clock,
      title: '运行时间',
      value: diagnostics?.uptimeSecs !== undefined ? formatUptime(diagnostics.uptimeSecs) : '--',
    },
    {
      icon: Cpu,
      title: '内存使用',
      value: diagnostics?.memory?.allocMb !== undefined ? `${diagnostics.memory.allocMb} MB` : '--',
      subtitle: diagnostics?.memory?.sysMb !== undefined ? `系统: ${diagnostics.memory.sysMb} MB` : undefined,
    },
    {
      icon: Database,
      title: '存储对象',
      value: storageStatsAvailable ? stats?.totalObjects?.toString() ?? '--' : '--',
      subtitle: storageStatsAvailable && stats?.totalSize !== undefined ? `CAS ${formatBytes(stats.totalSize)}` : undefined,
    },
    {
      icon: HardDrive,
      title: '磁盘使用',
      value: diskStatsAvailable ? formatUsagePercent(stats?.diskUsageRatio) : '--',
      subtitle: diskStatsAvailable && stats?.diskAvailable !== undefined ? `可用 ${formatBytes(stats.diskAvailable)}` : undefined,
    },
  ]

  if (!isLoading && loadError && !hasAvailableData) {
    return (
      <div className="p-4 sm:p-6 lg:p-8">
        <EmptyState
          icon={AlertCircle}
          title={loadErrorPresentation.title}
          description={loadErrorPresentation.description}
          action={
            <Button className="btn-secondary rounded-lg" onPress={handleRefresh}>
              重新加载
            </Button>
          }
        />
      </div>
    )
  }

  return (
    <div className="p-4 space-y-6 sm:p-6 lg:p-8">
      {/* Header */}
      <PageHeader
        title="系统健康"
        subtitle="监控系统状态和性能指标"
        icon={Activity}
        actions={
          <Button
            className="btn-secondary rounded-lg"
            startContent={<RefreshCw size={16} />}
            onPress={handleRefresh}
            isLoading={isLoading}
          >
            刷新
          </Button>
        }
      />

      {hasPartialError && (
        <Card className="border-warning/30 bg-warning/5 shadow-none">
          <CardBody className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
            <div className="flex items-start gap-3">
              <AlertCircle size={18} className="mt-0.5 shrink-0 text-warning" />
              <div>
                <p className="text-sm font-medium text-foreground">部分健康数据加载失败</p>
                <p className="text-xs text-default-600">当前页面展示的是可用数据，部分指标可能不是最新状态。</p>
              </div>
            </div>
            <Button
              size="sm"
              variant="flat"
              className="rounded-lg"
              startContent={<RefreshCw size={14} />}
              onPress={handleRefresh}
            >
              重新加载
            </Button>
          </CardBody>
        </Card>
      )}

      {/* System Status */}
      <Card className="card-meridian">
        <CardHeader className="pb-0">
          <div className="flex items-center gap-2">
            <div className="gradient-meridian rounded-lg p-2">
              <Server size={16} className="text-white" />
            </div>
            <div>
              <span className="font-semibold">系统状态</span>
              <p className="text-default-500 text-xs">服务组件健康检查</p>
            </div>
          </div>
        </CardHeader>
        <CardBody>
          <div className="flex flex-wrap gap-2 mb-4">
            <StatusIndicator 
              status={diagnostics?.system?.filesystemInitialized ?? 'unknown'} 
              label="文件系统" 
            />
            <StatusIndicator 
              status={diagnostics?.system?.dataplaneConnected ?? 'unknown'} 
              label="数据面" 
            />
            <StatusIndicator 
              status={diagnostics?.system?.thumbnailServiceReady ?? 'unknown'} 
              label="缩略图服务" 
            />
            <StatusIndicator 
              status={diagnostics?.system?.maintenanceHistoryReady ?? 'unknown'} 
              label="维护历史" 
            />
            {diagnostics?.system?.backupManagerReady !== undefined && (
              <StatusIndicator
                status={diagnostics.system.backupManagerReady}
                label="备份管理"
              />
            )}
            <StatusIndicator 
              status={diagnostics?.system?.activityLogReady ?? 'unknown'} 
              label="活动日志" 
            />
            {diagnostics?.system?.favoritesStoreReady !== undefined && (
              <StatusIndicator 
                status={diagnostics.system.favoritesStoreReady} 
                label="收藏存储" 
              />
            )}
            {diagnostics?.system?.smbRuntimeReady !== undefined && (
              <StatusIndicator
                status={diagnostics.system.smbRuntimeReady}
                label="SMB 运行态"
              />
            )}
          </div>

          {diagnostics?.version && (
            <div className="space-y-1 text-sm text-default-500">
              <div>
                <span className="font-medium">{diagnostics.version.name}</span>
                {' '}v{diagnostics.version.version} · {formatGoVersion(diagnostics.version.go)}
              </div>
              {buildTime && (
                <div className="flex items-center gap-1 text-xs">
                  <Clock size={12} />
                  <span>构建 {buildTime}</span>
                </div>
              )}
            </div>
          )}
        </CardBody>
      </Card>

      {alertsPresentation && AlertsStatusIcon && (
        <Card className={`shadow-none ${alertsPresentation.className}`}>
          <CardBody className="flex items-start gap-3">
            <AlertsStatusIcon size={18} className={`mt-0.5 shrink-0 ${alertsPresentation.iconClassName}`} />
            <div className="min-w-0">
              <p className="text-sm font-medium text-foreground">{alertsPresentation.title}</p>
              <p className="mt-1 text-xs leading-5 text-default-600">{alertsPresentation.description}</p>
            </div>
          </CardBody>
        </Card>
      )}

      {maintenancePresentation && MaintenanceStatusIcon && (
        <Card className={`shadow-none ${maintenancePresentation.className}`}>
          <CardBody className="flex items-start gap-3">
            <MaintenanceStatusIcon size={18} className={`mt-0.5 shrink-0 ${maintenancePresentation.iconClassName}`} />
            <div className="min-w-0">
              <p className="text-sm font-medium text-foreground">{maintenancePresentation.title}</p>
              <p className="mt-1 text-xs leading-5 text-default-600">{maintenancePresentation.description}</p>
            </div>
          </CardBody>
        </Card>
      )}

      {smbPresentation && SMBStatusIcon && (
        <Card className={`shadow-none ${smbPresentation.className}`}>
          <CardBody className="flex items-start gap-3">
            <SMBStatusIcon size={18} className={`mt-0.5 shrink-0 ${smbPresentation.iconClassName}`} />
            <div className="min-w-0">
              <p className="text-sm font-medium text-foreground">{smbPresentation.title}</p>
              <p className="mt-1 text-xs leading-5 text-default-600">{smbPresentation.description}</p>
            </div>
          </CardBody>
        </Card>
      )}

      {/* Stats Grid */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {statsCards.map((stat) => (
          <div key={stat.title} className="stat-card">
            <div className="relative">
              <div className="flex items-start justify-between">
                <div>
                  <p className="text-default-500 text-sm">{stat.title}</p>
                  <div className="mt-1 flex items-baseline gap-1">
                    <span className="data-value-large">{stat.value}</span>
                  </div>
                  {stat.subtitle && (
                    <p className="text-default-500 mt-1 text-xs">{stat.subtitle}</p>
                  )}
                </div>
                <div className="gradient-meridian-subtle rounded-lg p-2.5">
                  <stat.icon className="text-accent-primary h-5 w-5" />
                </div>
              </div>
            </div>
          </div>
        ))}
      </div>

      {/* Storage Details */}
      <div className="grid grid-cols-1 gap-6 xl:grid-cols-3">
        {/* Storage Card */}
        <Card className="card-meridian">
          <CardHeader className="pb-0">
            <div className="flex items-center gap-2">
              <div className="bg-accent-primary/10 rounded-lg p-2">
                <HardDrive size={16} className="text-accent-primary" />
              </div>
              <div>
                <span className="font-semibold">存储详情</span>
                <p className="text-default-500 text-xs">磁盘与 CAS 存储状态</p>
              </div>
            </div>
          </CardHeader>
          <CardBody className="space-y-4">
            <div>
              <div className="flex justify-between text-sm mb-2">
                <span className="text-default-500">{diskStatsAvailable ? '磁盘使用' : 'CAS 大小'}</span>
                <span className="data-value">
                  {diskStatsAvailable && stats?.diskUsed !== undefined
                    ? formatBytes(stats.diskUsed)
                    : storageStatsAvailable && stats?.totalSize !== undefined ? formatBytes(stats.totalSize) : '--'}
                </span>
              </div>
              {diskStatsAvailable && diskUsagePercent !== undefined ? (
                <Progress
                  value={diskUsagePercent}
                  color="primary"
                  className="h-2"
                  aria-label="磁盘使用"
                />
              ) : storageStatsAvailable ? (
                <Progress 
                  isIndeterminate
                  color="primary" 
                  className="h-2"
                  aria-label="存储使用"
                />
              ) : (
                <div className="h-2 rounded-full bg-content2/50" aria-label="存储使用" />
              )}
              <p className="text-xs text-default-400 mt-2">
                {diskStatsAvailable
                  ? `${stats?.diskAvailable !== undefined ? formatBytes(stats.diskAvailable) : '--'} 可用 / ${stats?.diskTotal !== undefined ? formatBytes(stats.diskTotal) : '--'} 总容量`
                  : storageStatsAvailable ? '磁盘容量统计不可用，仅显示 CAS 数据' : '统计不可用'}
              </p>
            </div>

            {filesystemPresentation && FilesystemStatusIcon && (
              <div className={`flex items-start gap-3 rounded-lg border p-3 ${filesystemPresentation.className}`}>
                <FilesystemStatusIcon size={17} className={`mt-0.5 shrink-0 ${filesystemPresentation.iconClassName}`} />
                <div className="min-w-0">
                  <p className="text-sm font-medium text-foreground">
                    {filesystemPresentation.title}
                    <span className="ml-2 text-xs font-normal text-default-500">
                      {formatFilesystemType(diskFilesystemType)}
                    </span>
                  </p>
                  <p className="mt-1 text-xs leading-5 text-default-600">{filesystemPresentation.description}</p>
                </div>
              </div>
            )}

            <Divider />

            <div className="grid grid-cols-1 gap-4 text-sm sm:grid-cols-2">
              <div className="text-center p-3 rounded-lg bg-content2/50">
                <p className="data-value break-anywhere text-2xl font-semibold leading-tight">{storageStatsAvailable ? stats?.totalObjects ?? '--' : '--'}</p>
                <p className="text-default-500 text-xs">对象数量</p>
              </div>
              <div className="text-center p-3 rounded-lg bg-content2/50">
                <p className="data-value break-anywhere text-2xl font-semibold leading-tight">
                  {storageStatsAvailable && stats?.dedupRatio !== undefined
                    ? `${(stats.dedupRatio * 100).toFixed(1)}%`
                    : '--'}
                </p>
                <p className="text-default-500 text-xs">去重比例</p>
              </div>
            </div>
          </CardBody>
        </Card>

        {/* Disk Health Card */}
        <Card className="card-meridian">
          <CardHeader className="pb-0">
            <div className="flex items-center gap-2">
              <div className="bg-success/10 rounded-lg p-2">
                <Thermometer size={16} className="text-success" />
              </div>
              <div>
                <span className="font-semibold">磁盘健康</span>
                <p className="text-default-500 text-xs">SMART、温度与设备在线状态</p>
              </div>
            </div>
          </CardHeader>
          <CardBody className="space-y-4">
            <div className={`flex items-start gap-3 rounded-lg border p-3 ${diskHealthPresentation.className}`}>
              <DiskHealthStatusIcon size={17} className={`mt-0.5 shrink-0 ${diskHealthPresentation.iconClassName}`} />
              <div className="min-w-0">
                <p className="text-sm font-medium text-foreground">{diskHealthPresentation.title}</p>
                <p className="mt-1 text-xs leading-5 text-default-600">{diskHealthPresentation.description}</p>
              </div>
            </div>

            {diskHealth?.devices.length ? (
              <div className="space-y-2">
                {diskHealth.devices.slice(0, 4).map((device) => (
                  <div key={`${device.path}-${device.name ?? ''}`} className="flex items-start justify-between gap-3 rounded-lg bg-content2/50 p-3">
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium text-foreground">{device.name || device.model || device.path}</p>
                      <p className="mt-1 truncate text-xs text-default-500">{device.model || device.path}</p>
                      {device.message && (
                        <p className="mt-1 text-xs leading-4 text-default-500">{device.message}</p>
                      )}
                    </div>
                    <div className="shrink-0 text-right">
                      <Chip
                        size="sm"
                        color={device.status === 'critical' ? 'danger' : device.status === 'warning' || device.status === 'unavailable' ? 'warning' : 'success'}
                        variant="flat"
                      >
                        {device.status === 'critical' ? '严重' : device.status === 'warning' ? '提醒' : device.status === 'unavailable' ? '不可用' : '正常'}
                      </Chip>
                      <p className="mt-2 text-xs text-default-500">
                        {diskHealthDeviceMetricSummary(device)}
                      </p>
                    </div>
                  </div>
                ))}
                {diskHealth.devices.length > 4 && (
                  <p className="text-xs text-default-500">还有 {diskHealth.devices.length - 4} 块磁盘未显示</p>
                )}
              </div>
            ) : (
              <div className="rounded-lg bg-content2/30 p-3 text-xs leading-5 text-default-500">
                当前没有可展示的磁盘设备。
              </div>
            )}
          </CardBody>
        </Card>

        {/* Trash Card */}
        <Card className="card-meridian">
          <CardHeader className="pb-0">
            <div className="flex items-center gap-2">
              <div className="bg-warning/10 rounded-lg p-2">
                <Trash2 size={16} className="text-warning" />
              </div>
              <div>
                <span className="font-semibold">回收站</span>
                <p className="text-default-500 text-xs">待清理文件</p>
              </div>
            </div>
          </CardHeader>
          <CardBody className="space-y-4">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div className="text-center p-3 rounded-lg bg-content2/50">
                <p className="data-value break-anywhere text-2xl font-semibold leading-tight">
                  {diagnostics?.filesystem?.trashItems ?? '--'}
                </p>
                <p className="text-default-500 text-xs">待删除文件</p>
              </div>
              <div className="text-center p-3 rounded-lg bg-content2/50">
                <p className="data-value break-anywhere text-2xl font-semibold leading-tight">
                  {diagnostics?.filesystem?.trashSize !== undefined
                    ? formatBytes(diagnostics.filesystem.trashSize)
                    : '--'}
                </p>
                <p className="text-default-500 text-xs">占用空间</p>
              </div>
            </div>

            <div className="text-xs text-default-500 p-3 rounded-lg bg-content2/30">
              回收站文件将按配置自动清理
            </div>
          </CardBody>
        </Card>
      </div>

      {/* Memory & Performance */}
      <Card className="card-meridian">
        <CardHeader className="pb-0">
          <div className="flex items-center gap-2">
            <div className="gradient-energy rounded-lg p-2">
              <Cpu size={16} className="text-white" />
            </div>
            <div>
              <span className="font-semibold">内存与性能</span>
              <p className="text-default-500 text-xs">运行时监控</p>
            </div>
          </div>
        </CardHeader>
        <CardBody>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-4">
            {[
              { label: '当前分配', value: formatMetricWithUnit(diagnostics?.memory?.allocMb, 'MB') },
              { label: '累计分配', value: formatMetricWithUnit(diagnostics?.memory?.totalAllocMb, 'MB') },
              { label: '系统内存', value: formatMetricWithUnit(diagnostics?.memory?.sysMb, 'MB') },
              { label: 'GC 次数', value: diagnostics?.memory?.numGc ?? '--' },
            ].map((item) => (
              <div key={item.label} className="rounded-lg bg-content2/50 p-3 text-center">
                <p className="data-value break-anywhere text-2xl font-semibold leading-tight">{item.value}</p>
                <p className="text-default-400 text-xs">{item.label}</p>
              </div>
            ))}
          </div>

          <Divider className="my-4" />

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-4">
            <div className="rounded-lg bg-content2/50 p-3 text-center">
              <p className="data-value break-anywhere text-2xl font-semibold leading-tight">
                {diagnostics?.goroutines ?? '--'}
              </p>
              <p className="text-default-400 text-xs">Goroutines</p>
            </div>
            {diagnostics?.dataplane && (
              <>
                <div className="rounded-lg bg-content2/50 p-3 text-center">
                  <div className={`inline-flex items-center gap-1 ${
                    diagnostics.dataplane.healthy === true
                      ? 'text-success'
                      : diagnostics.dataplane.healthy === false
                        ? 'text-danger'
                        : 'text-default-500'
                  }`}>
                    {diagnostics.dataplane.healthy === true ? <div className="live-indicator scale-75" /> : <AlertCircle size={14} />}
                    <span className="text-lg font-semibold">
                      {diagnostics.dataplane.healthy === true
                        ? '健康'
                        : diagnostics.dataplane.healthy === false
                          ? '异常'
                          : '未知'}
                    </span>
                  </div>
                  <p className="text-default-400 text-xs">数据面状态</p>
                </div>
                <div className="rounded-lg bg-content2/50 p-3 text-center">
                  <p className="data-value break-anywhere text-2xl font-semibold leading-tight">
                    {diagnostics.dataplane.version || '--'}
                  </p>
                  <p className="text-default-400 text-xs">数据面版本</p>
                </div>
                <div className="rounded-lg bg-content2/50 p-3 text-center">
                  <p className="data-value break-anywhere text-2xl font-semibold leading-tight">
                    {diagnostics.dataplane.uptimeSec !== undefined
                      ? formatUptime(diagnostics.dataplane.uptimeSec) 
                      : '--'}
                  </p>
                  <p className="text-default-400 text-xs">数据面运行</p>
                </div>
              </>
            )}
          </div>
        </CardBody>
      </Card>
    </div>
  )
}
