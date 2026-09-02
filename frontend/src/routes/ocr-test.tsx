import { type DragEvent, type SubmitEvent, useState } from 'react'
import { ensureAuth } from '../lib/auth'
import {
  listOCRProviders,
  listProviderModels,
  showsOCRModelWarning,
  testOCR,
  OCR_MODEL_WARNING,
  type CatalogModel,
} from '../lib/api/providers'
import { useAsync } from '../hooks/useAsync'
import { ModelSelect } from '../components/ProviderModelFields'
import { Button } from '../components/ui'

const ACCEPTED_EXTENSIONS = new Set([
  '.pdf',
  '.jpg',
  '.jpeg',
  '.png',
  '.webp',
  '.avif',
  '.tif',
  '.tiff',
  '.gif',
  '.docx',
  '.pptx',
])
const ACCEPTED_MIME_PREFIXES = ['image/']
const ACCEPTED_MIME_TYPES = new Set([
  'application/pdf',
  'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  'application/vnd.openxmlformats-officedocument.presentationml.presentation',
])

function isAcceptedFile(file: File) {
  const extension = file.name.includes('.')
    ? file.name.slice(file.name.lastIndexOf('.')).toLowerCase()
    : ''
  if (ACCEPTED_EXTENSIONS.has(extension)) return true
  if (ACCEPTED_MIME_TYPES.has(file.type)) return true
  return ACCEPTED_MIME_PREFIXES.some((prefix) => file.type.startsWith(prefix))
}

export function OCRTestPage() {
  const [provider, setProvider] = useState('')
  const [model, setModel] = useState('')
  const [file, setFile] = useState<File | null>(null)
  const [dragging, setDragging] = useState(false)
  const [running, setRunning] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState('')
  const [meta, setMeta] = useState('')

  const providersState = useAsync(async () => {
    await ensureAuth()
    const next = await listOCRProviders()
    // Preselect the first provider once the list arrives.
    setProvider((current) => current || next[0]?.id || '')
    return next
  }, [])
  const providers = providersState.data ?? []
  const loadingProviders = providersState.loading

  const selected = providers.find((item) => item.id === provider)
  const hideModel = selected?.sdk === 'google_vision'
  const showWarning = showsOCRModelWarning(selected?.sdk)

  const modelsState = useAsync(async () => {
    if (!provider || hideModel) {
      return [] as CatalogModel[]
    }
    const next = await listProviderModels(provider, 'ocr')
    // Preselect the first catalog model once the list arrives.
    setModel((current) => current || next.models[0]?.id || '')
    return next.models
  }, [provider, hideModel])
  const models = modelsState.data ?? []
  const loadingModels = modelsState.loading

  function selectFile(next: File | null) {
    if (!next) {
      setFile(null)
      return
    }
    if (!isAcceptedFile(next)) {
      setError('Unsupported file type. Use PDF, common image formats, DOCX, or PPTX.')
      return
    }
    setError('')
    setFile(next)
  }

  function onDragOver(event: DragEvent<HTMLLabelElement>) {
    event.preventDefault()
  }

  function onDragEnter(event: DragEvent<HTMLLabelElement>) {
    event.preventDefault()
    setDragging(true)
  }

  function onDragLeave(event: DragEvent<HTMLLabelElement>) {
    event.preventDefault()
    if (event.currentTarget.contains(event.relatedTarget as Node)) return
    setDragging(false)
  }

  function onDrop(event: DragEvent<HTMLLabelElement>) {
    event.preventDefault()
    setDragging(false)
    selectFile(event.dataTransfer.files?.[0] ?? null)
  }

  async function onSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!file) {
      setError('Choose a file to test.')
      return
    }
    if (!provider) {
      setError('Choose an OCR provider.')
      return
    }

    try {
      setRunning(true)
      setError('')
      setResult('')
      setMeta('')

      const response = await testOCR(file, provider, hideModel ? undefined : model)
      setResult(response.text)
      setMeta(
        `${response.char_count.toLocaleString()} characters · ${response.provider} · ${response.duration}`,
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : 'OCR test failed')
    } finally {
      setRunning(false)
    }
  }

  return (
    <section className="mx-auto flex max-w-3xl flex-col gap-5">
      <div>
        <h2 className="font-display text-2xl font-semibold tracking-tight text-ink">OCR test</h2>
        <p className="text-sm text-ink-soft">
          Upload a file and run OCR with a configured provider. Results are not saved.
        </p>
      </div>

      <form className="flex flex-col gap-4" onSubmit={onSubmit}>
        <label className="flex flex-col gap-1.5 text-sm font-medium text-ink-muted">
          Provider
          <select
            value={provider}
            onChange={(event) => {
              setProvider(event.target.value)
              setModel('')
            }}
            disabled={loadingProviders || providers.length === 0 || running}
            className="w-full rounded-xs border border-line-strong bg-surface px-3 py-2 text-sm font-normal text-ink outline-none focus:border-oxblood focus:ring-1 focus:ring-oxblood disabled:cursor-not-allowed disabled:opacity-50"
          >
            {loadingProviders ? (
              <option value="">Loading providers...</option>
            ) : providers.length === 0 ? (
              <option value="">No providers configured</option>
            ) : (
              providers.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name}
                </option>
              ))
            )}
          </select>
        </label>

        {!hideModel && provider && (
          <ModelSelect
            key={provider}
            label="OCR"
            model={model}
            models={models}
            loading={loadingModels}
            disabled={running}
            onChange={setModel}
          />
        )}
        {showWarning && <p className="text-xs text-amber-800">{OCR_MODEL_WARNING}</p>}

        <label
          className={`flex min-h-44 cursor-pointer flex-col items-center justify-center gap-1 rounded-none border border-dashed p-6 text-center transition-colors ${
            dragging
              ? 'border-ink bg-bright'
              : 'border-line-strong bg-surface hover:border-ink/50 hover:bg-bright'
          }`}
          onDragOver={onDragOver}
          onDragEnter={onDragEnter}
          onDragLeave={onDragLeave}
          onDrop={onDrop}
        >
          <input
            type="file"
            accept=".pdf,.jpg,.jpeg,.png,.webp,.avif,.tif,.tiff,.gif,.docx,.pptx,application/pdf,image/*"
            onChange={(event) => selectFile(event.target.files?.[0] ?? null)}
            className="hidden"
            disabled={running}
          />
          <span className="text-sm font-medium text-ink">
            {file ? file.name : 'Choose a file'}
          </span>
          {!file && <span className="text-xs text-ink-faint">or drop it here (max 10 MB)</span>}
        </label>

        {(error || providersState.error) && (
          <p className="text-sm text-madder">{error || providersState.error}</p>
        )}

        <Button type="submit" disabled={running || !file || !provider || providers.length === 0}>
          {running ? 'Running OCR...' : 'Run OCR'}
        </Button>
      </form>

      {(result || running) && (
        <div className="flex flex-col gap-2">
          <div className="flex items-center justify-between gap-4">
            <h3 className="text-sm font-medium text-ink-muted">Result</h3>
            {meta && <p className="text-xs text-ink-soft">{meta}</p>}
          </div>
          <textarea
            readOnly
            rows={20}
            value={running ? 'Running OCR...' : result}
            className="min-h-96 w-full resize-y rounded-xs border border-line-strong bg-surface px-3 py-2 font-mono text-xs leading-relaxed text-ink outline-none"
          />
        </div>
      )}
    </section>
  )
}
