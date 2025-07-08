import { describe, expect, it } from 'vitest'
import type { BackupJob } from '@/api/files'
import {
  backupJobNeedsAttention,
  getBackupAttentionNextStepSummary,
  getBackupAttentionNextSteps,
  getBackupAttentionReasonSummary,
  getBackupAttentionReasons,
} from './backupAttention'

function backupJob(overrides: Partial<BackupJob> = {}): BackupJob {
  return {
    id: 'external-disk',
    name: '外置硬盘备份',
    type: 'local',
    source: '/srv/mnemonas',
    destination: '/mnt/backup-drive/mnemonas',
    disabled: false,
    retention_status: 'ok',
    health_status: 'ok',
    restore_drill_status: 'ok',
    include_config: true,
    verify_after_backup: true,
    exclude: [],
    running: false,
    ...overrides,
  }
}

describe('backupAttention', () => {
  it('does not flag healthy backup jobs', () => {
    const job = backupJob()

    expect(getBackupAttentionReasons(job)).toEqual([])
    expect(getBackupAttentionNextSteps(job)).toEqual([])
    expect(backupJobNeedsAttention(job)).toBe(false)
    expect(getBackupAttentionReasonSummary([job])).toBe('任务已配置，等待下一次备份')
    expect(getBackupAttentionNextStepSummary([job])).toBe('打开备份与维护查看任务状态')
  })

  it('flags warning-only backup and restore results with next steps', () => {
    const job = backupJob({
      last_run: {
        status: 'completed',
        warning: true,
        warnings: ['backup completed with warnings'],
      } as NonNullable<BackupJob['last_run']>,
      last_restore: {
        status: 'completed',
        target_path: '/restore/mnemonas',
        warnings: ['restore completed with warnings'],
      } as NonNullable<BackupJob['last_restore']>,
      last_matching_restore_verify: {
        status: 'completed',
        warnings: [],
      } as NonNullable<BackupJob['last_matching_restore_verify']>,
    })

    expect(getBackupAttentionReasons(job)).toEqual(['最近备份有警告', '最近恢复有警告'])
    expect(getBackupAttentionNextSteps(job)).toEqual([
      '运行立即备份并查看最近备份结果',
      '导出恢复摘要并复核恢复警告',
    ])
    expect(getBackupAttentionNextStepSummary([job])).toBe('运行立即备份并查看最近备份结果、导出恢复摘要并复核恢复警告')
    expect(backupJobNeedsAttention(job)).toBe(true)
    expect(getBackupAttentionReasonSummary([job])).toBe('最近备份有警告、最近恢复有警告')
  })

  it('summarizes unique restore attention reasons', () => {
    const pendingVerifyJob = backupJob({
      id: 'pending-verify',
      restore_drill_status: 'due',
      last_restore: {
        status: 'completed',
        target_path: '/restore/pending',
      } as NonNullable<BackupJob['last_restore']>,
      last_matching_restore_verify: undefined,
    })
    const verifyWarningJob = backupJob({
      id: 'verify-warning',
      last_matching_restore_verify: {
        status: 'completed',
        warnings: ['extra restored file'],
      } as NonNullable<BackupJob['last_matching_restore_verify']>,
    })

    expect(getBackupAttentionReasons(pendingVerifyJob)).toEqual(['恢复演练待执行', '恢复待校验'])
    expect(getBackupAttentionNextSteps(pendingVerifyJob)).toEqual([
      '执行恢复演练并复核演练历史',
      '运行检查恢复完成只读校验',
    ])
    expect(getBackupAttentionReasons(verifyWarningJob)).toEqual(['恢复检查有警告'])
    expect(getBackupAttentionNextSteps(verifyWarningJob)).toEqual(['导出恢复摘要并复核恢复检查警告'])
    expect(getBackupAttentionReasonSummary([pendingVerifyJob, verifyWarningJob])).toBe('恢复演练待执行、恢复待校验 等 3 类问题')
    expect(getBackupAttentionNextStepSummary([pendingVerifyJob, verifyWarningJob])).toBe('执行恢复演练并复核演练历史、运行检查恢复完成只读校验 等 3 步')
  })

  it('flags a running restore verification for the latest restore', () => {
    const job = backupJob({
      last_restore: {
        status: 'completed',
        target_path: '/restore/mnemonas',
      } as NonNullable<BackupJob['last_restore']>,
      last_matching_restore_verify: {
        status: 'running',
        target_path: '/restore/mnemonas',
        warnings: [],
      } as NonNullable<BackupJob['last_matching_restore_verify']>,
    })

    expect(getBackupAttentionReasons(job)).toEqual(['恢复检查中'])
    expect(getBackupAttentionNextSteps(job)).toEqual(['等待恢复检查完成'])
    expect(getBackupAttentionReasonSummary([job])).toBe('恢复检查中')
    expect(getBackupAttentionNextStepSummary([job])).toBe('等待恢复检查完成')
  })

  it('deduplicates backup next steps across health and run failures', () => {
    const job = backupJob({
      health_status: 'failed',
      last_run: {
        status: 'failed',
        warning: true,
        warnings: ['backup failed'],
      } as NonNullable<BackupJob['last_run']>,
      retention_status: 'warning',
      restore_drill_status: 'stale',
      last_restore: {
        status: 'failed',
        target_path: '/restore/mnemonas',
      } as NonNullable<BackupJob['last_restore']>,
      last_matching_restore_verify: {
        status: 'failed',
        warnings: [],
      } as NonNullable<BackupJob['last_matching_restore_verify']>,
    })

    expect(getBackupAttentionNextSteps(job)).toEqual([
      '运行立即备份并查看最近备份结果',
      '运行检查保留并确认快照或远端保留策略',
      '执行恢复演练并复核演练历史',
      '重新生成恢复预览后再执行恢复',
      '重新运行检查恢复并处理校验失败项',
    ])
  })
})
