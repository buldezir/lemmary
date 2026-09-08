/**
 * An id for one Deep Search run, generated here rather than by the server.
 *
 * Why the client mints it: a run outlives its connection, so aborting the
 * stream does not stop the work — cancelling has to be said out loud, by
 * POSTing this id to /search/cancel. If the server minted it and sent it back
 * in the first event, Cancel would do nothing until that event arrived, which
 * on a Deep Search is seconds of fanning out across documents and exactly when
 * someone is most likely to press it.
 *
 * Why it needs no randomness worth the name: the server treats it as an opaque
 * key and cancelSearchRun scopes the lookup to the owner, so a guessed id from
 * another account finds nothing. Uniqueness among one account's live runs is
 * the entire requirement. It is not a credential.
 *
 * So no crypto. `crypto.randomUUID` was the first spelling of this and it was
 * a bug: it exists only in a secure context, so a self-hosted instance reached
 * over plain HTTP at a LAN address — a normal way to run this app, which the
 * passkey helpers already treat as one — threw "crypto.randomUUID is not a
 * function" before a query could be sent.
 */

// Per page load, so two tabs starting a run in the same millisecond still
// differ. Math.random is ample for that and needs no secure context.
const session = Math.random().toString(36).slice(2, 10)
let counter = 0

export function runId(): string {
  counter += 1
  return `run-${Date.now().toString(36)}-${session}-${counter.toString(36)}`
}
