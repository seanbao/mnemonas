import { normalizePath } from './utils'

export const INVALID_PATH_SEGMENT_NAME_DESCRIPTION = '名称不能包含路径分隔符、空字符，且不能为 . 或 ..。'

export function getPathSegmentNameValidationError(name: string, emptyDescription = '请输入名称'): string | null {
  const trimmed = name.trim()
  if (!trimmed) {
    return emptyDescription
  }

  if (
    trimmed === '.'
    || trimmed === '..'
    || trimmed.includes('/')
    || trimmed.includes('\\')
    || trimmed.includes('\0')
  ) {
    return INVALID_PATH_SEGMENT_NAME_DESCRIPTION
  }

  return null
}

export function joinPathSegment(parentPath: string, name: string): string {
  const validationError = getPathSegmentNameValidationError(name)
  if (validationError) {
    throw new Error(validationError)
  }

  const normalizedParentPath = normalizePath(parentPath)
  const trimmedName = name.trim()
  return normalizedParentPath === '/' ? `/${trimmedName}` : `${normalizedParentPath}/${trimmedName}`
}
