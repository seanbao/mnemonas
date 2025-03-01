import { authFetch } from './auth'
import { encodePathForUrl, normalizePath } from '@/lib/utils'

const API_BASE = '/api/v1'
let batchCheckSupported: boolean | null = null

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

export class FavoritesError extends Error {
  status: number
  
  constructor(message: string, status: number) {
    super(message)
    this.name = 'FavoritesError'
    this.status = status
  }
  
  get isNotFound(): boolean {
    return this.status === 404
  }
  
  get isConflict(): boolean {
    return this.status === 409
  }
}

function createFavoritesError(response: Response, fallback: string): Promise<FavoritesError> {
  return (async () => {
    let message = fallback
    try {
      const body: FavoritesApiResponse<never> = await response.json()
      message = getFavoritesErrorMessage(body, message)
    } catch {
      // Keep fallback when the error body cannot be parsed.
    }
    return new FavoritesError(message, response.status)
  })()
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

/**
 * List user's favorites
 */
export async function listFavorites(): Promise<Favorite[]> {
  const response = await authFetch(`${API_BASE}/favorites`)
  
  if (!response.ok) {
    let message = '获取收藏列表失败'
    try {
      const body: FavoritesApiResponse<never> = await response.json()
      message = getFavoritesErrorMessage(body, message)
    } catch { /* ignore */ }
    throw new FavoritesError(message, response.status)
  }

  const body: FavoritesApiResponse<FavoritesResponse> = await response.json()
  if (!body.data) {
    throw new FavoritesError('获取收藏列表响应无效', response.status)
  }
  const data = body.data
  return data.favorites
}

/**
 * Add path to favorites
 */
export async function addFavorite(path: string, note = ''): Promise<Favorite> {
  const normalizedPath = normalizePath(path)
  const response = await authFetch(`${API_BASE}/favorites`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: normalizedPath, note }),
  })
  
  if (!response.ok) {
    let message = '添加收藏失败'
    if (response.status === 409) {
      message = '已经收藏过了'
    }
    try {
      const body: FavoritesApiResponse<never> = await response.json()
      message = getFavoritesErrorMessage(body, message)
    } catch { /* ignore */ }
    throw new FavoritesError(message, response.status)
  }

  const body: FavoritesApiResponse<Favorite> = await response.json()
  if (!body.data) {
    throw new FavoritesError('添加收藏响应无效', response.status)
  }
  return body.data
}

/**
 * Remove path from favorites
 */
export async function removeFavorite(path: string): Promise<void> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const response = await authFetch(`${API_BASE}/favorites${encodedPath}`, {
    method: 'DELETE',
  })
  
  if (!response.ok) {
    let message = '移除收藏失败'
    try {
      const body: FavoritesApiResponse<never> = await response.json()
      message = getFavoritesErrorMessage(body, message)
    } catch { /* ignore */ }
    throw new FavoritesError(message, response.status)
  }
}

/**
 * Check if a path is favorited
 */
export async function checkFavorite(path: string): Promise<boolean> {
  const normalizedPath = normalizePath(path)
  const response = await authFetch(`${API_BASE}/favorites/check?path=${encodeURIComponent(normalizedPath)}`)
  
  if (!response.ok) {
    throw await createFavoritesError(response, '获取收藏状态失败')
  }
  
  const body: FavoritesApiResponse<{ path: string; is_favorite: boolean }> = await response.json()
  if (!body.data) {
    return false
  }
  return body.data.is_favorite
}

/**
 * Check multiple paths at once
 */
export async function checkFavorites(paths: string[]): Promise<Record<string, boolean>> {
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
  const body: FavoritesApiResponse<CheckPathsResponse> = await response.json()
  if (!body.data) {
    return Object.fromEntries(paths.map(p => [p, false]))
  }
  const data = body.data
  const mapped: Record<string, boolean> = {}
  for (const [normalized, isFavorite] of Object.entries(data.favorites)) {
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
export async function updateFavoriteNote(path: string, note: string): Promise<void> {
  const normalizedPath = normalizePath(path)
  const encodedPath = encodePathForUrl(normalizedPath)
  const response = await authFetch(`${API_BASE}/favorites${encodedPath}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ note }),
  })
  
  if (!response.ok) {
    let message = '更新备注失败'
    try {
      const body: FavoritesApiResponse<never> = await response.json()
      message = getFavoritesErrorMessage(body, message)
    } catch { /* ignore */ }
    throw new FavoritesError(message, response.status)
  }
}

/**
 * Toggle favorite status
 */
export async function toggleFavorite(path: string, isFavorited: boolean): Promise<boolean> {
  if (isFavorited) {
    await removeFavorite(path)
    return false
  } else {
    await addFavorite(path)
    return true
  }
}
