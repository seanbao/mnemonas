export interface StructuredJsonErrorDetails {
  message: string
  code?: string
}

export interface StructuredJsonErrorOptions {
  localizeCode?: (code: string) => string | undefined
  problemJson?: boolean
}

export async function readStructuredJsonErrorDetails(
  response: Response,
  fallback: string,
  options: StructuredJsonErrorOptions = {},
): Promise<StructuredJsonErrorDetails | undefined> {
  const contentType = response.headers?.get?.('Content-Type') ?? null
  if (!isJsonContentType(contentType)) {
    return undefined
  }

  let body: unknown
  try {
    body = await response.clone().json()
  } catch {
    return undefined
  }

  return extractStructuredJsonErrorDetails(body, fallback, {
    ...options,
    problemJson: options.problemJson ?? isProblemJsonContentType(contentType),
  })
}

export function isJsonContentType(contentType: string | null): boolean {
  if (!contentType) {
    return false
  }
  const mediaType = contentType.split(';')[0].trim().toLowerCase()
  return mediaType === 'application/json' || mediaType.endsWith('+json')
}

export function extractStructuredJsonErrorDetails(
  body: unknown,
  _fallback: string,
  options: StructuredJsonErrorOptions = {},
): StructuredJsonErrorDetails | undefined {
  if (!isRecord(body)) {
    return undefined
  }

  const code = getErrorCode(body)
  const localized = code ? options.localizeCode?.(code) : undefined
  const explicitMessage = getExplicitErrorMessage(body, options.problemJson === true)
  if (localized && isStructuredErrorBody(body, true, options.problemJson === true)) {
    return { message: localized, code }
  }
  if (!explicitMessage || !isStructuredErrorBody(body, false, options.problemJson === true)) {
    return undefined
  }

  return {
    message: explicitMessage,
    code,
  }
}

function isProblemJsonContentType(contentType: string | null): boolean {
  if (!contentType) {
    return false
  }
  const mediaType = contentType.split(';')[0].trim().toLowerCase()
  return mediaType === 'application/problem+json'
}

function isStructuredErrorBody(body: Record<string, unknown>, allowCodeOnly: boolean, problemJson: boolean): boolean {
  const hasErrorField = typeof body.error === 'string' && body.error.length > 0
    || (isRecord(body.error) && (
      allowCodeOnly && typeof body.error.code === 'string' && body.error.code.length > 0
      || typeof body.error.message === 'string' && body.error.message.length > 0
    ))
  const hasTopLevelError = typeof body.code === 'string'
    && typeof body.message === 'string'
    && body.message.length > 0
  const hasFalseSuccessError = body.success === false && (
    allowCodeOnly && typeof body.code === 'string' && body.code.length > 0
    || typeof body.message === 'string' && body.message.length > 0
  )
  const hasProblemError = problemJson && (
    typeof body.detail === 'string' && body.detail.length > 0
    || typeof body.title === 'string' && body.title.length > 0
  )

  return hasErrorField || hasTopLevelError || hasFalseSuccessError || hasProblemError
}

function getErrorCode(body: Record<string, unknown>): string | undefined {
  if (isRecord(body.error) && typeof body.error.code === 'string') {
    return body.error.code
  }

  if (typeof body.code === 'string') {
    return body.code
  }

  return undefined
}

function getExplicitErrorMessage(body: Record<string, unknown>, problemJson: boolean): string | undefined {
  if (typeof body.error === 'string' && body.error) {
    return body.error
  }

  if (isRecord(body.error) && typeof body.error.message === 'string' && body.error.message) {
    return body.error.message
  }

  if (typeof body.message === 'string' && body.message) {
    return body.message
  }

  if (problemJson && typeof body.detail === 'string' && body.detail) {
    return body.detail
  }

  if (problemJson && typeof body.title === 'string' && body.title) {
    return body.title
  }

  return undefined
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}
