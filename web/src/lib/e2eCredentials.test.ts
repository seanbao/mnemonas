import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { resolveE2ECredentials } from '../../e2e/helpers/credentials'

const originalEnv = {
  E2E_PASSWORD: process.env.E2E_PASSWORD,
  E2E_PASSWORD_FILE: process.env.E2E_PASSWORD_FILE,
  E2E_USERNAME: process.env.E2E_USERNAME,
  HOME: process.env.HOME,
}

const tempRoots: string[] = []

afterEach(() => {
  restoreEnv('E2E_PASSWORD', originalEnv.E2E_PASSWORD)
  restoreEnv('E2E_PASSWORD_FILE', originalEnv.E2E_PASSWORD_FILE)
  restoreEnv('E2E_USERNAME', originalEnv.E2E_USERNAME)
  restoreEnv('HOME', originalEnv.HOME)

  for (const root of tempRoots.splice(0)) {
    fs.rmSync(root, { recursive: true, force: true })
  }
})

describe('resolveE2ECredentials', () => {
  it('prefers explicit environment credentials', () => {
    process.env.E2E_USERNAME = 'operator'
    process.env.E2E_PASSWORD = '  env-secret  '

    expect(resolveE2ECredentials()).toEqual({
      username: 'operator',
      password: 'env-secret',
      passwordSource: 'env',
    })
  })

  it('reads an explicit E2E password file', () => {
    const root = makeTempRoot()
    const passwordFile = path.join(root, 'initial-password.txt')
    writePasswordFile(passwordFile, 'file-secret')
    process.env.E2E_PASSWORD_FILE = passwordFile

    expect(resolveE2ECredentials()).toMatchObject({
      password: 'file-secret',
      passwordSource: 'file',
    })
  })

  it('falls back to the nested default home initial-password path', () => {
    const home = makeTempRoot()
    writePasswordFile(
      path.join(home, '.mnemonas', '.mnemonas', 'initial-password.txt'),
      'nested-home-secret',
    )
    process.env.HOME = home

    expect(resolveE2ECredentials()).toMatchObject({
      password: 'nested-home-secret',
      passwordSource: 'file',
    })
  })

  it('falls back to the storage-root default home initial-password path', () => {
    const home = makeTempRoot()
    writePasswordFile(
      path.join(home, '.mnemonas', 'initial-password.txt'),
      'storage-root-secret',
    )
    process.env.HOME = home

    expect(resolveE2ECredentials()).toMatchObject({
      password: 'storage-root-secret',
      passwordSource: 'file',
    })
  })

  it('does not fall back to home defaults when an explicit E2E password file is missing', () => {
    const home = makeTempRoot()
    writePasswordFile(
      path.join(home, '.mnemonas', '.mnemonas', 'initial-password.txt'),
      'nested-home-secret',
    )
    process.env.HOME = home
    process.env.E2E_PASSWORD_FILE = path.join(home, 'missing-initial-password.txt')

    expect(resolveE2ECredentials()).toMatchObject({
      password: '',
      passwordSource: 'missing',
    })
  })
})

function makeTempRoot(): string {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'mnemonas-e2e-credentials-'))
  tempRoots.push(root)
  return root
}

function writePasswordFile(filePath: string, password: string): void {
  fs.mkdirSync(path.dirname(filePath), { recursive: true })
  fs.writeFileSync(filePath, `Password: ${password}\n`, { mode: 0o600 })
}

function restoreEnv(name: keyof NodeJS.ProcessEnv, value: string | undefined): void {
  if (value === undefined) {
    delete process.env[name]
    return
  }
  process.env[name] = value
}
