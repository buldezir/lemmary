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

  // Keyed by the file they belong to, so a URL or a failure that arrives after
  // the page moved on is never rendered.
  const [resolved, setResolved] = useState<{ key: string; url: string } | null>(null)
  const [failure, setFailure] = useState<{ key: string; message: string } | null>(null)
  const key = `${collectionId}/${id}/${file}`

  useEffect(() => {
    const fileKey = `${collectionId}/${id}/${file}`
    const ref = { id, collectionId, file }
    const controller = new AbortController()
    let blobUrl = ''
    void (kind === 'pdf' ? fetchPdfBlobUrl(ref, controller.signal) : fileUrlWithToken(ref))
      .then((url) => {
        if (controller.signal.aborted) {
          if (kind === 'pdf') {
            URL.revokeObjectURL(url)
          }
          return
        }
        if (kind === 'pdf') {
          blobUrl = url
        }
        setResolved({ key: fileKey, url })
      })
      .catch((err: unknown) => {
        // A realtime-driven remount can race a mint; the survivor has the token.
        if (controller.signal.aborted || (err instanceof ClientResponseError && err.isAbort)) {
          return
        }
        setFailure({
          key: fileKey,
          message: err instanceof Error ? err.message : t('documentPreview.loadFailed'),
        })
      })

    return () => {
      controller.abort()
      if (blobUrl) {
        URL.revokeObjectURL(blobUrl)
      }
    }
  }, [id, collectionId, file, kind])

  const url = resolved?.key === key ? resolved.url : null
  const error = failure?.key === key ? failure.message : ''

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
        onError={() => setFailure({ key, message: t('documentPreview.imageFailed') })}
        className="h-full w-full border border-line bg-surface object-contain"
      />
    )
  }

  // No #view=FitH zoom hint: Safari can miss a blob URL that carries a
  // fragment, which blanks the frame.
  return (
    <iframe
      title={t('documentPreview.label')}
      src={url}
      className="h-full w-full border border-line bg-wash"
    />
  )
}
