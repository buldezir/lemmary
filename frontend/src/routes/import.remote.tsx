import { type SubmitEvent, useState } from 'react'
import {
  importFromRemote,
  type RemoteImportMode,
  type RemoteImportResult,
  type RemoteImportSource,
} from '../lib/api/imports'
import { Button, inputClassName, labelClassName, labelTextClassName } from '../components/ui'
import { t } from '../i18n'

const sources = {
  ngx: {
    title: t('importNgx.title'),
    intro: t('importNgx.intro'),
    url: t('importNgx.url'),
    urlPlaceholder: 'https://paperless.example.com',
    apiKeyPlaceholder: t('importNgx.apiKeyPlaceholder'),
    preserveLabel: t('importNgx.preserveLabel'),
    preserveDescription: t('importNgx.preserveDescription'),
  },
  papra: {
    title: t('importPapra.title'),
    intro: t('importPapra.intro'),
    url: t('importPapra.url'),
    urlPlaceholder: 'https://papra.example.com',
    apiKeyPlaceholder: t('importPapra.apiKeyPlaceholder'),
    preserveLabel: t('importPapra.preserveLabel'),
    preserveDescription: t('importPapra.preserveDescription'),
  },
}

export function ImportNgxPage() {
  return <RemoteImport source="ngx" />
}

export function ImportPapraPage() {
  return <RemoteImport source="papra" />
}

function RemoteImport({ source }: { source: RemoteImportSource }) {
  const text = sources[source]
  const modeOptions: { value: RemoteImportMode; label: string; description: string }[] = [
    { value: 'preserve', label: text.preserveLabel, description: text.preserveDescription },
    {
      value: 'reprocess',
      label: t('importNgx.reprocessLabel'),
      description: t('importNgx.reprocessDescription'),
    },
  ]
  const [url, setUrl] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [mode, setMode] = useState<RemoteImportMode>('preserve')
  const [running, setRunning] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<RemoteImportResult | null>(null)

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!url.trim() || !apiKey.trim()) {
      setError(t('importNgx.required'))
      return
    }

    try {
      setRunning(true)
      setError('')
      setResult(null)
      const summary = await importFromRemote(source, url.trim(), apiKey.trim(), mode)
      setResult(summary)
      setApiKey('')
    } catch (err) {
      setError(err instanceof Error ? err.message : t('importNgx.failed'))
    } finally {
      setRunning(false)
    }
  }

  return (
    <section className="space-y-5">
      <div>
        <h2 className="font-display text-xl font-semibold text-ink">{text.title}</h2>
        <p className="mt-1 text-sm text-ink-soft">
          {text.intro}
        </p>
      </div>

      <form onSubmit={onSubmit} className="space-y-4 rounded-none border border-line bg-surface p-5">
        <label className={labelClassName}>
          <span className={labelTextClassName}>{text.url}</span>
          <input
            className={inputClassName}
            type="url"
            required
            placeholder={text.urlPlaceholder}
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            autoComplete="off"
          />
        </label>
        <label className={labelClassName}>
          <span className={labelTextClassName}>{t('importNgx.apiKey')}</span>
          <input
            className={inputClassName}
            type="password"
            required
            placeholder={text.apiKeyPlaceholder}
            value={apiKey}
            onChange={(e) => setApiKey(e.target.value)}
            autoComplete="off"
          />
        </label>
        <fieldset className="space-y-2" disabled={running}>
          <legend className={labelTextClassName}>{t('importNgx.mode')}</legend>
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
                name="import-mode"
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
          {running ? t('importNgx.importing') : t('importNgx.start')}
        </Button>
      </form>

      {error && <p className="text-sm text-madder">{error}</p>}

      {result && (
        <div className="space-y-2 rounded-none border border-line bg-bright p-5 text-sm text-ink-muted">
          <p className="font-medium text-ink">{t('importNgx.finished')}</p>
          <ul className="list-inside list-disc space-y-1">
            <li>{t('importNgx.imported', { count: result.imported })}</li>
            <li>{t('importNgx.skippedDuplicates', { count: result.skipped_duplicates })}</li>
            <li>{t('importNgx.failedCount', { count: result.failed })}</li>
            <li>{t('importNgx.tagsUpserted', { count: result.tags_upserted })}</li>
            {source === 'ngx' && (
              <>
                <li>{t('importNgx.correspondentsUpserted', { count: result.correspondents_upserted })}</li>
                <li>{t('importNgx.typesUpserted', { count: result.document_types_upserted })}</li>
              </>
            )}
          </ul>
          {result.errors.length > 0 && (
            <div className="mt-3">
              <p className="font-medium text-ink">{t('importNgx.errors')}</p>
              <ul className="mt-1 list-inside list-disc space-y-1 text-madder">
                {result.errors.map((msg) => (
                  <li key={msg}>{msg}</li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </section>
  )
}
