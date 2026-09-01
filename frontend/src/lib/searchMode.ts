import type { SearchMode } from './api/ai'

const storageKey = 'lemmary.search-mode'
const fallback: SearchMode = 'search'

function isSearchMode(value: unknown): value is SearchMode {
  return value === 'search' || value === 'research'
}

/**
 * The Deep Search mode to start a new chat in.
 *
 * The toggle is a working preference, not a per-chat setting: leaving the page
 * and coming back, or reloading it, should not quietly drop someone back into
 * Search when they have been researching. An existing conversation still
 * overrides this with the mode its own last turn ran in — continuing a research
 * chat as a plain search would answer a different question than the one above
 * it in the transcript.
 *
 * Storage is best-effort in both directions. A browser that refuses it (private
 * mode, site data blocked) throws on access rather than returning null, and a
 * remembered toggle position is never worth failing a page render over.
 */
export function readPreferredSearchMode(): SearchMode {
  try {
    const stored = globalThis.localStorage?.getItem(storageKey)
    return isSearchMode(stored) ? stored : fallback
  } catch {
    return fallback
  }
}

export function writePreferredSearchMode(mode: SearchMode) {
  try {
    globalThis.localStorage?.setItem(storageKey, mode)
  } catch {
    // Ignored: see readPreferredSearchMode.
  }
}
