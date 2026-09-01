import { type SubmitEvent, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { fetchDocumentsArchive } from '../lib/api/documents'
import { saveBlob } from '../lib/download'
import { Button } from '../components/ui'

export function ExportPage() {
  const [running, setRunning] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    try {
      setRunning(true)
      setError('')
      setSuccess('')
      saveBlob(await fetchDocumentsArchive(), 'lemmary-export.zip')
      setSuccess('Archive download started.')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Download failed')
    } finally {
      setRunning(false)
    }
  }

  return (
    <div className="mx-auto max-w-xl space-y-6">
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight text-ink">
          Export archive
        </h1>
        <p className="mt-1 text-sm text-ink-soft">
          Download a full backup of your library as a single zip. It can be restored into this or
          any other Lemmary instance under{' '}
          <Link to="/import" className="font-medium text-oxblood underline">
            Import &rarr; Lemmary archive
          </Link>
          .
        </p>
      </div>

      <form
        onSubmit={onSubmit}
        className="space-y-4 rounded-none border border-line bg-surface p-5"
      >
        <div className="text-sm text-ink-soft">
          <p className="font-medium text-ink">The archive contains</p>
          <ul className="mt-2 list-inside list-disc space-y-1">
            <li>every original file you uploaded</li>
            <li>
              an <code>.ocr.txt</code> sidecar with the extracted text
            </li>
            <li>
              a <code>.metadata.json</code> sidecar with titles, tags, dates and other extracted
              fields
            </li>
            <li>the generated thumbnails, and all of your tags, correspondents and document types</li>
          </ul>
          <p className="mt-3">
            Settings and API keys are not included — they belong to the instance, not to your
            documents.
          </p>
        </div>
        <Button type="submit" disabled={running}>
          {running ? 'Preparing archive…' : 'Download backup'}
        </Button>
      </form>

      {error && <p className="text-sm text-madder">{error}</p>}
      {success && <p className="text-sm text-forest">{success}</p>}
    </div>
  )
}
