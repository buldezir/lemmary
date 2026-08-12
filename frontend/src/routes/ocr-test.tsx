import { type DragEvent, type SubmitEvent, useEffect, useState } from 'react'
import {
  ensureAuth,
  listOCRProviders,
  listProviderModels,
  testOCR,
  type CatalogModel,
  type OCRProviderInfo,
} from '../lib/pocketbase'
import { OCR_MODEL_WARNING } from '../components/ProviderModelFields'

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
  const extension = file.name.includes('.') ? file.name.slice(file.name.lastIndexOf('.')).toLowerCase() : ''
  if (ACCEPTED_EXTENSIONS.has(extension)) return true
  if (ACCEPTED_MIME_TYPES.has(file.type)) return true
  return ACCEPTED_MIME_PREFIXES.some((prefix) => file.type.startsWith(prefix))
}

export function OCRTestPage() {
  const [providers, setProviders] = useState<OCRProviderInfo[]>([])
  const [provider, setProvider] = useState('')
  const [model, setModel] = useState('')
  const [models, setModels] = useState<CatalogModel[]>([])
  const [file, setFile] = useState<File | null>(null)
  const [dragging, setDragging] = useState(false)
  const [loadingProviders, setLoadingProviders] = useState(true)
  const [running, setRunning] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState('')
  const [meta, setMeta] = useState('')

  useEffect(() => {
    let active = true

    async function loadProviders() {
      try {
        setLoadingProviders(true)
        await ensureAuth()
        const next = await listOCRProviders()
        if (!active) return
        setProviders(next)
        setProvider((current) => current || next[0]?.id || '')
        setError('')
      } catch (err) {
        if (active) {
          setError(err instanceof Error ? err.message : 'Failed to load OCR providers')
        }
      } finally {
        if (active) {
          setLoadingProviders(false)
        }
      }
    }

    void loadProviders()

    return () => {
      active = false
    }
  }, [])

  const selected = providers.find((item) => item.id === provider)
  const hideModel = selected?.sdk === 'google_vision'
  const showWarning = Boolean(selected && selected.sdk !== 'openrouter')

  useEffect(() => {
    if (!provider || hideModel) {
      setModels([])
      return
    }
    let active = true
    async function loadModels() {
      try {
        const next = await listProviderModels(provider, 'ocr')
        if (!active) return
        setModels(next.models)
        setModel((current) => current || next.models[0]?.id || '')
      } catch {
        if (active) setModels([])
      }
    }
    void loadModels()
    return () => {
      active = false
    }
  }, [provider, hideModel])

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
      setMeta(`${response.char_count.toLocaleString()} characters · ${response.provider} · ${response.duration}`)
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
          <label className="flex flex-col gap-1.5 text-sm font-medium text-stone-700">
            Model
            <input
              list="ocr-test-models"
              value={model}
              onChange={(event) => setModel(event.target.value)}
              placeholder="Model id"
              className="w-full rounded-md border border-stone-300 bg-stone-50 px-3 py-2 text-sm font-normal text-stone-950 outline-none focus:border-gray-900 focus:ring-1 focus:ring-gray-900"
            />
            <datalist id="ocr-test-models">
              {models.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.name}
                </option>
              ))}
            </datalist>
          </label>
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

        {error && <p className="text-sm text-red-600">{error}</p>}

        <button
          type="submit"
          disabled={running || !file || !provider || providers.length === 0}
          className="rounded-md bg-gray-900 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-gray-700 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {running ? 'Running OCR...' : 'Run OCR'}
        </button>
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
