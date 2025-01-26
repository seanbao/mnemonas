const major = Number.parseInt(process.versions.node.split('.')[0], 10)

if (!Number.isFinite(major) || major < 20) {
  console.error(`Node.js 20+ is required for web commands. Current: ${process.versions.node}.`)
  console.error('Load the repository version with: source "$HOME/.nvm/nvm.sh" && nvm use')
  process.exit(1)
}
