const SHARE_DOWNLOAD_CLIENT_NONCE_KEY = 'mnemonas.share-download-client-nonce.v1'
const SHARE_DOWNLOAD_CLIENT_NONCE_BYTES = 32
const SHARE_DOWNLOAD_CLIENT_NONCE_LENGTH = 43

let cachedNonce: string | null = null

function encodeBase64Url(bytes: Uint8Array): string {
  let binary = ''
  for (const byte of bytes) {
    binary += String.fromCharCode(byte)
  }
  return btoa(binary)
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replace(/=+$/u, '')
}

function decodeCanonicalNonce(value: string): Uint8Array | null {
  if (value.length !== SHARE_DOWNLOAD_CLIENT_NONCE_LENGTH || !/^[A-Za-z0-9_-]+$/u.test(value)) {
    return null
  }

  try {
    const padded = `${value.replaceAll('-', '+').replaceAll('_', '/')}=`
    const binary = atob(padded)
    if (binary.length !== SHARE_DOWNLOAD_CLIENT_NONCE_BYTES) {
      return null
    }
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
    return encodeBase64Url(bytes) === value ? bytes : null
  } catch {
    return null
  }
}

function readStoredNonce(): string | null {
  try {
    if (typeof localStorage === 'undefined') {
      return null
    }
    const value = localStorage.getItem(SHARE_DOWNLOAD_CLIENT_NONCE_KEY)
    return value && decodeCanonicalNonce(value) ? value : null
  } catch {
    return null
  }
}

function persistAndReadBackNonce(value: string): string {
  try {
    if (typeof localStorage === 'undefined') {
      return value
    }
    localStorage.setItem(SHARE_DOWNLOAD_CLIENT_NONCE_KEY, value)
    const stored = localStorage.getItem(SHARE_DOWNLOAD_CLIENT_NONCE_KEY)
    return stored && decodeCanonicalNonce(stored) ? stored : value
  } catch {
    return value
  }
}

function createNonce(): string {
  if (!globalThis.crypto || typeof globalThis.crypto.getRandomValues !== 'function') {
    throw new Error('当前浏览器无法创建安全下载凭证')
  }
  const bytes = new Uint8Array(SHARE_DOWNLOAD_CLIENT_NONCE_BYTES)
  globalThis.crypto.getRandomValues(bytes)
  return encodeBase64Url(bytes)
}

export function getOrCreateShareDownloadClientNonce(): string {
  if (cachedNonce) {
    return cachedNonce
  }

  const stored = readStoredNonce()
  if (stored) {
    cachedNonce = stored
    return stored
  }

  cachedNonce = persistAndReadBackNonce(createNonce())
  return cachedNonce
}
