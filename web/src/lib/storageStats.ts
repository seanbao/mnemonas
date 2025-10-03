export interface CasStorageStatsLike {
  storageStatsAvailable?: boolean
  totalSize?: number
  totalObjects?: number
  uniqueSize?: number
  dedupRatio?: number
}

export interface DiskStorageStatsLike {
  diskStatsAvailable?: boolean
  diskTotal?: number
  diskFree?: number
  diskAvailable?: number
  diskUsed?: number
  diskUsageRatio?: number
  diskFilesystemType?: string
  diskMountPoint?: string
  diskMountSource?: string
  diskMountOptions?: string
  diskNativeDataChecksumSupport?: boolean
}

export type DiskSpaceStatusLevel = 'unknown' | 'normal' | 'warning' | 'critical'

export interface DiskSpaceStatus {
  level: DiskSpaceStatusLevel
  title: string
  description: string
  label: string
}

export type FilesystemIntegrityStatusLevel = 'supported' | 'unknown' | 'volatile' | 'remote' | 'limited'

export interface FilesystemIntegrityStatus {
  level: FilesystemIntegrityStatusLevel
  title: string
  description: string
  label: string
}

const DEFAULT_WARNING_USAGE_RATIO = 0.9
const DEFAULT_CRITICAL_USAGE_RATIO = 0.95
const DEFAULT_WARNING_MIN_AVAILABLE_BYTES = 10 * 1024 * 1024 * 1024
const DEFAULT_CRITICAL_MIN_AVAILABLE_BYTES = 1 * 1024 * 1024 * 1024

export function areStorageStatsAvailable(stats: CasStorageStatsLike | undefined): boolean {
  if (!stats) {
    return false
  }
  if (stats.storageStatsAvailable !== undefined) {
    return stats.storageStatsAvailable
  }
  return (
    stats.totalSize !== undefined
    || stats.totalObjects !== undefined
    || stats.uniqueSize !== undefined
    || stats.dedupRatio !== undefined
  )
}

export function areDiskStatsAvailable(stats: DiskStorageStatsLike | undefined): boolean {
  if (!stats) {
    return false
  }
  if (stats.diskStatsAvailable !== undefined) {
    return stats.diskStatsAvailable
  }
  return (
    stats.diskTotal !== undefined
    || stats.diskFree !== undefined
    || stats.diskAvailable !== undefined
    || stats.diskUsed !== undefined
    || stats.diskUsageRatio !== undefined
    || stats.diskFilesystemType !== undefined
    || stats.diskMountPoint !== undefined
    || stats.diskMountSource !== undefined
  )
}

export function clampUsagePercent(value: number | undefined): number | undefined {
  if (value === undefined || !Number.isFinite(value)) {
    return undefined
  }
  return Math.min(100, Math.max(0, value * 100))
}

export function formatUsagePercent(value: number | undefined): string {
  const percent = clampUsagePercent(value)
  return percent === undefined ? '--' : `${percent.toFixed(1)}%`
}

export function formatFilesystemType(value: string | undefined): string {
  const normalized = value?.trim()
  if (!normalized || normalized === 'unknown') {
    return '未知'
  }
  const lower = normalized.toLowerCase()
  if (lower === 'ext') {
    return 'EXT 系列'
  }
  if (/^ext[234]$/.test(lower)) {
    return lower.toUpperCase()
  }
  if (['zfs', 'btrfs', 'xfs', 'nfs', 'cifs', 'smb', 'smb2'].includes(lower)) {
    return lower.toUpperCase()
  }
  return normalized
}

export function getFilesystemIntegrityStatus(
  fsType: string | undefined,
  nativeDataChecksumSupport: boolean | undefined,
): FilesystemIntegrityStatus {
  const normalized = fsType?.trim().toLowerCase()
  if (nativeDataChecksumSupport === true) {
    return {
      level: 'supported',
      title: '原生数据校验支持',
      description: `${formatFilesystemType(fsType)} 具备底层校验与 scrub 能力，仍需保留独立备份。`,
      label: '已支持',
    }
  }

  if (!normalized || normalized === 'unknown') {
    return {
      level: 'unknown',
      title: '文件系统未知',
      description: '无法识别底层文件系统，建议在部署机上运行 mnemonas-doctor 核对磁盘布局。',
      label: '未知',
    }
  }

  if (normalized === 'tmpfs') {
    return {
      level: 'volatile',
      title: '临时文件系统',
      description: '当前存储看起来是 tmpfs，重启可能丢失数据。请迁移到持久磁盘。',
      label: '临时存储',
    }
  }

  if (['nfs', 'cifs', 'smb', 'smb2', 'fuse'].some((prefix) => normalized.startsWith(prefix))) {
    return {
      level: 'remote',
      title: '网络或 FUSE 存储',
      description: '请确认一致性、断线恢复和独立备份策略。',
      label: '需确认',
    }
  }

  return {
    level: 'limited',
    title: '建议使用 ZFS/Btrfs',
    description: '当前未检测到底层数据校验与 scrub 能力，请依赖 MnemoNAS scrub 和独立备份。',
    label: '未检测到',
  }
}

export function getDiskSpaceStatus(stats: DiskStorageStatsLike | undefined): DiskSpaceStatus {
  if (!areDiskStatsAvailable(stats)) {
    return {
      level: 'unknown',
      title: '磁盘容量未知',
      description: '无法判断剩余空间，请检查存储统计服务或运行部署诊断脚本。',
      label: '容量未知',
    }
  }

  const usageRatio = Number.isFinite(stats?.diskUsageRatio) ? stats!.diskUsageRatio! : undefined
  const availableBytes = Number.isFinite(stats?.diskAvailable) ? stats!.diskAvailable! : undefined
  if (usageRatio === undefined && availableBytes === undefined) {
    return {
      level: 'unknown',
      title: '磁盘容量未知',
      description: '存储统计未返回可用容量或使用率，暂时无法判断剩余空间风险。',
      label: '容量未知',
    }
  }

  const usageIsCritical = usageRatio !== undefined && usageRatio >= DEFAULT_CRITICAL_USAGE_RATIO
  const usageIsWarning = usageRatio !== undefined && usageRatio >= DEFAULT_WARNING_USAGE_RATIO
  const freeIsCritical = availableBytes !== undefined && availableBytes < DEFAULT_CRITICAL_MIN_AVAILABLE_BYTES
  const freeIsWarning = availableBytes !== undefined && availableBytes < DEFAULT_WARNING_MIN_AVAILABLE_BYTES

  if (usageIsCritical || freeIsCritical) {
    return {
      level: 'critical',
      title: '存储空间严重不足',
      description: '剩余空间已经接近危险线，请尽快清理回收站、迁移数据或扩容磁盘。',
      label: '严重不足',
    }
  }

  if (usageIsWarning || freeIsWarning) {
    return {
      level: 'warning',
      title: '存储空间偏紧',
      description: '剩余空间低于推荐余量，建议开启提醒并安排清理或扩容。',
      label: '偏紧',
    }
  }

  return {
    level: 'normal',
    title: '存储空间充足',
    description: '当前磁盘剩余空间处于正常范围。',
    label: '空间充足',
  }
}
