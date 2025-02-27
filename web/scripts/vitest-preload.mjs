import crypto from 'node:crypto'

if (typeof crypto.getRandomValues !== 'function') {
	crypto.getRandomValues = crypto.webcrypto.getRandomValues.bind(crypto.webcrypto)
}

globalThis.crypto = crypto.webcrypto
global.crypto = crypto.webcrypto
