const [major, minor] = process.versions.node
  .split('.')
  .map((part) => Number.parseInt(part, 10))

const supported = (major === 20 && minor >= 19) || major > 22 || (major === 22 && minor >= 12)

if (!supported) {
  console.error(`Node.js ^20.19.0 or >=22.12.0 is required for web commands. Current: ${process.versions.node}.`)
  console.error('Load the repository version with: source "$HOME/.nvm/nvm.sh" && nvm use')
  process.exit(1)
}
