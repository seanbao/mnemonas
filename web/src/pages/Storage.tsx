import { useQuery } from '@tanstack/react-query'
import { Button, Skeleton } from '@heroui/react'
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

// Stat card for storage metrics
function StorageStatCard({ 
  label, 
  value, 
  icon: Icon, 
  gradient,
  subValue 
}: { 
  label: string
  value: string | number
  icon: React.ElementType
  gradient: string
  subValue?: string
}) {
  return (
    <div className="rounded-xl bg-content1 border border-divider p-4 card-hover">
      <div className="flex items-start justify-between">
        <div className={`w-10 h-10 rounded-lg bg-gradient-to-br ${gradient} flex items-center justify-center`}>
          <Icon size={20} className="text-white" />
        </div>
        {subValue && (
          <span className="text-xs text-success bg-success/10 px-2 py-1 rounded-full">
            {subValue}
          </span>
        )}
      </div>
      <div className="mt-3">
        <p className="text-xl font-semibold">{value}</p>
        <p className="text-sm text-default-500">{label}</p>
      </div>
    </div>
  )
}

// Action card for maintenance operations
function MaintenanceCard({
  title,
  description,
  icon: Icon,
  gradient,
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
  gradient: string
  lastRun: string
  estimate: string
  buttonText: string
  buttonColor: 'success' | 'warning' | 'danger'
  onPress: () => void
  isDisabled?: boolean
}) {
  return (
    <div className="rounded-xl bg-content1 border border-divider p-5 card-hover">
      <div className="flex items-center gap-3 mb-4">
        <div className={`w-10 h-10 rounded-lg bg-gradient-to-br ${gradient} flex items-center justify-center`}>
          <Icon size={20} className="text-white" />
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
          <div className="w-10 h-10 rounded-lg bg-gradient-to-br from-violet-500 to-purple-500 flex items-center justify-center">
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
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <div className="w-10 h-10 rounded-lg bg-gradient-to-br from-violet-500 to-purple-500 flex items-center justify-center">
            <HardDrive size={20} className="text-white" />
          </div>
          <div>
            <h1 className="text-xl font-semibold">存储管理</h1>
            <p className="text-default-500 text-sm">CAS 内容寻址存储系统</p>
          </div>
        </div>
        <Button 
          variant="flat" 
          startContent={<RefreshCw size={16} />}
          onPress={() => refetch()}
        >
          刷新
        </Button>
      </div>

      {/* Storage Overview Card */}
      <div className="rounded-xl bg-content1 border border-divider p-5">
        <div className="flex items-center justify-between mb-4">
          <div className="flex items-center gap-2">
            <Database size={18} className="text-primary-500" />
            <span className="font-medium">存储空间使用情况</span>
          </div>
          <span className="text-sm text-default-500">
            {formatBytes(stats?.totalSize || 0)} 已使用
          </span>
        </div>
        
        <div className="space-y-2">
          <div className="h-2 rounded-full bg-content2 overflow-hidden">
            <div 
              className="h-full bg-gradient-to-r from-primary-500 to-violet-500 rounded-full"
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
        <StorageStatCard
          label="对象总数"
          value={stats?.totalObjects?.toLocaleString() || 0}
          icon={Database}
          gradient="from-blue-500 to-cyan-500"
        />
        <StorageStatCard
          label="存储大小"
          value={formatBytes(stats?.totalSize || 0)}
          icon={HardDrive}
          gradient="from-violet-500 to-purple-500"
        />
        <StorageStatCard
          label="去重率"
          value={`${((stats?.dedupRatio || 0) * 100).toFixed(1)}%`}
          icon={Sparkles}
          gradient="from-emerald-500 to-green-500"
          subValue="高效"
        />
        <StorageStatCard
          label="节省空间"
          value={formatBytes((stats?.totalSize || 0) * (stats?.dedupRatio || 0))}
          icon={TrendingUp}
          gradient="from-amber-500 to-orange-500"
        />
      </div>

      {/* Maintenance Actions */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <MaintenanceCard
          title="数据巡检 (Scrub)"
          description="验证所有数据完整性"
          icon={Activity}
          gradient="from-emerald-500 to-green-500"
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
          gradient="from-amber-500 to-orange-500"
          lastRun="功能开发中"
          estimate="即将推出"
          buttonText="开始清理（即将推出）"
          buttonColor="warning"
          onPress={() => {}}
          isDisabled
        />
      </div>
    </div>
  )
}
