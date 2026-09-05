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

// VitePress builds the docs with base '/docs/' (see .vitepress/config.ts), and
// in production PocketBase serves them out of public/. In dev they need a
// server of their own, and this is the port it gets.
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
 * It lives here rather than in the `dev` script because the script is not the
 * only way this server starts: scripts/dev.sh runs the two halves inside a
 * container and publishes exactly one port, and it does that by invoking `vite`
 * directly with a port of its choosing. A docs server started next to it by
 * `concurrently` was both unreachable (its port is not published) and, in that
 * path, never started at all -- which is why an in-app /docs link answered with
 * the SPA. Hanging it off the Vite server itself means every way of starting
 * the app gets the docs too.
 *
 * A server already on the port is left alone, so `pnpm run docs:dev` in another
 * terminal keeps working.
 */
function docsDevServer(): Plugin {
  return {
    name: 'lemmary-docs-dev-server',
    apply: 'serve',
    async configureServer(server) {
      // No http server means nobody can browse to /docs anyway: this is Vitest,
      // or another embedder running Vite in middleware mode. Starting a docs
      // process there just hangs the run that spawned it.
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

      // Deliberately not a SIGINT/SIGTERM handler of our own: registering one
      // suppresses Node's default terminate and Vite's handler does not make up
      // for it, so the cure was Ctrl+C no longer stopping Vite. What is left
      // covers the ways this actually shuts down -- Ctrl+C signals the whole
      // process group, so the child gets it first-hand, and a SIGTERM (what
      // scripts/dev.sh sends) closes the http server on the way out.
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
      // Same reasoning as /_/ above: unproxied, /docs falls through to the
      // SPA's index.html and the in-app documentation links land on a blank
      // route instead of the page they name.
      '/docs': docs,
    },
  },
})
