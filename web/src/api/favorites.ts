import { authFetch } from './auth'
import { INVALID_API_RESPONSE_MESSAGE } from '@/lib/apiMessages'
import { readStructuredJsonErrorDetails } from '@/lib/jsonErrorResponse'
import { encodePathForUrl, normalizePath } from '@/lib/utils'

const API_BASE = '/api/v1'
let batchCheckSupported: boolean | null = null

export interface FavoritesRequestOptions {
  signal?: AbortSignal
}

export interface Favorite {
  path: string
  user_id: string
  created_at: string
  note?: string
}

export interface FavoritesResponse {
  favorites: Favorite[]
  count: number
}

export interface CheckPathsResponse {
  favorites: Record<string, boolean>
}

export interface FavoritesActionResult {
  message?: string
}

export class FavoritesError extends Error {
  status: number
  code?: string
  
  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = 'FavoritesError'
    this.status = status
    this.code = code
  }
  
  get isNotFound(): boolean {
    return this.status === 404
  }
  
  get isConflict(): boolean {
    return this.status === 409
  }

  get isFeatureDisabled(): boolean {
    return this.code === 'FAVORITES_FEATURE_DISABLED'
  }

  get isUnavailable(): boolean {
    return this.code === 'FAVORITES_UNAVAILABLE' || (this.status === 503 && !this.isFeatureDisabled)
  }
}

async function createFavoritesError(response: Response, fallback: string): Promise<FavoritesError> {
  const structuredError = await readStructuredJsonErrorDetails(response, fallback)
  if (structuredError) {
    return new FavoritesError(structuredError.message, response.status, structuredError.code)
  }

  let message = fallback
  let code: string | undefined
  try {
    const body: FavoritesApiResponse<never> = await response.json()
    message = getFavoritesErrorMessage(body, message)
    code = getFavoritesErrorCode(body)
  } catch {
    // Keep fallback when the error body cannot be parsed.
  }
  return new FavoritesError(message, response.status, code)
}

interface FavoritesApiError {
  code?: string
  message: string
}

interface FavoritesApiResponse<T> {
  success: boolean
  data?: T
  message?: string
  error?: FavoritesApiError | string
}

function isValidFavorite(value: unknown): value is Favorite {
  return !!value &&
    typeof value === 'object' &&
    typeof (value as Favorite).path === 'string' &&
    typeof (value as Favorite).user_id === 'string' &&
    typeof (value as Favorite).created_at === 'string' &&
    ((value as Favorite).note === undefined || typeof (value as Favorite).note === 'string')
}

function isNonNegativeSafeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}

function isValidFavoritesResponse(value: unknown): value is FavoritesResponse {
  if (!value || typeof value !== 'object') {
    return false
  }
  const response = value as FavoritesResponse
  return Array.isArray(response.favorites) &&
    response.favorites.every(isValidFavorite) &&
    isNonNegativeSafeInteger(response.count) &&
    response.count >= response.favorites.length
}

function getFavoritesErrorMessage(body: FavoritesApiResponse<never>, fallback: string): string {
  if (typeof body.error === 'string' && body.error) {
    return body.error
  }
  if (body.error && typeof body.error === 'object' && body.error.message) {
    return body.error.message
  }
  if (body.message) {
    return body.message
  }
  return fallback
}

function getFavoritesErrorCode(body: FavoritesApiResponse<never>): string | undefined {
  if (body.error && typeof body.error === 'object' && body.error.code) {
    return body.error.code
  }
  return undefined
}

async function readFavoritesSuccess<T>(response: Response, invalidMessage: string): Promise<FavoritesApiResponse<T>> {
  let body: FavoritesApiResponse<T>
  try {
    body = await response.json()
  } catch {
    throw new FavoritesError(invalidMessage, response.status)
  }

  if (body.success !== true || body.data === undefined) {
    throw new FavoritesError(invalidMessage, response.status)
  }
  return body
}

async function readFavoritesSuccessData<T>(response: Response, invalidMessage: string): Promise<T> {
  const body = await readFavoritesSuccess<T>(response, invalidMessage)
  return body.data as T
}

async function readFavoritesActionSuccess(response: Response, invalidMessage: string): Promise<FavoritesActionResult> {
  const body = await readFavoritesSuccess<null>(response, invalidMessage)
  return {
    message: body.message,
  }
}

/**
 * List user's favorites
 */
export async function listFavorites(options: FavoritesRequestOptions = {}): Promise<Favorite[]> {
  const response = await authFetch(`${API_BASE}/favorites`, options)
  
  if (!response.ok) {
    throw await createFavoritesError(response, '获取收藏列表失败')
  }

  const data = await readFavoritesSuccessData<FavoritesResponse>(response, INVALID_API_RESPONSE_MESSAGE)
  if (!isValidFavoritesResponse(data)) {
    throw new FavoritesError(INVALID_API_RESPONSE_MESSAGE, response.status)
  }
  return data.favorites
}

/**
 * Add path to favorites
 */
export async function addFavorite(path: string, note = '', options: FavoritesRequestOptions = {}): Promise<Favorite> {
  const normalizedPath = normalizePath(path)
  const response = await authFetch(`${API_BASE}/favorites`, {
    ...options,
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: normalizedPath, note }),
  })
  
  if (!response.ok) {
    const fallback = response.status === 409 ? '已经收藏过了' : '添加收藏失败'
    throw await createFavoritesError(response, fallback)
  }

  const data = await readFavoritesSuccessData<unknown>(response, INVALID_API_RESPONSE_MESSAGE)
  if (!isValidFavorite(data)) {
    throw new FavoritesError(INVALID_API_RESPONSE_MESSAGE, response.status)
  }
  return data
}

/**
 * Remove path from favorites
 */
export async function removeFavorite(path: string, options: FavoritesRequestOptions = {}): Promise<FavoritesActionResult> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const response = await authFetch(`${API_BASE}/favorites${encodedPath}`, {
    ...options,
    method: 'DELETE',
  })
  
  if (!response.ok) {
    throw await createFavoritesError(response, '移除收藏失败')
  }

  return readFavoritesActionSuccess(response, INVALID_API_RESPONSE_MESSAGE)
}

/**
 * Check if a path is favorited
 */
export async function checkFavorite(path: string, options: FavoritesRequestOptions = {}): Promise<boolean> {
  const normalizedPath = normalizePath(path)
  const url = `${API_BASE}/favorites/check?path=${encodeURIComponent(normalizedPath)}`
  const response = options.signal ? await authFetch(url, { signal: options.signal }) : await authFetch(url)
  
  if (!response.ok) {
    throw await createFavoritesError(response, '获取收藏状态失败')
  }
  
  const data = await readFavoritesSuccessData<{ path: string; is_favorite: boolean }>(response, INVALID_API_RESPONSE_MESSAGE)
  if (typeof data.is_favorite !== 'boolean') {
    throw new FavoritesError(INVALID_API_RESPONSE_MESSAGE, response.status)
  }
  return data.is_favorite
}

/**
 * Check multiple paths at once
 */
export async function checkFavorites(paths: string[], options: FavoritesRequestOptions = {}): Promise<Record<string, boolean>> {
  if (batchCheckSupported === false) {
    return Object.fromEntries(paths.map(p => [p, false]))
  }
  const normalizedMap = new Map<string, string>()
  const normalizedPaths = paths.map((path) => {
    const normalized = normalizePath(path)
    normalizedMap.set(normalized, path)
    return normalized
  })
  const response = await authFetch(`${API_BASE}/favorites/check-batch`, {
    ...options,
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ paths: normalizedPaths }),
  })
  
  if (!response.ok) {
    if (response.status === 404) {
      batchCheckSupported = false
      return Object.fromEntries(paths.map(p => [p, false]))
    }
    throw await createFavoritesError(response, '获取收藏状态失败')
  }
  batchCheckSupported = true
  const data = await readFavoritesSuccessData<CheckPathsResponse>(response, INVALID_API_RESPONSE_MESSAGE)
  if (
    !data.favorites ||
    typeof data.favorites !== 'object' ||
    Array.isArray(data.favorites)
  ) {
    throw new FavoritesError(INVALID_API_RESPONSE_MESSAGE, response.status)
  }
  const mapped: Record<string, boolean> = {}
  for (const [normalized, isFavorite] of Object.entries(data.favorites)) {
    if (typeof isFavorite !== 'boolean') {
      throw new FavoritesError(INVALID_API_RESPONSE_MESSAGE, response.status)
    }
    const original = normalizedMap.get(normalized)
    if (original) {
      mapped[original] = isFavorite
    }
  }
  return mapped
}

/**
 * Update note for a favorite
 */
export async function updateFavoriteNote(path: string, note: string, options: FavoritesRequestOptions = {}): Promise<FavoritesActionResult> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const response = await authFetch(`${API_BASE}/favorites${encodedPath}`, {
    ...options,
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ note }),
  })
  
  if (!response.ok) {
    throw await createFavoritesError(response, '更新备注失败')
  }

  return readFavoritesActionSuccess(response, INVALID_API_RESPONSE_MESSAGE)
}

/**
 * Toggle favorite status
 */
export async function toggleFavorite(path: string, isFavorited: boolean, options: FavoritesRequestOptions = {}): Promise<boolean> {
  if (isFavorited) {
    await removeFavorite(path, options)
    return false
  } else {
    await addFavorite(path, '', options)
    return true
  }
}
