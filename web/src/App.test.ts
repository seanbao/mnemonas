import { describe, expect, it } from 'vitest'
import { shouldValidateSessionOnInitialRoute } from './lib/authInitialRoute'
import { routeRenderDiagnosticMessage } from './lib/routeDiagnostics'

describe('shouldValidateSessionOnInitialRoute', () => {
  it('skips session probing for unauthenticated public entry routes', () => {
    expect(shouldValidateSessionOnInitialRoute('/login')).toBe(false)
    expect(shouldValidateSessionOnInitialRoute('/s')).toBe(false)
    expect(shouldValidateSessionOnInitialRoute('/s/public-share-id')).toBe(false)
  })

  it('keeps session probing for protected routes', () => {
    expect(shouldValidateSessionOnInitialRoute('/')).toBe(true)
    expect(shouldValidateSessionOnInitialRoute('/files')).toBe(true)
    expect(shouldValidateSessionOnInitialRoute('/settings')).toBe(true)
    expect(shouldValidateSessionOnInitialRoute('/account/security')).toBe(true)
  })
})

describe('routeRenderDiagnosticMessage', () => {
  it('redacts secret-like fragments before route errors are logged', () => {
    const message = routeRenderDiagnosticMessage(new Error('render failed token=route-secret --password local-secret'))

    expect(message).toContain('token=<redacted>')
    expect(message).toContain('--password <redacted>')
    expect(message).not.toContain('route-secret')
    expect(message).not.toContain('local-secret')
  })
})
