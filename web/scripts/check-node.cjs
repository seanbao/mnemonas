const { readFileSync } = require('node:fs')
const { join, resolve } = require('node:path')

const webRoot = process.env.MNEMONAS_WEB_ROOT ? resolve(process.env.MNEMONAS_WEB_ROOT) : join(__dirname, '..')
const packagePath = join(webRoot, 'package.json')
const packageJson = JSON.parse(readFileSync(packagePath, 'utf8'))
const engineRange = packageJson.engines?.node

function fail(message) {
  console.error(message)
  process.exit(1)
}

function parseVersion(value) {
  const match = /^v?(\d+)\.(\d+)\.(\d+)$/.exec(value.trim())
  if (!match) {
    fail(`Unsupported Node.js version format: ${value}`)
  }
  return match.slice(1).map((part) => Number.parseInt(part, 10))
}

function compareVersions(left, right) {
  for (let index = 0; index < 3; index += 1) {
    if (left[index] !== right[index]) {
      return left[index] - right[index]
    }
  }
  return 0
}

function satisfiesClause(version, clause) {
  const trimmed = clause.trim()
  if (trimmed.startsWith('^')) {
    const minimum = parseVersion(trimmed.slice(1))
    return version[0] === minimum[0] && compareVersions(version, minimum) >= 0
  }
  if (trimmed.startsWith('>=')) {
    return compareVersions(version, parseVersion(trimmed.slice(2))) >= 0
  }
  fail(`Unsupported Node.js engine clause in ${packagePath}: ${clause}`)
}

function satisfiesEngineRange(version, range) {
  return range
    .split('||')
    .some((clause) => satisfiesClause(version, clause))
}

if (typeof engineRange !== 'string' || engineRange.trim() === '') {
  fail(`${packagePath}: missing engines.node`)
}

const currentVersion = parseVersion(process.versions.node)

if (!satisfiesEngineRange(currentVersion, engineRange)) {
  console.error(`Node.js ${engineRange} is required for web commands. Current: ${process.versions.node}.`)
  console.error('Load the repository version with: source "$HOME/.nvm/nvm.sh" && nvm use')
  process.exit(1)
}
