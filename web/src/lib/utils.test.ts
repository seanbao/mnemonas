import { describe, it, expect } from 'vitest'
import { vi } from 'vitest'
import { 
  copyTextToClipboard,
  formatBytes, 
  parseByteSize,
  openUrlInNewTab,
  formatDate, 
  formatDuration,
  formatUptimeSeconds,
  formatRelativeTime,
  sanitizeFilename,
  normalizePath,
  normalizeWebDAVPrefix,
  isValidWebDAVPrefix,
  webDAVPrefixOverlapsReservedRoute,
  formatWebDAVUrl,
  ensureZipExtension,
  getFilenameFromContentDisposition,
  encodePathForUrl,
  decodePathFromUrl,
  isImageFile,
  isVideoFile,
  getFileIcon,
  cn
} from './utils'

describe('copyTextToClipboard', () => {
  it('uses navigator clipboard when available', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })

    await expect(copyTextToClipboard('hello')).resolves.toBeUndefined()
    expect(writeText).toHaveBeenCalledWith('hello')
  })

  it('falls back to document.execCommand when clipboard api is unavailable', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: undefined,
    })
    const execCommandMock = vi.fn().mockReturnValue(true)
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: execCommandMock,
    })

    await expect(copyTextToClipboard('fallback')).resolves.toBeUndefined()
    expect(execCommandMock).toHaveBeenCalledWith('copy')
  })

  it('rejects when both clipboard strategies fail', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: undefined,
    })
    const execCommandMock = vi.fn().mockReturnValue(false)
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: execCommandMock,
    })

    await expect(copyTextToClipboard('fail')).rejects.toThrow('剪贴板不可用')
    expect(execCommandMock).toHaveBeenCalledWith('copy')
  })
})

describe('openUrlInNewTab', () => {
  it('returns true when browser allows opening a new tab', () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue({ opener: window } as unknown as Window)

    expect(openUrlInNewTab('/download')).toBe(true)
    expect(openSpy).toHaveBeenCalledWith('/download', '_blank', 'noopener,noreferrer')

    openSpy.mockRestore()
  })

  it('returns false when browser blocks the new tab', () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null)

    expect(openUrlInNewTab('/download')).toBe(false)

    openSpy.mockRestore()
  })

  it('returns false when window.open is unavailable', () => {
    const originalOpen = window.open
    Object.defineProperty(window, 'open', {
      configurable: true,
      value: undefined,
    })

    try {
      expect(openUrlInNewTab('/download')).toBe(false)
    } finally {
      Object.defineProperty(window, 'open', {
        configurable: true,
        value: originalOpen,
      })
    }
  })

  it('refuses script and local-file URLs', () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue({ opener: window } as unknown as Window)

    expect(openUrlInNewTab('javascript:alert(1)')).toBe(false)
    expect(openUrlInNewTab('file:///etc/passwd')).toBe(false)
    expect(openSpy).not.toHaveBeenCalled()

    openSpy.mockRestore()
  })
})

describe('formatBytes', () => {
  it('formats 0 bytes', () => {
    expect(formatBytes(0)).toBe('0 B')
  })

  it('formats bytes', () => {
    expect(formatBytes(512)).toBe('512 B')
    expect(formatBytes(0.5)).toBe('0.5 B')
  })

  it('formats kilobytes', () => {
    expect(formatBytes(1024)).toBe('1 KB')
    expect(formatBytes(1536)).toBe('1.5 KB')
  })

  it('formats megabytes', () => {
    expect(formatBytes(1048576)).toBe('1 MB')
    expect(formatBytes(1572864)).toBe('1.5 MB')
  })

  it('formats gigabytes', () => {
    expect(formatBytes(1073741824)).toBe('1 GB')
  })

  it('respects decimals parameter', () => {
    expect(formatBytes(1536, 0)).toBe('2 KB')
    expect(formatBytes(1536, 3)).toBe('1.5 KB')
  })

  it('clamps negative decimals to zero', () => {
    expect(formatBytes(1536, -2)).toBe('2 KB')
  })

  it('formats negative byte counts without producing NaN labels', () => {
    expect(formatBytes(-512)).toBe('-512 B')
    expect(formatBytes(-1536)).toBe('-1.5 KB')
  })

  it('falls back for non-finite byte counts', () => {
    expect(formatBytes(Number.NaN)).toBe('--')
    expect(formatBytes(Number.POSITIVE_INFINITY)).toBe('--')
  })
})

describe('parseByteSize', () => {
  it('parses bytes without unit', () => {
    expect(parseByteSize('1024')).toBe(1024)
  })

  it('parses sizes with units', () => {
    expect(parseByteSize('1 KB')).toBe(1024)
    expect(parseByteSize('1.5MB')).toBe(1572864)
    expect(parseByteSize('2 GB')).toBe(2147483648)
  })

  it('rejects invalid sizes', () => {
    expect(() => parseByteSize('')).toThrow('无效的大小')
    expect(() => parseByteSize('abc')).toThrow('无效的大小格式')
  })
})

describe('formatDuration', () => {
  it('formats sub-second durations as milliseconds', () => {
    expect(formatDuration(500)).toBe('500 毫秒')
  })

  it('formats milliseconds to seconds', () => {
    expect(formatDuration(5000)).toBe('5 秒')
  })

  it('formats milliseconds to minutes', () => {
    expect(formatDuration(120000)).toBe('2 分钟')
  })

  it('formats milliseconds to hours', () => {
    expect(formatDuration(7200000)).toBe('2 小时')
  })

  it('formats mixed durations', () => {
    expect(formatDuration(3660000)).toBe('1 小时 1 分钟')
    expect(formatDuration(90000)).toBe('1 分 30 秒')
  })
})

describe('formatUptimeSeconds', () => {
  it('formats second and minute level uptime', () => {
    expect(formatUptimeSeconds(0)).toBe('0 秒')
    expect(formatUptimeSeconds(59.9)).toBe('59 秒')
    expect(formatUptimeSeconds(90)).toBe('1 分 30 秒')
  })

  it('formats hour and day level uptime', () => {
    expect(formatUptimeSeconds(5400)).toBe('1 小时 30 分钟')
    expect(formatUptimeSeconds(86400)).toBe('1 天')
    expect(formatUptimeSeconds(90000)).toBe('1 天 1 小时')
  })

  it('falls back for unknown uptime values', () => {
    expect(formatUptimeSeconds(undefined)).toBe('--')
    expect(formatUptimeSeconds(Number.NaN)).toBe('--')
  })
})

describe('formatRelativeTime', () => {
  it('formats recent and older timestamps relative to now', () => {
    vi.useFakeTimers()
    try {
      vi.setSystemTime(new Date('2026-05-04T12:00:00Z'))

      expect(formatRelativeTime('2026-05-04T11:59:30Z')).toBe('刚刚')
      expect(formatRelativeTime('2026-05-04T11:30:00Z')).toBe('30 分钟前')
      expect(formatRelativeTime('2026-05-04T09:00:00Z')).toBe('3 小时前')
      expect(formatRelativeTime('2026-05-03T12:00:00Z')).toBe('昨天')
      expect(formatRelativeTime('2026-05-01T12:00:00Z')).toBe('3 天前')
      expect(formatRelativeTime('2026-04-20T12:00:00Z')).toBe('2 周前')
      expect(formatRelativeTime('2026-03-01T12:00:00Z')).toContain('2026')
    } finally {
      vi.useRealTimers()
    }
  })

  it('falls back for invalid timestamps', () => {
    expect(formatRelativeTime('not-a-date')).toBe('--')
  })
})

describe('sanitizeFilename', () => {
  it('removes path separators', () => {
    expect(sanitizeFilename('path/to/file.txt')).toBe('path_to_file.txt')
    expect(sanitizeFilename('path\\to\\file.txt')).toBe('path_to_file.txt')
  })

  it('removes parent directory references', () => {
    expect(sanitizeFilename('../../../etc/passwd')).toBe('______etc_passwd')
  })

  it('throws error for empty or whitespace names', () => {
    expect(() => sanitizeFilename('')).toThrow('无效的文件名')
    expect(() => sanitizeFilename('   ')).toThrow('无效的文件名')
  })

  it('preserves valid filenames', () => {
    expect(sanitizeFilename('valid-file.txt')).toBe('valid-file.txt')
    expect(sanitizeFilename('document.pdf')).toBe('document.pdf')
  })

  it('prefixes reserved Windows device filenames', () => {
    expect(sanitizeFilename('CON')).toBe('_CON')
    expect(sanitizeFilename('nul.txt')).toBe('_nul.txt')
    expect(sanitizeFilename('COM1.log')).toBe('_COM1.log')
    expect(sanitizeFilename('LPT9')).toBe('_LPT9')
  })
})

describe('getFilenameFromContentDisposition', () => {
  it('uses decoded UTF-8 extended filenames first', () => {
    expect(getFilenameFromContentDisposition(
      'attachment; filename="fallback.txt"; filename*=UTF-8\'\'report%20final.txt',
      'download'
    )).toBe('report final.txt')
  })

  it('unescapes quoted filenames with quotes and semicolons', () => {
    expect(getFilenameFromContentDisposition(
      'attachment; filename="report\\"2026;final.pdf"',
      'download'
    )).toBe('report"2026;final.pdf')
  })

  it('falls back for invalid or missing filename parameters', () => {
    expect(getFilenameFromContentDisposition('attachment', 'download')).toBe('download')
    expect(getFilenameFromContentDisposition(null, 'download')).toBe('download')
  })
})

describe('ensureZipExtension', () => {
  it('adds a zip extension only when it is missing', () => {
    expect(ensureZipExtension('docs')).toBe('docs.zip')
    expect(ensureZipExtension('backups.zip')).toBe('backups.zip')
    expect(ensureZipExtension('BACKUPS.ZIP')).toBe('BACKUPS.ZIP')
  })

  it('normalizes surrounding whitespace and trailing dots before adding the zip extension', () => {
    expect(ensureZipExtension(' family photos ')).toBe('family photos.zip')
    expect(ensureZipExtension('family photos.zip ')).toBe('family photos.zip')
    expect(ensureZipExtension('export.')).toBe('export.zip')
  })

  it('uses a stable archive fallback for blank filenames', () => {
    expect(ensureZipExtension('')).toBe('download.zip')
    expect(ensureZipExtension('   ')).toBe('download.zip')
  })
})

describe('normalizePath', () => {
  it('ensures path starts with /', () => {
    expect(normalizePath('path/to/file')).toBe('/path/to/file')
  })

  it('removes trailing slashes', () => {
    expect(normalizePath('/path/to/file/')).toBe('/path/to/file')
  })

  it('handles root path', () => {
    expect(normalizePath('/')).toBe('/')
    expect(normalizePath('')).toBe('/')
  })

  it('collapses multiple slashes', () => {
    expect(normalizePath('//path//to//file')).toBe('/path/to/file')
  })

  it('throws on path traversal attempts', () => {
    expect(() => normalizePath('/../etc/passwd')).toThrow('非法路径')
    expect(() => normalizePath('//../etc/passwd')).toThrow('非法路径')
    expect(() => normalizePath('/safe\\..\\secret.txt')).toThrow('非法路径')
  })

  it('throws on null byte paths instead of silently rewriting them', () => {
    expect(() => normalizePath('/docs/report\0.pdf')).toThrow('非法路径')
  })

  it('normalizes backslashes as path separators', () => {
    expect(normalizePath('docs\\report.txt')).toBe('/docs/report.txt')
  })
})

describe('normalizeWebDAVPrefix', () => {
  it('ensures prefix starts with /', () => {
    expect(normalizeWebDAVPrefix('dav')).toBe('/dav')
  })

  it('trims trailing slash', () => {
    expect(normalizeWebDAVPrefix('/dav/')).toBe('/dav')
  })

  it('cleans repeated slashes, dot segments, and padded path segments', () => {
    expect(normalizeWebDAVPrefix('//dav//files///')).toBe('/dav/files')
    expect(normalizeWebDAVPrefix('./0/0//0 /')).toBe('/0/0/0')
    expect(normalizeWebDAVPrefix('00/ /')).toBe('/00')
    expect(normalizeWebDAVPrefix('safe/../api')).toBe('/api')
  })

  it('returns / for empty or root input', () => {
    expect(normalizeWebDAVPrefix('')).toBe('/')
    expect(normalizeWebDAVPrefix(' / ')).toBe('/')
    expect(normalizeWebDAVPrefix('/')).toBe('/')
  })

  it('rejects characters that cannot be used in an HTTP path prefix', () => {
    expect(isValidWebDAVPrefix('/dav')).toBe(true)
    expect(isValidWebDAVPrefix('/dav\\files')).toBe(false)
    expect(isValidWebDAVPrefix('/dav?files')).toBe(false)
    expect(isValidWebDAVPrefix('/dav#files')).toBe(false)
    expect(isValidWebDAVPrefix('/dav\nfiles')).toBe(false)
  })

  it('detects prefixes that overlap reserved application routes', () => {
    expect(webDAVPrefixOverlapsReservedRoute('/')).toBe(true)
    expect(webDAVPrefixOverlapsReservedRoute('/api')).toBe(true)
    expect(webDAVPrefixOverlapsReservedRoute('api/v1')).toBe(true)
    expect(webDAVPrefixOverlapsReservedRoute('/s/shared')).toBe(true)
    expect(webDAVPrefixOverlapsReservedRoute('/health')).toBe(true)
    expect(webDAVPrefixOverlapsReservedRoute('/dav')).toBe(false)
    expect(webDAVPrefixOverlapsReservedRoute('/api-files')).toBe(false)
  })
})

describe('formatWebDAVUrl', () => {
  it('returns absolute URL as-is', () => {
    expect(formatWebDAVUrl('https://localhost', 'https://example.com/dav')).toBe(
      'https://example.com/dav'
    )
  })

  it('combines origin and relative path', () => {
    expect(formatWebDAVUrl('https://localhost', '/dav/')).toBe('https://localhost/dav/')
    expect(formatWebDAVUrl('https://localhost/', 'dav')).toBe('https://localhost/dav')
  })

  it('falls back to origin when url is empty', () => {
    expect(formatWebDAVUrl('https://localhost', '')).toBe('https://localhost')
  })
})

describe('URL path helpers', () => {
  it('encodes special characters within path segments', () => {
    expect(encodePathForUrl('/docs/a #1?/report%.pdf')).toBe('/docs/a%20%231%3F/report%25.pdf')
  })

  it('decodes special characters within path segments', () => {
    expect(decodePathFromUrl('/docs/a%20%231%3F/report%25.pdf')).toBe('/docs/a #1?/report%.pdf')
  })

  it('throws on invalid encoded paths', () => {
    expect(() => decodePathFromUrl('/%E0%A4%A')).toThrow(URIError)
  })
})
describe('file type detection', () => {
  describe('isImageFile', () => {
    it('detects image files', () => {
      expect(isImageFile('photo.jpg')).toBe(true)
      expect(isImageFile('photo.JPEG')).toBe(true)
      expect(isImageFile('image.png')).toBe(true)
      expect(isImageFile('icon.gif')).toBe(true)
      expect(isImageFile('graphic.webp')).toBe(true)
      expect(isImageFile('icon.svg')).toBe(true)
    })

    it('rejects non-image files', () => {
      expect(isImageFile('document.txt')).toBe(false)
      expect(isImageFile('video.mp4')).toBe(false)
      expect(isImageFile('')).toBe(false)
    })
  })

  describe('isVideoFile', () => {
    it('detects video files', () => {
      expect(isVideoFile('movie.mp4')).toBe(true)
      expect(isVideoFile('clip.webm')).toBe(true)
      expect(isVideoFile('video.mkv')).toBe(true)
    })

    it('rejects non-video files', () => {
      expect(isVideoFile('photo.jpg')).toBe(false)
      expect(isVideoFile('song.mp3')).toBe(false)
      expect(isVideoFile('')).toBe(false)
    })
  })
})

describe('getFileIcon', () => {
  it('returns folder for directories', () => {
    expect(getFileIcon('folder', true)).toBe('folder')
  })

  it('returns image for image files', () => {
    expect(getFileIcon('photo.jpg', false)).toBe('image')
    expect(getFileIcon('photo.avif', false)).toBe('image')
    expect(getFileIcon('photo.heic', false)).toBe('image')
    expect(getFileIcon('scan.tiff', false)).toBe('image')
  })

  it('returns video for video files', () => {
    expect(getFileIcon('movie.mp4', false)).toBe('video')
  })

  it('returns audio for audio files', () => {
    expect(getFileIcon('song.mp3', false)).toBe('audio')
  })

  it('returns document for document files', () => {
    expect(getFileIcon('report.pdf', false)).toBe('document')
    expect(getFileIcon('sheet.xlsx', false)).toBe('document')
  })

  it('returns code for code files', () => {
    expect(getFileIcon('app.js', false)).toBe('code')
    expect(getFileIcon('main.ts', false)).toBe('code')
    expect(getFileIcon('app.py', false)).toBe('code')
  })

  it('returns archive for archive files', () => {
    expect(getFileIcon('backup.zip', false)).toBe('archive')
    expect(getFileIcon('package.tar', false)).toBe('archive')
  })

  it('returns file for unknown types', () => {
    expect(getFileIcon('unknown.xyz', false)).toBe('file')
    expect(getFileIcon('readme.txt', false)).toBe('file')
    expect(getFileIcon('', false)).toBe('file')
  })
})

describe('cn (classnames)', () => {
  it('merges class names', () => {
    expect(cn('foo', 'bar')).toBe('foo bar')
  })

  it('handles conditional classes', () => {
    const condition = false
    expect(cn('foo', condition && 'bar', 'baz')).toBe('foo baz')
    const trueCondition = true
    expect(cn('foo', trueCondition && 'bar', 'baz')).toBe('foo bar baz')
  })

  it('handles undefined and null', () => {
    expect(cn('foo', undefined, null, 'bar')).toBe('foo bar')
  })

  it('merges tailwind classes correctly', () => {
    // tailwind-merge should handle conflicting classes
    expect(cn('px-2', 'px-4')).toBe('px-4')
    expect(cn('text-red-500', 'text-blue-500')).toBe('text-blue-500')
  })
})

describe('formatDate', () => {
  it('formats ISO date string', () => {
    const result = formatDate('2024-01-15T10:30:00Z')
    expect(result).toMatch(/2024/)
  })

  it('handles Date object', () => {
    const date = new Date('2024-06-20T15:00:00Z')
    const result = formatDate(date.toISOString())
    expect(result).toMatch(/2024/)
  })

  it('falls back for invalid date strings', () => {
    expect(formatDate('not-a-date')).toBe('--')
  })
})
