import type { User } from '@/api/auth'
import { resolveUserHomeScope } from '@/lib/userScope'

export function getFileQueryScopeKey(user: Pick<User, 'id' | 'role' | 'homeDir'> | null | undefined): string {
  const { rootPath, hasInvalidHomeDir } = resolveUserHomeScope(user)
  const effectiveRootPath = hasInvalidHomeDir ? '__invalid__' : (rootPath ?? '/')

  return `${user?.id ?? 'anonymous'}:${user?.role ?? 'guest'}:${effectiveRootPath}`
}

export function getFilesQueryKey(fileScopeKey: string, path: string) {
  return ['files', fileScopeKey, path] as const
}