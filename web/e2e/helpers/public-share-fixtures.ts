import fs from 'node:fs'
import path from 'node:path'
import { test } from '@playwright/test'
import { isAuthSkipAllowed } from './auth-check'

const E2E_ROOT = process.env.MNEMONAS_E2E_ROOT || '/tmp/mnemonas-playwright'

export const PUBLIC_SHARE_ID_FILE = path.join(E2E_ROOT, 'backend', 'public-share-id.txt')
export const PROTECTED_SHARE_ID_FILE = path.join(E2E_ROOT, 'backend', 'protected-share-id.txt')
export const PROTECTED_SHARE_PASSWORD_FILE = path.join(E2E_ROOT, 'backend', 'protected-share-password.txt')
export const DISABLED_SHARE_ID_FILE = path.join(E2E_ROOT, 'backend', 'disabled-share-id.txt')
export const FOLDER_SHARE_ID_FILE = path.join(E2E_ROOT, 'backend', 'folder-share-id.txt')

const publicShareFixtureFiles = [
  PUBLIC_SHARE_ID_FILE,
  PROTECTED_SHARE_ID_FILE,
  DISABLED_SHARE_ID_FILE,
  FOLDER_SHARE_ID_FILE,
]

function missingFixtureMessage(filePath: string): string {
  return `Missing seeded public-share E2E fixture: ${filePath}`
}

function failOrSkipMissingFixture(filePath: string): never {
  const message = missingFixtureMessage(filePath)
  if (isAuthSkipAllowed()) {
    test.skip(true, `Skipped: ${message}`)
  }

  throw new Error(`${message}. Default isolated Playwright runs must create this fixture in web/scripts/start-e2e-backend.sh.`)
}

export function readPublicShareFixture(filePath: string): string | null {
  if (!fs.existsSync(filePath)) {
    return null
  }

  const value = fs.readFileSync(filePath, 'utf8').trim()
  return value || null
}

export function requirePublicShareFixture(filePath: string): string {
  return readPublicShareFixture(filePath) ?? failOrSkipMissingFixture(filePath)
}

export function publicEntryRoutes(): string[] {
  const shareIds: string[] = []
  const missingFiles: string[] = []

  for (const filePath of publicShareFixtureFiles) {
    const value = readPublicShareFixture(filePath)
    if (value) {
      shareIds.push(value)
      continue
    }
    missingFiles.push(filePath)
  }

  if (missingFiles.length > 0 && !isAuthSkipAllowed()) {
    throw new Error(
      `Missing seeded public-share E2E fixtures: ${missingFiles.join(', ')}. Default isolated Playwright runs must create these fixtures in web/scripts/start-e2e-backend.sh.`,
    )
  }

  return [
    '/login',
    '/s',
    ...shareIds.map((shareId) => `/s/${shareId}`),
  ]
}
