import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Card, CardBody, CardHeader, Button, Chip, Progress, Divider, Table, TableHeader, TableColumn, TableBody, TableRow, TableCell, Modal, ModalContent, ModalHeader, ModalBody, ModalFooter, Input, Checkbox, addToast } from '@heroui/react'
import { 
  Archive,
  HardDrive,
  ShieldCheck, 
  Play, 
  Download, 
  CheckCircle, 
  XCircle, 
  AlertCircle,
  Clock,
  Database,
  RefreshCw,
  FileWarning,
  RotateCcw,
} from 'lucide-react'
import { PageHeader } from '@/components/ui/PageHeader'
import { StatCard } from '@/components/ui/StatCard'
import { EmptyState } from '@/components/ui/EmptyState'
import {
  ApiError,
  getScrubResult,
  runScrub,
  downloadDiagnosticsExport,
  listBackupJobs,
  runBackupJob,
  runBackupRestoreDrill,
  restoreBackupJob,
  type BackupJob,
  type BackupRunResult,
  type BackupRestoreDrillResult,
  type BackupRestoreResult,
  type ScrubResult,
  type ScrubError,
} from '@/api/files'
import { formatBytes, formatDuration } from '@/lib/utils'
import { useUser } from '@/stores/auth'

function getMaintenanceLoadErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      title: '校验结果暂不可用',
      description: '维护历史或数据面当前不可用，请检查系统状态或稍后重试。',
    }
  }

  return {
    title: '加载校验结果失败',
    description: error instanceof Error ? error.message : '请稍后重试',
  }
}

function getMaintenanceActionErrorPresentation(
  error: unknown,
  fallbackTitle: string,
  unavailableTitle: string,
  unavailableDescription: string,
): { title: string; description: string; color: 'warning' | 'danger' } {
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      title: unavailableTitle,
      description: unavailableDescription,
      color: 'warning',
    }
  }

  return {
    title: fallbackTitle,
    description: error instanceof Error ? error.message : '请稍后重试',
    color: 'danger',
  }
}

function isScrubAlreadyRunningError(error: unknown): boolean {
  return error instanceof ApiError && error.status === 409 && error.message.includes('already running')
}

// Status chip component
function StatusChip({ status, warning }: { status?: string; warning?: boolean }) {
  if (!status) return null

  if (status === 'completed' && warning) {
    return (
      <Chip size="sm" color="warning" variant="flat" startContent={<FileWarning size={14} />}>
        校验完成（有警告）
      </Chip>
    )
  }
  
  const configs: Record<string, { color: 'success' | 'warning' | 'danger' | 'default'; icon: React.ReactNode; label: string }> = {
    completed: { color: 'success', icon: <CheckCircle size={14} />, label: '校验完成' },
    running: { color: 'warning', icon: <RefreshCw size={14} className="animate-spin" />, label: '校验中...' },
    failed: { color: 'danger', icon: <XCircle size={14} />, label: '校验失败' },
  }
  
  const config = configs[status] || { color: 'default', icon: <AlertCircle size={14} />, label: status }
  
  return (
    <Chip size="sm" color={config.color} variant="flat" startContent={config.icon}>
      {config.label}
    </Chip>
  )
}

// Result summary card
function ResultSummary({ result }: { result: ScrubResult }) {
  if (!result.has_result || !result.status || result.status === 'running') {
    return null
  }

  const formatCount = (value: number | undefined): string | number => value === undefined ? '--' : value
  const toneForCount = (
    value: number | undefined,
    alertTone: 'warning' | 'danger'
  ): 'default' | 'warning' | 'danger' => {
    if (value === undefined) {
      return 'default'
    }
    return value > 0 ? alertTone : 'default'
  }
  
  return (
    <div className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-4">
      <StatCard
        title="总对象数"
        value={formatCount(result.total_objects)}
        icon={Database}
        tone="primary"
      />
      <StatCard
        title="有效对象"
        value={formatCount(result.valid_objects)}
        icon={CheckCircle}
        tone="success"
      />
      <StatCard
        title="损坏对象"
        value={formatCount(result.corrupted_objects)}
        icon={AlertCircle}
        tone={toneForCount(result.corrupted_objects, 'danger')}
      />
      <StatCard
        title="缺失对象"
        value={formatCount(result.missing_objects)}
        icon={XCircle}
        tone={toneForCount(result.missing_objects, 'warning')}
      />
    </div>
  )
}

// Error list component
function ErrorList({ errors }: { errors: ScrubError[] }) {
  if (!errors || errors.length === 0) return null
  
  return (
    <div className="mt-4">
      <h4 className="text-sm font-medium mb-2 flex items-center gap-2">
        <FileWarning size={16} className="text-danger" />
        发现的问题 ({errors.length})
      </h4>
      <div className="responsive-table">
        <Table aria-label="错误列表" isStriped>
          <TableHeader>
            <TableColumn>哈希</TableColumn>
            <TableColumn>错误类型</TableColumn>
            <TableColumn>详情</TableColumn>
          </TableHeader>
          <TableBody>
            {errors.slice(0, 100).map((error, index) => (
              <TableRow key={index}>
                <TableCell>
                  <code className="text-xs">{error.hash.slice(0, 16)}...</code>
                </TableCell>
                <TableCell>
                  <Chip size="sm" color="danger" variant="flat">{error.error_type}</Chip>
                </TableCell>
                <TableCell className="text-sm">{error.message}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      {errors.length > 100 && (
        <p className="text-sm text-default-500 mt-2">
          仅显示前 100 条，共 {errors.length} 条错误
        </p>
      )}
    </div>
  )
}

function BackupStatusChip({ status, warning }: { status?: string; warning?: boolean }) {
  if (!status) {
    return <Chip size="sm" variant="flat">未运行</Chip>
  }

  if (status === 'completed' && warning) {
    return (
      <Chip size="sm" color="warning" variant="flat" startContent={<FileWarning size={14} />}>
        完成（有警告）
      </Chip>
    )
  }

  const configs: Record<string, { color: 'success' | 'warning' | 'danger' | 'default'; icon: React.ReactNode; label: string }> = {
    completed: { color: 'success', icon: <CheckCircle size={14} />, label: '完成' },
    running: { color: 'warning', icon: <RefreshCw size={14} className="animate-spin" />, label: '运行中' },
    failed: { color: 'danger', icon: <XCircle size={14} />, label: '失败' },
  }
  const config = configs[status] || { color: 'default', icon: <AlertCircle size={14} />, label: status }
  return (
    <Chip size="sm" color={config.color} variant="flat" startContent={config.icon}>
      {config.label}
    </Chip>
  )
}

function BackupHealthChip({ status }: { status: string }) {
  const configs: Record<string, { color: 'success' | 'warning' | 'danger' | 'default'; icon: React.ReactNode; label: string }> = {
    ok: { color: 'success', icon: <CheckCircle size={14} />, label: '健康' },
    running: { color: 'warning', icon: <RefreshCw size={14} className="animate-spin" />, label: '运行中' },
    manual: { color: 'default', icon: <Clock size={14} />, label: '手动任务' },
    due: { color: 'warning', icon: <Clock size={14} />, label: '待运行' },
    stale: { color: 'warning', icon: <FileWarning size={14} />, label: '备份过期' },
    failed: { color: 'danger', icon: <XCircle size={14} />, label: '异常' },
    disabled: { color: 'default', icon: <AlertCircle size={14} />, label: '已停用' },
  }
  const config = configs[status] || { color: 'default', icon: <AlertCircle size={14} />, label: status }
  return (
    <Chip size="sm" color={config.color} variant="flat" startContent={config.icon}>
      {config.label}
    </Chip>
  )
}

function formatDateTime(value?: string): string {
  if (!value) {
    return '--'
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return '--'
  }
  return date.toLocaleString('zh-CN')
}

function formatBackupDuration(value?: string): string {
  if (!value) {
    return ''
  }

  const hours = value.match(/^(\d+)h0m0s$/)
  if (hours) {
    const count = Number(hours[1])
    if (count > 0 && count % 24 === 0) {
      return `${count / 24} 天`
    }
    return `${count} 小时`
  }

  const minutes = value.match(/^(\d+)m0s$/)
  if (minutes) {
    return `${Number(minutes[1])} 分钟`
  }

  const seconds = value.match(/^(\d+)s$/)
  if (seconds) {
    return `${Number(seconds[1])} 秒`
  }

  return value
}

function getBackupTriggerLabel(trigger?: string): string {
  if (trigger === 'scheduled') {
    return '自动'
  }
  return '手动'
}

function getBackupRetentionText(job: BackupJob): string {
  if (job.type === 'restic' || job.type === 'rclone') {
    return '远端保留策略由外部工具管理'
  }
  const parts: string[] = []
  if (job.max_snapshots && job.max_snapshots > 0) {
    parts.push(`最多 ${job.max_snapshots} 个快照`)
  }
  if (job.max_age) {
    parts.push(`最长 ${formatBackupDuration(job.max_age)}`)
  }
  return parts.length > 0 ? parts.join(' · ') : '未配置自动清理'
}

function getBackupScheduleWindowText(job: BackupJob): string {
  if (!job.schedule_window_start || !job.schedule_window_end) {
    return ''
  }
  return `自动窗口: ${job.schedule_window_start}-${job.schedule_window_end}`
}

function canRunBackupRestoreDrill(job: BackupJob): boolean {
  if (job.type === 'restic' || job.type === 'rclone') {
    return true
  }
  return job.last_run?.status === 'completed'
}

function canRunBackupRestore(job: BackupJob): boolean {
  if (job.type === 'restic' || job.type === 'rclone') {
    return true
  }
  return job.type === 'local' && job.last_run?.status === 'completed'
}

function getBackupRunMetricText(result: BackupRunResult): string {
  if (result.file_count === 0 && result.total_bytes === 0 && !result.snapshot_path) {
    return '外部备份命令已完成'
  }
  return `${result.file_count} 个文件 · ${formatBytes(result.total_bytes)}`
}

function getBackupRestoreDrillMetricText(result: BackupRestoreDrillResult): string {
  if (result.file_count === 0 && result.verified_bytes === 0 && !result.restored_path) {
    return '校验命令已完成'
  }
  return `校验 ${result.file_count} 个文件 · ${formatBytes(result.verified_bytes)}`
}

function getBackupRestoreMetricText(result: BackupRestoreResult): string {
  if (result.file_count === 0 && result.verified_bytes === 0 && !result.snapshot_path) {
    return '恢复命令已完成'
  }
  return `${result.file_count} 个文件 · ${formatBytes(result.verified_bytes)}`
}

function getRestoreTargetDescription(job: BackupJob | null): string {
  if (job?.type === 'restic') {
    return '目标目录必须在 storage.root、备份来源和本地 restic 仓库之外；父目录存在，目标不存在或为空。'
  }
  if (job?.type === 'rclone') {
    return '目标目录必须在 storage.root 和备份来源之外；父目录存在，目标不存在或为空。'
  }
  return '目标目录必须在 storage.root、备份来源和备份目标之外；父目录存在，目标不存在或为空。'
}

function BackupRunSummary({ result }: { result?: BackupRunResult }) {
  if (!result) {
    return <span className="text-default-400">尚未备份</span>
  }
  return (
    <div className="space-y-1 text-sm">
      <div className="flex items-center gap-2">
        <BackupStatusChip status={result.status} warning={result.warning} />
        <span className="text-default-500">{formatDateTime(result.finished_at ?? result.started_at)}</span>
      </div>
      <div className="text-default-500">
        {getBackupTriggerLabel(result.trigger)} · {getBackupRunMetricText(result)}
      </div>
      {result.pruned_snapshots !== undefined && result.pruned_snapshots > 0 && (
        <div className="text-default-500">已清理 {result.pruned_snapshots} 个旧快照</div>
      )}
      {result.warnings && result.warnings.length > 0 && (
        <div className="text-warning">{result.warnings[0]}</div>
      )}
      {result.error_message && <div className="text-danger">{result.error_message}</div>}
    </div>
  )
}

function BackupDrillSummary({ result }: { result?: BackupRestoreDrillResult }) {
  if (!result) {
    return <span className="text-default-400">尚未演练</span>
  }
  return (
    <div className="space-y-1 text-sm">
      <div className="flex items-center gap-2">
        <BackupStatusChip status={result.status} />
        <span className="text-default-500">{formatDateTime(result.finished_at ?? result.started_at)}</span>
      </div>
      <div className="text-default-500">
        {getBackupRestoreDrillMetricText(result)}
      </div>
      {result.error_message && <div className="text-danger">{result.error_message}</div>}
    </div>
  )
}

function getBackupConflictTitle(error: unknown, fallback: string): string {
  if (!(error instanceof ApiError) || error.status !== 409) {
    return fallback
  }
  if (error.message.includes('disabled')) {
    return '备份任务已停用'
  }
  if (error.message.includes('no completed snapshots')) {
    return '暂无可演练的备份快照'
  }
  if (error.message.includes('already running')) {
    return '备份任务正在运行'
  }
  return fallback
}

function getBackupConflictDescription(error: unknown, fallback: string): string {
  if (!(error instanceof ApiError) || error.status !== 409) {
    return fallback
  }
  if (error.message.includes('disabled')) {
    return '请先在配置文件中启用该任务并重启服务。'
  }
  if (error.message.includes('no completed snapshots')) {
    return '请先完成一次成功备份，再执行恢复演练。'
  }
  if (error.message.includes('already running')) {
    return '已有备份或恢复演练正在执行，请稍后刷新状态。'
  }
  return fallback
}

export default function Maintenance() {
  const queryClient = useQueryClient()
  const user = useUser()
  const [isExporting, setIsExporting] = useState(false)
  const [isAwaitingRunningState, setIsAwaitingRunningState] = useState(false)
  const [restoreJob, setRestoreJob] = useState<BackupJob | null>(null)
  const [restoreTargetPath, setRestoreTargetPath] = useState('')
  const [restoreIncludeConfig, setRestoreIncludeConfig] = useState(false)
  const scrubResultQueryKey = ['scrub-result', user?.id ?? 'anonymous'] as const
  const backupJobsQueryKey = ['backup-jobs', user?.id ?? 'anonymous'] as const
  
  // Fetch last scrub result
  const { data: scrubResult, isLoading, error, refetch } = useQuery({
    queryKey: scrubResultQueryKey,
    queryFn: getScrubResult,
    refetchInterval: (query) => {
      // Auto-refresh while scrub is running
      const data = query.state.data
      return data?.status === 'running' ? 2000 : false
    },
  })
  const loadErrorPresentation = getMaintenanceLoadErrorPresentation(error)

  const {
    data: backupJobs = [],
    isLoading: isLoadingBackups,
    error: backupError,
    refetch: refetchBackups,
  } = useQuery({
    queryKey: backupJobsQueryKey,
    queryFn: listBackupJobs,
  })

  const handleRefreshScrubResult = async () => {
    const result = await refetch()
    if (result.error) {
      const errorPresentation = getMaintenanceActionErrorPresentation(
        result.error,
        '刷新失败',
        '校验结果暂不可用',
        '维护历史或数据面当前不可用，请检查系统状态或稍后重试。',
      )
      addToast({
        title: errorPresentation.title,
        description: errorPresentation.description,
        color: errorPresentation.color,
      })
      return
    }

    addToast({ title: '校验结果已刷新', color: 'success' })
  }

  const handleRefreshBackups = async () => {
    const result = await refetchBackups()
    if (result.error) {
      const errorPresentation = getMaintenanceActionErrorPresentation(
        result.error,
        '刷新备份任务失败',
        '备份任务暂不可用',
        '备份管理器当前不可用，请检查配置后重试。',
      )
      addToast({
        title: errorPresentation.title,
        description: errorPresentation.description,
        color: errorPresentation.color,
      })
      return
    }
    addToast({ title: '备份任务已刷新', color: 'success' })
  }

  const openRestoreModal = (job: BackupJob) => {
    setRestoreJob(job)
    setRestoreTargetPath('')
    setRestoreIncludeConfig(job.type === 'local' && Boolean(job.include_config))
  }

  const closeRestoreModal = () => {
    if (restoreMutation.isPending) {
      return
    }
    setRestoreJob(null)
    setRestoreTargetPath('')
    setRestoreIncludeConfig(false)
  }
  
  // Run scrub mutation
  const scrubMutation = useMutation({
    mutationFn: () => runScrub(),
    onSuccess: (result) => {
      if (result.status === 'running') {
        void queryClient.refetchQueries({ queryKey: scrubResultQueryKey, type: 'active' }).finally(() => {
          setIsAwaitingRunningState(false)
        })
      } else {
        void queryClient.invalidateQueries({ queryKey: scrubResultQueryKey })
        setIsAwaitingRunningState(false)
      }

      const title = result.warning
        ? (result.message ?? '数据校验完成，但存在警告')
        : (result.status === 'running' ? '数据校验已启动' : '数据校验已完成')
      addToast({ title, color: result.warning ? 'warning' : 'success' })
    },
    onError: (error: unknown) => {
      if (isScrubAlreadyRunningError(error)) {
        void queryClient.refetchQueries({ queryKey: scrubResultQueryKey, type: 'active' }).finally(() => {
          setIsAwaitingRunningState(false)
        })
        addToast({
          title: '数据校验已在运行',
          description: '已有校验任务正在执行，已同步最新状态。',
          color: 'warning',
        })
        return
      }

      setIsAwaitingRunningState(false)
      const errorPresentation = getMaintenanceActionErrorPresentation(
        error,
        '启动校验失败',
        '数据校验暂不可用',
        '数据面或维护服务当前不可用，请检查系统状态后重试。',
      )
      addToast({
        title: errorPresentation.title,
        description: errorPresentation.description,
        color: errorPresentation.color,
      })
    },
    onMutate: () => {
      setIsAwaitingRunningState(true)
    },
  })

  const runBackupMutation = useMutation({
    mutationFn: (jobId: string) => runBackupJob(jobId),
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: backupJobsQueryKey })
      addToast({
        title: result.warning ? '备份完成但有警告' : '备份已完成',
        description: getBackupRunMetricText(result).replace(' · ', '，'),
        color: result.warning ? 'warning' : 'success',
      })
    },
    onError: (error: unknown) => {
      void queryClient.invalidateQueries({ queryKey: backupJobsQueryKey })
      const errorPresentation = getMaintenanceActionErrorPresentation(
        error,
        '执行备份失败',
        '备份任务暂不可用',
        '备份管理器当前不可用，请检查配置后重试。',
      )
      addToast({
        title: getBackupConflictTitle(error, errorPresentation.title),
        description: getBackupConflictDescription(error, errorPresentation.description),
        color: error instanceof ApiError && error.status === 409 ? 'warning' : errorPresentation.color,
      })
    },
  })

  const restoreDrillMutation = useMutation({
    mutationFn: (jobId: string) => runBackupRestoreDrill(jobId, false),
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: backupJobsQueryKey })
      addToast({
        title: '恢复演练已完成',
        description: getBackupRestoreDrillMetricText(result).replace(' · ', '，'),
        color: 'success',
      })
    },
    onError: (error: unknown) => {
      void queryClient.invalidateQueries({ queryKey: backupJobsQueryKey })
      const errorPresentation = getMaintenanceActionErrorPresentation(
        error,
        '执行恢复演练失败',
        '恢复演练暂不可用',
        '备份管理器当前不可用，请检查配置后重试。',
      )
      addToast({
        title: getBackupConflictTitle(error, errorPresentation.title),
        description: getBackupConflictDescription(error, errorPresentation.description),
        color: error instanceof ApiError && error.status === 409 ? 'warning' : errorPresentation.color,
      })
    },
  })

  const restoreMutation = useMutation({
    mutationFn: (req: { jobId: string; targetPath: string; includeConfig: boolean }) => restoreBackupJob(req.jobId, req.targetPath, req.includeConfig),
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: backupJobsQueryKey })
      addToast({
        title: '备份已恢复',
        description: `${getBackupRestoreMetricText(result)}，目标: ${result.target_path}`,
        color: 'success',
      })
      setRestoreJob(null)
      setRestoreTargetPath('')
      setRestoreIncludeConfig(false)
    },
    onError: (error: unknown) => {
      const errorPresentation = getMaintenanceActionErrorPresentation(
        error,
        '恢复备份失败',
        '恢复功能暂不可用',
        '备份管理器当前不可用，请检查配置后重试。',
      )
      addToast({
        title: getBackupConflictTitle(error, errorPresentation.title),
        description: getBackupConflictDescription(error, errorPresentation.description),
        color: error instanceof ApiError && error.status === 409 ? 'warning' : errorPresentation.color,
      })
    },
  })
  
  // Handle export
  const handleExport = async () => {
    setIsExporting(true)
    try {
      await downloadDiagnosticsExport()
      addToast({ title: '诊断信息导出已开始', color: 'success' })
    } catch (error) {
      const errorPresentation = getMaintenanceActionErrorPresentation(
        error,
        '导出诊断信息失败',
        '诊断导出暂不可用',
        '诊断导出服务当前不可用，请检查系统状态后重试。',
      )
      addToast({
        title: errorPresentation.title,
        description: errorPresentation.description,
        color: errorPresentation.color,
      })
    } finally {
      setIsExporting(false)
    }
  }
  
  const isRunning = scrubResult?.status === 'running' || isAwaitingRunningState
  
  return (
    <div className="h-full overflow-auto custom-scrollbar">
      <div className="space-y-6 p-4 sm:p-6">
      <PageHeader
        title="系统维护"
        subtitle="数据校验、备份与诊断工具"
        icon={ShieldCheck}
        actions={
          <Button
            className="btn-secondary rounded-lg"
            startContent={<Download size={18} />}
            isLoading={isExporting}
            onPress={handleExport}
          >
            导出诊断信息
          </Button>
        }
      />
      
      {/* Scrub Card */}
      <Card className="card-meridian">
        <CardHeader className="flex flex-col items-start gap-3 pb-2 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-accent-primary/15">
              <ShieldCheck size={20} className="text-accent-primary" />
            </div>
            <div>
              <h3 className="font-semibold">数据完整性校验</h3>
              <p className="text-xs text-default-500">验证所有存储对象的 BLAKE3 哈希值</p>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {scrubResult && <StatusChip status={scrubResult.status} warning={scrubResult.warning} />}
            <Button
              className="btn-primary rounded-lg shadow-md"
              startContent={isRunning ? <RefreshCw size={18} className="animate-spin" /> : <Play size={18} />}
              isLoading={scrubMutation.isPending}
              isDisabled={isRunning}
              onPress={() => scrubMutation.mutate()}
            >
              {isRunning ? '校验中...' : '开始校验'}
            </Button>
          </div>
        </CardHeader>
        <Divider />
        <CardBody>
          {isLoading ? (
            <div className="flex items-center justify-center py-8">
              <RefreshCw size={24} className="animate-spin text-default-400" />
            </div>
          ) : error ? (
            <div className="flex items-center justify-center py-8">
              <EmptyState
                icon={AlertCircle}
                title={loadErrorPresentation.title}
                description={loadErrorPresentation.description}
                action={
                  <Button variant="bordered" className="rounded-lg" onPress={handleRefreshScrubResult}>
                    重新加载
                  </Button>
                }
              />
            </div>
          ) : scrubResult?.has_result ? (
            <>
              {/* Meta info */}
              <div className="flex flex-wrap items-center gap-4 text-sm text-default-500">
                {scrubResult.id && (
                  <div className="flex items-center gap-1">
                    <Database size={14} />
                    <span>任务 ID: {scrubResult.id}</span>
                  </div>
                )}
                {scrubResult.start_time && (
                  <div className="flex items-center gap-1">
                    <Clock size={14} />
                    <span>开始: {new Date(scrubResult.start_time).toLocaleString('zh-CN')}</span>
                  </div>
                )}
                {scrubResult.duration_ms !== undefined && scrubResult.status !== 'running' && (
                  <div className="flex items-center gap-1">
                    <Clock size={14} />
                    <span>耗时: {formatDuration(scrubResult.duration_ms)}</span>
                  </div>
                )}
                {scrubResult.total_size !== undefined && (
                  <div className="flex items-center gap-1">
                    <Database size={14} />
                    <span>数据量: {formatBytes(scrubResult.total_size)}</span>
                  </div>
                )}
              </div>
              
              {/* Progress indicator while running */}
              {isRunning && (
                <div className="mt-4">
                  <Progress
                    size="sm"
                    isIndeterminate
                    aria-label="校验进行中"
                    className="max-w-full"
                  />
                  <p className="text-sm text-default-500 mt-2">正在校验数据完整性，这可能需要一些时间...</p>
                </div>
              )}
              
              {/* Result summary */}
              <ResultSummary result={scrubResult} />

              {scrubResult.warning && scrubResult.status !== 'running' && (
                <div className="mt-4 p-3 bg-warning/10 rounded-lg border border-warning/20">
                  <div className="flex items-start gap-2">
                    <FileWarning size={16} className="mt-0.5 text-warning" />
                    <div>
                      <p className="text-sm text-warning">本次校验完成，但存在警告</p>
                      {scrubResult.message && (
                        <p className="text-sm text-warning mt-1">{scrubResult.message}</p>
                      )}
                    </div>
                  </div>
                </div>
              )}
              
              {/* Error message */}
              {scrubResult.error_message && (
                <div className="mt-4 p-3 bg-danger/10 rounded-lg border border-danger/20">
                  <p className="text-sm text-danger">{scrubResult.error_message}</p>
                </div>
              )}
              
              {/* Error list */}
              {scrubResult.errors && <ErrorList errors={scrubResult.errors} />}
            </>
          ) : (
            <div className="text-center py-8 text-default-500">
              <ShieldCheck size={48} className="mx-auto mb-4 opacity-30" />
              <p>尚未执行过数据校验</p>
              <p className="text-sm mt-1">点击"开始校验"来验证所有存储数据的完整性</p>
            </div>
          )}
        </CardBody>
      </Card>

      {/* Backup Jobs Card */}
      <Card className="card-meridian">
        <CardHeader className="flex flex-col items-start gap-3 pb-2 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-accent-primary/15">
              <Archive size={20} className="text-accent-primary" />
            </div>
            <div>
              <h3 className="font-semibold">备份任务与恢复演练</h3>
              <p className="text-xs text-default-500">执行本地快照或远端备份任务，并校验恢复路径</p>
            </div>
          </div>
          <Button
            variant="bordered"
            className="rounded-lg"
            startContent={<RefreshCw size={18} />}
            onPress={handleRefreshBackups}
          >
            刷新任务
          </Button>
        </CardHeader>
        <Divider />
        <CardBody>
          {isLoadingBackups ? (
            <div className="flex items-center justify-center py-8">
              <RefreshCw size={24} className="animate-spin text-default-400" />
            </div>
          ) : backupError ? (
            <div className="flex items-center justify-center py-8">
              <EmptyState
                icon={AlertCircle}
                title={backupError instanceof ApiError && backupError.isUnavailable ? '备份任务暂不可用' : '加载备份任务失败'}
                description={backupError instanceof ApiError && backupError.isUnavailable
                  ? '备份管理器当前不可用，请检查配置后重试。'
                  : (backupError instanceof Error ? backupError.message : '请稍后重试')}
                action={
                  <Button variant="bordered" className="rounded-lg" onPress={handleRefreshBackups}>
                    重新加载
                  </Button>
                }
              />
            </div>
          ) : backupJobs.length === 0 ? (
            <div className="text-center py-8 text-default-500">
              <HardDrive size={48} className="mx-auto mb-4 opacity-30" />
              <p>尚未配置备份任务</p>
              <p className="text-sm mt-1">在 config.toml 中添加 [[backup.jobs]] 后重启服务。</p>
            </div>
          ) : (
            <div className="responsive-table">
              <Table aria-label="备份任务列表">
                <TableHeader>
                  <TableColumn>任务</TableColumn>
                  <TableColumn>目标</TableColumn>
                  <TableColumn>计划与保留</TableColumn>
                  <TableColumn>最近备份</TableColumn>
                  <TableColumn>恢复演练</TableColumn>
                  <TableColumn>操作</TableColumn>
                </TableHeader>
                <TableBody>
                  {backupJobs.map((job: BackupJob) => {
                    const isRunningBackup = runBackupMutation.isPending && runBackupMutation.variables === job.id
                    const isRunningDrill = restoreDrillMutation.isPending && restoreDrillMutation.variables === job.id
                    const isRunningRestore = restoreMutation.isPending && restoreMutation.variables?.jobId === job.id
                    const isBusy = job.running || isRunningBackup || isRunningDrill || isRunningRestore
                    return (
                      <TableRow key={job.id}>
                        <TableCell>
                          <div className="space-y-1">
                            <div className="flex items-center gap-2">
                              <span className="font-medium">{job.name}</span>
                              <BackupHealthChip status={job.health_status} />
                              {job.running && <BackupStatusChip status="running" />}
                            </div>
                            <div className="text-xs text-default-500">
                              {job.id} · {job.type}
                            </div>
                            <div className="max-w-[22rem] truncate text-xs text-default-400" title={job.source}>
                              来源: {job.source}
                            </div>
                          </div>
                        </TableCell>
                        <TableCell>
                          <div className="max-w-[20rem] truncate text-sm" title={job.destination}>
                            {job.destination}
                          </div>
                          <div className="text-xs text-default-500">
                            {job.verify_after_backup ? '备份后校验' : '备份后不校验'}
                            {job.include_config ? ' · 包含配置' : ''}
                          </div>
                          {job.command && (
                            <div className="max-w-[20rem] truncate text-xs text-default-400" title={job.command}>
                              命令: {job.command}
                            </div>
                          )}
                        </TableCell>
                        <TableCell>
                          <div className="space-y-1 text-sm">
                            <div className="flex items-center gap-1 text-default-700">
                              <Clock size={14} />
                              <span>{job.disabled ? '任务已停用' : job.schedule_interval ? `每 ${formatBackupDuration(job.schedule_interval)}` : '手动执行'}</span>
                            </div>
                            <div className="text-xs text-default-500">
                              {job.next_run_at ? `下次运行: ${formatDateTime(job.next_run_at)}` : '不会自动运行'}
                            </div>
                            {getBackupScheduleWindowText(job) && (
                              <div className="text-xs text-default-500">
                                {getBackupScheduleWindowText(job)}
                              </div>
                            )}
                            <div className="text-xs text-default-500">
                              {getBackupRetentionText(job)}
                            </div>
                            {job.health_message && (
                              <div className={job.health_status === 'failed' ? 'text-xs text-danger' : 'text-xs text-default-400'}>
                                {job.health_message}
                              </div>
                            )}
                          </div>
                        </TableCell>
                        <TableCell><BackupRunSummary result={job.last_run} /></TableCell>
                        <TableCell><BackupDrillSummary result={job.last_restore_drill} /></TableCell>
                        <TableCell>
                          <div className="flex flex-wrap gap-2">
                            <Button
                              size="sm"
                              className="rounded-lg"
                              color="primary"
                              startContent={isRunningBackup ? <RefreshCw size={16} className="animate-spin" /> : <Archive size={16} />}
                              isLoading={isRunningBackup}
                              isDisabled={isBusy || job.disabled}
                              onPress={() => runBackupMutation.mutate(job.id)}
                            >
                              立即备份
                            </Button>
                            <Button
                              size="sm"
                              variant="bordered"
                              className="rounded-lg"
                              startContent={isRunningDrill ? <RefreshCw size={16} className="animate-spin" /> : <RotateCcw size={16} />}
                              isLoading={isRunningDrill}
                              isDisabled={isBusy || job.disabled || !canRunBackupRestoreDrill(job)}
                              onPress={() => restoreDrillMutation.mutate(job.id)}
                            >
                              恢复演练
                            </Button>
                            <Button
                              size="sm"
                              variant="bordered"
                              className="rounded-lg"
                              startContent={isRunningRestore ? <RefreshCw size={16} className="animate-spin" /> : <HardDrive size={16} />}
                              isLoading={isRunningRestore}
                              isDisabled={isBusy || job.disabled || !canRunBackupRestore(job)}
                              onPress={() => openRestoreModal(job)}
                            >
                              恢复
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </div>
          )}
        </CardBody>
      </Card>
      
      {/* Info Card */}
      <Card className="card-meridian">
        <CardBody className="text-sm text-default-600">
          <h4 className="font-medium mb-2">维护建议</h4>
          <ul className="list-disc list-inside space-y-1">
            <li>校验会读取每个存储块并重新计算 BLAKE3 哈希值</li>
            <li>对比计算的哈希与存储的哈希来检测数据损坏</li>
            <li>大量数据时校验可能需要较长时间</li>
            <li>本地备份任务应写入 storage.root 之外的磁盘、挂载点或快照目标</li>
            <li>restic/rclone 任务会调用外部工具执行备份与校验</li>
            <li>本地恢复演练会复制最近快照并通过 manifest 校验</li>
            <li>restic/rclone 恢复会先写入独立目录，并在安装前执行恢复校验</li>
          </ul>
        </CardBody>
      </Card>

      <Modal
        isOpen={restoreJob !== null}
        onClose={closeRestoreModal}
        classNames={{ base: 'bg-content1 border border-divider' }}
      >
        <ModalContent>
          <ModalHeader className="flex flex-col gap-1">
            <div className="flex items-center gap-3">
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-warning/10">
                <HardDrive size={20} className="text-warning" />
              </div>
              <span>恢复备份到目录</span>
            </div>
          </ModalHeader>
          <ModalBody>
            <div className="space-y-4">
              {restoreJob && (
                <div className="rounded-lg border border-divider bg-content2/50 p-4 text-sm">
                  <div className="font-medium">{restoreJob.name}</div>
                  <div className="mt-1 text-default-500">{restoreJob.id} · {restoreJob.type}</div>
                  <div className="mt-1 truncate text-default-400" title={restoreJob.destination}>
                    备份目标: {restoreJob.destination}
                  </div>
                </div>
              )}
              <Input
                label="目标目录"
                placeholder="/mnt/restore/mnemonas"
                value={restoreTargetPath}
                onValueChange={setRestoreTargetPath}
                isDisabled={restoreMutation.isPending}
                description={getRestoreTargetDescription(restoreJob)}
              />
              {restoreJob?.type === 'local' && (
                <Checkbox
                  isSelected={restoreIncludeConfig}
                  onValueChange={setRestoreIncludeConfig}
                  isDisabled={restoreMutation.isPending}
                >
                  同时恢复备份中的配置文件
                </Checkbox>
              )}
              <div className="flex items-start gap-2 rounded-lg border border-warning/20 bg-warning/10 p-3 text-sm text-warning">
                <AlertCircle size={16} className="mt-0.5 shrink-0" />
                <span>恢复不会覆盖当前数据目录。请先恢复到独立目录，人工确认后再切换服务配置或迁移数据。</span>
              </div>
            </div>
          </ModalBody>
          <ModalFooter>
            <Button variant="light" className="rounded-lg" onPress={closeRestoreModal} isDisabled={restoreMutation.isPending}>
              取消
            </Button>
            <Button
              color="warning"
              className="rounded-lg"
              isLoading={restoreMutation.isPending}
              isDisabled={!restoreJob || restoreTargetPath.trim() === ''}
              onPress={() => {
                if (!restoreJob) return
                restoreMutation.mutate({
                  jobId: restoreJob.id,
                  targetPath: restoreTargetPath.trim(),
                  includeConfig: restoreIncludeConfig,
                })
              }}
            >
              开始恢复
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
      </div>
    </div>
  )
}
