import { useEffect, useId, useState } from 'react'
import {
  listProviderModels,
  type AIProvider,
  type CatalogModel,
  type ProviderSDK,
} from '../lib/pocketbase'

const inputClassName =
  'w-full rounded-md border border-stone-300 bg-stone-50 px-3 py-2 text-sm outline-none focus:border-gray-900 focus:ring-1 focus:ring-gray-900'
const labelClassName = 'flex flex-col gap-1'
const labelTextClassName = 'text-xs font-medium text-stone-500'

export const OCR_MODEL_WARNING =
  'Choose this model wisely — this provider does not advertise which models accept file inputs.'

export function isLLMProvider(sdk: string) {
  return sdk === 'openai' || sdk === 'openrouter'
}

export function sdkLabel(sdk: ProviderSDK | string) {
  switch (sdk) {
    case 'openai':
      return 'OpenAI'
    case 'openrouter':
      return 'OpenRouter'
    case 'google_vision':
      return 'Google Cloud Vision'
    case 'mistral':
      return 'Mistral OCR'
    default:
      return sdk
  }
}

type ProviderModelFieldsProps = {
  label: string
  providers: AIProvider[]
  providerId: string
  model: string
  purpose: 'ocr' | 'llm'
  onProviderChange: (providerId: string) => void
  onModelChange: (model: string) => void
  allowEmpty?: boolean
}

export function ProviderModelFields({
  label,
  providers,
  providerId,
  model,
  purpose,
  onProviderChange,
  onModelChange,
  allowEmpty = false,
}: ProviderModelFieldsProps) {
  const listId = useId()
  const [models, setModels] = useState<CatalogModel[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const selected = providers.find((item) => item.id === providerId)
  const hideModel = purpose === 'ocr' && selected?.sdk === 'google_vision'
  const showWarning = purpose === 'ocr' && selected != null && selected.sdk !== 'openrouter'

  useEffect(() => {
    if (!providerId || hideModel) {
      setModels([])
      setError('')
      setLoading(false)
      return
    }

    let active = true
    async function load() {
      try {
        setLoading(true)
        setError('')
        const next = await listProviderModels(providerId, purpose)
        if (!active) return
        setModels(next.models)
      } catch (err) {
        if (!active) return
        setModels([])
        setError(err instanceof Error ? err.message : 'Failed to load models')
      } finally {
        if (active) setLoading(false)
      }
    }
    void load()
    return () => {
      active = false
    }
  }, [providerId, purpose, hideModel])

  return (
    <div className="grid gap-4 sm:grid-cols-2 sm:col-span-2">
      <label className={labelClassName}>
        <span className={labelTextClassName}>{label} provider</span>
        <select
          className={inputClassName}
          value={providerId}
          onChange={(event) => onProviderChange(event.target.value)}
        >
          {allowEmpty || providers.length === 0 ? <option value="">Select a provider</option> : null}
          {providers.map((item) => (
            <option key={item.id} value={item.id}>
              {item.alias} ({sdkLabel(item.sdk)})
            </option>
          ))}
        </select>
      </label>
      {!hideModel && (
        <label className={labelClassName}>
          <span className={labelTextClassName}>{label} model</span>
          <input
            className={inputClassName}
            list={listId}
            value={model}
            placeholder={loading ? 'Loading models…' : 'Model id'}
            onChange={(event) => onModelChange(event.target.value)}
          />
          <datalist id={listId}>
            {models.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name}
              </option>
            ))}
          </datalist>
        </label>
      )}
      {error && <p className="text-xs text-amber-700 sm:col-span-2">{error}. You can still type a model id.</p>}
      {showWarning && (
        <p className="text-xs text-amber-800 sm:col-span-2">{OCR_MODEL_WARNING}</p>
      )}
    </div>
  )
}
