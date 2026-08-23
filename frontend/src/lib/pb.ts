import PocketBase from 'pocketbase'

// `document` is absent when this module loads outside a browser (unit tests).
const fallbackOrigin =
  typeof document !== 'undefined' ? document.location.origin : 'http://127.0.0.1:8090'

export const pbUrl: string = import.meta.env.VITE_POCKETBASE_URL || fallbackOrigin

export const pb = new PocketBase(pbUrl)
export const pbAdminUrl = `${pbUrl}/_/`
