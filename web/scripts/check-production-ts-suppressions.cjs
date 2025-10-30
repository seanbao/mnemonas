#!/usr/bin/env node

const { existsSync, readdirSync, readFileSync } = require('node:fs')
const { join, relative, resolve, sep } = require('node:path')

const webRoot = process.env.MNEMONAS_WEB_ROOT ? resolve(process.env.MNEMONAS_WEB_ROOT) : join(__dirname, '..')
const srcRoot = join(webRoot, 'src')
const sourceFilePattern = /\.[cm]?[tj]sx?$/
const testFilePattern = /\.(?:test|spec)\.[cm]?[tj]sx?$/
const suppressionPattern = /@ts-(?:ignore|expect-error|nocheck)\b/

function shouldSkipFile(file) {
  const parts = relative(srcRoot, file).split(sep)
  return testFilePattern.test(file) || parts.includes('test') || parts.includes('__mocks__')
}

function listSourceFiles(dir) {
  if (!existsSync(dir)) {
    return []
  }

  const entries = readdirSync(dir, { withFileTypes: true })
  const files = []

  for (const entry of entries) {
    const fullPath = join(dir, entry.name)
    if (entry.isDirectory()) {
      files.push(...listSourceFiles(fullPath))
      continue
    }
    if (entry.isFile() && sourceFilePattern.test(entry.name) && !shouldSkipFile(fullPath)) {
      files.push(fullPath)
    }
  }

  return files
}

const failures = []

for (const file of listSourceFiles(srcRoot)) {
  const source = readFileSync(file, 'utf8')
  const lines = source.split(/\r?\n/)
  lines.forEach((line, index) => {
    if (suppressionPattern.test(line)) {
      failures.push(`${relative(webRoot, file)}:${index + 1}`)
    }
  })
}

if (failures.length > 0) {
  console.error('Production source files must not suppress TypeScript errors:')
  for (const failure of failures) {
    console.error(`  ${failure}`)
  }
  process.exit(1)
}

console.log('[production-ts-suppression-check] no production TypeScript suppressions found')
