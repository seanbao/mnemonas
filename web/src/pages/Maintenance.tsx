import { useEffect, useRef, useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useSearchParams } from 'react-router-dom'
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
  ListChecks,
  Copy,
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
  checkBackupRetentionJob,
  runBackupRestoreDrill,
  previewBackupRestoreJob,
  previewBatchBackupRestore,
  restoreBackupJob,
  runBatchBackupRestore,
  verifyBackupRestoreJob,
  downloadBackupRestoreReport,
  type BackupBatchRestoreItemResult,
  type BackupBatchRestoreItemRequest,
  type BackupBatchRestorePreviewResult,
  type BackupBatchRestoreResult,
  type BackupJob,
  type BackupRetentionCheckResult,
  type BackupRestorePreflightCheck,
  type BackupRunResult,
  type BackupRestoreDrillResult,
  type BackupRestorePreviewResult,
  type BackupRestoreResult,
  type BackupRestoreVerifyResult,
  type ScrubResult,
  type ScrubError,
} from '@/api/files'
import { cn, copyTextToClipboard, formatBytes, formatDuration, hasControlCharacter } from '@/lib/utils'
import { GENERIC_ACTION_ERROR_DESCRIPTION, GENERIC_LOAD_ERROR_DESCRIPTION, getUserFacingErrorDescription } from '@/lib/apiMessages'
import { backupJobNeedsAttention, getBackupAttentionNextSteps, getBackupAttentionReasons } from '@/lib/backupAttention'
import { redactDiagnosticSecretFragments } from '@/lib/diagnosticMessages'
import { useUser } from '@/stores/auth'

type AbortControllerRef = { current: AbortController | null }
type ScrubMutationRequest = { signal: AbortSignal }
type BackupJobMutationRequest = { jobId: string; signal: AbortSignal }
type RestorePreviewMutationRequest = { jobId: string; targetPath: string; includeConfig: boolean; signal: AbortSignal }
type RestoreMutationRequest = RestorePreviewMutationRequest
type RestoreVerifyMutationRequest = { jobId: string; targetPath: string; signal: AbortSignal }
type BatchRestoreMutationRequest = { items: BackupBatchRestoreItemRequest[]; signal: AbortSignal }
type RestorePreviewRequestSnapshot = Omit<RestorePreviewMutationRequest, 'signal'>
const clipboardWriteFailureDescription = '请检查浏览器剪贴板权限。'

function createActionAbortController(ref: AbortControllerRef): AbortController {
  ref.current?.abort()
  const controller = new AbortController()
  ref.current = controller
  return controller
}

function clearActionAbortController(ref: AbortControllerRef, signal: AbortSignal): void {
  if (ref.current?.signal === signal) {
    ref.current = null
  }
}

function abortActionControllers(refs: AbortControllerRef[]): void {
  refs.forEach((ref) => {
    ref.current?.abort()
    ref.current = null
  })
}

function isAbortError(error: unknown): boolean {
  return error instanceof Error && error.name === 'AbortError'
}

function getMaintenanceLoadErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      title: '校验结果暂不可用',
      description: '维护历史或数据面当前不可用，请检查设备状态或稍后重试。',
    }
  }

  return {
    title: '加载校验结果失败',
    description: getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION),
  }
}

function getBackupLoadErrorPresentation(error: unknown): { title: string; description: string } {
  if (error instanceof ApiError && error.isUnavailable) {
    return {
      title: '备份任务暂不可用',
      description: '备份管理器当前不可用，请检查配置后重试。',
    }
  }

  return {
    title: '加载备份任务失败',
    description: getUserFacingErrorDescription(error, GENERIC_LOAD_ERROR_DESCRIPTION),
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
    description: getUserFacingErrorDescription(error, GENERIC_ACTION_ERROR_DESCRIPTION),
    color: 'danger',
  }
}

function isScrubAlreadyRunningError(error: unknown): boolean {
  return error instanceof ApiError && error.status === 409 && diagnosticMessageIncludes(error.message, 'already running')
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
  
  const config = configs[status] || { color: 'default', icon: <AlertCircle size={14} />, label: '未知状态' }
  
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
    <div className="mt-4 grid grid-cols-2 gap-2 sm:gap-3 md:grid-cols-4">
      <StatCard
        title="总对象数"
        value={formatCount(result.total_objects)}
        icon={Database}
        tone="primary"
        density="compact"
      />
      <StatCard
        title="有效对象"
        value={formatCount(result.valid_objects)}
        icon={CheckCircle}
        tone="success"
        density="compact"
      />
      <StatCard
        title="损坏对象"
        value={formatCount(result.corrupted_objects)}
        icon={AlertCircle}
        tone={toneForCount(result.corrupted_objects, 'danger')}
        density="compact"
      />
      <StatCard
        title="缺失对象"
        value={formatCount(result.missing_objects)}
        icon={XCircle}
        tone={toneForCount(result.missing_objects, 'warning')}
        density="compact"
      />
    </div>
  )
}

function getRunningScrubProgressText(result: ScrubResult | undefined): string {
  if (
    result?.valid_objects !== undefined
    && result.total_objects !== undefined
  ) {
    const formatCount = (value: number): string => new Intl.NumberFormat('zh-CN').format(value)
    return `正在校验数据完整性，已验证 ${formatCount(result.valid_objects)} / ${formatCount(result.total_objects)} 个对象`
  }

  return '正在校验数据完整性'
}

const scrubErrorTypeLabels: Record<string, string> = {
  corrupted: '校验不一致',
  missing: '对象缺失',
  io_error: '读取失败',
}

const scrubErrorMessagesByBackendMessage: Record<string, string> = {
  'object failed integrity verification': '对象内容与索引记录不一致，请检查存储介质并从备份恢复。',
  'object is missing': '对象数据缺失，请从备份恢复受影响文件。',
  'object could not be read': '对象读取失败，请检查磁盘或权限状态。',
  'object verification failed': '对象校验失败，请查看服务日志并确认备份状态。',
}

const scrubErrorMessagesByType: Record<string, string> = {
  corrupted: scrubErrorMessagesByBackendMessage['object failed integrity verification'],
  missing: scrubErrorMessagesByBackendMessage['object is missing'],
  io_error: scrubErrorMessagesByBackendMessage['object could not be read'],
}

const scrubResultMessagesByBackendMessage: Record<string, string> = {
  'scrub completed with persistence warning': '校验结果已完成，但历史记录保存不完整；建议下载诊断包并检查服务日志。',
  'scrub failed; check server logs for details': '数据校验未完成；建议下载诊断包并检查服务日志。',
}

function normalizeDiagnosticMessageKey(value: string): string {
  return value.trim().replace(/\s+/g, ' ').toLowerCase()
}

function diagnosticMessageIncludes(value: string, expected: string): boolean {
  return normalizeDiagnosticMessageKey(value).includes(expected)
}

function getScrubErrorTypeLabel(errorType: string): string {
  return scrubErrorTypeLabels[normalizeDiagnosticMessageKey(errorType)] ?? '校验失败'
}

function getScrubErrorDisplayMessage(error: ScrubError): string {
  const message = scrubErrorMessagesByBackendMessage[normalizeDiagnosticMessageKey(error.message)]
  if (message) {
    return message
  }

  return scrubErrorMessagesByType[normalizeDiagnosticMessageKey(error.error_type)] ?? '对象校验失败，请查看服务日志并确认备份状态。'
}

function getScrubResultDisplayMessage(message: string): string {
  return scrubResultMessagesByBackendMessage[normalizeDiagnosticMessageKey(message)]
    ?? '数据校验结果已记录，请下载诊断包并检查服务日志。'
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
                  <Chip size="sm" color="danger" variant="flat">{getScrubErrorTypeLabel(error.error_type)}</Chip>
                </TableCell>
                <TableCell className="text-sm">{getScrubErrorDisplayMessage(error)}</TableCell>
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
  const config = configs[status] || { color: 'default', icon: <AlertCircle size={14} />, label: '未知状态' }
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
  const config = configs[status] || { color: 'default', icon: <AlertCircle size={14} />, label: '未知状态' }
  return (
    <Chip size="sm" color={config.color} variant="flat" startContent={config.icon}>
      {config.label}
    </Chip>
  )
}

function BackupPolicyChip({ status, staleLabel = '过期' }: { status: string; staleLabel?: string }) {
  const configs: Record<string, { color: 'success' | 'warning' | 'danger' | 'default'; icon: React.ReactNode; label: string }> = {
    ok: { color: 'success', icon: <CheckCircle size={14} />, label: '已确认' },
    due: { color: 'warning', icon: <Clock size={14} />, label: '待验证' },
    stale: { color: 'warning', icon: <FileWarning size={14} />, label: staleLabel },
    warning: { color: 'warning', icon: <FileWarning size={14} />, label: '需确认' },
    failed: { color: 'danger', icon: <XCircle size={14} />, label: '失败' },
    running: { color: 'warning', icon: <RefreshCw size={14} className="animate-spin" />, label: '运行中' },
    disabled: { color: 'default', icon: <AlertCircle size={14} />, label: '已停用' },
  }
  const config = configs[status] || { color: 'default', icon: <AlertCircle size={14} />, label: '未知状态' }
  return (
    <Chip size="sm" color={config.color} variant="flat" startContent={config.icon}>
      {config.label}
    </Chip>
  )
}

function getBackupPolicyStatusLabel(status: string, staleLabel = '过期'): string {
  const labels: Record<string, string> = {
    ok: '已确认',
    due: '待验证',
    stale: staleLabel,
    warning: '需确认',
    failed: '失败',
    running: '运行中',
    disabled: '已停用',
  }
  return labels[status] ?? '未知状态'
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

  const match = value.match(/^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/)
  if (!match || match[0] === '') {
    return '未知时长'
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

function getBackupJobFocusElementId(jobId: string): string {
  return `backup-job-${encodeURIComponent(jobId)}`
}

function getBackupTriggerLabel(trigger?: string): string {
  const normalized = trigger?.trim().toLowerCase()
  if (!normalized || normalized === 'manual') {
    return '手动'
  }
  if (normalized === 'scheduled') {
    return '自动'
  }
  return '未知触发方式'
}

const backupDiagnosticMessagesByBackendMessage: Record<string, string> = {
  'last successful backup completed recently': '最近一次备份成功完成。',
  'latest backup failed but a previous snapshot is available': '最近一次备份失败，但仍有可用历史快照。',
  'backup job disabled': '备份任务已停用。',
  'manifest missing': '清单文件缺失',
  'check command failed': '检测命令执行失败',
  'disk full': '磁盘空间不足',
  'restore failed': '恢复失败',
  'verify failed': '恢复校验失败',
  'restore drill ok': '恢复演练仍在预期窗口内',
  'restore drill due': '需要执行恢复演练',
  'restore drill stale': '恢复演练已过期',
  'restore drill failed': '最近一次恢复演练失败',
  'restore drill running': '恢复演练正在运行',
  'no successful backup yet': '尚无成功备份，先完成备份再执行恢复演练',
  'restore target already exists': '恢复目标已存在',
  'all batch restore items failed': '所有批量恢复项目均失败',
  'batch restore preflight failed before writes; no target data was written': '批量恢复预检未通过，未写入任何目标数据',
  'batch restore preflight failed before this item started': '批量恢复预检未通过，该项目未开始写入',
}

function getBackupDiagnosticDisplayMessageForNormalized(normalized: string): string | null {
  const partialBatchFailure = normalized.match(/^(\d+) of (\d+) batch restore items failed$/)
  if (partialBatchFailure) {
    return `${Number(partialBatchFailure[1])} / ${Number(partialBatchFailure[2])} 个批量恢复项目失败`
  }

  const batchTargetConflict = normalized.match(/^restore target already exists: restore target conflicts with batch item (\d+)$/)
  if (batchTargetConflict) {
    return `恢复目标与第 ${Number(batchTargetConflict[1]) + 1} 项重复或存在父子嵌套。`
  }
  return backupDiagnosticMessagesByBackendMessage[normalized] ?? null
}

function getBackupDiagnosticDisplayMessage(message: string): string {
  const normalized = normalizeDiagnosticMessageKey(message)
  const indexedItemFailure = normalized.match(/^item (\d+): (.+)$/)
  if (indexedItemFailure) {
    const itemNumber = Number(indexedItemFailure[1]) + 1
    const itemMessage = getBackupDiagnosticDisplayMessageForNormalized(indexedItemFailure[2])
    if (itemMessage) {
      return `项目 ${itemNumber}：${itemMessage}`
    }
  }

  return getBackupDiagnosticDisplayMessageForNormalized(normalized) ?? redactDiagnosticSecretFragments(message)
}

function getRestoreDrillDisplayMessage(message?: string): string {
  return message ? getBackupDiagnosticDisplayMessage(message) : '尚未演练'
}

function getBackupRetentionText(job: BackupJob): string {
  if ((job.type === 'restic' || job.type === 'rclone') && job.retention_message) {
    return job.retention_message
  }
  const parts: string[] = []
  if (job.max_snapshots && job.max_snapshots > 0) {
    parts.push(`最多 ${job.max_snapshots} 个快照`)
  }
  if (job.max_age) {
    parts.push(`最长 ${formatBackupDuration(job.max_age)}`)
  }
  return parts.length > 0 ? parts.join(' · ') : (job.retention_message || '未配置自动清理')
}

function getBackupRetentionCheckMetricText(result: BackupRetentionCheckResult): string {
  if (result.status === 'running') {
    return '检测中'
  }
  if (result.status === 'failed') {
    return result.error_message ? `检测失败：${getBackupDiagnosticDisplayMessage(result.error_message)}` : '检测失败'
  }
  if (result.snapshot_count !== undefined && result.snapshot_count > 0) {
    return `${result.snapshot_count} 个快照`
  }
  if (result.file_count !== undefined && result.file_count > 0) {
    return `${result.file_count} 个文件 · ${formatBytes(result.total_bytes ?? 0)}`
  }
  return '未发现可恢复内容'
}

type MaintenanceTaskResult = {
  status: string
  warning?: boolean
  warnings?: string[]
  error_message?: string
}

function hasMaintenanceTaskWarning(result: MaintenanceTaskResult): boolean {
  return result.warning === true || (result.warnings?.length ?? 0) > 0
}

function getMaintenanceTaskToastColor(result: MaintenanceTaskResult): 'success' | 'warning' | 'danger' {
  if (result.status === 'failed') {
    return 'danger'
  }
  if (result.status === 'running' || hasMaintenanceTaskWarning(result)) {
    return 'warning'
  }
  return 'success'
}

function getMaintenanceTaskDescription(result: MaintenanceTaskResult, fallback: string): string {
  const diagnosticMessage = result.error_message ?? result.warnings?.[0]
  if (diagnosticMessage) {
    return getBackupDiagnosticDisplayMessage(diagnosticMessage)
  }
  return fallback
}

function getBackupRetentionCheckTime(result?: BackupRetentionCheckResult): string {
  if (!result) {
    return ''
  }
  return formatDateTime(result.finished_at ?? result.started_at)
}

function getBackupScheduleWindowText(job: BackupJob): string {
  if (!job.schedule_window_start || !job.schedule_window_end) {
    return ''
  }
  return `自动窗口：${job.schedule_window_start}-${job.schedule_window_end}`
}

const backupStarterConfigSnippet = `[[backup.jobs]]
id = "external-disk"
name = "外置硬盘备份"
type = "local"
destination = "/mnt/backup-drive/mnemonas"
schedule_interval = "24h"
max_snapshots = 7
verify_after_backup = true`

const batchRestoreItemLimit = 20

function canRunBackupRestoreDrill(job: BackupJob): boolean {
  if (job.type === 'restic' || job.type === 'rclone') {
    return true
  }
  return job.type === 'local' && hasCompletedLocalBackupSnapshot(job)
}

function canRunBackupRestore(job: BackupJob): boolean {
  if (job.type === 'restic' || job.type === 'rclone') {
    return true
  }
  return job.type === 'local' && hasCompletedLocalBackupSnapshot(job)
}

function canVerifyLatestBackupRestore(job: BackupJob): boolean {
  return job.last_restore?.status === 'completed' && Boolean(job.last_restore.target_path)
}

function BackupAttentionSummary({ job }: { job: BackupJob }) {
  const reasons = getBackupAttentionReasons(job)
  if (reasons.length === 0) {
    return null
  }

  const nextSteps = getBackupAttentionNextSteps(job)
  const hasFailure = reasons.some((reason) => reason.includes('失败') || reason.includes('异常'))
  const visibleReasons = reasons.slice(0, 2)
  const suffix = reasons.length > visibleReasons.length ? ` 等 ${reasons.length} 项` : ''
  const summary = reasons.join('；')
  const visibleSteps = nextSteps.slice(0, 2)
  const stepSuffix = nextSteps.length > visibleSteps.length ? ` 等 ${nextSteps.length} 步` : ''
  const stepSummary = nextSteps.join('；')

  return (
    <div className="space-y-1">
      <div
        aria-label={`待处理原因：${summary}`}
        className="flex flex-wrap items-center gap-1 text-xs text-warning"
        title={summary}
      >
        <Chip
          size="sm"
          color={hasFailure ? 'danger' : 'warning'}
          variant="flat"
          startContent={<FileWarning size={14} />}
        >
          需处理
        </Chip>
        <span>{visibleReasons.join('、')}{suffix}</span>
      </div>
      {nextSteps.length > 0 && (
        <div
          aria-label={`建议处理：${stepSummary}`}
          className="flex items-center gap-1 text-xs text-default-500"
          title={stepSummary}
        >
          <ListChecks size={14} className="shrink-0 text-warning" />
          <span>建议：{visibleSteps.join('、')}{stepSuffix}</span>
        </div>
      )}
    </div>
  )
}

function hasCompletedLocalBackupSnapshot(job: BackupJob): boolean {
  return job.last_run?.status === 'completed' || job.last_successful_run?.status === 'completed'
}

function getBackupRunMetricText(result: BackupRunResult): string {
  if (result.status === 'running') {
    return '备份任务运行中'
  }
  if (result.status === 'failed') {
    return '备份任务失败'
  }
  if (result.file_count === 0 && result.total_bytes === 0 && !result.snapshot_path) {
    return '外部备份命令已完成'
  }
  return `${result.file_count} 个文件 · ${formatBytes(result.total_bytes)}`
}

function getBackupRestoreDrillMetricText(result: BackupRestoreDrillResult): string {
  if (result.status === 'running') {
    return '恢复演练运行中'
  }
  if (result.status === 'failed') {
    return '恢复演练失败'
  }
  if (result.file_count === 0 && result.verified_bytes === 0 && !result.restored_path) {
    return '校验命令已完成'
  }
  return `校验 ${result.file_count} 个文件 · ${formatBytes(result.verified_bytes)}`
}

function getBackupRestoreMetricText(result: BackupRestoreResult): string {
  if (result.status === 'running') {
    return '恢复任务运行中'
  }
  if (result.status === 'failed') {
    return '恢复任务失败'
  }
  if (result.file_count === 0 && result.verified_bytes === 0 && !result.snapshot_path) {
    return '恢复命令已完成'
  }
  return `${result.file_count} 个文件 · ${formatBytes(result.verified_bytes)}`
}

function getBackupRestorePreviewMetricText(result: BackupRestorePreviewResult): string {
  if (result.status === 'running') {
    return '恢复预览生成中'
  }
  if (result.status === 'failed') {
    return '恢复预览失败'
  }
  if (result.file_count === 0 && result.total_bytes === 0 && !result.snapshot_path) {
    return '可恢复内容已确认'
  }
  return `预计 ${result.file_count} 个文件 · ${formatBytes(result.total_bytes)}`
}

function getBackupRestoreVerifyMetricText(result: BackupRestoreVerifyResult): string {
  if (result.status === 'running') {
    return '恢复目录检查中'
  }
  if (result.status === 'failed') {
    return '恢复目录检查失败'
  }
  if (result.file_count === 0 && result.verified_bytes === 0) {
    return '目标目录已检查'
  }
  return `检查 ${result.file_count} 个文件 · ${formatBytes(result.verified_bytes)}`
}

function getBackupSnapshotReferenceText(snapshotPath?: string): string {
  if (!snapshotPath) {
    return ''
  }
  const normalized = snapshotPath.replace(/\\/g, '/')
  const name = normalized.split('/').filter(Boolean).pop()
  return name ? `对照快照 ${name}` : '已记录对照快照'
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

function getRestoreTargetSlug(job: BackupJob): string {
  const slug = job.id
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^[.-]+|[.-]+$/g, '')
    .replace(/-+/g, '-')
  return slug || 'backup'
}

function getSuggestedRestoreTargetPath(job: BackupJob): string {
  return `/mnt/restore/${getRestoreTargetSlug(job)}`
}

function RestoreCheckRow({
  tone,
  title,
  description,
}: {
  tone: 'success' | 'warning' | 'danger' | 'default'
  title: string
  description: string
}) {
  const iconClass = tone === 'success' ? 'text-success' : tone === 'warning' ? 'text-warning' : tone === 'danger' ? 'text-danger' : 'text-default-400'
  const borderClass = tone === 'success' ? 'border-success/20 bg-success/5' : tone === 'warning' ? 'border-warning/20 bg-warning/10' : tone === 'danger' ? 'border-danger/20 bg-danger/10' : 'border-divider bg-content2/60'
  const Icon = tone === 'success' ? CheckCircle : tone === 'warning' || tone === 'danger' ? AlertCircle : Clock

  return (
    <div className={`flex items-start gap-3 rounded-lg border p-3 text-sm ${borderClass}`}>
      <Icon size={16} className={`mt-0.5 shrink-0 ${iconClass}`} />
      <div className="min-w-0">
        <div className="font-medium text-default-800">{title}</div>
        <div className="mt-1 text-default-500">{description}</div>
      </div>
    </div>
  )
}

type RestoreFlowStepState = 'complete' | 'active' | 'pending' | 'blocked' | 'warning'

type RestoreFlowStep = {
  id: string
  title: string
  description: string
  state: RestoreFlowStepState
}

function getRestoreFlowStepLabel(state: RestoreFlowStepState): string {
  switch (state) {
    case 'complete':
      return '已完成'
    case 'active':
      return '当前'
    case 'blocked':
      return '需处理'
    case 'warning':
      return '需复核'
    default:
      return '待处理'
  }
}

function getRestoreFlowStepColor(state: RestoreFlowStepState): 'success' | 'warning' | 'danger' | 'default' {
  switch (state) {
    case 'complete':
      return 'success'
    case 'active':
    case 'warning':
      return 'warning'
    case 'blocked':
      return 'danger'
    default:
      return 'default'
  }
}

function getRestoreFlowStepClass(state: RestoreFlowStepState): string {
  switch (state) {
    case 'complete':
      return 'border-success/20 bg-success/5'
    case 'active':
    case 'warning':
      return 'border-warning/20 bg-warning/10'
    case 'blocked':
      return 'border-danger/20 bg-danger/10'
    default:
      return 'border-divider bg-content1'
  }
}

function RestoreFlowStepIcon({ state }: { state: RestoreFlowStepState }) {
  if (state === 'complete') {
    return <CheckCircle size={16} className="text-success" />
  }
  if (state === 'blocked') {
    return <XCircle size={16} className="text-danger" />
  }
  if (state === 'warning') {
    return <FileWarning size={16} className="text-warning" />
  }
  if (state === 'active') {
    return <Clock size={16} className="text-warning" />
  }
  return <Clock size={16} className="text-default-400" />
}

function RestoreFlowStepsPanel({
  ariaLabel,
  title,
  steps,
}: {
  ariaLabel: string
  title: string
  steps: RestoreFlowStep[]
}) {
  return (
    <div aria-label={ariaLabel} className="rounded-lg border border-divider bg-content2/50 p-3 text-sm">
      <div className="flex items-center gap-2 font-medium text-default-800">
        <ListChecks size={16} />
        <span>{title}</span>
      </div>
      <ol className="mt-3 grid gap-2 sm:grid-cols-2">
        {steps.map((step, index) => (
          <li key={step.id} className={`min-w-0 rounded-lg border p-3 ${getRestoreFlowStepClass(step.state)}`}>
            <div className="flex items-start gap-2">
              <RestoreFlowStepIcon state={step.state} />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-xs text-default-500">阶段 {index + 1}</span>
                  <Chip size="sm" color={getRestoreFlowStepColor(step.state)} variant="flat">
                    {getRestoreFlowStepLabel(step.state)}
                  </Chip>
                </div>
                <div className="mt-1 font-medium text-default-800">{step.title}</div>
                <div className="mt-1 break-words text-xs text-default-500">{step.description}</div>
              </div>
            </div>
          </li>
        ))}
      </ol>
    </div>
  )
}

function RestoreFlowGuide({
  targetPath,
  targetReady,
  targetError,
  preview,
  previewMatches,
  isPreviewing,
  isRestoring,
  result,
  verifyResult,
  isVerifying,
}: {
  targetPath: string
  targetReady: boolean
  targetError: string | null
  preview: BackupRestorePreviewResult | null
  previewMatches: boolean
  isPreviewing: boolean
  isRestoring: boolean
  result: BackupRestoreResult | null
  verifyResult: BackupRestoreVerifyResult | null
  isVerifying: boolean
}) {
  const previewFailed = Boolean(preview && previewMatches && (preview.status === 'failed' || hasFailedRestorePreflight(preview)))
  const previewComplete = Boolean(result) || Boolean(preview && previewMatches && !previewFailed)
  const restoreFailed = result?.status === 'failed'
  const restoreComplete = Boolean(result && !restoreFailed)
  const verifyWarnings = verifyResult?.warnings ?? []
  const verifyFailed = verifyResult?.status === 'failed'

  const steps: RestoreFlowStep[] = [{
    id: 'target',
    title: '目标目录',
    description: targetError
      ? '目标目录格式需要处理'
      : targetReady
        ? `目标已填写：${targetPath.trim()}`
        : '填写独立恢复目录',
    state: targetError ? 'blocked' : targetReady ? 'complete' : 'active',
  }, {
    id: 'preview',
    title: '恢复预览',
    description: result
      ? '已完成预览和预检'
      : isPreviewing
        ? '正在生成预览和预检'
        : previewFailed
          ? '预检未通过，处理失败项后重新生成预览'
          : preview && !previewMatches
            ? '目标目录或配置选项已变更，需要重新生成预览'
            : previewComplete
              ? '预览已确认，可复核执行'
              : targetReady
                ? '生成预览以确认文件、配置和预检'
                : '目标目录确认后生成预览',
    state: result || previewComplete
      ? 'complete'
      : previewFailed
        ? 'blocked'
        : targetReady || isPreviewing || Boolean(preview)
          ? 'active'
          : 'pending',
  }, {
    id: 'restore',
    title: '执行恢复',
    description: restoreFailed
      ? '恢复失败，查看错误后重新执行'
      : restoreComplete
        ? '恢复已写入独立目录'
        : isRestoring
          ? '正在写入恢复目录'
          : previewComplete
            ? '预览通过，可开始恢复'
            : '预览通过后执行恢复',
    state: restoreFailed
      ? 'blocked'
      : restoreComplete
        ? 'complete'
        : isRestoring || previewComplete
          ? 'active'
          : 'pending',
  }, {
    id: 'verify',
    title: '只读校验与切换',
    description: !result
      ? '恢复完成后自动检查'
      : restoreFailed
        ? '恢复失败时不应切换'
        : isVerifying
          ? '正在执行只读校验'
          : !verifyResult
            ? '等待只读校验结果'
            : verifyFailed
              ? '只读校验失败，切换前处理'
              : verifyWarnings.length > 0 || !verifyResult.looks_like_storage_root
                ? '校验完成但仍需复核结构和警告'
                : '只读校验完成，可按清单人工切换',
    state: !result
      ? 'pending'
      : restoreFailed || verifyFailed
        ? 'blocked'
        : isVerifying || !verifyResult
          ? 'active'
          : verifyWarnings.length > 0 || !verifyResult.looks_like_storage_root
            ? 'warning'
            : 'complete',
  }]

  return <RestoreFlowStepsPanel ariaLabel="恢复流程进度" title="恢复流程" steps={steps} />
}

function getRestorePreflightTone(status: BackupRestorePreflightCheck['status']): 'success' | 'warning' | 'danger' | 'default' {
  if (status === 'passed') {
    return 'success'
  }
  if (status === 'failed') {
    return 'danger'
  }
  if (status === 'warning') {
    return 'warning'
  }
  return 'default'
}

function hasFailedRestorePreflight(result: BackupRestorePreviewResult | null): boolean {
  return Boolean(result?.preflight_checks?.some((check) => check.status === 'failed'))
}

function hasWarningRestorePreflight(result: BackupRestorePreviewResult | null): boolean {
  return Boolean(
    (result?.warnings?.length ?? 0) > 0
    || result?.preflight_checks?.some((check) => check.status === 'warning'),
  )
}

function getRestorePreviewPanelClass(result: BackupRestorePreviewResult, matches: boolean): string {
  if (!matches) {
    return 'rounded-lg border border-default-200 bg-content2/70 p-4 text-sm'
  }
  if (hasFailedRestorePreflight(result)) {
    return 'rounded-lg border border-danger/20 bg-danger/10 p-4 text-sm'
  }
  if (hasWarningRestorePreflight(result)) {
    return 'rounded-lg border border-warning/20 bg-warning/10 p-4 text-sm'
  }
  return 'rounded-lg border border-success/20 bg-success/10 p-4 text-sm'
}

function getRestorePreviewTitle(result: BackupRestorePreviewResult, matches: boolean): string {
  if (!matches) {
    return '预览已失效'
  }
  if (hasFailedRestorePreflight(result)) {
    return '预览未通过'
  }
  if (hasWarningRestorePreflight(result)) {
    return '预览已确认，有提醒'
  }
  return '预览已确认'
}

function RestorePreflightList({ checks }: { checks?: BackupRestorePreflightCheck[] }) {
  if (!checks || checks.length === 0) {
    return null
  }
  return (
    <div className="mt-3 grid gap-2">
      {checks.map((check) => (
        <RestoreCheckRow
          key={check.id}
          tone={getRestorePreflightTone(check.status)}
          title={check.title}
          description={check.detail || check.status}
        />
      ))}
    </div>
  )
}

function getRestorePreflightCounts(checks?: BackupRestorePreflightCheck[]): { passed: number; warning: number; failed: number } {
  return (checks ?? []).reduce((counts, check) => {
    if (check.status === 'passed') {
      counts.passed += 1
    } else if (check.status === 'warning') {
      counts.warning += 1
    } else if (check.status === 'failed') {
      counts.failed += 1
    }
    return counts
  }, { passed: 0, warning: 0, failed: 0 })
}

function getRestorePreflightDetail(checks: BackupRestorePreflightCheck[] | undefined, id: string): string | null {
  const check = checks?.find((candidate) => candidate.id === id)
  return check?.detail || check?.title || null
}

type RestoreImpactTone = 'success' | 'warning' | 'danger' | 'default'

function getRestoreImpactItemClass(tone: RestoreImpactTone): string {
  switch (tone) {
  case 'success':
    return 'border-success/20 bg-success/5'
  case 'warning':
    return 'border-warning/20 bg-warning/10'
  case 'danger':
    return 'border-danger/20 bg-danger/10'
  default:
    return 'border-divider bg-content2/60'
  }
}

function RestoreImpactItem({
  label,
  value,
  tone = 'default',
}: {
  label: string
  value: string
  tone?: RestoreImpactTone
}) {
  return (
    <div className={`min-w-0 rounded-lg border p-3 ${getRestoreImpactItemClass(tone)}`}>
      <div className="text-xs font-medium text-default-500">{label}</div>
      <div className="mt-1 break-words text-sm text-default-700">{value}</div>
    </div>
  )
}

function RestoreImpactSummary({
  result,
  matches,
}: {
  result: BackupRestorePreviewResult
  matches: boolean
}) {
  const counts = getRestorePreflightCounts(result.preflight_checks)
  const hasWarnings = (result.warnings?.length ?? 0) > 0 || counts.warning > 0
  const targetState = getRestorePreflightDetail(result.preflight_checks, 'target_state')
    ?? '恢复执行前会再次确认目标目录不存在或为空。'
  const conflictText = !matches
    ? '目标目录或配置选项已变更，当前预览不能作为执行依据。'
    : counts.failed > 0 || result.status === 'failed'
      ? '存在失败预检，处理失败项并重新生成预览前不会执行恢复。'
      : hasWarnings
        ? '存在预检提醒，恢复前需要人工确认目标目录、容量或配置影响。'
        : '未发现会覆盖当前 storage.root 的冲突；恢复写入独立目标目录。'
  const permissionText = result.config_included
    ? '配置文件会单独恢复到 .mnemonas-restore/config.toml；切换前需人工比对用户、目录权限、分享、告警和公开访问设置。'
    : '本次不恢复配置文件，当前运行中的用户、目录权限、分享、告警和公开访问设置不会自动改变。'
  const sampleText = result.sample_paths && result.sample_paths.length > 0
    ? `样例：${result.sample_paths.slice(0, 3).join('、')}`
    : '预览未返回样例路径。'

  return (
    <div aria-label="恢复影响摘要" className="mt-3 rounded-lg border border-divider bg-content1 p-3">
      <div className="flex items-center gap-2 text-sm font-medium text-default-800">
        <ListChecks size={16} />
        <span>恢复影响摘要</span>
      </div>
      <div className="mt-3 grid gap-2 sm:grid-cols-2">
        <RestoreImpactItem label="目标状态" value={targetState} tone={matches && counts.failed === 0 ? 'success' : 'danger'} />
        <RestoreImpactItem label="冲突与覆盖" value={conflictText} tone={!matches || counts.failed > 0 ? 'danger' : hasWarnings ? 'warning' : 'success'} />
        <RestoreImpactItem label="权限影响" value={permissionText} tone={result.config_included ? 'warning' : 'success'} />
        <RestoreImpactItem label="恢复范围" value={`${getBackupRestorePreviewMetricText(result)}；${sampleText}`} tone="default" />
        <RestoreImpactItem
          label="预检结果"
          value={`${counts.passed} 项通过 · ${counts.warning} 项提醒 · ${counts.failed} 项失败`}
          tone={counts.failed > 0 ? 'danger' : counts.warning > 0 ? 'warning' : 'success'}
        />
        <RestoreImpactItem
          label="恢复后校验"
          value="恢复完成后自动执行只读校验；切换 storage.root 前需保留旧目录和旧配置作为回滚点。"
          tone="default"
        />
      </div>
    </div>
  )
}

function RestoreExecutionReview({
  result,
  matches,
}: {
  result: BackupRestorePreviewResult
  matches: boolean
}) {
  const checks = result.preflight_checks ?? []
  const { passed: passedCount, warning: warningCount, failed: failedCount } = getRestorePreflightCounts(checks)
  const toneClass = !matches || failedCount > 0
    ? 'border-danger/20 bg-danger/10'
    : warningCount > 0 || (result.warnings?.length ?? 0) > 0
      ? 'border-warning/20 bg-warning/10'
      : 'border-success/20 bg-success/10'

  const reviewItems = [
    { label: '目标目录', value: result.target_path },
    { label: '写入边界', value: '恢复只写入独立目录，不覆盖当前 storage.root。' },
    { label: '恢复内容', value: getBackupRestorePreviewMetricText(result) },
    {
      label: '配置文件',
      value: result.config_available
        ? (result.config_included ? '将恢复到 .mnemonas-restore/config.toml' : '本次不恢复配置文件')
        : '备份未提供可恢复配置文件',
    },
    {
      label: '预检结果',
      value: `${passedCount} 项通过 · ${warningCount} 项提醒 · ${failedCount} 项失败`,
    },
    { label: '恢复后检查', value: '恢复完成后自动执行只读校验，并显示切换步骤和回滚清单。' },
    { label: '切换前确认', value: '只在只读校验通过后切换；保留原 storage.root、原配置文件和回滚清单。' },
  ]

  const handleCopyReviewReport = async () => {
    try {
      await copyTextToClipboard(formatRestoreExecutionReviewReport(result, matches))
      addToast({ title: '恢复执行前复核记录已复制', color: 'success' })
    } catch {
      addToast({
        title: '复制恢复执行前复核记录失败',
        description: clipboardWriteFailureDescription,
        color: 'danger',
      })
    }
  }

  return (
    <div aria-label="恢复执行前复核" className={`mt-3 rounded-lg border p-3 text-sm ${toneClass}`}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="font-medium text-default-800">恢复执行前复核</div>
        <Button size="sm" variant="flat" className="rounded-lg" startContent={<Copy size={14} />} onPress={handleCopyReviewReport}>
          复制复核记录
        </Button>
      </div>
      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        {reviewItems.map((item) => (
          <div key={item.label} className="min-w-0">
            <div className="text-xs text-default-500">{item.label}</div>
            <div className="mt-1 break-words text-default-700">{item.value}</div>
          </div>
        ))}
      </div>
      {!matches && (
        <div className="mt-3 text-xs text-danger">目标目录或配置选项已变更，当前复核不可用于执行恢复。</div>
      )}
    </div>
  )
}

function formatRestoreExecutionReviewReport(
  result: BackupRestorePreviewResult,
  matches: boolean,
): string {
  const { passed: passedCount, warning: warningCount, failed: failedCount } = getRestorePreflightCounts(result.preflight_checks)
  const sections = [
    '恢复执行前复核',
    `任务 ID：${result.job_id}`,
    `恢复目标：${result.target_path}`,
    `恢复内容：${getBackupRestorePreviewMetricText(result)}`,
    `配置文件：${result.config_available ? (result.config_included ? '将恢复到 .mnemonas-restore/config.toml' : '本次不恢复配置文件') : '备份未提供可恢复配置文件'}`,
    `预检结果：${passedCount} 项通过 · ${warningCount} 项提醒 · ${failedCount} 项失败`,
    `预览状态：${matches ? '当前目标目录和配置选项与预览一致' : '目标目录或配置选项已变更，当前复核不可用于执行恢复'}`,
    '写入边界：恢复只写入独立目录，不覆盖当前 storage.root。',
    '恢复后检查：恢复完成后自动执行只读校验，并显示切换步骤和回滚清单。',
    '切换前确认：只在只读校验通过后切换；保留原 storage.root、原配置文件和回滚清单。',
  ]

  if (result.sample_paths && result.sample_paths.length > 0) {
    sections.push(`路径样例：${result.sample_paths.slice(0, 5).join('；')}`)
  }
  if (result.warnings && result.warnings.length > 0) {
    sections.push(`恢复提醒：${result.warnings.map(getBackupDiagnosticDisplayMessage).join('；')}`)
  }

  return sections.join('\n')
}

function RestoreChecklistBlock({
  title,
  items,
}: {
  title: string
  items?: string[]
}) {
  const [checkedItems, setCheckedItems] = useState<Set<string>>(new Set())

  if (!items || items.length === 0) {
    return null
  }

  const toggleItem = (itemKey: string, selected: boolean) => {
    setCheckedItems((current) => {
      const next = new Set(current)
      if (selected) {
        next.add(itemKey)
      } else {
        next.delete(itemKey)
      }
      return next
    })
  }

  return (
    <div aria-label={`${title}确认进度`} className="rounded-lg border border-divider bg-content2/60 p-3 text-sm">
      <div className="flex items-center justify-between gap-3">
        <div className="font-medium text-default-800">{title}</div>
        <Chip size="sm" variant="flat" color={checkedItems.size === items.length ? 'success' : 'default'}>
          已确认 {checkedItems.size} / {items.length} 项
        </Chip>
      </div>
      <div className="mt-2 space-y-2">
        {items.map((item, index) => {
          const itemKey = `${index}:${item}`
          return (
            <Checkbox
              key={itemKey}
              size="sm"
              isSelected={checkedItems.has(itemKey)}
              onValueChange={(selected) => toggleItem(itemKey, selected)}
            >
              <span className="text-default-600">{item}</span>
            </Checkbox>
          )
        })}
      </div>
    </div>
  )
}

function getRestoreVerificationReportText(
  verifyResult: BackupRestoreVerifyResult | null,
  isVerifying: boolean,
): string {
  if (isVerifying) {
    return '检查中'
  }
  if (!verifyResult) {
    return '尚未完成恢复目录检查'
  }
  const warnings = verifyResult.warnings ?? []
  const statusText = verifyResult.status === 'failed'
    ? '检查失败'
    : warnings.length > 0
      ? '检查完成，有警告'
      : '检查完成'
  return `${statusText}；${getBackupRestoreVerifyMetricText(verifyResult)}`
}

function formatRestoreChecklistItems(items: string[] | undefined): string[] {
  if (!items || items.length === 0) {
    return ['无']
  }
  return items.map((item, index) => `${index + 1}. ${item}`)
}

function formatRestoreCutoverReport(
  result: BackupRestoreResult,
  verifyResult: BackupRestoreVerifyResult | null,
  isVerifying: boolean,
): string {
  const restoreWarnings = result.warnings ?? []
  const verifyWarnings = verifyResult?.warnings ?? []
  const sections = [
    '恢复切换记录',
    `任务 ID：${result.job_id}`,
    `恢复状态：${getBackupTaskStatusLabel(result.status)}`,
    `恢复目标：${result.target_path}`,
    `恢复内容：${getBackupRestoreMetricText(result)}`,
    `配置文件：${result.config_restored ? (result.config_path ?? '已恢复配置文件') : '未恢复配置文件'}`,
    `只读校验：${getRestoreVerificationReportText(verifyResult, isVerifying)}`,
    `存储结构：${verifyResult?.looks_like_storage_root ? '可作为完整 storage.root 候选目录' : '未确认完整 storage.root 结构'}`,
  ]

  if (result.error_message) {
    sections.push(`恢复错误：${getBackupDiagnosticDisplayMessage(result.error_message)}`)
  }
  if (restoreWarnings.length > 0) {
    sections.push(`恢复警告：${restoreWarnings.map(getBackupDiagnosticDisplayMessage).join('；')}`)
  }
  if (verifyWarnings.length > 0) {
    sections.push(`校验警告：${verifyWarnings.map(getBackupDiagnosticDisplayMessage).join('；')}`)
  }

  return [
    ...sections,
    '',
    '切换步骤',
    ...formatRestoreChecklistItems(result.cutover_checklist),
    '',
    '回滚清单',
    ...formatRestoreChecklistItems(result.rollback_checklist),
  ].join('\n')
}

function getBatchRestoreItemConfigText(item: BackupBatchRestoreItemResult): string {
  if (item.restore?.config_restored) {
    return item.restore.config_path ?? '已恢复配置文件'
  }
  if (item.restore) {
    return '未恢复配置文件'
  }
  if (item.include_config) {
    return '已请求恢复配置文件，尚未确认写入'
  }
  return '未请求恢复配置文件'
}

function getBatchRestoreItemVerifyText(item: BackupBatchRestoreItemResult): string {
  if (!item.verify) {
    return item.status === 'failed' ? '未执行只读校验' : '尚未返回只读校验结果'
  }
  const warnings = item.verify.warnings ?? []
  const statusText = item.verify.status === 'failed'
    ? '检查失败'
    : warnings.length > 0
      ? '检查完成，有警告'
      : '检查完成'
  const storageText = item.verify.looks_like_storage_root
    ? '可作为完整 storage.root 候选目录'
    : '未确认完整 storage.root 结构'
  return `${statusText}；${getBackupRestoreVerifyMetricText(item.verify)}；${storageText}`
}

function getBatchRestoreItemWarningText(item: BackupBatchRestoreItemResult): string | null {
  const warnings = [
    ...(item.warnings ?? []),
    ...(item.restore?.warnings ?? []),
    ...(item.verify?.warnings ?? []),
  ].map(getBackupDiagnosticDisplayMessage)
  return warnings.length > 0 ? warnings.join('；') : null
}

function getBatchRestoreCutoverCandidates(result: BackupBatchRestoreResult): BackupBatchRestoreItemResult[] {
  return result.items.filter((item) => item.status === 'completed' && item.restore?.status === 'completed')
}

function getBatchRestoreCutoverCandidateReviewText(item: BackupBatchRestoreItemResult): string {
  if (!item.verify) {
    return '缺少只读校验结果，不能直接切换。'
  }
  if (item.verify.status === 'failed') {
    return '只读校验失败，处理后重新校验再切换。'
  }
  if (!item.verify.looks_like_storage_root) {
    return '未确认完整 storage.root 结构，切换前需人工复核目录内容。'
  }
  if ((item.verify.warnings?.length ?? 0) > 0) {
    return '只读校验有警告，切换前需复核警告并保留原目录。'
  }
  return '只读校验通过，可作为 storage.root 候选目录；切换前保留原目录和原配置。'
}

function getBatchRestoreItemDispositionText(item: BackupBatchRestoreItemResult): string {
  const normalizedError = item.error_message ? normalizeDiagnosticMessageKey(item.error_message) : ''
  if (item.status === 'failed') {
    if (normalizedError.includes('restore target already exists') || normalizedError.includes('restore target conflicts with batch item')) {
      return '目标冲突未写入：保留现有目标目录，改为新的独立目录后重新生成批量预览。'
    }
    if (normalizedError.includes('batch restore preflight failed')) {
      return '预检拦截未写入：处理失败预检项后重新生成批量预览。'
    }
    return '恢复失败：确认目标目录状态和错误原因后重新预览，未完成项不能进入切换。'
  }
  if (!item.restore || item.restore.status !== 'completed') {
    return '恢复未完成：等待结果或重新执行后再判断冲突处置。'
  }
  if (!item.verify) {
    return '等待只读校验：缺少校验结果前不能切换或覆盖原目录。'
  }
  if (item.verify.status === 'failed') {
    return '阻止切换：只读校验失败，处理失败原因后重新校验。'
  }
  if (!item.verify.looks_like_storage_root) {
    return '人工复核：未确认完整 storage.root 结构，按子目录迁移或重新恢复处理。'
  }
  if ((item.verify.warnings?.length ?? 0) > 0 || (item.warnings?.length ?? 0) > 0 || (item.restore.warnings?.length ?? 0) > 0) {
    return '暂缓切换：恢复或只读校验存在警告，复核警告后再决定是否切换。'
  }
  return '可进入切换复核：只读校验通过，未发现覆盖当前 storage.root 的冲突。'
}

function getBatchRestoreItemDispositionTone(item: BackupBatchRestoreItemResult): RestoreImpactTone {
  const disposition = getBatchRestoreItemDispositionText(item)
  if (disposition.startsWith('可进入切换复核')) {
    return 'success'
  }
  if (disposition.startsWith('恢复未完成') || disposition.startsWith('等待只读校验')) {
    return 'default'
  }
  if (disposition.startsWith('阻止切换') || disposition.startsWith('目标冲突') || disposition.startsWith('预检拦截') || disposition.startsWith('恢复失败')) {
    return 'danger'
  }
  return 'warning'
}

function formatBatchRestoreResultReport(result: BackupBatchRestoreResult, jobs: BackupJob[] = []): string {
  const jobsById = getBatchRestoreJobLookup(jobs)
  const sections = [
    '批量恢复记录',
    `批次 ID：${result.id}`,
    `批次状态：${getBackupTaskStatusLabel(result.status)}${result.warning ? '（有警告）' : ''}`,
    `恢复内容：${getBatchRestoreMetricText(result)}`,
    `恢复项目：${result.items.length} 项`,
  ]

  if (result.error_message) {
    sections.push(`批次错误：${getBackupDiagnosticDisplayMessage(result.error_message)}`)
  }
  if (result.warnings && result.warnings.length > 0) {
    sections.push(`批次警告：${result.warnings.map(getBackupDiagnosticDisplayMessage).join('；')}`)
  }

  const itemSections = result.items.flatMap((item, index) => {
    const job = jobsById.get(item.job_id)
    const lines = [
      `${index + 1}. ${getBatchRestoreItemJobLabel(item, job)}`,
      ...getBatchRestoreJobContextReportLines(job).map((line) => `   ${line}`),
      `   状态：${getBackupTaskStatusLabel(item.status)}`,
      `   目标目录：${item.target_path}`,
      `   恢复内容：${item.restore ? getBackupRestoreMetricText(item.restore) : '未完成恢复写入'}`,
      `   配置文件：${getBatchRestoreItemConfigText(item)}`,
      `   只读校验：${getBatchRestoreItemVerifyText(item)}`,
      `   处置建议：${getBatchRestoreItemDispositionText(item)}`,
    ]
    const snapshotPath = item.verify?.snapshot_path ?? item.restore?.snapshot_path
    if (snapshotPath) {
      lines.push(`   快照：${getBackupSnapshotReferenceText(snapshotPath)}`)
    }
    const warningText = getBatchRestoreItemWarningText(item)
    if (warningText) {
      lines.push(`   警告：${warningText}`)
    }
    if (item.verify?.error_message) {
      lines.push(`   校验错误：${getBackupDiagnosticDisplayMessage(item.verify.error_message)}`)
    }
    if (item.error_message) {
      lines.push(`   错误：${getBackupDiagnosticDisplayMessage(item.error_message)}`)
    }
    return lines
  })

  const cutoverCandidateSections = getBatchRestoreCutoverCandidates(result).flatMap((item, index) => [
    `${index + 1}. ${getBatchRestoreItemJobLabel(item, jobsById.get(item.job_id))}`,
    `   候选目录：${item.target_path}`,
    `   切换复核：${getBatchRestoreCutoverCandidateReviewText(item)}`,
    `   配置文件：${getBatchRestoreItemConfigText(item)}`,
  ])

  return [
    ...sections,
    '',
    '项目结果',
    ...itemSections,
    '',
    '冲突处置记录',
    '覆盖边界：批量恢复只写入各自目标目录，不覆盖当前 storage.root；失败项和未通过校验项不能进入切换。',
    ...result.items.map((item, index) => `${index + 1}. ${getBatchRestoreItemJobLabel(item, jobsById.get(item.job_id))}：${getBatchRestoreItemDispositionText(item)}`),
    ...(cutoverCandidateSections.length > 0 ? ['', '跨目录切换候选', ...cutoverCandidateSections] : []),
  ].join('\n')
}

function RestoreCutoverChecklist({
  result,
  verifyResult,
  isVerifying,
}: {
  result: BackupRestoreResult
  verifyResult: BackupRestoreVerifyResult | null
  isVerifying: boolean
}) {
  const restoreWarnings = result.warnings ?? []
  const restoreFailed = result.status === 'failed'
  const hasRestoreWarnings = restoreWarnings.length > 0
  const verifyWarnings = verifyResult?.warnings ?? []
  const verifyTone = !verifyResult || isVerifying ? 'default' : verifyResult.status === 'failed' ? 'danger' : verifyWarnings.length > 0 ? 'warning' : 'success'
  const storageTone = !verifyResult || isVerifying ? 'default' : verifyResult.status === 'failed' ? 'danger' : verifyResult.looks_like_storage_root ? 'success' : 'warning'
  const configTone = restoreFailed ? 'default' : result.config_restored ? (verifyResult?.config_found ? 'success' : 'warning') : 'default'
  const completionToneClass = restoreFailed
    ? 'border-danger/20 bg-danger/10'
    : hasRestoreWarnings
    ? 'border-warning/20 bg-warning/10'
    : 'border-success/20 bg-success/10'
  const completionTitleClass = restoreFailed ? 'text-danger' : hasRestoreWarnings ? 'text-warning' : 'text-success'
  const handleCopyRestoreCutoverReport = async () => {
    try {
      await copyTextToClipboard(formatRestoreCutoverReport(result, verifyResult, isVerifying))
      addToast({ title: '恢复切换记录已复制', color: 'success' })
    } catch {
      addToast({
        title: '复制恢复切换记录失败',
        description: clipboardWriteFailureDescription,
        color: 'danger',
      })
    }
  }

  return (
    <div className="space-y-4">
      <div className={`rounded-lg border p-4 text-sm ${completionToneClass}`}>
        <div className="flex items-center justify-between gap-3">
          <div className={`font-medium ${completionTitleClass}`}>
            {restoreFailed ? '恢复失败' : hasRestoreWarnings ? '恢复已完成，有警告' : '恢复已完成'}
          </div>
          <BackupStatusChip status={result.status} warning={hasRestoreWarnings} />
        </div>
        {!restoreFailed && (
          <div className="mt-3">
            <Button
              size="sm"
              variant="flat"
              className="rounded-lg"
              startContent={<Copy size={14} />}
              onPress={handleCopyRestoreCutoverReport}
            >
              复制切换记录
            </Button>
          </div>
        )}
        <div className="mt-2 text-default-600">{getBackupRestoreMetricText(result)}</div>
        <div className="mt-1 truncate font-mono text-xs text-default-500" title={result.target_path}>
          {result.target_path}
        </div>
      </div>

      <RestorePreflightList checks={result.preflight_checks} />

      {hasRestoreWarnings && (
        <div className="rounded-lg border border-warning/20 bg-warning/10 p-3 text-sm text-warning">
          <div className="font-medium">恢复警告</div>
          <div className="mt-2 space-y-1">
            {restoreWarnings.map((warning) => (
              <div key={warning}>{getBackupDiagnosticDisplayMessage(warning)}</div>
            ))}
          </div>
        </div>
      )}

      <div className="grid gap-3">
        <RestoreCheckRow
          tone={restoreFailed ? 'danger' : 'success'}
          title="恢复目录"
          description={restoreFailed ? '恢复未完成；请查看错误信息并确认目标目录状态。' : '数据已写入独立目录，当前数据目录未被覆盖。'}
        />
        <RestoreCheckRow
          tone={verifyTone}
          title="只读校验"
          description={isVerifying ? '正在检查恢复目录。' : verifyResult ? getBackupRestoreVerifyMetricText(verifyResult) : '尚未完成恢复目录检查。'}
        />
        {verifyResult?.snapshot_path && (
          <RestoreCheckRow
            tone="success"
            title="对照快照"
            description={getBackupSnapshotReferenceText(verifyResult.snapshot_path)}
          />
        )}
        <RestoreCheckRow
          tone={storageTone}
          title="存储结构"
          description={
            verifyResult?.looks_like_storage_root
              ? '已检测到 files/ 和 .mnemonas/，可作为完整 storage.root 候选目录。'
              : '未确认完整 storage.root 结构；如果本次只恢复子目录，请按子目录迁移处理。'
          }
        />
        <RestoreCheckRow
          tone={configTone}
          title="配置文件"
          description={
            result.config_restored
              ? (verifyResult?.config_found ? `已恢复到 ${verifyResult.config_path}` : '恢复记录包含配置文件，但校验未找到该文件。')
              : '本次恢复未包含配置文件。'
          }
        />
        <RestoreCheckRow
          tone={restoreFailed ? 'danger' : verifyResult && verifyResult.status !== 'failed' && verifyWarnings.length === 0 ? 'success' : 'default'}
          title="切换准备"
          description={restoreFailed ? '恢复失败时不应切换 storage.root；先处理失败原因并重新执行恢复。' : '切换前保留旧目录和旧配置；切换后确认健康检查、登录、文件列表、上传、下载和版本历史。'}
        />
      </div>

      {result.error_message && (
        <div className="rounded-lg border border-danger/20 bg-danger/10 p-3 text-sm text-danger">
          <div className="font-medium">恢复错误</div>
          <div className="mt-2">{getBackupDiagnosticDisplayMessage(result.error_message)}</div>
        </div>
      )}

      {verifyWarnings.length > 0 && (
        <div className="rounded-lg border border-warning/20 bg-warning/10 p-3 text-sm text-warning">
          <div className="font-medium">校验警告</div>
          <div className="mt-2 space-y-1">
            {verifyWarnings.map((warning) => (
              <div key={warning}>{getBackupDiagnosticDisplayMessage(warning)}</div>
            ))}
          </div>
        </div>
      )}

      {!restoreFailed && <RestoreChecklistBlock key={`${result.id}:cutover`} title="切换步骤" items={result.cutover_checklist} />}
      {!restoreFailed && <RestoreChecklistBlock key={`${result.id}:rollback`} title="回滚清单" items={result.rollback_checklist} />}
    </div>
  )
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
        <div className="text-warning">{getBackupDiagnosticDisplayMessage(result.warnings[0])}</div>
      )}
      {result.error_message && <div className="text-danger">{getBackupDiagnosticDisplayMessage(result.error_message)}</div>}
    </div>
  )
}

function getBackupTaskStatusLabel(status: string): string {
  switch (status) {
    case 'completed':
      return '完成'
    case 'running':
      return '运行中'
    case 'failed':
      return '失败'
    default:
      return '未知状态'
  }
}

function getRestoreDrillFailureCategoryLabel(category?: string): string | null {
  const normalized = category?.trim().toLowerCase()
  if (!normalized) {
    return null
  }

  switch (normalized) {
    case 'no_snapshot':
      return '无可用快照'
    case 'unsupported_job_type':
      return '不支持的任务类型'
    case 'unsafe_path':
      return '路径安全检查失败'
    case 'integrity_check':
      return '完整性校验失败'
    case 'external_command':
      return '外部命令失败'
    case 'cancelled':
      return '任务被取消'
    case 'io':
      return '文件系统读写失败'
    case 'unknown':
      return '未分类失败'
    default:
      return '未分类失败'
  }
}

function BackupDrillHistoryList({ history }: { history?: BackupRestoreDrillResult[] }) {
  if (!history || history.length <= 1) {
    return null
  }
  const visibleHistory = history.slice(0, 3)
  const failedCount = history.filter((entry) => entry.status === 'failed').length
  const warningCount = history.filter((entry) => entry.warning || (entry.warnings?.length ?? 0) > 0).length
  return (
    <div className="mt-2 rounded-lg bg-default-50 p-2 text-xs text-default-500">
      <div className="mb-1 font-medium text-default-600">最近演练记录</div>
      <div className="space-y-1">
        {visibleHistory.map((entry) => (
          <div key={entry.id} className="flex items-center justify-between gap-2">
            <span className={entry.status === 'failed' ? 'text-danger' : entry.status === 'running' || entry.warning || (entry.warnings?.length ?? 0) > 0 ? 'text-warning' : 'text-default-500'}>
              {getBackupTaskStatusLabel(entry.status)}
            </span>
            <span className="truncate text-default-400">{formatDateTime(entry.finished_at ?? entry.started_at)}</span>
          </div>
        ))}
      </div>
      {failedCount > 0 && (
        <div className="mt-1 text-warning">近 {history.length} 次包含 {failedCount} 次失败</div>
      )}
      {warningCount > 0 && (
        <div className="mt-1 text-warning">近 {history.length} 次包含 {warningCount} 次警告</div>
      )}
    </div>
  )
}

function getBackupRestoreDrillStatsText(job: BackupJob): string | null {
  const stats = job.restore_drill_stats
  if (!stats || stats.total_runs === 0) {
    return null
  }
  const successRate = Math.round(stats.success_rate * 100)
  const streakText = stats.consecutive_failures
    ? `连续失败 ${stats.consecutive_failures} 次`
    : stats.consecutive_successes
      ? `连续成功 ${stats.consecutive_successes} 次`
      : '暂无连续趋势'
  return `近 ${stats.total_runs} 次成功率 ${successRate}% · ${streakText}`
}

function BackupDrillSummary({ job }: { job: BackupJob }) {
  const result = job.last_restore_drill
  const statsText = getBackupRestoreDrillStatsText(job)
  const latestFailureCategory = getRestoreDrillFailureCategoryLabel(job.restore_drill_stats?.last_failure_category ?? result?.failure_category)
  if (!result) {
    return (
      <div className="space-y-1 text-sm">
        <div className="flex items-center gap-2">
          <BackupPolicyChip status={job.restore_drill_status} staleLabel="演练过期" />
        </div>
        <div className="text-default-500">{getRestoreDrillDisplayMessage(job.restore_drill_message)}</div>
        {job.restore_drill_stale_after && (
          <div className="text-default-400">提醒周期：{formatBackupDuration(job.restore_drill_stale_after)}</div>
        )}
        {job.last_restore_drill_reminder_at && (
          <div className="text-default-400">最近提醒：{formatDateTime(job.last_restore_drill_reminder_at)}</div>
        )}
        {statsText && <div className="text-default-400">{statsText}</div>}
      </div>
    )
  }
  return (
    <div className="space-y-1 text-sm">
      <div className="flex items-center gap-2">
        <BackupStatusChip status={result.status} warning={result.warning || (result.warnings?.length ?? 0) > 0} />
        <BackupPolicyChip status={job.restore_drill_status} staleLabel="演练过期" />
        <span className="text-default-500">{formatDateTime(result.finished_at ?? result.started_at)}</span>
      </div>
      <div className="text-default-500">
        {getBackupRestoreDrillMetricText(result)}
      </div>
      {job.restore_drill_message && (
        <div className={job.restore_drill_status === 'failed' ? 'text-danger' : job.restore_drill_status === 'stale' || job.restore_drill_status === 'due' ? 'text-warning' : 'text-default-400'}>
          {getRestoreDrillDisplayMessage(job.restore_drill_message)}
        </div>
      )}
      {job.last_restore_drill_reminder_at && (
        <div className="text-default-400">最近提醒：{formatDateTime(job.last_restore_drill_reminder_at)}</div>
      )}
      {statsText && (
        <div className={job.restore_drill_stats?.consecutive_failures ? 'text-warning' : 'text-default-400'}>
          {statsText}
        </div>
      )}
      {job.restore_drill_stats?.last_failure_message && (
        <div className="text-warning">最近失败：{getBackupDiagnosticDisplayMessage(job.restore_drill_stats.last_failure_message)}</div>
      )}
      {latestFailureCategory && (
        <div className="text-warning">失败类型：{latestFailureCategory}</div>
      )}
      {result.warnings && result.warnings.length > 0 && (
        <div className="text-warning">{getBackupDiagnosticDisplayMessage(result.warnings[0])}</div>
      )}
      {result.error_message && <div className="text-danger">{getBackupDiagnosticDisplayMessage(result.error_message)}</div>}
      <BackupDrillHistoryList history={job.restore_drill_history} />
    </div>
  )
}

function BackupRestoreReportFindings({ findings }: { findings?: string[] }) {
  const visibleFindings = (findings ?? []).filter((finding) => finding.trim() !== '')
  if (visibleFindings.length === 0) {
    return null
  }
  const displayFindings = visibleFindings.map(getBackupDiagnosticDisplayMessage)
  const hasBlockingFinding = visibleFindings.some((finding) => !finding.startsWith('未发现阻塞项'))
  const suffix = visibleFindings.length > 1 ? ` 等 ${visibleFindings.length} 项` : ''
  const fullSummary = displayFindings.join('\n')
  return (
    <div
      aria-label={`摘要发现：${displayFindings.join('；')}`}
      className={hasBlockingFinding ? 'text-warning' : 'text-default-400'}
      title={fullSummary}
    >
      摘要发现：{displayFindings[0]}{suffix}
    </div>
  )
}

function BackupRestoreHistoryList({ history }: { history?: BackupRestoreResult[] }) {
  if (!history || history.length <= 1) {
    return null
  }

  const visibleHistory = history.slice(0, 3)
  const failedCount = history.filter((entry) => entry.status === 'failed').length

  return (
    <div aria-label="最近恢复记录" className="mt-2 rounded-lg bg-default-50 p-2 text-xs text-default-500">
      <div className="mb-1 font-medium text-default-600">最近恢复记录（{history.length} 条）</div>
      <div className="space-y-2">
        {visibleHistory.map((entry) => (
          <div key={entry.id} className="space-y-1 border-t border-default-100 pt-2 first:border-t-0 first:pt-0">
            <div className="flex items-center justify-between gap-2">
              <span className={entry.status === 'failed' ? 'text-danger' : entry.status === 'running' ? 'text-warning' : 'text-default-500'}>
                {getBackupTaskStatusLabel(entry.status)}
              </span>
              <span className="truncate text-default-400">{formatDateTime(entry.finished_at ?? entry.started_at)}</span>
            </div>
            <div className="truncate text-default-500" title={entry.target_path}>目标：{entry.target_path}</div>
            <div className="text-default-400">{getBackupRestoreMetricText(entry)}</div>
            {entry.error_message && <div className="text-danger">{getBackupDiagnosticDisplayMessage(entry.error_message)}</div>}
            {entry.warnings && entry.warnings.length > 0 && <div className="text-warning">{getBackupDiagnosticDisplayMessage(entry.warnings[0])}</div>}
          </div>
        ))}
      </div>
      {failedCount > 0 && (
        <div className="mt-2 text-warning">近 {history.length} 次包含 {failedCount} 次失败</div>
      )}
    </div>
  )
}

function BackupRestoreVerifyChip({
  needsMatchingVerify,
  verify,
}: {
  needsMatchingVerify: boolean
  verify: BackupRestoreVerifyResult | null
}) {
  if (needsMatchingVerify) {
    return (
      <Chip size="sm" color="warning" variant="flat" startContent={<Clock size={14} />}>
        待校验
      </Chip>
    )
  }
  if (!verify) {
    return null
  }
  if (verify.status === 'running') {
    return (
      <Chip size="sm" color="warning" variant="flat" startContent={<RefreshCw size={14} className="animate-spin" />}>
        检查中
      </Chip>
    )
  }
  if (verify.status === 'failed') {
    return (
      <Chip size="sm" color="danger" variant="flat" startContent={<XCircle size={14} />}>
        检查失败
      </Chip>
    )
  }
  if ((verify.warnings?.length ?? 0) > 0) {
    return (
      <Chip size="sm" color="warning" variant="flat" startContent={<FileWarning size={14} />}>
        检查有警告
      </Chip>
    )
  }
  return (
    <Chip size="sm" color="success" variant="flat" startContent={<CheckCircle size={14} />}>
      已校验
    </Chip>
  )
}

function BackupRestoreSummary({ job }: { job: BackupJob }) {
  const result = job.last_restore
  if (!result) {
    return (
      <div className="space-y-1 text-sm">
        <span className="text-default-400">尚未恢复</span>
        <BackupRestoreReportFindings findings={job.restore_report_findings} />
      </div>
    )
  }
  const verify = job.last_matching_restore_verify ?? null
  const needsMatchingVerify = result.status === 'completed' && !verify
  const restoreWarnings = result.warnings ?? []
  const verifyWarnings = verify?.warnings ?? []

  return (
    <div className="space-y-1 text-sm">
      <div className="flex items-center gap-2">
        <BackupStatusChip status={result.status} warning={restoreWarnings.length > 0} />
        {result.status === 'completed' && (
          <BackupRestoreVerifyChip needsMatchingVerify={needsMatchingVerify} verify={verify} />
        )}
        <span className="text-default-500">{formatDateTime(result.finished_at ?? result.started_at)}</span>
      </div>
      <div className="text-default-500">
        {getBackupRestoreMetricText(result)}
      </div>
      <div className="max-w-[18rem] truncate text-default-400" title={result.target_path}>
        目标：{result.target_path}
      </div>
      <BackupRestoreHistoryList history={job.restore_history} />
      {verify && (
        <div className={verify.status === 'failed' ? 'text-danger' : verify.warnings && verify.warnings.length > 0 ? 'text-warning' : 'text-default-400'}>
          最近检查：{getBackupRestoreVerifyMetricText(verify)}
        </div>
      )}
      {verify?.snapshot_path && (
        <div className="max-w-[18rem] truncate text-default-400" title={verify.snapshot_path}>
          {getBackupSnapshotReferenceText(verify.snapshot_path)}
        </div>
      )}
      {needsMatchingVerify && (
        <div className="text-warning">最近恢复尚未完成匹配的只读校验</div>
      )}
      <BackupRestoreReportFindings findings={job.restore_report_findings} />
      {restoreWarnings.length > 0 && <div className="text-warning">{getBackupDiagnosticDisplayMessage(restoreWarnings[0])}</div>}
      {verifyWarnings.length > 0 && <div className="text-warning">{getBackupDiagnosticDisplayMessage(verifyWarnings[0])}</div>}
      {result.error_message && <div className="text-danger">{getBackupDiagnosticDisplayMessage(result.error_message)}</div>}
    </div>
  )
}

function getBackupConflictTitle(error: unknown, fallback: string): string {
  if (!(error instanceof ApiError) || error.status !== 409) {
    return fallback
  }
  const message = normalizeDiagnosticMessageKey(error.message)
  if (message.includes('disabled')) {
    return '备份任务已停用'
  }
  if (message.includes('no completed snapshots')) {
    return '暂无可演练的备份快照'
  }
  if (message.includes('already running')) {
    return '备份任务正在运行'
  }
  return fallback
}

function getBackupConflictDescription(error: unknown, fallback: string): string {
  if (!(error instanceof ApiError) || error.status !== 409) {
    return fallback
  }
  const message = normalizeDiagnosticMessageKey(error.message)
  if (message.includes('disabled')) {
    return '请先在配置文件中启用该任务并重启服务。'
  }
  if (message.includes('no completed snapshots')) {
    return '请先完成一次成功备份，再执行恢复或演练。'
  }
  if (message.includes('already running')) {
    return '已有备份或恢复演练正在执行，请稍后刷新状态。'
  }
  return fallback
}

function normalizeRestoreTargetForCompare(value: string): string {
  const normalized = normalizeRestoreTargetForRequest(value)
  if (normalized) {
    return normalized
  }
  const trimmed = value.trim()
  if (trimmed.length <= 1) {
    return trimmed
  }
  return trimmed.replace(/\/+$/, '')
}

function isAbsoluteRestoreTargetPath(value: string): boolean {
  const trimmed = value.trim()
  return trimmed.startsWith('/')
}

function normalizeRestoreTargetSegments(pathBody: string, separatorPattern: RegExp): string[] {
  return pathBody.split(separatorPattern).filter(Boolean)
}

function hasUnsafeRestoreTargetSegment(value: string): boolean {
  return value.trim().split('/').some((segment) => segment === '.' || segment === '..')
}

function normalizeRestoreTargetForRequest(value: string): string | null {
  const trimmed = value.trim()
  if (trimmed.includes('\\')) {
    return null
  }
  if (!trimmed.startsWith('/')) {
    return null
  }
  const segments = normalizeRestoreTargetSegments(trimmed.slice(1), /\/+/)
  if (segments.length === 0 || segments.some((segment) => segment === '.' || segment === '..')) {
    return null
  }
  return `/${segments.join('/')}`
}

function normalizeBatchRestoreItemsForRequest(items: BackupBatchRestoreItemRequest[]): BackupBatchRestoreItemRequest[] {
  return items.map((item) => ({
    ...item,
    target_path: normalizeRestoreTargetForRequest(item.target_path) ?? item.target_path.trim(),
  }))
}

function getRestoreTargetSafetyPath(value: string): string | null {
  const trimmed = value.trim()
  if (trimmed.startsWith('/')) {
    return `/${normalizeRestoreTargetSegments(trimmed.slice(1), /\/+/).join('/')}`
  }
  return null
}

function isRestoreTargetProtectedPath(value: string): boolean {
  const safetyPath = getRestoreTargetSafetyPath(value)
  if (!safetyPath) {
    return false
  }
  const normalized = safetyPath.replace(/\/+$/, '') || '/'
  if (normalized === '/' || /^[a-z]:$/.test(normalized) || /^\/\/[^/]+\/[^/]+$/.test(normalized)) {
    return true
  }
  return new Set([
    '/bin', '/boot', '/dev', '/etc', '/home', '/lib', '/lib64', '/media', '/mnt',
    '/opt', '/proc', '/root', '/run', '/sbin', '/srv', '/sys', '/tmp', '/usr',
    '/usr/local', '/usr/local/bin', '/usr/local/share', '/var',
  ]).has(normalized)
}

function getRestoreTargetInputError(targetPath: string): string | null {
  if (hasControlCharacter(targetPath)) {
    return '恢复目标不能包含控制字符。'
  }
  const trimmed = targetPath.trim()
  if (trimmed === '') {
    return null
  }
  if (trimmed.includes('\\')) {
    return '恢复目标不能包含反斜杠，请使用服务器上的 POSIX 绝对路径。'
  }
  if (!isAbsoluteRestoreTargetPath(trimmed)) {
    return '恢复目标必须是服务器上的绝对路径，例如 /mnt/restore/mnemonas。'
  }
  if (hasUnsafeRestoreTargetSegment(trimmed)) {
    return '恢复目标不能包含 . 或 .. 路径段。'
  }
  if (isRestoreTargetProtectedPath(trimmed)) {
    return '恢复目标不能是文件系统根目录或受保护系统目录。'
  }
  return null
}

function getBatchRestoreTargetInputError(items: BackupBatchRestoreItemRequest[]): string | null {
  for (const [index, item] of items.entries()) {
    const error = getRestoreTargetInputError(item.target_path)
    if (error) {
      return `第 ${index + 1} 项：${error}`
    }
  }
  return null
}

type RestoreTargetConflictKey = {
  root: string
  segments: string[]
}

function getRestoreTargetConflictKey(value: string): RestoreTargetConflictKey | null {
  const trimmed = value.trim()
  if (trimmed.startsWith('/')) {
    return {
      root: '/',
      segments: normalizeRestoreTargetSegments(trimmed.slice(1), /\/+/),
    }
  }
  return null
}

function restoreTargetContainsOrEquals(parent: string, child: string): boolean {
  const parentPath = getRestoreTargetConflictKey(parent)
  const childPath = getRestoreTargetConflictKey(child)
  if (!parentPath || !childPath || parentPath.root !== childPath.root) {
    return false
  }
  if (parentPath.segments.length > childPath.segments.length) {
    return false
  }
  return parentPath.segments.every((segment, index) => segment === childPath.segments[index])
}

function getBatchRestoreTargetConflict(items: BackupBatchRestoreItemRequest[]): string | null {
  const seen: Array<{ index: number; path: string }> = []
  for (const [index, item] of items.entries()) {
    const targetPath = item.target_path.trim()
    if (!getRestoreTargetConflictKey(targetPath)) {
      continue
    }
    for (const existing of seen) {
      if (restoreTargetContainsOrEquals(existing.path, targetPath) || restoreTargetContainsOrEquals(targetPath, existing.path)) {
        return `第 ${existing.index + 1} 项和第 ${index + 1} 项的目标目录重复或存在父子嵌套，请改为互不包含的独立目录。`
      }
    }
    seen.push({ index, path: targetPath })
  }
  return null
}

function effectiveRestoreIncludeConfig(job: BackupJob | null, includeConfig: boolean): boolean {
  return job?.type === 'local' && includeConfig
}

function isCurrentRestorePreview(
  preview: BackupRestorePreviewResult | null,
  request: RestorePreviewRequestSnapshot | null,
  job: BackupJob | null,
  targetPath: string,
  includeConfig: boolean,
): boolean {
  if (!preview || !request || !job || preview.job_id !== request.jobId || request.jobId !== job.id || preview.status !== 'completed') {
    return false
  }
  return normalizeRestoreTargetForCompare(request.targetPath) === normalizeRestoreTargetForCompare(targetPath)
    && request.includeConfig === effectiveRestoreIncludeConfig(job, includeConfig)
}

function getBatchRestorePreviewMetricText(result: BackupBatchRestorePreviewResult): string {
  if (result.status === 'running') {
    return '批量恢复预览生成中'
  }
  if (result.status === 'failed') {
    return '批量恢复预览失败'
  }
  return `${result.items.length} 项 · 预计 ${result.total_files} 个文件 · ${formatBytes(result.total_bytes)}`
}

function getBatchRestoreMetricText(result: BackupBatchRestoreResult): string {
  if (result.status === 'running') {
    return '批量恢复运行中'
  }
  if (result.status === 'failed') {
    return '批量恢复失败'
  }
  const completedCount = result.items.filter((item) => item.status === 'completed').length
  return `${completedCount}/${result.items.length} 项完成 · ${result.total_files} 个文件 · ${formatBytes(result.verified_bytes)}`
}

function getBatchRestoreJobLookup(jobs: BackupJob[]): Map<string, BackupJob> {
  return new Map(jobs.map((job) => [job.id, job]))
}

function getBatchRestoreItemJobLabel(item: BackupBatchRestoreItemResult, job: BackupJob | undefined): string {
  return job ? `${job.name}（${item.job_id}）` : item.job_id
}

function getBatchRestoreJobContextReportLines(job: BackupJob | undefined): string[] {
  if (!job) {
    return []
  }

  const lines = [
    `任务类型：${job.type}`,
    `备份来源：${job.source}`,
    `备份目标：${job.destination}`,
  ]
  if (job.repository) {
    lines.push(`仓库：${job.repository}`)
  }
  if (job.remote) {
    lines.push(`远端：${job.remote}`)
  }
  lines.push(`保留策略：${getBackupPolicyStatusLabel(job.retention_status)} · ${getBackupRetentionText(job)}`)
  if (job.retention_policy) {
    lines.push(`保留命令：${job.retention_policy}`)
  }
  return lines
}

function buildBatchRestoreItems(
  jobs: BackupJob[],
  selectedJobIds: string[],
  targets: Record<string, string>,
  includeConfig: Record<string, boolean>,
): BackupBatchRestoreItemRequest[] {
  const items: BackupBatchRestoreItemRequest[] = []
  selectedJobIds.forEach((jobId) => {
    const job = jobs.find((candidate) => candidate.id === jobId)
    if (!job) {
      return
    }
    items.push({
      job_id: job.id,
      target_path: (targets[job.id] ?? '').trim(),
      include_config: effectiveRestoreIncludeConfig(job, includeConfig[job.id] ?? false),
    })
  })
  return items
}

function hasFailedBatchRestorePreview(result: BackupBatchRestorePreviewResult | null): boolean {
  if (!result) {
    return false
  }
  return result.status === 'failed'
    || result.items.some((item) => item.status === 'failed' || hasFailedRestorePreflight(item.preview ?? null))
}

function isCurrentBatchRestorePreview(
  preview: BackupBatchRestorePreviewResult | null,
  requestItems: BackupBatchRestoreItemRequest[] | null,
  items: BackupBatchRestoreItemRequest[],
): boolean {
  if (!preview || !requestItems || preview.status !== 'completed' || preview.items.length !== requestItems.length || requestItems.length !== items.length || items.length === 0) {
    return false
  }
  return items.every((item, index) => {
    const requestItem = requestItems[index]
    return requestItem?.job_id === item.job_id
      && Boolean(requestItem.include_config) === Boolean(item.include_config)
    && normalizeRestoreTargetForCompare(requestItem.target_path) === normalizeRestoreTargetForCompare(item.target_path)
  })
}

function getBatchRestorePreflightCounts(result: BackupBatchRestorePreviewResult): { passed: number; warning: number; failed: number } {
  return result.items.reduce((counts, item) => {
    const itemCounts = getRestorePreflightCounts(item.preview?.preflight_checks)
    counts.passed += itemCounts.passed
    counts.warning += itemCounts.warning
    counts.failed += itemCounts.failed
    if (item.status === 'failed') {
      counts.failed += 1
    }
    return counts
  }, { passed: 0, warning: 0, failed: 0 })
}

function BatchRestoreImpactSummary({
  result,
  matches,
}: {
  result: BackupBatchRestorePreviewResult
  matches: boolean
}) {
  const preflightCounts = getBatchRestorePreflightCounts(result)
  const configIncludedCount = result.items.filter((item) => item.include_config).length
  const failedItems = result.items.filter((item) => item.status === 'failed').length
  const warningItems = result.items.filter((item) => (item.preview?.warnings?.length ?? 0) > 0 || item.preview?.preflight_checks?.some((check) => check.status === 'warning')).length
  const hasFailures = failedItems > 0 || preflightCounts.failed > 0 || result.status === 'failed'
  const hasWarnings = (result.warnings?.length ?? 0) > 0 || warningItems > 0 || preflightCounts.warning > 0 || result.warning
  const conflictText = !matches
    ? '选中的任务、目标目录或配置选项已变更，当前批量预览不能作为执行依据。'
    : hasFailures
      ? '存在失败项目或失败预检，处理后需重新生成批量预览。'
      : hasWarnings
        ? '存在提醒项目，执行前需要逐项确认目标目录、容量或配置影响。'
        : '未发现批量目标目录重复或父子嵌套；恢复会按顺序写入独立目录。'
  const permissionText = configIncludedCount > 0
    ? `${configIncludedCount} 项会恢复配置文件到各自目标目录；切换前需逐项比对用户、目录权限、分享、告警和公开访问设置。`
    : '本批次不恢复配置文件，当前运行中的用户、目录权限、分享、告警和公开访问设置不会自动改变。'

  return (
    <div aria-label="批量恢复影响摘要" className="rounded-lg border border-divider bg-content1 p-3">
      <div className="flex items-center gap-2 text-sm font-medium text-default-800">
        <ListChecks size={16} />
        <span>批量恢复影响摘要</span>
      </div>
      <div className="mt-3 grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
        <RestoreImpactItem
          label="目标目录"
          value={`${result.items.length} 个独立目标目录；批量执行前会阻止重复或父子嵌套目标。`}
          tone={matches && !hasFailures ? 'success' : 'danger'}
        />
        <RestoreImpactItem label="冲突与覆盖" value={conflictText} tone={!matches || hasFailures ? 'danger' : hasWarnings ? 'warning' : 'success'} />
        <RestoreImpactItem label="权限影响" value={permissionText} tone={configIncludedCount > 0 ? 'warning' : 'success'} />
        <RestoreImpactItem label="恢复内容" value={`${result.total_files} 个文件 · ${formatBytes(result.total_bytes)}`} tone="default" />
        <RestoreImpactItem
          label="预检结果"
          value={`${preflightCounts.passed} 项通过 · ${preflightCounts.warning} 项提醒 · ${preflightCounts.failed} 项失败`}
          tone={preflightCounts.failed > 0 ? 'danger' : preflightCounts.warning > 0 ? 'warning' : 'success'}
        />
        <RestoreImpactItem
          label="恢复后校验"
          value="每个成功项目都会自动执行只读校验；批量结果汇总已校验文件数和字节数。"
          tone="default"
        />
      </div>
    </div>
  )
}

function BatchRestorePreviewSummary({ result }: { result: BackupBatchRestorePreviewResult }) {
  return (
    <div className={result.warning || result.status === 'failed' ? 'rounded-lg border border-warning/20 bg-warning/10 p-4 text-sm' : 'rounded-lg border border-success/20 bg-success/10 p-4 text-sm'}>
      <div className="flex items-center justify-between gap-3">
        <div className="font-medium">批量预览结果</div>
        <BackupStatusChip status={result.status} warning={result.warning} />
      </div>
      <div className="mt-2 text-default-600">{getBatchRestorePreviewMetricText(result)}</div>
      {result.warnings && result.warnings.length > 0 && (
        <div className="mt-2 text-warning">{getBackupDiagnosticDisplayMessage(result.warnings[0])}</div>
      )}
      <div className="mt-3 space-y-2">
        {result.items.map((item) => (
          <div key={`${item.index}-${item.job_id}`} className="rounded-lg border border-divider bg-content1 p-3">
            <div className="flex items-center justify-between gap-3">
              <div className="min-w-0">
                <div className="font-medium">{item.job_id}</div>
                <div className="truncate font-mono text-xs text-default-500" title={item.target_path}>{item.target_path}</div>
              </div>
              <BackupStatusChip status={item.status} warning={(item.preview?.warnings?.length ?? 0) > 0} />
            </div>
            {item.preview && (
              <>
                <div className="mt-2 text-default-500">{getBackupRestorePreviewMetricText(item.preview)}</div>
                <RestorePreflightList checks={item.preview.preflight_checks} />
                {item.preview.warnings && item.preview.warnings.length > 0 && (
                  <div className="mt-2 text-warning">{getBackupDiagnosticDisplayMessage(item.preview.warnings[0])}</div>
                )}
              </>
            )}
            {item.error_message && <div className="mt-2 text-danger">{getBackupDiagnosticDisplayMessage(item.error_message)}</div>}
          </div>
        ))}
      </div>
    </div>
  )
}

function BatchRestoreExecutionReview({
  result,
  matches,
}: {
  result: BackupBatchRestorePreviewResult
  matches: boolean
}) {
  const preflightCounts = getBatchRestorePreflightCounts(result)
  const configIncludedCount = result.items.filter((item) => item.include_config).length
  const hasWarnings = result.warning || (result.warnings?.length ?? 0) > 0 || preflightCounts.warning > 0
  const hasFailures = result.status === 'failed' || preflightCounts.failed > 0
  const toneClass = !matches || hasFailures
    ? 'border-danger/20 bg-danger/10'
    : hasWarnings
      ? 'border-warning/20 bg-warning/10'
      : 'border-success/20 bg-success/10'
  const targetSummary = `${result.items.length} 个互不重叠的独立目标目录`

  const reviewItems = [
    { label: '恢复项目', value: `${result.items.length} 项` },
    { label: '目标目录', value: targetSummary },
    { label: '恢复内容', value: `${result.total_files} 个文件 · ${formatBytes(result.total_bytes)}` },
    { label: '配置文件', value: configIncludedCount > 0 ? `${configIncludedCount} 项会恢复配置文件` : '本次不恢复配置文件' },
    { label: '预检结果', value: `${preflightCounts.passed} 项通过 · ${preflightCounts.warning} 项提醒 · ${preflightCounts.failed} 项失败` },
    { label: '恢复后检查', value: '每个成功项目都会自动执行只读校验；批量结果会汇总已校验文件数和字节数。' },
    { label: '跨目录切换', value: '逐项只读校验通过后再切换；保留原目录和原配置作为回滚点。' },
  ]

  return (
    <div aria-label="批量恢复执行前复核" className={`rounded-lg border p-3 text-sm ${toneClass}`}>
      <div className="font-medium text-default-800">批量恢复执行前复核</div>
      <div className="mt-3 grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {reviewItems.map((item) => (
          <div key={item.label} className="min-w-0">
            <div className="text-xs text-default-500">{item.label}</div>
            <div className="mt-1 break-words text-default-700">{item.value}</div>
          </div>
        ))}
      </div>
      {!matches && (
        <div className="mt-3 text-xs text-danger">选中的任务、目标目录或配置选项已变更，当前复核不可用于执行批量恢复。</div>
      )}
    </div>
  )
}

function BatchRestoreReadinessSummary({
  items,
  withinLimit,
  targetInputError,
  targetConflict,
  preview,
  previewMatches,
  previewHasFailed,
}: {
  items: BackupBatchRestoreItemRequest[]
  withinLimit: boolean
  targetInputError: string | null
  targetConflict: string | null
  preview: BackupBatchRestorePreviewResult | null
  previewMatches: boolean
  previewHasFailed: boolean
}) {
  const selectedCount = items.length
  const filledTargetCount = items.filter((item) => item.target_path.trim().length > 0).length
  const configIncludedCount = items.filter((item) => item.include_config).length
  const previewCounts = preview ? getBatchRestorePreflightCounts(preview) : null
  const previewHasWarnings = Boolean(preview && (
    preview.warning
    || (preview.warnings?.length ?? 0) > 0
    || (previewCounts?.warning ?? 0) > 0
  ))
  const targetHasIssue = Boolean(targetInputError || targetConflict || !withinLimit)
  const targetText = selectedCount === 0
    ? '尚未选择目标'
    : `${filledTargetCount} / ${selectedCount} 已填写${targetConflict ? '；存在重复或父子嵌套' : targetInputError ? '；存在格式错误' : ''}`
  const previewText = selectedCount === 0
    ? '选择任务后生成批量预览'
    : targetHasIssue
      ? '处理目标目录后再生成批量预览'
      : !preview
        ? '需要生成批量预览'
        : !previewMatches
          ? '选择、目标或配置已变更，需重新生成批量预览'
          : previewHasFailed
            ? '批量预检存在失败项'
            : previewHasWarnings
              ? '批量预览可用于执行，但存在提醒'
              : '批量预览可用于执行'
  const previewTone: RestoreImpactTone = selectedCount === 0 || !preview
    ? 'default'
    : !previewMatches || previewHasFailed
      ? 'danger'
      : previewHasWarnings
        ? 'warning'
        : 'success'

  return (
    <div aria-label="批量恢复准备度摘要" className="rounded-lg border border-divider bg-content1 p-3">
      <div className="flex items-center gap-2 text-sm font-medium text-default-800">
        <ListChecks size={16} />
        <span>批量恢复准备度摘要</span>
      </div>
      <div className="mt-3 grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
        <RestoreImpactItem
          label="选择任务"
          value={selectedCount === 0 ? '尚未选择任务' : `${selectedCount} / ${batchRestoreItemLimit} 项`}
          tone={selectedCount === 0 ? 'default' : withinLimit ? 'success' : 'danger'}
        />
        <RestoreImpactItem
          label="目标目录"
          value={targetText}
          tone={selectedCount === 0 ? 'default' : targetHasIssue ? 'danger' : filledTargetCount === selectedCount ? 'success' : 'warning'}
        />
        <RestoreImpactItem
          label="配置文件"
          value={configIncludedCount > 0 ? `${configIncludedCount} 项会恢复配置文件` : '本批次不恢复配置文件'}
          tone={configIncludedCount > 0 ? 'warning' : 'success'}
        />
        <RestoreImpactItem label="预览状态" value={previewText} tone={previewTone} />
      </div>
    </div>
  )
}

function BatchRestoreFlowGuide({
  items,
  withinLimit,
  targetInputError,
  targetConflict,
  targetsReady,
  preview,
  previewMatches,
  previewHasFailed,
  isPreviewing,
  isRestoring,
  result,
}: {
  items: BackupBatchRestoreItemRequest[]
  withinLimit: boolean
  targetInputError: string | null
  targetConflict: string | null
  targetsReady: boolean
  preview: BackupBatchRestorePreviewResult | null
  previewMatches: boolean
  previewHasFailed: boolean
  isPreviewing: boolean
  isRestoring: boolean
  result: BackupBatchRestoreResult | null
}) {
  const selectedCount = items.length
  const targetBlocked = !withinLimit || Boolean(targetInputError || targetConflict)
  const previewFailed = Boolean(preview && previewMatches && previewHasFailed)
  const previewComplete = Boolean(result) || Boolean(preview && previewMatches && !previewFailed)
  const failedResultItems = result?.items.filter((item) => item.status === 'failed').length ?? 0
  const allResultItemsFailed = Boolean(result && result.items.length > 0 && failedResultItems === result.items.length)
  const resultHasReviewItems = Boolean(result && (
    result.warning
    || (result.warnings?.length ?? 0) > 0
    || failedResultItems > 0
    || result.items.some((item) => (
      (item.warnings?.length ?? 0) > 0
      || item.restore?.status === 'failed'
      || item.verify?.status === 'failed'
      || (item.verify?.warnings?.length ?? 0) > 0
      || (item.verify ? !item.verify.looks_like_storage_root : false)
    ))
  ))
  const resultFailed = result?.status === 'failed' || allResultItemsFailed

  const steps: RestoreFlowStep[] = [{
    id: 'select',
    title: '选择任务',
    description: selectedCount === 0
      ? '选择要恢复的备份任务'
      : !withinLimit
        ? `一次最多恢复 ${batchRestoreItemLimit} 项`
        : `已选择 ${selectedCount} 项`,
    state: selectedCount === 0 ? 'active' : withinLimit ? 'complete' : 'blocked',
  }, {
    id: 'target',
    title: '目标目录',
    description: selectedCount === 0
      ? '选择任务后填写独立目标目录'
      : targetConflict
        ? '目标目录重复或存在父子嵌套'
        : targetInputError
          ? '目标目录格式需要处理'
          : !withinLimit
            ? `一次最多恢复 ${batchRestoreItemLimit} 项`
            : targetsReady
              ? `${selectedCount} 个目标目录已确认`
              : '为每个任务填写独立目标目录',
    state: selectedCount === 0
      ? 'pending'
      : targetBlocked
        ? 'blocked'
        : targetsReady
          ? 'complete'
          : 'active',
  }, {
    id: 'preview',
    title: '批量预览',
    description: result
      ? '已完成批量预览和预检'
      : isPreviewing
        ? '正在生成批量预览和预检'
        : previewFailed
          ? '存在失败预检，处理后重新生成预览'
          : preview && !previewMatches
            ? '选择、目标或配置已变更，需要重新生成预览'
            : previewComplete
              ? '批量预览可用于执行'
              : targetsReady
                ? '生成批量预览以确认预检'
                : '目标目录确认后生成预览',
    state: result || previewComplete
      ? 'complete'
      : previewFailed
        ? 'blocked'
        : targetsReady || isPreviewing || Boolean(preview)
          ? 'active'
          : 'pending',
  }, {
    id: 'execute',
    title: '执行与只读校验',
    description: resultFailed
      ? '批量恢复失败，未完成的项目需处理'
      : resultHasReviewItems
        ? '批量恢复完成但存在需复核项目'
        : result
          ? '批量恢复和只读校验已完成'
          : isRestoring
            ? '正在按顺序写入目标目录'
            : previewComplete
              ? '预览通过，可开始批量恢复'
              : '预览通过后执行批量恢复',
    state: resultFailed
      ? 'blocked'
      : resultHasReviewItems
        ? 'warning'
        : result
          ? 'complete'
          : isRestoring || previewComplete
            ? 'active'
            : 'pending',
  }]

  return <RestoreFlowStepsPanel ariaLabel="批量恢复流程进度" title="批量恢复流程" steps={steps} />
}

function BatchRestoreResultSummary({ result, jobs }: { result: BackupBatchRestoreResult; jobs: BackupJob[] }) {
  const cutoverCandidates = getBatchRestoreCutoverCandidates(result)
  const jobsById = getBatchRestoreJobLookup(jobs)
  const handleCopyBatchRestoreReport = async () => {
    try {
      await copyTextToClipboard(formatBatchRestoreResultReport(result, jobs))
      addToast({ title: '批量恢复记录已复制', color: 'success' })
    } catch {
      addToast({
        title: '复制批量恢复记录失败',
        description: clipboardWriteFailureDescription,
        color: 'danger',
      })
    }
  }

  return (
    <div className="space-y-4">
      <div className={result.warning || result.status === 'failed' ? 'rounded-lg border border-warning/20 bg-warning/10 p-4 text-sm' : 'rounded-lg border border-success/20 bg-success/10 p-4 text-sm'}>
        <div className="flex items-center justify-between gap-3">
          <div className="font-medium">{result.status === 'failed' ? '批量恢复失败' : result.warning ? '批量恢复完成，有警告' : '批量恢复已完成'}</div>
          <BackupStatusChip status={result.status} warning={result.warning} />
        </div>
        <div className="mt-2 text-default-600">{getBatchRestoreMetricText(result)}</div>
        {result.error_message && <div className="mt-2 text-danger">{getBackupDiagnosticDisplayMessage(result.error_message)}</div>}
        {result.warnings && result.warnings.length > 0 && <div className="mt-2 text-warning">{getBackupDiagnosticDisplayMessage(result.warnings[0])}</div>}
        <div className="mt-3">
          <Button
            size="sm"
            variant="flat"
            className="rounded-lg"
            startContent={<Copy size={14} />}
            onPress={handleCopyBatchRestoreReport}
          >
            复制批量恢复记录
          </Button>
        </div>
      </div>
      {cutoverCandidates.length > 0 && (
        <div aria-label="批量恢复跨目录切换候选" className="rounded-lg border border-primary/20 bg-primary/5 p-3 text-sm">
          <div className="flex items-center gap-2 font-medium text-default-800">
            <ListChecks size={16} />
            <span>跨目录切换候选</span>
          </div>
          <div className="mt-2 text-xs leading-5 text-default-500">
            逐项只读校验通过后再切换；切换前保留原 storage.root、原配置文件和回滚清单。
          </div>
          <div className="mt-3 space-y-2">
            {cutoverCandidates.map((item) => {
              const job = jobsById.get(item.job_id)
              return (
                <div key={`${item.index}-${item.job_id}-cutover`} className="rounded-md bg-content1 p-2">
                  <div className="font-medium text-default-700">{getBatchRestoreItemJobLabel(item, job)}</div>
                  <div className="mt-1 truncate font-mono text-xs text-default-500" title={item.target_path}>{item.target_path}</div>
                  {job && <div className="mt-1 truncate text-xs text-default-400" title={job.destination}>备份目标：{job.destination}</div>}
                  <div className="mt-1 text-xs text-default-600">{getBatchRestoreCutoverCandidateReviewText(item)}</div>
                  <div className="mt-1 text-xs text-default-500">配置文件：{getBatchRestoreItemConfigText(item)}</div>
                </div>
              )
            })}
          </div>
        </div>
      )}
      {result.items.length > 0 && (
        <div aria-label="批量恢复冲突处置记录" className="rounded-lg border border-divider bg-content1 p-3 text-sm">
          <div className="flex items-center gap-2 font-medium text-default-800">
            <FileWarning size={16} />
            <span>冲突处置记录</span>
          </div>
          <div className="mt-2 text-xs leading-5 text-default-500">
            批量恢复只写入各自目标目录，不覆盖当前 storage.root；失败项和未通过校验项不能进入切换。
          </div>
          <div className="mt-3 grid gap-2">
            {result.items.map((item) => {
              const job = jobsById.get(item.job_id)
              return (
                <RestoreImpactItem
                  key={`${item.index}-${item.job_id}-disposition`}
                  label={getBatchRestoreItemJobLabel(item, job)}
                  value={getBatchRestoreItemDispositionText(item)}
                  tone={getBatchRestoreItemDispositionTone(item)}
                />
              )
            })}
          </div>
        </div>
      )}
      <div className="space-y-2">
        {result.items.map((item) => {
          const job = jobsById.get(item.job_id)
          return (
            <div key={`${item.index}-${item.job_id}`} className="rounded-lg border border-divider bg-content2/60 p-3 text-sm">
              <div className="flex items-center justify-between gap-3">
                <div className="min-w-0">
                  <div className="font-medium">{getBatchRestoreItemJobLabel(item, job)}</div>
                  <div className="truncate font-mono text-xs text-default-500" title={item.target_path}>{item.target_path}</div>
                </div>
                <BackupStatusChip status={item.status} warning={(item.warnings?.length ?? 0) > 0 || item.verify?.status === 'failed' || (item.verify?.warnings?.length ?? 0) > 0 || Boolean(item.verify?.error_message)} />
              </div>
              {job && (
                <div className="mt-2 grid gap-1 text-xs text-default-500 sm:grid-cols-2">
                  <div className="truncate" title={job.destination}>备份目标：{job.destination}</div>
                  <div className="truncate" title={getBackupRetentionText(job)}>
                    保留策略：{getBackupPolicyStatusLabel(job.retention_status)} · {getBackupRetentionText(job)}
                  </div>
                </div>
              )}
              {item.restore && <div className="mt-2 text-default-500">{getBackupRestoreMetricText(item.restore)}</div>}
              {item.verify && (
                <div className={item.verify.warnings && item.verify.warnings.length > 0 ? 'mt-1 text-warning' : 'mt-1 text-default-500'}>
                  只读校验：{getBackupRestoreVerifyMetricText(item.verify)}
                </div>
              )}
              {item.verify?.snapshot_path && (
                <div className="mt-1 truncate text-default-400" title={item.verify.snapshot_path}>
                  {getBackupSnapshotReferenceText(item.verify.snapshot_path)}
                </div>
              )}
              {item.warnings && item.warnings.length > 0 && <div className="mt-1 text-warning">{getBackupDiagnosticDisplayMessage(item.warnings[0])}</div>}
              {item.verify?.error_message && <div className="mt-1 text-danger">校验错误：{getBackupDiagnosticDisplayMessage(item.verify.error_message)}</div>}
              {item.error_message && <div className="mt-1 text-danger">{getBackupDiagnosticDisplayMessage(item.error_message)}</div>}
              <div className="mt-2 text-default-500">处置建议：{getBatchRestoreItemDispositionText(item)}</div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

export default function Maintenance() {
  const queryClient = useQueryClient()
  const [searchParams] = useSearchParams()
  const user = useUser()
  const diagnosticsExportAbortControllerRef = useRef<AbortController | null>(null)
  const restoreReportExportAbortControllerRef = useRef<AbortController | null>(null)
  const scrubAbortControllerRef = useRef<AbortController | null>(null)
  const runBackupAbortControllerRef = useRef<AbortController | null>(null)
  const retentionCheckAbortControllerRef = useRef<AbortController | null>(null)
  const restoreDrillAbortControllerRef = useRef<AbortController | null>(null)
  const restorePreviewAbortControllerRef = useRef<AbortController | null>(null)
  const restoreAbortControllerRef = useRef<AbortController | null>(null)
  const restoreVerifyAbortControllerRef = useRef<AbortController | null>(null)
  const batchRestorePreviewAbortControllerRef = useRef<AbortController | null>(null)
  const batchRestoreAbortControllerRef = useRef<AbortController | null>(null)
  const [isExporting, setIsExporting] = useState(false)
  const [exportingRestoreReportJobId, setExportingRestoreReportJobId] = useState<string | null>(null)
  const [isAwaitingRunningState, setIsAwaitingRunningState] = useState(false)
  const [restoreJob, setRestoreJob] = useState<BackupJob | null>(null)
  const [restoreTargetPath, setRestoreTargetPath] = useState('')
  const [restoreIncludeConfig, setRestoreIncludeConfig] = useState(false)
  const [restorePreview, setRestorePreview] = useState<BackupRestorePreviewResult | null>(null)
  const [restorePreviewRequest, setRestorePreviewRequest] = useState<RestorePreviewRequestSnapshot | null>(null)
  const [restoreResult, setRestoreResult] = useState<BackupRestoreResult | null>(null)
  const [restoreVerifyResult, setRestoreVerifyResult] = useState<BackupRestoreVerifyResult | null>(null)
  const [isBatchRestoreOpen, setIsBatchRestoreOpen] = useState(false)
  const [batchRestoreSelectedJobIds, setBatchRestoreSelectedJobIds] = useState<string[]>([])
  const [batchRestoreTargets, setBatchRestoreTargets] = useState<Record<string, string>>({})
  const [batchRestoreIncludeConfig, setBatchRestoreIncludeConfig] = useState<Record<string, boolean>>({})
  const [batchRestorePreview, setBatchRestorePreview] = useState<BackupBatchRestorePreviewResult | null>(null)
  const [batchRestorePreviewItems, setBatchRestorePreviewItems] = useState<BackupBatchRestoreItemRequest[] | null>(null)
  const [batchRestoreResult, setBatchRestoreResult] = useState<BackupBatchRestoreResult | null>(null)
  const scrubResultQueryKey = ['scrub-result', user?.id ?? 'anonymous'] as const
  const backupJobsQueryKey = ['backup-jobs', user?.id ?? 'anonymous'] as const

  useEffect(() => {
    return () => {
      abortActionControllers([
        diagnosticsExportAbortControllerRef,
        restoreReportExportAbortControllerRef,
        scrubAbortControllerRef,
        runBackupAbortControllerRef,
        retentionCheckAbortControllerRef,
        restoreDrillAbortControllerRef,
        restorePreviewAbortControllerRef,
        restoreAbortControllerRef,
        restoreVerifyAbortControllerRef,
        batchRestorePreviewAbortControllerRef,
        batchRestoreAbortControllerRef,
      ])
    }
  }, [])
  
  // Fetch last scrub result
  const { data: scrubResult, isLoading, error, refetch } = useQuery({
    queryKey: scrubResultQueryKey,
    queryFn: ({ signal }) => getScrubResult({ signal }),
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
    queryFn: ({ signal }) => listBackupJobs({ signal }),
  })
  const backupLoadErrorPresentation = getBackupLoadErrorPresentation(backupError)
  const focusedBackupJobID = searchParams.get('backupJob')?.trim() ?? ''

  useEffect(() => {
    if (!focusedBackupJobID || backupJobs.length === 0) {
      return
    }

    const element = document.getElementById(getBackupJobFocusElementId(focusedBackupJobID))
    if (typeof element?.scrollIntoView === 'function') {
      element.scrollIntoView({ block: 'center', behavior: 'smooth' })
    }
  }, [backupJobs, focusedBackupJobID])

  const handleRefreshScrubResult = async () => {
    const result = await refetch()
    if (result.error) {
      const errorPresentation = getMaintenanceActionErrorPresentation(
        result.error,
        '刷新失败',
        '校验结果暂不可用',
        '维护历史或数据面当前不可用，请检查设备状态或稍后重试。',
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
    setRestoreTargetPath(getSuggestedRestoreTargetPath(job))
    setRestoreIncludeConfig(job.type === 'local' && Boolean(job.include_config))
    setRestorePreview(null)
    setRestorePreviewRequest(null)
    setRestoreResult(null)
    setRestoreVerifyResult(null)
  }

  const closeRestoreModal = () => {
    if (restoreMutation.isPending || restorePreviewMutation.isPending || restoreVerifyMutation.isPending) {
      return
    }
    setRestoreJob(null)
    setRestoreTargetPath('')
    setRestoreIncludeConfig(false)
    setRestorePreview(null)
    setRestorePreviewRequest(null)
    setRestoreResult(null)
    setRestoreVerifyResult(null)
  }

  const handleRestoreTargetPathChange = (value: string) => {
    setRestoreTargetPath(value)
    setRestorePreview(null)
    setRestorePreviewRequest(null)
    setRestoreResult(null)
    setRestoreVerifyResult(null)
  }

  const handleRestoreIncludeConfigChange = (value: boolean) => {
    setRestoreIncludeConfig(value)
    setRestorePreview(null)
    setRestorePreviewRequest(null)
    setRestoreResult(null)
    setRestoreVerifyResult(null)
  }

  const openBatchRestoreModal = () => {
    const defaults: Record<string, boolean> = {}
    const targetDefaults: Record<string, string> = {}
    backupJobs.forEach((job) => {
      defaults[job.id] = job.type === 'local' && Boolean(job.include_config)
      if (!job.disabled && canRunBackupRestore(job)) {
        targetDefaults[job.id] = getSuggestedRestoreTargetPath(job)
      }
    })
    setBatchRestoreSelectedJobIds([])
    setBatchRestoreTargets(targetDefaults)
    setBatchRestoreIncludeConfig(defaults)
    setBatchRestorePreview(null)
    setBatchRestorePreviewItems(null)
    setBatchRestoreResult(null)
    setIsBatchRestoreOpen(true)
  }

  const closeBatchRestoreModal = () => {
    if (batchRestorePreviewMutation.isPending || batchRestoreMutation.isPending) {
      return
    }
    setIsBatchRestoreOpen(false)
    setBatchRestoreSelectedJobIds([])
    setBatchRestoreTargets({})
    setBatchRestorePreview(null)
    setBatchRestorePreviewItems(null)
    setBatchRestoreResult(null)
  }

  const resetBatchRestoreProgress = () => {
    setBatchRestorePreview(null)
    setBatchRestorePreviewItems(null)
    setBatchRestoreResult(null)
  }

  const applyBatchRestoreSelection = (jobs: BackupJob[]) => {
    const selectedJobs = jobs.slice(0, batchRestoreItemLimit)
    const nextTargets = { ...batchRestoreTargets }
    const nextIncludeConfig = { ...batchRestoreIncludeConfig }

    selectedJobs.forEach((job) => {
      if (!nextTargets[job.id]) {
        nextTargets[job.id] = getSuggestedRestoreTargetPath(job)
      }
      nextIncludeConfig[job.id] = job.type === 'local' && Boolean(job.include_config)
    })

    setBatchRestoreSelectedJobIds(selectedJobs.map((job) => job.id))
    setBatchRestoreTargets(nextTargets)
    setBatchRestoreIncludeConfig(nextIncludeConfig)
    resetBatchRestoreProgress()
  }

  const selectAllBatchRestoreJobs = () => {
    applyBatchRestoreSelection(restorableBackupJobs)
  }

  const selectAttentionBatchRestoreJobs = () => {
    applyBatchRestoreSelection(batchRestoreAttentionJobs)
  }

  const clearBatchRestoreSelection = () => {
    setBatchRestoreSelectedJobIds([])
    resetBatchRestoreProgress()
  }

  const handleBatchRestoreSelectedChange = (jobId: string, selected: boolean) => {
    setBatchRestoreSelectedJobIds((current) => (
      selected
        ? (current.includes(jobId) ? current : [...current, jobId])
        : current.filter((currentJobId) => currentJobId !== jobId)
    ))
    resetBatchRestoreProgress()
  }

  const handleBatchRestoreTargetChange = (jobId: string, value: string) => {
    setBatchRestoreTargets((current) => ({ ...current, [jobId]: value }))
    resetBatchRestoreProgress()
  }

  const handleBatchRestoreIncludeConfigChange = (jobId: string, value: boolean) => {
    setBatchRestoreIncludeConfig((current) => ({ ...current, [jobId]: value }))
    resetBatchRestoreProgress()
  }
  
  // Run scrub mutation
  const scrubMutation = useMutation({
    mutationFn: (request: ScrubMutationRequest) => runScrub(undefined, { signal: request.signal }),
    onSuccess: (result, request) => {
      if (request.signal.aborted) {
        return
      }
      if (result.status === 'running') {
        void queryClient.refetchQueries({ queryKey: scrubResultQueryKey, type: 'active' }).finally(() => {
          setIsAwaitingRunningState(false)
        })
      } else {
        void queryClient.invalidateQueries({ queryKey: scrubResultQueryKey })
        setIsAwaitingRunningState(false)
      }

      const title = result.warning
        ? '数据校验完成，但存在警告'
        : (result.status === 'running' ? '数据校验已启动' : '数据校验已完成')
      addToast({ title, color: result.warning ? 'warning' : 'success' })
    },
    onError: (error: unknown, request) => {
      if (request.signal.aborted || isAbortError(error)) {
        return
      }
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
        '数据面或维护服务当前不可用，请检查设备状态后重试。',
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
    onSettled: (_result, _error, request) => {
      clearActionAbortController(scrubAbortControllerRef, request.signal)
    },
  })

  const runBackupMutation = useMutation({
    mutationFn: (request: BackupJobMutationRequest) => runBackupJob(request.jobId, { signal: request.signal }),
    onSuccess: (result, request) => {
      if (request.signal.aborted) {
        return
      }
      void queryClient.invalidateQueries({ queryKey: backupJobsQueryKey })
      const toastColor = getMaintenanceTaskToastColor(result)
      addToast({
        title: result.status === 'failed' ? '备份任务失败' : hasMaintenanceTaskWarning(result) ? '备份完成但有警告' : '备份已完成',
        description: getMaintenanceTaskDescription(result, getBackupRunMetricText(result).replace(' · ', '，')),
        color: toastColor,
      })
    },
    onError: (error: unknown, request) => {
      if (request.signal.aborted || isAbortError(error)) {
        return
      }
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
    onSettled: (_result, _error, request) => {
      clearActionAbortController(runBackupAbortControllerRef, request.signal)
    },
  })

  const retentionCheckMutation = useMutation({
    mutationFn: (request: BackupJobMutationRequest) => checkBackupRetentionJob(request.jobId, { signal: request.signal }),
    onSuccess: (result, request) => {
      if (request.signal.aborted) {
        return
      }
      void queryClient.invalidateQueries({ queryKey: backupJobsQueryKey })
      const toastColor = getMaintenanceTaskToastColor(result)
      addToast({
        title: result.status === 'failed' ? '保留策略检测失败' : hasMaintenanceTaskWarning(result) ? '保留策略检测完成，有警告' : '保留策略检测完成',
        description: getMaintenanceTaskDescription(result, getBackupRetentionCheckMetricText(result).replace(' · ', '，')),
        color: toastColor,
      })
    },
    onError: (error: unknown, request) => {
      if (request.signal.aborted || isAbortError(error)) {
        return
      }
      void queryClient.invalidateQueries({ queryKey: backupJobsQueryKey })
      const errorPresentation = getMaintenanceActionErrorPresentation(
        error,
        '检查保留策略失败',
        '备份任务暂不可用',
        '备份管理器当前不可用，请检查配置后重试。',
      )
      addToast({
        title: getBackupConflictTitle(error, errorPresentation.title),
        description: getBackupConflictDescription(error, errorPresentation.description),
        color: error instanceof ApiError && error.status === 409 ? 'warning' : errorPresentation.color,
      })
    },
    onSettled: (_result, _error, request) => {
      clearActionAbortController(retentionCheckAbortControllerRef, request.signal)
    },
  })

  const restoreDrillMutation = useMutation({
    mutationFn: (request: BackupJobMutationRequest) => runBackupRestoreDrill(request.jobId, false, { signal: request.signal }),
    onSuccess: (result, request) => {
      if (request.signal.aborted) {
        return
      }
      void queryClient.invalidateQueries({ queryKey: backupJobsQueryKey })
      const toastColor = getMaintenanceTaskToastColor(result)
      addToast({
        title: result.status === 'failed' ? '恢复演练失败' : result.status === 'running' ? '恢复演练已启动' : hasMaintenanceTaskWarning(result) ? '恢复演练完成，有警告' : '恢复演练已完成',
        description: getMaintenanceTaskDescription(result, getBackupRestoreDrillMetricText(result).replace(' · ', '，')),
        color: toastColor,
      })
    },
    onError: (error: unknown, request) => {
      if (request.signal.aborted || isAbortError(error)) {
        return
      }
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
    onSettled: (_result, _error, request) => {
      clearActionAbortController(restoreDrillAbortControllerRef, request.signal)
    },
  })

  const restorePreviewMutation = useMutation({
    mutationFn: (req: RestorePreviewMutationRequest) => previewBackupRestoreJob(req.jobId, req.targetPath, req.includeConfig, { signal: req.signal }),
    onSuccess: (result, request) => {
      if (request.signal.aborted) {
        return
      }
      setRestorePreview(result)
      setRestorePreviewRequest({
        jobId: request.jobId,
        targetPath: request.targetPath,
        includeConfig: request.includeConfig,
      })
      const hasFailedPreflight = result.status === 'failed' || (result.preflight_checks?.some((check) => check.status === 'failed') ?? false)
      const hasWarnings = hasFailedPreflight || (result.warnings?.length ?? 0) > 0
      addToast({
        title: result.status === 'failed' ? '恢复预览失败' : hasFailedPreflight ? '恢复预检未通过' : hasWarnings ? '恢复预览已生成，有提醒' : '恢复预览已生成',
        description: getMaintenanceTaskDescription(result, getBackupRestorePreviewMetricText(result).replace(' · ', '，')),
        color: hasFailedPreflight ? 'danger' : hasWarnings ? 'warning' : 'success',
      })
    },
    onError: (error: unknown, request) => {
      if (request.signal.aborted || isAbortError(error)) {
        return
      }
      setRestorePreview(null)
      setRestorePreviewRequest(null)
      const errorPresentation = getMaintenanceActionErrorPresentation(
        error,
        '生成恢复预览失败',
        '恢复预览暂不可用',
        '备份管理器当前不可用，请检查配置后重试。',
      )
      addToast({
        title: getBackupConflictTitle(error, errorPresentation.title),
        description: getBackupConflictDescription(error, errorPresentation.description),
        color: error instanceof ApiError && error.status === 409 ? 'warning' : errorPresentation.color,
      })
    },
    onSettled: (_result, _error, request) => {
      clearActionAbortController(restorePreviewAbortControllerRef, request.signal)
    },
  })

  const restoreVerifyMutation = useMutation({
    mutationFn: (req: RestoreVerifyMutationRequest) => verifyBackupRestoreJob(req.jobId, req.targetPath, { signal: req.signal }),
    onSuccess: (result, request) => {
      if (request.signal.aborted) {
        return
      }
      void queryClient.invalidateQueries({ queryKey: backupJobsQueryKey })
      setRestoreVerifyResult(result)
      const verifyWarnings = result.warnings ?? []
      const toastColor = getMaintenanceTaskToastColor(result)
      addToast({
        title: result.status === 'failed' ? '恢复目录检查失败' : verifyWarnings.length > 0 ? '恢复目录检查完成，有警告' : '恢复目录检查完成',
        description: getMaintenanceTaskDescription(result, getBackupRestoreVerifyMetricText(result).replace(' · ', '，')),
        color: toastColor,
      })
    },
    onError: (error: unknown, request) => {
      if (request.signal.aborted || isAbortError(error)) {
        return
      }
      setRestoreVerifyResult(null)
      const errorPresentation = getMaintenanceActionErrorPresentation(
        error,
        '校验恢复目录失败',
        '恢复校验暂不可用',
        '备份管理器当前不可用，请检查配置后重试。',
      )
      addToast({
        title: getBackupConflictTitle(error, errorPresentation.title),
        description: getBackupConflictDescription(error, errorPresentation.description),
        color: error instanceof ApiError && error.status === 409 ? 'warning' : errorPresentation.color,
      })
    },
    onSettled: (_result, _error, request) => {
      clearActionAbortController(restoreVerifyAbortControllerRef, request.signal)
    },
  })

  const startRestoreVerify = (jobId: string, targetPath: string) => {
    const targetInputError = getRestoreTargetInputError(targetPath)
    if (targetInputError) {
      addToast({
        title: '恢复目标格式无效',
        description: targetInputError,
        color: 'danger',
      })
      return
    }
    const normalizedTargetPath = normalizeRestoreTargetForRequest(targetPath) ?? targetPath.trim()
    const controller = createActionAbortController(restoreVerifyAbortControllerRef)
    restoreVerifyMutation.mutate({ jobId, targetPath: normalizedTargetPath, signal: controller.signal })
  }

  const restoreMutation = useMutation({
    mutationFn: (req: RestoreMutationRequest) => restoreBackupJob(req.jobId, req.targetPath, req.includeConfig, { signal: req.signal }),
    onSuccess: (result, request) => {
      if (request.signal.aborted) {
        return
      }
      void queryClient.invalidateQueries({ queryKey: backupJobsQueryKey })
      setRestoreResult(result)
      setRestoreVerifyResult(null)
      setRestoreTargetPath(request.targetPath)
      const restoreWarnings = result.warnings ?? []
      const toastColor = getMaintenanceTaskToastColor(result)
      addToast({
        title: result.status === 'failed' ? '备份恢复失败' : restoreWarnings.length > 0 ? '备份已恢复，有警告' : '备份已恢复',
        description: getMaintenanceTaskDescription(result, `${getBackupRestoreMetricText(result)}，目标：${result.target_path}`),
        color: toastColor,
      })
      if (result.status !== 'failed') {
        startRestoreVerify(result.job_id, request.targetPath)
      }
    },
    onError: (error: unknown, request) => {
      if (request.signal.aborted || isAbortError(error)) {
        return
      }
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
    onSettled: (_result, _error, request) => {
      clearActionAbortController(restoreAbortControllerRef, request.signal)
    },
  })

  const batchRestorePreviewMutation = useMutation({
    mutationFn: (request: BatchRestoreMutationRequest) => previewBatchBackupRestore(request.items, { signal: request.signal }),
    onSuccess: (result, request) => {
      if (request.signal.aborted) {
        return
      }
      setBatchRestorePreview(result)
      setBatchRestorePreviewItems(request.items.map((item) => ({ ...item })))
      setBatchRestoreResult(null)
      const hasFailedPreflight = hasFailedBatchRestorePreview(result)
      const hasWarnings = hasFailedPreflight || result.warning || (result.warnings?.length ?? 0) > 0
      addToast({
        title: hasFailedPreflight ? '批量恢复预检未通过' : hasWarnings ? '批量恢复预览已生成，有提醒' : '批量恢复预览已生成',
        description: hasWarnings && result.warnings?.[0]
          ? getBackupDiagnosticDisplayMessage(result.warnings[0])
          : getBatchRestorePreviewMetricText(result).replace(' · ', '，'),
        color: hasFailedPreflight ? 'danger' : hasWarnings ? 'warning' : 'success',
      })
    },
    onError: (error: unknown, request) => {
      if (request.signal.aborted || isAbortError(error)) {
        return
      }
      setBatchRestorePreview(null)
      setBatchRestorePreviewItems(null)
      const errorPresentation = getMaintenanceActionErrorPresentation(
        error,
        '生成批量恢复预览失败',
        '批量恢复暂不可用',
        '备份管理器当前不可用，请检查配置后重试。',
      )
      addToast({
        title: getBackupConflictTitle(error, errorPresentation.title),
        description: getBackupConflictDescription(error, errorPresentation.description),
        color: error instanceof ApiError && error.status === 409 ? 'warning' : errorPresentation.color,
      })
    },
    onSettled: (_result, _error, request) => {
      clearActionAbortController(batchRestorePreviewAbortControllerRef, request.signal)
    },
  })

  const batchRestoreMutation = useMutation({
    mutationFn: (request: BatchRestoreMutationRequest) => runBatchBackupRestore(request.items, { signal: request.signal }),
    onSuccess: (result, request) => {
      if (request.signal.aborted) {
        return
      }
      void queryClient.invalidateQueries({ queryKey: backupJobsQueryKey })
      setBatchRestoreResult(result)
      const batchWarnings = result.warnings ?? []
      const batchRestoreDescription = batchWarnings[0]
        ? getBackupDiagnosticDisplayMessage(batchWarnings[0])
        : result.error_message
          ? getBackupDiagnosticDisplayMessage(result.error_message)
          : getBatchRestoreMetricText(result).replace(' · ', '，')
      addToast({
        title: result.status === 'failed' ? '批量恢复失败' : result.warning ? '批量恢复完成，有警告' : '批量恢复已完成',
        description: batchRestoreDescription,
        color: result.status === 'failed' ? 'danger' : result.warning ? 'warning' : 'success',
      })
    },
    onError: (error: unknown, request) => {
      if (request.signal.aborted || isAbortError(error)) {
        return
      }
      const errorPresentation = getMaintenanceActionErrorPresentation(
        error,
        '执行批量恢复失败',
        '批量恢复暂不可用',
        '备份管理器当前不可用，请检查配置后重试。',
      )
      addToast({
        title: getBackupConflictTitle(error, errorPresentation.title),
        description: getBackupConflictDescription(error, errorPresentation.description),
        color: error instanceof ApiError && error.status === 409 ? 'warning' : errorPresentation.color,
      })
    },
    onSettled: (_result, _error, request) => {
      clearActionAbortController(batchRestoreAbortControllerRef, request.signal)
    },
  })
  
  // Handle export
  const handleExport = async () => {
    diagnosticsExportAbortControllerRef.current?.abort()
    const controller = new AbortController()
    diagnosticsExportAbortControllerRef.current = controller
    setIsExporting(true)
    try {
      await downloadDiagnosticsExport({ signal: controller.signal })
      addToast({ title: '诊断信息导出已开始', color: 'success' })
    } catch (error) {
      if (controller.signal.aborted) {
        return
      }
      const errorPresentation = getMaintenanceActionErrorPresentation(
        error,
        '下载诊断包失败',
        '诊断包暂不可用',
        '诊断包服务当前不可用，请检查设备状态后重试。',
      )
      addToast({
        title: errorPresentation.title,
        description: errorPresentation.description,
        color: errorPresentation.color,
      })
    } finally {
      if (diagnosticsExportAbortControllerRef.current === controller) {
        diagnosticsExportAbortControllerRef.current = null
        setIsExporting(false)
      }
    }
  }

  const handleDownloadRestoreReport = async (job: BackupJob) => {
    restoreReportExportAbortControllerRef.current?.abort()
    const controller = new AbortController()
    restoreReportExportAbortControllerRef.current = controller
    setExportingRestoreReportJobId(job.id)
    try {
      await downloadBackupRestoreReport(job.id, { signal: controller.signal })
      addToast({ title: '恢复摘要导出已开始', description: job.name, color: 'success' })
    } catch (error) {
      if (controller.signal.aborted) {
        return
      }
      const errorPresentation = getMaintenanceActionErrorPresentation(
        error,
        '导出恢复摘要失败',
        '恢复摘要暂不可用',
        '备份管理器当前不可用，请检查配置后重试。',
      )
      addToast({
        title: getBackupConflictTitle(error, errorPresentation.title),
        description: getBackupConflictDescription(error, errorPresentation.description),
        color: error instanceof ApiError && error.status === 409 ? 'warning' : errorPresentation.color,
      })
    } finally {
      if (restoreReportExportAbortControllerRef.current === controller) {
        restoreReportExportAbortControllerRef.current = null
        setExportingRestoreReportJobId(null)
      }
    }
  }

  const startScrub = () => {
    const controller = createActionAbortController(scrubAbortControllerRef)
    scrubMutation.mutate({ signal: controller.signal })
  }

  const startBackupRun = (jobId: string) => {
    const controller = createActionAbortController(runBackupAbortControllerRef)
    runBackupMutation.mutate({ jobId, signal: controller.signal })
  }

  const startRetentionCheck = (jobId: string) => {
    const controller = createActionAbortController(retentionCheckAbortControllerRef)
    retentionCheckMutation.mutate({ jobId, signal: controller.signal })
  }

  const startRestoreDrill = (jobId: string) => {
    const controller = createActionAbortController(restoreDrillAbortControllerRef)
    restoreDrillMutation.mutate({ jobId, signal: controller.signal })
  }

  const startRestorePreview = () => {
    if (!restoreJob) return
    const targetInputError = getRestoreTargetInputError(restoreTargetPath)
    if (targetInputError) {
      addToast({
        title: '恢复目标格式无效',
        description: targetInputError,
        color: 'danger',
      })
      return
    }
    const targetPath = normalizeRestoreTargetForRequest(restoreTargetPath)
    if (!targetPath) return
    const controller = createActionAbortController(restorePreviewAbortControllerRef)
    restorePreviewMutation.mutate({
      jobId: restoreJob.id,
      targetPath,
      includeConfig: restoreIncludeConfigForRequest,
      signal: controller.signal,
    })
  }

  const startRestore = () => {
    if (!restoreJob) return
    const targetInputError = getRestoreTargetInputError(restoreTargetPath)
    if (targetInputError) {
      addToast({
        title: '恢复目标格式无效',
        description: targetInputError,
        color: 'danger',
      })
      return
    }
    const targetPath = normalizeRestoreTargetForRequest(restoreTargetPath)
    if (!targetPath) return
    const controller = createActionAbortController(restoreAbortControllerRef)
    restoreMutation.mutate({
      jobId: restoreJob.id,
      targetPath,
      includeConfig: restoreIncludeConfigForRequest,
      signal: controller.signal,
    })
  }

  const startBatchRestorePreview = () => {
    const targetInputError = getBatchRestoreTargetInputError(batchRestoreItems)
    if (targetInputError) {
      addToast({
        title: '批量恢复目标格式无效',
        description: targetInputError,
        color: 'danger',
      })
      return
    }
    if (batchRestoreTargetConflict) {
      addToast({
        title: '批量恢复目标冲突',
        description: batchRestoreTargetConflict,
        color: 'danger',
      })
      return
    }
    const controller = createActionAbortController(batchRestorePreviewAbortControllerRef)
    batchRestorePreviewMutation.mutate({ items: normalizeBatchRestoreItemsForRequest(batchRestoreItems), signal: controller.signal })
  }

  const startBatchRestore = () => {
    const targetInputError = getBatchRestoreTargetInputError(batchRestoreItems)
    if (targetInputError) {
      addToast({
        title: '批量恢复目标格式无效',
        description: targetInputError,
        color: 'danger',
      })
      return
    }
    if (batchRestoreTargetConflict) {
      addToast({
        title: '批量恢复目标冲突',
        description: batchRestoreTargetConflict,
        color: 'danger',
      })
      return
    }
    const controller = createActionAbortController(batchRestoreAbortControllerRef)
    batchRestoreMutation.mutate({ items: normalizeBatchRestoreItemsForRequest(batchRestoreItems), signal: controller.signal })
  }
  
  const isRunning = scrubResult?.status === 'running' || isAwaitingRunningState
  const runningScrubProgressText = getRunningScrubProgressText(scrubResult)
  const restoreIncludeConfigForRequest = effectiveRestoreIncludeConfig(restoreJob, restoreIncludeConfig)
  const restoreTargetInputError = getRestoreTargetInputError(restoreTargetPath)
  const restoreTargetReady = restoreTargetPath.trim() !== '' && !restoreTargetInputError
  const restorePreviewMatches = isCurrentRestorePreview(restorePreview, restorePreviewRequest, restoreJob, restoreTargetPath, restoreIncludeConfig)
  const restorePreviewHasFailedPreflight = hasFailedRestorePreflight(restorePreview)
  const restoreActionPending = restoreMutation.isPending || restorePreviewMutation.isPending || restoreVerifyMutation.isPending
  const restorableBackupJobs = backupJobs.filter((job) => !job.disabled && canRunBackupRestore(job))
  const batchRestoreAttentionJobs = restorableBackupJobs.filter(backupJobNeedsAttention)
  const batchRestoreItems = buildBatchRestoreItems(backupJobs, batchRestoreSelectedJobIds, batchRestoreTargets, batchRestoreIncludeConfig)
  const batchRestoreWithinLimit = batchRestoreItems.length <= batchRestoreItemLimit
  const batchRestoreTargetInputError = getBatchRestoreTargetInputError(batchRestoreItems)
  const batchRestoreTargetConflict = getBatchRestoreTargetConflict(batchRestoreItems)
  const batchRestoreTargetsReady = batchRestoreItems.length > 0 && batchRestoreWithinLimit && !batchRestoreTargetInputError && !batchRestoreTargetConflict && batchRestoreItems.every((item) => item.target_path.length > 0)
  const batchRestorePreviewMatches = isCurrentBatchRestorePreview(batchRestorePreview, batchRestorePreviewItems, batchRestoreItems)
  const batchRestorePreviewHasFailed = hasFailedBatchRestorePreview(batchRestorePreview)
  const batchRestoreActionPending = batchRestorePreviewMutation.isPending || batchRestoreMutation.isPending
  
  return (
    <div className="h-full overflow-auto custom-scrollbar">
      <div className="space-y-6 p-4 sm:p-6">
      <PageHeader
        title="备份与维护"
        subtitle="检查数据完整性，执行备份和恢复演练"
        icon={ShieldCheck}
        actions={
          <Button
            className="btn-secondary rounded-lg"
            startContent={<Download size={18} />}
            isLoading={isExporting}
            onPress={handleExport}
          >
            下载诊断包
          </Button>
        }
      />
      
      {/* Scrub Card */}
      <Card className="card-mnemonas">
        <CardHeader className="flex flex-col items-start gap-3 pb-2 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-accent-primary/15">
              <ShieldCheck size={20} className="text-accent-primary" />
            </div>
            <div>
              <h3 className="font-semibold">数据完整性校验</h3>
              <p className="text-xs text-default-500">检查已存数据是否仍可正确读取</p>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {scrubResult && <StatusChip status={scrubResult.status} warning={scrubResult.warning} />}
            <Button
              className="btn-primary rounded-lg shadow-md"
              startContent={isRunning ? <RefreshCw size={18} className="animate-spin" /> : <Play size={18} />}
              isLoading={scrubMutation.isPending}
              isDisabled={isRunning}
              onPress={startScrub}
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
                    <span>任务 ID：{scrubResult.id}</span>
                  </div>
                )}
                {scrubResult.start_time && (
                  <div className="flex items-center gap-1">
                    <Clock size={14} />
                    <span>开始：{new Date(scrubResult.start_time).toLocaleString('zh-CN')}</span>
                  </div>
                )}
                {scrubResult.duration_ms !== undefined && scrubResult.status !== 'running' && (
                  <div className="flex items-center gap-1">
                    <Clock size={14} />
                    <span>耗时：{formatDuration(scrubResult.duration_ms)}</span>
                  </div>
                )}
                {scrubResult.total_size !== undefined && (
                  <div className="flex items-center gap-1">
                    <Database size={14} />
                    <span>数据量：{formatBytes(scrubResult.total_size)}</span>
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
                    aria-valuetext={runningScrubProgressText}
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
                        <p className="text-sm text-warning mt-1">{getScrubResultDisplayMessage(scrubResult.message)}</p>
                      )}
                    </div>
                  </div>
                </div>
              )}
              
              {/* Error message */}
              {scrubResult.error_message && (
                <div className="mt-4 p-3 bg-danger/10 rounded-lg border border-danger/20">
                  <p className="text-sm text-danger">{getScrubResultDisplayMessage(scrubResult.error_message)}</p>
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
      <Card className="card-mnemonas">
        <CardHeader className="flex flex-col items-start gap-3 pb-2 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-accent-primary/15">
              <Archive size={20} className="text-accent-primary" />
            </div>
            <div>
              <h3 className="font-semibold">备份任务与恢复演练</h3>
              <p className="text-xs text-default-500">执行外置盘或远端备份，并定期确认能恢复</p>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="bordered"
              className="rounded-lg"
              startContent={<ListChecks size={18} />}
              isDisabled={restorableBackupJobs.length === 0}
              onPress={openBatchRestoreModal}
            >
              批量恢复
            </Button>
            <Button
              variant="bordered"
              className="rounded-lg"
              startContent={<RefreshCw size={18} />}
              onPress={handleRefreshBackups}
            >
              刷新任务
            </Button>
          </div>
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
                title={backupLoadErrorPresentation.title}
                description={backupLoadErrorPresentation.description}
                action={
                  <Button variant="bordered" className="rounded-lg" onPress={handleRefreshBackups}>
                    重新加载
                  </Button>
                }
              />
            </div>
          ) : backupJobs.length === 0 ? (
            <div className="grid grid-cols-1 gap-4 py-2 lg:grid-cols-[minmax(0,0.95fr)_minmax(0,1.05fr)]">
              <div className="flex flex-col justify-center rounded-lg border border-dashed border-divider bg-content2/35 p-6 text-default-500">
                <HardDrive size={42} className="mb-4 opacity-40" />
                <p className="text-base font-medium text-foreground">尚未配置备份任务</p>
                <p className="mt-2 text-sm leading-6">建议先配置一个独立外置盘或远端目标。配置后重启服务，再运行一次备份和恢复演练。</p>
              </div>
              <div className="rounded-lg border border-divider bg-content2/45 p-4">
                <div className="mb-3 flex items-center gap-2 text-sm font-medium text-foreground">
                  <Archive size={16} className="text-accent-primary" />
                  外置盘本地备份示例
                </div>
                <pre className="max-h-72 overflow-y-auto overflow-x-hidden whitespace-pre-wrap break-words rounded-lg bg-content1 p-3 text-left text-xs leading-5 text-default-700">
                  <code>{backupStarterConfigSnippet}</code>
                </pre>
                <p className="mt-3 text-xs leading-5 text-default-500">
                  把目标目录换成独立磁盘挂载点；不要把备份目标放在 storage.root 内。
                </p>
              </div>
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
                  <TableColumn>最近恢复</TableColumn>
                  <TableColumn>操作</TableColumn>
                </TableHeader>
                <TableBody>
                  {backupJobs.map((job: BackupJob) => {
                    const isRunningBackup = runBackupMutation.isPending && runBackupMutation.variables?.jobId === job.id
                    const isCheckingRetention = retentionCheckMutation.isPending && retentionCheckMutation.variables?.jobId === job.id
                    const isRunningDrill = restoreDrillMutation.isPending && restoreDrillMutation.variables?.jobId === job.id
                    const isRunningRestore = restoreMutation.isPending && restoreMutation.variables?.jobId === job.id
                    const isRunningRestoreVerify = restoreVerifyMutation.isPending && restoreVerifyMutation.variables?.jobId === job.id
                    const isExportingReport = exportingRestoreReportJobId === job.id
                    const isBusy = job.running || isRunningBackup || isCheckingRetention || isRunningDrill || isRunningRestore || isRunningRestoreVerify
                    const isFocusedBackupJob = focusedBackupJobID === job.id
                    return (
                      <TableRow key={job.id}>
                        <TableCell>
                          <div
                            id={getBackupJobFocusElementId(job.id)}
                            role="group"
                            aria-label={`${job.name} 备份任务${isFocusedBackupJob ? '，安全自检定位' : ''}`}
                            className={cn(
                              '-m-2 space-y-1 rounded-lg border border-transparent p-2',
                              isFocusedBackupJob && 'border-warning/60 bg-warning/10 ring-2 ring-warning/30',
                            )}
                            data-backup-job-id={job.id}
                            data-focused-backup-job={isFocusedBackupJob ? 'true' : undefined}
                          >
                            <div className="flex items-center gap-2">
                              <span className="font-medium">{job.name}</span>
                              <BackupHealthChip status={job.health_status} />
                              {job.running && <BackupStatusChip status="running" />}
                              {isFocusedBackupJob && (
                                <Chip size="sm" color="warning" variant="flat">
                                  安全自检定位
                                </Chip>
                              )}
                            </div>
                            <div className="text-xs text-default-500">
                              {job.id} · {job.type}
                            </div>
                            <div className="max-w-[22rem] truncate text-xs text-default-400" title={job.source}>
                              来源：{job.source}
                            </div>
                            <BackupAttentionSummary job={job} />
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
                              命令：{job.command}
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
                              {job.next_run_at ? `下次运行：${formatDateTime(job.next_run_at)}` : '不会自动运行'}
                            </div>
                            {getBackupScheduleWindowText(job) && (
                              <div className="text-xs text-default-500">
                                {getBackupScheduleWindowText(job)}
                              </div>
                            )}
                            <div className="flex items-center gap-2 text-xs text-default-500">
                              <BackupPolicyChip status={job.retention_status} />
                              <span>{getBackupRetentionText(job)}</span>
                            </div>
                            {job.retention_policy && (
                              <div className="max-w-[18rem] truncate text-xs text-default-400" title={job.retention_policy}>
                                策略：{job.retention_policy}
                              </div>
                            )}
                            {job.last_retention_check && (
                              <div className={job.last_retention_check.warning || job.last_retention_check.status === 'failed' ? 'text-xs text-warning' : 'text-xs text-default-400'}>
                                最近检测：{getBackupRetentionCheckMetricText(job.last_retention_check)}
                                {getBackupRetentionCheckTime(job.last_retention_check) && ` · ${getBackupRetentionCheckTime(job.last_retention_check)}`}
                              </div>
                            )}
                            {job.health_message && (
                              <div className={job.health_status === 'failed' ? 'text-xs text-danger' : 'text-xs text-default-400'}>
                                {getBackupDiagnosticDisplayMessage(job.health_message)}
                              </div>
                            )}
                          </div>
                        </TableCell>
                        <TableCell><BackupRunSummary result={job.last_run} /></TableCell>
                        <TableCell><BackupDrillSummary job={job} /></TableCell>
                        <TableCell><BackupRestoreSummary job={job} /></TableCell>
                        <TableCell>
                          <div className="flex flex-wrap gap-2">
                            <Button
                              size="sm"
                              className="rounded-lg"
                              color="primary"
                              startContent={isRunningBackup ? <RefreshCw size={16} className="animate-spin" /> : <Archive size={16} />}
                              isLoading={isRunningBackup}
                              isDisabled={isBusy || job.disabled}
                              onPress={() => startBackupRun(job.id)}
                            >
                              立即备份
                            </Button>
                            <Button
                              size="sm"
                              variant="bordered"
                              className="rounded-lg"
                              startContent={isCheckingRetention ? <RefreshCw size={16} className="animate-spin" /> : <FileWarning size={16} />}
                              isLoading={isCheckingRetention}
                              isDisabled={isBusy || job.disabled}
                              onPress={() => startRetentionCheck(job.id)}
                            >
                              检查保留
                            </Button>
                            <Button
                              size="sm"
                              variant="bordered"
                              className="rounded-lg"
                              startContent={isRunningDrill ? <RefreshCw size={16} className="animate-spin" /> : <RotateCcw size={16} />}
                              isLoading={isRunningDrill}
                              isDisabled={isBusy || job.disabled || !canRunBackupRestoreDrill(job)}
                              onPress={() => startRestoreDrill(job.id)}
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
                            <Button
                              size="sm"
                              variant="bordered"
                              className="rounded-lg"
                              startContent={isRunningRestoreVerify ? <RefreshCw size={16} className="animate-spin" /> : <CheckCircle size={16} />}
                              isLoading={isRunningRestoreVerify}
                              isDisabled={(isBusy && !isRunningRestoreVerify) || job.disabled || !canVerifyLatestBackupRestore(job)}
                              onPress={() => {
                                if (!job.last_restore) return
                                startRestoreVerify(job.id, job.last_restore.target_path)
                              }}
                            >
                              检查恢复
                            </Button>
                            <Button
                              size="sm"
                              variant="bordered"
                              className="rounded-lg"
                              startContent={isExportingReport ? <RefreshCw size={16} className="animate-spin" /> : <Download size={16} />}
                              isLoading={isExportingReport}
                              isDisabled={isExportingReport}
                              onPress={() => void handleDownloadRestoreReport(job)}
                            >
                              导出摘要
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
      <Card className="card-mnemonas">
        <CardBody className="text-sm text-default-600">
          <h4 className="font-medium mb-2">维护建议</h4>
          <ul className="list-disc list-inside space-y-1">
            <li>校验会读取每个存储块并重新计算 BLAKE3 哈希值</li>
            <li>对比计算的哈希与存储的哈希来检测数据损坏</li>
            <li>大量数据时校验可能需要较长时间</li>
            <li>本地备份任务应写入 storage.root 之外的磁盘、挂载点或快照目标</li>
            <li>restic/rclone 任务会调用外部工具执行备份与校验</li>
            <li>本地恢复演练会复制最近快照并通过 manifest 校验</li>
            <li>restic/rclone 恢复会先写入独立目录；rclone 会在安装前执行远端一致性校验，恢复成功后会执行只读校验</li>
            <li>批量恢复应先生成预览，确认每个目标目录互不重叠后再执行</li>
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
              <RestoreFlowGuide
                targetPath={restoreTargetPath}
                targetReady={restoreTargetReady}
                targetError={restoreTargetInputError}
                preview={restorePreview}
                previewMatches={restorePreviewMatches}
                isPreviewing={restorePreviewMutation.isPending}
                isRestoring={restoreMutation.isPending}
                result={restoreResult}
                verifyResult={restoreVerifyResult}
                isVerifying={restoreVerifyMutation.isPending}
              />
              {restoreResult ? (
                <RestoreCutoverChecklist
                  result={restoreResult}
                  verifyResult={restoreVerifyResult}
                  isVerifying={restoreVerifyMutation.isPending}
                />
              ) : (
                <>
                  {restoreJob && (
                    <div className="rounded-lg border border-divider bg-content2/50 p-4 text-sm">
                      <div className="font-medium">{restoreJob.name}</div>
                      <div className="mt-1 text-default-500">{restoreJob.id} · {restoreJob.type}</div>
                      <div className="mt-1 truncate text-default-400" title={restoreJob.destination}>
                        备份目标：{restoreJob.destination}
                      </div>
                    </div>
                  )}
                  <Input
                    label="目标目录"
                    placeholder="/mnt/restore/mnemonas"
                    value={restoreTargetPath}
                    onValueChange={handleRestoreTargetPathChange}
                    isDisabled={restoreActionPending}
                    description={getRestoreTargetDescription(restoreJob)}
                    isInvalid={Boolean(restoreTargetInputError)}
                    errorMessage={restoreTargetInputError ?? undefined}
                  />
                  {restoreJob && restoreTargetPath === getSuggestedRestoreTargetPath(restoreJob) && (
                    <div className="rounded-lg border border-primary/20 bg-primary/5 p-3 text-xs leading-5 text-default-500">
                      已填入建议目录，可按实际挂载点修改；执行前仍需生成预览并通过恢复预检。
                    </div>
                  )}
                  {restoreJob?.type === 'local' && (
                    <Checkbox
                      isSelected={restoreIncludeConfig}
                      onValueChange={handleRestoreIncludeConfigChange}
                      isDisabled={restoreActionPending}
                    >
                      同时恢复备份中的配置文件
                    </Checkbox>
                  )}
                  <div className="flex items-start gap-2 rounded-lg border border-warning/20 bg-warning/10 p-3 text-sm text-warning">
                    <AlertCircle size={16} className="mt-0.5 shrink-0" />
                    <span>恢复不会覆盖当前数据目录。请先恢复到独立目录，人工确认后再切换服务配置或迁移数据。</span>
                  </div>
                  {restorePreview && (
                    <div className={getRestorePreviewPanelClass(restorePreview, restorePreviewMatches)}>
                      <div className="flex items-center justify-between gap-3">
                        <div className="font-medium">{getRestorePreviewTitle(restorePreview, restorePreviewMatches)}</div>
                        <BackupStatusChip
                          status={hasFailedRestorePreflight(restorePreview) ? 'failed' : restorePreview.status}
                          warning={hasWarningRestorePreflight(restorePreview)}
                        />
                      </div>
                      <div className="mt-2 text-default-600">{getBackupRestorePreviewMetricText(restorePreview)}</div>
                      <div className="mt-1 truncate text-default-500" title={restorePreview.target_path}>
                        目标：{restorePreview.target_path}
                      </div>
                      {restorePreview.config_available && (
                        <div className="mt-1 text-default-500">
                          配置文件：{restorePreview.config_included ? '将恢复到 .mnemonas-restore/config.toml' : '本次不恢复'}
                        </div>
                      )}
                      <RestoreImpactSummary result={restorePreview} matches={restorePreviewMatches} />
                      <RestoreExecutionReview result={restorePreview} matches={restorePreviewMatches} />
                      <RestorePreflightList checks={restorePreview.preflight_checks} />
                      {restorePreview.warnings && restorePreview.warnings.length > 0 && (
                        <div className="mt-3 rounded-lg border border-warning/20 bg-warning/10 p-3 text-xs text-warning">
                          {getBackupDiagnosticDisplayMessage(restorePreview.warnings[0])}
                        </div>
                      )}
                      {restorePreview.sample_paths && restorePreview.sample_paths.length > 0 && (
                        <div className="mt-3 space-y-1">
                          <div className="text-xs font-medium text-default-500">样例文件</div>
                          <div className="space-y-1">
                            {restorePreview.sample_paths.slice(0, 5).map((sample) => (
                              <div key={sample} className="truncate rounded-md bg-content1 px-2 py-1 font-mono text-xs text-default-600" title={sample}>
                                {sample}
                              </div>
                            ))}
                          </div>
                        </div>
                      )}
                      {!restorePreviewMatches && (
                        <div className="mt-3 text-xs text-warning">目标目录或配置选项已变更，请重新生成预览。</div>
                      )}
                      {restorePreviewMatches && restorePreviewHasFailedPreflight && (
                        <div className="mt-3 text-xs text-danger">预检未通过，需处理失败项后重新生成预览。</div>
                      )}
                    </div>
                  )}
                </>
              )}
            </div>
          </ModalBody>
          <ModalFooter>
            {restoreResult ? (
              <>
                <Button variant="light" className="rounded-lg" onPress={closeRestoreModal} isDisabled={restoreVerifyMutation.isPending}>
                  关闭
                </Button>
                <Button
                  variant="bordered"
                  className="rounded-lg"
                  startContent={exportingRestoreReportJobId === restoreJob?.id ? <RefreshCw size={16} className="animate-spin" /> : <Download size={16} />}
                  isLoading={exportingRestoreReportJobId === restoreJob?.id}
                  isDisabled={!restoreJob || exportingRestoreReportJobId === restoreJob.id}
                  onPress={() => {
                    if (!restoreJob) return
                    void handleDownloadRestoreReport(restoreJob)
                  }}
                >
                  导出摘要
                </Button>
                <Button
                  color="primary"
                  className="rounded-lg"
                  isLoading={restoreVerifyMutation.isPending}
                  isDisabled={!restoreJob || !restoreTargetReady}
                  onPress={() => {
                    if (!restoreJob || !restoreResult) return
                    startRestoreVerify(restoreJob.id, restoreTargetPath.trim())
                  }}
                >
                  重新检查
                </Button>
              </>
            ) : (
              <>
                <Button variant="light" className="rounded-lg" onPress={closeRestoreModal} isDisabled={restoreActionPending}>
                  取消
                </Button>
                <Button
                  variant="bordered"
                  className="rounded-lg"
                  isLoading={restorePreviewMutation.isPending}
                  isDisabled={!restoreJob || !restoreTargetReady || restoreMutation.isPending}
                  onPress={startRestorePreview}
                >
                  生成预览
                </Button>
                <Button
                  color="warning"
                  className="rounded-lg"
                  isLoading={restoreMutation.isPending}
                  isDisabled={!restoreJob || !restoreTargetReady || !restorePreviewMatches || restorePreviewHasFailedPreflight || restorePreviewMutation.isPending}
                  onPress={startRestore}
                >
                  开始恢复
                </Button>
              </>
            )}
          </ModalFooter>
        </ModalContent>
      </Modal>
      <Modal
        isOpen={isBatchRestoreOpen}
        onClose={closeBatchRestoreModal}
        size="5xl"
        scrollBehavior="inside"
        classNames={{ base: 'bg-content1 border border-divider' }}
      >
        <ModalContent>
          <ModalHeader className="flex flex-col gap-1">
            <div className="flex items-center gap-3">
              <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-warning/10">
                <ListChecks size={20} className="text-warning" />
              </div>
              <div>
                <div>批量恢复到独立目录</div>
                <div className="mt-1 text-xs font-normal text-default-500">
                  已选 {batchRestoreItems.length} 项，最多 {batchRestoreItemLimit} 项
                </div>
              </div>
            </div>
          </ModalHeader>
          <ModalBody>
            <div className="space-y-4">
              <BatchRestoreFlowGuide
                items={batchRestoreItems}
                withinLimit={batchRestoreWithinLimit}
                targetInputError={batchRestoreTargetInputError}
                targetConflict={batchRestoreTargetConflict}
                targetsReady={batchRestoreTargetsReady}
                preview={batchRestorePreview}
                previewMatches={batchRestorePreviewMatches}
                previewHasFailed={batchRestorePreviewHasFailed}
                isPreviewing={batchRestorePreviewMutation.isPending}
                isRestoring={batchRestoreMutation.isPending}
                result={batchRestoreResult}
              />
              {batchRestoreResult ? (
                <BatchRestoreResultSummary result={batchRestoreResult} jobs={backupJobs} />
              ) : (
                <>
                <div className="flex items-start gap-2 rounded-lg border border-warning/20 bg-warning/10 p-3 text-sm text-warning">
                  <AlertCircle size={16} className="mt-0.5 shrink-0" />
                  <span>批量恢复会按顺序写入多个独立目录，不会覆盖当前数据目录。请先生成预览并检查每个目标目录。</span>
                </div>
                <BatchRestoreReadinessSummary
                  items={batchRestoreItems}
                  withinLimit={batchRestoreWithinLimit}
                  targetInputError={batchRestoreTargetInputError}
                  targetConflict={batchRestoreTargetConflict}
                  preview={batchRestorePreview}
                  previewMatches={batchRestorePreviewMatches}
                  previewHasFailed={batchRestorePreviewHasFailed}
                />
                <div className="flex flex-col gap-3 rounded-lg border border-divider bg-content2/50 p-3 text-sm sm:flex-row sm:items-center sm:justify-between">
                  <div>
                    <div className="font-medium text-foreground">批量选择</div>
                    <div className="mt-1 text-xs text-default-500">
                      可恢复任务 {restorableBackupJobs.length} 项，待处理 {batchRestoreAttentionJobs.length} 项；选择后会保留已填写目标，空目标使用建议目录。
                    </div>
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <Button
                      size="sm"
                      variant="bordered"
                      className="rounded-lg"
                      startContent={<AlertCircle size={16} />}
                      isDisabled={batchRestoreActionPending || batchRestoreAttentionJobs.length === 0}
                      onPress={selectAttentionBatchRestoreJobs}
                    >
                      {batchRestoreAttentionJobs.length > batchRestoreItemLimit ? `选择待处理前 ${batchRestoreItemLimit} 项` : '选择待处理'}
                    </Button>
                    <Button
                      size="sm"
                      variant="bordered"
                      className="rounded-lg"
                      startContent={<ListChecks size={16} />}
                      isDisabled={batchRestoreActionPending || restorableBackupJobs.length === 0}
                      onPress={selectAllBatchRestoreJobs}
                    >
                      {restorableBackupJobs.length > batchRestoreItemLimit ? `选择前 ${batchRestoreItemLimit} 项` : '选择全部'}
                    </Button>
                    <Button
                      size="sm"
                      variant="light"
                      className="rounded-lg"
                      isDisabled={batchRestoreActionPending || batchRestoreSelectedJobIds.length === 0}
                      onPress={clearBatchRestoreSelection}
                    >
                      清空选择
                    </Button>
                  </div>
                </div>
                {restorableBackupJobs.length === 0 ? (
                  <EmptyState
                    icon={HardDrive}
                    title="没有可恢复的备份任务"
                    description="本地任务需要至少一次成功备份；远端任务需要未停用且配置完整。"
                  />
                ) : (
                  <div className="space-y-3">
                    {restorableBackupJobs.map((job) => {
                      const selected = batchRestoreSelectedJobIds.includes(job.id)
                      const targetInputError = selected ? getRestoreTargetInputError(batchRestoreTargets[job.id] ?? '') : null
                      return (
                        <div key={job.id} className={selected ? 'rounded-lg border border-primary/30 bg-primary/5 p-4' : 'rounded-lg border border-divider bg-content2/50 p-4'}>
                          <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(18rem,1.4fr)] lg:items-start">
                            <div className="min-w-0 space-y-2">
                              <Checkbox
                                aria-label={`选择 ${job.name}`}
                                isSelected={selected}
                                onValueChange={(value) => handleBatchRestoreSelectedChange(job.id, value)}
                                isDisabled={batchRestoreActionPending}
                              >
                                <span className="font-medium">{job.name}</span>
                              </Checkbox>
                              <div className="ml-7 space-y-1 text-xs text-default-500">
                                <div>{job.id} · {job.type}</div>
                                <div className="truncate" title={job.destination}>备份目标：{job.destination}</div>
                                <div className="truncate" title={job.source}>来源：{job.source}</div>
                              </div>
                            </div>
                            <div className="space-y-3">
                              <Input
                                label={`${job.name} 目标目录`}
                                placeholder={`/mnt/restore/${job.id}`}
                                value={batchRestoreTargets[job.id] ?? ''}
                                onValueChange={(value) => handleBatchRestoreTargetChange(job.id, value)}
                                isDisabled={!selected || batchRestoreActionPending}
                                description={selected ? getRestoreTargetDescription(job) : '选择该任务后可使用建议目标目录，或改成自定义独立目录。'}
                                isInvalid={Boolean(targetInputError)}
                                errorMessage={targetInputError ?? undefined}
                              />
                              {job.type === 'local' && (
                                <Checkbox
                                  isSelected={batchRestoreIncludeConfig[job.id] ?? false}
                                  onValueChange={(value) => handleBatchRestoreIncludeConfigChange(job.id, value)}
                                  isDisabled={!selected || batchRestoreActionPending}
                                >
                                  同时恢复配置文件
                                </Checkbox>
                              )}
                            </div>
                          </div>
                        </div>
                      )
                    })}
                  </div>
                )}
                {!batchRestoreWithinLimit && (
                  <div className="rounded-lg border border-danger/20 bg-danger/10 p-3 text-sm text-danger">
                    一次最多恢复 {batchRestoreItemLimit} 项，请减少选择后重新生成预览。
                  </div>
                )}
                {batchRestoreTargetConflict && (
                  <div className="rounded-lg border border-danger/20 bg-danger/10 p-3 text-sm text-danger">
                    {batchRestoreTargetConflict}
                  </div>
                )}
                {batchRestoreTargetInputError && (
                  <div className="rounded-lg border border-danger/20 bg-danger/10 p-3 text-sm text-danger">
                    {batchRestoreTargetInputError}
                  </div>
                )}
                {batchRestorePreview && (
                  <>
                    <BatchRestorePreviewSummary result={batchRestorePreview} />
                    <BatchRestoreImpactSummary result={batchRestorePreview} matches={batchRestorePreviewMatches} />
                    <BatchRestoreExecutionReview result={batchRestorePreview} matches={batchRestorePreviewMatches} />
                    {!batchRestorePreviewMatches && (
                      <div className="rounded-lg border border-warning/20 bg-warning/10 p-3 text-sm text-warning">
                        选中的任务或目标目录已变更，请重新生成批量预览。
                      </div>
                    )}
                    {batchRestorePreviewMatches && batchRestorePreviewHasFailed && (
                      <div className="rounded-lg border border-danger/20 bg-danger/10 p-3 text-sm text-danger">
                        存在未通过预检的条目，处理失败项后重新生成预览。
                      </div>
                    )}
                  </>
                )}
                </>
              )}
            </div>
          </ModalBody>
          <ModalFooter>
            {batchRestoreResult ? (
              <Button variant="light" className="rounded-lg" onPress={closeBatchRestoreModal}>
                关闭
              </Button>
            ) : (
              <>
                <Button variant="light" className="rounded-lg" onPress={closeBatchRestoreModal} isDisabled={batchRestoreActionPending}>
                  取消
                </Button>
                <Button
                  variant="bordered"
                  className="rounded-lg"
                  isLoading={batchRestorePreviewMutation.isPending}
                  isDisabled={!batchRestoreTargetsReady || batchRestoreMutation.isPending}
                  onPress={startBatchRestorePreview}
                >
                  生成批量预览
                </Button>
                <Button
                  color="warning"
                  className="rounded-lg"
                  isLoading={batchRestoreMutation.isPending}
                  isDisabled={!batchRestoreTargetsReady || !batchRestorePreviewMatches || batchRestorePreviewHasFailed || batchRestorePreviewMutation.isPending}
                  onPress={startBatchRestore}
                >
                  开始批量恢复
                </Button>
              </>
            )}
          </ModalFooter>
        </ModalContent>
      </Modal>
      </div>
    </div>
  )
}
