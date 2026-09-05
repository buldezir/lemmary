import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitepress'

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const vuePkg = path.join(frontendRoot, 'node_modules/vue')

export default defineConfig({
  title: 'Lemmary',
  description: 'Setup and operation documentation',
  head: [['link', { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }]],
  srcDir: '../docs',
  base: '/docs/',
  outDir: '../public/docs',
  // Repo path links (e.g. ../backend/...) are intentional; they are not docs pages.
  ignoreDeadLinks: [/\.\.\//],
  vite: {
    resolve: {
      alias: [
        {
          find: 'vue/server-renderer',
          replacement: path.join(vuePkg, 'server-renderer/index.mjs'),
        },
        {
          find: 'vue',
          replacement: path.join(vuePkg, 'dist/vue.runtime.esm-bundler.js'),
        },
      ],
    },
  },
  themeConfig: {
    nav: [
      { text: 'Screenshots', link: '/screenshots' },
      { text: 'Compare', link: '/comparison' },
      { text: 'Self-hosting', link: '/self_hosting' },
      { text: 'Configure', link: '/setup' },
      { text: 'AI providers', link: '/ai_providers' },
      { text: 'Passkeys', link: '/passkeys' },
    ],
    sidebar: [
      {
        text: 'Guides',
        items: [
          { text: 'Screenshots', link: '/screenshots' },
          { text: 'Lemmary vs alternatives', link: '/comparison' },
          { text: 'Self-hosting with Docker', link: '/self_hosting' },
          { text: 'Configuration Guide', link: '/setup' },
          { text: 'Development environment', link: '/development' },
          { text: 'Storage', link: '/storage' },
          { text: 'AI providers and models', link: '/ai_providers' },
          { text: 'Local OCR', link: '/local_ocr' },
          { text: 'Local embeddings', link: '/local_embeddings' },
          { text: 'Google Vision', link: '/google_vision' },
          { text: 'OAuth2', link: '/oauth' },
          { text: 'Passkeys', link: '/passkeys' },
          { text: 'Encryption at rest', link: '/encryption' },
        ],
      },
    ],
  },
})
