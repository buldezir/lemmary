/** How long a saved blob's URL outlives the click that started the download. */
export const REVOKE_AFTER_MS = 60_000

export function saveBlob(blob: Blob, filename: string) {
  const objectUrl = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = objectUrl
  anchor.download = filename
  anchor.rel = 'noopener'
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  // Arc reads the blob after click() returns; a URL revoked at once leaves the
  // download stuck at 100%.
  setTimeout(() => URL.revokeObjectURL(objectUrl), REVOKE_AFTER_MS)
}
