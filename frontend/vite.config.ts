/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

const backend = {
  target: `http://${process.env.DEV_HOST || '127.0.0.1'}:8090`,
  changeOrigin: true,
}

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
    // Vite owns the URL a developer opens, so anything the backend serves has
    // to be proxied through it or it simply is not reachable: dev.sh runs the
    // two servers inside a container and publishes only this port, so the
    // backend's own :8090 is not addressable from the host at all.
    //
    // /_/ is PocketBase's superuser panel. Without it here a request for /_/
    // falls through to Vite, which answers with the SPA's index.html, and the
    // app routes that path to nothing -- a blank page rather than an error,
    // which is a confusing way to find out the panel is not proxied.
    proxy: {
      '/api': backend,
      '/_/': backend,
    },
  },
})
