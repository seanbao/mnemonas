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
import { StatCard } from '@/components/ui/StatCard'
function StatusIndicator({ 
  status, 
  label 
}: { 
  status: boolean | 'warning' | 'unknown'
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
  const dedupSavings = stats && stats.totalSize > 0 
    ? ((stats.totalSize - stats.totalObjects) / stats.totalSize * 100).toFixed(1)
    : '0'

  return (
    <div className="h-full overflow-auto custom-scrollbar">
      <div className="p-6 space-y-6">
      {/* Header */}
      <PageHeader
        title="系统健康"
        subtitle="监控系统状态和性能指标"
        icon={Activity}
        actions={
          <Button
            color="primary"
            variant="flat"
            startContent={<RefreshCw size={16} />}
            onPress={() => refetchDiag()}
            isLoading={isLoading}
          >
            刷新
          </Button>
        }
      />

      {/* System Status */}
        <Card className="bg-content1 border-divider shadow-[var(--shadow-soft)]">
        <CardHeader className="pb-2">
          <div className="flex items-center gap-2">
            <Server size={18} className="text-accent-primary" />
            <span className="font-medium">系统状态</span>
          </div>
        </CardHeader>
        <CardBody className="pt-2">
          <div className="flex flex-wrap gap-2">
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
            <div className="mt-4 text-sm text-default-500">
              <span className="font-medium">{diagnostics.version.name}</span>
              {' '}v{diagnostics.version.version} · Go {diagnostics.version.go}
            </div>
          )}
        </CardBody>
      </Card>

      {/* Stats Grid */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard
          icon={Clock}
          title="运行时间"
          value={diagnostics?.uptimeSecs ? formatUptime(diagnostics.uptimeSecs) : '--'}
          tone="primary"
        />
        <StatCard
          icon={Cpu}
          title="内存使用"
          value={diagnostics?.memory ? `${diagnostics.memory.allocMb} MB` : '--'}
          subtitle={diagnostics?.memory ? `系统: ${diagnostics.memory.sysMb} MB` : undefined}
          tone="secondary"
        />
        <StatCard
          icon={Database}
          title="存储对象"
          value={stats?.totalObjects ?? '--'}
          subtitle={stats?.totalSize ? formatBytes(stats.totalSize) : undefined}
          tone="success"
        />
        <StatCard
          icon={HardDrive}
          title="去重率"
          value={stats?.dedupRatio ? `${(stats.dedupRatio * 100).toFixed(1)}%` : '--'}
          subtitle={dedupSavings !== '0' ? `节省 ${dedupSavings}%` : undefined}
          tone="warning"
        />
      </div>

      {/* Storage Details */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        {/* Storage Card */}
        <Card className="bg-content1 border-divider shadow-[var(--shadow-soft)]">
          <CardHeader className="pb-2">
            <div className="flex items-center gap-2">
              <HardDrive size={18} className="text-primary-500" />
              <span className="font-medium">存储详情</span>
            </div>
          </CardHeader>
          <CardBody className="pt-2 space-y-4">
            <div>
              <div className="flex justify-between text-sm mb-2">
                <span className="text-default-500">总存储</span>
                <span>{stats ? formatBytes(stats.totalSize) : '--'}</span>
              </div>
              <Progress 
                value={70} 
                color="primary" 
                className="h-2"
                aria-label="存储使用"
              />
            </div>

            <Divider />

            <div className="grid grid-cols-2 gap-4 text-sm">
              <div>
                <p className="text-default-500">对象数量</p>
                <p className="font-semibold text-2xl">{stats?.totalObjects ?? '--'}</p>
              </div>
              <div>
                <p className="text-default-500">去重比例</p>
                <p className="font-semibold text-2xl">
                  {stats?.dedupRatio ? `${(stats.dedupRatio * 100).toFixed(2)}%` : '--'}
                </p>
              </div>
            </div>
          </CardBody>
        </Card>

        {/* Trash Card */}
        <Card className="bg-content1 border-divider shadow-[var(--shadow-soft)]">
          <CardHeader className="pb-2">
            <div className="flex items-center gap-2">
              <Trash2 size={18} className="text-warning" />
              <span className="font-medium">回收站</span>
            </div>
          </CardHeader>
          <CardBody className="pt-2 space-y-4">
            <div className="grid grid-cols-2 gap-4">
              <div>
                <p className="text-default-500 text-sm">待删除文件</p>
                <p className="font-semibold text-2xl">
                  {diagnostics?.filesystem?.trashItems ?? '--'}
                </p>
              </div>
              <div>
                <p className="text-default-500 text-sm">占用空间</p>
                <p className="font-semibold text-2xl">
                  {diagnostics?.filesystem?.trashSize 
                    ? formatBytes(diagnostics.filesystem.trashSize) 
                    : '--'}
                </p>
              </div>
            </div>

            <div className="text-xs text-default-500">
              回收站文件将在 30 天后自动清理
            </div>
          </CardBody>
        </Card>
      </div>

      {/* Memory & Performance */}
      <Card className="bg-content1 border-divider shadow-[var(--shadow-soft)]">
        <CardHeader className="pb-2">
          <div className="flex items-center gap-2">
            <Cpu size={18} className="text-violet-500" />
            <span className="font-medium">内存与性能</span>
          </div>
        </CardHeader>
        <CardBody className="pt-2">
          <div className="grid grid-cols-2 md:grid-cols-4 gap-6">
            <div>
              <p className="text-default-400 text-sm">当前分配</p>
              <p className="font-semibold text-2xl">
                {diagnostics?.memory?.allocMb ?? '--'} MB
              </p>
            </div>
            <div>
              <p className="text-default-400 text-sm">累计分配</p>
              <p className="font-semibold text-2xl">
                {diagnostics?.memory?.totalAllocMb ?? '--'} MB
              </p>
            </div>
            <div>
              <p className="text-default-400 text-sm">系统内存</p>
              <p className="font-semibold text-2xl">
                {diagnostics?.memory?.sysMb ?? '--'} MB
              </p>
            </div>
            <div>
              <p className="text-default-400 text-sm">GC 次数</p>
              <p className="font-semibold text-2xl">
                {diagnostics?.memory?.numGc ?? '--'}
              </p>
            </div>
          </div>

          <Divider className="my-4" />

          <div className="grid grid-cols-2 md:grid-cols-4 gap-6">
            <div>
              <p className="text-default-400 text-sm">Goroutines</p>
              <p className="font-semibold text-2xl">
                {diagnostics?.goroutines ?? '--'}
              </p>
            </div>
            {diagnostics?.dataplane && (
              <>
                <div>
                  <p className="text-default-400 text-sm">数据面状态</p>
                  <p className="font-semibold text-2xl">
                    {diagnostics.dataplane.healthy ? '健康' : '异常'}
                  </p>
                </div>
                <div>
                  <p className="text-default-400 text-sm">数据面版本</p>
                  <p className="font-semibold text-2xl">
                    {diagnostics.dataplane.version || '--'}
                  </p>
                </div>
                <div>
                  <p className="text-default-400 text-sm">数据面运行</p>
                  <p className="font-semibold text-2xl">
                    {diagnostics.dataplane.uptimeSec 
                      ? formatUptime(diagnostics.dataplane.uptimeSec) 
                      : '--'}
                  </p>
                </div>
              </>
            )}
          </div>
        </CardBody>
      </Card>
      </div>
    </div>
  )
}
