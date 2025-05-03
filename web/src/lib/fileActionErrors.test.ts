import { describe, expect, it } from 'vitest'
import { getFileDownloadErrorToast, isFilesystemUnavailableError } from './fileActionErrors'

describe('fileActionErrors', () => {
  it('detects unavailable filesystem errors by status or code', () => {
    expect(isFilesystemUnavailableError({ status: 503 })).toBe(true)
    expect(isFilesystemUnavailableError({ code: 'SERVICE_UNAVAILABLE' })).toBe(true)
    expect(isFilesystemUnavailableError({ status: 500, code: 'INTERNAL' })).toBe(false)
    expect(isFilesystemUnavailableError(null)).toBe(false)
  })

  it('returns a warning toast for unavailable filesystem downloads', () => {
    expect(getFileDownloadErrorToast({ status: 503 })).toEqual({
      title: '下载暂不可用',
      description: '文件系统当前不可用，请检查系统健康状态或稍后重试。',
      color: 'warning',
    })
  })

  it('returns a danger toast with the original error message for ordinary download failures', () => {
    expect(getFileDownloadErrorToast(new Error('permission denied'))).toEqual({
      title: '下载失败',
      description: 'permission denied',
      color: 'danger',
    })
  })

  it('uses a generic description for unknown ordinary download failures', () => {
    expect(getFileDownloadErrorToast('boom')).toEqual({
      title: '下载失败',
      description: '请稍后重试',
      color: 'danger',
    })
  })
})
