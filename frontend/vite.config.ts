import react from '@vitejs/plugin-react'
import { existsSync, readFileSync } from 'node:fs'
import { defineConfig } from 'vitest/config'

const backendUrl = process.env.ASTRORDER_BACKEND_URL || 'http://127.0.0.1:30002'
// Optional machine-local host allowlist; never commit private hostnames.
const hostsFile = new URL('./vite.hosts.local.json', import.meta.url)
const allowedHosts: unknown = existsSync(hostsFile) ? JSON.parse(readFileSync(hostsFile, 'utf8')) : []
if (!Array.isArray(allowedHosts) || !allowedHosts.every(host => typeof host === 'string' && host.length > 0)) {
  throw new Error('vite.hosts.local.json must contain an array of hostnames')
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    host: '127.0.0.1',
    port: 30001,
    strictPort: true,
    allowedHosts,
    proxy: {
      '/api': backendUrl,
      '/health': backendUrl,
      '/ws': { target: backendUrl.replace(/^http/, 'ws'), ws: true },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
    exclude: ['**/node_modules/**', '**/dist/**'],
  },
})
