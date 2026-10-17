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

/**
 * The thumb size migration 1730000060 declares on the documents file field.
 * PocketBase scales only to a declared size; any other serves the original.
 */
export const DOCUMENT_THUMB = '400x0'

/**
 * What a document card can show as a picture: a PDF's rendered first page, or
 * an image itself, scaled. Nothing for the rest.
 */
export function cardImage(document: { file: string; preview?: string }): { name: string; thumb?: string } | null {
  if (document.preview) {
    return { name: document.preview }
  }
  if (previewKind(document.file) === 'image') {
    return { name: document.file, thumb: DOCUMENT_THUMB }
  }
  return null
}
