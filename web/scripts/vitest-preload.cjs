const crypto = require('node:crypto')

if (typeof crypto.getRandomValues !== 'function') {
	crypto.getRandomValues = crypto.webcrypto.getRandomValues.bind(crypto.webcrypto)
}

global.crypto = crypto.webcrypto
globalThis.crypto = crypto.webcrypto
