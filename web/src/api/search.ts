/**
 * Search API
 * Global file search functionality
 */

import { authFetch } from './auth'

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

function isSearchResultWire(value: unknown): value is SearchResultWire {
  return !!value
    && typeof value === 'object'
    && typeof (value as SearchResultWire).name === 'string'
    && typeof (value as SearchResultWire).path === 'string'
    && typeof (value as SearchResultWire).size === 'number'
    && (typeof (value as SearchResultWire).isDir === 'boolean' || typeof (value as SearchResultWire).is_dir === 'boolean')
    && (typeof (value as SearchResultWire).modTime === 'string' || typeof (value as SearchResultWire).mod_time === 'string')
    && ((value as SearchResultWire).hash === undefined || typeof (value as SearchResultWire).hash === 'string')
}

function isSearchResponse(value: unknown): value is SearchResponse & { results: SearchResultWire[] } {
  return !!value &&
    typeof value === 'object' &&
    typeof (value as SearchResponse).query === 'string' &&
    typeof (value as SearchResponse).count === 'number' &&
    Array.isArray((value as SearchResponse).results) &&
    (value as SearchResponse & { results: unknown[] }).results.every((item) => isSearchResultWire(item))
}

/**
 * Search for files matching the query
 * @param query - Search query (case-insensitive substring match)
 * @param limit - Maximum results to return (default 50, max 100)
 */
export async function searchFiles(query: string, limit: number = 50): Promise<SearchResponse> {
  const trimmedQuery = query.trim()
  if (!trimmedQuery) {
    throw new Error("Search query is required")
  }
  if (!Number.isInteger(limit) || limit <= 0 || limit > 100) {
    throw new Error("Search limit must be between 1 and 100")
  }

  const params = new URLSearchParams({ q: trimmedQuery })
  if (limit && limit !== 50) {
    params.set('limit', String(limit))
  }
  
  const response = await authFetch(`/api/v1/search?${params}`)
  
  if (!response.ok) {
    let message = 'Search failed'
    let code: string | undefined
    try {
      const body = await response.json() as SearchApiResponse<never> & { code?: string }
      const topLevelCode = typeof body.code === 'string' ? body.code : undefined
      message = typeof body.error === 'string'
        ? body.error
        : body.error?.message || body.message || 'Search failed'
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
    throw new Error('服务器返回了无效的数据')
  }

  const looksWrapped = !!result &&
    typeof result === 'object' &&
    ('success' in result || 'data' in result || 'error' in result || 'message' in result)

  const data = looksWrapped
    ? ((result as SearchApiResponse<SearchResponse>).success === true ? (result as SearchApiResponse<SearchResponse>).data : undefined)
    : result

  if (!isSearchResponse(data)) {
    throw new Error('服务器返回了无效的数据')
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
