import { useEffect, useState } from 'react'
import { ClientResponseError } from 'pocketbase'
import { fileUrlWithToken, type DocumentFileRef } from '../lib/api/documents'
import { previewKind } from '../lib/documentPreview'

/**
 * The document file itself, viewed by the browser. PocketBase stamps every file
 * response with `...; sandbox` in a CSP, which sounds like it would stop a
 * framed PDF viewer; it does not, in either Chrome or Firefox, so the tokened
 * URL goes straight into the frame and the file is never pulled through JS.
 */
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
    void fileUrlWithToken({ id, collectionId, file })
      .then((url) => {
        if (active) {
          setResolved({ key: `${collectionId}/${id}/${file}`, url })
          setError('')
        }
      })
      .catch((err: unknown) => {
        // A realtime-driven remount can race a mint; the survivor has the token.
        if (err instanceof ClientResponseError && err.isAbort) {
          return
        }
        if (active) {
          setError(err instanceof Error ? err.message : 'Failed to load the file')
        }
      })

    return () => {
      active = false
    }
  }, [id, collectionId, file])

  const url = resolved?.key === key ? resolved.url : null

  if (error || url === null || kind === 'none') {
    return (
      <div className="flex h-full items-center justify-center border border-line bg-surface p-6">
        <p className={`text-center text-sm ${error ? 'text-madder' : 'text-ink-soft'}`}>
          {error || (kind === 'none' ? 'This file type has no preview.' : 'Loading preview...')}
        </p>
      </div>
    )
  }

  if (kind === 'image') {
    return (
      <img
        src={url}
        alt="Document preview"
        onError={() => setError('The image could not be loaded.')}
        className="h-full w-full border border-line bg-surface object-contain"
      />
    )
  }

  // #view=FitH is the viewer's own initial-zoom hint. Set once: changing the
  // fragment re-navigates the frame.
  //
  // The file token is user-scoped and rides in the query string, so
  // referrerPolicy keeps it out of the Referer of anything the framed document
  // links to.
  //
  // No onError: a refused frame gets an error *document*, which loads
  // successfully as far as the element is concerned.
  //
  // ponytail: the token lives three minutes, so a viewer that went back for more
  // bytes long after the frame loaded would be refused. In practice it has the
  // whole file by then. Fetch it into a blob URL instead if that ever bites.
  return (
    <iframe
      title="Document preview"
      src={`${url}#view=FitH`}
      referrerPolicy="no-referrer"
      className="h-full w-full border border-line bg-wash"
    />
  )
}
