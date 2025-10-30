#!/usr/bin/env node

const { existsSync, readdirSync, readFileSync } = require('node:fs')
const { extname, join, relative, resolve } = require('node:path')

const webRoot = process.env.MNEMONAS_WEB_ROOT ? resolve(process.env.MNEMONAS_WEB_ROOT) : join(__dirname, '..')
const expectedPackageName = 'mnemonas-web'
const scannableExtensions = new Set(['.cjs', '.css', '.js', '.json', '.md', '.mjs', '.ts', '.tsx'])

function readJson(path) {
  return JSON.parse(readFileSync(path, 'utf8'))
}

function fail(message) {
  console.error(`[project-identity-check] ${message}`)
  process.exit(1)
}

function requireEqual(label, actual, expected) {
  if (actual !== expected) {
    fail(`${label} = ${JSON.stringify(actual)}, expected ${JSON.stringify(expected)}`)
  }
}

function listFiles(dir) {
  if (!existsSync(dir)) {
    return []
  }

  const entries = readdirSync(dir, { withFileTypes: true })
  const files = []

  for (const entry of entries) {
    const fullPath = join(dir, entry.name)
    if (entry.isDirectory()) {
      files.push(...listFiles(fullPath))
      continue
    }
    if (entry.isFile() && scannableExtensions.has(extname(entry.name)) && !shouldSkipFile(fullPath)) {
      files.push(fullPath)
    }
  }

  return files
}

function shouldSkipFile(file) {
  return relative(webRoot, file) === 'scripts/check-project-identity.cjs'
}

function lineNumberAt(source, index) {
  return source.slice(0, index).split('\n').length
}

function findForbiddenTerms(file, terms) {
  const source = readFileSync(file, 'utf8')
  const failures = []

  for (const term of terms) {
    let index = source.indexOf(term)
    while (index !== -1) {
      failures.push(`${relative(webRoot, file)}:${lineNumberAt(source, index)}: ${term}`)
      index = source.indexOf(term, index + term.length)
    }
  }

  return failures
}

const packageJson = readJson(join(webRoot, 'package.json'))
const packageLock = readJson(join(webRoot, 'package-lock.json'))
const lockRoot = packageLock.packages?.[''] ?? {}

requireEqual('web/package.json name', packageJson.name, expectedPackageName)
requireEqual('web/package-lock.json name', packageLock.name, expectedPackageName)
requireEqual('web/package-lock.json root package name', lockRoot.name, expectedPackageName)

const oldProjectName = ['Meri', 'dian'].join('')
const oldProjectNameLower = oldProjectName.toLowerCase()
const forbiddenTerms = [
  oldProjectName,
  oldProjectNameLower,
  ['card', oldProjectNameLower].join('-'),
  ['gradient', oldProjectNameLower].join('-'),
  ['gradient', oldProjectNameLower, 'subtle'].join('-'),
]

const scanRoots = ['src', 'e2e', 'scripts']
const rootFiles = ['README.md', 'README.en.md', 'package.json', 'package-lock.json']
const files = [
  ...rootFiles.map((file) => join(webRoot, file)).filter(existsSync),
  ...scanRoots.flatMap((dir) => listFiles(join(webRoot, dir))),
]

const failures = files.flatMap((file) => findForbiddenTerms(file, forbiddenTerms))

if (failures.length > 0) {
  console.error('Frontend files contain stale project identity terms:')
  for (const failure of failures) {
    console.error(`  ${failure}`)
  }
  process.exit(1)
}

console.log(`[project-identity-check] package ${expectedPackageName} and frontend identity terms are consistent`)
