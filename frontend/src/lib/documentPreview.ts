/**
 * The document record carries no mime type, only the filename, and PocketBase
 * appends a content-sniffed extension to an upload that arrives without one.
 *
 * `none` covers what the browser will not render (docx, xlsx) and what is
 * already on the page as OCR text (txt, csv); those get a download link.
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
