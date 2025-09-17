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

  // Avoid Windows device names, including names with extensions such as CON.txt.
  const baseName = sanitized.split('.')[0]?.toUpperCase()
  const reservedWindowsNames = new Set([
    'CON', 'PRN', 'AUX', 'NUL',
    'COM1', 'COM2', 'COM3', 'COM4', 'COM5', 'COM6', 'COM7', 'COM8', 'COM9',
    'LPT1', 'LPT2', 'LPT3', 'LPT4', 'LPT5', 'LPT6', 'LPT7', 'LPT8', 'LPT9',
  ])
  if (baseName && reservedWindowsNames.has(baseName)) {
    sanitized = `_${sanitized}`
  }
  
  // Ensure we have a valid filename
  if (!sanitized || sanitized === '.' || sanitized === '..') {
    throw new Error('无效的文件名')
  }
  
  return sanitized
}

function splitContentDispositionParts(header: string): string[] {
  const parts: string[] = []
  let current = ''
  let quoted = false
  let escaped = false

  for (const char of header) {
    if (escaped) {
      current += char
      escaped = false
      continue
    }
    if (quoted && char === '\\') {
      current += char
      escaped = true
      continue
    }
    if (char === '"') {
      quoted = !quoted
      current += char
      continue
    }
    if (char === ';' && !quoted) {
      parts.push(current.trim())
      current = ''
      continue
    }
    current += char
  }

  if (current.trim()) {
    parts.push(current.trim())
  }

  return parts
}

function unquoteContentDispositionValue(value: string): string {
  const trimmed = value.trim()
  if (trimmed.length < 2 || !trimmed.startsWith('"') || !trimmed.endsWith('"')) {
    return trimmed
  }

  let unquoted = ''
  for (let i = 1; i < trimmed.length - 1; i += 1) {
    const char = trimmed[i]
    if (char === '\\' && i + 1 < trimmed.length - 1) {
      i += 1
      unquoted += trimmed[i]
      continue
    }
    unquoted += char
  }
  return unquoted
}

function decodeExtendedFilename(value: string): string {
  const unquoted = unquoteContentDispositionValue(value)
  const match = unquoted.match(/^([^']*)'[^']*'(.*)$/)
  const charset = (match?.[1] ?? '').toLowerCase()
  const encoded = match?.[2] ?? unquoted

  if (charset && charset !== 'utf-8') {
    return encoded
  }

  try {
    return decodeURIComponent(encoded)
  } catch {
    return encoded
  }
}

export function getFilenameFromContentDisposition(contentDisposition: string | null, fallback: string): string {
  if (!contentDisposition) {
    return fallback
  }

  const params = new Map<string, string>()
  for (const part of splitContentDispositionParts(contentDisposition).slice(1)) {
    const equalsIndex = part.indexOf('=')
    if (equalsIndex <= 0) {
      continue
    }
    const key = part.slice(0, equalsIndex).trim().toLowerCase()
    const value = part.slice(equalsIndex + 1)
    params.set(key, value)
  }

  const extendedFilename = params.get('filename*')
  if (extendedFilename) {
    const decoded = decodeExtendedFilename(extendedFilename)
    if (decoded) {
      return decoded
    }
  }

  const filename = params.get('filename')
  if (filename) {
    const unquoted = unquoteContentDispositionValue(filename)
    if (unquoted) {
      return unquoted
    }
  }

  return fallback
}

export function ensureZipExtension(filename: string): string {
  const normalized = filename.trim().replace(/[.\s]+$/g, '')
  const baseFilename = normalized || 'download'
  return baseFilename.toLowerCase().endsWith('.zip') ? baseFilename : `${baseFilename}.zip`
}

/**
 * Validate and normalize a path for API requests.
 * Ensures the path starts with / and doesn't contain dangerous sequences.
 */
export function normalizePath(path: string): string {
  if (path.includes('\0')) {
    throw new Error('非法路径')
  }

  let normalized = path.replace(/\\/g, '/')
  
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
  
  // Reject dot segments so API paths have one canonical representation.
  const segments = normalized.split('/').filter(Boolean)
  if (segments.some((segment) => segment === '.' || segment === '..')) {
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

export function pathWithinBase(basePath: string, targetPath: string): boolean {
  const normalizedBase = normalizePath(basePath)
  const normalizedTarget = normalizePath(targetPath)

  if (normalizedBase === '/') {
    return normalizedTarget.startsWith('/')
  }

  return normalizedTarget === normalizedBase || normalizedTarget.startsWith(`${normalizedBase}/`)
}

/**
 * Normalize WebDAV prefix input for config updates.
 */
export function normalizeWebDAVPrefix(prefix: string): string {
  const trimmed = prefix.trim()
  if (!trimmed || trimmed === '/') {
    return '/'
  }

  let current = trimmed.startsWith('/') ? trimmed : `/${trimmed}`
  for (;;) {
    const next = cleanURLPathPrefix(current.trim())
    if (next === current) {
      return next
    }
    current = next
  }
}

function cleanURLPathPrefix(prefix: string): string {
  const parts: string[] = []
  for (const rawSegment of prefix.split('/')) {
    const segment = rawSegment.trim()
    if (!segment || segment === '.') {
      continue
    }
    if (segment === '..') {
      parts.pop()
      continue
    }
    parts.push(segment)
  }

  return parts.length === 0 ? '/' : `/${parts.join('/')}`
}

export function isValidWebDAVPrefix(prefix: string): boolean {
  const normalized = normalizeWebDAVPrefix(prefix)
  if (/[\\?#]/.test(normalized)) {
    return false
  }
  for (let index = 0; index < normalized.length; index += 1) {
    const code = normalized.charCodeAt(index)
    if (code <= 0x1f || code === 0x7f) {
      return false
    }
  }
  return true
}

export function webDAVPrefixOverlapsReservedRoute(prefix: string): boolean {
  const normalized = normalizeWebDAVPrefix(prefix)
  if (normalized === '/') {
    return true
  }
  return ['/api', '/s', '/health'].some((reserved) => (
    normalized === reserved || normalized.startsWith(`${reserved}/`)
  ))
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
  if (!isSafeNewTabUrl(url)) {
    return false
  }

  const newWindow = window.open(url, '_blank', 'noopener,noreferrer')
  if (newWindow) {
    newWindow.opener = null
    return true
  }

  return false
}

function isSafeNewTabUrl(url: string): boolean {
  try {
    const parsed = new URL(url, window.location.origin)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' || parsed.protocol === 'blob:'
  } catch {
    return false
  }
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
  if (!Number.isFinite(bytes)) return '--'
  if (bytes === 0) return '0 B'

  const k = 1024
  const dm = decimals < 0 ? 0 : decimals
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  const sign = bytes < 0 ? '-' : ''
  const absoluteBytes = Math.abs(bytes)

  const i = Math.min(
    Math.max(0, Math.floor(Math.log(absoluteBytes) / Math.log(k))),
    sizes.length - 1,
  )

  return `${sign}${parseFloat((absoluteBytes / Math.pow(k, i)).toFixed(dm))} ${sizes[i]}`
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

export function formatUptimeSeconds(value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value)) {
    return '--'
  }

  const totalSeconds = Math.max(0, Math.floor(value))
  const days = Math.floor(totalSeconds / 86400)
  const hours = Math.floor((totalSeconds % 86400) / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60

  if (days > 0) {
    return hours > 0 ? `${days} 天 ${hours} 小时` : `${days} 天`
  }
  if (hours > 0) {
    return minutes > 0 ? `${hours} 小时 ${minutes} 分钟` : `${hours} 小时`
  }
  if (minutes > 0) {
    return seconds > 0 ? `${minutes} 分 ${seconds} 秒` : `${minutes} 分钟`
  }
  return `${seconds} 秒`
}

export function formatDate(dateStr: string): string {
  const date = new Date(dateStr)
  if (Number.isNaN(date.getTime())) {
    return '--'
  }
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
  if (Number.isNaN(date.getTime())) {
    return '--'
  }
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
  
  const imageExts = ['jpg', 'jpeg', 'png', 'gif', 'webp', 'svg', 'ico', 'bmp', 'avif', 'heic', 'heif', 'tiff', 'tif']
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
