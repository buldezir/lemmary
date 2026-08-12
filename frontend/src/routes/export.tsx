import { type SubmitEvent, useEffect, useState } from 'react'
import {
  downloadDocumentsArchive,
  ensureAuth,
  type ExportArchiveMode,
} from '../lib/pocketbase'

const labelTextClassName = 'text-xs font-medium text-stone-500'

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
  const [loading, setLoading] = useState(true)
  const [running, setRunning] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')

  useEffect(() => {
    let active = true

    async function load() {
      try {
        await ensureAuth()
      } catch (err) {
        if (active) {
          setError(err instanceof Error ? err.message : 'Failed to load')
        }
      } finally {
        if (active) setLoading(false)
      }
    }

    void load()
    return () => {
      active = false
    }
  }, [])

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    try {
      setRunning(true)
      setError('')
      setSuccess('')
      await downloadDocumentsArchive(mode)
      setSuccess('Archive download started.')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Download failed')
    } finally {
      setRunning(false)
    }
  }

  if (loading) {
    return <p className="text-sm text-stone-500">Loading…</p>
  }

  return (
    <div className="mx-auto max-w-xl space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-stone-950">Export archive</h1>
        <p className="mt-1 text-sm text-stone-500">
          Download a zip of all your documents. Choose whether to include OCR text and
          metadata sidecars alongside the original files.
        </p>
      </div>

      <form onSubmit={onSubmit} className="space-y-4 rounded-lg border border-stone-200 bg-stone-50 p-5">
        <fieldset className="space-y-2" disabled={running}>
          <legend className={labelTextClassName}>Export mode</legend>
          {modeOptions.map((option) => (
            <label
              key={option.value}
              className={`flex cursor-pointer items-start gap-2 rounded-md border px-3 py-2 text-sm ${
                mode === option.value
                  ? 'border-stone-400 bg-white text-stone-800'
                  : 'border-stone-200 bg-stone-50 text-stone-700'
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
                <span className="mt-0.5 block text-xs font-normal text-stone-500">
                  {option.description}
                </span>
              </span>
            </label>
          ))}
        </fieldset>
        <button
          type="submit"
          disabled={running}
          className="rounded-md bg-gray-900 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-gray-800 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {running ? 'Preparing archive…' : 'Download archive'}
        </button>
      </form>

      {error && <p className="text-sm text-red-600">{error}</p>}
      {success && <p className="text-sm text-green-700">{success}</p>}
    </div>
  )
}
