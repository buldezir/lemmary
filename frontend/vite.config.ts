/// <reference types="vitest/config" />
import { spawn } from 'node:child_process'
import net from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

const frontendRoot = path.dirname(fileURLToPath(import.meta.url))

const backend = {
  target: `http://${process.env.DEV_HOST || '127.0.0.1'}:8090`,
  changeOrigin: true,
}

// In production PocketBase serves the built docs out of public/; in dev they
// need a server of their own, on this port.
const DOCS_PORT = 5174
const docs = {
  target: `http://127.0.0.1:${DOCS_PORT}`,
  // The docs client opens an HMR socket back through whatever origin served
  // the page, which is this one.
  ws: true,
}

function listening(port: number) {
  return new Promise<boolean>((resolve) => {
    const socket = net.connect({ port, host: '127.0.0.1' })
    const done = (answer: boolean) => {
      socket.destroy()
      resolve(answer)
    }
    socket.setTimeout(300)
    socket.once('connect', () => done(true))
    socket.once('error', () => done(false))
    socket.once('timeout', () => done(false))
  })
}

/**
 * Starts the VitePress dev server alongside this one, so /docs is reachable
 * through the same origin as the app.
 *
 * Here rather than in the `dev` script because scripts/dev.sh invokes `vite`
 * directly, so a docs server started beside the script never ran at all and
 * in-app /docs links answered with the SPA. A server already on the port is
 * left alone, so `pnpm run docs:dev` keeps working.
 */
function docsDevServer(): Plugin {
  return {
    name: 'lemmary-docs-dev-server',
    apply: 'serve',
    async configureServer(server) {
      // No http server means Vitest or another middleware-mode embedder, where
      // spawning a docs process just hangs the run.
      if (!server.httpServer) return
      if (await listening(DOCS_PORT)) return

      const child = spawn(
        path.join(frontendRoot, 'node_modules/.bin/vitepress'),
        ['dev', '--host', '127.0.0.1', '--port', String(DOCS_PORT), '--strictPort'],
        { cwd: frontendRoot, stdio: 'inherit' },
      )
      child.on('error', (err) => {
        server.config.logger.warn(`docs server failed to start: ${err.message}`)
      })

      // Deliberately no SIGINT/SIGTERM handler of our own: registering one
      // suppresses Node's default terminate, and the cure was Ctrl+C no longer
      // stopping Vite. Ctrl+C signals the whole process group so the child gets
      // it first-hand, and a SIGTERM closes the http server on the way out.
      const stop = () => {
        child.kill('SIGTERM')
      }
      server.httpServer?.once('close', stop)
      process.once('exit', stop)
    },
  }
}

export default defineConfig({
  envDir: '..',
  // Same SETUP_ADMIN_* names the server uses; Vite only exposes VITE_ by default.
  // Dev-only; see src/lib/auth.ts.
  envPrefix: ['VITE_', 'SETUP_ADMIN_'],
  plugins: [react(), tailwindcss(), docsDevServer()],
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
    // dev.sh publishes only this port, so anything the backend serves has to be
    // proxied through it or it is not reachable at all. Unproxied, /_/
    // (PocketBase's superuser panel) falls through to the SPA's index.html and
    // answers with a blank route rather than an error.
    proxy: {
      '/api': backend,
      '/_/': backend,
      // Same reasoning as /_/ above.
      '/docs': docs,
    },
  },
})
