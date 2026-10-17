import type { RecordModel } from 'pocketbase'
import { useEffect, useState } from 'react'
import { adoptLanguage, languageNames, type Language } from '../i18n'
import { AuthRequiredError } from './auth'
import { pb } from './pb'

/** What a document card shows under its title: the summary, or the first page's preview image. */
export type CardLayout = 'summary' | 'preview'

/**
 * The account's preferences: one JSON field on users, so a new key needs no
 * migration. The account writes the field itself, so every key is read
 * defensively and anything unknown counts as no choice.
 */
export type Preferences = { card_layout?: CardLayout; language?: Language }

function preferencesOf(record: RecordModel | null): Record<string, unknown> {
  const stored: unknown = record?.preferences
  return stored && typeof stored === 'object' && !Array.isArray(stored) ? (stored as Record<string, unknown>) : {}
}

/** No choice, and anything this build does not know, is the summary. */
export function cardLayoutOf(record: RecordModel | null): CardLayout {
  return preferencesOf(record).card_layout === 'preview' ? 'preview' : 'summary'
}

/** No choice, and anything this build does not know, leaves the browser's language. */
export function languageOf(record: RecordModel | null): Language | null {
  const stored = preferencesOf(record).language
  return typeof stored === 'string' && Object.hasOwn(languageNames, stored) ? (stored as Language) : null
}

/** Follows the signed-in account's record, so a saved change applies without a reload. */
export function useCardLayout(): CardLayout {
  const [layout, setLayout] = useState(() => cardLayoutOf(pb.authStore.record))
  useEffect(() => pb.authStore.onChange((_token, record) => setLayout(cardLayoutOf(record))), [])
  return layout
}

/**
 * Merged onto the stored object, read fresh: the field is written whole, and the
 * copy from sign-in would drop a key another device saved since. The SDK writes
 * the saved record back into the auth store, which useCardLayout follows.
 */
export async function savePreferences(patch: Preferences) {
  const record = pb.authStore.record
  if (!record) {
    throw new AuthRequiredError()
  }
  const users = pb.collection('users')
  const stored = await users.getOne(record.id, { fields: 'preferences', requestKey: null })
  await users.update(record.id, { preferences: { ...preferencesOf(stored), ...patch } })
}

/** The account's language wins over this browser's, once the account has chosen one. */
export function applyAccountLanguage() {
  const language = languageOf(pb.authStore.record)
  if (language) {
    adoptLanguage(language)
  }
}
