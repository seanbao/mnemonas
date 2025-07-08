import { getRedactedDiagnosticMessage } from './diagnosticMessages'

export function routeRenderDiagnosticMessage(error: unknown): string {
  if (error instanceof Error) {
    return getRedactedDiagnosticMessage(`${error.name}: ${error.message}`) ?? 'Route render failed'
  }
  if (typeof error === 'string') {
    return getRedactedDiagnosticMessage(error) ?? 'Route render failed'
  }
  return 'Route render failed'
}
