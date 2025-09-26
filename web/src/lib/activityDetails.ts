import type { ActionType } from '@/api/activity'
import { formatBytes, formatDate, formatDuration } from '@/lib/utils'
import {
  getDiskHealthDeviceDisplayMessage,
  getDiskHealthReportDisplayMessage,
  getDiskHealthStatusLabel,
} from '@/lib/diskHealthMessages'
import { redactDiagnosticSecretFragments } from '@/lib/diagnosticMessages'

const ACTIVITY_DETAIL_LABELS: Record<string, string> = {
  type: '类型',
  permission: '权限',
  has_password: '密码保护',
  expires_at: '过期时间',
  access_count: '访问次数',
  max_access: '访问上限',
  count: '项目数',
  path: '路径',
  from: '来源路径',
  to: '目标路径',
  source_path: '来源路径',
  target_path: '目标路径',
  destination_path: '目标路径',
  original_path: '原路径',
  restore_path: '恢复路径',
  restore_source: '恢复来源',
  quota_path: '配额路径',
  hash: '版本哈希',
  archive: '归档格式',
  entries: '归档项目数',
  status: '状态',
  message: '诊断',
  id: '记录 ID',
  checked_at: '检查时间',
  started_at: '开始时间',
  finished_at: '完成时间',
  device_count: '设备数',
  warning_count: '提醒数',
  critical_devices: '严重异常设备',
  warning_devices: '提醒设备',
  unavailable_devices: '不可用设备',
  device: '首个异常设备',
  device_status: '设备状态',
  device_message: '设备诊断',
  temperature_c: '温度',
  wear_percent_used: '介质磨损',
  available_spare_percent: '可用备用空间',
  media_errors: '介质错误',
  total_objects: '总对象',
  valid_objects: '有效对象',
  corrupted_objects: '损坏对象',
  missing_objects: '缺失对象',
  total_size: '校验数据量',
  duration_ms: '耗时',
  error_count: '错误数',
  error_message: '诊断',
  persistence_warning: '记录持久化',
  cleanup_warning: '清理状态',
  trash_cleanup_warning: '回收站清理',
  metadata_restore: '关联元数据',
  partial: '执行结果',
  trigger: '触发方式',
}

const ACTIVITY_DETAIL_ORDER = new Map<string, number>([
  ['type', 0],
  ['permission', 1],
  ['has_password', 2],
  ['expires_at', 3],
  ['access_count', 4],
  ['max_access', 5],
  ['count', 6],
  ['path', 6.5],
  ['from', 6.6],
  ['source_path', 6.6],
  ['original_path', 6.6],
  ['to', 7],
  ['target_path', 7.1],
  ['destination_path', 7.1],
  ['restore_path', 7.1],
  ['restore_source', 7.2],
  ['quota_path', 7.2],
  ['hash', 8],
  ['archive', 9],
  ['entries', 10],
  ['status', 10],
  ['message', 11],
  ['error_message', 11],
  ['checked_at', 12],
  ['started_at', 12],
  ['finished_at', 13],
  ['device_count', 14],
  ['warning_count', 15],
  ['critical_devices', 16],
  ['warning_devices', 17],
  ['unavailable_devices', 18],
  ['device', 19],
  ['device_status', 20],
  ['device_message', 21],
  ['temperature_c', 22],
  ['wear_percent_used', 23],
  ['available_spare_percent', 24],
  ['media_errors', 25],
  ['total_objects', 30],
  ['valid_objects', 31],
  ['corrupted_objects', 32],
  ['missing_objects', 33],
  ['total_size', 34],
  ['duration_ms', 35],
  ['error_count', 36],
  ['persistence_warning', 37],
  ['cleanup_warning', 38],
  ['trash_cleanup_warning', 39],
  ['metadata_restore', 40],
  ['partial', 41],
  ['trigger', 42],
  ['id', 90],
])

function formatActivityDetailLabel(key: string): string {
  return ACTIVITY_DETAIL_LABELS[key] ?? key
}

function formatDiskHealthDeviceLabel(value: string): string {
  const trimmed = value.trim()
  const moreMatch = trimmed.match(/\s*\(\+(\d+) more\)$/i)
  const baseValue = moreMatch ? trimmed.slice(0, moreMatch.index).trim() : trimmed
  const labels = baseValue.split(/\s*,\s*/).filter(Boolean).map((label) => {
    const cleanLabel = label.trim()
    return cleanLabel.toLowerCase() === 'unnamed device' ? '未命名设备' : cleanLabel
  })
  const formattedLabels = labels.length > 0 ? labels.join('、') : trimmed
  return moreMatch ? `${formattedLabels} 等 ${moreMatch[1]} 个` : formattedLabels
}

function isDiskHealthDeviceLabelKey(key: string): boolean {
  return key === 'device'
    || key === 'critical_devices'
    || key === 'warning_devices'
    || key === 'unavailable_devices'
}

function formatDiskHealthActivityDetailValue(key: string, value: string, details: Record<string, string>): string {
  if (isDiskHealthDeviceLabelKey(key)) {
    return formatDiskHealthDeviceLabel(value)
  }

  if (key === 'status' || key === 'device_status') {
    return getDiskHealthStatusLabel(value)
  }

  if (key === 'message') {
    return getDiskHealthReportDisplayMessage(value, details.status ?? '')
  }

  if (key === 'device_message') {
    return getDiskHealthDeviceDisplayMessage(value, details.device_status ?? details.status ?? '') ?? ''
  }

  if (key === 'checked_at') {
    const formatted = formatDate(value)
    return formatted === '--' ? value : formatted
  }

  if (key === 'temperature_c' && /^-?\d+$/.test(value)) {
    return `${value} C`
  }

  if ((key === 'wear_percent_used' || key === 'available_spare_percent') && /^\d+$/.test(value)) {
    return `${value}%`
  }

  if (key === 'media_errors' && /^\d+$/.test(value)) {
    return `${value} 个`
  }

  if ((key === 'device_count' || key === 'warning_count') && /^\d+$/.test(value)) {
    return `${value} 个`
  }

  return value
}

function getScrubActivityStatusLabel(status: string): string {
  const labels: Record<string, string> = {
    completed: '已完成',
    failed: '失败',
    running: '运行中',
    cancelled: '已取消',
  }
  return labels[status.trim().toLowerCase()] ?? '未知状态'
}

function getScrubActivityTriggerLabel(trigger: string): string {
  const labels: Record<string, string> = {
    manual: '手动执行',
    scheduled: '定时任务',
    retry: '自动重试',
    scheduled_retry: '自动重试',
  }
  return labels[trigger.trim().toLowerCase()] ?? '未知触发方式'
}

function getScrubActivityDiagnosticMessage(message: string): string {
  const messages: Record<string, string> = {
    'scrub failed; check server logs for details': '数据校验未完成；建议下载诊断包并检查服务日志。',
    'object failed integrity verification': '对象内容与索引记录不一致，请检查存储介质并从备份恢复。',
    'object is missing': '对象数据缺失，请检查存储介质并从备份恢复。',
    'object could not be read': '对象读取失败，请检查存储权限和介质状态。',
    'object verification failed': '对象校验失败，请查看服务日志并确认备份状态。',
  }
  return messages[message.trim().toLowerCase()] ?? '数据校验结果已记录，请查看维护页面和服务日志。'
}

function formatPositiveIntegerDetail(value: string, unit: string): string {
  return /^\d+$/.test(value) ? `${value} ${unit}` : value
}

function formatScrubActivityDetailValue(key: string, value: string): string {
  if (key === 'status') {
    return getScrubActivityStatusLabel(value)
  }

  if (key === 'trigger') {
    return getScrubActivityTriggerLabel(value)
  }

  if (key === 'error_message' || key === 'message') {
    return getScrubActivityDiagnosticMessage(value)
  }

  if (key === 'started_at' || key === 'finished_at') {
    const formatted = formatDate(value)
    return formatted === '--' ? value : formatted
  }

  if (key === 'total_size' && /^\d+$/.test(value)) {
    return formatBytes(Number(value))
  }

  if (key === 'duration_ms' && /^\d+$/.test(value)) {
    return formatDuration(Number(value))
  }

  if (key === 'persistence_warning') {
    if (value === 'true') {
      return '结果记录保存异常，请检查维护历史。'
    }
    if (value === 'false') {
      return '正常'
    }
  }

  if (['total_objects', 'valid_objects', 'corrupted_objects', 'missing_objects', 'error_count'].includes(key)) {
    return formatPositiveIntegerDetail(value, '个')
  }

  return value
}

function isTrashActivityAction(action: ActionType): boolean {
  return action === 'trash_restore' || action === 'trash_delete' || action === 'trash_empty'
}

function formatTrashActivityDetailValue(key: string, value: string): string {
  if (key === 'count') {
    return formatPositiveIntegerDetail(value, '项')
  }

  if (key === 'partial') {
    if (value === 'true') {
      return '仅完成部分项目'
    }
    if (value === 'false') {
      return '已完成全部项目'
    }
  }

  if (key === 'cleanup_warning') {
    if (value === 'true') {
      return '部分垃圾箱数据清理不完整，请检查存储状态。'
    }
    if (value === 'false') {
      return '正常'
    }
  }

  if (key === 'persistence_warning') {
    if (value === 'true') {
      return '操作已完成，但变更记录保存异常。'
    }
    if (value === 'false') {
      return '正常'
    }
  }

  if (key === 'metadata_restore') {
    if (value === 'failed') {
      return '关联分享或收藏恢复失败，请检查相关记录。'
    }
    if (value === 'ok' || value === 'succeeded') {
      return '正常'
    }
    return '状态未知'
  }

  return value
}

function formatActivityDetailValue(action: ActionType, key: string, value: string, details: Record<string, string>): string {
  if (action === 'disk_health') {
    return formatDiskHealthActivityDetailValue(key, value, details)
  }

  if (action === 'scrub') {
    return formatScrubActivityDetailValue(key, value)
  }

  if (isTrashActivityAction(action)) {
    return formatTrashActivityDetailValue(key, value)
  }

  if (key === 'type') {
    const labels: Record<string, string> = {
      file: '文件',
      folder: '文件夹',
      directory: '文件夹',
    }
    return labels[value] ?? '未知类型'
  }

  if (key === 'hash') {
    return value.length > 12 ? `${value.slice(0, 12)}...` : value
  }

  if (key === 'restore_source') {
    const labels: Record<string, string> = {
      version: '版本历史',
      trash: '回收站',
      backup: '备份',
    }
    return labels[value.trim().toLowerCase()] ?? '未知来源'
  }

  if (key === 'archive') {
    const labels: Record<string, string> = {
      zip: 'ZIP',
    }
    return labels[value.trim().toLowerCase()] ?? '未知归档格式'
  }

  if (key === 'entries') {
    return formatPositiveIntegerDetail(value, '项')
  }

  if (key === 'message' || key === 'error_message') {
    return redactDiagnosticSecretFragments(value)
  }

  if (key === 'persistence_warning') {
    if (value === 'true') {
      return '操作已完成，但变更记录保存异常。'
    }
    if (value === 'false') {
      return '正常'
    }
  }

  if (key === 'cleanup_warning') {
    if (value === 'true') {
      return '残留数据清理不完整，请检查存储状态。'
    }
    if (value === 'false') {
      return '正常'
    }
  }

  if (key === 'trash_cleanup_warning') {
    if (value === 'true') {
      return '回收站关联数据清理不完整，请检查回收站状态。'
    }
    if (value === 'false') {
      return '正常'
    }
  }

  if (key === 'permission') {
    const labels: Record<string, string> = {
      read: '只读',
      read_write: '读写',
    }
    return labels[value] ?? '未知权限'
  }

  if (key === 'has_password') {
    if (value === 'true') return '是'
    if (value === 'false') return '否'
  }

  if (key === 'expires_at') {
    const formatted = formatDate(value)
    return formatted === '--' ? value : formatted
  }

  if ((key === 'access_count' || key === 'max_access') && /^\d+$/.test(value)) {
    return `${value} 次`
  }

  return value
}

export interface ActivityDetailEntry {
  key: string
  label: string
  value: string
}

export function getActivityDetailEntries(action: ActionType, details: Record<string, string>): ActivityDetailEntry[] {
  return Object.entries(details)
    .filter(([, value]) => value.trim() !== '')
    .sort(([leftKey], [rightKey]) => {
      const leftOrder = ACTIVITY_DETAIL_ORDER.get(leftKey) ?? Number.MAX_SAFE_INTEGER
      const rightOrder = ACTIVITY_DETAIL_ORDER.get(rightKey) ?? Number.MAX_SAFE_INTEGER
      if (leftOrder !== rightOrder) {
        return leftOrder - rightOrder
      }
      return leftKey.localeCompare(rightKey)
    })
    .map(([key, value]) => ({
      key,
      label: formatActivityDetailLabel(key),
      value: formatActivityDetailValue(action, key, value, details),
    }))
    .filter((entry) => entry.value.trim() !== '')
}
