import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { Button, Skeleton, Card, CardBody, CardHeader, addToast } from '@heroui/react'
import { 
  HardDrive, 
  Activity, 
  Trash2, 
  RefreshCw,
  Copy,
  CheckCircle2,
  Clock,
  Database,
  Sparkles,
  TrendingUp,
  AlertCircle,
  ShieldCheck,
} from 'lucide-react'
import { ApiError, getStorageStats, type DirectoryQuotaUsage, type StorageStats } from '@/api/files'
import { formatBytes } from '@/lib/utils'
import { areDiskStatsAvailable, areStorageStatsAvailable, clampUsagePercent, formatFilesystemType, formatUsagePercent, getDiskSpaceStatus, getFilesystemIntegrityStatus, type FilesystemIntegrityStatus, type FilesystemIntegrityStatusLevel } from '@/lib/storageStats'
import { GENERIC_ACTION_ERROR_DESCRIPTION, getUserFacingErrorDescription } from '@/lib/apiMessages'
import { PageHeader } from '@/components/ui/PageHeader'
import { EmptyState } from '@/components/ui/EmptyState'
import { useIsAdmin, useUser } from '@/stores/auth'

const storageStatsLoadErrorDescription = '存储统计加载失败，请检查网络或稍后重试。'
const clipboardWriteFailureDescription = '请检查浏览器剪贴板权限。'

function formatStorageSize(value: number | undefined): string {
  return value === undefined ? '--' : formatBytes(value)
}

function formatCount(value: number | undefined): string {
  return value === undefined ? '--' : value.toLocaleString()
}

function getStorageErrorPresentation(
  error: unknown,
  fallbackDescription = storageStatsLoadErrorDescription,
): { title: string; description: string } {
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      title: '存储统计暂不可用',
      description: '存储统计服务当前不可用，请检查设备状态或稍后重试。',
    }
  }

  return {
    title: '加载存储统计失败',
    description: getUserFacingErrorDescription(error, fallbackDescription),
  }
}

function getStorageRefreshErrorToast(error: unknown): { title: string; description: string; color: 'warning' | 'danger' } {
  const presentation = getStorageErrorPresentation(error, GENERIC_ACTION_ERROR_DESCRIPTION)
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      ...presentation,
      color: 'warning',
    }
  }

  return {
    ...presentation,
    color: 'danger',
  }
}

function getDiskUsageBarClass(level: 'unknown' | 'normal' | 'warning' | 'critical'): string {
  if (level === 'critical') {
    return 'bg-danger/70'
  }
  if (level === 'warning') {
    return 'bg-warning/70'
  }
  return 'bg-accent-primary'
}

function getDiskSpacePanelClass(level: 'unknown' | 'normal' | 'warning' | 'critical'): string {
  return level === 'critical'
    ? 'border-danger/25 bg-danger/5 text-danger'
    : 'border-warning/25 bg-warning/5 text-warning'
}

function getFilesystemIntegrityPanelClass(level: FilesystemIntegrityStatusLevel): string {
  if (level === 'supported') {
    return 'border-success/25 bg-success/5'
  }
  if (level === 'volatile') {
    return 'border-danger/25 bg-danger/5'
  }
  if (level === 'unknown') {
    return 'border-default-200 bg-content2/40'
  }
  return 'border-warning/25 bg-warning/5'
}

function formatStorageBackingSummary(stats: StorageStats, filesystemIntegrityStatus: FilesystemIntegrityStatus | undefined): string {
  const rows = [
    ['文件系统', formatFilesystemType(stats.diskFilesystemType)],
    ['数据校验能力', filesystemIntegrityStatus?.title ?? '--'],
    ['挂载点', stats.diskMountPoint ?? '--'],
    ['存储源', stats.diskMountSource ?? '--'],
    ['挂载选项', stats.diskMountOptions ?? '--'],
    ['磁盘容量', formatStorageSize(stats.diskTotal)],
    ['已用空间', formatStorageSize(stats.diskUsed)],
    ['可用空间', formatStorageSize(stats.diskAvailable)],
    ['磁盘占用', formatUsagePercent(stats.diskUsageRatio)],
  ]
  return rows.map(([label, value]) => `${label}: ${value}`).join('\n')
}

function getDirectoryQuotaStatusLabel(quota: DirectoryQuotaUsage): string {
  if (quota.status === 'missing') {
    return '目录未创建'
  }
  if (quota.status === 'exceeded') {
    return '已达上限'
  }
  if (quota.status === 'warning') {
    return '接近上限'
  }
  return '正常'
}

function getDirectoryQuotaBadgeClass(status: DirectoryQuotaUsage['status']): string {
  if (status === 'exceeded') {
    return 'border-danger/25 bg-danger/10 text-danger'
  }
  if (status === 'warning') {
    return 'border-warning/25 bg-warning/10 text-warning'
  }
  if (status === 'missing') {
    return 'border-default-200 bg-content2 text-default-600'
  }
  return 'border-success/25 bg-success/10 text-success'
}

function getDirectoryQuotaBarClass(status: DirectoryQuotaUsage['status']): string {
  if (status === 'exceeded') {
    return 'bg-danger/70'
  }
  if (status === 'warning') {
    return 'bg-warning/70'
  }
  if (status === 'missing') {
    return 'bg-default-300'
  }
  return 'bg-success/70'
}

// Action card for maintenance operations
function MaintenanceCard({
  title,
  description,
  icon: Icon,
  lastRun,
  estimate,
  buttonText,
  buttonColor,
  onPress,
  isDisabled = false,
}: {
  title: string
  description: string
  icon: React.ElementType
  lastRun: string
  estimate: string
  buttonText: string
  buttonColor: 'success' | 'warning' | 'danger'
  onPress: () => void
  isDisabled?: boolean
}) {
  return (
    <div className="stat-card">
      <div className="relative">
        <div className="flex items-center gap-3 mb-4">
          <div className="gradient-meridian-subtle flex h-10 w-10 items-center justify-center rounded-lg">
            <Icon size={20} className="text-accent-primary" />
          </div>
          <div>
            <h3 className="font-medium">{title}</h3>
            <p className="text-sm text-default-500">{description}</p>
          </div>
        </div>
        
        <div className="space-y-1.5 mb-4">
          <div className="flex items-center gap-2 text-sm text-default-500">
            <CheckCircle2 size={14} className="text-success" />
            <span>{lastRun}</span>
          </div>
          <div className="flex items-center gap-2 text-sm text-default-500">
            <Clock size={14} />
            <span>{estimate}</span>
          </div>
        </div>
        
        <Button 
          color={buttonColor} 
          variant="flat" 
          className="w-full rounded-lg"
          onPress={onPress}
          isDisabled={isDisabled}
        >
          {buttonText}
        </Button>
      </div>
    </div>
  )
}

export function StoragePage() {
  const navigate = useNavigate()
  const user = useUser()
  const isAdmin = useIsAdmin()
  const { data: stats, isLoading, error, refetch } = useQuery({
    queryKey: ['stats', user?.id ?? 'anonymous', isAdmin],
    queryFn: ({ signal }) => getStorageStats({ signal }),
    refetchInterval: 30000,
  })
  const storageErrorPresentation = error ? getStorageErrorPresentation(error) : null

  const handleRefresh = async () => {
    const result = await refetch()
    if (result.error) {
      addToast(getStorageRefreshErrorToast(result.error))
      return
    }
    addToast({ title: '存储统计已刷新', color: 'success' })
  }

  if (isLoading) {
    return (
      <div className="p-4 space-y-6 sm:p-6 lg:p-8">
        <div className="flex items-center gap-3">
          <div className="w-10 h-10 rounded-lg bg-accent-primary flex items-center justify-center">
            <HardDrive size={20} className="text-white" />
          </div>
          <div>
            <Skeleton className="w-32 h-5 rounded-lg mb-1" />
            <Skeleton className="w-48 h-4 rounded-lg" />
          </div>
        </div>
        <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
          {[1, 2, 3, 4].map((i) => (
            <div key={i} className="stat-card">
              <Skeleton className="w-10 h-10 rounded-lg mb-3" />
              <Skeleton className="w-20 h-6 rounded-lg mb-1" />
              <Skeleton className="w-16 h-4 rounded-lg" />
            </div>
          ))}
        </div>
      </div>
    )
  }

  if (error) {
    return (
      <div className="p-4 space-y-6 sm:p-6 lg:p-8">
        <PageHeader
          title="空间与存储"
          subtitle="文件占用、版本对象和目录配额"
          icon={HardDrive}
          actions={
            <Button
              variant="flat"
              startContent={<RefreshCw size={16} />}
              onPress={handleRefresh}
              className="rounded-lg"
            >
              刷新
            </Button>
          }
        />

        <EmptyState
          icon={AlertCircle}
          title={storageErrorPresentation!.title}
          description={storageErrorPresentation!.description}
          action={
            <Button variant="bordered" className="rounded-lg" onPress={handleRefresh}>
              重新加载
            </Button>
          }
        />
      </div>
    )
  }

  const storageStatsAvailable = areStorageStatsAvailable(stats)
  const diskStatsAvailable = areDiskStatsAvailable(stats)
  const casBytes = storageStatsAvailable ? stats?.totalSize : undefined
  const diskUsedBytes = diskStatsAvailable ? stats?.diskUsed : undefined
  const diskTotalBytes = diskStatsAvailable ? stats?.diskTotal : undefined
  const diskAvailableBytes = diskStatsAvailable ? stats?.diskAvailable : undefined
  const diskUsagePercent = diskStatsAvailable ? clampUsagePercent(stats?.diskUsageRatio) : undefined
  const diskSpaceStatus = getDiskSpaceStatus(stats)
  const filesystemIntegrityStatus = diskStatsAvailable
    ? getFilesystemIntegrityStatus(stats?.diskFilesystemType, stats?.diskNativeDataChecksumSupport)
    : undefined
  const shouldShowDiskSpaceAlert = diskSpaceStatus.level === 'warning' || diskSpaceStatus.level === 'critical'
  const overviewUsedBytes = diskStatsAvailable ? diskUsedBytes : casBytes
  const hasUsage = overviewUsedBytes !== undefined && overviewUsedBytes > 0
  const hasStorageBackingDetails = isAdmin && diskStatsAvailable && (
    stats?.diskMountPoint
    || stats?.diskMountSource
    || stats?.diskMountOptions
    || filesystemIntegrityStatus
  )
  const storageBackingSummary = stats ? formatStorageBackingSummary(stats, filesystemIntegrityStatus) : ''
  const uniqueBytes = storageStatsAvailable ? stats?.uniqueSize ?? 0 : 0
  const savedBytes = storageStatsAvailable && casBytes !== undefined && stats?.uniqueSize !== undefined
    ? Math.max(0, casBytes - uniqueBytes)
    : undefined
  const overviewSubtitle = diskStatsAvailable && diskUsedBytes !== undefined
    ? `${formatBytes(diskUsedBytes)} 已使用${diskAvailableBytes !== undefined ? ` · ${formatBytes(diskAvailableBytes)} 可用` : ''}`
    : casBytes !== undefined
      ? `${formatBytes(casBytes)} CAS 数据 · 磁盘容量不可用`
      : '统计不可用'
  const directoryQuotas = stats?.directoryQuotas ?? []

  const statsCards = [
    {
      title: '磁盘容量',
      value: diskStatsAvailable ? formatStorageSize(diskTotalBytes) : '--',
      icon: HardDrive,
    },
    {
      title: '可用空间',
      value: diskStatsAvailable ? formatStorageSize(diskAvailableBytes) : '--',
      icon: Activity,
    },
    {
      title: '磁盘占用',
      value: diskStatsAvailable ? formatUsagePercent(stats?.diskUsageRatio) : '--',
      icon: TrendingUp,
    },
    {
      title: '文件系统',
      value: diskStatsAvailable ? formatFilesystemType(stats?.diskFilesystemType) : '--',
      icon: stats?.diskNativeDataChecksumSupport === true ? ShieldCheck : HardDrive,
    },
    {
      title: '对象总数',
      value: storageStatsAvailable ? formatCount(stats?.totalObjects) : '--',
      icon: Database,
    },
    {
      title: 'CAS 大小',
      value: storageStatsAvailable ? formatStorageSize(casBytes) : '--',
      icon: Database,
    },
    {
      title: '去重率',
      value: storageStatsAvailable && stats?.dedupRatio !== undefined ? `${(stats.dedupRatio * 100).toFixed(1)}%` : '--',
      icon: Sparkles,
    },
    {
      title: '节省空间',
      value: formatStorageSize(savedBytes),
      icon: TrendingUp,
    },
  ]

  const handleCopyStorageBackingSummary = async () => {
    if (!navigator.clipboard?.writeText) {
      addToast({
        title: '无法复制存储摘要',
        description: '当前浏览器不支持剪贴板写入。',
        color: 'warning',
      })
      return
    }

    try {
      await navigator.clipboard.writeText(storageBackingSummary)
      addToast({ title: '存储摘要已复制', color: 'success' })
    } catch {
      addToast({
        title: '无法复制存储摘要',
        description: clipboardWriteFailureDescription,
        color: 'danger',
      })
    }
  }

  return (
    <div className="p-4 space-y-6 sm:p-6 lg:p-8">
      {/* Header */}
      <PageHeader
        title="空间与存储"
        subtitle="文件占用、版本对象和目录配额"
        icon={HardDrive}
        actions={
          <Button
            variant="flat"
            startContent={<RefreshCw size={16} />}
            onPress={handleRefresh}
            className="rounded-lg"
          >
            刷新
          </Button>
        }
      />

      {/* Storage Overview Card */}
      <Card className="card-meridian">
        <CardHeader className="pb-0">
          <div className="flex items-center gap-2">
            <div className="gradient-meridian rounded-lg p-2">
              <Database className="h-4 w-4 text-white" />
            </div>
            <div>
              <span className="font-semibold">存储空间使用情况</span>
              <p className="text-default-500 text-xs">
                {overviewSubtitle}
              </p>
            </div>
          </div>
        </CardHeader>
        <CardBody>
          <div className="space-y-2">
            <div className="h-2 rounded-full bg-content2 overflow-hidden">
              <div 
                className={hasUsage ? `h-full rounded-full flow-line opacity-70 ${getDiskUsageBarClass(diskSpaceStatus.level)}` : "h-full bg-accent-primary/30 rounded-full"}
                style={{ width: diskUsagePercent !== undefined ? `${diskUsagePercent}%` : hasUsage ? '100%' : '0%' }}
              />
            </div>
            <div className="flex justify-between text-sm text-default-500">
              <span>{overviewUsedBytes !== undefined ? '已用' : '统计不可用'}</span>
              <span>{diskStatsAvailable ? formatStorageSize(diskTotalBytes) : '--'}</span>
            </div>
            {shouldShowDiskSpaceAlert && (
              <div className={`mt-3 flex items-start gap-2 rounded-lg border p-3 ${getDiskSpacePanelClass(diskSpaceStatus.level)}`}>
                <AlertCircle size={16} className="mt-0.5 shrink-0" />
                <div className="min-w-0">
                  <p className="text-sm font-medium text-foreground">{diskSpaceStatus.title}</p>
                  <p className="mt-1 text-xs leading-5 text-default-600">{diskSpaceStatus.description}</p>
                </div>
              </div>
            )}
            {hasStorageBackingDetails && (
              <div className="mt-4 grid gap-3 rounded-lg border border-divider bg-content1 p-3 text-sm sm:grid-cols-2">
                <div className="flex min-w-0 flex-col gap-2 sm:col-span-2 sm:flex-row sm:items-center sm:justify-between">
                  <div className="min-w-0">
                    <p className="font-medium text-foreground">存储承载详情</p>
                    <p className="mt-1 text-xs text-default-500">用于核对实际挂载点、设备来源和底层校验能力。</p>
                  </div>
                  <Button
                    size="sm"
                    variant="flat"
                    startContent={<Copy size={14} />}
                    onPress={handleCopyStorageBackingSummary}
                    className="w-fit rounded-lg"
                  >
                    复制存储摘要
                  </Button>
                </div>
                {stats?.diskMountPoint && (
                  <div className="min-w-0">
                    <p className="text-xs text-default-500">挂载点</p>
                    <p className="mt-1 truncate font-medium text-foreground" title={stats.diskMountPoint}>{stats.diskMountPoint}</p>
                  </div>
                )}
                {stats?.diskMountSource && (
                  <div className="min-w-0">
                    <p className="text-xs text-default-500">存储源</p>
                    <p className="mt-1 truncate font-medium text-foreground" title={stats.diskMountSource}>{stats.diskMountSource}</p>
                  </div>
                )}
                {filesystemIntegrityStatus && (
                  <div className={`min-w-0 rounded-lg border p-3 ${getFilesystemIntegrityPanelClass(filesystemIntegrityStatus.level)}`}>
                    <p className="text-xs text-default-500">数据校验能力</p>
                    <div className="mt-1 flex min-w-0 items-center gap-2">
                      {filesystemIntegrityStatus.level === 'supported' ? (
                        <ShieldCheck size={15} className="shrink-0 text-success" />
                      ) : (
                        <AlertCircle size={15} className="shrink-0 text-warning" />
                      )}
                      <p className="truncate font-medium text-foreground">{filesystemIntegrityStatus.title}</p>
                    </div>
                    <p className="mt-1 text-xs leading-5 text-default-600">{filesystemIntegrityStatus.description}</p>
                  </div>
                )}
                {isAdmin && stats?.diskMountOptions && (
                  <div className="min-w-0 sm:col-span-2">
                    <p className="text-xs text-default-500">挂载选项</p>
                    <p className="mt-1 truncate font-mono text-xs text-default-600" title={stats.diskMountOptions}>{stats.diskMountOptions}</p>
                  </div>
                )}
              </div>
            )}
          </div>
        </CardBody>
      </Card>

      {/* Stats Grid */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-3 xl:grid-cols-4">
        {statsCards.map((stat) => (
          <div key={stat.title} className="stat-card">
            <div className="relative">
              <div className="flex items-start justify-between">
                <div>
                  <p className="text-default-500 text-sm">{stat.title}</p>
                  <div className="mt-1 flex items-baseline gap-1">
                    <span className="data-value-large">{stat.value}</span>
                  </div>
                </div>
                <div className="gradient-meridian-subtle rounded-lg p-2.5">
                  <stat.icon className="text-accent-primary h-5 w-5" />
                </div>
              </div>
            </div>
          </div>
        ))}
      </div>

      {isAdmin && (
        <Card className="card-meridian">
          <CardHeader className="pb-0">
            <div className="flex items-center gap-2">
              <div className="gradient-meridian-subtle rounded-lg p-2">
                <HardDrive className="h-4 w-4 text-accent-primary" />
              </div>
              <div>
                <span className="font-semibold">目录配额</span>
                <p className="text-xs text-default-500">按目录统计当前文件占用和剩余额度</p>
              </div>
            </div>
          </CardHeader>
          <CardBody>
            {!stats?.directoryQuotaStatsAvailable ? (
              <div className="rounded-lg border border-warning/25 bg-warning/5 p-4 text-sm text-default-600">
                目录配额统计暂不可用，请稍后刷新或检查存储状态。
              </div>
            ) : directoryQuotas.length === 0 ? (
              <div className="rounded-lg border border-divider bg-content1 p-4 text-sm text-default-500">
                未配置目录配额。可在设置的版本保留页添加目录容量限制。
              </div>
            ) : (
              <div className="space-y-3">
                {directoryQuotas.map((quota) => {
                  const usagePercent = clampUsagePercent(quota.usageRatio) ?? 0
                  return (
                    <div key={quota.path} className="rounded-lg border border-divider bg-content1 p-4">
                      <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                        <div className="min-w-0">
                          <p className="truncate font-medium text-foreground">{quota.path}</p>
                          <p className="mt-1 text-sm text-default-500">
                            {formatBytes(quota.usedBytes)} / {formatBytes(quota.quotaBytes)}
                            <span className="mx-2 text-default-300">·</span>
                            剩余 {formatBytes(quota.availableBytes)}
                          </p>
                        </div>
                        <span className={`w-fit rounded-full border px-2.5 py-1 text-xs font-medium ${getDirectoryQuotaBadgeClass(quota.status)}`}>
                          {getDirectoryQuotaStatusLabel(quota)}
                        </span>
                      </div>
                      <div className="mt-3 h-2 overflow-hidden rounded-full bg-content2">
                        <div
                          className={`h-full rounded-full ${getDirectoryQuotaBarClass(quota.status)}`}
                          style={{ width: `${usagePercent}%` }}
                        />
                      </div>
                      <div className="mt-2 flex justify-between text-xs text-default-500">
                        <span>{quota.exists ? '当前目录' : '路径不存在'}</span>
                        <span>{formatUsagePercent(quota.usageRatio)}</span>
                      </div>
                    </div>
                  )
                })}
              </div>
            )}
          </CardBody>
        </Card>
      )}

      {/* Maintenance Actions */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <MaintenanceCard
          title="完整性检查"
          description="确认已存数据仍可正确读取"
          icon={Activity}
          lastRun="在备份与维护中执行"
          estimate="支持随时启动"
          buttonText={isAdmin ? '打开维护工具' : '仅管理员可用'}
          buttonColor="success"
          onPress={() => navigate('/maintenance')}
          isDisabled={!isAdmin}
        />
        
        <MaintenanceCard
          title="清理历史对象"
          description="清理不再被引用的版本数据"
          icon={Trash2}
          lastRun="在备份与维护中执行"
          estimate="支持干运行与保护期"
          buttonText={isAdmin ? '打开维护工具' : '仅管理员可用'}
          buttonColor="warning"
          onPress={() => navigate('/maintenance')}
          isDisabled={!isAdmin}
        />
      </div>
    </div>
  )
}
