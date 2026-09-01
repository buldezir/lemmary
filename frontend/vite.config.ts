/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  envDir: '..',
  // Same SETUP_ADMIN_* names the server uses; Vite only exposes VITE_ by default.
  // Dev-only; see src/lib/auth.ts.
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
