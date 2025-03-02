import { describe, it, expect } from 'vitest'
import { vi } from 'vitest'
import { 
  copyTextToClipboard,
  formatBytes, 
  parseByteSize,
  openUrlInNewTab,
  formatDate, 
  formatDuration,
  sanitizeFilename,
  normalizePath,
  normalizeWebDAVPrefix,
  formatWebDAVUrl,
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
})

describe('formatBytes', () => {
  it('formats 0 bytes', () => {
    expect(formatBytes(0)).toBe('0 B')
  })

  it('formats bytes', () => {
    expect(formatBytes(512)).toBe('512 B')
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
  })
})

describe('normalizeWebDAVPrefix', () => {
  it('ensures prefix starts with /', () => {
    expect(normalizeWebDAVPrefix('dav')).toBe('/dav')
  })

  it('trims trailing slash', () => {
    expect(normalizeWebDAVPrefix('/dav/')).toBe('/dav')
  })

  it('returns / for empty or root input', () => {
    expect(normalizeWebDAVPrefix('')).toBe('/')
    expect(normalizeWebDAVPrefix(' / ')).toBe('/')
    expect(normalizeWebDAVPrefix('/')).toBe('/')
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
    })
  })
})

describe('getFileIcon', () => {
  it('returns folder for directories', () => {
    expect(getFileIcon('folder', true)).toBe('folder')
  })

  it('returns image for image files', () => {
    expect(getFileIcon('photo.jpg', false)).toBe('image')
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
})
