import { describe, expect, it } from 'vitest'
import { getRedactedDiagnosticMessage, redactDiagnosticSecretFragments } from './diagnosticMessages'

describe('diagnosticMessages', () => {
  it('redacts secret-like values in unknown diagnostics', () => {
    const raw = 'rclone failed https://user:repo-pass@backup.example/repo?token=batch-secret --password restic-secret Authorization: Bearer bearer-secret api-key: api-secret'

    const redacted = redactDiagnosticSecretFragments(raw)

    expect(redacted).toBe('rclone failed https://<redacted>@backup.example/repo?token=<redacted> --password <redacted> Authorization: Bearer <redacted> api-key: <redacted>')
    for (const leaked of ['user', 'repo-pass', 'batch-secret', 'restic-secret', 'bearer-secret', 'api-secret']) {
      expect(redacted).not.toContain(leaked)
    }
  })

  it('redacts URL userinfo even when the URL has no password separator', () => {
    const raw = 'rclone failed https://repo-token@backup.example/repo'

    const redacted = redactDiagnosticSecretFragments(raw)

    expect(redacted).toBe('rclone failed https://<redacted>@backup.example/repo')
    expect(redacted).not.toContain('repo-token')
  })

  it('redacts quoted flags, headers, assignments, and JSON values', () => {
    const raw = `restic failed: --password repo:pass/with/slash --secret-access-key=secret/value:with-colon --token remote-token --api-key "quoted token" secret='spaced secret' Authorization: Bearer "bearer secret" X-Auth-Token: header-token X-Api-Key: "header quoted token" {"access_key_id":"json-akia","secret_access_key":"json secret","authorization":"Bearer json bearer"}`

    const redacted = redactDiagnosticSecretFragments(raw)

    for (const leaked of [
      'repo:pass/with/slash',
      'pass/with/slash',
      'secret/value:with-colon',
      'value:with-colon',
      'remote-token',
      'quoted token',
      'spaced secret',
      'bearer secret',
      'header-token',
      'header quoted token',
      'json-akia',
      'json secret',
      'json bearer',
    ]) {
      expect(redacted).not.toContain(leaked)
    }
    expect(redacted).toContain('--password <redacted>')
    expect(redacted).toContain('--secret-access-key=<redacted>')
    expect(redacted).toContain('--token <redacted>')
    expect(redacted).toContain('--api-key "<redacted>"')
    expect(redacted).toContain("secret='<redacted>'")
    expect(redacted).toContain('Authorization: Bearer "<redacted>"')
    expect(redacted).toContain('X-Auth-Token: <redacted>')
    expect(redacted).toContain('X-Api-Key: "<redacted>"')
    expect(redacted).toContain('"access_key_id":"<redacted>"')
    expect(redacted).toContain('"secret_access_key":"<redacted>"')
    expect(redacted).toContain('"authorization":"<redacted>"')
  })

  it('redacts less common quoted diagnostic secret formats', () => {
    const raw = `backup failed password="double quoted password" 'token':'single json token' Proxy-Authorization: Basic 'proxy basic secret' username: 'operator name' --sig 'signed flag value'`

    const redacted = redactDiagnosticSecretFragments(raw)

    for (const leaked of [
      'double quoted password',
      'single json token',
      'proxy basic secret',
      'operator name',
      'signed flag value',
    ]) {
      expect(redacted).not.toContain(leaked)
    }
    expect(redacted).toContain('password="<redacted>"')
    expect(redacted).toContain("'token':'<redacted>'")
    expect(redacted).toContain("Proxy-Authorization: Basic '<redacted>'")
    expect(redacted).toContain("username: '<redacted>'")
    expect(redacted).toContain("--sig '<redacted>'")
  })

  it('keeps non-sensitive diagnostics unchanged', () => {
    expect(redactDiagnosticSecretFragments('RCLONE Exit Status 1')).toBe('RCLONE Exit Status 1')
  })

  it('returns trimmed redacted diagnostics for optional display text', () => {
    expect(getRedactedDiagnosticMessage('  登录失败 token=auth-secret  ')).toBe('登录失败 token=<redacted>')
    expect(getRedactedDiagnosticMessage('   ')).toBeUndefined()
    expect(getRedactedDiagnosticMessage(undefined)).toBeUndefined()
  })
})
