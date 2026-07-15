import type {
  DirectoryAccessReportData,
  DirectoryAccessReviewRecord,
  DirectoryAccessReviewRecordCreateRequest,
} from '@/api/settings'

export const reviewHistoryLimit = 5
const reviewHistoryStoragePrefix = 'mnemonas_directory_access_review_history'

export type ReviewHistoryEntry = {
  id: string
  recordedAt: string
  reviewer?: string
  title: string
  path: string
  preview: boolean
  users: number
  readAllowed: number
  writeAllowed: number
  relatedShares: number
  reportText: string
}

export function getHistoryStorageKey(userID: string | undefined): string {
  return `${reviewHistoryStoragePrefix}:${userID?.trim() || 'anonymous'}`
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.trim().length > 0
}

function isNonNegativeSafeInteger(value: unknown): value is number {
  return Number.isSafeInteger(value) && Number(value) >= 0
}

function isHistoryEntry(value: unknown): value is ReviewHistoryEntry {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const entry = value as ReviewHistoryEntry
  return isNonEmptyString(entry.id)
    && isNonEmptyString(entry.recordedAt)
    && Number.isFinite(Date.parse(entry.recordedAt))
    && (entry.reviewer === undefined || isNonEmptyString(entry.reviewer))
    && isNonEmptyString(entry.title)
    && isNonEmptyString(entry.path)
    && typeof entry.preview === 'boolean'
    && isNonNegativeSafeInteger(entry.users)
    && isNonNegativeSafeInteger(entry.readAllowed)
    && isNonNegativeSafeInteger(entry.writeAllowed)
    && isNonNegativeSafeInteger(entry.relatedShares)
    && isNonEmptyString(entry.reportText)
}

export function loadHistory(storageKey: string): ReviewHistoryEntry[] {
  try {
    const raw = window.localStorage.getItem(storageKey)
    const parsed: unknown = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed) ? parsed.filter(isHistoryEntry).slice(0, reviewHistoryLimit) : []
  } catch {
    return []
  }
}

export function saveHistory(storageKey: string, entries: ReviewHistoryEntry[]): boolean {
  try {
    window.localStorage.setItem(storageKey, JSON.stringify(entries.slice(0, reviewHistoryLimit)))
    return true
  } catch {
    return false
  }
}

export function serverRecordToHistory(record: DirectoryAccessReviewRecord): ReviewHistoryEntry {
  return {
    id: record.id,
    recordedAt: record.reviewed_at,
    reviewer: record.reviewer,
    title: record.title,
    path: record.path,
    preview: record.preview,
    users: record.users,
    readAllowed: record.read_allowed,
    writeAllowed: record.write_allowed,
    relatedShares: record.related_shares,
    reportText: record.report_text,
  }
}

export function mergeHistory(primary: ReviewHistoryEntry[], fallback: ReviewHistoryEntry[]): ReviewHistoryEntry[] {
  const seen = new Set<string>()
  return [...primary, ...fallback].filter((entry) => {
    if (seen.has(entry.id)) return false
    seen.add(entry.id)
    return true
  }).slice(0, reviewHistoryLimit)
}

export function historyRequest(
  report: DirectoryAccessReportData,
  title: string,
  reportText: string,
): DirectoryAccessReviewRecordCreateRequest {
  return {
    title,
    path: report.path,
    preview: report.preview === true,
    users: report.summary.users,
    read_allowed: report.summary.read_allowed,
    read_denied: report.summary.read_denied,
    write_allowed: report.summary.write_allowed,
    write_denied: report.summary.write_denied,
    related_shares: report.summary.related_shares,
    active_related_shares: report.summary.active_related_shares,
    password_protected_shares: report.summary.password_protected_shares,
    report_text: reportText,
  }
}
