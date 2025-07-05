import type { BackupJob } from '@/api/files'

export function getBackupAttentionReasons(job: BackupJob): string[] {
  const latestVerify = job.last_matching_restore_verify
  const reasons: string[] = []

  if (job.health_status === 'failed') {
    reasons.push('备份健康异常')
  } else if (job.health_status === 'stale') {
    reasons.push('备份过期')
  }

  if (job.last_run?.status === 'failed') {
    reasons.push('最近备份失败')
  } else if (job.last_run?.warning || (job.last_run?.warnings?.length ?? 0) > 0) {
    reasons.push('最近备份有警告')
  }

  if (job.retention_status === 'failed') {
    reasons.push('保留策略失败')
  } else if (job.retention_status === 'warning') {
    reasons.push('保留策略需确认')
  }

  if (job.restore_drill_status === 'failed') {
    reasons.push('恢复演练失败')
  } else if (job.restore_drill_status === 'stale') {
    reasons.push('恢复演练过期')
  } else if (job.restore_drill_status === 'due') {
    reasons.push('恢复演练待执行')
  }

  if (job.last_restore?.status === 'failed') {
    reasons.push('最近恢复失败')
  } else if ((job.last_restore?.warnings?.length ?? 0) > 0) {
    reasons.push('最近恢复有警告')
  } else if (job.last_restore?.status === 'completed' && !latestVerify) {
    reasons.push('恢复待校验')
  }

  if (latestVerify?.status === 'failed') {
    reasons.push('恢复检查失败')
  } else if ((latestVerify?.warnings?.length ?? 0) > 0) {
    reasons.push('恢复检查有警告')
  }

  return reasons
}

function addUniqueStep(steps: string[], step: string): void {
  if (!steps.includes(step)) {
    steps.push(step)
  }
}

export function getBackupAttentionNextSteps(job: BackupJob): string[] {
  const latestVerify = job.last_matching_restore_verify
  const steps: string[] = []

  if (
    job.health_status === 'failed'
    || job.health_status === 'stale'
    || job.last_run?.status === 'failed'
    || job.last_run?.warning
    || (job.last_run?.warnings?.length ?? 0) > 0
  ) {
    addUniqueStep(steps, '运行立即备份并查看最近备份结果')
  }

  if (job.retention_status === 'failed' || job.retention_status === 'warning') {
    addUniqueStep(steps, '运行检查保留并确认快照或远端保留策略')
  }

  if (
    job.restore_drill_status === 'failed'
    || job.restore_drill_status === 'stale'
    || job.restore_drill_status === 'due'
  ) {
    addUniqueStep(steps, '执行恢复演练并复核演练历史')
  }

  if (job.last_restore?.status === 'failed') {
    addUniqueStep(steps, '重新生成恢复预览后再执行恢复')
  } else if ((job.last_restore?.warnings?.length ?? 0) > 0) {
    addUniqueStep(steps, '导出恢复摘要并复核恢复警告')
  } else if (job.last_restore?.status === 'completed' && !latestVerify) {
    addUniqueStep(steps, '运行检查恢复完成只读校验')
  }

  if (latestVerify?.status === 'failed') {
    addUniqueStep(steps, '重新运行检查恢复并处理校验失败项')
  } else if ((latestVerify?.warnings?.length ?? 0) > 0) {
    addUniqueStep(steps, '导出恢复摘要并复核恢复检查警告')
  }

  return steps
}

export function backupJobNeedsAttention(job: BackupJob): boolean {
  return getBackupAttentionReasons(job).length > 0
}

export function getBackupAttentionReasonSummary(jobs: BackupJob[]): string {
  const reasons = Array.from(new Set(jobs.flatMap(getBackupAttentionReasons)))
  if (reasons.length === 0) {
    return '任务已配置，等待下一次备份'
  }
  const visibleReasons = reasons.slice(0, 2)
  const suffix = reasons.length > visibleReasons.length ? ` 等 ${reasons.length} 类问题` : ''
  return `${visibleReasons.join('、')}${suffix}`
}

export function getBackupAttentionNextStepSummary(jobs: BackupJob[]): string {
  const steps = Array.from(new Set(jobs.flatMap(getBackupAttentionNextSteps)))
  if (steps.length === 0) {
    return '打开备份与维护查看任务状态'
  }
  const visibleSteps = steps.slice(0, 2)
  const suffix = steps.length > visibleSteps.length ? ` 等 ${steps.length} 步` : ''
  return `${visibleSteps.join('、')}${suffix}`
}
