const major = Number.parseInt(process.versions.node.split('.')[0], 10)

if (!Number.isFinite(major) || major < 20) {
  console.error(`Node.js 20+ is required for web tests. Current: ${process.versions.node}.`)
  process.exit(1)
}
