/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  envDir: '..',
  // The backend's own names, not a VITE_ copy of them. Vite exposes only
  // VITE_-prefixed keys by default, and envDir above already points it at the
  // root .env, so widening the prefix is what lets the dev auto-login read the
  // same SETUP_ADMIN_* pair the server creates the first admin from. One
  // credential under one name; see src/lib/auth.ts for why it stays out of a
  // production bundle.
  envPrefix: ['VITE_', 'SETUP_ADMIN_'],
  plugins: [react(), tailwindcss()],
  test: {
    // Unit tests only. The browser suite is Playwright's, and lives in the
    // private development repository rather than here.
    include: ['src/**/*.test.{ts,tsx}'],
  },
  build: {
    outDir: '../public',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: `http://${process.env.DEV_HOST || '127.0.0.1'}:8090`,
        changeOrigin: true,
      },
    },
  },
})
