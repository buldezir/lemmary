import { type SubmitEvent, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { fetchDocumentsArchive } from '../lib/api/documents'
import { saveBlob } from '../lib/download'
import { Button } from '../components/ui'
import { t, tNode } from '../i18n'

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
      setSuccess(t('export.started'))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('export.error'))
    } finally {
      setRunning(false)
    }
  }

  return (
    <div className="mx-auto max-w-xl space-y-5">
      <div>
        <h1 className="font-display text-3xl font-semibold tracking-tight text-ink">
          {t('export.title')}
        </h1>
        <p className="mt-1 text-sm text-ink-soft">
          {tNode('export.intro', {
            link: (
              <Link to="/import" className="font-medium text-oxblood underline">
                {t('export.introLink')}
              </Link>
            ),
          })}
        </p>
      </div>

      <form
        onSubmit={onSubmit}
        className="space-y-4 rounded-none border border-line bg-surface p-5"
      >
        <div className="text-sm text-ink-soft">
          <p className="font-medium text-ink">{t('export.contains')}</p>
          <ul className="mt-2 list-inside list-disc space-y-1">
            <li>{t('export.originals')}</li>
            <li>{tNode('export.ocrSidecar', { code: <code>.ocr.txt</code> })}</li>
            <li>{tNode('export.metadataSidecar', { code: <code>.metadata.json</code> })}</li>
            <li>{t('export.thumbnails')}</li>
          </ul>
          <p className="mt-3">{t('export.notIncluded')}</p>
        </div>
        <Button type="submit" disabled={running}>
          {running ? t('export.preparing') : t('export.download')}
        </Button>
      </form>

      {error && <p className="text-sm text-madder">{error}</p>}
      {success && <p className="text-sm text-forest">{success}</p>}
    </div>
  )
}
