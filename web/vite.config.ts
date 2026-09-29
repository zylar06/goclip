import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// The repository VERSION is the only application version source.
const version = readFileSync(new URL('../VERSION', import.meta.url), 'utf8').trim()
if (!version) throw new Error('../VERSION must not be empty')

export default defineConfig({
  plugins: [react()],
  define: { __APP_VERSION__: JSON.stringify(version) },
  server: {
    proxy: { '/api': { target: process.env.AUTOCLIP_API_ORIGIN || 'http://127.0.0.1:8080' } },
    fs: { allow: [fileURLToPath(new URL('.', import.meta.url))] },
  },
  build: { sourcemap: false },
  test: {
    environment: 'jsdom',
    setupFiles: ['./tests/setup.ts'],
    restoreMocks: true,
    clearMocks: true,
    testTimeout: 10000,
    maxWorkers: 2,
  },
})
