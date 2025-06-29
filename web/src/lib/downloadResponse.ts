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
