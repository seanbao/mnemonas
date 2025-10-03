export const INVALID_API_RESPONSE_MESSAGE = '服务器返回了无效的数据'
export const GENERIC_ACTION_ERROR_DESCRIPTION = '操作未完成，请稍后重试。'
export const GENERIC_LOAD_ERROR_DESCRIPTION = '数据加载失败，请检查网络或稍后重试。'

export function getUserFacingErrorDescription(
  error: unknown,
  fallback = GENERIC_ACTION_ERROR_DESCRIPTION,
): string {
  if (error instanceof Error && error.message === INVALID_API_RESPONSE_MESSAGE) {
    return INVALID_API_RESPONSE_MESSAGE
  }

  return fallback
}
