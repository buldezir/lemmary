/**
 * An id for one Deep Search run, minted by the client: cancelling POSTs this id
 * to /search/cancel, and waiting for a server-minted one would leave Cancel
 * dead for the seconds a run takes to fan out.
 *
 * Not a credential, so no crypto: the server treats it as an opaque key and
 * scopes the lookup to the owner. `crypto.randomUUID` exists only in a secure
 * context, so it threw on a self-hosted instance reached over plain HTTP.
 */

// Per page load, so two tabs starting a run in the same millisecond still
// differ. Math.random is ample for that and needs no secure context.
const session = Math.random().toString(36).slice(2, 10)
let counter = 0

export function runId(): string {
  counter += 1
  return `run-${Date.now().toString(36)}-${session}-${counter.toString(36)}`
}
