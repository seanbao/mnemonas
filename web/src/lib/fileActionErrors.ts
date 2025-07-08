import { getUserFacingErrorDescription } from './apiMessages'
import { getNonBlankJsonString } from './jsonErrorResponse'

function getErrorStatus(error: unknown): number | undefined {
  if (error && typeof error === 'object' && 'status' in error && typeof error.status === 'number') {
    return error.status
  }

  return undefined
}

function getErrorCode(error: unknown): string | undefined {
  if (error && typeof error === 'object' && 'code' in error) {
    return getNonBlankJsonString(error.code)
  }

  return undefined
}

function getErrorMessage(error: unknown): string | undefined {
  if (error instanceof Error) {
    return getNonBlankJsonString(error.message)
  }

  if (error && typeof error === 'object' && 'message' in error) {
    return getNonBlankJsonString(error.message)
  }

  return undefined
}

export interface FileActionErrorToast {
  title: string
  description: string
  color: 'warning' | 'danger'
}

export function isFilesystemUnavailableError(error: unknown): boolean {
  const status = getErrorStatus(error)
  const code = getErrorCode(error)
  return status === 503 || code === 'SERVICE_UNAVAILABLE'
}

export function isQuotaExceededError(error: unknown): boolean {
  const status = getErrorStatus(error)
  const code = getErrorCode(error)
  return status === 507 || code === 'QUOTA_EXCEEDED'
}

export function isMissingFileError(error: unknown): boolean {
  const status = getErrorStatus(error)
  const code = getErrorCode(error)
  return status === 404 || code === 'FILE_NOT_FOUND'
}

export function getMissingFileErrorDescription(error: unknown): string | null {
  if (!isMissingFileError(error)) {
    return null
  }

  return '该文件可能已被移动或删除，请刷新列表后重试。'
}

export function getFileLoadErrorDescription(error: unknown, fallback: string): string {
  return getMissingFileErrorDescription(error) ?? getUserFacingErrorDescription(error, fallback)
}

export function getQuotaExceededErrorToast(error: unknown): FileActionErrorToast | null {
  if (!isQuotaExceededError(error)) {
    return null
  }

  const message = getErrorMessage(error)
  if (message === 'directory quota exceeded') {
    return {
      title: '容量配额不足',
      description: '目标目录的容量配额不足，请清理空间或调整目录配额后重试。',
      color: 'warning',
    }
  }

  if (message === 'user quota exceeded') {
    return {
      title: '容量配额不足',
      description: '当前用户的容量配额不足，请清理空间或调整用户配额后重试。',
      color: 'warning',
    }
  }

  return {
    title: '容量配额不足',
    description: '可用容量配额不足，请清理空间或调整配额后重试。',
    color: 'warning',
  }
}

export function getPathConflictErrorToast(error: unknown): FileActionErrorToast | null {
  const message = getErrorMessage(error)

  if (message === 'resource already exists') {
    return {
      title: '同名项目已存在',
      description: '当前目录中已存在同名文件或文件夹，请使用其他名称。',
      color: 'warning',
    }
  }

  if (message === 'parent path is not a directory') {
    return {
      title: '目标位置不可用',
      description: '当前目录状态已变更，请刷新列表后重试。',
      color: 'warning',
    }
  }

  return null
}

export function getSharedPathConflictErrorToast(errors: unknown[]): FileActionErrorToast | null {
  const firstToast = errors[0] ? getPathConflictErrorToast(errors[0]) : null
  if (!firstToast) {
    return null
  }

  const everyErrorMatches = errors.every((error) => {
    const toast = getPathConflictErrorToast(error)
    return (
      toast?.title === firstToast.title
      && toast.description === firstToast.description
      && toast.color === firstToast.color
    )
  })

  return everyErrorMatches ? firstToast : null
}

export function getSharedQuotaExceededErrorToast(errors: unknown[]): FileActionErrorToast | null {
  const firstToast = errors[0] ? getQuotaExceededErrorToast(errors[0]) : null
  if (!firstToast) {
    return null
  }

  const everyErrorMatches = errors.every((error) => {
    const toast = getQuotaExceededErrorToast(error)
    return (
      toast?.title === firstToast.title
      && toast.description === firstToast.description
      && toast.color === firstToast.color
    )
  })

  return everyErrorMatches ? firstToast : null
}

export function getArchiveDownloadErrorToast(error: unknown): FileActionErrorToast | null {
  const code = getErrorCode(error)
  const message = getErrorMessage(error)

  if (
    code === 'PAYLOAD_TOO_LARGE' &&
    (message === '归档内容过大' || message === 'archive content is too large')
  ) {
    return {
      title: '归档下载失败',
      description: '归档内容过大，请缩小选择范围后重试。',
      color: 'warning',
    }
  }

  if (message === '归档包含的条目过多' || message === 'archive contains too many entries') {
    return {
      title: '归档下载失败',
      description: '归档条目过多，请缩小选择范围后重试。',
      color: 'warning',
    }
  }

  if (message === '归档条目名称冲突，请刷新后重试' || message === 'archive contains duplicate entries') {
    return {
      title: '归档下载失败',
      description: '归档条目名称冲突，请刷新列表后重新下载。',
      color: 'warning',
    }
  }

  if (message === '文件内容已变更，请刷新后重试' || message === 'archive entry changed during download') {
    return {
      title: '归档下载失败',
      description: '文件内容已变更，请刷新列表后重新下载。',
      color: 'warning',
    }
  }

  return null
}

export function getSharedArchiveDownloadErrorToast(errors: unknown[]): FileActionErrorToast | null {
  if (errors.length === 0) {
    return null
  }

  const archiveToasts = errors.map(getArchiveDownloadErrorToast)
  if (archiveToasts.some((toast) => !toast)) {
    return null
  }

  const firstToast = archiveToasts[0]
  if (!firstToast) {
    return null
  }

  const everyToastMatches = archiveToasts.every((toast) => (
    toast?.description === firstToast.description
    && toast.color === firstToast.color
  ))

  if (everyToastMatches) {
    return {
      ...firstToast,
      title: '批量归档下载失败',
    }
  }

  return {
    title: '批量归档下载失败',
    description: '多个归档下载失败，请缩小选择范围或刷新列表后重试。',
    color: 'warning',
  }
}

export function getSharedMissingFileDownloadErrorToast(errors: unknown[]): FileActionErrorToast | null {
  if (errors.length === 0 || !errors.every(isMissingFileError)) {
    return null
  }

  return {
    title: '批量下载失败',
    description: '所选文件可能已被移动或删除，请刷新列表后重试。',
    color: 'warning',
  }
}

export function getFileDownloadErrorToast(error: unknown): FileActionErrorToast {
  if (isFilesystemUnavailableError(error)) {
    return {
      title: '下载暂不可用',
      description: '文件系统当前不可用，请检查设备状态或稍后重试。',
      color: 'warning',
    }
  }

  const missingFileDescription = getMissingFileErrorDescription(error)
  if (missingFileDescription) {
    return {
      title: '文件已不存在',
      description: missingFileDescription,
      color: 'warning',
    }
  }

  const archiveToast = getArchiveDownloadErrorToast(error)
  if (archiveToast) {
    return archiveToast
  }

  return {
    title: '下载失败',
    description: getUserFacingErrorDescription(error),
    color: 'danger',
  }
}
