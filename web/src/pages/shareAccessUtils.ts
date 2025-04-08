import type { PublicShareInfo } from '@/api/share'

export function getFolderPathAfterShareAuth(currentFolderPath: string, info: PublicShareInfo): string {
  if (info.type !== 'folder') {
    return ''
  }

  return currentFolderPath
}