import { defineConfig } from 'vite'
import { webcrypto } from 'node:crypto'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

const apiProxyTarget = process.env.VITE_API_PROXY_TARGET || 'http://localhost:8080'
const apiProxy = {
  '/api': {
    target: apiProxyTarget,
    changeOrigin: false,
  },
  '/dav': {
    target: apiProxyTarget,
    changeOrigin: false,
  },
  '/health': {
    target: apiProxyTarget,
    changeOrigin: false,
  },
}

if (!globalThis.crypto || typeof globalThis.crypto.getRandomValues !== 'function') {
  globalThis.crypto = webcrypto as typeof globalThis.crypto
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  build: {
    rollupOptions: {
      output: {
        // Keep only coarse-grained chunks for the heaviest stable dependencies.
        // Let Vite/Rollup split UI libraries with the route graph instead of forcing
        // all HeroUI code into a single shared chunk.
        manualChunks(id) {
          if (id.includes('node_modules/framer-motion')) {
            return 'motion'
          }

          if (
            id.includes('node_modules/react/') ||
            id.includes('node_modules/react-dom/') ||
            id.includes('node_modules/react-router-dom/')
          ) {
            return 'vendor'
          }

          if (id.includes('node_modules/@tanstack/react-query')) {
            return 'query'
          }

          return undefined
        },
      },
    },
  },
  server: {
    proxy: apiProxy,
  },
  preview: {
    proxy: apiProxy,
  },
})
