/**
 * Our own writes, announced directly. PocketBase's realtime subscription is
 * optional by design, and a badge that ignores the user's own write until a
 * socket happens to be connected reads as a bug.
 */

type Listener = () => void

const listeners = new Set<Listener>()

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
