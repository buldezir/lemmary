/**
 * A nudge, in-tab, for "the documents changed and I know because I changed
 * them".
 *
 * The list page learns about changes from PocketBase's realtime subscription,
 * but that connection is optional by design -- the page keeps working without
 * it, it just stops refreshing on its own. The header's Inbox count cannot
 * afford that: a badge that stays at 3 after the user clears all three reads as
 * a bug. So a write says so directly, and realtime remains the way to hear
 * about changes made *elsewhere*.
 *
 * Deliberately not an EventTarget: no DOM, nothing to clean up but the
 * unsubscribe, and it is testable without a document.
 */

type Listener = () => void

const listeners = new Set<Listener>()

/** Subscribes; returns the unsubscribe. */
export function onDocumentsChanged(listener: Listener): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function notifyDocumentsChanged(): void {
  // Copied first: a listener that unsubscribes itself while we notify must not
  // shorten the set we are walking.
  for (const listener of [...listeners]) {
    listener()
  }
}
