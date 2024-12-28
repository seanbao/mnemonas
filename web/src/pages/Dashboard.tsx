import { useQuery } from '@tanstack/react-query'
import { Card, CardBody, Skeleton } from '@heroui/react'
import { 
  HardDrive, 
  FileBox, 
  Activity, 
  Clock,
  CheckCircle2,
  AlertCircle,
  ArrowRight
} from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { getHealth, getStorageStats } from '@/api/files'
import { formatBytes, cn } from '@/lib/utils'

interface StatCardProps {
  title: string
  value: string | number
  icon: React.ComponentType<{ size?: number; className?: string }>
  description?: string
  iconBg: string
}

function StatCard({ title, value, icon: Icon, description, iconBg }: StatCardProps) {
  return (
    <div className="rounded-xl bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 p-5 card-hover">
      <div className="flex items-start justify-between mb-4">
        <div className={cn("p-2.5 rounded-lg", iconBg)}>
          <Icon size={20} className="text-white" />
        </div>
      </div>
      
      <p className="text-sm text-zinc-500 mb-1">{title}</p>
      <p className="text-2xl font-semibold tracking-tight">{value}</p>
      {description && (
        <p className="text-xs text-zinc-400 mt-1">{description}</p>
      )}
    </div>
  )
}

interface QuickActionProps {
  icon: React.ComponentType<{ size?: number; className?: string }>
  label: string
  description: string
  onClick: () => void
  iconBg: string
}

function QuickAction({ icon: Icon, label, description, onClick, iconBg }: QuickActionProps) {
  return (
    <button 
      className="group p-4 rounded-xl bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 text-left transition-all hover:border-violet-300 dark:hover:border-violet-500/50 card-hover"
      onClick={onClick}
    >
      <div className={cn("w-10 h-10 rounded-lg flex items-center justify-center mb-3", iconBg)}>
        <Icon size={20} className="text-white" />
      </div>
      <h3 className="font-medium text-foreground mb-0.5">{label}</h3>
      <p className="text-sm text-zinc-500">{description}</p>
      
      <div className="flex items-center gap-1 mt-3 text-sm text-violet-600 dark:text-violet-400 opacity-0 group-hover:opacity-100 transition-opacity">
        <span>进入</span>
        <ArrowRight size={14} className="group-hover:translate-x-0.5 transition-transform" />
      </div>
    </button>
  )
}

export function DashboardPage() {
  const navigate = useNavigate()
  
  const { data: health, isLoading: healthLoading } = useQuery({
    queryKey: ['health'],
    queryFn: getHealth,
    refetchInterval: 30000,
  })

  const { data: stats, isLoading: statsLoading } = useQuery({
    queryKey: ['stats'],
    queryFn: getStorageStats,
    refetchInterval: 30000,
  })

  const isLoading = healthLoading || statsLoading

  if (isLoading) {
    return (
      <div className="space-y-6">
        <div>
          <Skeleton className="w-48 h-8 rounded-lg mb-2" />
          <Skeleton className="w-64 h-4 rounded-lg" />
        </div>
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
          {[1, 2, 3, 4].map((i) => (
            <div key={i} className="rounded-xl bg-white dark:bg-zinc-900 border border-zinc-200 dark:border-zinc-800 p-5">
              <Skeleton className="w-10 h-10 rounded-lg mb-4" />
              <Skeleton className="w-20 h-4 rounded mb-2" />
              <Skeleton className="w-32 h-7 rounded" />
            </div>
          ))}
        </div>
      </div>
    )
  }

  const isHealthy = health?.status === 'healthy'

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">仪表盘</h1>
          <p className="text-zinc-500 mt-0.5 text-sm">系统概览</p>
        </div>
        <div className={cn(
          "flex items-center gap-2 px-3 py-1.5 rounded-full text-sm",
          isHealthy 
            ? "bg-emerald-50 dark:bg-emerald-500/10 text-emerald-600 dark:text-emerald-400" 
            : "bg-red-50 dark:bg-red-500/10 text-red-600 dark:text-red-400"
        )}>
          {isHealthy ? <CheckCircle2 size={14} /> : <AlertCircle size={14} />}
          <span>{isHealthy ? '运行正常' : '异常'}</span>
        </div>
      </div>

      {/* Stats Grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
        <StatCard
          title="存储使用"
          value={formatBytes(stats?.totalSize || 0)}
          icon={HardDrive}
          iconBg="bg-gradient-to-br from-blue-500 to-cyan-500"
        />
        <StatCard
          title="文件对象"
          value={stats?.totalObjects?.toLocaleString() || '0'}
          icon={FileBox}
          description="总计存储对象"
          iconBg="bg-gradient-to-br from-violet-500 to-purple-500"
        />
        <StatCard
          title="去重率"
          value={`${((stats?.dedupRatio || 1) * 100).toFixed(1)}%`}
          icon={Activity}
          description="存储效率"
          iconBg="bg-gradient-to-br from-emerald-500 to-teal-500"
        />
        <StatCard
          title="运行时间"
          value={health?.uptime || '-'}
          icon={Clock}
          iconBg="bg-gradient-to-br from-amber-500 to-orange-500"
        />
      </div>

      {/* Storage Overview */}
      <Card className="bg-white dark:bg-zinc-900 border-zinc-200 dark:border-zinc-800">
        <CardBody className="p-5">
          <div className="flex items-center justify-between mb-4">
            <h2 className="font-medium">存储概览</h2>
            <span className="text-xs text-zinc-400">实时</span>
          </div>
          
          <div className="space-y-2 mb-5">
            <div className="flex justify-between text-sm">
              <span className="text-zinc-500">已用空间</span>
              <span>{formatBytes(stats?.totalSize || 0)}</span>
            </div>
            <div className="h-2 rounded-full bg-zinc-100 dark:bg-zinc-800 overflow-hidden">
              <div 
                className="h-full rounded-full bg-gradient-to-r from-violet-500 to-purple-500"
                style={{ width: '30%' }}
              />
            </div>
          </div>

          <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
            {[
              { label: '总对象数', value: stats?.totalObjects?.toLocaleString() || '0' },
              { label: '总大小', value: formatBytes(stats?.totalSize || 0) },
              { label: '去重率', value: `${((stats?.dedupRatio || 1) * 100).toFixed(1)}%` },
              { label: '版本', value: health?.version || '-' },
            ].map((item, i) => (
              <div key={i} className="p-3 rounded-lg bg-zinc-50 dark:bg-zinc-800 text-center">
                <p className="text-lg font-medium">{item.value}</p>
                <p className="text-xs text-zinc-500">{item.label}</p>
              </div>
            ))}
          </div>
        </CardBody>
      </Card>

      {/* Quick Actions */}
      <div>
        <h2 className="font-medium mb-3">快速操作</h2>
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-3">
          <QuickAction
            icon={FileBox}
            label="文件管理"
            description="浏览和管理文件"
            onClick={() => navigate('/files')}
            iconBg="bg-gradient-to-br from-blue-500 to-cyan-500"
          />
          <QuickAction
            icon={HardDrive}
            label="存储管理"
            description="查看存储状态"
            onClick={() => navigate('/storage')}
            iconBg="bg-gradient-to-br from-emerald-500 to-teal-500"
          />
          <QuickAction
            icon={Activity}
            label="系统健康"
            description="检查系统状态"
            onClick={() => navigate('/health')}
            iconBg="bg-gradient-to-br from-amber-500 to-orange-500"
          />
          <QuickAction
            icon={Clock}
            label="版本历史"
            description="查看文件版本"
            onClick={() => navigate('/versions')}
            iconBg="bg-gradient-to-br from-violet-500 to-purple-500"
          />
        </div>
      </div>
    </div>
  )
}
