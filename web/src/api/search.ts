/**
 * Search API
 * Global file search functionality
 */

import { authFetch } from './auth'
import { INVALID_API_RESPONSE_MESSAGE } from '@/lib/apiMessages'
import { readStructuredJsonErrorDetails } from '@/lib/jsonErrorResponse'

interface SearchApiError {
  code?: string
  message?: string
}

interface SearchApiResponse<T> {
  success?: boolean
  data?: T
  message?: string
  error?: string | SearchApiError
}

export interface SearchResult {
  name: string
  path: string
  isDir: boolean
  size: number
  modTime: string
  hash?: string
}

export interface SearchResponse {
  query: string
  results: SearchResult[]
  count: number
}

export interface SearchFilesOptions {
  limit?: number
  signal?: AbortSignal
}

export class SearchError extends Error {
  status: number
  statusText: string
  code?: string

  constructor(message: string, status: number, statusText: string, code?: string) {
    super(message)
    this.name = 'SearchError'
    this.status = status
    this.statusText = statusText
    this.code = code
  }

  get isUnavailable(): boolean {
    return this.status === 503 || this.code === 'SERVICE_UNAVAILABLE'
  }
}

const SEARCH_FAILED_MESSAGE = '搜索失败'

interface SearchResultWire {
  name: string
  path: string
  isDir?: boolean
  is_dir?: boolean
  size: number
  modTime?: string
  mod_time?: string
  hash?: string
}

function isNonNegativeSafeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}

function isSearchResultWire(value: unknown): value is SearchResultWire {
  return !!value
    && typeof value === 'object'
    && typeof (value as SearchResultWire).name === 'string'
    && typeof (value as SearchResultWire).path === 'string'
    && isNonNegativeSafeInteger((value as SearchResultWire).size)
    && (typeof (value as SearchResultWire).isDir === 'boolean' || typeof (value as SearchResultWire).is_dir === 'boolean')
    && (typeof (value as SearchResultWire).modTime === 'string' || typeof (value as SearchResultWire).mod_time === 'string')
    && ((value as SearchResultWire).hash === undefined || typeof (value as SearchResultWire).hash === 'string')
}

function isSearchResponse(value: unknown): value is SearchResponse & { results: SearchResultWire[] } {
  return !!value &&
    typeof value === 'object' &&
    typeof (value as SearchResponse).query === 'string' &&
    isNonNegativeSafeInteger((value as SearchResponse).count) &&
    Array.isArray((value as SearchResponse).results) &&
    (value as SearchResponse & { results: unknown[] }).results.every((item) => isSearchResultWire(item)) &&
    (value as SearchResponse).count >= (value as SearchResponse & { results: unknown[] }).results.length
}

/**
 * Search for files matching the query
 * @param query - Search query (case-insensitive substring match)
 * @param optionsOrLimit - Search options, or a legacy maximum result count.
 */
export async function searchFiles(query: string, optionsOrLimit: SearchFilesOptions | number = {}): Promise<SearchResponse> {
  const trimmedQuery = query.trim()
  const limit = typeof optionsOrLimit === 'number'
    ? optionsOrLimit
    : optionsOrLimit.limit ?? 50
  const signal = typeof optionsOrLimit === 'number'
    ? undefined
    : optionsOrLimit.signal
  if (!trimmedQuery) {
    throw new Error('请输入搜索关键词')
  }
  if (!Number.isInteger(limit) || limit <= 0 || limit > 100) {
    throw new Error('搜索结果数量必须在 1 到 100 之间')
  }

  const params = new URLSearchParams({ q: trimmedQuery })
  if (limit && limit !== 50) {
    params.set('limit', String(limit))
  }

  const url = `/api/v1/search?${params}`
  const response = signal ? await authFetch(url, { signal }) : await authFetch(url)
  
  if (!response.ok) {
    const structuredError = await readStructuredJsonErrorDetails(response, SEARCH_FAILED_MESSAGE)
    if (structuredError) {
      throw new SearchError(structuredError.message, response.status, response.statusText, structuredError.code)
    }

    let message = SEARCH_FAILED_MESSAGE
    let code: string | undefined
    try {
      const body = await response.json() as SearchApiResponse<never> & { code?: string }
      const topLevelCode = typeof body.code === 'string' ? body.code : undefined
      message = typeof body.error === 'string'
        ? body.error
        : body.error?.message || body.message || SEARCH_FAILED_MESSAGE
      if (typeof body.error !== 'string' && typeof body.error?.code === 'string') {
        code = body.error.code
      } else if (topLevelCode) {
        code = topLevelCode
      }
    } catch {
      // Fall back to a generic error when the backend did not return valid JSON.
    }
    throw new SearchError(message, response.status, response.statusText, code)
  }

  let result: unknown
  try {
    result = await response.json()
  } catch {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }

  const looksWrapped = !!result &&
    typeof result === 'object' &&
    ('success' in result || 'data' in result || 'error' in result || 'message' in result)

  const data = looksWrapped
    ? ((result as SearchApiResponse<SearchResponse>).success === true ? (result as SearchApiResponse<SearchResponse>).data : undefined)
    : result

  if (!isSearchResponse(data)) {
    throw new Error(INVALID_API_RESPONSE_MESSAGE)
  }

  return {
    query: data.query,
    count: data.count,
    results: (data.results || []).map((item: SearchResultWire) => ({
      name: item.name,
      path: item.path,
      isDir: item.isDir ?? item.is_dir ?? false,
      size: item.size,
      modTime: item.modTime ?? item.mod_time ?? '',
      hash: item.hash,
    })),
  }
}
