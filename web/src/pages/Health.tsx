import { useQuery } from '@tanstack/react-query'
import { Card, CardBody, CardHeader, Progress, Chip, Button, Divider } from '@heroui/react'
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
  Clock,
} from 'lucide-react'
import { getDiagnostics, getStorageStats } from '@/api/files'
import { formatBytes } from '@/lib/utils'
import { PageHeader } from '@/components/ui/PageHeader'

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

export function HealthPage() {
  const { data: diagnostics, isLoading: diagLoading, refetch: refetchDiag } = useQuery({
    queryKey: ['diagnostics'],
    queryFn: getDiagnostics,
    refetchInterval: 30000, // Refresh every 30 seconds
  })

  const { data: stats, isLoading: statsLoading } = useQuery({
    queryKey: ['storage-stats'],
    queryFn: getStorageStats,
    refetchInterval: 30000,
  })

  const isLoading = diagLoading || statsLoading

  // Calculate dedup savings
  const dedupSavings = stats && stats.totalSize > 0 && stats.uniqueSize > 0
    ? ((stats.totalSize - stats.uniqueSize) / stats.totalSize * 100).toFixed(1)
    : '0'

  const statsCards = [
    {
      icon: Clock,
      title: '运行时间',
      value: diagnostics?.uptimeSecs ? formatUptime(diagnostics.uptimeSecs) : '--',
      gradient: 'from-blue-500/20 to-violet-500/20',
    },
    {
      icon: Cpu,
      title: '内存使用',
      value: diagnostics?.memory ? `${diagnostics.memory.allocMb} MB` : '--',
      subtitle: diagnostics?.memory ? `系统: ${diagnostics.memory.sysMb} MB` : undefined,
      gradient: 'from-violet-500/20 to-fuchsia-500/20',
    },
    {
      icon: Database,
      title: '存储对象',
      value: stats?.totalObjects?.toString() ?? '--',
      subtitle: stats?.totalSize ? formatBytes(stats.totalSize) : undefined,
      gradient: 'from-emerald-500/20 to-cyan-500/20',
    },
    {
      icon: HardDrive,
      title: '去重率',
      value: stats?.dedupRatio !== undefined
        ? `${(stats.dedupRatio * 100).toFixed(1)}%`
        : '--',
      subtitle: dedupSavings !== '0' ? `节省 ${dedupSavings}%` : undefined,
      gradient: 'from-amber-500/20 to-orange-500/20',
    },
  ]

  return (
    <div className="p-6 lg:p-8 space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <PageHeader
          title="系统健康"
          subtitle="监控系统状态和性能指标"
          icon={Activity}
        />
        <Button
          className="btn-secondary rounded-xl"
          startContent={<RefreshCw size={16} />}
          onPress={() => refetchDiag()}
          isLoading={isLoading}
        >
          刷新
        </Button>
      </div>

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
          </div>

          {diagnostics?.version && (
            <div className="text-sm text-default-500">
              <span className="font-medium">{diagnostics.version.name}</span>
              {' '}v{diagnostics.version.version} · Go {diagnostics.version.go}
            </div>
          )}
        </CardBody>
      </Card>

      {/* Stats Grid */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        {statsCards.map((stat) => (
          <div key={stat.title} className="stat-card">
            <div className={`absolute inset-0 bg-gradient-to-br ${stat.gradient} rounded-2xl opacity-50`} />
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
                <div className="gradient-meridian-subtle rounded-xl p-2.5">
                  <stat.icon className="text-accent-primary h-5 w-5" />
                </div>
              </div>
            </div>
          </div>
        ))}
      </div>

      {/* Storage Details */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Storage Card */}
        <Card className="card-meridian">
          <CardHeader className="pb-0">
            <div className="flex items-center gap-2">
              <div className="bg-accent-primary/10 rounded-lg p-2">
                <HardDrive size={16} className="text-accent-primary" />
              </div>
              <div>
                <span className="font-semibold">存储详情</span>
                <p className="text-default-500 text-xs">CAS 存储状态</p>
              </div>
            </div>
          </CardHeader>
          <CardBody className="space-y-4">
            <div>
              <div className="flex justify-between text-sm mb-2">
                <span className="text-default-500">总存储</span>
                <span className="data-value">{stats ? formatBytes(stats.totalSize) : '--'}</span>
              </div>
              <Progress 
                isIndeterminate
                color="primary" 
                className="h-2"
                aria-label="存储使用"
              />
              <p className="text-xs text-default-400 mt-2">容量未知</p>
            </div>

            <Divider />

            <div className="grid grid-cols-2 gap-4 text-sm">
              <div className="text-center p-3 rounded-lg bg-content2/50">
                <p className="text-2xl font-semibold data-value">{stats?.totalObjects ?? '--'}</p>
                <p className="text-default-500 text-xs">对象数量</p>
              </div>
              <div className="text-center p-3 rounded-lg bg-content2/50">
                <p className="text-2xl font-semibold data-value">
                  {stats?.dedupRatio !== undefined
                    ? `${(stats.dedupRatio * 100).toFixed(1)}%`
                    : '--'}
                </p>
                <p className="text-default-500 text-xs">去重比例</p>
              </div>
            </div>
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
            <div className="grid grid-cols-2 gap-4">
              <div className="text-center p-3 rounded-lg bg-content2/50">
                <p className="text-2xl font-semibold data-value">
                  {diagnostics?.filesystem?.trashItems ?? '--'}
                </p>
                <p className="text-default-500 text-xs">待删除文件</p>
              </div>
              <div className="text-center p-3 rounded-lg bg-content2/50">
                <p className="text-2xl font-semibold data-value">
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
          <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
            {[
              { label: '当前分配', value: `${diagnostics?.memory?.allocMb ?? '--'} MB` },
              { label: '累计分配', value: `${diagnostics?.memory?.totalAllocMb ?? '--'} MB` },
              { label: '系统内存', value: `${diagnostics?.memory?.sysMb ?? '--'} MB` },
              { label: 'GC 次数', value: diagnostics?.memory?.numGc ?? '--' },
            ].map((item) => (
              <div key={item.label} className="text-center p-3 rounded-lg bg-content2/50">
                <p className="text-2xl font-semibold data-value">{item.value}</p>
                <p className="text-default-400 text-xs">{item.label}</p>
              </div>
            ))}
          </div>

          <Divider className="my-4" />

          <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
            <div className="text-center p-3 rounded-lg bg-content2/50">
              <p className="text-2xl font-semibold data-value">
                {diagnostics?.goroutines ?? '--'}
              </p>
              <p className="text-default-400 text-xs">Goroutines</p>
            </div>
            {diagnostics?.dataplane && (
              <>
                <div className="text-center p-3 rounded-lg bg-content2/50">
                  <div className={`inline-flex items-center gap-1 ${diagnostics.dataplane.healthy ? 'text-success' : 'text-danger'}`}>
                    {diagnostics.dataplane.healthy ? <div className="live-indicator scale-75" /> : <XCircle size={14} />}
                    <span className="text-lg font-semibold">{diagnostics.dataplane.healthy ? '健康' : '异常'}</span>
                  </div>
                  <p className="text-default-400 text-xs">数据面状态</p>
                </div>
                <div className="text-center p-3 rounded-lg bg-content2/50">
                  <p className="text-2xl font-semibold data-value">
                    {diagnostics.dataplane.version || '--'}
                  </p>
                  <p className="text-default-400 text-xs">数据面版本</p>
                </div>
                <div className="text-center p-3 rounded-lg bg-content2/50">
                  <p className="text-2xl font-semibold data-value">
                    {diagnostics.dataplane.uptimeSec 
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
