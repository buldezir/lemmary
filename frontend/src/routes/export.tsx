import { type SubmitEvent, useState } from 'react'
import { fetchDocumentsArchive, type ExportArchiveMode } from '../lib/api/documents'
import { saveBlob } from '../lib/download'
import { Button, labelTextClassName } from '../components/ui'

const modeOptions: { value: ExportArchiveMode; label: string; description: string }[] = [
  {
    value: 'originals',
    label: 'Original files only',
    description: 'Download a zip containing only the original uploaded files.',
  },
  {
    value: 'ocr',
    label: 'Originals with OCR text',
    description:
      'Include .ocr.txt sidecars next to each original when OCR text is available.',
  },
  {
    value: 'metadata',
    label: 'Originals with OCR and metadata',
    description:
      'Include OCR sidecars plus .metadata.json with titles, tags, dates, and other extracted fields.',
  },
]

export function ExportPage() {
  const [mode, setMode] = useState<ExportArchiveMode>('originals')
  const [running, setRunning] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    try {
      setRunning(true)
      setError('')
      setSuccess('')
      saveBlob(await fetchDocumentsArchive(mode), 'lemmary-export.zip')
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
        <h1 className="font-display text-3xl font-semibold tracking-tight text-ink">Export archive</h1>
        <p className="mt-1 text-sm text-ink-soft">
          Download a zip of all your documents. Choose whether to include OCR text and
          metadata sidecars alongside the original files.
        </p>
      </div>

      <form onSubmit={onSubmit} className="space-y-4 rounded-none border border-line bg-surface p-5">
        <fieldset className="space-y-2" disabled={running}>
          <legend className={labelTextClassName}>Export mode</legend>
          {modeOptions.map((option) => (
            <label
              key={option.value}
              className={`flex cursor-pointer items-start gap-2 rounded-xs border px-3 py-2 text-sm ${
                mode === option.value
                  ? 'border-ink bg-bright text-ink'
                  : 'border-line bg-surface text-ink-muted'
              }`}
            >
              <input
                type="radio"
                className="mt-0.5"
                name="export-mode"
                value={option.value}
                checked={mode === option.value}
                onChange={() => setMode(option.value)}
              />
              <span>
                <span className="font-medium">{option.label}</span>
                <span className="mt-0.5 block text-xs font-normal text-ink-soft">
                  {option.description}
                </span>
              </span>
            </label>
          ))}
        </fieldset>
        <Button type="submit" disabled={running}>
          {running ? 'Preparing archive…' : 'Download archive'}
        </Button>
      </form>

      {error && <p className="text-sm text-madder">{error}</p>}
      {success && <p className="text-sm text-forest">{success}</p>}
    </div>
  )
}
