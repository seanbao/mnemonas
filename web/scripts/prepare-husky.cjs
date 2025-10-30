#!/usr/bin/env node

const { existsSync } = require('node:fs')
const { join, resolve } = require('node:path')
const { spawnSync } = require('node:child_process')

const webRoot = process.env.MNEMONAS_WEB_ROOT ? resolve(process.env.MNEMONAS_WEB_ROOT) : join(__dirname, '..')
const repoRoot = resolve(webRoot, '..')
const omittedDependencyGroups = (process.env.npm_config_omit ?? '')
  .split(/[,\s]+/)
  .filter(Boolean)

if (
  process.env.HUSKY === '0'
  || process.env.npm_config_ignore_scripts === 'true'
  || process.env.npm_config_production === 'true'
  || process.env.NODE_ENV === 'production'
  || omittedDependencyGroups.includes('dev')
  || !existsSync(join(repoRoot, '.git'))
  || !existsSync(join(webRoot, '.husky'))
) {
  process.exit(0)
}

const result = spawnSync('husky', ['web/.husky'], {
  cwd: repoRoot,
  stdio: 'inherit',
  shell: process.platform === 'win32',
})

if (result.error) {
  console.error(`Failed to install Husky hooks: ${result.error.message}`)
  process.exit(1)
}

process.exit(result.status ?? 1)
