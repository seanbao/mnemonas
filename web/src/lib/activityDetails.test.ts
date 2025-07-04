import { describe, expect, it } from 'vitest'
import { getActivityDetailEntries } from './activityDetails'

describe('activityDetails', () => {
  it('formats common file operation details', () => {
    expect(getActivityDetailEntries('move', {
      to: '/archive/report.pdf',
      type: 'directory',
      hash: 'abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
      persistence_warning: 'true',
    })).toEqual([
      { key: 'type', label: '类型', value: '文件夹' },
      { key: 'to', label: '目标路径', value: '/archive/report.pdf' },
      { key: 'hash', label: '版本哈希', value: 'abcdef123456...' },
      { key: 'persistence_warning', label: '记录持久化', value: '操作已完成，但变更记录保存异常。' },
    ])
  })

  it('formats archive download details', () => {
    expect(getActivityDetailEntries('download', {
      archive: 'zip',
      entries: '128',
    })).toEqual([
      { key: 'archive', label: '归档格式', value: 'ZIP' },
      { key: 'entries', label: '归档项目数', value: '128 项' },
    ])
  })

  it('formats version restore details with a readable restore source', () => {
    expect(getActivityDetailEntries('restore', {
      restore_source: 'version',
      hash: 'abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890',
    })).toEqual([
      { key: 'restore_source', label: '恢复来源', value: '版本历史' },
      { key: 'hash', label: '版本哈希', value: 'abcdef123456...' },
    ])
  })

  it('omits details hidden by access filtering', () => {
    expect(getActivityDetailEntries('move', {
      to: '',
      from: '   ',
      type: 'file',
    })).toEqual([
      { key: 'type', label: '类型', value: '文件' },
    ])
  })

  it('formats path-like detail keys with product labels', () => {
    expect(getActivityDetailEntries('move', {
      from: '/inbox/report.pdf',
      to: '/archive/report.pdf',
      quota_path: '/team',
      target_path: '/team/report.pdf',
    })).toEqual([
      { key: 'from', label: '来源路径', value: '/inbox/report.pdf' },
      { key: 'to', label: '目标路径', value: '/archive/report.pdf' },
      { key: 'target_path', label: '目标路径', value: '/team/report.pdf' },
      { key: 'quota_path', label: '配额路径', value: '/team' },
    ])
  })

  it('formats disk health device label summaries without backend placeholders', () => {
    expect(getActivityDetailEntries('disk_health', {
      critical_devices: 'data, unnamed device (+2 more)',
      device: 'unnamed device',
      device_status: 'critical',
    })).toEqual([
      { key: 'critical_devices', label: '严重异常设备', value: 'data、未命名设备 等 2 个' },
      { key: 'device', label: '首个异常设备', value: '未命名设备' },
      { key: 'device_status', label: '设备状态', value: '严重异常' },
    ])
  })

  it('keeps scrub-specific messages distinct from generic warnings', () => {
    expect(getActivityDetailEntries('scrub', {
      status: 'failed',
      trigger: 'scheduled_retry',
      error_message: 'scrub failed; check server logs for details',
      persistence_warning: 'true',
      duration_ms: '90000',
    })).toEqual([
      { key: 'status', label: '状态', value: '失败' },
      { key: 'error_message', label: '诊断', value: '数据校验未完成；建议下载诊断包并检查服务日志。' },
      { key: 'duration_ms', label: '耗时', value: '1 分 30 秒' },
      { key: 'persistence_warning', label: '记录持久化', value: '结果记录保存异常，请检查维护历史。' },
      { key: 'trigger', label: '触发方式', value: '自动重试' },
    ])
  })

  it('keeps trash cleanup wording specific to trash actions', () => {
    expect(getActivityDetailEntries('trash_empty', {
      count: '3',
      cleanup_warning: 'true',
      partial: 'true',
    })).toEqual([
      { key: 'count', label: '项目数', value: '3 项' },
      { key: 'cleanup_warning', label: '清理状态', value: '部分垃圾箱数据清理不完整，请检查存储状态。' },
      { key: 'partial', label: '执行结果', value: '仅完成部分项目' },
    ])
  })
})
