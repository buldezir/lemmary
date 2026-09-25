export function saveBlob(blob: Blob, filename: string) {
  const objectUrl = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = objectUrl
  anchor.download = filename
  anchor.rel = 'noopener'
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  // The download reads the blob after click() returns: revoked in the same
  // tick, Chromium (Arc on macOS) stalls it at 100% and never finishes.
  setTimeout(() => URL.revokeObjectURL(objectUrl), 60_000)
}
