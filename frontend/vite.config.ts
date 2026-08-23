/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  envDir: '..',
  plugins: [react(), tailwindcss()],
  test: {
    // Unit tests only; Playwright owns e2e/.
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
