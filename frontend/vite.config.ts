/// <reference types="vitest/config" />
import { existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

const here = path.dirname(fileURLToPath(import.meta.url))

/**
 * Resolves the `@ext` alias: the module supplying this build's edition.
 *
 * Selection is explicit rather than "use src/ext-cloud/ if it is there", and
 * that is the whole safety property. The backend half of an edition is chosen
 * by a Go build tag, which cannot be inferred from a directory listing, so an
 * auto-detecting frontend would happily build cloud pages against a core binary
 * that serves none of their routes. One switch drives both — see EDITION in the
 * Dockerfile.
 *
 * Keep the fallback in step with the `@ext` paths in tsconfig.app.json, or the
 * bundler and the type checker will disagree about what was built.
 */
function editionDir(): string {
  const dir = process.env.LEMMARY_EXT || './src/ext'
  const resolved = path.resolve(here, dir)
  if (!existsSync(resolved)) {
    throw new Error(`LEMMARY_EXT points at ${resolved}, which does not exist`)
  }
  return resolved
}

export default defineConfig({
  envDir: '..',
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@ext': editionDir() },
  },
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
