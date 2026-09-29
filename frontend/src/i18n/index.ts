import { createElement, Fragment, type ReactNode } from 'react'
import { de } from './de'
import { en } from './en'
import { ru } from './ru'

export const languageNames = { en: 'English', de: 'Deutsch', ru: 'Русский' } as const
export type Language = keyof typeof languageNames

export type Plural = Partial<Record<Intl.LDMLPluralRule, string>> & { other: string }
export type Translation<T> = { [K in keyof T]: T[K] extends string ? string : Plural }
export type MessageKey = keyof typeof en
export type Catalog = Translation<typeof en>

const catalogs: Record<Language, Catalog> = { en, de, ru }
// A cookie rather than localStorage: the backend reads it too, for its own messages.
const cookieName = 'lemmary_lang'
const placeholder = /\{(\w+)\}/g

/** The stored choice first, then the browser's own list, by primary subtag. */
export function pickLanguage(stored: string | null, preferred: readonly string[]): Language {
  for (const tag of [stored ?? '', ...preferred]) {
    const code = tag.toLowerCase().split('-')[0]
    if (Object.hasOwn(languageNames, code)) return code as Language
  }
  return 'en'
}

function detect(): Language {
  if (typeof document === 'undefined') return 'en'
  let stored: string | null = null
  try {
    stored = new RegExp(`(?:^|; )${cookieName}=([^;]*)`).exec(document.cookie)?.[1] ?? null
  } catch {
    // Site data blocked: follow the browser.
  }
  return pickLanguage(stored, navigator.languages)
}

export const lang = detect()
if (typeof document !== 'undefined') document.documentElement.lang = lang

/** Reloads: every label in the app is read once, at load. */
export function setLanguage(next: Language) {
  try {
    document.cookie = `${cookieName}=${next}; path=/; max-age=31536000; SameSite=Lax`
  } catch {
    // Site data blocked: nothing to remember it in.
  }
  window.location.reload()
}

type Vars = Record<string, string | number>

export function translator(language: Language) {
  const catalog = catalogs[language]
  const rules = new Intl.PluralRules(language)
  const template = (key: MessageKey, count: unknown): string => {
    const entry = catalog[key]
    return typeof entry === 'string' ? entry : (entry[rules.select(Number(count))] ?? entry.other)
  }
  return {
    t(key: MessageKey, vars: Vars = {}): string {
      return template(key, vars.count).replace(placeholder, (match, name: string) =>
        Object.hasOwn(vars, name) ? String(vars[name]) : match,
      )
    },
    /** For a sentence with elements in it: `{link}` is replaced by `values.link`. */
    tNode(key: MessageKey, values: Record<string, ReactNode>): ReactNode {
      const parts = template(key, values.count).split(placeholder)
      return createElement(
        Fragment,
        null,
        ...parts.map((part, i) =>
          i % 2 === 0 ? part : Object.hasOwn(values, part) ? values[part] : `{${part}}`,
        ),
      )
    },
  }
}

// ponytail: every catalog is bundled; lazy-load the non-English ones if bundle size matters.
export const { t, tNode } = translator(lang)

export function docsUrl(page = ''): string {
  return lang === 'en' ? `/docs/${page}` : `/docs/${lang}/${page}`
}
