import { sanitizeFilename } from '@/lib/utils'
import {
  readStructuredJsonErrorDetails,
  type StructuredJsonErrorDetails,
  type StructuredJsonErrorOptions,
} from './jsonErrorResponse'

export type DownloadJsonErrorDetails = StructuredJsonErrorDetails

export interface DownloadJsonErrorOptions extends StructuredJsonErrorOptions {
  contentDisposition?: string | null
}

export type DownloadErrorProbeFetcher = (url: string, options: RequestInit) => Promise<Response>

export const DOWNLOAD_PROBE_HEADER = 'X-Mnemonas-Download-Probe'
export const DOWNLOAD_PROBE_HEADER_VALUE = 'json-error'
export const MAX_RETAINED_DOWNLOAD_FRAMES = 64
export const BROWSER_DOWNLOAD_CAPACITY_CODE = 'BROWSER_DOWNLOAD_CAPACITY_REACHED'

const activeDownloadFrames = new Set<HTMLIFrameElement>()
let downloadFrameCleanupRegistered = false
let pendingDownloadFrameReservations = 0

export interface BrowserDownloadReservation {
  trigger: (url: string, filename: string) => void
  release: () => void
}

export class BrowserDownloadCapacityError extends Error {
  readonly code = BROWSER_DOWNLOAD_CAPACITY_CODE

  constructor() {
    super('当前页面已提交过多下载，请刷新页面后重试')
    this.name = 'BrowserDownloadCapacityError'
  }
}

export function isBrowserDownloadCapacityError(error: unknown): boolean {
  return error instanceof BrowserDownloadCapacityError
    || (
      error !== null
      && typeof error === 'object'
      && 'code' in error
      && error.code === BROWSER_DOWNLOAD_CAPACITY_CODE
    )
}

export async function readDownloadJsonErrorDetails(
  response: Response,
  fallback: string,
  options: DownloadJsonErrorOptions = {},
): Promise<DownloadJsonErrorDetails | undefined> {
  const contentDisposition = options.contentDisposition ?? response.headers?.get?.('Content-Disposition') ?? null
  if (isDownloadContentDisposition(contentDisposition)) {
    return undefined
  }

  return readStructuredJsonErrorDetails(response, fallback, options)
}

export async function readRangedDownloadJsonErrorDetails(
  url: string,
  fallback: string,
  fetchResponse: DownloadErrorProbeFetcher,
): Promise<DownloadJsonErrorDetails | undefined> {
  try {
    const response = await fetchResponse(url, {
      headers: {
        Range: 'bytes=0-0',
        [DOWNLOAD_PROBE_HEADER]: DOWNLOAD_PROBE_HEADER_VALUE,
      },
    })
    try {
      if (response.ok) {
        return undefined
      }
      return await readDownloadJsonErrorDetails(response, fallback)
    } finally {
      await cancelUnreadResponseBody(response)
    }
  } catch {
    return undefined
  }
}

export function triggerBrowserDownload(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  try {
    link.download = sanitizeFilename(filename)
  } catch {
    link.download = 'download'
  }
  document.body.appendChild(link)
  try {
    link.click()
  } finally {
    try {
      link.parentNode?.removeChild(link)
    } finally {
      URL.revokeObjectURL(url)
    }
  }
}

export function triggerBrowserDownloadUrl(url: string, filename: string): void {
  const reservation = reserveBrowserDownloadNavigation()
  try {
    reservation.trigger(url, filename)
  } finally {
    reservation.release()
  }
}

export function reserveBrowserDownloadNavigation(): BrowserDownloadReservation {
  if (activeDownloadFrames.size + pendingDownloadFrameReservations >= MAX_RETAINED_DOWNLOAD_FRAMES) {
    throw new BrowserDownloadCapacityError()
  }

  pendingDownloadFrameReservations += 1
  let available = true
  return {
    trigger(url: string, filename: string): void {
      if (!available) {
        throw new Error('下载提交凭证已失效')
      }
      available = false
      pendingDownloadFrameReservations -= 1
      triggerReservedBrowserDownloadUrl(url, filename)
    },
    release(): void {
      if (!available) {
        return
      }
      available = false
      pendingDownloadFrameReservations -= 1
    },
  }
}

function triggerReservedBrowserDownloadUrl(url: string, filename: string): void {
  let parsedUrl: URL
  try {
    parsedUrl = new URL(url, window.location.origin)
  } catch {
    throw new Error('不安全的下载地址')
  }
  if (
    (parsedUrl.protocol !== 'http:' && parsedUrl.protocol !== 'https:')
    || parsedUrl.origin !== window.location.origin
    || parsedUrl.username !== ''
    || parsedUrl.password !== ''
  ) {
    throw new Error('不安全的下载地址')
  }

  let safeFilename = 'download'
  try {
    safeFilename = sanitizeFilename(filename)
  } catch {
    // Keep the safe fallback label.
  }

  const frame = document.createElement('iframe')
  frame.hidden = true
  frame.tabIndex = -1
  frame.title = `下载 ${safeFilename}`
  frame.name = nextDownloadFrameName()
  frame.referrerPolicy = 'no-referrer'
  frame.setAttribute('aria-hidden', 'true')
  frame.setAttribute('data-mnemonas-download-frame', '')
  frame.setAttribute('sandbox', 'allow-downloads')
  document.body.appendChild(frame)
  frame.addEventListener('load', () => releaseBrowserDownloadFrame(frame), { once: true })
  retainBrowserDownloadFrame(frame)

  const link = document.createElement('a')
  link.href = parsedUrl.toString()
  link.target = frame.name
  link.referrerPolicy = 'no-referrer'
  link.hidden = true
  link.tabIndex = -1
  link.setAttribute('aria-hidden', 'true')
  try {
    document.body.appendChild(link)
    link.click()
  } catch (error) {
    releaseBrowserDownloadFrame(frame)
    throw error
  } finally {
    link.removeAttribute('href')
    link.remove()
  }
}

function nextDownloadFrameName(): string {
  if (!globalThis.crypto?.getRandomValues) {
    throw new Error('当前浏览器无法创建安全下载目标')
  }

  for (let attempt = 0; attempt < 4; attempt += 1) {
    const random = globalThis.crypto.getRandomValues(new Uint8Array(16))
    const suffix = Array.from(random, (byte) => byte.toString(16).padStart(2, '0')).join('')
    const name = `mnemonas-download-${suffix}`
    if (document.getElementsByName(name).length === 0) {
      return name
    }
  }
  throw new Error('当前页面无法分配安全下载目标')
}

function retainBrowserDownloadFrame(frame: HTMLIFrameElement): void {
  activeDownloadFrames.add(frame)
  if (downloadFrameCleanupRegistered) {
    return
  }

  window.addEventListener('pagehide', cleanupBrowserDownloadFrames, { once: true })
  downloadFrameCleanupRegistered = true
}

function releaseBrowserDownloadFrame(frame: HTMLIFrameElement): void {
  activeDownloadFrames.delete(frame)
  frame.remove()
}

function cleanupBrowserDownloadFrames(): void {
  for (const frame of activeDownloadFrames) {
    frame.remove()
  }
  activeDownloadFrames.clear()
  downloadFrameCleanupRegistered = false
}

function isDownloadContentDisposition(contentDisposition: string | null): boolean {
  if (!contentDisposition) {
    return false
  }

  return contentDisposition.split(';').some((part, index) => {
    const trimmed = part.trim().toLowerCase()
    if (index === 0) {
      return trimmed === 'attachment'
    }

    const equalsIndex = trimmed.indexOf('=')
    if (equalsIndex <= 0) {
      return false
    }
    const key = trimmed.slice(0, equalsIndex).trim()
    return key === 'filename' || key === 'filename*'
  })
}

async function cancelUnreadResponseBody(response: Response): Promise<void> {
  try {
    await response.body?.cancel()
  } catch {
    // Ignore best-effort cancellation failures.
  }
}
