import type { DirectoryQuotaSummary } from '@/lib/directoryQuota'
import type { DiskSpaceStatus, FilesystemIntegrityStatus } from '@/lib/storageStats'

export type StorageRiskLevel = 'normal' | 'warning' | 'critical'

export type StorageRiskItem = {
  key: string
  level: Exclude<StorageRiskLevel, 'normal'>
  title: string
  description: string
  action: string
}

export type StorageRiskSummary = {
  tone: StorageRiskLevel
  title: string
  description: string
}

export function buildStorageRiskItems(
  diskSpaceStatus: DiskSpaceStatus,
  filesystemIntegrityStatus: FilesystemIntegrityStatus | undefined,
  directoryQuotaStatsAvailable: boolean | undefined,
  directoryQuotaSummary: DirectoryQuotaSummary,
): StorageRiskItem[] {
  const items: StorageRiskItem[] = []

  if (diskSpaceStatus.level === 'critical') {
    items.push({
      key: 'disk-space-critical',
      level: 'critical',
      title: diskSpaceStatus.title,
      description: diskSpaceStatus.description,
      action: '先清理回收站和临时数据，再安排扩容或迁移。',
    })
  } else if (diskSpaceStatus.level === 'warning' || diskSpaceStatus.level === 'unknown') {
    items.push({
      key: `disk-space-${diskSpaceStatus.level}`,
      level: 'warning',
      title: diskSpaceStatus.title,
      description: diskSpaceStatus.description,
      action: '复核增长趋势，并确认容量提醒已经启用。',
    })
  }

  if (filesystemIntegrityStatus) {
    if (filesystemIntegrityStatus.level === 'volatile') {
      items.push({
        key: 'filesystem-volatile',
        level: 'critical',
        title: filesystemIntegrityStatus.title,
        description: filesystemIntegrityStatus.description,
        action: '迁移到持久化磁盘后再作为 NAS 存储使用。',
      })
    } else if (filesystemIntegrityStatus.level !== 'supported') {
      items.push({
        key: `filesystem-${filesystemIntegrityStatus.level}`,
        level: 'warning',
        title: filesystemIntegrityStatus.title,
        description: filesystemIntegrityStatus.description,
        action: '保留独立备份，并定期运行完整性检查。',
      })
    }
  }

  if (directoryQuotaStatsAvailable === false) {
    items.push({
      key: 'directory-quota-unavailable',
      level: 'warning',
      title: '目录配额统计不可用',
      description: '无法判断目录配额是否已接近或超过上限。',
      action: '刷新存储统计，或检查后端存储状态。',
    })
  } else if (directoryQuotaSummary.totalCount === 0) {
    items.push({
      key: 'directory-quota-missing-policy',
      level: 'warning',
      title: '未配置目录配额',
      description: '共享目录没有容量边界时，单个目录可能持续占满存储空间。',
      action: '为家庭共享、媒体库或团队目录配置明确上限。',
    })
  } else {
    if (directoryQuotaSummary.exceededCount > 0) {
      items.push({
        key: 'directory-quota-exceeded',
        level: 'critical',
        title: `${directoryQuotaSummary.exceededCount} 个目录已达配额上限`,
        description: '超限目录会阻止继续写入匹配路径。',
        action: '清理目录内容、提高配额，或迁移部分数据。',
      })
    }
    if (directoryQuotaSummary.missingCount > 0) {
      items.push({
        key: 'directory-quota-missing',
        level: 'warning',
        title: `${directoryQuotaSummary.missingCount} 个配额目录不存在`,
        description: '配置仍存在，但目标目录当前无法统计实际占用。',
        action: '创建目标目录，或删除不再使用的配额配置。',
      })
    }
    if (directoryQuotaSummary.warningCount > 0) {
      items.push({
        key: 'directory-quota-warning',
        level: 'warning',
        title: `${directoryQuotaSummary.warningCount} 个目录接近配额上限`,
        description: '这些目录的增长可能很快转为写入失败。',
        action: '复核近期增长，并确认是否需要扩容或归档。',
      })
    }
  }

  return items
}

export function getStorageRiskLevel(items: StorageRiskItem[]): StorageRiskLevel {
  if (items.some((item) => item.level === 'critical')) {
    return 'critical'
  }
  if (items.some((item) => item.level === 'warning')) {
    return 'warning'
  }
  return 'normal'
}

export function getStorageRiskSummary(level: StorageRiskLevel): StorageRiskSummary {
  if (level === 'critical') {
    return {
      tone: 'critical',
      title: '需立即处理',
      description: '存在会影响写入或数据可靠性的存储风险。',
    }
  }
  if (level === 'warning') {
    return {
      tone: 'warning',
      title: '需要复核',
      description: '存储可以继续使用，但有配置或可靠性事项需要确认。',
    }
  }
  return {
    tone: 'normal',
    title: '状态正常',
    description: '容量、底层校验和目录配额未发现需处理项；仍应保留独立备份。',
  }
}

export function getStorageRiskNextStepSummary(items: StorageRiskItem[]): string {
  if (items.length === 0) {
    return '继续保留外部备份，并定期运行完整性检查和恢复演练。'
  }

  const steps = Array.from(new Set(items.map((item) => item.action.replace(/[。.]$/, ''))))
  const visibleSteps = steps.slice(0, 2)
  const suffix = steps.length > visibleSteps.length ? ` 等 ${steps.length} 步` : ''
  return `${visibleSteps.join('；')}${suffix}`
}
