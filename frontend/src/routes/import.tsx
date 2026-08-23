import { type SubmitEvent, useState } from 'react'
import { importFromNgx, type NgxImportMode, type NgxImportResult } from '../lib/api/imports'
import { Button, inputClassName, labelClassName, labelTextClassName } from '../components/ui'

const modeOptions: { value: NgxImportMode; label: string; description: string }[] = [
  {
    value: 'preserve',
    label: 'Keep Paperless-ngx metadata',
    description:
      'Import title, tags, correspondent, document type, date, and OCR text. Preview and duplicate detection still run; AI does not overwrite metadata.',
  },
  {
    value: 'reprocess',
    label: 'Import files only and reprocess',
    description:
      'Import only the original files, then run the full OCR and AI pipeline as if they were newly uploaded.',
  },
]

export function ImportPage() {
  const [url, setUrl] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [mode, setMode] = useState<NgxImportMode>('preserve')
  const [running, setRunning] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<NgxImportResult | null>(null)

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!url.trim() || !apiKey.trim()) {
      setError('URL and API key are required')
      return
    }

    try {
      setRunning(true)
      setError('')
      setResult(null)
      const summary = await importFromNgx(url.trim(), apiKey.trim(), mode)
      setResult(summary)
      setApiKey('')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Import failed')
    } finally {
      setRunning(false)
    }
  }

  return (
    <div className="mx-auto max-w-xl space-y-6">
      <div>
        <h1 className="text-2xl font-semibold text-stone-950">Import from Paperless-ngx</h1>
        <p className="mt-1 text-sm text-stone-500">
          Pull documents from an existing Paperless-ngx instance using its URL and API token. The
          remote token belongs to a specific ngx user, so imported documents are added to your
          account. Choose whether to keep remote metadata or reprocess files through OCR and AI. The
          API key is not stored.
        </p>
      </div>

      <form onSubmit={onSubmit} className="space-y-4 rounded-lg border border-stone-200 bg-stone-50 p-5">
        <label className={labelClassName}>
          <span className={labelTextClassName}>Paperless-ngx URL</span>
          <input
            className={inputClassName}
            type="url"
            required
            placeholder="https://paperless.example.com"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            autoComplete="off"
          />
        </label>
        <label className={labelClassName}>
          <span className={labelTextClassName}>API key</span>
          <input
            className={inputClassName}
            type="password"
            required
            placeholder="Token from Paperless-ngx profile"
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            autoComplete="off"
          />
        </label>
        <fieldset className="space-y-2" disabled={running}>
          <legend className={labelTextClassName}>Import mode</legend>
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
                name="import-mode"
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
        <Button type="submit" disabled={running}>
          {running ? 'Importing…' : 'Start import'}
        </Button>
      </form>

      {error && <p className="text-sm text-red-600">{error}</p>}

      {result && (
        <div className="space-y-2 rounded-lg border border-stone-200 bg-white p-5 text-sm text-stone-700">
          <p className="font-medium text-stone-950">Import finished</p>
          <ul className="list-inside list-disc space-y-1">
            <li>Imported: {result.imported}</li>
            <li>Skipped duplicates: {result.skipped_duplicates}</li>
            <li>Failed: {result.failed}</li>
            <li>Tags upserted: {result.tags_upserted}</li>
            <li>Correspondents upserted: {result.correspondents_upserted}</li>
            <li>Document types upserted: {result.document_types_upserted}</li>
          </ul>
          {result.errors.length > 0 && (
            <div className="mt-3">
              <p className="font-medium text-stone-950">Errors</p>
              <ul className="mt-1 list-inside list-disc space-y-1 text-red-700">
                {result.errors.map((msg) => (
                  <li key={msg}>{msg}</li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
