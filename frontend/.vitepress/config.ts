import { existsSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig, type DefaultTheme, type HeadConfig } from 'vitepress'

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const vuePkg = path.join(frontendRoot, 'node_modules/vue')
const docsRoot = path.resolve(frontendRoot, '../docs')

type Lang = 'en' | 'de' | 'ru'
type Label = Record<Lang, string>

const langs: Lang[] = ['en', 'de', 'ru']
const site = 'https://lemmary.app/docs/'

for (const lang of ['de', 'ru']) {
  for (const page of readdirSync(docsRoot).filter((name) => name.endsWith('.md'))) {
    if (!existsSync(path.join(docsRoot, lang, page))) {
      throw new Error(`docs/${lang}/${page} is missing: every docs page has a de and a ru copy`)
    }
  }
}

const nav: Array<[string, Label]> = [
  ['screenshots', { en: 'Screenshots', de: 'Screenshots', ru: 'Скриншоты' }],
  ['comparison', { en: 'Compare', de: 'Vergleich', ru: 'Сравнение' }],
  ['deep_research', { en: 'Deep Research', de: 'Deep Research', ru: 'Deep Research' }],
  ['self_hosting', { en: 'Self-hosting', de: 'Self-Hosting', ru: 'Хостинг' }],
  ['setup', { en: 'Configure', de: 'Konfiguration', ru: 'Настройка' }],
  ['guided_ai_setup', { en: 'AI setup', de: 'KI-Einrichtung', ru: 'Настройка ИИ' }],
  ['ai_providers', { en: 'AI providers', de: 'KI-Anbieter', ru: 'Провайдеры ИИ' }],
  ['passkeys', { en: 'Passkeys', de: 'Passkeys', ru: 'Ключи доступа' }],
]

const guides: Array<[string, Label]> = [
  ['screenshots', { en: 'Screenshots', de: 'Screenshots', ru: 'Скриншоты' }],
  [
    'comparison',
    { en: 'Lemmary vs alternatives', de: 'Lemmary im Vergleich', ru: 'Lemmary и альтернативы' },
  ],
  ['deep_research', { en: 'Deep Research', de: 'Deep Research', ru: 'Deep Research' }],
  [
    'self_hosting',
    {
      en: 'Self-hosting with Docker',
      de: 'Self-Hosting mit Docker',
      ru: 'Самостоятельный хостинг в Docker',
    },
  ],
  [
    'guided_ai_setup',
    {
      en: 'Guided AI provider setup',
      de: 'Geführte Einrichtung des KI-Anbieters',
      ru: 'Пошаговая настройка провайдера ИИ',
    },
  ],
  ['setup', { en: 'Configuration Guide', de: 'Konfigurationsleitfaden', ru: 'Руководство по настройке' }],
  [
    'development',
    { en: 'Development environment', de: 'Entwicklungsumgebung', ru: 'Среда разработки' },
  ],
  ['storage', { en: 'Storage', de: 'Speicher', ru: 'Хранилище' }],
  [
    'ai_providers',
    { en: 'AI providers and models', de: 'KI-Anbieter und Modelle', ru: 'Провайдеры и модели ИИ' },
  ],
  ['paperless_ngx', { en: 'Paperless-ngx API', de: 'Paperless-ngx-API', ru: 'API Paperless-ngx' }],
  ['mcp', { en: 'MCP for agents', de: 'MCP für Agenten', ru: 'MCP для агентов' }],
  ['scanning', { en: 'Scanning', de: 'Scannen', ru: 'Сканирование' }],
  ['local_ocr', { en: 'Local OCR', de: 'Lokales OCR', ru: 'Локальное OCR' }],
  [
    'local_embeddings',
    { en: 'Local embeddings', de: 'Lokale Embeddings', ru: 'Локальные эмбеддинги' },
  ],
  ['local_ai_macos', { en: 'Local AI on a Mac', de: 'Lokale KI auf dem Mac', ru: 'Локальный ИИ на Mac' }],
  ['google_vision', { en: 'Google Vision', de: 'Google Vision', ru: 'Google Vision' }],
  ['chatgpt_login', { en: 'ChatGPT sign-in', de: 'ChatGPT-Anmeldung', ru: 'Вход через ChatGPT' }],
  ['oauth', { en: 'OAuth2', de: 'OAuth2', ru: 'OAuth2' }],
  ['passkeys', { en: 'Passkeys', de: 'Passkeys', ru: 'Ключи доступа' }],
  [
    'encryption',
    { en: 'Encryption at rest', de: 'Verschlüsselung im Ruhezustand', ru: 'Шифрование данных' },
  ],
]

const chrome: Record<Exclude<Lang, 'en'>, DefaultTheme.Config> = {
  de: {
    outline: { label: 'Auf dieser Seite' },
    docFooter: { prev: 'Vorherige Seite', next: 'Nächste Seite' },
    sidebarMenuLabel: 'Menü',
    returnToTopLabel: 'Zurück nach oben',
    darkModeSwitchLabel: 'Darstellung',
    langMenuLabel: 'Sprache ändern',
  },
  ru: {
    outline: { label: 'На этой странице' },
    docFooter: { prev: 'Предыдущая страница', next: 'Следующая страница' },
    sidebarMenuLabel: 'Меню',
    returnToTopLabel: 'Наверх',
    darkModeSwitchLabel: 'Оформление',
    langMenuLabel: 'Сменить язык',
  },
}

function themeFor(lang: Lang): DefaultTheme.Config {
  const prefix = lang === 'en' ? '' : `/${lang}`
  return {
    ...(lang === 'en' ? {} : chrome[lang]),
    nav: nav.map(([page, text]) => ({ text: text[lang], link: `${prefix}/${page}` })),
    sidebar: [
      {
        text: { en: 'Guides', de: 'Anleitungen', ru: 'Руководства' }[lang],
        items: guides.map(([page, text]) => ({ text: text[lang], link: `${prefix}/${page}` })),
      },
    ],
  }
}

function urlFor(page: string, lang: Lang) {
  const file = page.replace(/(^|\/)index\.md$/, '$1').replace(/\.md$/, '.html')
  return `${site}${lang === 'en' ? '' : `${lang}/`}${file}`
}

export default defineConfig({
  title: 'Lemmary',
  head: [['link', { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }]],
  srcDir: '../docs',
  base: '/docs/',
  outDir: '../public/docs',
  // Repo path links (e.g. ../backend/...) are intentional; they are not docs pages.
  ignoreDeadLinks: [/\.\.\//],
  sitemap: { hostname: site },
  transformHead({ pageData, title, description }) {
    if (pageData.isNotFound) return
    const lang = langs.find((l) => pageData.relativePath.startsWith(`${l}/`)) ?? 'en'
    const page = lang === 'en' ? pageData.relativePath : pageData.relativePath.slice(lang.length + 1)
    const url = urlFor(page, lang)
    return [
      ['link', { rel: 'canonical', href: url }],
      ...langs.map((l): HeadConfig => ['link', { rel: 'alternate', hreflang: l, href: urlFor(page, l) }]),
      ['link', { rel: 'alternate', hreflang: 'x-default', href: urlFor(page, 'en') }],
      ['meta', { property: 'og:url', content: url }],
      ['meta', { property: 'og:title', content: title }],
      ['meta', { property: 'og:description', content: description }],
      ['meta', { property: 'og:image', content: 'https://lemmary.app/social-card.png' }],
      ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
    ]
  },
  locales: {
    root: {
      label: 'English',
      lang: 'en',
      description: 'Setup and operation documentation',
      themeConfig: themeFor('en'),
    },
    de: {
      label: 'Deutsch',
      lang: 'de',
      description: 'Dokumentation zu Einrichtung und Betrieb',
      themeConfig: themeFor('de'),
    },
    ru: {
      label: 'Русский',
      lang: 'ru',
      description: 'Документация по установке и эксплуатации',
      themeConfig: themeFor('ru'),
    },
  },
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
})
