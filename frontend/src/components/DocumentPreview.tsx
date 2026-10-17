import { useEffect, useState } from 'react'
import { ClientResponseError } from 'pocketbase'
import { t } from '../i18n'
import { fetchPdfBlobUrl, fileUrlWithToken, type DocumentFileRef } from '../lib/api/documents'
import { previewKind } from '../lib/documentPreview'

/** The document file itself, viewed by the browser. */
export function DocumentPreview({ record }: { record: DocumentFileRef }) {
  // Primitives, not the record: the detail page hands down a fresh object every
  // second while the pipeline runs, and depending on it would re-mint a token
  // and reload the frame just as often.
  const { id, collectionId, file } = record
  const kind = previewKind(file)

  // Keyed by the file it was minted for, so a token that arrives after the page
  // moved on is never rendered.
  const [resolved, setResolved] = useState<{ key: string; url: string } | null>(null)
  const [error, setError] = useState('')
  const key = `${collectionId}/${id}/${file}`

  useEffect(() => {
    let active = true
    let blobUrl = ''
    const ref = { id, collectionId, file }
    void (kind === 'pdf' ? fetchPdfBlobUrl(ref) : fileUrlWithToken(ref))
      .then((url) => {
        if (kind === 'pdf') {
          blobUrl = url
        }
        if (active) {
          setResolved({ key: `${collectionId}/${id}/${file}`, url })
          setError('')
        } else if (blobUrl) {
          URL.revokeObjectURL(blobUrl)
        }
      })
      .catch((err: unknown) => {
        // A realtime-driven remount can race a mint; the survivor has the token.
        if (err instanceof ClientResponseError && err.isAbort) {
          return
        }
        if (active) {
          setError(err instanceof Error ? err.message : t('documentPreview.loadFailed'))
        }
      })

    return () => {
      active = false
      if (blobUrl) {
        URL.revokeObjectURL(blobUrl)
      }
    }
  }, [id, collectionId, file, kind])

  const url = resolved?.key === key ? resolved.url : null

  if (error || url === null || kind === 'none') {
    return (
      <div className="flex h-full items-center justify-center border border-line bg-surface p-6">
        <p className={`text-center text-sm ${error ? 'text-madder' : 'text-ink-soft'}`}>
          {error ||
            (kind === 'none' ? t('documentPreview.noPreview') : t('documentPreview.loading'))}
        </p>
      </div>
    )
  }

  if (kind === 'image') {
    return (
      <img
        src={url}
        alt={t('documentPreview.label')}
        onError={() => setError(t('documentPreview.imageFailed'))}
        className="h-full w-full border border-line bg-surface object-contain"
      />
    )
  }

  // #view=FitH is the viewer's own initial-zoom hint. Set once: changing the
  // fragment re-navigates the frame.
  return (
    <iframe
      title={t('documentPreview.label')}
      src={`${url}#view=FitH`}
      className="h-full w-full border border-line bg-wash"
    />
  )
}
