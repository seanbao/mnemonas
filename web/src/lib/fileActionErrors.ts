function getErrorStatus(error: unknown): number | undefined {
  if (error && typeof error === 'object' && 'status' in error && typeof error.status === 'number') {
    return error.status
  }

  return undefined
}

function getErrorCode(error: unknown): string | undefined {
  if (error && typeof error === 'object' && 'code' in error && typeof error.code === 'string') {
    return error.code
  }

  return undefined
}

export function isFilesystemUnavailableError(error: unknown): boolean {
  const status = getErrorStatus(error)
  const code = getErrorCode(error)
  return status === 503 || code === 'SERVICE_UNAVAILABLE'
}

export function getFileDownloadErrorToast(error: unknown): {
  title: string
  description: string
  color: 'warning' | 'danger'
} {
  if (isFilesystemUnavailableError(error)) {
    return {
      title: '下载暂不可用',
      description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
      color: 'warning',
    }
  }

  return {
    title: '下载失败',
    description: error instanceof Error ? error.message : '请稍后重试',
    color: 'danger',
  }
}