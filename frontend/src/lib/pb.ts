import PocketBase from 'pocketbase'

// `document` is absent when this module loads outside a browser (unit tests).
const fallbackOrigin =
  typeof document !== 'undefined' ? document.location.origin : 'http://127.0.0.1:8090'

export const pbUrl: string = import.meta.env.VITE_POCKETBASE_URL || fallbackOrigin

export const pb = new PocketBase(pbUrl)
export const pbAdminUrl = `${pbUrl}/_/`

// A build may serve 423 Locked on every API route while it waits for something
// only a person can supply — the core build never does, so this is inert here.
// A tab that was already open when the server restarted into that state would
// otherwise sit there failing every request; reloading lands it on whatever the
// locked server serves instead.
//
// Guarded by a flag because many requests can fail at once, and each would
// otherwise trigger its own reload.
let reloadingForLock = false

pb.afterSend = (response, data) => {
  if (response.status === 423 && !reloadingForLock && typeof window !== 'undefined') {
    reloadingForLock = true
    window.location.reload()
  }
  return data
}
