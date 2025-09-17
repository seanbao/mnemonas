import { describe, expect, it } from 'vitest'
import {
  INVALID_PATH_SEGMENT_NAME_DESCRIPTION,
  getPathSegmentNameValidationError,
  joinPathSegment,
} from './pathSegmentName'

describe('path segment name validation', () => {
  it('accepts regular file and folder names', () => {
    expect(getPathSegmentNameValidationError('report.txt')).toBeNull()
    expect(getPathSegmentNameValidationError('家庭 照片')).toBeNull()
    expect(getPathSegmentNameValidationError('  trimmed-name  ')).toBeNull()
  })

  it('uses a contextual empty-name message', () => {
    expect(getPathSegmentNameValidationError('   ', '请输入文件夹名称')).toBe('请输入文件夹名称')
  })

  it.each(['.', '..', 'folder/child', 'folder\\child', 'bad\0name'])(
    'rejects unsafe path segment name %s',
    (name) => {
      expect(getPathSegmentNameValidationError(name)).toBe(INVALID_PATH_SEGMENT_NAME_DESCRIPTION)
    }
  )

  it('joins a normalized parent path and a single child segment', () => {
    expect(joinPathSegment('/', 'report.txt')).toBe('/report.txt')
    expect(joinPathSegment('/docs/', ' report.txt ')).toBe('/docs/report.txt')
    expect(joinPathSegment('docs', '.env')).toBe('/docs/.env')
  })

  it('rejects unsafe child names while joining paths', () => {
    expect(() => joinPathSegment('/docs', '../escape')).toThrow(INVALID_PATH_SEGMENT_NAME_DESCRIPTION)
    expect(() => joinPathSegment('/docs', 'child\\nested')).toThrow(INVALID_PATH_SEGMENT_NAME_DESCRIPTION)
  })

  it('rejects unsafe parent paths while joining paths', () => {
    expect(() => joinPathSegment('/docs/./drafts', 'report.txt')).toThrow('非法路径')
  })
})
