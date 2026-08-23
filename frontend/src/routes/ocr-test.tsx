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
    <section className="mx-auto flex max-w-3xl flex-col gap-6">
      <div>
        <h2 className="text-xl font-semibold text-stone-950">OCR test</h2>
        <p className="text-sm text-stone-500">
          Upload a file and run OCR with a configured provider. Results are not saved.
        </p>
      </div>

      <form className="flex flex-col gap-4" onSubmit={onSubmit}>
        <label className="flex flex-col gap-1.5 text-sm font-medium text-stone-700">
          Provider
          <select
            value={provider}
            onChange={(event) => {
              setProvider(event.target.value)
              setModel('')
            }}
            disabled={loadingProviders || providers.length === 0 || running}
            className="w-full rounded-md border border-stone-300 bg-stone-50 px-3 py-2 text-sm font-normal text-stone-950 outline-none focus:border-gray-900 focus:ring-1 focus:ring-gray-900 disabled:cursor-not-allowed disabled:opacity-50"
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
          className={`flex min-h-44 cursor-pointer flex-col items-center justify-center gap-1 rounded-lg border border-dashed p-6 text-center transition-colors ${
            dragging
              ? 'border-gray-900 bg-white'
              : 'border-stone-300 bg-stone-50 hover:border-stone-400 hover:bg-white'
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
          <span className="text-sm font-medium text-stone-950">
            {file ? file.name : 'Choose a file'}
          </span>
          {!file && <span className="text-xs text-stone-400">or drop it here (max 10 MB)</span>}
        </label>

        {(error || providersState.error) && (
          <p className="text-sm text-red-600">{error || providersState.error}</p>
        )}

        <Button type="submit" disabled={running || !file || !provider || providers.length === 0}>
          {running ? 'Running OCR...' : 'Run OCR'}
        </Button>
      </form>

      {(result || running) && (
        <div className="flex flex-col gap-2">
          <div className="flex items-center justify-between gap-4">
            <h3 className="text-sm font-medium text-stone-700">Result</h3>
            {meta && <p className="text-xs text-stone-500">{meta}</p>}
          </div>
          <textarea
            readOnly
            rows={20}
            value={running ? 'Running OCR...' : result}
            className="min-h-96 w-full resize-y rounded-md border border-stone-300 bg-stone-50 px-3 py-2 font-mono text-xs leading-relaxed text-stone-950 outline-none"
          />
        </div>
      )}
    </section>
  )
}
