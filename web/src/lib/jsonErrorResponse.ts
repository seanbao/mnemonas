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
  const errorMessage = isRecord(body.error) ? getNonBlankJsonString(body.error.message) : undefined
  const errorCode = isRecord(body.error) ? getNonBlankJsonString(body.error.code) : undefined
  const topLevelCode = getNonBlankJsonString(body.code)
  const topLevelMessage = getNonBlankJsonString(body.message)
  const problemDetail = getNonBlankJsonString(body.detail)
  const problemTitle = getNonBlankJsonString(body.title)
  const hasErrorField = getNonBlankJsonString(body.error) !== undefined
    || (isRecord(body.error) && (
      allowCodeOnly && errorCode !== undefined
      || errorMessage !== undefined
    ))
  const hasTopLevelError = topLevelCode !== undefined && topLevelMessage !== undefined
  const hasFalseSuccessError = body.success === false && (
    allowCodeOnly && topLevelCode !== undefined
    || topLevelMessage !== undefined
  )
  const hasProblemError = problemJson && (
    problemDetail !== undefined
    || problemTitle !== undefined
  )

  return hasErrorField || hasTopLevelError || hasFalseSuccessError || hasProblemError
}

function getErrorCode(body: Record<string, unknown>): string | undefined {
  if (isRecord(body.error)) {
    const errorCode = getNonBlankJsonString(body.error.code)
    if (errorCode !== undefined) {
      return errorCode
    }
  }

  return getNonBlankJsonString(body.code)
}

function getExplicitErrorMessage(body: Record<string, unknown>, problemJson: boolean): string | undefined {
  const stringError = getNonBlankJsonString(body.error)
  if (stringError !== undefined) {
    return stringError
  }

  if (isRecord(body.error)) {
    const errorMessage = getNonBlankJsonString(body.error.message)
    if (errorMessage !== undefined) {
      return errorMessage
    }
  }

  const message = getNonBlankJsonString(body.message)
  if (message !== undefined) {
    return message
  }

  const detail = problemJson ? getNonBlankJsonString(body.detail) : undefined
  if (detail !== undefined) {
    return detail
  }

  const title = problemJson ? getNonBlankJsonString(body.title) : undefined
  if (title !== undefined) {
    return title
  }

  return undefined
}

export function getNonBlankJsonString(value: unknown): string | undefined {
  if (typeof value !== 'string') {
    return undefined
  }

  const trimmed = value.trim()
  return trimmed.length > 0 ? trimmed : undefined
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}
