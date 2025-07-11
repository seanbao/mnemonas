#!/usr/bin/env node

const { readdirSync } = require('node:fs')
const { extname, join, resolve } = require('node:path')
const { spawnSync } = require('node:child_process')

const scriptsDir = process.env.MNEMONAS_WEB_ROOT ? join(resolve(process.env.MNEMONAS_WEB_ROOT), 'scripts') : __dirname
const nodeScriptExtensions = new Set(['.cjs', '.js', '.mjs'])

const scripts = readdirSync(scriptsDir, { withFileTypes: true })
  .filter((entry) => entry.isFile() && nodeScriptExtensions.has(extname(entry.name)))
  .map((entry) => join(scriptsDir, entry.name))
  .sort()

if (scripts.length === 0) {
  console.error('No Node tool scripts found under web/scripts.')
  process.exit(1)
}

for (const script of scripts) {
  const result = spawnSync(process.execPath, ['--check', script], {
    stdio: 'inherit',
  })

  if (result.error) {
    console.error(`Failed to check ${script}: ${result.error.message}`)
    process.exit(1)
  }

  if (result.status !== 0) {
    process.exit(result.status ?? 1)
  }
}

console.log(`[node-script-check] checked ${scripts.length} Node tool scripts`)
