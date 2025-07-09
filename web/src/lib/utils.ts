import { type ClassValue, clsx } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/**
 * Sanitize a filename to prevent path traversal and other security issues.
 * Removes path separators, null bytes, and other dangerous characters.
 */
export function sanitizeFilename(filename: string): string {
  // Remove null bytes
  let sanitized = filename.replace(/\0/g, '')
  
  // Remove path separators and parent directory references
  sanitized = sanitized.replace(/[/\\]/g, '_')
  sanitized = sanitized.replace(/\.\./g, '_')
  
  // Remove control characters (0x00-0x1F and 0x7F)
  // eslint-disable-next-line no-control-regex
  sanitized = sanitized.replace(/[\x00-\x1F\x7F]/g, '')
  
  // Trim leading/trailing dots and spaces (Windows compatibility)
  sanitized = sanitized.replace(/^[\s.]+|[\s.]+$/g, '')
  
  // Ensure we have a valid filename
  if (!sanitized || sanitized === '.' || sanitized === '..') {
    throw new Error('无效的文件名')
  }
  
  return sanitized
}

/**
 * Validate and normalize a path for API requests.
 * Ensures the path starts with / and doesn't contain dangerous sequences.
 */
export function normalizePath(path: string): string {
  // Remove null bytes
  let normalized = path.replace(/\0/g, '')
  
  // Ensure path starts with /
  if (!normalized.startsWith('/')) {
    normalized = '/' + normalized
  }
  
  // Remove double slashes
  normalized = normalized.replace(/\/+/g, '/')
  
  // Remove trailing slash (except for root)
  if (normalized.length > 1 && normalized.endsWith('/')) {
    normalized = normalized.slice(0, -1)
  }
  
  // Check for path traversal attempts
  if (normalized.includes('/../') || normalized.endsWith('/..') || normalized === '/..') {
    throw new Error('非法路径')
  }
  
  return normalized
}

export function normalizeUserHomeDir(homeDir: string): string {
  const trimmed = homeDir.trim()
  if (!trimmed) {
    throw new Error('非法主目录')
  }

  return normalizePath(trimmed.replace(/\\/g, '/'))
}

/**
 * Normalize WebDAV prefix input for config updates.
 */
export function normalizeWebDAVPrefix(prefix: string): string {
  const trimmed = prefix.trim()
  if (!trimmed || trimmed === '/') {
    return '/'
  }
  const withSlash = trimmed.startsWith('/') ? trimmed : `/${trimmed}`
  return withSlash.endsWith('/') ? withSlash.slice(0, -1) : withSlash
}

/**
 * Format a WebDAV URL for display and copy.
 */
export function formatWebDAVUrl(origin: string, url: string): string {
  const trimmed = url.trim()
  if (!trimmed) {
    return origin
  }
  if (/^https?:\/\//i.test(trimmed)) {
    return trimmed
  }
  const normalizedOrigin = origin.endsWith('/') ? origin.slice(0, -1) : origin
  const normalizedPath = trimmed.startsWith('/') ? trimmed : `/${trimmed}`
  return `${normalizedOrigin}${normalizedPath}`
}

/**
 * Copy text to clipboard with a fallback for restricted environments.
 */
export async function copyTextToClipboard(text: string): Promise<void> {
  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text)
    return
  }

  if (typeof document === 'undefined') {
    throw new Error('剪贴板不可用')
  }

  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', 'true')
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  textarea.style.pointerEvents = 'none'
  document.body.appendChild(textarea)
  textarea.select()

  try {
    const success = typeof document.execCommand === 'function' && document.execCommand('copy')
    if (!success) {
      throw new Error('剪贴板不可用')
    }
  } finally {
    document.body.removeChild(textarea)
  }
}

/**
 * Open a URL in a new tab and report whether the browser allowed it.
 */
export function openUrlInNewTab(url: string): boolean {
  if (typeof window === 'undefined' || typeof window.open !== 'function') {
    return false
  }

  const newWindow = window.open(url, '_blank', 'noopener,noreferrer')
  if (newWindow) {
    newWindow.opener = null
    return true
  }

  return false
}

/**
 * Encode path segments for URL use while preserving the path structure.
 */
export function encodePathForUrl(path: string): string {
  return path
    .split('/')
    .map(segment => encodeURIComponent(segment))
    .join('/')
}

/**
 * Decode path segments from URL use while preserving the path structure.
 */
export function decodePathFromUrl(path: string): string {
  return path
    .split('/')
    .map(segment => decodeURIComponent(segment))
    .join('/')
}

export function formatBytes(bytes: number, decimals = 2): string {
  if (bytes === 0) return '0 B'

  const k = 1024
  const dm = decimals < 0 ? 0 : decimals
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

  const i = Math.floor(Math.log(bytes) / Math.log(k))

  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`
}

export function parseByteSize(value: string): number {
  const trimmed = value.trim()
  if (!trimmed) {
    throw new Error('无效的大小')
  }

  const normalized = trimmed.replace(/\s+/g, '').toUpperCase()
  const match = normalized.match(/^(\d+(?:\.\d+)?)(B|KB|MB|GB|TB|PB)?$/)
  if (!match) {
    throw new Error('无效的大小格式')
  }

  const amount = Number(match[1])
  const unit = match[2] || 'B'
  const multipliers: Record<string, number> = {
    B: 1,
    KB: 1024,
    MB: 1024 ** 2,
    GB: 1024 ** 3,
    TB: 1024 ** 4,
    PB: 1024 ** 5,
  }

  const multiplier = multipliers[unit]
  if (!multiplier || Number.isNaN(amount)) {
    throw new Error('无效的大小格式')
  }

  return Math.round(amount * multiplier)
}

export function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms} 毫秒`
  
  const seconds = Math.floor(ms / 1000)
  if (seconds < 60) return `${seconds} 秒`
  
  const minutes = Math.floor(seconds / 60)
  const remainingSecs = seconds % 60
  if (minutes < 60) {
    return remainingSecs > 0 ? `${minutes} 分 ${remainingSecs} 秒` : `${minutes} 分钟`
  }
  
  const hours = Math.floor(minutes / 60)
  const remainingMins = minutes % 60
  return remainingMins > 0 ? `${hours} 小时 ${remainingMins} 分钟` : `${hours} 小时`
}

export function formatDate(dateStr: string): string {
  const date = new Date(dateStr)
  return date.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}

export function formatRelativeTime(dateStr: string): string {
  const date = new Date(dateStr)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffSeconds = Math.floor(diffMs / 1000)
  const diffMinutes = Math.floor(diffSeconds / 60)
  const diffHours = Math.floor(diffMinutes / 60)
  const diffDays = Math.floor(diffHours / 24)

  if (diffSeconds < 60) return '刚刚'
  if (diffMinutes < 60) return `${diffMinutes} 分钟前`
  if (diffHours < 24) return `${diffHours} 小时前`
  if (diffDays === 1) return '昨天'
  if (diffDays < 7) return `${diffDays} 天前`
  if (diffDays < 30) return `${Math.floor(diffDays / 7)} 周前`
  return date.toLocaleDateString('zh-CN')
}

export function getFileIcon(name: string, isDir: boolean): string {
  if (isDir) return 'folder'
  
  const ext = name.split('.').pop()?.toLowerCase()
  
  const imageExts = ['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'ico', 'bmp']
  const videoExts = ['mp4', 'mkv', 'avi', 'mov', 'wmv', 'flv', 'webm']
  const audioExts = ['mp3', 'wav', 'flac', 'aac', 'ogg', 'm4a']
  const docExts = ['pdf', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx']
  const codeExts = ['js', 'ts', 'jsx', 'tsx', 'py', 'go', 'rs', 'java', 'c', 'cpp', 'h']
  const archiveExts = ['zip', 'rar', '7z', 'tar', 'gz', 'bz2']
  
  if (imageExts.includes(ext || '')) return 'image'
  if (videoExts.includes(ext || '')) return 'video'
  if (audioExts.includes(ext || '')) return 'audio'
  if (docExts.includes(ext || '')) return 'document'
  if (codeExts.includes(ext || '')) return 'code'
  if (archiveExts.includes(ext || '')) return 'archive'
  
  return 'file'
}

export function isImageFile(name: string): boolean {
  const ext = name.split('.').pop()?.toLowerCase()
  const imageExts = ['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'ico', 'bmp', 'avif', 'heic', 'heif', 'tiff', 'tif']
  return imageExts.includes(ext || '')
}

export function isVideoFile(name: string): boolean {
  const ext = name.split('.').pop()?.toLowerCase()
  const videoExts = ['mp4', 'mkv', 'avi', 'mov', 'wmv', 'flv', 'webm']
  return videoExts.includes(ext || '')
}
