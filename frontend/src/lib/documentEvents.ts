/**
 * "The documents changed, and I know because I changed them."
 *
 * The list and the header's Inbox count both learn about changes from
 * PocketBase's realtime subscription, which is optional by design. A badge or a
 * list that ignores the user's own write until a socket happens to be connected
 * reads as a bug, so writes say so directly and realtime stays the way to hear
 * about changes made elsewhere.
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
