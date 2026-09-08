/**
 * Which viewer, if any, can show a stored document file.
 *
 * The document record carries no mime type, only the stored filename -- and
 * PocketBase appends a content-sniffed extension when an upload arrives
 * without one, so the extension is a fair proxy for the bytes.
 *
 * `none` covers the two families the browser will not render (docx, xlsx) and
 * the two whose text is already on the page in the OCR-text field (txt, csv).
 * Those get a download link rather than a frame.
 */
export type PreviewKind = 'pdf' | 'image' | 'none'

const IMAGE_EXTENSIONS = ['jpg', 'jpeg', 'png', 'webp']

export function previewKind(filename: string): PreviewKind {
  const dot = filename.lastIndexOf('.')
  if (dot < 0) {
    return 'none'
  }

  const extension = filename.slice(dot + 1).toLowerCase()
  if (extension === 'pdf') {
    return 'pdf'
  }
  if (IMAGE_EXTENSIONS.includes(extension)) {
    return 'image'
  }
  return 'none'
}
