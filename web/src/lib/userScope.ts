import type { User } from '@/api/auth'
import { normalizeUserHomeDir } from '@/lib/utils'

export interface UserHomeScope {
  rootPath: string | null
  scopedHomeDir: string | null
  hasInvalidHomeDir: boolean
}

export const invalidHomeDirTitle = '主目录配置无效'

export function getInvalidHomeDirDescription(action: string): string {
  return `当前账户未配置有效的主目录，无法${action}。请联系管理员修复账户 home_dir。`
}

export function resolveUserHomeScope(user: Pick<User, 'role' | 'homeDir'> | null | undefined): UserHomeScope {
  if (!user || user.role === 'admin') {
    return {
      rootPath: '/',
      scopedHomeDir: null,
      hasInvalidHomeDir: false,
    }
  }

  const rawHomeDir = typeof user.homeDir === 'string' ? user.homeDir.trim() : ''
  if (!rawHomeDir) {
    return {
      rootPath: null,
      scopedHomeDir: null,
      hasInvalidHomeDir: true,
    }
  }

  try {
    const normalizedHomeDir = normalizeUserHomeDir(rawHomeDir)
    return {
      rootPath: normalizedHomeDir,
      scopedHomeDir: normalizedHomeDir,
      hasInvalidHomeDir: false,
    }
  } catch {
    return {
      rootPath: null,
      scopedHomeDir: null,
      hasInvalidHomeDir: true,
    }
  }
}