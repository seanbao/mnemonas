import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

export interface E2ECredentials {
  username: string
  password: string
  passwordSource: 'env' | 'file' | 'missing'
}

function parseInitialPasswordFile(filePath: string): string {
  const content = fs.readFileSync(filePath, 'utf8')
  const match = content.match(/^Password:\s*(.+)$/m)
  return match?.[1]?.trim() ?? ''
}

function getPasswordFileCandidates(): string[] {
  const candidates: string[] = []

  if (process.env.E2E_PASSWORD_FILE) {
    candidates.push(process.env.E2E_PASSWORD_FILE)
  }

  const homeDir = os.homedir()
  if (homeDir) {
    candidates.push(
      path.join(homeDir, '.mnemonas', '.mnemonas', 'initial-password.txt'),
      path.join(homeDir, '.mnemonas', 'initial-password.txt'),
    )
  }

  return candidates
}

export function resolveE2ECredentials(): E2ECredentials {
  const username = process.env.E2E_USERNAME || 'admin'
  const envPassword = process.env.E2E_PASSWORD?.trim()
  if (envPassword) {
    return {
      username,
      password: envPassword,
      passwordSource: 'env',
    }
  }

  for (const candidate of getPasswordFileCandidates()) {
    try {
      if (!fs.existsSync(candidate)) {
        continue
      }

      const password = parseInitialPasswordFile(candidate)
      if (!password) {
        continue
      }

      return {
        username,
        password,
        passwordSource: 'file',
      }
    } catch {
      // Ignore unreadable candidates and continue to the next location.
    }
  }

  return {
    username,
    password: '',
    passwordSource: 'missing',
  }
}