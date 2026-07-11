import { describe, expect, it } from 'vitest'
import {
  getBrowserDownloadCapacityErrorToast,
  getFileDownloadErrorToast,
  getSharedBrowserDownloadCapacityErrorToast,
  getSharedArchiveDownloadErrorToast,
  getSharedMissingFileDownloadErrorToast,
  getQuotaExceededErrorToast,
  getPathConflictErrorToast,
  getSharedQuotaExceededErrorToast,
  getSharedPathConflictErrorToast,
  isFilesystemUnavailableError,
  isQuotaExceededError,
} from './fileActionErrors'
import { INVALID_API_RESPONSE_MESSAGE } from './apiMessages'
import { BrowserDownloadCapacityError, BROWSER_DOWNLOAD_CAPACITY_CODE } from './downloadResponse'

describe('fileActionErrors', () => {
  it('detects unavailable filesystem errors by status or code', () => {
    expect(isFilesystemUnavailableError({ status: 503 })).toBe(true)
    expect(isFilesystemUnavailableError({ code: 'SERVICE_UNAVAILABLE' })).toBe(true)
    expect(isFilesystemUnavailableError({ status: 500, code: 'INTERNAL' })).toBe(false)
    expect(isFilesystemUnavailableError(null)).toBe(false)
  })

  it('detects known file action error codes after trimming whitespace', () => {
    expect(isFilesystemUnavailableError({ code: ' SERVICE_UNAVAILABLE ' })).toBe(true)
    expect(isQuotaExceededError({ code: ' QUOTA_EXCEEDED ' })).toBe(true)
    expect(isFilesystemUnavailableError({ code: '   ' })).toBe(false)
    expect(isQuotaExceededError({ code: '   ' })).toBe(false)
  })

  it('detects quota errors by status or code', () => {
    expect(isQuotaExceededError({ status: 507 })).toBe(true)
    expect(isQuotaExceededError({ code: 'QUOTA_EXCEEDED' })).toBe(true)
    expect(isQuotaExceededError({ status: 500, code: 'INTERNAL' })).toBe(false)
    expect(isQuotaExceededError(null)).toBe(false)
  })

  it('returns a warning toast for unavailable filesystem downloads', () => {
    expect(getFileDownloadErrorToast({ status: 503 })).toEqual({
      title: '下载暂不可用',
      description: '文件系统当前不可用，请检查设备状态或稍后重试。',
      color: 'warning',
    })
  })

  it('returns actionable single and shared warnings when browser download capacity is exhausted', () => {
    const expected = {
      title: '需要刷新后继续下载',
      description: '当前页面已提交的下载已达到上限，请刷新页面后继续。',
      color: 'warning',
    }

    expect(getBrowserDownloadCapacityErrorToast(new BrowserDownloadCapacityError())).toEqual(expected)
    expect(getFileDownloadErrorToast({ code: BROWSER_DOWNLOAD_CAPACITY_CODE })).toEqual(expected)
    expect(getSharedBrowserDownloadCapacityErrorToast([
      new BrowserDownloadCapacityError(),
      { code: BROWSER_DOWNLOAD_CAPACITY_CODE },
    ])).toEqual(expected)
    expect(getSharedBrowserDownloadCapacityErrorToast([
      new BrowserDownloadCapacityError(),
      new Error('other failure'),
    ])).toBeNull()
  })

  it('returns a generic danger toast for ordinary download failures', () => {
    expect(getFileDownloadErrorToast(new Error('permission denied'))).toEqual({
      title: '下载失败',
      description: '操作未完成，请稍后重试。',
      color: 'danger',
    })
  })

  it('returns a warning toast when the download target no longer exists', () => {
    expect(getFileDownloadErrorToast({ status: 404, code: 'FILE_NOT_FOUND', message: 'file not found' })).toEqual({
      title: '文件已不存在',
      description: '该文件可能已被移动或删除，请刷新列表后重试。',
      color: 'warning',
    })
  })

  it('returns a stable invalid API response message for malformed download responses', () => {
    expect(getFileDownloadErrorToast(new Error(INVALID_API_RESPONSE_MESSAGE))).toEqual({
      title: '下载失败',
      description: INVALID_API_RESPONSE_MESSAGE,
      color: 'danger',
    })
  })

  it('returns actionable warnings for archive download conflicts', () => {
    expect(getFileDownloadErrorToast({
      status: 409,
      code: 'CONFLICT',
      message: '文件内容已变更，请刷新后重试',
    })).toEqual({
      title: '归档下载失败',
      description: '文件内容已变更，请刷新列表后重新下载。',
      color: 'warning',
    })
  })

  it('returns actionable warnings for archive size limits', () => {
    expect(getFileDownloadErrorToast({
      status: 413,
      code: 'PAYLOAD_TOO_LARGE',
      message: '归档内容过大',
    })).toEqual({
      title: '归档下载失败',
      description: '归档内容过大，请缩小选择范围后重试。',
      color: 'warning',
    })
  })

  it('returns an actionable warning when archive capacity is saturated', () => {
    expect(getFileDownloadErrorToast({
      status: 429,
      code: 'ARCHIVE_DOWNLOAD_RATE_LIMITED',
      message: '归档下载任务较多，请稍后重试',
    })).toEqual({
      title: '归档下载繁忙',
      description: '当前归档下载任务较多，请稍后重试。',
      color: 'warning',
    })
  })

  it('returns a shared batch warning for matching archive download failures', () => {
    expect(getSharedArchiveDownloadErrorToast([
      { status: 413, code: 'PAYLOAD_TOO_LARGE', message: '归档内容过大' },
      { status: 413, code: 'PAYLOAD_TOO_LARGE', message: 'archive content is too large' },
    ])).toEqual({
      title: '批量归档下载失败',
      description: '归档内容过大，请缩小选择范围后重试。',
      color: 'warning',
    })
  })

  it('returns a shared batch warning for missing file download failures', () => {
    expect(getSharedMissingFileDownloadErrorToast([
      { status: 404, code: 'FILE_NOT_FOUND', message: 'file not found' },
      { status: 404, code: 'FILE_NOT_FOUND', message: 'file not found' },
    ])).toEqual({
      title: '批量下载失败',
      description: '所选文件可能已被移动或删除，请刷新列表后重试。',
      color: 'warning',
    })

    expect(getSharedMissingFileDownloadErrorToast([
      { status: 404, code: 'FILE_NOT_FOUND', message: 'file not found' },
      new Error('download failed'),
    ])).toBeNull()
  })

  it('returns a generic batch warning for mixed archive download failures', () => {
    expect(getSharedArchiveDownloadErrorToast([
      { status: 413, code: 'PAYLOAD_TOO_LARGE', message: '归档内容过大' },
      { status: 409, code: 'CONFLICT', message: '文件内容已变更，请刷新后重试' },
    ])).toEqual({
      title: '批量归档下载失败',
      description: '多个归档下载失败，请缩小选择范围或刷新列表后重试。',
      color: 'warning',
    })
  })

  it('does not treat mixed archive and ordinary errors as a shared archive failure', () => {
    expect(getSharedArchiveDownloadErrorToast([
      { status: 413, code: 'PAYLOAD_TOO_LARGE', message: '归档内容过大' },
      new Error('download failed'),
    ])).toBeNull()
  })

  it('uses a generic description for unknown ordinary download failures', () => {
    expect(getFileDownloadErrorToast('boom')).toEqual({
      title: '下载失败',
      description: '操作未完成，请稍后重试。',
      color: 'danger',
    })
  })

  it('returns localized warnings for path conflict backend messages', () => {
    expect(getPathConflictErrorToast(new Error('resource already exists'))).toEqual({
      title: '同名项目已存在',
      description: '当前目录中已存在同名文件或文件夹，请使用其他名称。',
      color: 'warning',
    })

    expect(getPathConflictErrorToast({ message: 'parent path is not a directory' })).toEqual({
      title: '目标位置不可用',
      description: '当前目录状态已变更，请刷新列表后重试。',
      color: 'warning',
    })
  })

  it('returns localized warnings for trimmed backend messages', () => {
    expect(getPathConflictErrorToast({ message: ' resource already exists ' })).toEqual({
      title: '同名项目已存在',
      description: '当前目录中已存在同名文件或文件夹，请使用其他名称。',
      color: 'warning',
    })

    expect(getQuotaExceededErrorToast({ code: 'QUOTA_EXCEEDED', message: ' user quota exceeded ' })).toEqual({
      title: '容量配额不足',
      description: '当前用户的容量配额不足，请清理空间或调整用户配额后重试。',
      color: 'warning',
    })
  })

  it('returns localized warnings for quota backend errors', () => {
    expect(getQuotaExceededErrorToast({ status: 507, message: 'directory quota exceeded' })).toEqual({
      title: '容量配额不足',
      description: '目标目录的容量配额不足，请清理空间或调整目录配额后重试。',
      color: 'warning',
    })

    expect(getQuotaExceededErrorToast({ code: 'QUOTA_EXCEEDED', message: 'user quota exceeded' })).toEqual({
      title: '容量配额不足',
      description: '当前用户的容量配额不足，请清理空间或调整用户配额后重试。',
      color: 'warning',
    })
  })

  it('ignores ordinary non-conflict errors', () => {
    expect(getPathConflictErrorToast(new Error('permission denied'))).toBeNull()
  })

  it('returns a shared conflict warning only when every error has the same known path conflict', () => {
    expect(getSharedPathConflictErrorToast([
      new Error('resource already exists'),
      { message: 'resource already exists' },
    ])).toEqual({
      title: '同名项目已存在',
      description: '当前目录中已存在同名文件或文件夹，请使用其他名称。',
      color: 'warning',
    })

    expect(getSharedPathConflictErrorToast([
      new Error('resource already exists'),
      new Error('parent path is not a directory'),
    ])).toBeNull()
    expect(getSharedPathConflictErrorToast([new Error('copy failed')])).toBeNull()
  })

  it('returns a shared quota warning only when every error is the same known quota error', () => {
    expect(getSharedQuotaExceededErrorToast([
      { status: 507, message: 'directory quota exceeded' },
      { code: 'QUOTA_EXCEEDED', message: 'directory quota exceeded' },
    ])).toEqual({
      title: '容量配额不足',
      description: '目标目录的容量配额不足，请清理空间或调整目录配额后重试。',
      color: 'warning',
    })

    expect(getSharedQuotaExceededErrorToast([
      { status: 507, message: 'directory quota exceeded' },
      { status: 507, message: 'user quota exceeded' },
    ])).toBeNull()
    expect(getSharedQuotaExceededErrorToast([new Error('copy failed')])).toBeNull()
  })
})
