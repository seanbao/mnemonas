import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'

const originalEnv = {
  MNEMONAS_E2E_ALLOW_AUTH_SKIP: process.env.MNEMONAS_E2E_ALLOW_AUTH_SKIP,
  MNEMONAS_E2E_ROOT: process.env.MNEMONAS_E2E_ROOT,
}

const tempRoots: string[] = []

afterEach(() => {
  restoreEnv('MNEMONAS_E2E_ALLOW_AUTH_SKIP', originalEnv.MNEMONAS_E2E_ALLOW_AUTH_SKIP)
  restoreEnv('MNEMONAS_E2E_ROOT', originalEnv.MNEMONAS_E2E_ROOT)
  vi.resetModules()

  for (const root of tempRoots.splice(0)) {
    fs.rmSync(root, { recursive: true, force: true })
  }
})

describe('public share E2E fixtures', () => {
  it('fails closed when default isolated public-share fixtures are missing', async () => {
    const root = makeTempRoot()
    const fixtures = await loadFixtures(root, '0')

    expect(() => fixtures.publicEntryRoutes()).toThrow(
      /Missing seeded public-share E2E fixtures: .*Default isolated Playwright runs must create these fixtures/,
    )
  })

  it('returns public entry routes for seeded share fixtures', async () => {
    const root = makeTempRoot()
    const backendRoot = path.join(root, 'backend')
    writeFixture(backendRoot, 'public-share-id.txt', 'public-id')
    writeFixture(backendRoot, 'protected-share-id.txt', 'protected-id')
    writeFixture(backendRoot, 'disabled-share-id.txt', 'disabled-id')
    writeFixture(backendRoot, 'folder-share-id.txt', 'folder-id')
    const fixtures = await loadFixtures(root, '0')

    expect(fixtures.publicEntryRoutes()).toEqual([
      '/login',
      '/s',
      '/s/public-id',
      '/s/protected-id',
      '/s/disabled-id',
      '/s/folder-id',
    ])
  })

  it('fails closed when a required share fixture is missing', async () => {
    const root = makeTempRoot()
    const fixtures = await loadFixtures(root, '0')

    expect(() => fixtures.requirePublicShareFixture(fixtures.PUBLIC_SHARE_ID_FILE)).toThrow(
      /Missing seeded public-share E2E fixture: .*Default isolated Playwright runs must create this fixture/,
    )
  })

  it('trims public share fixture IDs when reading them', async () => {
    const root = makeTempRoot()
    const backendRoot = path.join(root, 'backend')
    writeFixture(backendRoot, 'public-share-id.txt', '  public-id  \n')
    const fixtures = await loadFixtures(root, '0')

    expect(fixtures.readPublicShareFixture(fixtures.PUBLIC_SHARE_ID_FILE)).toBe('public-id')
  })
})

function makeTempRoot(): string {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'mnemonas-public-share-fixtures-'))
  tempRoots.push(root)
  return root
}

async function loadFixtures(root: string, allowAuthSkip: string) {
  process.env.MNEMONAS_E2E_ROOT = root
  process.env.MNEMONAS_E2E_ALLOW_AUTH_SKIP = allowAuthSkip
  vi.resetModules()
  return import('../../e2e/helpers/public-share-fixtures')
}

function writeFixture(backendRoot: string, fileName: string, value: string): void {
  fs.mkdirSync(backendRoot, { recursive: true })
  fs.writeFileSync(path.join(backendRoot, fileName), value, { mode: 0o600 })
}

function restoreEnv(name: keyof NodeJS.ProcessEnv, value: string | undefined): void {
  if (value === undefined) {
    delete process.env[name]
    return
  }
  process.env[name] = value
}
