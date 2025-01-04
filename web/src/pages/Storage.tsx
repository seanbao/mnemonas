import { useQuery } from '@tanstack/react-query'
import { Button, Skeleton, Chip } from '@heroui/react'
import { 
  HardDrive, 
  Activity, 
  Trash2, 
  RefreshCw,
  CheckCircle2,
  Clock,
  Database,
  Sparkles,
  TrendingUp
} from 'lucide-react'
import { getStorageStats } from '@/api/files'
import { formatBytes } from '@/lib/utils'
import { PageHeader } from '@/components/ui/PageHeader'
import { StatCard } from '@/components/ui/StatCard'

// Action card for maintenance operations
function MaintenanceCard({
  title,
  description,
  icon: Icon,
  iconClass,
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
  iconClass: string
  lastRun: string
  estimate: string
  buttonText: string
  buttonColor: 'success' | 'warning' | 'danger'
  onPress: () => void
  isDisabled?: boolean
}) {
  return (
    <div className="rounded-xl bg-content1 border border-divider p-5 card-hover shadow-[var(--shadow-soft)]">
      <div className="flex items-center gap-3 mb-4">
        <div className={`w-10 h-10 rounded-lg ${iconClass} flex items-center justify-center`}>
          <Icon size={20} className="text-current" />
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
        className="w-full"
        onPress={onPress}
        isDisabled={isDisabled}
      >
        {buttonText}
      </Button>
    </div>
  )
}

export function StoragePage() {
  const { data: stats, isLoading, refetch } = useQuery({
    queryKey: ['stats'],
    queryFn: getStorageStats,
  })

  if (isLoading) {
    return (
      <div className="space-y-6">
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
            <div key={i} className="rounded-xl bg-content1 border border-divider p-4">
              <Skeleton className="w-10 h-10 rounded-lg mb-3" />
              <Skeleton className="w-20 h-6 rounded-lg mb-1" />
              <Skeleton className="w-16 h-4 rounded-lg" />
            </div>
          ))}
        </div>
      </div>
    )
  }

  // TODO: Get actual capacity from API when available
  const totalCapacity = stats?.totalCapacity || 10 * 1024 * 1024 * 1024 // Default 10GB if not provided
  const usedPercent = Math.min(((stats?.totalSize || 0) / totalCapacity) * 100, 100)

  return (
    <div className="h-full overflow-auto custom-scrollbar">
      <div className="p-6 space-y-6">
      {/* Header */}
      <PageHeader
        title="存储管理"
        subtitle="CAS 内容寻址存储系统"
        icon={HardDrive}
        actions={
          <Button 
            variant="flat" 
            startContent={<RefreshCw size={16} />}
            onPress={() => refetch()}
          >
            刷新
          </Button>
        }
      />

      {/* Storage Overview Card */}
      <div className="rounded-xl bg-content1 border border-divider p-5 shadow-[var(--shadow-soft)]">
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center gap-2">
            <Database size={18} className="text-accent-primary" />
            <span className="font-medium">存储空间使用情况</span>
          </div>
          <span className="text-sm text-default-500">
            {formatBytes(stats?.totalSize || 0)} 已使用
          </span>
        </div>
        
        <div className="space-y-2">
          <div className="h-2 rounded-full bg-content2 overflow-hidden">
            <div 
              className="h-full bg-accent-primary rounded-full"
              style={{ width: `${usedPercent}%` }}
            />
          </div>
          <div className="flex justify-between text-sm text-default-500">
            <span>0 GB</span>
            <span>{formatBytes(totalCapacity)}</span>
          </div>
        </div>
      </div>

      {/* Stats Grid */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <StatCard
          title="对象总数"
          value={stats?.totalObjects?.toLocaleString() || 0}
          icon={Database}
          tone="primary"
        />
        <StatCard
          title="存储大小"
          value={formatBytes(stats?.totalSize || 0)}
          icon={HardDrive}
          tone="primary"
        />
        <StatCard
          title="去重率"
          value={`${((stats?.dedupRatio || 0) * 100).toFixed(1)}%`}
          icon={Sparkles}
          tone="success"
          action={
            <Chip size="sm" color="success" variant="flat">
              高效
            </Chip>
          }
        />
        <StatCard
          title="节省空间"
          value={formatBytes((stats?.totalSize || 0) * (stats?.dedupRatio || 0))}
          icon={TrendingUp}
          tone="secondary"
        />
      </div>

      {/* Maintenance Actions */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <MaintenanceCard
          title="数据巡检 (Scrub)"
          description="验证所有数据完整性"
          icon={Activity}
          iconClass="bg-accent-primary/15 text-accent-primary"
          lastRun="功能开发中"
          estimate="即将推出"
          buttonText="开始巡检（即将推出）"
          buttonColor="success"
          onPress={() => {}}
          isDisabled
        />
        
        <MaintenanceCard
          title="垃圾回收 (GC)"
          description="清理无引用的数据块"
          icon={Trash2}
          iconClass="bg-warning/15 text-warning"
          lastRun="功能开发中"
          estimate="即将推出"
          buttonText="开始清理（即将推出）"
          buttonColor="warning"
          onPress={() => {}}
          isDisabled
        />
      </div>
      </div>
    </div>
  )
}
