import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const STORAGE_KEY = 'mnemonas.share-download-client-nonce.v1'

function encodeNonce(bytes: Uint8Array): string {
  let binary = ''
  for (const byte of bytes) {
    binary += String.fromCharCode(byte)
  }
  return btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/u, '')
}

async function loadNonceModule() {
  return import('./shareDownloadClientNonce')
}

describe('getOrCreateShareDownloadClientNonce', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.resetModules()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('reuses a canonical persisted nonce without requesting randomness', async () => {
    const persisted = encodeNonce(Uint8Array.from({ length: 32 }, (_, index) => index))
    localStorage.setItem(STORAGE_KEY, persisted)
    const random = vi.spyOn(globalThis.crypto, 'getRandomValues')
    const { getOrCreateShareDownloadClientNonce } = await loadNonceModule()

    expect(getOrCreateShareDownloadClientNonce()).toBe(persisted)
    expect(getOrCreateShareDownloadClientNonce()).toBe(persisted)
    expect(random).not.toHaveBeenCalled()
  })

  it.each([
    ['wrong length', 'short'],
    ['padding', `${'A'.repeat(43)}=`],
    ['non URL-safe character', `${'A'.repeat(42)}+`],
    ['non-canonical tail bits', `${'A'.repeat(42)}B`],
  ])('replaces a stored %s value with secure random bytes', async (_name, invalid) => {
    localStorage.setItem(STORAGE_KEY, invalid)
    const bytes = new Uint8Array(32).fill(7)
    const random = vi.spyOn(globalThis.crypto, 'getRandomValues').mockImplementation((target) => {
      new Uint8Array(target.buffer, target.byteOffset, target.byteLength).set(bytes)
      return target
    })
    const { getOrCreateShareDownloadClientNonce } = await loadNonceModule()

    const nonce = getOrCreateShareDownloadClientNonce()

    expect(nonce).toBe(encodeNonce(bytes))
    expect(localStorage.getItem(STORAGE_KEY)).toBe(nonce)
    expect(random).toHaveBeenCalledOnce()
  })

  it('keeps one in-memory nonce when browser storage is unavailable', async () => {
    const bytes = new Uint8Array(32).fill(11)
    const storageError = new DOMException('localStorage is blocked', 'SecurityError')
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw storageError
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw storageError
    })
    const random = vi.spyOn(globalThis.crypto, 'getRandomValues').mockImplementation((target) => {
      new Uint8Array(target.buffer, target.byteOffset, target.byteLength).set(bytes)
      return target
    })
    const { getOrCreateShareDownloadClientNonce } = await loadNonceModule()

    const first = getOrCreateShareDownloadClientNonce()
    const second = getOrCreateShareDownloadClientNonce()

    expect(first).toBe(encodeNonce(bytes))
    expect(second).toBe(first)
    expect(random).toHaveBeenCalledOnce()
  })

  it('uses a canonical value won by another tab during write-back', async () => {
    const generatedBytes = new Uint8Array(32).fill(13)
    const winningNonce = encodeNonce(new Uint8Array(32).fill(17))
    let reads = 0
    const getItem = vi.spyOn(window.localStorage, 'getItem').mockImplementation(() => {
      reads += 1
      return reads === 1 ? null : winningNonce
    })
    const setItem = vi.spyOn(window.localStorage, 'setItem').mockImplementation(() => undefined)
    vi.spyOn(globalThis.crypto, 'getRandomValues').mockImplementation((target) => {
      new Uint8Array(target.buffer, target.byteOffset, target.byteLength).set(generatedBytes)
      return target
    })
    const { getOrCreateShareDownloadClientNonce } = await loadNonceModule()

    expect(getOrCreateShareDownloadClientNonce()).toBe(winningNonce)
    expect(setItem).toHaveBeenCalledWith(STORAGE_KEY, encodeNonce(generatedBytes))
    getItem.mockRestore()
    setItem.mockRestore()
  })

  it('fails closed when secure browser randomness is unavailable', async () => {
    localStorage.removeItem(STORAGE_KEY)
    vi.stubGlobal('crypto', undefined)
    vi.resetModules()
    const { getOrCreateShareDownloadClientNonce } = await loadNonceModule()

    try {
      expect(() => getOrCreateShareDownloadClientNonce()).toThrow('当前浏览器无法创建安全下载凭证')
    } finally {
      vi.unstubAllGlobals()
    }
  })
})
