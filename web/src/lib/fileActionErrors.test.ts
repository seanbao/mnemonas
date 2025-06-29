import { describe, expect, it } from 'vitest'
import {
  getFileDownloadErrorToast,
  getQuotaExceededErrorToast,
  getPathConflictErrorToast,
  getSharedQuotaExceededErrorToast,
  getSharedPathConflictErrorToast,
  isFilesystemUnavailableError,
  isQuotaExceededError,
} from './fileActionErrors'
import { INVALID_API_RESPONSE_MESSAGE } from './apiMessages'

describe('fileActionErrors', () => {
  it('detects unavailable filesystem errors by status or code', () => {
    expect(isFilesystemUnavailableError({ status: 503 })).toBe(true)
    expect(isFilesystemUnavailableError({ code: 'SERVICE_UNAVAILABLE' })).toBe(true)
    expect(isFilesystemUnavailableError({ status: 500, code: 'INTERNAL' })).toBe(false)
    expect(isFilesystemUnavailableError(null)).toBe(false)
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

  it('returns a generic danger toast for ordinary download failures', () => {
    expect(getFileDownloadErrorToast(new Error('permission denied'))).toEqual({
      title: '下载失败',
      description: '操作未完成，请稍后重试。',
      color: 'danger',
    })
  })

  it('returns a stable invalid API response message for malformed download responses', () => {
    expect(getFileDownloadErrorToast(new Error(INVALID_API_RESPONSE_MESSAGE))).toEqual({
      title: '下载失败',
      description: INVALID_API_RESPONSE_MESSAGE,
      color: 'danger',
    })
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
